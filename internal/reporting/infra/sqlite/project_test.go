//go:build unix

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// workplace is a disk with an app, a sub-folder of it, a sibling sharing the app's name as a prefix, and a link to
// the sub-folder, and an index in which one session worked in each of them, and in two folders that name no place.
type workplace struct {
	app, sub, sibling, link string
	index                   *SQLiteUsage
}

// worked is where each session of the workplace worked, and what its one request cost (a distinct number each, so
// that the sum of a selection tells which sessions it holds).
func (w workplace) worked() []struct {
	id, folder string
	cost       int
} {
	return []struct {
		id, folder string
		cost       int
	}{
		{"app", w.app, 1}, {"sub", w.sub, 2}, {"sibling", w.sibling, 4}, {"link", w.link, 8},
		{"relative", "app/web", 16}, {"dotdot", w.app + "/../app-2", 32},
	}
}

func newWorkplace(t *testing.T) workplace {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := workplace{app: filepath.Join(dir, "app"), sibling: filepath.Join(dir, "app-2"), link: filepath.Join(dir, "shortcut")}
	w.sub = filepath.Join(w.app, "web")
	for _, d := range []string{w.sub, w.sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(w.sub, w.link); err != nil {
		t.Fatal(err)
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
	for i, s := range w.worked() {
		at := t0 + int64(i)
		exec(`INSERT INTO usage_requests VALUES (?, 'm', 1, 1, 0, 0, ?, 1, NULL, NULL, NULL, 'claude', 'main', ?)`, at, s.cost, s.folder)
		exec(`INSERT INTO usage_tool_calls VALUES (?, ?, '', 0, 0, 0, NULL, 'claude', ?)`, at, "tool-"+s.id, s.folder)
		exec(`INSERT INTO usage_folders VALUES (?)`, s.folder)
		exec(`INSERT INTO session_overview VALUES (?, ?, 'claude', 'main', NULL, ?, ?, ?, ?, ?)`, s.id, s.id, s.folder, at, at, at, at)
		exec(`INSERT INTO session_requests VALUES (?, ?, ?, 'm', 1, 1, 0, 0, ?, 1, NULL, NULL, NULL)`, s.id, s.id, at, s.cost)
	}
	exec(`INSERT INTO usage_folders VALUES ('')`) // a session that has no folder is not listed, but a reader must survive one
	w.index, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.index.Close() })
	return w
}

func projectAt(t *testing.T, dir string) kernel.Project {
	t.Helper()
	p, err := projectfolders.Of(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// costOf is the sum of the costs of the sessions whose ids are named: the figure a selection of them must show.
func costOf(w workplace, ids ...string) report.USD {
	var sum int
	for _, s := range w.worked() {
		for _, id := range ids {
			if s.id == id {
				sum += s.cost
			}
		}
	}
	return report.USD(sum)
}

func TestTheReportOfAProjectCountsItsFolderAndWhatLiesBelowIt(t *testing.T) {
	// Given sessions that worked in the app, below it, through a link to below it, in a sibling that shares its name
	// as a prefix, and in folders that are not absolute paths
	w := newWorkplace(t)
	f := report.Filter{Project: projectAt(t, w.app)}

	// When the usage of the app is asked for
	total, err := w.index.Total(f)
	if err != nil {
		t.Fatal(err)
	}

	// Then only the sessions of the app count: its root, its sub-folder, and the link that leads there
	if want := costOf(w, "app", "sub", "link"); total.Requests != 3 || total.Cost != want {
		t.Errorf("total = %d requests costing %v, want 3 costing %v", total.Requests, total.Cost, want)
	}
}

func TestEveryGroupingAndTheToolsOfAProjectLeaveOutTheOtherFolders(t *testing.T) {
	// Given the same workplace
	w := newWorkplace(t)
	f := report.Filter{Project: projectAt(t, w.app)}

	// When the usage is grouped by folder, and the tools asked for
	folders, err := w.index.UsageBy(f, report.ByFolder)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := w.index.Tools(f)
	if err != nil {
		t.Fatal(err)
	}

	// Then only folders of the app are listed, as the logs wrote them, and only its tools
	var gotFolders, gotTools []string
	for _, g := range folders {
		gotFolders = append(gotFolders, g.Group)
	}
	for _, x := range tools {
		gotTools = append(gotTools, x.Name)
	}
	if fmt.Sprint(len(gotFolders), " ", len(gotTools)) != "3 3" {
		t.Errorf("folders = %q, tools = %q", gotFolders, gotTools)
	}
	for _, name := range gotTools {
		if name != "tool-app" && name != "tool-sub" && name != "tool-link" {
			t.Errorf("tool %q is not one of the app's", name)
		}
	}
}

func TestAProjectGivenThroughALinkIsTheFolderTheLinkLeadsTo(t *testing.T) {
	// Given the workplace, and the app's sub-folder reached by its link
	w := newWorkplace(t)

	// When the usage of the project at that link is asked for
	total, err := w.index.Total(report.Filter{Project: projectAt(t, w.link)})

	// Then it is the sub-folder's: sessions that worked there directly or through the link count, those of the app's root do not
	if want := costOf(w, "sub", "link"); err != nil || total.Cost != want {
		t.Errorf("total = %v %v, want %v", total.Cost, err, want)
	}
}

func TestTheSessionsOfAProjectAreTheOnesThatWorkedInIt(t *testing.T) {
	// Given the workplace
	w := newWorkplace(t)

	// When the sessions of the app are listed
	inApp, err := w.index.Sessions().Sessions(context.Background(), report.Filter{Project: projectAt(t, w.app)})
	if err != nil {
		t.Fatal(err)
	}

	// Then the project keeps the sessions of its folders, each with its own figures
	var cost report.USD
	for _, s := range inApp {
		cost += s.Cost
	}
	if len(inApp) != 3 || cost != costOf(w, "app", "sub", "link") {
		t.Errorf("sessions of the app = %q costing %v", ids(inApp), cost)
	}
}

func TestEverySessionIsListedWhenNoProjectIsAsked(t *testing.T) {
	// Given the workplace
	w := newWorkplace(t)

	// When the sessions are listed without a project
	everyone, err := w.index.Sessions().Sessions(context.Background(), report.Filter{})

	// Then every session is listed, those of folders that are not paths too
	if err != nil || len(everyone) != 6 {
		t.Errorf("sessions without a project = %q, %v", ids(everyone), err)
	}
}

func TestAProjectKeepsTheOtherFiltersOfTheQuery(t *testing.T) {
	// Given the workplace, and a project filter along with a harness no session used
	w := newWorkplace(t)
	f := report.Filter{Project: projectAt(t, w.app), Harness: kernel.Codex}

	// When usage and sessions are asked for
	total, err := w.index.Total(f)
	list, errList := w.index.Sessions().Sessions(context.Background(), f)

	// Then nothing matches both
	if err != nil || errList != nil || total.Requests != 0 || len(list) != 0 {
		t.Errorf("total = %+v %v, sessions = %q %v", total, err, ids(list), errList)
	}
}

func TestAFolderNoSessionWorkedInHasNoSessions(t *testing.T) {
	// Given the workplace and a folder next to it
	w := newWorkplace(t)
	elsewhere := filepath.Join(filepath.Dir(w.app), "elsewhere")

	// When asking whether sessions worked in each folder
	for dir, want := range map[string]bool{w.app: true, w.sub: true, w.link: true, w.sibling: true, elsewhere: false, filepath.Dir(w.app) + "/app-": false} {
		got, err := w.index.HasSessions(projectAt(t, dir))

		// Then only the folders sessions worked in, or below, say yes
		if err != nil || got != want {
			t.Errorf("HasSessions(%s) = %v, %v, want %v", dir, got, err, want)
		}
	}
}

func TestTheQuestionsOfOneSnapshotAgreeOnTheFoldersOfTheProject(t *testing.T) {
	// Given the workplace, and a snapshot that asks a first question about the app, in which a link leads to its sub-folder
	w := newWorkplace(t)
	f := report.Filter{Project: projectAt(t, w.app)}
	var first, second, groups report.Usage
	err := w.index.Snapshot(context.Background(), func(r reporting.UsageReader) (err error) {
		if first, err = r.Total(f); err != nil {
			return err
		}

		// When the link is pointed outside the app before the next questions of the same snapshot
		if err = os.Remove(w.link); err != nil {
			return err
		}
		if err = os.Symlink(w.sibling, w.link); err != nil {
			return err
		}
		if second, err = r.Total(f); err != nil {
			return err
		}
		byFolder, err := r.UsageBy(f, report.ByFolder)
		for _, g := range byFolder {
			groups.Requests += g.Requests
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Then they all count the same sessions, while a question asked outside a snapshot sees the link where it leads now
	if second.Requests != first.Requests || groups.Requests != first.Requests {
		t.Errorf("requests = %d, then %d, then %d by folder", first.Requests, second.Requests, groups.Requests)
	}
	if now, err := w.index.Total(f); err != nil || now.Requests != first.Requests-1 {
		t.Errorf("outside a snapshot: %d requests, %v, want %d", now.Requests, err, first.Requests-1)
	}
}
