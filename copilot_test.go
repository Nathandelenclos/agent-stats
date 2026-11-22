package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

func TestCopilotSessionReachesTheReportThroughTheDefaultIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "OPENCODE_DB", "KILO_DB", "GEMINI_CLI_HOME", "COPILOT_HOME"} {
		t.Setenv(env, "")
	}
	if err := os.CopyFS(home, os.DirFS("testdata/home-copilot")); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "index.db")
	index, err := sqlite.Open(sqlite.Options{Path: dbPath, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	rates := mustRates(t, pricing.RatesPerToken{Output: 1e-6})
	sync, err := newIndexRun(index, pricing.PriceList{"copilot-test": rates, "replacement-model": rates})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := sync(context.Background(), nil)
	if err != nil || rep.Indexed != 1 || len(rep.Errors) != 0 || rep.SkippedLines != 0 {
		t.Fatalf("index = %+v, %v", rep, err)
	}

	h, err := kernel.ParseHarness("copilot")
	if err != nil {
		t.Fatal(err)
	}
	detail := sessionShown(t, dbPath, "copilot:00000000-0000-4000-8000-000000000030")
	if detail.Harness != h || detail.Folder != "/example/project" || detail.Title != "Review the example project." ||
		len(detail.Prompts) != 1 || detail.Prompts[0].Text != detail.Title {
		t.Errorf("session = %+v", detail)
	}
	if detail.Requests != 2 || detail.InputWithCache() != 0 || detail.Output != 14 ||
		detail.Unpriced != 2 || detail.Cost != 0 || detail.AvgDuration != 0 || detail.AvgTTFT != 0 {
		t.Errorf("usage = %+v", detail.Usage)
	}
	models := map[string]int64{}
	for _, model := range detail.Models {
		models[model.Group] = model.Output
	}
	if len(models) != 2 || models["copilot-test"] != 11 || models["replacement-model"] != 3 {
		t.Errorf("models = %v", models)
	}
	if len(detail.Tools) != 1 {
		t.Fatalf("tools = %+v", detail.Tools)
	}
	tool := detail.Tools[0]
	if tool.Server != "example" || tool.Name != "list" || tool.Calls != 1 || tool.Errors != 1 ||
		tool.AvgDuration != 1500 || tool.ArgsChars != int64(len(`{"path":"."}`)) ||
		tool.ResultChars != int64(len(`"Permission denied"`)) {
		t.Errorf("tool = %+v", tool)
	}
}
