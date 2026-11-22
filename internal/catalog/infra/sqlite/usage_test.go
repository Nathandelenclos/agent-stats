package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// publishedIndex creates a database holding stand-ins for the usage_* views the indexing context publishes.
func publishedIndex(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms := func(day int) int64 { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC).UnixMilli() }
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`CREATE TABLE usage_tool_calls (ts INTEGER, name TEXT, server TEXT, harness TEXT)`, nil},
		{`CREATE TABLE usage_requests (ts INTEGER, agent_type TEXT, harness TEXT)`, nil},
		{`INSERT INTO usage_tool_calls VALUES (?, 'review', 'grace', 'claude'), (?, 'review', 'grace', 'claude'), (?, 'Read', '', 'claude'), (?, 'get_issue', 'github', 'codex')`, []any{ms(1), ms(3), ms(2), ms(2)}},
		{`INSERT INTO usage_requests VALUES (?, 'Explore', 'claude'), (?, 'main', 'claude'), (?, 'Explore', 'claude')`, []any{ms(1), ms(1), ms(4)}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestActivityGroupsTheViewsPerHarnessAndName(t *testing.T) {
	// Given an index with calls to an MCP server and a built-in tool, and subagent requests
	u, err := OpenReadOnly(publishedIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// When the whole activity is read
	a, err := u.Activity(domain.Filter{})

	// Then calls and requests are counted per harness and name, with their latest time
	if err != nil {
		t.Fatal(err)
	}
	var grace domain.ToolActivity
	for _, x := range a.Tools {
		if x.Server == "grace" {
			grace = x
		}
	}
	if len(a.Tools) != 3 || grace.Calls != 2 || !grace.Last.Equal(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)) || grace.Harness != kernel.Claude {
		t.Errorf("tools: %+v", a.Tools)
	}
	var explore int64
	for _, x := range a.Agents {
		if x.AgentType == "Explore" {
			explore = x.Requests
		}
	}
	if explore != 2 {
		t.Errorf("agents: %+v", a.Agents)
	}
}

func TestActivityHonoursPeriodAndHarness(t *testing.T) {
	// Given the same index
	u, err := OpenReadOnly(publishedIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// When asked for Claude since 2 January
	a, err := u.Activity(domain.Filter{Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Harness: kernel.Claude})

	// Then earlier calls and the other harness are left out
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range a.Tools {
		if x.Harness != kernel.Claude || (x.Server == "grace" && x.Calls != 1) {
			t.Errorf("tool %+v", x)
		}
	}
	if len(a.Tools) != 2 || len(a.Agents) != 1 || a.Agents[0].Requests != 1 {
		t.Errorf("activity: %+v", a)
	}
}

func TestIndexIsOpenedReadOnly(t *testing.T) {
	// Given an index opened by the catalogue
	path := publishedIndex(t)
	u, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// When it tries to write, then SQLite refuses
	if _, err := u.db.Exec(`DELETE FROM usage_tool_calls`); err == nil {
		t.Error("the catalogue wrote to the index")
	}
}

func TestMissingIndexIsAnError(t *testing.T) {
	if _, err := OpenReadOnly(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Error("a missing index opened")
	}
}

func TestUnknownHarnessInTheIndexIsRefused(t *testing.T) {
	// Given a row naming a harness no slice knows
	path := publishedIndex(t)
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec(`INSERT INTO usage_tool_calls VALUES (1, 'x', '', 'vim')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	u, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// When the activity is read, then the corrupt row is reported instead of being shown under a wrong harness
	if _, err := u.Activity(domain.Filter{}); err == nil {
		t.Error("an unknown harness was accepted")
	}
}
