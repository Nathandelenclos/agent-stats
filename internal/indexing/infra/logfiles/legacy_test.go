package logfiles

// The parsers of commit 80bf4b1, which decoded every line with encoding/json alone, kept verbatim (types and
// functions renamed with a legacy prefix) as the oracle of the equivalence tests: whatever a line meant then,
// it must still mean now. Do not edit them to follow the parsers.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

type legacyClaudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type legacyClaudeLine struct {
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

// legacyParseClaude reads a Claude Code transcript (~/.claude/projects/<folder>/<session>.jsonl,
// subagents in <session>/subagents/agent-<id>.jsonl).
// Claude Code writes one line per content block, repeating the message's usage on each: requests are keyed by message id.
func legacyParseClaude(path string, r io.Reader) (domain.Transcript, error) {
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
		p.Session.AgentType = legacyClaudeAgentType(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	}
	invalid := map[string]bool{} // message ids whose token counts are impossible

	requestStart := map[string]time.Time{} // api-request id -> ts
	reqIndex := map[string]int{}           // message id -> index in p.Requests
	toolIndex := map[string]int{}
	firstBlock := map[string]time.Time{}
	var lastModel string

	oversized, err := legacyEachLine(r, func(line []byte) {
		var l legacyClaudeLine
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
			blocks := legacyClaudeBlocks(m.Content)
			if m.Model == "<synthetic>" {
				if l.IsAPIErrorMessage {
					p.Requests = append(p.Requests, domain.Request{Key: "err:" + m.ID, At: ts, Model: lastModel, Provider: "anthropic", Failure: legacyFirstText(blocks)})
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
				legacyAddClaudePrompt(p, l.UUID, ts, text)
				return
			}
			for j, b := range legacyClaudeBlocks(l.Message.Content) {
				switch b.Type {
				case "tool_result":
					if i, ok := toolIndex[b.ToolUseID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError = len(b.Content), b.IsError
						t.Duration = between(t.At, ts)
					}
				case "text":
					legacyAddClaudePrompt(p, fmt.Sprintf("%s:%d", l.UUID, j), ts, b.Text)
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

func legacyAddClaudePrompt(p *domain.Transcript, key string, ts time.Time, text string) {
	// Skip harness-injected messages: command wrappers, reminders, interruptions.
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") {
		return
	}
	if pr, ok := domain.NewPrompt(key, ts, text); ok {
		p.Prompts = append(p.Prompts, pr)
	}
}

func legacyClaudeBlocks(raw json.RawMessage) []legacyClaudeBlock {
	var blocks []legacyClaudeBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

func legacyFirstText(blocks []legacyClaudeBlock) string {
	for _, b := range blocks {
		if b.Type == "text" {
			return b.Text
		}
	}
	return domain.UnspecifiedFailure
}

// legacyMaxMetaBytes bounds the subagent .meta.json read next to a transcript (real ones are a few hundred bytes).
const legacyMaxMetaBytes = 64 << 10

// legacyClaudeAgentType reads the subagent type Claude Code stores next to the transcript, "subagent" when absent or unusable.
func legacyClaudeAgentType(metaPath string) domain.AgentType {
	f, err := openRegular(metaPath)
	if err != nil {
		return domain.Subagent
	}
	defer f.Close()
	var meta struct {
		AgentType string `json:"agentType"`
	}
	if json.NewDecoder(io.LimitReader(f, legacyMaxMetaBytes)).Decode(&meta) != nil {
		return domain.Subagent
	}
	if t, ok := domain.ParseAgentType(meta.AgentType); ok {
		return t
	}
	return domain.Subagent
}

type legacyCodexUsage struct {
	Input     int64 `json:"input_tokens"` // includes cached tokens
	Cached    int64 `json:"cached_input_tokens"`
	CacheW    int64 `json:"cache_write_input_tokens"`
	Output    int64 `json:"output_tokens"`
	Reasoning int64 `json:"reasoning_output_tokens"`
}

type legacyCodexDuration struct {
	Secs  float64 `json:"secs"`
	Nanos float64 `json:"nanos"`
}

func (d *legacyCodexDuration) duration() domain.Duration {
	if d == nil {
		return domain.Duration{}
	}
	return millis(d.Secs*1000 + d.Nanos/1e6)
}

type legacyCodexPayload struct {
	Type         string            `json:"type"`
	ID           string            `json:"id"`
	Cwd          string            `json:"cwd"`
	ThreadSource string            `json:"thread_source"`
	ForkedFrom   string            `json:"forked_from_id"`
	Model        string            `json:"model"`
	Name         string            `json:"name"`
	Arguments    json.RawMessage   `json:"arguments"`
	Input        json.RawMessage   `json:"input"`
	CallID       string            `json:"call_id"`
	Output       json.RawMessage   `json:"output"`
	ResponseID   string            `json:"response_id"`
	Usage        *legacyCodexUsage `json:"usage"`
	Settings     *struct {
		Model string `json:"model"`
	} `json:"thread_settings"`
	Info *struct {
		Total *legacyCodexUsage `json:"total_token_usage"`
		Last  *legacyCodexUsage `json:"last_token_usage"`
	} `json:"info"`
	Item *struct {
		Type      string               `json:"type"`
		ID        string               `json:"id"`
		Server    string               `json:"server"`
		Tool      string               `json:"tool"`
		Kind      string               `json:"kind"`
		Arguments json.RawMessage      `json:"arguments"`
		Result    json.RawMessage      `json:"result"`
		Error     json.RawMessage      `json:"error"`
		Status    string               `json:"status"`
		Command   json.RawMessage      `json:"command"`
		Output    string               `json:"aggregated_output"`
		ExitCode  *int                 `json:"exit_code"`
		Duration  *legacyCodexDuration `json:"duration"`
		Changes   json.RawMessage      `json:"changes"`
		Query     string               `json:"query"`
		Path      string               `json:"path"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
}

// legacyParseCodex reads a Codex rollout (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl[.zst]).
// Real tool calls are the item_completed events: the model's "exec" custom tool is only a JS wrapper around them.
func legacyParseCodex(path string, r io.Reader) (domain.Transcript, error) {
	p := &domain.Transcript{Session: domain.Session{Harness: kernel.Codex, AgentType: domain.MainAgent}}
	var model string
	var fallback []domain.Request // from token_count events, used only when the file has no token_usage_record
	var lastTotal legacyCodexUsage
	callIndex := map[string]int{}
	ordinal := 0

	oversized, err := legacyEachLine(r, func(line []byte) {
		ordinal++
		var l struct {
			Type      string             `json:"type"`
			Timestamp string             `json:"timestamp"`
			Payload   legacyCodexPayload `json:"payload"`
		}
		if json.Unmarshal(line, &l) != nil {
			p.SkippedLines++
			return
		}
		ts := parseTS(l.Timestamp)
		pl := &l.Payload
		switch l.Type {
		case "session_meta":
			// A forked rollout replays its origin's session_meta after its own: the first one is this file's.
			if !p.Session.ID.IsZero() {
				return
			}
			if id, err := domain.NewSessionID(kernel.Codex, pl.ID); err == nil {
				p.Session.ID = id
			} else if id, err := legacyCodexFileID(path); err == nil {
				p.Session.ID = id
			}
			if parent, err := domain.NewSessionID(kernel.Codex, pl.ForkedFrom); err == nil {
				p.Session.ParentID = parent
			}
			p.Session.Folder = pl.Cwd
			if pl.ThreadSource == "subagent" {
				p.Session.AgentType = domain.Subagent
			}
			p.Session.Span(ts)
		case "turn_context":
			if pl.Model != "" {
				model = pl.Model
			}
		case "token_usage_record":
			if pl.Usage != nil {
				if req, err := legacyCodexRequest(pl.ResponseID, ts, model, *pl.Usage); err == nil {
					p.Requests = append(p.Requests, req)
				} else {
					p.SkippedLines++
				}
			}
			p.Session.Span(ts)
		case "response_item":
			p.Session.Span(ts)
			switch pl.Type {
			case "function_call":
				// exec_command/write_stdin also surface as CommandExecution items; count those instead.
				if pl.Name == "exec_command" || pl.Name == "write_stdin" {
					return
				}
				callIndex[pl.CallID] = len(p.Tools)
				p.Tools = append(p.Tools, domain.ToolCall{Key: pl.CallID, At: ts, Name: pl.Name, ArgsChars: len(pl.Arguments)})
			case "function_call_output":
				if i, ok := callIndex[pl.CallID]; ok {
					p.Tools[i].ResultChars = len(pl.Output)
					p.Tools[i].Duration = between(p.Tools[i].At, ts)
				}
			}
		case "event_msg":
			switch pl.Type {
			case "thread_settings_applied":
				if pl.Settings != nil && pl.Settings.Model != "" {
					model = pl.Settings.Model
				}
			case "token_count":
				if pl.Info == nil || pl.Info.Last == nil || pl.Info.Total == nil || *pl.Info.Total == lastTotal {
					return // repeated event for the same response
				}
				lastTotal = *pl.Info.Total
				if req, err := legacyCodexRequest("tc:"+strconv.Itoa(ordinal), ts, model, *pl.Info.Last); err == nil {
					fallback = append(fallback, req)
				} else {
					p.SkippedLines++
				}
			case "item_completed":
				if pl.Item != nil {
					legacyCodexItem(p, ts, pl)
				}
			}
		}
	})
	p.SkippedLines += oversized
	if len(p.Requests) == 0 {
		p.Requests = fallback
	}
	if p.Session.ID.IsZero() {
		id, idErr := legacyCodexFileID(path)
		if idErr != nil {
			return domain.Transcript{}, idErr
		}
		p.Session.ID = id
	}
	p.TitleFromFirstPrompt()
	return *p, err
}

// legacyCodexRequest builds a request from a usage record, whose input count includes the cached tokens.
func legacyCodexRequest(key string, ts time.Time, model string, u legacyCodexUsage) (domain.Request, error) {
	tokens, err := kernel.NewTokens(kernel.TokenCounts{Input: u.Input - u.Cached, CacheRead: u.Cached, CacheWrite: u.CacheW,
		Output: u.Output, Reasoning: u.Reasoning})
	if err != nil {
		return domain.Request{}, err
	}
	return domain.Request{Key: key, At: ts, Model: model, Provider: "openai", Tokens: tokens}, nil
}

func legacyCodexItem(p *domain.Transcript, ts time.Time, pl *legacyCodexPayload) {
	it := pl.Item
	failed := it.Status == "failed" || (len(it.Error) > 0 && string(it.Error) != "null")
	t := domain.ToolCall{Key: it.ID, At: ts, IsError: failed, Duration: it.Duration.duration()}
	switch it.Type {
	case "UserMessage":
		var parts []string
		for _, c := range it.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		if pr, ok := domain.NewPrompt(it.ID, ts, strings.Join(parts, "\n")); ok {
			p.Prompts = append(p.Prompts, pr)
		}
		return
	case "CommandExecution":
		t.Name, t.ArgsChars, t.ResultChars = "shell", len(it.Command), len(it.Output)
		t.IsError = t.IsError || (it.ExitCode != nil && *it.ExitCode != 0)
	case "McpToolCall":
		t.Name, t.Server, t.ArgsChars, t.ResultChars = it.Tool, it.Server, len(it.Arguments), len(it.Result)
	case "FileChange":
		t.Name, t.ArgsChars = "apply_patch", len(it.Changes)
	case "ImageView":
		t.Name, t.ArgsChars = "view_image", len(it.Path)
	case "Extension":
		t.Name, t.ArgsChars = it.Kind, len(it.Query)
	default:
		return // reasoning, agent messages, subagent notifications, collab calls already logged as function_call
	}
	p.Tools = append(p.Tools, t)
}

// legacyCodexFileID names a session after its rollout file when the log carries no session id.
func legacyCodexFileID(path string) (domain.SessionID, error) {
	return domain.NewSessionID(kernel.Codex, strings.SplitN(filepath.Base(path), ".", 2)[0])
}

type legacyPiBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type legacyPiUsageLog struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoningTokens"`
	Cost       *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type legacyPiLine struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	ModelID   string `json:"modelId"`
	Message   *struct {
		Role         string            `json:"role"`
		Content      json.RawMessage   `json:"content"`
		Provider     string            `json:"provider"`
		Model        string            `json:"model"`
		StopReason   string            `json:"stopReason"`
		ErrorMessage string            `json:"errorMessage"`
		Duration     float64           `json:"duration"`
		TTFT         float64           `json:"ttft"`
		ToolCallID   string            `json:"toolCallId"`
		IsError      bool              `json:"isError"`
		Usage        *legacyPiUsageLog `json:"usage"`
	} `json:"message"`
}

// legacySessionDir matches pi/OMP session file and directory names: "<ISO time>_<uuid>".
var legacySessionDir = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d-]+Z_([0-9a-f-]{36})$`)

// legacyParsePi reads a pi or oh-my-pi session (~/.pi/agent/sessions, ~/.omp/agent/sessions).
// Subagent sessions are stored in a directory named after their parent session.
func legacyParsePi(harness kernel.Harness) Parser {
	return func(path string, r io.Reader) (domain.Transcript, error) {
		p := &domain.Transcript{Session: domain.Session{Harness: harness, AgentType: domain.MainAgent}}
		stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		native := stem
		if m := legacySessionDir.FindStringSubmatch(stem); m != nil {
			native = m[1]
		}
		id, err := domain.NewSessionID(harness, native)
		if err != nil {
			return domain.Transcript{}, err
		}
		p.Session.ID = id
		for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if m := legacySessionDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
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
		oversized, err := legacyEachLine(r, func(line []byte) {
			var l legacyPiLine
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
				var blocks []legacyPiBlock
				_ = json.Unmarshal(m.Content, &blocks)
				switch m.Role {
				case "user":
					var parts []string
					for _, b := range blocks {
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
					if req.Failure == "" && req.StopReason == legacyPiStopError {
						req.Failure = domain.UnspecifiedFailure
					}
					if err := legacyPiUsage(&req, m.Usage); err == nil {
						p.Requests = append(p.Requests, req)
					} else {
						p.SkippedLines++
					}
					for _, b := range blocks {
						if b.Type != "toolCall" {
							continue
						}
						server, name := domain.SplitMCPToolName(b.Name)
						toolIndex[b.ID] = len(p.Tools)
						p.Tools = append(p.Tools, domain.ToolCall{Key: b.ID, At: ts, Name: name, Server: server, ArgsChars: len(b.Arguments)})
					}
				case "toolResult":
					if i, ok := toolIndex[m.ToolCallID]; ok {
						t := &p.Tools[i]
						t.ResultChars, t.IsError, t.Duration = len(m.Content), m.IsError, between(t.At, ts)
					}
				}
			}
		})
		p.SkippedLines += oversized
		p.TitleFromFirstPrompt()
		return *p, err
	}
}

// legacyPiStopError is the stop reason pi and oh-my-pi write for a failed request.
const legacyPiStopError = "error"

// legacyPiUsage sets the tokens and the logged cost of req; an impossible value refuses the request.
func legacyPiUsage(req *domain.Request, u *legacyPiUsageLog) error {
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

// legacyEachLine calls fn for every non-empty line up to maxLineBytes (tool results can be megabytes) and returns
// how many longer lines it skipped. fn must not keep the slice.
func legacyEachLine(r io.Reader, fn func([]byte)) (oversized int, err error) {
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
