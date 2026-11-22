package sqlite

import (
	"database/sql"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// SQLitePrompts reads the prompts the user typed from the session_* views of the SQLite index. It shares the
// connection of the SQLiteUsage it comes from.
type SQLitePrompts struct{ db *sql.DB }

var _ reporting.PromptQueries = (*SQLitePrompts)(nil)

// Prompts gives access to the prompts of the same index.
func (s *SQLiteUsage) Prompts() *SQLitePrompts { return &SQLitePrompts{s.db} }

// FlaggedPrompts lists the prompts typed in the filter's range that show a signal, most recent first. One
// statement reads one state of the index. The prompts are the user's own: those of a session that is a root of the
// listing, not the briefs the run's subagents received. Each text is cut to report.PromptTextLimit characters.
func (s *SQLitePrompts) FlaggedPrompts(f report.Filter) ([]report.FlaggedPrompt, error) {
	cond, args := `(p.yelling + p.profanity + p.anguish + p.negation + p.blame > 0 OR p.repetition <> 0)`, []any{report.PromptTextLimit}
	if !f.Since.IsZero() {
		cond, args = cond+" AND p.ts >= ?", append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		cond, args = cond+" AND o.harness = ?", append(args, f.Harness.String())
	}
	rows, err := s.db.Query(`SELECT p.session_id, o.harness, COALESCE(o.title, ''), COALESCE(o.folder, ''), p.ts, substr(p.text, 1, ?),
			p.yelling, p.profanity, p.anguish, p.negation, p.repetition, p.blame
		FROM session_prompts p JOIN session_overview o ON o.id = p.session_id AND o.id = o.root_id
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
