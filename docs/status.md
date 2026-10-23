# Status - 2026-10-22

Done:
- Full build per docs/spec.md, including milestone 6: risk tiers T1-T3, the warden, the tribunal and the always-the-person ceiling enforced at the point of effect (risk.md).
- Gate: the change classifier reads diffs with `--no-renames`, so moving a protected file out is protected; an emptied test command on main is red once the project has run one.
- Linux sandbox on stock Ubuntu 23.10+ (`apparmor_restrict_unprivileged_userns=1`): the same mounts, tools and egress run under bubblewrap; WSL keeps the build's own namespaces.
- An isolated lead granted modules starts under bubblewrap (the mount-point fix landed in #14). CI runs the e2e under bubblewrap on ubuntu-latest too (`sandboxed-e2e`, which probes the sandbox first), so a sandbox break goes red on a PR.
- Core (~3k lines), Linux namespace sandbox (+WSL, +Seatbelt), Claude Code + fake harness, all default modules, daemon + API, CLI, TUI, first run, autostart, self-update/self-release, installers, CI + release workflows, demo project.
- Tests passing:
  - the control center in tmux (`HIVE_E2E_TUI=1`, `TestControlCenterInTmux`): first run on a clean HOME, a chat that lands, dashboard, approve and decline from Waiting, module access, settings saved, quit and reopen, 80x24 to 160x48 and resizing;
  - unit: gate guarantees (pinned merge, check + verdict bound to sha, foreign git metadata refused, only hive/* pushed, protected paths), journal, harness parsing, egress filtering, schedules, TUI fits 50×16 to 120×40;
  - e2e (real daemon, fake harness, local forge): ask→merge with review, projects, failing tests stop with PR open, score drop → revert + lesson quoted in next brief, sweep files an ask, kill/restart at every stage with no duplicates, usage-limit park/resume, crash retry, request → warden → agent, module install + update, self-release.
  - risk on fakes (unit `internal/mod/risk`, e2e `TestRiskPaths`): T1 stays the worker's; warden do / safer / decline / ends silent; T3 → three-seat tribunal across models, two rounds, majority runs the warden's exact script; split panel declines a large gain → person "run" runs it; a script changed after the vote is refused by digest (unit only: e2e has no hook to swap it mid-flight); the ceiling holds a money script after a warden allow; a request with injected instructions to the warden runs only the warden's script; autonomy 1 asks first, 3 tells after; restart mid-step doesn't re-run.
  - fix round 1, after a real Claude + GitHub run on the tower: the fake harness now behaves like Claude on a read-only turn (no shell tool; reads only its checkout and the dir it was given), so `TestRiskPaths` and `TestGateAndLoop` fail without the fixes. Panel votes come back as a structured answer; read-only turns get `--add-dir` for their run dir and a reviewer's land must carry a token from its brief.md; the warden's brief names the host paths of the checkout and home its script runs against.
  - rename hole: `TestRenameOutOfProtectedPath` fails on the old classifier, passes now. Passes both bare and inside the WSL sandbox.
  - WSL sandbox isolation checked by hand: /mnt, /home, WSL interop hidden; /etc, /usr read-only; only checkout writable; public HTTPS via proxy 200; local services 403; no direct network.

- A person's own module is built as a project of its own and installed from it (`install_module --project`, on the person's "install"); the workspace lead names `--project` on every brief, so an ask no longer lands in the only project by default (`TestAskForModule`). An installed module follows its project only when the repository gets a newer `vX.Y.Z` tag, and nothing tags a module project yet.
- Asked about the Hive itself, the lead reads its installed modules and `hive agent status` before it answers, instead of guessing (`TestAskAboutTheHive`; live twin `TestLiveAskAboutTheHive`, not yet run on the host). The lead runs on its own model setting, `lead_model`, opus by default; an older config keeps a model the person chose, and the old sonnet default moves to opus.
- Release v0.1.0 published with binaries (linux/darwin amd64+arm64, windows amd64). One-line install via `gh api …/install.ps1 | Out-String | iex` verified into a scratch dir; installs `hive-v0`.
- CI green on Windows, Ubuntu and macOS (vet, unit, e2e). Claude Code inside the WSL sandbox verified up to the API (invalid token → 401).

Not verified:
- The tribunal with real models under bubblewrap: `TestLiveTribunal` passed on the tower bare (three models approved, the majority ran the warden's script). Under bubblewrap no tribunal formed, because the ask was clipped at 120 characters and a sandboxed worker read the hidden fixture as already gone; both are fixed (the whole ask reaches the agents, and the worker role says outside paths are hidden), and the live probes now run sandboxed when the sandbox works. Re-run under bubblewrap. Injection resistance by a real model is untested.
- The bubblewrap path live: `TestProbeBothPaths` (non-short, `internal/sandbox`) runs `hive sandbox-probe` under each path, but skips both inside a nested sandbox. Run it on a stock Ubuntu host.
- Approved steps on Windows: `execute` runs them with Git for Windows' `sh`, but only the three-system CI run exercises it, and a timeout there ends `sh` and not its children.
- Live agents with real Claude Code (no plan usage spent yet). Sandboxed live runs read a short-lived token from Claude Code's credential file at run time; not exercised.
- The GitHub forge path (gh issue/PR/merge) end to end; only the local forge is exercised.
- Installers and self-update with real release assets (no repo/release published).
- macOS Seatbelt sandbox; native Linux host (same namespace code as WSL).
- Autostart registration on a real account (the tmux test stubs systemctl and loginctl).

Next:
1. Owner decisions: publish the repo + first release (makes install lines work); live smoke run with real Claude (plan usage; bare vs sandboxed).
2. Run `go test ./internal/sandbox/ -run TestProbeBothPaths -v` on a stock Ubuntu host, and `HIVE_E2E_HARNESS=claude go test ./internal/e2e/ -run 'TestLiveWarden|TestLiveTribunal' -v -timeout 90m` for a real warden and panel (plan usage).
3. A live Codex run with a real Codex login: a worker turn, a resume and a read-only reviewer seat, sandboxed.

Resume: read CLAUDE.md, then this file; run `go test -short ./...` and `go test ./internal/e2e/`.
