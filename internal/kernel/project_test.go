package kernel

import (
	"errors"
	"testing"
)

func TestAProjectContainsItsRootAndWhatLiesBelowIt(t *testing.T) {
	// Given the project rooted at /work/app
	p, err := NewProject("/work/app")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		folder string
		want   bool
	}{
		"its root":                                       {"/work/app", true},
		"a sub-folder":                                   {"/work/app/web/src", true},
		"its root written untidily":                      {"/work/app/./web/../", true},
		"a sibling sharing its name as a prefix":         {"/work/app-2", false},
		"a sibling sharing its name as a prefix, deeper": {"/work/app-2/web", false},
		"its parent":                                     {"/work", false},
		"another project":                                {"/play/app", false},
		"a way back out of it":                           {"/work/app/../other", false},
		"a relative folder, which names no place":        {"work/app", false},
		"an empty folder":                                {"", false},
	} {
		// When asking whether it holds a folder
		got := p.Contains(c.folder)

		// Then only the root and what lies below it count
		if got != c.want {
			t.Errorf("%s: Contains(%q) = %v, want %v", name, c.folder, got, c.want)
		}
	}
}

func TestTheZeroProjectStandsForEveryProject(t *testing.T) {
	// Given no project in particular
	var p Project

	// When asking about folders
	// Then every one is in it, and it has no root nor name
	if !p.IsZero() || !p.Contains("/anywhere") || !p.Contains("relative") || p.Root() != "" || p.Name() != "" {
		t.Errorf("zero project = %#v", p)
	}
}

func TestAProjectRootAtTheTopOfTheTreeContainsEverything(t *testing.T) {
	p, err := NewProject("/")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Contains("/work/app") || !p.Contains("/") {
		t.Error("the root folder does not contain /work/app")
	}
}

func TestAProjectIsNamedAfterItsFolder(t *testing.T) {
	// Given a folder written with a trailing slash
	p, err := NewProject("/work/app/")

	// Then the project is rooted at the tidy folder and called after its last element
	if err != nil || p.Root() != "/work/app" || p.Name() != "app" || p.IsZero() {
		t.Errorf("project = %#v %v", p, err)
	}
}

func TestAProjectNeedsAnAbsoluteRoot(t *testing.T) {
	for _, bad := range []string{"", "app", "./app", "../app"} {
		// When a project is rooted at a folder that is not an absolute path
		_, err := NewProject(bad)

		// Then it is refused
		if !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("NewProject(%q) = %v", bad, err)
		}
	}
}

// sessionsIn is the answer of an index that holds sessions of the projects it is given.
func sessionsIn(projects ...Project) func(Project) (bool, error) {
	return func(p Project) (bool, error) {
		for _, known := range projects {
			if known == p {
				return true, nil
			}
		}
		return false, nil
	}
}

func projectAt(t *testing.T, root string) Project {
	t.Helper()
	p, err := NewProject(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheProjectOfTheWorkingFolderIsKeptWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of /work/app
	app, home := projectAt(t, "/work/app"), projectAt(t, "/home/me")

	// When the project of the working folder /work/app is chosen
	got, err := WorkingProject(app, home, sessionsIn(app))

	// Then the view is about that project
	if err != nil || got != app {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestAWorkingFolderNoSessionWorkedInGivesTheViewOfEveryProject(t *testing.T) {
	// Given an index with sessions of /work/app only
	app, other, home := projectAt(t, "/work/app"), projectAt(t, "/work/other"), projectAt(t, "/home/me")

	// When the project of another folder is chosen
	got, err := WorkingProject(other, home, sessionsIn(app))

	// Then the view is about every project
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestTheHomeFolderIsNobodysProjectEvenWhenSessionsWorkedBelowIt(t *testing.T) {
	// Given an index with sessions in the home folder and below it, which makes the home hold every project
	home := projectAt(t, "/home/me")

	// When the project of the home folder is chosen
	got, err := WorkingProject(home, home, sessionsIn(home))

	// Then the view is about every project
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestNoWorkingFolderGivesTheViewOfEveryProject(t *testing.T) {
	got, err := WorkingProject(Project{}, Project{}, sessionsIn())
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestAnIndexThatCannotBeReadIsReportedNotTakenForAnEmptyOne(t *testing.T) {
	boom := errors.New("boom")
	app := projectAt(t, "/work/app")
	_, err := WorkingProject(app, Project{}, func(Project) (bool, error) { return true, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
