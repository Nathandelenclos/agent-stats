package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/tui"
)

// interactive reports whether to open the interface: only when the user did not ask for JSON
// and both ends of the session are a terminal, so pipes and redirections keep the plain report.
func interactive(asJSON bool, in, out *os.File) bool {
	return !asJSON && isatty.IsTerminal(in.Fd()) && isatty.IsTerminal(out.Fd())
}

// tuiSync adapts the indexing run to the interface's Sync port: the counts become progress and what
// was skipped becomes the note shown afterwards (nothing may be printed over the screen).
func tuiSync(run indexRun) tui.Sync {
	return func(progress func(int)) (string, error) {
		rep, err := run(func(r indexing.IndexReport) { progress(r.Indexed) })
		return syncNote(rep), err
	}
}

func syncNote(rep indexing.IndexReport) string {
	var parts []string
	if n := len(rep.Errors); n > 0 {
		parts = append(parts, fmt.Sprintf("skipped files: %d", n))
	}
	if rep.SkippedLines > 0 {
		parts = append(parts, fmt.Sprintf("skipped log lines: %d", rep.SkippedLines))
	}
	return strings.Join(parts, " · ")
}
