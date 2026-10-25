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

// stopWait is how long a daemon waits for the lock while its holder doesn't
// answer: a daemon shutting down holds it until it has exited, which a
// Windows runner has taken seconds to do.
var stopWait = 30 * time.Second

// lockWorkspace takes the workspace's daemon lock, trying for up to wait
// (an update's restart waits out the daemon it replaces, a start waits out
// one stopping) or once. With unlessUp it stops waiting as soon as a daemon
// there answers; a hand-off waits even for one that still answers.
func lockWorkspace(ws core.Workspace, wait time.Duration, unlessUp bool) (*workspaceLock, error) {
	if err := os.MkdirAll(ws.Private(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockFile(ws), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		// As when another user's launcher made the file first: say whose it
		// is, since a unit retrying this every 10s says nothing else.
		if owner := fileOwner(lockFile(ws)); owner != "" {
			return nil, fmt.Errorf("%w (the file belongs to %s; this daemon runs as %s, so it can't take the workspace until that file goes)", err, owner, fileOwnerOf(os.Getuid()))
		}
		return nil, err
	}
	readHolder := func() (h holder) {
		raw, _ := core.SafeRead(lockFile(ws), 4096)
		json.Unmarshal(raw, &h)
		return h
	}
	said := false
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		ok, err := proc.TryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			break
		}
		if time.Since(start) > wait || unlessUp && running(ws) {
			f.Close()
			return nil, &AlreadyRunning{Root: ws.Root, By: readHolder()}
		}
		if !said && time.Since(start) > 2*time.Second {
			// A pause at boot, or behind a hung daemon, reads from daemon.log.
			said = true
			h := readHolder()
			fmt.Printf("hive: daemon pid %d (started by %s) holds %s but doesn't answer; waiting up to %s for it to come up or let go\n", h.PID, or(h.By, "something unknown"), ws.Root, wait.Round(time.Second))
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
	said := false
	for start := time.Now(); lockHeld(ws) && time.Since(start) < bootWait; time.Sleep(200 * time.Millisecond) {
		if c, ok := Running(ws); ok {
			return c, true
		}
		if !said && time.Since(start) > 3*time.Second {
			// A hung holder stalls the command a minute: say why.
			said = true
			fmt.Fprintf(os.Stderr, "hive: waiting up to %s for the daemon starting on %s (it holds the workspace lock but doesn't answer yet)\n", bootWait.Round(time.Second), ws.Root)
		}
	}
	return Running(ws)
}

func running(ws core.Workspace) bool { _, ok := Running(ws); return ok }
