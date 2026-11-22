package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// workedIn is an index of what agents did in two projects: 3 requests and 1 session in /work/app, 4 requests and 2 sessions
// in /work/other. It answers what the filter keeps, and counts the questions it is asked.
type workedIn struct{ reads int }

const (
	requestsInApp, requestsElsewhere = 3, 4
	sessionsInApp, sessionsElsewhere = 1, 2
)

func (x *workedIn) requests(f domain.Filter) int64 {
	if f.Project.IsZero() {
		return requestsInApp + requestsElsewhere
	}
	if f.Project.Contains("/work/app") {
		return requestsInApp
	}
	return requestsElsewhere
}

func (x *workedIn) Snapshot(_ context.Context, fn func(reporting.UsageReader) error) error {
	return fn(x)
}
func (x *workedIn) Total(f domain.Filter) (domain.Usage, error) {
	x.reads++
	return domain.Usage{Requests: x.requests(f)}, nil
}

func (x *workedIn) UsageBy(f domain.Filter, d domain.Dimension) ([]domain.UsageGroup, error) {
	x.reads++
	if d != domain.ByDay {
		return nil, nil
	}
	return []domain.UsageGroup{{Group: "2026-11-20", Usage: domain.Usage{Requests: x.requests(f)}}}, nil
}

func (x *workedIn) Tools(domain.Filter) ([]domain.ToolUsage, error) { x.reads++; return nil, nil }

func (x *workedIn) Sessions(_ context.Context, f domain.Filter) ([]domain.SessionSummary, error) {
	x.reads++
	var out []domain.SessionSummary
	for i, folder := range []string{"/work/app", "/work/other", "/work/other"} {
		if f.Project.IsZero() || f.Project.Contains(folder) {
			id, _ := domain.NewSessionID(fmt.Sprintf("s%d", i))
			out = append(out, domain.SessionSummary{ID: id, Harness: kernel.Claude, Folder: folder})
		}
	}
	return out, nil
}

func (x *workedIn) Session(context.Context, domain.SessionID, kernel.Project) (domain.SessionDetail, error) {
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

// scopedRig is a server started in the app project. startedOnAll says whether it was started with --all.
func scopedRig(t *testing.T, startedOnAll bool) (*rig, *workedIn) {
	t.Helper()
	index := &workedIn{}
	app := appProject(t)
	return newRig(t, func(o *Options) {
		o.Project, o.Restricted = app, !startedOnAll
		o.Reports = reporting.BuildTimelineReport{Queries: index}
		o.Sessions = reporting.ListSessions{Queries: index}
	}), index
}

// covered is what a report and a list of sessions say they hold.
type covered struct{ requests, days, sessions int64 }

// ask reads the report and the sessions the way the page does, with the query string given to both.
func ask(t *testing.T, r *rig, query string) covered {
	t.Helper()
	report := decode[reportJSON](t, r.get("/api/report"+query))
	sessions := decode[sessionsJSON](t, r.get("/api/sessions"+query))
	return covered{report.Total.Requests, report.Daily[0].Requests, int64(sessions.Count)}
}

var (
	inApp      = covered{requestsInApp, requestsInApp, sessionsInApp}
	everywhere = covered{requestsInApp + requestsElsewhere, requestsInApp + requestsElsewhere, sessionsInApp + sessionsElsewhere}
)

func TestMetaSaysWhichProjectTheServerRunsInAndThatItOpensOnIt(t *testing.T) {
	// Given a server started in a project
	r, _ := scopedRig(t, false)

	// When the page asks what it can filter on
	got := decode[metaJSON](t, r.get("/api/meta"))

	// Then it learns the project and that the page opens on it
	if got.Project != "app" || got.Defaults.Project != "current" {
		t.Errorf("meta = %+v", got)
	}
}

func TestMetaOffersNoProjectWhenNoneWasDetected(t *testing.T) {
	// Given a server started outside any project that sessions worked in
	r := newRig(t)

	// When the page asks what it can filter on
	got := decode[metaJSON](t, r.get("/api/meta"))

	// Then there is no project and the page opens on every project
	if got.Project != "" || got.Defaults.Project != "all" {
		t.Errorf("meta = %+v", got)
	}
}

func TestMetaOpensOnEveryProjectWhenTheServerWasStartedWithAll(t *testing.T) {
	// Given a server started in a project with --all
	r, _ := scopedRig(t, true)

	// When the page asks what it can filter on
	got := decode[metaJSON](t, r.get("/api/meta"))

	// Then it still learns the project, so that it can switch to it, and opens on every project
	if got.Project != "app" || got.Defaults.Project != "all" {
		t.Errorf("meta = %+v", got)
	}
}

func TestTheReportTheDaysAndTheSessionsAreThoseOfTheProjectByDefault(t *testing.T) {
	// Given a server started in a project that other projects share the index with
	r, _ := scopedRig(t, false)

	// When the page asks for the report and the sessions without naming a scope
	got := ask(t, r, "")

	// Then the totals, the days and the sessions are those of the project
	if got != inApp {
		t.Errorf("covered %+v, want %+v", got, inApp)
	}
}

func TestEveryProjectIsCoveredWhenTheClientAsksForAll(t *testing.T) {
	// Given a server started in a project
	r, _ := scopedRig(t, false)

	// When the page asks for every project
	got := ask(t, r, "?project=all")

	// Then the totals, the days and the sessions are those of every project
	if got != everywhere {
		t.Errorf("covered %+v, want %+v", got, everywhere)
	}
}

func TestTheProjectCanBeAskedForWhenTheServerWasStartedWithAll(t *testing.T) {
	// Given a server started in a project with --all
	r, _ := scopedRig(t, true)

	// When the page asks for the current project
	got := ask(t, r, "?project=current")

	// Then the totals, the days and the sessions are those of the project
	if got != inApp {
		t.Errorf("covered %+v, want %+v", got, inApp)
	}
}

func TestEveryProjectIsCoveredByDefaultWhenTheServerWasStartedWithAll(t *testing.T) {
	// Given a server started in a project with --all
	r, _ := scopedRig(t, true)

	// When the page asks without naming a scope
	got := ask(t, r, "")

	// Then it covers every project
	if got != everywhere {
		t.Errorf("covered %+v, want %+v", got, everywhere)
	}
}

func TestAScopeOtherThanCurrentOrAllIsRefusedWithoutReadingTheIndex(t *testing.T) {
	r, index := scopedRig(t, false)
	for _, c := range []struct{ name, target string }{
		{"a folder", "/api/report?project=%2Fwork%2Fother"},
		{"a folder on the sessions", "/api/sessions?project=%2Fwork%2Fother"},
		{"the folder of the project itself", "/api/report?project=%2Fwork%2Fapp"},
		{"a relative folder", "/api/report?project=.."},
		{"another name", "/api/report?project=app"},
		{"another case", "/api/report?project=ALL"},
		{"an empty value", "/api/report?project="},
		{"a repeated parameter", "/api/report?project=all&project=current"},
		{"a scope on the meta", "/api/meta?project=all"},
		{"a scope on the sync", "/api/sync?project=all"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Given a request that names a scope the API does not offer
			// When it is sent
			rec := r.get(c.target)

			// Then it is refused as an invalid parameter
			wantProblem(t, rec, http.StatusBadRequest, problemInvalidParameter)
			if strings.Contains(rec.Body.String(), "work") {
				t.Errorf("the refusal repeats the folder: %s", rec.Body.String())
			}
		})
	}
	// And no figure was read
	if index.reads != 0 {
		t.Errorf("the index was read %d times for refused requests", index.reads)
	}
}

func TestTheCurrentProjectIsRefusedWhenNoneWasDetected(t *testing.T) {
	// Given a server started outside any project
	r := newRig(t)

	// When the page asks for the current project
	rec := r.get("/api/report?project=current")

	// Then it is refused rather than widened to every project, and no figure is read
	p := wantProblem(t, rec, http.StatusBadRequest, problemInvalidParameter)
	if p.Parameter != "project" || len(r.idx.filters) != 0 {
		t.Errorf("parameter %q, index asked %d times", p.Parameter, len(r.idx.filters))
	}
}
