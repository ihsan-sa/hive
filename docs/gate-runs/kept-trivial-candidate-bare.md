# Gate gate-1792827950-cd98d537: KEPT

no regression, no gain; ties are allowed for this change

- candidate 02480c521 (0.0.0-gate.02480c521), binary sha256 9e472cfd690f; control 521131d17; sandbox bare, 111s

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.02480c521 |
| org | 1 | true | 0.0 |  | gate-1792827950-cd98d537-candidate |
| benchmark | 1 | true | 15.7 | score=3 asks_finished=1 seconds=14.04 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":14.040238661} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792828055-4ce19cba rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.521131d17 |
| org | 1 | true | 0.0 |  | gate-1792827950-cd98d537-control |
| benchmark | 1 | true | 15.7 | score=3 asks_finished=1 seconds=14.04 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":14.040229097} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792828058-5a9aed6d rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
