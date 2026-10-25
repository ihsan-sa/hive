package sandbox

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pdeathsig (dieWithDaemon, and the gate's dieWithGate) fires when the OS
// thread that started the child ends, and Go ends a thread when a goroutine
// that locked it returns still locked: a check started from one would be
// killed mid-run. So no code in the daemon, the gate or anything they call
// locks a thread. The one exception is sandbox-exec's own launcher
// (lowerPriority), a process of its own whose main goroutine holds the
// thread until it exits.
func TestNoDaemonCodeLocksAThread(t *testing.T) {
	allowed := map[string]bool{"internal/sandbox/linux.go": true}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("runtime.LockOSThread()")) && !allowed[rel] {
			t.Errorf("%s locks an OS thread: a child it starts there dies with the thread (dieWithDaemon)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
