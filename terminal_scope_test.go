package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// keepGoCaches pins the Go caches where they are now: building the program under another HOME would fill a new module cache in it.
func keepGoCaches(t *testing.T) {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	dirs := strings.Fields(string(out))
	t.Setenv("GOMODCACHE", dirs[0])
	t.Setenv("GOCACHE", dirs[1])
}

func TestTheInterfaceStartedFromAProjectOnAnEmptyIndexRestrictsToItOnceTheLogsAreRead(t *testing.T) {
	// Given sessions logged in the app and in a sibling project, an index that holds none yet, and the interface opened from the app
	keepGoCaches(t)
	w := newScopedWorkplace(t)
	s := onATerminalIn(t, w.app, false, "--no-sync=false", "--db", w.db)

	// When the indexing of the logs ends
	// Then the views are about the app: the title names it
	s.waitFor(t, "project: app", 20*time.Second)
	s.quit(t)
}
