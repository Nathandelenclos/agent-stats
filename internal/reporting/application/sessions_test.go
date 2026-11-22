package application

import (
	"errors"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// recordedSessions is a session index that remembers what it was asked.
type recordedSessions struct {
	filter domain.Filter
	asked  domain.SessionID
	list   []domain.SessionSummary
	detail domain.SessionDetail
	err    error
}

func (r *recordedSessions) Sessions(f domain.Filter) ([]domain.SessionSummary, error) {
	r.filter = f
	return r.list, r.err
}

func (r *recordedSessions) Session(id domain.SessionID) (domain.SessionDetail, error) {
	r.asked = id
	return r.detail, r.err
}

func TestListSessionsAsksForTheRangeOfTheQuery(t *testing.T) {
	// Given an index holding one session
	id, _ := domain.NewSessionID("claude:s1")
	idx := &recordedSessions{list: []domain.SessionSummary{{ID: id, Title: "fix the build"}}}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// When the sessions of a harness since a date are listed
	got, err := ListSessions{Queries: idx}.Run(Query{Since: since, Harness: kernel.Codex})

	// Then the index is asked for exactly that range, and its sessions come back
	if err != nil || len(got) != 1 || got[0].Title != "fix the build" {
		t.Fatalf("sessions = %+v, %v", got, err)
	}
	if !idx.filter.Since.Equal(since) || idx.filter.Harness != kernel.Codex {
		t.Errorf("filter = %+v", idx.filter)
	}
}

func TestShowSessionDescribesTheSessionAskedFor(t *testing.T) {
	// Given an index that knows a session
	id, _ := domain.NewSessionID("claude:s1")
	idx := &recordedSessions{detail: domain.SessionDetail{SessionSummary: domain.SessionSummary{ID: id}, Prompts: []domain.Prompt{{Text: "hi"}}}}

	// When that session is shown
	d, err := ShowSession{Queries: idx}.Run(id)

	// Then the index was asked about it and its prompts are in the answer
	if err != nil || idx.asked != id || len(d.Prompts) != 1 {
		t.Errorf("detail = %+v, asked %v, %v", d, idx.asked, err)
	}
}

func TestShowSessionReportsAnUnknownSession(t *testing.T) {
	// Given an index that lists no such session
	idx := &recordedSessions{err: domain.ErrSessionNotFound}
	id, _ := NewSessionID("claude:gone")

	// When it is shown, then the caller can tell it is missing
	if _, err := (ShowSession{Queries: idx}).Run(id); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestSessionFiguresFollowFromWhatTheIndexReports(t *testing.T) {
	// Given a session run mostly on an expensive model, with a few long prompts
	s := domain.SessionSummary{
		Usage:       domain.Usage{Requests: 10, Cost: 10},
		PromptCount: 4, PromptChars: 100,
		Start: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC),
	}
	big := domain.UsageGroup{Group: "big", Usage: domain.Usage{Requests: 2, Cost: 9}}
	small := domain.UsageGroup{Group: "small", Usage: domain.Usage{Requests: 8, Cost: 1}}

	// Then the model share is by cost, the prompt average is per prompt and the duration is the time span
	if got := s.ModelShare(big); got != 0.9 {
		t.Errorf("big share = %v", got)
	}
	if got := s.ModelShare(small); got != 0.1 {
		t.Errorf("small share = %v", got)
	}
	if got := s.AvgPromptChars(); got != 25 {
		t.Errorf("average prompt = %v", got)
	}
	if d, ok := s.Duration(); !ok || d != 30*time.Minute {
		t.Errorf("duration = %v %v", d, ok)
	}

	// And with nothing priced the share falls back to requests, and no prompt averages to zero
	s.Cost = 0
	if got := s.ModelShare(small); got != 0.8 {
		t.Errorf("unpriced share = %v", got)
	}
	if got := (domain.SessionSummary{}).AvgPromptChars(); got != 0 {
		t.Errorf("average of no prompt = %v", got)
	}
	if got := (domain.SessionSummary{}).ModelShare(small); got != 0 {
		t.Errorf("share of an empty session = %v", got)
	}
}
