package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// promptsBase is when the first fixture prompt was typed.
var promptsBase = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

// reviewIndex returns the path to an index of sessions where the user corrected the agent:
//   - claude:api and codex:api, in /work/api, each with the same correction (typed a bit differently) and a calm prompt;
//   - claude:web, in /work/web, where the user shouts, and repeats an instruction twice;
//   - pi:odd, untitled and without folder, whose prompt carries terminal escapes;
//   - the subagent of claude:api, which got a flagged brief that the user did not type.
func reviewIndex(t *testing.T, firstPrompt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	save := func(h kernel.Harness, name, title, folder string, parent *domain.SessionID, minutes int, texts ...string) domain.SessionID {
		id, _ := domain.NewSessionID(h, name)
		s := domain.Session{ID: id, Harness: h, AgentType: domain.MainAgent, Title: title, Folder: folder, Start: firstPrompt.Add(time.Duration(minutes) * time.Minute), End: firstPrompt.Add(time.Duration(minutes+30) * time.Minute)}
		file := "/logs/" + h.String() + "-" + name + ".jsonl"
		if parent != nil {
			id, _ = parent.Subagent(name)
			s.ID, s.ParentID, s.AgentType = id, *parent, domain.Subagent
		}
		var prompts []domain.Prompt
		for i, text := range texts {
			prompts = append(prompts, domain.Prompt{Key: string(rune('a' + i)), At: s.Start.Add(time.Duration(i) * time.Minute), Text: text})
		}
		err := w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: file, Harness: h}, Transcript: indexing.IndexedTranscript{Transcript: domain.Transcript{Session: s, Prompts: prompts}, Signals: domain.AnalyzeSession(prompts)}}})[0]
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	api := save(kernel.Claude, "api", "migrate the billing module", "/work/api", nil, 0,
		"add a unit test for the invoice total",
		"Non, n'utilise pas de point-virgule dans ce projet",
		"WHY IS THE BUILD STILL BROKEN?? you broke the import")
	save(kernel.Claude, "kid", "billing helper", "", &api, 5, "NO NO brief for the helper")
	save(kernel.Codex, "api", "rename the invoice fields", "/work/api", nil, 120,
		"rename the fields",
		"non n'utilise pas de point virgule dans ce projet !")
	save(kernel.Claude, "web", "fix the header", "/work/web", nil, 240,
		"run the linter before you answer",
		"run the linter before you answer",
		"run the linter before you answer",
		"STOP SHOUTING NOW")
	save(kernel.Pi, "odd", "", "", nil, 300, "\x1b[2J wrong file,\topen\nmain.go please")

	return path
}

func runReview(t *testing.T, db string, args ...string) string {
	t.Helper()
	t.Setenv("TZ", "UTC")
	out, stderr, err := runCLI(t, "", append([]string{"prompts", "--db", db, "--no-sync", "--all"}, args...)...)
	if err != nil {
		t.Fatalf("prompts %v: %v\n%s", args, err, stderr)
	}
	return out
}

func TestPromptsReviewIsPrintedAsApprovedText(t *testing.T) {
	// Given the prompts of five sessions, two of them with the same correction
	db := reviewIndex(t, promptsBase)

	// When the review is printed as text
	out := runReview(t, db, "--since", "all")

	// Then it matches the approved review, and no terminal escape got through
	approve(t, "prompts.approved.txt", "prompts.approved.txt.received", []byte(out))
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape sequence in the output:\n%q", out)
	}
}

func TestPromptsReviewIsPrintedAsApprovedJSON(t *testing.T) {
	// Given the same prompts
	db := reviewIndex(t, promptsBase)

	// When the review is printed as JSON
	out := runReview(t, db, "--since", "all", "--json")

	// Then it matches the approved JSON
	approve(t, "prompts.approved.json", "prompts.approved.json.received", []byte(out))
}

func TestPromptsReviewOfAnEmptyRangeSaysSoAndPrintsEmptyLists(t *testing.T) {
	// Given a range with no prompt at all
	db := reviewIndex(t, promptsBase)

	// When the review is printed as text, then as JSON
	text := runReview(t, db, "--since", "2026-01-07")
	js := runReview(t, db, "--since", "2026-01-07", "--json")

	// Then the text says there is nothing, and the JSON lists are empty arrays, not null
	if text != "All projects\nNo flagged prompts in this range.\n" {
		t.Errorf("text = %q", text)
	}
	for _, want := range []string{`"prompts": []`, `"recurring": []`, `"byFolder": []`, `"sessions": 0`} {
		if !strings.Contains(js, want) {
			t.Errorf("json lacks %s:\n%s", want, js)
		}
	}
}

func TestPromptsReviewCanBeLimitedToOneHarness(t *testing.T) {
	// Given the sessions of three harnesses
	db := reviewIndex(t, promptsBase)

	// When the review is limited to one harness
	codex := runReview(t, db, "--since", "all", "--harness", "codex")

	// Then only that harness's prompt is listed
	if !strings.HasPrefix(codex, "All projects\n1 flagged prompt in 1 session\n") || strings.Contains(codex, "/work/web") {
		t.Errorf("codex review:\n%s", codex)
	}
}

func TestPromptsReviewCanBeLimitedToAPeriod(t *testing.T) {
	// Given sessions starting four hours before midnight, the cutoff expressible as a CLI date
	db := reviewIndex(t, promptsBase.Add(10*time.Hour))

	// When the review is limited to the prompts typed from the fifth hour on
	late := runReview(t, db, "--since", "2026-01-06")

	// Then only the late sessions' prompts are listed
	if !strings.HasPrefix(late, "All projects\n4 flagged prompts in 2 sessions\n") || strings.Contains(late, "/work/api") {
		t.Errorf("late review:\n%s", late)
	}
}
