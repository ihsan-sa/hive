package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// holder is who holds a workspace's daemon lock, written into the lock file
// once it is taken, so a second daemon can name the one that runs.
type holder struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	By      string    `json:"by"` // what started it: a systemd unit, or the parent process
}

// lockFile is the daemon's exclusive lock on its workspace: one daemon runs
// per workspace, whatever started it (its unit, autostart, a person, an
// update's restart). The kernel drops it when the process ends, a crash or
// an out-of-memory kill included.
func lockFile(ws core.Workspace) string { return filepath.Join(ws.Private(), "daemon.lock") }

// workspaceLock is a held lock; Release is safe to call more than once.
type workspaceLock struct{ f *os.File }

func (l *workspaceLock) Release() {
	if l != nil && l.f != nil {
		proc.Unlock(l.f)
		l.f.Close()
		l.f = nil
	}
}

// AlreadyRunning is the error of a daemon whose workspace another holds.
type AlreadyRunning struct {
	Root string
	By   holder
}

func (e *AlreadyRunning) Error() string {
	s := "the Hive is already running on " + e.Root
	if e.By.PID > 0 {
		s += fmt.Sprintf(": daemon pid %d", e.By.PID)
		if !e.By.Started.IsZero() {
			s += ", up since " + e.By.Started.Local().Format(time.DateTime)
		}
		if e.By.By != "" {
			s += ", started by " + e.By.By
		}
	}
	return s + "; this one exits (`" + core.Cmd + " stop` ends the running one)"
}

// lockWorkspace takes the workspace's daemon lock, trying for up to wait
// (an update's restart waits out the daemon it replaces) or once.
func lockWorkspace(ws core.Workspace, wait time.Duration) (*workspaceLock, error) {
	if err := os.MkdirAll(ws.Private(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockFile(ws), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for end := time.Now().Add(wait); ; time.Sleep(100 * time.Millisecond) {
		ok, err := proc.TryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			break
		}
		if time.Now().After(end) {
			var h holder
			raw, _ := core.SafeRead(lockFile(ws), 4096)
			json.Unmarshal(raw, &h)
			f.Close()
			return nil, &AlreadyRunning{Root: ws.Root, By: h}
		}
	}
	raw, _ := json.Marshal(holder{PID: os.Getpid(), Started: time.Now().UTC(), By: startedBy()})
	f.Truncate(0)
	f.WriteAt(raw, 0)
	return &workspaceLock{f: f}, nil
}

// startedBy names what started this daemon: its parent, and the systemd
// unit it runs in.
func startedBy() string {
	pp := os.Getppid()
	by := fmt.Sprintf("process %d", pp)
	if comm, err := os.ReadFile("/proc/" + strconv.Itoa(pp) + "/comm"); err == nil {
		by += " (" + strings.TrimSpace(string(comm)) + ")"
	}
	if name, _ := setup.Unit(); name != "" {
		by += " in systemd unit " + name
	}
	return by
}

// lockHeld reports whether a daemon holds the workspace's lock: one that is
// starting up holds it before it writes daemon.json.
func lockHeld(ws core.Workspace) bool {
	f, err := os.OpenFile(lockFile(ws), os.O_RDWR, 0)
	if err != nil {
		return false // never locked, or not ours to read: starting one says which
	}
	defer f.Close()
	ok, err := proc.TryLock(f)
	if ok {
		proc.Unlock(f)
	}
	return !ok && err == nil
}

// bootWait is how long a command waits for a daemon that holds the lock to
// come up, rather than start another (a slow start has taken over 30s).
var bootWait = time.Minute

// booting waits for a daemon that is starting on the workspace, so a command
// run as the unit's daemon starts doesn't start a second one. It gives up
// when the lock is let go with no daemon up, as when that one failed.
func booting(ws core.Workspace) (*api.Client, bool) {
	for end := time.Now().Add(bootWait); lockHeld(ws) && time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if c, ok := Running(ws); ok {
			return c, true
		}
	}
	return Running(ws)
}
