package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

const (
	claudeQuotaFile      = "claude-quota.json"
	antigravityQuotaFile = "antigravity-quota.json"
)

func quotaFile(name string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "agent-stats", name)
}

func recordQuotaStatus(provider, file string, record func(io.Reader, string, time.Time) error) error {
	path := quotaFile(file)
	if path == "" {
		return fmt.Errorf("no user cache directory for %s quota", provider)
	}
	return record(os.Stdin, path, time.Now())
}

func readQuotas(ctx context.Context) []quota.Window {
	windows := quota.ReadCodex(ctx)
	now := time.Now()
	if path := quotaFile(claudeQuotaFile); path != "" {
		windows = append(windows, quota.ReadClaude(path, now)...)
	}
	if path := quotaFile(antigravityQuotaFile); path != "" {
		windows = append(windows, quota.ReadAntigravity(path, now)...)
	}
	windows = append(windows, quota.ReadCopilot(ctx)...)
	windows = append(windows, quota.ReadGrok(ctx)...)
	return append(windows, quota.ReadOpenCodeGo(ctx, os.Getenv("AGENT_STATS_OPENCODE_URL"))...)
}
