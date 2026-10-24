//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBwrapArgsKeepReach(t *testing.T) {
	a := initArgs{
		Spec: Spec{Argv: []string{"claude", "-p"}, Cwd: "/work", UID: 1000, GID: 1000,
			Mounts: []Mount{{Host: "/h/work", Inside: "/work", RW: true}, {Host: "/h/ref", Inside: "/ref"}}},
		Socket: "/rt/egress.sock",
		Tools:  map[string]string{"claude": "/h/bin/claude"},
	}
	got := strings.Join(bwrapArgs(a, "/h/hive"), " ")
	for _, want := range []string{
		"--unshare-all", "--die-with-parent", "--uid 1000 --gid 1000",
		"--bind /h/work /work", "--ro-bind /h/ref /ref", "--ro-bind /h/bin/claude /hive/bin/claude",
		"--bind /rt/egress.sock /hive/egress.sock", "--chdir /work", "--remount-ro /",
		"-- /hive/libexec/hive sandbox-inner --fwd -- claude -p",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bwrap args lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--share-net") || strings.Contains(got, "/home") {
		t.Errorf("bwrap args widen reach:\n%s", got)
	}
	a.Spec.Local = []string{"127.0.0.1:11434"}
	if got := strings.Join(bwrapArgs(a, "/h/hive"), " "); !strings.Contains(got, "sandbox-inner --fwd --local 127.0.0.1:11434 -- claude -p") {
		t.Errorf("a granted local port isn't forwarded inside:\n%s", got)
	}
	a.Socket = ""
	if slices.Contains(bwrapArgs(a, "/h/hive"), "--fwd") {
		t.Error("no network must mean no forwarder")
	}
}

// TestForcedBwrapNeverFallsBack: with HIVE_SANDBOX_BWRAP=1 and no bwrap to
// start, the sandbox still takes the bwrap path, so it fails instead of
// quietly running in the build's own namespaces.
func TestForcedBwrapNeverFallsBack(t *testing.T) {
	t.Setenv("HIVE_SANDBOX_BWRAP", "1")
	t.Setenv("PATH", t.TempDir())
	if name := (Linux{}).Name(); name != "bubblewrap" {
		t.Fatalf("forced sandbox = %q, want bubblewrap", name)
	}
	if err := exec.Command(bwrapPath(), "true").Run(); err == nil {
		t.Fatal("a bwrap that isn't there started")
	}
}

// TestNoSelfExecInTests: a test that reaches the sandbox in-process gets an
// error, never a re-run of its own test binary. It asks hiveBinary, not
// Probe, so that without the guard it fails rather than forking.
func TestNoSelfExecInTests(t *testing.T) {
	if self, err := hiveBinary(); err == nil || !strings.Contains(err.Error(), "test binary") {
		t.Fatalf("hiveBinary in a test = %q, %v; want a test binary refusal", self, err)
	}
}

// TestProbeBothPaths runs the real probe through the built binary on each
// path this host allows: the build's own namespaces and bubblewrap.
func TestProbeBothPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	ran := 0
	for _, path := range []struct{ name, env string }{{"namespaces", "0"}, {"bubblewrap", "1"}} {
		t.Run(path.name, func(t *testing.T) {
			if path.env == "1" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil {
				t.Skip("bwrap can't create namespaces here")
			}
			if path.env == "0" && (bwrapPath() != "" || exec.Command("unshare", "-Ur", "true").Run() != nil) {
				t.Skip("unprivileged user namespaces are restricted here")
			}
			cmd := exec.Command(bin, "sandbox-probe")
			cmd.Env = append(os.Environ(), "HIVE_SANDBOX_BWRAP="+path.env)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("probe: %v %s", err, out)
			}
			ran++
		})
	}
	t.Logf("%d of 2 sandbox paths ran on this host", ran)
}

// TestHostPythonBothPaths runs a host python that lives outside the system
// dirs through the built binary on each path this host allows. The fake prints
// 7 where its folder is read-only, as it is once bound inside, and 8 where it
// can write it, so 7 means that path found it by name and ran it sandboxed.
func TestHostPythonBothPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	tmp := t.TempDir()
	if underSystem(tmp) {
		t.Skip("the temp dir is under a system dir")
	}
	bin := filepath.Join(tmp, "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	py := mkTree(t, tmp, "py/bin/python3")[0]
	if err := os.WriteFile(py, []byte("#!/bin/sh\n[ -w \"${0%/*}\" ] && echo 8 || echo 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, path := range []struct{ name, env string }{{"namespaces", "0"}, {"bubblewrap", "1"}} {
		t.Run(path.name, func(t *testing.T) {
			if path.env == "1" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil {
				t.Skip("bwrap can't create namespaces here")
			}
			if path.env == "0" && (bwrapPath() != "" || exec.Command("unshare", "-Ur", "true").Run() != nil) {
				t.Skip("unprivileged user namespaces are restricted here")
			}
			dir := t.TempDir()
			work := filepath.Join(dir, "work")
			if err := os.Mkdir(work, 0o755); err != nil {
				t.Fatal(err)
			}
			s := Spec{
				ID: "py", Argv: []string{"python3"}, Env: InsideEnv("/tmp", false), Cwd: "/work",
				Mounts: []Mount{{Host: work, Inside: "/work", RW: true}},
				Stdout: filepath.Join(dir, "out"), UID: os.Getuid(), GID: os.Getgid(),
			}
			spec := filepath.Join(dir, "spec.json")
			if err := WriteSpec(spec, s); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "sandbox-exec", "--spec", spec)
			cmd.Env = append(os.Environ(), "HIVE_SANDBOX_BWRAP="+path.env, "PATH="+filepath.Dir(py)+":"+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				e, _ := os.ReadFile(s.Stdout + ".err")
				t.Fatalf("sandbox-exec: %v %s %s", err, out, e)
			}
			if out, _ := os.ReadFile(s.Stdout); strings.TrimSpace(string(out)) != "7" {
				t.Fatalf("python3 inside printed %q, want 7 (8 is a writable, unsandboxed run)", out)
			}
			ran++
		})
	}
	t.Logf("%d of 2 sandbox paths ran on this host", ran)
}

// A uv python under the home is bound read-only at its own install, not at
// ~/.local, and found by name inside right after the Hive's dirs.
func TestHostInterpreterReachesInside(t *testing.T) {
	home := t.TempDir()
	if underSystem(home) {
		t.Skip("the temp dir is under a system dir")
	}
	defer func(old []string) { interpreters = old }(interpreters)
	interpreters = []string{"python3"}
	inst := mkTree(t, home, ".local/share/uv/python/cpython-3.12/bin/python3", ".local/share/uv/python/cpython-3.12/lib/os.py")[0]
	inst = filepath.Dir(filepath.Dir(inst))
	mkTree(t, home, ".local/share/keyrings/login.keyring")
	if err := os.MkdirAll(filepath.Join(home, ".local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(inst, "bin/python3"), filepath.Join(home, ".local/bin/python3")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, ".local/bin")+":/usr/bin:/bin")
	a := initArgs{Spec: Spec{Env: InsideEnv("/hive/home", false)}}
	hostInterpreters(&a)
	if !slices.Equal(a.Roots, []string{inst}) {
		t.Fatalf("roots %q, want only %s", a.Roots, inst)
	}
	var path []string
	for _, kv := range a.Spec.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = filepath.SplitList(v)
		}
	}
	at := slices.Index(path, filepath.Join(inst, "bin"))
	if at < 1 || !strings.HasPrefix(path[at-1], "/hive/") || slices.Index(path, "/usr/bin") < at {
		t.Fatalf("PATH %q: want the install right after the /hive/ dirs, before /usr/bin", path)
	}
	if got := strings.Join(bwrapArgs(a, "/h/hive"), " "); !strings.Contains(got, "--ro-bind "+inst+" "+inst) {
		t.Fatalf("bwrap args lack the install:\n%s", got)
	}
}

// No interpreter widens the bind past its own install: not /usr, the home,
// ~/.local (keyrings) or ~/.cargo (the crates.io token), and nothing at all
// when the home is unknown.
func TestInstallRootNeverBindsMore(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secrets := mkTree(t, home, ".cargo/credentials.toml", ".cargo/lib/x", ".local/share/keyrings/login.keyring",
		".local/lib/python3.12/site.py", "lib/x", ".tool/python3")
	uvLocal := mkTree(t, home, ".local/bin/uv")[0]
	uvCargo := mkTree(t, home, ".cargo/bin/uv")[0]
	homeBin := mkTree(t, home, "bin/python3")[0]
	// A HOME that runs through a symlink (/home/u -> /data/u) still keeps the
	// home and ~/.local out; one that resolves nowhere binds nothing.
	link := filepath.Join(t.TempDir(), "u")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Symlink(filepath.Join(home, "missing"), gone); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		p, home, want string
		ok            bool
	}{
		{"/usr/bin/python3", home, "", true},
		{uvLocal, home, filepath.Join(home, ".local/bin"), true},
		{uvCargo, home, filepath.Join(home, ".cargo/bin"), true},
		{homeBin, home, filepath.Join(home, "bin"), true},
		{secrets[5], home, "", false},
		{uvLocal, "", "", false},
		{uvCargo, "", "", false},
		{uvLocal, "rel/home", "", false},
		{uvLocal, link, filepath.Join(home, ".local/bin"), true},
		{uvCargo, link, filepath.Join(home, ".cargo/bin"), true},
		{homeBin, link, filepath.Join(home, "bin"), true},
		{uvLocal, gone, "", false},
	} {
		got, ok := installRoot(c.p, c.home, nil)
		if got != c.want || ok != c.ok {
			t.Errorf("installRoot(%s, %q) = %q, %v; want %q, %v", c.p, c.home, got, ok, c.want, c.ok)
		}
		for _, s := range append(secrets[:3:3], "/usr/lib/os-release") {
			if got != "" && strings.HasPrefix(s, got+"/") {
				t.Errorf("installRoot(%s) = %s exposes %s", c.p, got, s)
			}
		}
	}
	defer func(old []string) { interpreters = old }(interpreters)
	interpreters = []string{"uv"}
	t.Setenv("HOME", "")
	t.Setenv("PATH", filepath.Join(home, ".local/bin")+":/usr/bin:/bin")
	a := initArgs{Spec: Spec{Env: InsideEnv("/hive/home", false)}}
	hostInterpreters(&a)
	if len(a.Roots) != 0 || strings.Contains(strings.Join(a.Spec.Env, " "), home) {
		t.Fatalf("with HOME unset: roots %q, env %q", a.Roots, a.Spec.Env)
	}
}

// No interpreter's install is bound when it holds the workspace or a folder
// covered in it, given as the spec's guard or as a mount: a python at
// /data/bin with lib/ beside it is bound as its bin alone when the workspace
// lives under /data, and not at all when only a dir holding it would do.
func TestInstallRootKeepsTheWorkspaceOut(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(data, "hive")
	private := filepath.Join(ws, ".hive", "private")
	py := mkTree(t, data, "bin/python3", "lib/python3.12/os.py", "hive/.hive/private/daemon.json",
		"hive/bin/python3", "opt/py/bin/python3", "opt/py/lib/x", "keep/bin/python3", "keep/lib/x", "keep/secret/token")
	guard := []string{ws, filepath.Join(ws, ".hive", "logins"), filepath.Join(ws, ".hive", "modules"), private}
	// The same workspace reached through a symlink is still kept out.
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, p string
		guard   []string
		want    string
		ok      bool
	}{
		{"workspace under the install", py[0], guard, filepath.Join(data, "bin"), true},
		{"workspace through a symlink", py[0], []string{link}, filepath.Join(data, "bin"), true},
		{"masked path under the install", py[0], []string{private}, filepath.Join(data, "bin"), true},
		{"masked path not made yet", py[0], []string{filepath.Join(link, ".hive", "logins")}, filepath.Join(data, "bin"), true},
		{"bin holding the workspace", py[0], []string{filepath.Join(data, "bin", "hive")}, "", false},
		{"install inside the workspace", py[3], guard, filepath.Join(ws, "bin"), true},
		{"allowed root beside the workspace", py[4], guard, filepath.Join(data, "opt/py"), true},
		{"no guard binds the install", py[0], nil, data, true},
		{"masked path inside the install", py[6], []string{py[8]}, filepath.Join(data, "keep/bin"), true},
	} {
		got, ok := installRoot(c.p, home, c.guard)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: installRoot(%s) = %q, %v; want %q, %v", c.name, c.p, got, ok, c.want, c.ok)
		}
		for _, g := range c.guard {
			if got != "" && holds(got, g) {
				t.Errorf("%s: installRoot(%s) = %s exposes %s", c.name, c.p, got, g)
			}
		}
	}
	// Through the spec: the guard and every mount's host path are kept out.
	defer func(old []string) { interpreters = old }(interpreters)
	interpreters = []string{"python3"}
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(data, "bin")+":/usr/bin:/bin")
	for _, s := range []Spec{
		{Env: InsideEnv("/hive/home", false), Guard: []string{private}},
		{Env: InsideEnv("/hive/home", false), Mounts: []Mount{{Host: filepath.Join(ws, "projects", "p"), Inside: "/work"}}},
	} {
		a := initArgs{Spec: s}
		hostInterpreters(&a)
		if !slices.Equal(a.Roots, []string{filepath.Join(data, "bin")}) {
			t.Errorf("spec %+v: roots %q, want only %s", s, a.Roots, filepath.Join(data, "bin"))
		}
	}
}

// mkTree makes each file (and its dirs) under root and returns their paths.
func mkTree(t *testing.T, root string, files ...string) []string {
	var out []string
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// A hidden directory becomes an empty read-only tmpfs and a hidden file
// /dev/null, both after the mount that exposes them.
func TestBwrapArgsHide(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "daemon.json")
	os.WriteFile(file, []byte("{}"), 0o600)
	a := initArgs{Spec: Spec{Argv: []string{"sh"}, Cwd: "/work", Mounts: []Mount{
		{Host: dir, Inside: "/hive/state"},
		{Host: dir, Inside: "/hive/state/logins", Hide: true},
		{Host: file, Inside: "/hive/state/daemon.json", Hide: true},
	}}}
	got := strings.Join(bwrapArgs(a, "/h/hive"), " ")
	want := "--ro-bind " + dir + " /hive/state --tmpfs /hive/state/logins --remount-ro /hive/state/logins --ro-bind /dev/null /hive/state/daemon.json"
	if !strings.Contains(got, want) {
		t.Errorf("bwrap args lack %q:\n%s", want, got)
	}
}
