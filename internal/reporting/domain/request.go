package domain

import "time"

// SessionRequest describes one indexed usage record in a session run.
type SessionRequest struct {
	At         time.Time
	Provider   string
	StopReason string
	Model      string
	AgentType  string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       USD
	Priced     bool
	Duration   Milliseconds
	TTFT       Milliseconds
	Error      string
}
