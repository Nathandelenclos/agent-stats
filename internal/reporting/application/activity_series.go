package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// ActivitySeriesQuery reads dated model requests or tool calls in the report's scope.
type ActivitySeriesQuery interface {
	ActivitySeries(context.Context, domain.Filter, domain.ActivityKind, domain.BucketSize) ([]domain.ActivitySample, error)
}

type ListActivitySeries struct {
	Queries ActivitySeriesQuery
}

func (uc ListActivitySeries) Run(ctx context.Context, q Query, kind domain.ActivityKind, size domain.BucketSize) ([]domain.ActivitySample, error) {
	return uc.Queries.ActivitySeries(ctx, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, kind, size)
}
