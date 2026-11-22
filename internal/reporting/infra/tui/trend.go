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

// trendPreview reserves room for a row of the selected table and the keyboard help.
func (m Model) trendPreview() []string {
	if !m.loaded || m.width < 40 || m.height < chromeLines+m.extraTitleLines()+8 {
		return nil
	}
	if len(m.daily) == 0 {
		if m.report.Total.Requests == 0 {
			return nil
		}
		return []string{"Daily usage is unavailable without request dates."}
	}

	start, end, days := recentDays(m.daily, min(trendDays, m.width-12-lipgloss.Width(costLegend)))
	if len(days) == 0 {
		return nil
	}
	return []string{
		titleStyle.Render("Daily usage") + fmt.Sprintf(" · %s to %s", start, end),
		"Requests    " + chartStyle.Render(sparkline(days, func(u reporting.Usage) float64 { return float64(u.Requests) })),
		"Tokens      " + chartStyle.Render(sparkline(days, func(u reporting.Usage) float64 { return float64(u.InputWithCache()) + float64(u.Output) })),
		"Known cost  " + chartStyle.Render(sparkline(days, knownCost)) + costLegend,
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

func (m Model) dailyView() string {
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join([]string{
		m.titleBar(), titleStyle.Render("Daily usage"), m.dailyPager.visible(), m.status(), m.help(),
	}, "\n"))
}

func (m Model) dailyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "esc":
		m.dailyOpen = false
	case "m", "q", "ctrl+c", "r", "p", "h", "a":
		return m.usageKey(msg)
	default:
		m.dailyPager.scrollKey(k)
	}
	return m, nil
}
