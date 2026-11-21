// Package app holds the use cases of agent-stats and the ports they need from the outside world.
package app

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

// LogFile is one session log as found on disk. Size and ModTime tell whether it changed since it was indexed.
type LogFile struct {
	Path    string
	Harness usage.Harness
	Size    int64
	ModTime time.Time
}

// SessionLogs finds and reads the session logs that harnesses write.
type SessionLogs interface {
	// List returns every log file; problems are files that could not be inspected and were skipped.
	List() (files []LogFile, problems []error, err error)
	Read(LogFile) (usage.Transcript, error)
}

// IndexedTranscript is a transcript with the costs and prompt signals derived from it.
type IndexedTranscript struct {
	usage.Transcript
	Costs   []PricedCost    // one per request
	Signals []usage.Signals // one per prompt
}

// PricedCost is the cost of one request; Priced is false when the model has no known price.
type PricedCost struct {
	USD    usage.USD
	Priced bool
}

// UsageIndex stores indexed transcripts and answers usage queries over them.
type UsageIndex interface {
	// IndexedFiles returns the log files indexed so far, as they were when indexed.
	IndexedFiles() (map[string]LogFile, error)
	// Save replaces whatever was indexed from file with t.
	Save(file LogFile, t IndexedTranscript) error
	Total(Filter) (Usage, error)
	UsageBy(Filter, Dimension) ([]UsageGroup, error)
	Tools(Filter) ([]ToolUsage, error)
}

// Filter restricts usage queries to activity since a time (zero: all) and optionally one harness (empty: all).
type Filter struct {
	Since   time.Time
	Harness usage.Harness
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
	Requests      int64   `json:"requests"`
	Failed        int64   `json:"failed"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheRead     int64   `json:"cacheRead"`
	CacheWrite    int64   `json:"cacheWrite"`
	Cost          float64 `json:"cost"`
	Unpriced      int64   `json:"unpriced"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	AvgTTFTMs     float64 `json:"avgTtftMs"`
	CacheRate     float64 `json:"cacheRate"` // share of prompt tokens served from cache
	FirstTS       int64   `json:"firstTs"`
	LastTS        int64   `json:"lastTs"`
}

// WithCacheRate returns u with CacheRate derived from its token counts.
func (u Usage) WithCacheRate() Usage {
	if prompt := u.Input + u.CacheRead + u.CacheWrite; prompt > 0 {
		u.CacheRate = float64(u.CacheRead) / float64(prompt)
	}
	return u
}

// UsageGroup is the usage of one group: a harness, a model, a folder or an agent type.
type UsageGroup struct {
	Group string `json:"key"`
	Usage
}

// ToolUsage aggregates calls to one tool.
type ToolUsage struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"` // MCP server, empty for built-in tools
	Calls       int64   `json:"calls"`
	Errors      int64   `json:"errors"`
	ArgsChars   int64   `json:"argsChars"`
	ResultChars int64   `json:"resultChars"`
	AvgMs       float64 `json:"avgMs"`
	LastTS      int64   `json:"lastTs"`
}
