package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const codexTimeout = 5 * time.Second

type codexLimit struct {
	Used    *float64 `json:"usedPercent"`
	Minutes int64    `json:"windowDurationMins"`
	Reset   int64    `json:"resetsAt"`
}

type codexBucket struct {
	Name      string     `json:"limitName"`
	Primary   codexLimit `json:"primary"`
	Secondary codexLimit `json:"secondary"`
}

type codexLimits struct {
	Single  codexBucket            `json:"rateLimits"`
	Buckets map[string]codexBucket `json:"rateLimitsByLimitId"`
}

// ReadCodex asks the installed Codex app-server for the active account's quota.
func ReadCodex(ctx context.Context) []Window {
	ctx, cancel := context.WithTimeout(ctx, codexTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	result, err := codexRPC(in, out)
	if err != nil {
		return nil
	}
	return result.windows(time.Now())
}

func codexRPC(in io.Writer, out io.Reader) (codexLimits, error) {
	var limits codexLimits
	enc := json.NewEncoder(in)
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 8192), 1<<20)
	request := func(id int, method string, params any) (json.RawMessage, error) {
		if err := enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return nil, err
		}
		for scan.Scan() {
			var answer struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(scan.Bytes(), &answer) == nil && answer.ID != nil && *answer.ID == id {
				if len(answer.Result) == 0 || len(answer.Error) > 0 {
					return nil, fmt.Errorf("Codex %s failed", method)
				}
				return answer.Result, nil
			}
		}
		return nil, fmt.Errorf("no reply to Codex %s", method)
	}
	info := map[string]any{"clientInfo": map[string]string{"name": "agent-stats", "version": "1"}}
	if _, err := request(0, "initialize", info); err != nil {
		return limits, err
	}
	if err := enc.Encode(map[string]string{"method": "initialized"}); err != nil {
		return limits, err
	}
	raw, err := request(1, "account/rateLimits/read", nil)
	if err != nil {
		return limits, err
	}
	err = json.Unmarshal(raw, &limits)
	return limits, err
}

func (r codexLimits) windows(now time.Time) []Window {
	if len(r.Buckets) == 0 {
		r.Buckets = map[string]codexBucket{"codex": r.Single}
	}
	keys := make([]string, 0, len(r.Buckets))
	for key := range r.Buckets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var windows []Window
	for _, key := range keys {
		bucket := r.Buckets[key]
		for _, limit := range []codexLimit{bucket.Primary, bucket.Secondary} {
			if limit.Used == nil {
				continue
			}
			label := periodLabel(limit.Minutes)
			if key != "codex" {
				name := strings.TrimSpace(bucket.Name)
				if name == "" {
					name = key
				}
				label = name + " · " + label
			}
			if window, ok := validWindow("Codex", label, *limit.Used, resetTime(limit.Reset), now); ok {
				windows = append(windows, window)
			}
		}
	}
	return windows
}
