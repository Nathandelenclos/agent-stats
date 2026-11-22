// Package tui is the interactive input adapter of the reporting context: a Bubble Tea program that
// shows the usage report in tabs, the sessions behind it (a list, and the detail of one), or the review of the prompts that showed frustration, and asks the
// application for new figures when the user changes the view.
// It holds presentation state only; every figure comes from the reporting use case.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Sync brings the index up to date and calls progress with the number of files indexed so far.
// The note it returns, if any, is shown when it is done (what was skipped, for instance).
type Sync func(progress func(indexed int)) (note string, err error)

// Options configures the interface.
type Options struct {
	Reports reporting.BuildReport
	// Sessions and Session read the sessions behind the report; the sessions mode (key m) is offered when both have queries.
	Sessions reporting.ListSessions
	Session  reporting.ShowSession
	// Prompts reviews the prompts that showed frustration; the prompts mode (key m) is offered when it has queries.
	Prompts reporting.ReviewPrompts
	// Sync, when set, runs in the background as the interface opens and on demand, then the report refreshes.
	Sync Sync
	// Harness is the harness the first report is limited to; the zero Harness means all of them.
	Harness kernel.Harness
	// SinceLabel names the period of the first report: "24h", "7d" or "30d" for the presets, "" for all.
	// Any other label stands for a custom period starting at Since.
	SinceLabel string
	Since      time.Time
}

// Run shows the interface until the user quits.
func Run(o Options) error {
	_, err := tea.NewProgram(New(o), tea.WithAltScreen()).Run()
	return err
}
