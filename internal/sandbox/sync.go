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
func runSync(argv, env []string, dir, stdin, stdout string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = env, dir
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
	end := group(cmd.Process.Pid)
	err = cmd.Wait()
	timedOut := ctx.Err() == context.DeadlineExceeded
	end(timedOut) // the timeout killed argv alone; what it started goes too
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

func openInput(path string) (*os.File, error) {
	if path == "" {
		return os.Open(os.DevNull)
	}
	return os.Open(path)
}

// Run runs s to completion directly on the host.
func (Bare) Run(s Spec, timeout time.Duration) (int, error) {
	env := withPathPrefix(append(os.Environ(), s.Env...), pathDirs(s))
	return runSync(niced(s.Argv, s.Nice), env, s.Cwd, s.Stdin, s.Stdout, timeout)
}
