package domain

// ActivityKind selects which recorded events are compared over time.
type ActivityKind string

const (
	ModelActivity ActivityKind = "models"
	ToolActivity  ActivityKind = "tools"
)

// BucketSize bounds how finely activity is grouped over a report's period.
type BucketSize string

const (
	HourBucket  BucketSize = "hour"
	DayBucket   BucketSize = "day"
	MonthBucket BucketSize = "month"
)

// ActivitySample counts dated activity for one model/provider or tool/server.
type ActivitySample struct {
	Bucket, Name, Source                 string
	Calls, Errors                        int64
	AvgDuration, AvgTTFT, AvgThroughput  float64
	DurationSamples, TTFTSamples         int64
	ThroughputSamples                    int64
	Input, Output, CacheRead, CacheWrite int64
	Cost, PremiumUnits                   float64
	Unpriced                             int64
}
