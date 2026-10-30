// Package heavy is `hive heavy -- cmd args`: a heavy command (a build, a test
// suite, a render) that runs one at a time on the machine.
//
// On 10-30 the hive user's units re-read ~110 GB an hour from a disk at ~76%
// IO pressure, because the builds running side by side (an eval's cargo, a
// worker's go test, a check) left too little memory for each other's file
// cache (#2207). The daemon paces its own checks (max_checks, one at a time
// on a busy disk); this paces the rest: turns' own builds, eval units and
// systemd-run steps. It is an exclusive flock on one file in the Hive's tools
// folder, which every turn and check mounts (read-only is enough to flock),
// so it needs no daemon and holds across sandboxes; the kernel drops the lock
// with the process, so a killed command never leaves it held.
package heavy

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// LockName is the lock's file in the tools folder.
const LockName = ".heavy.lock"

// HeldEnv marks a command already under the lock, so a heavy command that
// starts another (a test that builds the binary it tests) doesn't wait on
// itself.
const HeldEnv = "HIVE_HEAVY"

// Patience is how long a command waits by default before it runs anyway: a
// held lock behind an hours-long eval build shouldn't stall a turn for hours.
const Patience = 30 * time.Minute

// Ready makes the lock's file in tools, so a sandbox that mounts the folder
// read-only can still flock it.
func Ready(tools string) error {
	f, err := os.OpenFile(filepath.Join(tools, LockName), os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Path is the lock's file: HIVE_HEAVY_LOCK if set, the tools folder's inside
// a sandbox (inside), else the one in tools on the host.
func Path(inside, tools string) string {
	if v := os.Getenv("HIVE_HEAVY_LOCK"); v != "" {
		return v
	}
	if fi, err := os.Stat(inside); err == nil && fi.IsDir() {
		return filepath.Join(inside, LockName)
	}
	return filepath.Join(tools, LockName)
}

// Main is `hive heavy [--what text] [--patience d] -- cmd args`, with lock
// the lock's file. It returns the command's exit code.
func Main(args []string, lock string) int {
	fs := flag.NewFlagSet("heavy", flag.ContinueOnError)
	what := fs.String("what", "", "what the command is, said while it waits")
	patience := fs.Duration("patience", Patience, "how long to wait for the lock before running anyway (0: no limit)")
	if fs.Parse(args) != nil || fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: hive heavy [--what text] [--patience 30m] -- cmd args\nRuns cmd once no other heavy command runs on this machine.")
		return 2
	}
	argv := fs.Args()
	release := func() {}
	if os.Getenv(HeldEnv) == "" {
		release = Take(lock, cmp(*what, argv[0]), *patience, os.Stderr)
	}
	defer release()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), HeldEnv+"=held")
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "hive heavy:", err)
		return 127
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sig {
			cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	signal.Stop(sig)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exit.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive heavy:", err)
		return 1
	}
	return 0
}

// Take holds the lock at path for what, waiting at most patience (0: no
// limit), and returns its release. What it waited, or why it runs without
// the lock, is said on say.
func Take(path, what string, patience time.Duration, say io.Writer) (release func()) {
	none := func() {}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		if f, err = os.Open(path); err != nil { // a read-only mount
			fmt.Fprintf(say, "hive heavy: running %s without the machine's lock: %v\n", what, err)
			return none
		}
	}
	ok, err := tryLock(f)
	if err != nil {
		f.Close()
		fmt.Fprintf(say, "hive heavy: running %s without the machine's lock: %v\n", what, err)
		return none
	}
	if !ok {
		start := time.Now()
		fmt.Fprintf(say, "hive heavy: %s waits for the heavy command running on this machine to end (one at a time)\n", what)
		for !ok {
			if patience > 0 && time.Since(start) >= patience {
				f.Close()
				fmt.Fprintf(say, "hive heavy: waited %s, running %s anyway\n", patience, what)
				return none
			}
			time.Sleep(pollEvery)
			if ok, err = tryLock(f); err != nil {
				f.Close()
				fmt.Fprintf(say, "hive heavy: running %s without the machine's lock: %v\n", what, err)
				return none
			}
		}
		fmt.Fprintf(say, "hive heavy: %s starts after waiting %s\n", what, time.Since(start).Round(time.Second))
	}
	return func() { f.Close() } // closing the last descriptor drops the flock
}

// pollEvery is how often a waiting command tries the lock again; a test
// shortens it.
var pollEvery = 2 * time.Second

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
