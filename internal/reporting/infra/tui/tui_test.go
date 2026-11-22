package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// fakeReports is the usage index behind BuildReport: it answers every filter with report, and remembers
// the queries it was asked (one Total call per report).
type fakeReports struct {
	report  reporting.Report
	err     error
	queries []reporting.Query
}

func (f *fakeReports) Snapshot(_ context.Context, fn func(reporting.UsageReader) error) error {
	return fn(f)
}

func (f *fakeReports) Total(fl domain.Filter) (domain.Usage, error) {
	f.queries = append(f.queries, reporting.Query{Since: fl.Since, Harness: fl.Harness})
	return f.report.Total, f.err
}

func (f *fakeReports) UsageBy(_ domain.Filter, d domain.Dimension) ([]domain.UsageGroup, error) {
	return map[domain.Dimension][]domain.UsageGroup{
		domain.ByHarness: f.report.ByHarness, domain.ByModel: f.report.ByModel,
		domain.ByFolder: f.report.ByFolder, domain.ByAgentType: f.report.ByAgent,
	}[d], nil
}

func (f *fakeReports) Tools(domain.Filter) ([]domain.ToolUsage, error) { return f.report.Tools, nil }

func build(f *fakeReports) reporting.BuildReport { return reporting.BuildReport{Queries: f} }

func usage(requests, failed, input, output, cacheRead int64, cost float64) reporting.Usage {
	return reporting.Usage{Requests: requests, Failed: failed, Input: input, Output: output, CacheRead: cacheRead,
		Cost: reporting.USD(cost), First: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Last: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
}

func group(name string, u reporting.Usage) reporting.UsageGroup {
	return reporting.UsageGroup{Group: name, Usage: u}
}

func sampleReport() reporting.Report {
	return reporting.Report{
		Total: usage(30, 2, 600, 300, 400, 12.5),
		ByHarness: []reporting.UsageGroup{
			group("claude", usage(20, 1, 400, 200, 300, 9)),
			group("codex", usage(10, 1, 200, 100, 100, 3.5)),
		},
		ByModel:  []reporting.UsageGroup{group("claude-x", usage(20, 1, 400, 200, 300, 9))},
		ByFolder: []reporting.UsageGroup{group("/work/app", usage(30, 2, 600, 300, 400, 12.5))},
		ByAgent:  []reporting.UsageGroup{group("main", usage(30, 2, 600, 300, 400, 12.5))},
		Tools:    []reporting.ToolUsage{{Name: "Bash", Calls: 42, Errors: 3, ResultChars: 12345}, {Name: "search", Server: "grace", Calls: 7}},
	}
}

// start returns the interface once the first report arrived.
func start(t *testing.T, reports *fakeReports) Model {
	t.Helper()
	m := New(Options{Reports: build(reports)})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init asks for no report")
	}
	return send(t, m, cmd())
}

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		}
		m = send(t, m, msg)
	}
	return m
}

func TestFirstReportShowsTotalsAndHarnesses(t *testing.T) {
	// Given indexed usage
	reports := &fakeReports{report: sampleReport()}

	// When the interface opens
	m := start(t, reports)

	// Then the totals line and the harness table are shown
	view := m.View()
	for _, want := range []string{
		"30 requests (2 failed) · $12.50", "1 000 in / 300 out · cache 40% · 2026-01-01 → 2026-02-01",
		"HARNESS", "claude", "codex", "$9.00", "$3.50",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestTabsShowOneTableEach(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport()})
	for _, c := range []struct{ key, header, row string }{
		{"2", "MODEL", "claude-x"},
		{"3", "FOLDER", "/work/app"},
		{"4", "AGENT", "main"},
		{"5", "MCP SERVER", "grace"},
		{"1", "HARNESS", "codex"},
	} {
		// When the user selects a tab by number
		m = press(t, m, c.key)
		// Then its table is shown
		if view := m.View(); !strings.Contains(view, c.header) || !strings.Contains(view, c.row) {
			t.Errorf("tab %s lacks %q / %q:\n%s", c.key, c.header, c.row, view)
		}
	}
}

func TestTabKeysCycleAndWrap(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport()})
	m = press(t, m, "tab", "tab")
	if view := m.View(); !strings.Contains(view, "FOLDER") {
		t.Fatalf("after two tab presses, want the folder table:\n%s", view)
	}
	m = press(t, m, "shift+tab", "shift+tab", "left")
	if view := m.View(); !strings.Contains(view, "MCP SERVER") {
		t.Errorf("left from the first tab, want the tools table:\n%s", view)
	}
	m = press(t, m, "right")
	if view := m.View(); !strings.Contains(view, "HARNESS") {
		t.Errorf("right from the last tab, want the harness table:\n%s", view)
	}
}

func TestEmptyRangeSaysSo(t *testing.T) {
	m := start(t, &fakeReports{})
	if view := m.View(); !strings.Contains(view, "No requests in this range.") || strings.Contains(view, "HARNESS") {
		t.Errorf("empty view:\n%s", view)
	}
}

func TestFailedReportKeepsThePreviousOneAndShowsTheError(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport()})
	m = send(t, m, reportMsg{err: errors.New("database is locked")})
	view := m.View()
	if !strings.Contains(view, "error: database is locked") || !strings.Contains(view, "claude") {
		t.Errorf("view after a failed report:\n%s", view)
	}
}

func TestLogTextCannotDriveTheTerminal(t *testing.T) {
	r := sampleReport()
	r.ByHarness = []reporting.UsageGroup{group("evil\x1b[2J\x07", usage(1, 0, 1, 1, 0, 0))}
	view := start(t, &fakeReports{report: r}).View()
	if strings.ContainsAny(view, "\x07") || strings.Contains(view, "\x1b[2J") {
		t.Errorf("control characters reached the view: %q", view)
	}
}

func TestQuitKeys(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport()})
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyCtrlC}} {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("%v does not quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%v does not quit", k)
		}
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	r := sampleReport()
	for i := 0; i < 50; i++ {
		r.ByHarness = append(r.ByHarness, group("h", usage(1, 0, 1, 1, 0, 0)))
	}
	m := start(t, &fakeReports{report: r})
	m = send(t, m, tea.WindowSizeMsg{Width: 70, Height: 15})
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

// sortReport ranks three harnesses by cost (the report's order): zed, amy, bob.
func sortReport() reporting.Report {
	r := sampleReport()
	r.ByHarness = []reporting.UsageGroup{
		group("zed", usage(5, 0, 10, 10, 0, 20)),
		group("amy", usage(30, 3, 10, 10, 0, 5)),
		group("Bob", usage(10, 1, 10, 10, 0, 1)),
	}
	return r
}

// order lists the names in the order the view shows them.
func order(view string, names ...string) string {
	type at struct {
		name string
		i    int
	}
	var found []at
	for _, n := range names {
		if i := strings.Index(view, n); i >= 0 {
			found = append(found, at{n, i})
		}
	}
	for i := range found {
		for j := i + 1; j < len(found); j++ {
			if found[j].i < found[i].i {
				found[i], found[j] = found[j], found[i]
			}
		}
	}
	var out []string
	for _, f := range found {
		out = append(out, f.name)
	}
	return strings.Join(out, " ")
}

func TestSortingByColumnFromTheKeyboard(t *testing.T) {
	m := start(t, &fakeReports{report: sortReport()})
	names := []string{"zed", "amy", "Bob"}
	if got := order(m.View(), names...); got != "zed amy Bob" {
		t.Fatalf("initial order = %q, want the report's", got)
	}

	// When the user sorts by the name column, then rows are alphabetical regardless of case
	m = press(t, m, "s")
	if got, view := order(m.View(), names...), m.View(); got != "amy Bob zed" || !strings.Contains(view, "HARNESS▲") {
		t.Errorf("by name: %q\n%s", got, view)
	}

	// When the user moves to the REQUESTS column, then the busiest comes first
	m = press(t, m, "s")
	if got, view := order(m.View(), names...), m.View(); got != "amy Bob zed" || !strings.Contains(view, "REQUESTS▼") {
		t.Errorf("by requests: %q\n%s", got, view)
	}

	// When the user reverses the order, then the least busy comes first
	m = press(t, m, "o")
	if got, view := order(m.View(), names...), m.View(); got != "zed Bob amy" || !strings.Contains(view, "REQUESTS▲") {
		t.Errorf("reversed: %q\n%s", got, view)
	}
}

func TestSortingCyclesBackToTheReportOrder(t *testing.T) {
	m := start(t, &fakeReports{report: sortReport()})
	for range 7 { // 7 columns on a usage table
		m = press(t, m, "s")
	}
	if view := m.View(); !strings.Contains(view, "COST▼") {
		t.Fatalf("after 7 presses, want the cost column sorted:\n%s", view)
	}
	if got := order(m.View(), "zed", "amy", "Bob"); got != "zed amy Bob" { // COST descending is the report's order here
		t.Errorf("by cost: %q", got)
	}
	m = press(t, m, "s")
	if strings.ContainsAny(m.View(), "▲▼") {
		t.Errorf("did not return to the report's order:\n%s", m.View())
	}
}

func TestReverseWithoutASortedColumnChangesNothing(t *testing.T) {
	m := press(t, start(t, &fakeReports{report: sortReport()}), "o")
	if got := order(m.View(), "zed", "amy", "Bob"); got != "zed amy Bob" {
		t.Errorf("order = %q", got)
	}
}

func TestEachTabKeepsItsOwnSort(t *testing.T) {
	m := start(t, &fakeReports{report: sortReport()})
	m = press(t, m, "s", "2", "3", "1") // sort harnesses by name, visit two other tabs, come back
	if got := order(m.View(), "zed", "amy", "Bob"); got != "amy Bob zed" {
		t.Errorf("harness tab lost its sort: %q", got)
	}
	if m = press(t, m, "2"); strings.ContainsAny(m.View(), "▲▼") {
		t.Errorf("model tab was sorted:\n%s", m.View())
	}
}

func TestToolsSortByCalls(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport()})
	m = press(t, m, "5", "s", "s", "s") // TOOL, MCP SERVER, CALLS (largest first)
	if got := order(m.View(), "Bash", "search"); got != "Bash search" {
		t.Errorf("calls descending: %q", got)
	}
	m = press(t, m, "o")
	if got := order(m.View(), "Bash", "search"); got != "search Bash" {
		t.Errorf("calls ascending: %q", got)
	}
}

var noon = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// startWith opens the interface with o at a fixed time and delivers the first report.
func startWith(t *testing.T, reports *fakeReports, o Options) Model {
	t.Helper()
	o.Reports = build(reports)
	m := New(o)
	m.now = func() time.Time { return noon }
	return send(t, m, m.Init()())
}

// answer runs the report request the last key press started and delivers its answer.
func answer(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no report was requested")
	}
	return send(t, m, cmd())
}

func pressCmd(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model), cmd
}

func TestFirstReportIsForEverythingByDefault(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{})
	if got := reports.queries; len(got) != 1 || got[0] != (reporting.Query{}) {
		t.Errorf("first query = %+v", got)
	}
	if view := m.View(); !strings.Contains(view, "period: all · harness: all") {
		t.Errorf("title lacks the filters:\n%s", view)
	}
}

func TestPeriodKeyAsksForAnotherReport(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{})
	// When the user presses p, the period wraps from all to 24h, 7d, 30d and back to all
	for _, c := range []struct {
		label string
		since time.Time
	}{
		{"24h", noon.Add(-24 * time.Hour)},
		{"7d", noon.AddDate(0, 0, -7)},
		{"30d", noon.AddDate(0, 0, -30)},
		{"all", time.Time{}},
	} {
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, "p")
		m = answer(t, m, cmd)
		got := reports.queries[len(reports.queries)-1]
		if !got.Since.Equal(c.since) || !got.Harness.IsZero() {
			t.Errorf("period %s: query = %+v, want since %v", c.label, got, c.since)
		}
		if view := m.View(); !strings.Contains(view, "period: "+c.label+" ·") {
			t.Errorf("title lacks period %s:\n%s", c.label, view)
		}
	}
}

func TestHarnessKeyCyclesThroughEveryHarness(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{})
	for _, want := range []kernel.Harness{kernel.Claude, kernel.Codex, kernel.OMP, kernel.Pi, {}} {
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, "h")
		m = answer(t, m, cmd)
		if got := reports.queries[len(reports.queries)-1]; got.Harness != want {
			t.Errorf("harness query = %+v, want %q", got, want)
		}
	}
	if view := m.View(); !strings.Contains(view, "harness: all") {
		t.Errorf("title after a full cycle:\n%s", view)
	}
}

func TestFiltersCombine(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{})
	m, _ = pressCmd(t, m, "p")
	m, cmd := pressCmd(t, m, "h")
	answer(t, m, cmd)
	got := reports.queries[len(reports.queries)-1]
	if !got.Since.Equal(noon.Add(-24*time.Hour)) || got.Harness != kernel.Claude {
		t.Errorf("query = %+v", got)
	}
}

func TestStartsOnTheFiltersOfTheCommandLine(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{SinceLabel: "7d", Harness: kernel.Codex})
	if got := reports.queries[0]; !got.Since.Equal(noon.AddDate(0, 0, -7)) || got.Harness != kernel.Codex {
		t.Errorf("first query = %+v", got)
	}
	// p moves on to the preset after 7d
	m, cmd := pressCmd(t, m, "p")
	answer(t, m, cmd)
	if got := reports.queries[1]; !got.Since.Equal(noon.AddDate(0, 0, -30)) || got.Harness != kernel.Codex {
		t.Errorf("after p: %+v", got)
	}
}

func TestACustomSinceIsKeptAndLeadsThePresets(t *testing.T) {
	since := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{SinceLabel: "2026-01-02", Since: since})
	if got := reports.queries[0]; !got.Since.Equal(since) {
		t.Errorf("first query = %+v", got)
	}
	if view := m.View(); !strings.Contains(view, "period: 2026-01-02") {
		t.Errorf("title:\n%s", view)
	}
	m, cmd := pressCmd(t, m, "p")
	answer(t, m, cmd)
	if got := reports.queries[1]; !got.Since.Equal(noon.Add(-24 * time.Hour)) {
		t.Errorf("after p: %+v", got)
	}
}

func TestAnswerToASupersededRequestIsIgnored(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{})
	m, slow := pressCmd(t, m, "p") // 24h, answered late
	m, fast := pressCmd(t, m, "p") // 7d
	stale := reporting.Report{Total: usage(1, 0, 1, 1, 0, 0), ByHarness: []reporting.UsageGroup{group("stale-harness", usage(1, 0, 1, 1, 0, 0))}}
	reports.report = stale
	m = send(t, m, slow()) // the 24h answer arrives after the 7d request was made
	if strings.Contains(m.View(), "stale-harness") {
		t.Error("a superseded answer replaced the view")
	}
	reports.report = sampleReport()
	m = send(t, m, fast())
	if view := m.View(); !strings.Contains(view, "claude") || strings.Contains(view, "updating") {
		t.Errorf("view after the current answer:\n%s", view)
	}
}

func TestViewSaysItIsUpdatingUntilTheAnswerArrives(t *testing.T) {
	m := startWith(t, &fakeReports{report: sampleReport()}, Options{})
	m, cmd := pressCmd(t, m, "p")
	if !strings.Contains(m.View(), "updating…") {
		t.Errorf("no progress shown:\n%s", m.View())
	}
	if m = answer(t, m, cmd); strings.Contains(m.View(), "updating…") {
		t.Error("still updating after the answer")
	}
}

// settle runs cmd and every command its messages lead to, feeding each message to the model,
// until nothing is left to do. The spinner's own ticks are skipped: they would never end.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case nil:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = settle(t, m, c)
		}
		return m
	case spinner.TickMsg:
		return m
	default:
		next, cmd := m.Update(msg)
		return settle(t, next.(Model), cmd)
	}
}

// fakeSync reports each count as progress, then ends with note and err; calls counts its runs.
type fakeSync struct {
	counts []int
	note   string
	err    error
	calls  int
}

func (f *fakeSync) run(_ context.Context, progress func(int)) (string, error) {
	f.calls++
	for _, c := range f.counts {
		progress(c)
	}
	return f.note, f.err
}

func openWithSync(t *testing.T, reports *fakeReports, s *fakeSync) Model {
	t.Helper()
	m := New(Options{Reports: build(reports), Sync: s.run})
	return settle(t, m, m.Init())
}

func TestIndexingRunsInTheBackgroundThenTheReportRefreshes(t *testing.T) {
	// Given a sync that indexes files and skips some
	reports := &fakeReports{report: sampleReport()}
	s := &fakeSync{counts: []int{50, 100}, note: "2 files skipped"}
	m := New(Options{Reports: build(reports), Sync: s.run})

	// When the interface opens, then it says it is indexing before anything finishes
	if view := m.View(); !strings.Contains(view, "indexing…") {
		t.Errorf("no indexing indicator on opening:\n%s", view)
	}
	m = settle(t, m, m.Init())

	// Then, once it is done, the indicator is gone, the note is shown and the report was asked for again
	view := m.View()
	if strings.Contains(view, "indexing…") || !strings.Contains(view, "2 files skipped") {
		t.Errorf("view after indexing:\n%s", view)
	}
	if s.calls != 1 || len(reports.queries) != 2 {
		t.Errorf("sync ran %d times, report asked %d times; want 1 and 2 (before and after indexing)", s.calls, len(reports.queries))
	}
}

func TestProgressShowsFilesIndexedSoFar(t *testing.T) {
	m := New(Options{Reports: build(&fakeReports{}), Sync: (&fakeSync{}).run})
	m = send(t, m, syncProgressMsg{indexed: 1250, events: make(chan tea.Msg)})
	if view := m.View(); !strings.Contains(view, "indexing… 1 250 files") {
		t.Errorf("progress:\n%s", view)
	}
}

func TestProgressReachesTheScreenWhileIndexingIsStillRunning(t *testing.T) {
	// Given a sync that is still working after its first progress report
	release := make(chan struct{})
	m := New(Options{Reports: build(&fakeReports{}), Sync: func(_ context.Context, progress func(int)) (string, error) {
		progress(50)
		<-release
		return "", nil
	}})

	// When the sync starts, then its progress comes back without waiting for its end
	got := make(chan tea.Msg)
	go func() { got <- m.startSync()() }()
	select {
	case msg := <-got:
		if p, ok := msg.(syncProgressMsg); !ok || p.indexed != 50 {
			t.Fatalf("first message = %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("progress is blocked until the sync ends")
	}

	// And its end arrives as a message once released
	close(release)
}

func TestProgressIsNotSentMoreOftenThanTheScreenNeedsIt(t *testing.T) {
	// Given a sync that reports three counts at once, then one 200 ms later
	clock := time.Unix(1000, 0)
	m := New(Options{Reports: build(&fakeReports{}), Sync: func(_ context.Context, progress func(int)) (string, error) {
		progress(50)
		progress(100)
		progress(150)
		clock = clock.Add(200 * time.Millisecond)
		progress(200)
		return "", nil
	}})
	m.now = func() time.Time { return clock }

	// When the interface listens to it
	var counts []int
	msg := m.startSync()()
	for {
		p, ok := msg.(syncProgressMsg)
		if !ok {
			break
		}
		counts = append(counts, p.indexed)
		msg = <-p.events
	}

	// Then the burst makes one message, and the later count another
	if fmt.Sprint(counts) != "[50 200]" {
		t.Errorf("progress messages = %v, want [50 200]", counts)
	}
}

// stopped reports whether stop returned within the time a test is willing to wait.
func stopped(stop func()) bool {
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func TestQuittingCancelsTheSyncAndWaitsForItToStop(t *testing.T) {
	// Given a sync that works until it is told to stop
	started := make(chan struct{})
	m := New(Options{Reports: build(&fakeReports{}), Sync: func(ctx context.Context, progress func(int)) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}})
	first := make(chan tea.Msg, 1)
	go func() { first <- m.startSync()() }()
	<-started

	// When the user quits
	_, cmd := pressCmd(t, m, "q")

	// Then the interface asks to close, the sync was told to stop, and closing waits for it: the index is not
	// closed under a run that is still saving
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("q did not quit")
	}
	if !stopped(m.stopSync) {
		t.Fatal("the sync was not told to stop, or stopping does not wait for it")
	}
	select {
	case msg := <-first:
		if done, ok := msg.(syncDoneMsg); !ok || !errors.Is(done.err, context.Canceled) {
			t.Errorf("the sync ended with %#v, want a cancellation", msg)
		}
	case <-time.After(2 * time.Second):
		t.Error("the sync's end is never delivered")
	}
}

func TestASyncThatEndsAfterTheInterfaceStoppedReadingDoesNotBlock(t *testing.T) {
	// Given a sync that reports more progress than the interface reads, then ends
	clock := time.Unix(1000, 0)
	finished := make(chan struct{})
	m := New(Options{Reports: build(&fakeReports{}), Sync: func(_ context.Context, progress func(int)) (string, error) {
		for i := 1; i <= 30; i++ {
			clock = clock.Add(time.Second)
			progress(i)
		}
		close(finished)
		return "", nil
	}})
	m.now = func() time.Time { return clock }

	// When the interface reads the first message only and then quits: the buffer of the stream is full
	first := m.startSync()()
	<-finished
	time.Sleep(20 * time.Millisecond) // let the sync try to send its end
	pressCmd(t, m, "q")

	// Then the sync's goroutine ends instead of waiting for a reader that is gone
	if _, ok := first.(syncProgressMsg); !ok {
		t.Fatalf("first message = %#v", first)
	}
	if !stopped(m.stopSync) {
		t.Error("the sync's last message blocks it for ever")
	}
}

func TestEveryKeyAndMessageIsAnsweredWithoutQueryingTheIndex(t *testing.T) {
	// Given an interface with its first report on screen
	reports := &fakeReports{report: sampleReport()}
	m := openWithSync(t, reports, &fakeSync{})
	asked := len(reports.queries)

	// When keys that only change the view are pressed and progress arrives
	m = press(t, m, "tab", "s", "o", "right", "1")
	m = send(t, m, syncProgressMsg{indexed: 50, events: make(chan tea.Msg)})
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Then the update loop queried nothing: only a command, run off the loop, asks the index
	if len(reports.queries) != asked {
		t.Errorf("the update loop made %d queries", len(reports.queries)-asked)
	}
}

func TestFailedIndexingIsShownAndTheViewStillRefreshes(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := openWithSync(t, reports, &fakeSync{err: errors.New("disk full")})
	view := m.View()
	if !strings.Contains(view, "indexing failed: disk full") || !strings.Contains(view, "claude") {
		t.Errorf("view:\n%s", view)
	}
	if len(reports.queries) != 2 {
		t.Errorf("report asked %d times, want 2", len(reports.queries))
	}
}

func TestReindexKeyRunsTheSyncAgain(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	s := &fakeSync{counts: []int{50}}
	m := openWithSync(t, reports, s)

	m, cmd := pressCmd(t, m, "r")
	if !strings.Contains(m.View(), "indexing…") {
		t.Errorf("no indicator after r:\n%s", m.View())
	}
	// r while it is running starts nothing more
	if _, again := pressCmd(t, m, "r"); again != nil {
		t.Error("r started a second sync while one was running")
	}
	m = settle(t, m, cmd)
	if s.calls != 2 || strings.Contains(m.View(), "indexing…") {
		t.Errorf("sync ran %d times; view:\n%s", s.calls, m.View())
	}
}

func TestWithoutSyncNothingIsIndexed(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := New(Options{Reports: build(reports)})
	if view := m.View(); strings.Contains(view, "indexing") || strings.Contains(view, "re-index") {
		t.Errorf("sync-less view mentions indexing:\n%s", view)
	}
	m = settle(t, m, m.Init())
	if _, cmd := pressCmd(t, m, "r"); cmd != nil {
		t.Error("r did something without a sync")
	}
	if len(reports.queries) != 1 {
		t.Errorf("report asked %d times, want 1", len(reports.queries))
	}
}

func TestSpinnerStopsWhenIndexingEnds(t *testing.T) {
	m := openWithSync(t, &fakeReports{report: sampleReport()}, &fakeSync{})
	if _, cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Error("the spinner keeps ticking after indexing ended")
	}
}

func TestIndexingMessagesCannotDriveTheTerminal(t *testing.T) {
	m := openWithSync(t, &fakeReports{report: sampleReport()}, &fakeSync{err: errors.New("bad\x1b[2Jpath"), note: "n\x07"})
	if view := m.View(); strings.ContainsAny(view, "\x07") || strings.Contains(view, "\x1b[2J") {
		t.Errorf("control characters reached the view: %q", view)
	}
}

func TestShorterReportKeepsASelectedRow(t *testing.T) {
	// Given the cursor far down a long table
	long := sortReport()
	for i := 0; i < 30; i++ {
		long.ByHarness = append(long.ByHarness, group("filler", usage(1, 0, 1, 1, 0, 0)))
	}
	reports := &fakeReports{report: long}
	m := start(t, reports)
	m = press(t, m, "j", "j", "j", "j", "j", "j", "j", "j", "j", "j")

	// When a filter leaves a single row
	reports.report = sortReport()
	reports.report.ByHarness = reports.report.ByHarness[:1]
	m, cmd := pressCmd(t, m, "p")
	m = answer(t, m, cmd)

	// Then the cursor is on that row, and scrolling down stays on it
	if c := m.table.Cursor(); c != 0 {
		t.Errorf("cursor = %d on a one-row table", c)
	}
	if m = press(t, m, "j"); m.table.Cursor() != 0 || !strings.Contains(m.View(), "zed") {
		t.Errorf("cursor = %d; view:\n%s", m.table.Cursor(), m.View())
	}
}

func TestToolsTabShowsToolCallsWhenNoRequestFallsInTheRange(t *testing.T) {
	// Given a range holding a tool call but no request (a request before the period start and its tool call
	// after it are filtered separately, each by its own timestamp)
	r := reporting.Report{Tools: []reporting.ToolUsage{{Name: "Bash", Calls: 1}}}
	m := start(t, &fakeReports{report: r})

	// When the user opens the tools tab
	m = press(t, m, "5")

	// Then the tool call is listed
	if view := m.View(); !strings.Contains(view, "Bash") || !strings.Contains(view, "MCP SERVER") {
		t.Errorf("tools tab hides the tool call:\n%s", view)
	}
}

func TestEmptyMessageIsPerTab(t *testing.T) {
	m := start(t, &fakeReports{})
	for _, c := range []struct{ key, want, not string }{
		{"1", "No requests in this range.", "No tool calls"},
		{"5", "No tool calls in this range.", "No requests"},
	} {
		view := press(t, m, c.key).View()
		if !strings.Contains(view, c.want) || strings.Contains(view, c.not) {
			t.Errorf("tab %s, want %q:\n%s", c.key, c.want, view)
		}
	}
}

func TestResizingKeepsTheSelectedRow(t *testing.T) {
	// Given the third row selected
	m := start(t, &fakeReports{report: sortReport()})
	m = press(t, m, "j", "j")
	if c := m.table.Cursor(); c != 2 {
		t.Fatalf("cursor = %d before resizing", c)
	}

	// When the terminal is widened by a column
	m = send(t, m, tea.WindowSizeMsg{Width: 81, Height: 24})

	// Then the same row stays selected
	if c := m.table.Cursor(); c != 2 {
		t.Errorf("cursor = %d after resizing, want 2", c)
	}
}

func TestTitleDescribesTheDataShownWhenAReloadFails(t *testing.T) {
	// Given the report of every harness on screen
	m := start(t, &fakeReports{report: sampleReport()})

	// When the user filters on claude and the reload fails
	m, _ = pressCmd(t, m, "h")
	m = send(t, m, reportMsg{seq: m.seq, err: errors.New("database is locked")})

	// Then the old figures stay with the filters that produced them, and the error is shown
	view := m.View()
	if !strings.Contains(view, "harness: all") || strings.Contains(view, "harness: claude") || !strings.Contains(view, "error: database is locked") {
		t.Errorf("title does not match the data:\n%s", view)
	}
}

func TestTitleFollowsTheFiltersOnceTheirReportArrives(t *testing.T) {
	reports := &fakeReports{report: sampleReport()}
	m := start(t, reports)
	m, cmd := pressCmd(t, m, "h")
	m = answer(t, m, cmd)
	if view := m.View(); !strings.Contains(view, "harness: claude") {
		t.Errorf("title lacks the new filter:\n%s", view)
	}
}

func TestResizingKeepsTheSelectedRowOnScreen(t *testing.T) {
	// Given a tab longer than the screen, scrolled down to its 20th row
	r := sampleReport()
	r.ByHarness = nil
	for i := range 40 {
		r.ByHarness = append(r.ByHarness, group(fmt.Sprintf("row-%02d", i), usage(int64(40-i), 0, 10, 10, 0, 1)))
	}
	m := start(t, &fakeReports{report: r})
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for range 19 {
		m = press(t, m, "j")
	}
	selected := strings.TrimSpace(m.table.SelectedRow()[0])

	// When the terminal is widened by a column
	m = send(t, m, tea.WindowSizeMsg{Width: 81, Height: 24})

	// Then the selected row is still on screen
	if !strings.Contains(m.View(), selected) {
		t.Errorf("%s left the screen after resizing:\n%s", selected, m.View())
	}
}
