package web

import (
	"errors"
	"net/http"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type traceEventJSON struct {
	TS         int64   `json:"ts"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	AgentID    string  `json:"agentId"`
	AgentType  string  `json:"agentType"`
	DurationMS float64 `json:"durationMs"`
	Failed     bool    `json:"failed"`
}

type traceJSON struct {
	Events  []traceEventJSON `json:"events"`
	HasMore bool             `json:"hasMore"`
}

func (s *Server) sessionTrace(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit, paramOffset)
	if bad != nil {
		bad.write(w)
		return
	}
	id, err := reporting.NewSessionID(r.PathValue("id"))
	if err != nil || s.opts.Trace.Queries == nil {
		sessionMissing(w)
		return
	}
	rows, err := s.opts.Trace.Run(r.Context(), id, p.query, p.limit+1, p.offset)
	if errors.Is(err, reporting.ErrSessionNotFound) {
		sessionMissing(w)
		return
	}
	if err != nil {
		s.fail(w, "reading the session trace", err)
		return
	}
	size := min(p.limit, len(rows))
	out := traceJSON{Events: make([]traceEventJSON, 0, size), HasMore: len(rows) > size}
	for _, row := range rows[:size] {
		out.Events = append(out.Events, traceEventJSON{
			kernel.UnixMilli(row.At), row.Kind, row.Label, row.AgentID, row.AgentType, float64(row.Duration), row.Failed,
		})
	}
	writeJSON(w, s.log, out)
}
