package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The release job can push a tag, publish and sign. The front's build runs third-party tools (vite, tailwind, tsc),
// so it must not run there: a job limited to reading builds it and hands web/dist over. These tests pin that split.

func workflowJob(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, jobs, ok := strings.Cut(string(raw), "\njobs:\n")
	if !ok {
		t.Fatal("release.yml has no jobs")
	}
	starts := regexp.MustCompile(`(?m)^  ([a-z-]+):$`).FindAllStringSubmatchIndex(jobs, -1)
	for i, s := range starts {
		if jobs[s[2]:s[3]] != name {
			continue
		}
		end := len(jobs)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		return jobs[s[0]:end]
	}
	t.Fatalf("release.yml has no job %q", name)
	return ""
}

func TestTheJobThatCanPublishAndSignBuildsNoFront(t *testing.T) {
	release := workflowJob(t, "release")
	if !strings.Contains(release, "contents: write") {
		t.Fatal("the release job no longer writes: this test guards the wrong job")
	}
	for _, tool := range []string{"npm ", "go generate", "setup-node"} {
		if strings.Contains(release, tool) {
			t.Errorf("the release job runs %q, which executes third-party build tools with write and signing rights", tool)
		}
	}
	goreleaser, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"before:", "hooks:", "go generate"} {
		if strings.Contains(string(goreleaser), hook) {
			t.Errorf("GoReleaser runs a build hook (%q) in the release job", hook)
		}
	}
}

func TestTheFrontIsBuiltByAJobThatOnlyReads(t *testing.T) {
	front := workflowJob(t, "front")
	if !strings.Contains(front, "contents: read") {
		t.Error("the front job does not declare read-only contents")
	}
	for _, right := range []string{"contents: write", "id-token", "attestations"} {
		if strings.Contains(front, right) {
			t.Errorf("the front job holds %q", right)
		}
	}
	if !strings.Contains(front, "npm ci") || !strings.Contains(front, "npm run build") {
		t.Error("the front job does not build the front from the lockfile")
	}
	if !strings.Contains(front, "persist-credentials: false") {
		t.Error("the front job keeps the checkout token in the clone its build tools run in")
	}
}

func TestTheReleaseJobTakesTheFrontFromTheFrontJob(t *testing.T) {
	release := workflowJob(t, "release")
	if !strings.Contains(release, "needs: [front, plan, release-notes]") {
		t.Error("the release job does not wait for the front job")
	}
	if !strings.Contains(release, "download-artifact") || !strings.Contains(release, "path: web/dist") {
		t.Error("the release job does not fetch web/dist built by the front job")
	}
}

// GitHub lets a run be re-run for 30 days. Re-running only the publish job after a failure reads the artifact of the
// front job again, so the artifact must outlive that window; past it, the manual run on the tag rebuilds everything.
const rerunWindowDays = 30

func TestTheFrontArtifactOutlivesTheWindowToRerunThePublishJob(t *testing.T) {
	front := workflowJob(t, "front")
	m := regexp.MustCompile(`retention-days: (\d+)`).FindStringSubmatch(front)
	if m == nil {
		t.Fatal("the front job sets no retention: the repository default applies, which a setting can change")
	}
	if days, _ := strconv.Atoi(m[1]); days < rerunWindowDays {
		t.Errorf("the front artifact is kept %d days: a re-run of the publish job inside the %d-day window would find it gone", days, rerunWindowDays)
	}
}

// The release carries a summary written by a third-party API from the text of public commits. The key for that API lives
// in one step of one job that only reads; the job that tags, signs and publishes never sees it, compiles nothing of
// tools/ and does not wait on that job's success.

func workflowText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	return withoutComments(string(raw))
}

func withoutComments(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// steps cuts a job into its steps: each starts at a "      - " line (the indent of a step in this file). What comes
// before the first step (the header of the job) is not a step.
func steps(job string) []string {
	var out []string
	for _, line := range strings.Split(job, "\n") {
		if strings.HasPrefix(line, "      - ") {
			out = append(out, "")
		}
		if len(out) > 0 {
			out[len(out)-1] += line + "\n"
		}
	}
	return out
}

func stepsMentioning(t *testing.T, job, text string) []string {
	t.Helper()
	var found []string
	for _, step := range steps(job) {
		if strings.Contains(step, text) {
			found = append(found, step)
		}
	}
	return found
}

func jobWithoutComments(t *testing.T, name string) string {
	return withoutComments(workflowJob(t, name))
}

// step returns the one step of the job that holds text.
func step(t *testing.T, job, text string) string {
	t.Helper()
	found := stepsMentioning(t, jobWithoutComments(t, job), text)
	if len(found) != 1 {
		t.Fatalf("%d steps of the %s job hold %q, want 1", len(found), job, text)
	}
	return found[0]
}

// script is the shell script of a step, as bash receives it.
func script(t *testing.T, job, text string) string {
	t.Helper()
	_, body, ok := strings.Cut(step(t, job, text), "run: |\n")
	if !ok {
		t.Fatalf("the step %q has no run block", text)
	}
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(lines, "\n")
}

// runScript runs a step's script in dir with the given environment and returns what GITHUB_OUTPUT received.
func runScript(t *testing.T, script, dir string, env ...string) (output string, out []byte, err error) {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+outFile, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	cmd.Env = append(cmd.Env, env...)
	out, err = cmd.CombinedOutput()
	raw, _ := os.ReadFile(outFile)
	return string(raw), out, err
}

// Every way to read the secrets context counts: the dotted form, secrets['X'], toJSON(secrets), secrets: inherit.
// Three mentions are expected: the declaration of the optional secret of the workflow_call, and its two reads, both in
// the release-notes job (a flag that says whether there is a key, and the key itself for the one step that uses it).
func TestTheSecretsContextIsReadOnlyByTheJobThatHoldsTheKey(t *testing.T) {
	count := func(text string) int { return len(regexp.MustCompile(`\bsecrets\b`).FindAllString(text, -1)) }
	if n := count(workflowText(t)); n != 3 {
		t.Fatalf("release.yml mentions secrets %d times, want 3 (the declaration and two reads)", n)
	}
	if n := count(jobWithoutComments(t, "release-notes")); n != 2 {
		t.Fatalf("the release-notes job mentions secrets %d times, want 2", n)
	}
}

func TestTheOnlyReadOfTheKeyIsInTheReleaseNotesJob(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release-notes"), "secrets.OPENAI_API_KEY") {
		t.Error("the key is read outside the release-notes job")
	}
}

func TestTheKeyIsInTheEnvironmentOfOneStepOnly(t *testing.T) {
	notes := jobWithoutComments(t, "release-notes")
	holders := stepsMentioning(t, notes, "OPENAI_API_KEY")
	if len(holders) != 1 || !strings.Contains(holders[0], "name: Write the summary") {
		t.Fatalf("%d steps mention the key, want only 'Write the summary'", len(holders))
	}
	// Twice in that step: the name of the variable and the secret it takes.
	if n := strings.Count(holders[0], "OPENAI_API_KEY"); n != 2 {
		t.Errorf("the step mentions the key %d times, want the variable and its secret only", n)
	}
	// Elsewhere in the job, only the flag that says there is one: a boolean, never the value.
	header, _, _ := strings.Cut(notes, "    steps:\n")
	if !strings.Contains(header, "AI_KEY_SET: ${{ secrets.OPENAI_API_KEY != '' }}") {
		t.Error("the job does not tell its steps whether there is a key through a boolean flag")
	}
}

func TestTheStepThatHoldsTheKeyHasNoSecretOnItsCommandLine(t *testing.T) {
	holder := stepsMentioning(t, jobWithoutComments(t, "release-notes"), "OPENAI_API_KEY")[0]
	_, run, _ := strings.Cut(holder, "run:")
	if strings.Contains(strings.ReplaceAll(run, "$RUNNER_TEMP", ""), "$") || strings.Contains(run, "${{") {
		t.Errorf("the command of the step expands something besides $RUNNER_TEMP:\n%s", run)
	}
}

func TestTheJobThatHoldsTheKeyOnlyReads(t *testing.T) {
	notes := jobWithoutComments(t, "release-notes")
	if !strings.Contains(notes, "contents: read") {
		t.Error("the release-notes job does not declare read-only contents")
	}
	for _, right := range []string{"contents: write", "id-token", "attestations", "GITHUB_TOKEN", "github.token", "GH_TOKEN"} {
		if strings.Contains(notes, right) {
			t.Errorf("the release-notes job holds %q next to the key", right)
		}
	}
}

func TestTheJobThatHoldsTheKeyKeepsNoCheckoutToken(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release-notes"), "persist-credentials: false") {
		t.Error("the release-notes job keeps the checkout token in its clone")
	}
}

func TestTheKeyIsWithheldWhenTheRepositoryVariableSaysOff(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release-notes"), "vars.AI_RELEASE_NOTES != 'off' && secrets.OPENAI_API_KEY || ''") {
		t.Error("the repository variable AI_RELEASE_NOTES=off no longer withholds the key")
	}
}

func TestTheJobsThatPlanAndPublishNeverMentionTheKey(t *testing.T) {
	for _, name := range []string{"front", "plan", "release"} {
		if strings.Contains(jobWithoutComments(t, name), "OPENAI") {
			t.Errorf("the %s job mentions the OpenAI key or model", name)
		}
	}
}

func TestThePlanJobOnlyReads(t *testing.T) {
	plan := jobWithoutComments(t, "plan")
	for _, right := range []string{"contents: write", "id-token", "attestations"} {
		if strings.Contains(plan, right) {
			t.Errorf("the plan job holds %q", right)
		}
	}
	if !strings.Contains(plan, "contents: read") || !strings.Contains(plan, "persist-credentials: false") {
		t.Error("the plan job does not read only, or keeps the checkout token")
	}
}

func TestThePublishingJobBuildsAndRunsNothingOfTools(t *testing.T) {
	release := jobWithoutComments(t, "release")
	if strings.Contains(release, "tools/") || strings.Contains(release, "releasenotes") || regexp.MustCompile(`\bgo (run|build|generate|install|test|vet)\b`).MatchString(release) {
		t.Error("the job that can write and sign compiles or runs Go code of the repository")
	}
}

func TestAFailureOfTheSummaryJobDoesNotStopTheRelease(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release-notes"), "continue-on-error: true") {
		t.Error("a failure of the release-notes job would be a failure of the run")
	}
}

// The summary is optional, so it must cost nothing when it is turned off or has no key, and cannot run for long.

func TestTheSummaryJobDoesNotStartWhenTheRepositoryVariableSaysOff(t *testing.T) {
	m := regexp.MustCompile(`(?m)^    if: (.*)$`).FindStringSubmatch(jobWithoutComments(t, "release-notes"))
	if m == nil || !strings.Contains(m[1], "vars.AI_RELEASE_NOTES != 'off'") || !strings.Contains(m[1], "needs.plan.outputs.release == 'true'") || !strings.Contains(m[1], "needs.plan.outputs.notes_id != ''") {
		t.Errorf("the release-notes job still starts with AI_RELEASE_NOTES=off; its if is %q", m)
	}
}

func TestTheSummaryJobBuildsNothingWithoutAKey(t *testing.T) {
	for _, text := range []string{"actions/checkout", "actions/setup-go", "name: Build the tool", "name: Write the summary", "name: Drop an empty summary", "name: Hand the summary over"} {
		if s := step(t, "release-notes", text); !strings.Contains(s, "if: env.AI_KEY_SET == 'true'") {
			t.Errorf("the step %q runs without a key:\n%s", text, s)
		}
	}
}

func TestTheSummaryJobAndItsAPICallAreTimeBounded(t *testing.T) {
	notes := jobWithoutComments(t, "release-notes")
	if !strings.Contains(notes, "timeout-minutes: 3\n") {
		t.Error("the release-notes job is not limited to 3 minutes")
	}
	if !strings.Contains(step(t, "release-notes", "name: Write the summary"), "timeout-minutes: 2") {
		t.Error("the step that calls the API has no time limit of its own")
	}
}

// The artifacts the publishing job consumes are named by the run, so another job of the run could upload one of the same
// name. Each is downloaded by the ID its producer reported, and the job fails when a required one is missing.

func TestTheJobsThatProduceAnArtifactReportItsID(t *testing.T) {
	for _, tc := range []struct{ job, output string }{{"front", "web_dist_id"}, {"plan", "notes_id"}, {"release-notes", "summary_id"}} {
		m := regexp.MustCompile(tc.output + `: \$\{\{ steps\.([a-z-]+)\.outputs\.artifact-id \}\}`).FindStringSubmatch(jobWithoutComments(t, tc.job))
		if m == nil {
			t.Errorf("the %s job has no output %s taken from its upload step", tc.job, tc.output)
			continue
		}
		if s := step(t, tc.job, "id: "+m[1]); !strings.Contains(s, "actions/upload-artifact") {
			t.Errorf("%s.%s does not come from an upload step:\n%s", tc.job, tc.output, s)
		}
	}
}

func TestThePublishingJobDownloadsEveryArtifactByID(t *testing.T) {
	downloads := stepsMentioning(t, jobWithoutComments(t, "release"), "actions/download-artifact")
	if len(downloads) != 3 {
		t.Fatalf("%d downloads, want the front, the notes and the summary", len(downloads))
	}
	for i, id := range []string{"needs.front.outputs.web_dist_id", "needs.plan.outputs.notes_id", "needs.release-notes.outputs.summary_id"} {
		if !strings.Contains(downloads[i], "artifact-ids: ${{ "+id+" }}") {
			t.Errorf("download %d does not take %s:\n%s", i, id, downloads[i])
		}
		if regexp.MustCompile(`(?m)^          name:`).MatchString(downloads[i]) {
			t.Errorf("download %d names its artifact, which another job of the run could replace:\n%s", i, downloads[i])
		}
	}
}

func TestThePublishingJobFailsWhenTheFrontOrTheNotesAreMissing(t *testing.T) {
	require := script(t, "release", "name: Require the front and the notes")
	for _, tc := range []struct {
		name, front, notes string
		fails              bool
	}{
		{"both present", "11", "12", false},
		{"no front", "", "12", true},
		{"no notes", "11", "", true},
		{"nothing", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runScript(t, require, t.TempDir(), "WEB_DIST_ID="+tc.front, "NOTES_ID="+tc.notes)
			if (err != nil) != tc.fails {
				t.Errorf("err = %v, want failure %v\n%s", err, tc.fails, out)
			}
		})
	}
}

func TestOnlyTheSummaryDownloadMayFail(t *testing.T) {
	for _, text := range []string{"artifact-ids: ${{ needs.front.outputs.web_dist_id }}", "artifact-ids: ${{ needs.plan.outputs.notes_id }}"} {
		if strings.Contains(step(t, "release", text), "continue-on-error") {
			t.Errorf("the download of %s is optional", text)
		}
	}
	optional := step(t, "release", "artifact-ids: ${{ needs.release-notes.outputs.summary_id }}")
	if !strings.Contains(optional, "continue-on-error: true") || !strings.Contains(optional, "if: steps.summary.outputs.fetch == 'true'") {
		t.Errorf("the summary download can fail the release, or runs although the check refused it:\n%s", optional)
	}
}

// The size of the summary artifact is read from the API before it is downloaded and extracted.
func TestTheSummaryIsNotDownloadedUnlessItIsSmallAndExists(t *testing.T) {
	check := script(t, "release", "name: Check the summary artifact")
	fake := t.TempDir()
	gh := func(body string) []string {
		if err := os.WriteFile(filepath.Join(fake, "gh"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return []string{"PATH=" + fake + ":" + os.Getenv("PATH")}
	}
	for _, tc := range []struct {
		name, id, gh string
		fetch        bool
	}{
		{"small", "7", "echo 300", true},
		{"no artifact id", "", "echo 300", false},
		{"too big", "7", "echo 100000", false},
		{"API error", "7", "exit 1", false},
		{"not a number", "7", "echo oops", false},
		{"id that is not a number", "7/../../x", "echo 300", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(gh(tc.gh), "SUMMARY_ID="+tc.id, "GH_TOKEN=x", "GITHUB_REPOSITORY=o/r")
			output, out, err := runScript(t, check, t.TempDir(), env...)
			if err != nil {
				t.Fatalf("the check failed the release: %v\n%s", err, out)
			}
			if got := strings.Contains(output, "fetch=true"); got != tc.fetch {
				t.Errorf("fetch = %v, want %v (output %q)", got, tc.fetch, output)
			}
		})
	}
}

func TestTheSummaryCheckRunsOnlyWhenTheSummaryJobSucceeded(t *testing.T) {
	if !strings.Contains(step(t, "release", "name: Check the summary artifact"), "if: needs.release-notes.result == 'success'") {
		t.Error("the check reads the API although the summary job was skipped or cancelled")
	}
}

// condition returns the `if` of the publishing job, without the ${{ }} around it.
func publishCondition(t *testing.T) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^    if: (.*)$`).FindStringSubmatch(jobWithoutComments(t, "release"))
	if m == nil {
		t.Fatal("the release job has no if")
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(m[1]), "${{"), "}}"))
}

// runState is what a run looks like to the publishing job: the result of each job it needs, the plan's decision, and
// whether the run was cancelled.
type runState struct {
	front, plan, notes, release string
	cancelled                   bool
}

// runs evaluates the condition. It only understands what the condition is made of today: `&&`, `!cancelled()` and
// comparisons of needs.<job>.result or needs.plan.outputs.release with a string. A term it does not know (an `||`,
// `always()`…) fails the test: a new shape of condition must be read again, not waved through. Without a status
// function GitHub adds success(): every needed job succeeded. `!cancelled()` replaces it, so it is a term the
// condition must state: it is what lets the release run when the optional job was skipped or failed.
func runs(t *testing.T, cond string, s runState) bool {
	t.Helper()
	values := map[string]string{
		"needs.front.result":         s.front,
		"needs.plan.result":          s.plan,
		"needs.release-notes.result": s.notes,
		"needs.plan.outputs.release": s.release,
	}
	statusFunction := false
	for _, term := range strings.Split(cond, "&&") {
		term = strings.TrimSpace(term)
		if term == "!cancelled()" {
			statusFunction = true
			if s.cancelled {
				return false
			}
			continue
		}
		m := regexp.MustCompile(`^(needs\.[a-z.-]+) == '([a-z]*)'$`).FindStringSubmatch(term)
		if m == nil {
			t.Fatalf("the release condition has a term this test does not evaluate: %q", term)
		}
		if got, known := values[m[1]]; !known {
			t.Fatalf("the release condition reads %s, which this test does not provide", m[1])
		} else if got != m[2] {
			return false
		}
	}
	if !statusFunction && (s.cancelled || s.front != "success" || s.plan != "success" || s.notes != "success") {
		return false
	}
	return true
}

var jobResults = []string{"success", "failure", "skipped", "cancelled"}

func TestThePublishingJobStatesItsStatusFunction(t *testing.T) {
	if !strings.Contains(" "+publishCondition(t)+" ", " !cancelled() ") {
		t.Error("without !cancelled(), GitHub applies success() and a skipped or failed summary job stops the release")
	}
}

func TestThePublishingJobRunsWhateverTheSummaryJobDid(t *testing.T) {
	cond := publishCondition(t)
	for _, notes := range jobResults {
		if !runs(t, cond, runState{"success", "success", notes, "true", false}) {
			t.Errorf("the release does not run when the release-notes job ended %s", notes)
		}
	}
}

func TestThePublishingJobDoesNotRunInACancelledRun(t *testing.T) {
	cond := publishCondition(t)
	for _, notes := range jobResults {
		if runs(t, cond, runState{"success", "success", notes, "true", true}) {
			t.Errorf("the release runs in a cancelled run (release-notes %s)", notes)
		}
	}
}

func TestThePublishingJobDoesNotRunWithoutTheFrontOrThePlan(t *testing.T) {
	cond := publishCondition(t)
	for _, result := range jobResults[1:] {
		if runs(t, cond, runState{result, "success", "success", "true", false}) {
			t.Errorf("the release runs when the front job ended %s", result)
		}
		if runs(t, cond, runState{"success", result, "success", "true", false}) {
			t.Errorf("the release runs when the plan job ended %s", result)
		}
	}
}

func TestThePublishingJobDoesNotRunWhenThereIsNothingToRelease(t *testing.T) {
	cond := publishCondition(t)
	for _, release := range []string{"false", ""} {
		if runs(t, cond, runState{"success", "success", "success", release, false}) {
			t.Errorf("the release runs when the plan says release=%q", release)
		}
	}
}

// The tag step runs again on "Re-run failed jobs", with the plan's create_tag=true of the first attempt, although the
// tag was pushed by it. It creates the tag only if it is missing, accepts one that already names this commit and refuses
// one that names another.

// gitRepo returns a work clone with two commits, its bare origin, and the SHAs of the first and the second commit.
func gitRepo(t *testing.T) (work, origin, first, second string) {
	t.Helper()
	root := t.TempDir()
	origin, work = filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "-q", "--bare", origin)
	git(root, "init", "-q", "-b", "main", work)
	git(work, "remote", "add", "origin", origin)
	git(work, "commit", "-q", "--allow-empty", "-m", "first")
	first = git(work, "rev-parse", "HEAD")
	git(work, "commit", "-q", "--allow-empty", "-m", "second")
	second = git(work, "rev-parse", "HEAD")
	git(work, "push", "-q", "origin", "main")
	return work, origin, first, second
}

func tagOf(t *testing.T, repo, tag string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "refs/tags/"+tag+"^{commit}")
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func TestTheTagStepCreatesAndPushesAMissingTag(t *testing.T) {
	work, origin, _, head := gitRepo(t)
	if _, out, err := runScript(t, script(t, "release", "name: Create the tag"), work, "VERSION=v1.2.3"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if tagOf(t, work, "v1.2.3") != head || tagOf(t, origin, "v1.2.3") != head {
		t.Error("the tag is not on HEAD, locally and on origin")
	}
}

func TestTheTagStepAcceptsATagAlreadyOnThisCommit(t *testing.T) {
	// Given the first attempt pushed the tag, then GoReleaser or the attestation failed and the job is re-run
	work, origin, _, head := gitRepo(t)
	step := script(t, "release", "name: Create the tag")
	if _, out, err := runScript(t, step, work, "VERSION=v1.2.3"); err != nil {
		t.Fatalf("first attempt: %v\n%s", err, out)
	}

	// When the step runs again
	_, out, err := runScript(t, step, work, "VERSION=v1.2.3")

	// Then it succeeds and leaves the tag where it was
	if err != nil || tagOf(t, work, "v1.2.3") != head || tagOf(t, origin, "v1.2.3") != head {
		t.Errorf("err = %v\n%s", err, out)
	}
}

func TestTheTagStepPushesAnExistingLocalTagThatOriginLacks(t *testing.T) {
	work, origin, _, head := gitRepo(t)
	if out, err := exec.Command("git", "-C", work, "tag", "v1.2.3", head).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, out, err := runScript(t, script(t, "release", "name: Create the tag"), work, "VERSION=v1.2.3"); err != nil || tagOf(t, origin, "v1.2.3") != head {
		t.Errorf("err = %v\n%s", err, out)
	}
}

func TestTheTagStepRefusesATagThatNamesAnotherCommit(t *testing.T) {
	// Given a tag of the same name on an older commit
	work, origin, first, _ := gitRepo(t)
	for _, repo := range []string{work, origin} {
		if out, err := exec.Command("git", "-C", repo, "tag", "v1.2.3", first).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}

	// When the step runs
	_, out, err := runScript(t, script(t, "release", "name: Create the tag"), work, "VERSION=v1.2.3")

	// Then it fails, and the tag stays on the older commit everywhere
	if err == nil {
		t.Fatalf("the step accepted a tag on another commit\n%s", out)
	}
	if tagOf(t, work, "v1.2.3") != first || tagOf(t, origin, "v1.2.3") != first {
		t.Error("the tag was moved")
	}
}

// The body is the git-cliff notes, under the summary when there is a valid one. The summary crosses a job boundary and
// is a reply of a third-party API: the step checks it again, in shell, because the publishing job runs none of tools/.

// chooseBody runs the "Choose the release body" step in a temporary RUNNER_TEMP that holds the notes and, when given,
// the summary artifact. It returns the file the step chose, and the notes' path.
func chooseBody(t *testing.T, notes string, summary *string) (chosen, notesPath string) {
	t.Helper()
	temp := t.TempDir()
	notesPath = filepath.Join(temp, "notes", "notes.md")
	files := map[string]*string{notesPath: &notes, filepath.Join(temp, "summary", "summary.md"): summary}
	for path, content := range files {
		if content == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(*content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output, out, err := runScript(t, script(t, "release", "name: Choose the release body"), temp, "RUNNER_TEMP="+temp)
	if err != nil {
		t.Fatalf("the step failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(strings.TrimPrefix(output, "path=")), notesPath
}

const sampleNotes = "### Features\n\n- **cli**: Add the catalog subcommand (a1b2c3d)\n"

func bodyWith(t *testing.T, summary string) (body string, withSummary bool) {
	t.Helper()
	chosen, notesPath := chooseBody(t, sampleNotes, &summary)
	raw, err := os.ReadFile(chosen)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), chosen != notesPath
}

func TestTheSummaryIsPublishedAboveTheNotes(t *testing.T) {
	for _, summary := range []string{
		"A new catalog command.",
		"A new catalog command.\n",
		"**Faster** and _cleaner_.\n\n- One bullet (1.5x).\n- Two\u2026 it\u2019s \u201cfine\u201d \u2013 really.",
	} {
		body, with := bodyWith(t, summary)
		if want := "## Summary\n\n" + strings.TrimRight(summary, "\n") + "\n\n" + sampleNotes; !with || body != want {
			t.Errorf("summary %q: body %q, want %q", summary, body, want)
		}
	}
}

func TestTheNotesAloneArePublishedWhenThereIsNoSummary(t *testing.T) {
	if chosen, notesPath := chooseBody(t, sampleNotes, nil); chosen != notesPath {
		t.Errorf("chose %s, want the notes %s", chosen, notesPath)
	}
}

// The same shapes as the tool refuses, which the shell repeats: nothing of them may reach a release body.
func TestTheNotesAloneArePublishedWhenTheSummaryIsNotPlainText(t *testing.T) {
	for name, summary := range map[string]string{
		"empty":                  "",
		"blank":                  " \n\n ",
		"heading":                "# Update now",
		"heading in a quote":     "Fixed.\n\n> ## Update now",
		"quote":                  "> Fixed.",
		"star bullet":            "* Fixed.",
		"ordered list":           "1. Fixed.",
		"nested bullet":          "- Fixed.\n  - Nested.",
		"setext heading":         "Big news\n---",
		"thematic break":         "Fixed.\n\n***",
		"html":                   "Fixed.<script>x</script>",
		"backtick":               "Run `x` now.",
		"mention":                "Thanks @octocat.",
		"link":                   "See [here](https://evil.example).",
		"bare url":               "See https://evil.example now.",
		"bare url in own repo":   "See https://github.com/o/r/issues/1.",
		"www":                    "See www.evil.example.",
		"issue number":           "Fixes #7.",
		"entity":                 "Fixed &#64;octocat.",
		"ampersand":              "A & B.",
		"escape":                 "Fixed \\<b>.",
		"table":                  "a | b",
		"strikethrough":          "Fixed ~~x~~.",
		"tab":                    "Fixed.\tThings.",
		"carriage return":        "Fixed.\r\nThings.",
		"bell":                   "Safe\x07text",
		"nul":                    "Safe\x00text",
		"bidirectional":          "Safe \u202eevil",
		"zero width space":       "Safe\u200btext",
		"accent outside the set": "Café ouvert.",
		"workflow command":       "Fixed.\n::error::x",
		"too long":               strings.Repeat("a", 1501),
		"far too long":           strings.Repeat("a", 7000),
	} {
		t.Run(name, func(t *testing.T) {
			if body, with := bodyWith(t, summary); with || body != sampleNotes {
				t.Errorf("published %q for %q", body, summary)
			}
		})
	}
}

// A check that cannot run is not a check that passed: when grep itself fails (exit 2), the summary is not published.
func TestTheNotesAloneArePublishedWhenTheCheckCannotRun(t *testing.T) {
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "grep"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	for path, content := range map[string]string{filepath.Join(temp, "notes", "notes.md"): sampleNotes, filepath.Join(temp, "summary", "summary.md"): "A new catalog command."} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output, out, err := runScript(t, script(t, "release", "name: Choose the release body"), temp, "RUNNER_TEMP="+temp, "PATH="+fake+":"+os.Getenv("PATH"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "path=" + filepath.Join(temp, "notes", "notes.md"); strings.TrimSpace(output) != want {
		t.Errorf("output %q, want %q", output, want)
	}
}

func TestTheSummaryAtTheLimitIsKept(t *testing.T) {
	if _, with := bodyWith(t, strings.Repeat("a", 1500)); !with {
		t.Error("a summary of 1 500 characters was refused")
	}
}

func TestTheStepFailsWithoutTheNotesBecauseAReleaseMustCarryThem(t *testing.T) {
	temp := t.TempDir()
	output, _, err := runScript(t, script(t, "release", "name: Choose the release body"), temp, "RUNNER_TEMP="+temp)
	if err == nil || output != "" {
		t.Errorf("the step chose a body without notes: %v, %q", err, output)
	}
}

func TestGoReleaserPublishesTheChosenBody(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release"), "--release-notes ${{ steps.body.outputs.path }}") {
		t.Error("GoReleaser does not publish the body the previous step chose")
	}
}

func TestTheSummaryJobHandsOverOnlyTheSummary(t *testing.T) {
	notes := jobWithoutComments(t, "release-notes")
	if strings.Contains(notes, "releasenotes compose") || strings.Contains(notes, "body.md") || strings.Contains(notes, "release-body") {
		t.Error("the summary job still composes and hands over a release body")
	}
	if !strings.Contains(step(t, "release-notes", "name: Hand the summary over"), "summary.md") {
		t.Error("the summary job does not hand over summary.md")
	}
}

// TestTheCallerGrantsEveryPermissionTheReleaseWorkflowAsks guards against a run that GitHub refuses to start: a called
// workflow cannot ask for a permission its calling job does not grant (startup_failure on 2026-11-22, actions: read).
func TestTheCallerGrantsEveryPermissionTheReleaseWorkflowAsks(t *testing.T) {
	// Given the permissions the jobs of the release workflow ask for
	asked := map[string]bool{}
	perm := regexp.MustCompile(`(?m)^\s+(actions|attestations|contents|id-token|packages|pull-requests|issues|checks|statuses|deployments|pages|security-events): (write|read)\b`)
	for _, m := range perm.FindAllStringSubmatch(withoutComments(workflowText(t)), -1) {
		asked[m[1]+": "+m[2]] = true
	}
	// When the CI job that calls it is read
	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	ci := withoutComments(string(raw))
	call := ci[strings.Index(ci, "uses: ./.github/workflows/release.yml")-600 : strings.Index(ci, "uses: ./.github/workflows/release.yml")]
	granted := map[string]bool{}
	for _, m := range perm.FindAllStringSubmatch(call, -1) {
		granted[m[1]+": "+m[2]] = true
		if m[2] == "write" {
			granted[m[1]+": read"] = true
		}
	}
	// Then it grants each of them
	for p := range asked {
		if !granted[p] {
			t.Errorf("the release workflow asks for %q, which the calling job in ci.yml does not grant", p)
		}
	}
}
