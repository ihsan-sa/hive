# Gate gate-1792808397: KEPT

no regression, no gain; ties are allowed for this change

- candidate 49c6a6ba4, control 615593a75, sandbox bare, 130s

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.6 |  | hive 0.0.0-dev |
| org | 1 | true | 0.3 |  | gate-1792808397-candidate |
| benchmark | 1 | true | 29.3 | score=2 asks_finished=1 seconds=15.24 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":15.243436995} |
| recurse | 1 | true | 1.1 |  | nested gate gate-1792808484: not better than the current Hive: nothing moved beyond the noise |
| daemon | 2 | true | 0.5 |  | hive 0.0.0-dev |
| org | 2 | true | 0.0 |  | gate-1792808397-candidate |
| benchmark | 2 | true | 18.6 | score=2 asks_finished=1 seconds=16.12 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":16.120899192} |
| recurse | 2 | true | 1.2 |  | nested gate gate-1792808524: not better than the current Hive: nothing moved beyond the noise |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.6 |  | hive 0.0.0-dev |
| org | 1 | true | 0.3 |  | gate-1792808397-control |
| benchmark | 1 | true | 19.1 | score=2 asks_finished=1 seconds=15.58 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":15.579212502} |
| recurse | 1 | true | 1.0 |  | nested gate gate-1792808485: not better than the current Hive: nothing moved beyond the noise |
| daemon | 2 | true | 0.6 |  | hive 0.0.0-dev |
| org | 2 | true | 0.0 |  | gate-1792808397-control |
| benchmark | 2 | true | 17.9 | score=2 asks_finished=1 seconds=16.07 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":16.066624614} |
| recurse | 2 | true | 1.1 |  | nested gate gate-1792808525: not better than the current Hive: nothing moved beyond the noise |

Org repositories made: 2, removed: 2, left: 0
