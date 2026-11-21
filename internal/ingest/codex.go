package ingest

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
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

func (d *codexDuration) duration() time.Duration {
	if d == nil {
		return 0
	}
	return millis(d.Secs*1000 + d.Nanos/1e6)
}

type codexPayload struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Cwd          string          `json:"cwd"`
	ThreadSource string          `json:"thread_source"`
	ForkedFrom   string          `json:"forked_from_id"`
	Model        string          `json:"model"`
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
	Input        json.RawMessage `json:"input"`
	CallID       string          `json:"call_id"`
	Output       json.RawMessage `json:"output"`
	ResponseID   string          `json:"response_id"`
	Usage        *codexUsage     `json:"usage"`
	Settings     *struct {
		Model string `json:"model"`
	} `json:"thread_settings"`
	Info *struct {
		Total *codexUsage `json:"total_token_usage"`
		Last  *codexUsage `json:"last_token_usage"`
	} `json:"info"`
	Item *struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Kind      string          `json:"kind"`
		Arguments json.RawMessage `json:"arguments"`
		Result    json.RawMessage `json:"result"`
		Error     json.RawMessage `json:"error"`
		Status    string          `json:"status"`
		Command   json.RawMessage `json:"command"`
		Output    string          `json:"aggregated_output"`
		ExitCode  *int            `json:"exit_code"`
		Duration  *codexDuration  `json:"duration"`
		Changes   json.RawMessage `json:"changes"`
		Query     string          `json:"query"`
		Path      string          `json:"path"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
}

// ParseCodex reads a Codex rollout (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl[.zst]).
// Real tool calls are the item_completed events: the model's "exec" custom tool is only a JS wrapper around them.
func ParseCodex(path string, r io.Reader) (usage.Transcript, error) {
	p := &usage.Transcript{Session: usage.Session{Harness: usage.Codex, AgentType: usage.MainAgent}}
	var model string
	var fallback []usage.Request // from token_count events, used only when the file has no token_usage_record
	var lastTotal codexUsage
	callIndex := map[string]int{}
	ordinal := 0

	err := eachLine(r, func(line []byte) {
		ordinal++
		var l struct {
			Type      string       `json:"type"`
			Timestamp string       `json:"timestamp"`
			Payload   codexPayload `json:"payload"`
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
			if p.Session.ID != "" {
				return
			}
			p.Session.ID = usage.Codex.SessionID(pl.ID)
			if pl.ForkedFrom != "" {
				p.Session.ParentID = usage.Codex.SessionID(pl.ForkedFrom)
			}
			p.Session.Folder = pl.Cwd
			if pl.ThreadSource == "subagent" {
				p.Session.AgentType = usage.Subagent
			}
			p.Session.Span(ts)
		case "turn_context":
			if pl.Model != "" {
				model = pl.Model
			}
		case "token_usage_record":
			if pl.Usage != nil {
				p.Requests = append(p.Requests, codexRequest(pl.ResponseID, ts, model, *pl.Usage))
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
				p.Tools = append(p.Tools, usage.ToolCall{Key: pl.CallID, At: ts, Name: pl.Name, ArgsChars: len(pl.Arguments)})
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
				fallback = append(fallback, codexRequest("tc:"+strconv.Itoa(ordinal), ts, model, *pl.Info.Last))
			case "item_completed":
				if pl.Item != nil {
					codexItem(p, ts, pl)
				}
			}
		}
	})
	if len(p.Requests) == 0 {
		p.Requests = fallback
	}
	if p.Session.ID == "" {
		p.Session.ID = usage.Codex.SessionID(strings.SplitN(filepath.Base(path), ".", 2)[0])
	}
	p.TitleFromFirstPrompt()
	return *p, err
}

func codexRequest(key string, ts time.Time, model string, u codexUsage) usage.Request {
	return usage.Request{
		Key: key, At: ts, Model: model, Provider: "openai",
		Tokens:    usage.Tokens{Input: u.Input - u.Cached, CacheRead: u.Cached, CacheWrite: u.CacheW, Output: u.Output},
		Reasoning: u.Reasoning,
	}
}

func codexItem(p *usage.Transcript, ts time.Time, pl *codexPayload) {
	it := pl.Item
	failed := it.Status == "failed" || (len(it.Error) > 0 && string(it.Error) != "null")
	t := usage.ToolCall{Key: it.ID, At: ts, IsError: failed, Duration: it.Duration.duration()}
	switch it.Type {
	case "UserMessage":
		var parts []string
		for _, c := range it.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		if pr, ok := usage.NewPrompt(it.ID, ts, strings.Join(parts, "\n")); ok {
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
