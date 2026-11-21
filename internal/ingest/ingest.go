// Package ingest turns harness session logs into one common shape.
// Each log file holds exactly one session (subagents get their own file).
package ingest

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Session is one conversation with an agent harness.
type Session struct {
	ID        string // unique across harnesses: "<harness>:<native id>"
	Harness   string
	ParentID  string // set for subagents
	AgentType string // "main", "subagent" or the subagent's declared type
	Title     string
	Folder    string
	Start     int64 // unix ms
	End       int64
}

// Request is one model call and its token usage.
type Request struct {
	Key          string // unique within the file
	TS           int64
	Model        string
	Provider     string
	Input        int64 // uncached input tokens
	Output       int64 // includes reasoning tokens
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64 // part of CacheWrite with a 1-hour TTL (Anthropic)
	Reasoning    int64
	Cost         float64 // as logged by the harness; 0 means "price it from the table"
	DurationMs   float64 // 0 when unknown
	TTFTMs       float64
	StopReason   string
	Error        string
}

// ToolCall is one tool invocation by the model.
type ToolCall struct {
	Key         string
	TS          int64
	Name        string
	Server      string // MCP server name, empty for built-in tools
	ArgsChars   int
	ResultChars int
	IsError     bool
	DurationMs  float64
}

// Prompt is one message typed by a human (or sent by a parent agent to a subagent).
type Prompt struct {
	Key  string
	TS   int64
	Text string
}

// Parsed is everything extracted from one log file.
type Parsed struct {
	Session  Session
	Requests []Request
	Tools    []ToolCall
	Prompts  []Prompt
}

// Parser reads one log file.
type Parser func(path string, r io.Reader) (*Parsed, error)

// ParseFile opens path (transparently decompressing .zst) and runs p on it.
func ParseFile(path string, p Parser) (*Parsed, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		d, err := zstd.NewReader(f)
		if err != nil {
			return nil, err
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

func parseTS(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// splitMCP recognises the "mcp__<server>__<tool>" naming used by Claude Code and others.
func splitMCP(name string) (server, tool string) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", name
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok {
		return "", name
	}
	return server, tool
}

// span widens the session time range to include ts.
func (s *Session) span(ts int64) {
	if ts == 0 {
		return
	}
	if s.Start == 0 || ts < s.Start {
		s.Start = ts
	}
	if ts > s.End {
		s.End = ts
	}
}

// finish fills the title from the first prompt when the harness did not log one.
func (p *Parsed) finish() *Parsed {
	if p.Session.Title == "" && len(p.Prompts) > 0 {
		t := strings.Join(strings.Fields(p.Prompts[0].Text), " ")
		if r := []rune(t); len(r) > 80 {
			t = string(r[:80]) + "…"
		}
		p.Session.Title = t
	}
	return p
}
