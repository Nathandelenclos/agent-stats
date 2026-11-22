package kernel

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Project is the folder a body of work lives in: sessions belong to a project when their working folder is its root or
// lies below it. The zero Project stands for no project in particular, that is every project.
// A Project holds an absolute, cleaned path; resolving symbolic links is up to whoever builds it, since that reads the disk.
type Project struct {
	root string
}

// ErrNotAbsolute reports a project root that is not an absolute path.
var ErrNotAbsolute = errors.New("project root is not an absolute path")

// NewProject returns the project rooted at root, which must be an absolute path. It is cleaned, so that
// "/work/app/" and "/work/./app" name the same project.
func NewProject(root string) (Project, error) {
	if !filepath.IsAbs(root) {
		return Project{}, fmt.Errorf("%w: %q", ErrNotAbsolute, root)
	}
	return Project{filepath.Clean(root)}, nil
}

// IsZero reports the zero Project, which stands for every project.
func (p Project) IsZero() bool { return p.root == "" }

// Root returns the project's folder, empty for the zero Project.
func (p Project) Root() string { return p.root }

// Name is the last element of the project's folder, which is how people call it.
func (p Project) Name() string {
	if p.IsZero() {
		return ""
	}
	return filepath.Base(p.root)
}

// WorkingProject decides which project a view is about when the user did not name one: candidate, the project of the
// folder the program runs in, if workedIn says sessions worked in it. A folder no session worked in (a new one) has
// nothing to report on its own, and the home folder is nobody's project since every other one lies below it: the view is
// then about every project, the zero Project. An error of workedIn is returned, never taken for "no session".
func WorkingProject(candidate, home Project, workedIn func(Project) (bool, error)) (Project, error) {
	if candidate.IsZero() || candidate == home {
		return Project{}, nil
	}
	found, err := workedIn(candidate)
	if err != nil || !found {
		return Project{}, err
	}
	return candidate, nil
}

// NamedProject is the project the user named, with whether it was the home folder. The home folder is nobody's project,
// as WorkingProject states: naming it asks for every project, the zero Project.
func NamedProject(given, home Project) (project Project, isHome bool) {
	if !given.IsZero() && given == home {
		return Project{}, true
	}
	return given, false
}

// Contains reports whether folder is the project's root or lies below it. The paths are compared element by element,
// never as text, so "/work/app-2" is not in "/work/app". A folder that is not absolute names no place and is in no
// project; the zero Project contains every folder, a relative one too.
func (p Project) Contains(folder string) bool {
	if p.IsZero() {
		return true
	}
	if !filepath.IsAbs(folder) {
		return false
	}
	rel, err := filepath.Rel(p.root, filepath.Clean(folder))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
