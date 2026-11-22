package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// fakePrompts is the prompt index behind ReviewPrompts; it remembers what it was asked.
type fakePrompts struct {
	flagged []domain.FlaggedPrompt
	err     error
	filters []domain.Filter
}

func (f *fakePrompts) FlaggedPrompts(_ context.Context, fl domain.Filter) ([]domain.FlaggedPrompt, error) {
	f.filters = append(f.filters, fl)
	return f.flagged, f.err
}

func flaggedAt(session, folder, text string, minutes int, s domain.PromptSignals) domain.FlaggedPrompt {
	return domain.FlaggedPrompt{Session: sid(session), Harness: kernel.Claude, Title: "title of " + session, Folder: folder,
		At: at(2, 10, minutes), Text: text, Signals: s}
}

// samplePrompts holds an ask typed in two sessions, a prompt that shouts, and one with a terminal escape and no folder.
func samplePrompts() *fakePrompts {
	return &fakePrompts{flagged: []domain.FlaggedPrompt{
		flaggedAt("claude:a", "/work/api", "WHY IS THE BUILD STILL BROKEN??", 3, domain.PromptSignals{Yelling: 5, Anguish: 2}),
		flaggedAt("claude:a", "/work/api", "Non, n'utilise pas de point-virgule", 2, domain.PromptSignals{Negation: 1}),
		flaggedAt("codex:b", "/work/api", "non n'utilise pas de point virgule !", 1, domain.PromptSignals{Negation: 1}),
		flaggedAt("pi:c", "", "\x1b[2J wrong file,\n\topen main.go", 0, domain.PromptSignals{Negation: 1}),
	}}
}

// startPrompts opens the interface on a wide terminal with the usage report, able to review prompts (and, when
// withSessions, to list sessions too).
func startPrompts(t *testing.T, prompts *fakePrompts, withSessions bool) Model {
	t.Helper()
	o := Options{Reports: build(&fakeReports{report: sampleReport()}), Prompts: reporting.ReviewPrompts{Queries: prompts}}
	if withSessions {
		s := sampleSessions()
		o.Sessions, o.Session = reporting.ListSessions{Queries: s}, reporting.ShowSession{Queries: s}
	}
	m := New(o)
	m.now = func() time.Time { return noon }
	m = send(t, m, m.Init()())
	return send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
}

// inPrompts opens the interface and switches to the prompts mode.
func inPrompts(t *testing.T, prompts *fakePrompts) Model {
	t.Helper()
	m, cmd := pressCmd(t, startPrompts(t, prompts, false), "m")
	return answer(t, m, cmd)
}

func TestPromptsModeIsOfferedOnlyWithPromptQueries(t *testing.T) {
	// Given an interface that was not given a way to review prompts
	m := start(t, &fakeReports{report: sampleReport()})

	// When the user presses m
	m, cmd := pressCmd(t, m, "m")

	// Then nothing happens, and the interface does not mention prompts
	if view := m.View(); cmd != nil || strings.Contains(view, "Prompts") || strings.Contains(view, "m prompts") {
		t.Errorf("view:\n%s", view)
	}
}

func TestTheKeyMCyclesThroughEveryModeTheInterfaceCanShow(t *testing.T) {
	// Given the interface on the usage report, able to show sessions and prompts
	m := startPrompts(t, samplePrompts(), true)
	var labels []string

	// When the user presses m four times
	for range 4 {
		view := m.View()
		for _, l := range []string{"[Usage] Sessions Prompts", "Usage [Sessions] Prompts", "Usage Sessions [Prompts]"} {
			if strings.Contains(view, l) {
				labels = append(labels, l)
			}
		}
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, "m")
		m = answer(t, m, cmd)
	}

	// Then the title names each mode in turn and the fourth press is back at the usage report
	want := "[Usage] Sessions Prompts|Usage [Sessions] Prompts|Usage Sessions [Prompts]|[Usage] Sessions Prompts"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("labels = %s", got)
	}
}

func TestTheHelpNamesTheModeTheKeyLeadsTo(t *testing.T) {
	// Given the interface able to show sessions and prompts
	m := startPrompts(t, samplePrompts(), true)

	// Then the usage help leads to sessions, sessions to prompts, and prompts back to usage
	for _, want := range []string{"m sessions", "m prompts", "m usage"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("help lacks %q:\n%s", want, m.View())
		}
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, "m")
		m = answer(t, m, cmd)
	}
}

func TestPromptsModeWithoutSessionsGoesStraightFromUsageToPrompts(t *testing.T) {
	// Given an interface that reviews prompts but lists no sessions
	m := startPrompts(t, samplePrompts(), false)

	// Then usage offers prompts only
	if view := m.View(); !strings.Contains(view, "[Usage] Prompts") || !strings.Contains(view, "m prompts") || strings.Contains(view, "Sessions") {
		t.Errorf("usage view:\n%s", view)
	}
	// And m leads there, and back
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)
	if view := m.View(); !strings.Contains(view, "Usage [Prompts]") || !strings.Contains(view, "m usage") {
		t.Errorf("prompts view:\n%s", view)
	}
}

func TestThePromptsViewShowsTheReview(t *testing.T) {
	// Given the review of four flagged prompts, two of them the same ask
	m := inPrompts(t, samplePrompts())

	// Then the view tells how many there are, the ask that came back with its hint, the folders and each prompt with its signals
	view := m.View()
	for _, want := range []string{
		"Usage [Prompts]", "4 flagged prompts in 3 sessions",
		"RECURRING", "Repeated 2 times in 2 sessions: consider writing this down.",
		"FOLDERS", "/work/api", "—",
		"PROMPTS", "WHY IS THE BUILD STILL BROKEN??", "yelling 5 · anguish 2", "title of claude:a · /work/api",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("prompts view lacks %q:\n%s", want, view)
		}
	}
}

func TestThePromptsViewNeutralisesPromptTexts(t *testing.T) {
	// Given a prompt with a terminal escape and line breaks
	m := inPrompts(t, samplePrompts())

	// Then the view shows it on one line with the escape replaced
	view := m.View()
	if strings.Contains(view, "\x1b[2J") || !strings.Contains(view, "�[2J wrong file, open main.go") {
		t.Errorf("view:\n%q", view)
	}
}

func TestThePromptsViewKeepsToTheTerminalWidth(t *testing.T) {
	// Given a narrow terminal
	m := send(t, inPrompts(t, samplePrompts()), tea.WindowSizeMsg{Width: 40, Height: 30})

	// Then no line of the view is wider than the terminal
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line of %d cells: %q", w, line)
		}
	}
}

func TestChangingThePeriodOrTheHarnessReviewsAgain(t *testing.T) {
	// Given the review on screen, the interface at noon
	prompts := samplePrompts()
	m := inPrompts(t, prompts)

	// When the user changes the period, then the harness
	m, cmd := pressCmd(t, m, "p") // all → 24h
	m = answer(t, m, cmd)
	m, cmd = pressCmd(t, m, "h") // all → first harness
	m = answer(t, m, cmd)

	// Then the index was asked for those ranges, and the title names them
	last := prompts.filters[len(prompts.filters)-1]
	if !last.Since.Equal(noon.Add(-24*time.Hour)) || last.Harness != kernel.Harnesses[0] {
		t.Errorf("filters = %+v", prompts.filters)
	}
	if view := m.View(); !strings.Contains(view, "period: 24h · harness: "+kernel.Harnesses[0].String()) {
		t.Errorf("view:\n%s", view)
	}
}

func TestAnAnswerToAnEarlierRequestIsIgnored(t *testing.T) {
	// Given a review on screen and a newer request in flight
	m := inPrompts(t, samplePrompts())
	m, _ = pressCmd(t, m, "p")

	// When the answer to the first request arrives late
	m = send(t, m, promptsMsg{seq: m.seq - 1, review: reporting.PromptReview{}})

	// Then the review on screen is untouched
	if !strings.Contains(m.View(), "4 flagged prompts") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestAReviewWithoutFlaggedPromptsSaysSo(t *testing.T) {
	// Given no flagged prompt in the range
	m := inPrompts(t, &fakePrompts{})

	// Then the view says so
	if view := m.View(); !strings.Contains(view, noFlaggedPrompts) || strings.Contains(view, "RECURRING") {
		t.Errorf("view:\n%s", view)
	}
}

func TestAReviewThatFailsShowsTheError(t *testing.T) {
	// Given an index that cannot be read
	m := inPrompts(t, &fakePrompts{err: errors.New("disk \x1b[31mfull")})

	// Then the status shows the failure, neutralised
	if view := m.View(); !strings.Contains(view, "error: disk �[31mfull") {
		t.Errorf("view:\n%s", view)
	}
}

func TestALongReviewScrollsAndStopsAtItsEnds(t *testing.T) {
	// Given sixty flagged prompts on a short terminal
	var many []domain.FlaggedPrompt
	for i := range 60 {
		many = append(many, flaggedAt("claude:a", "/work", fmt.Sprintf("prompt number %02d", i), i, domain.PromptSignals{Negation: 1}))
	}
	m := send(t, inPrompts(t, &fakePrompts{flagged: many}), tea.WindowSizeMsg{Width: 100, Height: 14})
	if !strings.Contains(m.View(), "60 flagged prompts in 1 session") || strings.Contains(m.View(), "prompt number 00") {
		t.Fatalf("top of the review:\n%s", m.View())
	}

	// When the user goes to the end, then back to the top
	m = press(t, m, "G")
	end := m.View()
	m = press(t, m, "g")

	// Then the end shows the oldest prompt, one more key goes nowhere, and the top is back
	if !strings.Contains(end, "prompt number 00") || strings.Contains(end, "60 flagged prompts") {
		t.Errorf("end of the review:\n%s", end)
	}
	if again := press(t, send(t, m, tea.KeyMsg{Type: tea.KeyEnd}), "j").View(); !strings.Contains(again, "prompt number 00") {
		t.Errorf("after the end:\n%s", again)
	}
	if !strings.Contains(m.View(), "60 flagged prompts in 1 session") {
		t.Errorf("back at the top:\n%s", m.View())
	}
}

func TestThePromptsViewShowsSignalsBesideLongMetadata(t *testing.T) {
	prompt := flaggedAt("claude:a", "/"+strings.Repeat("f", 39), "keep this file", 0, domain.PromptSignals{Negation: 1})
	prompt.Title = strings.Repeat("t", 40)
	view := strings.Join(reviewLines(domain.NewPromptReview([]domain.FlaggedPrompt{prompt}), 80), "\n")
	if !strings.Contains(view, "negation 1") {
		t.Fatalf("signal lost at 80 columns:\n%s", view)
	}
}

func TestThePromptsViewCanSwitchBetweenProjectAndAllProjects(t *testing.T) {
	project, err := kernel.NewProject("/work/api")
	if err != nil {
		t.Fatal(err)
	}
	prompts := samplePrompts()
	m := New(Options{
		Reports: build(&fakeReports{report: sampleReport()}),
		Prompts: reporting.ReviewPrompts{Queries: prompts},
		Project: project, Restricted: true,
	})
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)
	m, cmd = pressCmd(t, m, "a")
	m = answer(t, m, cmd)

	if len(prompts.filters) != 2 || prompts.filters[0].Project != project || !prompts.filters[1].Project.IsZero() {
		t.Fatalf("prompt review scopes = %+v", prompts.filters)
	}
	if !strings.Contains(m.View(), "all projects") {
		t.Fatal("title did not follow the scope of the review")
	}
}

func TestReturningToPromptsHidesThePreviousScopesReview(t *testing.T) {
	project, err := kernel.NewProject("/work/api")
	if err != nil {
		t.Fatal(err)
	}
	m := inPrompts(t, samplePrompts())
	m.project = project
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)
	m, cmd = pressCmd(t, m, "a")
	m = answer(t, m, cmd)
	m, _ = pressCmd(t, m, "m")

	if view := m.View(); strings.Contains(view, "4 flagged prompts") || !strings.Contains(view, "loading") {
		t.Fatalf("previous scope is still visible:\n%s", view)
	}
}
