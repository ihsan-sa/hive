# Gate gate-1792808631: ROLLED BACK

worse than the current Hive: benchmark seconds: candidate 25.61, control 15.57 (noise ±0.976, n=2/2)

- candidate 29d596df5, control 615593a75, sandbox bare, 135s
- regression: benchmark seconds: candidate 25.61, control 15.57 (noise ±0.976, n=2/2)

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.5 |  | hive 0.0.0-dev |
| org | 1 | true | 0.0 |  | gate-1792808631-candidate |
| benchmark | 1 | true | 29.4 | score=2 asks_finished=1 seconds=26.05 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":26.046224206} |
| recurse | 1 | true | 1.3 |  | nested gate gate-1792808707: not better than the current Hive: nothing moved beyond the noise |
| daemon | 2 | true | 0.9 |  | hive 0.0.0-dev |
| org | 2 | true | 0.1 |  | gate-1792808631-candidate |
| benchmark | 2 | true | 29.8 | score=2 asks_finished=1 seconds=25.17 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":25.16853104} |
| recurse | 2 | true | 2.9 |  | nested gate gate-1792808761: not better than the current Hive: nothing moved beyond the noise |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 0.5 |  | hive 0.0.0-dev |
| org | 1 | true | 0.0 |  | gate-1792808631-control |
| benchmark | 1 | true | 18.5 | score=2 asks_finished=1 seconds=15.78 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":15.784554282} |
| recurse | 1 | true | 1.9 |  | nested gate gate-1792808708: not better than the current Hive: nothing moved beyond the noise |
| daemon | 2 | true | 0.6 |  | hive 0.0.0-dev |
| org | 2 | true | 0.1 |  | gate-1792808631-control |
| benchmark | 2 | true | 19.5 | score=2 asks_finished=1 seconds=15.36 cost_usd=0 | {"asks":1,"asks_finished":1,"cost_usd":0,"score":2,"seconds":15.357852818} |
| recurse | 2 | true | 1.6 |  | nested gate gate-1792808764: not better than the current Hive: nothing moved beyond the noise |

Org repositories made: 2, removed: 2, left: 0
