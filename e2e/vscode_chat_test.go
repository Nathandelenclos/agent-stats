package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

func useIndexHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	fixtureEnv(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	for _, key := range []string{"CLINE_DIR", "CLINE_DATA_DIR", "CLINE_SESSION_DATA_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME", "OPENCODE_DB", "KILO_DB"} {
		t.Setenv(key, "")
	}
	return home
}

func testCodeUserDir(home string) string {
	var data string
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support")
	case "windows":
		data = filepath.Join(home, "AppData", "Roaming")
	default:
		data = filepath.Join(home, ".config")
	}
	return filepath.Join(data, "Code", "User")
}

func cliIndexForTest(t *testing.T) (string, func() indexing.IndexReport) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fixture.db")
	index, err := sqlite.Open(sqlite.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	indexer := indexing.IndexSessions{Logs: logfiles.NewFileSessionLogs(logfiles.DefaultSources(home)), Store: index,
		Prices: pricing.PriceList{"test-model": mustRates(t, pricing.RatesPerToken{Input: 1e-6, Output: 2e-6})}, Workers: 1}
	return dbPath, func() indexing.IndexReport {
		t.Helper()
		rep, err := indexer.RunContext(t.Context(), nil)
		if err != nil || len(rep.Errors) != 0 {
			t.Fatalf("index: %+v, %v", rep, err)
		}
		return rep
	}
}

func installReaderFixture(t *testing.T, dst, src string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeReaderFixture(t, dst, string(b))
}

func writeReaderFixture(t *testing.T, dst, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVSCodeChatIndexesTurnsThenPicksUpDiagnosticSpans(t *testing.T) {
	home := useIndexHome(t)
	workspace := filepath.Join(testCodeUserDir(home), "workspaceStorage", "fixture-workspace")
	project := filepath.Join(home, "a project")
	writeReaderFixture(t, filepath.Join(workspace, "workspace.json"), `{"folder":`+strconv.Quote(project)+`}`)
	fixtures := filepath.Join("..", "internal", "indexing", "infra", "logfiles", "testdata", "vscode-chat")
	installReaderFixture(t, filepath.Join(workspace, "chatSessions", "chat-1.jsonl"), filepath.Join(fixtures, "chat.jsonl"))
	// VS Code leaves the previous copy behind when a window moves to a workspace.
	installReaderFixture(t, filepath.Join(testCodeUserDir(home), "globalStorage", "emptyWindowChatSessions", "chat-1.jsonl"), filepath.Join(fixtures, "chat.jsonl"))
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 2 || rep.SkippedLines != 2 {
		t.Fatalf("first sync: %+v", rep)
	}
	h, err := kernel.ParseHarness("vscode-chat")
	if err != nil {
		t.Fatal(err)
	}
	detail := sessionShown(t, db, "vscode-chat:chat-1")
	if detail.Harness != h || detail.Title != "Check the fixture" || detail.Folder != project ||
		detail.Requests != 0 || len(detail.Prompts) != 1 || len(detail.Tools) != 1 || detail.Tools[0].Calls != 1 {
		t.Fatalf("without diagnostics: %+v", detail)
	}

	installReaderFixture(t, filepath.Join(workspace, "GitHub.copilot-chat", "debug-logs", "chat-1", "main.jsonl"), filepath.Join(fixtures, "main.jsonl"))
	if rep := sync(); rep.Indexed != 2 {
		t.Fatalf("diagnostic companion was not re-read: %+v", rep)
	}
	detail = sessionShown(t, db, "vscode-chat:chat-1")
	if detail.Requests != 2 || detail.Input != 42 || detail.Output != 8 || detail.Failed != 1 ||
		detail.Unpriced != 1 || detail.AvgTTFT != 40 || len(detail.Prompts) != 1 || len(detail.Tools) != 1 ||
		detail.Tools[0].Calls != 1 || detail.Tools[0].ResultChars != 8 {
		t.Fatalf("with diagnostics: %+v", detail)
	}
	if rep := sync(); rep.Indexed != 0 {
		t.Fatalf("unchanged source: %+v", rep)
	}
}

func TestVSCodeChatUsesOlderJSONAndIgnoresOtherParticipants(t *testing.T) {
	home := useIndexHome(t)
	folder := filepath.Join(testCodeUserDir(home), "globalStorage", "emptyWindowChatSessions")
	writeReaderFixture(t, filepath.Join(folder, "old.json"), `{"sessionId":"old","creationDate":1773200230000,"requests":[{"requestId":"turn","timestamp":1773200230200,"message":"Describe the fixture","agent":{"id":"github.copilot.default"},"response":["A response"]}]}`)
	writeReaderFixture(t, filepath.Join(folder, "foreign.json"), `{"sessionId":"foreign","creationDate":1773200230000,"requests":[{"requestId":"turn","timestamp":1773200230200,"message":{"text":"Draw a diagram"},"agent":{"extensionId":{"value":"demo.diagram"}}}]}`)
	db, sync := cliIndexForTest(t)
	if rep := sync(); rep.Indexed != 2 {
		t.Fatalf("sync: %+v", rep)
	}
	if detail := sessionShown(t, db, "vscode-chat:old"); detail.Requests != 0 || detail.Title != "Describe the fixture" {
		t.Fatalf("snapshot: %+v", detail)
	}
	check, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var count int
	if err := check.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("sessions = %d, %v", count, err)
	}
	if rep := sync(); rep.Indexed != 0 {
		t.Fatalf("unchanged source: %+v", rep)
	}
}
