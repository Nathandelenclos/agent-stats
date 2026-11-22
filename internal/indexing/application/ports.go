// Package application holds the use case of the indexing context and the ports it needs.
package application

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// LogFile is one session log as found on disk. Size and ModTime tell whether it changed since it was indexed.
type LogFile struct {
	Path    string
	Harness kernel.Harness
	Size    int64
	ModTime time.Time
}

// SessionLogs finds and reads the session logs that harnesses write.
type SessionLogs interface {
	// List returns every log file; problems are files that could not be inspected and were skipped.
	List() (files []LogFile, problems []error, err error)
	Read(LogFile) (domain.Transcript, error)
}

// IndexedTranscript is a transcript with the costs and prompt signals derived from it.
type IndexedTranscript struct {
	domain.Transcript
	Costs   []PricedCost     // one per request
	Signals []domain.Signals // one per prompt
}

// PricedCost is the cost of one request; Priced is false when the model has no known price.
type PricedCost struct {
	Cost   kernel.Cost
	Priced bool
}

// IndexedLog is a log file with the transcript indexed from it.
type IndexedLog struct {
	File       LogFile
	Transcript IndexedTranscript
}

// TranscriptStore keeps indexed transcripts.
type TranscriptStore interface {
	// IndexedFiles returns the log files indexed so far, as they were when indexed.
	IndexedFiles() (map[string]LogFile, error)
	// Save replaces whatever was indexed from each log with its transcript, applying them in order, and
	// returns one error per log, nil for those that were saved. A log that fails leaves the index as it was
	// for that log and does not stop the others.
	Save(batch []IndexedLog) []error
}

// PriceList tells what tokens used with a model cost; false when the model has no known price.
type PriceList interface {
	Cost(model string, t kernel.Tokens) (kernel.Cost, bool)
}
