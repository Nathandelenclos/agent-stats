package logfiles

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestCopilotKeepsActivityWithoutOptionalFields(t *testing.T) {
	log := `{"type":"user.message","data":{"content":"Check this"}}
{"type":"user.message","data":{"content":"Generated continuation","isAutopilotContinuation":true}}
{"type":"user.message","data":{"content":"Generated kickoff","agentAuthored":true}}
{"type":"assistant.message","data":{"requestId":"one","outputTokens":2}}
{"type":"assistant.message","data":{"requestId":"one","serviceRequestId":"service","model":"effective"}}
{"type":"assistant.message","data":{"requestId":"old","model":"old-model"}}
{"type":"session.resume","data":{"selectedModel":"resumed-model"}}
{"type":"assistant.message","agentId":"child","data":{"serviceRequestId":"child","outputTokens":1}}
{"type":"assistant.message","data":{"serviceRequestId":"root"}}
{"type":"assistant.message","ephemeral":true,"data":{"serviceRequestId":"transient","outputTokens":90}}
{"type":"assistant.message","data":{"messageId":"synthetic","content":"Automatic context"}}
{"type":"tool.execution_start","data":{"toolCallId":"one","toolName":"list"}}
{"type":"tool.execution_complete","data":{"toolCallId":"one"}}
{"type":"tool.execution_start","data":{"toolCallId":"another","toolName":"view"}}
{"type":"tool.execution_complete","data":{"toolCallId":"orphan","success":false}}
{"type":"subagent.started","agentId":"child","data":{"toolCallId":"another","agentName":"example"}}
{"type":"future.event","data":{"model":17}}
{"type":"assistant.message","data":{"requestId":"invalid","outputTokens":-1}}
{`
	tr := parse(t, ParseCopilot, "session-state/local-id/events.jsonl", log)
	if tr.Session.ID.String() != "copilot:local-id" || tr.Session.AgentType != domain.MainAgent ||
		!tr.Session.ParentID.IsZero() || tr.Session.Folder != "" || !tr.Session.Start.IsZero() {
		t.Errorf("session = %+v", tr.Session)
	}
	if len(tr.Prompts) != 1 || tr.Prompts[0].Key == "" || tr.Prompts[0].Text != "Check this" || tr.Session.Title != "Check this" {
		t.Errorf("prompts = %+v, title %q", tr.Prompts, tr.Session.Title)
	}
	if len(tr.Requests) != 4 {
		t.Fatalf("requests = %+v", tr.Requests)
	}
	first, second := tr.Requests[0], tr.Requests[1]
	if first.Model != "effective" || first.Tokens.Counts() != (kernel.TokenCounts{Output: 2}) ||
		second.Model != "old-model" || second.Tokens.Counts() != (kernel.TokenCounts{}) ||
		first.Duration.Known() || first.TTFT.Known() || !first.LoggedCost.IsZero() ||
		tr.Requests[2].Model != "" || tr.Requests[2].Tokens.Counts().Output != 1 ||
		tr.Requests[3].Model != "resumed-model" {
		t.Errorf("requests = %+v", tr.Requests)
	}
	if len(tr.Tools) != 2 || tr.Tools[0].Name != "list" || tr.Tools[0].IsError || tr.Tools[0].Duration.Known() ||
		tr.Tools[1].Name != "view" || tr.Tools[1].Duration.Known() {
		t.Errorf("tools = %+v", tr.Tools)
	}
	if tr.SkippedLines != 2 {
		t.Errorf("skipped %d lines, want 2", tr.SkippedLines)
	}
}

func TestCopilotDiscoveryOnlyReadsOneEventLogPerSessionDirectory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "profile")
	t.Setenv("COPILOT_HOME", root)
	var src Source
	for _, candidate := range DefaultSources(home) {
		if candidate.Harness == kernel.Copilot {
			src = candidate
			break
		}
	}
	if src.Root != filepath.Join(root, "session-state") {
		t.Fatalf("Copilot source = %q", src.Root)
	}
	for _, name := range []string{"session/events.jsonl", "session/drafts/events.jsonl", "session/session.jsonl", "events.jsonl"} {
		path := filepath.Join(src.Root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"type":"session.start"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, problems, err := NewFileSessionLogs([]Source{src}).List()
	if err != nil || len(problems) != 0 || len(files) != 1 || files[0].Path != filepath.Join(src.Root, "session", "events.jsonl") {
		t.Fatalf("files = %v, problems = %v, err = %v", files, problems, err)
	}
}

func TestCopilotRefusesUnreadableJournals(t *testing.T) {
	path := "session-state/local-id/events.jsonl"
	if _, err := ParseCopilot(path, strings.NewReader("{bad json")); err == nil {
		t.Error("unreadable journal accepted")
	}
	if _, err := ParseCopilot(path, io.MultiReader(strings.NewReader(`{"type":"session.start"}`+"\n"), &capped{})); !errors.Is(err, ErrLogTooLarge) {
		t.Errorf("read error = %v, want ErrLogTooLarge", err)
	}
}
