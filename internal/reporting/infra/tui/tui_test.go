package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// fakeReports answers every query with report, and remembers the queries it was asked.
type fakeReports struct {
	report  reporting.Report
	err     error
	queries []reporting.Query
}

func (f *fakeReports) Run(q reporting.Query) (reporting.Report, error) {
	f.queries = append(f.queries, q)
	return f.report, f.err
}

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
	m := New(Options{Reports: reports})
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
	if m.tab != folderTab {
		t.Fatalf("after two tab presses: %v", m.tab)
	}
	m = press(t, m, "shift+tab", "shift+tab", "left")
	if m.tab != toolsTab {
		t.Errorf("left from the first tab: %v, want the last", m.tab)
	}
	m = press(t, m, "right")
	if m.tab != harnessTab {
		t.Errorf("right from the last tab: %v, want the first", m.tab)
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
	if m.sorts[harnessTab].col != 6 {
		t.Fatalf("after 7 presses: %+v", m.sorts[harnessTab])
	}
	if got := order(m.View(), "zed", "amy", "Bob"); got != "zed amy Bob" { // COST descending is the report's order here
		t.Errorf("by cost: %q", got)
	}
	m = press(t, m, "s")
	if m.sorts[harnessTab] != reportOrder || strings.ContainsAny(m.View(), "▲▼") {
		t.Errorf("did not return to the report's order: %+v", m.sorts[harnessTab])
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
	if m.sorts[modelTab] != reportOrder {
		t.Errorf("model tab was sorted: %+v", m.sorts[modelTab])
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
	o.Reports = reports
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
