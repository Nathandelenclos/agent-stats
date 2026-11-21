package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/app"
	"github.com/Nathandelenclos/agent-stats/internal/ingest"
	"github.com/Nathandelenclos/agent-stats/internal/store"
	"github.com/Nathandelenclos/agent-stats/internal/usage"
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
	prices := usage.PriceList{
		"claude-x": mustRates(t, 3e-6, 15e-6, 0.3e-6, 3.75e-6),
		"claude-y": mustRates(t, 1e-6, 5e-6, 0.1e-6, 1.25e-6),
		"gpt-x":    mustRates(t, 1.25e-6, 10e-6, 0.125e-6, 0),
	}
	index, err := store.Open(filepath.Join(t.TempDir(), "index.db"), "me@host")
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	logs := ingest.NewFileSessionLogs(ingest.DefaultSources(fixtureHome(t)))
	rep, err := app.IndexSessions{Logs: logs, Index: index, Prices: prices}.Run(nil)
	if err != nil || len(rep.Errors) > 0 || rep.Indexed != 7 {
		t.Fatalf("index: %+v %v", rep, err)
	}

	got := map[string]app.Report{}
	for name, f := range map[string]app.Filter{
		"all":         {},
		"omp":         {Harness: usage.OMP},
		"since-jan-2": {Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	} {
		if got[name], err = (app.BuildReport{Index: index}).Run(f); err != nil {
			t.Fatal(err)
		}
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

func mustRates(t *testing.T, input, output, cacheRead, cacheWrite float64) usage.Rates {
	t.Helper()
	r, err := usage.NewRates(input, output, cacheRead, cacheWrite)
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

func TestFormatting(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1 000", 1234567: "1 234 567", -1234: "-1 234"} {
		if got := num(in); got != want {
			t.Errorf("num(%d) = %q, want %q", in, got, want)
		}
	}
	if got := shorten("/very/long/path", 6); got != "…/path" {
		t.Errorf("shorten = %q", got)
	}
}
