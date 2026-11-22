package application

import (
	"context"
	"fmt"
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
	// Workers is how many logs are read at once; zero or less means one. The composition root decides how many
	// the machine affords: the use case does not look at the machine.
	Workers int
	// ReadAhead is how many bytes of logs may be read ahead of the writer; zero means 512 MiB.
	ReadAhead int64
	// SaveBatch is how many logs are saved at once, in one transaction; zero means 64.
	SaveBatch int
}

// IndexReport says what an IndexSessions run did.
type IndexReport struct {
	Scanned, Indexed int
	SkippedLines     int // unusable lines in the logs indexed by this run
	Errors           []string
}

// Run indexes every new or changed log. progress, when set, is called every 50 indexed files.
func (uc IndexSessions) Run(progress func(IndexReport)) (IndexReport, error) {
	return uc.RunContext(context.Background(), progress)
}

// RunContext is Run, stopping when ctx is done: it returns the report so far and the context's error, once the
// batch being saved is saved. What was read and not saved yet is dropped; the next run reads it again.
func (uc IndexSessions) RunContext(ctx context.Context, progress func(IndexReport)) (IndexReport, error) {
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
	batch := uc.SaveBatch
	if batch <= 0 {
		batch = defaultSaveBatch
	}
	ahead := newWindow(ctx, uc.readAheadBytes())
	defer ahead.stop()
	take := uc.readAhead(ctx, changed, ahead)

	var pending []readLog // in the order of the list: failures stay in it, so errors are reported in that order
	flush := func() {
		var toSave []IndexedLog
		for _, p := range pending {
			if p.err == nil {
				toSave = append(toSave, IndexedLog{File: p.file, Transcript: p.indexed})
			}
		}
		var saved []error
		if len(toSave) > 0 {
			saved = uc.Store.Save(toSave)
		}
		for _, p := range pending {
			ahead.leave(p.file.weight()) // only now is the transcript gone from memory
			err := p.err
			if err == nil {
				err, saved = saved[0], saved[1:]
			}
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", p.file.Path, err))
				continue
			}
			rep.Indexed++
			rep.SkippedLines += p.indexed.Transcript.SkippedLines
			if progress != nil && rep.Indexed%50 == 0 {
				progress(rep)
			}
		}
		pending = pending[:0]
	}
	for range changed {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		r, ok := take(flush)
		if !ok {
			return rep, ctx.Err()
		}
		// A reader that waits for room while the writer waits for a batch to fill would wait for ever:
		// save what is there, however few logs that are.
		if pending = append(pending, r); len(pending) == batch || ahead.hasWaiter() {
			flush()
		}
	}
	flush()
	return rep, nil
}

const defaultSaveBatch = 64

// readLog is a log read: its indexed transcript, or why that failed.
type readLog struct {
	file    LogFile
	indexed IndexedTranscript
	err     error
}

// readAheadBytes bounds the logs read but not saved yet: a transcript weighs a fraction of the log it comes
// from. It lets the workers pass the largest logs of the list (tens of megabytes, seconds to parse) instead of
// waiting for them, which a window counted in files did not.
const readAheadBytes = 512 << 20

// window counts the bytes of the logs being read, waiting to be taken and waiting to be saved: a log leaves it
// when its batch is saved, not before, or a batch of 64 transcripts would sit in memory outside the count.
// Something is always let in when nothing is held, so a log over the limit is still read.
type window struct {
	mu          sync.Mutex
	freed       *sync.Cond
	ctx         context.Context
	held, limit int64
	files       int
	waiting     bool          // enter is waiting for room
	stalled     chan struct{} // told, without blocking, each time enter starts waiting
	cancelWake  func() bool   // stops the wake-up that cancels the waiting
}

func newWindow(ctx context.Context, limit int64) *window {
	w := &window{ctx: ctx, limit: limit, stalled: make(chan struct{}, 1)}
	w.freed = sync.NewCond(&w.mu)
	w.cancelWake = context.AfterFunc(ctx, func() {
		w.mu.Lock() // so that a waiter that has just tested the context is waiting when the broadcast comes
		w.mu.Unlock()
		w.freed.Broadcast()
	})
	return w
}

func (w *window) stop() { w.cancelWake() }

// enter waits for room for size bytes; it is false when the context was cancelled first.
func (w *window) enter(size int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.files > 0 && w.held+size > w.limit {
		if w.ctx.Err() != nil {
			return false
		}
		w.waiting = true
		select {
		case w.stalled <- struct{}{}:
		default: // already told
		}
		w.freed.Wait()
	}
	w.waiting = false
	if w.ctx.Err() != nil {
		return false
	}
	w.held += size
	w.files++
	return true
}

func (w *window) leave(size int64) {
	w.mu.Lock()
	w.held -= size
	w.files--
	w.mu.Unlock()
	w.freed.Broadcast()
}

// hasWaiter reports whether a reader is waiting for the window to free up.
func (w *window) hasWaiter() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.waiting
}

// readAhead starts reading, parsing and pricing files in the background and returns take, which hands over
// the results one at a time in the order of files; take must be called once per file, and returns false when ctx
// is done. While take waits for a result it calls flush if the readers wait for room. The workers run ahead of
// the caller within the window.
func (uc IndexSessions) readAhead(ctx context.Context, files []LogFile, ahead *window) (take func(flush func()) (readLog, bool)) {
	workers := min(max(uc.Workers, 1), len(files))
	results := make([]chan readLog, len(files)) // one per file, so results come out in order
	for i := range results {
		results[i] = make(chan readLog, 1)
	}
	next := make(chan int)
	go func() {
		defer close(next)
		for i, f := range files {
			if !ahead.enter(f.weight()) {
				return
			}
			select {
			case next <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	for range workers {
		go func() {
			for i := range next {
				r := readLog{file: files[i]}
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
	return func(flush func()) (readLog, bool) {
		for {
			select {
			case r := <-results[i]:
				i++
				return r, true
			case <-ahead.stalled:
				flush() // a no-op when nothing waits to be saved
			case <-ctx.Done():
				return readLog{}, false
			}
		}
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
