package logfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func clineSources(home, codeUser string) []Source {
	data := filepath.Join(home, ".cline", "data")
	for _, override := range []struct{ key, suffix string }{{"CLINE_DIR", "data"}, {"CLINE_DATA_DIR", ""}} {
		if dir := strings.TrimSpace(os.Getenv(override.key)); dir != "" {
			data = filepath.Join(dir, override.suffix)
		}
	}
	sessions := strings.TrimSpace(os.Getenv("CLINE_SESSION_DATA_DIR"))
	if sessions == "" {
		sessions = filepath.Join(data, "sessions")
	}
	sessions = filepath.Clean(sessions)
	tasks := filepath.Join(codeUser, "globalStorage", "saoudrizwan.claude-dev", "tasks")
	oldTask := func(manifest string) string {
		return filepath.Join(tasks, filepath.Base(filepath.Dir(manifest)), "ui_messages.json")
	}
	return []Source{
		{Harness: kernel.Cline, Root: tasks, Match: func(path string) bool {
			return filepath.Dir(filepath.Dir(path)) == tasks && filepath.Base(path) == "ui_messages.json"
		}, Parse: parseClineTask, Beside: func(path string) string { return companionRevision(clineTaskHistory(path)) }},
		{Harness: kernel.Cline, Root: sessions, Match: func(path string) bool {
			dir := filepath.Dir(path)
			return filepath.Dir(dir) == sessions && filepath.Base(path) == filepath.Base(dir)+".json"
		}, Parse: func(path string, r io.Reader) (domain.Transcript, error) {
			return parseClineSession(path, r, regularLog(oldTask(path)))
		}, Beside: func(path string) string { return companionRevision(clineSessionMessages(path), oldTask(path)) }},
		{Harness: kernel.Cline, Root: sessions, Match: func(path string) bool {
			dir := filepath.Dir(path)
			return filepath.Dir(dir) == sessions && strings.HasSuffix(path, ".messages.json") &&
				filepath.Base(path) != filepath.Base(dir)+".messages.json"
		}, Parse: parseClineChild, Beside: func(path string) string { return companionRevision(clineParentManifest(path)) }},
	}
}

func clineTaskHistory(path string) string {
	return filepath.Join(filepath.Dir(path), "api_conversation_history.json")
}

func clineSessionMessages(path string) string {
	return strings.TrimSuffix(path, ".json") + ".messages.json"
}

func clineParentManifest(path string) string {
	dir := filepath.Dir(path)
	return filepath.Join(dir, filepath.Base(dir)+".json")
}

type clineManifest struct {
	Version       int
	SessionID     string `json:"session_id"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	CWD           string
	WorkspaceRoot string `json:"workspace_root"`
	Prompt        string
}

func (m clineManifest) folder() string {
	if cwd := localFolder(m.WorkspaceRoot); cwd != "" {
		return cwd
	}
	return localFolder(m.CWD)
}

type clineConversation struct {
	Version   int
	SessionID string
	Agent     string
	Origin    struct{ ParentThreadID, Subagent string }
	Messages  []clineMessage
}

type clineMessage struct {
	ID, Role, SessionID string
	TS                  int64
	Content             json.RawMessage
	Metadata            struct{ DisplayOnly bool }
	ModelInfo           struct{ ID, Provider string }
	Metrics             *struct {
		InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens *int64
		Cost                                                         *float64
	}
}

func parseClineSession(path string, r io.Reader, hasOldTask bool) (domain.Transcript, error) {
	var m clineManifest
	if err := decodeConversation(r, &m); err != nil {
		return domain.Transcript{}, err
	}
	if m.Version != 1 || m.SessionID != filepath.Base(filepath.Dir(path)) {
		return domain.Transcript{}, errors.New("unrecognized Cline session manifest")
	}
	id, err := domain.NewSessionID(kernel.Cline, m.SessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.Cline, AgentType: domain.MainAgent, Folder: m.folder()}}
	t.Session.Span(parseTS(m.StartedAt))
	t.Session.Span(parseTS(m.EndedAt))
	var conv clineConversation
	if err := readConversation(clineSessionMessages(path), &conv); err == nil {
		if conv.Version != 1 || conv.SessionID != m.SessionID || conv.Agent != "lead" {
			return domain.Transcript{}, errors.New("unrecognized Cline session messages")
		}
		conv.Messages = clineResumedMessages(conv.Messages, hasOldTask)
		appendClineMessages(&t, conv)
	} else if !os.IsNotExist(err) {
		return domain.Transcript{}, fmt.Errorf("Cline session messages: %w", err)
	}
	if len(t.Prompts) == 0 && !hasOldTask {
		if p, ok := domain.NewPrompt("manifest", parseTS(m.StartedAt), m.Prompt); ok {
			t.Prompts = append(t.Prompts, p)
		}
	}
	t.Session.Title = domain.Title(m.Prompt)
	t.TitleFromFirstPrompt()
	return t, nil
}

func clineResumedMessages(messages []clineMessage, hasOldTask bool) []clineMessage {
	const resumeWarning = "Warning: this is a legacy conversation, which means tool names may have changed."
	for i, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		text, _, _ := clineContent(msg.Content)
		if !strings.HasPrefix(text, resumeWarning) {
			continue
		}
		if hasOldTask {
			return messages[i+1:]
		}
		// The SDK copies the old task total into the last imported assistant message.
		for j := range i {
			messages[j].Metrics = nil
		}
		return append(messages[:i], messages[i+1:]...)
	}
	return messages
}

func parseClineChild(path string, r io.Reader) (domain.Transcript, error) {
	var conv clineConversation
	if err := decodeConversation(r, &conv); err != nil {
		return domain.Transcript{}, err
	}
	root := filepath.Base(filepath.Dir(path))
	if conv.Version != 1 || (conv.Agent != "subagent" && conv.Agent != "teammate") ||
		conv.Origin.ParentThreadID != root || !strings.HasPrefix(conv.SessionID, root+"__") {
		return domain.Transcript{}, nil
	}
	id, err := domain.NewSessionID(kernel.Cline, conv.SessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	parent, err := domain.NewSessionID(kernel.Cline, conv.Origin.ParentThreadID)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.Cline, AgentType: domain.Subagent, ParentID: parent}}
	var m clineManifest
	if err := readConversation(clineParentManifest(path), &m); err == nil && m.Version == 1 && m.SessionID == root {
		t.Session.Folder = m.folder()
	}
	appendClineMessages(&t, conv)
	t.TitleFromFirstPrompt()
	if t.Session.Title == "" {
		t.Session.Title = domain.Title(conv.Origin.Subagent)
	}
	return t, nil
}

type clinePart struct {
	Type, ID, Name, Text string
	ToolUseID            string `json:"tool_use_id"`
	Input                json.RawMessage
	Content              json.RawMessage
	IsError              bool `json:"is_error"`
}

func clineContent(raw json.RawMessage) (string, []clinePart, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, true
	}
	var text string
	if raw[0] == '"' {
		ok := json.Unmarshal(raw, &text) == nil
		return text, nil, ok
	}
	var parts []clinePart
	if json.Unmarshal(raw, &parts) != nil {
		return "", nil, false
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), parts, true
}

func appendClineMessages(t *domain.Transcript, conv clineConversation) {
	seen := map[string]bool{}
	tools := map[string]int{}
	var assistant []clineMessage
	for i, msg := range conv.Messages {
		if msg.SessionID != "" && msg.SessionID != conv.SessionID || msg.Metadata.DisplayOnly {
			continue
		}
		if msg.ID == "" {
			msg.ID = "message:" + strconv.Itoa(i)
		}
		if seen[msg.ID] {
			continue
		}
		seen[msg.ID] = true
		at := epochMillis(msg.TS)
		t.Session.Span(at)
		text, parts, ok := clineContent(msg.Content)
		if !ok {
			t.SkippedLines++
			continue
		}
		hasResult := appendClineTools(t, tools, msg, parts)
		switch msg.Role {
		case "user":
			if !hasResult && strings.TrimSpace(text) != "" {
				appendClineRequests(t, assistant)
				assistant = assistant[:0]
				if p, ok := domain.NewPrompt(msg.ID, at, text); ok {
					t.Prompts = append(t.Prompts, p)
				}
			}
		case "assistant":
			assistant = append(assistant, msg)
		}
	}
	appendClineRequests(t, assistant)
}

func appendClineRequests(t *domain.Transcript, assistant []clineMessage) {
	var previousMetrics bool
	for i, msg := range assistant {
		m := msg.Metrics
		if m == nil || m.InputTokens == nil && m.OutputTokens == nil && m.CacheReadTokens == nil && m.CacheWriteTokens == nil && m.Cost == nil {
			continue
		}
		// shortcut: a lone terminal metric in a multi-call run may be a run total, import it when Cline marks its scope.
		if i > 0 && i == len(assistant)-1 && !previousMetrics {
			continue
		}
		previousMetrics = true
		input, output, cacheRead, cacheWrite := tokenValue(m.InputTokens), tokenValue(m.OutputTokens), tokenValue(m.CacheReadTokens), tokenValue(m.CacheWriteTokens)
		count := kernel.TokenCounts{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite}
		// The SDK embeds both cache counts in inputTokens; legacy task metrics already separate them.
		tokens, err := kernel.NewTokens(count)
		if err == nil && m.InputTokens != nil {
			count.Input -= cacheRead + cacheWrite
			tokens, err = kernel.NewTokens(count)
		}
		var cost kernel.Cost
		var costErr error
		if m.Cost != nil {
			cost, costErr = kernel.NewCost(*m.Cost)
		}
		if err != nil || costErr != nil {
			t.SkippedLines++
		}
		t.Requests = append(t.Requests, domain.Request{Key: msg.ID, At: epochMillis(msg.TS), Model: msg.ModelInfo.ID, Provider: msg.ModelInfo.Provider,
			Tokens: tokens, LoggedCost: cost, NoEstimatedCost: err != nil || costErr != nil || m.InputTokens == nil || m.OutputTokens == nil})
	}
}

func tokenValue(n *int64) int64 {
	if n != nil {
		return *n
	}
	return 0
}

func appendClineTools(t *domain.Transcript, tools map[string]int, msg clineMessage, parts []clinePart) (hasResult bool) {
	for _, part := range parts {
		switch {
		case msg.Role == "assistant" && part.Type == "tool_use" && part.ID != "" && part.Name != "":
			if _, duplicate := tools[part.ID]; duplicate {
				continue
			}
			server, name := domain.SplitMCPToolName(part.Name)
			args := len(part.Input)
			if string(part.Input) == "null" {
				args = 0
			}
			tools[part.ID] = len(t.Tools)
			t.Tools = append(t.Tools, domain.ToolCall{Key: part.ID, At: epochMillis(msg.TS), Name: name, Server: server, ArgsChars: args})
		case msg.Role == "user" && part.Type == "tool_result":
			hasResult = true
			if i, ok := tools[part.ToolUseID]; ok {
				tool := &t.Tools[i]
				text, parts, ok := clineContent(part.Content)
				if ok {
					tool.ResultChars = len(text)
					if len(parts) > 0 {
						tool.ResultChars = len(part.Content)
					}
				}
				tool.IsError = part.IsError
			}
		}
	}
	return hasResult
}

type clineUIMessage struct {
	TS              int64
	Type, Say, Text string
	Partial         bool
}

type clineUIUsage struct {
	TokensIn, TokensOut, CacheReads, CacheWrites *int64
	Cost                                         *float64
	CancelReason, StreamingFailedMessage         string
}

func parseClineTask(path string, r io.Reader) (domain.Transcript, error) {
	var messages []clineUIMessage
	if err := decodeConversation(r, &messages); err != nil || len(messages) == 0 {
		return domain.Transcript{}, err
	}
	id, err := domain.NewSessionID(kernel.Cline, filepath.Base(filepath.Dir(path)))
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.Cline, AgentType: domain.MainAgent}}
	seen := map[int64]bool{}
	for i, msg := range messages {
		at := epochMillis(msg.TS)
		t.Session.Span(at)
		if msg.Type != "say" || msg.Partial {
			continue
		}
		switch msg.Say {
		case "task", "user_feedback":
			if p, ok := domain.NewPrompt("ui:"+strconv.Itoa(i), at, msg.Text); ok {
				t.Prompts = append(t.Prompts, p)
			}
		case "api_req_started":
			var usage clineUIUsage
			if json.Unmarshal([]byte(msg.Text), &usage) != nil {
				t.SkippedLines++
				continue
			}
			if seen[msg.TS] || !appendClineTaskRequest(&t, msg, usage) {
				continue
			}
			seen[msg.TS] = true
		}
	}
	var history []clineMessage
	if err := readConversation(clineTaskHistory(path), &history); err != nil && !os.IsNotExist(err) {
		return domain.Transcript{}, fmt.Errorf("Cline API conversation: %w", err)
	}
	tools := map[string]int{}
	for _, msg := range history {
		_, parts, ok := clineContent(msg.Content)
		if !ok {
			t.SkippedLines++
			continue
		}
		appendClineTools(&t, tools, msg, parts)
	}
	t.TitleFromFirstPrompt()
	return t, nil
}

func appendClineTaskRequest(t *domain.Transcript, msg clineUIMessage, usage clineUIUsage) bool {
	if usage.TokensIn == nil && usage.TokensOut == nil && usage.CacheReads == nil && usage.CacheWrites == nil && usage.Cost == nil &&
		usage.CancelReason != "streaming_failed" && usage.CancelReason != "retries_exhausted" {
		return false
	}
	tokens, err := kernel.NewTokens(kernel.TokenCounts{Input: tokenValue(usage.TokensIn), Output: tokenValue(usage.TokensOut),
		CacheRead: tokenValue(usage.CacheReads), CacheWrite: tokenValue(usage.CacheWrites)})
	var cost kernel.Cost
	var costErr error
	if usage.Cost != nil {
		cost, costErr = kernel.NewCost(*usage.Cost)
	}
	if err != nil || costErr != nil {
		t.SkippedLines++
	}
	req := domain.Request{Key: "ui:" + strconv.FormatInt(msg.TS, 10), At: epochMillis(msg.TS), Tokens: tokens, LoggedCost: cost, NoEstimatedCost: true}
	if usage.CancelReason == "streaming_failed" || usage.CancelReason == "retries_exhausted" {
		req.Failure = usage.StreamingFailedMessage
		if req.Failure == "" {
			req.Failure = usage.CancelReason
		}
	}
	t.Requests = append(t.Requests, req)
	return true
}
