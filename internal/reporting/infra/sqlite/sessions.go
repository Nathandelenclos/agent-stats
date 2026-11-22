package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SQLiteSessions reads sessions from the session_* views of the SQLite index. It shares the connection of the
// SQLiteUsage it comes from, and each question it answers reads one state of the index.
type SQLiteSessions struct{ db *sql.DB }

var (
	_ reporting.SessionQueries      = (*SQLiteSessions)(nil)
	_ reporting.SessionPromptQuery  = (*SQLiteSessions)(nil)
	_ reporting.RelatedSessionQuery = (*SQLiteSessions)(nil)
)

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

// A run is what a row of the listing stands for: a root session (a main session, or a subagent whose parent was never
// indexed) and the subagents below it. Their time span is the run's: from the first start to the last activity of any
// of them. Inside a project a run holds only the sessions that worked in the project, so that the sessions add up to
// what the report of the project counts; a subagent that worked in the project whose root did not stands for a run of
// its own. Without a project a run is the root and all its subagents, as the index's session_overview places them.
//
// runs is the WITH clause that gives the queries two tables: unit, each session with the run it belongs
// to and its own metadata, and run, the harness and time span of each run keyed by its head session.
type runs struct {
	with string
	args []any
}

const runTable = `run(id, harness, started, ended) AS (
	SELECT u.run_id, h.harness, COALESCE(MIN(NULLIF(o.started_at, 0)), 0), COALESCE(MAX(o.ended_at), 0)
	FROM unit u JOIN session_overview o ON o.id = u.id JOIN session_overview h ON h.id = u.run_id
	GROUP BY u.run_id, h.harness)`

// runsOf places the sessions of the index, or the ones that worked in project p, in runs.
func runsOf(q querier, p kernel.Project) (runs, error) {
	if p.IsZero() { // the view already spans each run
		return runs{with: `WITH unit(id, run_id, harness, agent_type, folder) AS (SELECT id, root_id, harness, agent_type, folder FROM session_overview),
			run(id, harness, started, ended) AS (SELECT id, harness, run_started_at, run_ended_at FROM session_overview WHERE id = root_id)`}, nil
	}
	list, err := projectfolders.List(q, p)
	if err != nil {
		return runs{}, err
	}
	return runs{`WITH unit(id, run_id, harness, agent_type, folder) AS (
		SELECT o.id, CASE WHEN h.folder IN (SELECT value FROM json_each(?)) THEN o.root_id ELSE o.id END, o.harness, o.agent_type, o.folder
		FROM session_overview o JOIN session_overview h ON h.id = o.root_id
		WHERE o.folder IN (SELECT value FROM json_each(?))), ` + runTable, []any{list, list}}, nil
}

// sessionCols selects the listed sessions, the head of each run. The prompt figures count the session's own prompts,
// not the briefs it sent to its subagents.
const sessionCols = `SELECT s.id, s.harness, s.agent_type, COALESCE(s.title, ''), COALESCE(s.folder, ''), run.started, run.ended,
	(SELECT COUNT(*) FROM unit c WHERE c.run_id = s.id AND c.id <> s.id),
	(SELECT COUNT(*) FROM session_prompts p WHERE p.session_id = s.id),
	(SELECT COALESCE(SUM(p.chars), 0) FROM session_prompts p WHERE p.session_id = s.id)
	FROM run JOIN session_overview s ON s.id = run.id WHERE 1 = 1`

// Sessions lists the sessions whose last activity is in the filter's range, most recent first.
func (s *SQLiteSessions) Sessions(ctx context.Context, f report.Filter) (out []report.SessionSummary, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, f.Project)
		if err != nil {
			return err
		}
		cond, args := rangeCondition(f)
		if out, err = sessionRows(q, r, cond, args); err != nil {
			return err
		}
		// The usage queries narrow to the same runs with a subquery, so a short range stays cheap.
		return addUsage(q, out, r, `u.run_id IN (SELECT run.id FROM run WHERE 1 = 1`+cond+`)`, args)
	})
	return out, err
}

// rangeCondition narrows the runs (aliased run) to the filter's period and harness, as " AND ..." conditions; every
// value is a bound parameter. The project is not here: it decides what a run holds.
func rangeCondition(f report.Filter) (cond string, args []any) {
	if !f.Since.IsZero() {
		cond, args = cond+" AND run.ended >= ?", append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		cond, args = cond+" AND run.harness = ?", append(args, f.Harness.String())
	}
	return cond, args
}

// Session describes one session of the listing of project p (of every project for the zero Project).
func (s *SQLiteSessions) Session(ctx context.Context, id report.SessionID, p kernel.Project) (d report.SessionDetail, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, p)
		if err != nil {
			return err
		}
		head := []any{id.String()}
		rows, err := sessionRows(q, r, " AND run.id = ?", head)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return report.ErrSessionNotFound
		}
		if err = addUsage(q, rows, r, "u.run_id = ?", head); err != nil {
			return err
		}
		d.SessionSummary = rows[0]
		if d.Tools, err = toolUsage(q, r.with, "session_tool_calls", " WHERE t.session_id IN (SELECT u.id FROM unit u WHERE u.run_id = ?)", append(slices.Clone(r.args), head...)); err != nil {
			return err
		}
		slices.SortStableFunc(d.Tools, func(a, b report.ToolUsage) int { // the query leaves equal counts in any order
			return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Name, b.Name), strings.Compare(a.Server, b.Server))
		})
		if d.Subagents, err = subagents(q, r, id); err != nil {
			return err
		}
		if d.Harness == kernel.Codex && d.AgentType == "main" {
			err = q.QueryRow(r.with+` SELECT COALESCE((
				SELECT parent.id FROM session_lineage l JOIN unit parent ON parent.id = l.parent_id
				WHERE l.id = ?), '')`, append(slices.Clone(r.args), head...)...).Scan(&d.ForkedFrom)
			if err != nil {
				return err
			}
		}
		d.Prompts, err = prompts(q, id)
		return err
	})
	return d, err
}

// sessionRows reads the listed sessions of runs that cond narrows to, most recent first, without their usage.
func sessionRows(q querier, r runs, cond string, args []any) ([]report.SessionSummary, error) {
	rows, err := q.Query(r.with+" "+sessionCols+cond+` ORDER BY run.ended DESC, run.started DESC, s.id`, append(slices.Clone(r.args), args...)...)
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

const runRequests = "session_requests r JOIN unit u ON u.id = r.session_id"

// addUsage fills the usage of each session from the requests of its run, and the models it used.
// cond selects the runs (aliased u, by u.run_id) whose requests (aliased r) count.
func addUsage(q querier, sessions []report.SessionSummary, r runs, cond string, args []any) error {
	at := make(map[string]int, len(sessions))
	for i, s := range sessions {
		at[s.ID.String()] = i
	}
	args = append(slices.Clone(r.args), args...)
	if err := usageGroups(q, r.with, "u.run_id", runRequests, cond, args, func(keys []string, u report.Usage) {
		if i, ok := at[keys[0]]; ok {
			sessions[i].Usage = u
		}
	}); err != nil {
		return err
	}
	return usageGroups(q, r.with, "u.run_id, r.model", runRequests, cond, args, func(keys []string, u report.Usage) {
		if i, ok := at[keys[0]]; ok {
			sessions[i].Models = append(sessions[i].Models, report.UsageGroup{Group: keys[1], Usage: u})
		}
	})
}

// usageGroups aggregates the requests of from (a table of requests aliased r) that cond selects, grouped by the columns
// keys names, most expensive first; fn gets each group's key values and usage. with, when it is not empty, is a WITH clause
// from and cond may use.
func usageGroups(q querier, with, keys, from, cond string, args []any, fn func(keys []string, u report.Usage)) error {
	rows, err := q.Query(with+` SELECT `+keys+`, `+usageCols+`
		FROM `+from+` WHERE `+cond+`
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

// subagents lists the subagents of the run the session heads, most expensive first.
func subagents(q querier, r runs, root report.SessionID) ([]report.SubagentUsage, error) {
	out, err := subagentRows(q, r, root)
	if err != nil || len(out) == 0 {
		return out, err
	}
	at := make(map[string]int, len(out))
	for i, agent := range out {
		at[agent.ID.String()] = i
	}
	args := append(slices.Clone(r.args), root.String())
	const children = "u.run_id = ? AND r.session_id <> u.run_id"
	if err := usageGroups(q, r.with, "r.session_id", runRequests, children, args, func(keys []string, u report.Usage) {
		out[at[keys[0]]].Usage = u
	}); err != nil {
		return nil, err
	}
	models, err := q.Query(r.with+` SELECT DISTINCT r.session_id, r.model FROM `+runRequests+` WHERE `+children+` ORDER BY r.model`, args...)
	if err != nil {
		return nil, err
	}
	defer models.Close()
	for models.Next() {
		var id, model string
		if err := models.Scan(&id, &model); err != nil {
			return nil, err
		}
		if model != "" {
			agent := &out[at[id]]
			agent.Models = append(agent.Models, model)
		}
	}
	slices.SortStableFunc(out, func(a, b report.SubagentUsage) int {
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Requests, a.Requests), strings.Compare(a.Title, b.Title))
	})
	return out, models.Err()
}

// subagentRows publishes a parent only when both endpoints belong to the selected run.
func subagentRows(q querier, r runs, root report.SessionID) ([]report.SubagentUsage, error) {
	rows, err := q.Query(r.with+` SELECT o.id, COALESCE(parent.id, ''), o.agent_type, COALESCE(o.title, ''), COALESCE(o.started_at, 0), COALESCE(o.ended_at, 0)
		FROM session_overview o JOIN unit u ON u.id = o.id
		LEFT JOIN session_lineage l ON l.id = o.id
		LEFT JOIN unit parent ON parent.id = l.parent_id AND parent.run_id = u.run_id
		WHERE u.run_id = ? AND o.id <> u.run_id ORDER BY o.id`, append(slices.Clone(r.args), root.String())...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.SubagentUsage
	for rows.Next() {
		var id, parent string
		var start, end int64
		var x report.SubagentUsage
		if err := rows.Scan(&id, &parent, &x.AgentType, &x.Title, &start, &end); err != nil {
			return nil, err
		}
		if x.ID, err = report.NewSessionID(id); err != nil {
			return nil, err
		}
		if parent != "" {
			x.ParentID, _ = report.NewSessionID(parent)
		}
		x.Start, x.End = fromMillis(start), fromMillis(end)
		out = append(out, x)
	}
	return out, rows.Err()
}

// SessionPrompt opens a single prompt from a session in the project's listing.
func (s *SQLiteSessions) SessionPrompt(ctx context.Context, id report.SessionID, position int64, project kernel.Project) (p report.Prompt, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, project)
		if err != nil {
			return err
		}
		row := q.QueryRow(r.with+` SELECT p.text, `+promptCols+`
			FROM session_prompts p JOIN run ON run.id = p.session_id
			WHERE p.session_id = ? AND p.position = ?`, append(slices.Clone(r.args), id.String(), position)...)
		p, err = scanPrompt(row)
		if err == sql.ErrNoRows {
			return report.ErrPromptNotFound
		}
		return err
	})
	return p, err
}

const promptCols = `p.position, p.ts, p.chars, p.words, p.yelling, p.profanity, p.anguish, p.negation, p.repetition, p.blame`

// prompts lists the session's own prompts in log order, each cut to report.PromptTextLimit characters.
func prompts(q querier, id report.SessionID) ([]report.Prompt, error) {
	rows, err := q.Query(`SELECT substr(p.text, 1, ?), `+promptCols+`
		FROM session_prompts p WHERE p.session_id = ? ORDER BY p.position`, report.PromptTextLimit, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.Prompt
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPrompt(row interface{ Scan(...any) error }) (p report.Prompt, err error) {
	var ts, repeated int64
	err = row.Scan(&p.Text, &p.Position, &ts, &p.Chars, &p.Words, &p.Signals.Yelling, &p.Signals.Profanity, &p.Signals.Anguish,
		&p.Signals.Negation, &repeated, &p.Signals.Blame)
	p.At, p.Signals.Repeated = fromMillis(ts), repeated != 0
	return p, err
}
