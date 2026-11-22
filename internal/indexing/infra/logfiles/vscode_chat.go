package logfiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func codeUserDir(home string) string {
	var data string
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support")
	case "windows":
		data = envOr("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	default:
		data = envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	return filepath.Join(data, "Code", "User")
}

func vscodeChatSources(user string) []Source {
	workspaces := filepath.Join(user, "workspaceStorage")
	emptyWindows := filepath.Join(user, "globalStorage", "emptyWindowChatSessions")
	return []Source{
		{Harness: kernel.VSCodeChat, Root: workspaces, Match: func(path string) bool {
			return filepath.Dir(filepath.Dir(filepath.Dir(path))) == workspaces &&
				filepath.Base(filepath.Dir(path)) == "chatSessions" && isVSCodeChat(path)
		}, Parse: parseVSCodeChat, Beside: vscodeChatRevision},
		{Harness: kernel.VSCodeChat, Root: emptyWindows, Match: func(path string) bool {
			return filepath.Dir(path) == emptyWindows && isVSCodeChat(path)
		}, Parse: parseVSCodeChat, Beside: vscodeChatRevision},
		{Harness: kernel.VSCodeChat, Root: workspaces, Match: func(path string) bool {
			workspace, folder := vscodeDebugWorkspace(path)
			if filepath.Dir(workspace) != workspaces || filepath.Ext(path) != ".jsonl" {
				return false
			}
			if filepath.Base(path) != "main.jsonl" {
				return strings.HasPrefix(filepath.Base(path), "runSubagent-")
			}
			return !hasVSCodeChat(user, workspace, folder)
		}, Parse: parseStandaloneVSCodeDebug, Beside: vscodeDebugRevision},
	}
}

func regularLog(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func isVSCodeChat(path string) bool {
	switch filepath.Ext(path) {
	case ".jsonl":
		return true
	case ".json":
		return !regularLog(path + "l")
	default:
		return false
	}
}

func vscodeDebugWorkspace(path string) (workspace, folder string) {
	dir := filepath.Dir(path)
	logs := filepath.Dir(dir)
	extension := filepath.Dir(logs)
	if filepath.Base(logs) != "debug-logs" || !strings.EqualFold(filepath.Base(extension), "GitHub.copilot-chat") {
		return "", ""
	}
	return filepath.Dir(extension), filepath.Base(dir)
}

func hasVSCodeChat(user, workspace, id string) bool {
	hasChat := func(folder string) bool {
		base := filepath.Join(folder, id)
		return regularLog(base+".jsonl") || regularLog(base+".json")
	}
	if hasChat(filepath.Join(workspace, "chatSessions")) ||
		hasChat(filepath.Join(user, "globalStorage", "emptyWindowChatSessions")) {
		return true
	}
	root := filepath.Join(user, "workspaceStorage")
	dirs, _ := os.ReadDir(root)
	for _, dir := range dirs {
		if dir.IsDir() && hasChat(filepath.Join(root, dir.Name(), "chatSessions")) {
			return true
		}
	}
	return false
}

func vscodeChatDebugPath(path string) string {
	storage := filepath.Dir(filepath.Dir(path))
	user := filepath.Dir(storage)
	if filepath.Base(filepath.Dir(path)) == "chatSessions" {
		user = filepath.Dir(user)
	}
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	debugIn := func(workspace string) string {
		return filepath.Join(workspace, "GitHub.copilot-chat", "debug-logs", id, "main.jsonl")
	}
	if filepath.Base(filepath.Dir(path)) == "chatSessions" {
		if debug := debugIn(storage); regularLog(debug) {
			return debug
		}
	}
	root := filepath.Join(user, "workspaceStorage")
	dirs, _ := os.ReadDir(root)
	for _, dir := range dirs {
		if debug := debugIn(filepath.Join(root, dir.Name())); dir.IsDir() && regularLog(debug) {
			return debug
		}
	}
	return ""
}

func companionRevision(paths ...string) string {
	var revision string
	for _, path := range paths {
		revision += ":"
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			revision += fileRevision(info)
		}
	}
	return revision
}

func vscodeChatRevision(path string) string {
	debug := vscodeChatDebugPath(path)
	return debug + companionRevision(debug, filepath.Join(filepath.Dir(filepath.Dir(path)), "workspace.json"), vscodeDebugFolderPath(debug))
}

func vscodeDebugFolderPath(path string) string {
	if workspace, _ := vscodeDebugWorkspace(path); workspace != "" {
		return filepath.Join(workspace, "workspace.json")
	}
	return ""
}

func vscodeDebugRevision(path string) string {
	workspace, _ := vscodeDebugWorkspace(path)
	if filepath.Base(path) == "main.jsonl" {
		return companionRevision(filepath.Join(workspace, "workspace.json"))
	}
	return companionRevision(filepath.Join(workspace, "workspace.json"), filepath.Join(filepath.Dir(path), "main.jsonl"))
}

// Replay and document decoding hold state in memory, unlike the line-oriented request readers.
const maxConversationBytes int64 = 256 << 20

func decodeConversation(r io.Reader, dst any) error {
	limited := &io.LimitedReader{R: r, N: maxConversationBytes + 1}
	dec := json.NewDecoder(limited)
	err := dec.Decode(dst)
	if err == nil {
		if extra := dec.Decode(new(json.RawMessage)); extra != io.EOF {
			err = errors.New("expected one JSON value")
		}
	}
	if limited.N == 0 {
		return ErrLogTooLarge
	}
	if err != nil {
		return errors.New("unreadable JSON document")
	}
	return nil
}

func readConversation(path string, dst any) error {
	f, err := openCompanion(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return decodeConversation(f, dst)
}

func openCompanion(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, ErrNotRegular
	}
	return f, nil
}

type vscodeChat struct {
	SessionID        string
	CreationDate     int64
	CustomTitle      string
	WorkingDirectory string
	Requests         []struct {
		RequestID         string
		Timestamp         int64
		ResponseTimestamp int64
		IsSystemInitiated bool
		AgentHostMetadata json.RawMessage
		Agent             struct {
			ID          string
			ExtensionID struct{ Value string }
		}
		Message  json.RawMessage
		Response []json.RawMessage
	}
}

type vscodeToolPart struct {
	Kind, ToolID, ToolCallID string
	IsComplete               bool
	Source                   struct{ Type, ID string }
	ToolSpecificData         struct {
		Kind, CommandLine     string
		TerminalCommandOutput struct{ Text string }
	}
}

func parseVSCodeChat(path string, r io.Reader) (domain.Transcript, error) {
	var chat vscodeChat
	var skipped int
	if filepath.Ext(path) == ".jsonl" {
		var err error
		skipped, err = replayVSCodeChat(r, &chat)
		if err != nil {
			return domain.Transcript{}, err
		}
	} else if err := decodeConversation(r, &chat); err != nil {
		return domain.Transcript{}, err
	}
	id, err := domain.NewSessionID(kernel.VSCodeChat, chat.SessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.VSCodeChat, AgentType: domain.MainAgent}, SkippedLines: skipped}
	t.Session.Span(epochMillis(chat.CreationDate))
	t.Session.Title = domain.Title(chat.CustomTitle)
	t.Session.Folder = localFolder(chat.WorkingDirectory)
	workspace := filepath.Dir(filepath.Dir(path))
	hasCopilot := false
	seenPrompts, seenTools := map[string]bool{}, map[string]bool{}
	for _, request := range chat.Requests {
		extension := request.Agent.ExtensionID
		if len(request.AgentHostMetadata) > 0 && string(request.AgentHostMetadata) != "null" {
			continue
		}
		if !(strings.EqualFold(extension.Value, "github.copilot-chat") ||
			strings.EqualFold(extension.Value, "github.copilot") ||
			extension.Value == "" && strings.HasPrefix(request.Agent.ID, "github.copilot.")) {
			continue
		}
		hasCopilot = true
		at := epochMillis(request.Timestamp)
		t.Session.Span(at)
		t.Session.Span(epochMillis(request.ResponseTimestamp))
		if !request.IsSystemInitiated && request.RequestID != "" && !seenPrompts[request.RequestID] {
			var message struct{ Text string }
			if json.Unmarshal(request.Message, &message) != nil {
				_ = json.Unmarshal(request.Message, &message.Text)
			}
			if p, ok := domain.NewPrompt(request.RequestID, at, message.Text); ok {
				t.Prompts = append(t.Prompts, p)
				seenPrompts[p.Key] = true
			}
		}
		for _, raw := range request.Response {
			var part vscodeToolPart
			if json.Unmarshal(raw, &part) != nil || part.Kind != "toolInvocationSerialized" || !part.IsComplete || part.ToolCallID == "" || part.ToolID == "" || seenTools[part.ToolCallID] {
				continue
			}
			server, name := domain.SplitMCPToolName(part.ToolID)
			if part.Source.Type == "mcp" && server == "" {
				server = part.Source.ID
			}
			tool := domain.ToolCall{Key: part.ToolCallID, At: at, Name: name, Server: server}
			if part.ToolSpecificData.Kind == "terminal" {
				tool.ArgsChars = len(part.ToolSpecificData.CommandLine)
				tool.ResultChars = len(part.ToolSpecificData.TerminalCommandOutput.Text)
			}
			t.Tools = append(t.Tools, tool)
			seenTools[tool.Key] = true
		}
	}
	if filepath.Base(filepath.Dir(path)) == "chatSessions" && t.Session.Folder == "" {
		t.Session.Folder = vscodeWorkspaceFolder(workspace)
	}
	if debugPath := vscodeChatDebugPath(path); debugPath != "" {
		if f, err := openCompanion(debugPath); err == nil {
			debug, readErr := readVSCodeDebug(f, chat.SessionID, false, "")
			f.Close()
			if readErr != nil {
				return domain.Transcript{}, readErr
			}
			hasCopilot = hasCopilot || len(debug.Requests)+len(debug.Tools) > 0
			t.Requests = debug.Requests
			if len(debug.Tools) > 0 {
				t.Tools = debug.Tools
			}
			t.SkippedLines += debug.SkippedLines
			t.Session.Span(debug.Session.Start)
			t.Session.Span(debug.Session.End)
			if t.Session.Folder == "" {
				workspace, _ := vscodeDebugWorkspace(debugPath)
				t.Session.Folder = vscodeWorkspaceFolder(workspace)
			}
		} else if !os.IsNotExist(err) {
			t.SkippedLines++
		}
	}
	if !hasCopilot {
		return domain.Transcript{}, nil
	}
	t.TitleFromFirstPrompt()
	return t, nil
}

func replayVSCodeChat(r io.Reader, chat *vscodeChat) (int, error) {
	var state any
	var skipped int
	limited := &io.LimitedReader{R: r, N: maxConversationBytes + 1}
	oversized, err := eachLine(limited, func(line []byte) {
		var op struct {
			Kind *int
			K    []json.RawMessage
			V    any
			I    *int
		}
		if json.Unmarshal(line, &op) != nil || op.Kind == nil {
			skipped++
			return
		}
		if *op.Kind == 0 {
			state = op.V
		} else if *op.Kind < 1 || *op.Kind > 3 || len(op.K) == 0 || len(op.K) > 64 ||
			!applyVSCodeMutation(&state, op.K, *op.Kind, op.V, op.I) {
			skipped++
		}
	})
	if limited.N == 0 {
		return 0, ErrLogTooLarge
	}
	if err != nil {
		return 0, err
	}
	if state == nil {
		return 0, errors.New("VS Code chat log has no snapshot")
	}
	raw, err := json.Marshal(state)
	if err != nil || json.Unmarshal(raw, chat) != nil {
		return 0, errors.New("unreadable VS Code chat snapshot")
	}
	return skipped + oversized, nil
}

func applyVSCodeMutation(state *any, path []json.RawMessage, kind int, value any, splice *int) bool {
	if len(path) == 0 {
		switch kind {
		case 1:
			*state = value
		case 2:
			arr, ok := (*state).([]any)
			if !ok && *state != nil {
				return false
			}
			if splice != nil {
				if *splice < 0 || *splice > len(arr) {
					return false
				}
				arr = arr[:*splice:*splice]
			}
			vals, ok := value.([]any)
			if !ok && value != nil {
				return false
			}
			*state = append(arr, vals...)
		case 3:
			*state = nil
		}
		return true
	}
	switch node := (*state).(type) {
	case map[string]any:
		var key string
		if json.Unmarshal(path[0], &key) != nil || key == "" {
			return false
		}
		child := node[key]
		if !applyVSCodeMutation(&child, path[1:], kind, value, splice) {
			return false
		}
		if kind == 3 && len(path) == 1 {
			delete(node, key)
		} else {
			node[key] = child
		}
		return true
	case []any:
		var index int
		return !bytes.Equal(path[0], []byte("null")) && json.Unmarshal(path[0], &index) == nil &&
			index >= 0 && index < len(node) && applyVSCodeMutation(&node[index], path[1:], kind, value, splice)
	default:
		return false
	}
}

func epochMillis(ms int64) time.Time {
	if ms <= 0 || ms > 253402300799999 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func localFolder(raw string) string {
	if filepath.IsAbs(raw) && !strings.ContainsRune(raw, 0) {
		return filepath.Clean(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Host != "" && u.Host != "localhost" || strings.ContainsRune(u.Path, 0) {
		return ""
	}
	path := filepath.FromSlash(u.Path)
	if runtime.GOOS == "windows" && len(path) > 2 && (path[0] == '/' || path[0] == '\\') && path[2] == ':' {
		path = path[1:]
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

func vscodeWorkspaceFolder(workspace string) string {
	f, err := openCompanion(filepath.Join(workspace, "workspace.json"))
	if err != nil {
		return ""
	}
	defer f.Close()
	var info struct{ Folder string }
	raw, err := io.ReadAll(io.LimitReader(f, maxProjectRootBytes+1))
	if err != nil || len(raw) > maxProjectRootBytes || json.Unmarshal(raw, &info) != nil {
		return ""
	}
	return localFolder(info.Folder)
}

type vscodeDebugEvent struct {
	SID, Type, Name, SpanID, Status string
	TS                              int64
	Dur                             float64
	Attrs                           struct {
		Model, Args, Result, Error, Content string
		ChildSessionID, ChildLogFile, Label string
		InputTokens, OutputTokens           *int64
		TTFT                                float64
	}
}

func parseStandaloneVSCodeDebug(path string, r io.Reader) (domain.Transcript, error) {
	workspace, parent := vscodeDebugWorkspace(path)
	id, parentID := parent, ""
	if name := filepath.Base(path); name != "main.jsonl" {
		f, err := openCompanion(filepath.Join(filepath.Dir(path), "main.jsonl"))
		if err != nil {
			return domain.Transcript{}, err
		}
		defer f.Close()
		id = ""
		_, err = eachLine(&capped{r: f, left: maxConversationBytes}, func(line []byte) {
			var e vscodeDebugEvent
			if json.Unmarshal(line, &e) == nil && e.SID == parent && e.Type == "child_session_ref" &&
				e.Attrs.ChildLogFile == name && strings.HasPrefix(e.Attrs.Label, "runSubagent") {
				id = e.Attrs.ChildSessionID
			}
		})
		if err != nil || id == "" {
			return domain.Transcript{}, err
		}
		parentID = parent
	}
	t, err := readVSCodeDebug(r, id, true, parentID)
	if err != nil || len(t.Requests)+len(t.Tools)+len(t.Prompts) == 0 {
		return domain.Transcript{}, err
	}
	t.Session.Folder = vscodeWorkspaceFolder(workspace)
	t.TitleFromFirstPrompt()
	return t, nil
}

func readVSCodeDebug(r io.Reader, id string, prompts bool, parent string) (domain.Transcript, error) {
	sid, err := domain.NewSessionID(kernel.VSCodeChat, id)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: sid, Harness: kernel.VSCodeChat, AgentType: domain.MainAgent}}
	if parent != "" {
		t.Session.ParentID, _ = domain.NewSessionID(kernel.VSCodeChat, parent)
		t.Session.AgentType = domain.Subagent
	}
	seen := map[string]bool{}
	oversized, err := eachLine(&capped{r: r, left: maxConversationBytes}, func(line []byte) {
		var e vscodeDebugEvent
		if json.Unmarshal(line, &e) != nil {
			t.SkippedLines++
			return
		}
		if e.SID != id || e.SpanID == "" || seen[e.Type+":"+e.SpanID] {
			return
		}
		at := epochMillis(e.TS)
		dur := millis(e.Dur)
		switch e.Type {
		case "llm_request":
			var count kernel.TokenCounts
			if e.Attrs.InputTokens != nil {
				count.Input = *e.Attrs.InputTokens
			}
			if e.Attrs.OutputTokens != nil {
				count.Output = *e.Attrs.OutputTokens
			}
			tokens, err := kernel.NewTokens(count)
			if err != nil {
				t.SkippedLines++
			}
			req := domain.Request{Key: e.SpanID, At: at, Model: e.Attrs.Model, Tokens: tokens, TTFT: millis(e.Attrs.TTFT), Duration: dur,
				PartialUsage: err != nil || e.Attrs.InputTokens == nil || e.Attrs.OutputTokens == nil}
			if e.Status == "error" {
				req.Failure = e.Attrs.Error
				if req.Failure == "" {
					req.Failure = domain.UnspecifiedFailure
				}
			}
			t.Requests = append(t.Requests, req)
		case "tool_call":
			if e.Name == "" {
				return
			}
			server, name := domain.SplitMCPToolName(e.Name)
			t.Tools = append(t.Tools, domain.ToolCall{Key: e.SpanID, At: at, Name: name, Server: server,
				ArgsChars: len(e.Attrs.Args), ResultChars: len(e.Attrs.Result), Duration: dur, IsError: e.Status == "error"})
		case "user_message":
			if prompts {
				if p, ok := domain.NewPrompt(e.SpanID, at, e.Attrs.Content); ok {
					t.Prompts = append(t.Prompts, p)
				}
			}
		case "session_start":
		default:
			return
		}
		seen[e.Type+":"+e.SpanID] = true
		t.Session.Span(at)
		if !at.IsZero() {
			t.Session.Span(at.Add(dur.Value()))
		}
	})
	if err != nil {
		return domain.Transcript{}, fmt.Errorf("VS Code diagnostic log: %w", err)
	}
	t.SkippedLines += oversized
	return t, nil
}
