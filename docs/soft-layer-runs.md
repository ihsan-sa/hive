# The soft-layer experiment: the real runs before real4

The runs of task 261025-045050-de0d9b6a8d (issue #618), on real agents (Opus) on the host, each started by a warden's step as a detached systemd user unit. The experiment, its arms and how a run fits the machine are in [soft-layer.md](soft-layer.md); its newest run (real4) is written up there.

## The other five arms (real3)

**Third real run: the other five arms (2026-10-25, 16:17-21:17Z, from 9b5cde4, one at a time, 60 minutes each, same caps).** Output in `~/hive/.hive/soft-layer/261025-real3/`. The machine held.

| arm | outcome | runs | turns | messages | peer | briefs | asks | merged | the ask's design | main's red fix |
|---|---|---|---|---|---|---|---|---|---|---|
| chatty | not landed: the ask in testing | 11 | 15 | 0 | 0 | 3 | 0 | 2 | `--timeout` flag, 12h cap (off brief) | tests only |
| quiet | not landed: the ask merging | 11 | 14 | 0 | 0 | 3 | 0 | 2 | `# hive-timeout:` line, 4h | guard loosened (`core.Exists`) |
| essay-tools | not landed: the ask merging | 11 | 13 | 0 | 0 | 3 | 0 | 2 | `# hive-timeout:` line, 4h | guard loosened (`core.Exists`) |
| philosophy | not landed: the ask merging | 11 | 14 | 0 | 0 | 3 | 0 | 2 | `# hive-timeout:` line, 4h | guard loosened, `--project` skipped |
| vague | not landed: the ask merging, red fix still in its checks | 9 | 12 | 0 | 0 | 2 | 0 | 0 | `# hive-timeout:` line, 4h | tests only, after one `fix` round |

Every arm ran the same course as `standard` and `essay`: the lead filed the ask within 10 seconds (every lead restated the done criteria and named `risk.go:41`/`:808` for the worker), a worker built it in 3-8 minutes, the reviewer and the Sonnet security seat said land within two minutes, then the red main (above) held it at merge, improve raised the missing starting score at about the 12-minute mark, and the project's lead diagnosed it in one to three minutes and filed a fix that merged in about 7. What then kept every ask from landing in 60 minutes is the hard layer: `Effects.Merge` refused the ask's head for want of a passing test run, the task stopped after repeated refusals, and its recheck queued behind a 20-minute score run on main with `max_checks` 1.

What differed, arm by arm:

- **chatty**: no talk at all, despite "message every related run at every step". Its worker went off brief (a `--timeout` flag on `warden`/`decide`, 12h), and both reads landed it; its lead caught the mismatch only when the stuck alert handed the task over at minute 43, and sent it back. Its lead also named the test-step/merge-step contradiction on its own.
- **quiet**: indistinguishable from `standard`. Working alone was already what everyone did.
- **essay-tools**: like `standard`; the bare command list added nothing visible over the essay alone. Its project lead wrote a `finish` account with tested and unsure lines.
- **philosophy**: like `standard`, with the most careful follow-up: the reviewer's follow-up worker also found and fixed a real flake (`internal/gate` reap tests reading `/proc/<pid>/environ` before execve sets it) instead of filing it. Its project lead reported main's new starting score.
- **vague**: the one arm whose reviewer said `fix` (a doc comment the worker's change had split), and the only lead that scheduled its own wake-ups to check back (at :08 and :38). Its red fix, test-only like `essay`'s, was still waiting for its check when the hour ran out, so it merged nothing.

## `standard` against `essay`, first time (real2)

**Second real run: `standard` against `essay` (2026-10-25, 15:34-16:13Z, from eeaeeea, one at a time, Opus, in a unit capped at 4 CPUs and 6 GB).** Output in `~/hive/.hive/soft-layer/261025-real2/`. The machine held.

| arm | outcome (as the test then read it) | runs | turns | messages | peer | briefs | asks | merged |
|---|---|---|---|---|---|---|---|---|
| standard | a merge after 19m35s | 9 | 10 | 0 | 0 | 3 | 0 | 1 |
| essay | a merge after 18m14s | 9 | 10 | 0 | 0 | 3 | 0 | 1 |

The merge in both arms was not the ask. Both sub-Hives started from a red main: two `internal/mod/risk` tests (`TestAutonomyDecidesProtectedChanges`, `TestModuleInstallAskCarriesTheProject`) fail in every Hive's own checks since checks set `TMPDIR=/hive/home/tmp` (`core.checkTmp`), because `opInstall` refuses any path under `/hive/`. H0's own checks fail the same way (`gate-*-test-*.log` from 12:04Z on), and H0 filed it at 16:10Z (task 261025-161000-2f8ecd9fb4). So in each arm:

1. The lead filed the ask as a fix within 10 seconds, with the done criteria word for word, and told the person in its chat (`essay`'s lead twice, the second a longer restatement).
2. One worker built it in 3-5 minutes. Both designs are the brief's: `# hive-timeout:` read from the pinned script text, capped at 4h, decide refusing a bad line, "timed out after X", docs and tests. `standard`'s also put the time in the person's ask and the panel brief; `essay`'s added a decide-time refusal test. Both also edited `modules/agents/warden.md` and `docs/soft-layer.md`: the task text is in the clone's own `softlayer_test.go`, which `standard`'s worker found and cited, a leak worth knowing when reading any arm.
3. The reviewer said land in 20-30 seconds, then the security seat (Sonnet) said land.
4. The ask then sat in `merging` with a stale reason ("waiting on a security read", though the read had landed): its tests failed only where main fails too, which the work module lets through review, but `Effects.Merge` refuses without a passing test run for the exact commit, and that refusal never reaches the task's reason. A hard-layer bug, filed with this change.
5. improve raised "main has no starting score" in the project's chat; the project's lead diagnosed the cause in under a minute and filed a fix, which merged in 7-8 minutes. Here the arms differ: `standard`'s fix lets a path through the sandbox-path guard whenever it exists on the host (`core.Exists`), weakening a guard in production code, and its security seat landed it; `essay`'s keeps the guard and makes only the tests set its roots aside (`sandboxRoots`, `hostTemp(t)`), the narrower fix.
6. The test took that merge as the arm landing and stopped it, with each reviewer's follow-up just started. Fixed with this change: an arm now ends only when the lead's own task merges (`askLanded`).

## How each arm behaves

**How each arm behaves, across the runs (real2, real3 and real4).** The same, nearly step for step. In seven arms, from the Hive's full prompts to one sentence, no agent sent another a message (0 in every arm, `chatty` included) and nobody asked past their reach, wrote a note or gave feedback: everything went person → lead → brief → worker → reviewer → seat, and improve → project lead → brief, with the chats as the only talk. So the soft layer, at any wording tried, doesn't make these agents behave as a hive on a task like this; the hard layer does the coordinating (the brief, the queue, the review schema, the merge path, improve's alerts), and the agents fill the slots it gives them. The one-line roles (`essay`, `essay-tools`, `philosophy`, `vague`) were as fast and as on-brief as the full ones; the off-brief designs (a `--timeout` flag in place of the script's line) came from the arms with the most words, `chatty` in real3 and `standard` in real4, and both passed review. Where the arms did differ, it was in judgement within a slot, not in shape: four of seven fixed the red main by loosening a production guard and two by changing tests only, and both kinds passed the security seat. One task and one run per arm can't tell those apart from chance. The baseline's peer talk (2 worker→worker messages in H0's 76) came from many workers on related tasks over a day, which one task with one worker at a time never sets up: a test of "behave as a hive" needs tasks that overlap.

## All seven at once (real1)

**First real run (2026-10-25, 13:16Z, all seven arms side by side, from 3d2020e):** no result. Within about 35 minutes the `essay` arm's sub-Hive daemon stopped answering (connection refused), and the test then lost its record to `t.Fatal`; the machine reached a load of about 600 and its memory ceiling, and the box froze the run. Hence the shape above: one arm at a time, capped inside, starting with `standard` against `essay`.
