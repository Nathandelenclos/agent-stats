package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// The prompts subcommand reviews the prompts that show frustration signals: where they concentrate and which asks
// keep coming back, so they can be written down once in the agent's instructions (AGENTS.md, CLAUDE.md).

// What the plain-text review lists at most; --json lists everything.
const (
	maxPrompts = 20
	maxAsks    = 10
	maxFolders = 10
	// excerptRunes is how much of a prompt, session title or folder the review shows, as one neutralised line.
	excerptRunes = 120
)

type jsonSignals struct {
	Yelling   int  `json:"yelling"`
	Profanity int  `json:"profanity"`
	Anguish   int  `json:"anguish"`
	Negation  int  `json:"negation"`
	Blame     int  `json:"blame"`
	Repeated  bool `json:"repeated"`
}

type jsonFlaggedPrompt struct {
	TS      int64       `json:"ts"`
	Harness string      `json:"harness"`
	Session string      `json:"session"`
	Title   string      `json:"title"`
	Folder  string      `json:"folder"`
	Signals jsonSignals `json:"signals"`
	Excerpt string      `json:"excerpt"`
}

type jsonRecurringAsk struct {
	Excerpt  string `json:"excerpt"`
	Count    int    `json:"count"`
	Sessions int    `json:"sessions"`
	FirstTS  int64  `json:"firstTs"`
	LastTS   int64  `json:"lastTs"`
}

type jsonFolderFrustration struct {
	Folder   string `json:"folder"`
	Prompts  int    `json:"prompts"`
	Sessions int    `json:"sessions"`
	Signals  int    `json:"signals"`
}

// jsonPromptReview is the --json form of a prompt review: timestamps in Unix milliseconds, 0 when unknown, and excerpts
// neutralised as in the plain-text review. Empty lists print as [], never null.
type jsonPromptReview struct {
	Prompts   []jsonFlaggedPrompt     `json:"prompts"`
	Sessions  int                     `json:"sessions"`
	Recurring []jsonRecurringAsk      `json:"recurring"`
	ByFolder  []jsonFolderFrustration `json:"byFolder"`
}

func promptReviewJSON(r reporting.PromptReview) jsonPromptReview {
	out := jsonPromptReview{Sessions: r.Sessions, Prompts: []jsonFlaggedPrompt{}, Recurring: []jsonRecurringAsk{}, ByFolder: []jsonFolderFrustration{}}
	for _, p := range r.Prompts {
		s := p.Signals
		out.Prompts = append(out.Prompts, jsonFlaggedPrompt{kernel.UnixMilli(p.At), p.Harness.String(), p.Session.String(),
			textfmt.Excerpt(p.Title, excerptRunes), p.Folder,
			jsonSignals{s.Yelling, s.Profanity, s.Anguish, s.Negation, s.Blame, s.Repeated}, textfmt.Excerpt(p.Text, excerptRunes)})
	}
	for _, a := range r.Recurring {
		out.Recurring = append(out.Recurring, jsonRecurringAsk{textfmt.Excerpt(a.Text, excerptRunes), a.Count, a.Sessions, kernel.UnixMilli(a.First), kernel.UnixMilli(a.Last)})
	}
	for _, f := range r.Folders {
		out.ByFolder = append(out.ByFolder, jsonFolderFrustration{f.Folder, f.Prompts, f.Sessions, f.Signals})
	}
	return out
}

// printPromptReview writes the review as text: the asks that came back, the folders, then the flagged prompts.
func printPromptReview(w io.Writer, r reporting.PromptReview) {
	if len(r.Prompts) == 0 {
		fmt.Fprintln(w, "No flagged prompts in this range.")
		return
	}
	fmt.Fprintf(w, "%s in %s\n", textfmt.Plural(len(r.Prompts), "flagged prompt", "flagged prompts"), textfmt.Plural(r.Sessions, "session", "sessions"))

	if len(r.Recurring) > 0 {
		fmt.Fprintln(w, "\nRECURRING")
		for i, a := range r.Recurring {
			if i == maxAsks {
				fmt.Fprintf(w, "… %d more\n", len(r.Recurring)-maxAsks)
				break
			}
			fmt.Fprintf(w, "  %s\n    %s\n", textfmt.Excerpt(a.Text, excerptRunes), a.Hint())
		}
	}

	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "FOLDER\tPROMPTS\tSESSIONS\tSIGNALS\t")
	for i, f := range r.Folders {
		if i == maxFolders {
			fmt.Fprintf(tw, "… %d more\t\t\t\t\n", len(r.Folders)-maxFolders)
			break
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t\n", textfmt.Shorten(textfmt.Printable(orDash(f.Folder)), 50),
			textfmt.Number(int64(f.Prompts)), textfmt.Number(int64(f.Sessions)), textfmt.Number(int64(f.Signals)))
	}
	tw.Flush()

	fmt.Fprintln(w, "\nPROMPTS")
	for i, p := range r.Prompts {
		if i == maxPrompts {
			fmt.Fprintf(w, "… %d more (--json lists them all)\n", len(r.Prompts)-maxPrompts)
			break
		}
		fmt.Fprintf(w, "%s  %s  %s  %s · %s\n    %s\n", when(p.At), p.Harness, strings.Join(p.Signals.Tags(), ", "),
			textfmt.Excerpt(orTitle(p.Title), 60), textfmt.Excerpt(orDash(p.Folder), 60), textfmt.Excerpt(p.Text, excerptRunes))
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func orTitle(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(untitled)"
	}
	return s
}

// when formats a prompt's time, a dash for an unknown one.
func when(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}

// promptsCommand is the name of the subcommand that reviews the flagged prompts.
const promptsCommand = "prompts"

// defaultPromptsSince is the period the review covers when --since is not given.
const defaultPromptsSince = "7d"

// subcommand reads the subcommand the command line names, "" for none. Flags may follow its name
// (agent-stats prompts --since 7d) as well as precede it: the flag package stops at the first word that is not a flag.
func subcommand(fs *flag.FlagSet) (string, error) {
	if fs.Arg(0) != promptsCommand {
		return "", nil
	}
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q after %s", fs.Arg(0), promptsCommand)
	}
	return promptsCommand, nil
}

// sinceOf is the start of the period --since names. The review covers the last week unless asked otherwise, and
// "all" asks for every prompt; the default command keeps parseSince's own rules.
func sinceOf(sub, since string, now time.Time) (time.Time, error) {
	if sub != promptsCommand {
		return parseSince(since, now)
	}
	switch since {
	case "":
		since = defaultPromptsSince
	case "all":
		return time.Time{}, nil
	}
	return parseSince(since, now)
}

// runPrompts reviews the flagged prompts q asks for and writes the review to out as text or JSON.
func runPrompts(out io.Writer, queries reporting.PromptQueries, q reporting.Query, cov coverage, asJSON bool) error {
	q.Project = cov.restriction()
	r, err := reporting.ReviewPrompts{Queries: queries}.Run(context.Background(), q)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(promptReviewJSON(r))
	}
	fmt.Fprintln(out, cov.label())
	printPromptReview(out, r)
	return nil
}
