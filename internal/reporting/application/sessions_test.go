package application

import (
	"context"
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

func (r *recordedSessions) Sessions(ctx context.Context, f domain.Filter) ([]domain.SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.filter = f
	return r.list, r.err
}

func (r *recordedSessions) Session(ctx context.Context, id domain.SessionID, _ kernel.Project) (domain.SessionDetail, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionDetail{}, err
	}
	r.asked = id
	return r.detail, r.err
}

func TestListSessionsAsksForTheRangeOfTheQuery(t *testing.T) {
	// Given an index holding one session
	id, _ := domain.NewSessionID("claude:s1")
	idx := &recordedSessions{list: []domain.SessionSummary{{ID: id, Title: "fix the build"}}}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// When the sessions of a harness since a date are listed
	got, err := ListSessions{Queries: idx}.Run(context.Background(), Query{Since: since, Harness: kernel.Codex})

	// Then the index is asked for exactly that range, and its sessions come back
	if err != nil || len(got.Sessions) != 1 || got.Sessions[0].Title != "fix the build" {
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
	d, err := ShowSession{Queries: idx}.Run(context.Background(), id, kernel.Project{})

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
	if _, err := (ShowSession{Queries: idx}).Run(context.Background(), id, kernel.Project{}); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestTheListedSessionsAreAddedUp(t *testing.T) {
	// Given three listed sessions, one with requests that have no known price
	idx := &recordedSessions{list: []domain.SessionSummary{
		{Usage: domain.Usage{Requests: 10, Failed: 1, Cost: 2.5}},
		{Usage: domain.Usage{Requests: 5, Cost: 0.5, Unpriced: 3}},
		{},
	}}

	// When they are listed
	got, err := ListSessions{Queries: idx}.Run(context.Background(), Query{})

	// Then the list carries the totals of what its sessions used
	if err != nil || got.Total.Requests != 15 || got.Total.Failed != 1 || got.Total.Cost != 3 || got.Total.Unpriced != 3 {
		t.Errorf("total = %+v, %v", got.Total, err)
	}
}

// aSessionOnABigModel is a session of ten requests and ten dollars, mostly on an expensive model, with four long prompts.
func aSessionOnABigModel() domain.SessionSummary {
	return domain.SessionSummary{
		Usage:       domain.Usage{Requests: 10, Cost: 10},
		PromptCount: 4, PromptChars: 100,
		Start: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC),
	}
}

var (
	bigModel   = domain.UsageGroup{Group: "big", Usage: domain.Usage{Requests: 2, Cost: 9}}
	smallModel = domain.UsageGroup{Group: "small", Usage: domain.Usage{Requests: 8, Cost: 1}}
)

func TestAModelsShareIsItsShareOfTheCost(t *testing.T) {
	// Given a session where one model made 2 of 10 requests but cost 9 of 10 dollars
	s := aSessionOnABigModel()

	// Then the share tells where the money went
	if got := s.ModelShare(bigModel); got != 0.9 {
		t.Errorf("big model share = %v, want 0.9", got)
	}
	if got := s.ModelShare(smallModel); got != 0.1 {
		t.Errorf("small model share = %v, want 0.1", got)
	}
}

func TestAModelsShareFallsBackToRequestsWhenNothingIsPriced(t *testing.T) {
	s := aSessionOnABigModel()
	s.Cost = 0
	if got := s.ModelShare(smallModel); got != 0.8 {
		t.Errorf("share without prices = %v, want 0.8", got)
	}
	if got := (domain.SessionSummary{}).ModelShare(smallModel); got != 0 {
		t.Errorf("share of a session without requests = %v, want 0", got)
	}
}

func TestThePromptsOfASessionAverageToTheirLength(t *testing.T) {
	if got := aSessionOnABigModel().AvgPromptChars(); got != 25 {
		t.Errorf("average prompt = %v, want 25", got)
	}
	if got := (domain.SessionSummary{}).AvgPromptChars(); got != 0 {
		t.Errorf("average of no prompt = %v, want 0", got)
	}
}

func TestASessionLastsFromItsFirstToItsLastEntry(t *testing.T) {
	if d, ok := aSessionOnABigModel().Duration(); !ok || d != 30*time.Minute {
		t.Errorf("duration = %v %v, want 30m", d, ok)
	}
	if _, ok := (domain.SessionSummary{}).Duration(); ok {
		t.Error("a session without timestamps has a duration")
	}
}

func TestNoSessionIsListedForACallerThatGaveUp(t *testing.T) {
	// Given a caller that has already given up, and an index holding a session
	ctx, giveUp := context.WithCancel(context.Background())
	giveUp()
	id, _ := domain.NewSessionID("claude:s1")
	idx := &recordedSessions{list: []domain.SessionSummary{{ID: id}}}

	// When the sessions are listed
	got, err := ListSessions{Queries: idx}.Run(ctx, Query{})

	// Then the caller is told it gave up and gets no session
	if !errors.Is(err, context.Canceled) || len(got.Sessions) != 0 {
		t.Errorf("sessions = %+v, error = %v, want none and context.Canceled", got, err)
	}
}

func TestNoSessionIsShownToACallerThatGaveUp(t *testing.T) {
	// Given a caller that has already given up, and an index that knows the session
	ctx, giveUp := context.WithCancel(context.Background())
	giveUp()
	id, _ := domain.NewSessionID("claude:s1")
	idx := &recordedSessions{detail: domain.SessionDetail{SessionSummary: domain.SessionSummary{ID: id}}}

	// When the session is shown
	_, err := ShowSession{Queries: idx}.Run(ctx, id, kernel.Project{})

	// Then the caller is told it gave up
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
