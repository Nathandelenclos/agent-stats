package application

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
)

// IndexSessions brings the index up to date with the session logs on disk.
// A changed log is read again from the start and replaces what was indexed from it, which also covers truncation.
// What was indexed from logs a harness deleted is kept, so history survives log rotation.
//
// Logs are read, parsed and priced by several workers at once, since that is nearly all of the time a run takes,
// while a single writer saves the results in the order the logs were listed. A file that shares sessions
// with another (a Codex rollout archived and live) therefore always ends up as it did in a sequential run.
type IndexSessions struct {
	Logs   SessionLogs
	Store  TranscriptStore
	Prices PriceList
	// Workers is how many logs are read at once; zero means three quarters of GOMAXPROCS (at least one),
	// which leaves the writer, the garbage collector and an interface that is drawing a core to run on.
	Workers int
	// ReadAhead is how many bytes of logs may be read ahead of the writer; zero means 512 MiB.
	ReadAhead int64
}

// IndexReport says what an IndexSessions run did.
type IndexReport struct {
	Scanned, Indexed int
	SkippedLines     int // unusable lines in the logs indexed by this run
	Errors           []string
}

// Run indexes every new or changed log. progress, when set, is called every 50 indexed files.
func (uc IndexSessions) Run(progress func(IndexReport)) (IndexReport, error) {
	var rep IndexReport
	known, err := uc.Store.IndexedFiles()
	if err != nil {
		return rep, err
	}
	files, problems, err := uc.Logs.List()
	for _, p := range problems {
		rep.Errors = append(rep.Errors, p.Error())
	}
	if err != nil {
		return rep, err
	}
	var changed []LogFile
	for _, f := range files {
		rep.Scanned++
		if k, ok := known[f.Path]; !ok || k.Size != f.Size || !k.ModTime.Equal(f.ModTime) {
			changed = append(changed, f)
		}
	}
	take := uc.readAhead(changed)
	for _, f := range changed {
		r := take()
		if r.err == nil {
			r.err = uc.Store.Save(f, r.indexed)
		}
		if r.err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", f.Path, r.err))
			continue
		}
		rep.Indexed++
		rep.SkippedLines += r.indexed.SkippedLines
		if progress != nil && rep.Indexed%50 == 0 {
			progress(rep)
		}
	}
	return rep, nil
}

// readResult is what reading one log gave: its indexed transcript, or why that failed.
type readResult struct {
	indexed IndexedTranscript
	err     error
}

// readAheadBytes bounds the logs read but not yet saved: a transcript weighs a fraction of the log it comes
// from. It lets the workers pass the largest logs of the list (tens of megabytes, seconds to parse) instead of
// waiting for them, which a window counted in files did not.
const readAheadBytes = 512 << 20

// window counts the bytes of logs being read or waiting to be saved. Something is always let in when nothing
// is held, so a log over the limit is still read.
type window struct {
	mu          sync.Mutex
	freed       *sync.Cond
	held, limit int64
	files       int
}

func newWindow(limit int64) *window {
	w := &window{limit: limit}
	w.freed = sync.NewCond(&w.mu)
	return w
}

func (w *window) enter(size int64) {
	w.mu.Lock()
	for w.files > 0 && w.held+size > w.limit {
		w.freed.Wait()
	}
	w.held += size
	w.files++
	w.mu.Unlock()
}

func (w *window) leave(size int64) {
	w.mu.Lock()
	w.held -= size
	w.files--
	w.mu.Unlock()
	w.freed.Broadcast()
}

// readAhead starts reading, parsing and pricing files in the background and returns take, which hands over
// the results one at a time in the order of files; take must be called once per file. The workers run ahead
// of the caller within readAheadBytes.
func (uc IndexSessions) readAhead(files []LogFile) (take func() readResult) {
	workers := uc.Workers
	if workers <= 0 {
		cpus := runtime.GOMAXPROCS(0)
		workers = max(1, cpus-cpus/4)
	}
	workers = min(workers, len(files))
	results := make([]chan readResult, len(files)) // one per file, so results come out in order
	for i := range results {
		results[i] = make(chan readResult, 1)
	}
	next := make(chan int)
	ahead := newWindow(uc.readAheadBytes())
	go func() {
		defer close(next)
		for i, f := range files {
			ahead.enter(f.Size)
			next <- i
		}
	}()
	for range workers {
		go func() {
			for i := range next {
				var r readResult
				t, err := uc.Logs.Read(files[i])
				if err != nil {
					r.err = err
				} else {
					r.indexed = uc.indexed(t)
				}
				results[i] <- r
			}
		}()
	}
	i := 0
	return func() readResult {
		r := <-results[i]
		ahead.leave(files[i].Size)
		i++
		return r
	}
}

func (uc IndexSessions) readAheadBytes() int64 {
	if uc.ReadAhead > 0 {
		return uc.ReadAhead
	}
	return readAheadBytes
}

func (uc IndexSessions) indexed(t domain.Transcript) IndexedTranscript {
	costs := make([]PricedCost, len(t.Requests))
	for i, r := range t.Requests {
		listed, known := uc.Prices.Cost(r.Model, r.Tokens)
		costs[i].Cost, costs[i].Priced = r.Cost(listed, known)
	}
	return IndexedTranscript{Transcript: t, Costs: costs, Signals: domain.AnalyzeSession(t.Prompts)}
}
