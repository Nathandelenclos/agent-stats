package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// fixtureHome copies testdata/home into a temp dir and adds a zstd-compressed archived Codex rollout.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.CopyFS(home, os.DirFS("testdata/home")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/zst/rollout-2.jsonl")
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
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
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

	usage, err := usagedb.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	got := map[string]jsonReport{}
	for name, f := range map[string]reporting.Query{
		"all":         {},
		"omp":         {Harness: kernel.OMP},
		"since-jan-2": {Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	} {
		r, err := reporting.BuildReport{Queries: usage}.Run(f)
		if err != nil {
			t.Fatal(err)
		}
		got[name] = toJSON(r)
	}
	received, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	received = append(received, '\n')
	approved, err := os.ReadFile("testdata/report.approved.json")
	if err != nil || !bytes.Equal(approved, received) {
		if werr := os.WriteFile("testdata/report.received.json", received, 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Fatalf("report differs from testdata/report.approved.json (%v); see testdata/report.received.json", err)
	}
	os.Remove("testdata/report.received.json")
}

func mustRates(t *testing.T, perToken pricing.RatesPerToken) pricing.Rates {
	t.Helper()
	r, err := pricing.NewRates(perToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":    {},
		"7d":  now.AddDate(0, 0, -7),
		"12h": now.Add(-12 * time.Hour),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got, err := parseSince("2026-01-02", now); err != nil || !got.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("date: %v %v", got, err)
	}
	for _, bad := range []string{"yesterday", "7x", "-"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestInteractiveOnlyOnATerminalWithoutJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Given stdout on a pipe (not a terminal), when no flag is set, then the plain report is kept.
	if interactive(false, w) {
		t.Error("a pipe was taken for a terminal")
	}
	if interactive(true, w) {
		t.Error("--json opened the interface")
	}
}

func TestSyncNoteSaysWhatWasSkipped(t *testing.T) {
	for _, c := range []struct {
		rep  indexing.IndexReport
		want string
	}{
		{indexing.IndexReport{Indexed: 3}, ""},
		{indexing.IndexReport{Errors: []string{"a", "b"}}, "skipped files: 2"},
		{indexing.IndexReport{Errors: []string{"a"}, SkippedLines: 4}, "skipped files: 1 · skipped log lines: 4"},
	} {
		if got := syncNote(c.rep); got != c.want {
			t.Errorf("syncNote(%+v) = %q, want %q", c.rep, got, c.want)
		}
	}
}
