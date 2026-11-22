package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

const paramRevision = "revision"

type fullPromptJSON struct {
	Position int64       `json:"position"`
	TS       int64       `json:"ts"`
	Text     string      `json:"text"`
	Chars    int         `json:"chars"`
	Signals  signalsJSON `json:"signals"`
}

func (s *Server) sessionPrompt(w http.ResponseWriter, r *http.Request) {
	// Apply the same policy as session excerpts before looking up any prompt.
	if !s.localOnly || s.opts.SessionPrompt.Queries == nil || s.opts.Sessions.Queries == nil {
		promptMissing(w)
		return
	}
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, paramRevision)
	if bad != nil {
		bad.write(w)
		return
	}
	id, idErr := reporting.NewSessionID(r.PathValue("id"))
	position, posErr := strconv.ParseInt(r.PathValue("position"), 10, 64)
	if idErr != nil || posErr != nil || position <= 0 {
		promptMissing(w)
		return
	}
	revision := r.URL.Query().Get(paramRevision)
	if revision != "" {
		digest, err := hex.DecodeString(revision)
		if err != nil || len(digest) != 16 {
			invalidParameter(paramRevision, "use the revision of the selected prompt preview").write(w)
			return
		}
	}
	if !p.query.Harness.IsZero() || !p.query.Since.IsZero() {
		show := reporting.ShowSession{Queries: s.opts.Sessions.Queries}
		detail, err := show.Run(r.Context(), id, p.query)
		switch {
		case errors.Is(err, reporting.ErrSessionNotFound):
			promptMissing(w)
			return
		case err != nil:
			s.fail(w, "checking the prompt's session", err)
			return
		case !sessionMatches(detail.SessionSummary, p.query):
			promptMissing(w)
			return
		}
	}
	full, err := s.opts.SessionPrompt.Run(r.Context(), id, position, p.query)
	switch {
	case errors.Is(err, reporting.ErrPromptNotFound), errors.Is(err, reporting.ErrSessionNotFound):
		promptMissing(w)
	case err != nil:
		s.fail(w, "reading the prompt", err)
	default:
		ts := kernel.UnixMilli(full.At)
		if revision != "" && revision != promptRevision(ts, full.Chars, full.Text) {
			problem{Type: problemPromptChanged, Title: "Prompt changed", Status: http.StatusConflict, Detail: "Refresh the prompt list and open it again."}.write(w)
			return
		}
		writeJSON(w, s.log, fullPromptJSON{full.Position, ts, full.Text, full.Chars, encodeSignals(full.Signals)})
	}
}

func promptMissing(w http.ResponseWriter) {
	problem{Type: problemNotFound, Title: "Prompt not found in this view", Status: http.StatusNotFound}.write(w)
}

// A position can be reused when the index is rebuilt. The revision describes the preview the reader opened.
func promptRevision(ts int64, chars int, text string) string {
	prefix, _, _ := strings.Cut(text, "\x00")
	i := 0
	for at := range prefix {
		if i == domain.PromptTextLimit {
			prefix = prefix[:at]
			break
		}
		i++
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(ts, 10) + "\n" + strconv.Itoa(chars) + "\n" + prefix))
	return hex.EncodeToString(digest[:16])
}
