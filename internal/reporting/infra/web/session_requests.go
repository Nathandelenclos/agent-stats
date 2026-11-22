package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type sessionRequestJSON struct {
	SessionID  string  `json:"sessionId"`
	Folder     string  `json:"folder"`
	TS         int64   `json:"ts"`
	Model      string  `json:"model"`
	Provider   string  `json:"provider"`
	StopReason string  `json:"stopReason"`
	AgentType  string  `json:"agentType"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Priced     bool    `json:"priced"`
	DurationMS float64 `json:"durationMs"`
	TTFTMS     float64 `json:"ttftMs"`
	Failed     bool    `json:"failed"`
	Error      string  `json:"error,omitempty"`
}

type sessionRequestsJSON struct {
	Requests []sessionRequestJSON `json:"requests"`
	HasMore  bool                 `json:"hasMore"`
}

func (s *Server) sessionRequests(w http.ResponseWriter, r *http.Request) {
	const offsetParam, failedParam = "offset", "failed"
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit, offsetParam, failedParam)
	if bad != nil {
		bad.write(w)
		return
	}
	var offset int64
	values := r.URL.Query()
	if values.Has(offsetParam) {
		var err error
		offset, err = strconv.ParseInt(values.Get(offsetParam), 10, 32)
		if err != nil || offset < 0 {
			invalidParameter(offsetParam, "use a non-negative integer").write(w)
			return
		}
	}
	failed := values.Get(failedParam)
	if values.Has(failedParam) && failed != "1" {
		invalidParameter(failedParam, "use 1 to show only failed requests").write(w)
		return
	}
	id, _ := reporting.NewSessionID(r.PathValue("id")) // a zero id lists the requests of the whole view
	if s.opts.SessionRequests.Queries == nil {
		sessionMissing(w)
		return
	}
	rows, err := s.opts.SessionRequests.Run(r.Context(), id, p.query, failed == "1", p.limit+1, int(offset))
	if errors.Is(err, reporting.ErrSessionNotFound) {
		sessionMissing(w)
		return
	}
	if err != nil {
		s.fail(w, "reading the session's requests", err)
		return
	}

	size := min(p.limit, len(rows))
	out := sessionRequestsJSON{Requests: make([]sessionRequestJSON, 0, size), HasMore: len(rows) > size}
	for _, row := range rows[:size] {
		item := sessionRequestJSON{
			SessionID: row.SessionID, Folder: row.Folder, TS: kernel.UnixMilli(row.At), Model: row.Model, AgentType: row.AgentType,
			Provider: row.Provider, StopReason: row.StopReason,
			Input: row.Input, Output: row.Output, CacheRead: row.CacheRead, CacheWrite: row.CacheWrite,
			Cost: float64(row.Cost), Priced: row.Priced, DurationMS: float64(row.Duration),
			TTFTMS: float64(row.TTFT), Failed: row.Error != "",
		}
		if s.localOnly {
			item.Error = row.Error
		}
		out.Requests = append(out.Requests, item)
	}
	writeJSON(w, s.log, out)
}
