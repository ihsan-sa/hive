package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	out, err := gitOnce(timeout, dir, env, args...)
	if err != nil && clearStaleLock(dir, err.Error()) {
		out, err = gitOnce(timeout, dir, env, args...)
	}
	return out, err
}

// clearStaleLock removes the lock a failed git call names when it is a plain
// file inside dir's own .git and older than staleGitLock, and says whether it
// did. Anything else (a lock elsewhere, a symlink, a fresh lock) is left alone.
func clearStaleLock(dir, msg string) bool {
	m := gitLockExists.FindStringSubmatch(msg)
	if m == nil || dir == "" {
		return false
	}
	gitDir, err := filepath.Abs(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return false
	}
	lock := m[1]
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(dir, lock)
	}
	lock = filepath.Clean(lock)
	if rel, err := filepath.Rel(gitDir, lock); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(lock)); err != nil {
		return false
	} else if realGit, err := filepath.EvalSymlinks(gitDir); err != nil || !strings.HasPrefix(real+string(filepath.Separator), realGit+string(filepath.Separator)) {
		return false
	}
	fi, err := os.Lstat(lock)
	if err != nil || !fi.Mode().IsRegular() || time.Since(fi.ModTime()) < staleGitLock {
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
