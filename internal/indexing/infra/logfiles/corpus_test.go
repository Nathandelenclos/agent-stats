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
}
