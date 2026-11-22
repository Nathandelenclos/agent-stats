package command

import (
	"fmt"

	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
)

func runPrices(args []string) error {
	const usage = "Usage: agent-stats prices pull"
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(usage)
		return nil
	}
	if len(args) != 1 || args[0] != "pull" {
		return fmt.Errorf("expected prices pull")
	}
	count, err := pricetable.Pull()
	if err != nil {
		return fmt.Errorf("pull prices: %w", err)
	}
	fmt.Printf("Updated local prices for %d models.\n", count)
	return nil
}
