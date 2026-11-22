package cli

import (
	"fmt"
	"io"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// WriteDaily prints daily values in reading order, without terminal styling or interactivity.
func WriteDaily(w io.Writer, daily []reporting.UsageGroup) {
	fmt.Fprintln(w, "\nDaily usage (dated requests only)")
	if len(daily) == 0 {
		fmt.Fprintln(w, "No dated requests in this range.")
	}
	for _, d := range daily {
		for _, line := range textfmt.UsageDay(d.Group, d.Requests, d.InputWithCache()+d.Output, float64(d.Cost), d.Unpriced) {
			fmt.Fprintln(w, line)
		}
	}
}
