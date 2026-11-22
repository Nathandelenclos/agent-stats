package logfiles

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
	reportdb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

func TestCursorIndexUsesOneSessionPerConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	user := cursorUserDir(home)
	transcriptDir := filepath.Join(home, ".cursor", "projects", "encoded-project", "agent-transcripts")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := func(file string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", "cursor", file))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Given a conversation and two copies of a child transcript.
	transcript := filepath.Join(transcriptDir, "session-a", "session-a.jsonl")
	write(transcript, fixture("session.jsonl"))
	write(filepath.Join(transcriptDir, "session-a", "subagents", "child-a.jsonl"), `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"fixture.txt"}}]}}`)
	write(filepath.Join(transcriptDir, "child-a", "child-a.jsonl"), fixture("session.jsonl"))
	write(filepath.Join(transcriptDir, "standalone", "standalone.jsonl"), `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Friday, Oct 30, 2026, 10:02 AM (UTC+2)</timestamp>\nStandalone."}]}}`)
	write(filepath.Join(transcriptDir, "unrelated", "other.jsonl"), fixture("session.jsonl"))

	indexPath := filepath.Join(t.TempDir(), "index.db")
	index, err := sqlite.Open(sqlite.Options{Path: indexPath, User: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	// When the default sources feed the index.
	uc := application.IndexSessions{Logs: NewFileSessionLogs(DefaultSources(home)), Store: index}
	sync := func(want, skipped int) {
		t.Helper()
		rep, err := uc.Run(nil)
		if err != nil || len(rep.Errors) != 0 || rep.Indexed != want || rep.SkippedLines != skipped {
			t.Fatalf("sync = %+v, %v, want %d indexed and %d invalid lines", rep, err, want, skipped)
		}
	}
	sync(3, 1)

	// Composer metadata can arrive later, enriching the same session and adding a history-only session.
	dbPath := filepath.Join(user, "globalStorage", "state.vscdb")
	write(dbPath, "")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(fixture("composer.sql")); err != nil {
		t.Fatal(err)
	}
	sync(2, 1)
	sync(0, 0)

	// Then the child is linked once, only explicit prompts and invocations count, and usage stays empty.
	usage, err := reportdb.OpenReadOnly(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	ctx := context.Background()
	id, err := report.NewSessionID("cursor:session-a")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := usage.Sessions().Session(ctx, id, kernel.Project{})
	if err != nil {
		t.Fatal(err)
	}
	tools := make([]string, 0, len(detail.Tools))
	for _, tool := range detail.Tools {
		tools = append(tools, tool.Name)
		if tool.Calls != 1 || tool.ResultChars != 0 || tool.AvgDuration != 0 || tool.Errors != 0 || tool.ArgsChars == 0 {
			t.Fatalf("unexpected tool usage: %+v", tool)
		}
	}
	slices.Sort(tools)
	if detail.PromptCount != 2 || len(detail.Prompts) != 2 || detail.Prompts[0].Text != "List the fixtures." ||
		detail.SubagentCount != 1 || detail.Requests != 0 || detail.Cost != 0 || !slices.Equal(tools, []string{"Glob", "Read"}) {
		t.Fatalf("unexpected conversation: %+v", detail)
	}

	// The workspace may appear after the session was first seen, and re-association refreshes only that session.
	folder := filepath.Join(home, "neutral project")
	uriPath := filepath.ToSlash(folder)
	if filepath.VolumeName(folder) != "" {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String()
	write(filepath.Join(user, "workspaceStorage", "workspace-a", "workspace.json"), `{"folder":"`+uri+`"}`)
	sync(1, 1)
	detail, err = usage.Sessions().Session(ctx, id, kernel.Project{})
	if err != nil || detail.Folder != folder {
		t.Fatalf("workspace = %q, err = %v", detail.Folder, err)
	}

	sessions, err := usage.Sessions().Sessions(ctx, report.Filter{Harness: kernel.Cursor, Since: time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("recent sessions = %+v, err = %v", sessions, err)
	}
}
