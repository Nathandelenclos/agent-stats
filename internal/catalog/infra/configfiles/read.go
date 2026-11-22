// Package configfiles reads what the harnesses have installed from their configuration directories. Everything it
// reads was written by other programs: files are opened without following FIFOs or devices, bounded in size, and
// only names are taken from them. MCP environment variables, headers, URLs and tokens are never read into an item.
package configfiles

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/platform/files"
)

const (
	maxConfigBytes = 8 << 20  // a JSON or TOML configuration (the largest seen: 0.3 MiB)
	maxHeadBytes   = 64 << 10 // the head of a skill or agent file, which is where its name is
	maxEntries     = 2000     // entries read from one directory
)

var (
	errTooLarge   = errors.New("too large")
	errTooMany    = errors.New("too many entries")
	errNotADir    = errors.New("not a directory")
	errMalformed  = errors.New("malformed")
	errOutsideDir = errors.New("outside its directory")
)

// readLimited reads a whole regular file of at most limit bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := files.OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errTooLarge
	}
	return b, nil
}

// readHead reads at most limit bytes from the start of a regular file.
func readHead(path string, limit int64) ([]byte, error) {
	f, err := files.OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readJSON decodes a configuration file into v.
func readJSON(path string, v any) error {
	b, err := readLimited(path, maxConfigBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errMalformed
	}
	return nil
}

// listDir lists a directory, following a symbolic link to it, in name order. A directory holding more than
// maxEntries entries is read up to that count and reported with errTooMany.
func listDir(dir string) ([]os.DirEntry, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errNotADir
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(maxEntries + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	if len(entries) > maxEntries {
		return entries[:maxEntries], errTooMany
	}
	return entries, nil
}

// within reports whether path lies inside dir once both are cleaned (symbolic links are not resolved).
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// reason says why a configuration was skipped, in words that hold no configuration value.
func reason(err error) string {
	switch {
	case errors.Is(err, files.ErrNotRegular):
		return "not a regular file"
	case errors.Is(err, errTooLarge):
		return "too large"
	case errors.Is(err, errTooMany):
		return "too many entries, the first " + "2000" + " read"
	case errors.Is(err, errMalformed):
		return "malformed"
	case errors.Is(err, errNotADir):
		return "not a directory"
	case errors.Is(err, errOutsideDir):
		return "points outside its directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	return "unreadable"
}
