package command

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/platform/files"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
)

const analysisSkillDirectory = "agent-stats-analysis"
const analysisSkillFile = "SKILL.md"

func runSkill(args []string, content string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, "Usage: agent-stats skill install [--dir DIR] [--force]")
		return nil
	}
	if args[0] != "install" {
		return fmt.Errorf("unknown skill command %q, use skill install", args[0])
	}
	flags := flag.NewFlagSet("agent-stats skill install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "skills directory (defaults to .agents/skills under your home folder)")
	force := flags.Bool("force", false, "replace an existing regular file with the bundled skill")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected skill install argument %q", flags.Arg(0))
	}
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot find your home directory, pass --dir: %w", err)
		}
		*dir = filepath.Join(home, ".agents", "skills")
	}
	destination, err := filepath.Abs(filepath.Join(*dir, analysisSkillDirectory))
	if err != nil {
		return err
	}
	installed, err := installAnalysisSkill(destination, content, *force)
	if err != nil {
		return err
	}
	action := "Installed"
	if !installed {
		action = "Already installed"
	}
	fmt.Fprintf(stdout, "%s %s at %s\n", action, analysisSkillDirectory,
		textfmt.Printable(filepath.Join(destination, analysisSkillFile)))
	return nil
}

func installAnalysisSkill(dir, content string, force bool) (bool, error) {
	path := filepath.Join(dir, analysisSkillFile)
	same, err := sameInstalledSkill(path, content)
	switch {
	case err == nil && same:
		return false, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return false, err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("cannot write a linked skill directory %q, choose another --dir", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return false, err
	}
	if !os.SameFile(info, opened) {
		return false, fmt.Errorf("skill directory %q changed while opening it", dir)
	}

	if info, err := root.Lstat(analysisSkillFile); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("cannot replace %q because it is not a regular file", path)
		}
		if !force {
			return unchangedOrConflict(path, content)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	tempName := ".skill-" + rand.Text()
	temp, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer root.Remove(tempName)
	_, writeErr := io.WriteString(temp, content)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return false, err
	}
	if force {
		err := root.Rename(tempName, analysisSkillFile)
		return err == nil, err
	}
	// A concurrent install can win without replacing the user's file or exposing a partial write.
	if err := root.Link(tempName, analysisSkillFile); err != nil {
		if errors.Is(err, os.ErrExist) {
			return unchangedOrConflict(path, content)
		}
		return false, err
	}
	return true, nil
}

func sameInstalledSkill(path, content string) (bool, error) {
	file, err := files.OpenRegular(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, int64(len(content))+1))
	return string(body) == content, err
}

func unchangedOrConflict(path, content string) (bool, error) {
	if same, _ := sameInstalledSkill(path, content); same {
		return false, nil
	}
	return false, fmt.Errorf("%q already contains a different skill, use --force to replace it", path)
}
