package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
)

func printable(s string) string { return textfmt.Printable(s) }

func onlyUnused(entries []catalog.Entry) []catalog.Entry {
	var out []catalog.Entry
	for _, e := range entries {
		if e.Unused() {
			out = append(out, e)
		}
	}
	return out
}

// jsonEntry is the --json form of a catalogue entry. Uses is null when the logs of the harness do not tell the item
// apart; lastTs is in Unix milliseconds, 0 when unknown.
type jsonEntry struct {
	Harness   string `json:"harness"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Origin    string `json:"origin"`
	Source    string `json:"source"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Uses      *int64 `json:"uses"`
	LastTS    int64  `json:"lastTs"`
}

type jsonCatalog struct {
	Entries []jsonEntry `json:"entries"`
	Skipped []string    `json:"skipped"`
}

func toCatalogJSON(c catalog.Catalog) jsonCatalog {
	out := jsonCatalog{Entries: []jsonEntry{}, Skipped: []string{}}
	out.Skipped = append(out.Skipped, c.Skipped...)
	for _, e := range c.Entries {
		je := jsonEntry{Harness: e.Harness.String(), Kind: e.Kind.String(), Name: e.Name.String(), Scope: e.Scope.String(),
			Origin: e.Origin.String(), Source: e.Source, Installed: e.Installed(), Enabled: !e.Disabled, LastTS: kernel.UnixMilli(e.Last)}
		if e.Tracked {
			n := e.Uses
			je.Uses = &n
		}
		out.Entries = append(out.Entries, je)
	}
	return out
}

func writeCatalogJSON(w io.Writer, c catalog.Catalog) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(toCatalogJSON(c))
}

// printCatalog writes one table per harness: kind, item, scope, uses and last use. Names come from configuration
// files and logs, so they are made printable.
func printCatalog(w io.Writer, c catalog.Catalog, onlyUnused bool) {
	if len(c.Entries) == 0 {
		if onlyUnused {
			fmt.Fprintln(w, "Nothing installed goes unused in this range.")
		} else {
			fmt.Fprintln(w, "Nothing installed or used in this range.")
		}
		return
	}
	for _, h := range kernel.Harnesses {
		var rows []catalog.Entry
		installed, unused := 0, 0
		for _, e := range c.Entries {
			if e.Harness != h {
				continue
			}
			rows = append(rows, e)
			if e.Installed() {
				installed++
			}
			if e.Unused() {
				unused++
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s · %d installed, %d never used\n", h, installed, unused)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tITEM\tSCOPE\tUSES\tLAST USED")
		for _, e := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Kind, textfmt.Shorten(printable(itemLabel(e)), 50), scopeLabel(e), usesLabel(e), lastLabel(e))
		}
		tw.Flush()
	}
}

func itemLabel(e catalog.Entry) string {
	if e.Disabled {
		return e.Name.String() + " (off)"
	}
	return e.Name.String()
}

func scopeLabel(e catalog.Entry) string {
	if p := e.Plugin(); p != "" {
		return e.Scope.String() + ":" + printable(p)
	}
	return e.Scope.String()
}

// usesLabel shows "?" where the logs cannot tell the item apart, so it is not mistaken for unused.
func usesLabel(e catalog.Entry) string {
	if !e.Tracked {
		return "?"
	}
	return textfmt.Number(e.Uses)
}

func lastLabel(e catalog.Entry) string {
	switch {
	case !e.Tracked:
		return "?"
	case e.Last.IsZero():
		return "never"
	}
	return day(e.Last)
}
