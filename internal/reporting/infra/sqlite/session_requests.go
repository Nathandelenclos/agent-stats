package sqlite

import (
	"context"
	"slices"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"

	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SessionRequests pages through the usage of a visible run, including its in-scope subagents.
func (s *SQLiteSessions) SessionRequests(ctx context.Context, id report.SessionID, f report.Filter, failedOnly bool, limit, offset int) (out []report.SessionRequest, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, f.Project)
		if err != nil {
			return err
		}
		args := slices.Clone(r.args)
		condition := ""
		if id.String() != "" {
			cond, visibility := rangeCondition(f)
			visible, err := q.Query(r.with+" SELECT 1 FROM run WHERE 1=1"+cond+" AND run.id = ?",
				append(slices.Clone(r.args), append(visibility, id.String())...)...)
			if err != nil {
				return err
			}
			found := visible.Next()
			err = visible.Err()
			visible.Close()
			if err != nil {
				return err
			}
			if !found {
				return report.ErrSessionNotFound
			}
			condition = " AND u.run_id = ?"
			args = append(args, id.String())
		} else {
			if !f.Since.IsZero() {
				condition += " AND r.ts >= ?"
				args = append(args, kernel.UnixMilli(f.Since))
			}
			if !f.Harness.IsZero() {
				condition += " AND u.harness = ?"
				args = append(args, f.Harness.String())
			}
		}
		if failedOnly {
			condition += " AND r.error IS NOT NULL"
		}
		rows, err := q.Query(r.with+`
			SELECT u.run_id, COALESCE(u.folder, ''), r.ts, r.model, u.agent_type, r.input, r.output, r.cache_read, r.cache_write,
				r.cost, r.priced, COALESCE(r.duration_ms, 0), COALESCE(r.ttft_ms, 0), COALESCE(r.error, ''), r.provider, COALESCE(r.stop_reason, '')
			FROM session_requests r JOIN unit u ON u.id = r.session_id
			WHERE 1 = 1`+condition+`
			ORDER BY r.ts DESC, r.session_id, r.key
			LIMIT ? OFFSET ?`, append(args, limit, offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item report.SessionRequest
			var ts int64
			if err := rows.Scan(&item.SessionID, &item.Folder, &ts, &item.Model, &item.AgentType, &item.Input, &item.Output,
				&item.CacheRead, &item.CacheWrite, &item.Cost, &item.Priced, &item.Duration,
				&item.TTFT, &item.Error, &item.Provider, &item.StopReason); err != nil {
				return err
			}
			item.At = fromMillis(ts)
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
