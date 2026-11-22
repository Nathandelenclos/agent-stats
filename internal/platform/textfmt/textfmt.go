// Package textfmt formats numbers and untrusted text for terminal output.
package textfmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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

// Head keeps the first n runes of s, followed by an ellipsis when it cut something.
func Head(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:max(0, n-1)]) + "…"
	}
	return s
}

// Duration formats d with its two largest units: 45s, 12m, 1h05m, 2d03h. Under a second it reads 0s.
func Duration(d time.Duration) string {
	d = d.Truncate(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd%02dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
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
