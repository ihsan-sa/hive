# Gate gate-1792832368-ce06f30b: KEPT

no regression, no gain; ties are allowed for this change

- candidate 968033683 (0.0.0-gate.968033683), binary sha256 547dadf629da; control 85b2f2618; sandbox bwrap, 177s

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.968033683 |
| org | 1 | true | 0.1 |  | gate-1792832368-ce06f30b-candidate |
| benchmark | 1 | true | 15.8 | score=3 asks_finished=1 seconds=13.55 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.547283665} |
| recurse | 1 | true | 3.1 |  | nested gate gate-1792832539-bb2caee3 rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.85b2f2618 |
| org | 1 | true | 0.1 |  | gate-1792832368-ce06f30b-control |
| benchmark | 1 | true | 15.8 | score=3 asks_finished=1 seconds=14.05 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":14.048837037} |
| recurse | 1 | true | 3.1 |  | nested gate gate-1792832543-afef834e rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
