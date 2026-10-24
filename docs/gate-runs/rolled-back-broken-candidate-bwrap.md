# Gate gate-1792832546-992919a1: ROLLED BACK

worse than the current Hive: bench passes 0/1, the control 1/1

- candidate prebuilt, binary sha256 40ca6eab6cf5; control prebuilt; sandbox bwrap, 201s
- regression: bench passes 0/1, the control 1/1
- regression: daemon passes 0/1, the control 1/1
- regression: recurse passes 0/1, the control 1/1

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | false | 60.2 |  | status never answered |
| org | 1 | true | 0.1 |  | gate-1792832546-992919a1-candidate |
| bench | 1 | false | 120.4 |  | …: === RUN   TestBenchmark     e2e_test.go:94: daemon didn't start in 2m0s: broken --- FAIL: TestBenchmark (120.34s) FAIL |
| recurse | 1 | false | 0.0 |  | no report from the nested gate |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-dev |
| org | 1 | true | 0.1 |  | gate-1792832546-992919a1-control |
| bench | 1 | true | 15.8 | score=3 asks_finished=1 seconds=14.05 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":14.050362564} |
| recurse | 1 | true | 3.1 |  | nested gate gate-1792832744-1916ef3a rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
