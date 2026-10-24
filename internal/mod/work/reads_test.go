package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// endRead ends reviewer run id with a verdict carrying its token.
func endRead(m *Module, id, verdict, extra string) {
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"` + verdict + `","summary":"` + verdict + ` from ` + id + `"` + extra + `,"token":"` + reviewToken(id) + `"}`)}})
}

// A small code change no guard names goes from its tests to the merge with no
// reviewer seat; one over the line is read.
func TestUnreadSkipsTheReader(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"main.go": "package main\n"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerging || !logged(task, "merges on its tests and checks with no read") {
		t.Fatalf("stage %q, log %v", task.Stage, task.Log)
	}

	m, task, p, _ = gateRig(t, false, map[string]string{"main.go": strings.Repeat("x\n", core.DefaultUnreadMax+1)})
	if err := m.test(task, p); err != nil || task.Stage != StageReviewing {
		t.Fatalf("a change over the line: stage %q, err %v", task.Stage, err)
	}
}

// A change a reviewer already read stays read when a fix round shrinks it
// under the line: shrinking never skips the re-read.
func TestUnreadNotAfterARead(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"main.go": "package main\n"})
	task.Reviewer = "r-t1-old-0"
	if err := m.test(task, p); err != nil || task.Stage != StageReviewing {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
}

// A fix round's re-read gets the earlier read's findings and the worker's
// delta since that head, and is asked to judge those rather than read it all.
func TestReReadSeesFindingsAndDelta(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"main.go": strings.Repeat("first\n", core.DefaultUnreadMax+1)})
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": "w1", "turn": 1, "status": core.StatusDone, "result": harness.Result{Text: "done"}})
	if err := m.test(task, p); err != nil || task.Stage != StageReviewing {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	if err := m.review(task, p); err != nil {
		t.Fatal(err)
	}
	first := task.Reviewer
	r1, _ := m.C.Runs.Get(first)
	if _, ok := r1.Inputs["delta.patch"]; ok || strings.Contains(r1.Inputs["brief.md"], "## A re-read") {
		t.Fatalf("the first read is of the whole change: %v", r1.Inputs["brief.md"])
	}
	endRead(m, first, "fix", `,"findings":[{"file":"main.go","issue":"FINDING-ONE: the loop is off by one"}]`)
	sha1 := task.SHA
	if err := m.review(task, p); err != nil || task.Stage != StageWorking || task.ReadAt != sha1 || !strings.Contains(task.ReadSaid, "FINDING-ONE") {
		t.Fatalf("stage %q, read at %q, said %q, err %v", task.Stage, task.ReadAt, task.ReadSaid, err)
	}

	// The worker fixes it with a commit of its own.
	w, _ := m.C.Runs.Get("w1")
	os.WriteFile(filepath.Join(w.Checkout, "fix.go"), []byte("package main // DELTA-LINE\n"), 0o644)
	must(t, w.Checkout, "add", "-A")
	must(t, w.Checkout, "commit", "-q", "-m", "fix")
	main, _ := m.C.Effects.Sync(p)
	sha2, err := m.C.Effects.Extract(p, w, main)
	if err != nil {
		t.Fatal(err)
	}
	task.SHA, task.Diff, task.Stage = sha2, "", StageReviewing
	if err := m.review(task, p); err != nil {
		t.Fatal(err)
	}
	r2, _ := m.C.Runs.Get(task.Reviewer)
	delta, brief := r2.Inputs["delta.patch"], r2.Inputs["brief.md"]
	if !strings.Contains(delta, "DELTA-LINE") || strings.Contains(delta, "first") {
		t.Fatalf("the delta should hold the fix alone:\n%s", delta)
	}
	for _, want := range []string{"## A re-read", "FINDING-ONE", "Judge whether its findings below are answered", core.Short(sha1)} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the re-read's brief lacks %q:\n%s", want, brief)
		}
	}
	if !strings.Contains(r2.Brief, "delta.patch") || !strings.Contains(r2.Brief, "rather than running the suite again") {
		t.Fatalf("the re-read's ask: %s", r2.Brief)
	}
}

// One land is enough for an ordinary change under the second-read line; an
// unsure land asks a second, separate reader, whose ask says why.
func TestSecondReadOnlyWhenUnsure(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"main.go": strings.Repeat("x\n", core.DefaultUnreadMax+1)})
	m.test(task, p)
	m.review(task, p)
	endRead(m, task.Reviewer, "land", "")
	if err := m.review(task, p); err != nil {
		t.Fatal(err)
	}
	if err := m.test(task, p); err != nil || task.Stage != StageMerging {
		t.Fatalf("one sure land should do: stage %q, err %v", task.Stage, err)
	}

	m, task, p, _ = gateRig(t, false, map[string]string{"main.go": strings.Repeat("x\n", core.DefaultUnreadMax+1)})
	m.test(task, p)
	m.review(task, p)
	endRead(m, task.Reviewer, "land", `,"unsure":true`)
	m.review(task, p)
	if err := m.test(task, p); err != nil || task.Stage != StageReviewing {
		t.Fatalf("an unsure land should ask for another read: stage %q, err %v", task.Stage, err)
	}
	m.review(task, p)
	r, _ := m.C.Runs.Get(task.Reviewer)
	if !strings.HasSuffix(r.ID, "-read2") || !strings.Contains(r.Brief, "second, separate read") {
		t.Fatalf("second read %s: %s", r.ID, r.Brief)
	}
}

// A sampled unread merge gets one read after it merged; its fix files one
// follow-up with the findings, once, and the task is done.
func TestSampledReadFilesAFollowUp(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"main.go": "package main\n"})
	main, _ := m.C.Effects.Sync(p)
	m.markUnread(task, p, main)
	if !task.Unread {
		t.Fatal("an unreviewed small change should be marked unread")
	}
	task.Sample, task.Stage, task.MergeSHA = true, StageMerged, main
	if err := m.step(task); err != nil || task.Stage != StageMerged {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	id := "p-t1-" + core.Short(task.SHA) + "-0"
	r, ok := m.C.Runs.Get(id)
	if !ok || !strings.Contains(r.Brief, "Sampled read") {
		t.Fatalf("no sampled read started: %+v", r)
	}
	endRead(m, id, "fix", `,"findings":[{"file":"main.go","issue":"SAMPLED-FINDING"}]`)
	for i := 0; i < 3; i++ {
		if err := m.step(task); err != nil {
			t.Fatal(err)
		}
	}
	follow := m.List(func(x Task) bool { return x.FollowUp == "t1" })
	if task.Stage != StageDone || task.Sample || len(follow) != 1 || !strings.Contains(follow[0].Known, "SAMPLED-FINDING") {
		t.Fatalf("stage %q, sample %v, follow-ups %+v", task.Stage, task.Sample, follow)
	}
	if st := m.readStats(p.Name); len(st.Seats) != 1 || st.Seats[0].Seat != "sampled" || st.Seats[0].Lands != 0 {
		t.Fatalf("stats %+v", st)
	}
}

// One task in n is sampled, by its id, and none at n <= 0.
func TestSampledShare(t *testing.T) {
	n := 0
	for i := 0; i < 1000; i++ {
		if sampled("task-"+string(rune('a'+i%26))+strings.Repeat("x", i/26), 5) {
			n++
		}
	}
	if n < 150 || n > 250 || sampled("x", 0) || sampled("x", -1) || !sampled("x", 1) {
		t.Fatalf("%d of 1000 sampled at 1 in 5", n)
	}
}

// The figures count reads per merge over the window and each seat's land
// rate, flagging a seat that lands nearly everything.
func TestReadStats(t *testing.T) {
	m, _, p, _ := gateRig(t, false, map[string]string{"main.go": "package main\n"})
	for i, id := range []string{"a", "b"} {
		tk := &Task{ID: id, Project: p.Name, Kind: "work", Stage: StageDone, MergeSHA: "m" + id, PR: core.PR{Number: 10 + i}}
		m.save(tk, "")
		m.C.J.MustAppend("effect.merged", map[string]any{"project": p.Name, "pr": 10 + i, "unread": id == "b"})
	}
	for i := 0; i < 25; i++ {
		run := "r-a-x-" + string(rune('a'+i))
		m.C.Runs.Start(core.RunSpec{ID: run, Role: "reviewer", Project: p.Name, Harness: "fake", ReadOnly: true, Task: "a"})
		m.C.J.MustAppend("effect.verdict", core.Verdict{Project: p.Name, Run: run, Verdict: "land"})
	}
	m.C.J.MustAppend("effect.verdict", core.Verdict{Project: p.Name, Run: "s-a-d-security-0", Seat: "security", Verdict: "fix"})
	st := m.readStats(p.Name)
	if st.Merges != 2 || st.Unread != 1 || st.Reads != 25 || st.PerMerge() != 12.5 {
		t.Fatalf("%+v", st)
	}
	line := st.Line()
	for _, want := range []string{"12.5 per merge over the last 2 merges (1 with no read)", "ordinary 25/25 land (100%) !", "security 0/1 land (0%)"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line lacks %q: %s", want, line)
		}
	}
}
