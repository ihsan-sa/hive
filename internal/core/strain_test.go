package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The budget holds a turn once the memory left can't take it beside the ones
// launching: in the Hive's cgroup when it has a limit, else on the machine,
// and never with the budget off or nothing to read.
func TestMemBudget(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(body), 0o644)
	}
	if why := memBudget(proc, root, DefaultTurnMB, 0); why != "" {
		t.Fatalf("held with nothing to read: %q", why)
	}
	// No cgroup limit: the machine's 1000 MB, 600 MB of it available.
	write(proc+"/self/cgroup", "0::/hive.service\n")
	write(root+"/hive.service/memory.max", "max\n")
	write(proc+"/meminfo", "MemTotal:       1024000 kB\nMemFree:  1 kB\nMemAvailable:    614400 kB\n")
	if why := memBudget(proc, root, 240, 1); why != "" {
		t.Fatalf("two turns of 240 MB fit in 600 MB: %q", why)
	}
	if why := memBudget(proc, root, 240, 2); !strings.HasPrefix(why, MemWait) {
		t.Fatalf("a third didn't wait: %q", why)
	}
	if why := memBudget(proc, root, 0, 99); why != "" {
		t.Fatalf("the budget off held: %q", why)
	}
	// A cgroup limit of 1000 MB, 900 MB used of which 400 MB is cache.
	write(root+"/hive.service/memory.max", "1048576000\n")
	write(root+"/hive.service/memory.current", "943718400\n")
	write(root+"/hive.service/memory.stat", "anon 1\nactive_file 5\ninactive_file 419430400\n")
	if why := memBudget(proc, root, 240, 1); why != "" {
		t.Fatalf("two turns fit in the cgroup's 500 MB left: %q", why)
	}
	if why := memBudget(proc, root, 240, 2); !strings.Contains(why, "500 of 1000 MB in use") {
		t.Fatalf("a third didn't wait on the cgroup: %q", why)
	}
}
