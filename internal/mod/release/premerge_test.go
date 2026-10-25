package release

import (
	"os"
	"os/exec"
	"path/filepath"
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

// The rehearsal really builds this tree as release.yml does and hands a
// running Hive built from it over to the candidate.
func TestRehearsalOnThisTree(t *testing.T) {
	if testing.Short() || runtime.GOOS != "linux" {
		t.Skip("builds the Hive three times and runs two daemons; Linux, not -short")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "hive"), "./cmd/hive")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmd := exec.Command("/bin/sh", "-c", rehearsal("me/hive"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "to the candidate passes") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The repository's name can't carry code into the script.
func TestRehearsalRepoIsInert(t *testing.T) {
	if got := safeRepo(`me/hive"; rm -rf $HOME; echo "`); got != "me/hiverm-rfHOMEecho" {
		t.Fatalf("safeRepo kept %q", got)
	}
}
