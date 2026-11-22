package sqlite

import (
	"context"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

const recordQuotaHistory = `
CREATE TABLE IF NOT EXISTS quota_snapshots (
	observed_at INTEGER NOT NULL, provider TEXT NOT NULL, label TEXT NOT NULL, reset_at INTEGER NOT NULL,
	remaining REAL NOT NULL CHECK (remaining BETWEEN 0 AND 100),
	PRIMARY KEY (observed_at, provider, label, reset_at)
) WITHOUT ROWID;
`

// RecordQuotaWindows retains observations made through the local quota view. Duplicate snapshots are ignored.
func (s *SQLiteIndex) RecordQuotaWindows(ctx context.Context, windows []quota.Window) error {
	if len(windows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range windows {
		if w.Observed.IsZero() {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO quota_snapshots VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			kernel.UnixMilli(w.Observed), w.Provider, w.Label, kernel.UnixMilli(w.ResetAt), w.Remaining); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteIndex) QuotaHistory(ctx context.Context, since time.Time, limit int) ([]quota.Window, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at, provider, label, reset_at, remaining
		FROM quota_snapshots WHERE observed_at >= ? ORDER BY observed_at DESC, provider, label, reset_at LIMIT ?`,
		kernel.UnixMilli(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []quota.Window
	for rows.Next() {
		var w quota.Window
		var observed, reset int64
		if err := rows.Scan(&observed, &w.Provider, &w.Label, &reset, &w.Remaining); err != nil {
			return nil, err
		}
		w.Observed = time.UnixMilli(observed)
		if reset != 0 {
			w.ResetAt = time.UnixMilli(reset)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
