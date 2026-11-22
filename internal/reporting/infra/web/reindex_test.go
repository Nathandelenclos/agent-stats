package web

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestReindexRejectsRequestsFromAnotherOrigin(t *testing.T) {
	r := newRig(t)
	for _, site := range []string{"", "same-site", "cross-site", "none"} {
		rec := r.do(http.MethodPost, "/api/sync", "Sec-Fetch-Site", site)
		wantProblem(t, rec, http.StatusForbidden, problemForeignOrigin)
	}
}

func TestReindexIsUnavailableWithNoSync(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/sync", "Sec-Fetch-Site", "same-origin")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestReindexDoesNotStartTwoJobsAtTheSameTime(t *testing.T) {
	release := make(chan struct{})
	var starts atomic.Int32
	r := newRig(t, func(o *Options) {
		o.Sync = func(ctx context.Context, progress func(int)) (string, error) {
			starts.Add(1)
			select {
			case <-release:
				progress(2)
			case <-ctx.Done():
			}
			return "", nil
		}
	})
	r.handler.indexCtx = t.Context()
	for range 2 {
		rec := r.do(http.MethodPost, "/api/sync", "Sec-Fetch-Site", "same-origin")
		if rec.Code != http.StatusOK || decode[syncStatus](t, rec).State != syncRunning {
			close(release)
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
	}
	close(release)
	r.handler.sync.jobs.Wait()
	if n := starts.Load(); n != 1 {
		t.Fatalf("%d jobs started, want 1", n)
	}
}

func TestReindexCanRunAgainAfterCompletion(t *testing.T) {
	var starts atomic.Int32
	r := newRig(t, func(o *Options) {
		o.Sync = func(context.Context, func(int)) (string, error) {
			starts.Add(1)
			return "", nil
		}
	})
	r.handler.indexCtx = t.Context()
	for range 2 {
		rec := r.do(http.MethodPost, "/api/sync", "Sec-Fetch-Site", "same-origin")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		r.handler.sync.jobs.Wait()
	}
	if n := starts.Load(); n != 2 {
		t.Fatalf("%d jobs started, want 2", n)
	}
}
