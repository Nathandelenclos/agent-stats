package tui

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

type catalogMsg struct {
	seq, period, harness int
	restricted           bool
	result               catalog.Catalog
	err                  error
}

type quotasMsg struct {
	seq     int
	windows []quota.Window
}

func (m Model) loadCatalog() tea.Cmd {
	read, q, seq, period, harness, restricted := m.catalog, m.query(), m.seq, m.period, m.harness, m.restricted
	unused := m.catalogUnused
	return func() tea.Msg {
		result, err := read(catalog.Query{Since: q.Since, Harness: q.Harness, Project: q.Project, UnusedOnly: unused})
		return catalogMsg{seq, period, harness, restricted, result, err}
	}
}

func (m Model) loadQuotas() tea.Cmd {
	read, seq, ctx := m.quotas, m.seq, m.syncs.ctx
	return func() tea.Msg { return quotasMsg{seq, read(ctx)} }
}

func (m Model) localDataKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "m":
		return m.switchMode()
	case "r":
		if m.mode == quotasMode {
			return m.reload()
		}
		return m.reindex()
	case "p", "h", "a", "u":
		if m.mode == catalogMode {
			m.localPager.scroll = 0
			switch k {
			case "p":
				m.period = (m.period + 1) % len(m.periods)
			case "h":
				m.harness = (m.harness + 1) % len(harnessChoices)
			case "a":
				return m.toggleRestriction()
			case "u":
				m.catalogUnused = !m.catalogUnused
				m.localLoaded = false
			}
			return m.reload()
		}
	default:
		m.localPager.scrollKey(k)
	}
	return m, nil
}

func (m *Model) layoutLocalData() {
	if m.mode != catalogMode && m.mode != quotasMode {
		return
	}
	m.localPager.display = max(1, m.height-m.extraTitleLines()-4)
	if !m.localLoaded {
		m.localPager.lines = []string{"Loading…"}
		if !m.pending && m.err != nil {
			m.localPager.lines = []string{"Catalog unavailable."}
		}
	} else if m.mode == catalogMode {
		m.localPager.lines = catalogLines(m.catalogResult, m.catalogUnused, m.width)
	} else {
		m.localPager.lines = quotaLines(m.quotaResult, m.width)
	}
	m.localPager.scrollBy(0)
}

func (m Model) localDataView() string {
	title := modeNames[m.mode]
	if m.mode == catalogMode && m.catalogUnused {
		title += " · Unused only"
	}
	status := m.status()
	if m.mode == quotasMode {
		status = ""
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join([]string{
		m.titleBar(), titleStyle.Render(title), m.localPager.visible(), status, m.help(),
	}, "\n"))
}

func catalogLines(result catalog.Catalog, unused bool, width int) []string {
	if len(result.Entries) == 0 {
		if unused {
			return []string{"No unused items in these filters."}
		}
		return []string{"No catalog items in these filters."}
	}
	lines := []string{textfmt.Plural(len(result.Entries), "item", "items")}
	if count := len(result.Skipped); count > 0 {
		lines = append(lines, textfmt.Plural(count, "configuration skipped", "configurations skipped"))
	}
	for _, row := range result.Entries {
		name := textfmt.Printable(row.Name.String())
		if row.Disabled {
			name += " (disabled)"
		}
		activity := "Usage unknown"
		if row.Tracked {
			activity = textfmt.Number(row.Uses) + " " + row.Unit().String()
		}
		scope := row.Scope.String()
		if row.Scope.String() == "plugin" && row.Plugin() != "" {
			scope += ":" + textfmt.Printable(row.Plugin())
		}
		detail := fmt.Sprintf("  %s · %s %s · %s", activity, row.Harness, row.Kind, scope)
		last := ""
		if !row.Last.IsZero() {
			last = "Last used " + row.Last.Local().Format("2006-01-02")
		}
		lines = append(lines, titleStyle.Render(name))
		if last != "" && lipgloss.Width(detail+" · "+last) > width {
			lines = append(lines, detail, "  "+last)
		} else if last != "" {
			lines = append(lines, detail+" · "+last)
		} else {
			lines = append(lines, detail)
		}
	}
	return lines
}

func quotaLines(windows []quota.Window, width int) []string {
	var lines []string
	for _, provider := range []string{"Codex", "Claude Code"} {
		heading := len(lines)
		lines = append(lines, titleStyle.Render(provider))
		found := false
		for _, window := range windows {
			if window.Provider != provider {
				continue
			}
			if !found && !window.Observed.IsZero() {
				lines[heading] += " · checked " + window.Observed.Local().Format("15:04")
			}
			found = true
			value := fmt.Sprintf("%.1f%% remaining", window.Remaining)
			label := "  " + textfmt.Printable(window.Label)
			if lipgloss.Width(label+" · "+value) <= width {
				lines = append(lines, label+" · "+value)
			} else {
				lines = append(lines, label, "  "+value)
			}
			barWidth := min(20, width-2)
			if barWidth >= 5 {
				filled := max(0, min(barWidth, int(math.Round(window.Remaining/100*float64(barWidth)))))
				lines = append(lines, "  "+chartStyle.Render(strings.Repeat("━", filled))+strings.Repeat("·", barWidth-filled))
			}
			if !window.ResetAt.IsZero() {
				lines = append(lines, "  Resets "+window.ResetAt.Local().Format("Jan 2, 15:04"))
			}
		}
		if !found {
			lines = append(lines, "  Quota unavailable")
		}
	}
	return lines
}
