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

`TestSoftLayer` (`internal/e2e/softlayer_test.go`), one arm at a time (`HIVE_SOFT_PARALLEL=2` for two at once, never more):

1. A fresh Hive (its own workspace and local forge), its agents module replaced by the arm's (written with a source record that isn't `builtin`, so the default install never restores the standard prompts; `TestInstallSurvivesTheDefaults`).
2. This Hive's state: its committed source as the project `hive` (with `.hive/goals.md`, `lessons.md` and `project.json`, so the same tests and score), and, with `HIVE_SOFT_KB=<kb>`, the knowledge base's notes and `practice/` (not `record/`).
3. One real task, in the lead's chat: by default an open fix from this Hive's queue (261024-192651, issue #460: an approved warden step can say it needs longer than 10 minutes); `HIVE_SOFT_TASK` gives another. The first default, the gate's unequal TMPDIRs, landed as 4425272 before any real run.
4. A local forge has no CI, so the test stands in for the forge checks main's `tests/LANDING-security.toml` requires (`test (ubuntu-latest)`, `sandboxed-e2e`), reporting them passed on each open PR's head. Without that, the first fake run showed, every arm waits at "waiting for the forge's checks" forever. Editing the clone's list instead breaks its own `TestRequiredChecks`, so the sub-Hive's tests would be red on main. The sub-Hive's own tests, reads and score still decide.
5. Each sub-Hive has `max_workers` 1 and `max_checks` 1, and the clone's test and score commands start with `GOMAXPROCS=4` and `GOFLAGS=-p=2` (a check's sandbox gets a fixed environment, so the cap goes in the command). The first real run, all seven arms side by side on 2026-10-25, took the machine to a load of about 600 and its 8 GB memory ceiling, and the box froze it. An arm's daemon and turns are stopped when its subtest ends, before the next arm starts; one whose daemon stops answering is reported so, with its journal and daemon log kept. The arms don't share a Go build or module cache: each check gets a home of its own and each sub-Hive its own module cache, and sharing them would take a core change; one arm at a time keeps that cost serial.
6. Wait until a change lands on its main or `HIVE_SOFT_WAIT` runs out (90 minutes on a real harness), then observe.

The test checks that every run the sub-Hive made started from the arm's orientation, and fails only if an arm can't be set up; it judges nothing. Each arm's journal, chats, runs, `main.log` and `report.json` go in `HIVE_SOFT_OUT/<arm>/`, and the table in `HIVE_SOFT_OUT/table.md`.

**Safety.** An arm's warden may have been told only "You are a warden.", and an approved warden script runs on the machine outside any sandbox. So unless `HIVE_SOFT_WARDENS=1`, each sub-Hive runs at autonomy 1: every step past an agent's reach is asked of the person (this test, which never answers) first, so no sub-Hive script runs. The asks are still counted and kept, which is what shows whether an arm's agents knew to ask.

```
# the pipeline, on the fake harness (prompts don't steer the fake agent)
HIVE_SOFT=standard,vague HIVE_SOFT_STATE=demo go test ./internal/e2e -run TestSoftLayer -v
# the experiment: real agents, plan usage, on a host with a Claude login
HIVE_SOFT=standard,essay HIVE_E2E_HARNESS=claude HIVE_SOFT_KB=~/hive/.hive/kb HIVE_SOFT_OUT=~/hive/.hive/soft-layer/<run> \
  go test ./internal/e2e -run TestSoftLayer -timeout 3h -v
```

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

How each behaves, so far: the same, step for step. No agent sent another a message in either arm (0 messages, 0 peer); everything went lead → brief → worker → reviewer → seat, with the chat as the only talk. Nobody asked past their reach (0 asks), wrote a note or gave feedback. The essay alone, with one-line roles, gave a lead, worker and reviewer as quick and as on-brief as the standard prompts, and the narrower fix of a red main. On this one task the soft layer barely shows: the hard layer (the brief, the review schema, the merge path) sets the shape. Whether a vaguer arm or `chatty` changes that is the next run's question.

**First real run (2026-10-25, 13:16Z, all seven arms side by side, from 3d2020e):** no result. Within about 35 minutes the `essay` arm's sub-Hive daemon stopped answering (connection refused), and the test then lost its record to `t.Fatal`; the machine reached a load of about 600 and its memory ceiling, and the box froze the run. Hence the shape above: one arm at a time, capped inside, starting with `standard` against `essay`.

A sub-Hive needs the host's Claude login, which an agent's sandbox doesn't hold, so the real runs are a warden's step on the host. A step is ended after 10 minutes (`risk.ExecTimeout`, until #460 lands), so the step only starts the run, detached in a systemd user unit.

On the fake harness, which ignores its prompts, the runs show the pipeline only: every arm installs and is used (the test checks each run's system prompt), on the demo project an ask lands in about 30 seconds, and on the clone of the Hive's own source (`philosophy`, 2026-10-25) the default task went lead → worker → the clone's full `go vet` and short tests → review → merge in 50 seconds, with no messages. The first clone runs (`quiet`, `essay`) found the one hard-layer gap a sub-Hive of the Hive's source hits: the forge checks no local forge runs (step 4).
