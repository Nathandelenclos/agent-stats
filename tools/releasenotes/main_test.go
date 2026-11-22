package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// workdir writes the notes file and returns the paths a run uses.
func workdir(t *testing.T) (notes, out string) {
	t.Helper()
	dir := t.TempDir()
	notes = filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte(gitCliffNotes), 0o644); err != nil {
		t.Fatal(err)
	}
	return notes, filepath.Join(dir, "summary.md")
}

// summarized runs the program against a working API and returns what it wrote and printed.
func summarized(t *testing.T) (summary, printed string, code int) {
	t.Helper()
	srv, _ := fakeAPI(t, answer(200, reply("Reports break down by day.")))
	notes, out := workdir(t)
	var stdout, stderr bytes.Buffer
	env := envOf(map[string]string{"OPENAI_API_KEY": testKey, "OPENAI_MODEL": "m"})
	code = run(context.Background(), []string{"summarize", "-notes", notes, "-out", out, "-api-url", srv.URL}, env, &stdout, &stderr)
	got, _ := os.ReadFile(out)
	return string(got), stdout.String() + stderr.String(), code
}

func TestSummarizeWritesTheSummaryFile(t *testing.T) {
	if summary, _, code := summarized(t); code != 0 || summary != "Reports break down by day." {
		t.Errorf("code %d, summary %q", code, summary)
	}
}

func TestSummarizePrintsNoSecret(t *testing.T) {
	if _, printed, _ := summarized(t); strings.Contains(printed, testKey) {
		t.Error("the key is printed")
	}
}

func TestSummarizeNeverFailsTheReleaseWhateverGoesWrong(t *testing.T) {
	failures := map[string]struct {
		answers []string // the API answer, or "" for no server
		env     map[string]string
	}{
		"no key":     {env: map[string]string{"OPENAI_MODEL": "m"}},
		"no model":   {env: map[string]string{"OPENAI_API_KEY": testKey}},
		"api down":   {env: map[string]string{"OPENAI_API_KEY": testKey, "OPENAI_MODEL": "m"}},
		"bad output": {answers: []string{reply("Mail @everyone")}, env: map[string]string{"OPENAI_API_KEY": testKey, "OPENAI_MODEL": "m"}},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			// Given a run that cannot produce a summary
			notes, out := workdir(t)
			url := "http://127.0.0.1:1"
			if len(tc.answers) > 0 {
				srv, _ := fakeAPI(t, answer(200, tc.answers[0]))
				url = srv.URL
			}
			var stdout, stderr bytes.Buffer

			// When the program summarizes
			code := run(context.Background(), []string{"summarize", "-notes", notes, "-out", out, "-api-url", url}, envOf(tc.env), &stdout, &stderr)

			// Then it still succeeds, leaves an empty summary and says why without the key
			got, err := os.ReadFile(out)
			if code != 0 || err != nil || len(got) != 0 {
				t.Errorf("code %d, summary %q, err %v", code, got, err)
			}
			if !strings.HasPrefix(stdout.String(), "::notice::AI release summary skipped: ") {
				t.Errorf("stdout = %q", stdout.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), testKey) {
				t.Error("the key is printed")
			}
		})
	}
}

func TestSummarizeRefusesToSendTheKeyInClearText(t *testing.T) {
	notes, out := workdir(t)
	var stdout, stderr bytes.Buffer
	env := envOf(map[string]string{"OPENAI_API_KEY": testKey, "OPENAI_MODEL": "m"})
	for _, u := range []string{"http://api.example.com/v1/responses", "ftp://x", "https://"} {
		if code := run(context.Background(), []string{"summarize", "-notes", notes, "-out", out, "-api-url", u}, env, &stdout, &stderr); code != 2 {
			t.Errorf("%s: code %d", u, code)
		}
	}
}

func TestUsageErrorsAreExitCodeTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{nil, {"nope"}, {"summarize"}, {"compose", "-notes", "x", "-out", "y"}, {"summarize", "-bad"}} {
		if code := run(context.Background(), args, envOf(nil), &stdout, &stderr); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}
