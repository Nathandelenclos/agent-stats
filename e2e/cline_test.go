package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

var clineFixtures = filepath.Join("..", "internal", "indexing", "infra", "logfiles", "testdata", "cline")

func TestClineIndexesHistoricalTaskAndReadsItsAPIHistoryOnce(t *testing.T) {
	home := useIndexHome(t)
	task := filepath.Join(testCodeUserDir(home), "globalStorage", "saoudrizwan.claude-dev", "tasks", "legacy")
	installReaderFixture(t, filepath.Join(task, "ui_messages.json"), filepath.Join(clineFixtures, "ui_messages.json"))
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 1 || rep.SkippedLines != 1 {
		t.Fatalf("index legacy task: %+v", rep)
	}
	h, err := kernel.ParseHarness("cline")
	if err != nil {
		t.Fatal(err)
	}
	detail := sessionShown(t, db, "cline:legacy")
	if detail.Harness != h || detail.Requests != 2 || detail.Input != 10 || detail.Output != 5 ||
		detail.CacheRead != 2 || detail.CacheWrite != 3 || detail.Cost != 0.02 || detail.Failed != 1 ||
		detail.Unpriced != 1 || detail.AvgDuration != 0 || len(detail.Prompts) != 2 || len(detail.Tools) != 0 {
		t.Fatalf("legacy task: %+v", detail)
	}

	installReaderFixture(t, filepath.Join(task, "api_conversation_history.json"), filepath.Join(clineFixtures, "api_conversation_history.json"))
	if rep := sync(); rep.Indexed != 1 || rep.Scanned != 1 {
		t.Fatalf("API history was not read as a companion: %+v", rep)
	}
	detail = sessionShown(t, db, "cline:legacy")
	if detail.Requests != 2 || len(detail.Prompts) != 2 || detail.Title != "Inspect the sample" ||
		len(detail.Tools) != 1 || detail.Tools[0].Calls != 1 || detail.Tools[0].Errors != 1 || detail.Tools[0].ResultChars == 0 {
		t.Fatalf("legacy task with API history: %+v", detail)
	}
}

func TestClineSessionKeepsOnlyUsageThatCanBeAttributedToCalls(t *testing.T) {
	home := useIndexHome(t)
	root := filepath.Join(home, "fixture sessions")
	t.Setenv("CLINE_DATA_DIR", filepath.Join(home, "unused data"))
	t.Setenv("CLINE_SESSION_DATA_DIR", root)
	dir := filepath.Join(root, "modern")
	project := filepath.Join(home, "sample project")
	manifest, err := os.ReadFile(filepath.Join(clineFixtures, "modern.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeReaderFixture(t, filepath.Join(dir, "modern.json"), strings.ReplaceAll(string(manifest), `"/example/project"`, strconv.Quote(project)))
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 1 {
		t.Fatalf("manifest sync: %+v", rep)
	}
	detail := sessionShown(t, db, "cline:modern")
	if detail.Harness != kernel.Cline || detail.Folder != project || detail.Requests != 0 ||
		len(detail.Prompts) != 1 || detail.Title != "Inspect the sample" {
		t.Fatalf("manifest alone: %+v", detail)
	}

	for _, name := range []string{"modern.messages.json", "worker.messages.json"} {
		installReaderFixture(t, filepath.Join(dir, name), filepath.Join(clineFixtures, name))
	}
	if rep := sync(); rep.Indexed != 2 || rep.Scanned != 2 {
		t.Fatalf("sync with companions: %+v", rep)
	}
	detail = sessionShown(t, db, "cline:modern")
	if detail.Requests != 3 || detail.Input != 25 || detail.Output != 8 || detail.CacheRead != 4 ||
		detail.CacheWrite != 2 || detail.Cost < 0.03999 || detail.Cost > 0.04001 ||
		len(detail.Prompts) != 2 || detail.SubagentCount != 1 || len(detail.Models) != 2 ||
		detail.AvgDuration != 0 || detail.AvgTTFT != 0 {
		t.Fatalf("modern task: %+v", detail)
	}
	if len(detail.Tools) != 1 || detail.Tools[0].Calls != 2 || detail.Tools[0].Errors != 1 || detail.Tools[0].ResultChars != 19 {
		t.Fatalf("tools: %+v", detail.Tools)
	}
	if rep := sync(); rep.Indexed != 0 {
		t.Fatalf("unchanged source: %+v", rep)
	}
}

func TestClineResumedTaskDoesNotCountImportedHistoryTwice(t *testing.T) {
	home := useIndexHome(t)
	task := filepath.Join(testCodeUserDir(home), "globalStorage", "saoudrizwan.claude-dev", "tasks", "legacy")
	dir := filepath.Join(home, ".cline", "data", "sessions", "legacy")
	installReaderFixture(t, filepath.Join(task, "ui_messages.json"), filepath.Join(clineFixtures, "ui_messages.json"))
	installReaderFixture(t, filepath.Join(task, "api_conversation_history.json"), filepath.Join(clineFixtures, "api_conversation_history.json"))
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 1 {
		t.Fatalf("historical sync: %+v", rep)
	}

	writeReaderFixture(t, filepath.Join(dir, "legacy.json"), `{"version":1,"session_id":"legacy","started_at":"2026-03-11T08:00:00Z","prompt":"Inspect the sample"}`)
	writeReaderFixture(t, filepath.Join(dir, "legacy.messages.json"), `{"version":1,"sessionId":"legacy","agent":"lead","messages":[
		{"id":"old-1","role":"user","content":"Inspect the sample"},
		{"id":"old-2","role":"assistant","content":"Old output","metrics":{"inputTokens":100,"cost":1}},
		{"id":"warning","role":"user","content":"Warning: this is a legacy conversation, which means tool names may have changed. Please use the most up-to-date tools you are aware of."},
		{"id":"resume","role":"user","content":"Check again","ts":1773216000100},
		{"id":"second","role":"assistant","content":"Done","ts":1773216000200,"metrics":{"inputTokens":7,"outputTokens":3,"cost":0.01}}
	]}`)
	if rep := sync(); rep.Indexed != 1 || rep.Scanned != 2 {
		t.Fatalf("resumed sync: %+v", rep)
	}
	detail := sessionShown(t, db, "cline:legacy")
	if detail.Requests != 3 || detail.Input != 17 || detail.Output != 8 || detail.Cost < 0.02999 || detail.Cost > 0.03001 ||
		detail.PromptCount != 3 || len(detail.Tools) != 1 {
		t.Fatalf("resumed task: %+v", detail)
	}
}
