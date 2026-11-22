package domain

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// RelationKind selects the dimension of the activity behind a report row.
type RelationKind int

const (
	RelationDay RelationKind = iota
	RelationHarness
	RelationModel
	RelationFolder
	RelationAgent
	RelationTool
	RelationMCP
)

// SessionRelation identifies the slice of activity a session listing should explain.
type SessionRelation struct {
	Kind    RelationKind
	Value   string         // group name, tool name, or MCP server name
	Server  string         // MCP server for a tool, empty for a built-in tool
	Day     time.Time      // calendar date for RelationDay, interpreted in the local time zone
	Servers []string       // exact observed server names contributing to a catalog entry
	Harness kernel.Harness // optional identity scope of a catalog server
}
