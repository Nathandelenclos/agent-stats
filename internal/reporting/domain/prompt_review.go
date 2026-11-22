package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Count is how many signals the prompt shows: every hit of every lexical signal, and one for a repetition.
func (s PromptSignals) Count() int {
	n := s.Yelling + s.Profanity + s.Anguish + s.Negation + s.Blame
	if s.Repeated {
		n++
	}
	return n
}

// Tags names the signals the prompt shows, with their count: "yelling 2", "negation 1", "repeated".
func (s PromptSignals) Tags() []string {
	var tags []string
	for _, t := range []struct {
		name string
		n    int
	}{{"yelling", s.Yelling}, {"profanity", s.Profanity}, {"anguish", s.Anguish}, {"negation", s.Negation}, {"blame", s.Blame}} {
		if t.n > 0 {
			tags = append(tags, fmt.Sprintf("%s %d", t.name, t.n))
		}
	}
	if s.Repeated {
		tags = append(tags, "repeated")
	}
	return tags
}

// FlaggedPrompt is a prompt the user typed in a session that shows at least one frustration signal, with
// the session it belongs to.
type FlaggedPrompt struct {
	Session SessionID
	Harness kernel.Harness
	Title   string // the session's
	Folder  string // the session's
	At      time.Time
	Text    string // the first PromptTextLimit characters
	Signals PromptSignals
}

// RecurringAsk is a prompt the user typed again and again: the same text, give or take case and punctuation,
// in at least two flagged prompts. It is what is worth writing down once, in the agent's instructions.
type RecurringAsk struct {
	Text        string // as typed in the latest of the prompts
	Count       int    // how many flagged prompts say it
	Sessions    int    // how many sessions they come from
	First, Last time.Time
}

// Hint suggests what to do about the ask: write it down once, where the agent reads its instructions.
func (a RecurringAsk) Hint() string {
	return fmt.Sprintf("Repeated %s in %s: consider writing this down.", times(a.Count, "time"), times(a.Sessions, "session"))
}

func times(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// FolderFrustration is how many flagged prompts a working folder gathered.
type FolderFrustration struct {
	Folder   string // empty when the log named none
	Prompts  int
	Sessions int
	Signals  int // the signals of its prompts, added up
}

// PromptReview is what the user keeps correcting or repeating: the flagged prompts most worth a look first,
// the asks that came back, and where the frustration concentrates.
type PromptReview struct {
	Prompts   []FlaggedPrompt     // most signals first, then most recent first
	Sessions  int                 // how many sessions they come from
	Recurring []RecurringAsk      // most repeated first
	Folders   []FolderFrustration // most flagged prompts first
}

// minRecurringWords is the least words an ask has to hold to be recurring: "stop" or "non" typed ten times
// is an interruption, not an instruction to write down.
const minRecurringWords = 2

// NewPromptReview reviews prompts. A prompt without any signal is not flagged and is left out.
func NewPromptReview(prompts []FlaggedPrompt) PromptReview {
	var r PromptReview
	for _, p := range prompts {
		if p.Signals.Any() {
			r.Prompts = append(r.Prompts, p)
		}
	}
	slices.SortStableFunc(r.Prompts, func(a, b FlaggedPrompt) int {
		return cmp.Or(cmp.Compare(b.Signals.Count(), a.Signals.Count()), b.At.Compare(a.At), strings.Compare(a.Session.String(), b.Session.String()))
	})
	r.Sessions = distinctSessions(r.Prompts)
	r.Recurring = recurringAsks(r.Prompts)
	r.Folders = folderFrustrations(r.Prompts)
	return r
}

func distinctSessions(prompts []FlaggedPrompt) int {
	seen := map[SessionID]bool{}
	for _, p := range prompts {
		seen[p.Session] = true
	}
	return len(seen)
}

// askKey reads a prompt as the same ask whatever its case, spacing and punctuation: letters and digits, lowercased,
// one space between words. ponytail: exact match on that key, a similarity measure (edit distance, embeddings)
// would also group rephrasings. The indexing context's own repetition test (case and spacing only, inside one
// session) is private to that context, so this one is the reporting context's.
func askKey(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

// recurringAsks groups prompts (already ordered most recent first within equal signals) by ask and keeps the
// asks said at least twice.
func recurringAsks(prompts []FlaggedPrompt) []RecurringAsk {
	type group struct {
		ask      RecurringAsk
		sessions map[SessionID]bool
	}
	groups := map[string]*group{}
	var keys []string
	for _, p := range prompts {
		key := askKey(p.Text)
		if strings.Count(key, " ") < minRecurringWords-1 {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &group{sessions: map[SessionID]bool{}}
			groups[key] = g
			keys = append(keys, key)
		}
		g.ask.Count++
		g.sessions[p.Session] = true
		if g.ask.Count == 1 || p.At.After(g.ask.Last) {
			g.ask.Text, g.ask.Last = p.Text, p.At
		}
		if g.ask.First.IsZero() || (!p.At.IsZero() && p.At.Before(g.ask.First)) {
			g.ask.First = p.At
		}
	}
	var out []RecurringAsk
	for _, key := range keys {
		if g := groups[key]; g.ask.Count >= 2 {
			g.ask.Sessions = len(g.sessions)
			out = append(out, g.ask)
		}
	}
	slices.SortStableFunc(out, func(a, b RecurringAsk) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(b.Sessions, a.Sessions), b.Last.Compare(a.Last), strings.Compare(askKey(a.Text), askKey(b.Text)))
	})
	return out
}

func folderFrustrations(prompts []FlaggedPrompt) []FolderFrustration {
	sessions := map[string]map[SessionID]bool{}
	at := map[string]*FolderFrustration{}
	var out []*FolderFrustration
	for _, p := range prompts {
		f, ok := at[p.Folder]
		if !ok {
			f = &FolderFrustration{Folder: p.Folder}
			at[p.Folder], sessions[p.Folder] = f, map[SessionID]bool{}
			out = append(out, f)
		}
		f.Prompts++
		f.Signals += p.Signals.Count()
		sessions[p.Folder][p.Session] = true
	}
	var list []FolderFrustration
	for _, f := range out {
		f.Sessions = len(sessions[f.Folder])
		list = append(list, *f)
	}
	slices.SortStableFunc(list, func(a, b FolderFrustration) int {
		return cmp.Or(cmp.Compare(b.Prompts, a.Prompts), cmp.Compare(b.Signals, a.Signals), strings.Compare(a.Folder, b.Folder))
	})
	return list
}
