package logfiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

const geminiDocument = `{"sessionId":"s-1","projectHash":"h","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:05:00.000Z","summary":"ignored",
"messages":[
{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":[{"text":"Add a "},{"text":"cache"}]},
{"id":"m2","timestamp":"2026-02-01T10:00:05.000Z","type":"gemini","content":"On it","model":"gemini-x",
 "tokens":{"input":1000,"output":50,"cached":600,"thoughts":20,"tool":10,"total":1080},
 "toolCalls":[{"id":"t1","name":"read_file","args":{"path":"a.go"},"result":[{"functionResponse":{}}],"status":"success","timestamp":"2026-02-01T10:00:06.000Z"},
              {"id":"t2","name":"mcp__demo__lookup","args":{},"result":[],"status":"error","timestamp":"2026-02-01T10:00:07.000Z"}]},
{"id":"m3","timestamp":"2026-02-01T10:00:09.000Z","type":"error","content":"quota"},
{"id":"m4","timestamp":"2026-02-01T10:00:10.000Z","type":"gemini","content":"cancelled before any usage"}
]}`

func TestAConversationInTheJSONFormatIsASessionWithItsPromptAndItsSpan(t *testing.T) {
	// Given a conversation with a prompt made of two parts, answers and an error
	// When it is read
	p := parse(t, ParseGemini, "/home/u/.gemini/tmp/demo/chats/session-1.json", geminiDocument)

	// Then the session is named by its id and spans its messages
	s := p.Session
	if s.ID.String() != "gemini:s-1" || s.Harness != kernel.Gemini || s.AgentType != domain.MainAgent || !s.ParentID.IsZero() {
		t.Errorf("session = %+v", s)
	}
	if want := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC); !s.Start.Equal(want) || !s.End.Equal(want.Add(10*time.Second)) {
		t.Errorf("span = %v .. %v", s.Start, s.End)
	}
	// And its prompt is the text of its parts, which also titles it
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Add a cache" || p.Prompts[0].Key != "m1" || s.Title != "Add a cache" {
		t.Errorf("prompts = %+v, title %q", p.Prompts, s.Title)
	}
}

func TestOnlyTheAnswerThatStatesItsUsageIsARequest(t *testing.T) {
	// Given a conversation with an answer that states its usage, an error and an answer without usage
	// When it is read
	p := parse(t, ParseGemini, "/home/u/.gemini/tmp/demo/chats/session-1.json", geminiDocument)

	// Then the answer with usage is the only request, and it has no time to first token or duration
	if len(p.Requests) != 1 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	r := p.Requests[0]
	if r.Key != "m2" || r.Model != "gemini-x" || r.Provider != "google" || r.Duration.Known() || r.TTFT.Known() || r.Failed() {
		t.Errorf("request = %+v", r)
	}
}

func TestTheCachedTokensArePartOfTheInputAndTheThoughtsPartOfTheOutput(t *testing.T) {
	// Given an answer of 1000 input tokens of which 600 are cached, 50 output tokens and 20 thoughts
	// When it is read
	p := parse(t, ParseGemini, "/home/u/.gemini/tmp/demo/chats/session-1.json", geminiDocument)

	// Then the cache is counted as cache reads and not added to the input, and the thoughts count as output
	want := kernel.TokenCounts{Input: 1000 - 600 + 10, Output: 70, CacheRead: 600, Reasoning: 20}
	if got := p.Requests[0].Tokens.Counts(); got != want {
		t.Errorf("tokens = %+v, want %+v", got, want)
	}
}

func TestToolCallsKeepTheirSizesTheirStatusAndTheServerTheyName(t *testing.T) {
	// Given an answer with two tool calls, one of which failed and belongs to an MCP server
	// When the conversation is read
	p := parse(t, ParseGemini, "/home/u/.gemini/tmp/demo/chats/session-1.json", geminiDocument)

	// Then each call states its size, its status and its server
	if len(p.Tools) != 2 {
		t.Fatalf("tools = %+v", p.Tools)
	}
	if a, b := p.Tools[0], p.Tools[1]; a.Name != "read_file" || a.ArgsChars != len(`{"path":"a.go"}`) || a.ResultChars != len(`[{"functionResponse":{}}]`) || a.IsError ||
		b.Server != "demo" || b.Name != "lookup" || !b.IsError || !b.At.Equal(time.Date(2026, 2, 1, 10, 0, 7, 0, time.UTC)) {
		t.Errorf("tools = %+v", p.Tools)
	}
}

func journal(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const answerToReplace = `{"id":"a1","timestamp":"2026-02-02T10:00:02.000Z","type":"gemini","content":"x","model":"m","tokens":{"input":10,"output":1,"cached":0,"thoughts":0,"tool":0,"total":11},"toolCalls":[{"id":"c1","name":"glob","args":{},"result":[],"status":"success","timestamp":"2026-02-02T10:00:02.000Z"}]}`

const conversationStart = `{"sessionId":"s-2","projectHash":"h","startTime":"2026-02-02T10:00:00.000Z","kind":"main"}`

func TestAnAnswerThatIsSaidAgainCountsOnceWithItsNewUsage(t *testing.T) {
	// Given a journal in which an answer is stated twice, the second time with other usage
	log := journal(conversationStart, answerToReplace,
		`{"id":"a1","timestamp":"2026-02-02T10:00:02.000Z","type":"gemini","content":"x again","model":"m","tokens":{"input":11,"output":1,"cached":0,"thoughts":0,"tool":0,"total":12}}`)

	// When it is read
	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	// Then the answer is one request with its last usage
	if len(p.Requests) != 1 || p.Requests[0].Key != "a1" || p.Requests[0].Tokens.Counts().Input != 11 {
		t.Errorf("requests = %+v", p.Requests)
	}
}

func TestAPromptThatWasRemovedIsNotCounted(t *testing.T) {
	log := journal(conversationStart,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":[{"text":"first"}]}`,
		`{"id":"u2","timestamp":"2026-02-02T10:00:03.000Z","type":"user","content":[{"text":"second"}]}`,
		`{"$patch":{"removeIds":["u2"]}}`)

	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	if len(p.Prompts) != 1 || p.Prompts[0].Text != "first" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
}

func TestRewindingToAMessageDropsItAndWhatCameAfter(t *testing.T) {
	log := journal(conversationStart,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":[{"text":"first"}]}`,
		`{"id":"u2","timestamp":"2026-02-02T10:00:03.000Z","type":"user","content":[{"text":"second"}]}`,
		`{"id":"u3","timestamp":"2026-02-02T10:00:05.000Z","type":"user","content":[{"text":"third"}]}`,
		`{"$rewindTo":"u2"}`)

	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	if len(p.Prompts) != 1 || p.Prompts[0].Text != "first" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
}

func TestAnEditedPromptIsCountedAsItNowReads(t *testing.T) {
	log := journal(conversationStart,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":[{"text":"first"}]}`,
		`{"$patch":{"updates":[{"id":"u1","content":[{"text":"first, edited"}]}]}}`)

	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	if len(p.Prompts) != 1 || p.Prompts[0].Text != "first, edited" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
}

func TestAnEditedToolResultIsMeasuredAsItNowStands(t *testing.T) {
	log := journal(conversationStart, answerToReplace,
		`{"$patch":{"id":"a1","toolCalls":[{"id":"c1","result":[{"big":"0123456789"}]}]}}`)

	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	if len(p.Tools) != 1 || p.Tools[0].ResultChars != len(`[{"big":"0123456789"}]`) {
		t.Errorf("tools = %+v", p.Tools)
	}
}

func TestMessagesAreCountedInTheOrderTheJournalGivesThem(t *testing.T) {
	log := journal(conversationStart, answerToReplace,
		`{"id":"a2","timestamp":"2026-02-02T10:00:04.000Z","type":"gemini","content":"y","model":"m","tokens":{"input":20,"output":2,"cached":0,"thoughts":0,"tool":0,"total":22}}`,
		`{"$patch":{"orderIds":["a2","a1"]}}`)

	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-2.jsonl", log)

	if len(p.Requests) != 2 || p.Requests[0].Key != "a2" || p.Requests[1].Key != "a1" {
		t.Errorf("requests = %+v", p.Requests)
	}
}

func TestRewindingToAMessageThatIsNotThereEmptiesTheConversation(t *testing.T) {
	log := journal(
		`{"sessionId":"s-3","projectHash":"h"}`,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":"hello"}`,
		`{"$rewindTo":"nowhere"}`,
		`{"id":"u2","timestamp":"2026-02-02T10:00:02.000Z","type":"user","content":"again"}`,
	)
	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-3.jsonl", log)
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "again" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
}

func TestARestatedConversationReplacesTheOneBeforeItAndKeepsWhatItSaysOfItself(t *testing.T) {
	log := journal(
		`{"sessionId":"s-4","projectHash":"h","startTime":"2026-02-02T10:00:00.000Z"}`,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":"old"}`,
		`{"$set":{"messages":[{"id":"u9","timestamp":"2026-02-02T10:00:02.000Z","type":"user","content":"new"}],"kind":"subagent"}}`,
	)
	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-4.jsonl", log)
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "new" || p.Session.AgentType != domain.Subagent || p.Session.ID.String() != "gemini:s-4" {
		t.Errorf("transcript = %+v", p)
	}
}

func TestGeminiSubagentJournalBelongsToTheSessionOfItsDirectory(t *testing.T) {
	// Given a journal stored in the directory of its parent session
	log := journal(`{"sessionId":"child-1","projectHash":"h","kind":"subagent"}`,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":"look into it"}`)

	// When it is read
	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/parent-7/child-1.jsonl", log)

	// Then it is a subagent of that session
	if p.Session.ID.String() != "gemini:parent-7/child-1" || p.Session.ParentID.String() != "gemini:parent-7" || p.Session.AgentType != domain.Subagent {
		t.Errorf("session = %+v", p.Session)
	}
}

func TestGeminiFolderComesFromTheProjectRootFileWhenItIsUsable(t *testing.T) {
	for name, tc := range map[string]struct{ root, want string }{
		"absolute path": {"/work/demo-app\n", "/work/demo-app"},
		"relative path": {"demo-app", ""},
		"control chars": {"/work/\x1b[2Jdemo", ""},
		"oversized":     {"/" + strings.Repeat("a", maxProjectRootBytes), ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Given a project directory with a .project_root file
			chats := filepath.Join(t.TempDir(), "proj", "chats")
			if err := os.MkdirAll(chats, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(chats), ".project_root"), []byte(tc.root), 0o600); err != nil {
				t.Fatal(err)
			}

			// When a conversation of that project is read
			p := parse(t, ParseGemini, filepath.Join(chats, "session-1.json"), `{"sessionId":"s-5","projectHash":"h","messages":[]}`)

			// Then the folder is the one it names, or unknown when what it holds is not an absolute printable path
			if p.Session.Folder != tc.want {
				t.Errorf("folder = %q, want %q", p.Session.Folder, tc.want)
			}
		})
	}
}

func TestAProjectRootFileThatIsALinkIsNotFollowed(t *testing.T) {
	// Given a project directory whose .project_root is a link to a file that names a folder
	dir := t.TempDir()
	chats := filepath.Join(dir, "proj", "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("/work/demo-app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "proj", ".project_root")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}

	// When a conversation of that project is read
	p := parse(t, ParseGemini, filepath.Join(chats, "session-1.json"), `{"sessionId":"s-5","projectHash":"h","messages":[]}`)

	// Then the folder is unknown
	if p.Session.Folder != "" {
		t.Errorf("folder = %q", p.Session.Folder)
	}
}

func TestAProjectRootFileCreatedAfterAConversationWasListedMakesItBeReadAgain(t *testing.T) {
	// Given a conversation that was listed, in a project that has no .project_root file yet
	root := t.TempDir()
	chats := filepath.Join(root, "proj", "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chats, "session-1.json"), []byte(`{"sessionId":"s-5","projectHash":"h","messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := NewFileSessionLogs([]Source{GeminiSource(root)})
	listed := func() application.LogFile {
		files, problems, err := logs.List()
		if err != nil || len(problems) > 0 || len(files) != 1 {
			t.Fatalf("list: %v, %v, %v", files, problems, err)
		}
		return files[0]
	}
	before := listed()

	// When the project root file is written, the conversation itself unchanged
	if err := os.WriteFile(filepath.Join(root, "proj", ".project_root"), []byte("/work/demo-app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := listed()

	// Then the conversation shows as changed, and is read with its folder
	if after.Revision == before.Revision {
		t.Fatal("the conversation shows as unchanged")
	}
	if p, err := logs.Read(after); err != nil || p.Session.Folder != "/work/demo-app" {
		t.Errorf("folder = %q, %v", p.Session.Folder, err)
	}
}

func TestGeminiConversationsWithHostileContentAreBoundedAndCounted(t *testing.T) {
	path := "/h/.gemini/tmp/demo/chats/session-6.json"
	// Given messages whose usage is impossible, whose type is wrong, and a name with control characters
	doc := `{"sessionId":"s-6","projectHash":"h","messages":[
{"id":"a","timestamp":"2026-02-02T10:00:01.000Z","type":"gemini","model":"m","tokens":{"input":-5,"output":1,"cached":0,"thoughts":0,"tool":0}},
{"id":"b","timestamp":"2026-02-02T10:00:02.000Z","type":"gemini","model":"m","tokens":{"input":10,"output":1,"cached":11,"thoughts":0,"tool":0}},
{"id":"c","timestamp":"2026-02-02T10:00:03.000Z","type":"gemini","model":"m","tokens":{"input":"many"}},
{"id":"d","timestamp":"2026-02-02T10:00:04.000Z","type":"gemini","model":"m","tokens":{"input":99999999999,"output":1,"cached":0,"thoughts":0,"tool":0}},
{"id":"e","timestamp":"not a time","type":"gemini","model":"m","tokens":{"input":3,"output":1,"cached":0,"thoughts":0,"tool":0},"toolCalls":[{"id":"t","name":"x\u001b[2J","args":null,"result":null,"status":"success"}]}
]}`

	// When the conversation is read
	p := parse(t, ParseGemini, path, doc)

	// Then the impossible usages are dropped and counted, the others are kept
	if len(p.Requests) != 1 || p.Requests[0].Key != "e" || p.SkippedLines != 4 {
		t.Errorf("requests = %+v, skipped %d", p.Requests, p.SkippedLines)
	}
}

func TestGeminiRefusesAConversationItCannotName(t *testing.T) {
	for name, doc := range map[string]string{
		"no id":                        `{"projectHash":"h","messages":[]}`,
		"hostile id":                   `{"sessionId":"../../etc/passwd","projectHash":"h","messages":[]}`,
		"control id":                   `{"sessionId":"a\u001bb","projectHash":"h","messages":[]}`,
		"not an object":                `[1,2]`,
		"truncated":                    `{"sessionId":"s","messages":[{"id":"a"`,
		"cut after the messages":       `{"sessionId":"s","projectHash":"h","messages":[]`,
		"cut after a value":            `{"sessionId":"s","projectHash":"h","messages":[],"kind":"main"`,
		"something after the document": `{"sessionId":"s","projectHash":"h","messages":[]} {}`,
		"messages a list":              `{"sessionId":"s","messages":7}`,
	} {
		_, err := ParseGemini("/h/.gemini/tmp/demo/chats/session-x.json", strings.NewReader(doc))
		var syntax *json.SyntaxError
		if !errors.Is(err, ErrConversationUnreadable) || errors.As(err, &syntax) {
			t.Errorf("%s: err = %v, want ErrConversationUnreadable and nothing of the decoder's", name, err)
		}
	}
}

func TestGeminiJournalSkipsMalformedLinesAndCountsThem(t *testing.T) {
	log := journal(
		`{"sessionId":"s-7","projectHash":"h"}`,
		`{"id":"u1","timestamp":"2026-02-02T10:00:01.000Z","type":"user","content":"kept"}`,
		`this is not json`,
		`{"id":"u2","type":`,
		`{"$patch":7}`,
		`{"$set":{"messages":"nope"}}`,
	)
	p := parse(t, ParseGemini, "/h/.gemini/tmp/demo/chats/session-7.jsonl", log)
	if len(p.Prompts) != 1 || p.SkippedLines != 4 {
		t.Errorf("prompts = %+v, skipped %d", p.Prompts, p.SkippedLines)
	}
}

func TestGeminiConversationLargerThanTheBoundIsRefused(t *testing.T) {
	defer func(n int64) { maxGeminiBytes = n }(maxGeminiBytes)
	maxGeminiBytes = 200
	for path, doc := range map[string]string{
		"/h/.gemini/tmp/p/chats/session-1.json":  geminiDocument,
		"/h/.gemini/tmp/p/chats/session-1.jsonl": journal(`{"sessionId":"s","projectHash":"h"}`, geminiDocument),
	} {
		if _, err := ParseGemini(path, strings.NewReader(doc)); !errors.Is(err, ErrLogTooLarge) {
			t.Errorf("%s: err = %v, want ErrLogTooLarge", path, err)
		}
	}
}

func TestOnlyConversationFilesInAChatsDirectoryAreGeminiLogs(t *testing.T) {
	for path, want := range map[string]bool{
		"/h/.gemini/tmp/p/chats/session-1.json":        true,
		"/h/.gemini/tmp/p/chats/session-1.jsonl":       true,
		"/h/.gemini/tmp/p/chats/parent/child.jsonl":    true,
		"/h/.gemini/tmp/p/logs.json":                   false,
		"/h/.gemini/tmp/p/tool-outputs/x.json":         false,
		"/h/.gemini/tmp/p/chats/session-1.jsonl.tmp-9": false,
		"/h/.gemini/tmp/p/chats/a/b/c.json":            false,
	} {
		if got := IsGeminiConversation(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}
