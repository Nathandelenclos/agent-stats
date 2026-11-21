// Package ingest reads harness session logs from disk into usage transcripts.
// Each log file holds exactly one session (subagents get their own file).
package ingest

import (
	"bufio"
	"bytes"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

// Parser reads one session log.
type Parser func(path string, r io.Reader) (usage.Transcript, error)

// ParseFile opens path (transparently decompressing .zst) and runs p on it.
func ParseFile(path string, p Parser) (usage.Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return usage.Transcript{}, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		d, err := zstd.NewReader(f)
		if err != nil {
			return usage.Transcript{}, err
		}
		defer d.Close()
		r = d
	}
	return p(path, r)
}

// eachLine calls fn for every non-empty line, without a line-length limit (tool results can be megabytes).
func eachLine(r io.Reader, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// parseTS reads an RFC 3339 timestamp, keeping millisecond precision; zero when absent or invalid.
func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(t.UnixMilli())
}

// between is the time from start to end at millisecond precision, a missing time counting as the Unix epoch
// (as the index stores it).
func between(start, end time.Time) time.Duration {
	return time.Duration(usage.UnixMilli(end)-usage.UnixMilli(start)) * time.Millisecond
}

// millis converts a duration logged in (possibly fractional) milliseconds.
func millis(ms float64) time.Duration {
	return time.Duration(math.Round(ms * float64(time.Millisecond)))
}
