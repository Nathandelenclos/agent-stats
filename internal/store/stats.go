package store

import "strings"

// Filter restricts queries to a time window (unix ms, 0 = unbounded) and optionally one harness.
type Filter struct {
	Since, Until int64
	Harness      string
}

func (f Filter) where(tsCol string) (string, []any) {
	var conds []string
	var args []any
	if f.Since > 0 {
		conds, args = append(conds, tsCol+" >= ?"), append(args, f.Since)
	}
	if f.Until > 0 {
		conds, args = append(conds, tsCol+" < ?"), append(args, f.Until)
	}
	if f.Harness != "" {
		conds, args = append(conds, "s.harness = ?"), append(args, f.Harness)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Usage aggregates model requests.
type Usage struct {
	Requests      int64   `json:"requests"`
	Failed        int64   `json:"failed"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheRead     int64   `json:"cacheRead"`
	CacheWrite    int64   `json:"cacheWrite"`
	Cost          float64 `json:"cost"`
	Unpriced      int64   `json:"unpriced"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	AvgTTFTMs     float64 `json:"avgTtftMs"`
	CacheRate     float64 `json:"cacheRate"` // share of prompt tokens served from cache
	FirstTS       int64   `json:"firstTs"`
	LastTS        int64   `json:"lastTs"`
}

// Row is a Usage grouped under a key (model, harness, folder…).
type Row struct {
	Key string `json:"key"`
	Usage
}

// COALESCE keeps an empty selection scannable as zeros.
const usageCols = `COUNT(*), COUNT(r.error), COALESCE(SUM(r.input), 0), COALESCE(SUM(r.output), 0),
	COALESCE(SUM(r.cache_read), 0), COALESCE(SUM(r.cache_write), 0), COALESCE(SUM(r.cost), 0), COALESCE(SUM(1 - r.priced), 0),
	COALESCE(AVG(r.duration_ms), 0), COALESCE(AVG(r.ttft_ms), 0), COALESCE(MIN(r.ts), 0), COALESCE(MAX(r.ts), 0)`

func (u *Usage) scanArgs() []any {
	return []any{&u.Requests, &u.Failed, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite,
		&u.Cost, &u.Unpriced, &u.AvgDurationMs, &u.AvgTTFTMs, &u.FirstTS, &u.LastTS}
}

func (u *Usage) finish() {
	if prompt := u.Input + u.CacheRead + u.CacheWrite; prompt > 0 {
		u.CacheRate = float64(u.CacheRead) / float64(prompt)
	}
}

// UsageBy groups requests by a SQL expression over r (requests) and s (sessions), most expensive first.
func (st *Store) UsageBy(f Filter, groupExpr string) ([]Row, error) {
	where, args := f.where("r.ts")
	rows, err := st.DB.Query(`SELECT `+groupExpr+`, `+usageCols+`
		FROM requests r JOIN sessions s ON s.id = r.session_id`+where+`
		GROUP BY 1 ORDER BY SUM(r.cost) DESC, COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(append([]any{&r.Key}, r.scanArgs()...)...); err != nil {
			return nil, err
		}
		r.finish()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Total aggregates every request matching f.
func (st *Store) Total(f Filter) (Usage, error) {
	where, args := f.where("r.ts")
	var u Usage
	err := st.DB.QueryRow(`SELECT `+usageCols+` FROM requests r JOIN sessions s ON s.id = r.session_id`+where, args...).Scan(u.scanArgs()...)
	u.finish()
	return u, err
}

// ToolRow aggregates calls to one tool.
type ToolRow struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"` // MCP server, empty for built-in tools
	Calls       int64   `json:"calls"`
	Errors      int64   `json:"errors"`
	ArgsChars   int64   `json:"argsChars"`
	ResultChars int64   `json:"resultChars"`
	AvgMs       float64 `json:"avgMs"`
	LastTS      int64   `json:"lastTs"`
}

// Tools aggregates tool calls, most used first.
func (st *Store) Tools(f Filter) ([]ToolRow, error) {
	where, args := f.where("t.ts")
	rows, err := st.DB.Query(`SELECT t.name, t.server, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts)
		FROM tool_calls t JOIN sessions s ON s.id = t.session_id`+where+`
		GROUP BY 1, 2 ORDER BY 3 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolRow
	for rows.Next() {
		var r ToolRow
		if err := rows.Scan(&r.Name, &r.Server, &r.Calls, &r.Errors, &r.ArgsChars, &r.ResultChars, &r.AvgMs, &r.LastTS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
