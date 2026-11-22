// Package projectfolders finds the project a folder belongs to and narrows the index's published views to a project.
// Paths are compared after their symbolic links are resolved, so a project reached through a link and the same
// project reached directly are one. Working folders come from session logs, which nobody vouches for: they are cleaned
// and resolved here, at the time of the question, and a folder that names no place is in no project.
package projectfolders

import (
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Resolve returns the absolute path path leads to, symbolic links resolved. The part of it that does not exist (a
// project folder since deleted) is kept as written, under the part that does. A path that is not absolute names no
// place, and gives "".
// ponytail: links are read when asked, not atomically; a link swapped meanwhile only mislabels one answer.
func Resolve(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	path = filepath.Clean(path)
	rest := ""
	for p := path; ; {
		parent := filepath.Dir(p)
		if parent == p { // nothing of it can be read, a link loop or a denied folder: take it as written
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
	}
}

// Of returns the project rooted at dir, relative to the working folder when it is not absolute.
func Of(dir string) (kernel.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return kernel.Project{}, err
	}
	return kernel.NewProject(Resolve(abs))
}
