//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A spec's Tmp is the sandbox's /tmp: a folder on disk, fresh each command
// and gone after it, never a tmpfs in memory; /dev/shm and a /tmp with no
// folder are tmpfs capped in size. Unit half: bwrap's command line.
func TestBwrapTmpOnDisk(t *testing.T) {
	a := initArgs{Spec: Spec{Argv: []string{"true"}, Cwd: "/", Tmp: "/h/run/tmp"}}
	got := strings.Join(bwrapArgs(a, "/h/hive"), " ")
	if !strings.Contains(got, "--bind /h/run/tmp /tmp") || strings.Contains(got, "--tmpfs /tmp") {
		t.Errorf("/tmp isn't the spec's folder:\n%s", got)
	}
	a.Spec.Tmp = ""
	if got := strings.Join(bwrapArgs(a, "/h/hive"), " "); !strings.Contains(got, "--tmpfs /tmp") {
		t.Errorf("no folder must mean a tmpfs /tmp:\n%s", got)
	}
}

// The real sandbox on each path this host allows.
func TestTmpOnDiskBothPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, path := range []struct{ name, env string }{{"namespaces", "0"}, {"bubblewrap", "1"}} {
		t.Run(path.name, func(t *testing.T) {
			if path.env == "1" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil {
				t.Skip("bwrap can't create namespaces here")
			}
			if path.env == "0" && (bwrapPath() != "" || exec.Command("unshare", "-Ur", "true").Run() != nil) {
				t.Skip("unprivileged user namespaces are restricted here")
			}
			dir := t.TempDir()
			work, tmp := filepath.Join(dir, "work"), filepath.Join(dir, "tmp")
			os.MkdirAll(work, 0o755)
			os.MkdirAll(tmp, 0o755)
			os.WriteFile(filepath.Join(tmp, "stale"), nil, 0o644)
			os.MkdirAll(filepath.Join(tmp, "stale-ro"), 0o755)
			os.WriteFile(filepath.Join(tmp, "stale-ro", "f"), nil, 0o644)
			os.Chmod(filepath.Join(tmp, "stale-ro"), 0o555)
			run := func(tmpDir string) string {
				s := Spec{
					ID:     "tmp",
					Argv:   []string{"/bin/sh", "-c", "test ! -e /tmp/stale && test ! -e /tmp/stale-ro && echo x > /tmp/f && mkdir -p /tmp/ro/d && touch /tmp/ro/d/f && chmod 555 /tmp/ro/d && grep -E ' /(tmp|dev/shm) ' /proc/self/mounts > /work/mounts"},
					Env:    InsideEnv("/tmp", false),
					Cwd:    "/work",
					Mounts: []Mount{{Host: work, Inside: "/work", RW: true}},
					Stdout: filepath.Join(dir, "out"),
					Tmp:    tmpDir,
					UID:    os.Getuid(), GID: os.Getgid(),
				}
				spec := filepath.Join(dir, "spec.json")
				if err := WriteSpec(spec, s); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(bin, "sandbox-exec", "--spec", spec)
				cmd.Env = append(os.Environ(), "HIVE_SANDBOX_BWRAP="+path.env)
				if out, err := cmd.CombinedOutput(); err != nil {
					e, _ := os.ReadFile(s.Stdout + ".err")
					t.Fatalf("sandbox-exec: %v %s %s", err, out, e)
				}
				raw, _ := os.ReadFile(filepath.Join(work, "mounts"))
				return string(raw)
			}
			mounts := run(tmp)
			for _, l := range strings.Split(strings.TrimSpace(mounts), "\n") {
				f := strings.Fields(l)
				if len(f) < 4 {
					continue
				}
				switch {
				case f[1] == "/tmp" && f[2] == "tmpfs":
					t.Errorf("/tmp is a tmpfs with a folder given: %s", l)
				case f[1] == "/dev/shm" && !strings.Contains(f[3], "size=1048576k"):
					t.Errorf("/dev/shm isn't capped: %s", l)
				}
			}
			if _, err := os.Stat(tmp); !os.IsNotExist(err) {
				t.Errorf("the /tmp folder outlived the command: %v", err)
			}
			if mounts := run(""); !strings.Contains(mounts, " /tmp tmpfs ") || !strings.Contains(mounts, "size=4194304k") {
				t.Errorf("no folder must mean a capped tmpfs /tmp:\n%s", mounts)
			}
		})
	}
}

// A command's /tmp goes whole even when it holds read-only folders, as a
// Go module cache's are: a plain remove leaves them, and they pile up turn
// after turn.
func TestRemoveTreeReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	ro := filepath.Join(dir, "gomod", "x@v1")
	os.MkdirAll(ro, 0o755)
	os.WriteFile(filepath.Join(ro, "f"), nil, 0o444)
	os.Chmod(ro, 0o555)
	if err := removeTree(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s outlived removeTree: %v", dir, err)
	}
}
