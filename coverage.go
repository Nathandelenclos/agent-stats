package main

import (
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectscope"
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
// zero Project when that folder cannot be told.
func workingProject() kernel.Project {
	cwd, err := os.Getwd()
	if err != nil {
		return kernel.Project{}
	}
	p, err := projectscope.Detect(cwd)
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
	p, err := projectscope.Of(home)
	if err != nil {
		return kernel.Project{}
	}
	return p
}

// chooseCoverage decides what a run covers. A project the user gave (--project) is the one the views are restricted to.
// Otherwise it is the working folder's project, when sessions worked in it, see kernel.WorkingProject. Whichever the
// project, --all asks for every project, and the project stays available to switch to.
func chooseCoverage(all bool, given, working, home kernel.Project, workedIn func(kernel.Project) (bool, error)) (coverage, error) {
	if !given.IsZero() {
		return coverage{project: given, restricted: !all}, nil
	}
	found, err := kernel.WorkingProject(working, home, workedIn)
	return coverage{project: found, restricted: !all && !found.IsZero()}, err
}

// coverageOf decides what a run covers from the flags --all and --project (dir, empty when absent) and the index.
func coverageOf(all bool, dir string, workedIn func(kernel.Project) (bool, error)) (coverage, error) {
	var given kernel.Project
	if dir != "" {
		var err error
		if given, err = projectscope.Of(dir); err != nil {
			return coverage{}, err
		}
	}
	return chooseCoverage(all, given, workingProject(), homeProject(), workedIn)
}
