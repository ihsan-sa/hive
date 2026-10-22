//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBwrapArgsKeepReach(t *testing.T) {
	a := initArgs{
		Spec: Spec{Argv: []string{"claude", "-p"}, Cwd: "/work", UID: 1000, GID: 1000,
			Mounts: []Mount{{Host: "/h/work", Inside: "/work", RW: true}, {Host: "/h/ref", Inside: "/ref"}}},
		Socket: "/rt/egress.sock",
		Tools:  map[string]string{"claude": "/h/bin/claude"},
	}
	got := strings.Join(bwrapArgs(a, "/h/hive"), " ")
	for _, want := range []string{
		"--unshare-all", "--die-with-parent", "--uid 1000 --gid 1000",
		"--bind /h/work /work", "--ro-bind /h/ref /ref", "--ro-bind /h/bin/claude /hive/bin/claude",
		"--bind /rt/egress.sock /hive/egress.sock", "--chdir /work", "--remount-ro /",
		"-- /hive/libexec/hive sandbox-inner --fwd -- claude -p",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bwrap args lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--share-net") || strings.Contains(got, "/home") {
		t.Errorf("bwrap args widen reach:\n%s", got)
	}
	a.Socket = ""
	if slices.Contains(bwrapArgs(a, "/h/hive"), "--fwd") {
		t.Error("no network must mean no forwarder")
	}
}

// TestNoSelfExecInTests: a test that reaches the sandbox in-process gets an
// error, never a re-run of its own test binary. It asks hiveBinary, not
// Probe, so that without the guard it fails rather than forking.
func TestNoSelfExecInTests(t *testing.T) {
	if self, err := hiveBinary(); err == nil || !strings.Contains(err.Error(), "test binary") {
		t.Fatalf("hiveBinary in a test = %q, %v; want a test binary refusal", self, err)
	}
}

// TestProbeBothPaths runs the real probe through the built binary on each
// path this host allows: the build's own namespaces and bubblewrap.
func TestProbeBothPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	ran := 0
	for _, path := range []struct{ name, env string }{{"namespaces", "0"}, {"bubblewrap", "1"}} {
		t.Run(path.name, func(t *testing.T) {
			if path.env == "1" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil {
				t.Skip("bwrap can't create namespaces here")
			}
			if path.env == "0" && (bwrapPath() != "" || exec.Command("unshare", "-Ur", "true").Run() != nil) {
				t.Skip("unprivileged user namespaces are restricted here")
			}
			cmd := exec.Command(bin, "sandbox-probe")
			cmd.Env = append(os.Environ(), "HIVE_SANDBOX_BWRAP="+path.env)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("probe: %v %s", err, out)
			}
			ran++
		})
	}
	t.Logf("%d of 2 sandbox paths ran on this host", ran)
}
