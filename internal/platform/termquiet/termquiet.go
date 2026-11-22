// Package termquiet stops the colour libraries linked into the program (Bubble Tea, through Lip Gloss and
// termenv) from querying the terminal when they load.
//
// Bubble Tea's init asks the terminal for its background colour whenever stdout is a terminal, and waits
// up to five seconds for the answer. A terminal that does not answer would stall every run, --json included,
// and the query leaks to the screen. termenv skips the query when CI is set, so this package sets it while
// the libraries initialise, and the program calls Restore as soon as it starts.
//
// ponytail: relies on Go initialising this package, which imports nothing, before the libraries (the order
// of the language specification: by import path among the packages ready to run); main_test checks it against
// the built program. Upgrade path: Bubble Tea v2 drops that init.
package termquiet

import "os"

var set bool // whether this package, and not the caller's environment, set CI

func init() {
	if os.Getenv("CI") == "" {
		set = os.Setenv("CI", "1") == nil
	}
}

// Restore removes the CI variable this package set, so that the interface detects the terminal's colours.
func Restore() {
	if set {
		os.Unsetenv("CI")
		set = false
	}
}
