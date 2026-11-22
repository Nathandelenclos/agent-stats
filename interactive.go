package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/tui"
)

// interactive reports whether to open the interface: whenever the report would be shown on a terminal and
// the user did not ask for JSON. Only stdout counts: the interface reads the keyboard from the controlling
// terminal when stdin is redirected, while a piped or redirected stdout keeps the plain report.
func interactive(asJSON bool, out *os.File) bool {
	return showInterface(asJSON, isatty.IsTerminal(out.Fd()), keyboardReachable)
}

// showInterface decides between the interface and the plain report. Without a keyboard to read (stdin
// redirected and no controlling terminal, as under a daemon or a detached session), the interface could
// not start, so the plain report is kept.
func showInterface(asJSON, onTerminal bool, keyboard func() bool) bool {
	return !asJSON && onTerminal && keyboard()
}

// keyboardReachable reports whether keys can be read: from stdin when it is a terminal, else from the
// controlling terminal, which Bubble Tea opens itself.
func keyboardReachable() bool {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return true
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	tty.Close()
	return true
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
