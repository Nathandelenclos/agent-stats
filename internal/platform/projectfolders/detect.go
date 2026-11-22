package projectfolders

import (
	"os"
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Detect returns the project the working folder cwd belongs to: the top-level folder of its Git repository, found by
// looking for .git (a folder, or the file a linked worktree and a submodule have) from cwd upwards, or cwd itself when
// it is in no repository. No git process is run.
func Detect(cwd string) (kernel.Project, error) {
	here, err := Of(cwd)
	if err != nil {
		return kernel.Project{}, err
	}
	for dir := here.Root(); ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return kernel.NewProject(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return here, nil
		}
		dir = parent
	}
}
