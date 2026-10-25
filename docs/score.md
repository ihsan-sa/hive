# The Hive's score

The Hive project (ihsan-sa/hive) has a score, so the improve module measures main after every merge, keeps or reverts against the best kept score, and the step-back (`improve.stepback`) has a number to beat. It is set in `.hive/project.json`:

```json
"score": {"command": "for t in git python3; do command -v \"$t\" >/dev/null || { \"$t\"; exit 127; }; done; go run ./cmd/hive-score", "direction": "higher", "tolerance": 3, "paths": ["internal/e2e/benchmark_test.go", "internal/demo/"]}
```

Code: `cmd/hive-score` (output format tested in `main_test.go`).

## What it measures

`hive-score` compiles `internal/e2e` once, then runs on the fake harness, one after the other:

1. **The benchmark**: `TestBenchmark` with `HIVE_BENCH=4`, the same test the recursive gate judges with (docs/gate.md): four asks on the demo textkit project, judged from what landed on textkit's main, on inputs drawn fresh for the run, with answers worked out by the test, not from what the Hive says. Its BENCH line is signed with a key `hive-score` hands it on stdin, and a line that doesn't verify counts as no line.
2. **The rest of the e2e suite** (`TestBenchmark` skipped). A test that fails is run once more on its own; a pass then counts as a pass (and is reported as flaky), since improve files flakes as work of their own. A test that never reports (a panic, the suite's timeout) counts as failed; skipped tests (the live probes, tmux) are left out.

It needs `go`, `git` and `python3` (the benchmark runs textkit with it). The command looks `git` and `python3` up in the shell before `go run` (a missing `go` fails the shell's own lookup), and runs a missing one so the shell prints its own `sh: 1: <tool>: not found` and the command exits 127: improve reads that as "can't run", not a score. The lookup has to be in the command: `go run` exits 1 whatever its program exits with, so a 127 from inside `hive-score` would never reach improve (`TestMissingToolCantRun` runs the command from project.json without python3 and checks `core.MissingTool` names it).

## How it combines

```
score = 100 × e2e pass rate
      + 10 × benchmark score          (textkit's 6 functions right on fresh inputs)
      + 10 × asks finished            (the 4 asked ones among them)
      − benchmark seconds / 6
```

The best is 200 less the time. Correctness dominates: one more function right (+10 or +20) outweighs a minute saved, and one e2e test of 40 is 2.5 points. A benchmark that prints no valid BENCH line scores 0 on both its terms and is charged its wall time. The output is the parts, one line each, and the number alone on the last line:

```
benchmark: 6/6 functions right, 4/4 asks finished, 55.8s
e2e: 40/40 passed (0 only on a second run)
190.70
```

**Cost** isn't in it yet. The benchmark's `cost_usd_reported` is the Hive's own count, which the gate doesn't judge either, and on the fake harness it is 0. Once cost is counted from outside (the gate's meter counts egress bytes; nothing meters a run under improve yet), it goes in as a penalty like time, below correctness.

## What moves it, and what doesn't

On the fake harness the fake worker writes the asked function, so the benchmark's score is 6/6 and asks 4/4 unless the pipeline breaks (an ask that never lands, a merge that goes wrong). What moves day to day is the **benchmark's seconds**: how fast the Hive takes four asks from brief to merged on main. A real harness would measure how good the work is, but costs plan usage and hours, so the score doesn't use one; the gate's `HIVE_E2E_HARNESS=claude` is the place for that.

So on this score "beats your benchmark" means: the Hive gets the same asks right in less time, or gets more of its own suite passing. A change that makes it no faster and breaks nothing is a tie, kept within the tolerance.

## Time and noise

On this machine (20 cores), 2026-10-24, six runs at the Hive of main fa3376e: 190.70, 190.63, 190.63, 190.74, 190.65 and 190.68 (the second alongside `go vet ./... && go test -short ./...`, the fourth alongside a second whole e2e suite). The benchmark took 55.6-56.2 s each time (the fake harness paces its turns, so its seconds barely move) and all 40 e2e tests passed every time. A run takes **about 5 minutes**: under a minute to build, about 1 minute for the benchmark and 4 for the suite. Its bounds are 7 minutes for the benchmark, 10 for the suite and 6 for re-running what failed, so with the build it stays inside the check's own 30; a test cut off by a bound counts as failed.

The tolerance (3) is far above that spread, and above one e2e test failing twice in a row (2.5), so a merge that changes nothing is kept rather than reverted on a slow run. A real gain in time needs the benchmark to be 18 s faster. Two things add noise: other checks sharing the machine (`max_checks`), which slow the benchmark and can flake a timing-bound e2e test (the second run absorbs most of it), and the fake harness's own pacing.

## First value on main

190.70 (then 190.63, 190.63, 190.74, 190.65, 190.68), measured by hand on this change's branch, whose Hive is main at fa3376e. improve records the kept baseline itself when it measures the merge of this change.

## Evaluator paths

`./cmd/hive-score` (from the command), `internal/e2e/benchmark_test.go` (the asks and how they are judged) and `internal/demo/` (textkit, what the asks work on) are the score's evaluator: a change to them is the person's to merge and starts a new baseline. The rest of `internal/e2e` isn't listed, since every change to the suite would otherwise wait on the person. That leaves one way to raise the pass rate without fixing anything, deleting a failing test; the review and the gate (which grades every candidate with the base's suite) are what catch it.

## What a score changes for the Hive's own work

- **Briefs.** The lead briefs a scored project with the person's goal as written, no `--done` bar, limit or steps of its own (modules/agents/lead.md): the score is the bar. Workers on this project see the score command, its tolerance and the best kept value in their brief, and that a lower score is reverted.
- **Checkpoints.** Every worker commit on a scored project is extracted and scored; one that beats the best kept score by more than the tolerance merges as a checkpoint while the worker goes on. Here each of those is a full score run (about five minutes) in a check slot, beside the merges' tests.
- **Step-backs.** `improve.stepback` now runs on this project when the best kept score stalls: a minimal setup tries the same goals in a fresh copy, scored with this command, and a win is filed as a fault in the Hive's own setup.
- **Reverts.** A merge that scores below the best kept by more than the tolerance is reverted, with the reason in `.hive/lessons.md`. A merge that changes the evaluator starts a new baseline instead.
