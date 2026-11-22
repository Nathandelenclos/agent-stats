package quota

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"
)

func TestCodexReadsTheAccountRateLimits(t *testing.T) {
	const response = `{"rateLimits":{
		"primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1700000600},
		"secondary":{"usedPercent":100,"windowDurationMins":10080}
	}}`
	now := time.Unix(1700000000, 0)
	limits, err := simulateCodex(t, "result", response)
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{
		{Provider: "Codex", Label: "5-hour limit", Remaining: 100, ResetAt: time.Unix(1700000600, 0), Observed: now},
		{Provider: "Codex", Label: "7-day limit", Remaining: 0, Observed: now},
	}
	assertWindows(t, limits.windows(now), want)
}

func TestCodexPrefersNamedBucketsAndOmitsUnusableWindows(t *testing.T) {
	const response = `{
		"rateLimits":{"primary":{"usedPercent":5,"windowDurationMins":300}},
		"rateLimitsByLimitId":{
			"codex":{"primary":{"usedPercent":40,"windowDurationMins":10080},"secondary":{"windowDurationMins":300}},
			"separate":{"limitName":"Separate","primary":{"usedPercent":75,"windowDurationMins":60}},
			"expired":{"primary":{"usedPercent":50,"windowDurationMins":300,"resetsAt":1700000000}},
			"invalid":{"primary":{"usedPercent":101,"windowDurationMins":300}}
		}
	}`
	now := time.Unix(1700000000, 0)
	limits, err := simulateCodex(t, "result", response)
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{
		{Provider: "Codex", Label: "7-day limit", Remaining: 60, Observed: now},
		{Provider: "Codex", Label: "Separate · 1-hour limit", Remaining: 25, Observed: now},
	}
	assertWindows(t, limits.windows(now), want)
}

func TestCodexRejectsAnUnsuccessfulQuotaReply(t *testing.T) {
	limits, err := simulateCodex(t, "error", `{"code":-32603,"message":"Usage unavailable"}`)
	if err == nil || len(limits.windows(time.Unix(1700000000, 0))) != 0 {
		t.Fatalf("expected no quota after an RPC error, got %v (%v)", limits, err)
	}
}

func simulateCodex(t *testing.T, field, payload string) (codexLimits, error) {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- replyToCodexClient(server, field, payload)
	}()
	limits, err := codexRPC(client, client)
	client.Close()
	if mockErr := <-done; mockErr != nil {
		t.Fatal(mockErr)
	}
	return limits, err
}

func replyToCodexClient(conn net.Conn, field, payload string) error {
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	for _, method := range []string{"initialize", "initialized", "account/rateLimits/read"} {
		var request struct {
			ID     json.RawMessage
			Method string
			Params struct {
				ClientInfo struct {
					Name, Version string
				}
			}
		}
		if err := dec.Decode(&request); err != nil {
			return fmt.Errorf("reading %s: %w", method, err)
		}
		if request.Method != method {
			return fmt.Errorf("got method %q, want %q", request.Method, method)
		}
		switch method {
		case "initialize":
			if request.Params.ClientInfo.Name == "" || request.Params.ClientInfo.Version == "" || len(request.ID) == 0 {
				return fmt.Errorf("initialize omitted client information or request ID")
			}
			if err := enc.Encode(map[string]any{"id": request.ID, "result": map[string]string{}}); err != nil {
				return err
			}
		case "initialized":
			if len(request.ID) != 0 {
				return fmt.Errorf("initialized must be a notification")
			}
		default:
			if len(request.ID) == 0 {
				return fmt.Errorf("rate-limit request omitted its ID")
			}
			if err := enc.Encode(map[string]string{"method": "irrelevant/notification"}); err != nil {
				return err
			}
			return enc.Encode(map[string]any{"id": request.ID, field: json.RawMessage(payload)})
		}
	}
	return nil
}

func assertWindows(t *testing.T, got, want []Window) {
	t.Helper()
	equal := slices.EqualFunc(got, want, func(a, b Window) bool {
		return a.Provider == b.Provider && a.Label == b.Label && a.Remaining == b.Remaining &&
			a.Observed.Equal(b.Observed) && a.ResetAt.Equal(b.ResetAt)
	})
	if !equal {
		t.Errorf("quota windows = %#v, want %#v", got, want)
	}
}
