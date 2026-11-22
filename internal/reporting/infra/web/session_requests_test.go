package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type requestIndex func(domain.SessionID, domain.Filter, bool, int, int) ([]domain.SessionRequest, error)

func (fn requestIndex) SessionRequests(_ context.Context, id domain.SessionID, f domain.Filter, failed bool, limit, offset int) ([]domain.SessionRequest, error) {
	return fn(id, f, failed, limit, offset)
}

func TestSessionRequestsFiltersAndErrorPrivacy(t *testing.T) {
	for _, remote := range []bool{false, true} {
		r := newRig(t, func(o *Options) {
			o.AllowRemote = remote
			o.SessionRequests.Queries = requestIndex(func(id domain.SessionID, f domain.Filter, failed bool, limit, offset int) ([]domain.SessionRequest, error) {
				if id.String() != "claude:root/sub" || f.Harness.String() != "claude" || !failed || limit != 3 || offset != 2 {
					t.Fatalf("unexpected query: %v, %+v, %v, %d, %d", id, f, failed, limit, offset)
				}
				return []domain.SessionRequest{{Error: "private failure"}, {Model: "opus"}, {Model: "next"}}, nil
			})
		})

		target := sessionBase + "/requests"
		got := decode[sessionRequestsJSON](t, r.get(target+"?harness=claude&failed=1&limit=2&offset=2"))
		if len(got.Requests) != 2 || !got.HasMore || !got.Requests[0].Failed {
			t.Fatalf("unexpected page: %+v", got)
		}
		if (got.Requests[0].Error != "") == remote {
			t.Fatalf("remote=%v error detail=%q", remote, got.Requests[0].Error)
		}
		wantProblem(t, r.get(target+"?offset=-1"), http.StatusBadRequest, problemInvalidParameter)
	}
}
