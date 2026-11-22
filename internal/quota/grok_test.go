package quota

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGrokReadsAccountAllowance(t *testing.T) {
	response := `{"jsonrpc":"2.0","method":"_x.ai/notification"}
{"jsonrpc":"2.0","id":1,"result":{"config":{"creditUsagePercent":42.5,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-11-16T00:00:00Z","end":"2026-11-23T00:00:00Z"},"prepaidBalance":{"val":1250},"onDemandCap":{"val":5000}}}}`
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	var sent bytes.Buffer

	billing := grokRPC(&sent, strings.NewReader(response))

	var request struct {
		Version string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal(sent.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.Version != "2.0" || request.ID != 1 || request.Method != "_x.ai/billing" {
		t.Fatalf("unexpected billing request: %+v", request)
	}
	assertWindows(t, billing.windows(now), []Window{
		{Provider: "Grok Build", Label: "Weekly limit", Remaining: 57.5, ResetAt: now.AddDate(0, 0, 1), Observed: now},
	})
}

func TestGrokConvertsLegacyMonthlyAllowance(t *testing.T) {
	response := `{"id":1,"result":{"config":{"monthlyLimit":{"val":100},"used":{},"billingPeriodEnd":"2026-12-01T00:00:00Z"}}}`
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)

	billing := grokRPC(&bytes.Buffer{}, strings.NewReader(response))

	assertWindows(t, billing.windows(now), []Window{
		{Provider: "Grok Build", Label: "Monthly limit", Remaining: 100, ResetAt: now.AddDate(0, 0, 9), Observed: now},
	})
}

func TestGrokSkipsUnavailableWindows(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for name, response := range map[string]string{
		"authentication required": `{"id":1,"error":{"code":-32000}}`,
		"prepaid credits only":    `{"id":1,"result":{"config":{"prepaidBalance":{"val":1250}}}}`,
		"invalid percentage":      `{"id":1,"result":{"config":{"creditUsagePercent":101,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-11-23T00:00:00Z"}}}}`,
		"invalid reset":           `{"id":1,"result":{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"later"}}}}`,
		"expired window":          `{"id":1,"result":{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-11-21T00:00:00Z"}}}}`,
		"future window":           `{"id":1,"result":{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-11-23T00:00:00Z","end":"2026-11-30T00:00:00Z"}}}}`,
		"missing period":          `{"id":1,"result":{"config":{"creditUsagePercent":0}}}`,
		"unknown period":          `{"id":1,"result":{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_UNKNOWN","end":"2026-11-23T00:00:00Z"}}}}`,
		"missing reset":           `{"id":1,"result":{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY"}}}}`,
		"legacy missing reset":    `{"id":1,"result":{"config":{"monthlyLimit":{"val":100},"used":{}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			billing := grokRPC(&bytes.Buffer{}, strings.NewReader(response))
			assertWindows(t, billing.windows(now), nil)
		})
	}
}
