package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Without a usable systemd, first run falls back to the desktop autostart
// and leaves no boot unit behind.
func TestAutostartFallbackLeavesNoUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // no systemctl
	if err := EnableAutostart(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "hive.service")); err == nil {
		t.Fatal("the boot unit was left behind")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "autostart", "hive.desktop")); err != nil {
		t.Fatalf("no desktop autostart: %v", err)
	}
}

// The daemon knows it runs in the Hive's unit from its cgroup.
func TestInUnit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cgroup")
	defer func(s string) { cgroupFile = s }(cgroupFile)
	cgroupFile = f
	for line, want := range map[string]bool{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/hive.service\n": true,
		"0::/user.slice/user-1000.slice/session-3.scope\n":                          false,
	} {
		os.WriteFile(f, []byte(line), 0o644)
		if InUnit() != want {
			t.Errorf("InUnit for %q: want %v", line, want)
		}
	}
}

// A handover rewrites the macOS plist without touching launchd, so unloading
// the job can't stop the daemon doing the handover.
func TestHandoverLeavesLaunchdAlone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd is macOS's")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	called := filepath.Join(bin, "called")
	os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\necho \"$@\" >> "+called+"\n"), 0o755)
	if err := EnableAutostartAt("/new/hive", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(launchAgent()); err != nil || !strings.Contains(string(raw), "<string>/new/hive</string>") || !strings.Contains(string(raw), "<key>AbandonProcessGroup</key><true/>") {
		t.Fatalf("plist %q, %v", raw, err)
	}
	if raw, err := os.ReadFile(called); err == nil {
		t.Fatalf("the handover ran launchctl %q", raw)
	}
}

// The daemon names any systemd unit it runs in, user or system, and only the
// person's user unit hive.service counts as the Hive's own.
func TestUnit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cgroup")
	defer func(s string) { cgroupFile = s }(cgroupFile)
	cgroupFile = f
	for _, c := range []struct {
		line, name string
		user, own  bool
	}{
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/hive.service\n", "hive.service", true, true},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agents.service\n", "agents.service", true, false},
		{"0::/system.slice/hive.service\n", "hive.service", false, false},
		{"0::/system.slice/hive-ihsan.service\n", "hive-ihsan.service", false, false},
		{"12:cpu,cpuacct:/system.slice/x.service\n1:name=systemd:/system.slice/hive@box.service\n", "hive@box.service", false, false},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", "", false, false},
	} {
		os.WriteFile(f, []byte(c.line), 0o644)
		if name, user := Unit(); name != c.name || user != c.user {
			t.Errorf("Unit for %q: got %q, %v", c.line, name, user)
		}
		if InUnit() != c.own {
			t.Errorf("InUnit for %q: want %v", c.line, c.own)
		}
	}
}

// A unit's Restart= and KillMode= come from systemctl, --user for a user unit.
func TestUnitSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in systemctl is a shell script")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	called := filepath.Join(bin, "called")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> "+called+"\nprintf 'Restart=on-failure\\nKillMode=control-group\\nMainPID=4242\\n'\n"), 0o755)
	if restart, kill, pid := UnitSettings("hive-box.service", false); restart != "on-failure" || kill != "control-group" || pid != 4242 {
		t.Fatalf("got Restart=%q KillMode=%q MainPID=%d", restart, kill, pid)
	}
	UnitSettings("agents.service", true)
	raw, _ := os.ReadFile(called)
	if want := "show -p Restart -p KillMode -p MainPID hive-box.service\n--user show -p Restart -p KillMode -p MainPID agents.service\n"; string(raw) != want {
		t.Fatalf("systemctl ran %q, want %q", raw, want)
	}
}

// A desktop autostart left from a start without systemd goes once the unit
// is enabled, so a restart and the next login don't each start a daemon.
func TestUnitReplacesALoginAutostart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	desk := filepath.Join(home, ".config", "autostart", "hive.desktop")
	os.MkdirAll(filepath.Dir(desk), 0o755)
	os.WriteFile(desk, []byte("[Desktop Entry]\nExec=env HIVE_HOME=/w /x/hive daemon\n"), 0o644)
	root := t.TempDir()
	if err := EnableAutostartAt("/x/hive", root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(desk); err == nil {
		t.Fatal("the login autostart is still there beside the unit")
	}
	if !StartUnit(root) {
		t.Fatal("the unit serving the workspace wasn't started")
	}
	if StartUnit(t.TempDir()) {
		t.Fatal("a unit serving another workspace was started")
	}
}
