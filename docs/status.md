# Status - 2026-10-28

[overview.md](overview.md) repeats a few facts from here (the untested macOS sandbox, Codex without a real login, the gate's first keep on merit not yet released, no hive-eval pilot run yet).
When one of them changes, change it there too.

Focus, on the person's word (2026-10-24, [goals](../.hive/goals.md#right-now-a-tight-fast-rsi-loop)): a tight, fast self-improvement loop first, at maximum autonomy, with every agent as able as the lead to act on what the loop needs; work that doesn't serve the loop waits.

After the first milestone, the real-evaluation milestone ([goals](../.hive/goals.md#then-the-real-evaluation-milestone)): show on held-out real work that the Hive after its own improvement beats the frozen Hive and plain Claude Code, measured from outside.
It has its own project, hive-eval (goals and a README so far, no pilot run yet); the Hive changes it needs (a frozen mode, budget accounting, counting interventions) count as its work.

Where that leads ([goals](../.hive/goals.md#where-it-leads-something-new-built-every-day)): once the core works and RSI is shown on itself, the Hive builds something new every day, maintains it, and says it was built with The Hive; posting it stays the person's.
Not started, by design.

Paused:
- macOS and Windows, on the person's word (2026-10-24, issue #564: "only building linux releases for now and only focusing on the RSI").
  CI (`test`, `sandboxed-e2e`, `old-releases`), the update and install-smoke tests run on ubuntu-latest alone, release.yml builds linux/amd64 and linux/arm64 only, and only a Linux Hive follows pre-releases (`runners` in internal/mod/release/prerelease.go).
  Nothing in the merge path waits on another system; ihsan-sa/hive's main has no branch protection or rulesets requiring the old `test (macos-latest)`/`test (windows-latest)` checks (GitHub's API, 2026-10-25). install.sh on macOS and install.ps1 stop with "Linux only for now" when a release has no binary for them, and the site and README say so.
  The macOS and Windows steps of update.yml, install-smoke.yml and old-releases.yml were removed; `git show 65b1651:<file>` has them.
  They come back once the self-improvement loop is tight and fast: restore the matrices, the release targets and `runners`, with Windows in ci's matrix for pushes to main as well as PRs (issue #595), so a PR's Windows red has main's result to compare against. Decide then whether a release follows the merge rule: today a release needs main's whole ci workflow green (`mainGreen` in internal/mod/release reads the raw runs, not `core.Advisory`), so a flaky Windows job on `advisory_checks` would hold every release.
  Notes below about macOS or Windows describe how things stood before the pause.

What's done isn't listed here: `git log` and the journal record every change, [log.md](log.md) the decisions, and each feature's own doc how it works.
This file holds only what stands now: the focus, what's paused, what isn't verified and what's next, so two changes rarely touch the same line.
Don't add a line here for a change you made.

Not verified:
- A change the recursive gate kept on merit, released and run in place of the Hive that kept it (its first keep, PR #441, won only on a flake of the control's; [gate.md](gate.md)).
- Sharing against the live endpoint (https://hive-api.ihsan.cc/v1/report) is probed by hand, one report; the tests use a local endpoint that checks the same contract.
- Issues, comments and an upstream PR from a fork (`FindPR` by `owner:branch`, `onUpstream`) on real GitHub; the live probe covers fork, PR, merge and release on the fork only.
- Injection resistance of the tribunal and warden by a real model.
- The bubblewrap path live: `TestProbeBothPaths` (non-short, `internal/sandbox`) runs `hive sandbox-probe` under each path, but skips both inside a nested sandbox.
  Run it on a stock Ubuntu host.
- Approved steps on Windows: `execute` runs them with Git for Windows' `sh`, but only the three-system CI run exercises it, and a timeout there ends `sh` and not its children.
- Live agents with real Claude Code: the first-hour probes (`TestLiveAskLands`, `TestLiveModuleLifecycle`, `TestLiveImproveSweep`) are written but haven't run; run `tests/live.sh` on the tower.
  Sandboxed live runs read a short-lived token from Claude Code's credential file at run time; not exercised.
- Self-update from a real release asset over the GitHub API on a person's machine (the one-line install was run once, on v0.1.0).
- macOS Seatbelt sandbox; native Linux host (same namespace code as WSL).
- Autostart registration on a real account (the tmux test stubs systemctl and loginctl).

Next:
1. Owner decision: a live smoke run with real Claude (plan usage; bare vs sandboxed).
   The repo and releases are already public.
2. Run `go test ./internal/sandbox/ -run TestProbeBothPaths -v` on a stock Ubuntu host, and `HIVE_E2E_HARNESS=claude go test ./internal/e2e/ -run 'TestLiveWarden|TestLiveTribunal' -v -timeout 90m` for a real warden and panel (plan usage).
3. A live Codex run with a real Codex login: `HIVE_E2E_HARNESS=codex go test ./internal/e2e/ -run TestLiveWorker -v -timeout 60m` (worker, resume, read-only reviewer, merge, sandboxed).
   The fake path (`TestCodexEndToEnd`) passes; the rollout-file usage format is read from Codex 0.153's strings and an unauthenticated run, not yet from a real reading.

Resume: read CLAUDE.md, then this file; run `go test -short ./...` and `go test ./internal/e2e/`.
