package models_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/models"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func gitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := core.Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// When a trial's outcome is recorded on a project whose project.json names a
// ledger, the Hive writes that ledger from its record as a record task: a
// host-made commit that needs no approval though the ledger is a score path.
// Once main holds it, nothing more is filed.
func TestATrialRecordedRefreshesTheLedger(t *testing.T) {
	a, _, forge := hiveWithForge(t)
	if _, err := forge.EnsureRepo("me/trials", "", false); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	gitOK(t, "", "clone", "-q", forge.RepoPath("me/trials"), tmp)
	os.MkdirAll(filepath.Join(tmp, ".hive"), 0o755)
	os.WriteFile(filepath.Join(tmp, ".hive", "project.json"), []byte(`{"test":"true","ledger":"ledger.json","score":{"command":"go run ./cmd/score","paths":["ledger.json"],"direction":"higher"}}`), 0o644)
	os.WriteFile(filepath.Join(tmp, "ledger.json"), []byte("{}\n"), 0o644)
	gitOK(t, tmp, "add", "-A")
	gitOK(t, tmp, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	gitOK(t, tmp, "push", "-q", "origin", "HEAD:main")
	p, err := a.C.Projects.Add(core.Project{Name: "trials", Repo: "me/trials"})
	if err != nil {
		t.Fatal(err)
	}
	a.C.J.MustAppend("model.trial", models.Trial{Task: "261028-000000-aaaaaaaaaa", Model: "qwen", Project: "trials", Title: "wc on open/qwen", Outcome: "merged", Turns: 3})
	a.C.J.MustAppend("model.trial", models.Trial{Task: "261028-000001-bbbbbbbbbb", Model: "qwen", Project: "lab", Title: "elsewhere", Outcome: "merged"})
	a.Models.Tick()
	a.Models.Tick() // filed once
	recs := a.Work.List(func(t work.Task) bool { return t.Kind == "record" })
	if len(recs) != 1 || recs[0].Project != "trials" || recs[0].Stage != work.StageRecording || !recs[0].Fix() {
		t.Fatalf("record tasks %+v", recs)
	}
	got := a.C.Effects.ReadFile(p, recs[0].SHA, "ledger.json")
	if got != strings.TrimSpace(a.Models.Ledger("trials")) || !strings.Contains(got, `"task": "261028-000000-aaaaaaaaaa"`) || strings.Contains(got, "elsewhere") || !strings.Contains(got, `"qwen": "`) {
		t.Fatalf("ledger written:\n%s", got)
	}
	main, _ := a.C.Effects.Sync(p)
	ch, err := a.C.Effects.Classify(p, main, recs[0].SHA)
	if err != nil || len(ch.Protected) != 0 || !a.C.Effects.Reviewed(p, ch, recs[0].SHA) {
		t.Fatalf("the record needs the person or a read: %+v %v", ch, err)
	}

	// Main comes to hold it (the record merged): the next tick files nothing.
	gitOK(t, tmp, "pull", "-q", "origin", "main")
	gitOK(t, tmp, "fetch", "-q", a.C.WS.Project("trials"), recs[0].SHA)
	gitOK(t, tmp, "merge", "-q", "--ff-only", recs[0].SHA)
	gitOK(t, tmp, "push", "-q", "origin", "HEAD:main")
	tk := recs[0]
	tk.Stage = work.StageDone
	a.C.J.MustAppend("task.saved", tk)
	a.Models.Tick()
	if n := len(a.Work.List(func(t work.Task) bool { return t.Kind == "record" })); n != 1 {
		t.Fatalf("%d record tasks once main held the ledger", n)
	}
}
