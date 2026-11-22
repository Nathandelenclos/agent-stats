package logfiles

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
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
func ParseClaude(path string, r io.Reader) (domain.Transcript, error) {
	p := &domain.Transcript{}
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	id, err := domain.NewSessionID(kernel.Claude, base)
	if err != nil {
		return domain.Transcript{}, err
	}
	p.Session = domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent}
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		parent, err := domain.NewSessionID(kernel.Claude, filepath.Base(filepath.Dir(filepath.Dir(path))))
		if err != nil {
			return domain.Transcript{}, err
		}
		if p.Session.ID, err = parent.Subagent(base); err != nil {
			return domain.Transcript{}, err
		}
		p.Session.ParentID = parent
		p.Session.AgentType = claudeAgentType(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	}
	invalid := map[string]bool{} // message ids whose token counts are impossible

	requestStart := map[string]time.Time{} // api-request id -> ts
	reqIndex := map[string]int{}           // message id -> index in p.Requests
	toolIndex := map[string]int{}
	firstBlock := map[string]time.Time{}
	var lastModel string

	oversized, err := eachLine(r, func(line []byte) {
		var l claudeLine
		if json.Unmarshal(line, &l) != nil {
			p.SkippedLines++
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
			p.Session.Span(ts)
			m := l.Message
			blocks := claudeBlocks(m.Content)
			if m.Model == "<synthetic>" {
				if l.IsAPIErrorMessage {
					p.Requests = append(p.Requests, domain.Request{Key: "err:" + m.ID, At: ts, Model: lastModel, Provider: "anthropic", Failure: firstText(blocks)})
				}
				return
			}
			lastModel = m.Model
			i, seen := reqIndex[m.ID]
			if !seen {
				i = len(p.Requests)
				reqIndex[m.ID] = i
				firstBlock[m.ID] = ts
				p.Requests = append(p.Requests, domain.Request{Key: m.ID, At: ts, Model: m.Model, Provider: "anthropic"})
			}
			req := &p.Requests[i]
			if m.StopReason != "" {
				req.StopReason = m.StopReason
			}
			if u := m.Usage; u != nil {
				c := kernel.TokenCounts{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation}
				if u.CacheDetail != nil {
					c.CacheWrite1h = u.CacheDetail.OneHour
				}
				if u.OutputDetail != nil {
					c.Reasoning = u.OutputDetail.Thinking
				}
				if tokens, err := kernel.NewTokens(c); err == nil {
					req.Tokens = tokens
				} else if !invalid[m.ID] {
					invalid[m.ID] = true
					p.SkippedLines++
				}
			}
			// Block timestamps are written when each block completes: the first approximates TTFT, the last the duration.
			if start, ok := requestStart[l.RequestRef]; ok && !start.IsZero() {
				req.TTFT = between(start, firstBlock[m.ID])
				req.Duration = between(start, ts)
			}
			for _, b := range blocks {
				if b.Type != "tool_use" {
					continue
				}
				server, name := domain.SplitMCPToolName(b.Name)
				toolIndex[b.ID] = len(p.Tools)
				p.Tools = append(p.Tools, domain.ToolCall{Key: b.ID, At: ts, Name: name, Server: server, ArgsChars: len(b.Input)})
			}
		case "user":
			if l.Message == nil || l.IsMeta {
				return
			}
			p.Session.Span(ts)
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
						t.Duration = between(t.At, ts)
					}
				case "text":
					addClaudePrompt(p, fmt.Sprintf("%s:%d", l.UUID, j), ts, b.Text)
				}
			}
		}
	})
	p.SkippedLines += oversized
	if len(invalid) > 0 {
		valid := p.Requests[:0]
		for _, r := range p.Requests {
			if !invalid[r.Key] {
				valid = append(valid, r)
			}
		}
		p.Requests = valid
	}
	p.TitleFromFirstPrompt()
	return *p, err
}

func addClaudePrompt(p *domain.Transcript, key string, ts time.Time, text string) {
	// Skip harness-injected messages: command wrappers, reminders, interruptions.
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") {
		return
	}
	if pr, ok := domain.NewPrompt(key, ts, text); ok {
		p.Prompts = append(p.Prompts, pr)
	}
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
	return domain.UnspecifiedFailure
}

// maxMetaBytes bounds the subagent .meta.json read next to a transcript (real ones are a few hundred bytes).
const maxMetaBytes = 64 << 10

// claudeAgentType reads the subagent type Claude Code stores next to the transcript, "subagent" when absent, unusable or "main".
func claudeAgentType(metaPath string) domain.AgentType {
	f, err := openRegular(metaPath)
	if err != nil {
		return domain.Subagent
	}
	defer f.Close()
	var meta struct {
		AgentType string `json:"agentType"`
	}
	if json.NewDecoder(io.LimitReader(f, maxMetaBytes)).Decode(&meta) != nil {
		return domain.Subagent
	}
	if t, ok := domain.ParseAgentType(meta.AgentType); ok && t != domain.MainAgent { // a file under subagents/ is never a main conversation
		return t
	}
	return domain.Subagent
}
