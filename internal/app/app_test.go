package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

type fakeLogs struct {
	files    []LogFile
	problems []error
	reads    map[string]usage.Transcript
	readErr  map[string]error
	read     []string
}

func (l *fakeLogs) List() ([]LogFile, []error, error) { return l.files, l.problems, nil }
func (l *fakeLogs) Read(f LogFile) (usage.Transcript, error) {
	l.read = append(l.read, f.Path)
	return l.reads[f.Path], l.readErr[f.Path]
}

type fakeIndex struct {
	UsageIndex // queries are not used by IndexSessions
	known      map[string]LogFile
	saved      map[string]IndexedTranscript
}

func (x *fakeIndex) IndexedFiles() (map[string]LogFile, error) { return x.known, nil }
func (x *fakeIndex) Save(f LogFile, t IndexedTranscript) error {
	x.saved[f.Path] = t
	return nil
}

func TestIndexSessionsIndexesOnlyNewOrChangedLogs(t *testing.T) {
	mtime := time.Unix(100, 5)
	rates, _ := usage.NewRates(1, 0, 0, 0)
	logs := &fakeLogs{
		files: []LogFile{
			{Path: "same", Size: 1, ModTime: mtime},
			{Path: "grown", Size: 2, ModTime: mtime},
			{Path: "new", Size: 1, ModTime: mtime},
			{Path: "broken", Size: 1, ModTime: mtime},
		},
		problems: []error{errors.New("unreadable: permission denied")},
		reads: map[string]usage.Transcript{
			"grown": {SkippedLines: 2, Requests: []usage.Request{{Model: "m", Tokens: usage.Tokens{Input: 3}}, {Model: "m", LoggedCost: 0.5}}},
			"new":   {Prompts: []usage.Prompt{{Text: "a"}, {Text: "A"}}},
		},
		readErr: map[string]error{"broken": errors.New("unexpected EOF")},
	}
	index := &fakeIndex{
		known: map[string]LogFile{"same": {Path: "same", Size: 1, ModTime: mtime}, "grown": {Path: "grown", Size: 1, ModTime: mtime}},
		saved: map[string]IndexedTranscript{},
	}
	rep, err := IndexSessions{Logs: logs, Index: index, Prices: usage.PriceList{"m": rates}}.Run(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 4 || rep.Indexed != 2 || rep.SkippedLines != 2 || len(rep.Errors) != 2 {
		t.Errorf("report = %+v", rep)
	}
	if len(logs.read) != 3 || logs.read[0] != "grown" {
		t.Errorf("read %v, want grown, new, broken", logs.read)
	}
	if c := index.saved["grown"].Costs; len(c) != 2 || c[0] != (PricedCost{3, true}) || c[1] != (PricedCost{0.5, true}) {
		t.Errorf("costs = %+v", c)
	}
	if s := index.saved["new"].Signals; len(s) != 2 || !s[1].Repeated {
		t.Errorf("signals = %+v", s)
	}
}

type fakeQueries struct {
	UsageIndex
	asked []Dimension
}

func (q *fakeQueries) Total(Filter) (Usage, error) { return Usage{Requests: 3}, nil }
func (q *fakeQueries) UsageBy(_ Filter, d Dimension) ([]UsageGroup, error) {
	q.asked = append(q.asked, d)
	return []UsageGroup{{Group: "g"}}, nil
}
func (q *fakeQueries) Tools(Filter) ([]ToolUsage, error) { return []ToolUsage{{Name: "t"}}, nil }

func TestBuildReportAsksEveryDimension(t *testing.T) {
	q := &fakeQueries{}
	r, err := BuildReport{Index: q}.Run(Filter{})
	if err != nil || r.Total.Requests != 3 || len(r.ByAgent) != 1 || len(r.Tools) != 1 {
		t.Fatalf("report = %+v %v", r, err)
	}
	if want := []Dimension{ByHarness, ByModel, ByFolder, ByAgentType}; len(q.asked) != 4 || q.asked[3] != want[3] {
		t.Errorf("asked %v, want %v", q.asked, want)
	}
}

func TestWithCacheRate(t *testing.T) {
	u := Usage{Input: 10, CacheRead: 30, CacheWrite: 10}
	if got := u.WithCacheRate(); got.CacheRate != 0.6 || u.CacheRate != 0 {
		t.Errorf("rate = %v, original changed to %v", got.CacheRate, u.CacheRate)
	}
}
