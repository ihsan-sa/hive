# What the Hive forces on agents, what it costs, what can go

Task 261025-223750-76bb026d40, issue #891. The person (2026-10-25): "how much of this is infra/forced on agents ... we dont want to overcomplicate or add more parts than necessary"; "the more constraints etc we add while building this the worse"; hive behaviour "should come naturally, not forced".

Measured from this Hive's journal (`$HIVE_STATE/events.jsonl`, 2026-10-24 01:55Z to 2026-10-26 17:02Z, 63 hours, 103,840 events) and the run folders. Cost is Claude Code's `cost_usd` per turn (API-equivalent, not plan money; cumulative within a session, so taken as the difference). The scripts are in this run's box (`$HIVE_STATE/runs/w-261025-223750-76bb026d40/box/*.py`, each takes `events.jsonl`).

## Where the spend goes

| role | runs | turns | cost | share | per turn |
|---|---|---|---|---|---|
| worker | 414 | 1323 | $991 | 34% | $0.75 |
| lead | 3 | 888 | $894 | 31% | $1.01 |
| reviewer (reader + security) | 2795 | 2802 | $893 | 31% | $0.32 |
| sweep, warden, stepback, filer, judge | 241 | 227 | $95 | 3% | |

Two thirds of the spend is not workers. **481 of the lead's 888 turns ended `NOOP`** ($557, 19% of everything): it was woken by a notification that needed nothing. Merge notifications alone woke it 210 times and 164 of those were `NOOP`; "Done: merged as checkpoints" 36 of 39.

## Where the time goes

Of 304 finished tasks, median minutes per stage: **queued 175**, working 15, merging 12, testing 10, reviewing 2. Time-weighted over every queued task, the waits were:

| why a brief waited to start | task-hours | share |
|---|---|---|
| another brief names the same file (`queue.go` `areas`) | 1930 | 66% |
| held by an agent (`queue --hold`) | 457 | 16% |
| worker pace / `max_workers` | 455 | 16% |
| machine strain | 87 | 3% |

So the largest single cost on the loop is a rule, not a resource: briefs serialised by the file names in their text, before any code is written, though a conflict met at merge already goes back to its worker with main.

## What a new agent reads before it can act

System prompts as given to the latest run of each role (words): lead 2746, worker 2274, warden 2274, judge 1928, reviewer and security reader 1846, sweep 1764, filer 1592, stepback 1534. The orientation, in all of them, is 1272 words (1015 when the soft-layer experiment counted it two days ago; the essay is 2089). A worker's brief adds ~860 (its lessons section ~250), the orientation then points at `practice/*.md` (2080 words) and the essay. So a worker reads ~3100 words before its ask, and is pointed at 4000 more.

A **reviewer**, which can change nothing, gets the same 1272-word orientation on how to talk to the Hive, then a brief of ~4300 words: the ask, the account, `docs/REVIEW.md` (735), all of `.hive/goals.md` (1841) and `.hive/lessons.md` twice (in the brief's project section and again in full). That is 2795 runs.

`.hive/lessons.md` ("what was reverted and why") is now mostly the gate's rollback lines: 8 of its 13 lines, three of them "nothing moved beyond the noise", which teach nothing and go into every brief and every review.

On the soft-layer real run (docs/soft-layer.md, real4) the essay with one-line roles did as well as the standard prompts on the same task, a little better in fidelity, at the same cost.

## Every mechanism, what it was for, what it did, verdict

Fired = in the 63 hours above.

| mechanism | added for | fired / caught | cost | verdict |
|---|---|---|---|---|
| Notifications to the lead (merged, done, checkpoint, gate, stuck) | the lead knows what happened | 607 notifications; 481 lead turns `NOOP` | $557, the lead's context | **simplify**: only what needs a decision wakes it |
| File-overlap hold before start (`areas`, quick prompt lane) | two workers redoing each other (#399, #406) | 66% of queue time; caught: not measured, a conflict would have gone back to its worker anyway | 1930 task-hours, ~500 lines in `queue.go` and the lead's longest paragraph | **remove** |
| Review advice filed as a follow-up brief | advice that shouldn't hold a merge | 165 of 534 tasks (31%), 98 done | a worker, reviews, checks and a queue slot each | **remove**: advice stays on the PR |
| Recursive gate, 3 reps a side | a gain must be shown (person's goal) | 89 runs, 31 machine-hours, median 27 min; 18 keep, 50 rollback (≥30 "nothing moved"), 22 could not run; the judge overturned 4 of the 5 rollbacks it saw | 3 builds + 3 e2e per side per gain, most of the machine strain | **simplify**: one rep, more only when the first differs |
| Fix/gain aim | which changes the gate sees | 102 tasks set to fix, 3 to gain | an op, a paragraph in the lead's prompt, re-labelling to skip the gate | keep while the gate is; shrinks with it |
| Orientation in every role | one shared picture | every run | 1272 words × 3451 runs | **simplify**: cut to what `hive agent help` doesn't say; drop it for read-only seats |
| Goals + lessons twice in each review | reviewer sees the person's intent | 2795 runs | ~3000 words a review | **simplify**: REVIEW.md stays, the rest by path |
| Gate rollback lines in `lessons.md` | the next worker learns from a rollback | 8 of 13 lines | ~250 words in every brief and review | **remove**: lessons are reverts after merge |
| Stall watcher → lead | a task stuck in a stage | 145 stalls, 141 handed to the lead; 28 of 40 such lead turns `NOOP` | lead turns, alerts | **simplify**: retry itself, tell the lead once if that fails |
| Lead prompt procedures | the lead runs the queue, aims, models, holds | every lead turn | 2275 words | **simplify** after the removals above |
| Security reader (security-listed paths only) | changes that act outside the box | 690 reads, 17 fix: a sandbox escape via `nice` on macOS, a symlink the daemon follows, a worker moving its own bar, a credential leak | ~$270 | **keep** |
| Reader, one per head (two on high/root paths) | a second pair of eyes | 1919 reads, 186 fix | ~$620 | keep |
| Review rounds cap (3) | a change that never converges | 34 tasks stopped | work discarded, lead notification | keep; watch |
| Warden on protected-path merges and asks | person's settings and outside steps | 63 merges (59 yes), 62 asks (7 declined) | $26 | keep: cheap, and the person's rule |
| Finish fields (account, tested, unsure) | the PR description, the judge's input | every finish | three lines | keep |
| Score measured after every merge, revert below | the person's score | 232 measures, 59 reverting | one score run per merge | keep |
| Pace, strain holds, `max_*` ceilings, nice levels | the machine froze (#771) | 769 pace changes, 1992 strain holds; 19% of queue time | ~240 lines in `pace.go` | keep; falls with the gate's load. The re-journaling is filed (261026-000327) |
| Release checks and asks | a bad release reaching the person | 62 release failures, 51 asks of the person, 123 alerts | | in flight (261025-124109) |
| Journal per pace move | | `task.saved` is 50,612 of 103,840 events, 29,128 of them `queued` | disk, every reader of the journal | filed (261026-000327) |

## The removals, ranked by cost saved for protection lost

Each is filed as its own brief (below). The ones on top save the most and protect against nothing that isn't caught elsewhere.

1. **Notifications that need nothing don't wake the lead.** ~19% of spend; the feed and record already carry them.
2. **No file-overlap hold before a worker starts.** Two thirds of queue time; a conflict still goes back to its worker at merge.
3. **Review advice isn't filed as work.** A third of all tasks; the advice stays on the merged PR and a sweep can take what matters.
4. **Read-only seats get their role and the change, not the orientation and the whole goals and lessons.** About a third off each of 2795 runs.
5. **Gate rollbacks don't write lessons.** ~250 words off every brief and review; the gate's own record keeps them.
6. **The gate runs one rep a side, and more only when the first differs.** About two thirds of 31 machine-hours, and of the strain that holds workers.
7. **The stall watcher retries a stuck task itself before telling the lead.**
8. **The orientation says only what an agent can't find out.** 1272 words to about 500, measured on the soft-layer arms.
9. **The lead's prompt drops the procedure its tools already explain.** After 2, 3 and 6.

Not filed, for the person: whether a prose or prompt change taken as a gain needs the gate at all. Of the gate's rollbacks most were "nothing moved beyond the noise", which a prompt change can't move on the fake harness; the goals say a gain must show itself at the gate, so it is theirs to change.

In flight and adding parts: PR #904 ("a problems board ... carried into every brief") puts more words into every brief, the cost this audit measures; weigh it against 4 and 8.
