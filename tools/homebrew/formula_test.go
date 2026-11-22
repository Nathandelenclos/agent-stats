package homebrew

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormulaUsesThePublishedArchiveForEachArchitecture(t *testing.T) {
	intelSHA := strings.Repeat("a", 64)
	armSHA := strings.Repeat("b", 64)
	intel := intelSHA + "  agent-stats_0.6.0_darwin_amd64.tar.gz\n"
	arm := armSHA + "  agent-stats_0.6.0_darwin_arm64.tar.gz\n"
	for _, tt := range []struct {
		name      string
		tag       string
		checksums string
		reject    bool
	}{
		{name: "published archives", tag: "v0.6.0", checksums: intel + arm},
		{name: "missing archive", tag: "v0.6.0", checksums: intel, reject: true},
		{name: "duplicate archive", tag: "v0.6.0", checksums: intel + arm + intel, reject: true},
		{name: "invalid digest", tag: "v0.6.0", checksums: intel + strings.Replace(arm, armSHA, "broken", 1), reject: true},
		{name: "invalid tag", tag: "v0.6.0-rc1", checksums: intel + arm, reject: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "checksums.txt")
			if err := os.WriteFile(manifest, []byte(tt.checksums), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := exec.Command("bash", "formula.sh", tt.tag, manifest).CombinedOutput()
			if tt.reject {
				if err == nil || strings.Contains(string(got), "class AgentStats") {
					t.Fatalf("generated a formula for invalid input: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("generate formula: %v\n%s", err, got)
			}
			for _, block := range []string{
				"on_intel do\n    url \"https://github.com/Nathandelenclos/agent-stats/releases/download/v0.6.0/agent-stats_0.6.0_darwin_amd64.tar.gz\"\n    sha256 \"" + intelSHA + "\"",
				"on_arm do\n    url \"https://github.com/Nathandelenclos/agent-stats/releases/download/v0.6.0/agent-stats_0.6.0_darwin_arm64.tar.gz\"\n    sha256 \"" + armSHA + "\"",
			} {
				if !strings.Contains(string(got), block) {
					t.Errorf("missing architecture-specific archive and digest: %s", block)
				}
			}
		})
	}
}
