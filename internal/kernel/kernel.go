// Package kernel is the shared kernel of agent-stats: the few concepts every context speaks of
// in the same sense (harnesses, token counts, the cost of a request). It does no I/O.
package kernel

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Harness is a coding agent whose session logs are read. The zero Harness means "no harness";
// the others are the variables below or the result of ParseHarness.
type Harness struct {
	name string
}

var (
	Claude = Harness{"claude"}
	Codex  = Harness{"codex"}
	OMP    = Harness{"omp"}
	Pi     = Harness{"pi"}
	// OpenCode and Kilo Code share one storage format, a SQLite database of sessions.
	OpenCode = Harness{"opencode"}
	Kilo     = Harness{"kilo"}
	Gemini   = Harness{"gemini"}
	Goose    = Harness{"goose"}
)

// String returns the harness name.
func (h Harness) String() string { return h.name }

// IsZero reports the zero Harness, which stands for no harness in particular.
func (h Harness) IsZero() bool { return h.name == "" }

// Harnesses lists every supported harness.
var Harnesses = []Harness{Claude, Codex, OMP, Pi, OpenCode, Kilo, Gemini, Goose}

// ErrUnknownHarness reports a harness name outside Harnesses.
var ErrUnknownHarness = errors.New("unknown harness")

// ParseHarness accepts one of the supported harness names.
func ParseHarness(name string) (Harness, error) {
	for _, h := range Harnesses {
		if h.name == name {
			return h, nil
		}
	}
	return Harness{}, fmt.Errorf("%w %q: use claude, codex, omp, pi, opencode, kilo, gemini or goose", ErrUnknownHarness, name)
}

// MaxTokenCount bounds one count of one request: far above any model's window (largest seen on real
// logs: under a million), and low enough that the index's sums cannot overflow.
const MaxTokenCount = 1 << 31

// TokenCounts are the raw counts of one request, as a log states them.
type TokenCounts struct {
	Input        int64 // uncached input tokens
	Output       int64 // includes reasoning tokens
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64 // part of CacheWrite written with Anthropic's 1-hour TTL
	Reasoning    int64 // part of Output
}

// Tokens are the validated token counts of one request. Build them with NewTokens.
type Tokens struct {
	counts TokenCounts
}

// ErrInvalidTokens reports a count that is negative, over MaxTokenCount, or inconsistent.
var ErrInvalidTokens = errors.New("invalid token counts")

// NewTokens validates c: every count within 0..MaxTokenCount, and the 1-hour cache writes part of the cache writes.
func NewTokens(c TokenCounts) (Tokens, error) {
	for _, n := range []int64{c.Input, c.Output, c.CacheRead, c.CacheWrite, c.CacheWrite1h, c.Reasoning} {
		if n < 0 || n > MaxTokenCount {
			return Tokens{}, fmt.Errorf("%w: %+v", ErrInvalidTokens, c)
		}
	}
	if c.CacheWrite1h > c.CacheWrite {
		return Tokens{}, fmt.Errorf("%w: 1-hour cache writes over cache writes", ErrInvalidTokens)
	}
	if c.Reasoning > c.Output {
		return Tokens{}, fmt.Errorf("%w: reasoning over output", ErrInvalidTokens)
	}
	return Tokens{c}, nil
}

// CacheWrite5m counts the cache writes with the default 5-minute TTL, billed at the cache-write rate.
func (t Tokens) CacheWrite5m() int64 { return t.counts.CacheWrite - t.counts.CacheWrite1h }

// Counts returns a copy of the counts.
func (t Tokens) Counts() TokenCounts { return t.counts }

// MaxRequestCost bounds the cost of one request (largest seen on real logs: about 10 USD).
const MaxRequestCost = 1e6

// Cost is the cost of one model request in US dollars. Build it with NewCost.
type Cost struct {
	usd float64
}

// ErrInvalidCost reports a cost that is negative, not finite, or over MaxRequestCost.
var ErrInvalidCost = errors.New("invalid request cost")

// NewCost validates an amount in US dollars.
func NewCost(usd float64) (Cost, error) {
	if usd < 0 || usd > MaxRequestCost || math.IsNaN(usd) {
		return Cost{}, fmt.Errorf("%w: %v USD", ErrInvalidCost, usd)
	}
	return Cost{usd}, nil
}

// USD returns the amount in US dollars.
func (c Cost) USD() float64 { return c.usd }

// IsZero reports a cost of nothing, which is also what a harness that logs no cost states.
func (c Cost) IsZero() bool { return c.usd == 0 }

// UnixMilli is t in milliseconds since the Unix epoch, 0 for an unknown (zero) time.
func UnixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
