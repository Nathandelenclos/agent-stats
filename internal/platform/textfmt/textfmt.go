// Package textfmt formats numbers and untrusted text for terminal output.
package textfmt

import (
	"strconv"
	"strings"
	"unicode"
)

// Number formats n with a space between thousands: 1234567 reads "1 234 567".
func Number(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

// Shorten keeps the last n runes of s, led by an ellipsis when it cut something.
func Shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n+1:])
	}
	return s
}

// Printable replaces control characters (escape sequences, tabs, newlines) in text read from session logs,
// so a log cannot drive the terminal or break a table.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}
