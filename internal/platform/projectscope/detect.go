package projectscope

import (
	"os"
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Detect returns the project the working folder cwd belongs to: the top-level folder of its Git repository, found by
// looking for .git (a folder, or the file a linked worktree and a submodule have) from cwd upwards, or cwd itself when
// it is in no repository. No git process is run.
// The home folder is nobody's project, whatever sits in it (a repository of dotfiles, or just the working folder of a
// shell): every other project lies below it, so the zero Project is returned in its place.
func Detect(cwd, home string) (kernel.Project, error) {
	here, err := Of(cwd)
	if err != nil {
		return kernel.Project{}, err
	}
	root := here
	for dir := here.Root(); ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if root, err = kernel.NewProject(dir); err != nil {
				return kernel.Project{}, err
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if home != "" && root.Root() == Resolve(home) {
		return kernel.Project{}, nil
	}
	return root, nil
}
