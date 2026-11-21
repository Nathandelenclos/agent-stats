package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeLine struct {
	Type              string `json:"type"`
	Timestamp         string `json:"timestamp"`
	SessionID         string `json:"sessionId"`
	Cwd               string `json:"cwd"`
	IsMeta            bool   `json:"isMeta"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	RequestRef        string `json:"requestRef"`
	ID                string `json:"id"`
	UUID              string `json:"uuid"`
	CustomTitle       string `json:"customTitle"`
	Message           *struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
		Usage      *struct {
			Input         int64 `json:"input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheDetail   *struct {
				OneHour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			OutputDetail *struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseClaude reads a Claude Code transcript (~/.claude/projects/<folder>/<session>.jsonl,
// subagents in <session>/subagents/agent-<id>.jsonl).
// Claude Code writes one line per content block, repeating the message's usage on each: requests are keyed by message id.
func ParseClaude(path string, r io.Reader) (*Parsed, error) {
	p := &Parsed{}
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	p.Session = Session{ID: "claude:" + base, Harness: "claude", AgentType: "main"}
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		parent := filepath.Base(filepath.Dir(filepath.Dir(path)))
		p.Session.ID = "claude:" + parent + "/" + base
		p.Session.ParentID = "claude:" + parent
		p.Session.AgentType = claudeAgentType(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	}

	requestStart := map[string]int64{} // api-request id -> ts
	reqIndex := map[string]int{}       // message id -> index in p.Requests
	toolIndex := map[string]int{}
	firstBlock := map[string]int64{}
	var lastModel string

	err := eachLine(r, func(line []byte) {
		var l claudeLine
		if json.Unmarshal(line, &l) != nil {
			return
		}
		ts := parseTS(l.Timestamp)
		if p.Session.Folder == "" && l.Cwd != "" {
			p.Session.Folder = l.Cwd
		}
		switch l.Type {
		case "custom-title":
			p.Session.Title = l.CustomTitle
		case "api-request":
			requestStart[l.ID] = ts
		case "assistant":
			if l.Message == nil {
				return
			}
			p.Session.span(ts)
			m := l.Message
			blocks := claudeBlocks(m.Content)
			if m.Model == "<synthetic>" {
				if l.IsAPIErrorMessage {
					p.Requests = append(p.Requests, Request{Key: "err:" + m.ID, TS: ts, Model: lastModel, Provider: "anthropic", Error: firstText(blocks)})
				}
				return
			}
			lastModel = m.Model
			i, seen := reqIndex[m.ID]
			if !seen {
				i = len(p.Requests)
				reqIndex[m.ID] = i
				firstBlock[m.ID] = ts
				p.Requests = append(p.Requests, Request{Key: m.ID, TS: ts, Model: m.Model, Provider: "anthropic"})
			}
			req := &p.Requests[i]
			if m.StopReason != "" {
				req.StopReason = m.StopReason
			}
			if u := m.Usage; u != nil {
				req.Input, req.Output, req.CacheRead, req.CacheWrite = u.Input, u.Output, u.CacheRead, u.CacheCreation
				if u.CacheDetail != nil {
					req.CacheWrite1h = u.CacheDetail.OneHour
				}
				if u.OutputDetail != nil {
					req.Reasoning = u.OutputDetail.Thinking
				}
			}
			// Block timestamps are written when each block completes: the first approximates TTFT, the last the duration.
			if start, ok := requestStart[l.RequestRef]; ok && start > 0 {
				req.TTFTMs = float64(firstBlock[m.ID] - start)
				req.DurationMs = float64(ts - start)
			}
			for _, b := range blocks {
				if b.Type != "tool_use" {
					continue
				}
				server, name := splitMCP(b.Name)
				toolIndex[b.ID] = len(p.Tools)
				p.Tools = append(p.Tools, ToolCall{Key: b.ID, TS: ts, Name: name, Server: server, ArgsChars: len(b.Input)})
			}
		case "user":
			if l.Message == nil || l.IsMeta {
				return
			}
			p.Session.span(ts)
			var text string
			if json.Unmarshal(l.Message.Content, &text) == nil {
				addClaudePrompt(p, l.UUID, ts, text)
				return
			}
			for j, b := range claudeBlocks(l.Message.Content) {
				switch b.Type {
				case "tool_result":
					if i, ok := toolIndex[b.ToolUseID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError = len(b.Content), b.IsError
						t.DurationMs = float64(ts - t.TS)
					}
				case "text":
					addClaudePrompt(p, fmt.Sprintf("%s:%d", l.UUID, j), ts, b.Text)
				}
			}
		}
	})
	return p.finish(), err
}

func addClaudePrompt(p *Parsed, key string, ts int64, text string) {
	// Skip harness-injected messages: command wrappers, reminders, interruptions.
	if t := strings.TrimSpace(text); t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") {
		return
	}
	p.Prompts = append(p.Prompts, Prompt{Key: key, TS: ts, Text: text})
}

func claudeBlocks(raw json.RawMessage) []claudeBlock {
	var blocks []claudeBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

func firstText(blocks []claudeBlock) string {
	for _, b := range blocks {
		if b.Type == "text" {
			return b.Text
		}
	}
	return "error"
}

func claudeAgentType(metaPath string) string {
	var meta struct {
		AgentType string `json:"agentType"`
	}
	if raw, err := os.ReadFile(metaPath); err == nil && json.Unmarshal(raw, &meta) == nil && meta.AgentType != "" {
		return meta.AgentType
	}
	return "subagent"
}
