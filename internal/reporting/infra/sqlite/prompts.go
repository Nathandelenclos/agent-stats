package sqlite

import (
	"database/sql"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SQLitePrompts reads the prompts the user typed from the session_* views of the SQLite index. It shares the
// connection of the SQLiteUsage it comes from.
type SQLitePrompts struct{ db *sql.DB }

var _ reporting.PromptQueries = (*SQLitePrompts)(nil)

// Prompts gives access to the prompts of the same index.
func (s *SQLiteUsage) Prompts() *SQLitePrompts { return &SQLitePrompts{s.db} }

// FlaggedPrompts lists the prompts typed in the filter's range that show a signal, most recent first.
// Only user prompts in main conversations are included. Full text is needed to group recurring asks.
func (s *SQLitePrompts) FlaggedPrompts(f report.Filter) ([]report.FlaggedPrompt, error) {
	var args []any
	cond := `(p.yelling + p.profanity + p.anguish + p.negation + p.blame > 0 OR p.repetition <> 0)`
	if !f.Since.IsZero() {
		cond, args = cond+" AND p.ts >= ?", append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		cond, args = cond+" AND o.harness = ?", append(args, f.Harness.String())
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	projectCondition, projectArgs, err := projectfolders.Condition(tx, f.Project, "o.folder")
	if err != nil {
		return nil, err
	}
	if projectCondition != "" {
		cond, args = cond+" AND "+projectCondition, append(args, projectArgs...)
	}
	rows, err := tx.Query(`SELECT p.session_id, o.harness, COALESCE(o.title, ''), COALESCE(o.folder, ''), p.ts, p.text,
			p.yelling, p.profanity, p.anguish, p.negation, p.repetition, p.blame
		FROM session_prompts p JOIN session_overview o ON o.id = p.session_id AND o.id = o.root_id AND o.agent_type = 'main'
		WHERE `+cond+` ORDER BY p.ts DESC, p.session_id, p.position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.FlaggedPrompt
	for rows.Next() {
		var id, harness string
		var ts int64
		var repeated int
		var x report.FlaggedPrompt
		if err := rows.Scan(&id, &harness, &x.Title, &x.Folder, &ts, &x.Text, &x.Signals.Yelling, &x.Signals.Profanity,
			&x.Signals.Anguish, &x.Signals.Negation, &repeated, &x.Signals.Blame); err != nil {
			return nil, err
		}
		if hasAgentEnvelope(x.Text) {
			continue
		}
		if x.Session, err = report.NewSessionID(id); err != nil {
			return nil, err
		}
		if x.Harness, err = kernel.ParseHarness(harness); err != nil {
			return nil, err
		}
		x.At, x.Signals.Repeated = fromMillis(ts), repeated != 0
		out = append(out, x)
	}
	return out, rows.Err()
}

// hasAgentEnvelope recognises the attribution T3 places before a relayed agent message.
func hasAgentEnvelope(text string) bool {
	line, _, _ := strings.Cut(text, "\n")
	thread, prefixed := strings.CutPrefix(strings.TrimSuffix(line, "\r"), `[T3 Code: the agent in T3 thread "`)
	id, suffixed := strings.CutSuffix(thread, `" sent this message, not the user.]`)
	return prefixed && suffixed && id != "" && !strings.ContainsRune(id, '"')
}
