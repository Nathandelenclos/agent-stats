//go:build unix

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// crossing is an index of two projects whose sessions work across them, each request costing a distinct number so that
// a sum tells which sessions it holds:
//   - parent-a, a main session in A (1), with a subagent in B (2) and a subagent in A (4);
//   - root-b, a main session in B (8), with a subagent in A (16).
type crossing struct {
	a, b  string
	index *SQLiteUsage
}

func newCrossing(t *testing.T) crossing {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := crossing{a: filepath.Join(dir, "a"), b: filepath.Join(dir, "b")}
	for _, d := range []string{c.a, c.b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%v: %s", err, query)
		}
	}
	exec(readModel + sessionModel)
	for i, s := range []struct {
		id, root, agent, folder string
		cost                    int
	}{
		{"parent-a", "parent-a", "main", c.a, 1},
		{"child-b", "parent-a", "subagent", c.b, 2},
		{"child-a", "parent-a", "subagent", c.a, 4},
		{"root-b", "root-b", "main", c.b, 8},
		{"child-of-b-in-a", "root-b", "subagent", c.a, 16},
	} {
		at := t0 + int64(i)
		exec(`INSERT INTO usage_requests
		(ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error, harness, agent_type, folder)
		VALUES (?, 'm', 1, 1, 0, 0, ?, 1, NULL, NULL, NULL, 'claude', ?, ?)`, at, s.cost, s.agent, s.folder)
		exec(`INSERT INTO session_overview VALUES (?, ?, 'claude', ?, ?, ?, ?, ?, ?, ?)`, s.id, s.root, s.agent, s.id, s.folder, at, at, at, at)
		exec(`INSERT INTO session_requests
		(root_id, session_id, ts, model, input, output, cache_read, cache_write, cost, priced, duration_ms, ttft_ms, error)
		VALUES (?, ?, ?, 'm', 1, 1, 0, 0, ?, 1, NULL, NULL, NULL)`, s.root, s.id, at, s.cost)
		exec(`INSERT INTO usage_folders VALUES (?)`, s.folder)
	}
	// A subagent present in A was started by an agent that worked in B.
	exec(`INSERT INTO session_lineage VALUES ('child-b', 'parent-a'), ('child-a', 'child-b')`)
	// the run of a root spans all its sessions, as the index's view says
	exec(`UPDATE session_overview SET run_started_at = ?, run_ended_at = ? WHERE root_id = 'parent-a'`, t0, t0+2)
	exec(`UPDATE session_overview SET run_started_at = ?, run_ended_at = ? WHERE root_id = 'root-b'`, t0+3, t0+4)
	c.index, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.index.Close() })
	return c
}

func (c crossing) scopes(t *testing.T) map[string]report.Filter {
	return map[string]report.Filter{"A": {Project: projectAt(t, c.a)}, "B": {Project: projectAt(t, c.b)}, "all": {}}
}

func sessionIDs(list []report.SessionSummary) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID.String())
	}
	slices.Sort(out)
	return out
}

func TestTheSessionsOfAProjectCountOnlyWhatWorkedInIt(t *testing.T) {
	// Given a session of A whose subagent worked in B, and a session of B whose subagent worked in A
	c := newCrossing(t)

	for name, want := range map[string]struct {
		ids  string
		cost map[string]report.USD
	}{
		"A":   {"child-of-b-in-a parent-a", map[string]report.USD{"parent-a": 1 + 4, "child-of-b-in-a": 16}},
		"B":   {"child-b root-b", map[string]report.USD{"root-b": 8, "child-b": 2}},
		"all": {"parent-a root-b", map[string]report.USD{"parent-a": 1 + 2 + 4, "root-b": 8 + 16}},
	} {
		// When the sessions of the scope are listed
		list, err := c.index.Sessions().Sessions(context.Background(), c.scopes(t)[name])
		if err != nil {
			t.Fatal(err)
		}

		// Then a session counts the sessions of its run that worked in the scope, and a subagent that worked in it
		// whose run started outside is listed on its own
		if got := strings.Join(sessionIDs(list), " "); got != want.ids {
			t.Errorf("%s: sessions = %q, want %q", name, got, want.ids)
		}
		for _, s := range list {
			if s.Cost != want.cost[s.ID.String()] {
				t.Errorf("%s: %s costs %v, want %v", name, s.ID, s.Cost, want.cost[s.ID.String()])
			}
		}
	}
}

func TestTheSessionsTotalIsTheReportTotalOfTheSameScope(t *testing.T) {
	// Given the same crossing sessions
	c := newCrossing(t)

	for name, f := range c.scopes(t) {
		// When the sessions and the report are asked for the same scope
		list, err := c.index.Sessions().Sessions(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		total, err := c.index.Total(f)
		if err != nil {
			t.Fatal(err)
		}

		// Then the sessions add up to the report's requests and cost
		sessions := report.NewSessionList(list).Total
		if sessions.Requests != total.Requests || sessions.Cost != total.Cost {
			t.Errorf("%s: sessions total = %d requests costing %v, report = %d costing %v", name, sessions.Requests, sessions.Cost, total.Requests, total.Cost)
		}
	}
}

func TestTheDescriptionOfASessionInAProjectCountsOnlyWhatWorkedInIt(t *testing.T) {
	// Given the crossing sessions
	c := newCrossing(t)

	// When the session of A that has a subagent in B is described for A
	d, err := c.index.Sessions().Session(context.Background(), mustID(t, "parent-a"), projectAt(t, c.a))

	// Then its figures, its subagents and its tools are those of the sessions that worked in A
	if err != nil || d.Cost != 1+4 || len(d.Subagents) != 1 || d.Subagents[0].Cost != 4 {
		t.Errorf("cost = %v, subagents = %+v, %v", d.Cost, d.Subagents, err)
	}
}

func TestAParentOutsideTheProjectIsNotExposedInTheGraph(t *testing.T) {
	c := newCrossing(t)
	id := mustID(t, "parent-a")

	all, err := c.index.Sessions().Session(context.Background(), id, kernel.Project{})
	if err != nil || len(all.Subagents) != 2 || all.Subagents[0].ParentID.String() != "child-b" {
		t.Fatalf("unrestricted subagents = %+v (%v)", all.Subagents, err)
	}

	scoped, err := c.index.Sessions().Session(context.Background(), id, projectAt(t, c.a))
	if err != nil || len(scoped.Subagents) != 1 || scoped.Subagents[0].ParentID.String() != "" {
		t.Fatalf("scoped subagents = %+v (%v)", scoped.Subagents, err)
	}
}

func TestTheSubagentsOfASessionInAProjectAreTheOnesThatWorkedInIt(t *testing.T) {
	// Given the crossing sessions
	c := newCrossing(t)

	// When A's session is listed
	list, err := c.index.Sessions().Sessions(context.Background(), c.scopes(t)["A"])
	if err != nil {
		t.Fatal(err)
	}

	// Then it counts the subagent that worked in A, not the one that worked in B
	for _, s := range list {
		if want := map[string]int{"parent-a": 1, "child-of-b-in-a": 0}[s.ID.String()]; s.SubagentCount != want {
			t.Errorf("%s has %d subagents, want %d", s.ID, s.SubagentCount, want)
		}
	}
}

func TestASubagentListedOnItsOwnCanBeOpened(t *testing.T) {
	// Given a subagent that worked in A, whose session started in B
	c := newCrossing(t)

	// When its description is asked for in A
	d, err := c.index.Sessions().Session(context.Background(), mustID(t, "child-of-b-in-a"), projectAt(t, c.a))

	// Then it describes that subagent alone
	if err != nil || d.ID.String() != "child-of-b-in-a" || d.Cost != 16 || len(d.Subagents) != 0 {
		t.Errorf("detail = %v %v %d subagents, %v", d.ID, d.Cost, len(d.Subagents), err)
	}
}

func TestASubagentIsOpenedOnlyInTheProjectThatListsItOnItsOwn(t *testing.T) {
	// Given the same subagent
	c := newCrossing(t)

	for name, p := range map[string]kernel.Project{"every project": {}, "B": projectAt(t, c.b)} {
		// When its description is asked for in a scope that lists it under its session
		_, err := c.index.Sessions().Session(context.Background(), mustID(t, "child-of-b-in-a"), p)

		// Then it is not found
		if !errors.Is(err, report.ErrSessionNotFound) {
			t.Errorf("in %s: %v", name, err)
		}
	}
}

func TestASessionThatNoListingHoldsIsNotFound(t *testing.T) {
	c := newCrossing(t)
	if _, err := c.index.Sessions().Session(context.Background(), mustID(t, "nobody"), projectAt(t, c.a)); !errors.Is(err, report.ErrSessionNotFound) {
		t.Errorf("unknown session: %v", err)
	}
}

func mustID(t *testing.T, id string) report.SessionID {
	t.Helper()
	sid, err := report.NewSessionID(id)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
