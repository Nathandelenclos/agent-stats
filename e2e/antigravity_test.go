package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestAntigravityMergesAppAndIDEWithCLITranscript(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000044"
	home := useIndexHome(t)
	roots := filepath.Join(home, ".gemini")
	for _, client := range []string{"antigravity", "antigravity-ide"} {
		dir := filepath.Join(roots, client)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		store, err := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT, title TEXT, last_modified_time DATETIME,
			last_user_input_time DATETIME, workspace_uris TEXT, parent_conversation_id TEXT)`)
		if err == nil {
			_, err = store.Exec(`INSERT INTO conversation_summaries VALUES (?, 'Fixture summary',
				'2026-11-23 12:00:00.125+00:00', '2026-11-23 11:59:00+00:00', '["file:///fixture/project"]', '')`, id)
		}
		store.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 1 {
		t.Fatalf("duplicate summary sync: %+v", rep)
	}
	sessionID := "antigravity:" + id
	detail := sessionShown(t, db, sessionID)
	if detail.Harness != kernel.Antigravity || detail.Title != "Fixture summary" ||
		detail.End.UnixMilli() != time.Date(2026, 11, 23, 12, 0, 0, 125000000, time.UTC).UnixMilli() || detail.Requests != 0 {
		t.Fatalf("summary only: %+v", detail)
	}
	const journal = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-23T11:57:00Z","content":"<USER_REQUEST>Check the fixture</USER_REQUEST><ADDITIONAL_METADATA>ignored</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-23T11:58:00Z","model":"gemini-test","input_tokens":5,"cache_read_tokens":7,"output_tokens":3,"tool_calls":[{"id":"call-1","name":"view_file","args":"{}"}]}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-23T11:58:00Z","model":"gemini-test","input_tokens":5,"cache_read_tokens":7,"output_tokens":3}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-23T11:58:30Z","model":"gemini-test","input_tokens":9}
{"step_index":3`
	stamp := time.Date(2026, 11, 23, 12, 0, 1, 0, time.UTC)
	for _, copy := range []struct{ client, name, contents string }{
		{"antigravity", "transcript.jsonl", strings.ReplaceAll(journal, "Check the fixture", "Truncated copy")},
		{"antigravity-cli", "transcript_full.jsonl", journal},
	} {
		path := filepath.Join(roots, copy.client, "brain", id, ".system_generated", "logs", copy.name)
		writeReaderFixture(t, path, copy.contents)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if rep := sync(); rep.Indexed != 1 || rep.SkippedLines != 1 {
		t.Fatalf("enrichment sync: %+v", rep)
	}
	detail = sessionShown(t, db, sessionID)
	if detail.Title != "Fixture summary" || detail.Requests != 1 || detail.Input != 5 || detail.CacheRead != 7 || detail.Output != 3 ||
		len(detail.Prompts) != 1 || detail.Prompts[0].Text != "Check the fixture" || len(detail.Tools) != 1 || detail.Tools[0].Calls != 1 {
		t.Fatalf("enriched session: %+v", detail)
	}
	if rep := sync(); rep.Indexed != 0 {
		t.Fatalf("unchanged source: %+v", rep)
	}
}
