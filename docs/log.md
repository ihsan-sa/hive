# Decision log

## 2026-10-22 Go, one static binary
- Decision: Go 1.27, single binary for CLI, daemon, sandbox launcher and in-sandbox agent client | Why: no runtime to ship, one-line install, cross-compiles | Tradeoff: no first-party Claude Agent SDK in Go; drive `claude -p --output-format stream-json` instead.

## 2026-10-22 Agents run as Claude Code print-mode sessions, resumed per turn
- Decision: each agent is a session id; a turn is one `claude -p --resume`; messages queue until the turn ends | Why: survives daemon restarts (output to files, processes detached, reattach by pid + start time); no long-lived pipes | Tradeoff: messages reach a busy agent at turn end (or via `hive agent inbox`).

## 2026-10-22 In-house Linux sandbox instead of bwrap
- Decision: namespaces built by the hive binary; egress through a host proxy that refuses local/private addresses | Why: no root, no packages (WSL Ubuntu had no bwrap, no sudo), and "no way to local services" needs filtering a plain shared netns can't give | Tradeoff: more code in `internal/sandbox`; macOS uses Seatbelt (untested).

## 2026-10-22 bubblewrap where Ubuntu restricts user namespaces
- Decision: when `kernel.apparmor_restrict_unprivileged_userns=1` (stock Ubuntu 23.10+), `sandbox-exec` hands the same mounts, tools and egress socket to `bwrap`, which runs `hive sandbox-inner` (loopback forwarder + command); WSL and other hosts keep the build's own namespaces | Why: the kernel refuses the hive binary's own user namespaces there, and bwrap's AppArmor profile is allowed them, so agents would otherwise fall back to bare runs | Tradeoff: two launch paths; `HIVE_SANDBOX_BWRAP=1` forces bwrap for tests.

## 2026-10-22 Commits leave checkouts as bundles made inside the sandbox
- Decision: `git bundle` runs sandboxed; host fetches with fsck, verifies descent from the recorded base | Why: an agent controls its `.git` (gitfile redirects, hooks, fsmonitor); host git never runs there | Tradeoff: an extra sandboxed step per landing.

## 2026-10-22 Checks use main's project.json; score/goals/settings are protected
- Decision: test/score commands come from main, not the PR; changes to `.hive/project.json`, `.hive/goals.md` and declared `protected` paths need the person | Why: a change must not rewrite the check it's judged by | Tradeoff: bootstrapping a project's score needs one person approval.

## 2026-10-22 Warden and tribunal run approved steps in the daemon
- Decision: replaces the v1 relay. Tiers route a step (T1 worker, T2 warden, T3 tribunal, ceiling person; autonomy moves T2); an approved script runs as `sh -c` in `.hive/risk/<id>` in the daemon after a sha256 digest check and `core.CeilingGuard`; the tribunal is three read-only runs over `tribunal_models`, two rounds, majority | Why: spec milestone 6, ported from the box's Claude build of the same spec | Tradeoff: no per-step sandbox beyond sh, its own directory and a timeout; the warden's script is the only thing weighed, so the warden must keep it to exactly the step.

## 2026-10-22 Read-only turns answer in structured output and read their run dir
- Decision: panelists vote through `--json-schema` like the reviewer; read-only turns get `--add-dir` for `/hive/run` and nothing else; a reviewer's land counts only with the token from its brief.md; the warden's brief gives host paths | Why: a real run found panelists had no shell to vote, reviewers couldn't read their inputs yet voted land, and warden scripts used sandbox paths on the host | Tradeoff: the fake harness now refuses shell and unlisted dirs on read-only turns, so it fails where Claude would.

## 2026-10-22 Squash merges, best-kept comparison
- Decision: squash merge pinned with `--match-head-commit`; measure each merge sha; compare with best kept score; revert via host-made commit + lessons line | Why: one commit per change makes reverts exact; best-kept stops drift under tolerance | Tradeoff: per-commit history of a branch isn't kept on main.

## 2026-10-22 Small = docs only
- Decision: no reviewer for docs-only changes ≤40 lines without protected paths; everything else is read | Why: comment-only detection in code is unreliable; conservative is safe | Tradeoff: a code typo fix still gets a review.

## 2026-10-22 System prompt inline inside sandboxes, file outside
- Decision: sandboxed turns pass `--append-system-prompt <text>`; bare Windows turns pass a file | Why: Windows argv is capped at 32k; inside the sandbox argv goes via the spec file with Linux limits, and older Claude Code builds may lack the -file flag | Tradeoff: two code paths in the Claude adapter.

## 2026-10-22 Red tests on a red main wait instead of going back
- Decision: when a change's tests fail, the gate looks at main's own test result first; if main is red too, the change waits (no fix round spent) and main's failure is one alert per main commit; once main is green the branch is brought up to date and tested again, and only a failure then goes back to the worker | Why: workers were sent to fix main's environment, which they can't | Tradeoff: a change that adds its own failure on top of a red main isn't caught until main is green.

## 2026-10-22 Egress lists, the person's floor, seats that name their harness
- Decision: `egress.default` and `egress.roles.<role>` hold a run's proxy to listed hosts (plus its harness's API host; no list is every public host, as before), refusals journalled as `run.egress_refused`; `floor` in the config raises any step whose text touches an entry to the person, at routing and at the point of effect; `tribunal_models` entries may be `{harness, model}`, refused at save and at convene when the harness is missing; a vote counts only as one clean structured answer, and seats run isolated | Why: carry the lines an existing warden holds (a host allow list, a rules floor its classifier can't lower, seats across vendors, one-verdict parsing) so moving onto this core doesn't weaken them | Tradeoff: the floor is the same best-effort text match as the ceiling; on macOS each command gets its own proxy port.
