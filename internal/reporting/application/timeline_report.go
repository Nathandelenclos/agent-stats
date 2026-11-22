package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// TimelineReport is the usage report with its usage per local calendar day, oldest first.
type TimelineReport struct {
	Report
	Daily []UsageGroup // Group is the day, as 2006-01-02
}

// BuildTimelineReport summarises indexed usage and when it happened. Interfaces that chart cost over time use it;
// the plain report and the terminal view keep BuildReport and do not pay for the per-day grouping.
type BuildTimelineReport struct {
	Queries UsageQueries
}

// Run builds the report for the activity q asks for, its days included, all from one state of the index; it gives up
// when ctx ends.
func (uc BuildTimelineReport) Run(ctx context.Context, q Query) (TimelineReport, error) {
	var r TimelineReport
	err := uc.Queries.Snapshot(ctx, func(u UsageReader) (err error) {
		f := domain.Filter{Since: q.Since, Harness: q.Harness}
		if r.Report, err = build(u, f); err != nil {
			return err
		}
		r.Daily, err = u.UsageBy(f, domain.ByDay)
		return err
	})
	return r, err
}
