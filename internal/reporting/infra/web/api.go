package web

import (
	"net/http"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// usageJSON is the API form of a usage: the fields of the command line's --json report. Timestamps are Unix
// milliseconds, 0 when unknown.
type usageJSON struct {
	Requests           int64   `json:"requests"`
	Failed             int64   `json:"failed"`
	Input              int64   `json:"input"`
	Output             int64   `json:"output"`
	CacheRead          int64   `json:"cacheRead"`
	CacheWrite         int64   `json:"cacheWrite"`
	Cost               float64 `json:"cost"`
	Unpriced           int64   `json:"unpriced"`
	AvgDurationMs      float64 `json:"avgDurationMs"`
	AvgTTFTMs          float64 `json:"avgTtftMs"`
	DurationSamples    int64   `json:"durationSamples"`
	TTFTSamples        int64   `json:"ttftSamples"`
	AvgTokensPerSecond float64 `json:"avgTokensPerSecond"`
	ThroughputSamples  int64   `json:"throughputSamples"`
	PremiumUnits       float64 `json:"premiumUnits"`
	CacheRate          float64 `json:"cacheRate"`
	FirstTS            int64   `json:"firstTs"`
	LastTS             int64   `json:"lastTs"`
}

func usage(u reporting.Usage) usageJSON {
	return usageJSON{u.Requests, u.Failed, u.Input, u.Output, u.CacheRead, u.CacheWrite, float64(u.Cost), u.Unpriced,
		float64(u.AvgDuration), float64(u.AvgTTFT),
		u.DurationSamples, u.TTFTSamples, u.AvgTokensPerSecond, u.ThroughputSamples, u.PremiumUnits, u.CacheRate(), kernel.UnixMilli(u.First), kernel.UnixMilli(u.Last)}
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

func tools(in []reporting.ToolUsage) []toolJSON {
	out := make([]toolJSON, 0, len(in))
	for _, t := range in {
		out = append(out, toolSummary(t))
	}
	return out
}

func toolSummary(t reporting.ToolUsage) toolJSON {
	return toolJSON{t.Name, t.Server, t.Calls, t.Errors, t.ArgsChars, t.ResultChars,
		float64(t.AvgDuration), kernel.UnixMilli(t.Last)}
}

// reportJSON is the usage report of the command line's --json, with the usage per local day added.
type reportJSON struct {
	Scope         string             `json:"scope"` // what the figures cover: "current" (the project of the server) or "all"
	Total         usageJSON          `json:"total"`
	ByHarness     []groupJSON        `json:"byHarness"`
	ByProvider    []groupJSON        `json:"byProvider"`
	ByModel       []groupJSON        `json:"byModel"`
	ByFolder      []groupJSON        `json:"byFolder"`
	ByAgent       []groupJSON        `json:"byAgentType"`
	Tools         []toolJSON         `json:"tools"`
	CostBreakdown *costBreakdownJSON `json:"costBreakdown,omitempty"`
	Hourly        []groupJSON        `json:"hourly,omitempty"`
	Daily         []groupJSON        `json:"daily"` // key: the local day, 2006-01-02, oldest first
}

// report answers GET /api/report?since=&harness=&project=.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject)
	if bad != nil {
		bad.write(w)
		return
	}
	build := s.opts.Reports
	build.IncludeHourly = !p.query.Since.IsZero() && s.now().Sub(p.query.Since) <= 73*time.Hour
	rep, err := build.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "building the report", err)
		return
	}
	out := reportJSON{Scope: p.scope, Total: usage(rep.Total), ByHarness: groups(rep.ByHarness), ByProvider: groups(rep.ByProvider), ByModel: groups(rep.ByModel),
		ByFolder: groups(rep.ByFolder), ByAgent: groups(rep.ByAgent), Daily: groups(rep.Daily), Hourly: groups(rep.Hourly), Tools: tools(rep.Tools),
		CostBreakdown: costBreakdown(rep.ByModel, s.opts.TokenRates)}
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
	RelatedTool    *toolJSON          `json:"relatedTool,omitempty"`
}

type sessionsJSON struct {
	Scope      string          `json:"scope"`
	Count      int             `json:"count"` // sessions matching the period, whatever limit cut the list to
	Total      totalJSON       `json:"total"` // of every matching session
	Sessions   []sessionJSON   `json:"sessions"`
	ToolTotals *toolTotalsJSON `json:"toolTotals,omitempty"`
}

type toolTotalsJSON struct {
	Calls  int64 `json:"calls"`
	Errors int64 `json:"errors"`
}

type totalJSON struct {
	Requests int64   `json:"requests"`
	Failed   int64   `json:"failed"`
	Cost     float64 `json:"cost"`
	Unpriced int64   `json:"unpriced"`
}

// sessions answers GET /api/sessions?since=&harness=&project=&limit=&offset=: the most recent sessions, without their prompts.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit, paramOffset)
	if bad != nil {
		bad.write(w)
		return
	}
	list, err := s.opts.Sessions.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "listing the sessions", err)
		return
	}
	writeJSON(w, s.log, sessionListing(list, p))
}

func sessionListing(list reporting.SessionList, p params) sessionsJSON {
	start := min(p.offset, len(list.Sessions))
	shown := list.Sessions[start : start+min(p.limit, len(list.Sessions)-start)]
	out := sessionsJSON{Scope: p.scope, Count: len(list.Sessions), Sessions: make([]sessionJSON, 0, len(shown)),
		Total: totalJSON{list.Total.Requests, list.Total.Failed, float64(list.Total.Cost), list.Total.Unpriced}}
	for _, x := range shown {
		out.Sessions = append(out.Sessions, sessionSummary(x))
	}
	return out
}

func sessionSummary(s reporting.SessionSummary) sessionJSON {
	d, _ := s.Duration()
	models := make([]sessionModelJSON, 0, len(s.Models))
	for _, m := range s.Models {
		models = append(models, sessionModelJSON{m.Group, m.Requests, float64(m.Cost), s.ModelShare(m)})
	}
	out := sessionJSON{
		ID: s.ID.String(), Harness: s.Harness.String(), Title: s.Title, Folder: s.Folder, AgentType: s.AgentType,
		StartTS: kernel.UnixMilli(s.Start), EndTS: kernel.UnixMilli(s.End), DurationMs: d.Milliseconds(),
		Usage: usage(s.Usage), Models: models, Subagents: s.SubagentCount, Prompts: s.PromptCount, AvgPromptChars: s.AvgPromptChars(),
	}
	if s.RelatedTool != nil {
		t := toolSummary(*s.RelatedTool)
		out.RelatedTool = &t
	}
	return out
}

type metaJSON struct {
	CatalogAvailable bool     `json:"catalogAvailable,omitempty"`
	QuotasAvailable  bool     `json:"quotasAvailable,omitempty"`
	Harnesses        []string `json:"harnesses"`
	Project          string   `json:"project"` // the name of the project of the folder the server runs in, "" when none was detected
	Defaults         struct {
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
	m.Harnesses = kernel.HarnessNames()
	m.CatalogAvailable = s.localOnly && s.opts.Catalog != nil
	m.QuotasAvailable = s.localOnly && s.opts.Quotas != nil
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
	if r.Method == http.MethodPost {
		if r.ContentLength != 0 {
			problem{Type: problemInvalidParameter, Title: "The request must have no body", Status: http.StatusBadRequest}.write(w)
			return
		}
		index := s.indexing()
		if index == nil {
			problem{Type: problemIndexingDisabled, Title: "Indexing is disabled", Status: http.StatusConflict}.write(w)
			return
		}
		if s.indexCtx == nil || s.indexCtx.Err() != nil {
			problem{Type: problemStopping, Title: "Server stopping", Status: http.StatusServiceUnavailable}.write(w)
			return
		}
		writeJSON(w, s.log, s.sync.start(s.indexCtx, index, false))
		return
	}
	writeJSON(w, s.log, s.sync.snapshot())
}

// fail answers 500 without detail: the cause goes to the server log, since it may name paths of the user's machine.
func (s *Server) fail(w http.ResponseWriter, doing string, err error) {
	s.log("%s: %v", doing, err)
	problem{Type: problemInternal, Title: "Internal error", Status: http.StatusInternalServerError}.write(w)
}

func (s *Server) modelActivity(w http.ResponseWriter, r *http.Request) {
	const modelParam = "model"
	p, bad := s.parseParamsBounded(r, 1024, paramSince, paramHarness, paramProject, modelParam)
	if bad != nil {
		bad.write(w)
		return
	}
	model := r.URL.Query().Get(modelParam)
	if model == "" || len(model) > 256 {
		invalidParameter(modelParam, "use the model name shown in the report, up to 256 bytes").write(w)
		return
	}
	rows, err := s.opts.Reports.ModelActivity(r.Context(), p.query, model)
	if err != nil {
		s.fail(w, "reading the model's activity", err)
		return
	}
	writeJSON(w, s.log, groups(rows))
}

type attributedToolJSON struct {
	Tool        string  `json:"tool"`
	Server      string  `json:"server"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	Calls       int64   `json:"calls"`
	Errors      int64   `json:"errors"`
	Tokens      float64 `json:"tokens"`
	Cost        float64 `json:"cost"`
	PricedCalls int64   `json:"pricedCalls"`
}

func (s *Server) toolModels(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject)
	if bad != nil {
		bad.write(w)
		return
	}
	if s.opts.ToolModels.Queries == nil {
		problem{Type: problemNotFound, Title: "Tool attribution unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	report, err := s.opts.ToolModels.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "attributing tool usage", err)
		return
	}
	out := make([]attributedToolJSON, 0, len(report))
	for _, row := range report {
		out = append(out, attributedToolJSON{
			row.Tool, row.Server, row.Model, row.Provider, row.Calls, row.Errors, row.Tokens, float64(row.Cost), row.PricedCalls,
		})
	}
	writeJSON(w, s.log, out)
}
