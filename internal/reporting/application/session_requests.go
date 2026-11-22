package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type SessionRequest = domain.SessionRequest

type SessionRequestQuery interface {
	SessionRequests(context.Context, domain.SessionID, domain.Filter, bool, int, int) ([]domain.SessionRequest, error)
}

type ListSessionRequests struct {
	Queries SessionRequestQuery
}

func (uc ListSessionRequests) Run(ctx context.Context, id SessionID, q Query, failedOnly bool, limit, offset int) ([]SessionRequest, error) {
	return uc.Queries.SessionRequests(ctx, id, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, failedOnly, limit, offset)
}
