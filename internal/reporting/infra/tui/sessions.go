package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// mode is what the interface shows: the usage report in tabs, the sessions behind it, or the review of the prompts
// that showed frustration. m cycles through the modes the interface was given a way to read.
type mode int

const (
	usageMode mode = iota
	sessionsMode
	promptsMode
	catalogMode
	quotasMode
)

var modeNames = [...]string{"Usage", "Sessions", "Prompts", "Catalog", "Quotas"}

// sessionsMsg carries the result of a sessions request; seq tells which request it answers.
type sessionsMsg struct {
	seq             int
	period, harness int  // the filters the list was asked for
	restricted      bool // and whether it was restricted to the project
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
	list      reporting.SessionList
	loaded    bool
	sort      sortState
	table     table.Model
	shown     []reporting.SessionSummary // the listed sessions in the order the table shows them
	detail    sessionDetail
	search    readingSearch
	promptSeq int
	// detailSeq is the latest detail request, whatever session it was for: an older answer is ignored, even for
	// the session on screen (it may describe the session before it grew).
	detailSeq int
}

// sessionDetail is the screen of one session: Esc closes it.
type sessionDetail struct {
	open          bool
	id            reporting.SessionID
	data          reporting.SessionDetail
	loaded        bool
	err           error
	fromPrompt    bool
	section       sessionSection
	list          readingList
	reader        readerState
	promptPreview reporting.Prompt
	pager
}

// pager shows a body of lines a screen at a time.
type pager struct {
	lines   []string // the body, cut to the terminal's width
	scroll  int      // the first line shown
	display int      // the lines the body can show
}

func newSessionsState() sessionsState {
	return sessionsState{sort: reportOrder, table: table.New(table.WithFocused(true))}
}

// sessionsEnabled reports whether the interface was given a way to read sessions.
func (m Model) sessionsEnabled() bool { return m.lister.Queries != nil && m.shower.Queries != nil }

// modeEnabled reports whether the interface can show the mode: the usage report always, the others when given their queries.
func (m Model) modeEnabled(md mode) bool {
	switch md {
	case sessionsMode:
		return m.sessionsEnabled()
	case promptsMode:
		return m.promptsEnabled()
	case catalogMode:
		return m.catalog != nil
	case quotasMode:
		return m.quotas != nil
	}
	return true
}

// nextMode is the mode m leads to: the one after the current mode that the interface can show, the current one if none.
func (m Model) nextMode() mode {
	for i := 1; i < len(modeNames); i++ {
		if next := (m.mode + mode(i)) % mode(len(modeNames)); m.modeEnabled(next) {
			return next
		}
	}
	return m.mode
}

// loadSessions asks the use case for the sessions of the current filters, off the UI loop.
func (m Model) loadSessions() tea.Cmd {
	lister, q, seq, period, harness, restricted := m.lister, m.query(), m.seq, m.period, m.harness, m.restricted
	if m.related.active {
		relations, relation := m.relations, m.related.query
		return func() tea.Msg {
			list, err := relations.Run(context.Background(), q, relation)
			return sessionsMsg{seq, period, harness, restricted, list, err}
		}
	}
	return func() tea.Msg {
		list, err := lister.Run(context.Background(), q)
		return sessionsMsg{seq, period, harness, restricted, list, err}
	}
}

// loadDetail asks the use case to describe a session, off the UI loop; answers to earlier requests no longer count.
func (m *Model) loadDetail(id reporting.SessionID) tea.Cmd {
	shower, q := m.shower, m.query()
	m.sessions.detailSeq++
	seq := m.sessions.detailSeq
	return func() tea.Msg {
		d, err := shower.Run(context.Background(), id, q)
		return detailMsg{seq, id, d, err}
	}
}

// switchMode moves to the next mode, asking for what it shows.
func (m Model) switchMode() (Model, tea.Cmd) {
	return m.selectMode(m.nextMode())
}

func (m Model) selectMode(next mode) (Model, tea.Cmd) {
	if next == m.mode && !m.dashboard.open {
		return m, nil
	}
	m.dashboard.open = false
	if m.related.active {
		m.restoreRelationSource()
	}
	m.dailyFromDashboard = false
	m.localSelection, m.localDetail, m.localFilter = 0, false, listFilter{}
	m.sessions.detail = sessionDetail{}
	m.dailyOpen = false
	if next == usageMode && m.loaded {
		m.shown = m.reported
	}
	m.mode = next
	if next == catalogMode || next == quotasMode {
		m.localLoaded = false
		m.localPager = pager{}
		m.err = nil
	}
	if next == promptsMode {
		m.prompts = promptsState{pager: pager{display: m.prompts.display}}
	}
	m.layout()
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
	return m.openSession(id)
}

func (m Model) openSession(id reporting.SessionID) (Model, tea.Cmd) {
	return m.openSessionDetail(id, false)
}

func (m Model) openPromptSession(id reporting.SessionID) (tea.Model, tea.Cmd) {
	m.mode = sessionsMode
	return m.openSessionDetail(id, true)
}

func (m Model) openSessionDetail(id reporting.SessionID, fromPrompt bool) (Model, tea.Cmd) {
	m.sessions.detail = sessionDetail{open: true, id: id, fromPrompt: fromPrompt}
	m.layout()
	detail := m.loadDetail(id) // loadDetail counts the request in m
	return m, detail
}

func (m Model) sessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	case "enter":
		return m.openSelected()
	case "/":
		m.sessions.search.begin()
		m.layout()
	case "esc":
		if m.sessions.search.cancel() {
			m.layout()
		}
	case "p":
		m.period = (m.period + 1) % len(m.periods)
		return m.reload()
	case "h":
		m.harness = (m.harness + 1) % len(harnessChoices)
		return m.reload()
	case "a":
		return m.toggleRestriction()
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
	if d.reader.open {
		return m.readerKey(msg)
	}
	switch k := msg.String(); k {
	case "esc", "backspace":
		if d.list.search.cancel() {
			break
		}
		if d.fromPrompt {
			m.mode = promptsMode
		}
		m.sessions.detail = sessionDetail{}
	case "tab", "right":
		d.changeSection((d.section + 1) % sessionSectionCount)
	case "shift+tab", "left":
		d.changeSection((d.section + sessionSectionCount - 1) % sessionSectionCount)
	case "1", "2", "3", "4", "5":
		d.changeSection(sessionSection(k[0] - '1'))
	case "enter":
		if !d.loaded {
			return m, nil
		}
		if d.section == sessionSummarySection {
			d.changeSection(sessionPromptsSection)
		} else if i := d.list.selected(); i >= 0 {
			return m.openSessionItem(i)
		}
	case "/":
		if d.section != sessionSummarySection {
			d.list.search.begin()
		}
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	default:
		if d.section == sessionSummarySection {
			d.scrollKey(k)
			return m, nil
		}
		d.list.move(k)
	}
	m.layout()
	return m, nil
}

// scrollKey scrolls as the key says, and does nothing for a key that is not a scrolling one.
func (p *pager) scrollKey(k string) {
	switch k {
	case "up", "k":
		p.scrollBy(-1)
	case "down", "j":
		p.scrollBy(1)
	case "pgup", "b":
		p.scrollBy(-p.display)
	case "pgdown", "f", " ":
		p.scrollBy(p.display)
	case "home", "g":
		p.scrollBy(-len(p.lines))
	case "end", "G":
		p.scrollBy(len(p.lines))
	}
}

func (p *pager) scrollBy(n int) {
	p.scroll = max(0, min(p.scroll+n, len(p.lines)-p.display))
}

// visible is the screenful of lines from the scroll position, padded with empty lines to fill it.
func (p pager) visible() string {
	end := min(len(p.lines), p.scroll+p.display)
	shown := append([]string(nil), p.lines[p.scroll:end]...)
	for len(shown) < p.display {
		shown = append(shown, "")
	}
	return strings.Join(shown, "\n")
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
	if len(list) > 0 && list[0].RelatedTool != nil {
		g := grid{headers: []string{"TITLE", "CALLS", "ERRORS", "HARNESS", "FOLDER"}, numeric: []bool{false, true, true, false, false}}
		for _, s := range list {
			var tool reporting.ToolUsage
			if s.RelatedTool != nil {
				tool = *s.RelatedTool
			}
			g.rows = append(g.rows, []cell{textCell(sessionTitle(s)), countCell(tool.Calls), countCell(tool.Errors), textCell(s.Harness.String()), textCell(textfmt.Shorten(s.Folder, 28))})
		}
		return g
	}
	g := grid{
		headers: []string{"TITLE", "COST", "PROMPTS", "AVG PROMPT", "REQUESTS", "DURATION", "START", "HARNESS", "MODELS", "SUBAGENTS", "AGENT", "FOLDER"},
		numeric: []bool{false, true, true, true, true, true, false, false, false, true, false, false},
	}
	for _, s := range list {
		g.rows = append(g.rows, []cell{
			textCell(sessionTitle(s)),
			{displayCost(s.Usage), float64(s.Cost)},
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
	return sessionsGrid(m.filteredSessions()).narrowedTo(m.sessionsTableWidth())
}

func (m Model) filteredSessions() []reporting.SessionSummary {
	if m.sessions.search.value == "" {
		return m.sessions.list.Sessions
	}
	var out []reporting.SessionSummary
	for _, s := range m.sessions.list.Sessions {
		if matchesReading(s.Title+" "+s.Folder+" "+s.Harness.String()+" "+s.ID.String(), m.sessions.search.value) {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) sessionsTableWidth() int {
	if m.width >= 120 && !m.expanded {
		return m.width - 32
	}
	return m.width
}

func (m Model) sessionFilterRows() int { return m.sessions.search.rows() }

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
		return "loading…" + "\n"
	}
	l := m.sessions.list
	if len(l.Sessions) == 0 {
		return noSessions + "\n"
	}
	count := textfmt.Plural(len(l.Sessions), "session", "sessions")
	if m.relationLabel() != "" {
		if l.Sessions[0].RelatedTool != nil {
			var totals reporting.ToolUsage
			for _, s := range l.Sessions {
				if s.RelatedTool != nil {
					totals.Calls += s.RelatedTool.Calls
					totals.Errors += s.RelatedTool.Errors
				}
			}
			return count + " · " + toolCounts(totals) + "\nMatching calls · Enter opens the full session"
		}
		return fmt.Sprintf("%s · %s requests · %s\nMatching usage · Enter opens the full session", count, textfmt.Number(l.Total.Requests), displayCost(l.Total))
	}
	return fmt.Sprintf("%s · %s requests · $%.2f\n", count, textfmt.Number(l.Total.Requests), float64(l.Total.Cost)) +
		"cost, requests, models cover subagents · prompts count what you typed"
}

const noSessions = "No sessions in this range."

// sessionsView draws the list and the selected session preview when there is room.
func (m Model) sessionsView() string {
	if m.sessions.detail.open {
		return m.sessionDetailView()
	}
	var b strings.Builder
	b.WriteString(m.titleBar() + "\n")
	heading := activeTab.Render("Sessions") + m.sortLabel()
	if context := m.relationLabel(); context != "" {
		heading = titleStyle.Render(context)
	}
	b.WriteString(heading + "\n")
	b.WriteString(m.sessionsSummary() + "\n")
	if filter := m.sessions.search.view(m.width, "Filter"); filter != "" {
		b.WriteString(filter + "\n")
	}
	switch {
	case len(m.sessions.shown) > 0:
		view := m.sessions.table.View()
		if width := m.sessionsTableWidth(); width < m.width {
			cut := lipgloss.NewStyle().MaxWidth(m.width - width - 2)
			if i := m.sessions.table.Cursor(); i >= 0 && i < len(m.sessions.shown) {
				s := m.sessions.shown[i]
				preview := append([]string{titleStyle.Render("Selected session"), ""},
					wrappedText(sessionTitle(s), m.width-width-2)...)
				preview = append(preview, "", s.Harness.String(), textfmt.Printable(orDash(s.Folder)))
				if s.RelatedTool != nil {
					preview = append(preview, "", toolCounts(*s.RelatedTool))
				}
				preview = append(preview, "", "Enter opens the session.")
				preview = preview[:min(len(preview), lipgloss.Height(view))]
				view = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(width).Render(view), "  ", cut.Render(strings.Join(preview, "\n")))
			}
		}
		b.WriteString(view + "\n")
	case m.sessions.search.value != "":
		b.WriteString("No matching sessions." + m.emptyBody(1+m.sessionFilterRows()) + "\n")
	default:
		b.WriteString(m.emptyBody(m.sessionFilterRows()) + "\n")
	}
	b.WriteString(m.status() + "\n")
	b.WriteString(m.help())
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
}

// sortLabel says how the sessions are ordered.
func (m Model) sortLabel() string {
	columns := m.sessions.table.Columns()
	col := m.sessions.sort.col
	if col < 0 || col >= len(columns) {
		return "  most recent first"
	}
	return "  sorted by " + strings.TrimSpace(columns[col].Title)
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
func signalTags(s reporting.PromptSignals) string { return strings.Join(s.Tags(), " · ") }
