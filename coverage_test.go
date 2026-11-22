//go:build unix

package main

import (
	"errors"
	"os/user"
	"slices"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

func TestTheHomeFolderIsReadFromTheUserDatabaseWhenHOMEIsNotSet(t *testing.T) {
	// Given a user whose environment has no HOME
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no home folder in the user database")
	}
	want, err := projectfolders.Of(u.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")

	// When the home folder is asked for
	got := homeProject()

	// Then it is the one the user database gives, not no folder at all
	if got != want {
		t.Errorf("home = %q, want %q", got.Root(), want.Root())
	}
}

// detectedIn is the DetectProject use case of an index that holds sessions of the projects it is given, with the home folder.
func detectedIn(home kernel.Project, projects ...kernel.Project) func(kernel.Project) (kernel.Project, error) {
	return reporting.DetectProject{Queries: sessionsIn(projects), Home: home}.Run
}

type sessionsIn []kernel.Project

func (s sessionsIn) HasSessions(p kernel.Project) (bool, error) { return slices.Contains(s, p), nil }

func TestTheCoverageFollowsWhereSessionsWorkedAndWhatTheUserAskedFor(t *testing.T) {
	app, _ := kernel.NewProject("/work/app")
	home, _ := kernel.NewProject("/home/me")
	other, _ := kernel.NewProject("/work/other")
	detect := detectedIn(home, app, home)
	for name, c := range map[string]struct {
		all            bool
		given, working kernel.Project
		want           coverage
	}{
		"the working folder's project, with sessions":      {false, kernel.Project{}, app, coverage{project: app, restricted: true}},
		"a working folder with no session":                 {false, kernel.Project{}, other, coverage{}},
		"the home folder, though sessions worked below it": {false, kernel.Project{}, home, coverage{}},
		"--all, which keeps the project to switch to":      {true, kernel.Project{}, app, coverage{project: app, restricted: false}},
		"--all from a folder with no session":              {true, kernel.Project{}, other, coverage{}},
		"--project, even where no session worked":          {false, other, app, coverage{project: other, restricted: true}},
		"--project with --all, which wins":                 {true, other, app, coverage{project: other, restricted: false}},
		"no working folder":                                {false, kernel.Project{}, kernel.Project{}, coverage{}},
		"--project naming the home folder":                 {false, home, app, coverage{viaHome: true}},
		"--project naming the home folder with --all":      {true, home, app, coverage{viaHome: true}},
	} {
		got, err := chooseCoverage(c.all, c.given, c.working, home, detect)
		if err != nil || got != c.want {
			t.Errorf("%s: coverage = %+v, %v, want %+v", name, got, err, c.want)
		}
	}
}

func TestTheCoverageOfAnIndexThatCannotBeReadIsAnError(t *testing.T) {
	boom := errors.New("boom")
	app, _ := kernel.NewProject("/work/app")
	_, err := chooseCoverage(false, kernel.Project{}, app, kernel.Project{}, func(kernel.Project) (kernel.Project, error) { return kernel.Project{}, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
