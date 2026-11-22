package web

import (
	"net/http"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

const promptExcerptChars = 240

type promptReviewJSON struct {
	Scope          string         `json:"scope"`
	Count          int            `json:"count"`
	Sessions       int            `json:"sessions"`
	PromptsVisible bool           `json:"promptsVisible"`
	Prompts        []flaggedJSON  `json:"prompts"`
	Recurring      []repeatedJSON `json:"recurring"`
	Folders        []folderJSON   `json:"byFolder"`
}

type flaggedJSON struct {
	Position int64       `json:"position,omitempty"`
	Revision string      `json:"revision,omitempty"`
	Session  string      `json:"session"`
	Harness  string      `json:"harness"`
	Title    string      `json:"title"`
	Folder   string      `json:"folder"`
	TS       int64       `json:"ts"`
	Excerpt  string      `json:"excerpt,omitempty"`
	Signals  signalsJSON `json:"signals"`
}

type repeatedJSON struct {
	Excerpt  string `json:"excerpt,omitempty"`
	Count    int    `json:"count"`
	Sessions int    `json:"sessions"`
	FirstTS  int64  `json:"firstTs"`
	LastTS   int64  `json:"lastTs"`
}

type folderJSON struct {
	Folder   string `json:"folder"`
	Prompts  int    `json:"prompts"`
	Sessions int    `json:"sessions"`
	Signals  int    `json:"signals"`
}

func (s *Server) prompts(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramLimit, paramOffset)
	if bad != nil {
		bad.write(w)
		return
	}
	if s.opts.Prompts.Queries == nil {
		problem{Type: problemNotFound, Title: "Prompt review unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	review, err := s.opts.Prompts.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "reviewing the prompts", err)
		return
	}
	writeJSON(w, s.log, promptReview(review, p.scope, s.localOnly, !s.opts.OmitPromptExcerpts, p.limit, p.offset))
}

func promptReview(r reporting.PromptReview, scope string, localPrompts, includeExcerpts bool, limit, offset int) promptReviewJSON {
	start := min(offset, len(r.Prompts))
	shown := r.Prompts[start : start+min(limit, len(r.Prompts)-start)]
	out := promptReviewJSON{
		Scope: scope, Count: len(r.Prompts), Sessions: r.Sessions, PromptsVisible: localPrompts,
		Prompts: make([]flaggedJSON, 0, len(shown)), Recurring: make([]repeatedJSON, 0, len(r.Recurring)),
		Folders: make([]folderJSON, 0, len(r.Folders)),
	}
	for _, p := range shown {
		row := flaggedJSON{Session: p.Session.String(), Harness: p.Harness.String(), Title: p.Title, Folder: p.Folder,
			TS: kernel.UnixMilli(p.At), Signals: encodeSignals(p.Signals)}
		if localPrompts {
			if includeExcerpts {
				row.Excerpt = textfmt.Excerpt(p.Text, promptExcerptChars)
			}
			if p.Position > 0 {
				row.Position = p.Position
				row.Revision = promptRevision(row.TS, p.Chars, p.Text)
			}
		}
		out.Prompts = append(out.Prompts, row)
	}
	for _, ask := range r.Recurring {
		row := repeatedJSON{Count: ask.Count, Sessions: ask.Sessions, FirstTS: kernel.UnixMilli(ask.First), LastTS: kernel.UnixMilli(ask.Last)}
		if localPrompts && includeExcerpts {
			row.Excerpt = textfmt.Excerpt(ask.Text, promptExcerptChars)
		}
		out.Recurring = append(out.Recurring, row)
	}
	for _, f := range r.Folders {
		out.Folders = append(out.Folders, folderJSON{f.Folder, f.Prompts, f.Sessions, f.Signals})
	}
	return out
}

type modelSignalsJSON struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prompts  int    `json:"prompts"`
	Flagged  int    `json:"flagged"`
}

func (s *Server) modelSignals(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject)
	if bad != nil {
		bad.write(w)
		return
	}
	if s.opts.ModelSignals.Queries == nil {
		problem{Type: problemNotFound, Title: "Model signals unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	groups, err := s.opts.ModelSignals.Run(r.Context(), p.query)
	if err != nil {
		s.fail(w, "comparing prompt signals", err)
		return
	}
	out := make([]modelSignalsJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, modelSignalsJSON{g.Provider, g.Model, g.Prompts, g.Flagged})
	}
	writeJSON(w, s.log, out)
}
