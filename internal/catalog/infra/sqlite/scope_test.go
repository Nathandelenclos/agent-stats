//go:build unix

package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// workplace is a disk with an app, a sub-folder of it, a sibling sharing the app's name as a prefix, and a link to the
// sub-folder; and an index in which one session worked in each of them, and in two folders that are not paths to a
// place. Each session called a tool, loaded a skill and made a request, all named after the session.
type workplace struct {
	app, sub, sibling, link string
	path                    string
}

func newWorkplace(t *testing.T) workplace {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := workplace{app: filepath.Join(dir, "app"), sibling: filepath.Join(dir, "app-2"), link: filepath.Join(dir, "shortcut"), path: filepath.Join(dir, "index.db")}
	w.sub = filepath.Join(w.app, "web")
	for _, d := range []string{w.sub, w.sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(w.sub, w.link); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", w.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE usage_tool_calls (ts INTEGER, name TEXT, server TEXT, skill TEXT, harness TEXT, folder TEXT);
		CREATE TABLE usage_requests (ts INTEGER, agent_type TEXT, harness TEXT, folder TEXT);
		CREATE TABLE usage_folders (folder TEXT)`); err != nil {
		t.Fatal(err)
	}
	for name, folder := range map[string]string{"app": w.app, "sub": w.sub, "sibling": w.sibling, "link": w.link, "relative": "app/web", "dotdot": w.app + "/../app-2"} {
		for _, q := range []string{
			`INSERT INTO usage_tool_calls VALUES (1, 'tool-' || ?1, '', '', 'claude', ?2)`,
			`INSERT INTO usage_tool_calls VALUES (1, 'Skill', '', 'skill-' || ?1, 'claude', ?2)`,
			`INSERT INTO usage_requests VALUES (1, 'agent-' || ?1, 'claude', ?2)`,
			`INSERT INTO usage_folders VALUES (?2)`,
		} {
			if _, err := db.Exec(q, name, folder); err != nil {
				t.Fatal(err)
			}
		}
	}
	return w
}

// activityIn reads the activity of the index narrowed to the project at dir, every project when dir is empty, and
// names the sessions it comes from.
func (w workplace) activityIn(t *testing.T, dir string) string {
	t.Helper()
	u, err := OpenReadOnly(w.path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	var f domain.Filter
	if dir != "" {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		if f.Project, err = kernel.NewProject(resolved); err != nil {
			t.Fatal(err)
		}
	}
	a, err := u.Activity(f)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, x := range a.Tools {
		seen = append(seen, x.Name)
	}
	for _, x := range a.Agents {
		seen = append(seen, x.AgentType)
	}
	for _, x := range a.Skills {
		seen = append(seen, x.Skill)
	}
	sort.Strings(seen)
	return strings.Join(seen, " ")
}

// by names what the sessions called did: a tool, a skill and an agent each, and the Skill tool that loaded the skill.
func by(sessions ...string) string {
	seen := []string{"Skill"}
	for _, s := range sessions {
		seen = append(seen, "agent-"+s, "skill-"+s, "tool-"+s)
	}
	sort.Strings(seen)
	return strings.Join(seen, " ")
}

func TestTheActivityOfAProjectIsThatOfItsFolderAndWhatLiesBelowIt(t *testing.T) {
	// Given sessions in the app, below it, through a link to below it, in a sibling sharing its name as a prefix, and in
	// folders that are not absolute paths
	w := newWorkplace(t)

	// When the activity of the app is read
	got := w.activityIn(t, w.app)

	// Then the tool calls, the agents and the skills are those of the app's sessions, the link's included
	if want := by("app", "sub", "link"); got != want {
		t.Errorf("activity = %q\nwant       %q", got, want)
	}
}

func TestTheActivityOfAProjectGivenThroughALinkIsThatOfTheFolderTheLinkLeadsTo(t *testing.T) {
	w := newWorkplace(t)
	if got, want := w.activityIn(t, w.link), by("sub", "link"); got != want {
		t.Errorf("activity = %q, want %q", got, want)
	}
}

func TestTheActivityOfNoProjectIsEveryonesAndThatOfAFolderHoldingSeveralIsTheirs(t *testing.T) {
	w := newWorkplace(t)
	if got, want := w.activityIn(t, ""), by("app", "sub", "sibling", "link", "relative", "dotdot"); got != want {
		t.Errorf("activity without a project = %q", got)
	}
	if got, want := w.activityIn(t, filepath.Dir(w.app)), by("app", "sub", "sibling", "link", "dotdot"); got != want {
		t.Errorf("activity of the folder holding both = %q", got)
	}
}
