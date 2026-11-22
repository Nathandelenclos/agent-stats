package web

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type sessionRequestJSON struct {
	SessionID    string  `json:"sessionId"`
	Folder       string  `json:"folder"`
	TS           int64   `json:"ts"`
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Purpose      string  `json:"purpose"`
	ServiceTier  string  `json:"serviceTier"`
	PremiumUnits float64 `json:"premiumUnits"`
	StopReason   string  `json:"stopReason"`
	AgentType    string  `json:"agentType"`
	Input        int64   `json:"input"`
	Output       int64   `json:"output"`
	CacheRead    int64   `json:"cacheRead"`
	CacheWrite   int64   `json:"cacheWrite"`
	Cost         float64 `json:"cost"`
	Priced       bool    `json:"priced"`
	DurationMS   float64 `json:"durationMs"`
	TTFTMS       float64 `json:"ttftMs"`
	Failed       bool    `json:"failed"`
	Error        string  `json:"error,omitempty"`
}

type sessionRequestsJSON struct {
	Requests []sessionRequestJSON `json:"requests"`
	HasMore  bool                 `json:"hasMore"`
}

func (s *Server) sessionRequests(w http.ResponseWriter, r *http.Request) {
	const failedParam, signatureParam, searchParam = "failed", "signature", "q"
	p, bad := s.parseParamsBounded(r, 1024, paramSince, paramHarness, paramProject, paramLimit, paramOffset, failedParam, signatureParam, searchParam)
	if bad != nil {
		bad.write(w)
		return
	}
	values := r.URL.Query()
	failed := values.Get(failedParam)
	if values.Has(failedParam) && failed != "1" {
		invalidParameter(failedParam, "use 1 to show only failed requests").write(w)
		return
	}
	signature := values.Get(signatureParam)
	if values.Has(signatureParam) {
		key, err := hex.DecodeString(signature)
		if !s.localOnly || err != nil || len(key) != 16 {
			invalidParameter(signatureParam, "use a signature shown in the local request journal").write(w)
			return
		}
	}
	id, _ := reporting.NewSessionID(r.PathValue("id")) // a zero id lists the requests of the whole view
	if s.opts.SessionRequests.Queries == nil {
		sessionMissing(w)
		return
	}
	search := strings.TrimSpace(values.Get(searchParam))
	if len(search) > 200 {
		invalidParameter(searchParam, "use at most 200 bytes").write(w)
		return
	}
	filter := reporting.RequestFilter{FailedOnly: failed == "1", Signature: signature, Search: search, SearchErrors: s.localOnly}
	rows, err := s.opts.SessionRequests.Run(r.Context(), id, p.query, filter, p.limit+1, p.offset)
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
			Provider: row.Provider, StopReason: row.StopReason, Purpose: row.Purpose, ServiceTier: row.ServiceTier, PremiumUnits: row.PremiumUnits,
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

type errorSignatureJSON struct {
	Key     string `json:"key"`
	Message string `json:"message"`
	Count   int64  `json:"count"`
	LastTS  int64  `json:"lastTs"`
}

func (s *Server) errorSignatures(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit)
	if bad != nil {
		bad.write(w)
		return
	}
	out := []errorSignatureJSON{}
	if s.localOnly && s.opts.ErrorSignatures.Queries != nil {
		rows, err := s.opts.ErrorSignatures.Run(r.Context(), p.query, p.limit)
		if err != nil {
			s.fail(w, "reading error signatures", err)
			return
		}
		for _, row := range rows {
			out = append(out, errorSignatureJSON{row.Key, row.Message, row.Count, kernel.UnixMilli(row.Last)})
		}
	}
	writeJSON(w, s.log, out)
}
