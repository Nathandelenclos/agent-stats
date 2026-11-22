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
// in one job that only reads; the job that tags, signs and publishes never sees it and does not wait on it.

func workflowText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func withoutComments(job string) string {
	var kept []string
	for _, line := range strings.Split(job, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func TestTheOpenAIKeyIsReadByOneStepOfOneJobThatOnlyReads(t *testing.T) {
	if n := strings.Count(workflowText(t), "secrets.OPENAI_API_KEY"); n != 1 {
		t.Fatalf("release.yml reads the key %d times, want once", n)
	}
	notes := workflowJob(t, "release-notes")
	if !strings.Contains(notes, "secrets.OPENAI_API_KEY") {
		t.Fatal("the key is read outside the release-notes job")
	}
	if !strings.Contains(notes, "contents: read") {
		t.Error("the release-notes job does not declare read-only contents")
	}
	for _, right := range []string{"contents: write", "id-token", "attestations", "GITHUB_TOKEN", "github.token"} {
		if strings.Contains(notes, right) {
			t.Errorf("the release-notes job holds %q next to the key", right)
		}
	}
	if !strings.Contains(notes, "persist-credentials: false") {
		t.Error("the release-notes job keeps the checkout token in its clone")
	}
	// The key is in the environment of the step that runs the tool, not of the job or of the build step before it.
	build, write, ok := strings.Cut(notes, "- name: Write the summary")
	if !ok || strings.Contains(build, "OPENAI_API_KEY") || !strings.Contains(write, "OPENAI_API_KEY") {
		t.Error("the key is not confined to the step that writes the summary")
	}
	if strings.Contains(write, "$OPENAI_API_KEY") {
		t.Error("the key is expanded on a command line")
	}
}

func TestTheJobsThatPlanAndPublishNeverSeeTheKey(t *testing.T) {
	for _, name := range []string{"front", "plan", "release"} {
		if strings.Contains(withoutComments(workflowJob(t, name)), "OPENAI") {
			t.Errorf("the %s job mentions the OpenAI key or model", name)
		}
	}
	plan := workflowJob(t, "plan")
	for _, right := range []string{"contents: write", "id-token", "attestations"} {
		if strings.Contains(plan, right) {
			t.Errorf("the plan job holds %q", right)
		}
	}
	if !strings.Contains(plan, "persist-credentials: false") {
		t.Error("the plan job keeps the checkout token")
	}
}

func TestTheReleaseDoesNotDependOnTheSummary(t *testing.T) {
	notes := workflowJob(t, "release-notes")
	if !strings.Contains(notes, "continue-on-error: true") {
		t.Error("a failure of the release-notes job would stop the release")
	}
	release := workflowJob(t, "release")
	if !strings.Contains(release, "needs: [front, plan, release-notes]") || !strings.Contains(release, "!cancelled()") {
		t.Error("the release job does not run after the release-notes job whatever its result")
	}
	if !strings.Contains(release, "needs.front.result == 'success'") || !strings.Contains(release, "needs.plan.outputs.release == 'true'") {
		t.Error("the release job no longer requires the front and a release to plan")
	}
	summary := release[strings.Index(release, "Fetch the summary"):]
	summary = summary[:strings.Index(summary, "- name: Compose")]
	if !strings.Contains(summary, "continue-on-error: true") {
		t.Error("a missing summary would stop the release")
	}
	if !strings.Contains(release, "--release-notes ${{ runner.temp }}/body.md") {
		t.Error("GoReleaser does not publish the composed body")
	}
}

func TestTheSummaryCanBeTurnedOffWithoutEditingTheWorkflow(t *testing.T) {
	if !strings.Contains(workflowJob(t, "release-notes"), "vars.AI_RELEASE_NOTES != 'off' && secrets.OPENAI_API_KEY || ''") {
		t.Error("the repository variable AI_RELEASE_NOTES=off no longer withholds the key")
	}
}
