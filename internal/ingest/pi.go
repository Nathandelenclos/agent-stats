package ingest

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type piLine struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	ModelID   string `json:"modelId"`
	Message   *struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		Provider     string          `json:"provider"`
		Model        string          `json:"model"`
		StopReason   string          `json:"stopReason"`
		ErrorMessage string          `json:"errorMessage"`
		Duration     float64         `json:"duration"`
		TTFT         float64         `json:"ttft"`
		ToolCallID   string          `json:"toolCallId"`
		IsError      bool            `json:"isError"`
		Usage        *struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheRead  int64 `json:"cacheRead"`
			CacheWrite int64 `json:"cacheWrite"`
			Reasoning  int64 `json:"reasoningTokens"`
			Cost       *struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
	} `json:"message"`
}

// sessionDir matches pi/OMP session file and directory names: "<ISO time>_<uuid>".
var sessionDir = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d-]+Z_([0-9a-f-]{36})$`)

// ParsePi reads a pi or oh-my-pi session (~/.pi/agent/sessions, ~/.omp/agent/sessions).
// Subagent sessions are stored in a directory named after their parent session.
func ParsePi(harness string) Parser {
	return func(path string, r io.Reader) (*Parsed, error) {
		p := &Parsed{Session: Session{Harness: harness, AgentType: "main"}}
		stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		p.Session.ID = harness + ":" + stem
		if m := sessionDir.FindStringSubmatch(stem); m != nil {
			p.Session.ID = harness + ":" + m[1]
		}
		for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if m := sessionDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
				p.Session.ParentID = harness + ":" + m[1]
				p.Session.ID = p.Session.ParentID + "/" + stem
				p.Session.AgentType = "subagent"
				break
			}
		}

		var provider, model string
		toolIndex := map[string]int{}
		err := eachLine(r, func(line []byte) {
			var l piLine
			if json.Unmarshal(line, &l) != nil {
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
				p.Session.span(ts)
				var blocks []piBlock
				_ = json.Unmarshal(m.Content, &blocks)
				switch m.Role {
				case "user":
					var parts []string
					for _, b := range blocks {
						if b.Type == "text" {
							parts = append(parts, b.Text)
						}
					}
					if text := strings.Join(parts, "\n"); strings.TrimSpace(text) != "" {
						p.Prompts = append(p.Prompts, Prompt{Key: l.ID, TS: ts, Text: text})
					}
				case "assistant":
					req := Request{Key: l.ID, TS: ts, Provider: m.Provider, Model: m.Model,
						DurationMs: m.Duration, TTFTMs: m.TTFT, StopReason: m.StopReason, Error: m.ErrorMessage}
					if req.Model == "" {
						req.Provider, req.Model = provider, model
					}
					if u := m.Usage; u != nil {
						req.Input, req.Output, req.CacheRead, req.CacheWrite, req.Reasoning = u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Reasoning
						if u.Cost != nil {
							req.Cost = u.Cost.Total
						}
					}
					if req.Error == "" && req.StopReason == "error" {
						req.Error = "error"
					}
					p.Requests = append(p.Requests, req)
					for _, b := range blocks {
						if b.Type != "toolCall" {
							continue
						}
						server, name := splitMCP(b.Name)
						toolIndex[b.ID] = len(p.Tools)
						p.Tools = append(p.Tools, ToolCall{Key: b.ID, TS: ts, Name: name, Server: server, ArgsChars: len(b.Arguments)})
					}
				case "toolResult":
					if i, ok := toolIndex[m.ToolCallID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError, t.DurationMs = len(m.Content), m.IsError, float64(ts-t.TS)
					}
				}
			}
		})
		return p.finish(), err
	}
}
