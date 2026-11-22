package application

import (
	"context"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// shiftingIndex is a usage index that indexing keeps changing: one more request is indexed after every answer
// given outside of a snapshot. Inside a snapshot the state stays what it was when the snapshot began.
type shiftingIndex struct {
	requests int64
	frozen   bool
	asked    []domain.Dimension
	ctx      context.Context // the one the last snapshot was asked under
}

func (x *shiftingIndex) Snapshot(ctx context.Context, fn func(UsageReader) error) error {
	x.ctx = ctx
	x.frozen = true
	defer func() { x.frozen = false }()
	return fn(x)
}

// answer is the number of requests indexed when the question is asked.
func (x *shiftingIndex) answer() int64 {
	n := x.requests
	if !x.frozen {
		x.requests++
	}
	return n
}

func (x *shiftingIndex) Total(domain.Filter) (domain.Usage, error) {
	return domain.Usage{Requests: x.answer()}, nil
}

func (x *shiftingIndex) UsageBy(_ domain.Filter, d domain.Dimension) ([]domain.UsageGroup, error) {
	x.asked = append(x.asked, d)
	return []domain.UsageGroup{{Group: "g", Usage: domain.Usage{Requests: x.answer()}}}, nil
}

func (x *shiftingIndex) Tools(domain.Filter) ([]domain.ToolUsage, error) {
	return []domain.ToolUsage{{Name: "t", Calls: x.answer()}}, nil
}

func TestReportDescribesOneStateOfTheIndexBeingWritten(t *testing.T) {
	// Given an index that indexing changes between any two questions asked outside of a snapshot
	idx := &shiftingIndex{requests: 1}

	// When a report is built
	r, err := BuildReport{Queries: idx}.Run(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}

	// Then every figure of it describes the same state: the total, each grouping and the tool calls agree
	got := []int64{r.Total.Requests, r.ByHarness[0].Requests, r.ByModel[0].Requests, r.ByFolder[0].Requests, r.ByAgent[0].Requests, r.Tools[0].Calls}
	for _, n := range got {
		if n != 1 {
			t.Fatalf("the report mixes states of the index: %v, want 1 everywhere", got)
		}
	}
}

func TestBuildReportAsksEveryDimension(t *testing.T) {
	q := &shiftingIndex{requests: 3}
	r, err := BuildReport{Queries: q}.Run(context.Background(), Query{})
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

type ctxKey struct{}

func TestTheReportsAreBuiltUnderTheCallersContext(t *testing.T) {
	// Given a caller whose context carries a mark, as the context of a request carries its cancellation
	ctx := context.WithValue(context.Background(), ctxKey{}, "request")
	for name, run := range map[string]func(*shiftingIndex) error{
		"report":   func(x *shiftingIndex) error { _, err := BuildReport{Queries: x}.Run(ctx, Query{}); return err },
		"timeline": func(x *shiftingIndex) error { _, err := BuildTimelineReport{Queries: x}.Run(ctx, Query{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			// When the report is built
			idx := &shiftingIndex{}
			if err := run(idx); err != nil {
				t.Fatal(err)
			}

			// Then the index was asked under that very context, so that it can stop when the caller gives up
			if idx.ctx != ctx {
				t.Errorf("snapshot context = %v, want the caller's", idx.ctx)
			}
		})
	}
}
