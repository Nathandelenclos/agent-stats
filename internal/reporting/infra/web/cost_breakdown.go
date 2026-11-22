package web

import reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"

// TokenRates are USD per token in the currently loaded public rate table.
type TokenRates struct {
	Input, Output, CacheRead, CacheWrite float64
}

type costBreakdownJSON struct {
	Input           float64  `json:"input"`
	Output          float64  `json:"output"`
	CacheRead       float64  `json:"cacheRead"`
	CacheWrite      float64  `json:"cacheWrite"`
	CacheSavings    *float64 `json:"cacheSavings"`
	CoveredRequests int64    `json:"coveredRequests"`
}

// costBreakdown reprices the reported token totals, independently of the historical index costs.
func costBreakdown(models []reporting.UsageGroup, lookup func(string) (TokenRates, bool)) *costBreakdownJSON {
	if lookup == nil {
		return nil
	}
	var comparablePrompt float64
	out := &costBreakdownJSON{}
	for _, model := range models {
		rates, found := lookup(model.Group)
		if !found {
			continue
		}
		out.Input += float64(model.Input) * rates.Input
		out.Output += float64(model.Output) * rates.Output
		out.CacheRead += float64(model.CacheRead) * rates.CacheRead
		out.CacheWrite += float64(model.CacheWrite) * rates.CacheWrite
		out.CoveredRequests += model.Requests
		comparablePrompt += float64(model.InputWithCache()) * rates.Input
	}
	if comparablePrompt > 0 {
		savings := (comparablePrompt - out.Input - out.CacheRead - out.CacheWrite) / comparablePrompt
		out.CacheSavings = &savings
	}
	return out
}
