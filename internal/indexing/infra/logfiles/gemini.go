package logfiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Gemini CLI keeps one conversation per file under <home>/.gemini/tmp/<project>/chats/: a JSON document in
// older versions, and since a recent one a JSONL journal that is replayed to get the conversation (a line adds
// or replaces a message, $patch edits messages, $rewindTo drops the tail, $set replaces the metadata).
// A subagent has a journal of its own, in the directory of its parent session.

// maxGeminiBytes bounds a conversation: it is held in memory to replay it, unlike the line logs of other harnesses.
var maxGeminiBytes int64 = 256 << 20

// ErrConversationUnreadable reports a Gemini CLI conversation that cannot be used: not JSON, truncated, or with no
// usable session id. What the JSON decoder says is kept as text, so that nothing above this reader depends on its errors.
var ErrConversationUnreadable = errors.New("conversation unreadable")

// geminiProvider is the provider of every Gemini CLI request: the CLI only talks to Google's API.
const geminiProvider = "google"

// geminiSessionID matches the ids Gemini CLI gives its sessions (UUIDs); the id becomes part of the session identity.
var geminiSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type geminiToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Args      size   `json:"args"`
	Result    size   `json:"result"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// geminiTokens are the counts of one message: Input includes the cached tokens, Output does not include the thoughts.
type geminiTokens struct {
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Cached   int64 `json:"cached"`
	Thoughts int64 `json:"thoughts"`
	Tool     int64 `json:"tool"`
}

type geminiMessage struct {
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Timestamp string           `json:"timestamp"`
	Content   json.RawMessage  `json:"content"`
	Model     string           `json:"model"`
	Tokens    *geminiTokens    `json:"tokens"`
	ToolCalls []geminiToolCall `json:"toolCalls"`
}

type geminiPatch struct {
	ID        string          `json:"id"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
	} `json:"toolCalls"`
	Updates   []*geminiPatch `json:"updates"`
	RemoveIDs []string       `json:"removeIds"`
	OrderIDs  []string       `json:"orderIds"`
}

// geminiMetadata is what a session says of itself, in the header of a JSON document or in a JSONL line.
type geminiMetadata struct {
	SessionID   string   `json:"sessionId"`
	ProjectHash string   `json:"projectHash"`
	StartTime   string   `json:"startTime"`
	Kind        string   `json:"kind"`
	Directories []string `json:"directories"`
}

// geminiRecord is one line of a journal. Which of the fields are there says what the line is.
type geminiRecord struct {
	geminiMessage
	geminiMetadata
	Rewind   *string         `json:"$rewindTo"`
	Patch    *geminiPatch    `json:"$patch"`
	Set      json.RawMessage `json:"$set"`
	Messages json.RawMessage `json:"messages"`
}

// geminiConversation is a conversation as replayed so far: messages keep their place when replaced.
type geminiConversation struct {
	meta     geminiMetadata
	ids      []string
	messages map[string]*geminiMessage
	skipped  int
}

func newGeminiConversation() *geminiConversation {
	return &geminiConversation{messages: map[string]*geminiMessage{}}
}

func (c *geminiConversation) put(m geminiMessage) {
	if m.ID == "" {
		c.skipped++
		return
	}
	if _, ok := c.messages[m.ID]; !ok {
		c.ids = append(c.ids, m.ID)
	}
	c.messages[m.ID] = &m
}

func (c *geminiConversation) remove(id string) {
	if _, ok := c.messages[id]; !ok {
		return
	}
	delete(c.messages, id)
	for i, x := range c.ids {
		if x == id {
			c.ids = append(c.ids[:i], c.ids[i+1:]...)
			return
		}
	}
}

func (c *geminiConversation) rewindTo(id string) {
	i := indexOf(c.ids, id)
	if i < 0 {
		i = 0 // an unknown message empties the conversation, as Gemini CLI does
	}
	for _, x := range c.ids[i:] {
		delete(c.messages, x)
	}
	c.ids = c.ids[:i]
}

func indexOf(ids []string, id string) int {
	for i, x := range ids {
		if x == id {
			return i
		}
	}
	return -1
}

// reorder moves the listed messages to the end, in the order given; the others keep their order in front.
func (c *geminiConversation) reorder(order []string) {
	listed := map[string]bool{}
	var tail []string
	for _, id := range order {
		if _, ok := c.messages[id]; ok && !listed[id] {
			listed[id] = true
			tail = append(tail, id)
		}
	}
	head := c.ids[:0:0]
	for _, id := range c.ids {
		if !listed[id] {
			head = append(head, id)
		}
	}
	c.ids = append(head, tail...)
}

func (c *geminiConversation) apply(p *geminiPatch) {
	if p.ID != "" {
		if m := c.messages[p.ID]; m != nil {
			if p.Content != nil {
				m.Content = p.Content
			}
			for _, tp := range p.ToolCalls {
				for i := range m.ToolCalls {
					if m.ToolCalls[i].ID == tp.ID && tp.Result != nil {
						m.ToolCalls[i].Result = size(len(tp.Result))
					}
				}
			}
		}
	}
	for _, u := range p.Updates {
		if u != nil && u.ID != "" {
			c.apply(&geminiPatch{ID: u.ID, Content: u.Content, ToolCalls: u.ToolCalls})
		}
	}
	for _, id := range p.RemoveIDs {
		c.remove(id)
	}
	if p.OrderIDs != nil {
		c.reorder(p.OrderIDs)
	}
}

// setMessages adds the messages of a JSON list, after emptying the conversation when replace is set; what is not a list changes nothing.
func (c *geminiConversation) setMessages(raw json.RawMessage, replace bool) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		c.skipped++
		return
	}
	if replace {
		c.ids, c.messages = nil, map[string]*geminiMessage{}
	}
	for _, r := range list {
		var m geminiMessage
		if json.Unmarshal(r, &m) != nil {
			c.skipped++
			continue
		}
		c.put(m)
	}
}

func (c *geminiConversation) mergeMeta(m geminiMetadata) {
	if m.SessionID != "" {
		c.meta.SessionID = m.SessionID
	}
	if m.ProjectHash != "" {
		c.meta.ProjectHash = m.ProjectHash
	}
	if m.StartTime != "" {
		c.meta.StartTime = m.StartTime
	}
	if m.Kind != "" {
		c.meta.Kind = m.Kind
	}
	if m.Directories != nil {
		c.meta.Directories = m.Directories
	}
}

// replay applies one line of a journal, in the order Gemini CLI checks what a line is.
func (c *geminiConversation) replay(line []byte) {
	var r geminiRecord
	if json.Unmarshal(line, &r) != nil {
		c.skipped++
		return
	}
	switch {
	case r.Rewind != nil:
		c.rewindTo(*r.Rewind)
	case r.Patch != nil:
		c.apply(r.Patch)
	case r.ID != "":
		c.put(r.geminiMessage)
	case r.Set != nil:
		var set struct {
			geminiMetadata
			Messages json.RawMessage `json:"messages"`
		}
		if json.Unmarshal(r.Set, &set) != nil {
			c.skipped++
			return
		}
		if set.Messages != nil {
			c.setMessages(set.Messages, true)
		}
		c.mergeMeta(set.geminiMetadata)
	case r.SessionID != "" && r.ProjectHash != "":
		c.mergeMeta(r.geminiMetadata)
		if r.Messages != nil {
			c.setMessages(r.Messages, false)
		}
	}
}

// readDocument reads a JSON document {"sessionId":…,"messages":[…]}. A message that is not usable is skipped
// and counted; a document that is not JSON, that is cut short or that has something after it is an error.
func (c *geminiConversation) readDocument(r io.Reader) error {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("not a JSON object")
	}
	header := map[string]json.RawMessage{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		if key == "messages" {
			if t, err := dec.Token(); err != nil || t != json.Delim('[') {
				return errors.New("messages is not a list")
			}
			for dec.More() {
				var m geminiMessage
				if err := dec.Decode(&m); err != nil {
					var typed *json.UnmarshalTypeError
					if !errors.As(err, &typed) {
						return err
					}
					c.skipped++
					continue
				}
				c.put(m)
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			continue
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		header[key.(string)] = raw
	}
	// A document is complete when its object is closed and nothing follows: a file cut short ends before the brace.
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return errors.New("truncated document")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("content after the document")
	}
	// A header value of the wrong type is ignored, like a message that cannot be used is skipped.
	var m geminiMetadata
	for key, dst := range map[string]any{"sessionId": &m.SessionID, "projectHash": &m.ProjectHash, "startTime": &m.StartTime,
		"kind": &m.Kind, "directories": &m.Directories} {
		if raw, ok := header[key]; ok && json.Unmarshal(raw, dst) != nil {
			c.skipped++
		}
	}
	c.mergeMeta(m)
	return nil
}

// ParseGemini reads a Gemini CLI conversation: a .json document or a .jsonl journal.
func ParseGemini(path string, r io.Reader) (domain.Transcript, error) {
	c := newGeminiConversation()
	limited := &io.LimitedReader{R: r, N: maxGeminiBytes + 1}
	if strings.HasSuffix(path, ".jsonl") {
		oversized, err := eachLine(limited, c.replay)
		c.skipped += oversized
		if err != nil {
			return domain.Transcript{}, err
		}
	} else if err := c.readDocument(limited); err != nil {
		if limited.N <= 0 {
			return domain.Transcript{}, fmt.Errorf("%w: over %d bytes", ErrLogTooLarge, maxGeminiBytes)
		}
		return domain.Transcript{}, fmt.Errorf("%s: %w: %v", filepath.Base(path), ErrConversationUnreadable, err)
	}
	if limited.N <= 0 {
		return domain.Transcript{}, fmt.Errorf("%w: over %d bytes", ErrLogTooLarge, maxGeminiBytes)
	}
	return c.transcript(path)
}

// geminiChatsDir is the "chats" directory a conversation file sits in, or in a subdirectory of: the second case is a subagent.
func geminiChatsDir(path string) (chats, parent string, ok bool) {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "chats" {
		return dir, "", true
	}
	if up := filepath.Dir(dir); filepath.Base(up) == "chats" {
		return up, filepath.Base(dir), true
	}
	return "", "", false
}

// IsGeminiConversation reports whether path is a conversation file of Gemini CLI: JSON or JSONL in a chats directory.
func IsGeminiConversation(path string) bool {
	if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl") {
		return false
	}
	_, _, ok := geminiChatsDir(path)
	return ok
}

// maxProjectRootBytes bounds the file that names the folder of a project.
const maxProjectRootBytes = 4096

// projectRoot is the .project_root file beside the chats, when it is a regular file: it is not followed when it is a link.
func projectRoot(chats string) (path string, info fs.FileInfo, ok bool) {
	path = filepath.Join(filepath.Dir(chats), ".project_root")
	info, err := os.Lstat(path)
	return path, info, err == nil && info.Mode().IsRegular()
}

// geminiBeside is what a conversation depends on besides its file, for its revision: the .project_root file that
// names its folder. A conversation read before that file existed must be read again when it appears.
func geminiBeside(path string) string {
	chats, _, ok := geminiChatsDir(path)
	if !ok {
		return ""
	}
	if _, info, ok := projectRoot(chats); ok {
		return fmt.Sprintf(":root=%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	return ""
}

// geminiFolder reads the folder of the project from the .project_root file Gemini CLI keeps beside the chats; empty when unknown.
func geminiFolder(chats string) string {
	path, info, ok := projectRoot(chats)
	if !ok {
		return ""
	}
	f, err := openRegular(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return "" // the file was replaced by a link since it was looked at
	}
	raw, _ := io.ReadAll(io.LimitReader(f, maxProjectRootBytes+1))
	if len(raw) > maxProjectRootBytes {
		return ""
	}
	folder := string(bytes.TrimSpace(raw))
	if strings.IndexFunc(folder, unicode.IsControl) >= 0 || !filepath.IsAbs(folder) {
		return ""
	}
	return folder
}

// text is the text of a message content: a string, or a list of parts that hold some.
func geminiText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func (c *geminiConversation) transcript(path string) (domain.Transcript, error) {
	p := domain.Transcript{Session: domain.Session{Harness: kernel.Gemini, AgentType: domain.MainAgent}}
	if !geminiSessionID.MatchString(c.meta.SessionID) {
		return domain.Transcript{}, fmt.Errorf("%s: %w: no valid session id", filepath.Base(path), ErrConversationUnreadable)
	}
	id, err := domain.NewSessionID(kernel.Gemini, c.meta.SessionID)
	if err != nil {
		return domain.Transcript{}, err
	}
	p.Session.ID = id
	chats, parent, _ := geminiChatsDir(path)
	if parent != "" && geminiSessionID.MatchString(parent) {
		parentID, err := domain.NewSessionID(kernel.Gemini, parent)
		if err != nil {
			return domain.Transcript{}, err
		}
		if p.Session.ID, err = parentID.Subagent(c.meta.SessionID); err != nil {
			return domain.Transcript{}, err
		}
		p.Session.ParentID, p.Session.AgentType = parentID, domain.Subagent
	} else if c.meta.Kind == "subagent" {
		p.Session.AgentType = domain.Subagent // the parent is not named: it is its own root
	}
	if chats != "" {
		p.Session.Folder = geminiFolder(chats)
	}
	if p.Session.Folder == "" && len(c.meta.Directories) > 0 && filepath.IsAbs(c.meta.Directories[0]) &&
		strings.IndexFunc(c.meta.Directories[0], unicode.IsControl) < 0 {
		p.Session.Folder = c.meta.Directories[0]
	}
	p.Session.Span(parseTS(c.meta.StartTime))
	p.SkippedLines = c.skipped
	for _, id := range c.ids {
		m := c.messages[id]
		ts := parseTS(m.Timestamp)
		p.Session.Span(ts)
		switch m.Type {
		case "user":
			if pr, ok := domain.NewPrompt(m.ID, ts, geminiText(m.Content)); ok {
				p.Prompts = append(p.Prompts, pr)
			}
		case "gemini":
			if m.Tokens != nil {
				req := domain.Request{Key: m.ID, At: ts, Provider: geminiProvider, Model: m.Model}
				if tokens, err := geminiUsage(*m.Tokens); err == nil {
					req.Tokens = tokens
					p.Requests = append(p.Requests, req)
				} else {
					p.SkippedLines++
				}
			}
			for _, t := range m.ToolCalls {
				server, name := domain.SplitMCPToolName(t.Name)
				at := parseTS(t.Timestamp)
				if at.IsZero() {
					at = ts
				}
				p.Tools = append(p.Tools, domain.ToolCall{Key: t.ID, At: at, Name: name, Server: server,
					ArgsChars: int(t.Args), ResultChars: int(t.Result), IsError: t.Status == "error"})
			}
		}
	}
	p.TitleFromFirstPrompt()
	return p, nil
}

// geminiUsage converts the counts of a message. The cached tokens are part of the input and the tool prompt
// tokens are input too; the thoughts are billed as output.
func geminiUsage(t geminiTokens) (kernel.Tokens, error) {
	if t.Cached < 0 || t.Cached > t.Input {
		return kernel.Tokens{}, kernel.ErrInvalidTokens
	}
	return kernel.NewTokens(kernel.TokenCounts{Input: t.Input - t.Cached + t.Tool, Output: t.Output + t.Thoughts,
		CacheRead: t.Cached, Reasoning: t.Thoughts})
}
