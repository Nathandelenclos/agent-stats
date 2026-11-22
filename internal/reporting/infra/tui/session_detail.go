package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type sessionSection int

const (
	sessionSummarySection sessionSection = iota
	sessionModelsSection
	sessionToolsSection
	sessionSubagentsSection
	sessionPromptsSection
	sessionSectionCount
)

var sessionSections = []string{"Summary", "Models", "Tools", "Subagents", "Prompts"}

func (d *sessionDetail) changeSection(section sessionSection) {
	if section != d.section {
		d.section, d.scroll, d.list = section, 0, readingList{}
	}
}

func (m *Model) layoutReadingViews(height int) {
	if m.mode == sessionsMode && m.sessions.detail.open {
		m.sessions.detail.layout(m.width, height-4)
	}
	if m.mode == promptsMode {
		m.prompts.layout(m.width, height-4)
	}
}

func (d *sessionDetail) layout(width, height int) {
	if d.reader.open {
		d.reader.layout(width, height)
		return
	}
	d.display = max(1, height-1)
	switch {
	case d.err != nil:
		d.lines = wrappedText("error: "+d.err.Error(), width)
	case !d.loaded:
		d.lines = []string{"loading…"}
	case d.section == sessionSummarySection:
		data := d.data
		metrics := fmt.Sprintf("%s · %s requests (%s failed) · %s subagents · %s prompts, average %s characters",
			displayCost(data.Usage), textfmt.Number(data.Requests), textfmt.Number(data.Failed), textfmt.Number(int64(data.SubagentCount)),
			textfmt.Number(data.PromptCount), avgText(data.SessionSummary))
		d.lines = wrappedText(sessionTitle(data.SessionSummary)+"\n\n"+data.Harness.String()+" · "+orDash(data.Folder)+" · "+span(data.SessionSummary)+"\n\n"+metrics, width)
		d.lines = append(d.lines, "", "Use ← → to browse session sections.")
		if data.Unpriced > 0 {
			d.lines = append(d.lines, wrappedText(fmt.Sprintf("Cost known for %s of %s requests.", textfmt.Number(data.Requests-data.Unpriced), textfmt.Number(data.Requests)), width)...)
		}
	default:
		d.list.layout(d.rows(), width, height-1-d.list.search.rows(), "No "+strings.ToLower(sessionSections[d.section])+" in this session.")
	}
	d.scrollBy(0)
}

func (d sessionDetail) rows() []readingRow {
	var rows []readingRow
	switch d.section {
	case sessionModelsSection:
		for _, r := range d.data.Models {
			rows = append(rows, readingRow{r.Group, fmt.Sprintf("%s requests · %s · %.0f%% share", textfmt.Number(r.Requests), displayCost(r.Usage), d.data.ModelShare(r)*100)})
		}
	case sessionToolsSection:
		for _, r := range d.data.Tools {
			rows = append(rows, readingRow{toolLabel(r), toolCounts(r)})
		}
	case sessionSubagentsSection:
		for _, r := range d.data.Subagents {
			rows = append(rows, readingRow{sessionTitle(reporting.SessionSummary{Title: r.Title}),
				fmt.Sprintf("%s · %s requests · %s", r.AgentType, textfmt.Number(r.Requests), displayCost(r.Usage))})
		}
	case sessionPromptsSection:
		for i, r := range d.data.Prompts {
			rows = append(rows, readingRow{fmt.Sprintf("#%d  %s", i+1, r.Text), promptLabel(r.At, r.Chars, r.Signals)})
		}
	}
	return rows
}

func (m Model) sessionDetailView() string {
	d := m.sessions.detail
	trail := "Sessions › Session"
	if d.fromPrompt {
		trail = "Prompts › Source session"
	}
	if d.reader.open {
		return m.readerView(d.reader, trail+" › "+sessionSections[d.section])
	}
	tabs := readingTabs(sessionSections, int(d.section), m.width)
	if d.section != sessionSummarySection && d.loaded && d.err == nil {
		return m.readingPage(trail, tabs, d.list.search.view(m.width, "Filter"), d.list.visible())
	}
	return m.readingPage(trail, tabs, "", d.visible())
}

func (m Model) openSessionItem(i int) (tea.Model, tea.Cmd) {
	d := &m.sessions.detail
	var title, label, text string
	switch d.section {
	case sessionModelsSection:
		row := d.data.Models[i]
		title, label = row.Group, "Model usage in this session, including its subagents."
		text = measuredUsage(row.Usage)
	case sessionToolsSection:
		row := d.data.Tools[i]
		title, label = toolLabel(row), "Calls in this session, including its subagents."
		text = toolCounts(row)
		if row.AvgDuration > 0 {
			text += fmt.Sprintf("\nAverage measured duration %.0f ms", float64(row.AvgDuration))
		}
		text += fmt.Sprintf("\nArguments %s chars\nResults %s chars", textfmt.Number(row.ArgsChars), textfmt.Number(row.ResultChars))
	case sessionSubagentsSection:
		row := d.data.Subagents[i]
		title, label = sessionTitle(reporting.SessionSummary{Title: row.Title}), "Subagent · "+row.AgentType
		text = measuredUsage(row.Usage)
	case sessionPromptsSection:
		return m.openSessionPrompt(d.data.Prompts[i], i)
	}
	d.reader = readerState{open: true, title: textfmt.Printable(title), label: label, text: text}
	m.layout()
	return m, nil
}

func promptLabel(at time.Time, chars int, signals reporting.PromptSignals) string {
	label := timeText(at, "01-02 15:04:05") + " · " + textfmt.Number(int64(chars)) + " characters"
	if tags := signalTags(signals); tags != "" {
		label += " · " + tags
	}
	return label
}

func toolLabel(row reporting.ToolUsage) string {
	if row.Server != "" {
		return row.Server + " / " + row.Name
	}
	return row.Name
}

func toolCounts(row reporting.ToolUsage) string {
	return fmt.Sprintf("%s calls · %s errors", textfmt.Number(row.Calls), textfmt.Number(row.Errors))
}

func measuredUsage(u reporting.Usage) string {
	return fmt.Sprintf("Requests %s · failed %s\nKnown cost %s\nInput tokens %s\nOutput tokens %s\nCache read %s\nCache write %s",
		textfmt.Number(u.Requests), textfmt.Number(u.Failed), displayCost(u), textfmt.Number(u.InputWithCache()), textfmt.Number(u.Output),
		textfmt.Number(u.CacheRead), textfmt.Number(u.CacheWrite))
}
