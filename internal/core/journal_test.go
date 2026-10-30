package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// Since reads from the last mark before the cursor, not from the start, and
// still returns every later event: those written before the journal was
// opened, those appended since, and one whose seq is out of order behind a
// mark (two writers).
func TestJournalSinceSeeksPastWhatItCantWant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	pad := strings.Repeat("x", 1000)
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3000 { // about 3 MB: marks at each MB
		j.MustAppend("a", map[string]string{"pad": pad})
	}
	j.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":2990,"kind":"late"}` + "\n") // a second writer, behind
	f.Close()
	if j, err = OpenJournal(path); err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if len(j.marks) < 2 {
		t.Fatalf("%d marks over 3 MB, want 2 or more", len(j.marks))
	}
	for range 1500 {
		j.MustAppend("b", map[string]string{"pad": pad})
	}
	for _, after := range []int64{0, 1, 1200, 2500, 2989, 3000, 4000, 4501} {
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
			t.Fatalf("Since(%d): %d events, want %d", after, len(got), len(want))
		}
	}
	// A late cursor starts near the end: reading the whole file would decode
	// all 4500 events, the last mark leaves at most a MB of them.
	from := int64(0)
	for _, m := range j.marks {
		if m.seq <= 4400 {
			from = m.off
		}
	}
	if fi, _ := os.Stat(path); fi.Size()-from > 2*markEvery {
		t.Fatalf("Since(4400) starts %d bytes from the end", fi.Size()-from)
	}
}
