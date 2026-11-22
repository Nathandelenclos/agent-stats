package command

import (
	"os"
	"strings"
	"testing"
)

func TestPricesCommandRejectsUnexpectedArguments(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"agent-stats", "prices", "pull", "extra"}
	if err := Run(testAnalysisSkill); err == nil || !strings.Contains(err.Error(), "expected prices pull") {
		t.Fatalf("unexpected command error: %v", err)
	}
}
