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
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Resolve returns the absolute path path leads to, symbolic links resolved and each existing folder named as it is on
// disk (a volume that ignores case lets a path be written in any case). The path is read from left to right, a link
// being followed before the ".." that comes after it, as the system would. The part of it that does not exist (a
// project folder since deleted) is kept as written, tidied, under the part that does. A path that is not absolute
// names no place, and gives "".
func Resolve(path string) string { return newResolver().resolve(path) }

// resolver resolves paths and remembers what each existing path it met leads to, so that the many folders of an index,
// which share their parents, read the disk once per parent. It lasts one run: the disk may change after.
// ponytail: links are read when asked, not atomically; a link swapped meanwhile only mislabels one answer.
type resolver struct{ seen map[string]string }

func newResolver() *resolver { return &resolver{seen: map[string]string{}} }

func (r *resolver) resolve(path string) string {
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
		if real, ok := r.seen[next]; ok {
			cur = real
			continue
		}
		info, err := os.Lstat(next)
		if err != nil { // nothing there, or nothing that can be read: the rest is kept as written
			return filepath.Join(append([]string{cur}, elems[i:]...)...)
		}
		real := filepath.Join(cur, onDisk(cur, e, info))
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(real)
			if errors.Is(err, fs.ErrNotExist) { // a link to nothing: it stays what it is
				return filepath.Join(append([]string{cur}, elems[i:]...)...)
			}
			if err != nil { // a link loop: take the path as written
				return filepath.Clean(path)
			}
			real = r.resolve(target) // the link was written in some case too
		}
		r.seen[next], cur = real, real
	}
	return cur
}

// onDisk is the name the entry e of dir (whose Lstat is info) has on disk. On a volume that ignores case, the entry
// is also reached under e with its case swapped: the name is then read from the folder's listing. On a volume that
// tells the cases apart, the swapped name is another entry or none, and e is what it is.
func onDisk(dir, e string, info fs.FileInfo) string {
	swapped := strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, e)
	if other, err := os.Lstat(filepath.Join(dir, swapped)); swapped == e || err != nil || !os.SameFile(info, other) {
		return e
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return e
	}
	for _, entry := range entries {
		if fi, err := entry.Info(); err == nil && strings.EqualFold(entry.Name(), e) && os.SameFile(fi, info) {
			return entry.Name()
		}
	}
	return e
}

// Of returns the project rooted at dir, relative to the working folder when it is not absolute.
func Of(dir string) (kernel.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return kernel.Project{}, err
	}
	return kernel.NewProject(Resolve(abs))
}
