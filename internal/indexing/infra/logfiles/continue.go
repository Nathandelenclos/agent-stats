package logfiles

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

var continueFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}\.json$`)

func isContinueSession(name string) bool {
	return name != "sessions.json" && continueFileName.MatchString(name)
}

func openContinueSession(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err == nil && os.SameFile(info, opened) {
		return f, nil
	}
	f.Close()
	return nil, fmt.Errorf("%s: session file changed before reading", path)
}

type continueToolState struct {
	ToolCallID string
	Status     string
	Output     []struct{ Content string }
}

type continueHistoryItem struct {
	Message struct {
		Role      string
		Content   json.RawMessage
		Usage     json.RawMessage
		ToolCalls []struct {
			ID       string
			Function struct{ Name, Arguments string }
		}
	}
	ToolCallStates []continueToolState
}

// ParseContinue reads a cn snapshot as written by core/util/history.ts at 5522c6f44c.
// The history has no per-message timestamps.
func ParseContinue(path string, r io.Reader) (domain.Transcript, error) {
	name := filepath.Base(path)
	if !isContinueSession(name) {
		return domain.Transcript{}, fmt.Errorf("%s: %w: invalid filename", name, ErrConversationUnreadable)
	}
	// The snapshot is one JSON record, bounded like a single record of a line log.
	limit := int64(maxLineBytes)
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return domain.Transcript{}, err
	}
	if int64(len(raw)) > limit {
		return domain.Transcript{}, fmt.Errorf("%w: over %d bytes", ErrLogTooLarge, limit)
	}
	var session struct {
		SessionID          string
		Title              string
		WorkspaceDirectory string
		History            []json.RawMessage
	}
	if json.Unmarshal(raw, &session) != nil || session.History == nil ||
		session.SessionID != strings.TrimSuffix(name, ".json") {
		return domain.Transcript{}, fmt.Errorf("%s: %w: invalid session snapshot", name, ErrConversationUnreadable)
	}
	id, err := domain.NewSessionID(kernel.Continue, session.SessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	p := domain.Transcript{Session: domain.Session{
		ID: id, Harness: kernel.Continue, AgentType: domain.MainAgent, Title: domain.Title(session.Title),
	}}
	if folder := session.WorkspaceDirectory; filepath.IsAbs(folder) && strings.IndexFunc(folder, unicode.IsControl) < 0 {
		p.Session.Folder = folder
	}
	for i, raw := range session.History {
		var item continueHistoryItem
		if json.Unmarshal(raw, &item) != nil || item.Message.Role == "" {
			p.SkippedLines++
			continue
		}
		key := strconv.Itoa(i)
		switch item.Message.Role {
		case "user":
			if prompt, ok := domain.NewPrompt(key, time.Time{}, messageText(item.Message.Content)); ok {
				p.Prompts = append(p.Prompts, prompt)
			}
		case "assistant":
			if usage := item.Message.Usage; len(usage) > 0 && string(usage) != "null" {
				request, err := continueRequest(key, usage)
				if err == nil {
					p.Requests = append(p.Requests, request)
				} else {
					p.SkippedLines++
				}
			}
			p.Tools = append(p.Tools, item.tools(key)...)
		}
	}
	p.TitleFromFirstPrompt()
	return p, nil
}

// cn mirrors the calls in toolCallStates, where it records the result and status.
// https://github.com/continuedev/continue/blob/5522c6f44c/extensions/cli/src/stream/handleToolCalls.ts
func (item continueHistoryItem) tools(key string) []domain.ToolCall {
	states := make(map[string]continueToolState, len(item.ToolCallStates))
	for _, state := range item.ToolCallStates {
		states[state.ToolCallID] = state
	}
	seen := make(map[string]bool, len(item.Message.ToolCalls))
	var tools []domain.ToolCall
	for _, call := range item.Message.ToolCalls {
		if call.ID == "" || seen[call.ID] || call.Function.Name == "" ||
			strings.IndexFunc(call.Function.Name, unicode.IsControl) >= 0 {
			continue
		}
		seen[call.ID] = true
		server, name := domain.SplitMCPToolName(call.Function.Name)
		state := states[call.ID]
		tool := domain.ToolCall{
			Key: key + ":" + call.ID, Name: name, Server: server,
			ArgsChars: len(call.Function.Arguments), IsError: state.Status == "errored",
		}
		for _, output := range state.Output {
			tool.ResultChars += len(output.Content)
		}
		tools = append(tools, tool)
	}
	return tools
}

// cn saves the last usage packet of each response and adds the model and rounded cost.
// https://github.com/continuedev/continue/blob/5522c6f44c/extensions/cli/src/stream/streamChatResponse.ts
func continueRequest(key string, raw json.RawMessage) (domain.Request, error) {
	var usage struct {
		Input     *int64 `json:"prompt_tokens"`
		Output    *int64 `json:"completion_tokens"`
		Model     string `json:"model"`
		CostCents int64  `json:"cost_cents"`
		Prompt    struct {
			Cached     int64  `json:"cached_tokens"`
			CacheRead  *int64 `json:"cache_read_tokens"`
			CacheWrite *int64 `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
		Completion struct {
			Reasoning int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if json.Unmarshal(raw, &usage) != nil || usage.Input == nil || usage.Output == nil {
		return domain.Request{}, kernel.ErrInvalidTokens
	}
	counts := kernel.TokenCounts{Input: *usage.Input, Output: *usage.Output, Reasoning: usage.Completion.Reasoning}
	if counts.Input < 0 || counts.Input > kernel.MaxTokenCount {
		return domain.Request{}, kernel.ErrInvalidTokens
	}
	// Continue's Anthropic adapter emits both explicit cache fields alongside uncached input.
	// With only OpenAI's cached_tokens field, cached tokens are included in prompt_tokens.
	// https://github.com/continuedev/continue/blob/5522c6f44c/packages/openai-adapters/src/apis/Anthropic.ts
	if prompt := usage.Prompt; prompt.CacheRead != nil && prompt.CacheWrite != nil {
		counts.CacheRead, counts.CacheWrite = *prompt.CacheRead, *prompt.CacheWrite
	} else {
		if prompt.Cached < 0 || prompt.Cached > counts.Input {
			return domain.Request{}, kernel.ErrInvalidTokens
		}
		counts.CacheRead = prompt.Cached
		counts.Input -= prompt.Cached
	}
	tokens, err := kernel.NewTokens(counts)
	if err != nil {
		return domain.Request{}, err
	}
	cost, err := kernel.NewCost(float64(usage.CostCents) / 100)
	if err != nil {
		return domain.Request{}, err
	}
	return domain.Request{Key: key, Model: usage.Model, Tokens: tokens, LoggedCost: cost}, nil
}
