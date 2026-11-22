package logfiles

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// dumpCorpus parses every hand-written line of h on its own, after h's prefix, and writes what the parser
// made of it (the transcript with its timestamps in UTC, or the error), then one digest of what it makes of
// every generated line. testdata/legacy.golden holds what the parsers of commit 80bf4b1 wrote.
func dumpCorpus(parse Parser, h corpusHarness) string {
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC
	one := func(line string) string {
		tr, err := parse(h.path, strings.NewReader(h.prefix+line+"\n"))
		return fmt.Sprintf("%+v\nerr=%v\n", tr, err)
	}
	var out strings.Builder
	for i, seed := range h.seeds {
		fmt.Fprintf(&out, "== %s seed %d\n%s", h.name, i, one(seed))
	}
	sum := sha256.New()
	lines := h.generated()
	for _, l := range lines {
		sum.Write([]byte(one(l)))
	}
	fmt.Fprintf(&out, "== %s generated %d lines\n%x\n", h.name, len(lines), sum.Sum(nil))
	return out.String()
}
