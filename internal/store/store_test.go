package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/ingest"
	"github.com/Nathandelenclos/agent-stats/internal/pricing"
)

const line1 = `{"type":"assistant","timestamp":"2026-01-01T10:00:00Z","cwd":"/repo","message":{"id":"m1","model":"m","usage":{"input_tokens":1000,"output_tokens":100}}}` + "\n"
const line2 = `{"type":"assistant","timestamp":"2026-01-02T10:00:00Z","message":{"id":"m2","model":"unknown","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"

func TestSync(t *testing.T) {
	root := t.TempDir()
	st, err := Open(filepath.Join(t.TempDir(), "index.db"), pricing.Table{"m": {0.001, 0.01, 0, 0}}, "me@host")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := []ingest.Source{{Harness: "claude", Root: root, Match: func(p string) bool { return strings.HasSuffix(p, ".jsonl") }, Parse: ingest.ParseClaude}}
	log := filepath.Join(root, "proj", "s1.jsonl")
	os.MkdirAll(filepath.Dir(log), 0o755)

	os.WriteFile(log, []byte(line1), 0o644)
	if rep, err := st.Sync(src, nil); err != nil || rep.Parsed != 1 {
		t.Fatalf("first sync: %+v %v", rep, err)
	}
	u, _ := st.Total(Filter{})
	if u.Requests != 1 || u.Cost != 2 || u.Unpriced != 0 {
		t.Fatalf("total = %+v", u)
	}

	if rep, _ := st.Sync(src, nil); rep.Parsed != 0 {
		t.Errorf("unchanged file parsed again: %+v", rep)
	}

	os.WriteFile(log, []byte(line1+line2), 0o644)
	os.Chtimes(log, time.Now(), time.Now().Add(time.Second))
	st.Sync(src, nil)
	u, _ = st.Total(Filter{})
	if u.Requests != 2 || u.Unpriced != 1 {
		t.Fatalf("after append: %+v", u)
	}
	if u, _ := st.Total(Filter{Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()}); u.Requests != 1 {
		t.Errorf("since filter: %+v", u)
	}

	// A harness moving the log (Codex archiving) must not double count it.
	moved := filepath.Join(root, "archive", "s1.jsonl")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	os.Rename(log, moved)
	st.Sync(src, nil)
	if u, _ := st.Total(Filter{}); u.Requests != 2 {
		t.Errorf("after move: %+v", u)
	}

	rows, err := st.UsageBy(Filter{}, "r.model")
	if err != nil || len(rows) != 2 || rows[0].Key != "m" {
		t.Errorf("by model: %+v %v", rows, err)
	}
}
