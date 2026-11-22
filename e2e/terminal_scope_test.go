//go:build unix

package e2e

import (
	"testing"
	"time"
)

func TestTheInterfaceStartedFromAProjectOnAnEmptyIndexRestrictsToItOnceTheLogsAreRead(t *testing.T) {
	// Given sessions logged in the app and in a sibling project, an index that holds none yet, and the interface opened from the app
	w := newScopedWorkplace(t)
	s := onATerminalIn(t, w.app, false, "--no-sync=false", "--db", w.db)

	// When the indexing of the logs ends
	// Then the views are about the app: the title names it
	s.waitFor(t, "project: app", 20*time.Second)
	s.quit(t)
}
