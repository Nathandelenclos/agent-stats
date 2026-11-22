package sqlite

import (
	"context"
	"fmt"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

var _ reporting.ActivitySeriesQuery = (*SQLiteUsage)(nil)

var activityBuckets = map[report.BucketSize]string{
	report.HourBucket:  "CAST(a.ts / 3600000 * 3600000 AS TEXT)",
	report.DayBucket:   "strftime('%Y-%m-%d', a.ts / 1000, 'unixepoch', 'localtime')",
	report.MonthBucket: "strftime('%Y-%m', a.ts / 1000, 'unixepoch', 'localtime')",
}

const modelActivityColumns = `COALESCE(a.model, ''), COALESCE(a.provider, ''), COUNT(*), COUNT(a.error),
	COALESCE(AVG(a.duration_ms), 0), COUNT(a.duration_ms), COALESCE(AVG(a.ttft_ms), 0), COUNT(a.ttft_ms),
	COALESCE(AVG(CASE WHEN a.duration_ms > 0 AND a.output > 0 THEN 1000.0 * a.output / a.duration_ms END), 0),
	COUNT(CASE WHEN a.duration_ms > 0 AND a.output > 0 THEN 1 END),
	COALESCE(SUM(a.input), 0), COALESCE(SUM(a.output), 0), COALESCE(SUM(a.cache_read), 0), COALESCE(SUM(a.cache_write), 0),
	COALESCE(SUM(a.cost), 0), COALESCE(SUM(a.premium_units), 0), COALESCE(SUM(1 - a.priced), 0)`

const toolActivityColumns = `COALESCE(a.name, ''), COALESCE(a.server, ''), COUNT(*), COALESCE(SUM(a.is_error), 0),
	COALESCE(AVG(a.duration_ms), 0), COUNT(a.duration_ms), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0`

// ActivitySeries groups observed events over time. Unknown timestamps have no bucket.
func (s *SQLiteUsage) ActivitySeries(ctx context.Context, f report.Filter, kind report.ActivityKind, size report.BucketSize) ([]report.ActivitySample, error) {
	bucket, ok := activityBuckets[size]
	if !ok {
		return nil, fmt.Errorf("unknown activity bucket %q", size)
	}
	var view, columns string
	switch kind {
	case report.ModelActivity:
		view, columns = "usage_requests", modelActivityColumns
	case report.ToolActivity:
		view, columns = "usage_tool_calls", toolActivityColumns
	default:
		return nil, fmt.Errorf("unknown activity kind %q", kind)
	}
	r := reader{q: scoped{ctx, s.db}}
	cond, args, err := r.where(f, "a")
	if err != nil {
		return nil, err
	}
	if cond == "" {
		cond = " WHERE a.ts > 0"
	} else {
		cond += " AND a.ts > 0"
	}
	rows, err := r.q.Query(`SELECT `+bucket+`,`+columns+` FROM `+view+` a`+cond+
		` GROUP BY 1, 2, 3 ORDER BY MIN(a.ts), 2, 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []report.ActivitySample
	for rows.Next() {
		var sample report.ActivitySample
		if err := rows.Scan(&sample.Bucket, &sample.Name, &sample.Source, &sample.Calls, &sample.Errors,
			&sample.AvgDuration, &sample.DurationSamples, &sample.AvgTTFT, &sample.TTFTSamples,
			&sample.AvgThroughput, &sample.ThroughputSamples,
			&sample.Input, &sample.Output, &sample.CacheRead, &sample.CacheWrite, &sample.Cost, &sample.PremiumUnits, &sample.Unpriced); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
