// Package application holds the use case of the reporting context and the port it reads usage through.
package application

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// The report's read models, as callers of BuildReport see them.
type (
	Usage      = domain.Usage
	UsageGroup = domain.UsageGroup
	ToolUsage  = domain.ToolUsage
)

// Query asks for the report of activity since a time (zero: all) and optionally one harness (empty: all).
type Query struct {
	Since   time.Time
	Harness kernel.Harness
}

// UsageQueries answers usage questions over the indexed sessions.
type UsageQueries interface {
	Total(domain.Filter) (domain.Usage, error)
	UsageBy(domain.Filter, domain.Dimension) ([]domain.UsageGroup, error)
	Tools(domain.Filter) ([]domain.ToolUsage, error)
}

// Report is the usage summary: totals, usage grouped along every dimension, and tool calls.
type Report struct {
	Total                                 Usage
	ByHarness, ByModel, ByFolder, ByAgent []UsageGroup
	Tools                                 []ToolUsage
}

// BuildReport summarises indexed usage.
type BuildReport struct {
	Queries UsageQueries
}

// Run builds the report for the activity q asks for.
func (uc BuildReport) Run(q Query) (Report, error) {
	f := domain.Filter{Since: q.Since, Harness: q.Harness}
	var r Report
	var err error
	if r.Total, err = uc.Queries.Total(f); err != nil {
		return r, err
	}
	for _, g := range []struct {
		dst *[]UsageGroup
		dim domain.Dimension
	}{
		{&r.ByHarness, domain.ByHarness},
		{&r.ByModel, domain.ByModel},
		{&r.ByFolder, domain.ByFolder},
		{&r.ByAgent, domain.ByAgentType},
	} {
		if *g.dst, err = uc.Queries.UsageBy(f, g.dim); err != nil {
			return r, err
		}
	}
	r.Tools, err = uc.Queries.Tools(f)
	return r, err
}
