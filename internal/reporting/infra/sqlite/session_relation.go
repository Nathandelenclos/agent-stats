package sqlite

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

const runTools = "session_tool_calls t JOIN unit u ON u.id = t.session_id"

func matchesToolCalls(kind report.RelationKind) bool {
	return kind == report.RelationTool || kind == report.RelationMCP
}

// RelatedSessions keeps each run's contributions to one row of the same filtered report.
func (s *SQLiteSessions) RelatedSessions(ctx context.Context, f report.Filter, rel report.SessionRelation) (out []report.SessionSummary, err error) {
	where, args, err := relationCondition(f, rel)
	if err != nil {
		return nil, err
	}
	err = s.snapshot(ctx, func(q querier) error {
		r, err := runsOf(q, f.Project)
		if err != nil {
			return err
		}
		from := runRequests
		if matchesToolCalls(rel.Kind) {
			from = runTools
		}
		out, err = sessionRows(q, r, " AND run.id IN (SELECT u.run_id FROM "+from+" WHERE "+where+")", args)
		if err != nil || len(out) == 0 {
			return err
		}
		if matchesToolCalls(rel.Kind) {
			return addRelatedTool(q, out, r, rel, where, args)
		}
		if err := addUsage(q, out, r, where, args); err != nil {
			return err
		}
		slices.SortStableFunc(out, func(a, b report.SessionSummary) int {
			return cmp.Or(cmp.Compare(b.Cost, a.Cost), cmp.Compare(b.Requests, a.Requests))
		})
		return nil
	})
	return out, err
}

// relationCondition filters the contributing rows themselves, since a run can span several days and folders.
func relationCondition(f report.Filter, rel report.SessionRelation) (string, []any, error) {
	var conds []string
	var args []any
	ts := "r.ts"
	if matchesToolCalls(rel.Kind) {
		ts = "t.ts"
	}
	if !f.Since.IsZero() {
		conds, args = append(conds, ts+" >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		conds, args = append(conds, "u.harness = ?"), append(args, f.Harness.String())
	}
	switch rel.Kind {
	case report.RelationDay:
		if rel.Day.IsZero() {
			return "", nil, fmt.Errorf("session relation requires a calendar day")
		}
		year, month, day := rel.Day.Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
		conds, args = append(conds, "r.ts > 0 AND r.ts >= ? AND r.ts < ?"), append(args, start.UnixMilli(), start.AddDate(0, 0, 1).UnixMilli())
	case report.RelationHarness:
		conds, args = append(conds, "u.harness = ?"), append(args, rel.Value)
	case report.RelationModel:
		conds, args = append(conds, "r.model = ?"), append(args, rel.Value)
	case report.RelationFolder:
		conds, args = append(conds, "COALESCE(u.folder, '') = ?"), append(args, rel.Value)
	case report.RelationAgent:
		conds, args = append(conds, "u.agent_type = ?"), append(args, rel.Value)
	case report.RelationTool:
		conds, args = append(conds, "t.name = ? AND t.server = ?"), append(args, rel.Value, rel.Server)
	case report.RelationMCP:
		if rel.Value == "" {
			return "", nil, fmt.Errorf("MCP relation requires a server")
		}
		names := rel.Servers
		if len(names) == 0 {
			names = []string{rel.Value}
		}
		conds = append(conds, "t.server IN (?"+strings.Repeat(",?", len(names)-1)+")")
		for _, name := range names {
			args = append(args, name)
		}
		if !rel.Harness.IsZero() {
			conds, args = append(conds, "u.harness = ?"), append(args, rel.Harness.String())
		}
	default:
		return "", nil, fmt.Errorf("unknown session relation %d", rel.Kind)
	}
	return strings.Join(conds, " AND "), args, nil
}

// addRelatedTool counts calls in each run without attributing model usage to the tool or server.
func addRelatedTool(q querier, sessions []report.SessionSummary, r runs, rel report.SessionRelation, where string, args []any) error {
	rows, err := q.Query(r.with+` SELECT u.run_id, COUNT(*), SUM(t.is_error), SUM(t.args_chars), SUM(t.result_chars),
		COALESCE(AVG(t.duration_ms), 0), MAX(t.ts) FROM `+runTools+` WHERE `+where+` GROUP BY u.run_id`, append(slices.Clone(r.args), args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	at := make(map[string]int, len(sessions))
	for i, s := range sessions {
		at[s.ID.String()] = i
	}
	identity := report.ToolUsage{Name: rel.Value, Server: rel.Server}
	if rel.Kind == report.RelationMCP {
		identity = report.ToolUsage{Server: rel.Value}
	}
	for rows.Next() {
		var id string
		var last int64
		tool := identity
		if err := rows.Scan(&id, &tool.Calls, &tool.Errors, &tool.ArgsChars, &tool.ResultChars, &tool.AvgDuration, &last); err != nil {
			return err
		}
		tool.Last = fromMillis(last)
		sessions[at[id]].RelatedTool = &tool
	}
	if err := rows.Err(); err != nil {
		return err
	}
	slices.SortStableFunc(sessions, func(a, b report.SessionSummary) int {
		return cmp.Compare(b.RelatedTool.Calls, a.RelatedTool.Calls)
	})
	return nil
}
