// Package domain models the catalogue: what a harness has installed (skills, agents, MCP servers, plugins)
// and what the session logs say was used, linked so that unused items stand out. It does no I/O.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Kind is what an item is. Use the variables below.
type Kind struct{ name string }

var (
	MCPServer = Kind{"mcp"}
	Skill     = Kind{"skill"}
	Agent     = Kind{"agent"}
	Plugin    = Kind{"plugin"}
	Tool      = Kind{"tool"} // a built-in tool, known only from the logs
)

// Kinds lists the kinds in display order.
var Kinds = []Kind{MCPServer, Skill, Agent, Plugin, Tool}

// String returns the kind name.
func (k Kind) String() string { return k.name }

// Scope is where an item is defined. Use the variables below.
type Scope struct{ name string }

var (
	ScopeUser    = Scope{"user"}
	ScopeProject = Scope{"project"}
	ScopePlugin  = Scope{"plugin"}
	ScopeLogs    = Scope{"logs"} // not found in any configuration: seen in the session logs only
)

// String returns the scope name.
func (s Scope) String() string { return s.name }

// MaxNameLen bounds an item name in bytes (real names are a few dozen).
const MaxNameLen = 128

// ErrInvalidName reports a name that is empty, too long, or holds characters a terminal could misread.
var ErrInvalidName = errors.New("invalid item name")

// Name is the name of an item: valid UTF-8 of printable characters, 1 to MaxNameLen bytes. Build it with NewName;
// the zero Name means "none".
type Name struct{ s string }

// NewName validates s. Control and format characters (escape sequences, newlines, bidirectional overrides) are refused.
func NewName(s string) (Name, error) {
	if s == "" || len(s) > MaxNameLen || !utf8.ValidString(s) {
		return Name{}, fmt.Errorf("%w: %d bytes", ErrInvalidName, len(s))
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return Name{}, fmt.Errorf("%w: unprintable character %U", ErrInvalidName, r)
		}
	}
	return Name{s}, nil
}

// String returns the name.
func (n Name) String() string { return n.s }

// IsZero reports the zero Name.
func (n Name) IsZero() bool { return n.s == "" }

// Item is something a harness has installed.
type Item struct {
	Harness kernel.Harness
	Kind    Kind
	Name    Name
	Scope   Scope
	// Origin is the plugin an item comes from, the zero Name for the others. A plugin item belongs to the plugin
	// that carries its own name.
	Origin Name
	// Source says where the item is defined: a path, as the adapter chooses to show it. It never holds a secret.
	Source   string
	Disabled bool
}

// Plugin is the short name of the plugin an item comes from, empty for the others.
func (i Item) Plugin() string {
	if i.Origin.IsZero() {
		return ""
	}
	return PluginName(i.Origin)
}

// identity is what makes two definitions the same item.
func (i Item) identity() [5]string {
	return [5]string{i.Harness.String(), i.Kind.name, i.Name.s, i.Scope.name, i.Origin.s}
}

// Filter restricts activity to what happened since a time (zero: all) and optionally one harness (empty: all).
type Filter struct {
	Since   time.Time
	Harness kernel.Harness
}

// ToolActivity is the logged calls of one tool; Server is its MCP server, empty for a built-in tool.
type ToolActivity struct {
	Harness kernel.Harness
	Name    string
	Server  string
	Calls   int64
	Last    time.Time
}

// AgentActivity is what the sessions of one agent type did: the model requests they made.
type AgentActivity struct {
	Harness   kernel.Harness
	AgentType string
	Requests  int64
	Last      time.Time
}

// SkillActivity is the logged loads of one skill.
type SkillActivity struct {
	Harness kernel.Harness
	Skill   string
	Calls   int64
	Last    time.Time
}

// Activity is what the session logs say was used.
type Activity struct {
	Tools  []ToolActivity
	Agents []AgentActivity
	Skills []SkillActivity
}

// Entry is one line of the catalogue: an item, or a tool, server, agent or skill seen in the logs but defined nowhere
// the catalogue looks. Uses counts invocations: tool calls for tools and MCP servers, loads for skills. The index
// records no count of subagent sessions, so an agent counts the model requests its sessions made instead.
type Entry struct {
	Item
	Uses int64
	Last time.Time
	// Tracked is false when the logs of the harness do not tell this kind of item apart: Uses is then unknown, not zero.
	Tracked bool
}

// Installed reports whether a configuration defines the entry.
func (e Entry) Installed() bool { return e.Scope != ScopeLogs }

// Unused reports an enabled, installed entry that the logs show was never used.
func (e Entry) Unused() bool { return e.Installed() && !e.Disabled && e.Tracked && e.Uses == 0 }

// tracked reports whether the index records usage of kind for harness h: tool calls everywhere, but the agent type
// of a subagent session and the skill a Skill call loads only for Claude Code (the others log neither by name).
func tracked(h kernel.Harness, k Kind) bool {
	switch k {
	case MCPServer, Tool:
		return true
	case Agent, Skill:
		return h == kernel.Claude
	}
	return false
}

// Link builds the catalogue: every installed item with its usage, then what the logs show of things no configuration
// defines. An item defined twice in the same place (same harness, kind, name, scope and origin) counts once. Items
// defined in several scopes each show the usage of their name. Entries come ordered by harness, kind, then most used.
func Link(items []Item, a Activity) []Entry {
	var entries []Entry
	seen := map[[5]string]bool{}
	claimedTool := map[int]bool{}
	claimedAgent := map[int]bool{}
	claimedSkill := map[int]bool{}
	for _, it := range items {
		if seen[it.identity()] {
			continue
		}
		seen[it.identity()] = true
		e := Entry{Item: it, Tracked: tracked(it.Harness, it.Kind)}
		switch it.Kind {
		case MCPServer:
			for i, t := range a.Tools {
				if t.Harness == it.Harness && t.Server != "" && serverMatches(it, t.Server) {
					e.add(t.Calls, t.Last)
					claimedTool[i] = true
				}
			}
		case Skill:
			for i, s := range a.Skills {
				if s.Harness == it.Harness && nameMatches(it, s.Skill) {
					e.add(s.Calls, s.Last)
					claimedSkill[i] = true
				}
			}
		case Agent:
			for i, g := range a.Agents {
				if g.Harness == it.Harness && nameMatches(it, g.AgentType) {
					e.add(g.Requests, g.Last)
					claimedAgent[i] = true
				}
			}
		}
		entries = append(entries, e)
	}
	entries = withPluginUsage(entries)

	servers := map[[2]string]int{} // (harness, server) -> index in entries, for an MCP server the logs alone name
	for i, t := range a.Tools {
		switch {
		case claimedTool[i]:
		case t.Server == "":
			entries = append(entries, logged(t.Harness, Tool, t.Name, t.Calls, t.Last))
		default:
			k := [2]string{t.Harness.String(), t.Server}
			i, ok := servers[k]
			if !ok {
				i = len(entries)
				servers[k] = i
				entries = append(entries, logged(t.Harness, MCPServer, t.Server, 0, time.Time{}))
			}
			entries[i].add(t.Calls, t.Last)
		}
	}
	for i, g := range a.Agents {
		if !claimedAgent[i] && g.AgentType != "main" && g.AgentType != "subagent" { // those two name no agent
			entries = append(entries, logged(g.Harness, Agent, g.AgentType, g.Requests, g.Last))
		}
	}
	for i, s := range a.Skills {
		if !claimedSkill[i] {
			entries = append(entries, logged(s.Harness, Skill, s.Skill, s.Calls, s.Last))
		}
	}
	sortEntries(entries)
	return entries
}

func (e *Entry) add(calls int64, last time.Time) {
	e.Uses += calls
	if last.After(e.Last) {
		e.Last = last
	}
}

// logged is an entry for something the logs name and no configuration defines. A name a terminal could misread is
// shown escaped rather than dropped: the figures are real.
func logged(h kernel.Harness, k Kind, name string, uses int64, last time.Time) Entry {
	n, err := NewName(name)
	if err != nil {
		n = Name{escaped(name)}
	}
	return Entry{Item: Item{Harness: h, Kind: k, Name: n, Scope: ScopeLogs}, Uses: uses, Last: last, Tracked: true}
}

// escaped turns what NewName refuses into something printable: unprintable or invalid characters become "?", cut to MaxNameLen bytes.
func escaped(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) {
			r = '?'
		}
		if b.Len()+utf8.RuneLen(r) > MaxNameLen {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// withPluginUsage gives each plugin the invocations of the MCP servers and skills it carries. The model requests of
// its agents are not added: they count something else. A plugin with none of those items, or none the logs can follow,
// has unknown usage.
func withPluginUsage(entries []Entry) []Entry {
	for i, p := range entries {
		if p.Kind != Plugin {
			continue
		}
		for _, c := range entries {
			if c.Origin == p.Name && c.Harness == p.Harness && c.Tracked && c.Kind != Agent {
				entries[i].Tracked = true
				entries[i].add(c.Uses, c.Last)
			}
		}
	}
	return entries
}

// PluginName is a plugin id without its marketplace: "name@marketplace" reads "name". The skills and agents a plugin
// carries are named "name:skill" in the logs.
func PluginName(id Name) string {
	name, _, _ := strings.Cut(id.s, "@")
	return name
}

// nameMatches reports whether a logged skill or agent name designates it. A plugin's items are logged as
// "plugin:name", or sometimes by their own name alone.
func nameMatches(it Item, logged string) bool {
	if logged == it.Name.s {
		return true
	}
	if it.Origin.IsZero() {
		return false
	}
	own, ok := strings.CutPrefix(it.Name.s, PluginName(it.Origin)+":")
	return ok && logged == own
}

// serverMatches reports whether a tool call logged under server belongs to the MCP server item. Claude Code writes
// "mcp__<server>__<tool>" with the characters of the server name outside [A-Za-z0-9_-] turned into "_", and calls
// a plugin's server "plugin_<plugin>_<server>".
func serverMatches(it Item, logged string) bool {
	name := it.Name.s
	if logged == name || logged == normalizeServer(name) {
		return true
	}
	return !it.Origin.IsZero() && logged == "plugin_"+normalizeServer(PluginName(it.Origin))+"_"+normalizeServer(name)
}

func normalizeServer(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}
