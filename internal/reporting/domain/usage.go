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

// USD is a sum of request costs in US dollars.
type USD float64

// Milliseconds is an average duration in milliseconds, as the index computes it (not rounded).
type Milliseconds float64

// Usage aggregates model requests.
type Usage struct {
	Requests    int64
	Failed      int64
	Input       int64 // uncached input tokens
	Output      int64
	CacheRead   int64
	CacheWrite  int64
	Cost        USD
	Unpriced    int64 // requests whose model has no known price
	AvgDuration Milliseconds
	AvgTTFT     Milliseconds
	First, Last time.Time
}

// InputWithCache counts every input token: uncached, read from cache and written to cache.
func (u Usage) InputWithCache() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// CacheRate is the share of input tokens read from cache, 0 when there was no input.
func (u Usage) CacheRate() float64 {
	if in := u.InputWithCache(); in > 0 {
		return float64(u.CacheRead) / float64(in)
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
	AvgDuration Milliseconds
	Last        time.Time
}
