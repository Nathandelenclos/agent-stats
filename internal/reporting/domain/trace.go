package domain

import "time"

// TraceEvent identifies an observed moment of a session without inferring timestamps missing from the log.
type TraceEvent struct {
	At        time.Time
	Kind      string
	Label     string
	AgentID   string
	AgentType string
	Duration  Milliseconds
	Failed    bool
}
