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
	if !strings.Contains(release, "needs: front") {
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
