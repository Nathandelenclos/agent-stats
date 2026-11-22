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
	USD        = domain.USD
)

// Query asks for the report of activity since a time (zero: all) and optionally one harness (empty: all).
type Query struct {
	Since   time.Time
	Harness kernel.Harness
}

// UsageReader answers usage questions over one state of the indexed sessions.
type UsageReader interface {
	Total(domain.Filter) (domain.Usage, error)
	UsageBy(domain.Filter, domain.Dimension) ([]domain.UsageGroup, error)
	Tools(domain.Filter) ([]domain.ToolUsage, error)
}

// UsageQueries gives access to the indexed sessions, which indexing may be changing meanwhile.
type UsageQueries interface {
	// Snapshot runs fn on one consistent state of the index: whatever indexing commits meanwhile
	// does not show in the answers the reader gives fn.
	Snapshot(fn func(UsageReader) error) error
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
	var r Report
	err := uc.Queries.Snapshot(func(u UsageReader) (err error) {
		r, err = build(u, domain.Filter{Since: q.Since, Harness: q.Harness})
		return err
	})
	return r, err
}

func build(u UsageReader, f domain.Filter) (Report, error) {
	var r Report
	var err error
	if r.Total, err = u.Total(f); err != nil {
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
		if *g.dst, err = u.UsageBy(f, g.dim); err != nil {
			return r, err
		}
	}
	r.Tools, err = u.Tools(f)
	return r, err
}
