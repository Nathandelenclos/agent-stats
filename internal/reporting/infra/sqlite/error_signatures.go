package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"regexp"
	"strings"

	"modernc.org/sqlite"

	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

var variableErrorParts = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|(?:req(?:uest)?|msg|trace|run|session|resp)[_-][a-z0-9_-]+|(?:0x)?[0-9a-f]{12,}|[0-9]+)\b`)

func normalizeError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	return variableErrorParts.ReplaceAllStringFunc(message, func(part string) string {
		if len(part) == 3 && part >= "400" && part <= "599" && part[1] >= '0' && part[1] <= '9' && part[2] >= '0' && part[2] <= '9' {
			return part
		}
		return "<value>"
	})
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("agent_stats_error_signature", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		message, _ := args[0].(string)
		return normalizeError(message), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("agent_stats_error_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		message, _ := args[0].(string)
		sum := sha256.Sum256([]byte(normalizeError(message)))
		return hex.EncodeToString(sum[:16]), nil
	})
}

// ErrorSignatures returns the most common normalized error messages in the selected report.
func (s *SQLiteSessions) ErrorSignatures(ctx context.Context, f report.Filter, limit int) (out []report.ErrorSignature, err error) {
	err = s.snapshot(ctx, func(q querier) error {
		r := reader{q: q}
		cond, args, err := r.where(f, "r")
		if err != nil {
			return err
		}
		separator := " AND "
		if cond == "" {
			separator = " WHERE "
		}
		rows, err := q.Query(`SELECT agent_stats_error_key(r.error), substr(agent_stats_error_signature(r.error), 1, 200),
			COUNT(*), MAX(r.ts) FROM usage_requests r`+cond+
			separator+`r.error IS NOT NULL GROUP BY 1 ORDER BY COUNT(*) DESC, MAX(r.ts) DESC, 1 LIMIT ?`, append(args, limit)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g report.ErrorSignature
			var ts int64
			if err := rows.Scan(&g.Key, &g.Message, &g.Count, &ts); err != nil {
				return err
			}
			g.Last = fromMillis(ts)
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}
