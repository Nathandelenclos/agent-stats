package e2e

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	catalogdomain "github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	catalogdb "github.com/Nathandelenclos/agent-stats/internal/catalog/infra/sqlite"
	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// These tests write Skill calls through the indexing adapter, or leave an index as an older build made it, and read
// them back through the catalogue's adapter: the skill column of usage_tool_calls is the contract between the two.

var skillCallTime = time.UnixMilli(1767261600000)

// openIndex opens the index at path with this build, and closes it when the test ends.
func openIndex(t *testing.T, path string) *sqlite.SQLiteIndex {
	t.Helper()
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// saveLog saves a Claude log holding the given tool calls.
func saveLog(t *testing.T, w *sqlite.SQLiteIndex, file string, tools ...domain.ToolCall) {
	t.Helper()
	saveLogHolding(t, w, file, nil, tools)
}

// saveLogWithAPrompt saves a Claude log holding the given tool calls and one prompt.
func saveLogWithAPrompt(t *testing.T, w *sqlite.SQLiteIndex, file string, tools ...domain.ToolCall) {
	t.Helper()
	saveLogHolding(t, w, file, []domain.Prompt{{Key: "p1", At: skillCallTime, Text: "hello"}}, tools)
}

func saveLogHolding(t *testing.T, w *sqlite.SQLiteIndex, file string, prompts []domain.Prompt, tools []domain.ToolCall) {
	t.Helper()
	id, err := domain.NewSessionID(kernel.Claude, filepath.Base(file))
	if err != nil {
		t.Fatal(err)
	}
	log := indexing.IndexedLog{File: indexing.LogFile{Path: file, Harness: kernel.Claude, Revision: "1"}, Transcript: indexing.IndexedTranscript{
		Transcript: domain.Transcript{Session: domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent, Start: skillCallTime, End: skillCallTime}, Tools: tools, Prompts: prompts},
		Signals:    domain.AnalyzeSession(prompts),
	}}
	if err := w.Save([]indexing.IndexedLog{log})[0]; err != nil {
		t.Fatal(err)
	}
}

// skillCall is a Claude Skill call; skill is empty when the call named no valid skill.
func skillCall(key, skill string) domain.ToolCall {
	return domain.ToolCall{Key: key, At: skillCallTime, Name: "Skill", Skill: skill}
}

// activityOf reads the activity the catalogue would show of the index at path.
func activityOf(t *testing.T, path string) catalogdomain.Activity {
	t.Helper()
	r, err := catalogdb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := r.Activity(catalogdomain.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// logsToReadAgain returns, among logs, those an index at path does not know, so that the next indexing reads them.
func logsToReadAgain(t *testing.T, path string, logs ...string) (again []string) {
	t.Helper()
	known, err := openIndex(t, path).IndexedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range logs {
		if _, ok := known[l]; !ok {
			again = append(again, l)
		}
	}
	return again
}

func TestASkillCallIsCountedAsALoadOfTheSkillItNamed(t *testing.T) {
	// Given a log with two calls of the Skill tool that named the same skill
	path := filepath.Join(t.TempDir(), "index.db")
	saveLog(t, openIndex(t, path), "/logs/s1.jsonl", skillCall("t1", "review"), skillCall("t2", "review"))

	// When the activity is read
	a := activityOf(t, path)

	// Then the skill is loaded twice, and no call is unnamed or awaits its log
	if len(a.Skills) != 1 || a.Skills[0].Skill != "review" || a.Skills[0].Calls != 2 || len(a.UnnamedSkillCalls) != 0 || len(a.PendingSkillLoads) != 0 {
		t.Errorf("activity: %+v", a)
	}
}

func TestASkillCallThatNamedNoValidSkillIsCountedAsUnnamed(t *testing.T) {
	// Given a log with a call of the Skill tool that named no valid skill, and one load of a skill
	path := filepath.Join(t.TempDir(), "index.db")
	saveLog(t, openIndex(t, path), "/logs/s1.jsonl", skillCall("t1", ""), skillCall("t2", "review"))

	// When the activity is read
	a := activityOf(t, path)

	// Then the call is counted as unnamed, and the usage of skills is still known
	if len(a.UnnamedSkillCalls) != 1 || a.UnnamedSkillCalls[0].Calls != 1 || a.UnnamedSkillCalls[0].Harness != kernel.Claude {
		t.Errorf("unnamed skill calls: %+v", a.UnnamedSkillCalls)
	}
	if len(a.PendingSkillLoads) != 0 || len(a.Skills) != 1 || a.Skills[0].Skill != "review" {
		t.Errorf("pending %v, skills %+v", a.PendingSkillLoads, a.Skills)
	}
}

func TestNoToolButTheSkillToolOfTheHarnessLoadsASkill(t *testing.T) {
	// Given a log with a Skill tool of an MCP server and a built-in tool, neither of which loads a skill
	path := filepath.Join(t.TempDir(), "index.db")
	saveLog(t, openIndex(t, path), "/logs/s1.jsonl", domain.ToolCall{Key: "t1", At: skillCallTime, Name: "Skill", Server: "market"}, domain.ToolCall{Key: "t2", At: skillCallTime, Name: "Read"})

	// When the activity is read
	a := activityOf(t, path)

	// Then no skill is loaded, unnamed or awaited
	if len(a.Skills) != 0 || len(a.UnnamedSkillCalls) != 0 || len(a.PendingSkillLoads) != 0 {
		t.Errorf("activity: %+v", a)
	}
}

func TestASkillCallIsPendingUntilItsLogIsReadAgainByAnIndexFromBeforeSkillsWereRecorded(t *testing.T) {
	for name, build := range map[string]func(*testing.T) string{
		"made before indexes were versioned": indexBeforeVersioningAt,
		"made by the main branch": func(t *testing.T) string {
			path, db := indexAsMainMadeIt(t)
			mainWritesALog(t, db, "claude", "/logs/with-skill.jsonl", "Skill", "")
			mainWritesALog(t, db, "claude", "/logs/without.jsonl", "Read", "")
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Given an index from before skills were recorded, holding a Skill call
			path := build(t)

			// When it is opened by this build
			openIndex(t, path)

			// Then the call awaits its log: the usage of skills of Claude is unknown, not zero
			if a := activityOf(t, path); !a.PendingSkillLoads[kernel.Claude] || len(a.Skills) != 0 || len(a.UnnamedSkillCalls) != 0 {
				t.Errorf("activity: %+v", a)
			}
		})
	}
}

func TestTheUsageOfSkillsIsKnownAgainOnceTheLogOfAPendingSkillCallIsReadAgain(t *testing.T) {
	// Given an index from before skills were recorded, opened by this build, whose only Skill call awaits its log
	path := indexBeforeVersioningAt(t)
	w := openIndex(t, path)

	// When the log is read again, and the call named its skill
	saveLog(t, w, "/logs/with-skill.jsonl", skillCall("t1", "review"))

	// Then the skill is counted and nothing awaits a log
	if a := activityOf(t, path); len(a.PendingSkillLoads) != 0 || len(a.Skills) != 1 || a.Skills[0].Skill != "review" || a.Skills[0].Calls != 1 {
		t.Errorf("activity: %+v", a)
	}
}

func TestTheUsageOfSkillsIsKnownAgainWhenTheLogOfAPendingSkillCallNamedNoValidSkill(t *testing.T) {
	// Given an index from before skills were recorded, opened by this build, whose only Skill call awaits its log
	path := indexBeforeVersioningAt(t)
	w := openIndex(t, path)

	// When the log is read again, and the call named no valid skill
	saveLog(t, w, "/logs/with-skill.jsonl", skillCall("t1", ""))

	// Then the call is counted as unnamed, and nothing awaits a log
	if a := activityOf(t, path); len(a.PendingSkillLoads) != 0 || len(a.Skills) != 0 || len(a.UnnamedSkillCalls) != 1 {
		t.Errorf("activity: %+v", a)
	}
}

func TestAnIndexFromBeforeSkillsWereRecordedReadsAgainOnlyTheLogsThatNeedIt(t *testing.T) {
	// Given the index made before indexes were versioned: a log with a Skill call, a log without, a Codex log with an MCP call named in full
	path := indexBeforeVersioningAt(t)

	// When it is opened
	// Then the next indexing reads the Codex log and the log with a Skill call again, and not the other
	if got := logsToReadAgain(t, path, "/logs/with-skill.jsonl", "/logs/without.jsonl", "/logs/codex-mcp.jsonl"); !slices.Equal(got, []string{"/logs/with-skill.jsonl", "/logs/codex-mcp.jsonl"}) {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexMadeByTheMainBranchReadsAgainTheLogsWithASkillCallOrAnMCPCallNamedInFull(t *testing.T) {
	// Given the index main made: a log with a Skill call, a log without, a Codex log with an MCP call named in full, a Codex log with it split
	path, db := indexAsMainMadeIt(t)
	mainWritesALog(t, db, "claude", "/logs/with-skill.jsonl", "Skill", "")
	mainWritesALog(t, db, "claude", "/logs/without.jsonl", "Read", "")
	mainWritesALog(t, db, "codex", "/logs/codex-mcp.jsonl", "mcp__demo__read", "")
	mainWritesALog(t, db, "codex", "/logs/codex-split.jsonl", "read", "demo")

	// When it is opened
	// Then the next indexing reads again the log with a Skill call and the Codex log with the call named in full, and neither of the others
	if got := logsToReadAgain(t, path, "/logs/with-skill.jsonl", "/logs/without.jsonl", "/logs/codex-mcp.jsonl", "/logs/codex-split.jsonl"); !slices.Equal(got, []string{"/logs/with-skill.jsonl", "/logs/codex-mcp.jsonl"}) {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexUpgradedBeforeIsNotUpgradedAgain(t *testing.T) {
	// Given an index from before skills were recorded, upgraded, whose forgotten logs were read again
	path := indexBeforeVersioningAt(t)
	w := openIndex(t, path)
	saveLog(t, w, "/logs/with-skill.jsonl", skillCall("t1", "review"))
	saveLog(t, w, "/logs/codex-mcp.jsonl")
	w.Close()

	// When it is opened again
	// Then no log is forgotten a second time, and the skill call still names its skill
	if got := logsToReadAgain(t, path, "/logs/with-skill.jsonl", "/logs/codex-mcp.jsonl"); len(got) != 0 {
		t.Errorf("logs to read again = %v", got)
	}
	if a := activityOf(t, path); len(a.PendingSkillLoads) != 0 || len(a.Skills) != 1 {
		t.Errorf("activity: %+v", a)
	}
}
