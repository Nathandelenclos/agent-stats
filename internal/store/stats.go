package store

import (
	"fmt"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/app"
	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

// where turns a filter into SQL conditions; every value is a bound parameter.
func where(f app.Filter, tsCol string) (string, []any) {
	var conds []string
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, tsCol+" >= ?"), append(args, usage.UnixMilli(f.Since))
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
var groupColumns = map[app.Dimension]string{
	app.ByHarness:   "s.harness",
	app.ByModel:     "r.model",
	app.ByFolder:    "COALESCE(s.folder, '')",
	app.ByAgentType: "s.agent_type",
}

// COALESCE keeps an empty selection scannable as zeros.
const usageCols = `COUNT(*), COUNT(r.error), COALESCE(SUM(r.input), 0), COALESCE(SUM(r.output), 0),
	COALESCE(SUM(r.cache_read), 0), COALESCE(SUM(r.cache_write), 0), COALESCE(SUM(r.cost), 0), COALESCE(SUM(1 - r.priced), 0),
	COALESCE(AVG(r.duration_ms), 0), COALESCE(AVG(r.ttft_ms), 0), COALESCE(MIN(r.ts), 0), COALESCE(MAX(r.ts), 0)`

func scanArgs(u *app.Usage) []any {
	return []any{&u.Requests, &u.Failed, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite,
		&u.Cost, &u.Unpriced, &u.AvgDurationMs, &u.AvgTTFTMs, &u.FirstTS, &u.LastTS}
}

// UsageBy groups requests along d, most expensive first.
func (s *SQLiteIndex) UsageBy(f app.Filter, d app.Dimension) ([]app.UsageGroup, error) {
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
	var out []app.UsageGroup
	for rows.Next() {
		var g app.UsageGroup
		if err := rows.Scan(append([]any{&g.Group}, scanArgs(&g.Usage)...)...); err != nil {
			return nil, err
		}
		g.Usage = g.WithCacheRate()
		out = append(out, g)
	}
	return out, rows.Err()
}

// Total aggregates every request matching f.
func (s *SQLiteIndex) Total(f app.Filter) (app.Usage, error) {
	cond, args := where(f, "r.ts")
	var u app.Usage
	err := s.db.QueryRow(`SELECT `+usageCols+` FROM requests r JOIN sessions s ON s.id = r.session_id`+cond, args...).Scan(scanArgs(&u)...)
	return u.WithCacheRate(), err
}

// Tools aggregates tool calls, most used first.
func (s *SQLiteIndex) Tools(f app.Filter) ([]app.ToolUsage, error) {
	cond, args := where(f, "t.ts")
	rows, err := s.db.Query(`SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts)
		FROM tool_calls t JOIN sessions s ON s.id = t.session_id`+cond+`
		GROUP BY 1, 2 ORDER BY 3 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ToolUsage
	for rows.Next() {
		var r app.ToolUsage
		if err := rows.Scan(&r.Name, &r.Server, &r.Calls, &r.Errors, &r.ArgsChars, &r.ResultChars, &r.AvgMs, &r.LastTS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
