package main

import (
	"fmt"
	"os"
	"os/user"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
)

// coverage is what the views of one run cover: project is the project they can be restricted to (the zero Project when
// there is none), and restricted says whether they are restricted to it now. viaHome says the user named the home folder
// as the project, which holds every project: the views then cover them all, and the label says why.
type coverage struct {
	project    kernel.Project
	restricted bool
	viaHome    bool
}

// restriction is the project a query is restricted to; the zero Project for every project.
func (c coverage) restriction() kernel.Project {
	if c.restricted {
		return c.project
	}
	return kernel.Project{}
}

// label says what the views cover, for the first line of a report.
func (c coverage) label() string {
	if c.viaHome {
		return "All projects (the home folder is not a project)"
	}
	if !c.restricted {
		return "All projects"
	}
	return fmt.Sprintf("Project: %s (%s)", textfmt.Printable(c.project.Name()), textfmt.Printable(c.project.Root()))
}

// workingProject is the project of the folder agent-stats runs in: the Git top-level, or the folder itself. It is the
// zero Project when that folder cannot be told. Reading the working folder is the composition root's job.
func workingProject() kernel.Project {
	cwd, err := os.Getwd()
	if err != nil {
		return kernel.Project{}
	}
	p, err := projectfolders.Detect(cwd)
	if err != nil {
		return kernel.Project{}
	}
	return p
}

// homeProject is the user's home folder: $HOME, or else what the user database says. It is the zero Project when
// neither tells.
func homeProject() kernel.Project {
	home, err := os.UserHomeDir()
	if err != nil {
		u, uerr := user.Current()
		if uerr != nil || u.HomeDir == "" {
			return kernel.Project{}
		}
		home = u.HomeDir
	}
	p, err := projectfolders.Of(home)
	if err != nil {
		return kernel.Project{}
	}
	return p
}

// chooseCoverage decides what a run covers. A project the user gave (--project) is the one the views are restricted to.
// Otherwise it is what detect makes of the working folder's project: that project when sessions worked in it, none
// otherwise (the DetectProject use case of the command's context). Whichever the project, --all asks for every project,
// and the project stays available to switch to. The home folder is nobody's project, given or not: naming it asks for
// every project.
func chooseCoverage(all bool, given, working, home kernel.Project, detect func(kernel.Project) (kernel.Project, error)) (coverage, error) {
	given, viaHome := kernel.NamedProject(given, home)
	if viaHome {
		return coverage{viaHome: true}, nil
	}
	if !given.IsZero() {
		return coverage{project: given, restricted: !all}, nil
	}
	found, err := detect(working)
	return coverage{project: found, restricted: !all && !found.IsZero()}, err
}

// givenProject is the project --project names, the zero Project when the flag is absent. Like workingProject, it reads
// the process's environment, which is the composition root's job: nothing below main knows folders or flags.
func givenProject(dir string) (kernel.Project, error) {
	if dir == "" {
		return kernel.Project{}, nil
	}
	return projectfolders.Of(dir)
}

// redetection is how the views of a long run find the project of the working folder again once the index has grown, which an
// empty index could not tell at first. It is nil when the user named the scope (--all, --project): detection must not
// change that.
func redetection(all bool, dir string, detect func(kernel.Project) (kernel.Project, error)) func() (kernel.Project, error) {
	if all || dir != "" {
		return nil
	}
	return func() (kernel.Project, error) { return detect(workingProject()) }
}

// coverageOf decides what a run covers from the flags --all and --project (dir, empty when absent).
func coverageOf(all bool, dir string, detect func(kernel.Project) (kernel.Project, error)) (coverage, error) {
	given, err := givenProject(dir)
	if err != nil {
		return coverage{}, err
	}
	return chooseCoverage(all, given, workingProject(), homeProject(), detect)
}
