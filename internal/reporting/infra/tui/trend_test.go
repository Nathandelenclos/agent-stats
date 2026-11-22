package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

func sampleDaily() []reporting.UsageGroup {
	first := group("2026-11-01", usage(10, 0, 1000, 200, 300, 0.005))
	last := group("2026-11-03", usage(20, 0, 2000, 400, 600, 0))
	last.Unpriced = last.Requests
	return []reporting.UsageGroup{first, last}
}

func TestDailyGraphsAndValuesFitTheTerminal(t *testing.T) {
	r := sampleReport()
	for i := 0; i < 30; i++ {
		r.ByHarness = append(r.ByHarness, group(fmt.Sprintf("fixture-%02d", i), usage(1, 0, 1, 1, 0, 0)))
	}
	m := start(t, &fakeReports{report: r, daily: sampleDaily()})
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	for _, want := range []string{"Requests", "Tokens", "Known cost", "[1 Harness]", "g charts", "? help", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("overview lacks %q:\n%s", want, view)
		}
	}
	if count := strings.Count(view, "fixture-"); count < 12 {
		t.Errorf("only %d detail rows visible on an 80-by-24 terminal:\n%s", count, view)
	}
	if strings.Contains(view, "Daily trends") {
		t.Errorf("the compact view puts graphs above the table:\n%s", view)
	}
	assertTrendSize(t, view, 80, 24)

	m = send(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), ">codex") {
		t.Fatalf("selected row has no text marker:\n%s", m.View())
	}
	for i := 0; i < 20; i++ {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	const selected = ">fixture-19"
	if !strings.Contains(m.View(), selected) {
		t.Fatalf("scrolling did not make the selection visible:\n%s", m.View())
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	if !strings.Contains(m.View(), selected) {
		t.Fatalf("selected row disappeared on resize:\n%s", m.View())
	}
	assertTrendSize(t, m.View(), 40, 15)

	m = press(t, m, "g")
	for _, want := range []string{"Daily usage", "Daily trends", "2026-11-01 to 2026-11-03", "▄·█", "█·?", "? unknown", "esc back", "q quit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("charts lack %q:\n%s", want, m.View())
		}
	}
	assertTrendSize(t, m.View(), 40, 15)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	for _, want := range []string{"2026-11-01", "2026-11-03", "1 500 tokens", "Cost unavailable"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("daily values lack %q:\n%s", want, m.View())
		}
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(m.View(), selected) || !strings.Contains(m.View(), "g charts") {
		t.Errorf("return from daily values lost the selected row:\n%s", m.View())
	}
}

func TestOverviewKeepsTotalsWhenTheSelectedBreakdownIsEmpty(t *testing.T) {
	r := sampleReport()
	r.Tools = nil
	r.Total.Unpriced = r.Total.Requests
	daily := sampleDaily()
	daily[0].Unpriced = daily[0].Requests
	daily[0].Cost = 0
	m := start(t, &fakeReports{report: r, daily: daily})
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "5")

	view := m.View()
	for _, want := range []string{"Known cost Unavailable", "No tool calls in this range.", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("overview lacks %q:\n%s", want, view)
		}
	}
	assertTrendSize(t, view, 80, 24)

	charts := press(t, m, "g").View()
	if !slices.ContainsFunc(strings.Split(charts, "\n"), func(line string) bool {
		return strings.HasPrefix(line, "Known cost") && strings.Contains(line, "?·?")
	}) {
		t.Errorf("the trend did not mark unpriced days as unknown:\n%s", charts)
	}

	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	view = m.View()
	if !strings.Contains(view, "Unavailable") || !strings.Contains(view, "g charts") || strings.Contains(view, "?·?") {
		t.Errorf("narrow view should keep the total and access to daily values:\n%s", view)
	}
	assertTrendSize(t, view, 40, 15)
}

func TestCostByCodingAgentShowsValuesAndRelativeBars(t *testing.T) {
	r := sampleReport()
	r.ByHarness = []reporting.UsageGroup{
		group("codex", usage(20, 0, 1, 1, 0, 9.20)),
		group("claude", usage(10, 0, 1, 1, 0, 5.60)),
	}
	m := start(t, &fakeReports{report: r})
	view := press(t, m, "g").View()
	section := strings.SplitN(view, "Estimated cost by coding agent", 2)
	if len(section) != 2 {
		t.Fatalf("missing bar chart:\n%s", view)
	}
	rows := strings.Split(section[1], "\n")[1:3]
	if !strings.Contains(rows[0], "Codex") || !strings.Contains(rows[0], "$9.20") ||
		!strings.Contains(rows[1], "Claude Code") || !strings.Contains(rows[1], "$5.60") {
		t.Errorf("missing agent name or exact cost: %v", rows)
	}
	if first, second := strings.Count(rows[0], "━"), strings.Count(rows[1], "━"); first != 34 || second != 21 {
		t.Errorf("unexpected bar lengths: %d and %d", first, second)
	}
	assertTrendSize(t, view, 80, 24)
}

func TestDailyValuesScrollAndIgnoreOlderReportAnswers(t *testing.T) {
	f := &fakeReports{report: sampleReport()}
	startDay := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		f.daily = append(f.daily, group(startDay.AddDate(0, 0, i).Format(time.DateOnly), usage(1, 0, 1, 1, 0, 1)))
	}
	m := send(t, start(t, f), tea.WindowSizeMsg{Width: 40, Height: 15})
	m = press(t, m, "g")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if !strings.Contains(m.View(), "2026-11-01") {
		t.Fatalf("missing first day:\n%s", m.View())
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), "2026-11-24") || strings.Contains(m.View(), "2026-11-01") {
		t.Fatalf("end did not scroll to the last page:\n%s", m.View())
	}
	stale := m.load()()
	m, newest := pressCmd(t, m, "h")
	f.daily = sampleDaily()[:1]
	m = answer(t, m, newest)
	m = send(t, m, stale)
	view := m.View()
	harness := f.queries[len(f.queries)-1].Harness
	if !strings.Contains(view, "2026-11-01") || strings.Contains(view, "2026-11-24") || !strings.Contains(view, "harness: "+harnessChoices[1].String()) || harness != harnessChoices[1] {
		t.Errorf("overview and daily values show different filters after an old answer:\n%s", view)
	}
}

func TestDailyGraphsRemainReadableWithoutColor(t *testing.T) {
	if os.Getenv("NO_COLOR") == "" {
		t.Skip("run with NO_COLOR=1 to exercise the monochrome renderer")
	}
	m := send(t, start(t, &fakeReports{report: sampleReport(), daily: sampleDaily()}), tea.WindowSizeMsg{Width: 120, Height: 32})
	view := m.View()
	if strings.Contains(view, "\x1b[") || !strings.Contains(view, "Requests") || !strings.Contains(view, "▄·█") || !strings.Contains(view, ">claude") {
		t.Errorf("without colour, the chart or selection is unreadable:\n%s", view)
	}
}

func TestWideUsageKeepsChartsBesideTheTableAndExpandsInPlace(t *testing.T) {
	r := sampleReport()
	r.ByHarness = nil
	for i := 0; i < 30; i++ {
		r.ByHarness = append(r.ByHarness, group(fmt.Sprintf("fixture-%02d", i), usage(1, 0, 1, 1, 0, 0)))
	}
	m := start(t, &fakeReports{report: r, daily: sampleDaily()})
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	chartOnHeader := slices.ContainsFunc(strings.Split(m.View(), "\n"), func(line string) bool {
		return strings.Contains(line, "HARNESS") && strings.Contains(line, "Daily trends")
	})
	if !chartOnHeader || !strings.Contains(m.View(), "Estimated cost by coding agent") {
		t.Fatalf("wide layout lacks graphs beside the table:\n%s", m.View())
	}
	assertTrendSize(t, m.View(), 120, 32)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	selected := strings.TrimSpace(m.table.SelectedRow()[0])
	tableHeight := m.table.Height()
	m = press(t, m, "e")
	expanded := m.View()
	if strings.Contains(expanded, "Daily trends") || !strings.Contains(expanded, "Expanded") ||
		!strings.Contains(expanded, ">"+selected) || m.table.Height() <= tableHeight {
		t.Errorf("expansion lost the selection or did not free space:\n%s", expanded)
	}
	assertTrendSize(t, expanded, 120, 32)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if view := m.View(); !strings.Contains(view, "Daily trends") || !strings.Contains(view, ">"+selected) {
		t.Errorf("restoring the layout lost the charts or selected row:\n%s", view)
	}
}

func TestHelpIsContextualAndClosesToTheSameView(t *testing.T) {
	m := start(t, &fakeReports{report: sampleReport(), daily: sampleDaily()})
	m = press(t, m, "g", "e")
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	m = press(t, m, "?")
	view := m.View()
	for _, want := range []string{"Keyboard shortcuts", "Expand current view", "Restore layout", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("help lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Select a usage table") {
		t.Errorf("chart help shows an unavailable command:\n%s", view)
	}
	assertTrendSize(t, view, 40, 15)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dailyOpen || !m.expanded || m.helpOpen {
		t.Fatal("closing help changed the underlying expanded view")
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dailyOpen || m.expanded {
		t.Fatal("Esc should restore the size before returning to the table")
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dailyOpen {
		t.Fatal("Esc should return to the table after restoring")
	}
}

func TestResizeKeepsSelectionWhenItsSortColumnIsHidden(t *testing.T) {
	m := send(t, start(t, &fakeReports{report: sampleReport()}), tea.WindowSizeMsg{Width: 120, Height: 24})
	for i := 0; i < 7; i++ {
		m = press(t, m, "s")
	}
	if m.sorts[m.tab].col != 6 {
		t.Fatal("precondition: expected the cost column to determine the order")
	}
	m = press(t, m, "o")
	before := m.table.SelectedRow()[0]
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	if selected := m.table.SelectedRow()[0]; strings.TrimSpace(selected) != strings.TrimSpace(before) {
		t.Errorf("selected %q before resize and %q after", before, selected)
	}
}

func assertTrendSize(t *testing.T, view string, width, height int) {
	t.Helper()
	if h := lipgloss.Height(view); h > height {
		t.Errorf("view is %d lines on a %d-line terminal:\n%s", h, height, view)
	}
	if w := lipgloss.Width(view); w > width {
		t.Errorf("view is %d cells on a %d-cell terminal:\n%s", w, width, view)
	}
}
