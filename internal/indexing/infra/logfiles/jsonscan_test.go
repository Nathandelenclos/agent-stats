package logfiles

import (
	"encoding/json"
	"strings"
	"testing"
)

// scanValid is the scanner's verdict on a whole text, to compare with json.Valid.
func scanValid(text string) bool {
	s := scanner{b: []byte(text)}
	s.ws()
	return s.value() && s.done()
}

var scannerTexts = []string{
	``, ` `, `{}`, `[]`, `null`, `true`, `false`, `0`, `-0`, `-`, `01`, `1.`, `.5`, `1e`, `1e+`, `1E-3`, `-1.5e10`, `0.0`,
	`"a"`, `"`, `"a`, "\"a\nb\"", "\"a\tb\"", `"\n"`, `"\q"`, `"é"`, `"\u00g9"`, `"\u00e"`, `"\u12"`, `"é"`, "\"\xff\"", `"\ud800"`,
	`{"a":1}`, `{"a":1,}`, `{"a" 1}`, `{a:1}`, `{"a":1 "b":2}`, `{"a":{"b":[1,2,{"c":null}]}}`, ` { "a" : [ 1 , 2 ] } `,
	`[1,]`, `[,1]`, `[1 2]`, `[[[[]]]]`, `{"a":1}x`, `{"a":1} {}`, `tru`, `nul`, `truex`, `[true,false,null]`,
	`{"k":"` + strings.Repeat("x", 100) + `\n` + strings.Repeat("é", 20) + `"}`, `"` + strings.Repeat("a", 7) + `"`, `"` + strings.Repeat("a", 8) + `"`,
	`"` + strings.Repeat("a", 8) + `\"`, "\"" + strings.Repeat("a", 9) + "\n\"", `"\\\\\\\\\\\\\\\\"`, `"a\`, `"a\u`,
	strings.Repeat("[", 400) + strings.Repeat("]", 400), "\xef\xbb\xbf{}", "{\"a\":1}\x00",
}

func TestScannerValidatesLikeEncodingJSON(t *testing.T) {
	for _, text := range scannerTexts {
		if got, want := scanValid(text), json.Valid([]byte(text)); got != want {
			t.Errorf("scanner says %v, encoding/json says %v for %q", got, want, text)
		}
	}
}

func FuzzScannerValidatesLikeEncodingJSON(f *testing.F) {
	for _, text := range scannerTexts {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := scanValid(text), json.Valid([]byte(text)); got != want {
			t.Errorf("scanner says %v, encoding/json says %v for %q", got, want, text)
		}
	})
}

func TestScannerStringsAreDecodedLikeEncodingJSON(t *testing.T) {
	for _, raw := range []string{`"plain"`, `"with \"escapes\" and é"`, `"é accents"`, "\"bad \xff utf8\"", `"😀 pair"`, `"\ud800 lone"`, `""`} {
		var want string
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		s := scanner{b: []byte(raw)}
		if got, ok := s.text(); !ok || got != want {
			t.Errorf("text(%s) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}

func TestScannerMeasuresValuesLikeTheSizeType(t *testing.T) {
	for _, raw := range []string{`"abc"`, `[1, 2,{"a": null}]`, `{"a":"b"}`, `null`, `12.5e3`, `true`} {
		var want size
		if err := json.Unmarshal([]byte(" "+raw+" "), &want); err != nil {
			t.Fatal(err)
		}
		s := scanner{b: []byte(" " + raw + " ")}
		s.ws()
		if got, ok := s.size(); !ok || got != int(want) {
			t.Errorf("size(%s) = %d, %v; want %d", raw, got, ok, want)
		}
	}
}

var stringContents = []string{``, `abc`, `é`, `a\nb`, `\"\\\/\b\f\n\r\t`, `éA`, `😀`, `\ud800`, `\ud800x`, `\ud800A`, `\udc00\ud800`, `😀\ud83d`, "bad \xff \xc3 \xe2\x82 bytes", "\xf0\x9f\x98\x80 emoji", `\u0000`}

func checkDecodedLen(t *testing.T, content string) {
	t.Helper()
	var want string
	if json.Unmarshal([]byte(`"`+content+`"`), &want) != nil {
		return // not a string encoding/json accepts: the scanner never measures it
	}
	if got := decodedLen([]byte(content)); got != len(want) {
		t.Errorf("decodedLen(%q) = %d, want %d", content, got, len(want))
	}
}

func TestDecodedLenMatchesEncodingJSON(t *testing.T) {
	for _, c := range stringContents {
		checkDecodedLen(t, c)
	}
}

func FuzzDecodedLenMatchesEncodingJSON(f *testing.F) {
	for _, c := range stringContents {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, content string) { checkDecodedLen(t, content) })
}
