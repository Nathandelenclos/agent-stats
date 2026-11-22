package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type SessionRequest = domain.SessionRequest
type RequestFilter = domain.RequestFilter

type SessionRequestQuery interface {
	SessionRequests(context.Context, domain.SessionID, domain.Filter, RequestFilter, int, int) ([]domain.SessionRequest, error)
}

type ListSessionRequests struct {
	Queries SessionRequestQuery
}

func (uc ListSessionRequests) Run(ctx context.Context, id SessionID, q Query, filters RequestFilter, limit, offset int) ([]SessionRequest, error) {
	return uc.Queries.SessionRequests(ctx, id, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, filters, limit, offset)
}

type ErrorSignature = domain.ErrorSignature

type ErrorSignatureQuery interface {
	ErrorSignatures(context.Context, domain.Filter, int) ([]domain.ErrorSignature, error)
}

type ListErrorSignatures struct {
	Queries ErrorSignatureQuery
}

func (uc ListErrorSignatures) Run(ctx context.Context, q Query, limit int) ([]ErrorSignature, error) {
	return uc.Queries.ErrorSignatures(ctx, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, limit)
}
