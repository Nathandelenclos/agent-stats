package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main without the process: the environment, the streams and the context are passed in.
func run(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: releasenotes summarize|compose [flags]")
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	notes := fs.String("notes", "", "git-cliff notes of the release")
	out := fs.String("out", "", "file to write")
	summaryPath := fs.String("summary", "", "compose: summary file, may be missing or empty")
	apiURL := fs.String("api-url", defaultAPIURL, "summarize: Responses API endpoint")
	switch args[0] {
	case "summarize", "compose":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *notes == "" || *out == "" {
		fmt.Fprintln(stderr, "-notes and -out are required")
		return 2
	}
	if args[0] == "compose" {
		return runCompose(*notes, *summaryPath, *out, env("GITHUB_REPOSITORY"), stdout, stderr)
	}
	return runSummarize(ctx, *notes, *out, *apiURL, env, stdout, stderr)
}

// runSummarize never fails the release: the summary file is always written, empty when there is nothing to say, and the
// exit code is 0 whatever happened with the API. Only a wrong call (usage, unwritable file) is an error.
func runSummarize(ctx context.Context, notesPath, out, apiURL string, env func(string) string, stdout, stderr io.Writer) int {
	if !safeAPIURL(apiURL) {
		fmt.Fprintln(stderr, "-api-url must be https (or http on localhost)")
		return 2
	}
	summary, err := func() (string, error) {
		notes, err := os.ReadFile(notesPath)
		if err != nil {
			return "", skip("notes unreadable")
		}
		model := env("OPENAI_MODEL")
		if model == "" {
			return "", skip("no model")
		}
		return newSummarizer(apiURL, model, env("OPENAI_API_KEY"), env("GITHUB_REPOSITORY")).summarize(ctx, string(notes))
	}()
	if err != nil {
		var s skip
		if !errors.As(err, &s) {
			s = "unexpected error" // never print an error that did not come from this program
		}
		fmt.Fprintf(stdout, "::notice::AI release summary skipped: %s\n", s)
	} else {
		fmt.Fprintf(stdout, "::notice::AI release summary written (%d characters)\n", len(summary))
	}
	if err := os.WriteFile(out, []byte(summary), 0o644); err != nil {
		fmt.Fprintln(stderr, "cannot write the summary file")
		return 1
	}
	return 0
}

// runCompose fails only when the git-cliff notes cannot be read: a release must carry its notes. A summary that is
// missing or does not pass validation again is left out, as if it had not been written.
func runCompose(notesPath, summaryPath, out, repo string, stdout, stderr io.Writer) int {
	notes, err := os.ReadFile(notesPath)
	if err != nil {
		fmt.Fprintln(stderr, "cannot read the release notes")
		return 1
	}
	var summary string
	if raw, err := os.ReadFile(summaryPath); summaryPath != "" && err == nil && strings.TrimSpace(string(raw)) != "" {
		// The file crossed a job boundary: the publishing job does not take it on trust.
		if summary, err = validate(string(raw), repo); err != nil {
			fmt.Fprintf(stdout, "::notice::AI release summary left out: %s\n", err)
			summary = ""
		}
	}
	if err := os.WriteFile(out, []byte(compose(string(notes), summary)), 0o644); err != nil {
		fmt.Fprintln(stderr, "cannot write the release body")
		return 1
	}
	return 0
}

// safeAPIURL keeps the key from travelling in clear text: https, or http to the local machine (tests, rehearsals).
func safeAPIURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		return u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	}
	return false
}
