package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAnalysisSkill = "---\nname: agent-stats-analysis\n---\n\n# Analyze agent activity\n"

func TestSkillInstallKeepsAnIdenticalInstallation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, ".agents", "skills", "agent-stats-analysis", "SKILL.md")
	var out bytes.Buffer
	if err := runSkill([]string{"install"}, testAnalysisSkill, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), path) {
		t.Fatalf("install output = %q, want path %q", &out, path)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != testAnalysisSkill {
		t.Fatalf("installed skill = %q (%v)", got, err)
	}
	out.Reset()
	if err := runSkill([]string{"install"}, testAnalysisSkill, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Already installed") {
		t.Fatalf("repeat install output = %q", &out)
	}
}

func TestSkillInstallRequiresForceToReplaceCustomContent(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	args := []string{"install", "--dir", dir}
	if err := runSkill(args, testAnalysisSkill, &out, &out); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-stats-analysis", "SKILL.md")
	if err := os.WriteFile(path, []byte("custom instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSkill(args, testAnalysisSkill, &out, &out); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("install over a customized skill = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "custom instructions\n" {
		t.Fatalf("custom skill = %q (%v)", got, err)
	}
	if err := runSkill(append(args, "--force"), testAnalysisSkill, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != testAnalysisSkill {
		t.Fatalf("forced skill = %q (%v)", got, err)
	}
}

func TestSkillInstallRecognizesIdenticalLinksWithoutOverwritingTheirTargets(t *testing.T) {
	for _, directoryLink := range []bool{false, true} {
		name := "file"
		if directoryLink {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			dest, source := t.TempDir(), t.TempDir()
			target := filepath.Join(source, "SKILL.md")
			if err := os.WriteFile(target, []byte(testAnalysisSkill), 0o600); err != nil {
				t.Fatal(err)
			}
			linkPath := filepath.Join(dest, "agent-stats-analysis")
			linkTarget := source
			if !directoryLink {
				if err := os.Mkdir(linkPath, 0o700); err != nil {
					t.Fatal(err)
				}
				linkPath = filepath.Join(linkPath, "SKILL.md")
				linkTarget = target
			}
			if err := os.Symlink(linkTarget, linkPath); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}

			var out bytes.Buffer
			args := []string{"install", "--dir", dest}
			if err := runSkill(args, testAnalysisSkill, &out, &out); err != nil || !strings.Contains(out.String(), "Already installed") {
				t.Fatalf("identical linked install = %q, %v", &out, err)
			}
			if err := runSkill(append(args, "--force"), testAnalysisSkill+"Changed.\n", &out, &out); err == nil {
				t.Fatal("overwrote a linked installation")
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != testAnalysisSkill {
				t.Fatalf("linked skill = %q (%v)", got, err)
			}
		})
	}
}

func TestSkillInstallRejectsUnknownArgumentsBeforeCreatingTheDestination(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	var out bytes.Buffer
	err := runSkill([]string{"install", "--dir", dir, "unexpected"}, testAnalysisSkill, &out, &out)
	if err == nil {
		t.Fatal("install accepted an unexpected argument")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("invalid install created its destination: %v", err)
	}
}

func TestConcurrentSkillInstallsKeepOneCompleteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-stats-analysis")
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := installAnalysisSkill(dir, testAnalysisSkill, false)
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("installation directory = %v (%v)", entries, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err != nil || string(got) != testAnalysisSkill {
		t.Fatalf("installed skill = %q (%v)", got, err)
	}
}
