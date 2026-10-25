package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A check that dies midway leaves nothing of it: not the job it started in
// the background, not its home or the temp files it wrote, not its checkout.
func TestCheckThatDiesLeavesNothing(t *testing.T) {
	c, _, p, main := setupProject(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	// Bare checks run with the host's environment, so the temp folder is
	// asked of the check's home by its path in the cache.
	script := `sleep 300 & echo $! > ` + pidFile + `; h=$(ls -d ` + c.WS.CheckHomes() + `/home-* | head -1); ` +
		`mkdir -p "$h/tmp/go-build1" "$h/.cache/go-build" && head -c 4096 /dev/zero > "$h/tmp/go-build1/x.test"; kill -9 $$`
	ch := c.Effects.RunCheck(p, main, "test", script)
	if ch.OK {
		t.Fatalf("a check killed midway passed: %+v", ch)
	}
	if strings.Contains(ch.Output, "No such file") {
		t.Fatalf("the check didn't get as far as its home: %s", ch.Output)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the check never started its job: %v\n%s", err, ch.Output)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	for i := 0; ; i++ {
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil || syscall.Kill(pid, 0) != nil || strings.Contains(string(stat), ") Z ") {
			break
		}
		if i == 100 {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the check's background job (pid %d) outlived it", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ms, _ := filepath.Glob(filepath.Join(c.WS.CheckHomes(), "home-*")); len(ms) > 0 {
		t.Errorf("the check's home outlived it: %v", ms)
	}
	if ds, _ := os.ReadDir(filepath.Join(c.WS.Gates(), p.Name)); len(ds) > 0 {
		t.Errorf("the check's checkout outlived it: %v", ds)
	}
}

// A check's temp folder is in its home, on disk, not the sandbox's /tmp in memory.
func TestCheckTempIsInItsHome(t *testing.T) {
	env := checkTmp([]string{"HOME=/hive/home", "TMPDIR=/tmp"})
	if env[1] != "TMPDIR=/hive/home/"+homeTmp {
		t.Fatalf("env %v", env)
	}
}
