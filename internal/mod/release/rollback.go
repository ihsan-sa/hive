package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// An update proves itself twice. Update runs the new binary before handing
// over to it, and puts the old one back at once if it doesn't run. Then the
// new daemon counts its starts in update.json until it has stayed up for
// SettleAfter; on its third start (two failed ones, each restarted by its
// supervisor or the next command) it puts the binary it replaced back and
// leaves the old daemon to start and say so.

// pending is an update whose new daemon hasn't stayed up yet.
type pending struct {
	From, To string
	// Paths are the binaries the update replaced; each <path>.old is what
	// it replaced.
	Paths []string
	// Added are binaries the update put where none was (the Linux one beside
	// a Windows install that had none); a rollback removes them.
	Added  []string `json:",omitempty"`
	Starts int
	// RolledBack says why, once the old binaries are back.
	RolledBack string `json:",omitempty"`
}

// failedStarts is how many starts of a new daemon may fail before the next
// one rolls back.
const failedStarts = 2

// SettleAfter is how long a new daemon stays up before its update counts as
// taken.
var SettleAfter = time.Minute

func pendingFile(ws core.Workspace) string { return filepath.Join(ws.Private(), "update.json") }

func readPending(ws core.Workspace) (pending, bool) {
	var p pending
	raw, err := os.ReadFile(pendingFile(ws))
	return p, err == nil && json.Unmarshal(raw, &p) == nil
}

func writePending(ws core.Workspace, p pending) error {
	raw, _ := json.Marshal(p)
	if err := os.MkdirAll(ws.Private(), 0o700); err != nil {
		return err
	}
	return core.WriteFileAtomic(pendingFile(ws), raw, 0o600)
}

// Boot is a daemon's first step once it is the one that runs: after an update
// to this version, it counts the start, and when too many have failed it puts
// the replaced binaries back and returns the path to start the old daemon
// from. It returns "" to carry on.
func Boot(ws core.Workspace) string {
	p, ok := readPending(ws)
	if !ok || p.To != core.Version || p.RolledBack != "" || len(p.Paths) == 0 {
		return ""
	}
	p.Starts++
	if p.Starts <= failedStarts {
		writePending(ws, p)
		return ""
	}
	if err := rollBack(p.Paths, p.Added); err != nil {
		fmt.Fprintf(os.Stderr, "hive: %s failed to start %d times, and rolling back to %s failed: %v\n", p.To, failedStarts, p.From, err)
		writePending(ws, p)
		return ""
	}
	p.RolledBack = fmt.Sprintf("%s failed to start %d times after the update", p.To, failedStarts)
	writePending(ws, p)
	fmt.Fprintf(os.Stderr, "hive: %s; rolled back to %s\n", p.RolledBack, p.From)
	return p.Paths[0]
}

// Settled clears the update once its new daemon has stayed up.
func Settled(ws core.Workspace) {
	if p, ok := readPending(ws); ok && p.To == core.Version {
		os.Remove(pendingFile(ws))
	}
}

// Report is the old daemon, started again after a rollback, saying so. An
// update file that names another version is left from an update this binary
// never ran, and goes.
func (m *Module) Report() {
	p, ok := readPending(m.C.WS)
	if !ok || p.To == core.Version {
		return
	}
	os.Remove(pendingFile(m.C.WS))
	if p.RolledBack == "" {
		return
	}
	m.C.J.MustAppend("hive.rolledback", map[string]string{"from": p.To, "to": core.Version, "why": p.RolledBack})
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "update", "ref": "restart", "text": "The update to " + p.To + " didn't start: " + p.RolledBack + ". The Hive rolled back to " + core.Version + " and won't install " + p.To + " on its own again; `" + core.Cmd + " update " + p.To + "` tries it."})
}

// rolledBack is a release this Hive rolled back from.
func (m *Module) rolledBack(tag string) bool { return m.rolledBackFrom()[tag] }

// rolledBackFrom are the releases this Hive rolled back from.
func (m *Module) rolledBackFrom() map[string]bool {
	back := map[string]bool{}
	m.C.J.Replay(func(e core.Event) {
		var x struct{ From string }
		if e.Kind == "hive.rolledback" && e.Decode(&x) == nil {
			back[x.From] = true
		}
	})
	return back
}

// rollBack puts each path's .old back in its place, keeping the failed
// binary as .failed (a running Windows executable can be renamed, not
// removed, so a .failed still running moves aside), and removes what the update added.
func rollBack(paths, added []string) error {
	for _, p := range paths {
		if !core.Exists(p + ".old") {
			return errors.New(p + ".old is gone")
		}
	}
	for _, p := range paths {
		if err := makeWay(p + ".failed"); err != nil {
			return err
		}
		if err := os.Rename(p, p+".failed"); err != nil {
			return err
		}
		if err := os.Rename(p+".old", p); err != nil {
			os.Rename(p+".failed", p)
			return err
		}
	}
	for _, p := range added {
		os.Remove(p)
	}
	return nil
}

// tryBinary errs unless exe runs and says it is tag.
var tryBinary = func(exe, tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, core.Clip(strings.TrimSpace(string(out)), 200))
	}
	if !strings.Contains(string(out), tag) {
		return fmt.Errorf("it says it is %q", core.Clip(strings.TrimSpace(string(out)), 200))
	}
	return nil
}
