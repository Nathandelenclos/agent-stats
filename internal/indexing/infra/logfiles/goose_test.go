package logfiles

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func gooseLogs(t *testing.T, script string) (*FileSessionLogs, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	buildDB(t, path, script)
	for _, source := range DefaultSources(home) {
		if source.Harness.String() == "goose" {
			return NewFileSessionLogs([]Source{source}), path
		}
	}
	t.Fatal("Goose is missing from the default sources")
	return nil, ""
}

func TestGooseReadsSessionActivityFromSQLite(t *testing.T) {
	logs, path := gooseLogs(t, "goose.sql")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := transcriptOf(t, logs, "example")
	if p.Session.ID.String() != "goose:example" || p.Session.Title != "Example" || p.Session.Folder != "/work/example" ||
		p.Session.Start.Unix() != 1769940000 || p.Session.End.Unix() != 1769940009 {
		t.Fatalf("session: %+v", p.Session)
	}
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Check the example" {
		t.Fatalf("prompts: %+v", p.Prompts)
	}
	if len(p.Requests) != 2 || p.SkippedLines != 1 {
		t.Fatalf("requests: %+v, skipped: %d", p.Requests, p.SkippedLines)
	}
	r := p.Requests[0]
	if r.Key != "answer" || r.At.UnixMilli() != 1769940002003 || r.Provider != "sample" || r.Model != "resolved" ||
		r.Tokens.Counts() != (kernel.TokenCounts{Input: 60, Output: 20, CacheRead: 30, CacheWrite: 10}) ||
		r.LoggedCost.USD() != 0.03 || r.Duration.Value() != 300*time.Millisecond || r.TTFT.Value() != 120*time.Millisecond {
		t.Fatalf("usage: %+v", r)
	}
	if r := p.Requests[1]; r.Failure != "creditsExhausted" || r.Model != "requested" || r.Duration.Known() {
		t.Fatalf("failed request: %+v", r)
	}
	if len(p.Tools) != 2 || p.Tools[0].Name != "lookup" || p.Tools[0].ArgsChars != 11 ||
		p.Tools[0].ResultChars == 0 || !p.Tools[0].IsError || p.Tools[0].Duration.Known() ||
		!p.Tools[1].IsError || p.Tools[1].ResultChars != len("unavailable") {
		t.Fatalf("tools: %+v", p.Tools)
	}
	child := transcriptOf(t, logs, "child")
	if child.Session.ParentID != p.Session.ID || child.Session.AgentType != domain.Subagent {
		t.Fatalf("child: %+v", child.Session)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Goose database changed: %v", err)
	}
}

func TestGooseOlderSQLiteKeepsActivityWithoutInventingUsage(t *testing.T) {
	logs, path := gooseLogs(t, "goose-legacy.sql")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "old.jsonl"), []byte("legacy transcript"), 0600); err != nil {
		t.Fatal(err)
	}
	if files := existingLogs(t, logs); len(files) != 1 {
		t.Fatalf("logs: %+v", files)
	}
	p := transcriptOf(t, logs, "old")
	if len(p.Prompts) != 1 || p.Session.Title != "Check an old example" ||
		len(p.Tools) != 1 || p.Tools[0].IsError || p.Tools[0].ResultChars == 0 || len(p.Requests) != 0 {
		t.Fatalf("transcript: %+v", p)
	}
}

func TestGooseReindexesAnInPlaceUsageCorrectionInWAL(t *testing.T) {
	logs, path := gooseLogs(t, "goose.sql")
	writer, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Ping(); err != nil {
		t.Fatal(err)
	}
	before := existingLogs(t, logs)["example"]
	if _, err := writer.Exec(`UPDATE messages SET metadata_json = replace(metadata_json, '0.03', '0.04') WHERE message_id = 'answer'`); err != nil {
		t.Fatal(err)
	}
	after := existingLogs(t, logs)["example"]
	if after.Revision == before.Revision || after.Path != before.Path {
		t.Fatalf("revision after WAL update: %q -> %q", before.Revision, after.Revision)
	}
	if p, err := logs.Read(after); err != nil || len(p.Requests) == 0 || p.Requests[0].LoggedCost.USD() != 0.04 {
		t.Fatalf("updated request: %+v, %v", p.Requests, err)
	}
}

func TestGooseReportsAnUnsupportedDatabase(t *testing.T) {
	logs, _ := gooseLogs(t, "messages.sql")
	files, problems, err := logs.List()
	if len(files) != 0 || len(problems) != 1 || !errors.Is(problems[0], ErrUnknownSchema) || err != nil {
		t.Fatalf("list: %+v, %v, %v", files, problems, err)
	}
}
