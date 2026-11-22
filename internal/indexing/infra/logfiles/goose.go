package logfiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Goose stores sessions in one SQLite database. Historical SQLite schemas may omit
// name, parent_session_id, message_id and metadata_json.
type gooseDatabase struct{}

var gooseSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (gooseDatabase) paths(root string) []string {
	return []string{filepath.Join(root, "sessions.db")}
}

func (gooseDatabase) list(path string) ([]dbSession, []error) {
	db, err := openSessionDatabase(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	defer db.Close()

	ctx := context.Background()
	sessions, err := columns(ctx, db, "sessions")
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	messages, err := columns(ctx, db, "messages")
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	if !hasAll(sessions, "id", "working_dir", "created_at", "updated_at") ||
		!hasAll(messages, "id", "session_id", "role", "content_json", "created_timestamp") {
		return nil, []error{unreadable(path, ErrUnknownSchema)}
	}

	// shortcut: Goose edits message metadata in place without touching the session timestamp.
	// Invalidate all sessions on a database or WAL change until a per-session change marker is available.
	revision := "goose"
	for _, file := range []string{path, path + "-wal"} {
		info, err := os.Stat(file)
		switch {
		case err == nil:
			revision += ":" + fileRevision(info)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, []error{unreadable(path, err)}
		}
	}
	metadataBytes := "0"
	if messages["metadata_json"] {
		metadataBytes = "COALESCE(length(CAST(metadata_json AS BLOB)), 0)"
	}
	rows, err := db.QueryContext(ctx, `SELECT `+bounded("s.id", 128, "s.id")+`, COALESCE(stats.bytes, 0)
		FROM sessions s LEFT JOIN (
			SELECT session_id, SUM(COALESCE(length(CAST(content_json AS BLOB)), 0) + `+metadataBytes+` + `+strconv.Itoa(maxDBKeyBytes)+`) AS bytes
			FROM messages GROUP BY session_id
		) stats ON stats.session_id = s.id ORDER BY s.id`)
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	defer rows.Close()
	var found []dbSession
	var problems []error
	for rows.Next() {
		var id sql.NullString
		var weight int64
		if err := rows.Scan(&id, &weight); err != nil {
			return found, append(problems, unreadable(path, err))
		}
		if !gooseSessionID.MatchString(id.String) {
			problems = append(problems, unreadable(path, domain.ErrEmptySessionID))
			continue
		}
		found = append(found, dbSession{id: id.String, revision: revision, weight: weight})
	}
	if err := rows.Err(); err != nil {
		problems = append(problems, unreadable(path, err))
	}
	return found, problems
}

func (gooseDatabase) read(path string, h kernel.Harness, s dbSession) (domain.Transcript, error) {
	p, err := readGooseSession(path, h, s.id)
	if err != nil {
		return domain.Transcript{}, unreadable(path+DBSessionSep+s.id, err)
	}
	return p, nil
}

// gooseColumn projects an optional column introduced by a Goose migration.
func gooseColumn(cols map[string]bool, name string, limit int, fallback string) string {
	if !cols[name] {
		return fallback
	}
	return bounded(name, limit, "COALESCE("+name+", "+fallback+")")
}

func readGooseSession(path string, h kernel.Harness, id string) (domain.Transcript, error) {
	var p domain.Transcript
	db, err := openSessionDatabase(path)
	if err != nil {
		return p, err
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	sessions, err := columns(ctx, tx, "sessions")
	if err != nil {
		return p, err
	}
	messages, err := columns(ctx, tx, "messages")
	if err != nil {
		return p, err
	}

	var title, folder, parent, created, updated sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT `+gooseColumn(sessions, "name", maxDBTextBytes, "NULL")+`, `+
		bounded("working_dir", maxDBTextBytes, "working_dir")+`, `+gooseColumn(sessions, "parent_session_id", 128, "NULL")+`, `+
		bounded("created_at", 64, "CAST(created_at AS TEXT)")+`, `+bounded("updated_at", 64, "CAST(updated_at AS TEXT)")+
		` FROM sessions WHERE id = ?`, id).Scan(&title, &folder, &parent, &created, &updated)
	if err != nil {
		return p, err
	}
	sessionID, err := domain.NewSessionID(h, id)
	if err != nil {
		return p, err
	}
	p.Session = domain.Session{ID: sessionID, Harness: h, AgentType: domain.MainAgent,
		Title: domain.Title(title.String), Folder: absoluteFolder(folder.String)}
	if parent.String != id && gooseSessionID.MatchString(parent.String) {
		p.Session.ParentID, err = domain.NewSessionID(h, parent.String)
		if err != nil {
			return p, err
		}
		p.Session.AgentType = domain.Subagent
	}
	p.Session.Span(gooseSessionTime(created.String))
	p.Session.Span(gooseSessionTime(updated.String))

	rows, err := tx.QueryContext(ctx, `SELECT CAST(id AS INTEGER), `+gooseColumn(messages, "message_id", maxDBKeyBytes, "NULL")+
		`, `+bounded("role", 16, "role")+`, CAST(created_timestamp AS INTEGER), `+
		bounded("content_json", maxDBRowBytes, "content_json")+`, `+gooseColumn(messages, "metadata_json", maxDBRowBytes, "'{}'")+
		` FROM messages WHERE session_id = ? ORDER BY created_timestamp, id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()

	seenRequests := map[string]bool{}
	seenPrompts := map[string]bool{}
	tools := map[string]int{}
	budget := maxDBSessionBytes
	for rows.Next() {
		var rowID, timestamp int64
		var key, role sql.NullString
		var content, metadata []byte
		if err := rows.Scan(&rowID, &key, &role, &timestamp, &content, &metadata); err != nil {
			return p, err
		}
		budget -= int64(len(content) + len(metadata) + maxDBKeyBytes)
		if budget < 0 {
			return p, ErrLogTooLarge
		}

		var blocks []gooseBlock
		var meta gooseMetadata
		at := gooseMessageTime(timestamp)
		p.Session.Span(at)
		if at.IsZero() || json.Unmarshal(content, &blocks) != nil || json.Unmarshal(metadata, &meta) != nil {
			p.SkippedLines++
			continue
		}

		var prompt []string
		var failure string
		for _, block := range blocks {
			switch block.Type {
			case "text":
				prompt = append(prompt, block.Text)
			case "error":
				switch block.Kind {
				case "authentication", "contextLengthExceeded", "creditsExhausted", "resourceExhausted":
					failure = block.Kind
				default:
					failure = domain.UnspecifiedFailure
				}
			case "toolRequest":
				if role.String != "assistant" || block.ID == "" || block.Call.Status != "success" || block.Call.Value.Name == "" {
					continue
				}
				if _, exists := tools[block.ID]; !exists {
					tools[block.ID] = len(p.Tools)
					p.Tools = append(p.Tools, domain.ToolCall{Key: block.ID, At: at, Name: block.Call.Value.Name,
						ArgsChars: gooseJSONChars(block.Call.Value.Arguments)})
				}
			case "toolResponse":
				if i, exists := tools[block.ID]; exists {
					block.applyResult(&p.Tools[i])
				}
			}
		}

		switch role.String {
		case "user":
			if meta.TurnContext || meta.CompactionSummary || (meta.UserVisible != nil && !*meta.UserVisible) {
				continue
			}
			promptKey := cmp2(key.String, "row:"+strconv.FormatInt(rowID, 10))
			if pr, ok := domain.NewPrompt(promptKey, at, strings.Join(prompt, "\n")); ok && !seenPrompts[promptKey] {
				p.Prompts = append(p.Prompts, pr)
				seenPrompts[promptKey] = true
			}
		case "assistant":
			if key.String == "" || seenRequests[key.String] {
				continue
			}
			req, ok, err := meta.request(key.String, at, failure)
			if err != nil {
				p.SkippedLines++
			} else if ok {
				p.Requests = append(p.Requests, req)
				seenRequests[key.String] = true
			}
		}
	}
	p.TitleFromFirstPrompt()
	return p, rows.Err()
}

func gooseSessionTime(value string) time.Time {
	if t := parseTS(value); !t.IsZero() {
		return t
	}
	t, _ := time.Parse(time.DateTime, value)
	return t
}

func gooseMessageTime(timestamp int64) time.Time {
	switch {
	case timestamp <= 0:
		return time.Time{}
	case timestamp > 10_000_000_000: // Goose's threshold between seconds and milliseconds.
		return epochMilli(timestamp)
	default:
		return time.Unix(timestamp, 0)
	}
}

type gooseBlock struct {
	Type, ID, Text, Kind string
	Call                 struct {
		Status string
		Value  struct {
			Name      string
			Arguments json.RawMessage
		}
	} `json:"toolCall"`
	Result struct {
		Status string
		Value  json.RawMessage
		Error  string
	} `json:"toolResult"`
}

func gooseJSONChars(raw json.RawMessage) int {
	if string(raw) == "null" {
		return 0
	}
	return utf8.RuneCount(raw)
}

func (b gooseBlock) applyResult(call *domain.ToolCall) {
	switch b.Result.Status {
	case "error":
		call.ResultChars = utf8.RuneCountInString(b.Result.Error)
		call.IsError = true
	case "success":
		var value struct {
			IsError bool
		}
		// Before Goose stored the CallToolResult object, the value was the content array.
		if len(b.Result.Value) > 0 && b.Result.Value[0] == '{' {
			if json.Unmarshal(b.Result.Value, &value) != nil {
				return
			}
		} else if len(b.Result.Value) == 0 || b.Result.Value[0] != '[' {
			return
		}
		call.ResultChars = gooseJSONChars(b.Result.Value)
		call.IsError = value.IsError
	}
}

type gooseMetadata struct {
	UserVisible                    *bool
	TurnContext, CompactionSummary bool
	Inference                      struct {
		Provider, RequestedModel, ResolvedModel string
	}
	Usage *struct {
		InputTokens                                     *int64
		OutputTokens, CacheReadTokens, CacheWriteTokens int64
		Cost, ElapsedMS, TimeToFirstTokenMS             float64
		IsCompaction                                    bool
	}
}

func (m gooseMetadata) request(key string, at time.Time, failure string) (domain.Request, bool, error) {
	if m.CompactionSummary || (m.Usage != nil && m.Usage.IsCompaction) || (m.Usage == nil && failure == "") {
		return domain.Request{}, false, nil
	}
	r := domain.Request{Key: key, At: at, Model: cmp2(m.Inference.ResolvedModel, m.Inference.RequestedModel),
		Provider: m.Inference.Provider, Failure: failure}
	if u := m.Usage; u != nil {
		counts := kernel.TokenCounts{Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens}
		if u.InputTokens != nil {
			// Goose's input is inclusive of both cache counters. Validate before subtracting.
			total := *u.InputTokens
			if total < 0 || total > kernel.MaxTokenCount || counts.CacheRead < 0 || counts.CacheRead > total ||
				counts.CacheWrite < 0 || counts.CacheWrite > total-counts.CacheRead {
				return domain.Request{}, false, kernel.ErrInvalidTokens
			}
			counts.Input = total - counts.CacheRead - counts.CacheWrite
		}
		var err error
		if r.Tokens, err = kernel.NewTokens(counts); err != nil {
			return domain.Request{}, false, err
		}
		if r.LoggedCost, err = kernel.NewCost(u.Cost); err != nil {
			return domain.Request{}, false, err
		}
		r.Duration, r.TTFT = millis(u.ElapsedMS), millis(u.TimeToFirstTokenMS)
	}
	return r, true, nil
}
