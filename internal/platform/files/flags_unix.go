//go:build unix

package files

import (
	"os"
	"syscall"
)

// O_NONBLOCK makes opening a FIFO return at once; reads on a regular file are unaffected.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
