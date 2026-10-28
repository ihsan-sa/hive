package agentcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// `hive agent heavy -- cmd` waits while the Hive says wait, runs cmd once
// it says go, gives the lease back as cmd ends, and returns cmd's exit code.
func TestHeavyWaitsRunsAndGivesTheLeaseBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs sh")
	}
	box := t.TempDir()
	for _, d := range []string{"outbox", "inbox"} {
		os.Mkdir(filepath.Join(box, d), 0o755)
	}
	t.Setenv("HIVE_BOX", box)
	defer func(p, r time.Duration) { heavyPoll, heavyRenew = p, r }(heavyPoll, heavyRenew)
	heavyPoll, heavyRenew = 10*time.Millisecond, time.Hour
	var mu sync.Mutex
	var asked []map[string]any
	stop := make(chan struct{})
	defer close(stop)
	go func() { // the daemon: wait twice, then go
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			ms, _ := filepath.Glob(filepath.Join(box, "outbox", "*.json"))
			for _, p := range ms {
				raw, _ := os.ReadFile(p)
				os.Remove(p)
				var req struct {
					ID   string         `json:"id"`
					Args map[string]any `json:"args"`
				}
				json.Unmarshal(raw, &req)
				mu.Lock()
				asked = append(asked, req.Args)
				res := map[string]any{"wait": "busy", "running": []string{"w1: pytest"}}
				if len(asked) > 2 {
					res = map[string]any{"go": true}
				}
				mu.Unlock()
				out, _ := json.Marshal(map[string]any{"ok": true, "result": res})
				os.WriteFile(filepath.Join(box, "inbox", req.ID+".json"), out, 0o644)
			}
		}
	}()
	mark := filepath.Join(t.TempDir(), "ran")
	if code := Heavy([]string{"--what", "the suite", "--", "sh", "-c", "touch " + mark + "; exit 3"}); code != 3 {
		t.Fatalf("exit %d, want the command's 3", code)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("the command never ran")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 4 || asked[0]["what"] != "the suite" || asked[3]["done"] != true || asked[0]["id"] != asked[3]["id"] {
		t.Fatalf("asked %v; want two waits, a go and the lease given back", asked)
	}
}

// Outside a Hive run the command runs at once, so a module's wrapper works
// for the person too.
func TestHeavyOutsideARunRunsAtOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs sh")
	}
	t.Setenv("HIVE_BOX", "")
	if code := Heavy([]string{"--", "sh", "-c", "exit 0"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if code := Heavy([]string{"--what", "x"}); code != 2 || !strings.Contains(Usage, "heavy") {
		t.Fatalf("no command: exit %d, want 2", code)
	}
}
