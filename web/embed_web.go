//go:build web

package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Embedded reports whether the binary carries the built front.
const Embedded = true

// Assets is the built front, with index.html at its root.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil { // dist is embedded above: it cannot be missing
		panic(err)
	}
	return sub
}
