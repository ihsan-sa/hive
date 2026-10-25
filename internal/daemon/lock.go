package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// holder is who holds a workspace's daemon lock, written into the lock file
// once it is taken, so a second daemon can name the one that runs.
type holder struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	By      string    `json:"by"` // what started it: a systemd unit, or the parent process
	// Stopping: it is shutting down, so a daemon starting now waits for the
	// lock rather than exit.
	Stopping bool `json:"stopping,omitempty"`
}

// lockFile is the daemon's exclusive lock on its workspace: one daemon runs
// per workspace, whatever started it (its unit, autostart, a person, an
// update's restart). The kernel drops it when the process ends, a crash or
// an out-of-memory kill included.
func lockFile(ws core.Workspace) string { return filepath.Join(ws.Private(), "daemon.lock") }

// workspaceLock is a held lock; Release is safe to call more than once.
type workspaceLock struct {
	f *os.File
	h holder
}

// Stopping marks the holder as shutting down.
func (l *workspaceLock) Stopping() {
	if l == nil || l.f == nil {
		return
	}
	l.h.Stopping = true
	raw, _ := json.Marshal(l.h)
	l.f.Truncate(0)
	l.f.WriteAt(raw, 0)
}

func (l *workspaceLock) Release() {
	if l != nil && l.f != nil {
		unlock(l.f)
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
		ok, err := tryLock(f)
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
	h := holder{PID: os.Getpid(), Started: time.Now().UTC(), By: startedBy()}
	raw, _ := json.Marshal(h)
	f.Truncate(0)
	f.WriteAt(raw, 0)
	return &workspaceLock{f: f, h: h}, nil
}

// takeWorkspace is lockWorkspace that also waits out a daemon on its way out
// after a shutdown, still letting go of the workspace (a start right after
// `hive stop`, or a control center's restart).
func takeWorkspace(ws core.Workspace, wait time.Duration) (*workspaceLock, error) {
	l, err := lockWorkspace(ws, wait)
	if held := (*AlreadyRunning)(nil); errors.As(err, &held) && held.By.Stopping {
		l, err = lockWorkspace(ws, 30*time.Second)
	}
	return l, err
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
