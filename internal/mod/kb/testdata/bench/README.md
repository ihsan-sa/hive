# The search's benchmark

Real questions about what this Hive did, each with the knowledge base files that answer it (`expect`) and others that bear on it (`also`), checked by hand against the record by agents that never saw this search. Taken from the earlier attempt at issue #270 (task 261024-074959-0cdbbee20e): `questions.json`, 14 from the night of 2026-10-24, among them the person's three ("why was #111 reverted", "what blocks the gate", "which merges skipped a security read"); `heldout.json`, 10 more on other topics.

`HIVE_KB_BENCH=$HIVE_KB go test ./internal/mod/kb -run TestSearchBenchmark -v` scores the top 10 files a question returns: recall (answer files among them), precision (share of them that answer or bear on it), MRR (one over the first answer's rank), distinct (share that are a file not shown already).

Measured 2026-10-26 on the live knowledge base (about 5,200 files, 167 MB of record), both engines on the same day:

| set | engine | recall | precision | MRR | distinct | per question |
|---|---|---|---|---|---|---|
| questions (14) | word match (before) | 0.08 | 0.04 | 0.06 | 0.78 | 136 s |
| questions (14) | this search | **0.45** | **0.21** | **0.61** | 1.00 | 1.5 s |
| held out (10) | word match (before) | 0.00 | 0.01 | 0.00 | 0.79 | 140 s |
| held out (10) | this search | **0.39** | **0.19** | **0.44** | 1.00 | 2.1 s |

This search was not tuned on either set. The record grows and newer files also answer some questions (later step-backs, for one), so compare engines on the same day.
