//go:build windows

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// WSL runs the Linux namespace sandbox inside a WSL 2 distribution. The
// Windows daemon writes the spec with /mnt/<drive> paths and starts the
// Linux hive binary's `sandbox-exec` through wsl.exe.
type WSL struct {
	Distro    string
	HiveLinux string // Windows path of the linux/amd64 hive binary

	once     sync.Once
	uid, gid int
	err      error
}

func platform(o Options) Sandbox {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return nil
	}
	d := o.WSLDistro
	if d == "" {
		d = DefaultDistro()
	}
	if d == "" {
		return nil
	}
	return &WSL{Distro: d, HiveLinux: o.HiveLinux}
}

func (w *WSL) Name() string   { return "wsl:" + w.Distro }
func (w *WSL) Isolated() bool { return true }

// Check verifies the distro runs, finds the person's Linux uid, and runs the probe.
func (w *WSL) Check() error {
	w.once.Do(func() {
		if w.HiveLinux == "" || !fileExists(w.HiveLinux) {
			w.err = errors.New("the Linux hive binary is missing; reinstall")
			return
		}
		out, err := w.run(30*time.Second, "id", "-u")
		if err != nil {
			w.err = fmt.Errorf("WSL distro %q did not start: %v", w.Distro, err)
			return
		}
		w.uid, _ = strconv.Atoi(strings.TrimSpace(out))
		out, _ = w.run(30*time.Second, "id", "-g")
		w.gid, _ = strconv.Atoi(strings.TrimSpace(out))
		if _, err := w.run(90*time.Second, WinToWSL(w.HiveLinux), "sandbox-probe"); err != nil {
			w.err = fmt.Errorf("sandbox probe in WSL failed: %v", err)
		}
	})
	return w.err
}

// Run executes a command in the distro (outside any sandbox) and returns stdout.
func (w *WSL) run(timeout time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", append([]string{"-d", w.Distro, "--exec"}, argv...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()+" "+out.String()))
	}
	return out.String(), nil
}

// Exec runs argv inside the distro, outside the sandbox (for setup checks).
func (w *WSL) Exec(timeout time.Duration, argv ...string) (string, error) {
	return w.run(timeout, argv...)
}

func (w *WSL) Start(s Spec) (Proc, error) {
	if err := w.Check(); err != nil {
		return Proc{}, err
	}
	hostStdout := s.Stdout
	s = w.translate(s)
	specPath, err := writeTempSpec(s)
	if err != nil {
		return Proc{}, err
	}
	argv := []string{"wsl.exe", "-d", w.Distro, "--exec", WinToWSL(w.HiveLinux), "sandbox-exec", "--spec", WinToWSL(specPath)}
	return proc.StartDetached(argv, os.Environ(), "", "", hostStdout+".wsl")
}

// WinToWSL maps C:\x\y to /mnt/c/x/y (the default automount root).
func WinToWSL(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if len(p) >= 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + filepath.ToSlash(p[2:])
	}
	return filepath.ToSlash(p)
}

// DefaultDistro returns the default WSL 2 distribution, skipping Docker's.
func DefaultDistro() string {
	out, err := exec.Command("wsl.exe", "-l", "-v").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(decodeWSL(out), "\n")
	var first string
	for _, l := range lines[1:] {
		def := strings.HasPrefix(strings.TrimSpace(l), "*")
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "*"))
		if len(f) < 3 || f[2] != "2" || strings.HasPrefix(f[0], "docker-desktop") {
			continue
		}
		if def {
			return f[0]
		}
		if first == "" {
			first = f[0]
		}
	}
	return first
}

// decodeWSL decodes wsl.exe's UTF-16LE output.
func decodeWSL(b []byte) string {
	if len(b) >= 2 && b[1] == 0 {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return strings.ReplaceAll(string(utf16.Decode(u)), "\r", "")
	}
	return strings.ReplaceAll(string(b), "\r", "")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// Exec and Init only run on Linux.
func Exec(string) int    { fmt.Fprintln(os.Stderr, "sandbox-exec runs on Linux"); return 125 }
func Init(string) int    { return Exec("") }
func Inner([]string) int { return Exec("") }

// Run runs s to completion inside the distro's sandbox.
func (w *WSL) Run(s Spec, timeout time.Duration) (int, error) {
	if err := w.Check(); err != nil {
		return -1, err
	}
	hostStdout := s.Stdout
	s = w.translate(s)
	specPath := hostStdout + ".sandbox.json"
	if err := WriteSpec(specPath, s); err != nil {
		return -1, err
	}
	argv := []string{"wsl.exe", "-d", w.Distro, "--exec", WinToWSL(w.HiveLinux), "sandbox-exec", "--spec", WinToWSL(specPath)}
	return runSync(argv, os.Environ(), "", "", hostStdout+".wsl", timeout)
}

func (w *WSL) translate(s Spec) Spec {
	s.UID, s.GID = w.uid, w.gid
	mounts := make([]Mount, len(s.Mounts))
	for i, m := range s.Mounts {
		m.Host = WinToWSL(m.Host)
		mounts[i] = m
	}
	s.Mounts = mounts
	tools := make([]string, len(s.Tools))
	for i, t := range s.Tools {
		if filepath.IsAbs(t) {
			t = WinToWSL(t)
		}
		tools[i] = t
	}
	s.Tools = tools
	s.Stdin, s.Stdout, s.StopFile = WinToWSL(s.Stdin), WinToWSL(s.Stdout), WinToWSL(s.StopFile)
	return s
}
