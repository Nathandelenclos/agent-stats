package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// The sessions' read models, as callers of ListSessions and ShowSession see them.
type (
	SessionID      = domain.SessionID
	SessionSummary = domain.SessionSummary
	SessionList    = domain.SessionList
	SessionDetail  = domain.SessionDetail
	SubagentUsage  = domain.SubagentUsage
	Prompt         = domain.Prompt
	PromptSignals  = domain.PromptSignals
)

// NewSessionID and ErrSessionNotFound are the domain's, for callers that only know this package.
var (
	NewSessionID       = domain.NewSessionID
	ErrSessionNotFound = domain.ErrSessionNotFound
)

// SessionQueries answers questions about indexed sessions. Each answer describes one state of the index, which
// indexing may be changing meanwhile. Each stops when its ctx does.
type SessionQueries interface {
	// Sessions lists the sessions active in the filter's range, most recent first.
	Sessions(context.Context, domain.Filter) ([]domain.SessionSummary, error)
	// Session describes one listed session; ErrSessionNotFound when the index lists none with that identity.
	Session(context.Context, domain.SessionID) (domain.SessionDetail, error)
}

// ListSessions lists the sessions active in a range, with what each used.
type ListSessions struct {
	Queries SessionQueries
}

// Run lists the sessions q asks for: those whose last activity is since q.Since (zero: all) and, when q.Harness is set, that harness's.
func (uc ListSessions) Run(ctx context.Context, q Query) (SessionList, error) {
	sessions, err := uc.Queries.Sessions(ctx, domain.Filter{Since: q.Since, Harness: q.Harness})
	return domain.NewSessionList(sessions), err
}

// ShowSession describes one session.
type ShowSession struct {
	Queries SessionQueries
}

// Run describes the session id names.
func (uc ShowSession) Run(ctx context.Context, id SessionID) (SessionDetail, error) {
	return uc.Queries.Session(ctx, id)
}
