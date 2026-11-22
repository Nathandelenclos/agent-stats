package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type sessionDetailIndex struct {
	sessionIndex
	project kernel.Project
}

func (idx *sessionDetailIndex) Session(_ context.Context, id domain.SessionID, project kernel.Project) (domain.SessionDetail, error) {
	idx.project = project
	if id.String() != "claude:root/sub" {
		return domain.SessionDetail{}, domain.ErrSessionNotFound
	}
	return domain.SessionDetail{
		SessionSummary: domain.SessionSummary{ID: id, Title: "Session", Harness: kernel.Claude, End: fixedNow},
		Tools:          []domain.ToolUsage{{Name: "Read", Calls: 2}},
		Subagents:      []domain.SubagentUsage{{Title: "Research", AgentType: "explore"}},
		Prompts: []domain.Prompt{{
			Position: 8, Text: "private prompt <script>", Chars: 23, Signals: domain.PromptSignals{Negation: 1},
		}},
	}, nil
}

func sessionDetailRig(t *testing.T, remote bool) (*rig, *sessionDetailIndex) {
	t.Helper()
	idx := &sessionDetailIndex{}
	return newRig(t, func(o *Options) {
		o.Sessions = reporting.ListSessions{Queries: idx}
		o.AllowRemote = remote
		o.Project, _ = kernel.NewProject("/work/app")
	}), idx
}

func TestSessionDetailCarriesTheSessionActivity(t *testing.T) {
	r, idx := sessionDetailRig(t, false)
	rec := r.get("/api/sessions/claude%3Aroot%2Fsub?project=current")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if idx.project.Root() != "/work/app" {
		t.Errorf("queried project = %q", idx.project.Root())
	}
	for _, want := range []string{`"scope":"current"`, `"id":"claude:root/sub"`, `"name":"Read"`, `"title":"Research"`, `"negation":1`, `"position":8`, `"revision":"`, `"excerpt":"private prompt`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q in %s", want, rec.Body.String())
		}
	}
}

func TestRemoteSessionDetailOmitsPromptText(t *testing.T) {
	r, _ := sessionDetailRig(t, true)
	rec := r.get("/api/sessions/claude%3Aroot%2Fsub")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "private prompt") || strings.Contains(body, `"excerpt"`) || strings.Contains(body, `"position"`) || strings.Contains(body, `"revision"`) || !strings.Contains(body, `"promptsVisible":false`) {
		t.Fatalf("remote session data = %s", body)
	}
}

func TestSessionDetailOutsideTheFiltersIsNotFound(t *testing.T) {
	r, _ := sessionDetailRig(t, false)
	wantProblem(t, r.get("/api/sessions/claude%3Aroot%2Fsub?harness=codex"), http.StatusNotFound, problemNotFound)
}
