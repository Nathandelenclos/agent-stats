package application

import (
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// fakeQueries is the usage index: it counts how often a consistent state was asked for, and refuses
// questions asked outside of one.
type fakeQueries struct {
	asked     []domain.Dimension
	snapshots int
	inside    bool
}

func (q *fakeQueries) Snapshot(fn func(UsageReader) error) error {
	q.snapshots++
	q.inside = true
	defer func() { q.inside = false }()
	return fn(q)
}

func (q *fakeQueries) Total(domain.Filter) (domain.Usage, error) {
	q.mustBeInside()
	return domain.Usage{Requests: 3}, nil
}
func (q *fakeQueries) UsageBy(_ domain.Filter, d domain.Dimension) ([]domain.UsageGroup, error) {
	q.mustBeInside()
	q.asked = append(q.asked, d)
	return []domain.UsageGroup{{Group: "g"}}, nil
}
func (q *fakeQueries) Tools(domain.Filter) ([]domain.ToolUsage, error) {
	q.mustBeInside()
	return []domain.ToolUsage{{Name: "t"}}, nil
}

func (q *fakeQueries) mustBeInside() {
	if !q.inside {
		panic("question asked outside of a snapshot")
	}
}

func TestBuildReportReadsOneSnapshot(t *testing.T) {
	// Given an index that can change between two questions
	q := &fakeQueries{}

	// When a report is built
	if _, err := (BuildReport{Queries: q}).Run(Query{}); err != nil {
		t.Fatal(err)
	}

	// Then every figure of it comes from a single consistent state
	if q.snapshots != 1 {
		t.Errorf("report read %d snapshots, want 1", q.snapshots)
	}
}

func TestBuildReportAsksEveryDimension(t *testing.T) {
	q := &fakeQueries{}
	r, err := BuildReport{Queries: q}.Run(Query{})
	if err != nil || r.Total.Requests != 3 || len(r.ByAgent) != 1 || len(r.Tools) != 1 {
		t.Fatalf("report = %+v %v", r, err)
	}
	if want := []domain.Dimension{domain.ByHarness, domain.ByModel, domain.ByFolder, domain.ByAgentType}; len(q.asked) != 4 || q.asked[3] != want[3] {
		t.Errorf("asked %v, want %v", q.asked, want)
	}
}

func TestCacheRate(t *testing.T) {
	if r := (domain.Usage{Input: 10, CacheRead: 30, CacheWrite: 10}).CacheRate(); r != 0.6 {
		t.Errorf("rate = %v", r)
	}
	if r := (domain.Usage{}).CacheRate(); r != 0 {
		t.Errorf("empty rate = %v", r)
	}
}
