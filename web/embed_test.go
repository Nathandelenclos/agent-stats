package webui

import (
	"io/fs"
	"testing"
)

// Whatever the build (with the web tag, the built front; without it, the notice), the server finds a page to serve.
func TestAssetsHoldTheEntryPage(t *testing.T) {
	info, err := fs.Stat(Assets(), "index.html")
	if err != nil || info.IsDir() || info.Size() == 0 {
		t.Fatalf("index.html = %v, %v", info, err)
	}
}
