package main

import (
	"os"
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

// steps cuts a job into its steps: each starts at a "      - " line (the indent of a step in this file).
func steps(job string) []string {
	var out []string
	for _, line := range strings.Split(job, "\n") {
		if strings.HasPrefix(line, "      - ") || len(out) == 0 {
			out = append(out, "")
		}
		out[len(out)-1] += line + "\n"
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

// Every way to read the secrets context counts: the dotted form, secrets['X'], toJSON(secrets), secrets: inherit.
// Two mentions are expected: the declaration of the optional secret of the workflow_call, and its single read.
func TestTheSecretsContextIsReadOnceAndDeclaredOnce(t *testing.T) {
	if n := len(regexp.MustCompile(`\bsecrets\b`).FindAllString(workflowText(t), -1)); n != 2 {
		t.Fatalf("release.yml mentions secrets %d times, want 2 (the declaration and one read)", n)
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
	if release := jobWithoutComments(t, "release"); strings.Contains(release, "tools/") || strings.Contains(release, "releasenotes") {
		t.Error("the job that can write and sign compiles or runs the release notes tool")
	}
}

func TestAFailureOfTheSummaryJobDoesNotStopTheRelease(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release-notes"), "continue-on-error: true") {
		t.Error("a failure of the release-notes job would be a failure of the run")
	}
}

// release.yml has no expression evaluator in its tests, so the condition is pinned by what it must and must not contain.
func publishCondition(t *testing.T) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^    if: (.*)$`).FindStringSubmatch(jobWithoutComments(t, "release"))
	if m == nil {
		t.Fatal("the release job has no if")
	}
	return m[1]
}

func TestThePublishingJobDoesNotAskForTheSummaryJobToSucceed(t *testing.T) {
	if cond := publishCondition(t); strings.Contains(cond, "release-notes") || !strings.Contains(cond, "!cancelled()") {
		t.Errorf("the release condition depends on release-notes, or no longer lifts the implicit success: %s", cond)
	}
}

func TestThePublishingJobStillRequiresTheFrontAndAReleaseToMake(t *testing.T) {
	cond := publishCondition(t)
	for _, want := range []string{"needs.front.result == 'success'", "needs.plan.result == 'success'", "needs.plan.outputs.release == 'true'"} {
		if !strings.Contains(cond, want) {
			t.Errorf("the release condition lacks %s: %s", want, cond)
		}
	}
}

func TestAMissingReleaseBodyDoesNotStopThePublishingJob(t *testing.T) {
	fetch := stepsMentioning(t, jobWithoutComments(t, "release"), "name: release-body")
	if len(fetch) != 1 || !strings.Contains(fetch[0], "continue-on-error: true") {
		t.Error("the step that fetches the release body can fail the release")
	}
}

func TestTheNotesAreRequiredByThePublishingJob(t *testing.T) {
	fetch := stepsMentioning(t, jobWithoutComments(t, "release"), "name: release-notes")
	if len(fetch) != 1 || strings.Contains(fetch[0], "continue-on-error") {
		t.Error("the git-cliff notes are optional in the publishing job")
	}
}

func TestGoReleaserPublishesTheChosenBody(t *testing.T) {
	if !strings.Contains(jobWithoutComments(t, "release"), "--release-notes ${{ steps.body.outputs.path }}") {
		t.Error("GoReleaser does not publish the body the previous step chose")
	}
}
