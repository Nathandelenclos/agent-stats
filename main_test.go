package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/command"
)

func TestCLIInstallsTheEmbeddedAnalysisSkill(t *testing.T) {
	dir := t.TempDir()
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{"agent-stats", "skill", "install", "--dir", dir}
	if err := command.Run(analysisSkill); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(".agents/skills/agent-stats-analysis/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(dir, "agent-stats-analysis", "SKILL.md"))
	if err != nil || string(installed) != string(source) {
		t.Fatalf("installed skill differs from the source: %v", err)
	}
}
