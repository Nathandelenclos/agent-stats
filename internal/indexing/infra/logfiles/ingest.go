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
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
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
		d, err := zstdReader(f)
		if err != nil {
			return domain.Transcript{}, err
		}
		defer releaseZstd(d)
		r = d
	}
	return p(path, &capped{r: r, left: maxLogBytes})
}

// unknownZstdRatio is how much a compressed log is assumed to expand when its frame does not state its size:
// real rollouts reach 11 times (ponytail: a measured ratio per source if logs ever compress better than that).
const unknownZstdRatio = 16

// logWeight is what reading a log may hold in memory, in bytes of log text, for application.LogFile.Weight:
// zero for a plain log, whose size says it, and for a compressed one the content size its first frame states
// plus the decoder's window, or unknownZstdRatio times its size when it states none. It reads a few bytes.
func logWeight(path string, size int64) int64 {
	if !strings.HasSuffix(path, ".zst") {
		return 0
	}
	guess := min(size*unknownZstdRatio, maxLogBytes)
	f, err := openRegular(path)
	if err != nil {
		return guess
	}
	defer f.Close()
	var head [18]byte // the longest frame header
	n, _ := io.ReadFull(f, head[:])
	var h zstd.Header
	if h.Decode(head[:n]) != nil || !h.HasFCS {
		return guess
	}
	return min(int64(min(h.FrameContentSize, 1<<62))+int64(min(h.WindowSize, maxZstdMemory)), maxLogBytes)
}

// zstdDecoders keeps decoders between files: a new one allocates its window and block buffers, tens of
// megabytes over a run of hundreds of rollouts. A decoder holds no state across Reset.
var zstdDecoders sync.Pool

// zstdReader returns a decoder reading r, decoding in the caller's goroutine (files are parsed in parallel already).
func zstdReader(r io.Reader) (*zstd.Decoder, error) {
	if d, ok := zstdDecoders.Get().(*zstd.Decoder); ok {
		if err := d.Reset(r); err == nil {
			return d, nil
		}
		d.Close()
	}
	return zstd.NewReader(r, zstd.WithDecoderMaxMemory(maxZstdMemory), zstd.WithDecoderConcurrency(1))
}

func releaseZstd(d *zstd.Decoder) {
	if d.Reset(nil) == nil { // drops the reference to the file
		zstdDecoders.Put(d)
		return
	}
	d.Close()
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

// lineBuffers keeps the 1 MiB read buffers between files: allocating one per file dominated the cost of
// small logs, and the freed pages were handed back to the system just to be requested again.
var lineBuffers = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 1<<20) }}

// eachLine calls fn for every non-empty line up to maxLineBytes (tool results can be megabytes) and returns
// how many longer lines it skipped. fn must not keep the slice.
func eachLine(r io.Reader, fn func([]byte)) (oversized int, err error) {
	br := lineBuffers.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() {
		br.Reset(nil)
		lineBuffers.Put(br)
	}()
	var spill []byte // the start of a line longer than the read buffer; shorter lines are read in place
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if !tooLong {
				if len(spill)+len(chunk) > maxLineBytes {
					tooLong = true
				} else {
					spill = append(spill, chunk...)
				}
			}
			continue
		}
		line := chunk
		if !tooLong {
			if len(spill)+len(chunk) > maxLineBytes {
				tooLong = true
			} else if len(spill) > 0 {
				spill = append(spill, chunk...)
				line = spill
			}
		}
		if tooLong {
			oversized++
		} else if l := bytes.TrimSpace(line); len(l) > 0 {
			fn(l)
		}
		spill, tooLong = spill[:0], false
		if err == io.EOF {
			return oversized, nil
		}
		if err != nil {
			return oversized, err
		}
	}
}

// size is a JSON value of which only the length in bytes is kept (argument and result sizes of tool calls).
// Unlike json.RawMessage it copies nothing, and tool results are the bulk of a log.
type size int

func (n *size) UnmarshalJSON(b []byte) error {
	*n = size(len(b))
	return nil
}

// parseTS reads an RFC 3339 timestamp, keeping millisecond precision; zero when absent or invalid.
func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(t.UnixMilli())
}

// between is the time from start to end, see domain.Elapsed.
func between(start, end time.Time) domain.Duration { return domain.Elapsed(start, end) }

// millis converts a duration logged in (possibly fractional) milliseconds.
func millis(ms float64) domain.Duration {
	return domain.LoggedDuration(ms)
}
