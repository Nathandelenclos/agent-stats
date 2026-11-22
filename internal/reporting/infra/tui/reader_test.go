package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type fullPromptReader struct {
	prompt   reporting.Prompt
	id       reporting.SessionID
	position int64
	project  kernel.Project
}

func (f *fullPromptReader) SessionPrompt(_ context.Context, id reporting.SessionID, position int64, project kernel.Project) (reporting.Prompt, error) {
	f.id, f.position, f.project = id, position, project
	return f.prompt, nil
}

func TestSessionPromptCanBeReadToTheEndAndReturnToTheSameRow(t *testing.T) {
	sessions := sampleSessions()
	detail := detailOf(sessions.list[0])
	preview := "First paragraph.\r\n\r\n\tgo test -p 1 ./...\n\n" + strings.Repeat("Long line with 広い characters. ", 75)
	full := preview + "\n\nquatrième paragraphe à retrouver."
	expected := reporting.Prompt{Position: 23, At: at(3, 4, 5), Chars: len([]rune(full)), Text: full}
	p := expected
	p.Text = preview
	detail.Prompts[1] = p
	sessions.details["codex:cheap"] = detail
	f := &fullPromptReader{prompt: expected}
	project, err := kernel.NewProject("/work")
	if err != nil {
		t.Fatal(err)
	}
	m := send(t, inSessions(t, sessions), tea.WindowSizeMsg{Width: 40, Height: 15})
	m.promptText = reporting.ShowSessionPrompt{Queries: f}
	m.project, m.restricted = project, true
	m = press(t, openDetail(t, m), "5", "down")

	m, cmd := pressType(t, m, tea.KeyEnter)
	if cmd == nil || !strings.Contains(m.View(), "Loading full prompt") {
		t.Fatalf("reading did not request the full prompt:\n%s", m.View())
	}
	m = send(t, m, cmd())
	if f.id != detail.ID || f.position != p.Position || f.project != project {
		t.Fatalf("full prompt selected %s/%d in %s", f.id.String(), f.position, f.project.Name())
	}
	lines := strings.Split(m.View(), "\n")
	goLine := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "    go test -p 1 ./...") })
	if goLine < 1 || strings.TrimSpace(lines[goLine-1]) != "" || len(lines) > 15 {
		t.Fatalf("reader lost code indentation, blank lines or height:\n%s", m.View())
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 40 {
			t.Errorf("line exceeds 40 cells: %q", l)
		}
	}
	m = press(t, m, "/", "quatrième")
	m, _ = pressType(t, m, tea.KeyEnter)
	if !strings.Contains(m.View(), "quatrième paragraphe à retrouver.") {
		t.Fatalf("find did not bring the final paragraph into view:\n%s", m.View())
	}
	m, _ = pressType(t, m, tea.KeyEsc) // clears the search
	m, _ = pressType(t, m, tea.KeyEsc) // returns to the prompt list
	if m.sessions.detail.reader.open || !strings.Contains(m.View(), "> #2 First paragraph.") {
		t.Errorf("return lost the selected prompt:\n%s", m.View())
	}
}

func TestSessionFilterOpensTheMatchingSession(t *testing.T) {
	sessions := sampleSessions()
	sessions.details["claude:dear"] = detailOf(sessions.list[1])
	m := press(t, inSessions(t, sessions), "/", "quick fix")
	m, _ = pressType(t, m, tea.KeyEnter)
	if len(m.sessions.shown) != 1 || strings.Contains(m.View(), "billing module") {
		t.Fatalf("filter did not narrow the session list:\n%s", m.View())
	}
	m = openDetail(t, m)
	m, _ = pressType(t, m, tea.KeyEsc)
	if len(sessions.asked) != 1 || sessions.asked[0] != "claude:dear" || !strings.Contains(m.View(), "Filter: quick fix") {
		t.Errorf("session or filter lost:\n%s", m.View())
	}
}

func TestRecurringOccurrenceOpensItsSourceAndBackRestoresTheGroup(t *testing.T) {
	review := samplePrompts()
	sessions := sampleSessions()
	first := sessions.list[0]
	first.ID = sid("claude:a")
	sessions.details[first.ID.String()] = detailOf(first)
	m := inPrompts(t, review)
	m.lister, m.shower = reporting.ListSessions{Queries: sessions}, reporting.ShowSession{Queries: sessions}
	m = press(t, m, "right")
	m, _ = pressType(t, m, tea.KeyEnter)
	if got := len(m.prompts.activeList().shown); got != 2 {
		t.Fatalf("recurring group shows %d occurrences:\n%s", got, m.View())
	}
	m, _ = pressType(t, m, tea.KeyEnter)
	if !strings.Contains(m.View(), "Prompts › Recurring › Prompt") {
		t.Fatalf("recurring occurrence does not open the reader:\n%s", m.View())
	}
	m, cmd := pressCmd(t, m, "s")
	m = answer(t, m, cmd)
	if !strings.Contains(m.View(), "Prompts › Source session") || !strings.Contains(m.View(), first.Title) {
		t.Fatalf("source button did not follow the indexed session ID:\n%s", m.View())
	}
	m, _ = pressType(t, m, tea.KeyEsc)
	if m.mode != promptsMode || !m.prompts.reader.open {
		t.Fatalf("return from source lost the open occurrence:\n%s", m.View())
	}
	m, _ = pressType(t, m, tea.KeyEsc)
	if m.prompts.reader.open || !m.prompts.group.open || m.prompts.activeList().selected() != 0 {
		t.Errorf("return from an occurrence lost its group or selection:\n%s", m.View())
	}
}

func TestPromptReaderIgnoresAResultForADifferentIndexRow(t *testing.T) {
	sessions := sampleSessions()
	detail := detailOf(sessions.list[0])
	detail.Prompts = []domain.Prompt{{Position: 9, At: at(1, 2, 3), Text: "correct preview", Chars: 100}}
	sessions.details["codex:cheap"] = detail
	f := &fullPromptReader{prompt: reporting.Prompt{Position: 9, At: at(1, 2, 3), Text: "unrelated row", Chars: 100}}
	m := inSessions(t, sessions)
	m.promptText = reporting.ShowSessionPrompt{Queries: f}
	m = press(t, openDetail(t, m), "5")
	m, cmd := pressType(t, m, tea.KeyEnter)
	m = answer(t, m, cmd)

	view := m.View()
	if !strings.Contains(view, "Prompt changed in the index.") || !strings.Contains(view, "correct preview") || strings.Contains(view, "unrelated row") {
		t.Fatalf("full prompt was accepted after its row changed:\n%s", view)
	}
}

func TestReaderFindReachesOccurrencesPastTheFirstWrappedLine(t *testing.T) {
	line := strings.Repeat("word ", 70) + "search needle first " + strings.Repeat("other ", 70) + "search needle second"
	r := readerState{text: line}
	r.search.value = "search needle"
	r.layout(40, 8)
	if len(r.matches) != 2 || r.matches[0] <= r.display || r.matches[1] <= r.matches[0]+r.display {
		t.Fatalf("hits were not associated with their wrapped lines: %v", r.matches)
	}
	r.goToMatch(0)
	if !strings.Contains(r.visible(), "needle first") {
		t.Errorf("first match is outside the visible lines: %s", r.visible())
	}
	r.goToMatch(1)
	if !strings.Contains(r.visible(), "search needle second") {
		t.Errorf("second match is outside the visible lines: %s", r.visible())
	}
}
