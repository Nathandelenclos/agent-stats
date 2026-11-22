// Package sqlite answers the reporting context's usage and session queries from the SQLite index that the indexing
// context writes. It opens the index read-only and reads only the usage_* and session_* views, the index's published read model.
package sqlite

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
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

// reader answers the usage questions through q.
type reader struct{ q querier }

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
	return &SQLiteUsage{db: db, reader: reader{db}}, nil
}

// Snapshot runs fn inside one read transaction. The index is in WAL mode, so the transaction keeps the state
// of its first read until it ends and the indexing writer is never blocked; the reader fn gets cannot see
// anything indexing commits meanwhile.
func (s *SQLiteUsage) Snapshot(fn func(reporting.UsageReader) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // nothing was written: ending the transaction is all it takes
	return fn(reader{tx})
}

// Close closes the database.
func (s *SQLiteUsage) Close() error { return s.db.Close() }

// where turns a filter into SQL conditions; every value is a bound parameter.
func where(f report.Filter, tsCol string) (string, []any) {
	var conds []string
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, tsCol+" >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		conds, args = append(conds, "harness = ?"), append(args, f.Harness.String())
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// groupColumns is the closed list of SQL expressions a usage query may group by.
var groupColumns = map[report.Dimension]string{
	report.ByHarness:   "harness",
	report.ByModel:     "model",
	report.ByFolder:    "COALESCE(folder, '')",
	report.ByAgentType: "agent_type",
	report.ByDay:       "strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch', 'localtime')",
}

// COALESCE keeps an empty selection scannable as zeros.
const usageCols = `COUNT(*), COUNT(r.error), COALESCE(SUM(r.input), 0), COALESCE(SUM(r.output), 0),
	COALESCE(SUM(r.cache_read), 0), COALESCE(SUM(r.cache_write), 0), COALESCE(SUM(r.cost), 0), COALESCE(SUM(1 - r.priced), 0),
	COALESCE(AVG(r.duration_ms), 0), COALESCE(AVG(r.ttft_ms), 0), COALESCE(MIN(r.ts), 0), COALESCE(MAX(r.ts), 0)`

// usageRow scans one usageCols row; timestamps are stored in Unix milliseconds, 0 when unknown.
type usageRow struct {
	u           report.Usage
	first, last int64
}

func (r *usageRow) args() []any {
	return []any{&r.u.Requests, &r.u.Failed, &r.u.Input, &r.u.Output, &r.u.CacheRead, &r.u.CacheWrite,
		&r.u.Cost, &r.u.Unpriced, &r.u.AvgDuration, &r.u.AvgTTFT, &r.first, &r.last}
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
	cond, args := where(f, "r.ts")
	order := "SUM(r.cost) DESC, COUNT(*) DESC"
	if d == report.ByDay {
		order = "1"
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
	cond, args := where(f, "r.ts")
	var row usageRow
	err := r.q.QueryRow(`SELECT `+usageCols+` FROM usage_requests r`+cond, args...).Scan(row.args()...)
	return row.usage(), err
}

// Tools aggregates tool calls, most used first.
func (r reader) Tools(f report.Filter) ([]report.ToolUsage, error) {
	cond, args := where(f, "t.ts")
	return toolUsage(r.q, "usage_tool_calls", cond, args)
}

// toolUsage aggregates the calls of view (aliased t) that cond selects, most used first.
func toolUsage(q querier, view, cond string, args []any) ([]report.ToolUsage, error) {
	rows, err := q.Query(`SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
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
