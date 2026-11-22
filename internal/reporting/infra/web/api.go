package web

import (
	"net/http"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// usageJSON is the API form of a usage: the fields of the command line's --json report. Timestamps are Unix
// milliseconds, 0 when unknown.
type usageJSON struct {
	Requests      int64   `json:"requests"`
	Failed        int64   `json:"failed"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheRead     int64   `json:"cacheRead"`
	CacheWrite    int64   `json:"cacheWrite"`
	Cost          float64 `json:"cost"`
	Unpriced      int64   `json:"unpriced"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	AvgTTFTMs     float64 `json:"avgTtftMs"`
	CacheRate     float64 `json:"cacheRate"`
	FirstTS       int64   `json:"firstTs"`
	LastTS        int64   `json:"lastTs"`
}

func usage(u reporting.Usage) usageJSON {
	return usageJSON{u.Requests, u.Failed, u.Input, u.Output, u.CacheRead, u.CacheWrite, float64(u.Cost), u.Unpriced,
		float64(u.AvgDuration), float64(u.AvgTTFT), u.CacheRate(), kernel.UnixMilli(u.First), kernel.UnixMilli(u.Last)}
}

type groupJSON struct {
	Key string `json:"key"`
	usageJSON
}

func groups(in []reporting.UsageGroup) []groupJSON {
	out := make([]groupJSON, 0, len(in)) // never null: an empty list reads as empty
	for _, g := range in {
		out = append(out, groupJSON{g.Group, usage(g.Usage)})
	}
	return out
}

type toolJSON struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"`
	Calls       int64   `json:"calls"`
	Errors      int64   `json:"errors"`
	ArgsChars   int64   `json:"argsChars"`
	ResultChars int64   `json:"resultChars"`
	AvgMs       float64 `json:"avgMs"`
	LastTS      int64   `json:"lastTs"`
}

// reportJSON is the usage report of the command line's --json, with the usage per local day added.
type reportJSON struct {
	Scope     string      `json:"scope"` // what the figures cover: "current" (the project of the server) or "all"
	Total     usageJSON   `json:"total"`
	ByHarness []groupJSON `json:"byHarness"`
	ByModel   []groupJSON `json:"byModel"`
	ByFolder  []groupJSON `json:"byFolder"`
	ByAgent   []groupJSON `json:"byAgentType"`
	Tools     []toolJSON  `json:"tools"`
	Daily     []groupJSON `json:"daily"` // key: the local day, 2006-01-02, oldest first
}

// report answers GET /api/report?since=&harness=&project=.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject)
	if bad != nil {
		bad.write(w)
		return
	}
	rep, err := s.opts.Reports.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "building the report", err)
		return
	}
	out := reportJSON{Scope: p.scope, Total: usage(rep.Total), ByHarness: groups(rep.ByHarness), ByModel: groups(rep.ByModel),
		ByFolder: groups(rep.ByFolder), ByAgent: groups(rep.ByAgent), Daily: groups(rep.Daily), Tools: make([]toolJSON, 0, len(rep.Tools))}
	for _, t := range rep.Tools {
		out.Tools = append(out.Tools, toolJSON{t.Name, t.Server, t.Calls, t.Errors, t.ArgsChars, t.ResultChars,
			float64(t.AvgDuration), kernel.UnixMilli(t.Last)})
	}
	writeJSON(w, s.log, out)
}

type sessionModelJSON struct {
	Model    string  `json:"model"`
	Requests int64   `json:"requests"`
	Cost     float64 `json:"cost"`
	Share    float64 `json:"share"` // of the session's cost, or of its requests when nothing was priced
}

type sessionJSON struct {
	ID             string             `json:"id"`
	Harness        string             `json:"harness"`
	Title          string             `json:"title"`
	Folder         string             `json:"folder"`
	AgentType      string             `json:"agentType"`
	StartTS        int64              `json:"startTs"`
	EndTS          int64              `json:"endTs"`
	DurationMs     int64              `json:"durationMs"` // 0 when unknown
	Usage          usageJSON          `json:"usage"`
	Models         []sessionModelJSON `json:"models"`
	Subagents      int                `json:"subagents"`
	Prompts        int64              `json:"prompts"`
	AvgPromptChars float64            `json:"avgPromptChars"`
}

type sessionsJSON struct {
	Count    int           `json:"count"` // sessions matching the period, whatever limit cut the list to
	Total    totalJSON     `json:"total"` // of every matching session
	Sessions []sessionJSON `json:"sessions"`
}

type totalJSON struct {
	Requests int64   `json:"requests"`
	Failed   int64   `json:"failed"`
	Cost     float64 `json:"cost"`
	Unpriced int64   `json:"unpriced"`
}

// sessions answers GET /api/sessions?since=&harness=&project=&limit=: the most recent sessions, without their prompts.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit)
	if bad != nil {
		bad.write(w)
		return
	}
	list, err := s.opts.Sessions.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "listing the sessions", err)
		return
	}
	shown := list.Sessions
	if len(shown) > p.limit {
		shown = shown[:p.limit]
	}
	out := sessionsJSON{Count: len(list.Sessions), Sessions: make([]sessionJSON, 0, len(shown)),
		Total: totalJSON{list.Total.Requests, list.Total.Failed, float64(list.Total.Cost), list.Total.Unpriced}}
	for _, x := range shown {
		d, _ := x.Duration()
		models := make([]sessionModelJSON, 0, len(x.Models))
		for _, m := range x.Models {
			models = append(models, sessionModelJSON{m.Group, m.Requests, float64(m.Cost), x.ModelShare(m)})
		}
		out.Sessions = append(out.Sessions, sessionJSON{x.ID.String(), x.Harness.String(), x.Title, x.Folder, x.AgentType,
			kernel.UnixMilli(x.Start), kernel.UnixMilli(x.End), d.Milliseconds(), usage(x.Usage), models, x.SubagentCount,
			x.PromptCount, x.AvgPromptChars()})
	}
	writeJSON(w, s.log, out)
}

type metaJSON struct {
	Harnesses []string `json:"harnesses"`
	Project   string   `json:"project"` // the name of the project of the folder the server runs in, "" when none was detected
	Defaults  struct {
		Since   string `json:"since"`   // the period the page opens on, "" for all
		Harness string `json:"harness"` // "" for all
		Scope   string `json:"scope"`   // what the page opens on: "current" (the project above) or "all"
	} `json:"defaults"`
}

// meta answers GET /api/meta: what the page needs to build its filters.
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	if _, bad := s.parseParams(r); bad != nil {
		bad.write(w)
		return
	}
	m := metaJSON{Harnesses: make([]string, 0, len(kernel.Harnesses))}
	for _, h := range kernel.Harnesses {
		m.Harnesses = append(m.Harnesses, h.String())
	}
	m.Defaults.Since, m.Defaults.Harness, m.Defaults.Scope = s.opts.Since, s.opts.Harness.String(), s.defaultScope()
	if project, _ := s.coverage(); !project.IsZero() {
		m.Project = project.Name()
	}
	writeJSON(w, s.log, m)
}

// syncProgress answers GET /api/sync: where the background indexing stands.
func (s *Server) syncProgress(w http.ResponseWriter, r *http.Request) {
	if _, bad := s.parseParams(r); bad != nil {
		bad.write(w)
		return
	}
	writeJSON(w, s.log, s.sync.snapshot())
}

// fail answers 500 without detail: the cause goes to the server log, since it may name paths of the user's machine.
func (s *Server) fail(w http.ResponseWriter, doing string, err error) {
	s.log("%s: %v", doing, err)
	problem{Type: problemInternal, Title: "Internal error", Status: http.StatusInternalServerError}.write(w)
}
