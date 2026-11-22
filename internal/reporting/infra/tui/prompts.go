package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

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

type promptSection int

const (
	flaggedSection promptSection = iota
	recurringSection
	foldersSection
	promptSectionCount
)

var promptSections = []string{"Flagged", "Recurring", "Folders"}

// promptsState preserves the selected rows independently of an open prompt or group.
type promptsState struct {
	review  reporting.PromptReview
	loaded  bool
	section promptSection
	list    readingList
	group   promptGroup
	reader  readerState
	pager
}

type promptGroup struct {
	open    bool
	title   string
	note    string
	folder  string
	ask     reporting.RecurringAsk
	prompts []reporting.FlaggedPrompt
	list    readingList
}

func (p *promptsState) activeList() *readingList {
	if p.group.open {
		return &p.group.list
	}
	return &p.list
}

func (p promptsState) occurrences() []reporting.FlaggedPrompt {
	if p.group.open {
		return p.group.prompts
	}
	return p.review.Prompts
}

func (p *promptsState) setReview(review reporting.PromptReview) {
	p.review, p.loaded = review, true
	if p.group.open {
		p.readGroup()
	}
}

func (p *promptsState) readGroup() {
	g := &p.group
	g.prompts = nil
	if g.ask.Count > 0 {
		g.prompts = p.review.Occurrences(g.ask)
		sessions := make(map[reporting.SessionID]bool)
		for _, prompt := range g.prompts {
			sessions[prompt.Session] = true
		}
		g.note = fmt.Sprintf("%s in %s", textfmt.Plural(len(g.prompts), "occurrence", "occurrences"),
			textfmt.Plural(len(sessions), "session", "sessions"))
		return
	}
	for _, prompt := range p.review.Prompts {
		if prompt.Folder == g.folder {
			g.prompts = append(g.prompts, prompt)
		}
	}
}

// promptsEnabled reports whether the interface was given a way to review prompts.
func (m Model) promptsEnabled() bool { return m.reviewer.Queries != nil }

// loadPrompts asks the use case for the review of the current filters, off the UI loop.
func (m Model) loadPrompts() tea.Cmd {
	reviewer, q, seq, period, harness, restricted := m.reviewer, m.query(), m.seq, m.period, m.harness, m.restricted
	return func() tea.Msg {
		r, err := reviewer.Run(context.Background(), q)
		return promptsMsg{seq, period, harness, restricted, r, err}
	}
}

func (m Model) promptsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.prompts
	if p.reader.open {
		return m.readerKey(msg)
	}
	list := p.activeList()
	switch k := msg.String(); k {
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
	case "esc", "backspace":
		if !list.search.cancel() && p.group.open {
			p.group = promptGroup{}
		}
	case "tab", "right":
		if !p.group.open {
			p.section, p.list = (p.section+1)%promptSectionCount, readingList{}
		}
	case "shift+tab", "left":
		if !p.group.open {
			p.section, p.list = (p.section+promptSectionCount-1)%promptSectionCount, readingList{}
		}
	case "/":
		list.search.begin()
	case "enter":
		if i := list.selected(); i >= 0 {
			p.openSelected(i)
		}
	default:
		list.move(k)
	}
	m.layout()
	return m, nil
}

func (p *promptsState) openSelected(i int) {
	switch {
	case p.group.open || p.section == flaggedSection:
		row := p.occurrences()[i]
		note := flaggedPromptLabel(row)
		p.reader = readerState{open: true, title: "Prompt", label: note, text: row.Text, source: row.Session}
	case p.section == recurringSection:
		ask := p.review.Recurring[i]
		p.group = promptGroup{open: true, title: "Recurring", ask: ask}
		p.readGroup()
	case p.section == foldersSection:
		folder := p.review.Folders[i].Folder
		p.group = promptGroup{open: true, title: "Folder", note: textfmt.Printable(orDash(folder)), folder: folder}
		p.readGroup()
	}
}

func (p *promptsState) layout(width, height int) {
	if p.reader.open {
		p.reader.layout(width, height)
		return
	}
	list := p.activeList()
	var out []readingRow
	empty := "loading…"
	if p.loaded {
		out, empty = p.rows()
	}
	chrome := 1
	if p.group.open || (p.loaded && len(p.review.Prompts) > 0) {
		chrome++
	}
	list.layout(out, width, height-chrome-list.search.rows(), empty)
}

func (p promptsState) rows() ([]readingRow, string) {
	var out []readingRow
	switch {
	case p.group.open || p.section == flaggedSection:
		for _, row := range p.occurrences() {
			out = append(out, readingRow{row.Text, flaggedPromptLabel(row)})
		}
		return out, noFlaggedPrompts
	case p.section == recurringSection:
		for _, ask := range p.review.Recurring {
			out = append(out, readingRow{ask.Text, ask.Hint()})
		}
		return out, "No recurring asks in this range."
	default:
		for _, f := range p.review.Folders {
			out = append(out, readingRow{orDash(f.Folder), fmt.Sprintf("%s · %s · %s",
				textfmt.Plural(f.Prompts, "prompt", "prompts"), textfmt.Plural(f.Sessions, "session", "sessions"), textfmt.Plural(f.Signals, "signal", "signals"))})
		}
		return out, "No folders in this range."
	}
}

// Signals lead the metadata so narrower terminals keep what caused the flag.
func flaggedPromptLabel(p reporting.FlaggedPrompt) string {
	return signalTags(p.Signals) + " · " + timeText(p.At, "2006-01-02 15:04") + " · " + p.Harness.String() + " · " +
		textfmt.Excerpt(sessionTitle(reporting.SessionSummary{Title: p.Title}), 40) + " · " + textfmt.Excerpt(orDash(p.Folder), 40)
}

func (m Model) promptsView() string {
	p := m.prompts
	trail := "Prompts"
	if p.group.open {
		trail += " › " + p.group.title
	}
	if p.reader.open {
		return m.readerView(p.reader, trail)
	}
	tabs := readingTabs(promptSections, int(p.section), m.width)
	list := p.activeList()
	body := list.visible()
	if p.group.open {
		tabs = p.group.note
	} else if p.loaded && len(p.review.Prompts) > 0 {
		tabs += "\n" + fmt.Sprintf("%s in %s", textfmt.Plural(len(p.review.Prompts), "flagged prompt", "flagged prompts"), textfmt.Plural(p.review.Sessions, "session", "sessions"))
	}
	return m.readingPage(trail, tabs, list.search.view(m.width, "Filter"), body)
}

const noFlaggedPrompts = "No flagged prompts in this range."
