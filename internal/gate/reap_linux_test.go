package gate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if _, err := os.Stat(filepath.Join(out, "control")); err == nil {
		t.Error("the run's folders were not removed")
	}
	for _, p := range pids {
		if syscall.Kill(p, 0) == nil {
			if z, _ := os.ReadFile("/proc/" + strconv.Itoa(p) + "/stat"); !bytes.Contains(z, []byte(") Z")) {
				t.Errorf("daemon %d still runs", p)
			}
		}
	}
}
