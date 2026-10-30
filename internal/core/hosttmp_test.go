package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestMain keeps every test's Prune out of the host's shared /tmp, and every
// run a test lays out off this machine's agent tokens and service manager.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "core-shared-tmp-")
	if err != nil {
		panic(err)
	}
	sharedTmp = dir
	toks := filepath.Join(dir, "no-agent-tokens")
	AgentTokens = func() string { return toks }
	UserRuntime = func() string { return "" }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// A daemon whose /tmp is in memory puts its scratch files on disk, in the
// workspace's private folder; a TMPDIR set elsewhere is left alone.
func TestUseDiskTempLeavesMemory(t *testing.T) {
	if runtime.GOOS != "linux" || !inMemory("/dev/shm") {
		t.Skip("needs a tmpfs at /dev/shm")
	}
	ws := Workspace{Root: t.TempDir()}
	if inMemory(ws.Root) {
		t.Skip("the test's temp folder is in memory itself")
	}
	shm, err := os.MkdirTemp("/dev/shm", "core-tmp-")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.RemoveAll(shm) })
	defer func(old string) { sharedTmp = old }(sharedTmp)
	sharedTmp = shm

	t.Setenv("TMPDIR", "/somewhere/else")
	if got, err := UseDiskTemp(ws); err != nil || got != "" || os.Getenv("TMPDIR") != "/somewhere/else" {
		t.Fatalf("a TMPDIR set elsewhere was moved: %q, %v", got, err)
	}
	t.Setenv("TMPDIR", "")
	got, err := UseDiskTemp(ws)
	if err != nil || got != ws.Temp() || os.Getenv("TMPDIR") != ws.Temp() || os.TempDir() != ws.Temp() {
		t.Fatalf("UseDiskTemp = %q, %v; TMPDIR %q, want %q", got, err, os.Getenv("TMPDIR"), ws.Temp())
	}
	if fi, err := os.Stat(ws.Temp()); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the temp folder: %v, %v", fi, err)
	}
}

// The clean job removes the Hive's leftovers in the shared temp folder and
// its own once nothing in them has changed for a day, and nothing else.
func TestPruneTempRemovesQuietLeftovers(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	shared := t.TempDir()
	defer func(old string) { sharedTmp = old }(sharedTmp)
	sharedTmp = shared
	old := time.Now().Add(-2 * leftoverAge)
	mk := func(p string, aged bool) string {
		os.MkdirAll(filepath.Join(p, "x"), 0o755)
		f := filepath.Join(p, "x", "f")
		os.WriteFile(f, []byte("data"), 0o644)
		if aged {
			for _, q := range []string{f, filepath.Join(p, "x"), p} {
				os.Chtimes(q, old, old)
			}
		}
		return p
	}
	stale := mk(filepath.Join(shared, "hive-check-1"), true)
	test := mk(filepath.Join(shared, "TestSoftLayerarmsstandard1"), true)
	own := mk(filepath.Join(ws.Temp(), "go-build1"), true)
	busy := mk(filepath.Join(shared, "hive-meter-2"), true)
	os.WriteFile(filepath.Join(busy, "x", "new"), nil, 0o644) // written to just now, deep inside
	sbx := mk(filepath.Join(shared, "hive-sbx-3"), true)
	other := mk(filepath.Join(shared, "go-build2"), true)
	fresh := mk(filepath.Join(shared, "hive-gate-4"), false)
	var gone []string
	pruneTemp(ws, func(p string) { gone = append(gone, p); RemoveTree(p) })
	for p, want := range map[string]bool{stale: false, test: false, own: false, busy: true, sbx: true, other: true, fresh: true} {
		if Exists(p) != want {
			t.Errorf("%s exists %v, want %v", p, Exists(p), want)
		}
	}
	if len(gone) != 3 {
		t.Errorf("dropped %v", gone)
	}
}

// The clean job removes a run's /tmp once no turn uses it: an ended run's
// and an unknown one's at once, a waiting run's once quiet for a day, never
// a running one's; read-only folders in it (a Go module cache's) go too.
func TestPruneTmpRemovesRunsScratch(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	old := time.Now().Add(-2 * leftoverAge)
	mk := func(id, status string, aged bool) string {
		if status != "" {
			rs.runs[id] = &Run{RunSpec: RunSpec{ID: id}, Status: status}
		}
		dir := filepath.Join(ws.RunTmp(id), "gomod", "x@v1")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "f"), []byte("data"), 0o444)
		os.Chmod(dir, 0o555)
		if aged {
			for p := dir; p != filepath.Dir(ws.RunTmp(id)); p = filepath.Dir(p) {
				os.Chtimes(p, old, old)
			}
			os.Chtimes(filepath.Join(dir, "f"), old, old)
		}
		return ws.RunTmp(id)
	}
	done := mk("done", StatusDone, false)
	stopped := mk("stopped", StatusStopped, false)
	unknown := mk("unknown", "", false)
	running := mk("running", StatusRunning, true)
	idle := mk("idle", StatusIdle, false)
	quietIdle := mk("quiet", StatusIdle, true)
	t.Cleanup(func() { RemoveTree(ws.Root) })
	var gone []string
	rs.pruneTmp(func(p string) { gone = append(gone, p); RemoveTree(p) })
	for p, want := range map[string]bool{done: false, stopped: false, unknown: false, running: true, idle: true, quietIdle: false} {
		if Exists(p) != want {
			t.Errorf("%s exists %v, want %v", p, Exists(p), want)
		}
	}
	if len(gone) != 4 {
		t.Errorf("dropped %v", gone)
	}
}

// An ended run's home is cut down to its sessions once it has been over for
// homeAge, its files kept, and marked so the next sweep passes it by: one
// that ended lately, a live one, or one moved since it was cut keeps or gets
// its due; one no run knows goes by its home's age.
func TestPruneHomesKeepsSessions(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	old := time.Now().Add(-2 * homeAge)
	mk := func(id, status string, updated time.Time) string {
		if status != "" {
			rs.runs[id] = &Run{RunSpec: RunSpec{ID: id}, Status: status, Updated: updated}
		}
		home := ws.RunHome(id)
		for _, d := range []string{".cache/go-build/00", "venv/lib", ".claude/projects/x", ".codex"} {
			os.MkdirAll(filepath.Join(home, d), 0o755)
		}
		os.WriteFile(filepath.Join(home, ".cache", "go-build", "00", "obj"), []byte("data"), 0o444)
		os.Chmod(filepath.Join(home, ".cache", "go-build", "00"), 0o555)
		os.WriteFile(filepath.Join(home, "notes.sh"), []byte("echo"), 0o644)
		os.Chtimes(home, updated, updated)
		return home
	}
	done := mk("done", StatusDone, old)
	recent := mk("recent", StatusBlocked, time.Now())
	running := mk("running", StatusRunning, old)
	unknown := mk("unknown", "", old)
	young := mk("young", "", time.Now())
	t.Cleanup(func() { RemoveTree(ws.Root) })
	var gone []string
	rs.pruneHomes(func(p string) { gone = append(gone, p); RemoveTree(p) })
	for home, cut := range map[string]bool{done: true, recent: false, running: false, unknown: true, young: false} {
		for _, keep := range []string{".claude/projects/x", ".codex", "notes.sh"} {
			if !Exists(filepath.Join(home, keep)) {
				t.Errorf("%s lost %s", home, keep)
			}
		}
		for _, scratch := range []string{".cache", "venv"} {
			if Exists(filepath.Join(home, scratch)) == cut {
				t.Errorf("%s: %s there %v, cut %v", home, scratch, !cut, cut)
			}
		}
		if Exists(filepath.Join(filepath.Dir(home), HomePruned)) != cut {
			t.Errorf("%s: marked %v, cut %v", home, !cut, cut)
		}
	}
	if len(gone) != 4 {
		t.Errorf("dropped %v", gone)
	}
	// Marked, it is passed by; reopened and ended again since, it is cut again.
	os.MkdirAll(filepath.Join(done, "venv"), 0o755)
	gone = nil
	rs.pruneHomes(func(p string) { gone = append(gone, p); RemoveTree(p) })
	if len(gone) != 0 {
		t.Errorf("a marked home was walked again: %v", gone)
	}
	mark := filepath.Join(filepath.Dir(done), HomePruned)
	os.Chtimes(mark, old.Add(-time.Hour), old.Add(-time.Hour))
	rs.pruneHomes(func(p string) { gone = append(gone, p); RemoveTree(p) })
	if len(gone) != 1 || Exists(filepath.Join(done, "venv")) {
		t.Errorf("a run that ended again after its cut kept its scratch: %v", gone)
	}
}
