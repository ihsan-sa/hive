//go:build darwin

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Seatbelt is the macOS sandbox: sandbox-exec with a generated profile.
// Reads are allowed except the person's credential stores; writes only to the
// run's mounts marked rw and a temp dir; network only to the local egress proxy.
//
// Untested on real macOS in v1's build environment; treat as provisional.
type Seatbelt struct {
	once sync.Once
	port string
	err  error
}

var seatbelt = &Seatbelt{}

func platform(Options) Sandbox {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil
	}
	return seatbelt
}

func (*Seatbelt) Name() string   { return "seatbelt" }
func (*Seatbelt) Isolated() bool { return false }

// Check starts the shared egress proxy on a loopback port.
func (s *Seatbelt) Check() error {
	s.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			s.err = err
			return
		}
		ServeEgress(ln)
		_, s.port, _ = net.SplitHostPort(ln.Addr().String())
	})
	return s.err
}

func (s *Seatbelt) Start(sp Spec) (Proc, error) {
	if err := s.Check(); err != nil {
		return Proc{}, err
	}
	profile := s.profile(sp)
	pf := sp.Stdout + ".sb"
	if err := os.WriteFile(pf, []byte(profile), 0o600); err != nil {
		return Proc{}, err
	}
	env := []string{}
	for _, kv := range sp.Env {
		if strings.Contains(kv, "127.0.0.1:"+ProxyPort) {
			kv = strings.ReplaceAll(kv, "127.0.0.1:"+ProxyPort, "127.0.0.1:"+s.port)
		}
		env = append(env, kv)
	}
	env = withPathPrefix(append(env, "PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"), toolDirs(sp.Tools))
	argv := append([]string{"sandbox-exec", "-f", pf}, sp.Argv...)
	return proc.StartDetached(argv, env, sp.Cwd, sp.Stdin, sp.Stdout)
}

func (s *Seatbelt) profile(sp Spec) string {
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
		} else if m.RW {
			fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", m.Host)
		}
	}
	fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%s\"))\n", s.port)
	b.WriteString("(allow network-outbound (literal \"/private/var/run/mDNSResponder\"))\n")
	return b.String()
}

func Exec(string) int    { fmt.Fprintln(os.Stderr, "sandbox-exec runs on Linux"); return 125 }
func Init(string) int    { return Exec("") }
func Inner([]string) int { return Exec("") }

// Run runs s to completion under Seatbelt.
func (s *Seatbelt) Run(sp Spec, timeout time.Duration) (int, error) {
	if err := s.Check(); err != nil {
		return -1, err
	}
	pf := sp.Stdout + ".sb"
	if err := os.WriteFile(pf, []byte(s.profile(sp)), 0o600); err != nil {
		return -1, err
	}
	env := withPathPrefix(append(s.env(sp), "PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"), toolDirs(sp.Tools))
	return runSync(append([]string{"sandbox-exec", "-f", pf}, sp.Argv...), env, sp.Cwd, sp.Stdin, sp.Stdout, timeout)
}

func (s *Seatbelt) env(sp Spec) []string {
	var env []string
	for _, kv := range sp.Env {
		env = append(env, strings.ReplaceAll(kv, "127.0.0.1:"+ProxyPort, "127.0.0.1:"+s.port))
	}
	return env
}
