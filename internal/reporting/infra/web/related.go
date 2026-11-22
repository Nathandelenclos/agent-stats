package web

import (
	"net/http"
	"net/url"
	"time"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// Relation names come from the index and may contain a long folder path.
const maxRelationQuery = 4096

const (
	paramKind        = "kind"
	paramValue       = "value"
	paramServer      = "server"
	paramItemHarness = "itemHarness"
	paramItemScope   = "itemScope"
	paramItemOrigin  = "itemOrigin"
)

func (s *Server) relatedSessions(w http.ResponseWriter, r *http.Request) {
	if s.opts.RelatedSessions.Queries == nil {
		relationMissing(w)
		return
	}
	p, bad := s.parseParamsBounded(r, maxRelationQuery, paramSince, paramHarness, paramProject, paramLimit, paramOffset,
		paramKind, paramValue, paramServer, paramItemHarness, paramItemScope, paramItemOrigin)
	if bad != nil {
		bad.write(w)
		return
	}
	values := r.URL.Query()
	if values.Get(paramKind) == "mcp" && (!s.localOnly || s.opts.Catalog == nil) {
		relationMissing(w)
		return
	}
	relation, bad := parseRelation(values)
	if bad != nil {
		bad.write(w)
		return
	}
	if relation.Kind == reporting.RelationMCP {
		found, err := s.resolveMCP(p.query, values, &relation)
		if err != nil {
			s.fail(w, "resolving the catalog entry", err)
			return
		}
		if !found {
			relationMissing(w)
			return
		}
		if len(relation.Servers) == 0 {
			out := sessionListing(reporting.SessionList{}, p)
			out.ToolTotals = &toolTotalsJSON{}
			writeJSON(w, s.log, out)
			return
		}
	}
	list, err := s.opts.RelatedSessions.Run(r.Context(), p.query, relation)
	if err != nil {
		s.fail(w, "listing related sessions", err)
		return
	}
	out := sessionListing(list, p)
	if relation.Kind == reporting.RelationTool || relation.Kind == reporting.RelationMCP {
		total := toolTotalsJSON{}
		for _, session := range list.Sessions {
			if session.RelatedTool != nil {
				total.Calls += session.RelatedTool.Calls
				total.Errors += session.RelatedTool.Errors
			}
		}
		out.ToolTotals = &total
	}
	writeJSON(w, s.log, out)
}

func parseRelation(values url.Values) (reporting.SessionRelation, *problem) {
	relation := reporting.SessionRelation{Value: values.Get(paramValue)}
	switch values.Get(paramKind) {
	case "day":
		relation.Kind = reporting.RelationDay
		day, err := time.ParseInLocation(time.DateOnly, relation.Value, time.Local)
		if err != nil || day.IsZero() || day.Format(time.DateOnly) != relation.Value {
			bad := invalidParameter(paramValue, "use a local calendar date such as 2006-01-02")
			return relation, &bad
		}
		relation.Day = day
	case "harness":
		relation.Kind = reporting.RelationHarness
		if _, err := kernel.ParseHarness(relation.Value); err != nil {
			bad := invalidParameter(paramValue, "use a known harness")
			return relation, &bad
		}
	case "model":
		relation.Kind = reporting.RelationModel
	case "folder":
		relation.Kind = reporting.RelationFolder
	case "agent":
		relation.Kind = reporting.RelationAgent
	case "tool":
		relation.Kind = reporting.RelationTool
		relation.Server = values.Get(paramServer)
	case "mcp":
		relation.Kind = reporting.RelationMCP
		harness, err := kernel.ParseHarness(values.Get(paramItemHarness))
		if err != nil {
			bad := invalidParameter(paramItemHarness, "use the harness of the catalog entry")
			return relation, &bad
		}
		relation.Harness = harness
		switch values.Get(paramItemScope) {
		case "user", "project", "plugin", "logs":
		default:
			bad := invalidParameter(paramItemScope, "use the scope of the catalog entry")
			return relation, &bad
		}
	default:
		bad := invalidParameter(paramKind, "use day, harness, model, folder, agent, tool, or mcp")
		return relation, &bad
	}
	if relation.Value == "" && (relation.Kind == reporting.RelationTool || relation.Kind == reporting.RelationMCP) {
		bad := invalidParameter(paramValue, "give the tool or catalog entry name")
		return relation, &bad
	}
	if relation.Kind != reporting.RelationTool && values.Has(paramServer) {
		bad := invalidParameter(paramServer, "only a tool relation accepts a server")
		return relation, &bad
	}
	if relation.Kind != reporting.RelationMCP {
		for _, key := range []string{paramItemHarness, paramItemScope, paramItemOrigin} {
			if values.Has(key) {
				bad := invalidParameter(key, "only an MCP relation accepts a catalog identity")
				return relation, &bad
			}
		}
	}
	return relation, nil
}

func (s *Server) resolveMCP(q reporting.Query, values url.Values, relation *reporting.SessionRelation) (bool, error) {
	result, err := s.opts.Catalog(catalog.Query{Since: q.Since, Harness: q.Harness, Project: q.Project})
	if err != nil {
		return false, err
	}
	for _, entry := range result.Entries {
		if entry.Kind.String() == "mcp" && entry.Name.String() == relation.Value && entry.Harness == relation.Harness &&
			entry.Scope.String() == values.Get(paramItemScope) && entry.Origin.String() == values.Get(paramItemOrigin) {
			relation.Servers = entry.ObservedServers()
			return true, nil
		}
	}
	return false, nil
}

func relationMissing(w http.ResponseWriter) {
	problem{Type: problemNotFound, Title: "Related sessions unavailable", Status: http.StatusNotFound}.write(w)
}
