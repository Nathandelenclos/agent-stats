package application

import (
	"errors"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// sessionsIn is an index that holds sessions of the projects it is given.
type sessionsIn struct {
	projects []kernel.Project
	err      error
}

func (s sessionsIn) HasSessions(p kernel.Project) (bool, error) {
	for _, known := range s.projects {
		if known == p {
			return true, s.err
		}
	}
	return false, s.err
}

func project(t *testing.T, root string) kernel.Project {
	t.Helper()
	p, err := kernel.NewProject(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheProjectOfTheWorkingFolderIsKeptWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of /work/app
	app := project(t, "/work/app")
	uc := DetectProject{Queries: sessionsIn{projects: []kernel.Project{app}}}

	// When the project of /work/app is detected
	got, err := uc.Run(app)

	// Then the view is about that project
	if err != nil || got != app {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestAFolderNoSessionWorkedInGivesTheViewOfEveryProject(t *testing.T) {
	// Given an index with sessions of /work/app only
	uc := DetectProject{Queries: sessionsIn{projects: []kernel.Project{project(t, "/work/app")}}}

	// When the project of the home folder is detected
	got, err := uc.Run(project(t, "/home/me"))

	// Then the view is about every project
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestNoWorkingFolderGivesTheViewOfEveryProject(t *testing.T) {
	got, err := DetectProject{Queries: sessionsIn{}}.Run(kernel.Project{})
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestAnIndexThatCannotBeReadIsReportedNotTakenForAnEmptyOne(t *testing.T) {
	boom := errors.New("boom")
	app := project(t, "/work/app")
	if _, err := (DetectProject{Queries: sessionsIn{projects: []kernel.Project{app}, err: boom}}).Run(app); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestTheProjectOfAQueryReachesTheIndexForTheReportAndForTheSessions(t *testing.T) {
	// Given a query about a project
	app := project(t, "/work/app")

	// When a report and a list of sessions are built for it
	asked := &shiftingIndex{}
	if _, err := (BuildReport{Queries: asked}).Run(Query{Project: app}); err != nil {
		t.Fatal(err)
	}
	listed := &recordedSessions{}
	if _, err := (ListSessions{Queries: listed}).Run(Query{Project: app}); err != nil {
		t.Fatal(err)
	}

	// Then the index is asked about that project each time
	if asked.filter.Project != app || listed.filter.Project != app {
		t.Errorf("report asked %v, sessions asked %v", asked.filter.Project, listed.filter.Project)
	}
}
