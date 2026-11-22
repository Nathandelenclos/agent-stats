package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

func TestZedThreadsReachSessionReports(t *testing.T) {
	// Given native Zed threads stored in both encodings, with one child linked to its parent.
	home := t.TempDir()
	fixtureEnv(t, home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("FLATPAK_XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	root := filepath.Join(home, ".local", "share", "zed")
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support", "Zed")
	case "windows":
		root = filepath.Join(home, "AppData", "Local", "Zed")
	}
	dir := filepath.Join(root, "threads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", filepath.Join(dir, "threads.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
		CREATE TABLE threads (id TEXT PRIMARY KEY, summary TEXT NOT NULL, updated_at TEXT NOT NULL,
			data_type TEXT NOT NULL, data BLOB NOT NULL, parent_id TEXT, folder_paths TEXT,
			folder_paths_order TEXT, created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile("../testdata/zed/thread.json")
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	for _, row := range []struct {
		id, parent, encoding string
		data                 []byte
	}{
		{"parent", "", "zstd", encoder.EncodeAll(payload, nil)},
		{"child", "parent", "json", payload},
	} {
		if _, err := source.Exec(`INSERT INTO threads VALUES (?, 'Sample thread', '2026-02-01T10:00:10Z',
			?, ?, NULLIF(?, ''), ?, '0', '2026-02-01T10:00:00Z')`,
			row.id, row.encoding, row.data, row.parent, home); err != nil {
			t.Fatal(err)
		}
	}

	// When the normal index and session report read this home.
	indexPath := filepath.Join(t.TempDir(), "index.db")
	rep, _ := moreIndex(t, home, indexPath)
	if len(rep.Errors) != 0 || rep.Indexed != 2 {
		t.Fatalf("first index: %+v", rep)
	}
	usage, err := usagedb.OpenReadOnly(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	sessions, err := usage.Sessions().Sessions(context.Background(), report.Filter{Harness: kernel.Zed})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Zed sessions: %+v, %v", sessions, err)
	}
	got := sessionShown(t, indexPath, "zed:parent")
	// Then the run contains two tool calls, but no fabricated model requests.
	if got.Folder != home || got.Title != "Sample thread" || got.SubagentCount != 1 || got.Requests != 0 ||
		got.Start.UTC().Format("15:04:05") != "10:00:00" || got.End.UTC().Format("15:04:05") != "10:00:10" ||
		len(got.Tools) != 1 || got.Tools[0].Name != "read_file" || got.Tools[0].Calls != 2 ||
		got.Tools[0].Errors != 2 || got.Tools[0].ArgsChars != 2*int64(len(`{"path":"sample"}`)) ||
		got.Tools[0].ResultChars != 2*int64(len(`[{"Text":"missing"}]`)) || !got.Tools[0].Last.IsZero() {
		t.Fatalf("Zed run: %+v", got)
	}
	if len(got.Prompts) != 1 || got.Prompts[0].Text != "Read the sample." || !got.Prompts[0].At.IsZero() {
		t.Fatalf("Zed prompts: %+v", got.Prompts)
	}

	// A change confined to a single row in the WAL reindexes only that thread, even at the same size and timestamp.
	if _, err := source.Exec(`UPDATE threads SET data = ? WHERE id = 'child'`,
		bytes.ReplaceAll(payload, []byte("read_file"), []byte("list_file"))); err != nil {
		t.Fatal(err)
	}
	if rep, _ := moreIndex(t, home, indexPath); len(rep.Errors) != 0 || rep.Indexed != 1 {
		t.Fatalf("updated index: %+v", rep)
	}
	if got := sessionShown(t, indexPath, "zed:parent"); len(got.Tools) != 2 {
		t.Fatalf("updated tools: %+v", got.Tools)
	}
	if rep, _ := moreIndex(t, home, indexPath); len(rep.Errors) != 0 || rep.Indexed != 0 {
		t.Fatalf("unchanged index: %+v", rep)
	}
}
