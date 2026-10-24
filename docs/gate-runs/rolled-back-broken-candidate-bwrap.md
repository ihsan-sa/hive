# Gate gate-1792826703-4fb67198: ROLLED BACK

worse than the current Hive: bench passes 0/1, the control 1/1

- candidate , control , sandbox bwrap, 201s
- regression: bench passes 0/1, the control 1/1
- regression: daemon passes 0/1, the control 1/1
- regression: recurse passes 0/1, the control 1/1

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | false | 60.2 |  | status never answered |
| org | 1 | false | 0.0 |  | …y '/tmp/TestBwrapGateKeepsAndRollsBack3626664902/001/rollback/org/gate-1792826703-4fb67198-candidate.git' does not exist |
| bench | 1 | false | 120.5 |  | …: === RUN   TestBenchmark     e2e_test.go:94: daemon didn't start in 2m0s: broken --- FAIL: TestBenchmark (120.38s) FAIL |
| recurse | 1 | false | 0.0 |  | no report from the nested gate |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-dev |
| org | 1 | false | 0.0 |  | …ory '/tmp/TestBwrapGateKeepsAndRollsBack3626664902/001/rollback/org/gate-1792826703-4fb67198-control.git' does not exist |
| bench | 1 | true | 15.9 | score=3 asks_finished=1 seconds=13.55 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.554280251} |
| recurse | 1 | true | 3.2 |  | nested gate gate-1792826902-c5a263f1 rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
