package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// listedFor is a session index that records the filters of the listings it is asked for.
type listedFor struct{ filters []domain.Filter }

func (x *listedFor) Sessions(_ context.Context, f domain.Filter) ([]domain.SessionSummary, error) {
	x.filters = append(x.filters, f)
	return nil, nil
}

func (x *listedFor) Session(context.Context, domain.SessionID) (domain.SessionDetail, error) {
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

// scopedRig is a server started in the app project, which reports and lists sessions through recording indexes.
// startedOnAll says whether it was started with --all.
func scopedRig(t *testing.T, startedOnAll bool) (*rig, *listedFor) {
	t.Helper()
	sessions := &listedFor{}
	app := appProject(t)
	return newRig(t, func(o *Options) {
		o.Project, o.Restricted = app, !startedOnAll
		o.Sessions = reporting.ListSessions{Queries: sessions}
	}), sessions
}

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

func TestTheReportAndTheSessionsAreThoseOfTheProjectByDefault(t *testing.T) {
	// Given a server started in a project
	r, sessions := scopedRig(t, false)

	// When the page asks for the report and the sessions without naming a scope
	r.get("/api/report")
	r.get("/api/sessions")

	// Then both are about the project
	if len(r.idx.filters) == 0 || r.idx.filters[0].Project != appProject(t) || len(sessions.filters) != 1 || sessions.filters[0].Project != appProject(t) {
		t.Errorf("report filters %+v, sessions filters %+v", r.idx.filters, sessions.filters)
	}
}

func TestTheReportAndTheSessionsCoverEveryProjectWhenAskedTo(t *testing.T) {
	// Given a server started in a project
	r, sessions := scopedRig(t, false)

	// When the page asks for every project
	r.get("/api/report?project=all")
	r.get("/api/sessions?project=all")

	// Then neither is restricted
	if len(r.idx.filters) == 0 || !r.idx.filters[0].Project.IsZero() || len(sessions.filters) != 1 || !sessions.filters[0].Project.IsZero() {
		t.Errorf("report filters %+v, sessions filters %+v", r.idx.filters, sessions.filters)
	}
}

func TestTheProjectCanBeAskedForWhenTheServerWasStartedWithAll(t *testing.T) {
	// Given a server started in a project with --all
	r, sessions := scopedRig(t, true)

	// When the page asks for the report and the sessions of the current project, and then without a scope
	r.get("/api/report?project=current")
	r.get("/api/sessions?project=current")
	r.get("/api/sessions")

	// Then the explicit ones are about the project and the default one covers every project
	if r.idx.filters[0].Project != appProject(t) || sessions.filters[0].Project != appProject(t) || !sessions.filters[1].Project.IsZero() {
		t.Errorf("report filters %+v, sessions filters %+v", r.idx.filters, sessions.filters)
	}
}

func TestAScopeOtherThanCurrentOrAllIsRefusedWithoutReadingTheIndex(t *testing.T) {
	r, sessions := scopedRig(t, false)
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
	if len(r.idx.filters) != 0 || len(sessions.filters) != 0 {
		t.Errorf("the index was read for a refused request: %v %v", r.idx.filters, sessions.filters)
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
