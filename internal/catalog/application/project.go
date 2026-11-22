package application

import "github.com/Nathandelenclos/agent-stats/internal/kernel"

// ProjectQueries tells where the indexed sessions worked.
type ProjectQueries interface {
	// HasSessions reports whether the index holds a session whose working folder is the project's folder or lies below it.
	HasSessions(kernel.Project) (bool, error)
}

// DetectProject decides which project a view is about when the user did not name one, as kernel.WorkingProject states.
type DetectProject struct {
	Queries ProjectQueries
	// Home is the user's home folder, which is nobody's project.
	Home kernel.Project
}

// Run returns candidate, the project of the folder the program runs in, when sessions worked in it; the zero Project (every
// project) otherwise.
func (uc DetectProject) Run(candidate kernel.Project) (kernel.Project, error) {
	return kernel.WorkingProject(candidate, uc.Home, uc.Queries.HasSessions)
}
