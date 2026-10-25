package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
)

// The installer answers before there is a Hive; first run takes that answer
// without asking, and `report asked` tells a reinstall not to ask again.
func TestInstallerAnswerReachesFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("HOME", dir)
	t.Setenv("HIVE_HOME", filepath.Join(dir, "hive"))
	if cmdReport([]string{"asked"}) != 1 {
		t.Fatal("nothing answered yet, but asked says yes")
	}
	if cmdReport([]string{"on"}) != 0 || cmdReport([]string{"asked"}) != 0 {
		t.Fatal("the installer's answer wasn't kept")
	}
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	if err := askShare(ws); err != nil {
		t.Fatal(err)
	}
	if cfg := ws.LoadConfig(); cfg.InstallID == "" {
		t.Fatalf("first run didn't take the installer's yes: %+v", cfg)
	} else if _, on := cfg.Sharing(); !on {
		t.Fatalf("first run didn't take the installer's yes: %q", cfg.Share)
	}
	if cmdReport([]string{"off"}) != 0 {
		t.Fatal("report off failed")
	}
	if err := askShare(ws); err != nil { // answered: no prompt, nothing changes
		t.Fatal(err)
	}
	if cfg := ws.LoadConfig(); cfg.Share != "off" || cmdReport([]string{"asked"}) != 0 {
		t.Fatalf("want off and answered, got %q", cfg.Share)
	}
}

// Both installers and first run ask report.Question word for word, which its
// own test holds to what is sent.
func TestInstallersAskTheQuestion(t *testing.T) {
	raw, err := os.ReadFile("../../install/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `Read-Host "`+"`n"+report.Question+` (off/failures/everything)"`) {
		t.Fatal("install.ps1 doesn't ask report.Question word for word")
	}
	if !strings.HasPrefix(shareQuestion, report.Question+" ") {
		t.Fatal("first run doesn't ask report.Question")
	}
}

// The installer's question: Enter and no terminal are kept as no, y as yes,
// and an install already answered isn't asked.
func TestInstallerShareAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the Linux and macOS installer; Windows runs install.ps1")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	raw, err := os.ReadFile("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	from, to := strings.Index(src, "# Sharing failures"), strings.LastIndex(src, "esac")
	if from < 0 || to < from {
		t.Fatal("the sharing block moved in install.sh")
	}
	block := src[from:to+len("esac")] + "\n"
	for _, c := range []struct {
		name, typed, want string
		asked             bool
		noTTY             bool
	}{
		{name: "Enter", typed: "\n", want: "report off"},
		{name: "yes", typed: "y\n", want: "report on"},
		{name: "failures", typed: "failures\n", want: "report on"},
		{name: "everything", typed: "everything\n", want: "report everything"},
		{name: "off", typed: "off\n", want: "report off"},
		{name: "no", typed: "n\n", want: "report off"},
		{name: "no terminal", noTTY: true, want: "report off"},
		{name: "answered before", typed: "y\n", asked: true, want: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			log, tty := filepath.Join(dir, "calls"), filepath.Join(dir, "tty")
			asked := "1"
			if c.asked {
				asked = "0"
			}
			// sh on Windows (Git's) reads a backslash as an escape, so it gets
			// the paths with forward slashes.
			fake := "#!/bin/sh\n[ \"$*\" = \"report asked\" ] && exit " + asked + "\necho \"$*\" >> '" + filepath.ToSlash(log) + "'\n"
			os.WriteFile(filepath.Join(dir, "hive"), []byte(fake), 0o755)
			if c.noTTY {
				tty = filepath.Join(dir, "missing", "tty")
			} else {
				os.WriteFile(tty, []byte(c.typed), 0o600)
			}
			cmd := exec.Command(sh, "-c", block)
			cmd.Env = append(os.Environ(), "DIR="+filepath.ToSlash(dir), "CMD=hive", "HIVE_TTY="+filepath.ToSlash(tty), "HIVE_SHARE=")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, _ := os.ReadFile(log)
			if strings.TrimSpace(string(got)) != c.want {
				t.Fatalf("installer ran %q, want %q", got, c.want)
			}
			if asked, _ := os.ReadFile(tty); !c.noTTY && !c.asked && !strings.Contains(string(asked), "\n"+report.Question+" (off/failures/everything) ") {
				t.Fatalf("the installer asked something other than report.Question:\n%s", asked)
			}
		})
	}
}

// The update workflow runs this checkout's install.sh to install an older
// release, which may not have the commands the installer now calls: before
// v0.1.18 there is no `report`, and an unknown command exits 2. The installer
// must still install it, without asking about sharing it can't record.
func TestInstallerOldReleases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the Linux and macOS installer; Windows runs install.ps1")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, tool := range []string{"curl", "install", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	installer, err := filepath.Abs("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		t.Skip("no release for " + runtime.GOARCH)
	}
	asset := "hive-" + runtime.GOOS + "-" + arch
	// Old releases answer what main.go answered then: --version and shutdown,
	// and "unknown command" with exit 2 for the rest. The current one has
	// `report`, unanswered.
	old := "#!/bin/sh\necho \"$*\" >> \"$CALLS\"\ncase \"$1\" in --version) echo \"$0 %s\" ;; shutdown) exit 1 ;; *) echo \"unknown command \\\"$1\\\"\" >&2; exit 2 ;; esac\n"
	current := "#!/bin/sh\necho \"$*\" >> \"$CALLS\"\ncase \"$*\" in --version) echo \"$0 v9.9.9\" ;; shutdown) exit 1 ;; 'report asked') exit 1 ;; 'report on'|'report off') ;; *) exit 2 ;; esac\n"
	for _, c := range []struct {
		name, version, cmd, bin, share, want string
	}{
		{name: "v0.1.2 as hive-v0", version: "v0.1.2", cmd: "hive-v0", bin: fmt.Sprintf(old, "v0.1.2")},
		{name: "v0.1.17, the last full release", version: "v0.1.17", cmd: "hive", bin: fmt.Sprintf(old, "v0.1.17")},
		{name: "v0.1.17 with an answer ahead", version: "v0.1.17", cmd: "hive", bin: fmt.Sprintf(old, "v0.1.17"), share: "yes"},
		{name: "a release with report", version: "v9.9.9", cmd: "hive", bin: current, want: "report off"},
		{name: "a release with report, answered ahead", version: "v9.9.9", cmd: "hive", bin: current, share: "yes", want: "report on"},
		{name: "a release with report but no levels, everything ahead", version: "v9.9.9", cmd: "hive", bin: current, share: "everything", want: "report everything; report on"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			serve, stubs, bin := filepath.Join(dir, "release"), filepath.Join(dir, "stubs"), filepath.Join(dir, "bin")
			for _, d := range []string{serve, stubs} {
				os.MkdirAll(d, 0o755)
			}
			os.WriteFile(filepath.Join(serve, asset), []byte(c.bin), 0o755)
			sum := sha256.Sum256([]byte(c.bin))
			os.WriteFile(filepath.Join(serve, "checksums.txt"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644)
			// curl -fsSL <url> -o <out> fetches the file the URL ends in, from this release only.
			curl := "#!/bin/sh\ncase \"$2\" in */releases/download/" + c.version + "/*) ;; *) echo \"unexpected url $2\" >&2; exit 22 ;; esac\ncp \"" + serve + "/${2##*/}\" \"$4\"\n"
			os.WriteFile(filepath.Join(stubs, "curl"), []byte(curl), 0o755)
			calls := filepath.Join(dir, "calls")
			cmd := exec.Command(sh, installer)
			cmd.Env = append(os.Environ(), "PATH="+stubs+":"+bin+":"+os.Getenv("PATH"), "HOME="+dir, "CALLS="+calls,
				"HIVE_VERSION="+c.version, "HIVE_CMD="+c.cmd, "HIVE_REPO=ihsan-sa/hive", "HIVE_BIN_DIR="+bin,
				"HIVE_SHARE="+c.share, "HIVE_TTY="+filepath.Join(dir, "no", "tty"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("installing %s: %v\n%s", c.version, err, out)
			}
			if !strings.Contains(string(out), "Installed. Run: "+c.cmd) {
				t.Fatalf("the installer didn't finish:\n%s", out)
			}
			raw, _ := os.ReadFile(calls)
			var report []string
			for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.HasPrefix(l, "report ") && l != "report asked" {
					report = append(report, l)
				}
			}
			if got := strings.Join(report, "; "); got != c.want {
				t.Fatalf("the installer ran %q on %s, want %q; all calls:\n%s", got, c.version, c.want, raw)
			}
		})
	}
}
