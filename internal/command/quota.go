package command

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

func quotaFile() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "agent-stats", "claude-quota.json")
}

func readQuotas(ctx context.Context) []quota.Window {
	windows := quota.ReadCodex(ctx)
	if path := quotaFile(); path != "" {
		windows = append(windows, quota.ReadClaude(path, time.Now())...)
	}
	return windows
}
