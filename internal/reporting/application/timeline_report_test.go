package application

import (
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

func TestTimelineReportAddsTheDaysToTheSameStateOfTheIndex(t *testing.T) {
	// Given an index that indexing changes between any two questions asked outside of a snapshot
	idx := &shiftingIndex{requests: 1}

	// When the report with its timeline is built
	r, err := BuildTimelineReport{Queries: idx}.Run(Query{})
	if err != nil {
		t.Fatal(err)
	}

	// Then the days agree with the totals and the groupings
	if r.Total.Requests != 1 || r.Daily[0].Requests != 1 || r.ByModel[0].Requests != 1 {
		t.Fatalf("the timeline mixes states of the index: total %d, day %d, model %d", r.Total.Requests, r.Daily[0].Requests, r.ByModel[0].Requests)
	}
	if got := idx.asked[len(idx.asked)-1]; got != domain.ByDay {
		t.Errorf("last dimension asked = %d, want the days", got)
	}
}
