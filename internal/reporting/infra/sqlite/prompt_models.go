package sqlite

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// ModelSignals groups a user's prompts by the last observed conversational model in that session.
func (s *SQLitePrompts) ModelSignals(ctx context.Context, f report.Filter) ([]report.ModelSignals, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := scoped{ctx, tx}
	condition := "1 = 1"
	var args []any
	if !f.Since.IsZero() {
		condition += " AND p.ts >= ?"
		args = append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		condition += " AND o.harness = ?"
		args = append(args, f.Harness.String())
	}
	projectCond, projectArgs, err := projectfolders.Condition(q, f.Project, "o.folder")
	if err != nil {
		return nil, err
	}
	if projectCond != "" {
		condition += " AND " + projectCond
		args = append(args, projectArgs...)
	}

	rows, err := q.Query(`SELECT p.text, (p.yelling + p.profanity + p.anguish + p.negation + p.blame > 0 OR p.repetition <> 0),
		COALESCE((SELECT r.provider || char(0) || r.model FROM session_requests r
			WHERE r.session_id = p.session_id AND r.ts > 0 AND r.ts < p.ts AND r.model <> ''
			AND (r.purpose = '' OR r.purpose = 'discarded-turn')
			ORDER BY r.ts DESC, r.key DESC LIMIT 1), '')
		FROM session_prompts p JOIN session_overview o ON o.id = p.session_id AND o.id = o.root_id AND o.agent_type = 'main'
		WHERE `+condition, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]report.ModelSignals{}
	for rows.Next() {
		var text, key string
		var flagged bool
		if err := rows.Scan(&text, &flagged, &key); err != nil {
			return nil, err
		}
		if hasAgentEnvelope(text) {
			continue
		}
		g := groups[key]
		g.Provider, g.Model, _ = strings.Cut(key, "\x00")
		g.Prompts++
		if flagged {
			g.Flagged++
		}
		groups[key] = g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]report.ModelSignals, 0, len(groups))
	for _, group := range groups {
		out = append(out, group)
	}
	slices.SortFunc(out, func(a, b report.ModelSignals) int {
		return cmp.Or(cmp.Compare(b.Flagged, a.Flagged), strings.Compare(a.Provider, b.Provider), strings.Compare(a.Model, b.Model))
	})
	return out, nil
}
