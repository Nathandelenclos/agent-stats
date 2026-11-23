package logfiles

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

var antigravityID = regexp.MustCompile(`^[[:xdigit:]]{8}(-[[:xdigit:]]{4}){3}-[[:xdigit:]]{12}$`)

type antigravitySummary struct {
	id, title, modified, lastInput, workspaces, parent string
}

func antigravityTime(value string) time.Time {
	return parseTS(strings.Replace(value, " ", "T", 1))
}

func antigravitySummaries(path string) ([]antigravitySummary, error) {
	db, err := openSessionDatabase(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	text := func(name string, limit int) string {
		return "COALESCE(" + bounded(name, limit, "CAST("+name+" AS TEXT)") + ", '')"
	}
	query := "SELECT " + text("conversation_id", 36) + "," + text("title", maxDBTextBytes) + "," +
		text("last_modified_time", 64) + "," + text("last_user_input_time", 64) + "," +
		text("workspace_uris", maxDBTextBytes) + "," + text("parent_conversation_id", 36) +
		" FROM conversation_summaries"
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []antigravitySummary
	for rows.Next() {
		var s antigravitySummary
		if err := rows.Scan(&s.id, &s.title, &s.modified, &s.lastInput, &s.workspaces, &s.parent); err != nil {
			return nil, err
		}
		if antigravityID.MatchString(s.id) {
			summaries = append(summaries, s)
		}
	}
	return summaries, rows.Err()
}

type antigravitySession struct {
	summary antigravitySummary
	path    string
	info    fs.FileInfo
}

func listAntigravity(root string, readers map[string]func() (domain.Transcript, error)) ([]application.LogFile, []error) {
	sessions := map[string]antigravitySession{}
	var problems []error
	for _, client := range []string{"antigravity", "antigravity-ide", "antigravity-cli"} {
		dir := filepath.Join(root, client)
		database := filepath.Join(dir, "conversation_summaries.db")
		summaries, err := antigravitySummaries(database)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, unreadable(database, err))
		}
		for _, s := range summaries {
			id := strings.ToLower(s.id)
			entry := sessions[id]
			if entry.summary.id == "" || antigravityTime(s.modified).After(antigravityTime(entry.summary.modified)) {
				entry.summary = s
			}
			sessions[id] = entry
		}

		brain := filepath.Join(dir, "brain")
		dirs, err := os.ReadDir(brain)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, err)
		}
		for _, d := range dirs {
			if !d.IsDir() || !antigravityID.MatchString(d.Name()) {
				continue
			}
			id := strings.ToLower(d.Name())
			entry := sessions[id]
			const full = "transcript_full.jsonl"
			for _, name := range []string{full, "transcript.jsonl"} {
				path := filepath.Join(brain, d.Name(), ".system_generated", "logs", name)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				if entry.info == nil || info.ModTime().After(entry.info.ModTime()) ||
					(info.ModTime().Equal(entry.info.ModTime()) && name == full && filepath.Base(entry.path) != full) {
					entry.path, entry.info = path, info
				}
			}
			if entry.info != nil {
				sessions[id] = entry
			}
		}
	}

	files := make([]application.LogFile, 0, len(sessions))
	for id, entry := range sessions {
		path := filepath.Join(root, "antigravity") + DBSessionSep + id
		rev := fmt.Sprintf("antigravity-v1:%x", sha256.Sum256([]byte(fmt.Sprintf("%q", entry.summary))))
		var weight int64
		if entry.info != nil {
			rev += ":" + entry.path + ":" + fileRevision(entry.info)
			weight = logWeight(entry.path, entry.info.Size())
		}
		files = append(files, application.LogFile{Path: path, Harness: kernel.Antigravity, Revision: rev, WeightBytes: weight})
		readers[path] = func() (domain.Transcript, error) { return entry.read(id) }
	}
	return files, problems
}

func (entry antigravitySession) read(id string) (domain.Transcript, error) {
	native, err := domain.NewSessionID(kernel.Antigravity, id)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{ID: native, Harness: kernel.Antigravity, AgentType: domain.MainAgent}}
	s := entry.summary
	t.Session.Title = domain.Title(s.title)
	t.Session.Span(antigravityTime(s.lastInput))
	t.Session.Span(antigravityTime(s.modified))
	var folders []string
	if json.Unmarshal([]byte(s.workspaces), &folders) == nil && len(folders) == 1 {
		t.Session.Folder = localFolder(folders[0])
	}
	if antigravityID.MatchString(s.parent) && !strings.EqualFold(id, s.parent) {
		t.Session.ParentID, _ = domain.NewSessionID(kernel.Antigravity, strings.ToLower(s.parent))
		t.Session.AgentType = domain.Subagent
	}
	if entry.path != "" {
		f, err := openCompanion(entry.path)
		if err != nil {
			return domain.Transcript{}, err
		}
		defer f.Close()
		if err := parseAntigravity(&capped{r: f, left: maxLogBytes}, &t); err != nil {
			return domain.Transcript{}, err
		}
	}
	if t.Session.Title == "" {
		t.TitleFromFirstPrompt()
	}
	return t, nil
}

func parseAntigravity(r io.Reader, t *domain.Transcript) error {
	seen := map[int]bool{}
	oversized, err := eachLine(r, func(line []byte) {
		var step struct {
			Index   *int   `json:"step_index"`
			At      string `json:"created_at"`
			Source  string `json:"source"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Content string `json:"content"`
			Model   string `json:"model"`
			Input   *int64 `json:"input_tokens"`
			Cache   *int64 `json:"cache_read_tokens"`
			Output  *int64 `json:"output_tokens"`
			Tools   []struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(line, &step) != nil || step.Index == nil || *step.Index < 0 {
			t.SkippedLines++
			return
		}
		at := parseTS(step.At)
		if at.IsZero() {
			t.SkippedLines++
			return
		}
		if seen[*step.Index] {
			return
		}
		seen[*step.Index] = true
		key := strconv.Itoa(*step.Index)
		t.Session.Span(at)
		if step.Type == "USER_INPUT" && (step.Source == "USER_EXPLICIT" || step.Source == "USER") {
			content := step.Content
			if after, ok := strings.CutPrefix(content, "<USER_REQUEST>"); ok {
				content, _, _ = strings.Cut(after, "</USER_REQUEST>")
			}
			if p, ok := domain.NewPrompt(key, at, content); ok {
				t.Prompts = append(t.Prompts, p)
			}
		}
		if step.Source != "MODEL" || step.Type != "PLANNER_RESPONSE" || step.Status != "DONE" {
			return
		}
		if step.Model != "" && len(step.Model) <= maxDBKeyBytes && step.Input != nil && step.Output != nil {
			counts := kernel.TokenCounts{Input: *step.Input, Output: *step.Output}
			if step.Cache != nil {
				counts.CacheRead = *step.Cache
			}
			if tokens, err := kernel.NewTokens(counts); err == nil {
				t.Requests = append(t.Requests, domain.Request{Key: key, At: at, Model: step.Model,
					Tokens: tokens, PartialUsage: step.Cache == nil})
			}
		}
		for i, tool := range step.Tools {
			if tool.Name == "" || len(tool.Name) > maxDBKeyBytes {
				continue
			}
			server, name := domain.SplitMCPToolName(tool.Name)
			var args string
			size := len(tool.Args)
			if json.Unmarshal(tool.Args, &args) == nil {
				size = len(args)
			}
			t.Tools = append(t.Tools, domain.ToolCall{Key: key + ":" + strconv.Itoa(i), At: at, Name: name, Server: server, ArgsChars: size})
		}
	})
	t.SkippedLines += oversized
	return err
}
