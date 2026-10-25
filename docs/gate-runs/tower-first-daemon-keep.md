# Gate gate-1792869669-8f3f90b3: KEPT

better than the current Hive with no regression: e2e passes 3/3, the control 2/3

- candidate 6861de9da (0.0.0-gate.6861de9da), binary sha256 65e9cd36059b; control d3587fafe; sandbox bwrap, 1959s
- gain: e2e passes 3/3, the control 2/3

## Candidate

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-gate.6861de9da |
| org | 1 | true | 0.1 |  | gate-1792869669-8f3f90b3-candidate |
| e2e | 1 | true | 235.6 |  |  |
| benchmark | 1 | true | 59.7 | score=6 asks_finished=4 seconds=57.94 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":57.942196662} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792870418-a6153995 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 2 | true | 1.5 |  | hive 0.0.0-gate.6861de9da |
| org | 2 | true | 0.1 |  | gate-1792869669-8f3f90b3-candidate |
| e2e | 2 | true | 232.1 |  |  |
| benchmark | 2 | true | 57.9 | score=6 asks_finished=4 seconds=56.18 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.175394405} |
| recurse | 2 | true | 3.1 |  | nested gate gate-1792871017-6e5bf603 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 3 | true | 1.5 |  | hive 0.0.0-gate.6861de9da |
| org | 3 | true | 0.1 |  | gate-1792869669-8f3f90b3-candidate |
| e2e | 3 | true | 251.8 |  |  |
| benchmark | 3 | true | 57.9 | score=6 asks_finished=4 seconds=56.22 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.222222603} |
| recurse | 3 | true | 3.0 |  | nested gate gate-1792871622-f4c16c93 rolled back its self-test (2 candidate results, 2 repos made and removed) |

## Control

| probe | rep | pass | seconds | metrics | detail |
|---|---|---|---|---|---|
| daemon | 1 | true | 1.6 |  | hive 0.0.0-gate.d3587fafe |
| org | 1 | true | 0.1 |  | gate-1792869669-8f3f90b3-control |
| e2e | 1 | false | 236.7 |  | …TestCheckpointsBankEachGain1684996296/001/hive/.hive/gates/bank/0b24d7cd-score-7e69ac1879/.git: directory not empty FAIL |
| benchmark | 1 | true | 57.9 | score=6 asks_finished=4 seconds=56.21 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.209527971} |
| recurse | 1 | true | 3.0 |  | nested gate gate-1792870421-b878f139 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 2 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| org | 2 | true | 0.1 |  | gate-1792869669-8f3f90b3-control |
| e2e | 2 | true | 241.7 |  |  |
| benchmark | 2 | true | 57.9 | score=6 asks_finished=4 seconds=56.19 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …sks":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.1875648} |
| recurse | 2 | true | 3.1 |  | nested gate gate-1792871020-a004aec3 rolled back its self-test (2 candidate results, 2 repos made and removed) |
| daemon | 3 | true | 1.5 |  | hive 0.0.0-gate.d3587fafe |
| org | 3 | true | 0.1 |  | gate-1792869669-8f3f90b3-control |
| e2e | 3 | true | 228.2 |  |  |
| benchmark | 3 | true | 57.9 | score=6 asks_finished=4 seconds=56.2 commits_landed=4 asks_done_reported=4 cost_usd_reported=0 egress_bytes=0 | …s":4,"asks_done_reported":4,"asks_finished":4,"commits_landed":4,"cost_usd_reported":0,"score":6,"seconds":56.195755093} |
| recurse | 3 | true | 3.1 |  | nested gate gate-1792871625-c6697969 rolled back its self-test (2 candidate results, 2 repos made and removed) |

Org repositories made: 2, removed: 2, left: 0
