package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type (
	RelationKind    = domain.RelationKind
	SessionRelation = domain.SessionRelation
)

const (
	RelationDay     = domain.RelationDay
	RelationHarness = domain.RelationHarness
	RelationModel   = domain.RelationModel
	RelationFolder  = domain.RelationFolder
	RelationAgent   = domain.RelationAgent
	RelationTool    = domain.RelationTool
	RelationMCP     = domain.RelationMCP
)

// RelatedSessionQuery identifies the sessions contributing to a report row, with only the matching activity.
type RelatedSessionQuery interface {
	RelatedSessions(context.Context, domain.Filter, domain.SessionRelation) ([]domain.SessionSummary, error)
}

// ListRelatedSessions lists the sessions behind a selected report row or day.
type ListRelatedSessions struct {
	Queries RelatedSessionQuery
}

// Run keeps only activity in both the report's filter and the selected row.
func (uc ListRelatedSessions) Run(ctx context.Context, q Query, rel SessionRelation) (SessionList, error) {
	rows, err := uc.Queries.RelatedSessions(ctx, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project}, rel)
	return domain.NewSessionList(rows), err
}
