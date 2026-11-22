package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) layoutHelp() {
	if !m.helpOpen {
		return
	}
	m.helpPager.display = max(1, m.height-3)
	lines := []string{"Esc         Restore layout or go back"}
	if m.dashboard.open {
		lines = append(lines, "g           Charts and daily values")
	} else {
		lines = append(lines, "e           Expand current view")
	}
	if m.mode == usageMode && !m.dailyOpen && !m.dashboard.open {
		lines = append(lines, "1–5         Select a usage table", "g           Charts and daily values")
	}
	if m.modeEnabled(catalogMode) && m.mode != usageMode && m.mode != catalogMode {
		lines = append(lines, "c           Open catalog")
	}
	if m.modeEnabled(quotasMode) && m.mode != usageMode && m.mode != quotasMode {
		lines = append(lines, "l           Open quotas")
	}
	for _, hint := range m.shortcuts() {
		key, label, _ := strings.Cut(hint, " ")
		if key == "g" || key == "esc" {
			continue
		}
		lines = append(lines, pad(key, 10, false)+"  "+label)
	}
	m.helpPager.lines = lines
	m.helpPager.scrollBy(0)
}

func (m Model) helpView() string {
	title := modeNames[m.mode]
	if m.dashboard.open {
		title = "Dashboard"
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join([]string{
		titleStyle.Render("Keyboard shortcuts · " + title),
		"",
		m.helpPager.visible(),
		"↑↓ scroll · Esc / ? close · q quit",
	}, "\n"))
}
