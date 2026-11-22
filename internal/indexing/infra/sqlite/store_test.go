package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func open(t *testing.T) *SQLiteIndex {
	t.Helper()
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db"), User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// save saves one log on its own.
func save(s *SQLiteIndex, f application.LogFile, t application.IndexedTranscript) error {
	return s.Save([]application.IndexedLog{{File: f, Transcript: t}})[0]
}

// hostile carries SQL metacharacters in every text value that reaches the database.
const hostile = `o'brian"; DROP TABLE requests; --`

func transcript(t *testing.T, native string, at time.Time) application.IndexedTranscript {
	t.Helper()
	sessionID, err := domain.NewSessionID(kernel.Claude, native)
	if err != nil {
		t.Fatal(err)
	}
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1000, Output: 100})
	cost, _ := kernel.NewCost(2)
	return application.IndexedTranscript{
		Transcript: domain.Transcript{
			Session: domain.Session{ID: sessionID, Harness: kernel.Claude, AgentType: domain.MainAgent, Folder: hostile, Start: at, End: at},
			Requests: []domain.Request{{Key: "m1", At: at, Model: hostile, Provider: "anthropic",
				Tokens: tokens, Duration: domain.NewDuration(1500 * time.Millisecond)}},
			Tools:   []domain.ToolCall{{Key: "t1", At: at, Name: hostile, Server: "grace", IsError: true}},
			Prompts: []domain.Prompt{{Key: "p1", At: at, Text: hostile}},
		},
		Costs:   []application.PricedCost{{Cost: cost, Priced: true}},
		Signals: []domain.Signals{{Chars: 3, Repeated: true}},
	}
}

func TestSaveKeepsValuesAsData(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	f := application.LogFile{Path: "/logs/" + hostile + ".jsonl", Harness: kernel.Claude, Size: 10, ModTime: time.Unix(0, 1767261600123456789)}
	if err := save(s, f, transcript(t, "s1", at)); err != nil {
		t.Fatal(err)
	}
	var model, file string
	var input int64
	var cost, duration float64
	if err := s.db.QueryRow(`SELECT model, file, input, cost, duration_ms FROM requests`).Scan(&model, &file, &input, &cost, &duration); err != nil ||
		model != hostile || file != f.Path || input != 1000 || cost != 2 || duration != 1500 {
		t.Errorf("request row = %q %q %d %v %v, %v", model, file, input, cost, duration, err)
	}
	var tool, folder, text string
	var repetition int
	if err := s.db.QueryRow(`SELECT t.name, s.folder, p.text, p.repetition FROM tool_calls t, sessions s, prompts p`).Scan(&tool, &folder, &text, &repetition); err != nil ||
		tool != hostile || folder != hostile || text != hostile || repetition != 1 {
		t.Errorf("rows = %q %q %q %d, %v", tool, folder, text, repetition, err)
	}
	known, err := s.IndexedFiles()
	if err != nil || len(known) != 1 || !known[f.Path].ModTime.Equal(f.ModTime) || known[f.Path].Size != 10 {
		t.Errorf("indexed files = %+v %v", known, err)
	}
}

func TestMovedLogIsNotCountedTwice(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	for _, path := range []string{"/logs/sessions/s1.jsonl", "/logs/archived/s1.jsonl"} {
		if err := save(s, application.LogFile{Path: path, Harness: kernel.Claude}, transcript(t, "s1", at)); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&n); err != nil || n != 1 {
		t.Errorf("after move: %d requests, %v", n, err)
	}
}

func TestIndexIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "agent-stats")
	path := filepath.Join(dir, "index.db")
	s, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := save(s, application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want no group or other access", p, info.Mode().Perm())
		}
	}

	// An index created by an older version is restricted when opened again.
	s.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("reopened index is %v", info.Mode().Perm())
	}
}

func TestSaveAppliesABatchInOrderAndUndoesOnlyTheLogThatFails(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	// Given a database that refuses the session "claude:bad"
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON sessions WHEN NEW.id = 'claude:bad' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	log := func(native string) application.IndexedLog {
		return application.IndexedLog{File: application.LogFile{Path: "/logs/" + native + ".jsonl", Harness: kernel.Claude, Size: 1}, Transcript: transcript(t, native, at)}
	}

	// When one batch holds a good log, the bad one, and a good one again
	errs := s.Save([]application.IndexedLog{log("a"), log("bad"), log("c")})

	// Then only the bad one fails and leaves nothing behind, the others are saved
	if errs[0] != nil || errs[1] == nil || errs[2] != nil {
		t.Fatalf("errors = %v", errs)
	}
	var sessions, requests, files int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM requests), (SELECT COUNT(*) FROM files)`).Scan(&sessions, &requests, &files); err != nil ||
		sessions != 2 || requests != 2 || files != 2 {
		t.Errorf("sessions %d, requests %d, files %d, %v; want 2 each", sessions, requests, files, err)
	}
}

func TestSaveOfNothingIsHarmless(t *testing.T) {
	if errs := open(t).Save(nil); len(errs) != 0 {
		t.Errorf("errors = %v", errs)
	}
}

func pageSize(t *testing.T, s *SQLiteIndex) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewIndexUsesLargePagesAndAnOlderOneKeepsItsOwn(t *testing.T) {
	// Given an index made by an older version, with 4 KiB pages
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`PRAGMA page_size = 4096; PRAGMA journal_mode = WAL; CREATE TABLE files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// When both it and a new index are opened and written to
	older, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	fresh := open(t)
	for _, s := range []*SQLiteIndex{older, fresh} {
		if err := save(s, application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
			t.Fatal(err)
		}
	}

	// Then the new one has 16 KiB pages and the old one is untouched
	if got := pageSize(t, fresh); got != 16384 {
		t.Errorf("new index page size = %d, want 16384", got)
	}
	if got := pageSize(t, older); got != 4096 {
		t.Errorf("older index page size = %d, want 4096", got)
	}
}
