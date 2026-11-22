package sqlite

import (
	"context"
	"slices"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"

	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SessionRequests pages through the usage of a visible run, including its in-scope subagents.
func (s *SQLiteSessions) SessionRequests(ctx context.Context, id report.SessionID, f report.Filter, filters report.RequestFilter, limit, offset int) (out []report.SessionRequest, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, f.Project)
		if err != nil {
			return err
		}
		args := slices.Clone(r.args)
		condition := ""
		if id.String() != "" {
			if err := r.checkVisible(q, id, f); err != nil {
				return err
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
		if filters.FailedOnly {
			condition += " AND r.error IS NOT NULL"
		}
		if filters.Signature != "" {
			condition += " AND r.error IS NOT NULL AND agent_stats_error_key(r.error) = ?"
			args = append(args, filters.Signature)
		}
		if filters.Search != "" {
			condition += ` AND (instr(lower(r.model), lower(?)) > 0 OR instr(lower(r.provider), lower(?)) > 0
				OR instr(lower(COALESCE(u.folder, '')), lower(?)) > 0`
			args = append(args, filters.Search, filters.Search, filters.Search)
			if filters.SearchErrors {
				condition += " OR instr(lower(r.error), lower(?)) > 0"
				args = append(args, filters.Search)
			}
			condition += ")"
		}
		rows, err := q.Query(r.with+`
			SELECT u.run_id, COALESCE(u.folder, ''), r.ts, r.model, u.agent_type, r.input, r.output, r.cache_read, r.cache_write,
				r.cost, r.priced, COALESCE(r.duration_ms, 0), COALESCE(r.ttft_ms, 0), COALESCE(r.error, ''), r.provider, COALESCE(r.stop_reason, ''), r.purpose, r.service_tier, r.premium_units
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
				&item.TTFT, &item.Error, &item.Provider, &item.StopReason, &item.Purpose, &item.ServiceTier, &item.PremiumUnits); err != nil {
				return err
			}
			item.At = fromMillis(ts)
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
