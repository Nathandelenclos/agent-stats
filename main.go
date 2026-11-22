// Command agent-stats indexes the session logs of coding agents and reports their usage.
package main

import (
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/command"
)

func main() {
	if err := command.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-stats:", err)
		os.Exit(1)
	}
}
