//go:build unix

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"
)

// freeAddr is a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// getJSON reads an API answer of the server at addr into v.
func getJSON(addr, path string, v any) error {
	res, err := http.Get("http://" + addr + path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

func TestTheServerStartedFromAProjectOnAnEmptyIndexOpensOnItOnceTheLogsAreRead(t *testing.T) {
	// Given sessions logged in the app and in a sibling project, an index that holds none yet, and a server started from the app
	w := newScopedWorkplace(t)
	t.Chdir(w.app)
	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() { done <- runServe([]string{"--db", w.db, "--addr", addr}) }()

	// When the indexing of the logs ends
	var meta struct {
		Project  string
		Defaults struct{ Scope string }
	}
	deadline := time.Now().Add(20 * time.Second)
	for meta.Project == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		_ = getJSON(addr, "/api/meta", &meta)
	}

	// Then the page opens on the app, and the report of the project counts its sessions and not the sibling's
	if meta.Project != "app" || meta.Defaults.Scope != "current" {
		t.Errorf("meta = %+v", meta)
	}
	var report struct{ Total struct{ Requests int } }
	if err := getJSON(addr, "/api/report?project=current", &report); err != nil || report.Total.Requests != 3 {
		t.Errorf("the project's report counts %d requests, %v", report.Total.Requests, err)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil { // what Ctrl-C does
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
}
