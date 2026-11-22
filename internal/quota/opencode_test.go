package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenCodeReadsGoAllowanceFromLocalServer(t *testing.T) {
	reset := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	body := fmt.Sprintf(`{"location":{"directory":"/private/workspace"},
		"data":{"workspace":{"name":"private account"},
		"budgets":[{"limit":80,"spent":32,"resetsAt":%[1]d}],
		"go":{"rolling":{"percent":0,"limited":false,"resetsAt":%[1]d},
		"weekly":{"percent":100,"limited":true,"resetsAt":%[1]d},
		"monthly":{"percent":12.5,"limited":false,"resetsAt":%[1]d}}}}`, reset.UnixMilli())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.RequestURI() != "/api/experimental/usage/limits" ||
			req.Header.Get("Accept") != "application/json" || req.Header.Get("Authorization") != "" {
			t.Errorf("unexpected OpenCode request: %s %s", req.Method, req.URL.RequestURI())
		}
		io.WriteString(w, body)
	}))
	defer server.Close()

	before := time.Now()
	windows := ReadOpenCodeGo(context.Background(), server.URL)
	if len(windows) != 3 {
		t.Fatalf("got %d quota windows, want 3", len(windows))
	}
	observed := windows[0].Observed
	if observed.Before(before) || observed.After(time.Now()) {
		t.Errorf("observation outside read interval: %v", observed)
	}
	assertWindows(t, windows, []Window{
		{Provider: "OpenCode Go", Label: "5-hour limit", Remaining: 100, ResetAt: reset, Observed: observed},
		{Provider: "OpenCode Go", Label: "7-day limit", Remaining: 0, ResetAt: reset, Observed: observed},
		{Provider: "OpenCode Go", Label: "Monthly limit", Remaining: 87.5, ResetAt: reset, Observed: observed},
	})
}

func TestOpenCodeSkipsUnavailableOrInvalidData(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"data":{"go":{"rolling":{"percent":37,"resetsAt":%d}}}}`, future)
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"unsigned", http.StatusOK, `{"data":null}`},
		{"budgets only", http.StatusOK, `{"data":{"budgets":[{"limit":80,"spent":32}]}}`},
		{"incomplete and expired", http.StatusOK, fmt.Sprintf(`{"data":{"go":{"rolling":{"resetsAt":%d},"weekly":{"percent":33},"monthly":{"percent":50,"resetsAt":1}}}}`, future)},
		{"out of range", http.StatusOK, fmt.Sprintf(`{"data":{"go":{"rolling":{"percent":-1,"resetsAt":%d},"weekly":{"percent":101,"resetsAt":%d}}}}`, future, future)},
		{"malformed", http.StatusOK, `{"data":{"go":`},
		{"too large", http.StatusOK, body + strings.Repeat(" ", maxOpenCodeUsage)},
		{"protected server", http.StatusUnauthorized, body},
		{"unsupported server", http.StatusNotFound, body},
		{"unavailable upstream", http.StatusServiceUnavailable, body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			assertWindows(t, ReadOpenCodeGo(context.Background(), server.URL), nil)
		})
	}
}

func TestOpenCodeInvalidResetPreservesValidWindows(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"data":{"go":{"rolling":{"percent":20,"resetsAt":%d},
			"weekly":{"percent":50,"resetsAt":9223372036854775807}}}}`, reset.UnixMilli())
	}))
	defer server.Close()

	windows := ReadOpenCodeGo(context.Background(), server.URL)
	if _, err := json.Marshal(windows); err != nil {
		t.Fatalf("quota windows must be serializable: %v", err)
	}
	if len(windows) != 1 || windows[0].Label != "5-hour limit" || windows[0].Remaining != 80 {
		t.Fatalf("expected only the valid quota window, got %v", windows)
	}
}

func TestOpenCodeOnlyContactsExplicitLoopbackOriginsWithoutRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.URL.Path != "/api/experimental/usage/limits" {
			t.Errorf("followed redirect to %s", req.URL.Path)
			return
		}
		if req.Header.Get("Authorization") != "" || req.URL.RawQuery != "" {
			t.Error("contacted a rejected OpenCode origin")
		}
		http.Redirect(w, req, "/redirected", http.StatusFound)
	}))
	defer server.Close()

	for _, origin := range []string{
		"",
		"ftp://127.0.0.1:1234",
		"http://0.0.0.0:1234",
		"http://localhost:1234",
		"http://127.0.0.1",
		"http://[::1]:0",
		"http://192.0.2.1:1234",
		strings.Replace(server.URL, "http://", "http://user:password@", 1),
		server.URL + "/unrelated",
		server.URL + "?unexpected=true",
	} {
		assertWindows(t, ReadOpenCodeGo(context.Background(), origin), nil)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("rejected origins produced %d requests", got)
	}
	assertWindows(t, ReadOpenCodeGo(context.Background(), server.URL), nil)
	if got := requests.Load(); got != 1 {
		t.Errorf("got %d requests, want one without redirect", got)
	}
}
