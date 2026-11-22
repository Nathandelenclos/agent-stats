package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type TraceEvent = domain.TraceEvent

type SessionTraceQuery interface {
	SessionTrace(context.Context, domain.SessionID, domain.Filter, int, int) ([]domain.TraceEvent, error)
}

type ShowSessionTrace struct {
	Queries SessionTraceQuery
}

func (uc ShowSessionTrace) Run(ctx context.Context, id SessionID, q Query, limit, offset int) ([]TraceEvent, error) {
	return uc.Queries.SessionTrace(ctx, id, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, limit, offset)
}
