package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// isolated is a sandbox with its own paths, for the module cache's path.
type isolated struct{ sandbox.Bare }

func (isolated) Isolated() bool { return true }

// removesCache removes the workspace's cache at the test's end with the
// change's own remover, and fails if it couldn't: the Go module cache leaves
// its files and folders read-only, which t.TempDir can't remove.
func removesCache(t *testing.T, c *Core) {
	t.Cleanup(func() {
		if err := RemoveTree(c.WS.Cache()); err != nil || Exists(c.WS.Cache()) {
			t.Errorf("the cache outlived RemoveTree: %v", err)
		}
	})
}

// A Go project's check gets the shared module cache read-only, offline, filled
// once per go.mod and go.sum by the effects process.
func TestChecksShareTheModuleCache(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared module cache is for Linux sandboxes")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	c, _ := testHive(t)
	removesCache(t, c)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644)
	g := c.Effects.goModules(isolated{}, dir)
	g.done()
	mounts, env := g.mounts, g.env
	if g.note != "" {
		t.Fatal(g.note)
	}
	if len(mounts) != 1 || mounts[0].Host != c.WS.GoModCache() || mounts[0].Inside != GoModInside || mounts[0].RW {
		t.Fatalf("mounts %+v", mounts)
	}
	for _, kv := range []string{"GOMODCACHE=" + GoModInside, "GOPROXY=off", "GOFLAGS=-mod=readonly -ldflags=-w"} {
		if !slices.Contains(env, kv) {
			t.Errorf("env %v lacks %s", env, kv)
		}
	}
	// As go leaves what it fetched (CI's go does so even for a module with no
	// requirements): read-only files in read-only folders.
	ro := filepath.Join(c.WS.GoModCache(), "cache", "download", "example.com", "x", "@v")
	os.MkdirAll(ro, 0o755)
	os.WriteFile(filepath.Join(ro, "v1.0.0.mod"), []byte("module example.com/x\n"), 0o444)
	for d := ro; d != c.WS.GoModCache(); d = filepath.Dir(d) {
		os.Chmod(d, 0o555)
	}
	sums, _ := os.ReadDir(c.WS.goSums())
	if len(sums) != 1 {
		t.Fatalf("%d go.sum markers, want 1", len(sums))
	}
	// Filled once: a second check of the same sums doesn't fetch again,
	// even with no go to fetch with.
	t.Setenv("PATH", t.TempDir())
	if g := c.Effects.goModules(isolated{}, dir); g.note != "" {
		t.Fatalf("fetched again: %s", g.note)
	} else {
		g.done()
	}
	// Another go.mod has no go to fetch with, and gets no cache (it fetches its own).
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m2\n"), 0o644)
	if g := c.Effects.goModules(isolated{}, dir); g.mounts != nil || g.env != nil || !strings.Contains(g.note, "not prefetched") {
		t.Fatalf("without go: %+v", g)
	}
	// A bare run keeps the host's own module cache.
	if g := c.Effects.goModules(sandbox.Bare{}, dir); g.mounts != nil {
		t.Fatal("bare run got the shared cache")
	}
}

// A check that fails for want of room is can't-run, not red, and raises one
// alert naming what uses the room; a check that runs again ends it.
func TestDiskFullIsCantRun(t *testing.T) {
	c, _, p, main := setupProject(t)
	alerts := 0
	c.J.Subscribe(func(e Event) {
		if e.Kind == "alert.raised" && strings.Contains(string(e.Data), `"disk"`) {
			alerts++
		}
	})
	full := `echo "go: writing go.mod cache: write /hive/home/go/pkg/mod/cache/download/x: disk quota exceeded" >&2; exit 1`
	for i := 0; i < 2; i++ {
		ch := c.Effects.RunCheck(p, main, "test", full)
		if ch.OK || !strings.Contains(ch.CantRun, "disk") {
			t.Fatalf("check %+v", ch)
		}
		if _, ok := c.Effects.GetCheck(p.Name, main, "test"); ok {
			t.Fatal("a full disk was kept as the commit's answer")
		}
	}
	if alerts != 1 {
		t.Fatalf("%d disk alerts, want 1", alerts)
	}
	if d := c.Effects.Disk(); d.At.IsZero() || d.Total == 0 {
		t.Fatalf("the alert measured nothing: %+v", d)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "echo 'write x: no space left on device'; exit 2"); ch.CantRun == "" {
		t.Fatalf("ENOSPC: %+v", ch)
	}
	if alerts != 1 {
		t.Fatal("a second alert while the first still stands")
	}
	c.Effects.RunCheck(p, main, "test", "true")
	c.Effects.RunCheck(p, main, "test", full)
	if alerts != 2 {
		t.Fatalf("after a check ran, a new shortage raised %d alerts in all, want 2", alerts)
	}
	// A failing test that quotes the error is the commit's red, not the disk's.
	for _, quoted := range []string{
		`printf -- '--- FAIL: TestX (0.00s)\n    x_test.go:9: check {CantRun:can'"'"'t run: disk quota exceeded}\nFAIL\n'; exit 1`,
		`printf '    x_test.go:9: write /x: no space left on device\nFAIL\n'; exit 1`,
	} {
		if ch := c.Effects.RunCheck(p, main, "test", quoted); ch.CantRun != "" || ch.OK {
			t.Fatalf("a test's own words read as a full disk: %+v", ch)
		}
	}
}

// A check cut off at its time limit is a slow check, not a red one: no
// verdict, not kept as the commit's answer, and its next try has twice the
// limit. An old record that says so only in its output reads the same.
func TestCutOffIsSlowNotRed(t *testing.T) {
	c, _, p, main := setupProject(t)
	saved := checkTimeout
	checkTimeout = time.Second
	t.Cleanup(func() { checkTimeout = saved })
	if l := c.Effects.checkLimit(p.Name, main, "test"); l != time.Second {
		t.Fatalf("first limit %s", l)
	}
	start := time.Now()
	ch := c.Effects.RunCheck(p, main, "test", "echo '--- FAIL: TestX'; sleep 2")
	if ch.OK || !ch.Slow || !strings.Contains(ch.CantRun, "slow check") || len(ch.Failed) > 0 {
		t.Fatalf("cut off: %+v", ch)
	}
	if _, ok := c.Effects.GetCheck(p.Name, main, "test"); ok {
		t.Fatal("a cut-off check was kept as the commit's answer")
	}
	if l := c.Effects.checkLimit(p.Name, main, "test"); l != 2*time.Second {
		t.Fatalf("limit after a cut-off %s, want twice it", l)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "sleep 1.3"); !ch.OK || ch.Slow || time.Since(start) < 2*time.Second {
		t.Fatalf("the longer try: %+v", ch)
	}
	if l := c.Effects.checkLimit(p.Name, main, "test"); l != time.Second {
		t.Fatalf("limit after a verdict %s", l)
	}
	c.J.MustAppend("effect.checked", Check{Project: p.Name, SHA: "old", Kind: "test", Output: "timed out\n=== 47% done"})
	if _, ok := c.Effects.GetCheck(p.Name, "old", "test"); ok || c.Effects.checkLimit(p.Name, "old", "test") != 2*time.Second {
		t.Fatal("an old cut-off record read as red")
	}
	c.J.MustAppend("effect.checked", Check{Project: p.Name, SHA: "own", Kind: "test", Output: "x\ntimed out\n"})
	if _, ok := c.Effects.GetCheck(p.Name, "own", "test"); !ok {
		t.Fatal("a command's own 'timed out' line read as a cut-off")
	}
}

// A check's home past its cap ends the check by its stop file.
func TestCheckHomeCap(t *testing.T) {
	saved := homeEvery
	homeEvery = 10 * time.Millisecond
	t.Cleanup(func() { homeEvery = saved })
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "big"), make([]byte, 2<<20), 0o644)
	over := watchHome(home, 1<<20)
	deadline := time.Now().Add(5 * time.Second)
	for !Exists(home+".stop") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !over() || !Exists(home+".stop") {
		t.Fatal("the cap didn't stop the check")
	}
	os.Remove(home + ".stop")
	small := t.TempDir()
	if watchHome(small, 1<<20)() {
		t.Fatal("an empty home was over its cap")
	}
}

// Prune removes stale check homes and clean checkouts, never a live one or
// the self gate's folder, and runs' homes live under the cache, gone after.
func TestPruneRemovesLeftovers(t *testing.T) {
	c, _ := testHive(t)
	t.Setenv("TMPDIR", t.TempDir()) // the temp homes Prune looks for are the test's, not the host's
	e := c.Effects
	e.sweep = func() int { return sandbox.SweepIn(t.TempDir(), os.TempDir()) }
	old := time.Now().Add(-2 * staleAfter)
	mk := func(p string) string {
		os.MkdirAll(filepath.Join(p, "x"), 0o755)
		os.WriteFile(filepath.Join(p, "x", "f"), []byte("data"), 0o444)
		os.Chmod(filepath.Join(p, "x"), 0o555) // as the module cache leaves its folders
		os.Chtimes(p, old, old)
		return p
	}
	live, done := e.newHome()
	defer done()
	os.Chtimes(live, old, old)
	stale := mk(filepath.Join(e.ws.CheckHomes(), "home-1"))
	// A home this daemon didn't mark is a dead daemon's check's, however
	// young: it goes at once, its stop file left to end the check if it runs.
	orphan := filepath.Join(e.ws.CheckHomes(), "home-2")
	os.MkdirAll(orphan, 0o755)
	lone := filepath.Join(e.ws.CheckHomes(), "home-3.stop")
	os.WriteFile(lone, nil, 0o600)
	os.Chtimes(lone, old, old)
	gate := mk(filepath.Join(e.ws.Gates(), "p", "abc-test-1"))
	recursive := mk(filepath.Join(e.ws.Gates(), "p", "recursive"))
	fresh := filepath.Join(e.ws.Gates(), "p", "def-test-2")
	os.MkdirAll(fresh, 0o755)
	t.Cleanup(func() { os.Chmod(filepath.Join(recursive, "x"), 0o755) })
	if got := e.Prune(); !strings.Contains(got, "removed 3") {
		t.Fatalf("prune said %q", got)
	}
	for p, want := range map[string]bool{live: true, stale: false, orphan: false, orphan + ".stop": true, lone: false, gate: false, recursive: true, fresh: true} {
		if Exists(p) != want {
			t.Errorf("%s exists %v, want %v", p, Exists(p), want)
		}
	}
	done()
	if Exists(live) {
		t.Fatal("a check's home outlived the check")
	}
}

// A running check's clean checkout is never pruned, though goBuild dates its
// folders to 2001: Prune took the tree from under a check's tests, which then
// failed on "getcwd() failed" (#1058). Once the check is done it goes, and so
// does one a crash left: the mark is the daemon's memory, gone on restart.
func TestPruneLeavesARunningChecksCheckout(t *testing.T) {
	c, _, p, main := setupProject(t)
	e := c.Effects
	e.sweep = func() int { return 0 }
	checkout := func() string {
		dir, err := e.cleanCheckout(p, main, "test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.dropCheckout(dir) })
		if err := stableTimes(dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dir := checkout()
	e.Prune()
	if !Exists(filepath.Join(dir, "README.md")) {
		t.Fatal("Prune removed a running check's checkout")
	}
	// The daemon crashed mid-check: the one that follows marks nothing live.
	restarted := &Effects{ws: e.ws, sweep: func() int { return 0 }}
	restarted.Prune()
	if Exists(dir) {
		t.Fatal("a crashed check's checkout outlived a restart's Prune")
	}
	// A done check leaves no mark behind to keep a folder at its path.
	done := checkout()
	e.dropCheckout(done)
	os.MkdirAll(done, 0o755)
	old := time.Now().Add(-2 * staleAfter)
	os.Chtimes(done, old, old)
	e.Prune()
	if Exists(done) {
		t.Fatal("dropCheckout left its checkout marked live")
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 3 << 30: "3.0 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
	if DiskFull("ok\nwrite /x: disk quota exceeded\n") != "write /x: disk quota exceeded" || DiskFull("--- FAIL: TestX\nwrite /x: disk quota exceeded") != "" || DiskFull("\tdisk quota exceeded") != "" {
		t.Fatal("DiskFull")
	}
}

// A commit can't steer the host's prefetch at a host file: a symlinked go.mod
// or go.sum, or a replace leading out of the checkout, skips the shared cache,
// and go's own output (which could quote what it read) never reaches the note.
func TestPrefetchReadsOnlyTheCheckout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared module cache is for Linux sandboxes")
	}
	c, _ := testHive(t)
	removesCache(t, c)
	secret := filepath.Join(t.TempDir(), "token")
	os.WriteFile(secret, []byte("hive-secret-token-123\n"), 0o600)
	cases := map[string]func(dir string){
		"go.mod symlink": func(dir string) { os.Symlink(secret, filepath.Join(dir, "go.mod")) },
		"go.sum symlink": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o644)
			os.Symlink(secret, filepath.Join(dir, "go.sum"))
		},
		"absolute replace": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nrequire example.com/x v1.0.0\nreplace example.com/x => "+filepath.Dir(secret)+"\n"), 0o644)
		},
		"replace out": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace (\n\texample.com/x => ../../x // out\n)\n"), 0o644)
		},
		"replace( with no space": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace(\n\texample.com/x => "+filepath.Dir(secret)+"\n)\n"), 0o644)
		},
		"quoted target with a space": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace example.com/x => \"../a b\"\n"), 0o644)
		},
		"raw-quoted absolute target": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace example.com/x=>`"+filepath.Dir(secret)+"`\n"), 0o644)
		},
		"unreadable target": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace example.com/x => \"/unterminated\n"), 0o644)
		},
		"replace through a symlink": func(dir string) {
			os.Symlink(filepath.Dir(secret), filepath.Join(dir, "sub"))
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\nreplace example.com/x v1.0.0 => ./sub\n"), 0o644)
		},
	}
	for name, setup := range cases {
		dir := t.TempDir()
		setup(dir)
		g := c.Effects.goModules(isolated{}, dir)
		g.done()
		if g.mounts != nil || !strings.Contains(g.note, "not prefetched") {
			t.Errorf("%s: got the shared cache (%+v)", name, g)
		}
	}
	if sums, _ := os.ReadDir(c.WS.goSums()); len(sums) != 0 {
		t.Fatalf("a prefetch ran: %d markers", len(sums))
	}
	// A broken go.mod that go would quote: the note says it failed, never what go read.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("hive-secret-token-123\n"), 0o644)
	if g := c.Effects.goModules(isolated{}, dir); strings.Contains(g.note, "secret") {
		t.Fatalf("go's output reached the note: %q", g.note)
	}
	// go's lexer reads "=>" as the arrow only on its own, and ends a target
	// at its punctuation: a second "=>", or a target that reads as inside
	// the checkout only up to a comma, refuses the prefetch.
	for _, bad := range []string{
		"module m\nreplace a=>b v1 => ../x\n",
		"module m\nreplace example.com/x => ./..,x\n",
		"module m\nreplace example.com/x => ./x => ../y\n",
		"module m\nreplace example.com/x=>./x\n",
		"module m\nreplace example.com/x \"=>\" ../x\n",
	} {
		if localReplace(dir, bad) == "" {
			t.Errorf("%q was taken", bad)
		}
	}
	// A replace to a plain folder inside the checkout is fine.
	for _, ok := range []string{
		"module m\nreplace example.com/x => ./x\nreplace example.com/y => example.com/z v1.2.3\n",
		"module m\nreplace(\n\texample.com/x => \"./a b\" // in the checkout\n)\n",
		"module m // => /not/a/replace\n",
		"module m\nreplace example.com/x v1.0.0 => ./x // a=>b\n",
	} {
		if why := localReplace(dir, ok); why != "" {
			t.Errorf("%q: %s", ok, why)
		}
	}
}

// A go command run on a throwaway home collects no telemetry there, so no
// detached child of go's writes under the home after go has exited and
// races its removal.
func TestPrefetchHomeHasTelemetryOff(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go here")
	}
	home := t.TempDir()
	off, err := goTelemetryOff(home)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "env", "GOTELEMETRY")
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "SystemRoot=" + os.Getenv("SystemRoot"), "GOENV=off", "GOTOOLCHAIN=local"}, off...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go env: %v %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "off" {
		t.Fatalf("go's telemetry mode on the prefetch home is %q, want off", got)
	}
}

// The prefetch's go runs with telemetry off: in its fresh home go would
// otherwise take the daily upload token and leave a detached child writing
// into the cache after RemoveTree (issue #514).
func TestPrefetchTurnsTelemetryOff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared module cache is for Linux sandboxes")
	}
	c, _ := testHive(t)
	removesCache(t, c)
	seen := filepath.Join(t.TempDir(), "mode")
	goBin := filepath.Join(c.WS.Tools(), "go", "bin", "go")
	os.MkdirAll(filepath.Dir(goBin), 0o755)
	os.WriteFile(goBin, []byte("#!/bin/sh\ncat \"$XDG_CONFIG_HOME/go/telemetry/mode\" > "+seen+"\n"), 0o755)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o644)
	if err := c.Effects.prefetch(dir); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(seen); strings.TrimSpace(string(raw)) != "off" {
		t.Fatalf("go saw telemetry mode %q", raw)
	}
}

// mapped is an isolated sandbox that runs on the host, each mount's inside
// path standing for its host path, and keeps every spec it ran.
type mapped struct {
	sandbox.Bare
	specs *[]sandbox.Spec
}

func (mapped) Isolated() bool { return true }

func (m mapped) Run(s sandbox.Spec, timeout time.Duration) (int, error) {
	*m.specs = append(*m.specs, s)
	script := s.Argv[len(s.Argv)-1]
	for _, mt := range s.Mounts {
		if s.Cwd == mt.Inside {
			s.Cwd = mt.Host
		}
		script = strings.ReplaceAll(script, mt.Inside, mt.Host)
	}
	s.Argv = []string{"/bin/sh", "-c", script}
	var env []string
	for _, kv := range s.Env {
		if !strings.HasPrefix(kv, "TMPDIR=") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	s.Env = env
	return m.Bare.Run(s, timeout)
}

// A check that couldn't have a module offline runs once more fetching its
// own, with the shared cache as its first proxy, in a checkout the first run
// never wrote to. If go.sum names the module, the cache lacked it and its
// marker goes; if not, the check needs more than the cache holds, and later
// checks go straight to fetching their own.
func TestStaleModuleCacheRunsAgain(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared module cache is for Linux sandboxes")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	// mapped's runs inherit this process's environment, and a check of the
	// Hive itself that has the shared cache runs its tests with GOPROXY=off:
	// only what RunCheck sets may count.
	t.Setenv("GOPROXY", "")
	c, f := testHive(t)
	removesCache(t, c)
	seedRepo(t, f, "me/gomod", map[string]string{
		"go.mod":             "module example.com/m\n\ngo 1.21\n",
		"go.sum":             "example.com/x v1.0.0 h1:abc=\nexample.com/x v1.0.0/go.mod h1:def=\n",
		".hive/project.json": `{"test": "true"}`,
	})
	p, err := c.Projects.Add(Project{Name: "gomod", Repo: "me/gomod"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := c.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	var specs []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &specs}, nil }
	// Offline it can't have the module it names; online it passes, but only
	// in a checkout no earlier run wrote to.
	check := func(missing string) string {
		return `test -e dirty && { echo "a used checkout"; exit 3; }; touch dirty; ` +
			`[ "$GOPROXY" = off ] && { echo "go: ` + missing + `: ` + staleModules + `"; exit 1; }; exit 0`
	}
	// Offline on the shared cache, or fetching its own with the cache as its
	// first proxy (mounted either way).
	sharedIn := func(s sandbox.Spec) bool {
		return slices.ContainsFunc(s.Mounts, func(m sandbox.Mount) bool { return m.Inside == GoModInside }) &&
			slices.Contains(s.Env, "GOPROXY=off")
	}
	ownFrom := func(s sandbox.Spec) bool {
		return slices.ContainsFunc(s.Mounts, func(m sandbox.Mount) bool { return m.Inside == GoModInside && !m.RW }) &&
			slices.Contains(s.Env, ownModEnv[0])
	}
	markers := func() []string {
		es, _ := os.ReadDir(c.WS.goSums())
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return names
	}

	ch := c.Effects.RunCheck(p, main, "test", check("example.com/x@v1.0.0"))
	if !ch.OK || ch.CantRun != "" {
		t.Fatalf("named module: %+v", ch)
	}
	if len(specs) != 2 || !sharedIn(specs[0]) || sharedIn(specs[1]) || !ownFrom(specs[1]) {
		t.Fatalf("named module: %d runs, want the shared cache offline then as the first proxy", len(specs))
	}
	if m := markers(); len(m) != 0 {
		t.Fatalf("the cache lacked what its marker promised, and the marker stayed: %v", m)
	}

	specs = nil
	if ch := c.Effects.RunCheck(p, main, "test", check("example.com/tool@latest")); !ch.OK || !strings.Contains(ch.Output, "doesn't name") {
		t.Fatalf("unnamed module: %+v", ch)
	}
	if m := markers(); len(m) != 2 || !slices.ContainsFunc(m, func(n string) bool { return strings.HasSuffix(n, ownModules) }) {
		t.Fatalf("markers %v, want the go.sum's and its own-modules mark", m)
	}
	specs = nil
	if ch := c.Effects.RunCheck(p, main, "test", check("example.com/tool@latest")); !ch.OK || len(specs) != 1 || sharedIn(specs[0]) || !ownFrom(specs[0]) {
		t.Fatalf("a later check of the same go.sum ran %d time(s) (%+v)", len(specs), ch)
	}

	// The mark lapses: a day on, checks of the same go.sum share the cache again.
	var own string
	for _, n := range markers() {
		if strings.HasSuffix(n, ownModules) {
			own = filepath.Join(c.WS.goSums(), n)
		}
	}
	old := time.Now().Add(-ownModulesFor - time.Minute)
	os.Chtimes(own, old, old)
	specs = nil
	if ch := c.Effects.RunCheck(p, main, "test", "exit 0"); !ch.OK || len(specs) != 1 || !sharedIn(specs[0]) {
		t.Fatalf("after the mark lapsed: %d run(s), shared %v (%+v)", len(specs), len(specs) > 0 && sharedIn(specs[0]), ch)
	}
	if Exists(own) {
		t.Fatal("a lapsed mark stayed")
	}

	// A check that fails fetching its own too is the commit's red: no mark.
	specs = nil
	failing := `[ "$GOPROXY" = off ] && echo "go: example.com/tool@latest: ` + staleModules + `"; exit 1`
	if ch := c.Effects.RunCheck(p, main, "test", failing); ch.OK || len(specs) != 2 || !strings.Contains(ch.Output, "example.com/tool@latest") {
		t.Fatalf("failing both ways: %d run(s) (%+v)", len(specs), ch)
	}
	if slices.ContainsFunc(markers(), func(n string) bool { return strings.HasSuffix(n, ownModules) }) {
		t.Fatal("a check that failed fetching its own marked its go.sum")
	}

	// What a failing test printed indented (a nested go it ran) is its own:
	// no second run, no mark.
	specs = nil
	nested := `printf -- '--- FAIL: TestNested (0.00s)\n    x_test.go:9: go: example.com/y@v1.0.0: ` + staleModules + `\n'; exit 1`
	if ch := c.Effects.RunCheck(p, main, "test", nested); ch.OK || len(specs) != 1 {
		t.Fatalf("a test's own words: %d run(s) (%+v)", len(specs), ch)
	}
	if slices.ContainsFunc(markers(), func(n string) bool { return strings.HasSuffix(n, ownModules) }) {
		t.Fatal("a test's own words marked its go.sum")
	}
}

// A test that fails because the disk filled under it is can't-run when the
// room where checks write is short as the check ends, and the commit's red
// when there is room (its message only quotes the error).
func TestFullDiskFailingATest(t *testing.T) {
	c, _, p, main := setupProject(t)
	low := false
	saved := roomOf
	roomOf = func(path string) (int64, int64, bool) {
		if low {
			return 1 << 20, 100 << 20, true
		}
		return 90 << 20, 100 << 20, true
	}
	t.Cleanup(func() { roomOf = saved })
	failing := `printf -- '--- FAIL: TestBig (0.00s)\nwrite /tmp/x: no space left on device\nFAIL\n'; exit 1`
	if ch := c.Effects.RunCheck(p, main, "test", failing); ch.CantRun != "" || len(ch.Failed) != 1 {
		t.Fatalf("with room: %+v", ch)
	}
	low = true
	if ch := c.Effects.RunCheck(p, main, "test", failing); !strings.Contains(ch.CantRun, "disk") {
		t.Fatalf("short of room: %+v", ch)
	}
	// What a test printed indented is still its own words.
	if ch := c.Effects.RunCheck(p, main, "test", `printf -- '--- FAIL: TestX (0.00s)\n    x_test.go:9: no space left on device\n'; exit 1`); ch.CantRun != "" {
		t.Fatalf("a test's own words: %+v", ch)
	}
	if !strings.Contains(c.Effects.Disk().String(), "quota") && !quotaShown {
		t.Fatal("the room figures don't say a per-user quota is missing from them")
	}
}

// A checkout is moved aside under the project's lock and removed after, and
// its removal is on the record.
func TestRemoveCheckout(t *testing.T) {
	c, _, p, main := setupProject(t)
	dir, err := c.Effects.NewCheckout(p, "w-1", main)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	c.J.Subscribe(func(e Event) {
		if e.Kind == "effect.checkout_removed" {
			removed++
		}
	})
	if err := c.Effects.RemoveCheckout(p.Name, "w-1"); err != nil || Exists(dir) || removed != 1 {
		t.Fatalf("err %v, still there %v, %d events", err, Exists(dir), removed)
	}
	if left, _ := filepath.Glob(filepath.Join(c.WS.Cache(), "removing-*")); len(left) != 0 {
		t.Fatalf("left aside: %v", left)
	}
	if c.Effects.RemoveCheckout(p.Name, "w-1") != nil || removed != 1 {
		t.Fatal("a checkout that's gone was removed again")
	}
}

// The disk measurement names each part once and says where the rest is.
func TestMeasureDiskNamesTheRest(t *testing.T) {
	c, _ := testHive(t)
	big := filepath.Join(c.WS.Hive(), "spill", "deep", "f")
	os.MkdirAll(filepath.Dir(big), 0o755)
	os.WriteFile(big, make([]byte, 1<<20), 0o644)
	os.MkdirAll(c.WS.Runs(), 0o755)
	os.WriteFile(filepath.Join(c.WS.Runs(), "r"), make([]byte, 4096), 0o644)
	os.MkdirAll(filepath.Join(c.WS.Temp(), "go-build1"), 0o755)
	os.WriteFile(filepath.Join(c.WS.Temp(), "go-build1", "f"), make([]byte, 8192), 0o644)
	d := c.Effects.MeasureDisk()
	var sum int64
	named := map[string]int64{}
	for _, p := range d.Parts {
		sum += p.Bytes
		named[p.Name] = p.Bytes
	}
	if named["runs"] < 4096 || named["other, most in .hive/spill"] != 1<<20 || sum > d.Total || d.Total != Size(c.WS.Root) {
		t.Fatalf("parts %+v, total %d (walked %d)", d.Parts, d.Total, Size(c.WS.Root))
	}
}

// What no part names in .hive/private is told by its own subfolder, not by
// .hive/private as a whole.
func TestTopOfLooksIntoPrivate(t *testing.T) {
	root := filepath.FromSlash("/w")
	for p, want := range map[string]string{
		"/w/f":                              "",
		"/w/a/f":                            "a",
		"/w/.hive/spill/deep/f":             ".hive/spill",
		"/w/.hive/private/f":                ".hive/private",
		"/w/.hive/private/keys/r/auth.json": ".hive/private/keys",
	} {
		if got := topOf(root, filepath.FromSlash(p)); got != want {
			t.Errorf("topOf(%s) = %q, want %q", p, got, want)
		}
	}
}

// A check of a commit on main writes the project's build cache; a check of
// any other commit reads it, read-only, through the cache program, and only
// once main has built something. Each checkout's file times follow content.
func TestOnlyMainWritesTheBuildCache(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared build cache is for Linux sandboxes")
	}
	c, _, p, main := setupProject(t)
	checkout := func() string {
		dir, err := c.Effects.cleanCheckout(p, main, "test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Effects.dropCheckout(dir) })
		return dir
	}
	dir := checkout()
	other := strings.Repeat("0", 40)
	if g := c.Effects.goBuild(isolated{}, p, main, dir); g.mounts != nil {
		t.Fatal("a checkout with no go.mod got a build cache")
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o644)
	if g := c.Effects.goBuild(isolated{}, p, other, dir); g.mounts != nil {
		t.Fatalf("a branch got a build cache before main built one: %+v", g)
	}
	g := c.Effects.goBuild(isolated{}, p, main, dir)
	g.done()
	if len(g.mounts) != 1 || g.mounts[0].Host != c.WS.GoBuildCache(p.Name) || !g.mounts[0].RW || !slices.Equal(g.env, []string{"GOCACHE=" + GoBuildInside}) {
		t.Fatalf("main's check: %+v", g)
	}
	g = c.Effects.goBuild(isolated{}, p, other, dir)
	g.done()
	if len(g.mounts) != 1 || g.mounts[0].RW || g.mounts[0].Inside != GoBuildInside || !slices.ContainsFunc(g.env, func(kv string) bool {
		return kv == "GOCACHEPROG=/hive/bin/hive gocacheprog "+GoBuildInside+" /hive/home/gobuild"
	}) {
		t.Fatalf("a branch's check: %+v", g)
	}
	if g := c.Effects.goBuild(sandbox.Bare{}, p, main, dir); g.mounts != nil {
		t.Fatal("a bare run got the shared build cache")
	}
	// The same content has the same time in another checkout, long past;
	// other content has another.
	again := checkout()
	stableTimes(again)
	times := func(d, name string) time.Time {
		fi, err := os.Stat(filepath.Join(d, name))
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime()
	}
	for _, name := range []string{"README.md", ".hive", ".hive/goals.md", "."} {
		if a, b := times(dir, name), times(again, name); !a.Equal(b) || time.Since(a) < 24*time.Hour {
			t.Errorf("%s: %v and %v", name, a, b)
		}
	}
	if times(dir, "README.md").Equal(times(dir, ".hive/goals.md")) {
		t.Error("two contents share a time")
	}
}

// A change's check runs go vet and go test on what the change can affect:
// its packages and those that import them. Main's and a bare RunCheck run
// everything, and a narrowing the go command refuses runs everything too.
func TestRunCheckSinceNarrowsToAffectedPackages(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/gomod", map[string]string{
		"go.mod":         "module example.com/m\n\ngo 1.21\n",
		"a/a.go":         "package a\n\nimport _ \"example.com/m/b\"\n",
		"b/b.go":         "package b\n",
		"c/c.go":         "package c\n",
		".hive/goals.md": "# Goals\n",
	})
	p, err := c.Projects.Add(Project{Name: "gomod", Repo: "me/gomod"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := c.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := commitInCheckout(t, c, p, "w1", main, "b/b.go", "package b\n\n// changed\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho \"ran go $*\"\ncase \"$*\" in *./a*) [ -f \"$REFUSE\" ] && { echo 'no Go files in /work/a'; exit 1; };; esac\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := "go vet ./... && go test -short ./..."

	ch := c.Effects.RunCheckSince(p, sha, main, "test", cmd)
	if !ch.OK || !slices.Equal(ch.Scope, []string{"./a", "./b"}) || !strings.Contains(ch.Output, "ran go test -short ./a ./b") {
		t.Fatalf("narrowed check: %+v", ch)
	}
	if ch := c.Effects.RunCheck(p, main, "test", cmd); ch.Scope != nil || !strings.Contains(ch.Output, "ran go test -short ./...") {
		t.Fatalf("main's check: %+v", ch)
	}
	refuse := filepath.Join(t.TempDir(), "refuse")
	os.WriteFile(refuse, nil, 0o644)
	t.Setenv("REFUSE", refuse)
	if ch := c.Effects.RunCheckSince(p, sha, main, "test", cmd); !ch.OK || ch.Scope != nil || !strings.Contains(ch.Output, "ran go test -short ./...") {
		t.Fatalf("a narrowing go refused: %+v", ch)
	}
	// A whole pass stands over a later narrowed run of the same commit.
	os.Remove(refuse)
	c.Effects.RunCheckSince(p, sha, main, "test", cmd)
	if got, _ := c.Effects.GetCheck(p.Name, sha, "test"); got.Scope != nil {
		t.Fatalf("a narrowed run replaced the whole one: %+v", got)
	}
	// A whole red is rechecked: a narrowed pass after it is the answer, so
	// the testing stage and Merge read the same record.
	if ch := c.Effects.RunCheck(p, sha, "test", "exit 1"); ch.OK || ch.Scope != nil {
		t.Fatalf("whole red: %+v", ch)
	}
	c.Effects.RunCheckSince(p, sha, main, "test", cmd)
	if got, _ := c.Effects.GetCheck(p.Name, sha, "test"); !got.OK || got.Scope == nil {
		t.Fatalf("a narrowed pass didn't replace a whole red: %+v", got)
	}
}

// The person hears when the disk is nearly out of room, whatever fails first,
// and the alert ends once the room is clearly back.
func TestWatchRoomAlertsBeforeAnyCheck(t *testing.T) {
	c, _, _, _ := setupProject(t)
	free := int64(50 << 30)
	saved := roomOf
	roomOf = func(path string) (int64, int64, bool) { return free, 1800 << 30, true }
	t.Cleanup(func() { roomOf = saved })
	cleaned := make(chan bool, 4)
	c.Effects.OnDiskFull = func() { cleaned <- true }
	var raised, cleared int
	c.J.Subscribe(func(e Event) {
		var a struct{ Kind, Ref, Text string }
		e.Decode(&a)
		if e.Kind == "alert.raised" && a.Kind == "disk" && a.Ref == "disk:room" {
			raised++
			if !strings.Contains(a.Text, "200.0 MB free") {
				t.Errorf("alert doesn't say the room left: %s", a.Text)
			}
		}
		if e.Kind == "alert.cleared" && a.Ref == "disk:room" {
			cleared++
		}
	})
	c.Effects.WatchRoom()
	if raised != 0 {
		t.Fatal("raised with room to spare")
	}
	free = 200 << 20
	c.Effects.WatchRoom()
	c.Effects.WatchRoom()
	if raised != 1 {
		t.Fatalf("raised %d times, want once", raised)
	}
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("the clean job wasn't asked for")
	}
	free = 3 << 30 // over the floor, under twice it: still short
	c.Effects.WatchRoom()
	if cleared != 0 {
		t.Fatal("cleared before the room was clearly back")
	}
	free = 10 << 30
	c.Effects.WatchRoom()
	if cleared != 1 {
		t.Fatalf("cleared %d times, want once", cleared)
	}
	// A small tmpfs counts against a tenth of itself, not 2 GiB.
	roomOf = func(path string) (int64, int64, bool) { return 500 << 20, 1 << 30, true }
	c.Effects.WatchRoom()
	if raised != 1 {
		t.Fatal("a half-empty small tmpfs raised the alert")
	}
}
