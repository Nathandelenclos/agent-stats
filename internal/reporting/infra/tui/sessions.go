package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// mode is what the interface shows: the usage report in tabs, or the sessions behind it.
type mode int

const (
	usageMode mode = iota
	sessionsMode
)

var modeNames = [...]string{"Usage", "Sessions"}

// sessionsMsg carries the result of a sessions request; seq tells which request it answers.
type sessionsMsg struct {
	seq             int
	period, harness int  // the filters the list was asked for
	scoped          bool // and whether it was restricted to the project
	list            reporting.SessionList
	err             error
}

// detailMsg carries the description of one session; seq tells which request it answers.
type detailMsg struct {
	seq    int
	id     reporting.SessionID
	detail reporting.SessionDetail
	err    error
}

// sessionsState is the presentation state of the sessions mode.
type sessionsState struct {
	list   reporting.SessionList
	loaded bool
	sort   sortState
	table  table.Model
	shown  []reporting.SessionSummary // the listed sessions in the order the table shows them
	detail sessionDetail
	// detailSeq is the latest detail request, whatever session it was for: an older answer is ignored, even for
	// the session on screen (it may describe the session before it grew).
	detailSeq int
}

// sessionDetail is the screen of one session: Esc closes it.
type sessionDetail struct {
	open    bool
	id      reporting.SessionID
	data    reporting.SessionDetail
	loaded  bool
	err     error
	lines   []string // the body, cut to the terminal's width
	scroll  int      // the first line shown
	display int      // the lines the body can show
}

func newSessionsState() sessionsState {
	return sessionsState{sort: reportOrder, table: table.New(table.WithFocused(true))}
}

// sessionsEnabled reports whether the interface was given a way to read sessions.
func (m Model) sessionsEnabled() bool { return m.lister.Queries != nil && m.shower.Queries != nil }

// loadSessions asks the use case for the sessions of the current filters, off the UI loop.
func (m Model) loadSessions() tea.Cmd {
	lister, q, seq, period, harness, scoped := m.lister, m.query(), m.seq, m.period, m.harness, m.scoped
	return func() tea.Msg {
		list, err := lister.Run(q)
		return sessionsMsg{seq, period, harness, scoped, list, err}
	}
}

// loadDetail asks the use case to describe a session, off the UI loop; answers to earlier requests no longer count.
func (m *Model) loadDetail(id reporting.SessionID) tea.Cmd {
	shower := m.shower
	m.sessions.detailSeq++
	seq := m.sessions.detailSeq
	return func() tea.Msg {
		d, err := shower.Run(id)
		return detailMsg{seq, id, d, err}
	}
}

// switchMode moves between the usage report and the sessions, asking for what the new mode shows.
func (m Model) switchMode() (Model, tea.Cmd) {
	if !m.sessionsEnabled() {
		return m, nil
	}
	m.sessions.detail = sessionDetail{}
	m.mode = (m.mode + 1) % mode(len(modeNames))
	return m.reload()
}

// selectedSession is the session on the selected row, the zero SessionID when there is none.
func (m Model) selectedSession() reporting.SessionID {
	if i := m.sessions.table.Cursor(); i >= 0 && i < len(m.sessions.shown) {
		return m.sessions.shown[i].ID
	}
	return reporting.SessionID{}
}

// openSelected opens the detail of the session on the selected row.
func (m Model) openSelected() (Model, tea.Cmd) {
	id := m.selectedSession()
	if id == (reporting.SessionID{}) {
		return m, nil
	}
	m.sessions.detail = sessionDetail{open: true, id: id}
	m.layout()
	detail := m.loadDetail(id) // before the return: loadDetail counts the request in m
	return m, detail
}

func (m Model) sessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	case "enter":
		return m.openSelected()
	case "p":
		m.period = (m.period + 1) % len(m.periods)
		return m.reload()
	case "h":
		m.harness = (m.harness + 1) % len(harnessChoices)
		return m.reload()
	case "a":
		return m.toggleScope()
	case "s":
		m.sessions.sort = m.sessions.sort.next(m.sessionsGrid())
		m.layout()
		m.sessions.table.SetCursor(0)
	case "o":
		if s := m.sessions.sort; s.col >= 0 {
			m.sessions.sort.desc = !s.desc
			m.layout()
			m.sessions.table.SetCursor(0)
		}
	default:
		var cmd tea.Cmd
		m.sessions.table, cmd = m.sessions.table.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) detailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.sessions.detail
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "backspace":
		m.sessions.detail = sessionDetail{}
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	case "up", "k":
		d.scrollBy(-1)
	case "down", "j":
		d.scrollBy(1)
	case "pgup", "b":
		d.scrollBy(-d.display)
	case "pgdown", "f", " ":
		d.scrollBy(d.display)
	case "home", "g":
		d.scrollBy(-len(d.lines))
	case "end", "G":
		d.scrollBy(len(d.lines))
	}
	return m, nil
}

func (d *sessionDetail) scrollBy(n int) {
	d.scroll = max(0, min(d.scroll+n, len(d.lines)-d.display))
}

// sessionTitle names a session in a table or a heading.
func sessionTitle(s reporting.SessionSummary) string {
	if strings.TrimSpace(s.Title) == "" {
		return "(untitled)"
	}
	return s.Title
}

// sessionsGrid lays out the sessions, the most important columns first: what a session cost and what the user typed
// come right after its title, so a narrow terminal leaves out the columns at the end, whole (see narrowedTo).
func sessionsGrid(list []reporting.SessionSummary) grid {
	g := grid{
		headers: []string{"TITLE", "COST", "PROMPTS", "AVG PROMPT", "REQUESTS", "DURATION", "START", "HARNESS", "MODELS", "SUBAGENTS", "AGENT", "FOLDER"},
		numeric: []bool{false, true, true, true, true, true, false, false, false, true, false, false},
	}
	for _, s := range list {
		g.rows = append(g.rows, []cell{
			textCell(sessionTitle(s)),
			{fmt.Sprintf("$%.2f", float64(s.Cost)), float64(s.Cost)},
			countCell(s.PromptCount),
			avgPromptCell(s),
			countCell(s.Requests),
			durationCell(s),
			{timeText(s.Start, "2006-01-02 15:04"), unixMillis(s.Start)},
			textCell(s.Harness.String()),
			textCell(modelsText(s)),
			countCell(int64(s.SubagentCount)),
			textCell(textfmt.Head(s.AgentType, 16)),
			textCell(textfmt.Shorten(s.Folder, 28)),
		})
	}
	return g
}

// sessionsGrid is the table of the sessions as far as the terminal's width holds it.
func (m Model) sessionsGrid() grid {
	return sessionsGrid(m.sessions.list.Sessions).narrowedTo(m.width)
}

func timeText(t time.Time, layout string) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format(layout)
}

func unixMillis(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixMilli())
}

func durationCell(s reporting.SessionSummary) cell {
	d, ok := s.Duration()
	if !ok {
		return cell{text: "—"}
	}
	return cell{textfmt.Duration(d), d.Seconds()}
}

func avgPromptCell(s reporting.SessionSummary) cell {
	if s.PromptCount == 0 {
		return cell{text: "—"}
	}
	return cell{fmt.Sprintf("%.0f", s.AvgPromptChars()), s.AvgPromptChars()}
}

// modelsText names the model that did most of the run, its share, and how many others took part.
func modelsText(s reporting.SessionSummary) string {
	if len(s.Models) == 0 {
		return "—"
	}
	top := s.Models[0]
	out := fmt.Sprintf("%s %.0f%%", textfmt.Head(top.Group, 24), s.ModelShare(top)*100)
	if n := len(s.Models) - 1; n > 0 {
		out += fmt.Sprintf(" +%d", n)
	}
	return out
}

// sessionsSummary is the sessions' totals on two lines.
func (m Model) sessionsSummary() string {
	if !m.sessions.loaded {
		return faintStyle.Render("loading…") + "\n"
	}
	l := m.sessions.list
	if len(l.Sessions) == 0 {
		return noSessions + "\n"
	}
	return fmt.Sprintf("%s sessions · %s requests · $%.2f\n", textfmt.Number(int64(len(l.Sessions))), textfmt.Number(l.Total.Requests), float64(l.Total.Cost)) +
		faintStyle.Render("cost, requests, models cover subagents · prompts count what you typed")
}

const noSessions = "No sessions in this range."

// sessionsView draws the sessions list, or the detail of one session.
func (m Model) sessionsView() string {
	var b strings.Builder
	b.WriteString(m.titleBar() + "\n")
	if d := m.sessions.detail; d.open {
		b.WriteString(activeTab.Render("Session") + faintStyle.Render("  one session, its subagents included") + "\n")
		b.WriteString(m.detailBody() + "\n")
	} else {
		b.WriteString(activeTab.Render("Sessions") + faintStyle.Render(m.sortLabel()) + "\n")
		b.WriteString(m.sessionsSummary() + "\n")
		if m.sessions.loaded && len(m.sessions.list.Sessions) > 0 {
			b.WriteString(m.sessions.table.View() + "\n")
		} else {
			b.WriteString(m.emptyBody() + "\n")
		}
	}
	b.WriteString(m.status() + "\n")
	b.WriteString(faintStyle.Render(m.help()))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
}

// sortLabel says how the sessions are ordered.
func (m Model) sortLabel() string {
	s := m.sessions.sort
	if s.col < 0 {
		return "  most recent first"
	}
	return "  sorted by " + sessionsGrid(nil).headers[s.col] + arrow(s.desc)
}

// detailBody is the visible part of the session detail.
func (m Model) detailBody() string {
	d := m.sessions.detail
	switch {
	case d.err != nil:
		return errorStyle.Render("error: "+textfmt.Printable(d.err.Error())) + strings.Repeat("\n", max(0, d.display-1))
	case !d.loaded:
		return faintStyle.Render("loading…") + strings.Repeat("\n", max(0, d.display-1))
	}
	end := min(len(d.lines), d.scroll+d.display)
	shown := append([]string(nil), d.lines[d.scroll:end]...)
	for len(shown) < d.display {
		shown = append(shown, "")
	}
	return strings.Join(shown, "\n")
}

// detailLines is the body of a session's detail: what it cost and ran on, the tools it called, the subagents
// it started and its prompts in order with their signals. Text from the logs is made printable and cut to width.
func detailLines(d reporting.SessionDetail, width int) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	section := func(title string) { out = append(out, "", titleStyle.Render(title)) }

	add("%s", titleStyle.Render(textfmt.Printable(sessionTitle(d.SessionSummary))))
	add("%s · %s · %s", d.Harness, textfmt.Printable(orDash(d.Folder)), span(d.SessionSummary))
	add("$%.2f · %s requests (%s failed) · %s subagents · %s prompts, average %s characters",
		float64(d.Cost), textfmt.Number(d.Requests), textfmt.Number(d.Failed), textfmt.Number(int64(d.SubagentCount)),
		textfmt.Number(d.PromptCount), avgText(d.SessionSummary))

	section("MODELS")
	mg := usageGrid("MODEL", d.Models)
	mg.headers, mg.numeric = append(mg.headers, "SHARE"), append(mg.numeric, true)
	for i := range mg.rows {
		mg.rows[i] = append(mg.rows[i], cell{fmt.Sprintf("%.0f%%", d.ModelShare(d.Models[i])*100), 0})
	}
	out = append(out, plainTable(mg)...)

	section("TOOLS")
	if len(d.Tools) == 0 {
		out = append(out, faintStyle.Render("no tool calls"))
	} else {
		out = append(out, plainTable(toolsGrid(d.Tools))...)
	}

	section("SUBAGENTS")
	if len(d.Subagents) == 0 {
		out = append(out, faintStyle.Render("none"))
	} else {
		sg := grid{headers: []string{"TYPE", "TITLE", "REQUESTS", "COST"}, numeric: []bool{false, false, true, true}}
		for _, s := range d.Subagents {
			sg.rows = append(sg.rows, []cell{textCell(s.AgentType), textCell(textfmt.Head(sessionTitle(reporting.SessionSummary{Title: s.Title}), 60)),
				countCell(s.Requests), {fmt.Sprintf("$%.2f", float64(s.Cost)), float64(s.Cost)}})
		}
		out = append(out, plainTable(sg)...)
	}

	section("PROMPTS")
	if len(d.Prompts) == 0 {
		out = append(out, faintStyle.Render("none"))
	}
	for i, p := range d.Prompts {
		meta := fmt.Sprintf("#%d · %s · %s characters", i+1, timeText(p.At, "01-02 15:04:05"), textfmt.Number(int64(p.Chars)))
		if tags := signalTags(p.Signals); tags != "" {
			meta += " · " + errorStyle.Render(tags)
		}
		out = append(out, meta, "    "+textfmt.Head(textfmt.Printable(strings.Join(strings.Fields(p.Text), " ")), max(10, width-4)))
	}

	cut := lipgloss.NewStyle().MaxWidth(max(1, width))
	for i, l := range out {
		out[i] = cut.Render(l)
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// span reads "2026-01-01 10:00 → 11:00 (1h00m)" with whatever the logs gave.
func span(s reporting.SessionSummary) string {
	out := timeText(s.Start, "2006-01-02 15:04") + " → " + timeText(s.End, "2006-01-02 15:04")
	if d, ok := s.Duration(); ok {
		out += " (" + textfmt.Duration(d) + ")"
	}
	return out
}

func avgText(s reporting.SessionSummary) string {
	if s.PromptCount == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", s.AvgPromptChars())
}

// signalTags names the signals of a prompt, nothing when it shows none.
func signalTags(s reporting.PromptSignals) string {
	var tags []string
	for _, t := range []struct {
		name string
		n    int
	}{{"yelling", s.Yelling}, {"profanity", s.Profanity}, {"anguish", s.Anguish}, {"negation", s.Negation}, {"blame", s.Blame}} {
		if t.n > 0 {
			tags = append(tags, fmt.Sprintf("%s %d", t.name, t.n))
		}
	}
	if s.Repeated {
		tags = append(tags, "repeated")
	}
	return strings.Join(tags, " · ")
}

// plainTable draws g as aligned text lines, every row: the detail scrolls.
func plainTable(g grid) []string {
	widths := make([]int, len(g.headers))
	for i, h := range g.headers {
		widths[i] = lipgloss.Width(h)
		for _, r := range g.rows {
			widths[i] = max(widths[i], lipgloss.Width(r[i].text))
		}
	}
	line := func(cells func(i int) string) string {
		parts := make([]string, len(widths))
		for i := range widths {
			parts[i] = pad(cells(i), widths[i], g.numeric[i])
			if !g.numeric[i] {
				parts[i] += strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(parts[i])))
			}
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	out := []string{headerStyle.UnsetPadding().Render(line(func(i int) string { return g.headers[i] }))}
	for _, r := range g.rows {
		out = append(out, line(func(i int) string { return r[i].text }))
	}
	return out
}
