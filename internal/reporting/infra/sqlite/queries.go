// Package sqlite answers the reporting context's usage and session queries from the SQLite index that the indexing
// context writes. It opens the index read-only and reads only the usage_* and session_* views, the index's published read model.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SQLiteUsage reads usage from the SQLite index. Asked directly, each question reads the index as it
// is then; Snapshot pins one state for a series of questions.
type SQLiteUsage struct {
	db *sql.DB
	reader
}

var _ reporting.UsageQueries = (*SQLiteUsage)(nil)

// querier is what both the database and a transaction on it answer queries through.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// scoped runs the queries of a database or a transaction under a context: each stops when ctx does, which a plain
// Query on a transaction does not.
type scoped struct {
	ctx context.Context
	db  interface {
		QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
		QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	}
}

func (s scoped) Query(query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(s.ctx, query, args...)
}

func (s scoped) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(s.ctx, query, args...)
}

// reader answers the usage questions through q. folders, when set, remembers the folders of the project the questions
// are about, so that the questions of one snapshot read the index's folders once and agree on them; a reader of the
// live index has none, for what it read would go stale.
type reader struct {
	q       querier
	folders *projectFolders
}

// projectFolders is the folders of one project as projectfolders.List gives them.
type projectFolders struct {
	project kernel.Project
	list    string
	read    bool
}

// folderList is the folders of p, read the first time and remembered after that when the reader remembers.
func (r reader) folderList(p kernel.Project) (string, error) {
	if m := r.folders; m != nil && m.read && m.project == p {
		return m.list, nil
	}
	list, err := projectfolders.List(r.q, p)
	if err == nil && r.folders != nil {
		*r.folders = projectFolders{p, list, true}
	}
	return list, err
}

var _ reporting.UsageReader = reader{}

// OpenReadOnly opens the index at path for reading only.
func OpenReadOnly(path string) (*SQLiteUsage, error) {
	abs, err := filepath.Abs(path) // a relative path would read as a URI authority
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteUsage{db: db, reader: reader{q: scoped{context.Background(), db}}}, nil
}

// Snapshot runs fn inside one read transaction. The index is in WAL mode, so the transaction keeps the state
// of its first read until it ends and the indexing writer is never blocked; the reader fn gets cannot see
// anything indexing commits meanwhile. The transaction and every query of the reader stop when ctx does.
func (s *SQLiteUsage) Snapshot(ctx context.Context, fn func(reporting.UsageReader) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // nothing was written: ending the transaction is all it takes
	return fn(reader{q: scoped{ctx, tx}, folders: &projectFolders{}})
}

// Close closes the database.
func (s *SQLiteUsage) Close() error { return s.db.Close() }

var _ reporting.ProjectQueries = (*SQLiteUsage)(nil)

// HasSessions reports whether a session of the index worked in the project's folder or below it.
func (s *SQLiteUsage) HasSessions(p kernel.Project) (bool, error) {
	folders, err := projectfolders.Folders(s.db, p)
	return len(folders) > 0, err
}

// where turns a filter into SQL conditions on the rows of a view aliased alias; every value is a bound parameter.
func (r reader) where(f report.Filter, alias string) (string, []any, error) {
	var conds []string
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, alias+".ts >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		conds, args = append(conds, "harness = ?"), append(args, f.Harness.String())
	}
	if !f.Project.IsZero() {
		list, err := r.folderList(f.Project)
		if err != nil {
			return "", nil, err
		}
		conds, args = append(conds, projectfolders.InList(alias+".folder")), append(args, list)
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

// groupColumns is the closed list of SQL expressions a usage query may group by.
var groupColumns = map[report.Dimension]string{
	report.ByProvider:  "provider",
	report.ByHarness:   "harness",
	report.ByModel:     "model",
	report.ByFolder:    "COALESCE(folder, '')",
	report.ByAgentType: "agent_type",
	report.ByHour:      "CAST(r.ts / 3600000 * 3600000 AS TEXT)",
	report.ByDay:       "strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch', 'localtime')",
}

// COALESCE keeps an empty selection scannable as zeros.
const usageCols = `COUNT(*), COUNT(r.error), COALESCE(SUM(r.input), 0), COALESCE(SUM(r.output), 0),
	COALESCE(SUM(r.cache_read), 0), COALESCE(SUM(r.cache_write), 0), COALESCE(SUM(r.cost), 0), COALESCE(SUM(1 - r.priced), 0),
	COALESCE(AVG(r.duration_ms), 0), COALESCE(AVG(r.ttft_ms), 0), COALESCE(MIN(r.ts), 0), COALESCE(MAX(r.ts), 0),
	COUNT(r.duration_ms), COUNT(r.ttft_ms),
	COALESCE(AVG(CASE WHEN r.duration_ms > 0 AND r.output > 0 THEN 1000.0 * r.output / r.duration_ms END), 0),
	COUNT(CASE WHEN r.duration_ms > 0 AND r.output > 0 THEN 1 END), COALESCE(SUM(r.premium_units), 0)`

// usageRow scans one usageCols row; timestamps are stored in Unix milliseconds, 0 when unknown.
type usageRow struct {
	u           report.Usage
	first, last int64
}

func (r *usageRow) args() []any {
	return []any{&r.u.Requests, &r.u.Failed, &r.u.Input, &r.u.Output, &r.u.CacheRead, &r.u.CacheWrite,
		&r.u.Cost, &r.u.Unpriced, &r.u.AvgDuration, &r.u.AvgTTFT, &r.first, &r.last,
		&r.u.DurationSamples, &r.u.TTFTSamples, &r.u.AvgTokensPerSecond, &r.u.ThroughputSamples, &r.u.PremiumUnits}
}

func (r *usageRow) usage() report.Usage {
	r.u.First, r.u.Last = fromMillis(r.first), fromMillis(r.last)
	return r.u
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// UsageBy groups requests along d, most expensive first (days: oldest first).
func (r reader) UsageBy(f report.Filter, d report.Dimension) ([]report.UsageGroup, error) {
	col, ok := groupColumns[d]
	if !ok {
		return nil, fmt.Errorf("unknown usage dimension %d", d)
	}
	cond, args, err := r.where(f, "r")
	if err != nil {
		return nil, err
	}
	order := "SUM(r.cost) DESC, COUNT(*) DESC"
	if d == report.ByDay || d == report.ByHour {
		order = "MIN(r.ts)"
		if cond == "" {
			cond = " WHERE r.ts > 0" // a request with an unknown time (0) has no day
		} else {
			cond += " AND r.ts > 0"
		}
	}
	rows, err := r.q.Query(`SELECT `+col+`, `+usageCols+`
		FROM usage_requests r`+cond+`
		GROUP BY 1 ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.UsageGroup
	for rows.Next() {
		var group string
		var row usageRow
		if err := rows.Scan(append([]any{&group}, row.args()...)...); err != nil {
			return nil, err
		}
		out = append(out, report.UsageGroup{Group: group, Usage: row.usage()})
	}
	return out, rows.Err()
}

// Total aggregates every request matching f.
func (r reader) Total(f report.Filter) (report.Usage, error) {
	cond, args, err := r.where(f, "r")
	if err != nil {
		return report.Usage{}, err
	}
	var row usageRow
	err = r.q.QueryRow(`SELECT `+usageCols+` FROM usage_requests r`+cond, args...).Scan(row.args()...)
	return row.usage(), err
}

// Tools aggregates tool calls, most used first.
func (r reader) Tools(f report.Filter) ([]report.ToolUsage, error) {
	cond, args, err := r.where(f, "t")
	if err != nil {
		return nil, err
	}
	return toolUsage(r.q, "", "usage_tool_calls", cond, args)
}

// toolUsage aggregates the calls of view (aliased t) that cond selects, most used first. with, when it is not empty, is a
// WITH clause cond may use.
func toolUsage(q querier, with, view, cond string, args []any) ([]report.ToolUsage, error) {
	rows, err := q.Query(with+` SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts)
		FROM `+view+` t`+cond+`
		GROUP BY 1, 2 ORDER BY 3 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.ToolUsage
	for rows.Next() {
		var r report.ToolUsage
		var last int64
		if err := rows.Scan(&r.Name, &r.Server, &r.Calls, &r.Errors, &r.ArgsChars, &r.ResultChars, &r.AvgDuration, &last); err != nil {
			return nil, err
		}
		r.Last = fromMillis(last)
		out = append(out, r)
	}
	return out, rows.Err()
}
