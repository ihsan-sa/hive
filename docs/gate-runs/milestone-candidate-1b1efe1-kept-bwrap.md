# Gate gate-1792872884-6d9819a6: KEPT

better than the current Hive with no regression: benchmark seconds: candidate 12.4, control 56.61 (noise ±1.87, n=5/5)

- candidate 1b1efe13b (0.0.0-gate.1b1efe13b), binary sha256 00280247b7df; control d3587fafe; sandbox bwrap, 556s
- gain: benchmark seconds: candidate 12.4, control 56.61 (noise ±1.87, n=5/5)

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.1b1efe13b |
| benchmark | 1 | true | 12.8 | score=6 asks_finished=4 seconds=11.55 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":11.553549925} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792873108-1fa4ff2c rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 2 | true | 1.5 |  | hive 0.0.0-gate.1b1efe13b |
| benchmark | 2 | true | 12.8 | score=6 asks_finished=4 seconds=11.55 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":11.553494591} |
| recurse | 2 | true | 3.1 |  | nested gate gate-1792873188-49298698 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 3 | true | 1.5 |  | hive 0.0.0-gate.1b1efe13b |
| benchmark | 3 | true | 12.8 | score=6 asks_finished=4 seconds=11.55 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":11.552571175} |
| recurse | 3 | true | 3.0 |  | nested gate gate-1792873267-260a70db rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 4 | true | 1.5 |  | hive 0.0.0-gate.1b1efe13b |
| benchmark | 4 | true | 17.1 | score=6 asks_finished=4 seconds=15.87 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":15.867668026} |
| recurse | 4 | true | 3.0 |  | nested gate gate-1792873352-e95c56a4 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 5 | true | 1.5 |  | hive 0.0.0-gate.1b1efe13b |
| benchmark | 5 | true | 12.7 | score=6 asks_finished=4 seconds=11.47 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …ks":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":11.47213863} |
| recurse | 5 | true | 3.0 |  | nested gate gate-1792873434-e9b82411 rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| benchmark | 1 | true | 57.9 | score=6 asks_finished=4 seconds=56.23 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.231238248} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792873111-701a9d35 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 2 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| benchmark | 2 | true | 57.5 | score=6 asks_finished=4 seconds=55.79 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":55.790868405} |
| recurse | 2 | true | 3.0 |  | nested gate gate-1792873191-d62afa8f rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 3 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| benchmark | 3 | true | 58.0 | score=6 asks_finished=4 seconds=56.24 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.236577382} |
| recurse | 3 | true | 3.1 |  | nested gate gate-1792873270-386f917c rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 4 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| benchmark | 4 | true | 58.7 | score=6 asks_finished=4 seconds=57.02 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":57.021815441} |
| recurse | 4 | true | 3.0 |  | nested gate gate-1792873355-194a469d rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 5 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| benchmark | 5 | true | 59.5 | score=6 asks_finished=4 seconds=57.75 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":57.746147321} |
| recurse | 5 | true | 3.1 |  | nested gate gate-1792873437-a97b9de4 rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
