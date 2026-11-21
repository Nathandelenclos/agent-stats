package ingest

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
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
func ParsePi(harness usage.Harness) Parser {
	return func(path string, r io.Reader) (usage.Transcript, error) {
		p := &usage.Transcript{Session: usage.Session{Harness: harness, AgentType: usage.MainAgent}}
		stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		p.Session.ID = harness.SessionID(stem)
		if m := sessionDir.FindStringSubmatch(stem); m != nil {
			p.Session.ID = harness.SessionID(m[1])
		}
		for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if m := sessionDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
				p.Session.ParentID = harness.SessionID(m[1])
				p.Session.ID = p.Session.ParentID + "/" + stem
				p.Session.AgentType = usage.Subagent
				break
			}
		}

		var provider, model string
		toolIndex := map[string]int{}
		err := eachLine(r, func(line []byte) {
			var l piLine
			if json.Unmarshal(line, &l) != nil {
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
					if pr, ok := usage.NewPrompt(l.ID, ts, strings.Join(parts, "\n")); ok {
						p.Prompts = append(p.Prompts, pr)
					}
				case "assistant":
					req := usage.Request{Key: l.ID, At: ts, Provider: m.Provider, Model: m.Model,
						Duration: millis(m.Duration), TTFT: millis(m.TTFT), StopReason: m.StopReason, Error: m.ErrorMessage}
					if req.Model == "" {
						req.Provider, req.Model = provider, model
					}
					if u := m.Usage; u != nil {
						req.Tokens = usage.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
						req.Reasoning = u.Reasoning
						if u.Cost != nil {
							req.LoggedCost = usage.USD(u.Cost.Total)
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
						server, name := usage.SplitMCPToolName(b.Name)
						toolIndex[b.ID] = len(p.Tools)
						p.Tools = append(p.Tools, usage.ToolCall{Key: b.ID, At: ts, Name: name, Server: server, ArgsChars: len(b.Arguments)})
					}
				case "toolResult":
					if i, ok := toolIndex[m.ToolCallID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError, t.Duration = len(m.Content), m.IsError, between(t.At, ts)
					}
				}
			}
		})
		p.TitleFromFirstPrompt()
		return *p, err
	}
}
