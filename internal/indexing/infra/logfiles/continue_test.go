package logfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestContinueReadsResponseUsageAndToolState(t *testing.T) {
	transcript, err := ParseFile("testdata/continue/cn-demo.json", ParseContinue)
	if err != nil {
		t.Fatal(err)
	}
	s := transcript.Session
	if s.ID.String() != "continue:cn-demo" || s.Harness != kernel.Continue || s.AgentType != domain.MainAgent ||
		s.Folder != "/demo" || s.Title != "Demo session" || !s.Start.IsZero() || !s.End.IsZero() {
		t.Fatalf("session = %+v", s)
	}
	if len(transcript.Prompts) != 1 || transcript.Prompts[0].Text != "List the examples." ||
		len(transcript.Requests) != 2 || len(transcript.Tools) != 1 || transcript.SkippedLines != 0 {
		t.Fatalf("transcript = %+v", transcript)
	}
	first, second := transcript.Requests[0], transcript.Requests[1]
	if first.Model != "demo-a" || first.LoggedCost.USD() != 0.02 || !first.At.IsZero() ||
		first.Tokens.Counts() != (kernel.TokenCounts{Input: 60, Output: 10, CacheRead: 40, Reasoning: 3}) {
		t.Errorf("first request = %+v", first)
	}
	if second.Model != "demo-b" || !second.LoggedCost.IsZero() ||
		second.Tokens.Counts() != (kernel.TokenCounts{Input: 20, Output: 5, CacheRead: 30, CacheWrite: 10}) {
		t.Errorf("second request = %+v", second)
	}
	if call := transcript.Tools[0]; call.Server != "demo" || call.Name != "lookup" ||
		call.ArgsChars != 2 || call.ResultChars != 12 || !call.IsError || !call.At.IsZero() {
		t.Errorf("tool = %+v", call)
	}
}

func TestContinueKeepsPromptsWhenUsageIsMissingOrInvalid(t *testing.T) {
	const snapshot = `{"sessionId":"cn-demo","workspaceDirectory":"relative","history":[
		{"message":{"role":"user","content":"List the examples."}},
		{"message":{"role":"assistant","usage":null}},
		{"message":{"role":"assistant","usage":{"completion_tokens":9}}},
		{"message":{"role":"assistant","usage":{"prompt_tokens":3,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":4}}}},
		{"message":{"role":"assistant","usage":{"prompt_tokens":3,"completion_tokens":-1}}},
		{"message":{"role":"assistant","usage":{"prompt_tokens":3,"completion_tokens":2,"cost_cents":-1}}},
		{"message":{"role":"assistant","usage":{"prompt_tokens":2147483649,"completion_tokens":2}}}
	]}`
	transcript, err := ParseContinue("cn-demo.json", strings.NewReader(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Prompts) != 1 || len(transcript.Requests) != 0 || transcript.SkippedLines != 5 ||
		transcript.Session.Title != "List the examples." || transcript.Session.Folder != "" {
		t.Fatalf("transcript = %+v", transcript)
	}
}

func TestContinueRefusesInvalidSnapshotsAndLinks(t *testing.T) {
	for _, tc := range []struct{ path, raw string }{
		{"sessions.json", `[]`},
		{"cn-demo.json", `{"sessionId":"other","history":[]}`},
		{"cn-demo.json", `{"sessionId":"../cn-demo","history":[]}`},
		{"cn-demo.json", `{"sessionId":"cn-demo","history":null}`},
		{"cn-demo.json", `{"sessionId":"cn-demo","history":[`},
		{"cn-demo.json", `{"sessionId":"cn-demo","history":[]}{}`},
	} {
		if _, err := ParseContinue(tc.path, strings.NewReader(tc.raw)); !errors.Is(err, ErrConversationUnreadable) {
			t.Errorf("%s (%s): %v", tc.path, tc.raw, err)
		}
	}
	oldLimit := maxLineBytes
	maxLineBytes = 128
	t.Cleanup(func() { maxLineBytes = oldLimit })
	if _, err := ParseContinue("cn-demo.json", strings.NewReader(strings.Repeat(" ", 129))); !errors.Is(err, ErrLogTooLarge) {
		t.Errorf("oversized snapshot: %v", err)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "external.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "sessions")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "cn-demo.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	logs := NewFileSessionLogs([]Source{continueSource(root)})
	files, problems, err := logs.List()
	if err != nil || len(problems) != 0 || len(files) != 1 {
		t.Fatalf("discovery: %v, %v, %v", files, problems, err)
	}
	if _, err := logs.Read(files[0]); !errors.Is(err, ErrNotRegular) {
		t.Errorf("linked snapshot: %v", err)
	}
}
