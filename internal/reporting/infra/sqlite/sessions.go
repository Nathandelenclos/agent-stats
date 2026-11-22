package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SQLiteSessions reads sessions from the session_* views of the SQLite index. It shares the connection of the
// SQLiteUsage it comes from, and each question it answers reads one state of the index.
type SQLiteSessions struct{ db *sql.DB }

var _ reporting.SessionQueries = (*SQLiteSessions)(nil)

// Sessions gives access to the sessions of the same index.
func (s *SQLiteUsage) Sessions() *SQLiteSessions { return &SQLiteSessions{s.db} }

// snapshot runs fn inside one read transaction, so the several queries of one answer agree with each other.
func (s *SQLiteSessions) snapshot(ctx context.Context, fn func(querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // nothing was written: ending the transaction is all it takes
	return fn(scoped{ctx, tx})
}

// sessionCols selects the listed sessions: a session, or a subagent whose parent was never indexed (a root).
// Their time span is the run's: from the session's start to the last activity of any of its subagents.
// The prompt figures count the session's own prompts, not the briefs it sent to its subagents.
const sessionCols = `SELECT s.id, s.harness, s.agent_type, COALESCE(s.title, ''), COALESCE(s.folder, ''), s.run_started_at, s.run_ended_at,
	(SELECT COUNT(*) FROM session_overview c WHERE c.root_id = s.id AND c.id <> s.id),
	(SELECT COUNT(*) FROM session_prompts p WHERE p.session_id = s.id),
	(SELECT COALESCE(SUM(p.chars), 0) FROM session_prompts p WHERE p.session_id = s.id)
	FROM session_overview s WHERE s.id = s.root_id`

// Sessions lists the sessions whose last activity is in the filter's range, most recent first.
func (s *SQLiteSessions) Sessions(ctx context.Context, f report.Filter) (out []report.SessionSummary, err error) {
	var cond string // narrows the roots (aliased s); every value is a bound parameter
	var args []any
	if !f.Since.IsZero() {
		cond, args = cond+" AND s.run_ended_at >= ?", append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		cond, args = cond+" AND s.harness = ?", append(args, f.Harness.String())
	}
	err = s.snapshot(ctx, func(q querier) error {
		var err error
		if out, err = sessionRows(q, cond, args); err != nil {
			return err
		}
		// The usage queries narrow to the same sessions with a subquery, so a short range stays cheap.
		return addUsage(q, out, `r.root_id IN (SELECT s.id FROM session_overview s WHERE s.id = s.root_id`+cond+`)`, args)
	})
	return out, err
}

// Session describes one session of the listing.
func (s *SQLiteSessions) Session(ctx context.Context, id report.SessionID) (d report.SessionDetail, err error) {
	root := []any{id.String()}
	err = s.snapshot(ctx, func(q querier) error {
		rows, err := sessionRows(q, " AND s.id = ?", root)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return report.ErrSessionNotFound
		}
		if err = addUsage(q, rows, "r.root_id = ?", root); err != nil {
			return err
		}
		d.SessionSummary = rows[0]
		if d.Tools, err = toolUsage(q, "session_tool_calls", " WHERE t.root_id = ?", root); err != nil {
			return err
		}
		slices.SortStableFunc(d.Tools, func(a, b report.ToolUsage) int { // the query leaves equal counts in any order
			return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Name, b.Name), strings.Compare(a.Server, b.Server))
		})
		if d.Subagents, err = subagents(q, id); err != nil {
			return err
		}
		d.Prompts, err = prompts(q, id)
		return err
	})
	return d, err
}

// sessionRows reads the listed sessions that cond narrows to, most recent first, without their usage.
func sessionRows(q querier, cond string, args []any) ([]report.SessionSummary, error) {
	rows, err := q.Query(sessionCols+cond+` ORDER BY s.run_ended_at DESC, s.run_started_at DESC, s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.SessionSummary
	for rows.Next() {
		var id, harness string
		var x report.SessionSummary
		var start, end int64
		if err := rows.Scan(&id, &harness, &x.AgentType, &x.Title, &x.Folder, &start, &end, &x.SubagentCount, &x.PromptCount, &x.PromptChars); err != nil {
			return nil, err
		}
		if x.ID, err = report.NewSessionID(id); err != nil {
			return nil, err
		}
		if x.Harness, err = kernel.ParseHarness(harness); err != nil {
			return nil, err
		}
		x.Start, x.End = fromMillis(start), fromMillis(end)
		out = append(out, x)
	}
	return out, rows.Err()
}

// addUsage fills the usage of each session from the requests of its run, and the models it used.
// roots selects the requests (aliased r) of those runs.
func addUsage(q querier, sessions []report.SessionSummary, roots string, args []any) error {
	at := make(map[string]int, len(sessions))
	for i, s := range sessions {
		at[s.ID.String()] = i
	}
	if err := usageGroups(q, "r.root_id", roots, args, func(keys []string, u report.Usage) {
		if i, ok := at[keys[0]]; ok {
			sessions[i].Usage = u
		}
	}); err != nil {
		return err
	}
	return usageGroups(q, "r.root_id, r.model", roots, args, func(keys []string, u report.Usage) {
		if i, ok := at[keys[0]]; ok {
			sessions[i].Models = append(sessions[i].Models, report.UsageGroup{Group: keys[1], Usage: u})
		}
	})
}

// usageGroups aggregates the requests of session_requests (aliased r) that cond selects, grouped by the columns
// keys names, most expensive first; fn gets each group's key values and usage.
func usageGroups(q querier, keys, cond string, args []any, fn func(keys []string, u report.Usage)) error {
	rows, err := q.Query(`SELECT `+keys+`, `+usageCols+`
		FROM session_requests r WHERE `+cond+`
		GROUP BY `+keys+` ORDER BY SUM(r.cost) DESC, COUNT(*) DESC, `+keys, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		k := make([]string, strings.Count(keys, ",")+1)
		var row usageRow
		dest := make([]any, 0, len(k)+12)
		for i := range k {
			dest = append(dest, &k[i])
		}
		if err := rows.Scan(append(dest, row.args()...)...); err != nil {
			return err
		}
		fn(k, row.usage())
	}
	return rows.Err()
}

// subagents lists the subagents the session started, most expensive first.
func subagents(q querier, root report.SessionID) ([]report.SubagentUsage, error) {
	ids, out, err := subagentRows(q, root)
	if err != nil {
		return nil, err
	}
	at := make(map[string]int, len(ids))
	for i, id := range ids {
		at[id] = i
	}
	err = usageGroups(q, "r.session_id", "r.root_id = ? AND r.session_id <> r.root_id", []any{root.String()}, func(keys []string, u report.Usage) {
		if i, ok := at[keys[0]]; ok {
			out[i].Usage = u
		}
	})
	slices.SortStableFunc(out, func(a, b report.SubagentUsage) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Requests, a.Requests), strings.Compare(a.Title, b.Title))
	})
	return out, err
}

// subagentRows reads the subagents of a session, with the identity the index gives each (needed to add up its requests).
func subagentRows(q querier, root report.SessionID) (ids []string, out []report.SubagentUsage, err error) {
	rows, err := q.Query(`SELECT o.id, o.agent_type, COALESCE(o.title, '') FROM session_overview o
		WHERE o.root_id = ? AND o.id <> o.root_id ORDER BY o.id`, root.String())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var x report.SubagentUsage
		if err := rows.Scan(&id, &x.AgentType, &x.Title); err != nil {
			return nil, nil, err
		}
		ids, out = append(ids, id), append(out, x)
	}
	return ids, out, rows.Err()
}

// prompts lists the session's own prompts in log order, each cut to report.PromptTextLimit characters.
func prompts(q querier, id report.SessionID) ([]report.Prompt, error) {
	rows, err := q.Query(`SELECT p.ts, substr(p.text, 1, ?), p.chars, p.words, p.yelling, p.profanity, p.anguish, p.negation, p.repetition, p.blame
		FROM session_prompts p WHERE p.session_id = ? ORDER BY p.position`, report.PromptTextLimit, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.Prompt
	for rows.Next() {
		var p report.Prompt
		var ts int64
		var repeated int
		if err := rows.Scan(&ts, &p.Text, &p.Chars, &p.Words, &p.Signals.Yelling, &p.Signals.Profanity, &p.Signals.Anguish,
			&p.Signals.Negation, &repeated, &p.Signals.Blame); err != nil {
			return nil, err
		}
		p.At, p.Signals.Repeated = fromMillis(ts), repeated != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
