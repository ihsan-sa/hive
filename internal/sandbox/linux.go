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
	specPath := s.Stdout + ".sandbox.json"
	if err := WriteSpec(specPath, s); err != nil {
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
	fail := func(err error) int { fmt.Fprintln(errf, "sandbox:", err); return 125 }

	base := "/dev/shm"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	rt, err := os.MkdirTemp(base, "hive-sbx-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(rt)
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
		defer ServeEgress(ln).Close()
	}
	for _, t := range s.Tools {
		p, err := findTool(t)
		if err != nil {
			return fail(err)
		}
		name := filepath.Base(t)
		if strings.HasPrefix(name, "hive") {
			name = "hive" // hive-linux-amd64 is `hive` inside
		}
		args.Tools[name] = p
	}
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
		cmd.Env = s.Env
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
	if !filepath.IsAbs(t) {
		p, err := exec.LookPath(t)
		if err != nil {
			for _, d := range []string{filepath.Join(os.Getenv("HOME"), ".local", "bin"), "/usr/local/bin"} {
				if _, e := os.Stat(filepath.Join(d, t)); e == nil {
					p, err = filepath.Join(d, t), nil
					break
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("tool %q not found on this machine", t)
		}
		t = p
	}
	return filepath.EvalSymlinks(t)
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
	if err := mkdirMount("tmpfs", root+"/tmp", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777"); err != nil {
		return fail("tmp", err)
	}
	for _, m := range s.Mounts {
		if err := bind(m.Host, root+m.Inside, m.RW); err != nil {
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
	err = cmd.Wait()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return fail("wait", err)
	}
	return 0
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
	} else {
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
	return mkdirMount("tmpfs", dev+"/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777")
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
	specPath := s.Stdout + ".sandbox.json"
	if err := WriteSpec(specPath, s); err != nil {
		return -1, err
	}
	return runSync([]string{self, "sandbox-exec", "--spec", specPath}, os.Environ(), "/", "", "", timeout)
}
