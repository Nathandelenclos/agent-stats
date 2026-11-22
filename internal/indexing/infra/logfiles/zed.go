package logfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Zed's native thread store at revision 2f808b3c2d:
// https://github.com/zed-industries/zed/blob/2f808b3c2d/crates/agent/src/db.rs
type zedDatabase struct{}

const maxZedThreadBytes = 64 << 20

func zedThreadsDir(home string) string {
	var data string
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support", "Zed")
	case "windows":
		data = filepath.Join(envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local")), "Zed")
	default:
		data = filepath.Join(envOr("FLATPAK_XDG_DATA_HOME", envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))), "zed")
	}
	return filepath.Join(data, "threads")
}

func (zedDatabase) paths(root string) []string {
	path := filepath.Join(root, "threads.db")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	return []string{path}
}

// zedRows selects one bounded payload and its metadata per row, also on databases without optional columns.
func zedRows(db *sql.DB) (string, error) {
	cols, err := columns(context.Background(), db, "threads")
	if err != nil {
		return "", err
	}
	if !hasAll(cols, "id", "summary", "updated_at", "data_type", "data") {
		return "", ErrUnknownSchema
	}
	text := func(name string, limit int) string {
		if !cols[name] {
			return "''"
		}
		return "COALESCE(" + bounded(name, limit, name) + ", '')"
	}
	return `SELECT ` + text("id", maxDBKeyBytes) + ", " +
		text("summary", maxDBTextBytes) + ", " + text("updated_at", 64) + ", " +
		text("data_type", 16) + ", " + text("parent_id", maxDBKeyBytes) + ", " +
		text("folder_paths", maxDBTextBytes) + ", " + text("created_at", 64) + ", " +
		boundedData + " FROM threads", nil
}

type zedRow struct {
	id, title, updated, encoding, parent, folders, created string
	data                                                   []byte
}

func (r *zedRow) scan(row interface{ Scan(...any) error }) error {
	if err := row.Scan(&r.id, &r.title, &r.updated, &r.encoding, &r.parent, &r.folders, &r.created, &r.data); err != nil {
		return err
	}
	switch {
	case r.id == "":
		return domain.ErrEmptySessionID
	case r.encoding != "json" && r.encoding != "zstd":
		return ErrUnknownSchema
	case r.data == nil:
		return ErrLogTooLarge
	}
	return nil
}

func (r zedRow) revision() string {
	// Zed can save changes without advancing updated_at.
	digest := sha256.New()
	fmt.Fprintf(digest, "%q", []string{r.title, r.updated, r.encoding, r.parent, r.folders, r.created})
	digest.Write(r.data)
	return hex.EncodeToString(digest.Sum(nil))
}

func (zedDatabase) list(path string) ([]dbSession, []error) {
	db, err := openSessionDatabase(path)
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	defer db.Close()
	query, err := zedRows(db)
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	rows, err := db.Query(query+" ORDER BY id", maxZedThreadBytes)
	if err != nil {
		return nil, []error{unreadable(path, err)}
	}
	defer rows.Close()

	var sessions []dbSession
	var problems []error
	for rows.Next() {
		var row zedRow
		if err := row.scan(rows); err != nil {
			problems = append(problems, unreadable(path, err))
			continue
		}
		weight := int64(len(row.data))
		if row.encoding == "zstd" {
			weight = min(weight*unknownZstdRatio, maxZedThreadBytes)
		}
		sessions = append(sessions, dbSession{id: row.id, revision: row.revision(), weight: weight})
	}
	if err := rows.Err(); err != nil {
		problems = append(problems, unreadable(path, err))
	}
	return sessions, problems
}

func (zedDatabase) read(path string, h kernel.Harness, s dbSession) (domain.Transcript, error) {
	db, err := openSessionDatabase(path)
	if err != nil {
		return domain.Transcript{}, unreadable(path, err)
	}
	defer db.Close()
	query, err := zedRows(db)
	if err != nil {
		return domain.Transcript{}, unreadable(path, err)
	}
	var row zedRow
	if err := row.scan(db.QueryRow(query+" WHERE id = ?", maxZedThreadBytes, s.id)); err != nil {
		return domain.Transcript{}, unreadable(path, err)
	}
	if row.revision() != s.revision {
		return domain.Transcript{}, unreadable(path, errors.New("Zed thread changed since listing"))
	}
	transcript, err := row.transcript(h)
	if err != nil {
		return domain.Transcript{}, unreadable(path, err)
	}
	return transcript, nil
}

func decodeZedThread(data []byte, encoding string, limit int, thread *zedThread) error {
	if encoding == "zstd" {
		d, err := zstdReader(bytes.NewReader(data))
		if err != nil {
			return errors.New("invalid Zed zstd stream")
		}
		defer releaseZstd(d)
		data, err = io.ReadAll(io.LimitReader(d, int64(limit)+1))
		if err != nil {
			return errors.New("invalid Zed zstd stream")
		}
	}
	if len(data) > limit {
		return ErrLogTooLarge
	}
	if err := json.Unmarshal(data, thread); err != nil {
		return errors.New("invalid Zed thread JSON")
	}
	// shortcut: only the native 0.3.0 message layout is read, upgrade when Agent 1 archives are needed.
	if thread.Version != "0.3.0" {
		return ErrUnknownSchema
	}
	if thread.Messages == nil {
		return errors.New("missing Zed messages")
	}
	return nil
}

type zedThread struct {
	Version  string
	Messages []json.RawMessage
	Subagent struct {
		ParentID string `json:"parent_thread_id"`
	} `json:"subagent_context"`
}

// zedMessage holds the native message variants that contain prompts or calls.
type zedMessage struct {
	User *struct {
		ID      string
		Content []struct{ Text string }
	}
	Agent *struct {
		Content []struct {
			ToolUse *struct {
				ID, Name string
				RawInput string `json:"raw_input"`
			}
		}
		ToolResults map[string]struct {
			IsError bool `json:"is_error"`
			Content size
		} `json:"tool_results"`
	}
}

func (r zedRow) transcript(h kernel.Harness) (domain.Transcript, error) {
	var thread zedThread
	if err := decodeZedThread(r.data, r.encoding, maxZedThreadBytes, &thread); err != nil {
		return domain.Transcript{}, err
	}
	id, err := domain.NewSessionID(h, r.id)
	if err != nil {
		return domain.Transcript{}, err
	}
	t := domain.Transcript{Session: domain.Session{
		ID: id, Harness: h, AgentType: domain.MainAgent, Title: domain.Title(r.title),
		Start: parseTS(r.created), End: parseTS(r.updated),
	}}
	if t.Session.End.IsZero() {
		return domain.Transcript{}, errors.New("invalid Zed thread update time")
	}
	// The index has one folder per session; a multi-root workspace has no unique project.
	if !strings.Contains(r.folders, "\n") && filepath.IsAbs(r.folders) {
		t.Session.Folder = filepath.Clean(r.folders)
	}
	parent := r.parent
	if parent == "" && len(thread.Subagent.ParentID) <= maxDBKeyBytes {
		parent = thread.Subagent.ParentID
	}
	if parent != "" {
		t.Session.ParentID, err = domain.NewSessionID(h, parent)
		if err != nil {
			return domain.Transcript{}, err
		}
		t.Session.AgentType = domain.Subagent
	}

	promptKeys, toolKeys := map[string]bool{}, map[string]bool{}
	for _, raw := range thread.Messages {
		// Resume is a unit variant, while Compaction carries neither type of activity.
		if bytes.Equal(raw, []byte(`"Resume"`)) {
			continue
		}
		var message zedMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			t.SkippedLines++
			continue
		}
		if user := message.User; user != nil && user.ID != "" && len(user.ID) <= maxDBKeyBytes && !promptKeys[user.ID] {
			var parts []string
			for _, block := range user.Content {
				if block.Text != "" {
					parts = append(parts, block.Text)
				}
			}
			if p, ok := domain.NewPrompt(user.ID, time.Time{}, strings.Join(parts, "\n")); ok {
				t.Prompts = append(t.Prompts, p)
				promptKeys[user.ID] = true
			}
		}
		if agent := message.Agent; agent != nil {
			for _, block := range agent.Content {
				call := block.ToolUse
				if call == nil || call.ID == "" || len(call.ID) > maxDBKeyBytes ||
					call.Name == "" || len(call.Name) > maxDBTextBytes || toolKeys[call.ID] {
					continue
				}
				result := agent.ToolResults[call.ID]
				t.Tools = append(t.Tools, domain.ToolCall{Key: call.ID, Name: call.Name,
					ArgsChars: len(call.RawInput), ResultChars: int(result.Content), IsError: result.IsError})
				toolKeys[call.ID] = true
			}
		}
	}
	// Zed's token counters are by turn or thread and do not identify individual model requests.
	t.TitleFromFirstPrompt()
	return t, nil
}
