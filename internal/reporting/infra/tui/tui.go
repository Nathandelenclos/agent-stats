// Package tui is the interactive input adapter of the reporting context: a Bubble Tea program that
// shows the usage report in tabs, the sessions behind it (a list, and the detail of one), or the review of the prompts that showed frustration, and asks the
// application for new figures when the user changes the view.
// It holds presentation state only; every figure comes from the reporting use case.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Sync brings the index up to date and calls progress with the number of files indexed so far.
// It is the interface's own option, supplied by the composition root, and not a port of an application: the
// interface does not know what is indexed, only that it may ask for the index to be refreshed.
// It must stop soon after ctx is done, which happens when the user quits: the index is closed once it has.
// The note it returns, if any, is shown when it is done (what was skipped, for instance).
type Sync func(ctx context.Context, progress func(indexed int)) (note string, err error)

// Options configures the interface.
type Options struct {
	Reports reporting.BuildTimelineReport
	// Sessions and Session read the sessions behind the report; the sessions mode (key m) is offered when both have queries.
	Sessions        reporting.ListSessions
	Session         reporting.ShowSession
	PromptText      reporting.ShowSessionPrompt
	RelatedSessions reporting.ListRelatedSessions
	// Prompts reviews the prompts that showed frustration; the prompts mode (key m) is offered when it has queries.
	Prompts reporting.ReviewPrompts
	Catalog func(catalog.Query) (catalog.Catalog, error)
	Quotas  quota.Reader
	// Sync, when set, runs in the background as the interface opens and on demand, then the report refreshes.
	Sync Sync
	// Harness is the harness the first report is limited to; the zero Harness means all of them.
	Harness kernel.Harness
	// SinceLabel names the period of the first report: "24h", "7d" or "30d" for the presets, "" for all.
	// Any other label stands for a custom period starting at Since.
	SinceLabel string
	Since      time.Time
	// Project is the project key a switches the views to, and Restricted whether the views start restricted to it. The zero
	// Project means there is none to switch to: the views cover every project and the key does nothing.
	Project    kernel.Project
	Restricted bool
	// Detect, when set, finds the project of the working folder again each time an indexing ends: a folder that no session
	// had worked in may have been given sessions by it. A project found where there was none becomes the scope the views
	// restrict to; a failure leaves the scope as it is. It runs off the interface's loop, and is left nil when the user
	// named the scope (--all, --project), which detection must not change.
	Detect func() (kernel.Project, error)
}

// Run shows the interface until the user quits, then waits for a sync still running to stop.
func Run(o Options) error {
	m := New(o)
	m.dashboard.enabled, m.dashboard.open = true, true
	m.layout()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	m.stopSync()
	return err
}
