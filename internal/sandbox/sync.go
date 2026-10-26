package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// ErrTimeout means a synchronous sandboxed command ran past its time.
var ErrTimeout = errors.New("timed out")

// runSync runs argv and returns its exit code. The exit code comes from the
// host-side process, never from anything the sandboxed command printed.
// Whatever argv started and left running ends when it returns, however it
// returns, and argv itself ends if the daemon dies first.
func runSync(argv, env []string, dir, stdin, stdout string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
	ownGroup(cmd)
	dieWithDaemon(cmd)
	in, err := openInput(stdin)
	if err != nil {
		return -1, err
	}
	defer in.Close()
	cmd.Stdin = in
	if stdout != "" {
		f, err := proc.OpenOutput(stdout)
		if err != nil {
			return -1, err
		}
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err = cmd.Start(); err != nil {
		return -1, err
	}
	// The timeout kills argv alone, and a finished argv may have left a job
	// running: what is left of its group ends once argv exits, before Wait
	// reaps argv and frees its group's id where the platform allows (exited).
	end := group(cmd.Process.Pid)
	ended := exited(cmd.Process.Pid)
	if ended {
		end(true)
	}
	err = cmd.Wait()
	if !ended {
		end(true)
	}
	timedOut := ctx.Err() == context.DeadlineExceeded
	if timedOut {
		return -1, ErrTimeout
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// runScoped runs argv under the checks' ceiling when s asks for it
// (Scoped). A scoped run that ends at once with status 1, systemd-run's own
// failure, while the probe now fails too (the user manager went away after
// it answered) never ran argv, so argv runs again as it is.
func runScoped(s Spec, argv, env []string, dir, stdin, stdout string, timeout time.Duration) (int, error) {
	in := argv
	if s.Scope {
		in = Scoped(argv)
	}
	start := time.Now()
	code, err := runSync(in, env, dir, stdin, stdout, timeout)
	if len(in) != len(argv) && err == nil && code == 1 && time.Since(start) < 10*time.Second && !reprobe() {
		return runSync(argv, env, dir, stdin, stdout, timeout)
	}
	return code, err
}

func openInput(path string) (*os.File, error) {
	if path == "" {
		return os.Open(os.DevNull)
	}
	return os.Open(path)
}

// OwnerEnv tags every process a bare synchronous command starts with the
// daemon that started it (proc.OwnerID). Pdeathsig ends the command itself
// when the daemon dies, and its group ends when it returns, but nothing ends
// the rest of its group, or what it detached, after a daemon crash: Sweep
// does, by this tag, once its daemon is gone. A daemon clears it from its own
// environment as it starts (cmd/hive), so a daemon some check started never
// passes a dead daemon's tag on to its agents' turns.
const OwnerEnv = "HIVE_SYNC_OWNER"

var owner = proc.OwnerID()

// Run runs s to completion directly on the host.
func (Bare) Run(s Spec, timeout time.Duration) (int, error) {
	env := withPathPrefix(append(os.Environ(), s.Env...), pathDirs(s))
	if owner != "" {
		env = append(env, OwnerEnv+"="+owner)
	}
	return runScoped(s, Niced(s.Argv, s.Nice), env, s.Cwd, s.Stdin, s.Stdout, timeout)
}
