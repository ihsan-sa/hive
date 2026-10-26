package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A can't-run at a main that isn't tied to its head is kept per main from
// the journal, so a restart remembers it; a judging run there clears it,
// and one tied to its own head never sets it.
func TestGateAtMainOutlivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	open := func() (*Effects, *Journal) {
		j, err := OpenJournal(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
		return NewEffects(Workspace{}, j, nil, nil, nil, nil), j
	}
	e, j := open()
	e.RecordGate(GateResult{Project: "hive", Head: "h1", Main: "m1", Outcome: GateCantRun, Reason: "the control fails", MainFails: true, At: time.Now()})
	e.RecordGate(GateResult{Project: "hive", Head: "h2", Main: "m2", Outcome: GateCantRun, Reason: "its own build", Alone: true, At: time.Now()})
	j.Close()
	e, _ = open()
	if g, ok := e.GateAtMain("hive", "m1"); !ok || !g.MainFails || g.Head != "h1" {
		t.Fatalf("after a restart: %+v %v, want main's failure remembered", g, ok)
	}
	if _, ok := e.GateAtMain("hive", "m2"); ok {
		t.Fatal("a can't-run tied to its own head held the main")
	}
	e.RecordGate(GateResult{Project: "hive", Head: "h3", Main: "m1", Outcome: GateRollback, Reason: "worse", At: time.Now()})
	if _, ok := e.GateAtMain("hive", "m1"); ok {
		t.Fatal("a run that judged at main left it held")
	}
}

// A measured verdict against an older main still stands at a newer one while
// main's commits since touch neither the change's files nor what the gate
// and score measure with; a commit that does, or no measurement, ends it.
func TestAMeasuredGateVerdictStandsWhileMainMovesElsewhere(t *testing.T) {
	c, f, p, m1 := setupProject(t)
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(p.Repo), tmp)
	commit := func(file, body string) string {
		os.MkdirAll(filepath.Dir(filepath.Join(tmp, file)), 0o755)
		os.WriteFile(filepath.Join(tmp, file), []byte(body), 0o644)
		mustGit(t, tmp, "add", "-A")
		mustGit(t, tmp, "commit", "-q", "-m", file)
		return mustGit(t, tmp, "rev-parse", "HEAD")
	}
	head := commit("code.txt.go", "package demo // better\n")
	mustGit(t, tmp, "reset", "-q", "--hard", m1)
	m2 := commit("README.md", "# demo, elsewhere\n")
	m3 := commit("tests/bench.txt", "a harder benchmark\n")
	mustGit(t, tmp, "reset", "-q", "--hard", m2)
	m4 := commit("code.txt.go", "package demo // main's own\n")
	for i, ref := range []string{head, m3, m4} {
		mustGit(t, tmp, "push", "-q", "origin", ref+":refs/heads/b"+string(rune('0'+i)))
	}
	mustGit(t, c.WS.Project(p.Name), "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")

	e := c.Effects
	e.RecordGate(GateResult{Project: p.Name, Head: head, Main: m1, Outcome: GateKeep, Reason: "better", Measured: true, At: time.Now()})
	if g, ok := e.StandingGate(p, head, m2); !ok || g.Main != m1 || !e.Kept(p, head, m2) {
		t.Fatalf("at a main that moved only README.md: %+v %v, want the keep at %s", g, ok, Short(m1))
	}
	if _, ok := e.StandingGate(p, head, m3); ok || e.Kept(p, head, m3) {
		t.Fatal("main changed tests/, which the score measures with, and the keep still stood")
	}
	if _, ok := e.StandingGate(p, head, m4); ok {
		t.Fatal("main changed the candidate's own file, and the keep still stood")
	}
	other := commit("other.txt", "x\n")
	mustGit(t, tmp, "push", "-q", "origin", other+":refs/heads/b9")
	mustGit(t, c.WS.Project(p.Name), "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
	e.RecordGate(GateResult{Project: p.Name, Head: other, Main: m1, Outcome: GateKeep, Reason: "stand-in", At: time.Now()})
	if e.Kept(p, other, m2) {
		t.Fatal("a verdict that measured nothing stood at another main")
	}
}
