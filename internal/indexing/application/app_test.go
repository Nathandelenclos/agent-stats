package application

import (
	"errors"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

type fakeLogs struct {
	files    []LogFile
	problems []error
	reads    map[string]domain.Transcript
	readErr  map[string]error
	read     []string
}

func (l *fakeLogs) List() ([]LogFile, []error, error) { return l.files, l.problems, nil }
func (l *fakeLogs) Read(f LogFile) (domain.Transcript, error) {
	l.read = append(l.read, f.Path)
	return l.reads[f.Path], l.readErr[f.Path]
}

type fakeStore struct {
	known map[string]LogFile
	saved map[string]IndexedTranscript
}

func (s *fakeStore) IndexedFiles() (map[string]LogFile, error) { return s.known, nil }
func (s *fakeStore) Save(f LogFile, t IndexedTranscript) error {
	s.saved[f.Path] = t
	return nil
}

// flatPrices charges 1 USD per input token for model "m".
type flatPrices struct{}

func (flatPrices) Cost(model string, t kernel.Tokens) (kernel.Cost, bool) {
	if model != "m" {
		return kernel.Cost{}, false
	}
	c, err := kernel.NewCost(float64(t.Counts().Input))
	return c, err == nil
}

func TestIndexSessionsIndexesOnlyNewOrChangedLogs(t *testing.T) {
	mtime := time.Unix(100, 5)
	three, _ := kernel.NewTokens(kernel.TokenCounts{Input: 3})
	logged, _ := kernel.NewCost(0.5)
	logs := &fakeLogs{
		files: []LogFile{
			{Path: "same", Size: 1, ModTime: mtime},
			{Path: "grown", Size: 2, ModTime: mtime},
			{Path: "new", Size: 1, ModTime: mtime},
			{Path: "broken", Size: 1, ModTime: mtime},
		},
		problems: []error{errors.New("unreadable: permission denied")},
		reads: map[string]domain.Transcript{
			"grown": {SkippedLines: 2, Requests: []domain.Request{{Model: "m", Tokens: three}, {Model: "m", LoggedCost: logged}, {Model: "x"}}},
			"new":   {Prompts: []domain.Prompt{{Text: "a"}, {Text: "A"}}},
		},
		readErr: map[string]error{"broken": errors.New("unexpected EOF")},
	}
	store := &fakeStore{
		known: map[string]LogFile{"same": {Path: "same", Size: 1, ModTime: mtime}, "grown": {Path: "grown", Size: 1, ModTime: mtime}},
		saved: map[string]IndexedTranscript{},
	}
	rep, err := IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}}.Run(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 4 || rep.Indexed != 2 || rep.SkippedLines != 2 || len(rep.Errors) != 2 {
		t.Errorf("report = %+v", rep)
	}
	if len(logs.read) != 3 || logs.read[0] != "grown" {
		t.Errorf("read %v, want grown, new, broken", logs.read)
	}
	c := store.saved["grown"].Costs
	if len(c) != 3 || c[0].Cost.USD() != 3 || !c[0].Priced || c[1].Cost.USD() != 0.5 || c[2].Priced {
		t.Errorf("costs = %+v", c)
	}
	if s := store.saved["new"].Signals; len(s) != 2 || !s[1].Repeated {
		t.Errorf("signals = %+v", s)
	}
}
