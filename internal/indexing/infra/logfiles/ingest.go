// Package ingest reads harness session logs from disk into usage transcripts.
// Each log file holds exactly one session (subagents get their own file).
package logfiles

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/files"
)

// Limits on what one log may cost to read. Logs are written by other programs: a corrupt or hostile
// file must not exhaust memory, while real lines (tool results) can weigh megabytes.
var (
	maxLineBytes       = 64 << 20 // a longer line is skipped and counted in Transcript.SkippedLines
	maxLogBytes  int64 = 8 << 30  // after decompression; a larger log is refused
)

const maxZstdMemory = 256 << 20

// ErrLogTooLarge reports a log larger than maxLogBytes once decompressed.
var ErrLogTooLarge = errors.New("session log too large")

// Parser reads one session log.
type Parser func(path string, r io.Reader) (domain.Transcript, error)

// ParseFile opens path (transparently decompressing .zst) and runs p on it.
func ParseFile(path string, p Parser) (domain.Transcript, error) {
	f, err := openRegular(path)
	if err != nil {
		return domain.Transcript{}, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		d, err := zstd.NewReader(f, zstd.WithDecoderMaxMemory(maxZstdMemory))
		if err != nil {
			return domain.Transcript{}, err
		}
		defer d.Close()
		r = d
	}
	return p(path, &capped{r: r, left: maxLogBytes})
}

// ErrNotRegular reports a log path that is not a regular file (a FIFO, a device…).
var ErrNotRegular = files.ErrNotRegular

// openRegular opens a file another program wrote, refusing anything but a regular file without blocking.
func openRegular(path string) (*os.File, error) { return files.OpenRegular(path) }

// capped fails with ErrLogTooLarge instead of reading past its limit.
type capped struct {
	r    io.Reader
	left int64
}

func (c *capped) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, fmt.Errorf("%w: over %d bytes", ErrLogTooLarge, maxLogBytes)
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// eachLine calls fn for every non-empty line up to maxLineBytes (tool results can be megabytes) and returns
// how many longer lines it skipped. fn must not keep the slice.
func eachLine(r io.Reader, fn func([]byte)) (oversized int, err error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var line []byte
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxLineBytes {
				tooLong = true
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			oversized++
		} else if l := bytes.TrimSpace(line); len(l) > 0 {
			fn(l)
		}
		line, tooLong = line[:0], false
		if err == io.EOF {
			return oversized, nil
		}
		if err != nil {
			return oversized, err
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
	return time.Duration(kernel.UnixMilli(end)-kernel.UnixMilli(start)) * time.Millisecond
}

// millis converts a duration logged in (possibly fractional) milliseconds.
func millis(ms float64) time.Duration {
	return domain.LoggedDuration(ms)
}
