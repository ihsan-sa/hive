package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// Since starts near its seq, not at the top of the file, and gives the same
// events a full read would: across a reopen, with a torn line and with seqs
// that didn't rise.
func TestJournalSinceMatchesAFullRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	var lines []byte
	for i := 1; i <= 3*markEvery; i++ {
		lines = fmt.Appendf(lines, `{"seq":%d,"kind":"a"}`+"\n", i)
		if i == markEvery+10 { // a second writer's seqs, past the next mark
			lines = fmt.Appendf(lines, `{"seq":%d,"kind":"b"}`+"\n", 2*markEvery+50)
		}
	}
	lines = append(lines, `{"seq":99999,"ki`...) // torn
	if err := os.WriteFile(path, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(j *Journal) {
		t.Helper()
		for _, after := range []int64{0, 5, markEvery, markEvery + 20, 2*markEvery + 40, 2*markEvery + 60, j.Seq() - 1, j.Seq()} {
			var want []int64
			j.Replay(func(e Event) {
				if e.Seq > after {
					want = append(want, e.Seq)
				}
			})
			var got []int64
			for _, e := range j.Since(after) {
				got = append(got, e.Seq)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("Since(%d): %d events, a full read has %d", after, len(got), len(want))
			}
		}
	}
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.marks) < 3 {
		t.Fatalf("%d marks, want one every %d lines", len(j.marks), markEvery)
	}
	check(j)
	for range 2 * markEvery {
		j.MustAppend("c", nil)
	}
	check(j)
	if evs := j.Since(j.Seq() - 1); len(evs) != 1 || evs[0].Kind != "c" {
		t.Fatalf("the last event: %+v", evs)
	}
	j.Close()
	if j, err = OpenJournal(path); err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	check(j)
}
