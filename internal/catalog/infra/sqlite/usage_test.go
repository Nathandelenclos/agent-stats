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
		{`CREATE TABLE usage_tool_calls (ts INTEGER, name TEXT, server TEXT, skill TEXT, harness TEXT)`, nil},
		{`CREATE TABLE usage_requests (ts INTEGER, agent_type TEXT, harness TEXT)`, nil},
		{`INSERT INTO usage_tool_calls VALUES (?, 'review', 'grace', '', 'claude'), (?, 'review', 'grace', '', 'claude'), (?, 'Read', '', '', 'claude'), (?, 'get_issue', 'github', '', 'codex'),
			(?, 'Skill', '', 'grace-review', 'claude'), (?, 'Skill', '', 'grace-review', 'claude')`, []any{ms(1), ms(3), ms(2), ms(2), ms(2), ms(4)}},
		{`INSERT INTO usage_requests VALUES (?, 'Explore', 'claude'), (?, 'main', 'claude'), (?, 'Explore', 'claude')`, []any{ms(1), ms(1), ms(4)}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// allActivity reads the whole activity of the stand-in index.
func allActivity(t *testing.T) domain.Activity {
	t.Helper()
	u, err := OpenReadOnly(publishedIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	a, err := u.Activity(domain.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// withSkillCall adds to the stand-in index a Claude Skill call of 4 January whose skill column holds skill: NULL for a
// call whose log the index has not read again, "" for one that named no valid skill.
func withSkillCall(t *testing.T, path string, skill any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO usage_tool_calls VALUES (?, 'Skill', '', ?, 'claude')`, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC).UnixMilli(), skill); err != nil {
		t.Fatal(err)
	}
}

// activityWithSkillCall reads the whole activity of the stand-in index after withSkillCall, and the activity from 5 January.
func activityWithSkillCall(t *testing.T, skill any) (all, later domain.Activity) {
	t.Helper()
	path := publishedIndex(t)
	withSkillCall(t, path, skill)
	u, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if all, err = u.Activity(domain.Filter{}); err != nil {
		t.Fatal(err)
	}
	if later, err = u.Activity(domain.Filter{Since: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	return all, later
}

func TestToolCallsAreCountedPerHarnessAndServer(t *testing.T) {
	// Given an index with calls to an MCP server, a built-in tool and another harness's server
	// When the activity is read
	a := allActivity(t)

	// Then calls are counted per harness, name and server, with their latest time
	var grace domain.ToolActivity
	for _, x := range a.Tools {
		if x.Server == "grace" {
			grace = x
		}
	}
	if len(a.Tools) != 4 || grace.Calls != 2 || !grace.Last.Equal(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)) || grace.Harness != kernel.Claude {
		t.Errorf("tools: %+v", a.Tools)
	}
}

func TestSubagentRequestsAreCountedPerAgentType(t *testing.T) {
	// Given an index with requests from the main conversation and from two Explore sessions
	// When the activity is read
	a := allActivity(t)

	// Then the Explore requests are counted together
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

func TestSkillLoadsAreCountedPerSkill(t *testing.T) {
	// Given an index with two loads of one skill
	// When the activity is read
	a := allActivity(t)

	// Then they are counted together, with the latest
	if len(a.Skills) != 1 || a.Skills[0].Skill != "grace-review" || a.Skills[0].Calls != 2 || !a.Skills[0].Last.Equal(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("skills: %+v", a.Skills)
	}
}

func TestASkillCallWhoseLogIsNotReadAgainYetMakesTheSkillUsageOfItsHarnessUnknown(t *testing.T) {
	// Given a Claude Skill call, on 4 January, whose log the index has not read again since it records skills
	// When the activity is read for the whole period, and for the period after that call
	all, later := activityWithSkillCall(t, nil)

	// Then Claude is flagged for the first period only, and Codex for neither
	if !all.PendingSkillLoads[kernel.Claude] || all.PendingSkillLoads[kernel.Codex] || len(later.PendingSkillLoads) != 0 {
		t.Errorf("all %v, later %v", all.PendingSkillLoads, later.PendingSkillLoads)
	}
}

func TestASkillCallThatNamedNoValidSkillIsCountedApartAndLeavesTheSkillUsageKnown(t *testing.T) {
	// Given a Claude Skill call, on 4 January, that named no valid skill
	// When the activity is read for the whole period, and for the period after that call
	all, later := activityWithSkillCall(t, "")

	// Then the call is counted as unnamed for the first period only, no harness has an unknown skill usage,
	// and the load of the named skill is still counted
	if len(all.UnnamedSkillCalls) != 1 || all.UnnamedSkillCalls[0].Harness != kernel.Claude || all.UnnamedSkillCalls[0].Calls != 1 || all.UnnamedSkillCalls[0].Skill != "" ||
		!all.UnnamedSkillCalls[0].Last.Equal(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)) || len(later.UnnamedSkillCalls) != 0 {
		t.Errorf("unnamed: all %+v, later %+v", all.UnnamedSkillCalls, later.UnnamedSkillCalls)
	}
	if len(all.PendingSkillLoads) != 0 || len(all.Skills) != 1 || all.Skills[0].Calls != 2 {
		t.Errorf("pending %v, skills %+v", all.PendingSkillLoads, all.Skills)
	}
}

func TestSkillCallsAreNeitherPendingNorUnnamedWhenEveryOneNamedItsSkill(t *testing.T) {
	// Given an index where every Skill call named its skill
	// When the activity is read
	a := allActivity(t)

	// Then nothing is pending and nothing is unnamed
	if len(a.PendingSkillLoads) != 0 || len(a.UnnamedSkillCalls) != 0 {
		t.Errorf("pending %v, unnamed %+v", a.PendingSkillLoads, a.UnnamedSkillCalls)
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
	if len(a.Tools) != 3 || len(a.Agents) != 1 || a.Agents[0].Requests != 1 || len(a.Skills) != 1 || a.Skills[0].Calls != 2 {
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
	if _, err := db.Exec(`INSERT INTO usage_tool_calls VALUES (1, 'x', '', '', 'vim')`); err != nil {
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
