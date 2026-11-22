// Package webui holds the built front of the web interface (web/dist) for the binary to embed.
//
// dist/ is not versioned. Build it with `go generate ./web` (npm ci, then npm run build), then compile with the web
// tag: `go build -tags web .`. Without the tag, the binary embeds a one-page notice that says how to build the front,
// so a plain `go build` or `go install` works on a checkout that never ran npm. Releases are built with the tag.
package webui

//go:generate npm ci
//go:generate npm run build
