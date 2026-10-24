//go:build linux

package sandbox

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// Stock Ubuntu (23.10 on) sets kernel.apparmor_restrict_unprivileged_userns,
// which stops the hive binary building its own user namespaces; bwrap holds
// an AppArmor profile that may. There the same root, mounts, tools and egress
// socket are handed to bwrap, which runs `hive sandbox-inner` as the command's
// parent. WSL leaves the setting off and keeps the build's own namespaces.
// HIVE_SANDBOX_BWRAP=1 forces bwrap (for tests): a missing bwrap then fails
// to start rather than falling back to the build's own namespaces.
func bwrapPath() string {
	if os.Getenv("HIVE_SANDBOX_BWRAP") == "1" {
		return "bwrap"
	}
	b, _ := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if strings.TrimSpace(string(b)) != "1" {
		return ""
	}
	p, _ := exec.LookPath("bwrap")
	return p
}

// bwrapArgs gives bwrap the reach Init builds: system dirs read-only, the
// spec's mounts, tools at /hive/bin, a fresh /dev, /proc and /tmp, no network
// but the egress socket, every namespace unshared, and a read-only root.
func bwrapArgs(a initArgs, self string) []string {
	s := a.Spec
	argv := []string{"--unshare-all", "--die-with-parent", "--new-session",
		"--uid", strconv.Itoa(s.UID), "--gid", strconv.Itoa(s.GID)}
	for _, d := range systemDirs {
		fi, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(d)
			argv = append(argv, "--symlink", target, d)
			continue
		}
		argv = append(argv, "--ro-bind", d, d)
	}
	argv = append(argv, "--dev", "/dev", "--tmpfs", "/dev/shm", "--proc", "/proc", "--tmpfs", "/tmp")
	for _, m := range s.Mounts {
		flag := "--ro-bind"
		if m.RW {
			flag = "--bind"
		}
		switch fi, err := os.Stat(m.Host); {
		case m.Hide && err == nil && fi.IsDir():
			argv = append(argv, "--tmpfs", m.Inside, "--remount-ro", m.Inside)
		case m.Hide:
			argv = append(argv, "--ro-bind", "/dev/null", m.Inside)
		default:
			argv = append(argv, flag, m.Host, m.Inside)
		}
	}
	for name, src := range a.Tools {
		argv = append(argv, "--ro-bind", src, "/hive/bin/"+name)
	}
	argv = append(argv, "--ro-bind", self, "/hive/libexec/hive")
	inner := []string{"/hive/libexec/hive", "sandbox-inner"}
	if a.Socket != "" {
		argv = append(argv, "--bind", a.Socket, "/hive/egress.sock")
		inner = append(inner, "--fwd")
		for _, l := range s.Local {
			inner = append(inner, "--local", l)
		}
	}
	argv = append(argv, "--chdir", s.Cwd, "--remount-ro", "/", "--")
	return append(append(append(argv, inner...), "--"), s.Argv...)
}

// Inner is `hive sandbox-inner [--fwd] -- argv...`, run by bwrap: it relays
// the sandbox's loopback proxy port to the egress socket and runs argv.
func Inner(args []string) int {
	fail := func(step string, err error) int {
		os.Stderr.WriteString("sandbox-inner: " + step + ": " + err.Error() + "\n")
		return 126
	}
	if len(args) > 0 && args[0] == "--fwd" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+ProxyPort)
		if err != nil {
			return fail("forwarder", err)
		}
		go forward(ln, "/hive/egress.sock")
		args = args[1:]
		var local []string
		for len(args) > 1 && args[0] == "--local" {
			local, args = append(local, args[1]), args[2:]
		}
		forwardLocal(local, "/hive/egress.sock", os.Stderr)
	}
	if len(args) < 2 || args[0] != "--" {
		return 2
	}
	argv := args[1:]
	prog, err := lookPathIn(argv[0], os.Environ())
	if err != nil {
		return fail("find "+argv[0], err)
	}
	cmd := exec.Command(prog, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return fail("start "+argv[0], err)
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
