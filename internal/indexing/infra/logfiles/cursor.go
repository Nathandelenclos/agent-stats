package logfiles

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func cursorUserDir(home string) string {
	var data string
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support")
	case "windows":
		data = envOr("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	default:
		data = envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	return filepath.Join(data, "Cursor", "User")
}

var cursorID = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")

type cursorLog struct {
	path, revision string
	weight         int64
	session        domain.Session
	draft          bool
}

// listCursor combines Composer metadata and transcripts before the index sees them.
// The index key survives the arrival of either source and the move to a nested subagent transcript.
func listCursor(root, user string, readers map[string]func() (domain.Transcript, error)) ([]application.LogFile, []error) {
	dbPath := filepath.Join(user, "globalStorage", "state.vscdb")
	logs := map[string]cursorLog{}
	if err := cursorHeaders(dbPath, user, logs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, []error{unreadable(dbPath, err)}
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || path == root {
			return err
		}
		parts := strings.Split(path[len(root)+1:], string(filepath.Separator))
		if d.IsDir() {
			if len(parts) == 2 && parts[1] != "agent-transcripts" ||
				len(parts) == 4 && parts[3] != "subagents" || len(parts) > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		id, jsonl := strings.CutSuffix(d.Name(), ".jsonl")
		if !d.Type().IsRegular() || !jsonl || !cursorID.MatchString(id) {
			return nil
		}
		var parent domain.SessionID
		switch {
		case len(parts) == 4 && parts[1] == "agent-transcripts" && parts[2] == id:
		case len(parts) == 5 && parts[1] == "agent-transcripts" && parts[3] == "subagents" && cursorID.MatchString(parts[2]) && parts[2] != id:
			parent, _ = domain.NewSessionID(kernel.Cursor, parts[2])
		default:
			return nil
		}
		log := logs[id]
		if log.path != "" && (parent.IsZero() || !log.session.ParentID.IsZero()) {
			return nil // Prefer the nested copy, whose path names the parent.
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		log.path, log.revision, log.weight = path, fileRevision(info), info.Size()
		if !parent.IsZero() {
			log.session.ParentID = parent
			if log.session.AgentType == (domain.AgentType{}) {
				log.session.AgentType = domain.Subagent
			}
		}
		logs[id] = log
		return nil
	})
	if err != nil {
		return nil, []error{err}
	}
	var files []application.LogFile
	for id, log := range logs {
		if log.draft && log.path == "" {
			continue
		}
		s := &log.session
		s.ID, _ = domain.NewSessionID(kernel.Cursor, id)
		s.Harness = kernel.Cursor
		if s.AgentType == (domain.AgentType{}) {
			s.AgentType = domain.MainAgent
		}
		path := dbPath + DBSessionSep + id
		readers[path] = log.read
		revision := fmt.Sprintf("cursor:%d:%d:%q:%q:%q:%q:%s", kernel.UnixMilli(s.Start), kernel.UnixMilli(s.End),
			s.Folder, s.AgentType.String(), s.ParentID.String(), log.path, log.revision)
		files = append(files, application.LogFile{Path: path, Harness: kernel.Cursor, Revision: revision, WeightBytes: min(log.weight, maxLogBytes)})
	}
	slices.SortFunc(files, func(a, b application.LogFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func cursorHeaders(path, user string, logs map[string]cursorLog) error {
	db, err := openSessionDatabase(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var found bool
	err = db.QueryRow(`SELECT count(*) = 7 FROM pragma_table_info('composerHeaders')
		WHERE name IN ('composerId', 'workspaceId', 'createdAt', 'lastUpdatedAt', 'isSubagent', 'subagentTypeName', 'value')`).Scan(&found)
	if err != nil || !found {
		return err
	}
	rows, err := db.Query("SELECT "+bounded("composerId", 128, "composerId")+", "+
		bounded("workspaceId", 128, "workspaceId")+`, CASE WHEN typeof(createdAt) = 'integer' THEN createdAt END,
		CASE WHEN typeof(lastUpdatedAt) = 'integer' THEN lastUpdatedAt END, isSubagent = 1, `+
		bounded("subagentTypeName", 64, "subagentTypeName")+`, COALESCE(CASE
			WHEN length(CAST(value AS BLOB)) <= ? AND json_valid(value)
			THEN json_type(value, '$.isDraft') = 'true' END, 0)
		FROM composerHeaders`, maxDBRowBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	folders := map[string]string{}
	for rows.Next() {
		var id, workspace, agentType sql.NullString
		var created, updated, subagent sql.NullInt64
		var draft bool
		if err := rows.Scan(&id, &workspace, &created, &updated, &subagent, &agentType, &draft); err != nil {
			return err
		}
		if !cursorID.MatchString(id.String) {
			continue
		}
		if _, ok := folders[workspace.String]; !ok {
			folders[workspace.String] = cursorFolder(user, workspace.String)
		}
		s := domain.Session{Folder: folders[workspace.String], Start: epochMilli(created.Int64), End: epochMilli(updated.Int64)}
		if subagent.Int64 == 1 {
			s.AgentType = domain.Subagent
			if declared, ok := domain.ParseAgentType(agentType.String); ok && declared != domain.MainAgent {
				s.AgentType = declared
			}
		}
		logs[id.String] = cursorLog{session: s, draft: draft}
	}
	return rows.Err()
}

// cursorFolder resolves the editor's workspace ID via its single-folder metadata.
func cursorFolder(user, workspace string) string {
	if !cursorID.MatchString(workspace) {
		return ""
	}
	path := filepath.Join(user, "workspaceStorage", workspace, "workspace.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := openRegular(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxProjectRootBytes+1))
	var workspaceMeta struct {
		Folder string
	}
	if err != nil || len(data) > maxProjectRootBytes || json.Unmarshal(data, &workspaceMeta) != nil {
		return ""
	}
	uri, err := url.Parse(workspaceMeta.Folder)
	if err != nil || uri.Scheme != "file" || uri.Host != "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" {
		return ""
	}
	folder := filepath.FromSlash(uri.Path)
	if runtime.GOOS == "windows" && len(folder) > 2 && folder[0] == '\\' && folder[2] == ':' {
		folder = folder[1:]
	}
	if !filepath.IsAbs(folder) || strings.IndexFunc(folder, unicode.IsControl) >= 0 {
		return ""
	}
	return filepath.Clean(folder)
}

func (log cursorLog) read() (domain.Transcript, error) {
	t := domain.Transcript{Session: log.session}
	if log.path == "" {
		return t, nil
	}
	return ParseFile(log.path, func(_ string, r io.Reader) (domain.Transcript, error) {
		line := 0
		skipped, err := eachLine(r, func(data []byte) {
			line++
			var row struct {
				Role    string
				Message struct {
					Content []struct {
						Type, Text, Name string
						Input            size
					}
				}
			}
			if json.Unmarshal(data, &row) != nil {
				t.SkippedLines++
				return
			}
			switch row.Role {
			case "user":
				var blocks []string
				for _, block := range row.Message.Content {
					if block.Type == "text" {
						blocks = append(blocks, block.Text)
					}
				}
				at, text := cursorPrompt(strings.Join(blocks, "\n"))
				t.Session.Span(at)
				if p, ok := domain.NewPrompt(strconv.Itoa(line), at, text); ok {
					t.Prompts = append(t.Prompts, p)
				}
			case "assistant":
				for i, block := range row.Message.Content {
					if block.Type != "tool_use" {
						continue
					}
					if strings.TrimSpace(block.Name) == "" || len(block.Name) > maxDBKeyBytes ||
						strings.IndexFunc(block.Name, unicode.IsControl) >= 0 {
						t.SkippedLines++
						continue
					}
					t.Tools = append(t.Tools, domain.ToolCall{Key: fmt.Sprintf("%d:%d", line, i),
						Name: block.Name, ArgsChars: int(block.Input)})
				}
			}
		})
		t.SkippedLines += skipped
		t.TitleFromFirstPrompt()
		return t, err
	})
}

// cursorPrompt removes the editor's wrappers.
// shortcut: dates use the observed English format and whole-hour offsets, extend when more formats are verified.
func cursorPrompt(text string) (time.Time, string) {
	var at time.Time
	text = strings.TrimSpace(text)
	if after, ok := strings.CutPrefix(text, "<timestamp>"); ok {
		if stamp, rest, ok := strings.Cut(after, "</timestamp>"); ok {
			clock, zone, ok := strings.Cut(stamp, " (UTC")
			offset, err := strconv.Atoi(strings.TrimSuffix(zone, ")"))
			if ok && err == nil && offset >= -12 && offset <= 14 {
				at, _ = time.ParseInLocation("Monday, Jan 2, 2006, 3:04 PM", clock, time.FixedZone("", offset*3600))
			}
			text = strings.TrimSpace(rest)
		}
	}
	if strings.HasPrefix(text, "<user_query>") && strings.HasSuffix(text, "</user_query>") {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "<user_query>"), "</user_query>"))
	}
	return at, text
}
