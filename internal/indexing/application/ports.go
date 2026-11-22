// Package application holds the use case of the indexing context and the ports it needs.
package application

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// LogFile is one session log as found on disk. Size and ModTime tell whether it changed since it was indexed.
// A log is a file, or one session of a database that holds many: Path is then "<database path>#<session id>", and
// Size counts the session's messages instead of bytes (Weight, an estimate in bytes, then says what reading it holds).
type LogFile struct {
	Path    string
	Harness kernel.Harness
	Size    int64
	ModTime time.Time
	// Weight is what reading the log may hold in memory, in bytes of log text: the decompressed size of a
	// compressed log, which Size understates tenfold. Zero means Size. Only List sets it.
	Weight int64
}

// weight is what the log counts for in the window that bounds what is read ahead.
func (f LogFile) weight() int64 {
	if f.Weight > 0 {
		return f.Weight
	}
	return f.Size
}

// SessionLogs finds and reads the session logs that harnesses write.
type SessionLogs interface {
	// List returns every log file; problems are files that could not be inspected and were skipped.
	List() (files []LogFile, problems []error, err error)
	// Read parses a file List returned. IndexSessions calls it from several goroutines at once.
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
