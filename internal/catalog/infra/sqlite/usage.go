// Package sqlite answers the catalogue context's activity questions from the SQLite index that the indexing context
// writes. It opens the index read-only and reads only the usage_* views, the index's published read model.
package sqlite

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectscope"
)

// Usage reads activity from the SQLite index.
type Usage struct{ db *sql.DB }

var _ application.ActivityReader = (*Usage)(nil)

// OpenReadOnly opens the index at path for reading only.
func OpenReadOnly(path string) (*Usage, error) {
	abs, err := filepath.Abs(path) // a relative path would read as a URI authority
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Usage{db}, nil
}

// Close closes the database.
func (u *Usage) Close() error { return u.db.Close() }

// HasSessions reports whether a session of the index worked in the project's folder or below it.
func (u *Usage) HasSessions(p kernel.Project) (bool, error) {
	folders, err := projectscope.Folders(u.db, p)
	return len(folders) > 0, err
}

// where turns a filter into SQL conditions on the harness, timestamp and folder columns; every value is a bound parameter.
func where(q querier, f domain.Filter, extra ...string) (string, []any, error) {
	conds := extra
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, "ts >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		conds, args = append(conds, "harness = ?"), append(args, f.Harness.String())
	}
	cond, projectArgs, err := projectscope.Condition(q, f.Project, "folder")
	if err != nil {
		return "", nil, err
	}
	if cond != "" {
		conds, args = append(conds, cond), append(args, projectArgs...)
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

// Activity reads tool calls, agent types and skill loads in one read transaction, so that indexing committing meanwhile cannot
// make them describe two states of the index.
func (u *Usage) Activity(f domain.Filter) (domain.Activity, error) {
	tx, err := u.db.Begin()
	if err != nil {
		return domain.Activity{}, err
	}
	defer tx.Rollback() // nothing was written
	var a domain.Activity
	if a.Tools, err = toolActivity(tx, f); err != nil {
		return domain.Activity{}, err
	}
	if a.Agents, err = agentActivity(tx, f); err != nil {
		return domain.Activity{}, err
	}
	if a.Skills, err = skillActivity(tx, f); err != nil {
		return domain.Activity{}, err
	}
	if a.UnnamedSkillCalls, err = unnamedSkillCalls(tx, f); err != nil {
		return domain.Activity{}, err
	}
	if a.PendingSkillLoads, err = pendingSkillLoads(tx, f); err != nil {
		return domain.Activity{}, err
	}
	return a, nil
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// harnessOf parses a harness name the index holds; one it does not know is a corrupt row.
func harnessOf(name string) (kernel.Harness, error) {
	h, err := kernel.ParseHarness(name)
	if err != nil {
		return h, fmt.Errorf("index: %w", err)
	}
	return h, nil
}

func toolActivity(q querier, f domain.Filter) ([]domain.ToolActivity, error) {
	cond, args, err := where(q, f)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT harness, name, server, COUNT(*), MAX(ts) FROM usage_tool_calls`+cond+` GROUP BY 1, 2, 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ToolActivity
	for rows.Next() {
		var harness string
		var t domain.ToolActivity
		var last int64
		if err := rows.Scan(&harness, &t.Name, &t.Server, &t.Calls, &last); err != nil {
			return nil, err
		}
		if t.Harness, err = harnessOf(harness); err != nil {
			return nil, err
		}
		t.Last = fromMillis(last)
		out = append(out, t)
	}
	return out, rows.Err()
}

func agentActivity(q querier, f domain.Filter) ([]domain.AgentActivity, error) {
	cond, args, err := where(q, f)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT harness, agent_type, COUNT(*), MAX(ts) FROM usage_requests`+cond+` GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AgentActivity
	for rows.Next() {
		var harness string
		var g domain.AgentActivity
		var last int64
		if err := rows.Scan(&harness, &g.AgentType, &g.Requests, &last); err != nil {
			return nil, err
		}
		if g.Harness, err = harnessOf(harness); err != nil {
			return nil, err
		}
		g.Last = fromMillis(last)
		out = append(out, g)
	}
	return out, rows.Err()
}

// The skill column of usage_tool_calls tells three things apart on a Skill call (the index's published contract):
// a name is the skill loaded, "" is a call that named no valid skill, and NULL is a call whose log the index has not
// read again since it records skills. Every other call holds "".
func skillActivity(q querier, f domain.Filter) ([]domain.SkillActivity, error) {
	return skillCalls(q, f, "skill <> ''")
}

// unnamedSkillCalls counts, per harness, the Skill calls that named no valid skill; they come back with an empty Skill.
func unnamedSkillCalls(q querier, f domain.Filter) ([]domain.SkillActivity, error) {
	return skillCalls(q, f, "name = 'Skill'", "server = ''", "skill = ''")
}

func skillCalls(q querier, f domain.Filter, conds ...string) ([]domain.SkillActivity, error) {
	cond, args, err := where(q, f, conds...)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT harness, skill, COUNT(*), MAX(ts) FROM usage_tool_calls`+cond+` GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SkillActivity
	for rows.Next() {
		var harness string
		var s domain.SkillActivity
		var last int64
		if err := rows.Scan(&harness, &s.Skill, &s.Calls, &last); err != nil {
			return nil, err
		}
		if s.Harness, err = harnessOf(harness); err != nil {
			return nil, err
		}
		s.Last = fromMillis(last)
		out = append(out, s)
	}
	return out, rows.Err()
}

// pendingSkillLoads flags the harnesses with a Skill call whose skill is NULL: its log is not read again yet.
func pendingSkillLoads(q querier, f domain.Filter) (map[kernel.Harness]bool, error) {
	cond, args, err := where(q, f, "name = 'Skill'", "server = ''", "skill IS NULL")
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT DISTINCT harness FROM usage_tool_calls`+cond, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[kernel.Harness]bool{}
	for rows.Next() {
		var harness string
		if err := rows.Scan(&harness); err != nil {
			return nil, err
		}
		h, err := harnessOf(harness)
		if err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
