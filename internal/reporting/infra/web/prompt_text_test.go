package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

const sessionBase = "/api/sessions/claude%3Aroot%2Fsub"

type fullPromptIndex struct {
	sessionDetailIndex
	full        domain.Prompt
	fullReads   int
	fullProject kernel.Project
}

func (idx *fullPromptIndex) Session(_ context.Context, id domain.SessionID, p kernel.Project) (domain.SessionDetail, error) {
	detail, err := idx.sessionDetailIndex.Session(context.Background(), id, p)
	if err == nil {
		preview := idx.full
		if n := []rune(preview.Text); len(n) > domain.PromptTextLimit {
			preview.Text = string(n[:domain.PromptTextLimit])
		}
		detail.Prompts = []domain.Prompt{preview}
	}
	return detail, err
}

func (idx *fullPromptIndex) SessionPrompt(_ context.Context, id domain.SessionID, position int64, p kernel.Project) (domain.Prompt, error) {
	idx.fullReads++
	idx.fullProject = p
	if id.String() != "claude:root/sub" || position != idx.full.Position {
		return domain.Prompt{}, domain.ErrPromptNotFound
	}
	return idx.full, nil
}

func fullPromptRig(t *testing.T, remote bool) (*rig, *fullPromptIndex) {
	t.Helper()
	text := "# Heading\n\n    indentation\n" + strings.Repeat("é", domain.PromptTextLimit) + "\n<private>"
	idx := &fullPromptIndex{full: domain.Prompt{Position: 19, At: fixedNow, Text: text, Chars: utf8.RuneCountInString(text),
		Signals: domain.PromptSignals{Negation: 2}}}
	r := newRig(t, func(o *Options) {
		o.Sessions.Queries = idx
		o.SessionPrompt.Queries = idx
		o.AllowRemote = remote
		o.Project, _ = kernel.NewProject("/work/app")
	})
	return r, idx
}

func TestLocalPromptReaderReturnsUntruncatedTextForThePreview(t *testing.T) {
	r, idx := fullPromptRig(t, false)
	const query = "?project=current&since=7d&harness=claude"
	detail := decode[sessionDetailJSON](t, r.get(sessionBase+query))
	preview := detail.Prompts[0]
	if preview.Position != 19 || preview.Revision == "" || strings.Contains(preview.Excerpt, "<private>") {
		t.Fatalf("preview position = %d, revision = %q", preview.Position, preview.Revision)
	}
	full := decode[fullPromptJSON](t, r.get(sessionBase+"/prompts/19"+query+"&revision="+preview.Revision))
	if full.Position != preview.Position || full.TS != preview.TS || full.Text != idx.full.Text || full.Chars != idx.full.Chars || full.Signals.Negation != 2 {
		t.Fatal("full prompt lost its content or metadata")
	}
	if idx.fullProject.Root() != "/work/app" || idx.fullReads != 1 {
		t.Errorf("full read in project %q, calls = %d", idx.fullProject.Root(), idx.fullReads)
	}
}

func TestPromptReaderRefusesAChangedIndexPosition(t *testing.T) {
	r, idx := fullPromptRig(t, false)
	detail := decode[sessionDetailJSON](t, r.get(sessionBase))
	idx.full.Text = "A different prompt in the same position"
	idx.full.Chars = utf8.RuneCountInString(idx.full.Text)
	rec := r.get(sessionBase + "/prompts/19?revision=" + detail.Prompts[0].Revision)
	wantProblem(t, rec, http.StatusConflict, problemPromptChanged)
	if strings.Contains(rec.Body.String(), idx.full.Text) {
		t.Fatalf("changed prompt appeared in the response: %s", rec.Body.String())
	}
}

func TestPromptReaderIsUnavailableRemotelyAndOutsideSessionFilters(t *testing.T) {
	remote, remoteIdx := fullPromptRig(t, true)
	wantProblem(t, remote.get(sessionBase+"/prompts/19"), http.StatusNotFound, problemNotFound)
	if remoteIdx.fullReads != 0 {
		t.Fatal("remote access read the full prompt")
	}
	local, localIdx := fullPromptRig(t, false)
	wantProblem(t, local.get(sessionBase+"/prompts/19?harness=codex"), http.StatusNotFound, problemNotFound)
	wantProblem(t, local.get(sessionBase+"/prompts/0"), http.StatusNotFound, problemNotFound)
	wantProblem(t, local.get(sessionBase+"/prompts/19?revision=invalid"), http.StatusBadRequest, problemInvalidParameter)
	if localIdx.fullReads != 0 {
		t.Fatal("a rejected selection read the full prompt")
	}
	wantProblem(t, local.get(sessionBase+"/prompts/20"), http.StatusNotFound, problemNotFound)
}
