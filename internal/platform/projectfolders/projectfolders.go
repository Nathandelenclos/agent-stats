// Package projectfolders finds the project a folder belongs to and narrows the index's published views to a project.
// Paths are compared after their symbolic links are resolved, so a project reached through a link and the same
// project reached directly are one. Working folders come from session logs, which nobody vouches for: they are cleaned
// and resolved here, at the time of the question, and a folder that names no place is in no project.
package projectfolders

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Resolve returns the absolute path path leads to, symbolic links resolved. The path is read from left to right, a
// link being followed before the ".." that comes after it, as the system would. The part of it that does not exist
// (a project folder since deleted) is kept as written, tidied, under the part that does. A path that is not absolute
// names no place, and gives "".
// ponytail: links are read when asked, not atomically; a link swapped meanwhile only mislabels one answer.
func Resolve(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	vol := filepath.VolumeName(path)
	cur := vol + string(filepath.Separator)
	elems := strings.Split(path[len(vol):], string(filepath.Separator))
	for i, e := range elems {
		switch e {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, e)
		info, err := os.Lstat(next)
		if err != nil { // nothing there, or nothing that can be read: the rest is kept as written
			return filepath.Join(append([]string{cur}, elems[i:]...)...)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(next)
			if errors.Is(err, fs.ErrNotExist) { // a link to nothing: it stays what it is
				return filepath.Join(append([]string{cur}, elems[i:]...)...)
			}
			if err != nil { // a link loop: take the path as written
				return filepath.Clean(path)
			}
			next = real
		}
		cur = next
	}
	return cur
}

// Of returns the project rooted at dir, relative to the working folder when it is not absolute.
func Of(dir string) (kernel.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return kernel.Project{}, err
	}
	return kernel.NewProject(Resolve(abs))
}
