// Package usage is the domain of agent-stats: sessions with coding agents, the model requests,
// tool calls and prompts they contain, and the rules that price and analyse them.
// It does no I/O: log formats, files and the index live in adapters.
package usage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// SessionID returns the index-wide identity of a session: "<harness>:<native id>".
func (h Harness) SessionID(native string) string { return string(h) + ":" + native }

// SubagentSessionID returns the identity of a subagent session: "<parent session id>/<subagent name>".
func SubagentSessionID(parentID, name string) string { return parentID + "/" + name }

// UnixMilli is t in milliseconds since the Unix epoch, 0 for an unknown (zero) time.
func UnixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// AgentType tells a main conversation from a subagent; harnesses that name their subagent types use that name.
type AgentType string

const (
	MainAgent AgentType = "main"
	Subagent  AgentType = "subagent"
)

var declaredAgentType = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:-]{0,63}$`)

// ParseAgentType accepts the subagent type a harness declares: a short name such as "Explore" or "code-reviewer".
func ParseAgentType(name string) (AgentType, bool) {
	if !declaredAgentType.MatchString(name) {
		return "", false
	}
	return AgentType(name), true
}

// Session is one conversation with an agent harness.
type Session struct {
	ID        string // unique across harnesses, see Harness.SessionID
	Harness   Harness
	ParentID  string // set for subagents
	AgentType AgentType
	Title     string
	Folder    string
	Start     time.Time
	End       time.Time
}

// Span widens the session time range to include t.
func (s *Session) Span(t time.Time) {
	if t.IsZero() {
		return
	}
	if s.Start.IsZero() || t.Before(s.Start) {
		s.Start = t
	}
	if t.After(s.End) {
		s.End = t
	}
}

// Tokens counts the tokens of one model request.
type Tokens struct {
	Input        int64 // uncached input tokens
	Output       int64 // includes reasoning tokens
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64 // part of CacheWrite written with Anthropic's 1-hour TTL
}

// MaxTokenCount bounds one count of one request: far above any model's window (largest seen: under a million),
// far below what would overflow the index's sums.
const MaxTokenCount = 1 << 40

// Valid reports whether every count is within 0..MaxTokenCount and the 1-hour cache writes fit in the cache writes.
func (t Tokens) Valid() bool {
	for _, n := range []int64{t.Input, t.Output, t.CacheRead, t.CacheWrite, t.CacheWrite1h} {
		if n < 0 || n > MaxTokenCount {
			return false
		}
	}
	return t.CacheWrite1h <= t.CacheWrite
}

// USD is an amount in US dollars.
type USD float64

// Request is one model call and its token usage.
type Request struct {
	Key      string // unique within the session
	At       time.Time
	Model    string
	Provider string
	Tokens
	Reasoning int64
	// LoggedCost is the cost the harness wrote itself. Zero means "price it from the price list":
	// oh-my-pi logs 0 for some models it did bill tokens for (checked on real logs, 2026-11-21).
	LoggedCost USD
	Duration   time.Duration // 0 when unknown
	TTFT       time.Duration
	StopReason string
	Error      string
}

// ToolCall is one tool invocation by the model.
type ToolCall struct {
	Key         string
	At          time.Time
	Name        string
	Server      string // MCP server name, empty for built-in tools
	ArgsChars   int
	ResultChars int
	IsError     bool
	Duration    time.Duration
}

// Prompt is one message typed by a human (or sent by a parent agent to a subagent).
type Prompt struct {
	Key  string
	At   time.Time
	Text string
}

// NewPrompt returns the prompt, or false when its text is blank and so carries no message.
func NewPrompt(key string, at time.Time, text string) (Prompt, bool) {
	if strings.TrimSpace(text) == "" {
		return Prompt{}, false
	}
	return Prompt{Key: key, At: at, Text: text}, true
}

// Transcript is everything one session log tells about its session.
type Transcript struct {
	Session  Session
	Requests []Request
	Tools    []ToolCall
	Prompts  []Prompt
	// SkippedLines counts log lines that could not be read (malformed or oversized).
	SkippedLines int
}

// DropInvalidRequests removes requests whose token counts or logged cost are impossible (negative),
// counting each as a skipped line: a corrupt log must not lower the totals.
func (t *Transcript) DropInvalidRequests() {
	valid := t.Requests[:0]
	for _, r := range t.Requests {
		if r.Tokens.Valid() && r.Reasoning >= 0 && r.Reasoning <= MaxTokenCount && r.LoggedCost >= 0 {
			valid = append(valid, r)
		} else {
			t.SkippedLines++
		}
	}
	t.Requests = valid
}

const maxTitleRunes = 80

// TitleFromFirstPrompt names an untitled session after its first prompt, on one line and at most 80 characters.
func (t *Transcript) TitleFromFirstPrompt() {
	if t.Session.Title != "" || len(t.Prompts) == 0 {
		return
	}
	title := strings.Join(strings.Fields(t.Prompts[0].Text), " ")
	if r := []rune(title); len(r) > maxTitleRunes {
		title = string(r[:maxTitleRunes]) + "…"
	}
	t.Session.Title = title
}

// SplitMCPToolName recognises the "mcp__<server>__<tool>" naming used by Claude Code and others.
func SplitMCPToolName(name string) (server, tool string) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", name
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok {
		return "", name
	}
	return server, tool
}
