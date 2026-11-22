package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

func TestActivitySeriesKeepsTheProjectAndDistinguishesIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	_, err = writer.Exec(`
		CREATE TABLE usage_folders(folder TEXT);
		INSERT INTO usage_folders VALUES ('/work/app'), ('/work/other');
		CREATE TABLE usage_requests(ts INTEGER, model TEXT, provider TEXT, error TEXT, duration_ms REAL,
			ttft_ms REAL, output INTEGER, harness TEXT, folder TEXT, input INTEGER DEFAULT 25,
			cache_read INTEGER DEFAULT 50, cache_write INTEGER DEFAULT 0, cost REAL DEFAULT 0.25,
			premium_units REAL DEFAULT 0.5, priced INTEGER DEFAULT 1);
		CREATE TABLE usage_tool_calls(ts INTEGER, name TEXT, server TEXT, is_error INTEGER,
			duration_ms REAL, harness TEXT, folder TEXT);
		INSERT INTO usage_requests(ts, model, provider, error, duration_ms, ttft_ms, output, harness, folder) VALUES
			(3600001, 'model', 'provider-a', NULL, 2000, 250, 50, 'claude', '/work/app'),
			(3600002, 'model', 'provider-b', 'failed', NULL, NULL, 0, 'claude', '/work/app'),
			(3600003, 'model', 'provider-a', NULL, 1000, NULL, 50, 'codex', '/work/app'),
			(3600004, 'model', 'provider-a', NULL, 1000, NULL, 50, 'claude', '/work/other'),
			(0, 'undated', 'provider-b', NULL, 1000, NULL, 50, 'claude', '/work/app');
		INSERT INTO usage_tool_calls VALUES
			(3600001, 'read', '', 0, 10, 'claude', '/work/app'),
			(3600002, 'read', 'files', 1, NULL, 'claude', '/work/app'),
			(3600003, 'read', 'files', 0, NULL, 'claude', '/work/other');
	`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, _ := kernel.NewProject("/work/app")
	filter := report.Filter{Project: project, Harness: kernel.Claude}
	models, err := store.ActivitySeries(t.Context(), filter, report.ModelActivity, report.HourBucket)
	if err != nil || len(models) != 2 {
		t.Fatalf("model series = %+v, error = %v", models, err)
	}
	if models[0].Bucket != "3600000" || models[0].Source != "provider-a" || models[0].Calls != 1 ||
		models[0].AvgThroughput != 25 || models[0].TTFTSamples != 1 || models[0].Cost != 0.25 ||
		models[0].CacheRead != 50 || models[0].PremiumUnits != 0.5 || models[1].Source != "provider-b" ||
		models[1].Errors != 1 || models[1].DurationSamples != 0 {
		t.Errorf("model series = %+v", models)
	}
	filter.Since = time.UnixMilli(3600002)
	tools, err := store.ActivitySeries(t.Context(), filter, report.ToolActivity, report.DayBucket)
	if err != nil || len(tools) != 1 || tools[0].Source != "files" || tools[0].Calls != 1 || tools[0].Errors != 1 {
		t.Errorf("tool series = %+v, error = %v", tools, err)
	}
}
