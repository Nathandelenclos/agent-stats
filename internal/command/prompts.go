package command

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	reportcli "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/cli"
)

// The prompts subcommand reviews the prompts that show frustration signals: where they concentrate and which asks
// keep coming back, so they can be written down once in the agent's instructions (AGENTS.md, CLAUDE.md).

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
	return reportcli.WritePromptReview(out, r, cov.label(), asJSON)
}
