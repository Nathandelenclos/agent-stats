package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

// fixtureHome copies testdata/home into a temp dir and adds a zstd-compressed archived Codex rollout.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.CopyFS(home, os.DirFS("../testdata/home")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../testdata/zst/rollout-2.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".codex", "archived_sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2.jsonl.zst"), enc.EncodeAll(raw, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureEnv(t, home)
	return home
}

// TestReportApproved locks the whole pipeline (discovery, parsing, pricing, index, report) on fixture logs.
// On a difference it writes testdata/report.received.json: read the diff, and only copy it over
// report.approved.json if the change is intended.
func TestReportApproved(t *testing.T) {
	prices := pricing.PriceList{
		"claude-x": mustRates(t, pricing.RatesPerToken{Input: 3e-6, Output: 15e-6, CacheRead: 0.3e-6, CacheWrite: 3.75e-6}),
		"claude-y": mustRates(t, pricing.RatesPerToken{Input: 1e-6, Output: 5e-6, CacheRead: 0.1e-6, CacheWrite: 1.25e-6}),
		"gpt-x":    mustRates(t, pricing.RatesPerToken{Input: 1.25e-6, Output: 10e-6, CacheRead: 0.125e-6}),
	}
	dbPath := filepath.Join(t.TempDir(), "index.db")
	index, err := sqlite.Open(sqlite.Options{Path: dbPath, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	logs := logfiles.NewFileSessionLogs(logfiles.DefaultSources(fixtureHome(t)))
	rep, err := indexing.IndexSessions{Logs: logs, Store: index, Prices: prices}.Run(nil)
	if err != nil || len(rep.Errors) > 0 || rep.Indexed != 7 {
		t.Fatalf("index: %+v %v", rep, err)
	}

	t.Setenv("TZ", "UTC")
	approveReports(t, dbPath, "report", map[string][]string{
		"all":         nil,
		"omp":         {"--harness", "omp"},
		"since-jan-2": {"--since", "2026-01-02"},
	})
}

// approveReports collects the CLI reports as a single JSON object, one value per set of flags.
func approveReports(t *testing.T, db, name string, cases map[string][]string) {
	t.Helper()
	got := make(map[string]json.RawMessage, len(cases))
	for label, args := range cases {
		out, stderr, err := runCLI(t, "", append([]string{"--db", db, "--no-sync", "--all", "--json"}, args...)...)
		if err != nil {
			t.Fatalf("report %s: %v\n%s", label, err, stderr)
		}
		got[label] = json.RawMessage(out)
	}
	received, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	approve(t, name+".approved.json", name+".received.json", append(received, '\n'))
}

func mustRates(t *testing.T, perToken pricing.RatesPerToken) pricing.Rates {
	t.Helper()
	r, err := pricing.NewRates(perToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
