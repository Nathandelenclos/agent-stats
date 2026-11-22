package logfiles

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

type codexUsage struct {
	Input     int64 `json:"input_tokens"` // includes cached tokens
	Cached    int64 `json:"cached_input_tokens"`
	CacheW    int64 `json:"cache_write_input_tokens"`
	Output    int64 `json:"output_tokens"`
	Reasoning int64 `json:"reasoning_output_tokens"`
}

type codexDuration struct {
	Secs  float64 `json:"secs"`
	Nanos float64 `json:"nanos"`
}

func (d *codexDuration) duration() domain.Duration {
	if d == nil {
		return domain.Duration{}
	}
	return millis(d.Secs*1000 + d.Nanos/1e6)
}

// textLen is a JSON string of which only the length of the decoded text is kept.
type textLen int

// UnmarshalJSON leaves the length as it is for a null, as encoding/json does for a string field.
func (n *textLen) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*n = textLen(len(s))
	return nil
}

type codexInfo struct {
	Total *codexUsage `json:"total_token_usage"`
	Last  *codexUsage `json:"last_token_usage"`
}

type codexSettings struct {
	Model string `json:"model"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexItemLog struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Kind      string          `json:"kind"`
	Arguments size            `json:"arguments"`
	Result    size            `json:"result"`
	Error     json.RawMessage `json:"error"`
	Status    string          `json:"status"`
	Command   size            `json:"command"`
	Output    textLen         `json:"aggregated_output"`
	ExitCode  *int            `json:"exit_code"`
	Duration  *codexDuration  `json:"duration"`
	Changes   size            `json:"changes"`
	Query     string          `json:"query"`
	Path      string          `json:"path"`
	Content   []codexContent  `json:"content"`
}

type codexPayload struct {
	Type         string         `json:"type"`
	ID           string         `json:"id"`
	Cwd          string         `json:"cwd"`
	ThreadSource string         `json:"thread_source"`
	ForkedFrom   string         `json:"forked_from_id"`
	Model        string         `json:"model"`
	Name         string         `json:"name"`
	Arguments    size           `json:"arguments"`
	CallID       string         `json:"call_id"`
	Output       size           `json:"output"`
	ResponseID   string         `json:"response_id"`
	Usage        *codexUsage    `json:"usage"`
	Settings     *codexSettings `json:"thread_settings"`
	Info         *codexInfo     `json:"info"`
	Item         *codexItemLog  `json:"item"`
}

type codexLine struct {
	Type      string       `json:"type"`
	Timestamp string       `json:"timestamp"`
	Payload   codexPayload `json:"payload"`
}

// ParseCodex reads a Codex rollout (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl[.zst]).
// Real tool calls are the item_completed events: the model's "exec" custom tool is only a JS wrapper around them.
func ParseCodex(path string, r io.Reader) (domain.Transcript, error) {
	p := &domain.Transcript{Session: domain.Session{Harness: kernel.Codex, AgentType: domain.MainAgent}}
	var model string
	var fallback []domain.Request // from token_count events, used only when the file has no token_usage_record
	var lastTotal codexUsage
	callIndex := map[string]int{}
	ordinal := 0

	oversized, err := eachLine(r, func(line []byte) {
		ordinal++
		l, ok := decodeLine(line, scanCodexLine)
		if !ok {
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
			} else if id, err := codexFileID(path); err == nil {
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
				if req, err := codexRequest(pl.ResponseID, ts, model, *pl.Usage); err == nil {
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
				server, name := domain.SplitMCPToolName(pl.Name)
				callIndex[pl.CallID] = len(p.Tools)
				p.Tools = append(p.Tools, domain.ToolCall{Key: pl.CallID, At: ts, Name: name, Server: server, ArgsChars: int(pl.Arguments)})
			case "function_call_output":
				if i, ok := callIndex[pl.CallID]; ok {
					p.Tools[i].ResultChars = int(pl.Output)
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
				p.Session.Span(ts)
				if req, err := codexRequest("tc:"+strconv.Itoa(ordinal), ts, model, *pl.Info.Last); err == nil {
					fallback = append(fallback, req)
				} else {
					p.SkippedLines++
				}
			case "item_completed":
				if pl.Item != nil {
					p.Session.Span(ts) // tool calls and prompts are logged here, after the response items they follow
					codexItem(p, ts, pl)
				}
			}
		}
	})
	p.SkippedLines += oversized
	if len(p.Requests) == 0 {
		p.Requests = fallback
	}
	if p.Session.ID.IsZero() {
		id, idErr := codexFileID(path)
		if idErr != nil {
			return domain.Transcript{}, idErr
		}
		p.Session.ID = id
	}
	p.TitleFromFirstPrompt()
	return *p, err
}

// codexRequest builds a request from a usage record, whose input count includes the cached tokens.
func codexRequest(key string, ts time.Time, model string, u codexUsage) (domain.Request, error) {
	tokens, err := kernel.NewTokens(kernel.TokenCounts{Input: u.Input - u.Cached, CacheRead: u.Cached, CacheWrite: u.CacheW,
		Output: u.Output, Reasoning: u.Reasoning})
	if err != nil {
		return domain.Request{}, err
	}
	return domain.Request{Key: key, At: ts, Model: model, Provider: "openai", Tokens: tokens}, nil
}

func codexItem(p *domain.Transcript, ts time.Time, pl *codexPayload) {
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
		t.Name, t.ArgsChars, t.ResultChars = "shell", int(it.Command), int(it.Output)
		t.IsError = t.IsError || (it.ExitCode != nil && *it.ExitCode != 0)
	case "McpToolCall":
		t.Name, t.Server, t.ArgsChars, t.ResultChars = it.Tool, it.Server, int(it.Arguments), int(it.Result)
	case "FileChange":
		t.Name, t.ArgsChars = "apply_patch", int(it.Changes)
	case "ImageView":
		t.Name, t.ArgsChars = "view_image", len(it.Path)
	case "Extension":
		t.Name, t.ArgsChars = it.Kind, len(it.Query)
	default:
		return // reasoning, agent messages, subagent notifications, collab calls already logged as function_call
	}
	p.Tools = append(p.Tools, t)
}

// codexFileID names a session after its rollout file when the log carries no session id.
func codexFileID(path string) (domain.SessionID, error) {
	return domain.NewSessionID(kernel.Codex, strings.SplitN(filepath.Base(path), ".", 2)[0])
}
