package sqlite

import (
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

// subagentOf returns the transcript of a subagent of parent (an indexed session or not).
func subagentOf(t *testing.T, parent, name string, at time.Time) application.IndexedTranscript {
	t.Helper()
	p, err := domain.NewSessionID(kernel.Claude, parent)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Subagent(name)
	if err != nil {
		t.Fatal(err)
	}
	tr := transcript(t, "unused", at)
	tr.Session.ID, tr.Session.ParentID, tr.Session.AgentType = id, p, domain.Subagent
	return tr
}

// listedUnder reads how the index groups sessions for a listing: each session, with the session it is listed under.
func listedUnder(t *testing.T, s *SQLiteIndex) map[string]string {
	t.Helper()
	listed := map[string]string{}
	rows, err := s.db.Query(`SELECT id, root_id FROM session_overview`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			t.Fatal(err)
		}
		listed[id] = root
	}
	return listed
}

// requestsListedUnder counts the requests a listing adds up for a session: its own and its subagents'.
func requestsListedUnder(t *testing.T, s *SQLiteIndex, session string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_requests WHERE root_id = ?`, session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestASubagentIsListedUnderItsSession(t *testing.T) {
	// Given a session with a subagent
	s := open(t)
	at := time.UnixMilli(1767261600000)
	for _, f := range []struct {
		path string
		tr   application.IndexedTranscript
	}{
		{"/logs/main.jsonl", transcript(t, "main", at)},
		{"/logs/child.jsonl", subagentOf(t, "main", "child", at)},
	} {
		if err := s.Save(application.LogFile{Path: f.path, Harness: kernel.Claude}, f.tr); err != nil {
			t.Fatal(err)
		}
	}

	// When the sessions are listed
	listed := listedUnder(t, s)

	// Then the subagent belongs to its session, and its requests count for it
	if listed["claude:main/child"] != "claude:main" || listed["claude:main"] != "claude:main" {
		t.Errorf("listed under = %v", listed)
	}
	if n := requestsListedUnder(t, s, "claude:main"); n != 2 {
		t.Errorf("requests of the session = %d, want its own and its subagent's", n)
	}
}

func TestASubagentWhoseSessionWasNeverIndexedIsListedOnItsOwn(t *testing.T) {
	// Given a subagent whose session log was never indexed
	s := open(t)
	if err := s.Save(application.LogFile{Path: "/logs/orphan.jsonl", Harness: kernel.Claude}, subagentOf(t, "gone", "orphan", time.UnixMilli(1767261600000))); err != nil {
		t.Fatal(err)
	}

	// When the sessions are listed, then it stands for itself
	if listed := listedUnder(t, s); listed["claude:gone/orphan"] != "claude:gone/orphan" {
		t.Errorf("listed under = %v", listed)
	}
}

func saveAll(t *testing.T, s *SQLiteIndex, logs map[string]application.IndexedTranscript) {
	t.Helper()
	for path, tr := range logs {
		if err := s.Save(application.LogFile{Path: path, Harness: kernel.Claude}, tr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedSubagentsAreListedUnderTheTopSession(t *testing.T) {
	// Given a session whose subagent started a subagent of its own
	s := open(t)
	at := time.UnixMilli(1767261600000)
	saveAll(t, s, map[string]application.IndexedTranscript{
		"/logs/a.jsonl": transcript(t, "a", at),
		"/logs/b.jsonl": subagentOf(t, "a", "b", at),
		"/logs/c.jsonl": subagentOf(t, "a/b", "c", at),
	})

	// When the sessions are listed
	listed := listedUnder(t, s)

	// Then both subagents belong to the top session, and the requests of the three count for it
	if listed["claude:a"] != "claude:a" || listed["claude:a/b"] != "claude:a" || listed["claude:a/b/c"] != "claude:a" {
		t.Errorf("listed under = %v", listed)
	}
	if n := requestsListedUnder(t, s, "claude:a"); n != 3 {
		t.Errorf("requests of the session = %d, want its own and both subagents'", n)
	}
}

func TestPromptsKeepTheOrderTheyWereTypedInAndTheirSignals(t *testing.T) {
	// Given a session whose prompts were typed in a different order than their keys sort
	s := open(t)
	at := time.UnixMilli(1767261600000)
	tr := transcript(t, "s1", at)
	tr.Prompts = []domain.Prompt{{Key: "z", At: at, Text: "first"}, {Key: "a", At: at, Text: hostile}}
	tr.Signals = []domain.Signals{{Chars: 5, Words: 1}, {Chars: 9, Words: 2, Yelling: 3, Profanity: 1, Anguish: 2, Negation: 4, Blame: 5, Repeated: true}}
	if err := s.Save(application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, tr); err != nil {
		t.Fatal(err)
	}

	// When the prompts of the session are read
	rows, err := s.db.Query(`SELECT text, chars, yelling, profanity, anguish, negation, repetition, blame
		FROM session_prompts WHERE session_id = 'claude:s1' ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	var last [7]int
	for rows.Next() {
		var text string
		if err := rows.Scan(&text, &last[0], &last[1], &last[2], &last[3], &last[4], &last[5], &last[6]); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}

	// Then they come back in the order they were typed in, each with its signals
	if len(texts) != 2 || texts[0] != "first" || texts[1] != hostile {
		t.Errorf("prompts = %q", texts)
	}
	if last != [7]int{9, 3, 1, 2, 4, 1, 5} {
		t.Errorf("signals of the second prompt = %v", last)
	}
}
