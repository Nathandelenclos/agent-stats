package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

func appProject(t *testing.T) kernel.Project {
	t.Helper()
	p, err := kernel.NewProject("/work/app")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheInterfaceOpensOnTheProjectItIsGiven(t *testing.T) {
	// Given the interface opened on a project
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{Project: appProject(t), Scoped: true})

	// Then the first report is about that project, and the title says so
	if got := reports.queries[0].Project; got != appProject(t) {
		t.Errorf("first report asked about %v", got)
	}
	if view := m.View(); !strings.Contains(view, "· project: app") || strings.Contains(view, "all projects") {
		t.Errorf("title does not name the project:\n%s", view)
	}
}

func TestTheKeyAMovesBetweenTheProjectAndEveryProject(t *testing.T) {
	// Given the interface on a project
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{Project: appProject(t), Scoped: true})

	// When the user presses a
	m, cmd := pressCmd(t, m, "a")

	// Then the title still names the report on screen until the new one arrives
	if view := m.View(); !strings.Contains(view, "project: app") {
		t.Errorf("title changed before the new report:\n%s", view)
	}

	// When the report for every project arrives
	m = answer(t, m, cmd)

	// Then it was asked for no project in particular, and the title says so
	if got := reports.queries[len(reports.queries)-1].Project; !got.IsZero() {
		t.Errorf("asked about %v", got)
	}
	if view := m.View(); !strings.Contains(view, "· all projects") || strings.Contains(view, "project: app") {
		t.Errorf("title does not say every project:\n%s", view)
	}

	// When the user presses a again
	m, cmd = pressCmd(t, m, "a")
	m = answer(t, m, cmd)

	// Then the project is back
	if got := reports.queries[len(reports.queries)-1].Project; got != appProject(t) || !strings.Contains(m.View(), "project: app") {
		t.Errorf("asked about %v:\n%s", got, m.View())
	}
}

func TestAnInterfaceOpenedOnEveryProjectCanSwitchToTheProjectOfTheFolder(t *testing.T) {
	// Given the interface opened on every project (--all) with a project to switch to
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{Project: appProject(t)})
	if !reports.queries[0].Project.IsZero() || !strings.Contains(m.View(), "all projects") {
		t.Fatalf("first query = %+v\n%s", reports.queries[0], m.View())
	}

	// When the user presses a
	m, cmd := pressCmd(t, m, "a")
	m = answer(t, m, cmd)

	// Then the report is about the project
	if got := reports.queries[len(reports.queries)-1].Project; got != appProject(t) || !strings.Contains(m.View(), "project: app") {
		t.Errorf("asked about %v", got)
	}
}

func TestWithoutAProjectTheKeyADoesNothingAndIsNotOffered(t *testing.T) {
	// Given the interface of a folder no session worked in: no project to switch to, even if asked to start on one
	reports := &fakeReports{report: sampleReport()}
	m := startWith(t, reports, Options{Scoped: true})
	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 30})

	// When the user presses a
	m, cmd := pressCmd(t, m, "a")

	// Then nothing is asked, the title says every project and the help does not list the key
	if cmd != nil || len(reports.queries) != 1 || !reports.queries[0].Project.IsZero() {
		t.Errorf("a asked for a report: %v %+v", cmd != nil, reports.queries)
	}
	if view := m.View(); !strings.Contains(view, "all projects") || strings.Contains(m.help(), "a all projects") || strings.Contains(m.help(), "a this project") {
		t.Errorf("view:\n%s\nhelp: %s", view, m.help())
	}
}

func TestTheHelpSaysWhatTheKeyAWouldSwitchTo(t *testing.T) {
	m := startWith(t, &fakeReports{report: sampleReport()}, Options{Project: appProject(t), Scoped: true})
	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 30})
	if !strings.Contains(m.help(), "a all projects") {
		t.Errorf("help = %q", m.help())
	}
	m, cmd := pressCmd(t, m, "a")
	m = answer(t, m, cmd)
	if !strings.Contains(m.help(), "a this project") {
		t.Errorf("help = %q", m.help())
	}
}

func TestAProjectNameIsShownWithoutItsControlCharacters(t *testing.T) {
	p, err := kernel.NewProject("/work/app\x1b[2J")
	if err != nil {
		t.Fatal(err)
	}
	m := startWith(t, &fakeReports{report: sampleReport()}, Options{Project: p, Scoped: true})
	if view := m.View(); strings.Contains(view, "\x1b[2J") {
		t.Errorf("title holds a control sequence: %q", view)
	}
}

func TestTheSessionsFollowTheScopeToo(t *testing.T) {
	// Given the interface on a project, in the sessions mode
	sessions := sampleSessions()
	m := New(Options{Reports: build(&fakeReports{report: sampleReport()}), Project: appProject(t), Scoped: true,
		Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions}})
	m.now = func() time.Time { return noon }
	m = send(t, m, m.Init()())
	m, cmd := pressCmd(t, m, "m")
	m = answer(t, m, cmd)

	// Then the sessions listed are those of the project, under a title that names it
	if got := sessions.filters[len(sessions.filters)-1].Project; got != appProject(t) || !strings.Contains(m.View(), "project: app") {
		t.Errorf("sessions asked about %v:\n%s", got, m.View())
	}

	// When the user presses a
	m, cmd = pressCmd(t, m, "a")
	m = answer(t, m, cmd)

	// Then the sessions of every project are listed
	if got := sessions.filters[len(sessions.filters)-1].Project; !got.IsZero() || !strings.Contains(m.View(), "all projects") {
		t.Errorf("sessions asked about %v:\n%s", got, m.View())
	}
}
