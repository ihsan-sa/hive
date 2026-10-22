package setup

import (
	"os"
	"path/filepath"
	"runtime"
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
