package sqlite

import (
	"fmt"
	"strings"

	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// where turns a filter into SQL conditions; every value is a bound parameter.
func where(f report.Filter, tsCol string) (string, []any) {
	var conds []string
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, tsCol+" >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if f.Harness != "" {
		conds, args = append(conds, "s.harness = ?"), append(args, string(f.Harness))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// groupColumns is the closed list of SQL expressions a usage query may group by.
var groupColumns = map[report.Dimension]string{
	report.ByHarness:   "s.harness",
	report.ByModel:     "r.model",
	report.ByFolder:    "COALESCE(s.folder, '')",
	report.ByAgentType: "s.agent_type",
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
		&r.u.CostUSD, &r.u.Unpriced, &r.u.AvgDurationMs, &r.u.AvgTTFTMs, &r.first, &r.last}
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
func (s *SQLiteIndex) UsageBy(f report.Filter, d report.Dimension) ([]report.UsageGroup, error) {
	col, ok := groupColumns[d]
	if !ok {
		return nil, fmt.Errorf("unknown usage dimension %d", d)
	}
	cond, args := where(f, "r.ts")
	rows, err := s.db.Query(`SELECT `+col+`, `+usageCols+`
		FROM requests r JOIN sessions s ON s.id = r.session_id`+cond+`
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
func (s *SQLiteIndex) Total(f report.Filter) (report.Usage, error) {
	cond, args := where(f, "r.ts")
	var row usageRow
	err := s.db.QueryRow(`SELECT `+usageCols+` FROM requests r JOIN sessions s ON s.id = r.session_id`+cond, args...).Scan(row.args()...)
	return row.usage(), err
}

// Tools aggregates tool calls, most used first.
func (s *SQLiteIndex) Tools(f report.Filter) ([]report.ToolUsage, error) {
	cond, args := where(f, "t.ts")
	rows, err := s.db.Query(`SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts)
		FROM tool_calls t JOIN sessions s ON s.id = t.session_id`+cond+`
		GROUP BY 1, 2 ORDER BY 3 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.ToolUsage
	for rows.Next() {
		var r report.ToolUsage
		var last int64
		if err := rows.Scan(&r.Name, &r.Server, &r.Calls, &r.Errors, &r.ArgsChars, &r.ResultChars, &r.AvgMs, &last); err != nil {
			return nil, err
		}
		r.Last = fromMillis(last)
		out = append(out, r)
	}
	return out, rows.Err()
}
