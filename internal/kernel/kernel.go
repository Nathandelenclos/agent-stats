// Package kernel is the shared kernel of agent-stats: the few concepts every context speaks of
// in the same sense (harnesses, token counts, the cost of a request). It does no I/O.
package kernel

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Harness is a coding agent whose session logs are read.
type Harness string

const (
	Claude Harness = "claude"
	Codex  Harness = "codex"
	OMP    Harness = "omp"
	Pi     Harness = "pi"
)

// Harnesses lists every supported harness.
var Harnesses = []Harness{Claude, Codex, OMP, Pi}

// ErrUnknownHarness reports a harness name outside Harnesses.
var ErrUnknownHarness = errors.New("unknown harness")

// ParseHarness accepts one of the supported harness names.
func ParseHarness(name string) (Harness, error) {
	for _, h := range Harnesses {
		if string(h) == name {
			return h, nil
		}
	}
	return "", fmt.Errorf("%w %q: use claude, codex, omp or pi", ErrUnknownHarness, name)
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
	return Tokens{c}, nil
}

// Counts returns a copy of the counts.
func (t Tokens) Counts() TokenCounts { return t.counts }

// Prompt is every token of the prompt: uncached, read from cache and written to cache.
func (t Tokens) Prompt() int64 { return t.counts.Input + t.counts.CacheRead + t.counts.CacheWrite }

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
