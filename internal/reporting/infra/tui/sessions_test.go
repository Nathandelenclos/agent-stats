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

// fakeSessions is the session index behind ListSessions and ShowSession; it remembers what it was asked.
type fakeSessions struct {
	list    []reporting.SessionSummary
	details map[string]reporting.SessionDetail
	err     error
	filters []domain.Filter
	asked   []string
	// inProject, when it describes a session for a project, is the description of the sessions as that project lists them.
	inProject map[kernel.Project]map[string]reporting.SessionDetail
}

func (f *fakeSessions) Sessions(_ context.Context, fl domain.Filter) ([]domain.SessionSummary, error) {
	f.filters = append(f.filters, fl)
	return f.list, f.err
}

func (f *fakeSessions) Session(_ context.Context, id domain.SessionID, p kernel.Project) (domain.SessionDetail, error) {
	f.asked = append(f.asked, id.String())
	if d, ok := f.inProject[p][id.String()]; ok {
		return d, nil
	}
	if d, ok := f.details[id.String()]; ok {
		return d, nil
	}
	return domain.SessionDetail{}, domain.ErrSessionNotFound
}

func sid(s string) reporting.SessionID {
	id, err := reporting.NewSessionID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func at(day, hour, min int) time.Time { return time.Date(2026, 1, day, hour, min, 0, 0, time.UTC) }

// session builds a listed session: a title, a harness, its cost, requests, prompts and how long it lasted.
func session(id, title string, h kernel.Harness, cost float64, requests, prompts, promptChars int64, last time.Time, lasted time.Duration) reporting.SessionSummary {
	return reporting.SessionSummary{
		ID: sid(id), Harness: h, Title: title, Folder: "/work/" + id, AgentType: "main", Start: last.Add(-lasted), End: last,
		Usage:       reporting.Usage{Requests: requests, Cost: reporting.USD(cost)},
		PromptCount: prompts, PromptChars: promptChars,
		Models: []domain.UsageGroup{{Group: "big-model", Usage: reporting.Usage{Requests: requests, Cost: reporting.USD(cost)}}},
	}
}

// sampleSessions is newest first, as the index lists them: a long cheap one, a short expensive one on a big model, and an untitled one.
func sampleSessions() *fakeSessions {
	cheap := session("codex:cheap", "refactor the billing module", kernel.Codex, 0.4, 40, 10, 2000, at(3, 12, 0), 90*time.Minute)
	dear := session("claude:dear", "quick fix", kernel.Claude, 12.5, 6, 3, 90, at(2, 12, 0), 5*time.Minute)
	dear.Models = []domain.UsageGroup{
		{Group: "opus", Usage: reporting.Usage{Requests: 4, Cost: 12}},
		{Group: "haiku", Usage: reporting.Usage{Requests: 2, Cost: 0.5}},
		{Group: "tiny", Usage: reporting.Usage{Requests: 1}},
	}
	dear.SubagentCount = 2
	bare := session("pi:bare", "", kernel.Pi, 0, 0, 0, 0, time.Time{}, 0)
	bare.Start, bare.Models = time.Time{}, nil
	return &fakeSessions{list: []reporting.SessionSummary{cheap, dear, bare}, details: map[string]reporting.SessionDetail{}}
}

func startSessions(t *testing.T, sessions *fakeSessions) Model {
	t.Helper()
	m := New(Options{Reports: build(&fakeReports{report: sampleReport()}),
		Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions}})
	m.now = func() time.Time { return noon }
	return send(t, m, m.Init()())
}

// inSessions opens the interface on a wide terminal and switches to the sessions mode.
func inSessions(t *testing.T, sessions *fakeSessions) Model {
	t.Helper()
	m := send(t, startSessions(t, sessions), tea.WindowSizeMsg{Width: 220, Height: 30})
	m, cmd := pressCmd(t, m, "m")
	return answer(t, m, cmd)
}

func pressType(t *testing.T, m Model, k tea.KeyType) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: k})
	return next.(Model), cmd
}

// openDetail selects the session on the first row and opens its detail, delivering the answer.
func openDetail(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := pressType(t, m, tea.KeyEnter)
	return answer(t, m, cmd)
}

func TestSessionsModeIsOfferedOnlyWithSessionQueries(t *testing.T) {
	// Given an interface that was not given a way to read sessions
	m := start(t, &fakeReports{report: sampleReport()})

	// When the user presses m
	m, cmd := pressCmd(t, m, "m")

	// Then nothing happens, and the interface does not mention sessions
	view := m.View()
	if cmd != nil || strings.Contains(view, "Sessions") || strings.Contains(view, "m sessions") || !strings.Contains(view, "HARNESS") {
		t.Errorf("view:\n%s", view)
	}
}

func TestTheModeLabelAndTheKeyTellHowToSwitch(t *testing.T) {
	// Given the interface on the usage report
	m := startSessions(t, sampleSessions())

	// Then the title names the mode and the help names the key that leads to the other one
	view := m.View()
	for _, want := range []string{"[Usage] Sessions", "m sessions", "period: all · harness: all"} {
		if !strings.Contains(view, want) {
			t.Errorf("usage view lacks %q:\n%s", want, view)
		}
	}

	// When the user presses m
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)

	// Then the sessions are shown under a label that says so, and m leads back
	view = m.View()
	for _, want := range []string{"Usage [Sessions]", "m usage", "enter open", "3 sessions · 46 requests · $12.90", "most recent first"} {
		if !strings.Contains(view, want) {
			t.Errorf("sessions view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "tab/←→ tabs") {
		t.Errorf("the usage keys are still advertised:\n%s", view)
	}
	m, cmd = pressCmd(t, m, "m")
	m = answer(t, m, cmd)
	if view := m.View(); !strings.Contains(view, "[Usage] Sessions") || !strings.Contains(view, "HARNESS") {
		t.Errorf("back on usage:\n%s", view)
	}
}

func TestSessionsTableShowsWhatToCompareCosts(t *testing.T) {
	// Given sessions that differ in cost, models and the length of the prompts
	m := inSessions(t, sampleSessions())

	// Then each row shows its title, harness, start, duration, requests, cost, model mix, prompts and their average length
	view := m.View()
	for _, want := range []string{
		"TITLE", "HARNESS", "START", "DURATION", "REQUESTS", "COST", "MODELS", "PROMPTS", "AVG PROMPT", "AGENT", "SUBAGENTS", "FOLDER",
		"refactor the billing module", "codex", "2026-01-03 10:30", "1h30m", "$0.40", "big-model 100%", "200",
		"quick fix", "claude", "5m", "$12.50", "opus 96% +2", "30", "main",
		"(untitled)", "/work/codex:cheap",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// And what the logs did not state reads as unknown
	if !strings.Contains(view, "—") {
		t.Errorf("no placeholder for an unknown start:\n%s", view)
	}
}

func TestOnAn80ColumnTerminalTheSessionsKeepTheirCostAndPromptFigures(t *testing.T) {
	// Given the sessions on a terminal of the classic 80 columns
	m := send(t, inSessions(t, sampleSessions()), tea.WindowSizeMsg{Width: 80, Height: 30})

	// Then the cost, the prompts and the average prompt length of each session are on screen, headers included
	view := m.View()
	for _, want := range []string{"COST", "PROMPTS", "AVG PROMPT", "$0.40", "$12.50", "200", "30"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	var cheap string
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(l), ">"), "refactor") {
			cheap = l
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(cheap), " "), "$0.40 10 200") {
		t.Errorf("the row of the first session does not show cost, prompts and average prompt: %q", cheap)
	}
	// And the figures come with the session's title, whatever the terminal could not hold was left out whole
	if !strings.Contains(view, "refactor the billing") || !strings.Contains(view, "quick fix") {
		t.Errorf("titles lost:\n%s", view)
	}
	for _, l := range strings.Split(view, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("line of %d columns on an 80-column terminal: %q", w, l)
		}
	}
}

func TestASortOnAColumnTheNarrowedTerminalCannotHoldIsDropped(t *testing.T) {
	// Given the sessions sorted by their folder, the last column, on a wide terminal
	m := press(t, inSessions(t, sampleSessions()), "s", "s", "s", "s", "s", "s", "s", "s", "s", "s", "s", "s")
	if view := m.View(); !strings.Contains(view, "FOLDER▲") {
		t.Fatalf("not sorted by folder:\n%s", view)
	}

	// When the terminal shrinks to 80 columns, which cannot hold that column
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})

	// Then the sessions are back in the order of the index
	view := m.View()
	if !strings.Contains(view, "most recent first") || strings.ContainsAny(view, "▲▼") {
		t.Errorf("sort left behind:\n%s", view)
	}
	if got := order(view, "refactor", "quick fix", "(untitled)"); got != "refactor quick fix (untitled)" {
		t.Errorf("order = %q", got)
	}
}

func TestTheSortKeyOnANarrowTerminalSortsTheColumnsItShows(t *testing.T) {
	// Given the sessions on an 80-column terminal
	m := send(t, inSessions(t, sampleSessions()), tea.WindowSizeMsg{Width: 80, Height: 30})

	// When the user presses s twice
	view := press(t, m, "s", "s").View()

	// Then the sessions are sorted by the second column, the cost
	if !strings.Contains(view, "COST▼") {
		t.Errorf("s does not sort by the second column:\n%s", view)
	}
}

func TestSessionsFollowThePeriodAndHarnessFilters(t *testing.T) {
	sessions := sampleSessions()
	m := inSessions(t, sessions)

	// When the user cycles the period and the harness
	m, cmd := pressCmd(t, m, "p")
	m = answer(t, m, cmd)
	m, cmd = pressCmd(t, m, "h")
	m = answer(t, m, cmd)

	// Then the sessions are asked for with those filters, and the title names them
	got := sessions.filters[len(sessions.filters)-1]
	if !got.Since.Equal(noon.Add(-24*time.Hour)) || got.Harness != kernel.Claude {
		t.Errorf("filter = %+v", got)
	}
	if view := m.View(); !strings.Contains(view, "period: 24h · harness: claude") {
		t.Errorf("title:\n%s", view)
	}
}

func TestSessionsSortByCost(t *testing.T) {
	m := inSessions(t, sampleSessions())
	names := []string{"refactor the billing", "quick fix", "(untitled)"}
	if got := order(m.View(), names...); got != "refactor the billing quick fix (untitled)" {
		t.Fatalf("initial order = %q, want the index's", got)
	}

	// When the user sorts by the COST column (the second), the most expensive comes first
	m = press(t, m, "s", "s")
	if got, view := order(m.View(), names...), m.View(); got != "quick fix refactor the billing (untitled)" || !strings.Contains(view, "COST▼") || !strings.Contains(view, "sorted by COST▼") {
		t.Errorf("by cost: %q\n%s", got, view)
	}

	// When the user reverses it, the cheapest comes first
	m = press(t, m, "o")
	if got := order(m.View(), names...); got != "(untitled) refactor the billing quick fix" {
		t.Errorf("reversed: %q", got)
	}

	// And the usage tabs kept their own order
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)
	if strings.ContainsAny(m.View(), "▲▼") {
		t.Errorf("the usage table was sorted:\n%s", m.View())
	}
}

func TestSessionsSortByAveragePromptLength(t *testing.T) {
	m := inSessions(t, sampleSessions())
	// When the user sorts by AVG PROMPT (the fourth column), the longest prompts come first
	m = press(t, m, "s", "s", "s", "s")
	if got, view := order(m.View(), "refactor the billing", "quick fix"), m.View(); got != "refactor the billing quick fix" || !strings.Contains(view, "AVG PROMPT▼") {
		t.Errorf("by average prompt: %q\n%s", got, view)
	}
}

func TestEnterOpensTheSelectedSessionWhateverTheSort(t *testing.T) {
	// Given sessions sorted by cost, the first row being the most expensive session
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := press(t, inSessions(t, sessions), "s", "s")

	// When the user presses Enter
	m, cmd := pressType(t, m, tea.KeyEnter)

	// Then that session's detail is asked for
	if cmd == nil {
		t.Fatal("no detail was requested")
	}
	m = send(t, m, cmd())
	if got := sessions.asked; len(got) != 1 || got[0] != "claude:dear" {
		t.Errorf("asked for %v", got)
	}
	if view := m.View(); !strings.Contains(view, "quick fix") {
		t.Errorf("the detail does not name the session:\n%s", view)
	}
}

func detailOf(s reporting.SessionSummary) reporting.SessionDetail {
	return reporting.SessionDetail{
		SessionSummary: s,
		Tools:          []domain.ToolUsage{{Name: "Bash", Calls: 42, Errors: 3}, {Name: "search", Server: "grace", Calls: 7}},
		Subagents:      []domain.SubagentUsage{{AgentType: "Explore", Title: "look around", Usage: reporting.Usage{Requests: 5, Cost: 1.25}}},
		Prompts: []domain.Prompt{
			{At: at(2, 11, 55), Text: "please fix the build", Chars: 20, Words: 4},
			{At: at(2, 11, 58), Text: "WHY IS THIS STILL BROKEN?? no, stop", Chars: 35, Words: 7,
				Signals: domain.PromptSignals{Yelling: 4, Anguish: 3, Negation: 2, Repeated: true}},
		},
	}
}

func TestDetailShowsWhatTheSessionDid(t *testing.T) {
	// Given a session that used several models and tools, started a subagent and received two prompts, one of them angry
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 100, Height: 40})
	m = press(t, m, "s", "s")

	// When the user opens it
	m = openDetail(t, m)

	// Then the figures of the session come first
	view := m.View()
	for _, want := range []string{
		"quick fix", "claude · /work/claude:dear · 2026-01-02 11:55 → 2026-01-02 12:00 (5m)",
		"$12.50 · 6 requests (0 failed) · 2 subagents · 3 prompts, average 30 characters",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail lacks %q:\n%s", want, view)
		}
	}
	// And the requests by model, with each model's share of the cost
	for _, want := range []string{"MODEL", "opus", "96%", "haiku", "4%", "SHARE"} {
		if !strings.Contains(view, want) {
			t.Errorf("models lack %q:\n%s", want, view)
		}
	}
	// And the tools, the subagents and the prompts in order, with their signals
	for _, want := range []string{
		"TOOLS", "Bash", "42", "grace",
		"SUBAGENTS", "Explore", "look around", "$1.25",
		"PROMPTS", "#1 · 01-02 11:55:00 · 20 characters", "please fix the build",
		"#2 · 01-02 11:58:00 · 35 characters · yelling 4 · anguish 3 · negation 2 · repeated", "WHY IS THIS STILL BROKEN?? no, stop",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "#1 · 01-02 11:55:00 · 20 characters ·") {
		t.Errorf("a calm prompt shows signals:\n%s", view)
	}
	if got := order(view, "MODEL", "TOOLS", "SUBAGENTS", "PROMPTS"); got != "MODEL TOOLS SUBAGENTS PROMPTS" {
		t.Errorf("sections out of order: %q", got)
	}
}

func TestDetailListsEveryToolAndSubagent(t *testing.T) {
	// Given a session that called 20 different tools and started 20 subagents
	d := detailOf(sampleSessions().list[1])
	d.Tools, d.Subagents = nil, nil
	for i := 1; i <= 20; i++ {
		d.Tools = append(d.Tools, domain.ToolUsage{Name: fmt.Sprintf("tool-%02d", i), Calls: int64(100 - i)})
		d.Subagents = append(d.Subagents, domain.SubagentUsage{AgentType: "Explore", Title: fmt.Sprintf("agent-%02d", i)})
	}

	// When its detail is drawn
	body := strings.Join(detailLines(d, 100), "\n")

	// Then every tool and every subagent is listed, with nothing left out
	for _, want := range []string{"tool-01", "tool-20", "agent-01", "agent-20"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	if strings.Contains(body, "more") {
		t.Errorf("detail leaves rows out:\n%s", body)
	}
}

func TestEscapeGoesBackToTheSessions(t *testing.T) {
	// Given the detail of the second session on screen, reached with the cursor moved down
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := press(t, inSessions(t, sessions), "down")
	m = openDetail(t, m)
	if !strings.Contains(m.View(), "esc back to sessions") {
		t.Fatalf("detail help:\n%s", m.View())
	}

	// When the user presses Esc
	m, cmd := pressType(t, m, tea.KeyEsc)

	// Then the list is back and nothing was requested
	view := m.View()
	if cmd != nil || !strings.Contains(view, "TITLE") || strings.Contains(view, "esc back") {
		t.Errorf("after Esc (cmd %v):\n%s", cmd != nil, view)
	}

	// And opening again the selected row shows the same session
	m = openDetail(t, m)
	if view := m.View(); !strings.Contains(view, "$12.50 · 6 requests") {
		t.Errorf("another session was selected:\n%s", view)
	}
}

func TestEscapeDoesNothingOnTheList(t *testing.T) {
	m := inSessions(t, sampleSessions())
	m, cmd := pressType(t, m, tea.KeyEsc)
	if cmd != nil || !strings.Contains(m.View(), "Usage [Sessions]") {
		t.Errorf("Esc on the list changed the view:\n%s", m.View())
	}
}

func TestDetailTextCannotDriveTheTerminal(t *testing.T) {
	// Given a session whose title, folder, models, tools, subagent and prompt hold escape sequences and line breaks
	sessions := sampleSessions()
	evil := "evil\x1b[2J\x07\nline"
	d := detailOf(sessions.list[0])
	d.Title, d.Folder = evil, evil
	d.Models = []domain.UsageGroup{{Group: evil, Usage: reporting.Usage{Requests: 1}}}
	d.Tools = []domain.ToolUsage{{Name: evil, Server: evil, Calls: 1}}
	d.Subagents = []domain.SubagentUsage{{AgentType: evil, Title: evil}}
	d.Prompts = []domain.Prompt{{Text: "hi\x1b]0;owned\x07\r\n\there \u009b31m", Chars: 5}}
	sessions.details["codex:cheap"] = d
	sessions.list[0].Title = evil

	// When the list and the detail are shown
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 100, Height: 40})
	list := m.View()
	m = openDetail(t, m)

	// Then no control character reaches the terminal
	for _, view := range []string{list, m.View()} {
		if strings.ContainsAny(view, "\x07\u009b\r\t") || strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]0") {
			t.Errorf("control characters reached the view: %q", view)
		}
	}
	if !strings.Contains(m.View(), "hi�]0;owned� here �31m") {
		t.Errorf("the prompt is missing:\n%s", m.View())
	}
}

// aLongSessionOnASmallTerminal opens the detail of a session with sixty prompts on a 60x14 terminal.
func aLongSessionOnASmallTerminal(t *testing.T) Model {
	t.Helper()
	sessions := sampleSessions()
	d := detailOf(sessions.list[0])
	d.Prompts = nil
	for i := 1; i <= 60; i++ {
		d.Prompts = append(d.Prompts, domain.Prompt{At: at(3, 10, i%60), Text: fmt.Sprintf("prompt number %d %s", i, strings.Repeat("long ", 40)), Chars: 220})
	}
	sessions.details["codex:cheap"] = d
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 60, Height: 14})
	return openDetail(t, m)
}

// fitsA14By60Terminal fails when the view is taller or wider than the terminal.
func fitsA14By60Terminal(t *testing.T, m Model) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) > 14 {
		t.Errorf("view is %d lines on a 14-line terminal", len(lines))
	}
	for _, l := range lines {
		if w := len([]rune(l)); w > 60 {
			t.Errorf("line of %d cells on a 60-cell terminal: %q", w, l)
		}
	}
}

func TestALongSessionFitsASmallTerminalFromItsTop(t *testing.T) {
	m := aLongSessionOnASmallTerminal(t)
	fitsA14By60Terminal(t, m)
	if view := m.View(); !strings.Contains(view, "refactor the billing module") || strings.Contains(view, "prompt number 60") {
		t.Errorf("top of the detail:\n%s", view)
	}
}

func TestEndAndHomeTakeTheDetailToItsLastPromptAndBack(t *testing.T) {
	m := aLongSessionOnASmallTerminal(t)

	// When the user goes to the end, then the last prompt is shown
	m, _ = pressType(t, m, tea.KeyEnd)
	fitsA14By60Terminal(t, m)
	if view := m.View(); !strings.Contains(view, "prompt number 60") || strings.Contains(view, "TOOLS") {
		t.Errorf("end of the detail:\n%s", view)
	}

	// When the user goes home, then the top is back
	m, _ = pressType(t, m, tea.KeyHome)
	if view := m.View(); !strings.Contains(view, "TOOLS") || !strings.Contains(view, "refactor the billing module") {
		t.Errorf("home:\n%s", view)
	}
}

func TestPagingMovesTheDetailByAScreenAndBack(t *testing.T) {
	m := aLongSessionOnASmallTerminal(t)

	// When the user pages down, then the first lines are gone
	m, _ = pressType(t, m, tea.KeyPgDown)
	if view := m.View(); strings.Contains(view, "refactor the billing module") || !strings.Contains(view, "SUBAGENTS") {
		t.Errorf("after a page down:\n%s", view)
	}

	// When the user pages up, then the top is back
	m, _ = pressType(t, m, tea.KeyPgUp)
	if view := m.View(); !strings.Contains(view, "refactor the billing module") {
		t.Errorf("after a page up:\n%s", view)
	}
}

func TestDetailOfAnUnknownSessionSaysSoAndEscapeStillWorks(t *testing.T) {
	// Given a list that names a session its detail query no longer finds (the index changed meanwhile)
	m := inSessions(t, sampleSessions())

	// When the user opens it
	m = openDetail(t, m)

	// Then the error is shown, and Esc leads back to the list
	if view := m.View(); !strings.Contains(view, "error: session not found") {
		t.Errorf("detail:\n%s", view)
	}
	m, _ = pressType(t, m, tea.KeyEsc)
	if view := m.View(); !strings.Contains(view, "TITLE") {
		t.Errorf("after Esc:\n%s", view)
	}
}

func TestAnAnswerToAnEarlierSessionsRequestIsIgnored(t *testing.T) {
	// Given a newer request for the sessions, asked by changing the period
	m, cmd := pressCmd(t, inSessions(t, sampleSessions()), "p")

	// When the answer to the earlier request arrives
	m = send(t, m, sessionsMsg{seq: m.seq - 1})

	// Then it does not replace the sessions on screen
	if view := m.View(); !strings.Contains(view, "quick fix") {
		t.Errorf("a stale answer emptied the list:\n%s", view)
	}
	m = answer(t, m, cmd)
	if view := m.View(); !strings.Contains(view, "quick fix") {
		t.Errorf("the answer to the newer request is lost:\n%s", view)
	}
}

func TestAnEarlierAnswerDoesNotReplaceTheRefreshedDetail(t *testing.T) {
	// Given a session opened while its detail was still loading, then re-indexed so that it was asked for again
	sessions := sampleSessions()
	sessions.details["codex:cheap"] = detailOf(sessions.list[0])
	m, open := pressType(t, inSessions(t, sessions), tea.KeyEnter)
	initial := open() // the first answer: 40 requests, held back
	grown := detailOf(sessions.list[0])
	grown.Requests = 41
	sessions.details["codex:cheap"] = grown
	next, refresh := m.Update(syncDoneMsg{})
	m = next.(Model)
	for _, c := range refresh().(tea.BatchMsg) {
		m = send(t, m, c())
	}
	if view := m.View(); !strings.Contains(view, "41 requests") {
		t.Fatalf("the refreshed detail is not shown:\n%s", view)
	}

	// When the first answer finally arrives
	m = send(t, m, initial)

	// Then the detail on screen is still the refreshed one
	if view := m.View(); !strings.Contains(view, "41 requests") || strings.Contains(view, "40 requests") {
		t.Errorf("a late answer replaced the refreshed detail:\n%s", view)
	}
}

func TestTheDetailOfASessionThatIsNoLongerOpenIsDropped(t *testing.T) {
	// Given the list of sessions, no detail open
	sessions := sampleSessions()
	m := inSessions(t, sessions)

	// When the detail of a session arrives late
	m = send(t, m, detailMsg{id: sid("claude:dear"), detail: detailOf(sessions.list[1])})

	// Then the detail does not open by itself
	if view := m.View(); strings.Contains(view, "esc back") {
		t.Errorf("a late detail opened itself:\n%s", view)
	}
}

func TestNoSessionsInTheRangeSaysSo(t *testing.T) {
	m := inSessions(t, &fakeSessions{})
	if view := m.View(); !strings.Contains(view, "No sessions in this range.") || strings.Contains(view, "TITLE") {
		t.Errorf("empty sessions:\n%s", view)
	}
	if m2, cmd := pressType(t, m, tea.KeyEnter); cmd != nil {
		t.Errorf("Enter on an empty list asked for a detail: %v", m2.View())
	}
}

func TestFailedSessionsKeepThePreviousListAndShowTheError(t *testing.T) {
	m := inSessions(t, sampleSessions())
	m = send(t, m, sessionsMsg{seq: m.seq, err: errors.New("database is locked")})
	if view := m.View(); !strings.Contains(view, "error: database is locked") || !strings.Contains(view, "quick fix") {
		t.Errorf("view after a failed list:\n%s", view)
	}
}

func TestSessionsViewFitsTheTerminal(t *testing.T) {
	sessions := sampleSessions()
	for i := 0; i < 50; i++ {
		sessions.list = append(sessions.list, session(fmt.Sprintf("claude:s%d", i), strings.Repeat("title ", 30), kernel.Claude, 1, 1, 1, 10, at(1, 1, 1), time.Hour))
	}
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 70, Height: 15})
	lines := strings.Split(m.View(), "\n")
	if len(lines) > 15 {
		t.Errorf("view is %d lines on a 15-line terminal", len(lines))
	}
	for _, l := range lines {
		if w := len([]rune(l)); w > 70 {
			t.Errorf("line of %d cells on a 70-cell terminal: %q", w, l)
		}
	}
}

func TestSessionsIndexedMeanwhileShowAfterIndexing(t *testing.T) {
	// Given the sessions on screen while indexing runs
	sessions := sampleSessions()
	m := New(Options{Reports: build(&fakeReports{report: sampleReport()}), Sync: func(context.Context, func(int)) (string, error) { return "", nil },
		Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions}})
	m = send(t, m, tea.WindowSizeMsg{Width: 220, Height: 30})
	m, cmd := pressCmd(t, send(t, m, m.load()()), "m")
	m = answer(t, m, cmd)
	if strings.Contains(m.View(), "fresh session") {
		t.Fatalf("the new session is there before it was indexed:\n%s", m.View())
	}

	// When indexing brings a new session and is done
	sessions.list = append([]reporting.SessionSummary{session("claude:new", "fresh session", kernel.Claude, 1, 1, 1, 10, at(4, 9, 0), time.Minute)}, sessions.list...)
	next, cmd := m.Update(syncDoneMsg{})
	m = answer(t, next.(Model), cmd)

	// Then the sessions on screen include it
	if view := m.View(); !strings.Contains(view, "fresh session") || !strings.Contains(view, "4 sessions") {
		t.Errorf("the new session does not show:\n%s", view)
	}
}

func TestQuitKeysWorkInEveryScreen(t *testing.T) {
	sessions := sampleSessions()
	sessions.details["codex:cheap"] = detailOf(sessions.list[0])
	list := inSessions(t, sessions)
	detail := openDetail(t, list)
	for name, m := range map[string]Model{"list": list, "detail": detail} {
		_, cmd := pressCmd(t, m, "q")
		if cmd == nil {
			t.Fatalf("q does not quit the %s", name)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("q does not quit the %s", name)
		}
	}
}

func TestATerminalThatReportsNoSizeKeepsTheDefaultSize(t *testing.T) {
	// Given a detail on screen
	sessions := sampleSessions()
	sessions.details["codex:cheap"] = detailOf(sessions.list[0])
	m := openDetail(t, inSessions(t, sessions))

	// When the terminal reports a size of 0x0, as a bare pseudo-terminal does
	m = send(t, m, tea.WindowSizeMsg{})

	// Then the lines are not cut to nothing
	if view := m.View(); !strings.Contains(view, "codex · /work/codex:cheap") {
		t.Errorf("view:\n%s", view)
	}
}

func TestTheSessionsHelpStillOffersQuitOnAn80ColumnTerminalWhileIndexing(t *testing.T) {
	// Given the sessions on an 80-column terminal, with indexing available
	sessions := sampleSessions()
	m := New(Options{Reports: build(&fakeReports{report: sampleReport()}), Sync: func(context.Context, func(int)) (string, error) { return "", nil },
		Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions}})
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m, cmd := pressCmd(t, send(t, m, m.load()()), "m")
	m = answer(t, m, cmd)

	// Then the help fits the terminal and the user can still read how to quit, switch mode and re-index
	help := m.help()
	if w := lipgloss.Width(help); w > 80 {
		t.Errorf("help of %d columns on an 80-column terminal: %q", w, help)
	}
	for _, want := range []string{"enter open", "m usage", "r re-index", "q quit"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q: %q", want, help)
		}
	}
}

func TestResizingTheTerminalKeepsTheSelectedSession(t *testing.T) {
	// Given the second session selected on a wide terminal
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := press(t, inSessions(t, sessions), "down")

	// When the terminal is narrowed to 80 columns, which drops columns
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})

	// Then Enter still opens that session
	m, cmd := pressType(t, m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("no detail was requested")
	}
	m = send(t, m, cmd())
	if got := sessions.asked; len(got) != 1 || got[0] != "claude:dear" {
		t.Errorf("asked for %v, want the selected session", got)
	}
}
