// Package files opens files that other programs wrote, for adapters that read untrusted input.
package files

import (
	"errors"
	"fmt"
	"os"
)

// ErrNotRegular reports a path that is not a regular file (a FIFO, a device, a directory…).
var ErrNotRegular = errors.New("not a regular file")

// OpenRegular opens path for reading and refuses anything but a regular file. The check runs on the
// opened descriptor, and the open does not block, so a FIFO or a device is refused instead of hanging.
func OpenRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, openFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = ErrNotRegular
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
