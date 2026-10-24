# Gate gate-1792829715-2ff761d1: KEPT

no regression, no gain; ties are allowed for this change

- candidate 07b6bbf14 (0.0.0-gate.07b6bbf14), binary sha256 091aabd71b61; control 19e49a649; sandbox bwrap, 205s

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-gate.07b6bbf14 |
| org | 1 | true | 0.2 |  | gate-1792829715-2ff761d1-candidate |
| benchmark | 1 | true | 15.9 | score=3 asks_finished=1 seconds=13.56 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.555890454} |
| recurse | 1 | true | 3.2 |  | nested gate gate-1792829913-146f811d rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-gate.19e49a649 |
| org | 1 | true | 0.2 |  | gate-1792829715-2ff761d1-control |
| benchmark | 1 | true | 15.9 | score=3 asks_finished=1 seconds=13.56 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.555357863} |
| recurse | 1 | true | 3.2 |  | nested gate gate-1792829916-9e9f3637 rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
