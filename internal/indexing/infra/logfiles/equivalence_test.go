package logfiles

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// The contract of the scanner and of the decoders that replaced encoding/json's plain structs: for any
// line, the transcript is the one commit 80bf4b1 made, but for the deliberate changes marked in legacy_test.go. These tests decode with the parsers of that commit
// (legacy_test.go), not with the decoders they check, which is what the tests that merely switch the
// scanner off cannot do: both sides of those use the same custom decoders.

func parserOf(harness string, legacy bool) Parser {
	switch {
	case harness == "claude" && legacy:
		return legacyParseClaude
	case harness == "claude":
		return ParseClaude
	case harness == "codex" && legacy:
		return legacyParseCodex
	case harness == "codex":
		return ParseCodex
	case legacy:
		return legacyParsePi(kernel.Pi)
	}
	return ParsePi(kernel.Pi)
}

func parseOne(p Parser, h corpusHarness, line string) (domain.Transcript, error) {
	return p(h.path, strings.NewReader(h.prefix+line+"\n"))
}

// sameAsLegacy reports whether the current parser, with the scanner on and off, makes of the line what the
// legacy one does.
func sameAsLegacy(h corpusHarness, line string) (problem string) {
	want, wantErr := parseOne(parserOf(h.name, true), h, line)
	defer func(on bool) { fastDecode = on }(fastDecode)
	for _, fast := range []bool{true, false} {
		fastDecode = fast
		got, err := parseOne(parserOf(h.name, false), h, line)
		switch {
		case (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()):
			return "errors differ"
		case !reflect.DeepEqual(got, want):
			if fast {
				return "with the scanner: " + firstDifference(got, want)
			}
			return "without the scanner: " + firstDifference(got, want)
		}
	}
	return ""
}

func TestParsersReadLinesAsTheLegacyOnesDid(t *testing.T) {
	odd := map[string][]string{"claude": oddClaudeLines, "codex": oddCodexLines, "pi": oddPiLines}
	for _, h := range corpusHarnesses {
		lines := append(append(append([]string(nil), h.seeds...), odd[h.name]...), h.generated()...)
		failed := 0
		for _, line := range lines {
			if problem := sameAsLegacy(h, line); problem != "" {
				if failed++; failed <= 10 {
					t.Errorf("%s: %s for %s", h.name, problem, line)
				}
			}
		}
		if failed > 10 {
			t.Errorf("%s: %d lines in all differ from what the legacy parser made", h.name, failed)
		}
		t.Logf("%s: %d lines compared", h.name, len(lines))
	}
}

// TestParsersMatchTheFrozenLegacyOutput checks the legacy oracle itself: testdata/legacy.golden was written
// by a build of commit 80bf4b1, then regenerated with the deliberate changes since (all marked in legacy_test.go): the
// Codex session-span fix of d4a12c3 (session end times only), then, from the legacy oracle and not from the current
// parsers, the skill of a Claude Skill call (the new Skill field of every tool call, set on Skill calls only)
// and the MCP server split out of a Codex function call name (Name and Server of those calls only), with corpus
// lines for both. The current parsers must still write the same.
func TestParsersMatchTheFrozenLegacyOutput(t *testing.T) {
	golden, err := os.ReadFile("testdata/legacy.golden")
	if err != nil {
		t.Fatal(err)
	}
	defer func(on bool) { fastDecode = on }(fastDecode)
	for _, fast := range []bool{true, false} {
		fastDecode = fast
		var got strings.Builder
		for _, h := range corpusHarnesses {
			got.WriteString(dumpCorpus(parserOf(h.name, false), h))
		}
		if got.String() != string(golden) {
			t.Errorf("scanner on = %v: the parsers no longer write what commit 80bf4b1 wrote (%s)", fast, firstGoldenDifference(got.String(), string(golden)))
		}
	}
}

func firstGoldenDifference(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	section := ""
	for i := range min(len(gl), len(wl)) {
		if strings.HasPrefix(wl[i], "== ") {
			section = wl[i]
		}
		if gl[i] != wl[i] {
			return "first difference after " + section
		}
	}
	return "lengths differ"
}

func FuzzParsersMatchTheLegacyOnes(f *testing.F) {
	for i, h := range corpusHarnesses {
		for _, line := range h.seeds {
			f.Add(uint8(i), line)
		}
		for j, line := range h.generated() {
			if j%25 == 0 {
				f.Add(uint8(i), line)
			}
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, line string) {
		h := corpusHarnesses[int(which)%len(corpusHarnesses)]
		if problem := sameAsLegacy(h, line); problem != "" {
			t.Errorf("%s: %s for %q", h.name, problem, line)
		}
	})
}

// TestParsersReadRealLogsAsTheLegacyOnesDid compares the transcripts of the logs found under the home
// directory AGENT_STATS_CORPUS names, the way TestScannerReadsRealLogsLikeEncodingJSON does, with the legacy parsers:
//
//	AGENT_STATS_CORPUS=$HOME go test ./internal/indexing/infra/logfiles -run RealLogsAsTheLegacy -v
func TestParsersReadRealLogsAsTheLegacyOnesDid(t *testing.T) {
	home := os.Getenv("AGENT_STATS_CORPUS")
	if home == "" {
		t.Skip("set AGENT_STATS_CORPUS to a home directory to compare on real logs")
	}
	files := 0
	for _, src := range DefaultSources(home) {
		if src.Harness == kernel.Continue {
			continue // no legacy Continue reader
		}
		legacy := legacyParsePi(src.Harness)
		switch src.Harness {
		case kernel.Claude:
			legacy = legacyParseClaude
		case kernel.Codex:
			legacy = legacyParseCodex
		}
		filepath.WalkDir(src.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !src.Match(path) {
				return nil
			}
			files++
			got, gerr := ParseFile(path, src.Parse)
			want, werr := ParseFile(path, legacy)
			if (gerr == nil) != (werr == nil) {
				t.Errorf("%s: errors differ", strings.TrimPrefix(path, home))
			} else if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: the transcript differs from the legacy one: %s", strings.TrimPrefix(path, home), firstDifference(got, want))
			}
			return nil
		})
	}
	t.Logf("%d logs compared", files)
	if files == 0 {
		t.Fatal("no log found under " + home)
	}
}
