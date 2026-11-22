package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// hostile carries SQL metacharacters in every text value that reaches the database.
const hostile = `o'brian"; DROP TABLE requests; --`

// readModel has the shape of the usage_* views this adapter reads, the indexing context's published read model.
// contract_test.go at the module root checks the adapter against the indexing adapter's real views.
const readModel = `
CREATE TABLE usage_requests (ts INTEGER NOT NULL, model TEXT NOT NULL,
	input INTEGER NOT NULL, output INTEGER NOT NULL, cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL,
	cost REAL NOT NULL, priced INTEGER NOT NULL, duration_ms REAL, ttft_ms REAL, error TEXT,
	harness TEXT NOT NULL, agent_type TEXT NOT NULL, folder TEXT);
CREATE TABLE usage_tool_calls (ts INTEGER NOT NULL, name TEXT NOT NULL, server TEXT NOT NULL,
	args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL, is_error INTEGER NOT NULL, duration_ms REAL, harness TEXT NOT NULL, folder TEXT);
CREATE TABLE usage_folders (folder TEXT NOT NULL);
`

// indexed writes one session into a fresh read model and returns the database path.
func indexed(t *testing.T, at time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms := at.UnixMilli()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{readModel, nil},
		{`INSERT INTO usage_requests VALUES (?, ?, 1000, 100, 0, 0, 2, 1, 1500, NULL, NULL, 'claude', 'main', ?)`, []any{ms, hostile, hostile}},
		{`INSERT INTO usage_tool_calls VALUES (?, ?, 'grace', 0, 0, 1, NULL, 'claude', NULL)`, []any{ms, hostile}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestQueriesReadTheIndex(t *testing.T) {
	at := time.UnixMilli(1767261600000)
	r, err := OpenReadOnly(indexed(t, at))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	u, err := r.Total(report.Filter{})
	if err != nil || u.Requests != 1 || u.Cost != 2 || u.Input != 1000 || u.AvgDuration != 1500 || !u.First.Equal(at) {
		t.Fatalf("total = %+v %v", u, err)
	}
	for _, d := range []report.Dimension{report.ByHarness, report.ByModel, report.ByFolder, report.ByAgentType} {
		if g, err := r.UsageBy(report.Filter{}, d); err != nil || len(g) != 1 || g[0].Requests != 1 {
			t.Errorf("dimension %d: %+v %v", d, g, err)
		}
	}
	if g, _ := r.UsageBy(report.Filter{}, report.ByModel); g[0].Group != hostile {
		t.Errorf("model group = %q", g[0].Group)
	}
	if tools, err := r.Tools(report.Filter{}); err != nil || len(tools) != 1 || tools[0].Name != hostile || tools[0].Errors != 1 || !tools[0].Last.Equal(at) {
		t.Errorf("tools = %+v %v", tools, err)
	}
	// A hostile harness filter is bound as a value: it matches nothing and breaks nothing.
	if u, err := r.Total(report.Filter{Harness: kernel.Codex}); err != nil || u.Requests != 0 {
		t.Errorf("other harness filter = %+v %v", u, err)
	}
	if u, _ := r.Total(report.Filter{Harness: kernel.Claude, Since: at.Add(time.Millisecond)}); u.Requests != 0 {
		t.Errorf("since filter kept an older request: %+v", u)
	}
	if _, err := r.UsageBy(report.Filter{}, report.Dimension(99)); err == nil {
		t.Error("unknown dimension accepted")
	}
}

func TestReaderCannotWrite(t *testing.T) {
	r, err := OpenReadOnly(indexed(t, time.UnixMilli(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.db.Exec(`DELETE FROM usage_requests`); err == nil {
		t.Error("read-only index accepted a write")
	}
}

func TestOpenReadOnlyDoesNotCreateAnIndex(t *testing.T) {
	if r, err := OpenReadOnly(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		r.Close()
		t.Error("missing index opened")
	}
}

func TestOpenReadOnlyAcceptsRelativeAndSpecialPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my index #1?")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := indexed(t, time.UnixMilli(1))
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, path := range []string{"index.db", filepath.Join(dir, "index.db")} {
		r, err := OpenReadOnly(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if u, err := r.Total(report.Filter{}); err != nil || u.Requests != 1 {
			t.Errorf("%s: total = %+v %v", path, u, err)
		}
		r.Close()
	}
}

func TestSnapshotReadsOneStateWhileTheIndexIsWritten(t *testing.T) {
	// Given an index in WAL mode, as the indexing context leaves it, and one request in it
	at := time.UnixMilli(1767261600000)
	path := indexed(t, at)
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// When indexing commits another request between two questions of the same snapshot
	var total report.Usage
	var groups []report.UsageGroup
	err = r.Snapshot(func(q reporting.UsageReader) error {
		var err error
		if total, err = q.Total(report.Filter{}); err != nil {
			return err
		}
		if _, err = w.Exec(`INSERT INTO usage_requests VALUES (?, 'm2', 1, 1, 0, 0, 1, 1, NULL, NULL, NULL, 'claude', 'main', NULL)`, at.UnixMilli()); err != nil {
			return err
		}
		groups, err = q.UsageBy(report.Filter{}, report.ByHarness)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Then both answers describe the same state, and the next snapshot sees the new request
	if total.Requests != 1 || len(groups) != 1 || groups[0].Requests != 1 {
		t.Errorf("total of %d requests but %+v by harness: one report mixes two states", total.Requests, groups)
	}
	if err := r.Snapshot(func(q reporting.UsageReader) error {
		u, err := q.Total(report.Filter{})
		if u.Requests != 2 {
			t.Errorf("next snapshot sees %d requests, want 2", u.Requests)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
