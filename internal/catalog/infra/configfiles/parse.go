package configfiles

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// jsonValue keeps a JSON value undecoded: configurations are read for the names of their keys, never their values.
type jsonValue = json.RawMessage

// frontmatterName returns the "name:" of a Markdown file's YAML frontmatter, fallback when there is none.
func frontmatterName(head []byte, fallback string) string {
	text := strings.TrimPrefix(string(head), "\xef\xbb\xbf")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fallback
	}
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			if v = unquote(strings.TrimSpace(v)); v != "" {
				return v
			}
		}
	}
	return fallback
}

// unquote drops a pair of matching quotes around a scalar.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// tomlName returns the string value of a top-level "name" key of a TOML file, fallback when there is none.
func tomlName(head []byte, fallback string) string {
	for _, line := range strings.Split(string(head), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			break // the top level ends at the first table
		}
		if key, val, ok := strings.Cut(t, "="); ok && strings.TrimSpace(key) == "name" {
			if s, ok := tomlString(strings.TrimSpace(val)); ok && s != "" {
				return s
			}
		}
	}
	return fallback
}

// tomlString reads a single-line basic or literal string at the start of v.
func tomlString(v string) (string, bool) {
	switch {
	case strings.HasPrefix(v, `"`):
		end := closingQuote(v)
		if end < 0 {
			return "", false
		}
		s, err := strconv.Unquote(v[:end+1])
		return s, err == nil
	case strings.HasPrefix(v, `'`):
		end := strings.Index(v[1:], `'`)
		if end < 0 {
			return "", false
		}
		return v[1 : 1+end], true
	}
	return "", false
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// tomlTable is a table header of a TOML file, with the value of its "enabled" key when it has a boolean one.
type tomlTable struct {
	Path    []string
	Enabled *bool
}

// tomlTables lists the tables of a TOML file in order. It reads no values but "enabled", and skips the lines inside
// multi-line strings so that text there cannot pass for a header. It does not read dotted keys or inline tables
// that define a table from a parent ([mcp_servers] with name = { … }); those are not seen.
func tomlTables(b []byte) []tomlTable {
	var tables []tomlTable
	cur := -1
	multi := ""
	for _, line := range bytes.Split(b, []byte("\n")) {
		l := string(line)
		if multi != "" {
			if strings.Contains(l, multi) {
				multi = ""
			}
			continue
		}
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "[["):
			cur = -1
		case strings.HasPrefix(t, "["):
			path, ok := tomlHeader(t)
			if !ok {
				cur = -1
				continue
			}
			tables = append(tables, tomlTable{Path: path})
			cur = len(tables) - 1
		default:
			key, val, ok := strings.Cut(t, "=")
			if !ok {
				continue
			}
			if cur >= 0 && strings.TrimSpace(key) == "enabled" {
				v, _, _ := strings.Cut(val, "#")
				switch strings.TrimSpace(v) {
				case "true":
					tables[cur].Enabled = ptr(true)
				case "false":
					tables[cur].Enabled = ptr(false)
				}
			}
			for _, d := range []string{`"""`, `'''`} {
				if strings.Count(val, d)%2 == 1 {
					multi = d
				}
			}
		}
	}
	return tables
}

func ptr[T any](v T) *T { return &v }

// tomlHeader parses "[a.\"b.c\".d]" into its dotted key parts.
func tomlHeader(t string) ([]string, bool) {
	s := strings.TrimSpace(t[1:])
	var parts []string
	for {
		var part string
		switch {
		case strings.HasPrefix(s, `"`):
			end := closingQuote(s)
			if end < 0 {
				return nil, false
			}
			unq, err := strconv.Unquote(s[:end+1])
			if err != nil {
				return nil, false
			}
			part, s = unq, s[end+1:]
		case strings.HasPrefix(s, `'`):
			end := strings.Index(s[1:], `'`)
			if end < 0 {
				return nil, false
			}
			part, s = s[1:1+end], s[end+2:]
		default:
			end := strings.IndexFunc(s, func(r rune) bool {
				return !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
			})
			if end < 0 {
				return nil, false
			}
			if end == 0 {
				return nil, false
			}
			part, s = s[:end], s[end:]
		}
		parts = append(parts, part)
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "."):
			s = strings.TrimSpace(s[1:])
		case strings.HasPrefix(s, "]"):
			rest := strings.TrimSpace(s[1:])
			return parts, rest == "" || strings.HasPrefix(rest, "#")
		default:
			return nil, false
		}
	}
}
