# Scripts behind forced.md

Each reads the Hive's journal: `python3 -I <script> "$HIVE_STATE/events.jsonl"`.

- `cost.py`: cost and turns per role (per-session `cost_usd` differences); an optional second argument is an ISO time to count from.
- `runs.py`: runs, turns, raw `cost_usd` sums and median system-prompt and brief words per role.
- `stages.py`: tasks by final stage, aim and source, and the time finished tasks spent in each stage.
- `q.py`: task-hours spent queued, by reason.
- `fu.py`: review follow-up tasks by final stage and whether they have a PR.
