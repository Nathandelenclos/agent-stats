//go:build unix

package files

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularRefusesFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, link} {
		done := make(chan error, 1)
		go func() {
			f, err := OpenRegular(path)
			if err == nil {
				f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegular) {
				t.Errorf("%s: err = %v, want ErrNotRegular", path, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: OpenRegular blocked on a FIFO", path)
		}
	}
	if _, err := OpenRegular(dir); !errors.Is(err, ErrNotRegular) {
		t.Errorf("directory: %v", err)
	}
	regular := filepath.Join(dir, "log.jsonl")
	if err := os.WriteFile(regular, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRegular(regular)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 3)
	if n, err := f.Read(buf); err != nil || n != 3 {
		t.Errorf("read = %d %v", n, err)
	}
}
