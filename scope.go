package main

import (
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectscope"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// scope is what the views of one run cover: project is the project they can be restricted to (the zero Project when
// there is none), and scoped says whether they are restricted to it now.
type scope struct {
	project kernel.Project
	scoped  bool
}

// restriction is the project a query is restricted to; the zero Project for every project.
func (s scope) restriction() kernel.Project {
	if s.scoped {
		return s.project
	}
	return kernel.Project{}
}

// label says what the views cover, for the first line of a report.
func (s scope) label() string {
	if !s.scoped {
		return "All projects"
	}
	return fmt.Sprintf("Project: %s (%s)", textfmt.Printable(s.project.Name()), textfmt.Printable(s.project.Root()))
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

// chooseScope decides what a run covers. A project the user gave (--project) is the one the views are restricted to.
// Otherwise it is the working folder's project, when sessions worked in it, unless the user asked for every project
// (--all); the working folder's project stays available to switch to in that case.
func chooseScope(all bool, given, working kernel.Project, detect reporting.DetectProject) (scope, error) {
	if !given.IsZero() {
		return scope{project: given, scoped: !all}, nil
	}
	found, err := detect.Run(working)
	return scope{project: found, scoped: !all && !found.IsZero()}, err
}

// scopeOf decides what a run covers from the flags --all and --project (dir, empty when absent) and the index.
func scopeOf(all bool, dir string, index reporting.ProjectQueries) (scope, error) {
	var given kernel.Project
	if dir != "" {
		var err error
		if given, err = projectscope.Of(dir); err != nil {
			return scope{}, err
		}
	}
	return chooseScope(all, given, workingProject(), reporting.DetectProject{Queries: index})
}
