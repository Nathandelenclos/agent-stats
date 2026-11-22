package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const copilotTimeout = 5 * time.Second

type copilotSnapshot struct {
	Entitlement *int64   `json:"entitlementRequests"`
	HasQuota    *bool    `json:"hasQuota"`
	Unlimited   bool     `json:"isUnlimitedEntitlement"`
	Remaining   *float64 `json:"remainingPercentage"`
	Reset       string   `json:"resetDate"`
}

type copilotLimits struct {
	Snapshots map[string]copilotSnapshot `json:"quotaSnapshots"`
}

// ReadCopilot asks the installed Copilot CLI for the active account's quota.
func ReadCopilot(ctx context.Context) []Window {
	ctx, cancel := context.WithTimeout(ctx, copilotTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "copilot", "--headless", "--stdio", "--no-auto-update", "--disable-builtin-mcps")
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
	limits, err := copilotRPC(in, out)
	if err != nil {
		return nil
	}
	return limits.windows(time.Now())
}

func copilotRPC(in io.Writer, out io.Reader) (copilotLimits, error) {
	var limits copilotLimits
	const request = `{"jsonrpc":"2.0","id":1,"method":"account.getQuota","params":{}}`
	if _, err := fmt.Fprintf(in, "Content-Length: %d\r\n\r\n%s", len(request), request); err != nil {
		return limits, err
	}
	const maxReplyBytes = 1 << 20
	reader := bufio.NewReader(io.LimitReader(out, maxReplyBytes))
	for {
		header, err := reader.ReadSlice('\n')
		if err != nil {
			return limits, err
		}
		value, ok := strings.CutPrefix(string(header), "Content-Length:")
		size, err := strconv.Atoi(strings.TrimSpace(value))
		if !ok || err != nil || size <= 0 || size > maxReplyBytes {
			return limits, fmt.Errorf("invalid Copilot message length")
		}
		separator, err := reader.ReadSlice('\n')
		if err != nil || string(separator) != "\r\n" {
			return limits, fmt.Errorf("invalid Copilot message separator")
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(reader, body); err != nil {
			return limits, err
		}
		var reply struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(body, &reply); err != nil {
			return limits, err
		}
		if string(reply.ID) != "1" {
			continue
		}
		if len(reply.Error) != 0 || len(reply.Result) == 0 {
			return limits, fmt.Errorf("Copilot account quota unavailable")
		}
		err = json.Unmarshal(reply.Result, &limits)
		return limits, err
	}
}

func (limits copilotLimits) windows(now time.Time) []Window {
	var windows []Window
	for _, bucket := range []struct{ key, label string }{
		{"premium_interactions", "Premium interactions"},
		{"chat", "Chat"},
		{"completions", "Completions"},
	} {
		snapshot := limits.Snapshots[bucket.key]
		if snapshot.Remaining == nil || *snapshot.Remaining < 0 || *snapshot.Remaining > 100 ||
			snapshot.Unlimited || (snapshot.HasQuota != nil && !*snapshot.HasQuota) ||
			(snapshot.Entitlement != nil && *snapshot.Entitlement <= 0) {
			continue
		}
		reset, err := time.Parse(time.RFC3339, snapshot.Reset)
		if err != nil {
			continue
		}
		if window, ok := validWindow("GitHub Copilot", bucket.label, 100-*snapshot.Remaining, reset, now); ok {
			window.Remaining = *snapshot.Remaining
			windows = append(windows, window)
		}
	}
	return windows
}
