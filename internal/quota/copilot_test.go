package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCopilotReadsAccountQuota(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	const payload = `{
		"quotaSnapshots": {
			"premium_interactions": {"remainingPercentage": 37.1234, "resetDate": "2026-12-01T00:00:00Z", "entitlementRequests": 1000, "usedRequests": 1},
			"chat": {"remainingPercentage": 0, "resetDate": "2026-12-01T00:00:00Z"},
			"completions": {"remainingPercentage": 100, "resetDate": "2026-12-01T00:00:00Z", "entitlementRequests": -1}
		}
	}`
	out := copilotFrame(`{"jsonrpc":"2.0","method":"unrelated"}`) +
		copilotFrame(`{"jsonrpc":"2.0","id":2,"result":null}`) +
		copilotFrame(`{"jsonrpc":"2.0","id":1,"result":`+payload+`}`)
	var request bytes.Buffer
	limits, err := copilotRPC(&request, strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	header, body, found := strings.Cut(request.String(), "\r\n\r\n")
	if !found || header != fmt.Sprintf("Content-Length: %d", len(body)) {
		t.Fatalf("bad request frame: %q", request.String())
	}
	var call struct {
		Version string `json:"jsonrpc"`
		ID      int
		Method  string
		Params  map[string]any
	}
	if err := json.Unmarshal([]byte(body), &call); err != nil || call.Version != "2.0" ||
		call.ID != 1 || call.Method != "account.getQuota" || call.Params == nil || len(call.Params) != 0 {
		t.Fatalf("bad request: %s (%v)", body, err)
	}
	assertWindows(t, limits.windows(now), []Window{
		{Provider: "GitHub Copilot", Label: "Premium interactions", Remaining: 37.1234, ResetAt: reset, Observed: now},
		{Provider: "GitHub Copilot", Label: "Chat", Remaining: 0, ResetAt: reset, Observed: now},
	})
}

func TestCopilotOmitsUnusableQuota(t *testing.T) {
	const future = `"resetDate":"2026-12-01T00:00:00Z"`
	for name, snapshot := range map[string]string{
		"absent percentage": future,
		"absent reset":      `"remainingPercentage":32`,
		"disabled":          `"remainingPercentage":32,"hasQuota":false,` + future,
		"empty entitlement": `"remainingPercentage":32,"entitlementRequests":0,` + future,
		"unlimited":         `"remainingPercentage":100,"isUnlimitedEntitlement":true,` + future,
		"negative":          `"remainingPercentage":-0.0000000001,` + future,
		"too large":         `"remainingPercentage":101,` + future,
		"invalid reset":     `"remainingPercentage":32,"resetDate":"invalid"`,
		"expired":           `"remainingPercentage":32,"resetDate":"2026-11-01T00:00:00Z"`,
	} {
		t.Run(name, func(t *testing.T) {
			payload := `{"jsonrpc":"2.0","id":1,"result":{"quotaSnapshots":{"chat":{` + snapshot + `}}}}`
			limits, err := copilotRPC(&bytes.Buffer{}, strings.NewReader(copilotFrame(payload)))
			if err != nil {
				t.Fatal(err)
			}
			if windows := limits.windows(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)); len(windows) != 0 {
				t.Fatalf("unexpected windows: %v", windows)
			}
		})
	}
}

func TestCopilotRejectsBrokenRPC(t *testing.T) {
	for name, response := range map[string]string{
		"no reply":        "",
		"RPC error":       copilotFrame(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601}}`),
		"missing result":  copilotFrame(`{"jsonrpc":"2.0","id":1}`),
		"oversized reply": "Content-Length: 1048577\r\n\r\n",
		"truncated body":  "Content-Length: 100\r\n\r\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := copilotRPC(&bytes.Buffer{}, strings.NewReader(response)); err == nil {
				t.Fatal("accepted broken response")
			}
		})
	}
}

func copilotFrame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
