package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(string(raw), `Read-Host "`+"`n"+report.Question+` (y/N)"`) {
		t.Fatal("install.ps1 doesn't ask report.Question word for word")
	}
	if !strings.HasPrefix(shareQuestion, report.Question+" ") {
		t.Fatal("first run doesn't ask report.Question")
	}
}

// The installer's question: Enter and no terminal are kept as no, y as yes,
// and an install already answered isn't asked.
func TestInstallerShareAnswer(t *testing.T) {
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
			fake := "#!/bin/sh\n[ \"$*\" = \"report asked\" ] && exit " + asked + "\necho \"$*\" >> '" + filepath.ToSlash(log) + "'\n" // sh on Windows eats a path's backslashes
			os.WriteFile(filepath.Join(dir, "hive"), []byte(fake), 0o755)
			if c.noTTY {
				tty = filepath.Join(dir, "missing", "tty")
			} else {
				os.WriteFile(tty, []byte(c.typed), 0o600)
			}
			cmd := exec.Command(sh, "-c", block)
			cmd.Env = append(os.Environ(), "DIR="+dir, "CMD=hive", "HIVE_TTY="+tty, "HIVE_SHARE=")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, _ := os.ReadFile(log)
			if strings.TrimSpace(string(got)) != c.want {
				t.Fatalf("installer ran %q, want %q", got, c.want)
			}
			if asked, _ := os.ReadFile(tty); !c.noTTY && !c.asked && !strings.Contains(string(asked), "\n"+report.Question+" (y/N) ") {
				t.Fatalf("the installer asked something other than report.Question:\n%s", asked)
			}
		})
	}
}
