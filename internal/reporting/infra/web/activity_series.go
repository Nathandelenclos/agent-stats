package web

import (
	"net/http"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

type activitySampleJSON struct {
	Bucket             string  `json:"bucket"`
	Name               string  `json:"name"`
	Source             string  `json:"source"`
	Calls              int64   `json:"calls"`
	Errors             int64   `json:"errors"`
	AvgDurationMS      float64 `json:"avgDurationMs"`
	AvgTTFTMS          float64 `json:"avgTtftMs"`
	AvgTokensPerSecond float64 `json:"avgTokensPerSecond"`
	DurationSamples    int64   `json:"durationSamples"`
	TTFTSamples        int64   `json:"ttftSamples"`
	ThroughputSamples  int64   `json:"throughputSamples"`
	Input              int64   `json:"input"`
	Output             int64   `json:"output"`
	CacheRead          int64   `json:"cacheRead"`
	CacheWrite         int64   `json:"cacheWrite"`
	Cost               float64 `json:"cost"`
	PremiumUnits       float64 `json:"premiumUnits"`
	Unpriced           int64   `json:"unpriced"`
}

type activitySeriesJSON struct {
	BucketSize domain.BucketSize    `json:"bucketSize"`
	Samples    []activitySampleJSON `json:"samples"`
}

func (s *Server) activitySeries(w http.ResponseWriter, r *http.Request) {
	p, bad := s.parseParams(r, paramSince, paramHarness, paramProject, "kind")
	if bad != nil {
		bad.write(w)
		return
	}
	kind := domain.ActivityKind(r.URL.Query().Get("kind"))
	if kind != domain.ModelActivity && kind != domain.ToolActivity {
		invalidParameter("kind", "use models or tools").write(w)
		return
	}
	if s.opts.ActivitySeries.Queries == nil {
		problem{Type: problemNotFound, Title: "Activity series unavailable", Status: http.StatusNotFound}.write(w)
		return
	}
	size := domain.DayBucket
	switch age := s.now().Sub(p.query.Since); {
	case p.query.Since.IsZero() || age > 120*24*time.Hour:
		size = domain.MonthBucket
	case age <= 72*time.Hour:
		size = domain.HourBucket
	}
	rows, err := s.opts.ActivitySeries.Run(r.Context(), p.query, kind, size)
	if err != nil {
		s.fail(w, "reading activity series", err)
		return
	}
	out := activitySeriesJSON{BucketSize: size, Samples: make([]activitySampleJSON, 0, len(rows))}
	for _, row := range rows {
		out.Samples = append(out.Samples, activitySampleJSON{
			Bucket: row.Bucket, Name: row.Name, Source: row.Source, Calls: row.Calls, Errors: row.Errors,
			AvgDurationMS: row.AvgDuration, AvgTTFTMS: row.AvgTTFT, AvgTokensPerSecond: row.AvgThroughput,
			DurationSamples: row.DurationSamples, TTFTSamples: row.TTFTSamples, ThroughputSamples: row.ThroughputSamples,
			Input: row.Input, Output: row.Output, CacheRead: row.CacheRead, CacheWrite: row.CacheWrite,
			Cost: row.Cost, PremiumUnits: row.PremiumUnits, Unpriced: row.Unpriced,
		})
	}
	writeJSON(w, s.log, out)
}
