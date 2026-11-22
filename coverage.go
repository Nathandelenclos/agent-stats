package main

import (
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
)

// coverage is what the views of one run cover: project is the project they can be restricted to (the zero Project when
// there is none), and restricted says whether they are restricted to it now.
type coverage struct {
	project    kernel.Project
	restricted bool
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

// homeProject is the user's home folder, the zero Project when it cannot be told.
func homeProject() kernel.Project {
	home, err := os.UserHomeDir()
	if err != nil {
		return kernel.Project{}
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
// and the project stays available to switch to.
func chooseCoverage(all bool, given, working kernel.Project, detect func(kernel.Project) (kernel.Project, error)) (coverage, error) {
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

// coverageOf decides what a run covers from the flags --all and --project (dir, empty when absent).
func coverageOf(all bool, dir string, detect func(kernel.Project) (kernel.Project, error)) (coverage, error) {
	given, err := givenProject(dir)
	if err != nil {
		return coverage{}, err
	}
	return chooseCoverage(all, given, workingProject(), detect)
}
