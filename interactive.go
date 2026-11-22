package main

import (
	"os"

	"github.com/mattn/go-isatty"
)

// interactive reports whether to open the interface: only when the user did not ask for JSON
// and both ends of the session are a terminal, so pipes and redirections keep the plain report.
func interactive(asJSON bool, in, out *os.File) bool {
	return !asJSON && isatty.IsTerminal(in.Fd()) && isatty.IsTerminal(out.Fd())
}
