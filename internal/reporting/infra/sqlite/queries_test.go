package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// hostile carries SQL metacharacters in every text value that reaches the database.
const hostile = `o'brian"; DROP TABLE requests; --`

// readModel is the part of the index this adapter reads: the indexing context's published read model.
// contract_test.go at the module root checks it against the indexing adapter's real schema.
const readModel = `
CREATE TABLE sessions (id TEXT PRIMARY KEY, harness TEXT NOT NULL, agent_type TEXT NOT NULL, folder TEXT);
CREATE TABLE requests (session_id TEXT NOT NULL, ts INTEGER NOT NULL, model TEXT NOT NULL,
	input INTEGER NOT NULL, output INTEGER NOT NULL, cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL,
	cost REAL NOT NULL, priced INTEGER NOT NULL, duration_ms REAL, ttft_ms REAL, error TEXT);
CREATE TABLE tool_calls (session_id TEXT NOT NULL, ts INTEGER NOT NULL, name TEXT NOT NULL, server TEXT NOT NULL,
	args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL, is_error INTEGER NOT NULL, duration_ms REAL);
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
		{`INSERT INTO sessions VALUES ('claude:s1', 'claude', 'main', ?)`, []any{hostile}},
		{`INSERT INTO requests VALUES ('claude:s1', ?, ?, 1000, 100, 0, 0, 2, 1, 1500, NULL, NULL)`, []any{ms, hostile}},
		{`INSERT INTO tool_calls VALUES ('claude:s1', ?, ?, 'grace', 0, 0, 1, NULL)`, []any{ms, hostile}},
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
	if _, err := r.db.Exec(`DELETE FROM requests`); err == nil {
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
