package usage

import (
	"regexp"
	"strings"
	"unicode"
)

// Signals are cheap lexical hints of user frustration in a prompt (French and English).
type Signals struct {
	Repeated     bool // same text as an earlier prompt of the session, ignoring case and spacing
	Chars, Words int
	Yelling      int // words of 3+ letters written in capitals
	Profanity    int
	Anguish      int // "!!", "??", "pourquoi", "why"…
	Negation     int // "non", "stop", "pas ça"…
	Blame        int // "tu as cassé", "you broke"…
}

// ponytail: keyword lists, a model-based judge would catch sarcasm and context.
var (
	profanity = regexp.MustCompile(`(?i)\b(merde|putain|bordel|chier|connerie|fuck\w*|shit|wtf|damn|crap|bullshit)\b`)
	anguish   = regexp.MustCompile(`(?i)[!?]{2,}|\b(pourquoi|why|encore|again|toujours pas|still not)\b`)
	negation  = regexp.MustCompile(`(?i)\b(non|no|nope|stop|arr[eê]te|pas [cç]a|not that|wrong|faux)\b`)
	blame     = regexp.MustCompile(`(?i)\b(tu as (cass|oubli|supprim|ignor)\w*|t'as (cass|oubli|supprim)\w*|you (broke|forgot|deleted|ignored)|je t'ai (dit|demand))`)
)

// Analyze computes the signals for one prompt.
func Analyze(text string) Signals {
	words := strings.Fields(text)
	s := Signals{Chars: len([]rune(text)), Words: len(words)}
	// Only runs of 2+ capitalised words count, so lone acronyms (API, JSON) are not yelling.
	run := 0
	for i := 0; i <= len(words); i++ {
		if i < len(words) && isShouted(words[i]) {
			run++
			continue
		}
		if run >= 2 {
			s.Yelling += run
		}
		run = 0
	}
	s.Profanity = len(profanity.FindAllStringIndex(text, -1))
	s.Anguish = len(anguish.FindAllStringIndex(text, -1))
	s.Negation = len(negation.FindAllStringIndex(text, -1))
	s.Blame = len(blame.FindAllStringIndex(text, -1))
	return s
}

// AnalyzeSession computes the signals of every prompt of one session, in order.
func AnalyzeSession(prompts []Prompt) []Signals {
	seen := map[string]bool{}
	out := make([]Signals, len(prompts))
	for i, p := range prompts {
		out[i] = Analyze(p.Text)
		norm := strings.ToLower(strings.Join(strings.Fields(p.Text), " "))
		out[i].Repeated = seen[norm]
		seen[norm] = true
	}
	return out
}

func isShouted(w string) bool {
	letters := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			if !unicode.IsUpper(r) {
				return false
			}
			letters++
		}
	}
	return letters >= 3
}
