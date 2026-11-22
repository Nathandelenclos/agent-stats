package application

import (
	"slices"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// sessionsIn is an index that holds sessions of the projects it is given.
type sessionsIn []kernel.Project

func (s sessionsIn) HasSessions(p kernel.Project) (bool, error) { return slices.Contains(s, p), nil }

func TestTheProjectOfTheWorkingFolderIsTheCatalogsWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of the app
	app, err := kernel.NewProject("/work/app")
	if err != nil {
		t.Fatal(err)
	}

	// When the project of the app is detected
	got, err := DetectProject{Queries: sessionsIn{app}}.Run(app)

	// Then the catalogue is about that project
	if err != nil || got != app {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestTheHomeFolderIsNotTheCatalogsProjectEvenWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of the home folder
	home, err := kernel.NewProject("/home/me")
	if err != nil {
		t.Fatal(err)
	}

	// When the project of the home folder is detected
	got, err := DetectProject{Queries: sessionsIn{home}, Home: home}.Run(home)

	// Then the catalogue is about every project
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}
