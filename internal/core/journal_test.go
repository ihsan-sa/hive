package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Two daemons on one journal each count seq from their own start, so seqs
// stop rising. Opening it says how many such events are new since the last
// report, and the next seq is past every one written.
func TestJournalCountsSeqsThatDidntRise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := `{"seq":1,"kind":"a"}
{"seq":2,"kind":"a"}
{"seq":3,"kind":"a"}
{"seq":2,"kind":"b"}
{"seq":3,"kind":"b"}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, total := j.Breaks(); fresh != 2 || total != 2 {
		t.Fatalf("breaks %d new of %d, want 2 of 2", fresh, total)
	}
	if e := j.MustAppend("alert.raised", map[string]any{"kind": "journal", "breaks": 2}); e.Seq != 4 {
		t.Fatalf("next seq %d, want 4", e.Seq)
	}
	j.Close()
	if j, err = OpenJournal(path); err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if fresh, total := j.Breaks(); fresh != 0 || total != 2 {
		t.Fatalf("after the report: breaks %d new of %d, want 0 of 2", fresh, total)
	}
}
