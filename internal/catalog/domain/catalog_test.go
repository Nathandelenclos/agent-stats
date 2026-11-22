package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func name(t *testing.T, s string) Name {
	t.Helper()
	n, err := NewName(s)
	if err != nil {
		t.Fatalf("NewName(%q): %v", s, err)
	}
	return n
}

func item(t *testing.T, h kernel.Harness, k Kind, n string, s Scope) Item {
	t.Helper()
	return Item{Harness: h, Kind: k, Name: name(t, n), Scope: s, Source: "~/cfg"}
}

func find(t *testing.T, entries []Entry, h kernel.Harness, k Kind, n string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Harness == h && e.Kind == k && e.Name.String() == n {
			return e
		}
	}
	t.Fatalf("no %s %q for %s in %+v", k, n, h, entries)
	return Entry{}
}

func TestNameRefusesWhatATerminalCouldMisread(t *testing.T) {
	// Given names with escape sequences, newlines, bidirectional overrides, invalid UTF-8, or no usable length
	for _, bad := range []string{"", "a\x1b[31mred", "two\nlines", "tab\tbed", "evil‮gnp", "bad\xffutf8", strings.Repeat("x", MaxNameLen+1)} {
		// When a name is built from them, then it is refused
		if _, err := NewName(bad); err == nil {
			t.Errorf("NewName(%q) accepted", bad)
		}
	}
	// And names with spaces, dots and accents are fine
	for _, ok := range []string{"claude.ai Claude Docs", "plugin:skill", "éclair", strings.Repeat("x", MaxNameLen)} {
		if _, err := NewName(ok); err != nil {
			t.Errorf("NewName(%q): %v", ok, err)
		}
	}
}

func TestInstalledItemsShowTheirOwnUsage(t *testing.T) {
	// Given a Claude MCP server and a Codex one called the same, and calls logged for each
	items := []Item{
		item(t, kernel.Claude, MCPServer, "github", ScopeUser),
		item(t, kernel.Codex, MCPServer, "github", ScopeUser),
		item(t, kernel.Claude, MCPServer, "idle", ScopeUser),
	}
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	act := Activity{Tools: []ToolActivity{
		{Harness: kernel.Claude, Name: "get_issue", Server: "github", Calls: 3, Last: at},
		{Harness: kernel.Claude, Name: "search", Server: "github", Calls: 2, Last: at.Add(time.Hour)},
		{Harness: kernel.Codex, Name: "get_issue", Server: "github", Calls: 7, Last: at},
	}}

	// When they are linked
	entries := Link(items, act)

	// Then each harness counts its own calls across the tools of the server, and an unused server shows zero
	if e := find(t, entries, kernel.Claude, MCPServer, "github"); e.Uses != 5 || !e.Last.Equal(at.Add(time.Hour)) {
		t.Errorf("claude github: %+v", e)
	}
	if e := find(t, entries, kernel.Codex, MCPServer, "github"); e.Uses != 7 {
		t.Errorf("codex github: %+v", e)
	}
	if e := find(t, entries, kernel.Claude, MCPServer, "idle"); e.Uses != 0 || !e.Unused() {
		t.Errorf("idle must be unused: %+v", e)
	}
}

func TestClaudeNamesTheServersOfPluginsAndConnectorsInItsOwnWay(t *testing.T) {
	// Given a server of a plugin and a server whose name has a dot and a space
	plugin := item(t, kernel.Claude, MCPServer, "chrome-devtools", ScopePlugin)
	plugin.Origin = name(t, "chrome-devtools-mcp@official")
	dotted := item(t, kernel.Claude, MCPServer, "claude.ai Docs", ScopeUser)
	act := Activity{Tools: []ToolActivity{
		{Harness: kernel.Claude, Name: "click", Server: "plugin_chrome-devtools-mcp_chrome-devtools", Calls: 4},
		{Harness: kernel.Claude, Name: "read", Server: "claude_ai_Docs", Calls: 2},
	}}

	// When they are linked
	entries := Link([]Item{plugin, dotted}, act)

	// Then the calls are found under the names Claude Code logs
	if e := find(t, entries, kernel.Claude, MCPServer, "chrome-devtools"); e.Uses != 4 {
		t.Errorf("plugin server: %+v", e)
	}
	if e := find(t, entries, kernel.Claude, MCPServer, "claude.ai Docs"); e.Uses != 2 {
		t.Errorf("dotted server: %+v", e)
	}
	if len(entries) != 2 {
		t.Errorf("claimed calls must not show again as logs-only entries: %+v", entries)
	}
}

func TestWhatTheLogsAloneNameStillShows(t *testing.T) {
	// Given calls to a built-in tool and to a server no configuration defines, and subagents of two types
	act := Activity{
		Tools: []ToolActivity{
			{Harness: kernel.Pi, Name: "bash", Calls: 9},
			{Harness: kernel.Claude, Name: "a", Server: "gone", Calls: 1},
			{Harness: kernel.Claude, Name: "b", Server: "gone", Calls: 2},
		},
		Agents: []AgentActivity{
			{Harness: kernel.Claude, AgentType: "main", Requests: 100},
			{Harness: kernel.Claude, AgentType: "subagent", Requests: 5},
			{Harness: kernel.Claude, AgentType: "Explore", Requests: 8},
		},
	}

	// When linked with nothing installed
	entries := Link(nil, act)

	// Then built-in tools, the server (calls summed) and the named agent appear as seen in the logs only
	if e := find(t, entries, kernel.Pi, Tool, "bash"); e.Uses != 9 || e.Scope != ScopeLogs || e.Installed() {
		t.Errorf("tool: %+v", e)
	}
	if e := find(t, entries, kernel.Claude, MCPServer, "gone"); e.Uses != 3 {
		t.Errorf("server: %+v", e)
	}
	if e := find(t, entries, kernel.Claude, Agent, "Explore"); e.Uses != 8 {
		t.Errorf("agent: %+v", e)
	}
	// And the generic "main" and "subagent" types name no agent
	if len(entries) != 3 {
		t.Errorf("want 3 entries, got %+v", entries)
	}
}

func TestUsageOfWhatTheLogsDoNotTellApartIsUnknownNotZero(t *testing.T) {
	// Given a Codex agent and skill, and a Claude agent, with no activity at all
	items := []Item{
		item(t, kernel.Codex, Agent, "reviewer", ScopeUser),
		item(t, kernel.Codex, Skill, "notes", ScopeUser),
		item(t, kernel.Claude, Agent, "reviewer", ScopeUser),
	}

	// When linked
	entries := Link(items, Activity{})

	// Then only Claude's agent is known to be unused
	for _, e := range entries {
		wantTracked := e.Harness == kernel.Claude
		if e.Tracked != wantTracked || e.Unused() != wantTracked {
			t.Errorf("%s %s: tracked=%v unused=%v", e.Harness, e.Name, e.Tracked, e.Unused())
		}
	}
}

func TestPluginsAddUpTheUsageOfWhatTheyCarry(t *testing.T) {
	// Given a plugin carrying a skill and a server, and a bare plugin whose content is not read
	plugin := item(t, kernel.Claude, Plugin, "demo@market", ScopeUser)
	skill := item(t, kernel.Claude, Skill, "demo:review", ScopePlugin)
	skill.Origin = plugin.Name
	server := item(t, kernel.Claude, MCPServer, "helper", ScopePlugin)
	server.Origin = plugin.Name
	bare := item(t, kernel.Codex, Plugin, "other@market", ScopeUser)
	act := Activity{
		Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "demo:review", Calls: 2}},
		Tools:  []ToolActivity{{Harness: kernel.Claude, Name: "ping", Server: "plugin_demo_helper", Calls: 5}},
	}

	// When linked
	entries := Link([]Item{plugin, skill, server, bare}, act)

	// Then the plugin shows 7 uses, and the bare one is unknown
	if e := find(t, entries, kernel.Claude, Plugin, "demo@market"); e.Uses != 7 || !e.Tracked {
		t.Errorf("plugin: %+v", e)
	}
	if e := find(t, entries, kernel.Codex, Plugin, "other@market"); e.Tracked || e.Unused() {
		t.Errorf("bare plugin: %+v", e)
	}
	// And a plugin skill is also found under its own name alone
	if e := Link([]Item{skill}, Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}); e[0].Uses != 1 {
		t.Errorf("bare skill name: %+v", e)
	}
}

func TestDisabledItemsAreNotReportedUnused(t *testing.T) {
	// Given a disabled server
	it := item(t, kernel.Codex, MCPServer, "off", ScopeUser)
	it.Disabled = true

	// When linked with no calls, then it is not flagged as never used
	if e := Link([]Item{it}, Activity{})[0]; e.Unused() {
		t.Errorf("a disabled item is off, not neglected: %+v", e)
	}
}

func TestAnItemDefinedTwiceInTheSamePlaceCountsOnce(t *testing.T) {
	// Given the same skill listed twice for one scope and once for another
	a := item(t, kernel.Claude, Skill, "dup", ScopeUser)
	b := item(t, kernel.Claude, Skill, "dup", ScopeProject)

	// When linked
	entries := Link([]Item{a, a, b}, Activity{})

	// Then there is one entry per scope
	if len(entries) != 2 {
		t.Errorf("want 2 entries, got %+v", entries)
	}
}

func TestEntriesComeOrderedByHarnessKindThenMostUsed(t *testing.T) {
	// Given unsorted items and activity
	items := []Item{
		item(t, kernel.Pi, Skill, "z", ScopeUser),
		item(t, kernel.Claude, Skill, "b", ScopeUser),
		item(t, kernel.Claude, MCPServer, "few", ScopeUser),
		item(t, kernel.Claude, MCPServer, "many", ScopeUser),
	}
	act := Activity{Tools: []ToolActivity{
		{Harness: kernel.Claude, Name: "t", Server: "few", Calls: 1},
		{Harness: kernel.Claude, Name: "t", Server: "many", Calls: 9},
	}}

	// When linked
	var got []string
	for _, e := range Link(items, act) {
		got = append(got, e.Harness.String()+"/"+e.Kind.String()+"/"+e.Name.String())
	}

	// Then Claude comes before pi, servers before skills, and the busiest server first
	want := "claude/mcp/many claude/mcp/few claude/skill/b pi/skill/z"
	if strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s", got, want)
	}
}

func TestLoggedNamesAreShownEscapedNeverRaw(t *testing.T) {
	// Given a log that names a tool with an escape sequence
	act := Activity{Tools: []ToolActivity{{Harness: kernel.Pi, Name: "ba\x1b[2Jd", Calls: 1}}}

	// When linked, then the entry keeps the figures and holds no control character
	e := Link(nil, act)[0]
	if e.Uses != 1 || strings.ContainsAny(e.Name.String(), "\x1b\n") {
		t.Errorf("entry: %+v", e)
	}
}
