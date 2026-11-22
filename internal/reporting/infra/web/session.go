package web

import (
	"errors"
	"net/http"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type sessionDetailJSON struct {
	Scope          string         `json:"scope"`
	Session        sessionJSON    `json:"session"`
	Tools          []toolJSON     `json:"tools"`
	Subagents      []subagentJSON `json:"subagents"`
	ForkedFrom     string         `json:"forkedFrom,omitempty"`
	PromptsVisible bool           `json:"promptsVisible"`
	Prompts        []promptJSON   `json:"prompts"`
}

type subagentJSON struct {
	ID        string    `json:"id,omitempty"`
	ParentID  string    `json:"parentId,omitempty"`
	StartTS   int64     `json:"startTs,omitempty"`
	EndTS     int64     `json:"endTs,omitempty"`
	Models    []string  `json:"models,omitempty"`
	AgentType string    `json:"agentType"`
	Title     string    `json:"title"`
	Usage     usageJSON `json:"usage"`
}

type promptJSON struct {
	TS      int64       `json:"ts"`
	Excerpt string      `json:"excerpt,omitempty"`
	Chars   int         `json:"chars"`
	Signals signalsJSON `json:"signals"`
}

type signalsJSON struct {
	Yelling   int  `json:"yelling"`
	Profanity int  `json:"profanity"`
	Anguish   int  `json:"anguish"`
	Negation  int  `json:"negation"`
	Blame     int  `json:"blame"`
	Repeated  bool `json:"repeated"`
}

func encodeSignals(s reporting.PromptSignals) signalsJSON {
	return signalsJSON{s.Yelling, s.Profanity, s.Anguish, s.Negation, s.Blame, s.Repeated}
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject)
	if bad != nil {
		bad.write(w)
		return
	}
	id, err := reporting.NewSessionID(r.PathValue("id"))
	if err != nil {
		sessionMissing(w)
		return
	}
	show := reporting.ShowSession{Queries: s.opts.Sessions.Queries}
	d, err := show.Run(r.Context(), id, p.query)
	switch {
	case errors.Is(err, reporting.ErrSessionNotFound):
		sessionMissing(w)
	case err != nil:
		s.fail(w, "reading the session", err)
	case !p.query.Harness.IsZero() && d.Harness != p.query.Harness,
		!p.query.Since.IsZero() && d.End.Before(p.query.Since):
		sessionMissing(w)
	default:
		writeJSON(w, s.log, sessionDetails(d, p.scope, s.localOnly))
	}
}

func sessionMissing(w http.ResponseWriter) {
	problem{Type: problemNotFound, Title: "Session not found in this view", Status: http.StatusNotFound}.write(w)
}

func sessionDetails(d reporting.SessionDetail, scope string, localPrompts bool) sessionDetailJSON {
	out := sessionDetailJSON{
		Scope: scope, Session: sessionSummary(d.SessionSummary), Tools: tools(d.Tools), ForkedFrom: d.ForkedFrom,
		Subagents: make([]subagentJSON, 0, len(d.Subagents)), PromptsVisible: localPrompts,
		Prompts: make([]promptJSON, 0, len(d.Prompts)),
	}
	for _, s := range d.Subagents {
		out.Subagents = append(out.Subagents, subagentJSON{
			ID: s.ID.String(), ParentID: s.ParentID.String(), AgentType: s.AgentType, Title: s.Title,
			StartTS: kernel.UnixMilli(s.Start), EndTS: kernel.UnixMilli(s.End), Models: s.Models, Usage: usage(s.Usage),
		})
	}
	for _, p := range d.Prompts {
		row := promptJSON{
			TS: kernel.UnixMilli(p.At), Chars: p.Chars,
			Signals: encodeSignals(p.Signals),
		}
		if localPrompts {
			row.Excerpt = p.Text
		}
		out.Prompts = append(out.Prompts, row)
	}
	return out
}
