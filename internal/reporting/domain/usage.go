// Package domain models usage as it is reported: aggregates of model requests and tool calls,
// grouped along a few dimensions.
package domain

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Filter restricts a report to activity since a time (zero: all) and optionally one harness (empty: all).
type Filter struct {
	Since   time.Time
	Harness kernel.Harness
}

// Dimension is a way to group usage.
type Dimension int

const (
	ByHarness Dimension = iota
	ByModel
	ByFolder
	ByAgentType
)

// Usage aggregates model requests.
type Usage struct {
	Requests      int64
	Failed        int64
	Input         int64
	Output        int64
	CacheRead     int64
	CacheWrite    int64
	CostUSD       float64
	Unpriced      int64 // requests whose model has no known price
	AvgDurationMs float64
	AvgTTFTMs     float64
	First, Last   time.Time
}

// CacheRate is the share of prompt tokens served from cache, 0 when there was no prompt.
func (u Usage) CacheRate() float64 {
	if prompt := u.Input + u.CacheRead + u.CacheWrite; prompt > 0 {
		return float64(u.CacheRead) / float64(prompt)
	}
	return 0
}

// UsageGroup is the usage of one group: a harness, a model, a folder or an agent type.
type UsageGroup struct {
	Group string
	Usage
}

// ToolUsage aggregates calls to one tool.
type ToolUsage struct {
	Name        string
	Server      string // MCP server, empty for built-in tools
	Calls       int64
	Errors      int64
	ArgsChars   int64
	ResultChars int64
	AvgMs       float64
	Last        time.Time
}
