// Command agent-stats indexes the session logs of coding agents and reports their usage.
package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/command"
)

//go:embed .agents/skills/agent-stats-analysis/SKILL.md
var analysisSkill string

func main() {
	if err := command.Run(analysisSkill); err != nil {
		fmt.Fprintln(os.Stderr, "agent-stats:", err)
		os.Exit(1)
	}
}
