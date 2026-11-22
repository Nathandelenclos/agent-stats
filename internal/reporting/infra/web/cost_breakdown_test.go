package web

import (
	"math"
	"testing"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

func TestCostBreakdownUsesCurrentRatesAndReportsCoverage(t *testing.T) {
	got := costBreakdown([]reporting.UsageGroup{
		{Group: "known", Usage: reporting.Usage{Requests: 2, Input: 10, Output: 5, CacheRead: 20, CacheWrite: 10, Cost: 200}},
		{Group: "unknown", Usage: reporting.Usage{Requests: 3, Input: 10, Output: 5, CacheRead: 20}},
	}, func(model string) (TokenRates, bool) {
		return TokenRates{Input: 0.1, Output: 0.2, CacheRead: 0.05, CacheWrite: 0.15}, model == "known"
	})
	if got.Input != 1 || got.Output != 1 || got.CacheRead != 1 || got.CacheWrite != 1.5 ||
		got.CoveredRequests != 2 || got.CacheSavings == nil || math.Abs(*got.CacheSavings-0.125) > 1e-9 {
		t.Fatalf("cost breakdown = %+v", got)
	}
}
