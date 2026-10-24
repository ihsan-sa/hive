package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Every agent finds what the Hive installed on PATH: inside the sandbox from
// InsideEnv, and on host paths (bare, Seatbelt) from the tools folder's mount.
func TestToolsOnPath(t *testing.T) {
	var path string
	for _, kv := range InsideEnv("/hive/home", false) {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, n := range ToolNames {
		if !strings.Contains(path, "/hive/tools/"+n+"/bin:") {
			t.Fatalf("PATH %q lacks %s", path, n)
		}
	}
	folder := filepath.Join(t.TempDir(), "tools")
	dirs := pathDirs(Spec{Mounts: []Mount{{Host: "/elsewhere", Inside: "/hive/kb"}, {Host: folder, Inside: ToolsInside}}})
	if !slices.Contains(dirs, filepath.Join(folder, "python", "bin")) || slices.Contains(dirs, filepath.Join("/elsewhere", "python", "bin")) {
		t.Fatalf("host PATH dirs: %q", dirs)
	}
}

// A python the Hive installed wins over an older one in the folder hive
// itself runs from (say /usr/local/bin) on host paths.
func TestInstalledToolShadowsHostRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host paths run under WSL on Windows")
	}
	host, folder := t.TempDir(), t.TempDir()
	mine := filepath.Join(folder, "python", "bin")
	for _, d := range []string{host, mine} {
		os.MkdirAll(d, 0o755)
		if err := os.WriteFile(filepath.Join(d, "python3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := withPathPrefix([]string{"PATH=/usr/bin:/bin"}, pathDirs(Spec{Tools: []string{filepath.Join(host, "hive")}, Mounts: []Mount{{Host: folder, Inside: ToolsInside}}}))
	t.Setenv("PATH", strings.TrimPrefix(env[0], "PATH="))
	if got, err := exec.LookPath("python3"); err != nil || got != filepath.Join(mine, "python3") {
		t.Fatalf("python3 resolves to %q (%v), not the installed one", got, err)
	}
}
