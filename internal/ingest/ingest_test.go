package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(t *testing.T, p Parser, path, log string) *Parsed {
	t.Helper()
	out, err := p(path, strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClaude(t *testing.T) {
	log := `{"type":"custom-title","customTitle":"Fix login"}
{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00Z","cwd":"/repo","message":{"role":"user","content":"Fix the login bug"}}
{"type":"user","uuid":"u0","isMeta":true,"timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":"injected"}}
{"type":"user","uuid":"u2","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"api-request","id":"rq1","timestamp":"2026-01-01T10:00:01Z"}
{"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:02Z","message":{"id":"m1","model":"claude-x","content":[{"type":"thinking","thinking":"…"}],"usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_1h_input_tokens":20}}}}
{"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:04Z","message":{"id":"m1","model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"mcp__grace__review","input":{"a":1}}],"usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
{"type":"user","uuid":"u3","timestamp":"2026-01-01T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"boom","is_error":true}]}}
{"type":"assistant","isApiErrorMessage":true,"timestamp":"2026-01-01T10:00:07Z","message":{"id":"e1","model":"<synthetic>","content":[{"type":"text","text":"overloaded"}]}}
`
	p := parse(t, ParseClaude, "/x/projects/-repo/abc.jsonl", log)
	if p.Session.ID != "claude:abc" || p.Session.Title != "Fix login" || p.Session.Folder != "/repo" {
		t.Errorf("session = %+v", p.Session)
	}
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Fix the login bug" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
	if len(p.Requests) != 2 {
		t.Fatalf("want 1 request (deduplicated by message id) + 1 error, got %+v", p.Requests)
	}
	r := p.Requests[0]
	if r.Input != 5 || r.Output != 7 || r.CacheRead != 100 || r.CacheWrite != 20 || r.StopReason != "tool_use" {
		t.Errorf("request = %+v", r)
	}
	if r.TTFTMs != 1000 || r.DurationMs != 3000 {
		t.Errorf("ttft %v duration %v, want 1000 and 3000", r.TTFTMs, r.DurationMs)
	}
	if e := p.Requests[1]; e.Error != "overloaded" || e.Model != "claude-x" {
		t.Errorf("error request = %+v", e)
	}
	if len(p.Tools) != 1 {
		t.Fatalf("tools = %+v", p.Tools)
	}
	if tc := p.Tools[0]; tc.Name != "review" || tc.Server != "grace" || !tc.IsError || tc.ResultChars != len(`"boom"`) || tc.DurationMs != 2000 {
		t.Errorf("tool = %+v", tc)
	}
}

func TestClaudeSubagent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sess1", "subagents")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "agent-a1.meta.json"), []byte(`{"agentType":"Explore"}`), 0o644)
	p := parse(t, ParseClaude, filepath.Join(dir, "agent-a1.jsonl"), "")
	if p.Session.ID != "claude:sess1/agent-a1" || p.Session.ParentID != "claude:sess1" || p.Session.AgentType != "Explore" {
		t.Errorf("session = %+v", p.Session)
	}
}

func TestCodex(t *testing.T) {
	log := `{"timestamp":"2026-01-01T10:00:00Z","type":"session_meta","payload":{"id":"fork","forked_from_id":"orig","cwd":"/repo"}}
{"timestamp":"2026-01-01T10:00:00Z","type":"session_meta","payload":{"id":"orig","cwd":"/elsewhere"}}
{"timestamp":"2026-01-01T10:00:00Z","type":"turn_context","payload":{"model":"gpt-x"}}
{"timestamp":"2026-01-01T10:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i1","content":[{"type":"text","text":"Run the tests"}]}}}
{"timestamp":"2026-01-01T10:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c0","arguments":"{}"}}
{"timestamp":"2026-01-01T10:00:02Z","type":"response_item","payload":{"type":"function_call","name":"wait","call_id":"c1","arguments":"{\"ms\":1}"}}
{"timestamp":"2026-01-01T10:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"done"}}
{"timestamp":"2026-01-01T10:00:04Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"x1","command":["go","test"],"aggregated_output":"FAIL","exit_code":1,"duration":{"secs":1,"nanos":500000000}}}}
{"timestamp":"2026-01-01T10:00:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"x2","server":"github","tool":"get_issue","arguments":{"n":1},"result":{"ok":true},"status":"completed"}}}
{"timestamp":"2026-01-01T10:00:06Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":50,"reasoning_output_tokens":10}}}
{"timestamp":"2026-01-01T10:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000},"last_token_usage":{"input_tokens":1000}}}}
`
	p := parse(t, ParseCodex, "/x/rollout-1.jsonl", log)
	if p.Session.ID != "codex:fork" || p.Session.ParentID != "codex:orig" || p.Session.Folder != "/repo" {
		t.Errorf("session = %+v", p.Session)
	}
	if len(p.Requests) != 1 {
		t.Fatalf("token_count must be ignored when token_usage_record exists: %+v", p.Requests)
	}
	if r := p.Requests[0]; r.Model != "gpt-x" || r.Input != 200 || r.CacheRead != 800 || r.Output != 50 {
		t.Errorf("request = %+v", r)
	}
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Run the tests" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
	names := map[string]ToolCall{}
	for _, tc := range p.Tools {
		names[tc.Name] = tc
	}
	if len(p.Tools) != 3 || names["exec_command"].Name != "" {
		t.Errorf("want wait, shell and get_issue, got %+v", p.Tools)
	}
	if sh := names["shell"]; !sh.IsError || sh.DurationMs != 1500 || sh.ResultChars != 4 {
		t.Errorf("shell = %+v", sh)
	}
	if m := names["get_issue"]; m.Server != "github" || m.IsError {
		t.Errorf("mcp = %+v", m)
	}
	if w := names["wait"]; w.ResultChars != len(`"done"`) || w.DurationMs != 1000 {
		t.Errorf("wait = %+v", w)
	}
}

func TestCodexTokenCountFallback(t *testing.T) {
	log := `{"timestamp":"2026-01-01T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10},"last_token_usage":{"input_tokens":10}}}}
{"timestamp":"2026-01-01T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10},"last_token_usage":{"input_tokens":10}}}}
{"timestamp":"2026-01-01T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":25},"last_token_usage":{"input_tokens":15}}}}
`
	p := parse(t, ParseCodex, "/x/rollout-2.jsonl", log)
	if len(p.Requests) != 2 || p.Requests[1].Input != 15 {
		t.Errorf("want 2 requests (repeat skipped), got %+v", p.Requests)
	}
	if p.Session.ID != "codex:rollout-2" {
		t.Errorf("fallback id = %s", p.Session.ID)
	}
}

func TestPi(t *testing.T) {
	log := `{"type":"session","id":"s","timestamp":"2026-01-01T10:00:00Z","cwd":"/repo","title":"Refactor"}
{"type":"model_change","timestamp":"2026-01-01T10:00:00Z","model":"anthropic/claude-x"}
{"type":"message","id":"e1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"POURQUOI ÇA CASSE ENCORE ??"}]}}
{"type":"message","id":"e2","timestamp":"2026-01-01T10:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"mcp__ctx__docs","arguments":{"q":"x"}}],"usage":{"input":3,"output":4,"cacheRead":5,"cacheWrite":6,"cost":{"total":0.5}},"duration":1200,"ttft":300,"stopReason":"toolUse"}}
{"type":"message","id":"e3","timestamp":"2026-01-01T10:00:03Z","message":{"role":"toolResult","toolCallId":"t1","isError":true,"content":[{"type":"text","text":"no"}]}}
{"type":"message","id":"e4","timestamp":"2026-01-01T10:00:04Z","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"429"}}
`
	dir := filepath.Join("/x", "-repo", "2026-01-01T10-00-00-000Z_01a0b36b-c3dc-721d-97da-bbcb86adfadf", "Worker")
	p := parse(t, ParsePi("omp"), filepath.Join(dir, "Worker.Task.jsonl"), log)
	s := p.Session
	if s.ParentID != "omp:01a0b36b-c3dc-721d-97da-bbcb86adfadf" || s.AgentType != "subagent" || s.Title != "Refactor" || s.Folder != "/repo" {
		t.Errorf("session = %+v", s)
	}
	if len(p.Requests) != 2 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	if r := p.Requests[0]; r.Model != "claude-x" || r.Provider != "anthropic" || r.Cost != 0.5 || r.DurationMs != 1200 || r.TTFTMs != 300 || r.CacheWrite != 6 {
		t.Errorf("request = %+v", r)
	}
	if p.Requests[1].Error != "429" {
		t.Errorf("error = %+v", p.Requests[1])
	}
	if len(p.Tools) != 1 || p.Tools[0].Server != "ctx" || p.Tools[0].Name != "docs" || !p.Tools[0].IsError {
		t.Errorf("tools = %+v", p.Tools)
	}
	if sig := Analyze(p.Prompts[0].Text); sig.Yelling != 2 || sig.Anguish < 2 {
		t.Errorf("signals = %+v", sig)
	}
}

func TestAnalyzeIgnoresLoneAcronyms(t *testing.T) {
	if s := Analyze("Update the API and the JSON schema"); s.Yelling != 0 || s.Profanity != 0 || s.Blame != 0 {
		t.Errorf("signals = %+v", s)
	}
	if s := Analyze("putain, tu as cassé le build"); s.Profanity != 1 || s.Blame != 1 {
		t.Errorf("signals = %+v", s)
	}
}
