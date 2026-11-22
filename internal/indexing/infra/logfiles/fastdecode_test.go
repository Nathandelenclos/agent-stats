package logfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// parseBoth parses a log with the scanner on, then with encoding/json alone.
func parseBoth(t *testing.T, p Parser, path, log string) (fast, slow domain.Transcript) {
	t.Helper()
	defer func(on bool) { fastDecode = on }(fastDecode)
	fastDecode = true
	fast = parse(t, p, path, log)
	fastDecode = false
	slow = parse(t, p, path, log)
	return fast, slow
}

// sameTranscript fails when the transcripts differ, saying where but never what: logs hold the prompts and
// paths of their authors, and a failure on real logs must not print them.
func sameTranscript(t *testing.T, what string, fast, slow domain.Transcript) {
	t.Helper()
	if reflect.DeepEqual(fast, slow) {
		return
	}
	t.Errorf("%s: the scanner and encoding/json read different transcripts: %s", what, firstDifference(fast, slow))
}

func firstDifference(fast, slow domain.Transcript) string {
	switch {
	case !reflect.DeepEqual(fast.Session, slow.Session):
		return "session"
	case fast.SkippedLines != slow.SkippedLines:
		return fmt.Sprintf("skipped lines (%d, %d)", fast.SkippedLines, slow.SkippedLines)
	}
	for _, part := range []struct {
		name string
		a, b any
	}{{"requests", fast.Requests, slow.Requests}, {"tool calls", fast.Tools, slow.Tools}, {"prompts", fast.Prompts, slow.Prompts}} {
		a, b := reflect.ValueOf(part.a), reflect.ValueOf(part.b)
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s (%d, %d)", part.name, a.Len(), b.Len())
		}
		for i := range a.Len() {
			if !reflect.DeepEqual(a.Index(i).Interface(), b.Index(i).Interface()) {
				return fmt.Sprintf("%s, number %d", part.name, i+1)
			}
		}
	}
	return "unknown"
}

// compareOddLines parses prefix plus each odd line, once per line, with and without the scanner.
// Odd lines are ones the scanner must either read as encoding/json does or hand back to it; at least
// minRefused of them must be handed back, or the cases no longer test that fallback.
func compareOddLines(t *testing.T, p Parser, path, prefix string, lines []string, minRefused int64) {
	t.Helper()
	before := scanRefused.Load()
	for _, line := range lines {
		fast, slow := parseBoth(t, p, path, prefix+line+"\n")
		sameTranscript(t, line, fast, slow)
	}
	if refused := scanRefused.Load() - before; refused < minRefused {
		t.Errorf("the scanner handed back only %d odd lines: the cases no longer test the fallback", refused)
	}
}

var oddPiLines = []string{
	`{"type":"session","cwd":"/a","title":"T"}`,
	`{"type":"session","type":"title","title":"dup"}`,                               // duplicate key
	`{"Type":"title","TITLE":"folded"}`,                                             // keys matching by case
	`{"type":"title","title":"escaped key"}`,                                        // key with an escape
	`{"type":"title","title":null}`,                                                 // null for a string
	`{"type":"title","title":5}`,                                                    // wrong type: encoding/json refuses the line
	`{"type":"title","title":"é é \n \"q\" 😀"}`,                                     // decoded strings
	"{\"type\":\"title\",\"title\":\"bad \xff utf8\"}",                              // invalid UTF-8
	`{"type":"message","id":"a","timestamp":"2026-01-01T10:00:00Z","message":null}`, // null message
	`{"type":"message","message":[]}`,                                               // wrong type message
	`{"type":"message","message":{"role":"user","content":"just text"}}`,            // content is not a list
	`{"type":"message","message":{"role":"user","content":null}}`,
	`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"a"},1,{"type":"text","text":"b"}]}}`,
	`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"a","text":"b"}]}}`,
	`{"type":"message","message":{"role":"user","content":[{"type":"text","Text":"folded"}]}}`,
	`{"type":"message","id":"x","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"mcp__s__t","arguments":{"a":[1,2,3]}}],"usage":{"input":1,"output":2,"cost":{"total":0.5}}}}`,
	`{"type":"message","id":"y","message":{"role":"assistant","usage":{"input":1.5}}}`, // float for an integer
	`{"type":"message","id":"y","message":{"role":"assistant","usage":{"input":-1}}}`,  // impossible value
	`{"type":"message","id":"y","message":{"role":"assistant","usage":{"input":1e2}}}`, // exponent for an integer
	`{"type":"message","id":"y","message":{"role":"assistant","duration":12.5,"ttft":1e1}}`,
	`{"type":"message","id":"y","message":{"role":"assistant","duration":"slow"}}`,
	`{"type":"message","message":{"role":"toolResult","toolCallId":"c1","isError":true,"content":[{"type":"text","text":"output"}]}}`,
	`{"type":"message","message":{"role":"toolResult","toolCallId":"c1","isError":1}}`,
	`{"type":"model_change","model":"p/m"}`,
	`{"type":"model_change","model":"p/"}`,
	`{"type":"model_change","provider":"p","modelId":"m"}`,
	`not json`, `[1,2]`, `{"type":"title"} trailing`, `{"type":"title",}`, `{"type":`,
	"\t {\"type\":\"title\",\"title\":\"spaces\"}\t ",
}

func TestScannerReadsPiLinesLikeEncodingJSON(t *testing.T) {
	// A call and a user message give the odd lines something to attach to.
	prefix := `{"type":"session","cwd":"/w"}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"assistant","model":"m","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"p":1}}],"usage":{"input":3}}}` + "\n"
	compareOddLines(t, ParsePi(kernel.Pi), "/x/s/2026-01-01T10-00-00-000Z_11111111-2222-3333-4444-555555555555.jsonl", prefix, oddPiLines, 15)
}

var oddClaudeLines = []string{
	`{"type":"custom-title","customTitle":"T"}`,
	`{"type":"custom-title","customTitle":"T","customTitle":"dup"}`,
	`{"TYPE":"custom-title","customtitle":"folded"}`,
	`{"type":"user","uuid":"u1","message":{"content":"plain prompt é é"}}`,
	`{"type":"user","uuid":"u2","message":{"content":null}}`,
	`{"type":"user","uuid":"u3","message":{"content":5}}`,
	`{"type":"user","uuid":"u4","message":{"content":{"a":1}}}`,
	`{"type":"user","uuid":"u5","message":{"content":[]}}`,
	`{"type":"user","uuid":"u6","message":{"content":[{"type":"text","text":"block prompt"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"x"}],"is_error":true}]}}`,
	`{"type":"user","uuid":"u7","message":{"content":[{"type":"text","text":"a","TEXT":"b"}]}}`,
	`{"type":"user","uuid":"u8","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":null}]}}`,
	`{"type":"user","uuid":"u9","isMeta":"yes","message":{"content":"x"}}`,
	`{"type":"user","uuid":"u0","message":{"content":[1,{"type":"text","text":"after a number"}]}}`,
	`{"type":"assistant","requestRef":"r","timestamp":"2026-01-01T10:00:03Z","message":{"id":"m2","model":"claude-x","stop_reason":"end_turn","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"cmd":"ls"}}],"usage":{"input_tokens":1,"output_tokens":2,"cache_creation":{"ephemeral_1h_input_tokens":1},"output_tokens_details":{"thinking_tokens":4}}}}`,
	`{"type":"assistant","message":{"id":"m3","model":"claude-x","content":"a string","usage":{"input_tokens":1.5}}}`,
	`{"type":"assistant","message":{"id":"m3","model":"claude-x","usage":{"input_tokens":-5}}}`,
	`{"type":"assistant","message":{"id":"m3","model":"claude-x","usage":null}}`,
	`{"type":"assistant","isApiErrorMessage":true,"message":{"id":"e2","model":"<synthetic>","content":[{"type":"text","text":"overloaded ☃"}]}}`,
	`{"type":"assistant","message":{"id":"e3","model":"<synthetic>","content":"oops"}}`,
	`{"type":"api-request","id":"rq2","timestamp":"2026-01-01T10:00:02Z"}`,
	`{"type":"summary","message":{"content":[{"type":"text","text":"ignored"}]},"leafUuid":"x"}`,
	`garbage`, `{"type":"user"`, `{"type":"user","message":{"content":"x"}} {}`,
}

func TestScannerReadsClaudeLinesLikeEncodingJSON(t *testing.T) {
	prefix := `{"type":"user","uuid":"u0","timestamp":"2026-01-01T10:00:00Z","cwd":"/r","message":{"role":"user","content":"start"}}` + "\n" +
		`{"type":"api-request","id":"rq1","timestamp":"2026-01-01T10:00:01Z"}` + "\n" +
		`{"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:02Z","message":{"id":"m1","model":"claude-x","content":[{"type":"tool_use","id":"t1","name":"mcp__a__b","input":{"x":1}}],"usage":{"input_tokens":1}}}` + "\n"
	compareOddLines(t, ParseClaude, "/x/projects/-r/abc.jsonl", prefix, oddClaudeLines, 10)
}

var oddCodexLines = []string{
	`{"type":"session_meta","timestamp":"2026-01-01T10:00:00Z","payload":{"id":"s1","cwd":"/w","thread_source":"subagent","forked_from_id":"p1"}}`,
	`{"type":"session_meta","payload":{"id":"s1","id":"s2"}}`,
	`{"type":"session_meta","Payload":{"ID":"folded"}}`,
	`{"type":"session_meta","payload":null}`,
	`{"type":"session_meta","payload":"text"}`,
	`{"type":"turn_context","payload":{"model":"gpt-x"}}`,
	`{"type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3}}}`,
	`{"type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":1,"cached_input_tokens":4}}}`,
	`{"type":"response_item","payload":{"type":"function_call","name":"search","call_id":"c1","arguments":"{\"q\":1}"}}`,
	`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"result text"}}`,
	`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-y"}}}`,
	`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5},"last_token_usage":{"input_tokens":5,"output_tokens":1}}}}`,
	`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":null,"last_token_usage":{}}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","command":"ls","aggregated_output":"out é \n \"q\" 😀 \ud800","exit_code":2,"duration":{"secs":1,"nanos":500000000}}}}`,
	"{\"type\":\"event_msg\",\"payload\":{\"type\":\"item_completed\",\"item\":{\"type\":\"CommandExecution\",\"id\":\"i1\",\"aggregated_output\":\"bad \xff\"}}}",
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i2","aggregated_output":5}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i3","aggregated_output":null,"exit_code":null}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i4","exit_code":1.5}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i5","server":"s","tool":"t","arguments":{"a":1},"result":{"content":[1,2,3]},"error":null,"status":"failed"}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i6","error":{"message":"boom"}}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i7","content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b é"}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i8","content":[{"type":"text","text":"x","text":"y"}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"i9","changes":[{"p":"a"}]}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"ImageView","id":"i10","path":"/p"}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Extension","id":"i11","kind":"k","query":"q"}}}`,
	`{"type":"event_msg","payload":{"type":"item_completed","item":null}}`,
	`garbage`, `{"type":"event_msg","payload":{}} x`,
}

func TestScannerReadsCodexLinesLikeEncodingJSON(t *testing.T) {
	prefix := `{"type":"session_meta","timestamp":"2026-01-01T10:00:00Z","payload":{"id":"s0","cwd":"/w"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-x"}}` + "\n"
	compareOddLines(t, ParseCodex, "/x/sessions/rollout-1.jsonl", prefix, oddCodexLines, 10)
}

// TestScannerReadsFixtureLogsLikeEncodingJSON compares every parser's transcripts on the fixture logs and,
// when AGENT_STATS_CORPUS names a home directory, on the real logs found under it:
//
//	AGENT_STATS_CORPUS=$HOME go test ./internal/indexing/infra/logfiles -run RealLogs -v
func TestScannerReadsFixtureLogsLikeEncodingJSON(t *testing.T) {
	compareHome(t, "../../../../testdata/home")
}

func TestScannerReadsRealLogsLikeEncodingJSON(t *testing.T) {
	home := os.Getenv("AGENT_STATS_CORPUS")
	if home == "" {
		t.Skip("set AGENT_STATS_CORPUS to a home directory to compare on real logs")
	}
	compareHome(t, home)
}

func compareHome(t *testing.T, home string) {
	t.Helper()
	defer func(on bool) { fastDecode = on }(fastDecode)
	files, refused := 0, int64(0)
	for _, src := range DefaultSources(home) {
		if src.Database != nil || src.Harness == kernel.Gemini || src.Harness == kernel.Grok { // these parsers do not use the fast scanner
			continue
		}
		filepath.WalkDir(src.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !src.Match(path) {
				return nil
			}
			files++
			fastDecode = true
			before := scanRefused.Load()
			fast, ferr := ParseFile(path, src.Parse)
			refused += scanRefused.Load() - before
			fastDecode = false
			slow, serr := ParseFile(path, src.Parse)
			if (ferr == nil) != (serr == nil) {
				t.Errorf("%s: errors differ: %v / %v", path, ferr, serr)
			}
			sameTranscript(t, strings.TrimPrefix(path, home), fast, slow)
			return nil
		})
	}
	t.Logf("%d logs compared, %d lines handed back to encoding/json", files, refused)
	if files == 0 {
		t.Fatal("no log found under " + home)
	}
}

func TestDifferenceReportSaysWhereButNeverWhatTheLogSays(t *testing.T) {
	a := domain.Transcript{Prompts: []domain.Prompt{{Key: "1", Text: "hunter2 is my password"}}}
	b := domain.Transcript{Prompts: []domain.Prompt{{Key: "1", Text: "other text"}}}
	got := firstDifference(a, b)
	if got != "prompts, number 1" || strings.Contains(got, "hunter2") {
		t.Errorf("difference = %q", got)
	}
	if got := firstDifference(domain.Transcript{SkippedLines: 1}, domain.Transcript{}); got != "skipped lines (1, 0)" {
		t.Errorf("difference = %q", got)
	}
}
