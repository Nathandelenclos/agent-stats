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
	for _, want := range []string{"Daily usage", "2026-11-01 to 2026-11-03", "Requests", "Tokens", "Known cost", "▄·█", "█·?", "? unknown", "[1 Harness]", "g daily values", "q quit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("overview lacks %q:\n%s", want, m.View())
		}
	}

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
	for _, want := range []string{"Daily usage", "2026-11-01", "2026-11-03", "1 500 tokens", "esc back", "q quit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("daily values lack %q:\n%s", want, m.View())
		}
	}
	assertTrendSize(t, m.View(), 40, 15)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), "Cost unavailable") {
		t.Errorf("the cost of unpriced requests appeared as zero:\n%s", m.View())
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(m.View(), selected) || !strings.Contains(m.View(), "g daily") {
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
	for _, want := range []string{"Overview", "Known cost", "Unavailable", "No tool calls in this range.", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("overview lacks %q:\n%s", want, view)
		}
	}
	if !slices.ContainsFunc(strings.Split(view, "\n"), func(line string) bool {
		return strings.HasPrefix(line, "Known cost") && strings.Contains(line, "Unavailable") && strings.Contains(line, "?·?")
	}) {
		t.Errorf("unknown cost should be marked on the same row as its trend:\n%s", view)
	}
	assertTrendSize(t, view, 80, 24)

	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	view = m.View()
	if !strings.Contains(view, "Unavailable") || !strings.Contains(view, "g daily") || strings.Contains(view, "?·?") {
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
	m := send(t, start(t, &fakeReports{report: r}), tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
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
	m := start(t, &fakeReports{report: sampleReport(), daily: sampleDaily()})
	view := m.View()
	if strings.Contains(view, "\x1b[") || !strings.Contains(view, "Requests") || !strings.Contains(view, "▄·█") || !strings.Contains(view, ">claude") {
		t.Errorf("without colour, the chart or selection is unreadable:\n%s", view)
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
