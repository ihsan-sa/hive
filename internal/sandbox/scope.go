package sandbox

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// checksSlice is the systemd user slice every check and recursive gate run
// goes under, in a scope of its own (Scoped).
var checksSlice = "app-hivechecks.slice"

var scope struct {
	sync.Mutex
	tried  time.Time
	prefix []string // systemd-run and its flags, nil where it can't run
}

// scopeRetry is how long a probe that found no user manager stands before
// the next scoped command tries again: one that starts after the daemon is
// used without a restart.
const scopeRetry = 10 * time.Minute

// Scoped prefixes argv so it runs in a transient systemd user scope under
// checksSlice, with a memory ceiling well under the Hive's share and CPU and
// IO weights under the agents': a check or gate run that grows without
// bound is throttled, then ended, by the kernel, alone, while the daemon,
// its agents and the person's login keep their memory. systemd-run execs
// argv in the process it started, so the pid, process group and Pdeathsig
// the caller set hold as before. argv[0] is resolved here, in the daemon's
// own environment, as Niced does. Where no systemd user manager answers
// (macOS, WSL or a container without one, a system unit's daemon), argv
// runs as it is, and the probe runs again after scopeRetry.
func Scoped(argv []string) []string {
	scope.Lock()
	if scope.prefix == nil && time.Since(scope.tried) > scopeRetry {
		scope.prefix, scope.tried = scopePrefix(), time.Now()
	}
	prefix := scope.prefix
	scope.Unlock()
	if prefix == nil || len(argv) == 0 {
		return argv
	}
	prog, err := exec.LookPath(argv[0])
	if err != nil {
		return argv
	}
	return append(append(append([]string{}, prefix...), prog), argv[1:]...)
}

// scopePrefix finds systemd-run and tries it once, with the same flags a
// check gets, so a user manager that is missing or refuses (a daemon in a
// system unit can't be moved into a user scope) costs no check its run.
// The slice gets the same ceiling, so checks at once share one.
func scopePrefix() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	run, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil
	}
	props := ceilingProps(ceiling())
	if ctl, err := exec.LookPath("systemctl"); err == nil {
		quiet(append([]string{ctl, "--user", "set-property", "--runtime", checksSlice}, props...))
	}
	prefix := []string{run, "--user", "--scope", "--quiet", "--collect", "--slice=" + checksSlice}
	for _, p := range props {
		prefix = append(prefix, "-p", p)
	}
	prefix = append(prefix, "--")
	if !quiet(append(append([]string{}, prefix...), "true")) {
		return nil
	}
	return prefix
}

func quiet(argv []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run() == nil
}

// ceilingProps is a check's share of a machine whose memory the Hive may
// use up to limit bytes: throttled past half, ended past three quarters
// with an eighth in swap (a check swapping without end slows every disk
// user, not itself alone), and a fifth of the default CPU and IO weight.
func ceilingProps(limit int64) []string {
	return []string{
		"MemoryHigh=" + strconv.FormatInt(limit/2, 10),
		"MemoryMax=" + strconv.FormatInt(limit/4*3, 10),
		"MemorySwapMax=" + strconv.FormatInt(limit/8, 10),
		"CPUWeight=20",
		"IOWeight=20",
	}
}

var ceiling = memCeiling

// memCeiling is the least of the machine's memory and every memory.max and
// memory.high set on the daemon's own cgroup and those above it: the Hive's
// share, wherever the person or their box put it.
func memCeiling() int64 {
	limit := memTotal()
	raw, _ := os.ReadFile("/proc/self/cgroup")
	for _, line := range strings.Split(string(raw), "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		for p := filepath.Clean(path); ; p = filepath.Dir(p) {
			for _, f := range []string{"memory.max", "memory.high"} {
				b, _ := os.ReadFile(filepath.Join("/sys/fs/cgroup", p, f))
				if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && n > 0 && n < limit {
					limit = n
				}
			}
			if p == "/" || p == "." {
				break
			}
		}
	}
	return limit
}

func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
				if kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 8 << 30
}
