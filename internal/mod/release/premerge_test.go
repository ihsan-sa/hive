package release

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// A change that trips the release scan goes back before it merges, naming
// where; one that only main's scan trips too, or that the scan can't judge
// for want of its lists, goes on with a note.
func TestPreMergeScan(t *testing.T) {
	m, _, _, _ := selfHive(t)
	m.Scan = nil
	m.Rehearse = func(core.Project, string) core.Check { return core.Check{OK: true} }
	p, _ := m.C.Projects.Get("hive")
	main := selfClone(t, m, map[string]string{scan.PublicList: "**\n", "README.md": "The Hive.\n"})
	sha := selfClone(t, m, map[string]string{"docs/a.md": "thanks alice-k\n"})
	t.Setenv(scan.HandlesEnv, "")
	if back, note := m.PreMerge(p, sha, main); back != "" || !strings.Contains(note, "scan was skipped") {
		t.Fatalf("no handle list here: back %q, note %q", back, note)
	}
	t.Setenv(scan.HandlesEnv, "alice-k")
	back, note := m.PreMerge(p, sha, main)
	if !strings.Contains(back, "release scan") || !strings.Contains(back, "docs/a.md:1: handle 1") || strings.Contains(back, "alice-k") || note != "" {
		t.Fatalf("a handle in a published file: back %q, note %q", back, note)
	}
	if back, note := m.PreMerge(p, sha, sha); back != "" || !strings.Contains(note, "fails on main") {
		t.Fatalf("main trips it too: back %q, note %q", back, note)
	}
	if back, _ := m.PreMerge(core.Project{Name: "other"}, sha, main); back != "" {
		t.Fatalf("a project not the Hive's own was held: %q", back)
	}
}

// A change that breaks the release build or the update to it goes back with
// the rehearsal's output; one main breaks too goes on, and so does one whose
// rehearsal couldn't run.
func TestPreMergeUpdate(t *testing.T) {
	m, _, _, _ := selfHive(t)
	p, _ := m.C.Projects.Get("hive")
	red := map[string]core.Check{
		"cand": {Output: "hive-premerge: the update from hive v0.1.110: the candidate never came up"},
		"both": {Output: "hive-premerge: the release build for linux/arm64 fails"},
		"main": {OK: true},
		"bad":  {Output: "hive-premerge: the release build for linux/arm64 fails"},
		"full": {CantRun: "can't run: the disk or a quota is full where checks run"},
	}
	m.Rehearse = func(_ core.Project, sha string) core.Check { return red[sha] }
	if back, _ := m.PreMerge(p, "cand", "main"); !strings.Contains(back, "the candidate never came up") || !strings.Contains(back, "update") {
		t.Fatalf("a broken update went on: %q", back)
	}
	if back, note := m.PreMerge(p, "both", "bad"); back != "" || !strings.Contains(note, "fails on main") {
		t.Fatalf("main breaks it too: back %q, note %q", back, note)
	}
	if back, note := m.PreMerge(p, "full", "main"); back != "" || !strings.Contains(note, "disk") {
		t.Fatalf("a rehearsal that couldn't run: back %q, note %q", back, note)
	}
	if back, note := m.PreMerge(p, "main", "main"); back != "" || note != "" {
		t.Fatalf("a green rehearsal: back %q, note %q", back, note)
	}
}

// The rehearsal is a script sh can read, and a commit with no cmd/hive runs
// none of it: no sandboxed check is started for it.
func TestRehearsalScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if out, err := exec.Command("/bin/sh", "-n", "-c", rehearsal("me/hive")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m, _, _, _ := selfHive(t)
	p, _ := m.C.Projects.Get("hive")
	sha := selfClone(t, m, map[string]string{"README.md": "The Hive.\n"})
	if c := m.rehearse(p, sha); c.Kind != "none" {
		t.Fatalf("rehearsed a commit with no cmd/hive: %+v", c)
	}
	if _, ok := m.C.Effects.GetCheck(p.Name, sha, "release"); ok {
		t.Fatal("a release check was recorded for it")
	}
}

// The repository's name can't carry code into the script.
func TestRehearsalRepoIsInert(t *testing.T) {
	if got := safeRepo(`me/hive"; rm -rf $HOME; echo "`); got != "me/hiverm-rfHOMEecho" {
		t.Fatalf("safeRepo kept %q", got)
	}
}
