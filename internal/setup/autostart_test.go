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
