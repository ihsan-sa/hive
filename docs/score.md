# The Hive's score

The Hive project (ihsan-sa/hive) has a score, so the improve module measures main after every merge, keeps or reverts against the best kept score, and the step-back (`improve.stepback`) has a number to beat. It is set in `.hive/project.json`:

```json
"score": {"command": "for t in git python3; do command -v \"$t\" >/dev/null || { \"$t\"; exit 127; }; done; go run ./cmd/hive-score", "direction": "higher", "tolerance": 3, "paths": ["cmd/hive-score/", "internal/e2e/benchmark_test.go", "internal/demo/"]}
```

Code: `cmd/hive-score` (output format tested in `main_test.go`). It is in the score's `paths` with the benchmark and the demo project, so a change to the formula alone starts a new baseline instead of reading as a gain or a loss.

## What it measures

`hive-score` compiles `internal/e2e` once, then runs on the fake harness, one after the other:

1. **The benchmark, six times**: `TestBenchmark` with `HIVE_BENCH=4`, the same test the recursive gate judges with (docs/gate.md): four asks on the demo textkit project, judged from what landed on textkit's main, on inputs drawn fresh for the run, with answers worked out by the test, not from what the Hive says. Three runs ask one by one, as the gate does; three ask **all at once** (`HIVE_BENCH_AT_ONCE=1`), as work reaches a real Hive. The at-once runs are the loop itself: how soon queued briefs start, how many work side by side, and how four branches that all edit `textkit/__init__.py` land on a main that moves under them. Each BENCH line is signed with a key `hive-score` hands it on stdin, and a line that doesn't verify counts as no line. The test looks every tenth of a second for an ask to end, so its seconds are the Hive's, not its own polling.
2. **The rest of the e2e suite** (`TestBenchmark` skipped). A test that fails is run once more, in a process of its own so one that panics can't fail its neighbours; a pass then counts as a pass (and is reported as flaky), since improve files flakes as work of their own. A test that never reports (a panic, the suite's timeout) counts as failed; skipped tests (the live probes, tmux) are left out. A test the parent commit (`HEAD^`) declares in `internal/e2e` and the candidate doesn't counts as failed (reported as `(deleted)`), so deleting a failing test can't raise the pass rate. If the suite's test list can't be read, `hive-score` exits 1 rather than print 0/0 as a score.

It needs `go`, `git` and `python3` (the benchmark runs textkit with it). The command looks `git` and `python3` up in the shell before `go run` (a missing `go` fails the shell's own lookup), and runs a missing one so the shell prints its own `sh: 1: <tool>: not found` and the command exits 127: improve reads that as "can't run", not a score. The lookup has to be in the command: `go run` exits 1 whatever its program exits with, so a 127 from inside `hive-score` would never reach improve (`TestMissingToolCantRun` runs the command from project.json without python3 and checks `core.MissingTool` names it).

## How it combines

```
score = 100 × e2e pass rate
      + 10 × benchmark score          (textkit's 6 functions right on fresh inputs, averaged over the six runs)
      + 10 × asks finished            (the 4 asked ones among them, averaged likewise)
      + 15 × log2(180 / seconds)      (seconds: the six runs together)
```

The speed term is on a log scale: every halving of the loop's time is worth 15 points however fast the Hive already is, and a seventh faster (13%) is about 3, the tolerance. When any run misses an ask (or prints no valid BENCH line), every run is charged its whole bound (2 minutes), the slowest a run can be, so landing the work always outweighs doing it faster. One e2e test of 43 is 2.3 points. 180 s is about three times what the six runs took when this started; where it sits moves every score alike. The output is the parts, one line each, and the number alone on the last line:

```
benchmark, one by one: 6/6 functions right, 4/4 asks finished, 11.4s
benchmark, all at once: 6/6 functions right, 4/4 asks finished, 6.8s
...
e2e: 43/43 passed (0 only on a second run)
225.70
```

**Cost** isn't in it yet. The benchmark's `cost_usd_reported` is the Hive's own count, which the gate doesn't judge either, and on the fake harness it is 0. Once cost is counted from outside (the gate's meter counts egress bytes; nothing meters a run under improve yet), it goes in as a penalty like time, below correctness.

## Why it changed (issue #673)

Until 2026-10-25 the score was `100 × pass rate + 10 × score + 10 × asks − seconds / 6`, with one benchmark run, asked one by one. About 30 measured merges in a row, big loop fixes among them (#470, #534, #571), scored 190.62-190.72: the only term that moved was the benchmark's seconds, at a point for every 6 s, and asked one by one the benchmark never had two briefs in the queue at once, so a change to how the loop queues, runs side by side or lands couldn't show. Once the loop woke on journal events (the milestone change, 79f3b11) the benchmark took 11.6 s and the time term had 2 points left in it, under the tolerance: no further speed-up could ever be kept as a gain.

Measured on this machine (20 cores), 2026-10-25, with this score:

| Hive | one by one | all at once | e2e | score |
|---|---|---|---|---|
| main at 46b24b0 (after the milestone) | 10.5-13.5 s | 6.4-8.3 s | 43/43 | 224.29, 224.74, 225.70, 225.50 (old score: 198.07) |
| before the milestone (f6b3f0a) | 56.3 s | 30.1 s | 43/43 | 192.1, worked out from those seconds (measured with two runs of each; old score: about 190.7) |

So the milestone's change is about +33 here, against +7 on the old score, and the next halving of the loop's time is worth another 15, where on the old score it was worth under 1.

Found while making it: the at-once runs took the same time with one worker as with four (7.0-7.6 s with `defaultWorkers` set to 1). All four asks name `textkit/__init__.py`, and the queue held a brief that names a file another working brief names until that one had pushed, so they ran one at a time whatever the pace allowed. Since issue #727 the queue lets briefs on one file start side by side when each names its own functions in it (`apart` in internal/mod/work/queue.go), and the at-once runs take 4.3-4.4 s (score 228.83 against 198.08).

## What moves it, and what doesn't

On the fake harness the fake worker writes the asked function, so the functions are 6/6 and asks 4/4 unless the pipeline breaks (an ask that never lands, a merge that goes wrong, which costs every run its bound). What moves day to day is the **loop's seconds**: how fast the Hive takes four asks from brief to merged on main, one by one and all at once. A real harness would measure how good the work is, but costs plan usage and hours, so the score doesn't use one; the gate's `HIVE_E2E_HARNESS=claude` is the place for that.

So on this score "beats your benchmark" means: the Hive gets the same asks right in about a seventh less time, or gets more of its own suite passing. A change that makes it no faster and breaks nothing is a tie, kept within the tolerance. The score is one data point for a judgement (docs/judge.md), not the judgement itself.

## Time and noise

On this machine (20 cores), 2026-10-25, four runs at main 46b24b0 while the Hive's own work kept the load at 20-44: 224.74, 224.29, 225.70 and 225.50, a spread of 1.4, about half the tolerance. With two runs of each way of asking instead of three the spread was 2.8 over six runs, too close to the tolerance, so it is three.

A run takes **about 3 minutes**: under a minute to build, about 1.5 for the six benchmark runs and 2 for the suite. Each benchmark run is bounded at 2 minutes, the suite at 10 and the re-run of what failed at 6, so with the build it stays inside the check's own 30; a test cut off by a bound counts as failed.

The at-once runs vary most (6.5-8.6 s for one run): the queue and the loop's ticks land differently each time. Three runs of each way of asking, added up, keep that under the tolerance. Other checks sharing the machine (`max_checks`) slow every run and can flake a timing-bound e2e test (the second run absorbs most of it).

## First value on main

The first score improve measured, inside the daemon's check sandbox, was **190.71** at a521c3a (2026-10-25 04:15 UTC), against 190.70 measured by hand on the branch: python3, git and the e2e suite run there as they do bare. The step-back then ran against it (best 198.08 by 12:27 UTC that day), so it has a number to beat. The first measure of the merge of the current score (c297e7a) starts a new baseline (it changes the evaluator), at about 225 where the old score had 198.

## Evaluator paths

`./cmd/hive-score` (from the command), `internal/e2e/benchmark_test.go` (the asks and how they are judged) and `internal/demo/` (textkit, what the asks work on) are the score's evaluator: a change to them is the person's to merge and starts a new baseline. The rest of `internal/e2e` isn't listed, since every change to the suite would otherwise wait on the person. A deleted test counts as failed against the parent's list, so a test removed on purpose costs 2.3 points once (under the tolerance), and two at once are a revert unless the person merges it. What the score itself doesn't catch, and the review must:

- **Skipping a failing test** (`t.Skip`, a build tag): skipped tests are left out of the rate.
- **The fake agent's own time.** What `internal/fakeagent` does in a turn (none of its sleeps are on the benchmark's path today) is part of the benchmark's seconds, and it isn't an evaluator path, since the rest of the suite drives it. A change that makes the fake worker cut corners or wait less makes the fake work quicker, not the Hive, and would beat the benchmark; review reads a change there as touching the score.

## What a score changes for the Hive's own work

- **Briefs.** The lead briefs a scored project with the person's goal as written, no `--done` bar, limit or steps of its own (modules/agents/lead.md): the score is the bar. Workers on this project see the score command, its tolerance and the best kept value in their brief, and that a lower score is reverted.
- **Checkpoints.** Every worker commit on a scored project is extracted and scored; one that beats the best kept score by more than the tolerance merges as a checkpoint while the worker goes on. Here each of those is a full score run (about five minutes) in a check slot, beside the merges' tests.
- **Step-backs.** `improve.stepback` now runs on this project when the best kept score stalls: a minimal setup tries the same goals in a fresh copy, scored with this command, and a win is filed as a fault in the Hive's own setup. The fault's worker gets the setup's transcripts and its commits as a patch; on this project those commits are the change that won, so the fault asks for them forward-ported onto main first.
- **Reverts.** A merge that scores below the best kept by more than the tolerance is reverted, with the reason in `.hive/lessons.md`. A merge that changes the evaluator starts a new baseline instead.
