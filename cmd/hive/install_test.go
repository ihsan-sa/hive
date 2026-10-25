package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installer runs this checkout's install.sh against a stub release of a fake
// hive, with env added; it returns the output, whether it succeeded, and the
// URLs curl was asked for. tests/install/run.sh runs the root path in a
// container; these cover what runs without root.
func installer(t *testing.T, home string, env ...string) (string, bool, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the Linux and macOS installer")
	}
	for _, tool := range []string{"sh", "install", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		t.Skip("no release for " + runtime.GOARCH)
	}
	script, err := filepath.Abs("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	serve, stubs := filepath.Join(dir, "release"), filepath.Join(dir, "stubs")
	os.MkdirAll(serve, 0o755)
	os.MkdirAll(stubs, 0o755)
	asset := "hive-" + runtime.GOOS + "-" + arch
	bin := "#!/bin/sh\ncase \"$*\" in --version) echo hive v9.9.9 ;; report*) exit 0 ;; *) exit 1 ;; esac\n"
	os.WriteFile(filepath.Join(serve, asset), []byte(bin), 0o755)
	sum := sha256.Sum256([]byte(bin))
	os.WriteFile(filepath.Join(serve, "checksums.txt"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644)
	calls := filepath.Join(dir, "curl-calls")
	curl := "#!/bin/sh\necho \"$2\" >> '" + calls + "'\ncp \"" + serve + "/${2##*/}\" \"$4\"\n"
	os.WriteFile(filepath.Join(stubs, "curl"), []byte(curl), 0o755)
	for _, e := range env {
		if k := "FAKE_UNAME="; strings.HasPrefix(e, k) { // uname -s says this; -m is the real one
			real, _ := exec.LookPath("uname")
			uname := "#!/bin/sh\n[ \"$1\" = -s ] && { echo " + e[len(k):] + "; exit; }\nexec " + real + " \"$@\"\n"
			os.WriteFile(filepath.Join(stubs, "uname"), []byte(uname), 0o755)
		}
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+stubs+":"+os.Getenv("PATH"), "HOME="+home, "HIVE_USER=", "HIVE_REPO=", "HIVE_BIN_DIR=",
		"HIVE_SHARE=no", "HIVE_TTY="+filepath.Join(dir, "no", "tty"))
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	raw, _ := os.ReadFile(calls)
	return string(out), err == nil, strings.Fields(string(raw))
}

// A release without this system's binary (Linux only for now, issue #564)
// says so before downloading anything but the checksums.
func TestInstallerSaysLinuxOnly(t *testing.T) {
	out, ok, calls := installer(t, t.TempDir(), "FAKE_UNAME=Darwin")
	if ok || !strings.Contains(out, "Linux only for now") || !strings.Contains(out, "issues/564") {
		t.Fatalf("want the Linux-only message, got ok=%v:\n%s", ok, out)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "/checksums.txt") {
		t.Fatalf("it fetched %v", calls)
	}
}

// A repo name is owner/name, matched whole: anything else is refused before
// a download, so a name can't stand for a pattern or a path.
func TestInstallerRefusesOddRepoNames(t *testing.T) {
	for _, repo := range []string{"ihsan-sa", "ihsan-sa/hive/x", "/hive", "ihsan-sa/", "ihsan-sa/hiv*", "a/x y", "a/x$(id)", "../../x/y", "../x", "a/.git"} {
		out, ok, calls := installer(t, t.TempDir(), "HIVE_REPO="+repo)
		if ok || len(calls) > 0 || !strings.Contains(out, "must be owner/name") {
			t.Fatalf("HIVE_REPO=%q: ok %v, fetched %v:\n%s", repo, ok, calls, out)
		}
	}
	if out, ok, _ := installer(t, t.TempDir(), "HIVE_REPO=a/x.y"); !ok {
		t.Fatalf("a/x.y is a real repo name:\n%s", out)
	}
}

// HIVE_USER installs for another user, which only root can do; anyone else
// is refused before anything is fetched or written.
func TestInstallerForAUserNeedsRoot(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getuid() == 0 {
		t.Skip("the non-root refusal, on Linux")
	}
	home := t.TempDir()
	out, ok, calls := installer(t, home, "HIVE_USER=worker")
	if ok || len(calls) > 0 || !strings.Contains(out, "needs root") {
		t.Fatalf("ok %v, fetched %v:\n%s", ok, calls, out)
	}
	if entries, _ := os.ReadDir(home); len(entries) > 0 {
		t.Fatalf("the refused install wrote %v", entries)
	}
}

// A write the installer can't make stops it: a bin folder linked to one the
// user can't write (as a worker's ~/.local/bin -> /etc/cron.hourly would be)
// fails the install, which writes nothing there and doesn't say it installed.
func TestInstallerStopsOnAFailedWrite(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere; tests/install/run.sh covers root")
	}
	home, locked := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(home, ".local"), 0o755)
	if err := os.Symlink(locked, filepath.Join(home, ".local", "bin")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(locked, 0o555)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	out, ok, _ := installer(t, home)
	if ok || strings.Contains(out, "Installed.") {
		t.Fatalf("the install went on past a failed write:\n%s", out)
	}
	if entries, _ := os.ReadDir(locked); len(entries) > 0 {
		t.Fatalf("it wrote %v through the link", entries)
	}
	// The same for a shell profile linked to a file the user can't write.
	home = t.TempDir()
	ro := filepath.Join(t.TempDir(), "profile")
	os.WriteFile(ro, []byte("# someone else's\n"), 0o444)
	os.Symlink(ro, filepath.Join(home, ".profile"))
	out, ok, _ = installer(t, home)
	if ok || strings.Contains(out, "Installed.") {
		t.Fatalf("the install went on past a failed profile write:\n%s", out)
	}
	if raw, _ := os.ReadFile(ro); string(raw) != "# someone else's\n" {
		t.Fatalf("the profile changed: %q", raw)
	}
}

// The PATH line goes in unless the profile already names the bin folder
// itself: a folder with a dot in its name isn't taken for a lookalike.
func TestInstallerMatchesThePathWhole(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "a.b", "bin")
	os.WriteFile(filepath.Join(home, ".profile"), []byte("export PATH=\""+filepath.Join(home, "aXb", "bin")+":$PATH\"\n"), 0o644)
	out, ok, _ := installer(t, home, "HIVE_BIN_DIR="+bin)
	if !ok {
		t.Fatalf("install:\n%s", out)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".profile"))
	if !strings.Contains(string(raw), "export PATH=\""+bin+":$PATH\"") {
		t.Fatalf("no PATH line for %s:\n%s", bin, raw)
	}
	// A rerun doesn't add it twice.
	installer(t, home, "HIVE_BIN_DIR="+bin)
	if again, _ := os.ReadFile(filepath.Join(home, ".profile")); strings.Count(string(again), bin) != 1 {
		t.Fatalf("the rerun added it again:\n%s", again)
	}
}

// The system unit install.sh writes as root runs as the worker, sees only
// its home writable and the system read-only, and gets the token files as
// systemd credentials. It is built here from install.sh's own lines.
func TestInstallerUnitIsSandboxed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh's unit is for Linux")
	}
	raw, err := os.ReadFile("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	from, to := strings.Index(src, `  unit="hive-$user.service"`), strings.Index(src, `>"$tmp/unit"`)
	if from < 0 || to < from {
		t.Fatal("the unit block moved in install.sh")
	}
	dir := t.TempDir()
	etc := filepath.Join(dir, "etc")
	os.MkdirAll(filepath.Join(etc, "tokens"), 0o700)
	for _, o := range []string{"ihsan-sa", "hive-rsi-lab"} {
		os.WriteFile(filepath.Join(etc, "tokens", o+".token"), nil, 0o600)
	}
	cmd := exec.Command("sh", "-c", src[from:to+len(`>"$tmp/unit"`)]+"\n")
	cmd.Env = append(os.Environ(), "user=worker", "home=/home/worker", "CMD=hive", "etc="+etc, "tmp="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	unit, _ := os.ReadFile(filepath.Join(dir, "unit"))
	lines := map[string]bool{}
	for _, l := range strings.Split(string(unit), "\n") {
		lines[l] = true
	}
	for _, want := range []string{"User=worker", "ExecStart=/home/worker/.local/bin/hive daemon", "ProtectSystem=strict", "ProtectHome=tmpfs",
		"BindPaths=/home/worker", "PrivateTmp=yes", "NoNewPrivileges=yes", "KillMode=process", "Restart=on-failure",
		"LoadCredential=ihsan-sa.token:" + filepath.Join(etc, "tokens", "ihsan-sa.token"),
		"LoadCredential=hive-rsi-lab.token:" + filepath.Join(etc, "tokens", "hive-rsi-lab.token")} {
		if !lines[want] {
			t.Errorf("the unit lacks %s:\n%s", want, unit)
		}
	}
	// Each agent's sandbox mounts a fresh proc in a new user namespace, which
	// the kernel refuses while /proc or /sys has mounts laid over it.
	for _, bad := range []string{"ProtectKernelTunables", "ProtectKernelLogs", "ProtectControlGroups", "ProtectProc", "ProcSubset"} {
		if strings.Contains(string(unit), "\n"+bad+"=") {
			t.Errorf("the unit sets %s, which stops agents' sandboxes:\n%s", bad, unit)
		}
	}
}
