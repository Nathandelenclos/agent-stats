// Package domain models what a session log tells: a session with an agent, its model requests,
// tool calls and prompts, and the rules that apply to them. It does no I/O.
package domain

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// SessionID identifies a session across harnesses: "<harness>:<native id>", and "<parent id>/<name>" for a subagent.
// Build it with NewSessionID; the zero SessionID means "none".
type SessionID struct {
	id string
}

// String returns the identity as stored: "<harness>:<native id>[/<name>]".
func (id SessionID) String() string { return id.id }

// IsZero reports the zero SessionID.
func (id SessionID) IsZero() bool { return id.id == "" }

// ErrEmptySessionID reports a missing native session id or subagent name.
var ErrEmptySessionID = errors.New("empty session id")

// NewSessionID returns the identity of a session that harness h names native.
func NewSessionID(h kernel.Harness, native string) (SessionID, error) {
	if native == "" || h.IsZero() {
		return SessionID{}, fmt.Errorf("%w for %q", ErrEmptySessionID, h)
	}
	return SessionID{h.String() + ":" + native}, nil
}

// Subagent returns the identity of the subagent session called name inside this session.
func (id SessionID) Subagent(name string) (SessionID, error) {
	if name == "" || id.IsZero() {
		return SessionID{}, fmt.Errorf("%w: subagent %q of %q", ErrEmptySessionID, name, id.id)
	}
	return SessionID{id.id + "/" + name}, nil
}

// AgentType tells a main conversation from a subagent; harnesses that name their subagent types use that name.
// Use MainAgent, Subagent or ParseAgentType.
type AgentType struct {
	name string
}

var (
	MainAgent = AgentType{"main"}
	Subagent  = AgentType{"subagent"}
)

// String returns the agent type name.
func (t AgentType) String() string { return t.name }

var declaredAgentType = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:-]{0,63}$`)

// ParseAgentType accepts the subagent type a harness declares: a short name such as "Explore" or "code-reviewer".
func ParseAgentType(name string) (AgentType, bool) {
	if !declaredAgentType.MatchString(name) {
		return AgentType{}, false
	}
	return AgentType{name}, true
}

// Session is one conversation with an agent harness.
type Session struct {
	ID        SessionID
	Harness   kernel.Harness
	ParentID  SessionID // set for subagents
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

// MaxLoggedDuration bounds a duration read from a log (longest seen on real logs: about 83 hours,
// a tool call left waiting over a weekend).
const MaxLoggedDuration = 7 * 24 * time.Hour

// Duration is how long a request or tool call took, as its log states it. The zero value means unknown.
type Duration struct {
	d time.Duration
}

// NewDuration keeps d when it is positive and at most MaxLoggedDuration; otherwise the duration is unknown.
func NewDuration(d time.Duration) Duration {
	if d <= 0 || d > MaxLoggedDuration {
		return Duration{}
	}
	return Duration{d}
}

// LoggedDuration reads a duration a log states in (possibly fractional) milliseconds.
func LoggedDuration(ms float64) Duration {
	if !(math.Abs(ms) < 1e12) { // NaN, infinite, or too large to convert to nanoseconds
		return Duration{}
	}
	return NewDuration(time.Duration(math.Round(ms * float64(time.Millisecond))))
}

// Elapsed is the time from start to end at millisecond precision, as two log timestamps state it;
// unknown when either is missing.
func Elapsed(start, end time.Time) Duration {
	if start.IsZero() || end.IsZero() {
		return Duration{}
	}
	return NewDuration(end.Truncate(time.Millisecond).Sub(start.Truncate(time.Millisecond)))
}

// Known reports whether the log stated a usable duration.
func (d Duration) Known() bool { return d.d > 0 }

// Value is the duration, 0 when unknown.
func (d Duration) Value() time.Duration { return d.d }

// UnspecifiedFailure is the failure of a request whose log says it failed without saying why.
const UnspecifiedFailure = "error"

// Request is one model call and its token usage.
type Request struct {
	Key      string // unique within the session
	At       time.Time
	Model    string
	Provider string
	Tokens   kernel.Tokens
	// LoggedCost is the cost the harness wrote itself; zero when it wrote none or wrote 0.
	LoggedCost kernel.Cost
	Duration   Duration
	TTFT       Duration
	StopReason string // as the harness names it
	Failure    string // why the request failed; empty when it did not
}

// Failed reports whether the request ended in an error.
func (r Request) Failed() bool { return r.Failure != "" }

// Cost returns what the request cost: the harness's own figure when it logged a positive one, otherwise
// the listed price (listed, known). oh-my-pi logs 0 for some models it did bill tokens for (checked on
// real logs, 2026-11-21), so a logged 0 means "price it from the list". priced is false when neither is known.
func (r Request) Cost(listed kernel.Cost, known bool) (cost kernel.Cost, priced bool) {
	if !r.LoggedCost.IsZero() {
		return r.LoggedCost, true
	}
	return listed, known
}

// ToolCall is one tool invocation by the model.
type ToolCall struct {
	Key    string
	At     time.Time
	Name   string
	Server string // MCP server name, empty for built-in tools
	// Skill is the skill a call loads (Claude Code's Skill tool); empty for every other call.
	Skill       string
	ArgsChars   int
	ResultChars int
	IsError     bool
	Duration    Duration
}

var declaredSkill = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:-]{0,127}$`)

// ParseSkillName accepts the name of a skill as a harness logs it: "review" or "plugin:review".
func ParseSkillName(name string) (string, bool) {
	return name, declaredSkill.MatchString(name)
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
	// SkippedLines counts log lines that could not be used (malformed, oversized or with impossible values).
	SkippedLines int
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
