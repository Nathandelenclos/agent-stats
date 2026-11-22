package cli

import (
	"strings"
	"testing"

	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

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
