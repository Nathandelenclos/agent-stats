//go:build !web

package webui

import (
	"embed"
	"io/fs"
)

//go:embed placeholder
var placeholder embed.FS

// Embedded reports whether the binary carries the built front.
const Embedded = false

// Assets is a one-page notice that says how to build the front, with index.html at its root.
func Assets() fs.FS {
	sub, err := fs.Sub(placeholder, "placeholder")
	if err != nil { // placeholder is embedded above: it cannot be missing
		panic(err)
	}
	return sub
}
