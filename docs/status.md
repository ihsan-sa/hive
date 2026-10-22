# Status - 2026-10-22

Done:
- Full build per docs/spec.md, including milestone 6: risk tiers T1-T3, the warden, the tribunal and the always-the-person ceiling enforced at the point of effect (risk.md).
- Gate: the change classifier reads diffs with `--no-renames`, so moving a protected file out is protected; an emptied test command on main is red once the project has run one.
- Linux sandbox on stock Ubuntu 23.10+ (`apparmor_restrict_unprivileged_userns=1`): the same mounts, tools and egress run under bubblewrap; WSL keeps the build's own namespaces.
- Core (~3k lines), Linux namespace sandbox (+WSL, +Seatbelt), Claude Code + fake harness, all default modules, daemon + API, CLI, TUI, first run, autostart, self-update/self-release, installers, CI + release workflows, demo project.
- Tests passing:
  - unit: gate guarantees (pinned merge, check + verdict bound to sha, foreign git metadata refused, only hive/* pushed, protected paths), journal, harness parsing, egress filtering, schedules, TUI fits 50×16 to 120×40;
  - e2e (real daemon, fake harness, local forge): ask→merge with review, projects, failing tests stop with PR open, score drop → revert + lesson quoted in next brief, sweep files an ask, kill/restart at every stage with no duplicates, usage-limit park/resume, crash retry, request → warden → agent, module install + update, self-release.
  - risk on fakes (unit `internal/mod/risk`, e2e `TestRiskPaths`): T1 stays the worker's; warden do / safer / decline / ends silent; T3 → three-seat tribunal across models, two rounds, majority runs the warden's exact script; split panel declines a large gain → person "run" runs it; a script changed after the vote is refused by digest (unit only: e2e has no hook to swap it mid-flight); the ceiling holds a money script after a warden allow; a request with injected instructions to the warden runs only the warden's script; autonomy 1 asks first, 3 tells after; restart mid-step doesn't re-run.
  - rename hole: `TestRenameOutOfProtectedPath` fails on the old classifier, passes now. Passes both bare and inside the WSL sandbox.
  - WSL sandbox isolation checked by hand: /mnt, /home, WSL interop hidden; /etc, /usr read-only; only checkout writable; public HTTPS via proxy 200; local services 403; no direct network.

- Release v0.1.0 published with binaries (linux/darwin amd64+arm64, windows amd64). One-line install via `gh api …/install.ps1 | Out-String | iex` verified into a scratch dir; installs `hive-v0`.
- CI green on Windows, Ubuntu and macOS (vet, unit, e2e). Claude Code inside the WSL sandbox verified up to the API (invalid token → 401).

Not verified:
- The warden and tribunal with real models: only the scripted fake agents have played them, so the prompts' judgement (and injection resistance by a real model) is untested.
- The bubblewrap path live: `TestProbeBothPaths` (non-short, `internal/sandbox`) runs `hive sandbox-probe` under each path, but skips both inside a nested sandbox. Run it on a stock Ubuntu host.
- No Windows executor for approved steps: `execute` runs `sh -c`, so on a bare Windows host an approved step fails rather than running.
- Live agents with real Claude Code (no plan usage spent yet). Sandboxed live runs read a short-lived token from Claude Code's credential file at run time; not exercised.
- The GitHub forge path (gh issue/PR/merge) end to end; only the local forge is exercised.
- Installers and self-update with real release assets (no repo/release published).
- macOS Seatbelt sandbox; native Linux host (same namespace code as WSL).
- Interactive first run and TUI driven by hand (layout test only); autostart registration not executed on this machine.

Next:
1. Owner decisions: publish the repo + first release (makes install lines work); live smoke run with real Claude (plan usage; bare vs sandboxed).
2. Run `go test ./internal/sandbox/ -run TestProbeBothPaths -v` on a stock Ubuntu host, and `HIVE_E2E_HARNESS=claude go test ./internal/e2e/ -run TestLiveWarden -v` for a real warden (plan usage).
3. Codex adapter.

Resume: read CLAUDE.md, then this file; run `go test -short ./...` and `go test ./internal/e2e/`.
