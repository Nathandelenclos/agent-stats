package application

import (
	"context"
	"slices"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// recordedByProject is an index of sessions that worked in /work/app (3 requests, one session) and elsewhere (4 more
// requests, two more sessions): what it answers depends on the project the filter names, the zero Project being every one.
type recordedByProject struct{ app kernel.Project }

func (x recordedByProject) requests(f domain.Filter) int64 {
	if f.Project == x.app {
		return 3
	}
	return 7
}

func (x recordedByProject) Snapshot(_ context.Context, fn func(UsageReader) error) error {
	return fn(x)
}
func (x recordedByProject) Total(f domain.Filter) (domain.Usage, error) {
	return domain.Usage{Requests: x.requests(f)}, nil
}
func (x recordedByProject) UsageBy(domain.Filter, domain.Dimension) ([]domain.UsageGroup, error) {
	return nil, nil
}
func (x recordedByProject) Tools(domain.Filter) ([]domain.ToolUsage, error) { return nil, nil }

func (x recordedByProject) Sessions(_ context.Context, f domain.Filter) ([]domain.SessionSummary, error) {
	if f.Project == x.app {
		return make([]domain.SessionSummary, 1), nil
	}
	return make([]domain.SessionSummary, 3), nil
}
func (x recordedByProject) Session(context.Context, domain.SessionID, kernel.Project) (domain.SessionDetail, error) {
	return domain.SessionDetail{}, domain.ErrSessionNotFound
}

func appProject(t *testing.T) kernel.Project {
	t.Helper()
	p, err := kernel.NewProject("/work/app")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheReportOfAProjectCountsItsRequestsOnly(t *testing.T) {
	// Given sessions that worked in a project and elsewhere
	app := appProject(t)

	// When the report of the project is built
	r, err := BuildReport{Queries: recordedByProject{app}}.Run(context.Background(), Query{Project: app})

	// Then it counts the requests of the project
	if err != nil || r.Total.Requests != 3 {
		t.Errorf("requests = %d, %v", r.Total.Requests, err)
	}
}

func TestTheReportOfNoProjectCountsEveryRequest(t *testing.T) {
	r, err := BuildReport{Queries: recordedByProject{appProject(t)}}.Run(context.Background(), Query{})
	if err != nil || r.Total.Requests != 7 {
		t.Errorf("requests = %d, %v", r.Total.Requests, err)
	}
}

func TestTheSessionsOfAProjectAreThoseOfTheProject(t *testing.T) {
	// Given the same sessions
	app := appProject(t)

	// When the sessions of the project are listed
	list, err := ListSessions{Queries: recordedByProject{app}}.Run(context.Background(), Query{Project: app})

	// Then only its session is listed
	if err != nil || len(list.Sessions) != 1 {
		t.Errorf("sessions = %d, %v", len(list.Sessions), err)
	}
}

// sessionsIn is an index that holds sessions of the projects it is given.
type sessionsIn []kernel.Project

func (s sessionsIn) HasSessions(p kernel.Project) (bool, error) { return slices.Contains(s, p), nil }

func TestTheProjectOfTheWorkingFolderIsTheViewWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of the app
	app := appProject(t)

	// When the project of the app is detected
	got, err := DetectProject{Queries: sessionsIn{app}}.Run(app)

	// Then the view is about that project
	if err != nil || got != app {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestTheHomeFolderIsNotTheViewEvenWhenSessionsWorkedInIt(t *testing.T) {
	// Given an index with sessions of the home folder
	home, err := kernel.NewProject("/home/me")
	if err != nil {
		t.Fatal(err)
	}

	// When the project of the home folder is detected
	got, err := DetectProject{Queries: sessionsIn{home}, Home: home}.Run(home)

	// Then the view is about every project
	if err != nil || !got.IsZero() {
		t.Errorf("project = %v, %v", got, err)
	}
}

func TestTheTimelineOfAProjectCountsItsRequestsOnly(t *testing.T) {
	// Given sessions that worked in a project and elsewhere
	app := appProject(t)

	// When the report with its timeline is built for the project
	r, err := BuildTimelineReport{Queries: recordedByProject{app}}.Run(context.Background(), Query{Project: app})

	// Then it counts the requests of the project
	if err != nil || r.Total.Requests != 3 {
		t.Errorf("requests = %d, %v", r.Total.Requests, err)
	}
}
