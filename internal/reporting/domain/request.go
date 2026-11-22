package domain

import "time"

// SessionRequest describes one indexed usage record in a session run.
type SessionRequest struct {
	At           time.Time
	SessionID    string
	Folder       string
	Provider     string
	StopReason   string
	ServiceTier  string
	Purpose      string
	PremiumUnits float64
	Model        string
	AgentType    string
	Input        int64
	Output       int64
	CacheRead    int64
	CacheWrite   int64
	Cost         USD
	Priced       bool
	Duration     Milliseconds
	TTFT         Milliseconds
	Error        string
}

// ErrorSignature groups repeated errors even when their messages contain changing identifiers.
type ErrorSignature struct {
	Key     string
	Message string
	Count   int64
	Last    time.Time
}
