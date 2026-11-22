package logfiles

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// A scanner reads the one JSON line a log parser is decoding, much faster than encoding/json: that package
// walks every byte of a line three or four times (validate, decode, skip what no field wants, decode a
// raw message again), and most of a log is tool output nobody reads past its length.
//
// A scanner never says why it stopped. Each method returns false when the text is not what the parser
// expects, and the parser then decodes the whole line again with encoding/json from a clean struct, which has
// the last word on what a line means, malformed ones included. The fast path therefore may refuse any line
// but must never accept one encoding/json would read differently: it refuses duplicate keys, keys that only
// match a field by letter case, null, and every value of a type other than the field's.
type scanner struct {
	b []byte
	i int
}

// maxScanDepth is how deep values may nest before the scanner gives up; encoding/json's own limit is 10000.
const maxScanDepth = 500

const (
	ones  = 0x0101010101010101
	highs = 0x8080808080808080
)

// hasZeroByte reports whether any byte of w is zero (exact, though not byte-precise about which).
func hasZeroByte(w uint64) bool { return (w-ones)&^w&highs != 0 }

// ws skips JSON whitespace.
func (s *scanner) ws() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\n', '\t', '\r':
			s.i++
		default:
			return
		}
	}
}

// done reports whether only whitespace is left.
func (s *scanner) done() bool {
	s.ws()
	return s.i == len(s.b)
}

// lit consumes the literal word at the cursor.
func (s *scanner) lit(word string) bool {
	if len(s.b)-s.i >= len(word) && string(s.b[s.i:s.i+len(word)]) == word {
		s.i += len(word)
		return true
	}
	return false
}

// plainString consumes the string at the cursor and returns its content as written. plain is true when that
// content needs no decoding: no escape and nothing but ASCII.
func (s *scanner) plainString() (raw []byte, plain, ok bool) {
	b, i := s.b, s.i
	if i >= len(b) || b[i] != '"' {
		return nil, false, false
	}
	i++
	start := i
	var bits uint64 // the bits seen in the content: bit 7 of a byte tells it is not ASCII
	escaped := false
	for i < len(b) {
		for i+8 <= len(b) { // eight bytes at a time while none is a quote, a backslash or a control character
			w := binary.LittleEndian.Uint64(b[i:])
			if hasZeroByte(w^(ones*'"')) || hasZeroByte(w^(ones*'\\')) || (w-ones*0x20)&^w&highs != 0 {
				break
			}
			bits |= w
			i += 8
		}
		if i >= len(b) {
			break
		}
		switch c := b[i]; {
		case c == '"':
			s.i = i + 1
			return b[start:i], !escaped && bits&highs == 0, true
		case c < 0x20:
			return nil, false, false
		case c == '\\':
			escaped = true
			if i+1 >= len(b) {
				return nil, false, false
			}
			switch b[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+5 >= len(b) || !isHex(b[i+2]) || !isHex(b[i+3]) || !isHex(b[i+4]) || !isHex(b[i+5]) {
					return nil, false, false
				}
				i += 6
			default:
				return nil, false, false
			}
		default:
			bits |= uint64(c)
			i++
		}
	}
	return nil, false, false
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// number consumes a JSON number at the cursor and returns its text.
func (s *scanner) number() ([]byte, bool) {
	b, i := s.b, s.i
	start := i
	if i < len(b) && b[i] == '-' {
		i++
	}
	if i >= len(b) {
		return nil, false
	}
	switch {
	case b[i] == '0':
		i++
	case b[i] >= '1' && b[i] <= '9':
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return nil, false
	}
	if i < len(b) && b[i] == '.' {
		i++
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			return nil, false
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			return nil, false
		}
	}
	s.i = i
	return b[start:i], true
}

// value consumes and validates the value at the cursor, whatever its type.
func (s *scanner) value() bool { return s.skip(0) }

func (s *scanner) skip(depth int) bool {
	if depth > maxScanDepth || s.i >= len(s.b) {
		return false
	}
	switch c := s.b[s.i]; {
	case c == '"':
		_, _, ok := s.plainString()
		return ok
	case c == '{':
		return s.members(depth, false, func([]byte) bool { return s.skip(depth + 1) })
	case c == '[':
		return s.elements(depth, func() bool { return s.skip(depth + 1) })
	case c == 't':
		return s.lit("true")
	case c == 'f':
		return s.lit("false")
	case c == 'n':
		return s.lit("null")
	case c == '-' || c >= '0' && c <= '9':
		_, ok := s.number()
		return ok
	}
	return false
}

// object reads the object at the cursor, calling member for each key; member must consume that key's value.
// A key with an escape or a non-ASCII byte is refused: it could still name a field once decoded.
func (s *scanner) object(member func(key []byte) bool) bool { return s.members(0, true, member) }

// members is object, with the refusal of such keys optional: the keys of values that are skipped are not read.
func (s *scanner) members(depth int, plainKeys bool, member func(key []byte) bool) bool {
	if depth > maxScanDepth || s.i >= len(s.b) || s.b[s.i] != '{' {
		return false
	}
	s.i++
	s.ws()
	if s.i < len(s.b) && s.b[s.i] == '}' {
		s.i++
		return true
	}
	for {
		key, plain, ok := s.plainString()
		if !ok || (plainKeys && !plain) {
			return false
		}
		s.ws()
		if s.i >= len(s.b) || s.b[s.i] != ':' {
			return false
		}
		s.i++
		s.ws()
		if !member(key) {
			return false
		}
		s.ws()
		if s.i >= len(s.b) {
			return false
		}
		switch s.b[s.i] {
		case '}':
			s.i++
			return true
		case ',':
			s.i++
			s.ws()
		default:
			return false
		}
	}
}

// array reads the array at the cursor, calling element for each one; element must consume it.
func (s *scanner) array(element func() bool) bool { return s.elements(0, element) }

func (s *scanner) elements(depth int, element func() bool) bool {
	if depth > maxScanDepth || s.i >= len(s.b) || s.b[s.i] != '[' {
		return false
	}
	s.i++
	s.ws()
	if s.i < len(s.b) && s.b[s.i] == ']' {
		s.i++
		return true
	}
	for {
		if !element() {
			return false
		}
		s.ws()
		if s.i >= len(s.b) {
			return false
		}
		switch s.b[s.i] {
		case ']':
			s.i++
			return true
		case ',':
			s.i++
			s.ws()
		default:
			return false
		}
	}
}

// size consumes any value and returns how many bytes it spans, as the size type measures it.
func (s *scanner) size() (int, bool) {
	start := s.i
	if !s.value() {
		return 0, false
	}
	return s.i - start, true
}

// text consumes a string and returns it decoded. Plain strings are copied as they are; others go through
// encoding/json, which owns the rules for escapes and invalid UTF-8.
func (s *scanner) text() (string, bool) {
	start := s.i
	raw, plain, ok := s.plainString()
	if !ok {
		return "", false
	}
	if plain {
		return string(raw), true
	}
	var out string
	if json.Unmarshal(s.b[start:s.i], &out) != nil {
		return "", false
	}
	return out, true
}

// int64 consumes an integer. A number with a fraction or exponent is left to encoding/json, which rejects it.
func (s *scanner) int64() (int64, bool) {
	raw, ok := s.number()
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil
}

func (s *scanner) float64() (float64, bool) {
	raw, ok := s.number()
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	return f, err == nil
}

func (s *scanner) bool() (bool, bool) {
	if s.lit("true") {
		return true, true
	}
	return false, s.lit("false")
}

// seen tells which fields of an object were read, so that a repeated key is noticed.
type seen uint64

// first marks field n and reports whether it was not marked yet.
func (m *seen) first(n uint) bool {
	if *m&(1<<n) != 0 {
		return false
	}
	*m |= 1 << n
	return true
}

// foldsToField reports whether key names one of fields apart from letter case. encoding/json fills a field
// from such a key, the scanner does not, so the line must be decoded by encoding/json instead.
func foldsToField(key []byte, fields [][]byte) bool {
	for _, f := range fields {
		if bytes.EqualFold(key, f) {
			return true
		}
	}
	return false
}

func fieldNames(names ...string) [][]byte {
	out := make([][]byte, len(names))
	for i, n := range names {
		out[i] = []byte(n)
	}
	return out
}

// scanRefused counts the lines the scanner handed back to encoding/json; tests read it to see how often that happens.
var scanRefused atomic.Int64

// fastDecode switches the scanner off, so that tests can compare what it reads with what encoding/json reads.
var fastDecode = true

// decodeLine decodes one log line into a T: with scan when it accepts the line, otherwise, and whenever
// fast decoding is off, with encoding/json from an empty T. ok is false for a line encoding/json refuses.
func decodeLine[T any](line []byte, scan func([]byte, *T) bool) (v T, ok bool) {
	if fastDecode {
		if scan(line, &v) {
			return v, true
		}
		scanRefused.Add(1)
	}
	var empty T
	v = empty
	return v, json.Unmarshal(line, &v) == nil
}

// setText, setInt64... read the value at the cursor into *dst.
func (s *scanner) setText(dst *string) (ok bool)     { *dst, ok = s.text(); return }
func (s *scanner) setInt64(dst *int64) (ok bool)     { *dst, ok = s.int64(); return }
func (s *scanner) setFloat64(dst *float64) (ok bool) { *dst, ok = s.float64(); return }
func (s *scanner) setBool(dst *bool) (ok bool)       { *dst, ok = s.bool(); return }
func (s *scanner) setSize(dst *int) (ok bool)        { *dst, ok = s.size(); return }
func (s *scanner) setTextLen(dst *textLen) (ok bool) {
	n, ok := s.textLen()
	*dst = textLen(n)
	return ok
}

// textLen consumes a string and returns the length of the text it decodes to, without building it.
func (s *scanner) textLen() (int, bool) {
	raw, plain, ok := s.plainString()
	if !ok {
		return 0, false
	}
	if plain {
		return len(raw), true
	}
	return decodedLen(raw), true
}

// decodedLen is the length of the text a JSON string with this content decodes to, by encoding/json's rules:
// an escape is one byte (a surrogate pair four, a lone surrogate three), and so is an invalid UTF-8 byte.
func decodedLen(raw []byte) int {
	n := 0
	for i := 0; i < len(raw); {
		switch c := raw[i]; {
		case c == '\\':
			if raw[i+1] != 'u' {
				i, n = i+2, n+1
				break
			}
			r := hex4(raw[i+2:])
			i += 6
			if utf16.IsSurrogate(r) {
				if i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					if pair := utf16.DecodeRune(r, hex4(raw[i+2:])); pair != unicode.ReplacementChar {
						i += 6
						n += 4
						break
					}
				}
				r = unicode.ReplacementChar
			}
			n += utf8.RuneLen(r)
		case c < utf8.RuneSelf:
			i, n = i+1, n+1
		default:
			r, size := utf8.DecodeRune(raw[i:])
			i, n = i+size, n+utf8.RuneLen(r)
		}
	}
	return n
}

// hex4 reads the four hexadecimal digits of a \u escape the scanner has validated.
func hex4(b []byte) rune {
	var r rune
	for _, c := range b[:4] {
		switch {
		case c <= '9':
			c -= '0'
		case c >= 'a':
			c = c - 'a' + 10
		default:
			c = c - 'A' + 10
		}
		r = r<<4 | rune(c)
	}
	return r
}
