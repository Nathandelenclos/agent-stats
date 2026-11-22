package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type activityIndex func(domain.Filter, domain.ActivityKind, domain.BucketSize) ([]domain.ActivitySample, error)

func (idx activityIndex) ActivitySeries(_ context.Context, filter domain.Filter, kind domain.ActivityKind, size domain.BucketSize) ([]domain.ActivitySample, error) {
	return idx(filter, kind, size)
}

func TestActivitySeriesUsesTheRequestedScopeAndPeriod(t *testing.T) {
	var want domain.BucketSize
	r := newRig(t, func(o *Options) {
		o.Project, _ = kernel.NewProject("/work/app")
		o.ActivitySeries.Queries = activityIndex(func(filter domain.Filter, kind domain.ActivityKind, size domain.BucketSize) ([]domain.ActivitySample, error) {
			if size != want || filter.Project.Root() != "/work/app" || filter.Harness != kernel.Claude || kind != domain.ToolActivity {
				t.Fatalf("received %v, %v, %v", filter, kind, size)
			}
			return []domain.ActivitySample{{Name: "read", Source: "memory", Calls: 4, Errors: 1}}, nil
		})
	})
	for _, tc := range []struct {
		period string
		size   domain.BucketSize
	}{{"24h", domain.HourBucket}, {"7d", domain.DayBucket}, {"", domain.MonthBucket}, {"180d", domain.MonthBucket}} {
		want = tc.size
		got := decode[activitySeriesJSON](t, r.get("/api/activity-series?kind=tools&harness=claude&project=current&since="+tc.period))
		if got.BucketSize != want || len(got.Samples) != 1 || got.Samples[0].Source != "memory" || got.Samples[0].Errors != 1 {
			t.Fatalf("%q: received %+v", tc.period, got)
		}
	}
	wantProblem(t, r.get("/api/activity-series?kind=folders"), http.StatusBadRequest, problemInvalidParameter)
}
