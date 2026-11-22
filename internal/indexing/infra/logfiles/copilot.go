package logfiles

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

var copilotSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type copilotEvent struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agentId"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Ephemeral bool            `json:"ephemeral"`
	Data      json.RawMessage `json:"data"`
}

type copilotAnswer struct {
	ParentToolCallID string `json:"parentToolCallId"`
	RequestID        string `json:"requestId"`
	ServiceID        string `json:"serviceRequestId"`
	Model            string `json:"model"`
	Output           *int64 `json:"outputTokens"`
	at               time.Time
	chosenModel      string
}

type copilotTool struct {
	ID              string `json:"toolCallId"`
	Name            string `json:"toolName"`
	MCPServer       string `json:"mcpServerName"`
	MCPConfigServer string `json:"mcpConfigServerName"`
	MCPTool         string `json:"mcpToolName"`
	Arguments       *size  `json:"arguments"`
	Success         *bool  `json:"success"`
	Error           *size  `json:"error"`
	Result          struct {
		Content         *size `json:"content"`
		DetailedContent *size `json:"detailedContent"`
		Contents        *size `json:"contents"`
	} `json:"result"`
}

// ParseCopilot reads one persisted GitHub Copilot CLI session from its events.jsonl.
func ParseCopilot(path string, r io.Reader) (domain.Transcript, error) {
	p := domain.Transcript{Session: domain.Session{Harness: kernel.Copilot, AgentType: domain.MainAgent}}
	var sessionID, selectedModel string
	var recognized bool
	var answers []copilotAnswer
	toolIndex := map[string]int{}
	lineNumber := 0

	oversized, err := eachLine(r, func(line []byte) {
		lineNumber++
		var ev copilotEvent
		if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
			p.SkippedLines++
			return
		}
		if ev.Ephemeral {
			return
		}
		at := parseTS(ev.Timestamp)
		p.Session.Span(at)
		read := func(payload any) bool {
			if len(ev.Data) != 0 && json.Unmarshal(ev.Data, payload) != nil {
				p.SkippedLines++
				return false
			}
			return true
		}
		switch ev.Type {
		case "session.start", "session.resume":
			var start struct {
				ID            string `json:"sessionId"`
				StartTime     string `json:"startTime"`
				ResumeTime    string `json:"resumeTime"`
				SelectedModel string `json:"selectedModel"`
				Context       struct {
					CWD string `json:"cwd"`
				} `json:"context"`
			}
			if !read(&start) {
				return
			}
			recognized = true
			if sessionID == "" && copilotSessionID.MatchString(start.ID) {
				sessionID = start.ID
			}
			if cwd := start.Context.CWD; p.Session.Folder == "" && filepath.IsAbs(cwd) && strings.IndexFunc(cwd, unicode.IsControl) < 0 {
				p.Session.Folder = cwd
			}
			selectedModel = cmp.Or(start.SelectedModel, selectedModel)
			p.Session.Span(parseTS(cmp.Or(start.StartTime, start.ResumeTime)))
		case "session.model_change":
			var change struct {
				NewModel string `json:"newModel"`
			}
			if read(&change) && change.NewModel != "" {
				selectedModel = change.NewModel
			}
		case "user.message":
			var user struct {
				Content               string `json:"content"`
				AgentAuthored         bool   `json:"agentAuthored"`
				AutopilotContinuation bool   `json:"isAutopilotContinuation"`
			}
			if !read(&user) || ev.AgentID != "" || user.AgentAuthored || user.AutopilotContinuation {
				return
			}
			key := ev.ID
			if key == "" {
				key = "line:" + strconv.Itoa(lineNumber)
			}
			if prompt, ok := domain.NewPrompt(key, at, user.Content); ok {
				recognized = true
				p.Prompts = append(p.Prompts, prompt)
			}
		case "assistant.message":
			var answer copilotAnswer
			if !read(&answer) || (answer.ServiceID == "" && answer.RequestID == "") {
				return
			}
			if answer.Output != nil && (*answer.Output < 0 || *answer.Output > kernel.MaxTokenCount) {
				p.SkippedLines++
				return
			}
			recognized = true
			answer.at = at
			if ev.AgentID == "" && answer.ParentToolCallID == "" {
				answer.chosenModel = selectedModel
			}
			answers = append(answers, answer)
		case "tool.execution_start", "tool.execution_complete":
			var event copilotTool
			if !read(&event) || event.ID == "" {
				return
			}
			i, exists := toolIndex[event.ID]
			if ev.Type == "tool.execution_complete" {
				if exists {
					event.complete(&p.Tools[i], at)
				}
				return
			}
			if !exists && event.Name != "" {
				recognized = true
				toolIndex[event.ID] = len(p.Tools)
				p.Tools = append(p.Tools, event.start(at))
			}
		}
	})
	if err != nil {
		return domain.Transcript{}, err
	}
	if sessionID == "" && filepath.Base(path) == "events.jsonl" {
		sessionID = filepath.Base(filepath.Dir(path))
	}
	if !recognized || !copilotSessionID.MatchString(sessionID) {
		return domain.Transcript{}, errors.New("unreadable Copilot CLI session: no usable journal or session id")
	}
	id, err := domain.NewSessionID(kernel.Copilot, sessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	p.Session.ID = id
	p.Requests = copilotRequests(answers)
	p.SkippedLines += oversized
	p.TitleFromFirstPrompt()
	return p, nil
}

func copilotRequests(answers []copilotAnswer) []domain.Request {
	// GitHub's requestId can repeat across calls. Alias a phase without a service id only
	// when its requestId points to exactly one service id.
	aliases := map[string]string{}
	for _, a := range answers {
		if a.RequestID == "" || a.ServiceID == "" {
			continue
		}
		if known, ok := aliases[a.RequestID]; ok && known != a.ServiceID {
			aliases[a.RequestID] = ""
		} else {
			aliases[a.RequestID] = a.ServiceID
		}
	}
	var requests []domain.Request
	index := map[string]int{}
	for _, a := range answers {
		service := a.ServiceID
		if service == "" {
			if alias, ok := aliases[a.RequestID]; ok {
				if alias == "" {
					continue
				}
				service = alias
			}
		}
		key := "request:" + a.RequestID
		if service != "" {
			key = "service:" + service
		}
		i, ok := index[key]
		if !ok {
			i = len(requests)
			index[key] = i
			requests = append(requests, domain.Request{Key: key, Model: a.chosenModel, PartialUsage: true})
		}
		req := &requests[i]
		if req.At.IsZero() {
			req.At = a.at
		}
		if a.Model != "" {
			req.Model = a.Model
		}
		if a.Output != nil {
			// Output is a count for the response and can be repeated across phases.
			req.Tokens, _ = kernel.NewTokens(kernel.TokenCounts{Output: *a.Output})
		}
	}
	return requests
}

func (e copilotTool) start(at time.Time) domain.ToolCall {
	server, name := domain.SplitMCPToolName(e.Name)
	if explicitServer := cmp.Or(e.MCPConfigServer, e.MCPServer); explicitServer != "" {
		server = explicitServer
		if e.MCPTool != "" {
			name = e.MCPTool
		}
	}
	call := domain.ToolCall{Key: e.ID, At: at, Name: name, Server: server}
	if e.Arguments != nil {
		call.ArgsChars = int(*e.Arguments)
	}
	return call
}

func (e copilotTool) complete(call *domain.ToolCall, at time.Time) {
	call.Duration = between(call.At, at)
	call.IsError = e.Error != nil || (e.Success != nil && !*e.Success)
	for _, result := range []*size{e.Result.DetailedContent, e.Result.Content, e.Result.Contents, e.Error} {
		if result != nil {
			call.ResultChars = int(*result)
			break
		}
	}
}
