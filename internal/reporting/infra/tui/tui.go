// Package tui is the interactive input adapter of the reporting context: a Bubble Tea program that
// shows the usage report in tabs and asks the application for a new report when the user changes the view.
// It holds presentation state only; every figure comes from the reporting use case.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Reports builds the usage report; reporting.BuildReport implements it.
type Reports interface {
	Run(reporting.Query) (reporting.Report, error)
}

// Options configures the interface.
type Options struct {
	Reports Reports
	Query   reporting.Query // what the first report asks for
}

// Run shows the interface until the user quits.
func Run(o Options) error {
	_, err := tea.NewProgram(New(o), tea.WithAltScreen()).Run()
	return err
}
