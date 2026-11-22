package application

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// recordedPrompts is a prompt index that remembers what it was asked.
type recordedPrompts struct {
	filter  domain.Filter
	flagged []domain.FlaggedPrompt
	err     error
}

func (r *recordedPrompts) FlaggedPrompts(f domain.Filter) ([]domain.FlaggedPrompt, error) {
	r.filter = f
	return r.flagged, r.err
}

var day1 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

// flagged builds a flagged prompt typed minutes after day1, in a session of a folder.
func flagged(session, folder, text string, minutes int, s domain.PromptSignals) domain.FlaggedPrompt {
	id, err := domain.NewSessionID(session)
	if err != nil {
		panic(err)
	}
	return domain.FlaggedPrompt{Session: id, Harness: kernel.Claude, Title: "title of " + session, Folder: folder,
		At: day1.Add(time.Duration(minutes) * time.Minute), Text: text, Signals: s}
}

func review(t *testing.T, prompts ...domain.FlaggedPrompt) PromptReview {
	t.Helper()
	r, err := ReviewPrompts{Queries: &recordedPrompts{flagged: prompts}}.Run(Query{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReviewPromptsAsksForTheRangeOfTheQuery(t *testing.T) {
	// Given an index holding one flagged prompt
	idx := &recordedPrompts{flagged: []domain.FlaggedPrompt{flagged("claude:a", "/work", "NO NO NO", 0, domain.PromptSignals{Yelling: 3})}}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// When the flagged prompts of a harness since a date are reviewed
	got, err := ReviewPrompts{Queries: idx}.Run(Query{Since: since, Harness: kernel.Codex})

	// Then the index is asked for exactly that range, and its prompt is in the review
	if err != nil || len(got.Prompts) != 1 {
		t.Fatalf("review = %+v, %v", got, err)
	}
	if !idx.filter.Since.Equal(since) || idx.filter.Harness != kernel.Codex {
		t.Errorf("filter = %+v", idx.filter)
	}
}

func TestReviewPromptsReportsWhenTheIndexCannotBeRead(t *testing.T) {
	// Given an index that fails
	boom := errors.New("boom")

	// When the prompts are reviewed, then the caller gets the failure and no review
	got, err := ReviewPrompts{Queries: &recordedPrompts{err: boom}}.Run(Query{})
	if !errors.Is(err, boom) || len(got.Prompts) != 0 {
		t.Errorf("review = %+v, %v", got, err)
	}
}

func TestAReviewWithoutFlaggedPromptsIsEmpty(t *testing.T) {
	// Given no flagged prompt at all
	// When they are reviewed
	r := review(t)

	// Then there is nothing to list, group or count
	if len(r.Prompts) != 0 || r.Sessions != 0 || len(r.Recurring) != 0 || len(r.Folders) != 0 {
		t.Errorf("review = %+v", r)
	}
}

func TestPromptsWithoutAnySignalAreNotFlagged(t *testing.T) {
	// Given a prompt that shows no signal beside one that does
	calm := flagged("claude:a", "/work", "add a test", 0, domain.PromptSignals{})
	angry := flagged("claude:a", "/work", "WHY IS IT BROKEN", 1, domain.PromptSignals{Yelling: 4})

	// When they are reviewed
	r := review(t, calm, angry)

	// Then only the second one is in the review
	if len(r.Prompts) != 1 || r.Prompts[0].Text != "WHY IS IT BROKEN" {
		t.Errorf("prompts = %+v", r.Prompts)
	}
}

func TestThePromptsWithTheMostSignalsComeFirst(t *testing.T) {
	// Given three flagged prompts: one repeated, one with four hits, and a newer one that is also repeated
	repeated := flagged("claude:a", "/work", "again", 0, domain.PromptSignals{Repeated: true})
	loud := flagged("claude:a", "/work", "loud", 1, domain.PromptSignals{Yelling: 2, Profanity: 1, Blame: 1})
	newer := flagged("claude:b", "/work", "newer", 2, domain.PromptSignals{Repeated: true})

	// When they are reviewed
	r := review(t, repeated, newer, loud)

	// Then the loudest leads, and prompts with as many signals follow most recent first
	var order []string
	for _, p := range r.Prompts {
		order = append(order, p.Text)
	}
	if !slices.Equal(order, []string{"loud", "newer", "again"}) {
		t.Errorf("order = %v", order)
	}
}

func TestAnAskTypedInSeveralSessionsIsRecurring(t *testing.T) {
	// Given the same correction, spelled with other case and punctuation, in two sessions, plus a one-off
	first := flagged("claude:a", "/work", "Non, n'utilise pas de point-virgule !", 0, domain.PromptSignals{Negation: 1})
	second := flagged("codex:b", "/work", "non n'utilise pas de point virgule", 60, domain.PromptSignals{Negation: 1})
	once := flagged("claude:a", "/work", "wrong file, open main.go", 5, domain.PromptSignals{Negation: 1})

	// When they are reviewed
	r := review(t, first, second, once)

	// Then the ask is recurring with its count, its sessions and its dates, worded as the latest prompt did
	if len(r.Recurring) != 1 {
		t.Fatalf("recurring = %+v", r.Recurring)
	}
	a := r.Recurring[0]
	if a.Count != 2 || a.Sessions != 2 || a.Text != second.Text || !a.First.Equal(first.At) || !a.Last.Equal(second.At) {
		t.Errorf("ask = %+v", a)
	}
}

func TestAnAskRepeatedInOneSessionCountsOneSession(t *testing.T) {
	// Given the same flagged ask typed three times in one session
	p := func(m int) domain.FlaggedPrompt {
		return flagged("claude:a", "/work", "run the tests before you answer", m, domain.PromptSignals{Repeated: true})
	}

	// When it is reviewed
	r := review(t, p(0), p(1), p(2))

	// Then it is recurring three times in one session
	if len(r.Recurring) != 1 || r.Recurring[0].Count != 3 || r.Recurring[0].Sessions != 1 {
		t.Errorf("recurring = %+v", r.Recurring)
	}
}

func TestOneWordInterruptionsAreNotRecurringAsks(t *testing.T) {
	// Given "stop" typed in three sessions
	stop := func(s string) domain.FlaggedPrompt {
		return flagged(s, "/work", "STOP!", 0, domain.PromptSignals{Negation: 1})
	}

	// When they are reviewed
	r := review(t, stop("claude:a"), stop("claude:b"), stop("claude:c"))

	// Then they are listed as flagged prompts, but no instruction is suggested for them
	if len(r.Prompts) != 3 || len(r.Recurring) != 0 {
		t.Errorf("review = %+v", r)
	}
}

func TestTheMostRepeatedAskComesFirst(t *testing.T) {
	// Given an ask said twice and another said three times, the first one more recently
	a := func(s string, m int) domain.FlaggedPrompt {
		return flagged(s, "/work", "do not touch the lockfile", m, domain.PromptSignals{Negation: 1})
	}
	b := func(s string, m int) domain.FlaggedPrompt {
		return flagged(s, "/work", "keep the commits small", m, domain.PromptSignals{Blame: 1})
	}

	// When they are reviewed
	r := review(t, a("claude:a", 50), a("claude:b", 60), b("claude:a", 0), b("claude:b", 1), b("claude:c", 2))

	// Then the three-times ask leads
	if len(r.Recurring) != 2 || r.Recurring[0].Count != 3 || r.Recurring[1].Count != 2 {
		t.Errorf("recurring = %+v", r.Recurring)
	}
}

func TestFrustrationIsCountedPerFolder(t *testing.T) {
	// Given three flagged prompts in /api (two sessions), one in /web, and one with no folder
	r := review(t,
		flagged("claude:a", "/api", "no no no", 0, domain.PromptSignals{Negation: 2}),
		flagged("claude:a", "/api", "WHY IS THIS BROKEN AGAIN", 1, domain.PromptSignals{Yelling: 4, Anguish: 1}),
		flagged("claude:b", "/api", "stop it", 2, domain.PromptSignals{Negation: 1}),
		flagged("codex:c", "/web", "that is wrong", 3, domain.PromptSignals{Negation: 1}),
		flagged("pi:d", "", "wrong again", 4, domain.PromptSignals{Negation: 1}),
	)

	// When they are reviewed
	// Then the folders come most flagged first, each with its prompts, sessions and signals
	want := []domain.FolderFrustration{
		{Folder: "/api", Prompts: 3, Sessions: 2, Signals: 8},
		{Folder: "", Prompts: 1, Sessions: 1, Signals: 1},
		{Folder: "/web", Prompts: 1, Sessions: 1, Signals: 1},
	}
	if !slices.Equal(r.Folders, want) {
		t.Errorf("folders = %+v", r.Folders)
	}
	if r.Sessions != 4 {
		t.Errorf("sessions = %d", r.Sessions)
	}
}

func TestSignalTagsNameWhatAPromptShows(t *testing.T) {
	// Given a prompt with several signals, and one with none
	s := domain.PromptSignals{Yelling: 2, Negation: 1, Repeated: true}

	// When their signals are named and counted
	// Then the tags list each signal with its hits, and a repetition counts once
	if got := s.Tags(); !slices.Equal(got, []string{"yelling 2", "negation 1", "repeated"}) || s.Count() != 4 {
		t.Errorf("tags = %v, count = %d", got, s.Count())
	}
	if (domain.PromptSignals{}).Tags() != nil || (domain.PromptSignals{}).Count() != 0 {
		t.Error("a calm prompt has tags")
	}
}
