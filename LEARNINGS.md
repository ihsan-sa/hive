# Learnings

Gotchas hit while building. Append; don't rewrite.

- **[harness] `claude -p` waits 3s for stdin** unless stdin is redirected; always give it a file (we pass the prompt that way, no argv length limits). `--append-system-prompt-file` exists though `--help` doesn't list it.
- **[harness] stream-json result** carries `structured_output` (with `--json-schema`), `is_error`, `api_error_status`, `total_cost_usd`; usage arrives as `rate_limit_event` with `status` (allowed / allowed_warning / rejected), `resetsAt`, per-window `unifiedWindows`.
- **[windows] wsl.exe** prints UTF-16LE for `-l -v`, and expands `$VAR` in its command line; use `--exec` and pass a script path. Git Bash rewrites `/mnt/...` args unless `MSYS_NO_PATHCONV=1`.
- **[windows] Git Bash `sh` isn't on PATH** for processes started from PowerShell; find it via `git.exe`'s install dir (`core.WindowsShell`).
- **[linux sandbox] remounting a bind read-only** fails with EPERM unless the source's locked flags (nosuid, nodev, noexec, atime) are kept; device-node binds must not get `nodev`.
- **[linux sandbox] WSL Ubuntu 22.04**: unprivileged user namespaces work, `slirp4netns` present, no `bwrap`, no passwordless sudo.
- **[tests] Windows** won't remove a temp dir with an open file; close journals in tests.
- **[editing] python heredoc replacements**: `\\n` inside a non-raw triple-quoted string became a real newline in Go source; use the Edit tool for strings with escapes.
- **[windows] O_APPEND output handles** are append-only on Windows; MSYS `sh` exits 1 silently when writing to one. Give children a write handle seeked to the end (`proc.OpenOutput`).
- **[linux sandbox] unprivileged bind of a tree with submounts needs MS_REC** (else EINVAL: it could reveal what submounts cover). Then remount each submount read-only from /proc/self/mountinfo. WSL's /usr has /usr/lib/wsl/* submounts.
- **[go] exec.Command(name)** resolves `name` against the *current* process's PATH at construction and keeps the error; setting `cmd.Path` later doesn't clear it. Resolve against the target environment first.
- **[network] the local router's DNS doesn't resolve example.com** here; tests use github.com.
- **[wsl] Claude Code in WSL** lives in ~/.local/bin (not on non-login PATH), version 2.1.71 here; its help doesn't list `--append-system-prompt-file`, so inside sandboxes the prompt is passed inline.
- **[go] exec.Cmd.Start can only be called once**, even after it failed ("exec: already started"); a retry with different flags needs a fresh Cmd. Bit us when Windows (GitHub Actions job objects) refuses CREATE_BREAKAWAY_FROM_JOB.
- **[editing] Git Bash heredocs collapse a doubled backslash to one** even with a quoted delimiter here, so Python edits fed through them lose escapes; write the script to a file with the Write tool, or use Edit.
- **[landing] the box that lands this repo has no Go**, so a local `tests/green.sh` or cc-green run there checks nothing (cc-green ran 0 checks). The evidence that the suite passed is GitHub CI on the PR's head, which runs the same vet, short tests and e2e.
- **[go] `core.Clip` flattens whitespace** (it joins `strings.Fields`), so it is for one-line summaries; a diff or a log clipped with it loses its newlines. Slice the string instead.
- **[proc] a zombie answers kill(pid, 0) and keeps its start time**, so a turn orphaned by a Hive restart and never reaped (a container whose PID 1 doesn't reap) read as alive forever: "running turn 1 $0.00". Liveness also checks the process isn't a zombie.
- **[update] a release workflow runs the old release's updater, not main's**, so a fix shows up only from the release after it. v0.1.2's `hive update` asked the configured forge for the latest release, and install-smoke's local forge has none, so it printed `hive: not found`; main asks github.com over HTTPS. Under update.yml, from == to is skipped, so a release's own leg proves nothing until the next one.
- **[linux] `os.Executable` follows a rename**: it reads /proc/self/exe, so after an update moves the running binary to `hive.old`, it names `hive.old`. v0.1.1 restarted on that and came back as v0.1.1, so ubuntu from v0.1.1 never reaches the new version. `onDisk` (since v0.1.2) strips the suffix.
