# The judge

A model's judgement decides whether a change is kept; the numbers are one thing it weighs. The person, 2026-10-24: "the scoring needs to be a data point into a judgement call from the model cuz some things might not result in increase in score but will def help". Code: `internal/core/judge.go` (the record, the model pick), `internal/mod/work/judge.go` (the run, the gate's judge), `internal/mod/improve/judge.go` (the judge after a merge); the role prompt is `modules/agents/judge.md`.

## Where it decides

- **The recursive gate** (a change to the Hive aimed at a gain, docs/gate.md). Once the gate has measured both sides (`report.json`'s `measured`, `GateResult.Measured`), keep or rollback, the judge reads the change's brief and its worker's account, the diff, and the gate's whole report, and decides. Its keep merges a change the numbers called "not better"; its rollback sends back one the numbers kept. The merge step (`Effects.Merge`, via `Effects.Kept`) takes the judge's newest call on that exact head and main over the gate's outcome. A candidate that failed before it was measured (it doesn't build, fails its own vet or short tests, rewrote its binary) is rolled back as the gate said, and a gate that couldn't run still waits: there is nothing for a judge to weigh.
- **After a merge** (every project, improve module). When the tests stay green but the score falls past its tolerance, the judge reads the change, its brief and account, and the score with its last 20 measurements on main, and decides whether main keeps the merge or reverts it. A keep records the measurement as kept and moves the baseline to this score, since main now stands there (unless a later merge was measured meanwhile, whose baseline stands). While one merge is with its judge, later merges of the project wait to be measured against the baseline it settles, so the same fall isn't judged twice; a judge run that takes over 3 hours is stopped before the next is asked. Red tests, a score that stopped producing a number, and a revert that left main worse go as before, unjudged: those are not the score.

A rise in the score after a merge needs no judgement: the change already passed its reads.

## How it runs

The judge is a read-only run of role `judge` on a clean checkout of the change, with `brief.md` (what it decides, what the numbers alone say, the change's brief and account, and a token), `diff.patch` and `numbers.md` in `$HIVE_RUN`. Its answer is structured (`core.JudgeSchema`): `verdict` keep or rollback, `summary`, and the token. The daemon records it (`judge.decided` in the journal) only from a read-only judge run it started on exactly that change (`Effects.RecordJudgement`), and says it on the PR. The run is named by the change (`j-<task>-<head>-<main>-<try>`, `j-<task>-<merge>-<try>`), so a restart finds it rather than starting another. A judge that gives no usable answer three times (`work.JudgeTries`) leaves the numbers to decide, and the task's log says so.

**Its model** (`Config.JudgePick`): a `judge` route the person sets comes first, then the reviewer's model, then the tribunal seats, taking the first that isn't the model the change's worker ran on, so a change isn't judged by its author. When every choice is the author's model it runs there and raises an alert. As a check, it doesn't move along the fallback list at a usage limit unless the person routes it.

**Lessons.** The daemon runs the gate with `--judged`, so the gate writes no lesson for a measured rollback; the judge's rollback writes it, in the gate's own words (never the judge's summary, which read the candidate's diff). A measured keep the judge rolled back goes to the worker with the judge's reasons.

Under the fake harness the judge goes with the numbers, unless the change asks `FAKE:judgekeep` or `FAKE:judgerollback`.
