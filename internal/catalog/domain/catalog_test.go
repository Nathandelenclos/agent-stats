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
	for _, bad := range []string{"", "a\x1b[31mred", "two\nlines", "tab\tbed", "evil\u202egnp", "bad\xffutf8", strings.Repeat("x", MaxNameLen+1)} {
		// When a name is built from them, then it is refused
		if _, err := NewName(bad); err == nil {
			t.Errorf("NewName(%q) accepted", bad)
		}
	}
}

func TestNameAcceptsWhatHarnessesReallyCallTheirItems(t *testing.T) {
	// Given names with spaces, dots, colons and accents, and one at the length bound
	for _, ok := range []string{"claude.ai Claude Docs", "plugin:skill", "éclair", strings.Repeat("x", MaxNameLen)} {
		// When a name is built from them, then it is accepted
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
	}
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	act := Activity{Tools: []ToolActivity{
		{Harness: kernel.Claude, Name: "get_issue", Server: "github", Calls: 3, Last: at},
		{Harness: kernel.Claude, Name: "search", Server: "github", Calls: 2, Last: at.Add(time.Hour)},
		{Harness: kernel.Codex, Name: "get_issue", Server: "github", Calls: 7, Last: at},
	}}

	// When they are linked
	entries := Link(items, act)

	// Then each harness counts its own calls, summed over the tools of the server
	if e := find(t, entries, kernel.Claude, MCPServer, "github"); e.Uses != 5 || !e.Last.Equal(at.Add(time.Hour)) {
		t.Errorf("claude github: %+v", e)
	}
	if e := find(t, entries, kernel.Codex, MCPServer, "github"); e.Uses != 7 {
		t.Errorf("codex github: %+v", e)
	}
}

func TestInstalledItemWithNoCallIsUnused(t *testing.T) {
	// Given an installed MCP server that no call names
	items := []Item{item(t, kernel.Claude, MCPServer, "idle", ScopeUser)}

	// When linked, then it shows zero uses and counts as unused
	if e := Link(items, Activity{})[0]; e.Uses != 0 || !e.Unused() {
		t.Errorf("idle: %+v", e)
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

// loggedOnly is activity no configuration defines: a built-in tool, a server, and subagents of two types.
var loggedOnly = Activity{
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

func TestBuiltInToolsShowFromTheLogsAlone(t *testing.T) {
	// Given calls to a built-in tool and nothing installed, when linked
	entries := Link(nil, loggedOnly)

	// Then the tool appears as seen in the logs only
	if e := find(t, entries, kernel.Pi, Tool, "bash"); e.Uses != 9 || e.Scope != ScopeLogs || e.Installed() {
		t.Errorf("tool: %+v", e)
	}
}

func TestServerNoConfigurationDefinesShowsWithItsCallsSummed(t *testing.T) {
	// Given calls to two tools of a server nothing installs, when linked
	entries := Link(nil, loggedOnly)

	// Then the server appears once, with both tools' calls
	if e := find(t, entries, kernel.Claude, MCPServer, "gone"); e.Uses != 3 {
		t.Errorf("server: %+v", e)
	}
}

func TestOnlyNamedAgentTypesShowFromTheLogs(t *testing.T) {
	// Given requests of the main conversation, of unnamed subagents and of an Explore agent, when linked
	entries := Link(nil, loggedOnly)

	// Then Explore appears, and "main" and "subagent" name no agent
	if e := find(t, entries, kernel.Claude, Agent, "Explore"); e.Uses != 8 {
		t.Errorf("agent: %+v", e)
	}
	for _, e := range entries {
		if e.Kind == Agent && e.Name.String() != "Explore" {
			t.Errorf("unnamed agent shown: %+v", e)
		}
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

func TestSkillUsageIsUnknownWhileSomeSkillCallAwaitsItsLogToBeReadAgain(t *testing.T) {
	// Given a Claude skill and agent with no named load, and a Skill call of Claude whose log is not read again yet
	items := []Item{
		item(t, kernel.Claude, Skill, "review", ScopeUser),
		item(t, kernel.Claude, Agent, "reviewer", ScopeUser),
	}
	act := Activity{PendingSkillLoads: map[kernel.Harness]bool{kernel.Claude: true}}

	// When linked
	entries := Link(items, act)

	// Then the skill is not reported unused, its usage being unknown, and the agent is still known to be unused
	if s := find(t, entries, kernel.Claude, Skill, "review"); s.Tracked || s.Unused() {
		t.Errorf("skill: %+v", s)
	}
	if a := find(t, entries, kernel.Claude, Agent, "reviewer"); !a.Unused() {
		t.Errorf("agent: %+v", a)
	}
}

// unnamedSkillCallsLinked links an installed Claude skill, three Skill calls that named no valid skill, and one load of another skill.
func unnamedSkillCallsLinked(t *testing.T) ([]Entry, time.Time) {
	t.Helper()
	items := []Item{item(t, kernel.Claude, Skill, "review", ScopeUser)}
	last := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	act := Activity{
		UnnamedSkillCalls: []SkillActivity{{Harness: kernel.Claude, Calls: 3, Last: last}},
		Skills:            []SkillActivity{{Harness: kernel.Claude, Skill: "other", Calls: 1}},
	}
	return Link(items, act), last
}

func TestSkillCallsThatNamedNoValidSkillAreListedApartAndCountedTogether(t *testing.T) {
	// Given three Skill calls that named no valid skill
	// When linked
	entries, last := unnamedSkillCallsLinked(t)

	// Then they are listed as what they are, defined nowhere, counted together with the latest time
	if u := find(t, entries, kernel.Claude, Skill, UnnamedSkillEntry); u.Installed() || u.Uses != 3 || !u.Last.Equal(last) {
		t.Errorf("unnamed skill calls: %+v", u)
	}
}

func TestSkillCallsThatNamedNoValidSkillAreCreditedToNoInstalledSkill(t *testing.T) {
	// Given an installed skill and three Skill calls that named no valid skill
	// When linked
	entries, _ := unnamedSkillCallsLinked(t)

	// Then the installed skill is known to be unused, as none of those calls can be credited to it
	if s := find(t, entries, kernel.Claude, Skill, "review"); !s.Tracked || s.Uses != 0 || !s.Unused() {
		t.Errorf("installed skill: %+v", s)
	}
}

func TestNoUnnamedSkillCallIsListedWhenEveryCallNamedItsSkill(t *testing.T) {
	// Given a skill load that named its skill and no call that did not
	act := Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}

	// When linked, then only the skill named is listed
	for _, e := range Link(nil, act) {
		if e.Name.String() == UnnamedSkillEntry {
			t.Errorf("unnamed skill calls listed: %+v", e)
		}
	}
}

func TestPluginCarryingASkillOfUnknownUsageIsNotReportedUnused(t *testing.T) {
	// Given a plugin that carries a skill and an MCP server never called, and a Claude Skill call whose log is not read again yet
	plugin := item(t, kernel.Claude, Plugin, "demo@market", ScopeUser)
	skill := item(t, kernel.Claude, Skill, "demo:review", ScopePlugin)
	skill.Origin = plugin.Name
	server := item(t, kernel.Claude, MCPServer, "helper", ScopePlugin)
	server.Origin = plugin.Name
	act := Activity{PendingSkillLoads: map[kernel.Harness]bool{kernel.Claude: true}}

	// When linked
	p := find(t, Link([]Item{plugin, skill, server}, act), kernel.Claude, Plugin, "demo@market")

	// Then the plugin's usage is unknown: its skill may have been loaded, so it is not listed as unused
	if p.Tracked || p.Unused() {
		t.Errorf("plugin: %+v", p)
	}
}

func TestPluginCarryingASkillOfUnknownUsageShowsTheCallsOfItsServer(t *testing.T) {
	// Given the same plugin, whose server was called twice
	plugin := item(t, kernel.Claude, Plugin, "demo@market", ScopeUser)
	skill := item(t, kernel.Claude, Skill, "demo:review", ScopePlugin)
	skill.Origin = plugin.Name
	server := item(t, kernel.Claude, MCPServer, "helper", ScopePlugin)
	server.Origin = plugin.Name
	act := Activity{
		PendingSkillLoads: map[kernel.Harness]bool{kernel.Claude: true},
		Tools:             []ToolActivity{{Harness: kernel.Claude, Name: "x", Server: "plugin_demo_helper", Calls: 2}},
	}

	// When linked, then the plugin keeps its known calls
	if p := find(t, Link([]Item{plugin, skill, server}, act), kernel.Claude, Plugin, "demo@market"); p.Uses != 2 || p.Unused() {
		t.Errorf("plugin: %+v", p)
	}
}

func TestPluginsAddUpTheUsageOfWhatTheyCarry(t *testing.T) {
	// Given a plugin carrying a skill, a server and an agent
	plugin := item(t, kernel.Claude, Plugin, "demo@market", ScopeUser)
	agent := item(t, kernel.Claude, Agent, "demo:checker", ScopePlugin)
	agent.Origin = plugin.Name
	skill := item(t, kernel.Claude, Skill, "demo:review", ScopePlugin)
	skill.Origin = plugin.Name
	server := item(t, kernel.Claude, MCPServer, "helper", ScopePlugin)
	server.Origin = plugin.Name
	act := Activity{
		Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "demo:review", Calls: 2}},
		Tools:  []ToolActivity{{Harness: kernel.Claude, Name: "ping", Server: "plugin_demo_helper", Calls: 5}},
		Agents: []AgentActivity{{Harness: kernel.Claude, AgentType: "demo:checker", Requests: 40}},
	}

	// When linked
	entries := Link([]Item{plugin, skill, server, agent}, act)

	// Then the plugin shows 7 uses: calls and loads, not the agent's 40 requests, which count something else
	if e := find(t, entries, kernel.Claude, Plugin, "demo@market"); e.Uses != 7 || !e.Tracked {
		t.Errorf("plugin: %+v", e)
	}
}

func TestPluginWhoseContentIsNotReadHasUnknownUsage(t *testing.T) {
	// Given a plugin with no item known to come from it
	bare := item(t, kernel.Codex, Plugin, "other@market", ScopeUser)

	// When linked, then its usage is unknown, not zero
	if e := Link([]Item{bare}, Activity{})[0]; e.Tracked || e.Unused() {
		t.Errorf("bare plugin: %+v", e)
	}
}

func TestPluginSkillIsAlsoFoundUnderItsOwnName(t *testing.T) {
	// Given a skill carried by a plugin, and a log that names it without the plugin prefix
	skill := item(t, kernel.Claude, Skill, "demo:review", ScopePlugin)
	skill.Origin = name(t, "demo@market")
	act := Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}

	// When linked, then the load counts for the skill
	if e := Link([]Item{skill}, act)[0]; e.Uses != 1 {
		t.Errorf("bare skill name: %+v", e)
	}
}

func TestBareSkillNameCreditsNoPluginWhenSeveralCarryIt(t *testing.T) {
	// Given two plugins that each carry a skill "review", and a log that names a load "review" without a plugin prefix
	alpha := item(t, kernel.Claude, Skill, "alpha:review", ScopePlugin)
	alpha.Origin = name(t, "alpha@market")
	beta := item(t, kernel.Claude, Skill, "beta:review", ScopePlugin)
	beta.Origin = name(t, "beta@market")
	act := Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}

	// When linked
	entries := Link([]Item{alpha, beta}, act)

	// Then neither plugin skill is credited with the load
	for _, n := range []string{"alpha:review", "beta:review"} {
		if e := find(t, entries, kernel.Claude, Skill, n); e.Uses != 0 {
			t.Errorf("%s credited with an ambiguous load: %+v", n, e)
		}
	}
}

func TestBareAgentNameCreditsNoPluginWhenSeveralCarryIt(t *testing.T) {
	// Given two plugins that each carry an agent "checker", and subagent requests logged as "checker"
	alpha := item(t, kernel.Claude, Agent, "alpha:checker", ScopePlugin)
	alpha.Origin = name(t, "alpha@market")
	beta := item(t, kernel.Claude, Agent, "beta:checker", ScopePlugin)
	beta.Origin = name(t, "beta@market")
	act := Activity{Agents: []AgentActivity{{Harness: kernel.Claude, AgentType: "checker", Requests: 3}}}

	// When linked, then neither plugin agent is credited with them
	entries := Link([]Item{alpha, beta}, act)
	for _, n := range []string{"alpha:checker", "beta:checker"} {
		if e := find(t, entries, kernel.Claude, Agent, n); e.Uses != 0 {
			t.Errorf("%s credited with ambiguous requests: %+v", n, e)
		}
	}
}

// carrying builds two plugin items of one kind that both answer to the short name "review".
func carrying(t *testing.T, k Kind) (alpha, beta, alphaPlugin, betaPlugin Item) {
	t.Helper()
	alpha = item(t, kernel.Claude, k, "alpha:review", ScopePlugin)
	alpha.Origin = name(t, "alpha@market")
	beta = item(t, kernel.Claude, k, "beta:review", ScopePlugin)
	beta.Origin = name(t, "beta@market")
	alphaPlugin = item(t, kernel.Claude, Plugin, "alpha@market", ScopeUser)
	betaPlugin = item(t, kernel.Claude, Plugin, "beta@market", ScopeUser)
	return
}

func TestWhatAnAmbiguousBareCallCouldDesignateHasUnknownUsage(t *testing.T) {
	// Given two plugins carrying "review" (a skill, then an agent), one call under the full name "alpha:review" and one
	// under the bare name "review" that either plugin could own
	for _, k := range []Kind{Skill, Agent} {
		t.Run(k.String(), func(t *testing.T) {
			alpha, beta, alphaPlugin, betaPlugin := carrying(t, k)
			act := Activity{}
			if k == Skill {
				act.Skills = []SkillActivity{{Harness: kernel.Claude, Skill: "alpha:review", Calls: 1}, {Harness: kernel.Claude, Skill: "review", Calls: 1}}
			} else {
				act.Agents = []AgentActivity{{Harness: kernel.Claude, AgentType: "alpha:review", Requests: 1}, {Harness: kernel.Claude, AgentType: "review", Requests: 1}}
			}

			// When linked
			entries := Link([]Item{alpha, beta, alphaPlugin, betaPlugin}, act)

			// Then the plugin whose call is named keeps its count, and the other's usage is unknown, not zero:
			// it is not reported unused, however the report is limited
			if a := find(t, entries, kernel.Claude, k, "alpha:review"); a.Uses != 1 || !a.Tracked {
				t.Errorf("alpha: %+v", a)
			}
			for _, e := range []Entry{find(t, entries, kernel.Claude, k, "beta:review")} {
				if e.Tracked || e.Unused() {
					t.Errorf("%s %s: tracked %v, unused %v", e.Kind, e.Name, e.Tracked, e.Unused())
				}
			}
			if k == Skill {
				if p := find(t, entries, kernel.Claude, Plugin, "beta@market"); p.Tracked || p.Unused() {
					t.Errorf("beta plugin: %+v", p)
				}
			}
		})
	}
}

func TestAPluginCarryingAnItemOfUnknownUsageIsNotReportedUnusedWhateverElseItCarries(t *testing.T) {
	// Given a plugin carrying an ambiguous skill and a server that was never called, and another plugin carrying "review"
	alpha, beta, alphaPlugin, betaPlugin := carrying(t, Skill)
	server := item(t, kernel.Claude, MCPServer, "beta-db", ScopePlugin)
	server.Origin = beta.Origin
	act := Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}

	// When linked
	entries := Link([]Item{alpha, beta, server, alphaPlugin, betaPlugin}, act)

	// Then neither plugin is reported unused: the bare call may be either one's
	for _, n := range []string{"alpha@market", "beta@market"} {
		if p := find(t, entries, kernel.Claude, Plugin, n); p.Unused() {
			t.Errorf("%s reported unused: %+v", n, p)
		}
	}
}

func TestSkillOfAPluginIsKnownUnusedWhenNoBareCallCouldBeItsOwn(t *testing.T) {
	// Given two plugins carrying "review" and a log without any call to it
	alpha, beta, _, _ := carrying(t, Skill)

	// When linked, then both are known unused: nothing could be theirs
	for _, e := range Link([]Item{alpha, beta}, Activity{}) {
		if !e.Unused() {
			t.Errorf("%s: %+v", e.Name, e)
		}
	}
}

func TestBareSkillNameStillCreditsTheSkillOfThatNameAlone(t *testing.T) {
	// Given a user skill "review" and a plugin skill "alpha:review", and a load logged as "review"
	user := item(t, kernel.Claude, Skill, "review", ScopeUser)
	plugin := item(t, kernel.Claude, Skill, "alpha:review", ScopePlugin)
	plugin.Origin = name(t, "alpha@market")
	act := Activity{Skills: []SkillActivity{{Harness: kernel.Claude, Skill: "review", Calls: 1}}}

	// When linked, then only the skill that is called exactly that is credited
	entries := Link([]Item{user, plugin}, act)
	if u, p := find(t, entries, kernel.Claude, Skill, "review"), find(t, entries, kernel.Claude, Skill, "alpha:review"); u.Uses != 1 || p.Uses != 0 {
		t.Errorf("user %+v, plugin %+v", u, p)
	}
}

func TestEntriesCountInTheUnitOfTheirKind(t *testing.T) {
	// Given one entry of each kind
	for kind, want := range map[Kind]Unit{MCPServer: UnitCalls, Tool: UnitCalls, Skill: UnitLoads, Agent: UnitRequests, Plugin: UnitInvocations} {
		// When asked for its unit, then it names what its uses count
		if got := (Entry{Item: Item{Kind: kind}}).Unit(); got != want {
			t.Errorf("%s counts %s, want %s", kind, got, want)
		}
	}
}

func TestPluginWithOnlyAgentsHasUnknownUsage(t *testing.T) {
	// Given a plugin that carries only an agent, which the logs show at work
	plugin := item(t, kernel.Claude, Plugin, "agents-only@market", ScopeUser)
	agent := item(t, kernel.Claude, Agent, "agents-only:helper", ScopePlugin)
	agent.Origin = plugin.Name
	act := Activity{Agents: []AgentActivity{{Harness: kernel.Claude, AgentType: "agents-only:helper", Requests: 3}}}

	// When linked, then the plugin does not claim an invocation count it cannot give
	if e := find(t, Link([]Item{plugin, agent}, act), kernel.Claude, Plugin, "agents-only@market"); e.Tracked || e.Unused() {
		t.Errorf("plugin: %+v", e)
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

func TestTallyCountsInstalledAndUnusedPerHarness(t *testing.T) {
	// Given, in Claude, one used server, one idle server and one disabled idle server, and a built-in tool seen in the logs
	off := item(t, kernel.Claude, MCPServer, "off", ScopeUser)
	off.Disabled = true
	items := []Item{item(t, kernel.Claude, MCPServer, "used", ScopeUser), item(t, kernel.Claude, MCPServer, "idle", ScopeUser), off}
	act := Activity{Tools: []ToolActivity{
		{Harness: kernel.Claude, Name: "t", Server: "used", Calls: 1},
		{Harness: kernel.Claude, Name: "Read", Calls: 4},
	}}

	// When the entries are tallied
	got := TallyByHarness(Link(items, act))[kernel.Claude]

	// Then the three installed count, only the enabled idle one is unused, and the tool seen in the logs is not installed
	if got != (Tally{Installed: 3, Unused: 1}) {
		t.Errorf("tally: %+v", got)
	}
}
