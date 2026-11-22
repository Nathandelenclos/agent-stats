package sqlite

import (
	"context"
	"slices"

	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SessionTrace merges indexed model usage, tools and prompts for the selected run.
func (s *SQLiteSessions) SessionTrace(ctx context.Context, id report.SessionID, f report.Filter, limit, offset int) (out []report.TraceEvent, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, f.Project)
		if err != nil {
			return err
		}
		if err := r.checkVisible(q, id, f); err != nil {
			return err
		}
		query := r.with + `,
			selected AS (SELECT id, agent_type FROM unit WHERE run_id = ?),
			events(ts, kind, label, agent_id, agent_type, duration, failed) AS (
				SELECT p.ts, 'prompt', 'User prompt', a.id, a.agent_type, 0, 0
				FROM session_prompts p JOIN selected a ON a.id = p.session_id WHERE a.id = ?
				UNION ALL
				SELECT r.ts, 'model', r.model, a.id, a.agent_type, COALESCE(r.duration_ms, 0), r.error IS NOT NULL
				FROM session_requests r JOIN selected a ON a.id = r.session_id
				UNION ALL
				SELECT t.ts, 'tool', CASE WHEN t.server = '' THEN t.name ELSE t.server || ' / ' || t.name END,
					a.id, a.agent_type, COALESCE(t.duration_ms, 0), t.is_error
				FROM session_tool_calls t JOIN selected a ON a.id = t.session_id
			)
			SELECT ts, kind, label, agent_id, agent_type, duration, failed FROM events
			ORDER BY CASE WHEN ts > 0 THEN 0 ELSE 1 END, ts, agent_id,
				CASE kind WHEN 'prompt' THEN 0 WHEN 'model' THEN 1 ELSE 2 END, label, duration, failed
			LIMIT ? OFFSET ?`
		rows, err := q.Query(query, append(slices.Clone(r.args), id.String(), id.String(), limit, offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event report.TraceEvent
			var ts int64
			if err := rows.Scan(&ts, &event.Kind, &event.Label, &event.AgentID, &event.AgentType, &event.Duration, &event.Failed); err != nil {
				return err
			}
			event.At = fromMillis(ts)
			out = append(out, event)
		}
		return rows.Err()
	})
	return out, err
}
