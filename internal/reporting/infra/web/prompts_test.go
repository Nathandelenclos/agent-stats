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

type promptIndex struct {
	filter domain.Filter
}

func (x *promptIndex) FlaggedPrompts(_ context.Context, f domain.Filter) ([]domain.FlaggedPrompt, error) {
	x.filter = f
	a, _ := domain.NewSessionID("claude:a")
	b, _ := domain.NewSessionID("claude:b")
	p := domain.FlaggedPrompt{
		Session: a, Harness: kernel.Claude, Folder: "/work/app",
		Position: 12, Chars: 14, Text: "private prompt", Signals: domain.PromptSignals{Negation: 1},
	}
	second := p
	second.Session = b
	return []domain.FlaggedPrompt{p, second}, nil
}

func promptsRig(t *testing.T, remote bool) (*rig, *promptIndex) {
	t.Helper()
	idx := &promptIndex{}
	return newRig(t, func(o *Options) {
		o.Prompts = reporting.ReviewPrompts{Queries: idx}
		o.Project, _ = kernel.NewProject("/work/app")
		o.AllowRemote = remote
	}), idx
}

func TestPromptReviewUsesTheRequestedFilters(t *testing.T) {
	r, idx := promptsRig(t, false)
	rec := r.get("/api/prompts?harness=claude&since=7d&project=current")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if idx.filter.Project.Root() != "/work/app" || idx.filter.Harness != kernel.Claude || !idx.filter.Since.Equal(fixedNow.AddDate(0, 0, -7)) {
		t.Fatalf("filter = %+v", idx.filter)
	}
}

func TestPromptReviewKeepsTheFullCountsWhenLimited(t *testing.T) {
	r, _ := promptsRig(t, false)
	rec := r.get("/api/prompts?limit=1")
	got := decode[promptReviewJSON](t, rec)
	if got.Count != 2 || len(got.Prompts) != 1 || got.Prompts[0].Position != 12 || got.Prompts[0].Revision != promptRevision(0, 14, "private prompt") || len(got.Recurring) != 1 || got.Recurring[0].Sessions != 2 || got.Folders[0].Prompts != 2 {
		t.Fatalf("review = %+v", got)
	}
}

func TestRemotePromptReviewOmitsAllExcerpts(t *testing.T) {
	r, _ := promptsRig(t, true)
	rec := r.get("/api/prompts")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "private prompt") || strings.Contains(body, `"excerpt"`) || strings.Contains(body, `"position"`) || strings.Contains(body, `"revision"`) || !strings.Contains(body, `"promptsVisible":false`) {
		t.Fatalf("remote report = %s", body)
	}
}
