package application

import "github.com/Nathandelenclos/agent-stats/internal/kernel"

// ProjectQueries tells where the indexed sessions worked.
type ProjectQueries interface {
	// HasSessions reports whether the index holds a session whose working folder is the project's folder or lies below it.
	HasSessions(kernel.Project) (bool, error)
}

// DetectProject decides which project a view is about when the user did not say: the project of the folder
// agent-stats runs in, if sessions worked in it. A folder no session worked in (the home folder, a new one) has
// nothing to report on its own, and the view is then about every project.
type DetectProject struct {
	Queries ProjectQueries
}

// Run returns candidate when the index holds sessions of that project, the zero Project (every project) otherwise.
func (uc DetectProject) Run(candidate kernel.Project) (kernel.Project, error) {
	if candidate.IsZero() {
		return kernel.Project{}, nil
	}
	found, err := uc.Queries.HasSessions(candidate)
	if err != nil || !found {
		return kernel.Project{}, err
	}
	return candidate, nil
}
