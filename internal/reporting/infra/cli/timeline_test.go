package cli

import (
	"strings"
	"testing"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

func TestDailyUsagePrintsDatedValuesInReadingOrderWithoutStyling(t *testing.T) {
	var output strings.Builder
	WriteDaily(&output, []reporting.UsageGroup{
		{Group: "2026-11-01", Usage: reporting.Usage{Requests: 3, Input: 100, CacheRead: 500, CacheWrite: 50, Output: 1, Cost: 0.000021, Unpriced: 1}},
		{Group: "2026-11-03", Usage: reporting.Usage{Requests: 4, Unpriced: 4}},
	})
	got := output.String()
	want := "\nDaily usage (dated requests only)\n2026-11-01\n  3 requests\n  651 tokens\n  0.000021 USD known cost\n  1 request without a cost\n2026-11-03\n  4 requests\n  0 tokens\n  Cost unavailable\n"
	if got != want {
		t.Errorf("daily usage:\n%s\nwant:\n%s", got, want)
	}
}
