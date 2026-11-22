package logfiles

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestZedDatabaseWithoutOptionalColumns(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "threads.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, summary TEXT NOT NULL,
		updated_at TEXT NOT NULL, data_type TEXT NOT NULL, data BLOB NOT NULL);
		INSERT INTO threads VALUES ('child', '', '2026-02-01T10:00:10Z', 'json',
			'{"version":"0.3.0","messages":[],"subagent_context":{"parent_thread_id":"parent","depth":1}}');
		INSERT INTO threads VALUES ('foreign', '', '2026-02-01T10:00:10Z', 'other', X'00')`); err != nil {
		t.Fatal(err)
	}

	logs := NewFileSessionLogs([]Source{{Harness: kernel.Zed, Root: root, Database: zedDatabase{}}})
	found, problems, err := logs.List()
	if err != nil || len(found) != 1 || len(problems) != 1 || !errors.Is(problems[0], ErrUnknownSchema) {
		t.Fatalf("listing: %v / %v / %v", found, problems, err)
	}
	got, err := logs.Read(found[0])
	if err != nil || got.Session.ParentID.String() != "zed:parent" || got.Session.AgentType.String() != "subagent" ||
		!got.Session.Start.IsZero() || got.Session.Folder != "" {
		t.Fatalf("old database columns: %+v, %v", got.Session, err)
	}

	// A writer can change a listed row before it is read. The next listing must retry it.
	if _, err := db.Exec(`UPDATE threads SET summary = 'Changed' WHERE id = 'child'`); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Read(found[0]); !errors.Is(err, ErrDatabaseUnreadable) {
		t.Fatalf("reading the previous revision: %v", err)
	}
}

func TestZedPayloadBoundsAndVersions(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	for _, tc := range []struct {
		name, encoding string
		data           []byte
		want           error
		message        string
	}{
		{"oversized zstd", "zstd", encoder.EncodeAll([]byte(strings.Repeat(" ", 1024)), nil), ErrLogTooLarge, ""},
		{"oversized JSON", "json", []byte(strings.Repeat(" ", 1024)), ErrLogTooLarge, ""},
		{"future version", "json", []byte(`{"version":"0.4.0","messages":[]}`), ErrUnknownSchema, ""},
		{"invalid zstd", "zstd", []byte("damaged"), nil, "invalid Zed zstd stream"},
		{"invalid JSON", "json", []byte(`{"private sample"`), nil, "invalid Zed thread JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var thread zedThread
			err := decodeZedThread(tc.data, tc.encoding, 128, &thread)
			if tc.want != nil && !errors.Is(err, tc.want) || tc.message != "" && (err == nil || err.Error() != tc.message) {
				t.Fatalf("decoding: %v", err)
			}
		})
	}
}
