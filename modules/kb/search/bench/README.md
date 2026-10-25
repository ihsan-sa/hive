# The search's benchmark

Real questions about what this Hive did, each with the knowledge base files that answer it, checked by hand against the record.

- `questions.json`: 14 questions from the night of 2026-10-24, the three the person named among them ("why was #111 reverted", "what blocks the gate", "which merges skipped a security read"). Written by an agent that read the record and never saw the search. The search was tuned on these.
- `heldout.json`: 10 more, on other topics (sweeps, step-backs, releases, alerts, feedback, stalls), written later by another such agent. Not tuned on, with one exception: they showed that a number in a question ("HTTP 403") was dropped as a word, and that was fixed.

Measured with `go run ./modules/kb/search bench --kb "$HIVE_KB" <file>` (`--engine words` for the word match this search replaced, `--show` for each question's top files, `--json` for the numbers). Over the top 10 files a question returns:

- **recall**: of the files that answer it, the share in the top 10;
- **precision**: of the top 10 results, the share that answer it (or are listed as relevant);
- **MRR**: one over the rank of the first file that answers it;
- **distinct**: of the top 10 results, the share that are a file not shown already (the word match returned the same chat or task file many times).

## Results

Measured 2026-10-25 02:24 UTC on the live knowledge base (2,419 files under `record/`, 68 MB):

| set | engine | recall | precision | MRR | distinct |
|---|---|---|---|---|---|
| questions (14, tuned on) | words (before) | 0.07 | 0.04 | 0.03 | 0.55 |
| questions (14, tuned on) | this search | **0.60** | **0.29** | **0.70** | 1.00 |
| held out (10) | words (before) | 0.05 | 0.08 | 0.12 | 0.59 |
| held out (10) | this search | **0.47** | **0.20** | **0.43** | 1.00 |

The held-out row is the honest one. Precision stays low partly by construction: a question has 3-4 answer files and 10 results are counted.

The record grows, so a later run gives other numbers; compare engines on the same day. A search on the live knowledge base takes about a second (the files are read on every call, on every core); the word match took about a minute per question on it.
