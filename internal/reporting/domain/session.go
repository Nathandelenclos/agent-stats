package domain

import (
	"errors"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// SessionID identifies a session in the index, as the index states it. Build it with NewSessionID.
type SessionID struct {
	id string
}

// ErrEmptySessionID reports a session asked for without an identity.
var ErrEmptySessionID = errors.New("empty session id")

// ErrSessionNotFound reports a session the index does not list: unknown, or a subagent (those belong to their session).
var ErrSessionNotFound = errors.New("session not found")

// NewSessionID returns the identity id names.
func NewSessionID(id string) (SessionID, error) {
	if id == "" {
		return SessionID{}, ErrEmptySessionID
	}
	return SessionID{id}, nil
}

// String returns the identity as the index states it.
func (id SessionID) String() string { return id.id }

// PromptTextLimit is how many characters of a prompt a session detail carries; Prompt.Chars tells its real length.
const PromptTextLimit = 2000

// SessionSummary is one session as a listing shows it. Its usage covers the whole run: the session and the
// subagents it started, so an expensive session is not hidden behind the agents it delegated to.
type SessionSummary struct {
	ID         SessionID
	Harness    kernel.Harness
	Title      string
	Folder     string
	AgentType  string // "main", or what the harness calls a subagent that has no indexed parent
	Start, End time.Time
	Usage
	Models        []UsageGroup // the models the run used, most expensive first
	SubagentCount int
	PromptCount   int64 // typed by the user in the session itself, not the briefs sent to its subagents
	PromptChars   int64
}

// Duration is the time between the session's first and last log entries, false when either is unknown.
func (s SessionSummary) Duration() (time.Duration, bool) {
	if s.Start.IsZero() || s.End.IsZero() || s.End.Before(s.Start) {
		return 0, false
	}
	return s.End.Sub(s.Start), true
}

// AvgPromptChars is the average length of the user's prompts, 0 when there were none.
func (s SessionSummary) AvgPromptChars() float64 {
	if s.PromptCount == 0 {
		return 0
	}
	return float64(s.PromptChars) / float64(s.PromptCount)
}

// ModelShare is the fraction of the run that m accounts for: of its cost, or of its requests when nothing was priced.
func (s SessionSummary) ModelShare(m UsageGroup) float64 {
	if s.Cost > 0 {
		return float64(m.Cost / s.Cost)
	}
	if s.Requests > 0 {
		return float64(m.Requests) / float64(s.Requests)
	}
	return 0
}

// SubagentUsage is one subagent a session started, with what it used. A subagent is not a session of its own:
// it has no identity to ask for, and its figures are part of the session that started it.
type SubagentUsage struct {
	AgentType string
	Title     string
	Usage
}

// SessionList is the sessions a listing shows, most recent first, with their totals.
type SessionList struct {
	Sessions []SessionSummary
	Total    Usage // requests, failures, cost and unpriced requests of every listed session
}

// NewSessionList lists sessions and adds up what they used.
func NewSessionList(sessions []SessionSummary) SessionList {
	l := SessionList{Sessions: sessions}
	for _, s := range sessions {
		l.Total.Requests += s.Requests
		l.Total.Failed += s.Failed
		l.Total.Cost += s.Cost
		l.Total.Unpriced += s.Unpriced
	}
	return l
}

// PromptSignals are cheap lexical hints of frustration the index found in one prompt.
type PromptSignals struct {
	Yelling   int // words of 3+ letters in capitals
	Profanity int
	Anguish   int // "!!", "??", "why"…
	Negation  int
	Blame     int
	Repeated  bool // same text as an earlier prompt of the session
}

// Any reports whether the prompt shows at least one signal.
func (s PromptSignals) Any() bool {
	return s.Repeated || s.Yelling+s.Profanity+s.Anguish+s.Negation+s.Blame > 0
}

// Prompt is one message the user typed in a session.
type Prompt struct {
	At      time.Time
	Text    string // the first PromptTextLimit characters
	Chars   int
	Words   int
	Signals PromptSignals
}

// SessionDetail is what one session did: its summary, the tools it called, the subagents it started and its prompts in order.
type SessionDetail struct {
	SessionSummary
	Tools     []ToolUsage // most used first, subagents' calls included
	Subagents []SubagentUsage
	Prompts   []Prompt
}
