package logfiles

import "strings"

// The lines the equivalence tests feed to the parsers, written so that this file needs nothing from the
// parsers: it was also run against the parsers of commit 80bf4b1 to record testdata/legacy.golden.

// corpusHarness is one parser's corpus: a log to start from, hand-written lines that once differed from
// what encoding/json meant, and the objects in which generated lines repeat, null or retype each key.
type corpusHarness struct {
	name     string
	path     string
	prefix   string // lines that give the odd ones something to attach to
	seeds    []string
	contexts []mutationContext
}

// A mutationContext is an object a log line contains: wrap builds a whole line around its members, base
// holds the members of a valid one and keys the field names the parser reads there.
type mutationContext struct {
	wrap func(members string) string
	base string
	keys []string
}

// mutationValues are what a key is given in a generated line, valid or not for the field it names.
var mutationValues = []string{
	`null`, `"abc"`, "\"é\\n\"", `7`, `-3`, `1.5`, `1e2`, `true`, `false`, `[]`, `{}`,
	`[{"type":"text","text":"bbbb"}]`, `["a",1,null]`, `{"type":"text","text":"zz"}`,
}

// generated returns every line of the corpus that repeats a key, gives it another value, spells it with
// other letter case, once before the key's own member and once after it.
func (h corpusHarness) generated() []string {
	var lines []string
	for _, c := range h.contexts {
		for _, key := range c.keys {
			for _, spelled := range []string{key, strings.ToUpper(key)} {
				for _, v := range mutationValues {
					member := `"` + spelled + `":` + v
					lines = append(lines, c.wrap(member+","+c.base), c.wrap(c.base+","+member))
				}
			}
		}
	}
	return lines
}

func around(s string) func(string) string {
	return func(m string) string { return strings.Replace(s, "$", m, 1) }
}

var corpusHarnesses = []corpusHarness{
	{
		name: "claude",
		path: "/x/projects/-r/abc.jsonl",
		prefix: `{"type":"user","uuid":"u0","timestamp":"2026-01-01T10:00:00Z","cwd":"/r","message":{"role":"user","content":"start"}}` + "\n" +
			`{"type":"api-request","id":"rq1","timestamp":"2026-01-01T10:00:01Z"}` + "\n" +
			`{"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:02Z","message":{"id":"m1","model":"claude-x","content":[{"type":"tool_use","id":"t1","name":"mcp__a__b","input":{"x":1}}],"usage":{"input_tokens":1}}}` + "\n",
		seeds: []string{
			`{"type":"user","uuid":"u1","message":{"content":"a","content":[{"type":"text","text":"bbbb"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"text","text":"bbbb"}],"content":"a"}}`,
			`{"type":"user","uuid":"u1","message":{"content":"a","content":null}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"text","text":"bbbb"}],"content":null}}`,
			`{"type":"user","uuid":"u1","message":{"content":"a","content":5}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"text","text":"bbbb"}],"content":{"a":1}}}`,
			`{"type":"user","uuid":"u1","message":{"content":"a"},"message":{"content":[{"type":"text","text":"bbbb"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":"aaaa"},"message":{"role":"user"}}`,
			`{"type":"user","uuid":"u1","message":{"content":"a"},"message":null}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b","text":"cccc"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[1,{"type":"text","text":"bbbb"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[null,{"type":"text","text":"bbbb"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":5,"text":"bbbb"}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"text","text":5}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"abc","content":null}]}}`,
			`{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[1,2,3],"is_error":null}]}}`,
			`{"type":"assistant","requestRef":"rq1","message":{"id":"m2","model":"claude-x","content":[{"type":"tool_use","id":"t2","name":"n","input":{"a":1},"input":null}]}}`,
			`{"type":"assistant","message":{"id":"m3","model":"<synthetic>","content":"oops"},"isApiErrorMessage":true}`,
			`{"type":"assistant","message":{"id":"m3","model":"<synthetic>","content":[{"type":"text","text":"a"}],"content":"b"},"isApiErrorMessage":true}`,
			`{"type":"assistant","message":{"id":"m4","model":"x","usage":{"input_tokens":1},"usage":{"output_tokens":2}}}`,
			`{"type":"assistant","message":{"id":"m4","model":"x","usage":{"input_tokens":1},"usage":null}}`,
		},
		contexts: []mutationContext{
			{around(`{$}`), `"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"content":[{"type":"text","text":"bbbb"}]}`,
				[]string{"type", "timestamp", "sessionId", "cwd", "isMeta", "isApiErrorMessage", "requestRef", "id", "uuid", "customTitle", "message"}},
			{around(`{$}`), `"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:06Z","isApiErrorMessage":true,"message":{"id":"m2","model":"<synthetic>","content":[{"type":"text","text":"err"}]}`,
				[]string{"type", "requestRef", "isApiErrorMessage", "message"}},
			{around(`{"type":"custom-title",$}`), `"customTitle":"T"`, []string{"customTitle", "type"}},
			{around(`{"type":"api-request","timestamp":"2026-01-01T10:00:03Z",$}`), `"id":"rq2"`, []string{"id", "timestamp"}},
			{around(`{"type":"user","uuid":"u1","message":{$}}`), `"content":"a"`, []string{"content", "id", "model", "stop_reason", "usage"}},
			{around(`{"type":"user","uuid":"u1","message":{$}}`), `"content":[{"type":"text","text":"bbbb"}]`, []string{"content"}},
			{around(`{"type":"assistant","requestRef":"rq1","timestamp":"2026-01-01T10:00:07Z","message":{$}}`),
				`"id":"m2","model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t2","name":"mcp__c__d","input":{"y":2}}],"usage":{"input_tokens":1,"output_tokens":2}`,
				[]string{"id", "model", "stop_reason", "content", "usage"}},
			{around(`{"type":"assistant","message":{"id":"m3","model":"x","usage":{$}}}`),
				`"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"cache_creation":{"ephemeral_1h_input_tokens":1},"output_tokens_details":{"thinking_tokens":5}`,
				[]string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation", "output_tokens_details"}},
			{around(`{"type":"assistant","message":{"id":"m3","model":"x","usage":{"cache_creation":{$}}}}`), `"ephemeral_1h_input_tokens":1`, []string{"ephemeral_1h_input_tokens"}},
			{around(`{"type":"assistant","message":{"id":"m3","model":"x","usage":{"output_tokens_details":{$}}}}`), `"thinking_tokens":5`, []string{"thinking_tokens"}},
			{around(`{"type":"user","uuid":"u1","message":{"content":[{$}]}}`), `"type":"text","text":"bbbb"`, []string{"type", "text", "id", "name", "input", "tool_use_id", "content", "is_error"}},
			{around(`{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:08Z","message":{"content":[{$}]}}`),
				`"type":"tool_result","tool_use_id":"t1","content":"abc","is_error":false`, []string{"type", "tool_use_id", "content", "is_error", "text"}},
			{around(`{"type":"assistant","requestRef":"rq1","message":{"id":"m2","model":"claude-x","content":[{$}]}}`),
				`"type":"tool_use","id":"t2","name":"mcp__c__d","input":{"y":2}`, []string{"type", "id", "name", "input"}},
		},
	},
	{
		name: "pi",
		path: "/x/s/2026-01-01T10-00-00-000Z_11111111-2222-3333-4444-555555555555.jsonl",
		prefix: `{"type":"session","cwd":"/w"}` + "\n" +
			`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
			`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"assistant","model":"m","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"p":1}}],"usage":{"input":3}}}` + "\n",
		seeds: []string{
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}],"content":false}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":false,"content":[{"type":"text","text":"abc"}]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}],"content":null}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}],"content":"text"}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}],"content":[]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abcdef"}],"content":[{"type":"text","text":"abc"}]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}]},"message":{"role":"user","content":[{"type":"text","text":"de"}]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"}]},"message":{"role":"user"}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"},3,{"type":"text","text":"d"}]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc"},null,{"type":"text","text":"d"}]}}`,
			`{"type":"message","id":"u2","message":{"role":"user","content":[{"type":"text","text":"abc","text":7}]}}`,
			`{"type":"message","id":"u2","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"n","arguments":{"a":1},"arguments":null}]}}`,
			`{"type":"message","id":"u2","message":{"role":"toolResult","toolCallId":"c1","content":"abc","content":null}}`,
			`{"type":"message","id":"u2","message":{"role":"toolResult","toolCallId":"c1","content":[{"type":"text","text":"abc"}],"content":"x"}}`,
			`{"type":"message","id":"u2","message":{"role":"assistant","usage":{"input":1},"usage":{"output":2}}}`,
			`{"type":"message","id":"u2","message":{"role":"assistant","usage":{"input":1,"cost":{"total":1}},"usage":{"cost":null}}}`,
			`{"type":"title","title":"t","title":null}`,
		},
		contexts: []mutationContext{
			{around(`{$}`), `"type":"message","id":"x","timestamp":"2026-01-01T10:00:09Z","message":{"role":"user","content":[{"type":"text","text":"abc"}]}`,
				[]string{"type", "id", "timestamp", "cwd", "title", "model", "provider", "modelId", "message"}},
			{around(`{$}`), `"type":"session","cwd":"/a","title":"T"`, []string{"type", "cwd", "title"}},
			{around(`{$}`), `"type":"model_change","model":"p/m","provider":"p","modelId":"m"`, []string{"type", "model", "provider", "modelId"}},
			{around(`{"type":"message","id":"x","timestamp":"2026-01-01T10:00:09Z","message":{$}}`), `"role":"user","content":[{"type":"text","text":"abc"}]`,
				[]string{"role", "content", "provider", "model", "stopReason", "errorMessage", "duration", "ttft", "toolCallId", "isError", "usage"}},
			{around(`{"type":"message","id":"x","timestamp":"2026-01-01T10:00:09Z","message":{$}}`),
				`"role":"assistant","provider":"p","model":"m","stopReason":"stop","errorMessage":"e","duration":1.5,"ttft":0.5,"content":[{"type":"toolCall","id":"c2","name":"mcp__a__b","arguments":{"q":1}}],"usage":{"input":1,"output":2}`,
				[]string{"role", "content", "provider", "model", "stopReason", "errorMessage", "duration", "ttft", "usage"}},
			{around(`{"type":"message","id":"x","timestamp":"2026-01-01T10:00:09Z","message":{$}}`), `"role":"toolResult","toolCallId":"c1","isError":false,"content":[{"type":"text","text":"out"}]`,
				[]string{"role", "content", "toolCallId", "isError"}},
			{around(`{"type":"message","id":"x","message":{"role":"assistant","model":"m","usage":{$}}}`),
				`"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"reasoningTokens":5,"cost":{"total":0.5}`, []string{"input", "output", "cacheRead", "cacheWrite", "reasoningTokens", "cost"}},
			{around(`{"type":"message","id":"x","message":{"role":"assistant","model":"m","usage":{"cost":{$}}}}`), `"total":0.5`, []string{"total"}},
			{around(`{"type":"message","id":"x","message":{"role":"user","content":[{$}]}}`), `"type":"text","text":"abc"`, []string{"type", "text", "id", "name", "arguments"}},
			{around(`{"type":"message","id":"x","message":{"role":"assistant","model":"m","content":[{$}]}}`), `"type":"toolCall","id":"c2","name":"n","arguments":{"a":1}`,
				[]string{"type", "id", "name", "arguments"}},
		},
	},
	{
		name: "codex",
		path: "/x/sessions/rollout-1.jsonl",
		prefix: `{"type":"session_meta","timestamp":"2026-01-01T10:00:00Z","payload":{"id":"s0","cwd":"/w"}}` + "\n" +
			`{"type":"turn_context","payload":{"model":"gpt-x"}}` + "\n" +
			`{"type":"response_item","payload":{"type":"function_call","name":"search","call_id":"c1","arguments":"{}"}}` + "\n",
		seeds: []string{
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","aggregated_output":"abc","aggregated_output":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","aggregated_output":null,"aggregated_output":"abc"}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","aggregated_output":"abc","aggregated_output":5}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","aggregated_output":"abc","aggregated_output":"é"}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i1","command":"ls","command":null,"exit_code":3,"exit_code":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[]}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{"type":"text","text":"abc"}],"content":[]}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{"type":"text","text":"abc"}],"content":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{"type":"text","text":"abc"}],"content":[{"type":"text","text":"de"}]}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{"type":"text","text":"abc"}],"content":false}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{"type":"text","text":"abc"},null,{"type":"text","text":"d"}]}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","tool":"t","arguments":{"a":1},"arguments":null,"result":"r","result":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","error":{"a":1},"error":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","error":null,"error":{"a":1}}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","duration":{"secs":1},"duration":{"nanos":5000000}}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","duration":{"secs":1},"duration":null}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","tool":"t"},"item":{"type":"CommandExecution","aggregated_output":"abcd"}}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","tool":"t"}},"payload":{"type":"item_completed"}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","tool":"t"}},"payload":null}`,
			`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"res","output":null}}`,
			`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"res"},"payload":{"output":"r"}}`,
			`{"type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10},"usage":{"output_tokens":3}}}`,
			`{"type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10},"usage":null}}`,
		},
		contexts: []mutationContext{
			{around(`{$}`), `"type":"event_msg","timestamp":"2026-01-01T10:00:09Z","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"i9","command":"ls","aggregated_output":"abc","exit_code":0}}`,
				[]string{"type", "timestamp", "payload"}},
			{around(`{"type":"response_item",$}`), `"payload":{"type":"function_call_output","call_id":"c1","output":"res"}`, []string{"payload", "type"}},
			{around(`{"type":"response_item","payload":{$}}`), `"type":"function_call","name":"s","call_id":"c2","arguments":"{}"`,
				[]string{"type", "id", "cwd", "thread_source", "forked_from_id", "model", "name", "arguments", "call_id", "output", "response_id", "usage", "thread_settings", "info", "item"}},
			{around(`{"type":"response_item","payload":{$}}`), `"type":"function_call_output","call_id":"c1","output":"res"`,
				[]string{"type", "call_id", "output", "name", "arguments"}},
			{around(`{"type":"session_meta","payload":{$}}`), `"id":"s1","cwd":"/w","thread_source":"subagent","forked_from_id":"p1"`, []string{"id", "cwd", "thread_source", "forked_from_id", "type"}},
			{around(`{"type":"turn_context","payload":{$}}`), `"model":"gpt-y"`, []string{"model"}},
			{around(`{"type":"token_usage_record","payload":{$}}`), `"response_id":"r1","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3}`, []string{"response_id", "usage"}},
			{around(`{"type":"token_usage_record","payload":{"response_id":"r1","usage":{$}}}`),
				`"input_tokens":10,"cached_input_tokens":4,"cache_write_input_tokens":1,"output_tokens":3,"reasoning_output_tokens":2`,
				[]string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"}},
			{around(`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{$}}}`), `"model":"gpt-z"`, []string{"model"}},
			{around(`{"type":"event_msg","payload":{"type":"token_count","info":{$}}}`),
				`"total_token_usage":{"input_tokens":5},"last_token_usage":{"input_tokens":5,"output_tokens":1}`, []string{"total_token_usage", "last_token_usage"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"CommandExecution","id":"i9","command":"ls","aggregated_output":"abc","exit_code":2,"duration":{"secs":1,"nanos":5}`,
				[]string{"type", "id", "server", "tool", "kind", "arguments", "result", "error", "status", "command", "aggregated_output", "exit_code", "duration", "changes", "query", "path", "content"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"McpToolCall","id":"i8","server":"s","tool":"t","arguments":{"a":1},"result":{"b":2},"error":null,"status":"completed"`,
				[]string{"type", "id", "server", "tool", "arguments", "result", "error", "status"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"UserMessage","id":"i7","content":[{"type":"text","text":"abc"}]`,
				[]string{"type", "id", "content"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"FileChange","id":"i6","changes":[{"p":"a"}]`, []string{"type", "id", "changes"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"ImageView","id":"i5","path":"/p"`, []string{"type", "id", "path"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{$}}}`), `"type":"Extension","id":"i4","kind":"k","query":"q"`, []string{"type", "id", "kind", "query"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"i3","duration":{$}}}}`), `"secs":1,"nanos":500000000`, []string{"secs", "nanos"}},
			{around(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"i2","content":[{$}]}}}`), `"type":"text","text":"abc"`, []string{"type", "text"}},
		},
	},
}
