package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// promptsMsg carries the result of a prompt review request; seq tells which request it answers.
type promptsMsg struct {
	seq             int
	period, harness int // the filters the review was asked for
	restricted      bool
	review          reporting.PromptReview
	err             error
}

// promptsState is the presentation state of the prompts mode: the review as a body of lines to scroll.
type promptsState struct {
	review reporting.PromptReview
	loaded bool
	pager
}

// promptsEnabled reports whether the interface was given a way to review prompts.
func (m Model) promptsEnabled() bool { return m.reviewer.Queries != nil }

// loadPrompts asks the use case for the review of the current filters, off the UI loop.
func (m Model) loadPrompts() tea.Cmd {
	reviewer, q, seq, period, harness, restricted := m.reviewer, m.query(), m.seq, m.period, m.harness, m.restricted
	return func() tea.Msg {
		r, err := reviewer.Run(q)
		return promptsMsg{seq, period, harness, restricted, r, err}
	}
}

func (m Model) promptsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	case "a":
		return m.toggleRestriction()
	case "p":
		m.period = (m.period + 1) % len(m.periods)
		return m.reload()
	case "h":
		m.harness = (m.harness + 1) % len(harnessChoices)
		return m.reload()
	default:
		m.prompts.scrollKey(k)
	}
	return m, nil
}

// promptsView draws the prompt review under the title.
func (m Model) promptsView() string {
	var b strings.Builder
	b.WriteString(m.titleBar() + "\n")
	b.WriteString(activeTab.Render("Prompts") + faintStyle.Render("  what you keep correcting or repeating · nothing is written for you") + "\n")
	switch {
	case m.prompts.loaded:
		b.WriteString(m.prompts.visible() + "\n")
	default:
		b.WriteString(faintStyle.Render("loading…") + strings.Repeat("\n", max(0, m.prompts.display-1)) + "\n")
	}
	b.WriteString(m.status() + "\n")
	b.WriteString(faintStyle.Render(m.help()))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
}

const noFlaggedPrompts = "No flagged prompts in this range."

// reviewLines is the body of the review: how many prompts, the asks that came back with a hint, the folders they
// concentrate in, then the prompts, most signal categories first. Text from the logs is made printable and cut to width.
func reviewLines(r reporting.PromptReview, width int) []string {
	if len(r.Prompts) == 0 {
		return []string{noFlaggedPrompts}
	}
	var out []string
	section := func(title string) { out = append(out, "", titleStyle.Render(title)) }
	out = append(out, titleStyle.Render(fmt.Sprintf("%s in %s", textfmt.Plural(len(r.Prompts), "flagged prompt", "flagged prompts"), textfmt.Plural(r.Sessions, "session", "sessions"))))

	section("RECURRING")
	if len(r.Recurring) == 0 {
		out = append(out, faintStyle.Render("no ask came back"))
	}
	for _, a := range r.Recurring {
		out = append(out, "  "+textfmt.Excerpt(a.Text, max(10, width-2)), "    "+faintStyle.Render(a.Hint()))
	}

	section("FOLDERS")
	fg := grid{headers: []string{"FOLDER", "PROMPTS", "SESSIONS", "SIGNALS"}, numeric: []bool{false, true, true, true}}
	for _, f := range r.Folders {
		fg.rows = append(fg.rows, []cell{textCell(textfmt.Shorten(orDash(f.Folder), 60)), countCell(int64(f.Prompts)), countCell(int64(f.Sessions)), countCell(int64(f.Signals))})
	}
	out = append(out, plainTable(fg)...)

	section("PROMPTS")
	for _, p := range r.Prompts {
		meta := fmt.Sprintf("%s · %s · %s · %s", timeText(p.At, "2006-01-02 15:04"), p.Harness, textfmt.Excerpt(sessionTitle(reporting.SessionSummary{Title: p.Title}), 40), textfmt.Excerpt(orDash(p.Folder), 40))
		out = append(out, meta, "    "+errorStyle.Render(signalTags(p.Signals)), "    "+textfmt.Excerpt(p.Text, max(10, width-4)))
	}

	cut := lipgloss.NewStyle().MaxWidth(max(1, width))
	for i, l := range out {
		out[i] = cut.Render(l)
	}
	return out
}
