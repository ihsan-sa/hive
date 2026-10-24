# Gate gate-1792826662-a8500731: KEPT

no regression, no gain; ties are allowed for this change

- candidate , control , sandbox bwrap, 41s

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-dev |
| org | 1 | false | 0.0 |  | …itory '/tmp/TestBwrapGateKeepsAndRollsBack3626664902/001/keep/org/gate-1792826662-a8500731-candidate.git' does not exist |
| bench | 1 | true | 15.9 | score=3 asks_finished=1 seconds=13.56 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.557284919} |
| recurse | 1 | true | 3.2 |  | nested gate gate-1792826697-76160e5a rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-dev |
| org | 1 | false | 0.0 |  | …ository '/tmp/TestBwrapGateKeepsAndRollsBack3626664902/001/keep/org/gate-1792826662-a8500731-control.git' does not exist |
| bench | 1 | true | 15.9 | score=3 asks_finished=1 seconds=13.56 commits_landed=1 asks_done_reported=1 cost_usd_reported=0 egress_bytes=0 | …s":1,"asks_done_reported":1,"asks_finished":1,"commits_landed":1,"cost_usd_reported":0,"score":3,"seconds":13.556809903} |
| recurse | 1 | true | 3.2 |  | nested gate gate-1792826700-e81f6661 rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
