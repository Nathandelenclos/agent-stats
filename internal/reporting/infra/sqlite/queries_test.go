package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	harness TEXT NOT NULL, agent_type TEXT NOT NULL, folder TEXT, provider TEXT NOT NULL DEFAULT '', premium_units REAL NOT NULL DEFAULT 0);
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
		{`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error, harness, agent_type, folder)
		VALUES (?, ?, 1000, 100, 0, 0, 2, 1, 1500, NULL, NULL, 'claude', 'main', ?)`, []any{ms, hostile, hostile}},
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
	err = r.Snapshot(context.Background(), func(q reporting.UsageReader) error {
		var err error
		if total, err = q.Total(report.Filter{}); err != nil {
			return err
		}
		if _, err = w.Exec(`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error, harness, agent_type, folder)
		VALUES (?, 'm2', 1, 1, 0, 0, 1, 1, NULL, NULL, NULL, 'claude', 'main', NULL)`, at.UnixMilli()); err != nil {
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
	if err := r.Snapshot(context.Background(), func(q reporting.UsageReader) error {
		u, err := q.Total(report.Filter{})
		if u.Requests != 2 {
			t.Errorf("next snapshot sees %d requests, want 2", u.Requests)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUsageByDayFollowsTheLocalCalendarOldestFirst(t *testing.T) {
	// Given a clock in Tokyo (UTC+9), requests on both sides of its midnight, one in a later day and one with no known time.
	// The time zone is read once per process (by SQLite's localtime on Linux too), so the scenario runs in a process
	// started in Tokyo, as a user's process would be.
	if os.Getenv("TZ") != "Asia/Tokyo" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUsageByDayFollowsTheLocalCalendarOldestFirst$", "-test.count=1")
		cmd.Env = append(os.Environ(), "TZ=Asia/Tokyo")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("scenario in Tokyo failed: %v\n%s", err, out)
		}
		return
	}
	path := indexed(t, time.UnixMilli(1))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM usage_requests`); err != nil {
		t.Fatal(err)
	}
	tokyo := time.FixedZone("JST", 9*3600)
	for _, ts := range []time.Time{
		time.Date(2026, 1, 3, 0, 30, 0, 0, tokyo), // 2026-01-02 in UTC, 2026-01-03 in Tokyo
		time.Date(2026, 1, 2, 23, 30, 0, 0, tokyo),
		time.Date(2026, 1, 5, 12, 0, 0, 0, tokyo),
	} {
		if _, err := db.Exec(`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error, harness, agent_type, folder)
		VALUES (?, 'm', 1, 1, 0, 0, 1, 1, NULL, NULL, NULL, 'claude', 'main', NULL)`, ts.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error, harness, agent_type, folder)
		VALUES (0, 'm', 1, 1, 0, 0, 1, 1, NULL, NULL, NULL, 'claude', 'main', NULL)`); err != nil {
		t.Fatal(err)
	}
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// When usage is grouped by day, with and without a period
	all, err := r.UsageBy(report.Filter{}, report.ByDay)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := r.UsageBy(report.Filter{Since: time.Date(2026, 1, 4, 0, 0, 0, 0, tokyo)}, report.ByDay)
	if err != nil {
		t.Fatal(err)
	}

	// Then the days are the user's, oldest first, the request without a time is on no day, and the period narrows them
	var days []string
	for _, g := range all {
		days = append(days, g.Group)
	}
	if want := []string{"2026-01-02", "2026-01-03", "2026-01-05"}; !slices.Equal(days, want) {
		t.Errorf("days = %v, want %v", days, want)
	}
	if len(recent) != 1 || recent[0].Group != "2026-01-05" {
		t.Errorf("days since the 4th = %+v", recent)
	}
}

func TestAReportStopsWhenItsContextEnds(t *testing.T) {
	// Given an index and a caller that gives up while its snapshot is open
	r, err := OpenReadOnly(indexed(t, time.UnixMilli(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())

	// When the caller's context ends between two questions
	err = r.Snapshot(ctx, func(q reporting.UsageReader) error {
		if _, err := q.Total(report.Filter{}); err != nil {
			t.Errorf("a question before the cancellation failed: %v", err)
		}
		cancel()
		_, err := q.Total(report.Filter{})
		return err
	})

	// Then the next question fails with the context's error instead of reading on
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestNoReportStartsForACallerThatAlreadyGaveUp(t *testing.T) {
	// Given an index and a caller that gave up before asking
	r, err := OpenReadOnly(indexed(t, time.UnixMilli(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When it asks for a snapshot
	called := false
	err = r.Snapshot(ctx, func(reporting.UsageReader) error { called = true; return nil })

	// Then the snapshot does not start
	if !errors.Is(err, context.Canceled) || called {
		t.Errorf("snapshot under an ended context: err = %v, ran = %v", err, called)
	}
}

func TestProviderUsageCountsTimingCoverage(t *testing.T) {
	path := indexed(t, time.UnixMilli(t0))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE usage_requests SET provider = 'anthropic', ttft_ms = 200;
		INSERT INTO usage_requests (ts, model, input, output, cache_read, cache_write, cost, priced, harness, agent_type, provider)
		VALUES (1, 'm', 0, 9999, 0, 0, 0, 0, 'claude', 'main', 'anthropic')`); err != nil {
		t.Fatal(err)
	}
	u, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	err = u.Snapshot(context.Background(), func(r reporting.UsageReader) error {
		groups, err := r.UsageBy(report.Filter{}, report.ByProvider)
		if err != nil {
			return err
		}
		if len(groups) != 1 {
			t.Fatalf("provider groups = %+v", groups)
		}
		got := groups[0]
		if got.Group != "anthropic" || got.Requests != 2 || got.DurationSamples != 1 || got.TTFTSamples != 1 ||
			got.ThroughputSamples != 1 || got.AvgTokensPerSecond < 66.6 || got.AvgTokensPerSecond > 66.7 {
			t.Fatalf("provider timing coverage = %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestErrorSignaturesGroupTransientIDsAndPreserveStatusCodes(t *testing.T) {
	path := indexed(t, time.UnixMilli(t0))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE usage_requests SET error = 'HTTP 429 in req_abcdef after 1 second';
		INSERT INTO usage_requests (ts, model, input, output, cache_read, cache_write, cost, priced, harness, agent_type, error) VALUES
		(1, 'm', 0, 0, 0, 0, 0, 0, 'claude', 'main', 'HTTP 429 in req_hijklm after 8 second'),
		(2, 'm', 0, 0, 0, 0, 0, 0, 'claude', 'main', 'HTTP 503 in req_hijklm after 8 second')`); err != nil {
		t.Fatal(err)
	}
	s := readTestSessions(t, path)
	groups, err := s.ErrorSignatures(t.Context(), report.Filter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Count != 2 || groups[0].Message != "HTTP 429 in <value> after <value> second" ||
		groups[0].Last.UnixMilli() != t0 || groups[1].Count != 1 || groups[0].Key == groups[1].Key {
		t.Fatalf("error groups = %+v", groups)
	}
}

func TestUsageByHourKeepsSeparateAbsoluteHours(t *testing.T) {
	at := time.UnixMilli(t0 + 12_000)
	path := indexed(t, at)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, harness, agent_type)
		VALUES (?, 'm', 0, 1, 0, 0, 0, 1, 'claude', 'main'),
		(0, 'm', 0, 1, 0, 0, 0, 1, 'claude', 'main')`, at.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	u, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	rows, err := u.UsageBy(report.Filter{}, report.ByHour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Group != "1767261600000" || rows[1].Group != "1767265200000" {
		t.Fatalf("hourly buckets = %+v", rows)
	}
}

func TestUsageByDayMatchesOnlyTheSelectedModel(t *testing.T) {
	u, err := OpenReadOnly(indexed(t, time.UnixMilli(t0)))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for _, model := range []string{hostile, "missing"} {
		days, err := u.UsageBy(report.Filter{Model: model}, report.ByDay)
		if err != nil || (len(days) > 0) != (model == hostile) {
			t.Fatalf("%q: days = %+v, %v", model, days, err)
		}
	}
}
