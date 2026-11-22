package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
}

func (f *fakeSessions) Sessions(fl domain.Filter) ([]domain.SessionSummary, error) {
	f.filters = append(f.filters, fl)
	return f.list, f.err
}

func (f *fakeSessions) Session(id domain.SessionID) (domain.SessionDetail, error) {
	f.asked = append(f.asked, id.String())
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

	// When the user sorts by the COST column (the sixth), the most expensive comes first
	m = press(t, m, "s", "s", "s", "s", "s", "s")
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
	// When the user sorts by AVG PROMPT (the ninth column), the longest prompts come first
	m = press(t, m, "s", "s", "s", "s", "s", "s", "s", "s", "s")
	if got, view := order(m.View(), "refactor the billing", "quick fix"), m.View(); got != "refactor the billing quick fix" || !strings.Contains(view, "AVG PROMPT▼") {
		t.Errorf("by average prompt: %q\n%s", got, view)
	}
}

func TestEnterOpensTheSelectedSessionWhateverTheSort(t *testing.T) {
	// Given sessions sorted by cost, the first row being the most expensive session
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := press(t, inSessions(t, sessions), "s", "s", "s", "s", "s", "s")

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
		Subagents:      []domain.SubagentUsage{{ID: sid(s.ID.String() + "/kid"), AgentType: "Explore", Title: "look around", Usage: reporting.Usage{Requests: 5, Cost: 1.25}}},
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
	m = press(t, m, "s", "s", "s", "s", "s", "s")

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

	// Then the list is back, with the same session selected, and nothing was requested
	view := m.View()
	if cmd != nil || !strings.Contains(view, "TITLE") || strings.Contains(view, "esc back") {
		t.Errorf("after Esc (cmd %v):\n%s", cmd != nil, view)
	}
	if got := m.sessions.ids[m.sessions.table.Cursor()].String(); got != "claude:dear" {
		t.Errorf("selection moved to %s", got)
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
	d.Subagents = []domain.SubagentUsage{{ID: sid("x/y"), AgentType: evil, Title: evil}}
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

func TestDetailFitsAndScrolls(t *testing.T) {
	// Given a session with sixty prompts on a small terminal
	sessions := sampleSessions()
	d := detailOf(sessions.list[0])
	d.Prompts = nil
	for i := 1; i <= 60; i++ {
		d.Prompts = append(d.Prompts, domain.Prompt{At: at(3, 10, i%60), Text: fmt.Sprintf("prompt number %d %s", i, strings.Repeat("long ", 40)), Chars: 220})
	}
	sessions.details["codex:cheap"] = d
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 60, Height: 14})
	m = openDetail(t, m)

	// Then the view fits the terminal and shows the top of the session
	fits := func(m Model) {
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
	fits(m)
	if view := m.View(); !strings.Contains(view, "refactor the billing module") || strings.Contains(view, "prompt number 60") {
		t.Errorf("top of the detail:\n%s", view)
	}

	// When the user goes to the end, then the last prompt is shown; Home brings the top back
	m, _ = pressType(t, m, tea.KeyEnd)
	fits(m)
	if view := m.View(); !strings.Contains(view, "prompt number 60") || strings.Contains(view, "TOOLS") {
		t.Errorf("end of the detail:\n%s", view)
	}
	m, _ = pressType(t, m, tea.KeyHome)
	if view := m.View(); !strings.Contains(view, "TOOLS") {
		t.Errorf("home:\n%s", view)
	}

	// And paging down, then up, moves by a screen and comes back
	m, _ = pressType(t, m, tea.KeyPgDown)
	paged := m.sessions.detail.scroll
	if paged != m.sessions.detail.display {
		t.Errorf("a page down moved by %d, want %d", paged, m.sessions.detail.display)
	}
	m, _ = pressType(t, m, tea.KeyPgUp)
	m, _ = pressType(t, m, tea.KeyUp)
	if m.sessions.detail.scroll != 0 {
		t.Errorf("scroll = %d after paging back", m.sessions.detail.scroll)
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

func TestStaleAnswersAreIgnored(t *testing.T) {
	sessions := sampleSessions()
	m := inSessions(t, sessions)

	// When the answer to an earlier sessions request arrives after a newer filter was asked for
	m, cmd := pressCmd(t, m, "p")
	stale := sessionsMsg{seq: m.seq - 1, list: nil}
	m = send(t, m, stale)

	// Then it does not replace the sessions on screen
	if view := m.View(); !strings.Contains(view, "quick fix") {
		t.Errorf("a stale answer emptied the list:\n%s", view)
	}
	m = answer(t, m, cmd)

	// And the detail of a session that is no longer open is dropped
	m = send(t, m, detailMsg{id: sid("claude:dear"), detail: detailOf(sessions.list[1])})
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

func TestIndexingRefreshesTheSessionsOnScreen(t *testing.T) {
	// Given the sessions mode with indexing on
	sessions := sampleSessions()
	m := New(Options{Reports: build(&fakeReports{report: sampleReport()}), Sync: func(func(int)) (string, error) { return "", nil },
		Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions}})
	m, cmd := pressCmd(t, send(t, m, m.load()()), "m")
	m = answer(t, m, cmd)
	before := len(sessions.filters)

	// When indexing is done
	next, cmd := m.Update(syncDoneMsg{})
	m2 := next.(Model)

	// Then the sessions are asked for again, not the usage report
	if cmd == nil {
		t.Fatal("no refresh after indexing")
	}
	if _, ok := cmd().(sessionsMsg); !ok || len(sessions.filters) != before+1 {
		t.Errorf("refresh did not read the sessions (%d reads, was %d)", len(sessions.filters), before)
	}
	if !strings.Contains(m2.help(), "r re-index") {
		t.Errorf("help: %s", m2.help())
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

func TestGridSortedIDsFollowTheRows(t *testing.T) {
	g := sessionsGrid(sampleSessions().list)
	got := g.sortedIDs(sortState{col: 5, desc: true}) // COST
	if strings.Join(got, " ") != "claude:dear codex:cheap pi:bare" {
		t.Errorf("ids = %v", got)
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
