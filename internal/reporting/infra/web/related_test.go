package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	inventory "github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type relatedIndex struct {
	filter   domain.Filter
	relation domain.SessionRelation
	rows     []domain.SessionSummary
	calls    int
}

func (idx *relatedIndex) RelatedSessions(_ context.Context, f domain.Filter, relation domain.SessionRelation) ([]domain.SessionSummary, error) {
	idx.calls++
	idx.filter, idx.relation = f, relation
	return idx.rows, nil
}

func TestRelatedSessionsKeepTheDayAndCurrentReportFilters(t *testing.T) {
	id, _ := domain.NewSessionID("claude:fixture")
	idx := &relatedIndex{rows: []domain.SessionSummary{{ID: id, Usage: domain.Usage{Requests: 3, Cost: 1.5}}}}
	r := newRig(t, func(o *Options) {
		o.RelatedSessions.Queries = idx
		o.Project, _ = kernel.NewProject("/work/app")
	})
	got := decode[sessionsJSON](t, r.get("/api/related-sessions?kind=day&value=2026-11-20&since=7d&harness=claude&project=current"))
	day := time.Date(2026, time.November, 20, 0, 0, 0, 0, time.Local)
	if idx.relation.Kind != domain.RelationDay || idx.relation.Day != day {
		t.Errorf("relation = %+v", idx.relation)
	}
	if idx.filter.Project.Root() != "/work/app" || idx.filter.Harness != kernel.Claude || !idx.filter.Since.Equal(fixedNow.AddDate(0, 0, -7)) {
		t.Errorf("filters = %+v", idx.filter)
	}
	if got.Scope != "current" || got.Count != 1 || len(got.Sessions) != 1 || got.Sessions[0].ID != id.String() || got.Total.Cost != 1.5 || got.ToolTotals != nil {
		t.Errorf("listing = %+v", got)
	}
}

func TestRelatedToolTotalsCoverTheEntireSelection(t *testing.T) {
	idx := &relatedIndex{rows: []domain.SessionSummary{
		{RelatedTool: &domain.ToolUsage{Name: "read", Server: "memory", Calls: 4, Errors: 1}},
		{RelatedTool: &domain.ToolUsage{Name: "read", Server: "memory", Calls: 3, Errors: 1}},
	}}
	r := newRig(t, func(o *Options) { o.RelatedSessions.Queries = idx })
	got := decode[sessionsJSON](t, r.get("/api/related-sessions?kind=tool&value=read&server=memory&limit=1"))
	if idx.relation.Kind != domain.RelationTool || idx.relation.Server != "memory" || idx.relation.Value != "read" {
		t.Errorf("relation = %+v", idx.relation)
	}
	if got.Count != 2 || len(got.Sessions) != 1 || got.Sessions[0].RelatedTool == nil || got.Sessions[0].RelatedTool.Calls != 4 || got.Total.Cost != 0 {
		t.Fatalf("listing = %+v", got)
	}
	if got.ToolTotals == nil || got.ToolTotals.Calls != 7 || got.ToolTotals.Errors != 2 {
		t.Errorf("tool totals = %+v", got.ToolTotals)
	}
}

func TestRelatedMCPRequiresACatalogIdentityAndUsesItsObservedAliases(t *testing.T) {
	idx := &relatedIndex{}
	name, _ := inventory.NewName("chrome-devtools")
	plugin, _ := inventory.NewName("chrome-devtools-mcp@official")
	otherPlugin, _ := inventory.NewName("chrome-devtools-mcp@private")
	item := catalog.Item{Harness: kernel.Claude, Name: name, Kind: inventory.MCPServer, Scope: inventory.ScopePlugin, Origin: plugin}
	r := newRig(t, func(o *Options) {
		o.RelatedSessions.Queries = idx
		o.Catalog = func(q catalog.Query) (catalog.Catalog, error) {
			if !q.Since.Equal(fixedNow.AddDate(0, 0, -7)) || q.Harness != kernel.Claude {
				t.Errorf("catalog filters = %+v", q)
			}
			items := inventory.Link([]catalog.Item{item}, inventory.Activity{Tools: []inventory.ToolActivity{
				{Harness: kernel.Claude, Name: "click", Server: "plugin_chrome-devtools-mcp_chrome-devtools", Calls: 2},
				{Harness: kernel.Claude, Name: "read", Server: "chrome-devtools", Calls: 1},
			}})
			other := item
			other.Origin = otherPlugin
			return catalog.Catalog{Entries: append([]catalog.Entry{{Item: other}}, items...)}, nil
		}
	})
	const route = "/api/related-sessions?kind=mcp&value=chrome-devtools&itemHarness=claude&itemScope=plugin&since=7d&harness=claude"
	wantProblem(t, r.get(route), http.StatusNotFound, problemNotFound)
	wantProblem(t, r.get(route+"&itemOrigin=chrome-devtools-mcp"), http.StatusNotFound, problemNotFound)
	got := decode[sessionsJSON](t, r.get(route+"&itemOrigin=chrome-devtools-mcp%40official"))
	if got.ToolTotals == nil || idx.calls != 1 || idx.relation.Kind != domain.RelationMCP || idx.relation.Harness != kernel.Claude ||
		!slices.Equal(idx.relation.Servers, []string{"plugin_chrome-devtools-mcp_chrome-devtools", "chrome-devtools"}) {
		t.Errorf("received %+v and resolved %+v", got, idx.relation)
	}
}

func TestRemoteRelatedMCPDoesNotReadThePrivateCatalog(t *testing.T) {
	idx := &relatedIndex{}
	r := newRig(t, func(o *Options) {
		o.AllowRemote = true
		o.RelatedSessions.Queries = idx
		o.Catalog = func(catalog.Query) (catalog.Catalog, error) {
			t.Error("remote relation read the catalog")
			return catalog.Catalog{}, nil
		}
	})
	wantProblem(t, r.get("/api/related-sessions?kind=mcp&value=memory&itemHarness=claude&itemScope=user"), http.StatusNotFound, problemNotFound)
	if idx.calls != 0 {
		t.Fatal("remote relation read the index")
	}
}

func TestRelatedSessionRejectsMalformedSelections(t *testing.T) {
	idx := &relatedIndex{}
	r := newRig(t, func(o *Options) { o.RelatedSessions.Queries = idx })
	for _, query := range []string{
		"kind=week", "kind=day&value=2026-02-30", "kind=day&value=2026-1-01", "kind=tool",
		"kind=harness&value=unknown", "kind=model&server=irrelevant",
		"kind=folder&itemScope=irrelevant", "kind=model&value=a&value=b",
		"kind=tool&value=read&limit=0",
	} {
		t.Run(query, func(t *testing.T) {
			wantProblem(t, r.get("/api/related-sessions?"+query), http.StatusBadRequest, problemInvalidParameter)
		})
	}
	if idx.calls != 0 {
		t.Fatal("malformed selection reached the index")
	}
}

func TestRelatedSessionAcceptsTheWholeFolderKey(t *testing.T) {
	idx := &relatedIndex{}
	r := newRig(t, func(o *Options) { o.RelatedSessions.Queries = idx })
	folder := "/work/" + strings.Repeat("équipe/", 30)
	if code := r.get("/api/related-sessions?kind=folder&value=" + url.QueryEscape(folder)).Code; code != http.StatusOK || idx.relation.Value != folder {
		t.Fatalf("long encoded path returned %d and forwarded %d bytes", code, len(idx.relation.Value))
	}
}
