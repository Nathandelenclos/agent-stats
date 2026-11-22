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

// where turns a filter into SQL conditions on the harness and timestamp columns; every value is a bound parameter.
func where(f domain.Filter, extra ...string) (string, []any) {
	conds := extra
	var args []any
	if !f.Since.IsZero() {
		conds, args = append(conds, "ts >= ?"), append(args, kernel.UnixMilli(f.Since))
	}
	if !f.Harness.IsZero() {
		conds, args = append(conds, "harness = ?"), append(args, f.Harness.String())
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Activity reads tool calls and agent types in one read transaction, so that indexing committing meanwhile cannot
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
	cond, args := where(f)
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
	cond, args := where(f)
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

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
