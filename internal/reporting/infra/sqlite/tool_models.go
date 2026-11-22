package sqlite

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// ToolsByModel counts the calls that the Pi readers place at their invoking request's timestamp.
func (s *SQLiteSessions) ToolsByModel(ctx context.Context, f report.Filter) (out []report.ToolModelUsage, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		condition := "o.harness IN ('omp', 'pi') AND r.ts > 0 AND (r.purpose = '' OR r.purpose = 'discarded-turn')"
		var args []any
		if !f.Since.IsZero() {
			condition += " AND r.ts >= ?"
			args = append(args, kernel.UnixMilli(f.Since))
		}
		if !f.Harness.IsZero() {
			condition += " AND o.harness = ?"
			args = append(args, f.Harness.String())
		}
		projectCond, projectArgs, err := projectfolders.Condition(q, f.Project, "o.folder")
		if err != nil {
			return err
		}
		if projectCond != "" {
			condition += " AND " + projectCond
			args = append(args, projectArgs...)
		}

		// Discard ambiguous matches. Split the invoking request's usage evenly across its recorded tool calls.
		rows, err := q.Query(`WITH requests AS (
			SELECT r.session_id, r.ts, MAX(r.model) AS model, MAX(r.provider) AS provider,
				MAX(r.input + r.output + r.cache_read + r.cache_write) AS tokens,
				MAX(CASE WHEN r.priced = 1 THEN r.cost END) AS cost
			FROM session_requests r JOIN session_overview o ON o.id = r.session_id
			WHERE `+condition+`
			GROUP BY r.session_id, r.ts HAVING COUNT(*) = 1
		), shares AS (
			SELECT t.name, t.server, r.model, r.provider, t.is_error, r.tokens, r.cost,
				COUNT(*) OVER (PARTITION BY r.session_id, r.ts) AS calls
			FROM requests r JOIN session_tool_calls t ON (t.session_id, t.ts) = (r.session_id, r.ts)
		)
		SELECT name, server, model, provider, COUNT(*), SUM(is_error), SUM(1.0 * tokens / calls),
			COALESCE(SUM(cost / calls), 0), COUNT(cost)
		FROM shares GROUP BY name, server, model, provider
		ORDER BY COUNT(*) DESC, name, server, model, provider`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row report.ToolModelUsage
			if err := rows.Scan(&row.Tool, &row.Server, &row.Model, &row.Provider, &row.Calls, &row.Errors, &row.Tokens, &row.Cost, &row.PricedCalls); err != nil {
				return err
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	return out, err
}
