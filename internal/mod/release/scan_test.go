package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// alerts are the texts of the release alerts raised so far.
func alerts(m *Module) (out []string) {
	m.C.J.Replay(func(e core.Event) {
		var x struct{ Kind, Text string }
		if e.Kind == "alert.raised" && e.Decode(&x) == nil && x.Kind == "release" {
			out = append(out, x.Text)
		}
	})
	return out
}

// selfClone makes the host clone of the self project with files committed,
// and returns its head.
func selfClone(t *testing.T, m *Module, files map[string]string) string {
	dir := m.C.WS.Project("hive")
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "x"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, _ := core.Git(dir, "rev-parse", "HEAD")
	return sha
}

// A release whose scan finds something is never cut, and the alert says
// where, never what.
func TestScanBlocksTheRelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	m.Scan = func(core.Project, string) error { return os.ErrPermission }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if len(n.got) != 0 || journaled(m, "hive.released") {
		t.Fatalf("released past a scan that blocks: %v", n.got)
	}
	if a := alerts(m); len(a) != 1 || !strings.Contains(a[0], "scan blocks it") {
		t.Fatalf("want one alert that the scan blocked it, got %q", a)
	}
}

// The release's own scan reads the commit's public list and the Hive's
// private lists: no handle list blocks, a clean commit passes, a key or a
// handle in a published file blocks, and one in a file the list leaves out
// doesn't.
func TestReleaseScanAtTheCommit(t *testing.T) {
	m, _, _, _ := selfHive(t)
	m.Scan = nil
	p, _ := m.C.Projects.Get("hive")
	key := "ghp_" + strings.Repeat("a1B2c3D4e5F6", 3)
	sha := selfClone(t, m, map[string]string{
		scan.PublicList:  "**\n!secret/**\n",
		"README.md":      "The Hive.\n",
		"secret/keys.md": key + "\nby alice-k\n",
	})
	t.Setenv(scan.HandlesEnv, "")
	if err := m.scan(p, sha); err == nil || !strings.Contains(err.Error(), "no handle list") {
		t.Fatalf("no handle list: %v", err)
	}
	os.WriteFile(filepath.Join(m.C.WS.Private(), scan.HandlesFile), []byte("alice-k\n"), 0o600)
	if err := m.scan(p, sha); err != nil {
		t.Fatalf("a clean commit: %v", err)
	}
	sha = selfClone(t, m, map[string]string{"docs/a.md": "thanks alice-k\n", "cmd/x.go": "var k = \"" + key + "\" // fake token\n"})
	err := m.scan(p, sha)
	if err == nil || !strings.Contains(err.Error(), "docs/a.md:1: handle 1") || !strings.Contains(err.Error(), "cmd/x.go:1: a GitHub token") {
		t.Fatalf("want the handle and the key, got %v", err)
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), key) {
		t.Fatalf("the failure prints what it found: %v", err)
	}
}

// A missing handle list reaches the person as one standing ask in Waiting,
// not an alert on every check; their done tries the release again, and a
// scan that reads a list answers the ask.
func TestNoHandleListAsksThePersonOnce(t *testing.T) {
	m, n, merged, land := selfHive(t)
	m.C.Person.Handle("scan_handles", m.onHandles) // as New does
	missing := true
	m.Scan = func(core.Project, string) error {
		if missing {
			return fmt.Errorf("%w: put one handle per line in the list", scan.ErrNoHandles)
		}
		return nil
	}
	merged(1, "a1", "m1")
	land("a1", "r1")
	for range 3 {
		m.AfterKept("hive", "m1")
	}
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if a := alerts(m); len(a) != 0 {
		t.Fatalf("a missing handle list alerts: %q", a)
	}
	open := m.C.Person.Open()
	if len(open) != 1 || open[0].Kind != "scan_handles" || !strings.Contains(open[0].Detail, "one handle per line") {
		t.Fatalf("want one standing handle-list ask, got %+v", open)
	}
	if len(n.got) != 0 || journaled(m, "hive.released") {
		t.Fatalf("released with no handle list: %v", n.got)
	}
	// Done with the list still missing asks again; done once it's there releases.
	m.C.Person.Answer(open[0].ID, "done", "")
	again := waitFor(t, func() []core.Ask { return m.C.Person.Open() }, func(a []core.Ask) bool { return len(a) == 1 && a[0].ID != open[0].ID })
	missing = false
	m.C.Person.Answer(again[0].ID, "done", "")
	waitFor(t, func() bool { return journaled(m, "hive.released") }, func(ok bool) bool { return ok })
	if !journaled(m, "hive.released") {
		t.Fatal("done with the list written didn't release")
	}
	if open := m.C.Person.Open(); len(open) != 0 {
		t.Fatalf("asks left open after the release: %+v", open)
	}
}

// A scan found in its own run answers the standing ask with no word from
// the person.
func TestHandleListFoundAnswersTheAsk(t *testing.T) {
	m, _, merged, land := selfHive(t)
	m.Scan = func(core.Project, string) error { return scan.ErrNoHandles }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if len(m.C.Person.Open()) != 1 {
		t.Fatal("no handle-list ask")
	}
	m.Scan = func(core.Project, string) error { return nil }
	m.AfterKept("hive", "m1")
	if open := m.C.Person.Open(); len(open) != 0 || !journaled(m, "hive.released") {
		t.Fatalf("the ask stands after a scan read a list: %+v", open)
	}
}

// waitFor polls get until ok, as person handlers run on their own goroutine.
func waitFor[T any](t *testing.T, get func() T, ok func(T) bool) T {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if v := get(); ok(v) || time.Now().After(deadline) {
			if !ok(v) {
				t.Fatalf("waited, still %+v", v)
			}
			return v
		}
	}
}

// A scan that blocks main files one fix naming each hit's file and line and
// the commit that wrote it, never the thing found; a retry at the same
// commit files nothing more.
func TestScanThatBlocksMainFilesAFix(t *testing.T) {
	m, _, merged, land := selfHive(t)
	m.Scan = nil
	type fix struct{ project, title, why, done, known string }
	var got []fix
	m.FileFix = func(project, title, why, done, known string) {
		got = append(got, fix{project, title, why, done, known})
	}
	os.WriteFile(filepath.Join(m.C.WS.Private(), scan.HandlesFile), []byte("alice-k\n"), 0o600)
	selfClone(t, m, map[string]string{scan.PublicList: "**\n", "README.md": "The Hive.\n"})
	sha := strings.TrimSpace(selfClone(t, m, map[string]string{"docs/a.md": "one\nthanks alice-k\n"}))
	p, _ := m.C.Projects.Get("hive")
	m.Scan = func(p core.Project, _ string) error { return m.scan(p, sha) }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	m.AfterKept("hive", "m1")
	if len(got) != 1 || got[0].project != "hive" || got[0].title != scanFix {
		t.Fatalf("want one fix filed, got %+v", got)
	}
	if !strings.Contains(got[0].known, "docs/a.md:2: handle 1 of the handle list (the line as ") {
		t.Fatalf("the fix doesn't name the hit: %q", got[0].known)
	}
	// At a commit the clone has, git blame names the one that wrote the line.
	m.fileScanFix(p, sha, m.scan(p, sha))
	if len(got) != 2 || !strings.Contains(got[1].known, "docs/a.md:2: handle 1 of the handle list (the line as "+core.Short(sha)+" wrote it)") {
		t.Fatalf("the fix doesn't name the commit that wrote the line: %+v", got)
	}
	if all := fmt.Sprint(got[0]); strings.Contains(all, "alice") {
		t.Fatalf("the fix says what was found: %s", all)
	}
	// Any other failure files nothing.
	m.fileScanFix(p, sha, os.ErrPermission)
	if len(got) != 2 {
		t.Fatalf("a failure with no hits filed a fix: %+v", got)
	}
}
