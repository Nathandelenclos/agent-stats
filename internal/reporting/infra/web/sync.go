package web

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sync brings the index up to date and calls progress with the number of files indexed so far. It stops when ctx is
// done. The note it returns, if any, says what was skipped.
type Sync func(ctx context.Context, progress func(indexed int)) (note string, err error)

// Sync states, as the page reads them.
const (
	syncDisabled = "disabled" // --no-sync: the index is read as it is
	syncRunning  = "running"
	syncDone     = "done"
	syncFailed   = "failed"
)

// syncStatus is what the page learns about the indexing.
type syncStatus struct {
	State       string `json:"state"`
	Indexed     int    `json:"indexed"`
	Note        string `json:"note,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   int64  `json:"startedAt"` // Unix milliseconds, 0 when it never started
	DataVersion int64  `json:"dataVersion"`
	FinishedAt  int64  `json:"finishedAt"` // Unix milliseconds, 0 while running
}

// syncer runs one indexing job at a time and lets handlers read its progress.
type syncer struct {
	mu     sync.Mutex
	jobs   sync.WaitGroup
	status syncStatus
	now    func() time.Time
	log    logf
}

func newSyncer(now func() time.Time, log logf) *syncer {
	return &syncer{now: now, log: log, status: syncStatus{State: syncDisabled}}
}

// start returns the running job, starting it if there is none. The server drains request handlers
// before it waits for jobs, so there can be no new start during the wait.
func (s *syncer) start(ctx context.Context, index Sync, automatic bool) syncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index == nil || ctx.Err() != nil || s.status.State == syncRunning {
		return s.status
	}
	s.status = syncStatus{State: syncRunning, StartedAt: s.now().UnixMilli(), DataVersion: s.status.DataVersion}
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		s.run(ctx, index, automatic)
	}()
	return s.status
}

// follow checks for log changes without overlapping the startup or manual jobs.
func (s *syncer) follow(ctx context.Context, index Sync, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.start(ctx, index, true)
		}
	}
}

func (s *syncer) snapshot() syncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *syncer) update(fn func(*syncStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.status)
}

// run indexes with sync and records its progress. The failure is logged in full; the page only learns that it failed,
// since the message may name files of the user's home. A stop asked through ctx is not a failure worth a log line.
func (s *syncer) run(ctx context.Context, sync Sync, automatic bool) {
	note, err := sync(ctx, func(n int) { s.update(func(st *syncStatus) { st.Indexed = n }) })
	s.update(func(st *syncStatus) {
		st.FinishedAt = s.now().UnixMilli()
		st.Note = note
		st.State = syncDone
		if !automatic || st.Indexed > 0 {
			st.DataVersion++
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.log("indexing failed: %v", err)
			}
			st.State, st.Error = syncFailed, "indexing failed: see the server log"
		}
	})
}
