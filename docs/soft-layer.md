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

| `open` | a shared space: no one assigns work, say what you take in the room, find what most needs doing from the goals; then the standard "Where things are", "Your reach" and command list | lead, worker, sweep: one line ("You are one of the agents in the space."); reviewer, warden, tribunal, judge: the standard prompts |

From lots of comms (`chatty`) to none (`quiet`), and from very specific (`standard`) to almost nothing (`vague`). `essay` is the person's suggestion as given; `essay-tools` separates "the essay alone doesn't say how to call the Hive" from "the essay doesn't make agents behave as a hive". The structured parts stay whatever the arm: a reviewer still has to answer `land` or `fix` in its schema, and the harness still gives every agent `hive agent` on its PATH.

## How an arm runs

`TestSoftLayer` (`internal/e2e/softlayer_test.go`), per arm, one arm at a time (see *Fitting the machine*):

1. A fresh Hive (its own workspace and local forge), its agents module replaced by the arm's (written with a source record that isn't `builtin`, so the default install never restores the standard prompts; `TestInstallSurvivesTheDefaults`).
2. This Hive's state: its committed source as the project `hive` (with `.hive/goals.md`, `lessons.md` and `project.json`, so the same tests and score), and, with `HIVE_SOFT_KB=<kb>`, the knowledge base's notes and `practice/` (not `record/`).
3. One real task, in the lead's chat: by default an open fix from this Hive's queue (261024-213942, the gate's unequal TMPDIRs that failed `TestMeter` on the candidate alone); `HIVE_SOFT_TASK` gives another.
4. A local forge has no CI, so the test stands in for the forge checks main's `tests/LANDING-security.toml` requires (`test (ubuntu-latest)`, `sandboxed-e2e`), reporting them passed on each open PR's head. Without that, the first fake run showed, every arm waits at "waiting for the forge's checks" forever. Editing the clone's list instead breaks its own `TestRequiredChecks`, so the sub-Hive's tests would be red on main. The sub-Hive's own tests, reads and score still decide.
5. Wait until a change lands on its main or `HIVE_SOFT_WAIT` runs out (90 minutes on a real harness), then observe.

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

- **One arm at a time.** `HIVE_SOFT_PARALLEL=2` runs two, each on its own 4 CPUs (the first and the second four the test may use); more isn't allowed. An arm's slot is freed only after its daemon, what it left running and its folder are gone, so the next arm starts on a quiet machine. Seven arms one by one at up to 90 minutes each is a long run: start with `standard,essay` and add arms in later runs.
- **Capped inside.** Each sub-Hive has `max_workers` 1, `max_checks` 1 and `max_gates` 1, saved as the person's ceilings so its pace can't raise them. Its daemon is started pinned to 4 of the CPUs the test may use (`softCPUs`; under a cpuset, those it allows, not CPUs 0-3), and CPU affinity passes to everything it starts, sandboxed checks included; Go sizes `GOMAXPROCS`, `go build -p` and `go test`'s parallelism to it, so no arm builds on more than 4 cores whatever its checks' environment. Where a check does get the daemon's environment (a bare sandbox), it also has `GOFLAGS=-p=2` and `GOMAXPROCS=4`.
- **Under a memory ceiling of its own.** On Linux a real run (`HIVE_E2E_HARNESS` set) refuses to start unless its cgroup, or one above it, has a `memory.max` of at most 4 GiB (the smallest on the way up counts, so a parent scope's ceiling does too): run it under a `systemd-run --user --scope` as above, with `MemorySwapMax=0`, so a run that outgrows its ceiling ends inside it, without the machine swapping and without touching the Hive's own runs. `HIVE_SOFT_UNBOUNDED=1` skips the check.
- **Measured.** Each arm's row in the table has its peak memory (the summed RSS of the test's processes and their descendants, sampled every 2 seconds) and the machine's highest 1-minute load while it ran; under a cgroup the table also gives the cgroup's own `memory.peak`. With `HIVE_SOFT_PARALLEL=2` an arm's peak MB includes the other arm's processes while both ran (the test adopts both daemons' orphans, so it can't tell them apart), and the table says so; the cgroup's peak is the pair's.

Not shared: the Go build and module caches. Each check builds in a home of its own that is removed after it (`internal/core/disk.go`), and each sub-Hive keeps its own module cache, which its own prune may empty, so a cache shared across sub-Hives could be emptied under another's check. With one arm at a time the cost is one module download per arm.

The dry run on the fake harness (2026-10-25, `HIVE_SOFT=standard,essay` on the clone of the Hive's own source, the default task): the arms ran one after the other, each went lead → worker → the clone's `go vet` and short tests → review → merge in about 3 minutes, and the test's processes peaked at 341 MB (`standard`) and 484 MB (`essay`). The peak load, 39 and 37, is the machine's, other Hive work included. Real agents add their harness processes (a lead, a worker and a reviewer at a time with these ceilings), which is what the 3 GB ceiling leaves room for.


## The open arm

The person (2026-10-28 08:56 local): "Thats the whole point of putting all the agents in one place like the hive all able to talk to one another and then letting them do unstructured work", and "stuff is far too rigid still like eerything needs to be dynamic and adapt". Task 261028-125710-1df12ead4d, issue #1905. The other arms change only the words inside today's pipeline: one ask, the lead files it, one worker builds it. `open` changes the shape as well, and keeps only the safety layer:

- **No lead, no task.** The test doesn't post an ask in the lead's chat. It files `HIVE_SOFT_OPEN_AGENTS` briefs (3 by default, 2 to 4) straight into the queue, the same open brief each ("agent i of n started at once in one shared space; nobody has assigned work; find with the others what most needs doing for this project's goals and get it landed", `softlayer.OpenBrief`), and the sub-Hive's `max_workers` is that many, so they all start at once. `max_checks` and `max_gates` stay 1.
- **One room.** `hive agent message --to room --text "..."` reaches every other live lead, worker and sweep on the sender's project at once (the chat module's router; `chat.Room`), each as an ordinary message marked "(to the room)", so each is in the journal as `msg.sent`. The read-only and safety seats (reviewer, warden, tribunal, judge) aren't in it. It works in any Hive, but only the open arm's prompts mention it.
- **Only the safety layer as structure.** The sandbox, the reach rules (warden, tribunal, the person; autonomy 1 unless `HIVE_SOFT_WARDENS=1`), and the merge path: tests, reads and keep-or-revert. So the reviewer, warden, tribunal and judge keep their standard prompts; the roles that do the work get one line, and the orientation tells them what holds and that everything else, how the space is organised included, is theirs to change.
- **Ends** when every open brief's task has merged, blocked or stopped, or at `HIVE_SOFT_WAIT`: "k of n agents' work landed". The table's `room` column counts posts to the room once each; each hearer's copy is also in `messages` and, worker to worker, in `peer`.

```
# on the fake harness: the shape (3 workers at once, no lead, 3 merges in about 10 seconds on the demo project, 2026-10-28)
HIVE_SOFT=open HIVE_SOFT_STATE=demo go test ./internal/e2e -run 'TestSoftLayer$' -v
# real agents: three harness processes beside the checks, so a 4G ceiling
systemd-run --user --scope -p MemoryHigh=3500M -p MemoryMax=4G -p MemorySwapMax=0 -p CPUWeight=20 -p IOWeight=20 \
  env HIVE_SOFT=standard,open HIVE_E2E_HARNESS=claude HIVE_SOFT_KB=~/hive/.hive/kb HIVE_SOFT_OUT=~/hive/.hive/soft-layer/<run> \
  go test ./internal/e2e -run 'TestSoftLayer$' -timeout 5h -v
```

What it can show against `standard`: whether agents left to organise themselves talk laterally (room posts, peer messages) where a shared object is in flight, whether they split the work or collide on it, and whether what lands is worth as much as one routed fix, at what cost in turns. The real run is still to come.

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

On the fake harness, which ignores its prompts, the runs show the pipeline only: every arm installs and is used (the test checks each run's system prompt), on the demo project an ask lands in about 30 seconds, and on the clone of the Hive's own source (`philosophy`, 2026-10-25) the default task went lead → worker → the clone's full `go vet` and short tests → review → merge in 50 seconds, with no messages. The first clone runs (`quiet`, `essay`) found the one hard-layer gap a sub-Hive of the Hive's source hits: the forge checks no local forge runs (step 4).

### `standard` against `open` (2026-10-28, open and open2)

Task 261028-131448-55adf03f90, issue #1907. Both runs use the same source: main 0788626 merged with the open arm's PR (#1908, head b47627e), not yet merged when the runs started. They run as detached units under `MemoryHigh=3500M`, `MemoryMax=4G`, `MemorySwapMax=0`, `CPUWeight=20`, `IOWeight=20`, `Nice=10`.

- **open** (request 261028-133249-fff2ec5933, unit `hive-soft-261028-open`, 13:33Z) ran no agents. The machine's own Claude login had been at its weekly limit since 2026-10-26 (it resets 2026-10-31 17:00Z). The `standard` arm's lead got a 429 on its first turn, was parked, and the arm idled its 90 minutes ("not landed: no task was filed"); `open` did the same after it. So an arm now ends at once when its sub-Hive's journal has a login parked past the arm's deadline: `not run: login … is at its usage limit until …` (`limitedPast`, `TestSoftLimitedPast`). A limit that resets inside the wait is still waited out.
- **open2** (request 261028-153503-04dfe6c7cd, unit `hive-soft-261028-open2`) was to point the sub-Hives at the `cc0` login (`CLAUDE_CONFIG_DIR`), the only one with room then, at 14% of its week. `cc0` also carries the main Hive's workers, so the acting lead bounded the run: a watchdog would read `cc0`'s weekly use from the Hive's journal every 5 minutes and stop the unit past 30%. It never ran an agent either. At 16:35Z its first step, a one-turn check that the login works, failed with `claude: not found`. A `systemd-run --user` unit starts with the user manager's PATH, which has no `~/.local/bin`, and the `PATH` the warden's step exported doesn't reach the script the unit runs. The daemon would have found `claude` anyway (`binDirs`), but the check ran before it. So the machine's reboot at about 18:45Z cut nothing short. A unit's script exports its PATH and resolves `claude`'s absolute path in the step, which fails at once if it's missing.
- **open3** (request 261029-110257-2fd6b04a85, unit `hive-soft-261029-open3`) runs from main d41cd377, which has the open arm merged (dc3fe6b). By then `cc0` was at 51% of its week, so the run uses the `sasan@salari` login, at 5% after its reset, with the same 30% watchdog. It also waits for a 5-minute load under 40, since the machine was at about 100 and swapping after the reboot. Output: `~/hive/.hive/soft-layer/261029-open3/`.
