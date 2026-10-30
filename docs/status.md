# Status - 2026-10-30

[overview.md](overview.md) repeats a few facts from here (the untested macOS sandbox, Codex without a real login, no real model judging a merge yet, no hive-eval pilot run yet).
When one of them changes, change it there too.

How a change lands now (the person, 10-30: "they can freely edit everything including the core with no process"; earlier: "just send it, minimal gates, fail fast, stuff breaks but revert"):
- An agent lands its own change, the core included, with nothing in between.
  A worker's `hive agent finish` merges its PR at once: no test, reviewer, security read, panel, protected-path approval, forge check or lead-held draft waits before it (`Effects.Merge`).
  Or any agent pushes to main itself on its own token (`git push https://github.com/$HIVE_REPO HEAD:main`), as a session in home does, and owns that push's CI after.
  A module's program runs with no code review first.
  An agent may run the tests; nothing makes it.
- What stays is the way back and the hard limits.
  Main keeps its history (the keep-history ruleset: no force-push, no deletion), so any change can be reverted.
  improve measures main after every merge and reverts a break or a score drop beyond noise ([score.md](score.md)); a fall with tests green after a change taken as a gain goes to a judge ([judge.md](judge.md)).
  An agent's own push isn't measured by the Hive: the agent watches its CI (`gh run list --branch main`) and fixes or reverts it.
  H0 installs only a main commit whose forge CI passed (`required_checks` in `tests/LANDING-security.toml` name them), rolls back a build that fails to start, and keeps the one it replaced for `hive agent fallback`; the nightly backup goes to `~/hive-backups`, with a git bundle of each private repository, which has no keep-history ruleset on the free plan.
  The publish scan still sends back a change that would publish private details, the hard limits (money, credentials, private details going out, opening the machine, anything outward in the person's name, deleting repositories or history) go to a warden, and the person's `hold_merges` still holds a project's merges.
- No gate: the recursive gate is out of the merge path and its code is deleted (#2142, PR #2166; [gate.md](gate.md)).
- H0 follows main (8fab9b7, 8474502): behaviour (the default modules) is copied from main into `.modules/` on every merge, and on the next five-minute look after a push; the binary is built from the newest main commit whose forge CI passed, stamped `<line>-main.<sha8>`, and installed between turns, with the one it replaced kept as `.old` for `hive agent fallback`.
  H0 ran `v0.1.150-main.8474502b` on 2026-10-30.
  Releases are still cut for every other install.

The first milestone is met, by the record in [goals](../.hive/goals.md#then-the-gate-recursive): #868, proposed by a step-back and kept by the gate on merit, merged as 1f2f0d0 and has run on H0 since v0.1.120 (2026-10-27).
That release was cut through the lead's warden request; H0 now installs main's builds with no one in between.

Focus, on the person's word (2026-10-24, [goals](../.hive/goals.md#right-now-a-tight-fast-rsi-loop)): a tight, fast self-improvement loop first, at maximum autonomy, with every agent as able as the lead to act on what the loop needs; work that doesn't serve the loop waits.

After the first milestone, the real-evaluation milestone ([goals](../.hive/goals.md#then-the-real-evaluation-milestone)): show on held-out real work that the Hive after its own improvement beats the frozen Hive and plain Claude Code, measured from outside.
It has its own project, hive-eval (goals and a README so far, no pilot run yet); the Hive changes it needs (a frozen mode, budget accounting, counting interventions) count as its work.

Where that leads ([goals](../.hive/goals.md#where-it-leads-something-new-built-every-day)): once the core works and RSI is shown on itself, the Hive builds something new every day, maintains it, and says it was built with The Hive; posting it stays the person's.
Not started, by design.

Paused:
- macOS and Windows, on the person's word (2026-10-24, issue #564: "only building linux releases for now and only focusing on the RSI").
  CI (`test`, `sandboxed-e2e`, `old-releases`), the update and install-smoke tests run on ubuntu-latest alone, release.yml builds linux/amd64 and linux/arm64 only, and only a Linux Hive follows pre-releases (`runners` in internal/mod/release/prerelease.go).
  Nothing in the merge path waits on another system; ihsan-sa/hive's main has no branch protection or rulesets requiring the old `test (macos-latest)`/`test (windows-latest)` checks (GitHub's API, 2026-10-25).
  install.sh on macOS and install.ps1 stop with "Linux only for now" when a release has no binary for them, and the site and README say so.
  The macOS and Windows steps of update.yml, install-smoke.yml and old-releases.yml were removed; `git show 65b1651:<file>` has them.
  They come back once the self-improvement loop is tight and fast: restore the matrices, the release targets and `runners`, with Windows in ci's matrix for pushes to main as well as PRs (issue #595), so a PR's Windows red has main's result to compare against. Decide then whether a release follows the merge rule: today a release needs main's whole ci workflow green (`mainGreen` in internal/mod/release reads the raw runs, not `core.Advisory`), so a flaky Windows job on `advisory_checks` would hold every release.
  Older notes about macOS or Windows (in git history) describe how things stood before the pause.

What's done is not listed here: a change doesn't add a line to this page or to [log.md](log.md) (issue #2100).
Every PR edited the same spot here, so independent PRs conflicted and each conflict cost a worker a turn (docs/status.md was touched 52 times and docs/log.md 51 in the 221 commits of 10-26 to 10-29).
What a change did and why is its PR's description (the worker's account), in `git log` and the record (`record/task/<id>.md` in the knowledge base).
The Done list as it stood is in `git show df6ae15:docs/status.md`.
Edit this page only when what it says now stops being true: the focus, what is paused, not verified or next.

Not verified:
- A real model's judgement of a merge whose score fell with tests green: no judge run has happened yet ([judge.md](judge.md)).
- Sharing against the live endpoint (https://hive-api.ihsan.cc/v1/report) is probed by hand, one report; the tests use a local endpoint that checks the same contract.
- Issues, comments and an upstream PR from a fork (`FindPR` by `owner:branch`, `onUpstream`) on real GitHub; the live probe covers fork, PR, merge and release on the fork only.
- Injection resistance of the tribunal and warden by a real model.
- The bubblewrap path live: `TestProbeBothPaths` (non-short, `internal/sandbox`) runs `hive sandbox-probe` under each path, but skips both inside a nested sandbox.
  Run it on a stock Ubuntu host.
- Approved steps on Windows: `execute` runs them with Git for Windows' `sh`, but only the three-system CI run exercises it, and a timeout there ends `sh` and not its children.
- The live probes with real Claude Code (H0's own turns run on it all day): the first-hour probes (`TestLiveAskLands`, `TestLiveModuleLifecycle`, `TestLiveImproveSweep`) are written but haven't run; run `tests/live.sh` on the tower.
  Sandboxed live runs read a short-lived token from Claude Code's credential file at run time; not exercised.
- Self-update from a real release asset over the GitHub API on a person's machine (the one-line install was run once, on v0.1.0).
- macOS Seatbelt sandbox; native Linux host (same namespace code as WSL).
- Autostart registration on a real account (the tmux test stubs systemctl and loginctl).

Next:
1. Run `go test ./internal/sandbox/ -run TestProbeBothPaths -v` on a stock Ubuntu host, and `HIVE_E2E_HARNESS=claude go test ./internal/e2e/ -run 'TestLiveWarden|TestLiveTribunal' -v -timeout 90m` for a real warden and panel (plan usage).
2. A live Codex run with a real Codex login: `HIVE_E2E_HARNESS=codex go test ./internal/e2e/ -run TestLiveWorker -v -timeout 60m` (worker, resume, read-only reviewer, merge, sandboxed).
   The fake path (`TestCodexEndToEnd`) passes; the rollout-file usage format is read from Codex 0.153's strings and an unauthenticated run, not yet from a real reading.

Resume: read CLAUDE.md, then this file; run `go test -short ./...` and `go test ./internal/e2e/`.
