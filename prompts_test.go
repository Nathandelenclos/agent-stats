package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// promptsBase is when the first fixture prompt was typed.
var promptsBase = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

// reviewIndexed indexes neutral sessions where the user corrected the agent, and returns a reader on the index:
//   - claude:api and codex:api, in /work/api, each with the same correction (typed a bit differently) and a calm prompt;
//   - claude:web, in /work/web, where the user shouts, and repeats an instruction twice;
//   - pi:odd, untitled and without folder, whose prompt carries terminal escapes;
//   - the subagent of claude:api, which got a flagged brief that the user did not type.
func reviewIndexed(t *testing.T) *usagedb.SQLiteUsage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	save := func(h kernel.Harness, name, title, folder string, parent *domain.SessionID, minutes int, texts ...string) domain.SessionID {
		id, _ := domain.NewSessionID(h, name)
		s := domain.Session{ID: id, Harness: h, AgentType: domain.MainAgent, Title: title, Folder: folder, Start: promptsBase.Add(time.Duration(minutes) * time.Minute), End: promptsBase.Add(time.Duration(minutes+30) * time.Minute)}
		file := "/logs/" + h.String() + "-" + name + ".jsonl"
		if parent != nil {
			id, _ = parent.Subagent(name)
			s.ID, s.ParentID, s.AgentType = id, *parent, domain.Subagent
		}
		var prompts []domain.Prompt
		for i, text := range texts {
			prompts = append(prompts, domain.Prompt{Key: string(rune('a' + i)), At: s.Start.Add(time.Duration(i) * time.Minute), Text: text})
		}
		err := w.Save(indexing.LogFile{Path: file, Harness: h}, indexing.IndexedTranscript{Transcript: domain.Transcript{Session: s, Prompts: prompts}, Signals: domain.AnalyzeSession(prompts)})
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

	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { usage.Close() })
	return usage
}

// approve compares got with testdata/name; on a difference it writes testdata/name.received for review.
func approve(t *testing.T, name string, got []byte) {
	t.Helper()
	approved, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil || !bytes.Equal(approved, got) {
		if werr := os.WriteFile(filepath.Join("testdata", name+".received"), got, 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Fatalf("output differs from testdata/%s (%v); see testdata/%s.received", name, err, name)
	}
	os.Remove(filepath.Join("testdata", name+".received"))
}

// inUTC makes the times of the review read the same on every machine.
func inUTC(t *testing.T) {
	t.Helper()
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
}

func TestPromptsReviewIsPrintedAsApprovedText(t *testing.T) {
	// Given the prompts of five sessions, two of them with the same correction
	inUTC(t)
	usage := reviewIndexed(t)

	// When the review is printed as text
	var out bytes.Buffer
	if err := runPrompts(&out, usage.Prompts(), reporting.Query{}, false); err != nil {
		t.Fatal(err)
	}

	// Then it matches the approved review, and no terminal escape got through
	approve(t, "prompts.approved.txt", out.Bytes())
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("escape sequence in the output:\n%q", out.String())
	}
}

func TestPromptsReviewIsPrintedAsApprovedJSON(t *testing.T) {
	// Given the same prompts
	inUTC(t)
	usage := reviewIndexed(t)

	// When the review is printed as JSON
	var out bytes.Buffer
	if err := runPrompts(&out, usage.Prompts(), reporting.Query{}, true); err != nil {
		t.Fatal(err)
	}

	// Then it matches the approved JSON
	approve(t, "prompts.approved.json", out.Bytes())
}

func TestPromptsReviewOfAnEmptyRangeSaysSoAndPrintsEmptyLists(t *testing.T) {
	// Given a range with no prompt at all
	usage := reviewIndexed(t)
	q := reporting.Query{Since: promptsBase.AddDate(1, 0, 0)}

	// When the review is printed as text, then as JSON
	var text, js bytes.Buffer
	if err := runPrompts(&text, usage.Prompts(), q, false); err != nil {
		t.Fatal(err)
	}
	if err := runPrompts(&js, usage.Prompts(), q, true); err != nil {
		t.Fatal(err)
	}

	// Then the text says there is nothing, and the JSON lists are empty arrays, not null
	if text.String() != "No flagged prompts in this range.\n" {
		t.Errorf("text = %q", text.String())
	}
	for _, want := range []string{`"prompts": []`, `"recurring": []`, `"byFolder": []`, `"sessions": 0`} {
		if !strings.Contains(js.String(), want) {
			t.Errorf("json lacks %s:\n%s", want, js.String())
		}
	}
}

func TestPromptsReviewCanBeLimitedToOneHarness(t *testing.T) {
	// Given the sessions of three harnesses
	usage := reviewIndexed(t)

	// When the review is limited to one harness
	var codex bytes.Buffer
	if err := runPrompts(&codex, usage.Prompts(), reporting.Query{Harness: kernel.Codex}, false); err != nil {
		t.Fatal(err)
	}

	// Then only that harness's prompt is listed
	if !strings.HasPrefix(codex.String(), "1 flagged prompt in 1 session\n") || strings.Contains(codex.String(), "/work/web") {
		t.Errorf("codex review:\n%s", codex.String())
	}
}

func TestPromptsReviewCanBeLimitedToAPeriod(t *testing.T) {
	// Given the sessions of the first five hours
	usage := reviewIndexed(t)

	// When the review is limited to the prompts typed from the fifth hour on
	var late bytes.Buffer
	if err := runPrompts(&late, usage.Prompts(), reporting.Query{Since: promptsBase.Add(4 * time.Hour)}, false); err != nil {
		t.Fatal(err)
	}

	// Then only the late sessions' prompts are listed
	if !strings.HasPrefix(late.String(), "4 flagged prompts in 2 sessions\n") || strings.Contains(late.String(), "/work/api") {
		t.Errorf("late review:\n%s", late.String())
	}
}

func TestPromptsSubcommandTakesItsFlagsAfterItsName(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
		err  bool
	}{
		{"no flag", []string{"prompts"}, "7d", false},
		{"flags after the name", []string{"prompts", "--since", "30d", "--harness", "codex", "--json"}, "30d", false},
		{"flags before the name", []string{"--since", "24h", "prompts"}, "24h", false},
		{"another word", []string{"prompts", "extra"}, "", true},
	} {
		// Given the command line the user typed
		fs := flag.NewFlagSet("agent-stats", flag.ContinueOnError)
		since := fs.String("since", "", "")
		fs.String("harness", "", "")
		fs.Bool("json", false, "")
		fs.SetOutput(&bytes.Buffer{})
		if err := fs.Parse(c.args); err != nil {
			t.Fatal(err)
		}

		// When the subcommand is read
		sub, err := subcommand(fs)

		// Then the subcommand is recognised with its flags, or refused with a stray word
		if (err != nil) != c.err {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if err == nil {
			got, _ := sinceOf(sub, *since, promptsBase)
			want, _ := parseSince(c.want, promptsBase)
			if sub != "prompts" || !got.Equal(want) {
				t.Errorf("%s: sub = %q, since = %v, want %v", c.name, sub, got, want)
			}
		}
	}
}

func TestOtherCommandLinesAreNoSubcommand(t *testing.T) {
	// Given the default command, with and without a word the program never knew
	for _, args := range [][]string{{}, {"--json"}, {"unknown"}} {
		fs := flag.NewFlagSet("agent-stats", flag.ContinueOnError)
		fs.Bool("json", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}

		// When the subcommand is read, then there is none and the default command keeps its way
		if sub, err := subcommand(fs); sub != "" || err != nil {
			t.Errorf("%v: sub = %q, %v", args, sub, err)
		}
	}
}

func TestPromptsPeriodDefaultsToAWeek(t *testing.T) {
	// Given no --since, when the review's period is read, then it is the last week
	if got, err := sinceOf("prompts", "", promptsBase); err != nil || !got.Equal(promptsBase.AddDate(0, 0, -7)) {
		t.Errorf("default = %v, %v", got, err)
	}
}

func TestPromptsPeriodAcceptsAll(t *testing.T) {
	// Given --since all, when the review's period is read, then it is every prompt
	if got, err := sinceOf("prompts", "all", promptsBase); err != nil || !got.IsZero() {
		t.Errorf("all = %v, %v", got, err)
	}
}

func TestTheDefaultCommandKeepsItsOwnSinceRules(t *testing.T) {
	// Given the default command, an empty --since still reads as everything
	if got, err := sinceOf("", "", promptsBase); err != nil || !got.IsZero() {
		t.Errorf("empty = %v, %v", got, err)
	}
	// And "all" is still refused there
	if _, err := sinceOf("", "all", promptsBase); err == nil {
		t.Error("the default command accepted all")
	}
}
