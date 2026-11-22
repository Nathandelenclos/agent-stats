package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
	"time"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

var argumentTime = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

func TestPromptsSubcommandTakesItsFlagsAfterItsName(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no flag", []string{"prompts"}, "7d"},
		{"flags after the name", []string{"prompts", "--since", "30d", "--harness", "codex", "--json"}, "30d"},
		{"flags before the name", []string{"--since", "24h", "prompts"}, "24h"},
	} {
		// Given the command line the user typed
		fs := flag.NewFlagSet("agent-stats", flag.ContinueOnError)
		since := fs.String("since", "", "")
		fs.String("harness", "", "")
		fs.Bool("json", false, "")
		fs.SetOutput(&bytes.Buffer{})
		if err := fs.Parse(c.args); err != nil {
			t.Fatal(err)
		}

		// When the subcommand is read
		sub, err := subcommand(fs)

		// Then the subcommand is recognised with its flags
		got, _ := sinceOf(sub, *since, argumentTime)
		want, _ := parseSince(c.want, argumentTime)
		if err != nil || sub != "prompts" || !got.Equal(want) {
			t.Errorf("%s: sub = %q, since = %v, want %v, err = %v", c.name, sub, got, want, err)
		}
	}
}

func TestPromptsSubcommandRefusesAStrayWord(t *testing.T) {
	// Given a command line with a word after the subcommand's name
	fs := flag.NewFlagSet("agent-stats", flag.ContinueOnError)
	if err := fs.Parse([]string{"prompts", "extra"}); err != nil {
		t.Fatal(err)
	}

	// When the subcommand is read, then the stray word is refused
	if _, err := subcommand(fs); err == nil {
		t.Error("a stray word was accepted")
	}
}

func TestOtherCommandLinesAreNoSubcommand(t *testing.T) {
	// Given the default command, with and without a word the program never knew
	for _, args := range [][]string{{}, {"--json"}, {"unknown"}} {
		fs := flag.NewFlagSet("agent-stats", flag.ContinueOnError)
		fs.Bool("json", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}

		// When the subcommand is read, then there is none and the default command keeps its way
		if sub, err := subcommand(fs); sub != "" || err != nil {
			t.Errorf("%v: sub = %q, %v", args, sub, err)
		}
	}
}

func TestPromptsPeriodDefaultsToAWeek(t *testing.T) {
	// Given no --since, when the review's period is read, then it is the last week
	if got, err := sinceOf("prompts", "", argumentTime); err != nil || !got.Equal(argumentTime.AddDate(0, 0, -7)) {
		t.Errorf("default = %v, %v", got, err)
	}
}

func TestPromptsPeriodAcceptsAll(t *testing.T) {
	// Given --since all, when the review's period is read, then it is every prompt
	if got, err := sinceOf("prompts", "all", argumentTime); err != nil || !got.IsZero() {
		t.Errorf("all = %v, %v", got, err)
	}
}

func TestTheDefaultCommandStillReadsAnEmptySinceAsEverything(t *testing.T) {
	// Given the default command with no --since, when its period is read, then it is everything
	if got, err := sinceOf("", "", argumentTime); err != nil || !got.IsZero() {
		t.Errorf("empty = %v, %v", got, err)
	}
}

func TestTheDefaultCommandStillRefusesAll(t *testing.T) {
	// Given the default command with --since all, when its period is read, then it is refused
	if _, err := sinceOf("", "all", argumentTime); err == nil {
		t.Error("the default command accepted all")
	}
}

func TestPromptReviewJSONPreservesDistinctLongFolderPaths(t *testing.T) {
	prefix := "/" + strings.Repeat("a", excerptRunes)
	review := reporting.PromptReview{Folders: []reporting.FolderFrustration{
		{Folder: prefix + "/one", Prompts: 1},
		{Folder: prefix + "/two", Prompts: 2},
	}}
	got := promptReviewJSON(review).ByFolder
	if got[0].Folder != prefix+"/one" || got[1].Folder != prefix+"/two" {
		t.Fatalf("folder paths were truncated: %+v", got)
	}
}
