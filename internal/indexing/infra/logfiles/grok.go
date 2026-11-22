package logfiles

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

const grokUpdates = "updates.jsonl"

type grokSummary struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	Created string `json:"created_at"`
	Updated string `json:"updated_at"`
	Forked  string `json:"forked_at"`
	Parent  string `json:"parent_session_id"`
	Kind    string `json:"session_kind"`
	Title   string `json:"generated_title"`
	Summary string `json:"session_summary"`
}

type grokChild struct {
	Parent string `json:"parent_session_id"`
	ID     string `json:"child_session_id"`
	Type   string `json:"subagent_type"`
}

type grokNotification struct {
	SessionID string     `json:"sessionId"`
	Update    grokUpdate `json:"update"`
}

type grokUpdate struct {
	Type      string          `json:"sessionUpdate"`
	ToolID    string          `json:"toolCallId"`
	Title     string          `json:"title"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	RawInput  grokSize        `json:"rawInput"`
	RawOutput grokSize        `json:"rawOutput"`
	Content   json.RawMessage `json:"content"`
	Meta      struct {
		PromptIndex *int `json:"promptIndex"`
		HostTurn    bool `json:"hostTurn"`
		Tool        struct {
			Name string `json:"name"`
		} `json:"x.ai/tool"`
	} `json:"_meta"`
}

type grokSize int

func (n *grokSize) UnmarshalJSON(b []byte) error {
	if len(b) != 4 || string(b) != "null" {
		*n = grokSize(len(b))
	}
	return nil
}

func openGrokFile(path string) (*os.File, error) {
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

func grokCompanion(path string, limit int64) ([]byte, error) {
	f, err := openGrokFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(raw)) > limit {
		return nil, ErrLogTooLarge
	}
	return raw, err
}

func grokSource(root string) Source {
	return Source{Harness: kernel.Grok, Root: root, Match: func(path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && strings.Count(rel, string(filepath.Separator)) == 2 && filepath.Base(path) == grokUpdates
	}, Beside: grokBeside}
}

func readGrokChild(root, path string) grokChild {
	rel, err := filepath.Rel(root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	if err != nil || len(parts) != 5 || parts[2] != "subagents" {
		return grokChild{}
	}
	raw, err := grokCompanion(path, 1<<20)
	var child grokChild
	if err != nil || json.Unmarshal(raw, &child) != nil || child.Parent != parts[1] || child.ID == child.Parent {
		return grokChild{}
	}
	return child
}

func grokFolder(dir, cwd string) string {
	group := filepath.Dir(dir)
	marker, _ := grokCompanion(filepath.Join(group, ".cwd"), maxProjectRootBytes)
	decoded, _ := url.PathUnescape(filepath.Base(group))
	for _, folder := range []string{cwd, string(marker), decoded} {
		if filepath.IsAbs(folder) && utf8.ValidString(folder) && strings.IndexFunc(folder, unicode.IsControl) < 0 {
			return folder
		}
	}
	return ""
}

func grokBeside(path string) string {
	dir := filepath.Dir(path)
	var revision strings.Builder
	for _, companion := range []string{
		filepath.Join(dir, "summary.json"),
		filepath.Join(dir, "usage.json"),
		filepath.Join(filepath.Dir(dir), ".cwd"),
	} {
		if info, err := os.Lstat(companion); err == nil && info.Mode().IsRegular() {
			revision.WriteString(":" + filepath.Base(companion) + "=" + fileRevision(info))
		}
	}
	return revision.String()
}

var (
	grokToolName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,256}$`)
	grokMCPName  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)__([A-Za-z0-9_-]+)$`)
)

func parseGrok(path string, r io.Reader, child grokChild, hasChildren bool) (domain.Transcript, error) {
	dir := filepath.Dir(path)
	native := filepath.Base(dir)
	id, err := domain.NewSessionID(kernel.Grok, native)
	if err != nil {
		return domain.Transcript{}, err
	}
	tr := domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.Grok, AgentType: domain.MainAgent}}
	var summary grokSummary
	if raw, err := grokCompanion(filepath.Join(dir, "summary.json"), 1<<20); err == nil {
		if json.Unmarshal(raw, &summary) != nil || summary.Info.ID != native {
			return domain.Transcript{}, errors.New("invalid Grok session summary")
		}
	} else if !os.IsNotExist(err) {
		return domain.Transcript{}, err
	}
	tr.Session.Folder = grokFolder(dir, summary.Info.CWD)
	tr.Session.Span(parseTS(summary.Created))
	tr.Session.Span(parseTS(summary.Updated))
	tr.Session.Title = domain.Title(summary.Title)
	if tr.Session.Title == "" {
		tr.Session.Title = domain.Title(summary.Summary)
	}
	parent := summary.Parent
	if child.Parent != "" {
		parent = child.Parent
	}
	if parent != native {
		tr.Session.ParentID, _ = domain.NewSessionID(kernel.Grok, parent)
	}
	if child.Parent != "" || summary.Kind == "subagent" || strings.HasPrefix(summary.Kind, "subagent_") {
		tr.Session.AgentType = domain.Subagent
		if agent, ok := domain.ParseAgentType(child.Type); ok {
			tr.Session.AgentType = agent
		}
	}
	forked := parseTS(summary.Forked)
	toolPositions := map[string]int{}
	var prompt strings.Builder
	var promptKey string
	var promptAt time.Time
	var promptIndex *int
	flushPrompt := func() {
		if p, ok := domain.NewPrompt(promptKey, promptAt, prompt.String()); ok {
			tr.Prompts = append(tr.Prompts, p)
		}
		prompt.Reset()
		promptKey, promptIndex = "", nil
	}
	line := 0
	oversized, err := eachLine(r, func(raw []byte) {
		line++
		var event struct {
			grokNotification
			Timestamp int64            `json:"timestamp"`
			Method    string           `json:"method"`
			Params    grokNotification `json:"params"`
		}
		if json.Unmarshal(raw, &event) != nil {
			tr.SkippedLines++
			flushPrompt()
			return
		}
		if event.Method != "" {
			if event.Method != "session/update" && event.Method != "_x.ai/session/update" {
				return
			}
			event.grokNotification = event.Params
		}
		update := event.Update
		if event.SessionID != native || update.Type == "" {
			tr.SkippedLines++
			return
		}
		var at time.Time
		if event.Timestamp > 0 && event.Timestamp < 253402300800 {
			at = time.Unix(event.Timestamp, 0).UTC()
		}
		// Les forks recopient les événements et les réhorodatent avant la création du résumé.
		if !forked.IsZero() && !at.After(forked.Truncate(time.Second)) {
			return
		}
		tr.Session.Span(at)
		if event.Method == "_x.ai/session/update" {
			hasChildren = hasChildren || update.Type == "subagent_spawned"
			return
		}
		if update.Type != "user_message_chunk" || update.Meta.HostTurn {
			flushPrompt()
		}
		switch update.Type {
		case "user_message_chunk":
			if update.Meta.HostTurn {
				return
			}
			index := update.Meta.PromptIndex
			if promptKey != "" && ((index == nil) != (promptIndex == nil) || index != nil && *index != *promptIndex) {
				flushPrompt()
			}
			if promptKey == "" {
				promptKey, promptAt, promptIndex = strconv.Itoa(line), at, index
			}
			var block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(update.Content, &block) == nil && block.Type == "text" {
				prompt.WriteString(block.Text)
			}
		case "tool_call", "tool_call_update":
			if update.ToolID == "" {
				tr.SkippedLines++
				return
			}
			i, found := toolPositions[update.ToolID]
			if !found {
				if update.Type != "tool_call" {
					return
				}
				i = len(tr.Tools)
				toolPositions[update.ToolID] = i
				name := update.Title
				if !grokToolName.MatchString(name) {
					name = "unknown"
					if grokToolName.MatchString(update.Kind) {
						name = update.Kind
					}
				}
				tr.Tools = append(tr.Tools, domain.ToolCall{Key: update.ToolID, At: at, Name: name})
			}
			call := &tr.Tools[i]
			if name := update.Meta.Tool.Name; grokToolName.MatchString(name) {
				call.Name = name
			}
			if call.ArgsChars == 0 {
				call.ArgsChars = int(update.RawInput)
			}
			if update.Status == "completed" || update.Status == "failed" {
				call.IsError = update.Status == "failed"
				call.ResultChars = int(update.RawOutput)
				if call.ResultChars == 0 && len(update.Content) > 0 && string(update.Content) != "null" {
					call.ResultChars = len(update.Content)
				}
			}
		}
	})
	if err != nil {
		return domain.Transcript{}, err
	}
	flushPrompt()
	tr.SkippedLines += oversized
	for i := range tr.Tools {
		call := &tr.Tools[i]
		if name := call.Name; !strings.Contains(name, "___") && strings.Count(name, "__") == 1 {
			if parts := grokMCPName.FindStringSubmatch(name); parts != nil {
				call.Server, call.Name = parts[1], parts[2]
			}
		}
	}
	tr.TitleFromFirstPrompt()
	// shortcut: le registre du parent peut inclure ses enfants, isoler les appels quand xAI expose leur provenance.
	if !hasChildren {
		if err := readGrokUsage(dir, native, forked, &tr); err != nil {
			return domain.Transcript{}, err
		}
	}
	return tr, nil
}

func readGrokUsage(dir, native string, forked time.Time, tr *domain.Transcript) error {
	raw, err := grokCompanion(filepath.Join(dir, "usage.json"), 16<<20)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var usage struct {
		ID    string `json:"sessionId"`
		Turns []struct {
			Number     uint32 `json:"turnNumber"`
			Ended      string `json:"endedAt"`
			Model      string `json:"primaryModelId"`
			Calls      uint64 `json:"modelCalls"`
			Input      int64  `json:"inputTokens"`
			Output     int64  `json:"outputTokens"`
			Read       int64  `json:"cachedReadTokens"`
			Write      int64  `json:"cacheCreationTokens"`
			Reasoning  int64  `json:"reasoningTokens"`
			Cost       int64  `json:"costUsdTicks"`
			Partial    bool   `json:"costIsPartial"`
			Incomplete bool   `json:"usageIsIncomplete"`
		} `json:"turns"`
	}
	if json.Unmarshal(raw, &usage) != nil || usage.ID != native {
		return errors.New("invalid Grok usage ledger")
	}
	seen := make(map[uint32]bool, len(usage.Turns))
	for _, turn := range usage.Turns {
		at := parseTS(turn.Ended)
		if turn.Calls != 1 || turn.Incomplete || at.IsZero() || !at.After(forked) || turn.Model == "" || seen[turn.Number] {
			continue
		}
		seen[turn.Number] = true
		if turn.Input < 0 || turn.Input > kernel.MaxTokenCount || turn.Read < 0 || turn.Read > turn.Input ||
			turn.Write < 0 || turn.Write > turn.Input-turn.Read {
			tr.SkippedLines++
			continue
		}
		tokens, err := kernel.NewTokens(kernel.TokenCounts{
			Input: turn.Input - turn.Read - turn.Write, Output: turn.Output,
			CacheRead: turn.Read, CacheWrite: turn.Write, Reasoning: turn.Reasoning,
		})
		if err != nil {
			tr.SkippedLines++
			continue
		}
		var cost kernel.Cost
		if !turn.Partial {
			cost, err = kernel.NewCost(float64(turn.Cost) / 1e10)
			if err != nil {
				tr.SkippedLines++
			}
		}
		tr.Session.Span(at)
		tr.Requests = append(tr.Requests, domain.Request{
			Key: strconv.FormatUint(uint64(turn.Number), 10), At: at, Model: turn.Model, Tokens: tokens, LoggedCost: cost,
		})
	}
	return nil
}
