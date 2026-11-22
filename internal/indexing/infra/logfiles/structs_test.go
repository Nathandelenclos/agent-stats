package logfiles

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestScannerFillsTheStructsEncodingJSONFills compares, line by line, what the scanner puts in a line's struct
// with what encoding/json puts there, down to an empty list being an empty slice and not a nil one.
func TestScannerFillsTheStructsEncodingJSONFills(t *testing.T) {
	odd := map[string][]string{"claude": oddClaudeLines, "codex": oddCodexLines, "pi": oddPiLines}
	extra := []string{`{"payload":{"item":{"content":[]}}}`, `{"message":{"content":[]}}`}
	for _, h := range corpusHarnesses {
		lines := append(append(append(append([]string(nil), extra...), h.seeds...), odd[h.name]...), h.generated()...)
		switch h.name {
		case "claude":
			sameStructs(t, h.name, lines, scanClaudeLine, func(l *claudeLine) {
				if l.Message != nil {
					l.Message.Content.resolve()
				}
			})
		case "codex":
			sameStructs(t, h.name, lines, scanCodexLine, func(*codexLine) {})
		case "pi":
			sameStructs(t, h.name, lines, scanPiLine, func(l *piLine) {
				if l.Message != nil {
					l.Message.Content.blocks()
				}
			})
		}
	}
}

// sameStructs checks every line the scanner accepts: encoding/json must accept it too and fill the same struct.
// finish decodes what the scanner leaves raw.
func sameStructs[T any](t *testing.T, name string, lines []string, scan func([]byte, *T) bool, finish func(*T)) {
	t.Helper()
	defer func(on bool) { fastDecode = on }(fastDecode)
	fastDecode = true
	accepted, failed := 0, 0
	for _, line := range lines {
		var fast T
		if !scan([]byte(line), &fast) {
			continue
		}
		accepted++
		var slow T
		if err := json.Unmarshal([]byte(line), &slow); err != nil {
			t.Errorf("%s: the scanner accepts a line encoding/json refuses: %s", name, line)
			continue
		}
		finish(&fast)
		finish(&slow)
		if !reflect.DeepEqual(fast, slow) {
			if failed++; failed <= 10 {
				t.Errorf("%s: the scanner and encoding/json fill different structs for %s", name, line)
			}
		}
	}
	if accepted == 0 {
		t.Errorf("%s: the scanner accepted no line", name)
	}
}
