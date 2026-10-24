# Gate gate-1792809000: ROLLED BACK

worse than the current Hive: e2e passes 0/1, the control 1/1

- candidate 3ed76b710, control 7fcc536f8, sandbox bare, 612s
- gain: benchmark seconds: candidate 15.59, control 16.06 (noise ±0.321, n=1/1)
- regression: e2e passes 0/1, the control 1/1

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.6 |  | hive 0.0.0-dev |
| org | 1 | true | 0.1 |  | gate-1792809000-candidate |
| e2e | 1 | false | 269.7 |  | …dule's skill: "The codeword is PLUM42.\n" FAIL FAIL	github.com/ihsan-sa/hive-standalone-build/internal/e2e	267.045s FAIL |
| benchmark | 1 | true | 20.9 | score=2 asks_finished=1 seconds=15.59 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":15.589847363} |
| recurse | 1 | true | 1.3 |  | nested gate gate-1792809610: not better than the current Hive: nothing moved beyond the noise |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.6 |  | hive 0.0.0-dev |
| org | 1 | true | 0.1 |  | gate-1792809000-control |
| e2e | 1 | true | 267.9 |  |  |
| benchmark | 1 | true | 17.8 | score=2 asks_finished=1 seconds=16.06 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":16.06338794} |
| recurse | 1 | true | 1.5 |  | nested gate gate-1792809611: not better than the current Hive: nothing moved beyond the noise |

Org repositories made: 2, removed: 2, left: 0
