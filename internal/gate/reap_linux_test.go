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

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
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
		if under(proc.EnvValue(bytes.Split(env, []byte{0}), rootEnv), out) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// daemonsLeft waits up to wait for the gate run's daemons to be gone and
// returns those still there. A killed process needs the CPU to exit, and on
// a throttled machine, or one blocked in I/O, that takes more than a moment.
func daemonsLeft(out string, wait time.Duration) []int {
	left := daemonsUnder(out)
	for end := time.Now().Add(wait); len(left) > 0 && time.Now().Before(end); left = daemonsUnder(out) {
		time.Sleep(100 * time.Millisecond)
	}
	return left
}

// running reports whether pid still runs: neither gone nor a zombie.
func running(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	z, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return !bytes.Contains(z, []byte(") Z"))
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
		// Not a skip: Reap below still runs. TestDeadGatesOrphanIsReaped
		// covers the next gate's sweep whatever happened here.
		t.Log("the daemon ended with its gate here: the next gate had nothing to end")
	} else {
		if n := reapOrphans(); n == 0 {
			t.Errorf("the next gate ended none of what the killed one left")
		}
		if left := daemonsLeft(out, 15*time.Second); len(left) > 0 {
			t.Errorf("daemons %v outlived the gate that started them", left)
		}
	}
	Reap(out)
	if _, err := os.Stat(filepath.Join(out, "candidate", "gocache")); err == nil {
		t.Error("the run's caches were not removed")
	}
	for _, p := range pids {
		for end := time.Now().Add(15 * time.Second); running(p) && time.Now().Before(end); {
			time.Sleep(100 * time.Millisecond)
		}
		if running(p) {
			t.Errorf("daemon %d still runs", p)
		}
	}
}

// A process a dead gate's child detached (setsid, out of every group the
// gate could kill) is ended by the next gate's sweep, always: unlike
// TestKilledGateLeavesNoDaemon, nothing here can end it first. Another
// test process's sweep (a gate test or a second run of this one) may end the
// orphan before this one does, so it is the orphan's end that is checked,
// not reapOrphans' count, and within seconds rather than its 60s sleep.
func TestDeadGatesOrphanIsReaped(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	ns, _ := os.Readlink("/proc/self/ns/pid")
	dead := strconv.Itoa(gone.Process.Pid) + ".1@" + ns
	orphan := exec.Command("sleep", "60")
	orphan.Env = append(os.Environ(), ownerEnv+"="+dead)
	orphan.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { orphan.Process.Kill() })
	live := exec.Command("sleep", "60")
	live.Env = append(os.Environ(), ownerEnv+"="+owner)
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.Process.Kill(); live.Wait() })
	n := reapOrphans()
	waited := make(chan error, 1)
	go func() { waited <- orphan.Wait() }()
	select {
	case err := <-waited:
		if err == nil {
			t.Error("the dead gate's orphan wasn't killed")
		}
	case <-time.After(10 * time.Second):
		t.Errorf("the dead gate's orphan wasn't killed (reapOrphans ended %d)", n)
	}
	if syscall.Kill(live.Process.Pid, 0) != nil {
		t.Error("reapOrphans killed a process of a gate that still runs")
	}
}

// Reap leaves the host alone when a candidate swaps a folder of the run
// for a symlink: nothing outside out is chmodded or removed.
func TestReapStaysInsideOut(t *testing.T) {
	out, host := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(host, "keep"), 0o500)
	cand := filepath.Join(out, "candidate")
	os.MkdirAll(cand, 0o755)
	for _, b := range []string{"home", "gocache"} {
		if err := os.Symlink(host, filepath.Join(cand, b)); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink(host, filepath.Join(out, "src-candidate"))
	link := filepath.Join(t.TempDir(), "out")
	os.Symlink(out, link)
	Reap(link)
	if _, err := os.Lstat(filepath.Join(cand, "home")); err != nil {
		t.Error("Reap followed a symlink given as out")
	}
	Reap(out)
	fi, err := os.Stat(filepath.Join(host, "keep"))
	if err != nil {
		t.Fatalf("Reap removed a host folder through a symlink: %v", err)
	}
	if fi.Mode().Perm() != 0o500 {
		t.Errorf("Reap chmodded a host folder through a symlink to %v", fi.Mode().Perm())
	}
	os.Chmod(filepath.Join(host, "keep"), 0o755)
}

// Reap ends a process of the run, but not a shell or an editor a person has
// open in --out, and keeps the children's logs while it removes their caches.
func TestReapKeepsWhatIsNotTheRuns(t *testing.T) {
	out := t.TempDir()
	cand := filepath.Join(out, "candidate")
	for _, d := range []string{"gocache", filepath.Join("hive", ".hive"), filepath.Join("hive", "projects", "lab")} {
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
	for _, b := range []string{"gocache", filepath.Join("hive", "projects")} {
		if _, err := os.Stat(filepath.Join(cand, b)); err == nil {
			t.Errorf("the child's %s was kept", b)
		}
	}
	if _, err := os.Stat(filepath.Join(cand, "hive", ".hive", "daemon.log")); err != nil {
		t.Errorf("the child's daemon.log went: %v", err)
	}
	if Untidy(out) {
		t.Error("a reaped run still reads as untidy")
	}
}

// ReapLeft, the hourly sweep's, ends a past run's tagged process but not
// one that only has its working directory in the run's candidate folder: a
// person's shell there, long after the gate ended.
func TestReapLeftSparesAShellInAnOldRun(t *testing.T) {
	out := t.TempDir()
	cand := filepath.Join(out, "candidate")
	os.MkdirAll(filepath.Join(cand, "gocache"), 0o755)
	start := func(dir string, env ...string) *exec.Cmd {
		cmd := exec.Command("sleep", "60")
		cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd
	}
	shell := start(cand)
	tagged := start(t.TempDir(), rootEnv+"="+cand)
	if n := ReapLeft(out); n != 1 {
		t.Errorf("ReapLeft ended %d processes, want 1", n)
	}
	if err := tagged.Wait(); err == nil {
		t.Error("the run's tagged process wasn't killed")
	}
	if syscall.Kill(shell.Process.Pid, 0) != nil {
		t.Error("ReapLeft killed a process that only had the old run's folder open")
	}
	if _, err := os.Stat(filepath.Join(cand, "gocache")); err == nil {
		t.Error("ReapLeft kept the run's caches")
	}
}
