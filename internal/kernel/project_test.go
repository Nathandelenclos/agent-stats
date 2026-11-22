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

func TestAProjectIsNamedAfterItsFolderAndNeedsAnAbsoluteRoot(t *testing.T) {
	p, err := NewProject("/work/app/")
	if err != nil || p.Root() != "/work/app" || p.Name() != "app" || p.IsZero() {
		t.Fatalf("project = %#v %v", p, err)
	}
	for _, bad := range []string{"", "app", "./app", "../app"} {
		if _, err := NewProject(bad); !errors.Is(err, ErrNotAbsolute) {
			t.Errorf("NewProject(%q) = %v", bad, err)
		}
	}
}
