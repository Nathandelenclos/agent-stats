//go:build !unix

package files

import "os"

const openFlags = os.O_RDONLY
