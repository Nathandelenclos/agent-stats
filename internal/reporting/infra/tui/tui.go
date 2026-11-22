// Package tui is the interactive input adapter of the reporting context: a Bubble Tea program that
// shows the usage report in tabs and asks the application for a new report when the user changes the view.
// It holds presentation state only; every figure comes from the reporting use case.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Reports builds the usage report; reporting.BuildReport implements it.
type Reports interface {
	Run(reporting.Query) (reporting.Report, error)
}

// Options configures the interface.
type Options struct {
	Reports Reports
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
