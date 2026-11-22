package web

import (
	"sync"
	"time"
)

// Sync brings the index up to date and calls progress with the number of files indexed so far.
// The note it returns, if any, says what was skipped.
type Sync func(progress func(indexed int)) (note string, err error)

// Sync states, as the page reads them.
const (
	syncDisabled = "disabled" // --no-sync: the index is read as it is
	syncRunning  = "running"
	syncDone     = "done"
	syncFailed   = "failed"
)

// syncStatus is what the page learns about the indexing.
type syncStatus struct {
	State      string `json:"state"`
	Indexed    int    `json:"indexed"`
	Note       string `json:"note,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"startedAt"`  // Unix milliseconds, 0 when it never started
	FinishedAt int64  `json:"finishedAt"` // Unix milliseconds, 0 while running
}

// syncer runs the indexing once, in the background, and lets handlers read its progress.
type syncer struct {
	mu     sync.Mutex
	status syncStatus
	now    func() time.Time
	log    logf
}

func newSyncer(enabled bool, now func() time.Time, log logf) *syncer {
	s := &syncer{now: now, log: log, status: syncStatus{State: syncDisabled}}
	if enabled {
		s.status.State = syncRunning
	}
	return s
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
// since the message may name files of the user's home.
func (s *syncer) run(sync Sync) {
	s.update(func(st *syncStatus) { st.StartedAt = s.now().UnixMilli() })
	note, err := sync(func(n int) { s.update(func(st *syncStatus) { st.Indexed = n }) })
	s.update(func(st *syncStatus) {
		st.FinishedAt = s.now().UnixMilli()
		st.Note = note
		st.State = syncDone
		if err != nil {
			s.log("indexing failed: %v", err)
			st.State, st.Error = syncFailed, "indexing failed: see the server log"
		}
	})
}
