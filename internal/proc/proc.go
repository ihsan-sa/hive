// Package proc starts detached processes and checks on them across restarts.
// A process is identified by pid plus start time, so a reused pid after a
// reboot is never mistaken for a live run, and an exited one nobody reaped
// (a zombie) counts as gone.
package proc

import (
	"io"
	"os"
	"os/exec"
	"time"
)

// Handle identifies a started process.
type Handle struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// StartDetached starts argv in its own session/process group with stdin and
// stdout redirected to files, so it outlives the caller.
func StartDetached(argv, env []string, dir, stdin, stdout string) (Handle, error) {
	in, err := openIn(stdin)
	if err != nil {
		return Handle{}, err
	}
	defer in.Close()
	var out, errf *os.File
	if stdout != "" {
		if out, err = OpenOutput(stdout); err != nil {
			return Handle{}, err
		}
		defer out.Close()
		if errf, err = OpenOutput(stdout + ".err"); err != nil {
			return Handle{}, err
		}
		defer errf.Close()
	}
	// Each attempt needs a fresh Cmd: exec refuses a second Start.
	var cmd *exec.Cmd
	for _, flags := range detachAttempts {
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Env, cmd.Dir, cmd.Stdin = env, dir, in
		if out != nil {
			cmd.Stdout, cmd.Stderr = out, errf
		}
		detach(cmd, flags)
		if err = cmd.Start(); err == nil {
			break
		}
	}
	if err != nil {
		return Handle{}, err
	}
	h := Handle{PID: cmd.Process.Pid, Started: StartTime(cmd.Process.Pid)}
	go cmd.Wait() // reap; the record of completion is the output file, not this
	return h, nil
}

func openIn(path string) (*os.File, error) {
	if path == "" {
		return os.Open(os.DevNull)
	}
	return os.Open(path)
}

// Alive reports whether h still runs (same pid and same start time).
func Alive(h Handle) bool { return h.PID > 0 && running(h.PID) && sameStart(h) }

// sameStart says h.PID is still the process h started; a zombie keeps its
// start time.
func sameStart(h Handle) bool {
	st := StartTime(h.PID)
	if st.IsZero() || h.Started.IsZero() {
		return true
	}
	d := st.Sub(h.Started)
	return d < 3*time.Second && d > -3*time.Second
}

// OpenOutput opens path for a child process to write, positioned at the end.
// Not O_APPEND: on Windows that yields an append-only handle, which some
// programs (MSYS sh, from Git for Windows) fail on without a word.
func OpenOutput(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
