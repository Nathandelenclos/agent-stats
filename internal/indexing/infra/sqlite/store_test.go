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
	if err := s.Save(f, transcript(t, "s1", at)); err != nil {
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
		if err := s.Save(application.LogFile{Path: path, Harness: kernel.Claude}, transcript(t, "s1", at)); err != nil {
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
	if err := s.Save(application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
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

// oldSchema is the index as it was before tool calls recorded the skill they load.
const oldSchema = `
CREATE TABLE files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL);
CREATE TABLE sessions (id TEXT PRIMARY KEY, file TEXT NOT NULL, harness TEXT NOT NULL, parent_id TEXT, agent_type TEXT NOT NULL,
	title TEXT, folder TEXT, user TEXT NOT NULL, started_at INTEGER, ended_at INTEGER);
CREATE TABLE tool_calls (file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
	name TEXT NOT NULL, server TEXT NOT NULL, args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL,
	is_error INTEGER NOT NULL, duration_ms REAL, PRIMARY KEY (session_id, key));
CREATE VIEW usage_tool_calls AS SELECT t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id;
INSERT INTO files VALUES ('/logs/with-skill.jsonl', 'claude', 1, 1), ('/logs/without.jsonl', 'claude', 1, 1);
INSERT INTO sessions VALUES ('claude:a', '/logs/with-skill.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1),
	('claude:b', '/logs/without.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1);
INSERT INTO tool_calls VALUES ('/logs/with-skill.jsonl', 't1', 'claude:a', 1, 'Skill', '', 0, 0, 0, NULL),
	('/logs/without.jsonl', 't2', 'claude:b', 1, 'Read', '', 0, 0, 0, NULL);
`

func TestOldIndexGainsTheSkillColumnAndReadsItsSkillFilesAgain(t *testing.T) {
	// Given an index written before tool calls recorded their skill, holding one log with a Skill call and one without
	path := filepath.Join(t.TempDir(), "index.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// When it is opened
	s, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Then the published view offers the skill, and only the log with a Skill call is forgotten, to be indexed again
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM usage_tool_calls WHERE skill = ''`).Scan(&n); err != nil || n != 2 {
		t.Errorf("view rows without a skill = %d, %v", n, err)
	}
	known, err := s.IndexedFiles()
	if err != nil || len(known) != 1 || known["/logs/without.jsonl"].Path == "" {
		t.Errorf("indexed files after migration = %+v %v", known, err)
	}
	// And opening it again changes nothing
	s2, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

func TestSaveRecordsTheSkillOfASkillCall(t *testing.T) {
	// Given a transcript whose tool call loads a skill
	s := open(t)
	at := time.UnixMilli(1767261600000)
	tr := transcript(t, "s1", at)
	tr.Tools[0].Name, tr.Tools[0].Server, tr.Tools[0].Skill = "Skill", "", "review"

	// When it is saved, then the published view shows the skill
	if err := s.Save(application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, tr); err != nil {
		t.Fatal(err)
	}
	var skill string
	if err := s.db.QueryRow(`SELECT skill FROM usage_tool_calls WHERE name = 'Skill'`).Scan(&skill); err != nil || skill != "review" {
		t.Errorf("skill = %q, %v", skill, err)
	}
}
