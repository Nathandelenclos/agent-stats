package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"
)

type grokBilling struct {
	Config *struct {
		UsedPercent *float64 `json:"creditUsagePercent"`
		Period      *struct {
			Type  string    `json:"type"`
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"currentPeriod"`
		MonthlyLimit *grokCents `json:"monthlyLimit"`
		Used         *grokCents `json:"used"`
		BillingEnd   time.Time  `json:"billingPeriodEnd"`
	} `json:"config"`
}

type grokCents struct {
	Value int64 `json:"val"`
}

// ReadGrok lit le quota du compte connecté dans Grok Build.
func ReadGrok(ctx context.Context) []Window {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "grok", "--no-auto-update", "agent", "stdio")
	cmd.Dir = os.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return nil
	}
	closePipes := context.AfterFunc(ctx, func() {
		_ = in.Close()
		_ = out.Close()
	})
	defer func() {
		cancel()
		_ = cmd.Wait()
		closePipes()
	}()
	return grokRPC(in, out).windows(time.Now())
}

func grokRPC(in io.Writer, out io.Reader) grokBilling {
	var billing grokBilling
	// Le RPC de facturation utilise la connexion existante sans démarrer les serveurs MCP configurés.
	if _, err := io.WriteString(in, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"_x.ai/billing\",\"params\":{}}\n"); err != nil {
		return billing
	}
	const maxReply = 1 << 20
	scan := bufio.NewScanner(io.LimitReader(out, maxReply))
	scan.Buffer(make([]byte, 8192), maxReply)
	for scan.Scan() {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scan.Bytes(), &reply) != nil || reply.ID != 1 {
			continue
		}
		if len(reply.Error) != 0 || json.Unmarshal(reply.Result, &billing) != nil {
			return grokBilling{}
		}
		break
	}
	return billing
}

func (billing grokBilling) windows(now time.Time) []Window {
	config := billing.Config
	if config == nil {
		return nil
	}

	var label string
	var used float64
	var reset time.Time
	switch {
	case config.UsedPercent != nil:
		used = *config.UsedPercent
		period := config.Period
		if period == nil || period.Start.After(now) {
			return nil
		}
		reset = period.End
		switch period.Type {
		case "USAGE_PERIOD_TYPE_WEEKLY":
			label = "Weekly limit"
		case "USAGE_PERIOD_TYPE_MONTHLY":
			label = "Monthly limit"
		default:
			return nil
		}
	case config.Period == nil && config.MonthlyLimit != nil && config.MonthlyLimit.Value > 0 &&
		config.Used != nil && config.Used.Value >= 0:
		label = "Monthly limit"
		used = min(100, 100*float64(config.Used.Value)/float64(config.MonthlyLimit.Value))
		reset = config.BillingEnd
	default:
		return nil
	}

	if reset.IsZero() {
		return nil
	}
	window, ok := validWindow("Grok Build", label, used, reset, now)
	if !ok {
		return nil
	}
	return []Window{window}
}
