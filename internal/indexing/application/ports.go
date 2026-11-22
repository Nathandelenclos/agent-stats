// Package application holds the use case of the indexing context and the ports it needs.
package application

import (
	"fmt"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// LogFile is one session log as found on disk. A log is a file, or one session of a database that holds many:
// Path is then "<database path>#<session id>".
type LogFile struct {
	Path    string
	Harness kernel.Harness
	// Revision says whether the log changed since it was indexed: the index keeps the one it saw, and reads the log
	// again when the reader reports another. Only the reader that produced a revision knows what it is made of (the
	// size and modification time of a file, what a database says of a session), so nothing else compares parts of it.
	Revision string
	// WeightBytes is what reading the log may hold in memory, in bytes of log text: the decompressed size of a
	// compressed log, which the file understates tenfold. It only paces the readers ahead of the writer and is not
	// part of the revision.
	WeightBytes int64
}

// FileRevision is the revision of a log that is a file: its size and its modification time in nanoseconds since the
// epoch. The readers of files give it, and the index gives it to a log an earlier version indexed, which it knows by
// those two: this is the only place that says how they make a revision.
func FileRevision(size, modTimeNanos int64) string {
	return fmt.Sprintf("%d:%d", size, modTimeNanos)
}

// SessionLogs finds and reads the session logs that harnesses write.
type SessionLogs interface {
	// List returns every log file; problems are files that could not be inspected and were skipped.
	List() (files []LogFile, problems []error, err error)
	// Read parses a file List returned. A zero session ID marks a file with no session to index.
	// IndexSessions calls it from several goroutines at once.
	Read(LogFile) (domain.Transcript, error)
}

// IndexedTranscript is a transcript with the costs and prompt signals derived from it.
type IndexedTranscript struct {
	domain.Transcript
	Costs   []PricedCost     // one per request
	Signals []domain.Signals // one per prompt
}

// PricedCost is the cost of one request; Priced is false when its cost cannot be determined.
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
