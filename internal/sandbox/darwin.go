//go:build darwin

package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Seatbelt is the macOS sandbox: sandbox-exec with a generated profile.
// Reads are allowed except the person's credential stores; writes only to the
// run's mounts marked rw and a temp dir; network only to the run's own egress
// proxy, a loopback port held for as long as the command runs.
//
// Untested on real macOS in v1's build environment; treat as provisional.
type Seatbelt struct{}

var seatbelt = &Seatbelt{}

func platform(Options) Sandbox {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil
	}
	return seatbelt
}

func (*Seatbelt) Name() string   { return "seatbelt" }
func (*Seatbelt) Isolated() bool { return false }

// Check makes sure a loopback port can be had for a proxy.
func (s *Seatbelt) Check() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		ln.Close()
	}
	return err
}

// proxy serves sp's egress proxy on a loopback port, recording refused hosts
// next to its output.
func proxy(sp Spec) (*Egress, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	var refused io.Writer
	if sp.Stdout != "" {
		if f, err := os.OpenFile(sp.Stdout+".egress", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			refused = f
		}
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return ServeEgress(ln, sp.Allow, sp.Local, refused), port, nil
}

func (s *Seatbelt) Start(sp Spec) (Proc, error) {
	e, port, err := proxy(sp)
	if err != nil {
		return Proc{}, err
	}
	pf := sp.Stdout + ".sb"
	if err := os.WriteFile(pf, []byte(s.profile(sp, port)), 0o600); err != nil {
		e.Close()
		return Proc{}, err
	}
	env := withPathPrefix(append(SeatbeltEnv(sp, port), "PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"), pathDirs(sp))
	argv := niced(append([]string{"sandbox-exec", "-f", pf}, sp.Argv...), sp.Nice)
	p, err := proc.StartDetached(argv, env, sp.Cwd, sp.Stdin, sp.Stdout)
	if err != nil {
		e.Close()
		return p, err
	}
	// The proxy lives as long as the command, but in the daemon: a daemon
	// restart cuts a running command's network.
	go func() {
		for proc.Alive(p) {
			time.Sleep(5 * time.Second)
		}
		e.Close()
	}()
	return p, nil
}

func (s *Seatbelt) profile(sp Spec, port string) string {
	home := os.Getenv("HOME")
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n(allow process*)\n(allow signal (target same-sandbox))\n")
	b.WriteString("(allow sysctl-read)\n(allow mach-lookup)\n(allow ipc-posix*)\n(allow file-read*)\n")
	for _, d := range []string{".ssh", ".config/gh", ".claude", ".claude.json", "Library/Keychains", ".aws", ".gnupg", ".netrc", ".docker", ".kube"} {
		fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", filepath.Join(home, d))
	}
	b.WriteString("(deny mach-lookup (global-name \"com.apple.SecurityServer\"))\n")
	b.WriteString("(allow file-write* (subpath \"/private/tmp\") (subpath \"/private/var/folders\") (literal \"/dev/null\") (literal \"/dev/tty\"))\n")
	for _, m := range sp.Mounts {
		if m.Hide {
			fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", m.Host)
		}
	}
	// The run's own writable folders may sit in a hidden one (its home in
	// .hive/private); the later rule wins.
	for _, m := range sp.Mounts {
		if m.RW {
			fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", m.Host)
		}
	}
	fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%s\"))\n", port)
	// A granted service on this machine: Seatbelt shares the host's loopback,
	// so the run reaches it directly as well as through the proxy.
	for _, a := range sp.Local {
		if _, p, err := net.SplitHostPort(a); err == nil {
			fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%s\"))\n", p)
		}
	}
	b.WriteString("(allow network-outbound (literal \"/private/var/run/mDNSResponder\"))\n")
	return b.String()
}

func Exec(string) int    { fmt.Fprintln(os.Stderr, "sandbox-exec runs on Linux"); return 125 }
func Init(string) int    { return Exec("") }
func Inner([]string) int { return Exec("") }

// Run runs s to completion under Seatbelt.
func (s *Seatbelt) Run(sp Spec, timeout time.Duration) (int, error) {
	e, port, err := proxy(sp)
	if err != nil {
		return -1, err
	}
	defer e.Close()
	pf := sp.Stdout + ".sb"
	if err := os.WriteFile(pf, []byte(s.profile(sp, port)), 0o600); err != nil {
		return -1, err
	}
	env := withPathPrefix(append(SeatbeltEnv(sp, port), "PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"), pathDirs(sp))
	return runSync(niced(append([]string{"sandbox-exec", "-f", pf}, sp.Argv...), sp.Nice), env, sp.Cwd, sp.Stdin, sp.Stdout, timeout)
}
