# The soft-layer experiment

The person (2026-10-24, 23:00-23:21 local): try different soft layers, "changing the prompts and working philosophy": spawn Hives in the same state as this one, give them very different agent prompts ("some super specific, some versions super vague, some only based on philosophy or even giving it my essay"), have them tackle a real RSI fix, "and you observe, look if they behave as a hive". Task 261025-032252-aebbfd79f3, issue #573.

The hard layer (core, sandbox, merge path, tests, reviews) is the same in every arm. Only the words the agents start with change: the agents module (`orientation.md` and the role prompts).

## The arms

`internal/softlayer` (`go run ./cmd/hive-soft variants`; `hive-soft show <arm> [role]` prints one):

| arm | orientation | roles |
|---|---|---|
| `standard` | the Hive's own (1015 words) | the Hive's own: the control |
| `chatty` | standard, plus "talk, a lot": message every related run at every step, reply to everything | standard |
| `quiet` | standard minus "Talk to each other like a hive", plus "work alone": no peers, no briefs, no helpers | standard minus the worker's "Others are working beside you" |
| `essay` | the person's essay, verbatim (2089 words) | one line: "You are a worker." etc. |
| `essay-tools` | the essay, then the bare list of `hive agent` commands | one line |
| `philosophy` | a page of principles (simplicity, talk, measure, remember, ask before acting outside, improve the Hive), no procedure | one line |
| `vague` | "You are an agent in a team of agents working on a software project. Do good work." | one line |

From lots of comms (`chatty`) to none (`quiet`), and from very specific (`standard`) to almost nothing (`vague`). `essay` is the person's suggestion as given; `essay-tools` separates "the essay alone doesn't say how to call the Hive" from "the essay doesn't make agents behave as a hive". The structured parts stay whatever the arm: a reviewer still has to answer `land` or `fix` in its schema, and the harness still gives every agent `hive agent` on its PATH.

## How an arm runs

`TestSoftLayer` (`internal/e2e/softlayer_test.go`), per arm, one arm at a time (see *Fitting the machine*):

1. A fresh Hive (its own workspace and local forge), its agents module replaced by the arm's (written with a source record that isn't `builtin`, so the default install never restores the standard prompts; `TestInstallSurvivesTheDefaults`).
2. This Hive's state: its committed source as the project `hive` (with `.hive/goals.md`, `lessons.md` and `project.json`, so the same tests and score), and, with `HIVE_SOFT_KB=<kb>`, the knowledge base's notes and `practice/` (not `record/`).
3. One real task, in the lead's chat: by default an open fix from this Hive's queue (261024-192651, issue #460: an approved warden step can say it needs longer than 10 minutes); `HIVE_SOFT_TASK` gives another. The first default, the gate's unequal TMPDIRs, landed as 4425272 before any real run.
4. A local forge has no CI, so the test stands in for the forge checks main's `tests/LANDING-security.toml` requires (`test (ubuntu-latest)`, `sandboxed-e2e`), reporting them passed on each open PR's head. Without that, the first fake run showed, every arm waits at "waiting for the forge's checks" forever. Editing the clone's list instead breaks its own `TestRequiredChecks`, so the sub-Hive's tests would be red on main. The sub-Hive's own tests, reads and score still decide.
5. The arm's caps are in *Fitting the machine* below. An arm whose sub-Hive daemon stops answering is reported so, with its journal and daemon log kept (the first real run lost the `essay` arm's record to a `t.Fatal`).
6. Wait until the lead's own task (the ask) lands on its main, not any merge (`askLanded`), or `HIVE_SOFT_WAIT` runs out (90 minutes on a real harness), then observe.

The test checks that every run the sub-Hive made started from the arm's orientation, and fails only if an arm can't be set up; it judges nothing. Each arm's journal, chats, runs, `main.log` and `report.json` go in `HIVE_SOFT_OUT/<arm>/`, and the table in `HIVE_SOFT_OUT/table.md`.

**Safety.** An arm's warden may have been told only "You are a warden.", and an approved warden script runs on the machine outside any sandbox. So unless `HIVE_SOFT_WARDENS=1`, each sub-Hive runs at autonomy 1: every step past an agent's reach is asked of the person (this test, which never answers) first, so no sub-Hive script runs. The asks are still counted and kept, which is what shows whether an arm's agents knew to ask.

```
# the pipeline, on the fake harness (prompts don't steer the fake agent)
HIVE_SOFT=standard,vague HIVE_SOFT_STATE=demo go test ./internal/e2e -run TestSoftLayer -v
# the experiment: real agents, plan usage, on a host with a Claude login
systemd-run --user --scope -p MemoryHigh=2500M -p MemoryMax=3G -p MemorySwapMax=0 -p CPUWeight=20 -p IOWeight=20 \
  env HIVE_SOFT=standard,essay HIVE_E2E_HARNESS=claude HIVE_SOFT_KB=~/hive/.hive/kb HIVE_SOFT_OUT=~/hive/.hive/soft-layer/<run> \
  go test ./internal/e2e -run 'TestSoftLayer$' -timeout 4h -v
```

## Fitting the machine

The first real run (2026-10-25, issue #771) started all seven arms side by side, each sub-Hive with no ceilings and its checks building on every core: about 28 builds and test runs at once, load ~600 on 20 cores, disks saturated, the Hive's 8 GB share full and swapping, until Autobox froze and then stopped it (and the lead's login check was killed while it was frozen). So now:

- **One arm at a time.** `HIVE_SOFT_PARALLEL=2` runs two; more isn't allowed. An arm's slot is freed only after its daemon, what it left running and its folder are gone, so the next arm starts on a quiet machine. Seven arms one by one at up to 90 minutes each is a long run: start with `standard,essay` and add arms in later runs.
- **Capped inside.** Each sub-Hive has `max_workers` 1, `max_checks` 1 and `max_gates` 1, saved as the person's ceilings so its pace can't raise them. Its daemon is started pinned to 4 CPUs (`softCPUs`), and CPU affinity passes to everything it starts, sandboxed checks included; Go sizes `GOMAXPROCS`, `go build -p` and `go test`'s parallelism to it, so no arm builds on more than 4 cores whatever its checks' environment. Where a check does get the daemon's environment (a bare sandbox), it also has `GOFLAGS=-p=2` and `GOMAXPROCS=4`.
- **Under a memory ceiling of its own.** On Linux a real run (`HIVE_E2E_HARNESS` set) refuses to start unless its own cgroup has a `memory.max` of at most 4 GiB: run it under a `systemd-run --user --scope` as above, with `MemorySwapMax=0`, so a run that outgrows its ceiling ends inside it, without the machine swapping and without touching the Hive's own runs. `HIVE_SOFT_UNBOUNDED=1` skips the check.
- **Measured.** Each arm's row in the table has its peak memory (the summed RSS of the test's processes and their descendants, sampled every 2 seconds) and the machine's highest 1-minute load while it ran; under a cgroup the table also gives the cgroup's own `memory.peak`.

Not shared: the Go build and module caches. Each check builds in a home of its own that is removed after it (`internal/core/disk.go`), and each sub-Hive keeps its own module cache, which its own prune may empty, so a cache shared across sub-Hives could be emptied under another's check. With one arm at a time the cost is one module download per arm.

The dry run on the fake harness (2026-10-25, `HIVE_SOFT=standard,essay` on the clone of the Hive's own source, the default task): the arms ran one after the other, each went lead → worker → the clone's `go vet` and short tests → review → merge in about 3 minutes, and the test's processes peaked at 341 MB (`standard`) and 484 MB (`essay`). The peak load, 39 and 37, is the machine's, other Hive work included. Real agents add their harness processes (a lead, a worker and a reviewer at a time with these ceilings), which is what the 3 GB ceiling leaves room for.


## What is observed

`softlayer.Observe` reads any Hive's journal (`hive-soft observe [--since t] [--until t] [--json] events.jsonl`):

- **runs** by role, and agent **turns**;
- **messages** (`msg.sent`), by role pair (`worker→lead`), and **peer**: worker to worker, the lateral talk a hive is meant to have and the person's framing (a sweep's message to a worker shows in the pairs, not here);
- **briefs** an agent filed and **helpers** it spawned (a task's first save, so re-saves don't count);
- **asks** past an agent's reach (risk requests), **finished** done/blocked, knowledge-base **notes**, **feedback** about the Hive, and **merged** changes.

## The baseline: this Hive, tonight

`hive-soft observe` over this Hive's own journal, all of it (to 2026-10-25 ~04:20Z, 1586 minutes): 2011 runs (reviewer 1688, worker 211, warden 66, sweep 35, filer 5, lead 3), 3231 turns, **76 messages**: lead→worker 34, worker→lead 20, sweep→lead 19, **worker→worker 2**, sweep→worker 1; so **2 peer (worker to worker) messages** in 76. 304 briefs, 1 helper, 49 asks, 792 done and 26 blocked, 24 notes, 28 feedback, 110 merges. The person's count at 23:21 local was 66 messages, 2 of them worker to worker. Since 22:00Z alone: 16 messages, 0 peer.

So the standard prompts make a hub, not a hive: almost every message goes through the lead, and the "talk to each other like a hive" paragraph (204ddf0) is too new to show here. The arms test whether any wording changes that, and what it costs.

## Results

A sub-Hive needs the host's Claude login, which an agent's sandbox doesn't hold (`claude -p` there says "Not logged in"), so a real run goes to a warden on the host. The step limit is 10 minutes, so the warden starts it as a detached `systemd-run --user` service with the scope's limits, not as a scope.

### `standard` against `essay`, under the caps (2026-10-25, real4)

Task 261025-165948, issue #822. A warden step (request 261025-182842) started the unit `hive-soft-261025-real4` from main 45079e9: `MemoryHigh=2500M`, `MemoryMax=3G`, `MemorySwapMax=0`, `CPUWeight=20`, `IOWeight=20`, `Nice=10`. It waited for the earlier five-arm run (real3) to end, so one arm ran on the machine at a time, and then ran 21:19-21:55Z. Each sub-Hive cloned the host's main at that moment (6043ef38c), whose tests were green; the task was the default one, the `# hive-timeout:` line on an approved warden step. Output: `~/hive/.hive/soft-layer/261025-real4/`.

| arm | outcome | minutes | runs | turns | messages | peer | briefs | helpers | asks | finished | notes | feedback | merged | peak MB | peak load |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| standard | landed f9f2749b0 after 17m1s | 17 | 5 | 6 | 0 | 0 | 2 | 0 | 0 | done 1 | 0 | 0 | 1 | 866 | 49.4 |
| essay | landed b1f4df6b7 after 17m21s | 18 | 5 | 6 | 0 | 0 | 2 | 0 | 0 | done 1 | 0 | 0 | 1 | 842 | 52.8 |

The run's cgroup peaked at 2126 MB, under the 3 GB ceiling. That was with a lead, a worker and a reviewer at a time, and nothing swapped. Peak load is the whole machine's, the Hive's own work included. The test exited FAIL only because `t.TempDir` couldn't remove each sub-Hive's read-only Go module cache; `settle` now makes those folders writable first (`TestSettleRemovesReadOnlyFolders`).

What each arm did:

- **Same shape, same time.** Lead files the ask as a fix (one brief), worker commits in about 3 minutes, its tests wait about 11 minutes behind the score run on main (`max_checks` 1), then tests, a reader and the security reader land it; the second brief is the automatic review follow-up, which the test stops. Of the 17 minutes, the queue is two thirds.
- **The `essay` worker built what was asked; the `standard` worker didn't.** `essay`: a `# hive-timeout: <duration>` line in the script, read by one `StepTimeout` that the warden's decision and the run both call, capped at 4h, bad values refused at decision, "timed out after X", the warden and tribunal prompts and `docs/risk.md` updated (79 lines). `standard`: `--timeout` flags on `hive agent warden` and `decide` instead of a line in the script (105 lines). Both readers, the security reader included, passed it. That is the same drift `chatty`'s worker made in real3: the brief's mechanism replaced by a nearby one, and review checking the code, not the code against the brief.
- **No hive, either way.** No messages at all, no `hive agent status`, no knowledge-base search, no notes, no feedback, no asks. With one worker at a time there is no peer to talk to, so this run can't show lateral talk; it shows only that neither wording makes a lone worker reach for the Hive's memory.

So on one real task, the person's essay with one-line roles did as well as the standard prompts, a little better in fidelity, at the same cost. One task and one run each is an anecdote, not a measurement: the next runs add the other arms one or two at a time, and a task that needs two workers at once is what can show whether any arm talks laterally.

### The other five arms (real3)

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

### `standard` against `essay`, first time (real2)

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

### How each arm behaves

**How each arm behaves, across the runs (real2, real3 and real4).** The same, nearly step for step. In seven arms, from the Hive's full prompts to one sentence, no agent sent another a message (0 in every arm, `chatty` included) and nobody asked past their reach, wrote a note or gave feedback: everything went person → lead → brief → worker → reviewer → seat, and improve → project lead → brief, with the chats as the only talk. So the soft layer, at any wording tried, doesn't make these agents behave as a hive on a task like this; the hard layer does the coordinating (the brief, the queue, the review schema, the merge path, improve's alerts), and the agents fill the slots it gives them. The one-line roles (`essay`, `essay-tools`, `philosophy`, `vague`) were as fast and as on-brief as the full ones; the off-brief designs (a `--timeout` flag in place of the script's line) came from the arms with the most words, `chatty` in real3 and `standard` in real4, and both passed review. Where the arms did differ, it was in judgement within a slot, not in shape: four of seven fixed the red main by loosening a production guard and two by changing tests only, and both kinds passed the security seat. One task and one run per arm can't tell those apart from chance. The baseline's peer talk (2 worker→worker messages in H0's 76) came from many workers on related tasks over a day, which one task with one worker at a time never sets up: a test of "behave as a hive" needs tasks that overlap.

### All seven at once (real1)

**First real run (2026-10-25, 13:16Z, all seven arms side by side, from 3d2020e):** no result. Within about 35 minutes the `essay` arm's sub-Hive daemon stopped answering (connection refused), and the test then lost its record to `t.Fatal`; the machine reached a load of about 600 and its memory ceiling, and the box froze the run. Hence the shape above: one arm at a time, capped inside, starting with `standard` against `essay`.

On the fake harness, which ignores its prompts, the runs show the pipeline only: every arm installs and is used (the test checks each run's system prompt), on the demo project an ask lands in about 30 seconds, and on the clone of the Hive's own source (`philosophy`, 2026-10-25) the default task went lead → worker → the clone's full `go vet` and short tests → review → merge in 50 seconds, with no messages. The first clone runs (`quiet`, `essay`) found the one hard-layer gap a sub-Hive of the Hive's source hits: the forge checks no local forge runs (step 4).
