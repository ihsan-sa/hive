//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"golang.org/x/sys/unix"
)

// Linux is the namespace sandbox. The hive binary is its own launcher:
// `hive sandbox-exec` (outer, on the host) runs the egress proxy and starts
// `hive sandbox-init` as pid 1 of new user/mount/pid/net/ipc/uts namespaces,
// which builds a read-only root, binds the run's writable paths, and starts
// the command as an unprivileged user in a nested user namespace.
type Linux struct{}

func platform(Options) Sandbox { return Linux{} }

func (Linux) Name() string {
	if bwrapPath() != "" {
		return "bubblewrap"
	}
	return "linux-namespaces"
}

func (Linux) Isolated() bool { return true }

// Check runs a probe sandbox and requires it to succeed.
func (Linux) Check() error { return Probe() }

func (Linux) Start(s Spec) (Proc, error) {
	self, err := hiveBinary()
	if err != nil {
		return Proc{}, err
	}
	specPath, err := writeTempSpec(s)
	if err != nil {
		return Proc{}, err
	}
	return proc.StartDetached([]string{self, "sandbox-exec", "--spec", specPath}, os.Environ(), "/", "", "")
}

// hiveBinary is the running binary, which launches its own sandboxes. A Go
// test binary can't: given `sandbox-exec` it would run its whole suite again,
// and a suite that probes the sandbox forks itself without end. So a test
// fails here, once, and asks the built hive binary instead.
func hiveBinary() (string, error) {
	self, err := os.Executable()
	if err == nil && testing.Testing() {
		return "", fmt.Errorf("%s is a test binary and can't run sandbox-exec; run the built hive binary", filepath.Base(self))
	}
	return self, err
}

// Probe starts a minimal sandbox and checks isolation holds.
func Probe() error {
	self, err := hiveBinary()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "hive-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	work := filepath.Join(dir, "work")
	os.MkdirAll(work, 0o755)
	s := Spec{
		ID:     "probe",
		Argv:   []string{"/bin/sh", "-c", "touch /work/ok && ! touch /etc/hive-probe 2>/dev/null"},
		Env:    InsideEnv("/tmp", false),
		Cwd:    "/work",
		Mounts: []Mount{{Host: work, Inside: "/work", RW: true}},
		Stdout: filepath.Join(dir, "out"),
		UID:    os.Getuid(), GID: os.Getgid(),
		// No Guard: it runs /bin/sh alone, so it binds no host interpreter.
	}
	specPath := filepath.Join(dir, "spec.json")
	if err := WriteSpec(specPath, s); err != nil {
		return err
	}
	out, err := exec.Command(self, "sandbox-exec", "--spec", specPath).CombinedOutput()
	if err != nil {
		e, _ := os.ReadFile(s.Stdout + ".err")
		return fmt.Errorf("probe failed: %v %s %s", err, strings.TrimSpace(string(out)), strings.TrimSpace(string(e)))
	}
	if _, err := os.Stat(filepath.Join(work, "ok")); err != nil {
		return errors.New("probe could not write its checkout")
	}
	return nil
}

type initArgs struct {
	Spec   Spec              `json:"spec"`
	Root   string            `json:"root"`
	Socket string            `json:"socket"`
	Tools  map[string]string `json:"tools"`
	Roots  []string          `json:"roots"` // host interpreter installs, bound read-only at their own paths
}

// Exec is `hive sandbox-exec`: the host side of one sandboxed command.
func Exec(specPath string) int {
	s, err := ReadSpec(specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		return 125
	}
	os.Remove(specPath) // it may carry a short-lived token; don't leave it on disk
	errf := os.Stderr
	if s.Stdout != "" {
		if f, err := proc.OpenOutput(s.Stdout + ".err"); err == nil {
			errf = f
		}
	}
	if s.Nice > 0 {
		// This launcher, the init it starts and the command inherit it, so a
		// check or a worker's turn yields the cpu to a lead's. Failing to is
		// no reason not to run; it is said in the run's stderr log.
		if err := lowerPriority(s.Nice); err != nil {
			fmt.Fprintln(errf, "sandbox: running at the daemon's priority, not", s.Nice, "under it:", err)
		}
	}
	fail := func(err error) int { fmt.Fprintln(errf, "sandbox:", err); return 125 }
	if s.Tmp != "" {
		// Fresh each command, as a tmpfs is; what a killed launcher left
		// goes the next time.
		os.RemoveAll(s.Tmp)
		if err := os.MkdirAll(s.Tmp, 0o700); err != nil {
			return fail(err)
		}
		defer os.RemoveAll(s.Tmp)
	}

	base := "/dev/shm"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	rt, err := os.MkdirTemp(base, "hive-sbx-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(rt)
	if held, err := holdRuntime(rt); err == nil {
		defer held.Close()
	} else {
		// It runs on, but a sweep reads its folder as a legacy one, gone
		// after runtimeStale (egress.sock with it) even while it still runs.
		fmt.Fprintln(errf, "sandbox: can't hold the runtime folder's lock:", err)
	}
	args := initArgs{Spec: s, Root: filepath.Join(rt, "root"), Tools: map[string]string{}}
	if err := os.Mkdir(args.Root, 0o755); err != nil {
		return fail(err)
	}
	if s.Network {
		args.Socket = filepath.Join(rt, "egress.sock")
		ln, err := net.Listen("unix", args.Socket)
		if err != nil {
			return fail(err)
		}
		var refused io.Writer
		if s.Stdout != "" {
			if f, err := os.OpenFile(s.Stdout+".egress", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				refused = f
			}
		}
		defer ServeEgress(ln, s.Allow, s.Local, s.Outside, refused).Close()
	}
	for _, t := range s.Tools {
		p, err := findTool(t)
		if err != nil {
			return fail(err)
		}
		if visibleInside(t, p) {
			continue
		}
		name := filepath.Base(t)
		if strings.HasPrefix(name, "hive") {
			name = "hive" // hive-linux-amd64 is `hive` inside
		}
		args.Tools[name] = p
	}
	hostInterpreters(&args)
	raw, _ := json.Marshal(args)
	argPath := filepath.Join(rt, "init.json")
	if err := os.WriteFile(argPath, raw, 0o600); err != nil {
		return fail(err)
	}

	in, err := openIn(s.Stdin)
	if err != nil {
		return fail(err)
	}
	defer in.Close()
	out := io.Writer(os.Stdout)
	if s.Stdout != "" {
		f, err := proc.OpenOutput(s.Stdout)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		out = f
	}
	cmd := exec.Command("/proc/self/exe", "sandbox-init", argPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC | syscall.CLONE_NEWUTS,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	if bw := bwrapPath(); bw != "" {
		self, err := hiveBinary()
		if err != nil {
			return fail(err)
		}
		cmd = exec.Command(bw, bwrapArgs(args, self)...)
		cmd.Env = args.Spec.Env // hostInterpreters put the install dirs on its PATH
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errf
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("start namespaces: %w (are unprivileged user namespaces allowed? on Ubuntu 23.10 and later install bubblewrap)", err))
	}
	stop := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 2)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-sig:
				cmd.Process.Kill() // pid 1 of the namespace: everything inside dies with it
			case <-t.C:
				if s.StopFile != "" {
					if _, err := os.Stat(s.StopFile); err == nil {
						cmd.Process.Kill()
					}
				}
			}
		}
	}()
	err = cmd.Wait()
	close(stop)
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func openIn(path string) (*os.File, error) {
	if path == "" {
		return os.Open(os.DevNull)
	}
	return os.Open(path)
}

func findTool(t string) (string, error) {
	p, err := lookTool(t)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// interpreters are the host's own runtimes a command may call by name. One
// installed outside systemDirs (by uv under the home, say) would be missing
// from the sandbox's root, so its install is bound read-only at its own path,
// and the dir it runs from goes on PATH after the Hive's own dirs: inside, the
// name finds the same program it finds on the host. Shims (pyenv, asdf, mise)
// are scripts, not links, so they are not followed and won't run inside; put
// the real install's bin on PATH instead. A snap app links to /usr/bin/snap,
// which can't start it without snapd, so snaps are skipped.
var interpreters = []string{"python3", "python", "node", "uv", "go"}

func hostInterpreters(a *initArgs) {
	home, _ := os.UserHomeDir()
	guard := append([]string{}, a.Spec.Guard...)
	cover := append([]string{}, a.Spec.Cover...)
	for _, m := range a.Spec.Mounts {
		guard = append(guard, m.Host)
		if m.Hide {
			cover = append(cover, m.Host)
		}
	}
	var dirs []string
	for _, n := range interpreters {
		p, err := findTool(n)
		if err != nil || filepath.Base(p) == "snap" {
			continue
		}
		root, ok := installRoot(p, home, guard, cover)
		if !ok {
			continue
		}
		if root != "" && !slices.Contains(a.Roots, root) {
			a.Roots = append(a.Roots, root)
		}
		if dir := filepath.Dir(p); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	a.Spec.Env = insertPath(a.Spec.Env, dirs)
}

// installRoot is what to bind so the resolved interpreter p runs inside: ""
// when the system dirs already hold it, the parent of its bin when that parent
// is an install of its own (a lib/ beside bin, as python, node and go have),
// else the bin alone. It is never the home, anything above it or a dot-dir
// right under it (~/.local, ~/.cargo hold keyrings and tokens), nor anything
// that holds a guarded path (the workspace, the folders covered in it, a
// mount's host path), nor anything inside a covered folder (cover: the
// folders covered wherever agents read .hive, and every hidden mount), so a
// python at /data/bin with lib/ beside it is bound as /data/bin alone when the
// workspace is under /data, and one under .hive/private is not bound at all;
// one inside the workspace elsewhere is. ok is false when only such a dir
// would do, when the home is unknown, relative or can't be resolved, and when
// there is no guard or a guarded or covered path is not absolute (a spec that
// can't say what to keep out binds nothing). So a python installed straight into ~/.local (bin and lib both
// there) is bound as its bin alone: it starts inside but can't find its lib.
// Install it with uv or into a prefix of its own.
func installRoot(p, home string, guard, cover []string) (root string, ok bool) {
	dir := filepath.Dir(p)
	if underSystem(dir) {
		return "", true
	}
	if !filepath.IsAbs(home) {
		return "", false
	}
	// p is resolved (findTool), so the home and the guarded paths must be
	// too: through a symlinked home, the checks below would see neither the
	// home nor ~/.local. A guarded path that doesn't resolve (not made yet) is
	// compared as given, and an unresolved parent of it as well.
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", false
	}
	if len(guard) == 0 {
		return "", false
	}
	held, ok := resolveAll(slices.Concat(guard, cover))
	if !ok {
		return "", false
	}
	covered, _ := resolveAll(cover)
	cands := []string{dir}
	if up := filepath.Dir(dir); filepath.Base(dir) == "bin" {
		if fi, err := os.Stat(filepath.Join(up, "lib")); err == nil && fi.IsDir() {
			cands = []string{up, dir}
		}
	}
next:
	for _, r := range cands {
		if holds(r, home) || filepath.Dir(r) == home && strings.HasPrefix(filepath.Base(r), ".") {
			continue
		}
		for _, g := range held {
			if holds(r, g) {
				continue next
			}
		}
		for _, c := range covered {
			if holds(c, r) {
				return "", false
			}
		}
		return r, true
	}
	return "", false
}

// holds reports whether binding dir r exposes path p: p is r or under it.
func holds(r, p string) bool {
	rel, err := filepath.Rel(r, p)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, "../"))
}

// resolveAll is each path cleaned, and resolved too when that differs; ok is
// false when one is not absolute.
func resolveAll(paths []string) (out []string, ok bool) {
	for _, g := range paths {
		if !filepath.IsAbs(g) {
			return nil, false
		}
		g = filepath.Clean(g)
		out = append(out, g)
		if r, err := resolveExisting(g); err == nil && r != g {
			out = append(out, r)
		}
	}
	return out, true
}

// resolveExisting resolves the symlinks in p's longest existing prefix and
// keeps the rest as given, so a guarded folder not made yet still resolves
// through a symlinked workspace.
func resolveExisting(p string) (string, error) {
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, rest...)...), nil
		}
		up := filepath.Dir(p)
		if up == p {
			return "", os.ErrNotExist
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = up
	}
}

func underSystem(p string) bool {
	for _, d := range systemDirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// visibleInside reports whether tool t, which resolves to p, is already on
// the sandbox's PATH. Such a tool (Codex's Node launcher, say) may need its
// package beside it, so it runs where it is instead of being bound alone.
func visibleInside(t, p string) bool {
	l, err := exec.LookPath(t)
	if filepath.IsAbs(t) || err != nil || !strings.Contains(":"+insideBins+":", ":"+filepath.Dir(l)+":") {
		return false
	}
	return underSystem(p)
}

// insertPath puts dirs not already on PATH after its /hive/ entries.
func insertPath(env, dirs []string) []string {
	for i, kv := range env {
		v, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		path := filepath.SplitList(v)
		at := 0
		for at < len(path) && strings.HasPrefix(path[at], "/hive/") {
			at++
		}
		var add []string
		for _, d := range dirs {
			if !slices.Contains(path, d) {
				add = append(add, d)
			}
		}
		env[i] = "PATH=" + strings.Join(slices.Insert(path, at, add...), ":")
	}
	return env
}

// systemDirs are exposed read-only; everything else (home, /mnt, /run, /tmp,
// sockets) simply isn't in the sandbox's root.
var systemDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc", "/opt", "/var", "/srv", "/nix"}

// Init is `hive sandbox-init`, pid 1 inside the namespaces.
func Init(argPath string) int {
	fail := func(step string, err error) int {
		fmt.Fprintf(os.Stderr, "sandbox-init: %s: %v\n", step, err)
		return 126
	}
	raw, err := os.ReadFile(argPath)
	if err != nil {
		return fail("read", err)
	}
	var a initArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return fail("parse", err)
	}
	s, root := a.Spec, a.Root

	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fail("make private", err)
	}
	if err := unix.Mount("tmpfs", root, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755"); err != nil {
		return fail("root tmpfs", err)
	}
	for _, d := range systemDirs {
		fi, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(d)
			os.Symlink(target, root+d)
			continue
		}
		if err := bind(d, root+d, false); err != nil {
			return fail("bind "+d, err)
		}
	}
	if err := setupDev(root); err != nil {
		return fail("dev", err)
	}
	if err := mkdirMount("proc", root+"/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return fail("proc", err)
	}
	if s.Tmp != "" {
		if err := bind(s.Tmp, root+"/tmp", true); err != nil {
			return fail("tmp", err)
		}
	} else if err := mkdirMount("tmpfs", root+"/tmp", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777,size="+strconv.Itoa(TmpfsSize)); err != nil {
		return fail("tmp", err)
	}
	for _, r := range a.Roots {
		if err := bind(r, root+r, false); err != nil {
			return fail("bind "+r, err)
		}
	}
	for _, m := range s.Mounts {
		var err error
		switch fi, serr := os.Stat(m.Host); {
		case m.Hide && serr == nil && fi.IsDir():
			err = unix.Mount("tmpfs", root+m.Inside, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_RDONLY, "mode=0555")
		case m.Hide:
			err = bindFlags("/dev/null", root+m.Inside, false, true) // a device, so it reads empty as under bwrap
		default:
			err = bind(m.Host, root+m.Inside, m.RW)
		}
		if err != nil {
			return fail("bind "+m.Inside, err)
		}
	}
	for name, src := range a.Tools {
		if err := bind(src, root+"/hive/bin/"+name, false); err != nil {
			return fail("tool "+name, err)
		}
	}
	if a.Socket != "" {
		if err := bind(a.Socket, root+"/hive/egress.sock", true); err != nil {
			return fail("egress socket", err)
		}
	}
	if err := os.MkdirAll(root+"/.oldroot", 0o700); err != nil {
		return fail("oldroot", err)
	}
	if err := unix.PivotRoot(root, root+"/.oldroot"); err != nil {
		return fail("pivot_root", err)
	}
	if err := os.Chdir("/"); err != nil {
		return fail("chdir", err)
	}
	if err := unix.Unmount("/.oldroot", unix.MNT_DETACH); err != nil {
		return fail("detach old root", err)
	}
	os.Remove("/.oldroot")
	if err := unix.Mount("", "/", "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return fail("read-only root", err)
	}
	if err := loopbackUp(); err != nil {
		return fail("loopback", err)
	}
	if a.Socket != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+ProxyPort)
		if err != nil {
			return fail("forwarder", err)
		}
		go forward(ln, "/hive/egress.sock")
		forwardLocal(s.Local, "/hive/egress.sock", os.Stderr)
	}

	// Resolve against the sandbox's PATH: exec.Command would look the name up
	// in pid 1's own environment and keep that error.
	prog, err := lookPathIn(s.Argv[0], s.Env)
	if err != nil {
		return fail("find "+s.Argv[0], err)
	}
	cmd := exec.Command(prog, s.Argv[1:]...)
	cmd.Args[0] = s.Argv[0]
	cmd.Env, cmd.Dir = s.Env, s.Cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The command runs as the person's uid in a nested user namespace, so it
	// holds no capabilities over the mounts built above.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: s.UID, HostID: 0, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: s.GID, HostID: 0, Size: 1}},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	if err := cmd.Start(); err != nil {
		return fail("start "+s.Argv[0], err)
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for range sig {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	}()
	code, err := reap(cmd.Process.Pid)
	if err != nil {
		return fail("wait", err)
	}
	return code
}

// reap waits for pid as pid 1 must: every orphan in the namespace is
// reparented here, so it reaps whatever exits until pid does, rather than
// leaving zombies (a check's go test, an agent's shells) until the sandbox
// ends. It returns pid's exit code, or -1 when a signal ended it.
func reap(pid int) (int, error) {
	for {
		var ws syscall.WaitStatus
		got, err := syscall.Wait4(-1, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if got == pid && (ws.Exited() || ws.Signaled()) {
			if ws.Signaled() {
				return -1, nil
			}
			return ws.ExitStatus(), nil
		}
	}
}

func lookPathIn(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			for _, d := range filepath.SplitList(v) {
				p := filepath.Join(d, name)
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
					return p, nil
				}
			}
		}
	}
	return "", exec.ErrNotFound
}

func mkdirMount(src, dst, fstype string, flags uintptr, data string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return unix.Mount(src, dst, fstype, flags, data)
}

// bind mounts src at dst, read-only unless rw. Remounting keeps the flags the
// kernel has locked on the source, or the remount is refused.
func bind(src, dst string, rw bool) error { return bindFlags(src, dst, rw, false) }

func bindFlags(src, dst string, rw, device bool) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
	} else if _, err := os.Stat(dst); err != nil {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		f.Close()
	}
	// Recursive: an unprivileged user namespace may not bind a tree without
	// its submounts (that could reveal what they cover).
	if err := unix.Mount(src, dst, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	for _, mp := range submounts(dst) {
		var st unix.Statfs_t
		if err := unix.Statfs(mp, &st); err != nil {
			continue // unreachable submount (e.g. a dead network mount)
		}
		flags := uintptr(unix.MS_REMOUNT|unix.MS_BIND|unix.MS_NOSUID) | lockedFlags(st.Flags)
		if !device {
			flags |= unix.MS_NODEV
		}
		if !rw {
			flags |= unix.MS_RDONLY
		}
		if err := unix.Mount("", mp, "", flags, ""); err != nil {
			return fmt.Errorf("remount %s (flags %#x, source flags %#x): %w", mp, flags, st.Flags, err)
		}
	}
	return nil
}

// submounts returns dst and every mount point below it, parents first.
func submounts(dst string) []string {
	out := []string{dst}
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := unescapeMount(f[4])
		if strings.HasPrefix(mp, dst+"/") {
			out = append(out, mp)
		}
	}
	return out
}

// unescapeMount decodes mountinfo's octal escapes (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var n int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%03o", &n); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func lockedFlags(f int64) uintptr {
	var out uintptr
	pairs := [][2]int64{{unix.ST_RDONLY, unix.MS_RDONLY}, {unix.ST_NOSUID, unix.MS_NOSUID}, {unix.ST_NODEV, unix.MS_NODEV},
		{unix.ST_NOEXEC, unix.MS_NOEXEC}, {unix.ST_NOATIME, unix.MS_NOATIME}, {unix.ST_NODIRATIME, unix.MS_NODIRATIME},
		{unix.ST_RELATIME, unix.MS_RELATIME}}
	for _, p := range pairs {
		if f&p[0] != 0 {
			out |= uintptr(p[1])
		}
	}
	return out
}

func setupDev(root string) error {
	dev := root + "/dev"
	if err := mkdirMount("tmpfs", dev, "tmpfs", unix.MS_NOSUID, "mode=0755"); err != nil {
		return err
	}
	for _, n := range []string{"null", "zero", "full", "random", "urandom", "tty"} {
		if err := bindFlags("/dev/"+n, dev+"/"+n, true, true); err != nil {
			return err
		}
	}
	for n, t := range map[string]string{"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0", "stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2"} {
		os.Symlink(t, dev+"/"+n)
	}
	return mkdirMount("tmpfs", dev+"/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777,size="+strconv.Itoa(ShmSize))
}

func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var ifr [40]byte
	copy(ifr[:], "lo")
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr[0]))); e != 0 {
		return e
	}
	flags := *(*uint16)(unsafe.Pointer(&ifr[16]))
	*(*uint16)(unsafe.Pointer(&ifr[16])) = flags | unix.IFF_UP | unix.IFF_RUNNING
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr[0]))); e != 0 {
		return e
	}
	return nil
}

// forward relays loopback connections inside the sandbox to the egress proxy.
func forward(ln net.Listener, sock string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			u, err := net.Dial("unix", sock)
			if err != nil {
				return
			}
			defer u.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(u, c); closeWrite(u); done <- struct{}{} }()
			go func() { io.Copy(c, u); closeWrite(c); done <- struct{}{} }()
			<-done
			<-done
		}()
	}
}

// Run runs s to completion; the exit code is the sandboxed command's.
func (Linux) Run(s Spec, timeout time.Duration) (int, error) {
	self, err := hiveBinary()
	if err != nil {
		return -1, err
	}
	specPath, err := writeTempSpec(s)
	if err != nil {
		return -1, err
	}
	return runSync([]string{self, "sandbox-exec", "--spec", specPath}, os.Environ(), "/", "", "", timeout, s.Disk)
}

// lowerPriority puts the calling thread `by` under the daemon's level,
// capped at 19 (Lowered), and keeps its goroutine on that thread for good:
// Linux nices a thread, not a process, and the sandbox forked later from
// this goroutine inherits the level only if nothing has moved it to another
// thread first. The level adds to the daemon's own, as `nice -n` does for
// the bare and Seatbelt sandboxes (the raw getpriority answers 20 minus the
// nice level). Going lower needs no privilege, and failing to is no reason
// not to run. The thread stays locked for good, so this process (hive
// sandbox-exec, one per command) must not go on to other work afterwards,
// and the fork must stay on this goroutine.
func lowerPriority(by int) error {
	if by <= 0 {
		return nil
	}
	runtime.LockOSThread()
	cur := 0
	if prio, err := unix.Getpriority(unix.PRIO_PROCESS, 0); err == nil {
		cur = 20 - prio
	}
	return unix.Setpriority(unix.PRIO_PROCESS, 0, Lowered(cur, by))
}
