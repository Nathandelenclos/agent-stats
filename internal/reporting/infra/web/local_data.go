package web

import (
	"net/http"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

type catalogRow struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin,omitempty"`
	Enabled bool   `json:"enabled"`
	Uses    *int64 `json:"uses"`
	Unit    string `json:"unit"`
	LastTS  int64  `json:"lastTs"`
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly || s.opts.Catalog == nil {
		problem{Type: problemNotFound, Title: "Catalog unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, "unused")
	if bad != nil {
		bad.write(w)
		return
	}
	values := r.URL.Query()
	unused := values.Get("unused")
	if values.Has("unused") && unused != "true" && unused != "false" {
		invalidParameter("unused", "use true or false").write(w)
		return
	}
	q := p.query
	result, err := s.opts.Catalog(catalog.Query{
		Since: q.Since, Harness: q.Harness, Project: q.Project, UnusedOnly: unused == "true",
	})
	if err != nil {
		s.fail(w, "reading the catalog", err)
		return
	}
	rows := make([]catalogRow, 0, len(result.Entries))
	for _, entry := range result.Entries {
		row := catalogRow{
			Harness: entry.Harness.String(), Kind: entry.Kind.String(), Name: entry.Name.String(),
			Scope: entry.Scope.String(), Origin: entry.Plugin(), Enabled: !entry.Disabled,
			Unit: entry.Unit().String(), LastTS: kernel.UnixMilli(entry.Last),
		}
		if entry.Tracked {
			row.Uses = &entry.Uses
		}
		rows = append(rows, row)
	}
	writeJSON(w, s.log, struct {
		Scope   string       `json:"scope"`
		Entries []catalogRow `json:"entries"`
		Skipped int          `json:"skipped"`
	}{p.scope, rows, len(result.Skipped)})
}

func (s *Server) quotas(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly || s.opts.Quotas == nil {
		problem{Type: problemNotFound, Title: "Quotas unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	if _, bad := s.parseParams(r); bad != nil {
		bad.write(w)
		return
	}
	windows := append([]quota.Window{}, s.opts.Quotas(r.Context())...)
	writeJSON(w, s.log, windows)
}
