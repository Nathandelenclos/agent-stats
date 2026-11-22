package logfiles

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

type piBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments size   `json:"arguments"`
}

// piContent is a message's content: a list of blocks, and its size in bytes.
// Decoded by encoding/json the blocks are there at once; the scanner leaves them in raw, to be decoded by
// blocks() for the messages that need them (the bulk of a log is tool results, which only need the size).
type piContent struct {
	Blocks []piBlock
	Size   int
	raw    []byte // the content as written, until blocks() decodes it; points into the line being parsed
}

func (c *piContent) UnmarshalJSON(b []byte) error {
	c.Size = len(b)
	_ = json.Unmarshal(b, &c.Blocks)
	return nil
}

// blocks returns the content's blocks, decoding them the first time.
func (c *piContent) blocks() []piBlock {
	if c.raw != nil {
		c.Blocks, c.raw = decodePiBlocks(c.raw), nil
	}
	return c.Blocks
}

type piCost struct {
	Total float64 `json:"total"`
}

type piUsageLog struct {
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Reasoning  int64   `json:"reasoningTokens"`
	Cost       *piCost `json:"cost"`
}

type piMessage struct {
	Role         string      `json:"role"`
	Content      piContent   `json:"content"`
	Provider     string      `json:"provider"`
	Model        string      `json:"model"`
	StopReason   string      `json:"stopReason"`
	ErrorMessage string      `json:"errorMessage"`
	Duration     float64     `json:"duration"`
	TTFT         float64     `json:"ttft"`
	ToolCallID   string      `json:"toolCallId"`
	IsError      bool        `json:"isError"`
	Usage        *piUsageLog `json:"usage"`
}

type piLine struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	Timestamp string     `json:"timestamp"`
	Cwd       string     `json:"cwd"`
	Title     string     `json:"title"`
	Model     string     `json:"model"`
	Provider  string     `json:"provider"`
	ModelID   string     `json:"modelId"`
	Message   *piMessage `json:"message"`
}

// sessionDir matches pi/OMP session file and directory names: "<ISO time>_<uuid>".
var sessionDir = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d-]+Z_([0-9a-f-]{36})$`)

// ParsePi reads a pi or oh-my-pi session (~/.pi/agent/sessions, ~/.omp/agent/sessions).
// Subagent sessions are stored in a directory named after their parent session.
func ParsePi(harness kernel.Harness) Parser {
	return func(path string, r io.Reader) (domain.Transcript, error) {
		p := &domain.Transcript{Session: domain.Session{Harness: harness, AgentType: domain.MainAgent}}
		stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		native := stem
		if m := sessionDir.FindStringSubmatch(stem); m != nil {
			native = m[1]
		}
		id, err := domain.NewSessionID(harness, native)
		if err != nil {
			return domain.Transcript{}, err
		}
		p.Session.ID = id
		for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if m := sessionDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
				parent, err := domain.NewSessionID(harness, m[1])
				if err != nil {
					return domain.Transcript{}, err
				}
				if p.Session.ID, err = parent.Subagent(stem); err != nil {
					return domain.Transcript{}, err
				}
				p.Session.ParentID = parent
				p.Session.AgentType = domain.Subagent
				break
			}
		}

		var provider, model string
		toolIndex := map[string]int{}
		oversized, err := eachLine(r, func(line []byte) {
			l, ok := decodeLine(line, scanPiLine)
			if !ok {
				p.SkippedLines++
				return
			}
			ts := parseTS(l.Timestamp)
			switch l.Type {
			case "session":
				p.Session.Folder = l.Cwd
				if l.Title != "" {
					p.Session.Title = l.Title
				}
			case "title", "title_change":
				if l.Title != "" {
					p.Session.Title = l.Title
				}
			case "model_change":
				provider, model = l.Provider, l.ModelID
				if l.Model != "" {
					provider, model, _ = strings.Cut(l.Model, "/")
					if model == "" {
						provider, model = "", l.Model
					}
				}
			case "message":
				m := l.Message
				if m == nil {
					return
				}
				p.Session.Span(ts)
				switch m.Role {
				case "user":
					var parts []string
					for _, b := range m.Content.blocks() {
						if b.Type == "text" {
							parts = append(parts, b.Text)
						}
					}
					if pr, ok := domain.NewPrompt(l.ID, ts, strings.Join(parts, "\n")); ok {
						p.Prompts = append(p.Prompts, pr)
					}
				case "assistant":
					req := domain.Request{Key: l.ID, At: ts, Provider: m.Provider, Model: m.Model,
						Duration: millis(m.Duration), TTFT: millis(m.TTFT), StopReason: m.StopReason, Failure: m.ErrorMessage}
					if req.Model == "" {
						req.Provider, req.Model = provider, model
					}
					if req.Failure == "" && req.StopReason == piStopError {
						req.Failure = domain.UnspecifiedFailure
					}
					if err := piUsage(&req, m.Usage); err == nil {
						p.Requests = append(p.Requests, req)
					} else {
						p.SkippedLines++
					}
					for _, b := range m.Content.blocks() {
						if b.Type != "toolCall" {
							continue
						}
						server, name := domain.SplitMCPToolName(b.Name)
						toolIndex[b.ID] = len(p.Tools)
						p.Tools = append(p.Tools, domain.ToolCall{Key: b.ID, At: ts, Name: name, Server: server, ArgsChars: int(b.Arguments)})
					}
				case "toolResult":
					if i, ok := toolIndex[m.ToolCallID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError, t.Duration = m.Content.Size, m.IsError, between(t.At, ts)
					}
				}
			}
		})
		p.SkippedLines += oversized
		p.TitleFromFirstPrompt()
		return *p, err
	}
}

// piStopError is the stop reason pi and oh-my-pi write for a failed request.
const piStopError = "error"

// piUsage sets the tokens and the logged cost of req; an impossible value refuses the request.
func piUsage(req *domain.Request, u *piUsageLog) error {
	if u == nil {
		return nil
	}
	tokens, err := kernel.NewTokens(kernel.TokenCounts{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead,
		CacheWrite: u.CacheWrite, Reasoning: u.Reasoning})
	if err != nil {
		return err
	}
	req.Tokens = tokens
	if u.Cost != nil {
		if req.LoggedCost, err = kernel.NewCost(u.Cost.Total); err != nil {
			return err
		}
	}
	return nil
}
