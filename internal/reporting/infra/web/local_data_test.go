package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

func getLocalData(t *testing.T, o Options, route string) *httptest.ResponseRecorder {
	t.Helper()
	s := New(o, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7373})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7373"+route, nil))
	return rec
}

func TestCatalogHTTPUsesFiltersAndOmitsSourcePaths(t *testing.T) {
	name, err := domain.NewName("test-skill")
	if err != nil {
		t.Fatal(err)
	}
	var query catalog.Query
	o := Options{Catalog: func(q catalog.Query) (catalog.Catalog, error) {
		query = q
		item := catalog.Item{Harness: kernel.Claude, Kind: domain.Skill, Name: name, Scope: domain.ScopeUser, Source: "/private/config.toml"}
		return catalog.Catalog{Entries: []catalog.Entry{{Item: item, Tracked: true}, {Item: item}}}, nil
	}}
	rec := getLocalData(t, o, "/api/catalog?harness=claude&project=all&unused=true")
	var result struct{ Entries []catalogRow }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Entries) != 2 {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body)
	}
	if query.Harness != kernel.Claude || !query.UnusedOnly || !query.Project.IsZero() {
		t.Fatalf("unexpected query: %+v", query)
	}
	if result.Entries[0].Uses == nil || *result.Entries[0].Uses != 0 || result.Entries[1].Uses != nil || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("unexpected entries: %s", rec.Body)
	}
	if rec := getLocalData(t, o, "/api/catalog?unused=maybe"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid unused parameter returned %d", rec.Code)
	}
}

func TestPrivateViewsAreUnavailableOnRemoteServers(t *testing.T) {
	o := Options{
		AllowRemote: true,
		Catalog: func(catalog.Query) (catalog.Catalog, error) {
			t.Error("remote request read the catalog")
			return catalog.Catalog{}, nil
		},
		Quotas: func(context.Context) []quota.Window {
			t.Error("remote request read quotas")
			return nil
		},
	}
	for _, route := range []string{"/api/catalog", "/api/quotas"} {
		if rec := getLocalData(t, o, route); rec.Code != http.StatusNotFound {
			t.Errorf("%s returned %d", route, rec.Code)
		}
	}
	if rec := getLocalData(t, o, "/api/meta"); strings.Contains(rec.Body.String(), "catalogAvailable") || strings.Contains(rec.Body.String(), "quotasAvailable") {
		t.Fatalf("remote server advertised private views: %s", rec.Body)
	}
}

func TestQuotaHTTPShowsExhaustedWindowsWithoutUsageFilters(t *testing.T) {
	o := Options{Quotas: func(context.Context) []quota.Window {
		return []quota.Window{{Provider: "Codex", Label: "7-day limit", Remaining: 0}}
	}}
	rec := getLocalData(t, o, "/api/quotas")
	var windows []quota.Window
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &windows) != nil || len(windows) != 1 || windows[0].Remaining != 0 {
		t.Fatalf("unexpected quota response: %d %s", rec.Code, rec.Body)
	}
	if rec := getLocalData(t, o, "/api/quotas?project=all"); rec.Code != http.StatusBadRequest {
		t.Fatalf("quota endpoint accepted project filter: %d", rec.Code)
	}
}

func TestQuotaHistoryIsLocalAndUsesTheLastThirtyDays(t *testing.T) {
	now := time.UnixMilli(1767261600000)
	o := Options{
		Now: func() time.Time { return now },
		QuotaHistory: func(_ context.Context, since time.Time, limit int) ([]quota.Window, error) {
			if !since.Equal(now.Add(-30*24*time.Hour)) || limit != 1000 {
				t.Fatalf("history window: %v, limit %d", since, limit)
			}
			return []quota.Window{{Provider: "Codex", Label: "Weekly", Remaining: 80, Observed: now}}, nil
		},
	}
	if rec := getLocalData(t, o, "/api/quotas/history"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Weekly") {
		t.Fatalf("local history: %d %s", rec.Code, rec.Body)
	}
	o.AllowRemote = true
	if rec := getLocalData(t, o, "/api/quotas/history"); rec.Code != http.StatusNotFound {
		t.Fatalf("remote history: %d %s", rec.Code, rec.Body)
	}
}
