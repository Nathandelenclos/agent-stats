package main

import (
	"strings"
	"testing"
)

// The validator is an allowlist: a summary is paragraphs and "- " bullets of plain text with limited emphasis. Whatever
// else Markdown can build (quotes, headings in a quote, lists in a quote, setext headings, HTML, links, autolinks) is
// rejected, wherever it sits in the text.
func TestValidateAcceptsOnlyParagraphsAndBullets(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"heading nested in a quote", "Fixed.\n\n> ## Update now"},
		{"quote", "> Fixed things."},
		{"quote nested in a quote", ">> Fixed things."},
		{"bullet in a quote", "> - Fixed things."},
		{"ordered list in a quote", "> 1. Fixed things."},
		{"heading", "Fixed.\n\n## Update now"},
		{"setext heading by equals", "Big news\n==="},
		{"setext heading by dashes", "Big news\n---"},
		{"thematic break", "Fixed.\n\n***"},
		{"thematic break by underscores", "Fixed.\n\n___"},
		{"star bullet", "* Fixed things."},
		{"plus bullet", "+ Fixed things."},
		{"ordered list", "1. Fixed things."},
		{"ordered list with a parenthesis", "1) Fixed things."},
		{"nested bullet", "- Fixed.\n  - Nested."},
		{"indented bullet", " - Fixed things."},
		{"indented paragraph", "  Fixed things."},
		{"html block", "<div>Fixed things.</div>"},
		{"html comment", "Fixed things.<!-- x -->"},
		{"autolink", "See <https://github.com/o/r> now."},
		{"bare url to a foreign host", "Install from https://evil.example/x now."},
		{"bare url to this repository", "See https://github.com/Nathandelenclos/agent-stats/issues/12."},
		{"www address", "Visit www.evil.example today."},
		{"protocol-relative url", "Install from //evil.example/x now."},
		{"markdown link", "See [the guide](https://github.com/Nathandelenclos/agent-stats)."},
		{"reference-style link", "See [the guide][a].\n\n[a]: /x"},
		{"image", "![x](a.png)"},
		{"issue reference", "Fixes #7."},
		{"foreign issue reference", "Fixed in evil/repo#1."},
		{"mention", "Thanks @octocat."},
		{"entity", "Fixed &#64;octocat."},
		{"ampersand", "Fixed A & B."},
		{"inline code", "Run `x` now."},
		{"code fence", "```\nx\n```"},
		{"tilde fence", "~~~\nx\n~~~"},
		{"strikethrough", "Fixed ~~things~~."},
		{"table", "a | b\n- | -"},
		{"backslash escape", "Fixed \\<b>."},
		{"workflow command", "Fixed.\n::error::x"},
		{"tab", "Fixed.\tThings."},
		{"carriage return alone", "Fixed.\rThings."},
		{"bidirectional override", "Safe ‮evil"},
		{"zero width space", "Safe​text"},
		{"too long", strings.Repeat("a", maxSummaryRunes+1)},
		{"empty", " \n "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := validate(tc.text); err == nil {
				t.Errorf("%q accepted as %q", tc.text, got)
			}
		})
	}
}

func TestValidateKeepsPlainParagraphsBulletsAndEmphasis(t *testing.T) {
	for _, text := range []string{
		"Reports now break down by day.",
		"Reports now break down by day.\n\n- Cached tokens have a cost.\n- Skipped rollouts no longer stop the index.",
		"**Faster** indexing, with _fewer_ surprises and *clear* costs.",
		"- **Reports**: break down by day (1.5x faster).\n- 2024 was long: it takes 10% less time, e.g. 3+4=7.",
		"It’s “faster” – and cheaper — now…",
		"First paragraph.\nSame paragraph.\n\nSecond paragraph.",
	} {
		if got, err := validate(text); err != nil || got != text {
			t.Errorf("%q: got %q, %v", text, got, err)
		}
	}
}
