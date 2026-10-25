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

**First real run (2026-10-25, 13:16Z, all seven arms side by side, from 3d2020e):** no result. Within about 35 minutes the `essay` arm's sub-Hive daemon stopped answering (connection refused), and the test then lost its record to `t.Fatal`; the machine reached a load of about 600 and its memory ceiling, and the box froze the run. Hence the shape above: one arm at a time, capped inside, starting with `standard` against `essay`.

Not run on real agents yet: a sub-Hive needs the host's Claude login, which an agent's sandbox doesn't hold (`claude -p` there says "Not logged in"), so the real run goes to a warden on the host, from main once this lands (follow-up brief filed with this change).

On the fake harness, which ignores its prompts, the runs show the pipeline only: every arm installs and is used (the test checks each run's system prompt), on the demo project an ask lands in about 30 seconds, and on the clone of the Hive's own source (`philosophy`, 2026-10-25) the default task went lead → worker → the clone's full `go vet` and short tests → review → merge in 50 seconds, with no messages. The first clone runs (`quiet`, `essay`) found the one hard-layer gap a sub-Hive of the Hive's source hits: the forge checks no local forge runs (step 4).
