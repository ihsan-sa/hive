package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Git runs host git in dir. Host git only ever runs in repositories the host
// created and controls (project clones, fresh gate clones), never in an
// agent's checkout, whose git metadata could point anywhere. Auto maintenance
// (gc, repack, pack-refs) runs before the command returns rather than in a
// detached process, so nothing still writes in a repo once its git call ends
// and the repo can be removed or reused at once.
func Git(dir string, args ...string) (string, error) {
	return GitT(2*time.Minute, dir, args...)
}

// GitT is Git with a timeout.
func GitT(timeout time.Duration, dir string, args ...string) (string, error) {
	return GitEnvT(timeout, dir, nil, args...)
}

// A git lock older than this belongs to no live git call (the longest the
// Hive gives one is ten minutes): it was left by a git that died, as when the
// machine ran out of memory, and is removed so one dead process doesn't stall
// every later call in the repo (#440).
const staleGitLock = 20 * time.Minute

var gitLockExists = regexp.MustCompile(`Unable to create '?([^'\n]+\.lock)'?: File exists`)

// GitEnvT is GitT with extra environment, such as the GH_TOKEN for a repo's owner.
// A call that fails on a stale lock in dir's own .git (see staleGitLock) has
// the lock removed and runs once more. With dir "" (a clone, an ls-remote)
// there is no repo of the Hive's to recover, so a lock is left alone.
func GitEnvT(timeout time.Duration, dir string, env []string, args ...string) (string, error) {
	key := ""
	if dir != "" && len(env) == 0 && pureGit(args) {
		key = dir + "\x00" + strings.Join(args, "\x00")
		if out, err, ok := gitMemo.get(key); ok {
			return out, err
		}
	}
	out, err := gitOnce(timeout, dir, env, args...)
	for _, wait := range gitLockWaits {
		if err == nil || !GitLockBusy(err.Error()) {
			break
		}
		if !clearStaleLock(dir, err.Error()) {
			if _, _, ok := ownLock(dir, err.Error()); !ok {
				break // a lock outside dir's own .git isn't the Hive's to wait on
			}
			time.Sleep(wait)
		}
		out, err = gitOnce(timeout, dir, env, args...)
	}
	var exit *exec.ExitError
	if key != "" && (err == nil || errors.As(err, &exit) && exit.ExitCode() == 1) {
		gitMemo.put(key, out, err)
	}
	return out, err
}

// pureGit says whether a git call's answer is fixed by the commits it names:
// merge-base or diff between full commit ids, or show of one (or of
// id:path), with no ref, path or flag value that could mean something else
// later. Such an answer is the same in any clone that has the commits, so a
// checkout removed and cloned again at the same path doesn't change it. Such
// calls repeat a lot (one merge pass asks the same ancestry and diff of the
// same two commits many times), and on a loaded machine every git process
// costs; the answer, or its exit 1 (not an ancestor, differs), is kept for
// gitMemoFor. merge-tree isn't one: the tree it names was written into one
// repo, and a fresh clone at the same path doesn't have it.
func pureGit(args []string) bool {
	if len(args) < 2 || !(args[0] == "merge-base" || args[0] == "diff" || args[0] == "show") {
		return false
	}
	n := 0
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		id, path, hasPath := strings.Cut(a, ":")
		if !fullID.MatchString(id) || hasPath && (args[0] != "show" || path == "") {
			return false
		}
		n++
	}
	if args[0] == "show" {
		return n > 0
	}
	return n >= 2 // diff of one commit is against the working tree
}

var fullID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// gitMemoFor is how long a pure call's answer is kept: a merge pass's worth,
// so the memo stays small.
const gitMemoFor = 10 * time.Minute

// gitMemoMax caps the memo's entries and gitMemoBytes the output one may hold.
const gitMemoMax, gitMemoBytes = 4096, 1 << 20

type gitAnswer struct {
	out string
	err error
	at  time.Time
}

type gitMemoT struct {
	mu sync.Mutex
	m  map[string]gitAnswer
}

var gitMemo = &gitMemoT{m: map[string]gitAnswer{}}

func (g *gitMemoT) get(key string) (string, error, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.m[key]
	if !ok || time.Since(a.at) > gitMemoFor {
		return "", nil, false
	}
	return a.out, a.err, true
}

func (g *gitMemoT) put(key, out string, err error) {
	if len(out) > gitMemoBytes {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.m) >= gitMemoMax {
		for k, a := range g.m {
			if time.Since(a.at) > gitMemoFor {
				delete(g.m, k)
			}
		}
		if len(g.m) >= gitMemoMax {
			g.m = map[string]gitAnswer{}
		}
	}
	g.m[key] = gitAnswer{out, err, time.Now()}
}

// gitLockWaits are the pauses before a call that found a live git lock in
// its repo's own .git (another git step on the same clone, as a fetch beside
// a checkout) runs again: the other step is usually done within seconds, and
// a busy lock is no fault of the step that met it (#2151).
var gitLockWaits = []time.Duration{time.Second, 3 * time.Second, 8 * time.Second}

// GitLockBusy says whether an error's text is git refusing to start because
// another git call holds a lock (index.lock, a ref's lock) in the repo: no
// verdict on the step, which waits and goes on.
func GitLockBusy(text string) bool { return gitLockExists.MatchString(text) }

// ownLock is the lock a failed git call names, when it is a plain file
// inside dir's own .git (no symlink on the way).
func ownLock(dir, msg string) (string, os.FileInfo, bool) {
	m := gitLockExists.FindStringSubmatch(msg)
	if m == nil || dir == "" {
		return "", nil, false
	}
	gitDir, err := filepath.Abs(filepath.Join(dir, ".git"))
	if err != nil {
		return "", nil, false
	}
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return "", nil, false
	}
	lock := m[1]
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(dir, lock)
	}
	lock = filepath.Clean(lock)
	if rel, err := filepath.Rel(gitDir, lock); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", nil, false
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(lock)); err != nil {
		return "", nil, false
	} else if realGit, err := filepath.EvalSymlinks(gitDir); err != nil || !strings.HasPrefix(real+string(filepath.Separator), realGit+string(filepath.Separator)) {
		return "", nil, false
	}
	fi, err := os.Lstat(lock)
	if err != nil || !fi.Mode().IsRegular() {
		return "", nil, false
	}
	return lock, fi, true
}

// clearStaleLock removes the lock a failed git call names when it is a plain
// file inside dir's own .git and older than staleGitLock, and says whether it
// did. Anything else (a lock elsewhere, a symlink, a fresh lock) is left alone.
func clearStaleLock(dir, msg string) bool {
	lock, fi, ok := ownLock(dir, msg)
	if !ok || time.Since(fi.ModTime()) < staleGitLock {
		return false
	}
	if os.Remove(lock) != nil {
		return false
	}
	fmt.Fprintf(os.Stderr, "hive: removed a stale git lock %s (%s old)\n", lock, time.Since(fi.ModTime()).Round(time.Minute))
	return true
}

func gitOnce(timeout time.Duration, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "transfer.fsckObjects=true", "-c", "core.autocrlf=false", "-c", "user.name=Hive", "-c", "user.email=hive@localhost",
		"-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "LC_ALL=C"), env...)
	if runtime.GOOS != "windows" {
		// A git ended by its timeout gets SIGTERM first, on which it removes
		// its own lock files; SIGKILL would leave index.lock behind and fail
		// every later step in the clone until the lock goes stale (#2151).
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s, %w", timeout, err)
	}
	if err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// RunCmd runs a host program with a timeout and returns trimmed stdout.
func RunCmd(timeout time.Duration, dir string, name string, args ...string) (string, error) {
	return RunCmdEnv(timeout, dir, nil, name, args...)
}

// RunCmdEnv is RunCmd with extra environment.
func RunCmdEnv(timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	out, stderr, err := runCmd(timeout, dir, env, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, stderr)
	}
	return out, nil
}

// runCmd runs a host program and hands back its trimmed stdout and stderr
// apart from how it ended.
func runCmd(timeout time.Duration, dir string, env []string, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1"), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s, %w", timeout, err)
	}
	return strings.TrimSpace(out.String()), strings.TrimSpace(errb.String()), err
}

// TimedOut reports whether an error's text is a host program killed by its
// own timeout (the "timed out after" runCmd and gitOnce put first): the
// machine was slow, not the step wrong. A kill with no timeout behind it (the
// kernel short of memory inside a step's own work) isn't one, nor is a test's
// own "test timed out after 2s" panic.
func TimedOut(text string) bool {
	return timedOut.MatchString(text)
}

// timedOut is tied to the two producers' wording, runCmd's and gitOnce's
// "timed out after 2m0s, signal: killed"; others (the gate's child, an open
// agent's "[timed out after Ns]") say it their own way and aren't a call.
var timedOut = regexp.MustCompile(`timed out after [0-9][0-9.µnmsh]*, `)
