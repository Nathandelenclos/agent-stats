// Package sqlite answers the reporting context's usage queries from the SQLite index that the indexing
// context writes. It opens the index read-only and reads only the usage_* views, the index's published read model.
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

// SQLiteUsage reads usage from the SQLite index.
type SQLiteUsage struct {
	db *sql.DB
}

var _ reporting.UsageQueries = (*SQLiteUsage)(nil)

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
	return &SQLiteUsage{db: db}, nil
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

// UsageBy groups requests along d, most expensive first.
func (s *SQLiteUsage) UsageBy(f report.Filter, d report.Dimension) ([]report.UsageGroup, error) {
	col, ok := groupColumns[d]
	if !ok {
		return nil, fmt.Errorf("unknown usage dimension %d", d)
	}
	cond, args := where(f, "r.ts")
	rows, err := s.db.Query(`SELECT `+col+`, `+usageCols+`
		FROM usage_requests r`+cond+`
		GROUP BY 1 ORDER BY SUM(r.cost) DESC, COUNT(*) DESC`, args...)
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
func (s *SQLiteUsage) Total(f report.Filter) (report.Usage, error) {
	cond, args := where(f, "r.ts")
	var row usageRow
	err := s.db.QueryRow(`SELECT `+usageCols+` FROM usage_requests r`+cond, args...).Scan(row.args()...)
	return row.usage(), err
}

// Tools aggregates tool calls, most used first.
func (s *SQLiteUsage) Tools(f report.Filter) ([]report.ToolUsage, error) {
	cond, args := where(f, "t.ts")
	rows, err := s.db.Query(`SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts)
		FROM usage_tool_calls t`+cond+`
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
