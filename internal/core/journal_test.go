package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// Appends at once share fsyncs, and each returns only once its own line is
// covered by one: every line is synced, whole and in seq order.
func TestJournalAppendsShareTheirFsync(t *testing.T) {
	durable = true
	defer func() { durable = false }()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			e := j.MustAppend("a", map[string]int{"n": 1})
			j.syncMu.Lock()
			synced := j.synced
			j.syncMu.Unlock()
			if raw, _ := os.ReadFile(path); synced < int64(len(raw)) && !strings.Contains(string(raw[:synced]), `"seq":`+strconv.FormatInt(e.Seq, 10)+",") {
				t.Errorf("seq %d returned before an fsync covered it", e.Seq)
			}
		})
	}
	wg.Wait()
	var seqs []int64
	j.Replay(func(e Event) { seqs = append(seqs, e.Seq) })
	if len(seqs) != 40 || !slices.IsSorted(seqs) || j.synced != j.size {
		t.Fatalf("%d events %v, synced %d of %d", len(seqs), seqs, j.synced, j.size)
	}
}

// A Replay of some kinds gives the same events as filtering the whole file,
// in file order, for lines read at open and appended since, with a "*" kind
// matching a start; once another writer grew the file, it reads the whole
// file and still sees that writer's line.
func TestJournalReplaysKindsFromItsIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"a", "release.ci_waiting", "release.ci_failed", "b"}
	for i := range 200 {
		j.MustAppend(kinds[i%len(kinds)], map[string]int{"i": i})
	}
	j.Close()
	if j, err = OpenJournal(path); err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for i := range 100 {
		j.MustAppend(kinds[i%len(kinds)], map[string]int{"i": i})
	}
	seqs := func(kinds ...string) (out []int64) {
		j.Replay(func(e Event) { out = append(out, e.Seq) }, kinds...)
		return out
	}
	var want []int64
	j.Replay(func(e Event) {
		if e.Kind == "a" || strings.HasPrefix(e.Kind, "release.ci_") {
			want = append(want, e.Seq)
		}
	})
	if got := seqs("release.ci_*", "a"); len(want) != 225 || !slices.Equal(got, want) {
		t.Fatalf("Replay(a, release.ci_*): %d events, want %d of 225 in order", len(got), len(want))
	}
	if got := seqs("none"); len(got) != 0 {
		t.Fatalf("Replay(none): %d events", len(got))
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":999,"kind":"a"}` + "\n") // a second writer
	f.Close()
	if got := seqs("a"); len(got) != 76 || got[len(got)-1] != 999 {
		t.Fatalf("after another writer: %d events of a, want 76 ending with its 999", len(got))
	}
}

// A replay reads Append's own lines without json.Unmarshal; what it gets
// must be what json.Unmarshal would give, for any line, and a line in another
// layout still goes through json.Unmarshal.
func TestJournalReadsItsLinesAsUnmarshalWould(t *testing.T) {
	big := `{"text":"` + strings.Repeat("x", 3<<20) + `"}`
	lines := []string{
		`{"seq":1,"at":"2026-10-30T01:02:03.123456789Z","kind":"task.saved","data":{"id":"a","n":[1,2]}}`,
		`{"seq":2,"at":"2026-10-30T01:02:03Z","kind":"hive.started"}`,
		`{"seq":3,"at":"2026-10-30T01:02:03+02:00","kind":"run.created","data":null}`,
		`{"seq":4,"at":"2026-10-30T01:02:03Z","kind":"chat.posted","data":"s"}`,
		`{"kind":"x.y","seq":5,"at":"2026-10-30T01:02:03Z","data":{"a":1}}`,
		` {"seq":6,"at":"2026-10-30T01:02:03Z","kind":"x.y","data":{}} `,
		`{"seq":7,"at":"2026-10-30T01:02:03Z","kind":"x.y","data":{}}`,
		`{"seq":8,"at":"2026-10-30T01:02:03Z","kind":"","data":{}}`,
		`{"seq":9,"at":"not a time","kind":"x.y","data":{}}`,
		`{"seq":10,"at":"2026-10-30T01:02:03Z","kind":"big.one","data":` + big + `}`,
		`not json`,
	}
	for _, l := range lines {
		var want Event
		wantOK := json.Unmarshal([]byte(l), &want) == nil && want.Kind != ""
		got, ok := parseEvent([]byte(l), nil)
		if ok != wantOK || ok && (got.Seq != want.Seq || !got.At.Equal(want.At) || got.Kind != want.Kind || string(got.Data) != string(want.Data)) {
			t.Errorf("%.80s: got %v %+v, want %v %+v", l, ok, got.Kind, wantOK, want.Kind)
		}
	}

	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"+`{"seq":11,"at":"2026-10-30T01:02:03Z","kind":"torn`), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var all, some []string
	j.Replay(func(e Event) { all = append(all, e.Kind) })
	j.ReplayExcept(func(e Event) {
		some = append(some, e.Kind)
		if e.Kind == "big.one" && string(e.Data) != big {
			t.Errorf("a line longer than the read buffer came back %d bytes, want %d", len(e.Data), len(big))
		}
	}, "task.saved", "run.created")
	if want := "task.saved hive.started run.created chat.posted x.y x.y x.y big.one"; strings.Join(all, " ") != want {
		t.Errorf("Replay got %v, want %s", all, want)
	}
	if want := "hive.started chat.posted x.y x.y x.y big.one"; strings.Join(some, " ") != want {
		t.Errorf("ReplayExcept got %v, want %s", some, want)
	}
	if j.Seq() != 10 {
		t.Errorf("seq %d, want 10", j.Seq())
	}
}
