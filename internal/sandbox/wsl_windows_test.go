//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A spec under a %TEMP% with an 8.3 short name (C:\Users\JOHNSM~1\...) is
// handed to WSL by its long name, which /mnt/c can open.
func TestWinToWSLExpandsShortNames(t *testing.T) {
	long := filepath.Join(t.TempDir(), "a long folder name")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(long, "hive-spec-1.json")
	if err := os.WriteFile(spec, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := syscall.UTF16PtrFromString(spec)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	short := syscall.UTF16ToString(buf[:n])
	if err != nil || !strings.Contains(short, "~") {
		t.Skip("this volume makes no 8.3 short names")
	}
	got := WinToWSL(short)
	if strings.Contains(got, "~") || !strings.HasSuffix(got, "/a long folder name/hive-spec-1.json") || !strings.HasPrefix(got, "/mnt/") {
		t.Fatalf("WinToWSL(%q) = %q, want the long path under /mnt", short, got)
	}
	// A path that doesn't exist yet keeps its name.
	if got := WinToWSL(`C:\Users\JOHNSM~1\gone\x.json`); got != "/mnt/c/Users/JOHNSM~1/gone/x.json" {
		t.Fatalf("missing path: %q", got)
	}
}
