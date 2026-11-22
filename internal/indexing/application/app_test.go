package application

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
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
	onRead   func(path string) // runs inside Read, before it answers

	mu   sync.Mutex
	read []string
}

func (l *fakeLogs) List() ([]LogFile, []error, error) { return l.files, l.problems, nil }
func (l *fakeLogs) Read(f LogFile) (domain.Transcript, error) {
	l.mu.Lock()
	l.read = append(l.read, f.Path)
	l.mu.Unlock()
	if l.onRead != nil {
		l.onRead(f.Path)
	}
	return l.reads[f.Path], l.readErr[f.Path]
}

type fakeStore struct {
	known  map[string]LogFile
	saved  map[string]IndexedTranscript
	order  []string     // paths in the order they were saved
	onSave func(string) // runs inside Save, once per log
	failOn string       // the log whose save fails
	sizes  []int        // the size of each batch
}

func (s *fakeStore) IndexedFiles() (map[string]LogFile, error) { return s.known, nil }
func (s *fakeStore) Save(batch []IndexedLog) []error {
	s.sizes = append(s.sizes, len(batch))
	errs := make([]error, len(batch))
	for i, l := range batch {
		if s.onSave != nil {
			s.onSave(l.File.Path)
		}
		if l.File.Path == s.failOn {
			errs[i] = errors.New("disk full")
			continue
		}
		s.saved[l.File.Path] = l.Transcript
		s.order = append(s.order, l.File.Path)
	}
	return errs
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
	if slices.Sort(logs.read); !slices.Equal(logs.read, []string{"broken", "grown", "new"}) {
		t.Errorf("read %v, want broken, grown, new", logs.read)
	}
	c := store.saved["grown"].Costs
	if len(c) != 3 || c[0].Cost.USD() != 3 || !c[0].Priced || c[1].Cost.USD() != 0.5 || c[2].Priced {
		t.Errorf("costs = %+v", c)
	}
	if s := store.saved["new"].Signals; len(s) != 2 || !s[1].Repeated {
		t.Errorf("signals = %+v", s)
	}
}

func manyLogs(n int) *fakeLogs {
	logs := &fakeLogs{reads: map[string]domain.Transcript{}}
	for i := range n {
		path := fmt.Sprintf("log-%02d", i)
		logs.files = append(logs.files, LogFile{Path: path, Size: 10})
		logs.reads[path] = domain.Transcript{SkippedLines: 1}
	}
	return logs
}

func TestIndexSessionsSavesInTheOrderOfTheListWhateverTheOrderOfReading(t *testing.T) {
	// Given the first log is slow to read and the others are fast, so reads complete out of order
	logs := manyLogs(20)
	logs.readErr = map[string]error{"log-03": errors.New("unexpected EOF")}
	release := make(chan struct{})
	var fastDone atomic.Int32
	logs.onRead = func(path string) {
		if path == "log-00" {
			<-release
			return
		}
		if fastDone.Add(1) == 5 { // later logs were read while it waits (the window holds 8 logs): it may finish now
			close(release)
		}
	}
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}}

	// When they are indexed by several workers
	rep, err := IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}, Workers: 4, ReadAhead: 80, SaveBatch: 1}.Run(nil)

	// Then they are saved and reported in the order of the list, as a sequential run would
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, f := range logs.files {
		if f.Path != "log-03" {
			want = append(want, f.Path)
		}
	}
	if !slices.Equal(store.order, want) {
		t.Errorf("saved %v, want %v", store.order, want)
	}
	if rep.Scanned != 20 || rep.Indexed != 19 || rep.SkippedLines != 19 || len(rep.Errors) != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestIndexSessionsReadsOnlyAFewBytesOfLogsAheadOfTheWriter(t *testing.T) {
	// Given a store that is slow to save
	logs := manyLogs(40)
	saving := make(chan struct{})
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}}
	var readsWhenFirstSaved int
	store.onSave = func(path string) {
		if path == "log-00" {
			time.Sleep(50 * time.Millisecond) // let the workers run as far ahead as they may
			logs.mu.Lock()
			readsWhenFirstSaved = len(logs.read)
			logs.mu.Unlock()
			close(saving)
		}
	}

	// When 3 workers index 40 logs of 10 bytes with room for 50 bytes
	if _, err := (IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}, Workers: 3, ReadAhead: 50, SaveBatch: 1}).Run(nil); err != nil {
		t.Fatal(err)
	}

	// Then no more than 5 logs were read before the first one was saved: they fit in the window,
	// and the first stays in it until it is saved
	<-saving
	if readsWhenFirstSaved > 5 {
		t.Errorf("%d logs read before the first was saved, want at most 5", readsWhenFirstSaved)
	}
	if len(store.saved) != 40 {
		t.Errorf("saved %d logs, want 40", len(store.saved))
	}
}

func TestIndexSessionsSavesLogsInBatchesAndReportsTheOnesThatFailed(t *testing.T) {
	// Given 150 logs, one of which the store cannot save
	logs := manyLogs(150)
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}, failOn: "log-70"}

	// When they are indexed with the default batch
	rep, err := IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}}.Run(nil)

	// Then they were saved in transactions of 64, in order, the failure is reported and the others are kept
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.sizes, []int{64, 64, 22}) {
		t.Errorf("batches of %v, want 64, 64, 22", store.sizes)
	}
	var want []string
	for _, f := range logs.files {
		if f.Path != "log-70" {
			want = append(want, f.Path)
		}
	}
	if !slices.Equal(store.order, want) {
		t.Errorf("saved %d logs, not the 149 of the list in order", len(store.order))
	}
	if rep.Indexed != 149 || len(rep.Errors) != 1 || rep.Errors[0] != "log-70: disk full" {
		t.Errorf("report = %+v", rep)
	}
}

func TestProgressIsReportedEveryFiftyLogsWhateverTheBatch(t *testing.T) {
	logs := manyLogs(120)
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}}
	var seen []int
	_, err := IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}}.Run(func(r IndexReport) { seen = append(seen, r.Indexed) })
	if err != nil || !slices.Equal(seen, []int{50, 100}) {
		t.Errorf("progress at %v, %v; want 50, 100", seen, err)
	}
}

// done fails the test when run does not return in time: a window that waits for a batch the writer waits
// for the window to fill would hang forever.
func done(t *testing.T, run func()) {
	t.Helper()
	finished := make(chan struct{})
	go func() { run(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the run does not end")
	}
}

func TestIndexSessionsKeepsALogInTheWindowUntilItsBatchIsSaved(t *testing.T) {
	// Given 40 logs of 10 bytes, room for 50, and batches of 64: far more than fit in the window
	logs := manyLogs(40)
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}}
	var readsWhenFirstSaved int
	store.onSave = func(path string) {
		if path == "log-00" {
			time.Sleep(50 * time.Millisecond) // let the workers run as far ahead as they may
			logs.mu.Lock()
			readsWhenFirstSaved = len(logs.read)
			logs.mu.Unlock()
		}
	}

	// When 3 workers index them
	var err error
	done(t, func() {
		_, err = IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}, Workers: 3, ReadAhead: 50}.Run(nil)
	})

	// Then the transcripts waiting for their batch counted in the window: at most 5 logs were read before
	// the first was saved (the batch is saved early, rather than hold the readers back for good), and all were saved
	if err != nil {
		t.Fatal(err)
	}
	if readsWhenFirstSaved > 5 {
		t.Errorf("%d logs read before the first was saved, want at most 5", readsWhenFirstSaved)
	}
	if len(store.saved) != 40 {
		t.Errorf("saved %d logs, want 40", len(store.saved))
	}
	if len(store.sizes) < 8 {
		t.Errorf("batches of %v: the window should have forced batches of at most 5", store.sizes)
	}
}

func TestIndexSessionsWeighsACompressedLogByWhatItHolds(t *testing.T) {
	// Given 6 logs of 1 byte on disk that hold 40 bytes of log once decompressed, with room for 50
	logs := manyLogs(6)
	for i := range logs.files {
		logs.files[i].Size, logs.files[i].Weight = 1, 40
	}
	var reading, most atomic.Int32
	logs.onRead = func(string) {
		n := reading.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		reading.Add(-1)
	}
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}}

	// When 4 workers index them
	done(t, func() {
		IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}, Workers: 4, ReadAhead: 50}.Run(nil)
	})

	// Then they were read one at a time, as the 40 bytes each holds allow, though 6 bytes of files would fit
	if most.Load() != 1 || len(store.saved) != 6 {
		t.Errorf("%d logs read at once, %d saved; want 1 and 6", most.Load(), len(store.saved))
	}
}

func TestIndexSessionsReportsErrorsInTheOrderOfTheList(t *testing.T) {
	// Given logs of which the first and the third cannot be read, the second cannot be saved and one is fine
	logs := manyLogs(4)
	logs.problems = []error{errors.New("unreadable: permission denied")}
	logs.readErr = map[string]error{"log-00": errors.New("unexpected EOF"), "log-02": errors.New("bad zstd")}
	store := &fakeStore{known: map[string]LogFile{}, saved: map[string]IndexedTranscript{}, failOn: "log-01"}

	// When they are indexed in one batch
	rep, err := IndexSessions{Logs: logs, Store: store, Prices: flatPrices{}, Workers: 3}.Run(nil)

	// Then the errors come in the order of the files, as in a run that handles them one by one
	want := []string{"unreadable: permission denied", "log-00: unexpected EOF", "log-01: disk full", "log-02: bad zstd"}
	if err != nil || !slices.Equal(rep.Errors, want) || rep.Indexed != 1 {
		t.Errorf("report = %+v, %v; want errors %v", rep, err, want)
	}
}
