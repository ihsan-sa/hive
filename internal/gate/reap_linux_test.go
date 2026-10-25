package gate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// daemonsUnder are the `hive daemon` processes a gate run in out started.
func daemonsUnder(out string) []int {
	var pids []int
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		argv := bytes.Split(cmdline, []byte{0})
		if len(argv) < 2 || string(argv[1]) != "daemon" {
			continue
		}
		env, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if under(envValue(bytes.Split(env, []byte{0}), rootEnv), out) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// A gate killed mid-run (no deferred cleanup runs) leaves its child's
// daemon orphaned, detached from every group the gate could kill; the next
// gate ends it, and the daemon's Reap of the run folder ends it and removes
// the run's folders.
func TestKilledGateLeavesNoDaemon(t *testing.T) {
	hive := builtHive(t)
	dir, err := os.MkdirTemp("", "gate-kill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAll(dir) })
	out := filepath.Join(dir, "out")
	log, _ := os.Create(filepath.Join(dir, "gate.log"))
	defer log.Close()
	g := exec.Command(hive, "gate", "--self-test", "--out", out)
	// Bare, as a self-test inside a side's sandbox runs.
	g.Env = append(os.Environ(), "HIVE_GATE_DEPTH=1", sandboxedEnv+"=1")
	g.Stdout, g.Stderr = log, log
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	defer g.Process.Kill()
	var pids []int
	for end := time.Now().Add(90 * time.Second); len(pids) == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			raw, _ := os.ReadFile(filepath.Join(dir, "gate.log"))
			t.Fatalf("the gate never started its child's daemon:\n%s", raw)
		}
		pids = daemonsUnder(out)
	}
	g.Process.Signal(syscall.SIGKILL)
	g.Wait()
	time.Sleep(300 * time.Millisecond)
	if len(daemonsUnder(out)) == 0 {
		t.Skip("the daemon ended with its gate here: nothing left to reap")
	}
	if n := reapOrphans(); n == 0 {
		t.Errorf("the next gate ended none of what the killed one left")
	}
	time.Sleep(200 * time.Millisecond)
	if left := daemonsUnder(out); len(left) > 0 {
		t.Errorf("daemons %v outlived the gate that started them", left)
	}
	Reap(out)
	if _, err := os.Stat(filepath.Join(out, "candidate", "gocache")); err == nil {
		t.Error("the run's caches were not removed")
	}
	for _, p := range pids {
		if syscall.Kill(p, 0) == nil {
			if z, _ := os.ReadFile("/proc/" + strconv.Itoa(p) + "/stat"); !bytes.Contains(z, []byte(") Z")) {
				t.Errorf("daemon %d still runs", p)
			}
		}
	}
}

// Reap ends a process of the run, but not a shell or an editor a person has
// open in --out, and keeps the children's logs while it removes their caches.
func TestReapKeepsWhatIsNotTheRuns(t *testing.T) {
	out := t.TempDir()
	cand := filepath.Join(out, "candidate")
	for _, d := range []string{"gocache", filepath.Join("hive", ".hive")} {
		os.MkdirAll(filepath.Join(cand, d), 0o755)
	}
	os.WriteFile(filepath.Join(cand, "hive", ".hive", "daemon.log"), []byte("log"), 0o644)
	start := func(dir string, env ...string) *exec.Cmd {
		cmd := exec.Command("sleep", "60")
		cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd
	}
	person := start(out)
	inRoot := start(filepath.Join(cand, "hive"))
	tagged := start(t.TempDir(), rootEnv+"="+cand)
	if !Untidy(out) {
		t.Error("a run with its caches left reads as tidy")
	}
	if n := Reap(out); n != 2 {
		t.Errorf("Reap ended %d processes, want 2", n)
	}
	for _, c := range []*exec.Cmd{inRoot, tagged} {
		if err := c.Wait(); err == nil {
			t.Errorf("process %d of the run wasn't killed", c.Process.Pid)
		}
	}
	if syscall.Kill(person.Process.Pid, 0) != nil {
		t.Error("Reap killed a process that only had --out open")
	}
	if _, err := os.Stat(filepath.Join(cand, "gocache")); err == nil {
		t.Error("the child's cache was kept")
	}
	if _, err := os.Stat(filepath.Join(cand, "hive", ".hive", "daemon.log")); err != nil {
		t.Errorf("the child's daemon.log went: %v", err)
	}
	if Untidy(out) {
		t.Error("a reaped run still reads as untidy")
	}
}

// Pdeathsig fires when the starting OS thread ends: no gate code locks one.
func TestGateLocksNoThread(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		raw, _ := os.ReadFile(name)
		if !strings.HasSuffix(name, "_test.go") && bytes.Contains(raw, []byte("runtime.LockOSThread()")) {
			t.Errorf("%s locks an OS thread: a child it starts there dies with the thread (dieWithGate)", name)
		}
	}
}
