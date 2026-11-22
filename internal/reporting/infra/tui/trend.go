package tui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

const (
	trendDays  = 30
	costLegend = "  ? unknown"
)

// overview reserves at most two lines for totals, leaving the rest of Usage to the table.
func (m Model) overview() []string {
	if !m.loaded {
		return []string{"Loading usage…"}
	}
	total := m.report.Total
	if total.Requests == 0 {
		if m.tab == toolsTab && len(m.report.Tools) == 0 {
			return []string{noToolCalls}
		}
		return []string{noRequests}
	}
	if m.expanded {
		return nil
	}

	requests := "Requests " + textfmt.Number(total.Requests)
	if total.Failed > 0 {
		requests += " (" + textfmt.Number(total.Failed) + " failed)"
	}
	known := "Known cost " + displayCost(total)
	tokens := "Tokens " + textfmt.Number(total.InputWithCache()+total.Output)
	cache := fmt.Sprintf("Input from cache %.0f%%", total.CacheRate()*100)
	totals := requests + " · " + known
	if lipgloss.Width(totals) > m.width {
		return []string{requests + " · " + tokens, known + fmt.Sprintf(" · Cache %.0f%%", total.CacheRate()*100)}
	}
	mix := tokens + fmt.Sprintf(" · Input %s · Output %s · %s",
		textfmt.Number(total.InputWithCache()), textfmt.Number(total.Output), cache)
	if lipgloss.Width(mix) > m.width {
		mix = tokens + " · " + cache
	}
	return []string{totals, mix}
}

// chartLines shares the same trends and cost breakdown between the sidebar and the charts view.
func (m Model) chartLines(width int) []string {
	const labelWidth = 12
	var lines []string
	if len(m.daily) > 0 && width > labelWidth+lipgloss.Width(costLegend) {
		from, to, days := recentDays(m.daily, min(trendDays, width-labelWidth-lipgloss.Width(costLegend)))
		if len(days) > 0 {
			lines = append(lines, titleStyle.Render("Daily trends"), from+" to "+to)
			for _, metric := range []struct {
				label string
				value func(reporting.Usage) float64
			}{
				{"Requests", func(u reporting.Usage) float64 { return float64(u.Requests) }},
				{"Tokens", func(u reporting.Usage) float64 { return float64(u.InputWithCache()) + float64(u.Output) }},
				{"Known cost", knownCost},
			} {
				graph := sparkline(days, metric.value)
				line := pad(metric.label, labelWidth, false) + chartStyle.Render(graph)
				if strings.Contains(graph, "?") {
					line += costLegend
				}
				lines = append(lines, line)
			}
		}
	}
	bars := m.costByHarness(width)
	if len(lines) > 0 && len(bars) > 0 {
		lines = append(lines, "")
	}
	return append(lines, bars...)
}

// costByHarness shows the leading rows of the report's cost-ordered harness breakdown.
func (m Model) costByHarness(width int) []string {
	const title = "Estimated cost by coding agent"
	count := min(len(m.report.ByHarness), 3)
	if count < 1 || width < lipgloss.Width(title) {
		return nil
	}
	groups := m.report.ByHarness[:count]
	labels, amounts := make([]string, count), make([]string, count)
	amountWidth := 0
	peak := reporting.USD(0)
	const labelWidth = 11
	for i, group := range groups {
		label := group.Group
		switch label {
		case "claude":
			label = "Claude Code"
		case "codex":
			label = "Codex"
		}
		labels[i] = lipgloss.NewStyle().MaxWidth(labelWidth).Render(textfmt.Printable(label))
		amounts[i] = displayCost(group.Usage)
		amountWidth = max(amountWidth, lipgloss.Width(amounts[i]))
		peak = max(peak, group.Cost)
	}
	barWidth := min(34, width-labelWidth-amountWidth-4)
	if barWidth < 4 {
		return nil
	}

	heading := title
	if count < len(m.report.ByHarness) {
		limited := fmt.Sprintf("%s · top %d", title, count)
		if lipgloss.Width(limited) <= width {
			heading = limited
		}
	}
	lines := []string{titleStyle.Render(heading)}
	for i, group := range groups {
		length := 0
		if peak > 0 && group.Cost > 0 {
			length = max(1, int(math.Round(float64(group.Cost)/float64(peak)*float64(barWidth))))
		}
		bar := costBarStyle.Render(strings.Repeat("━", length))
		label := labels[i] + strings.Repeat(" ", labelWidth-lipgloss.Width(labels[i]))
		value := pad(amounts[i], amountWidth, true)
		lines = append(lines, label+"  "+bar+strings.Repeat(" ", barWidth-length)+"  "+value)
	}
	return lines
}

func displayCost(u reporting.Usage) string {
	switch {
	case u.Requests > 0 && u.Requests == u.Unpriced:
		return "Unavailable"
	case u.Cost > 0 && u.Cost < 0.01:
		return "<$0.01"
	default:
		return fmt.Sprintf("$%.2f", float64(u.Cost))
	}
}

// recentDays covers calendar days, so a gap in activity gets a zero column rather than closing the time axis.
func recentDays(daily []reporting.UsageGroup, limit int) (start, end string, days []reporting.Usage) {
	first, err := time.Parse(time.DateOnly, daily[0].Group)
	if err != nil {
		return "", "", nil
	}
	last, err := time.Parse(time.DateOnly, daily[len(daily)-1].Group)
	if err != nil {
		return "", "", nil
	}
	from := last.AddDate(0, 0, 1-limit)
	if first.After(from) {
		from = first
	}

	i := len(daily) - 1
	for day := last; !day.Before(from); day = day.AddDate(0, 0, -1) {
		var value reporting.Usage
		if i >= 0 && daily[i].Group == day.Format(time.DateOnly) {
			value = daily[i].Usage
			i--
		}
		days = append(days, value)
	}
	slices.Reverse(days)
	return from.Format(time.DateOnly), last.Format(time.DateOnly), days
}

// knownCost returns a negative sentinel for a day whose requests all have unknown costs.
func knownCost(u reporting.Usage) float64 {
	if u.Requests > 0 && u.Requests == u.Unpriced {
		return -1
	}
	return float64(u.Cost)
}

func sparkline(days []reporting.Usage, value func(reporting.Usage) float64) string {
	levels := []rune("·▁▂▃▄▅▆▇█")
	peak := 0.0
	for _, d := range days {
		peak = max(peak, value(d))
	}

	var out strings.Builder
	for _, d := range days {
		n := value(d)
		if n < 0 {
			out.WriteByte('?')
			continue
		}
		level := 0
		if peak > 0 {
			level = int(math.Ceil(n / peak * float64(len(levels)-1)))
		}
		out.WriteRune(levels[level])
	}
	return out.String()
}

func dailyLines(daily []reporting.UsageGroup) []string {
	if len(daily) == 0 {
		return []string{"No dated requests in this range."}
	}
	lines := []string{"Only dated requests are included."}
	for _, d := range daily {
		day := textfmt.UsageDay(d.Group, d.Requests, d.InputWithCache()+d.Output, float64(d.Cost), d.Unpriced)
		day[0] = titleStyle.Render(day[0])
		lines = append(lines, day...)
	}
	return lines
}

func (m Model) dailyContent() []string {
	if !m.loaded {
		return []string{"Loading daily usage…"}
	}
	lines := m.chartLines(m.width)
	if len(lines) > 0 {
		lines = append(lines, "", titleStyle.Render("Daily values"))
	}
	return append(lines, dailyLines(m.daily)...)
}

func (m Model) dailyView() string {
	title := "Daily usage"
	if p := m.dailyPager; len(p.lines) > p.display {
		title += fmt.Sprintf(" · %d–%d of %d lines", p.scroll+1, p.scroll+p.display, len(p.lines))
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join([]string{
		m.titleBar(), titleStyle.Render(title), m.dailyPager.visible(), m.status(), m.help(),
	}, "\n"))
}

func (m Model) dailyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "esc":
		m.dailyOpen = false
		m.layout()
	case "m", "r", "p", "h", "a":
		return m.usageKey(msg)
	default:
		m.dailyPager.scrollKey(k)
	}
	return m, nil
}
