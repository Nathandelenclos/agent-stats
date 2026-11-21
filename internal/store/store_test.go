package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/app"
	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

func open(t *testing.T) *SQLiteIndex {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"), "me@host")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// hostile carries SQL metacharacters in every text value that reaches the database.
const hostile = `o'brian"; DROP TABLE requests; --`

func transcript(sessionID string, at time.Time) app.IndexedTranscript {
	return app.IndexedTranscript{
		Transcript: usage.Transcript{
			Session: usage.Session{ID: sessionID, Harness: usage.Claude, AgentType: usage.MainAgent, Folder: hostile, Start: at, End: at},
			Requests: []usage.Request{{Key: "m1", At: at, Model: hostile, Provider: "anthropic",
				Tokens: usage.Tokens{Input: 1000, Output: 100}, Duration: 1500 * time.Millisecond}},
			Tools:   []usage.ToolCall{{Key: "t1", At: at, Name: hostile, Server: "grace", IsError: true}},
			Prompts: []usage.Prompt{{Key: "p1", At: at, Text: hostile}},
		},
		Costs:   []app.PricedCost{{USD: 2, Priced: true}},
		Signals: []usage.Signals{{Chars: 3, Repeated: true}},
	}
}

func TestSaveKeepsValuesAsData(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	f := app.LogFile{Path: "/logs/" + hostile + ".jsonl", Harness: usage.Claude, Size: 10, ModTime: time.Unix(0, 1767261600123456789)}
	if err := s.Save(f, transcript("claude:s1", at)); err != nil {
		t.Fatal(err)
	}

	u, err := s.Total(app.Filter{})
	if err != nil || u.Requests != 1 || u.Cost != 2 || u.Input != 1000 || u.AvgDurationMs != 1500 || u.FirstTS != 1767261600000 {
		t.Fatalf("total = %+v %v", u, err)
	}
	for _, d := range []app.Dimension{app.ByHarness, app.ByModel, app.ByFolder, app.ByAgentType} {
		if g, err := s.UsageBy(app.Filter{}, d); err != nil || len(g) != 1 || g[0].Requests != 1 {
			t.Errorf("dimension %d: %+v %v", d, g, err)
		}
	}
	if g, _ := s.UsageBy(app.Filter{}, app.ByModel); g[0].Group != hostile {
		t.Errorf("model group = %q", g[0].Group)
	}
	if tools, err := s.Tools(app.Filter{}); err != nil || len(tools) != 1 || tools[0].Name != hostile || tools[0].Errors != 1 {
		t.Errorf("tools = %+v %v", tools, err)
	}
	var text string
	var repetition int
	if err := s.db.QueryRow(`SELECT text, repetition FROM prompts`).Scan(&text, &repetition); err != nil || text != hostile || repetition != 1 {
		t.Errorf("prompt = %q %d %v", text, repetition, err)
	}

	// A hostile harness filter is bound as a value: it matches nothing and breaks nothing.
	if u, err := s.Total(app.Filter{Harness: usage.Harness(hostile)}); err != nil || u.Requests != 0 {
		t.Errorf("hostile harness filter = %+v %v", u, err)
	}
	if u, _ := s.Total(app.Filter{Harness: usage.Claude, Since: at.Add(time.Millisecond)}); u.Requests != 0 {
		t.Errorf("since filter kept an older request: %+v", u)
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
		if err := s.Save(app.LogFile{Path: path, Harness: usage.Claude}, transcript("claude:s1", at)); err != nil {
			t.Fatal(err)
		}
	}
	if u, err := s.Total(app.Filter{}); err != nil || u.Requests != 1 {
		t.Errorf("after move: %+v %v", u, err)
	}
}

func TestUsageByRefusesUnknownDimension(t *testing.T) {
	if _, err := open(t).UsageBy(app.Filter{}, app.Dimension(99)); err == nil {
		t.Error("unknown dimension accepted")
	}
}

func TestIndexIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "agent-stats")
	path := filepath.Join(dir, "index.db")
	s, err := Open(path, "me@host")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Save(app.LogFile{Path: "/l.jsonl", Harness: usage.Claude}, transcript("claude:s1", time.UnixMilli(1))); err != nil {
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
	s2, err := Open(path, "me@host")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("reopened index is %v", info.Mode().Perm())
	}
}
