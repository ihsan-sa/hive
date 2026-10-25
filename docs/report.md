# Sharing failures with the developer

An install can send failure and usage reports to the Hive's developer so the improve loop learns from real installs. It is off unless the person says yes. The installer and first run ask one question, `report.Question` word for word, which names every field a report holds and its cap (a test holds the question to the code, so neither changes alone). The installer asks on the terminal (`/dev/tty` on Linux and macOS, so `curl | sh` works; the console on Windows); with no terminal, or on Enter, the answer is no, and `HIVE_SHARE=yes` or `HIVE_SHARE=no` answers ahead of time. The answer goes to `share` in `.hive/config.json` (a time when yes, `off` when no); before first run has made the folder, it waits in `share` next to the workspace pointer file (`~/.config/hive/` on Linux) and first run moves it in. First run asks only if nothing has answered, a reinstall doesn't ask again, and `hive report on|off` or Settings → "share reports" changes it (turning it on there shows `report.Question` first, and Enter on it is the yes; `hive report on` prints what a report holds). Nothing is sent from before sharing was turned on; turning it on when it is already on keeps that time, so nothing since the last report is skipped. A failed answer in `install.sh` warns and the install goes on.

This is separate from `hive share`, which sends one bundle of the Hive's chats, runs, notes and logs only when the person runs it, to `share_bundle_url`. They stay two because they differ in kind: reports are small, scrubbed and sent on their own once the person agreed; a bundle is everything, unscrubbed, and each one is agreed to by hand. The bundle's `config.json` leaves out `install_id` and `share_url`, so a bundle can't be matched to the install's reports.

## The endpoint

Reports go to `share_url` in `.hive/config.json`, by default `https://hive-api.ihsan.cc/v1/report`. The endpoint is run by the developer, and this page says only how the Hive meets its contract. In short: `POST`, `Content-Type: application/json`, one report per request, at most 64 KiB, no login. It answers 202 when it stored the report; 400 (not JSON or not the schema), 411, 413 or 415 when it never will; 429 when an install sends more than 30 an hour (or everyone together too many); 507 when its store is full.

## Schema version

This page is version 1 of the report schema, and the version is the `/v1/` in the address. A report a v1 receiver would refuse (a new key, a new `type`, a tighter or looser field) is a new version at `/v2/report`, documented here, and the Hive switches its default address when it sends it; a v1 receiver keeps taking v1 reports. `version` in a report is the Hive's own version, not the schema's. The contract tests (`internal/mod/report/contracttest`) refuse a request to any other path.

## What the Hive does

- The `report.send` job (default `every 6h`, a schedule like any other) reads the journal since the last run that queued reports, turns what it finds into reports, scrubs each one, adds them to the queue in `.hive/reports/pending.jsonl` and marks the journal. Then it POSTs the queue one report at a time, oldest first. `hive report send` runs it now.
- A run makes one `failure` report per failure, oldest first, then one `usage` report. A usage report whose counts are all zero isn't made, and a run with nothing queued sends nothing.
- A run sends at most 25 reports, so the job stays under the endpoint's 30 an hour. If the queue holds more, the oldest are dropped and the usage report counts the dropped failures in `failures_dropped`.
- 2xx: sent. 400, 411, 413 or 415: dropped for good, never retried; its copy stays. Anything else (429, 507, another status, a timeout of 30 s, no connection): the run stops there and that report and the rest wait in the queue for the next run. The Hive carries on either way.
- Every report sent is kept as `.hive/reports/sent/<time>-<n>.json`, byte for byte the body that went out. The copy is written before the report leaves and removed if it is kept for the next run; if it can't be written, nothing is sent. `hive report` shows the state; `hive report show` prints the last report.
- `hive doctor` failures are appended to `.hive/reports/outbox.jsonl` (only while sharing is on) and go into the next run's reports. The job never rewrites the outbox: the `report.queued` journal event records how far into it a run read, so a failure the doctor appends while a run is reading or sending waits for the next run instead of being lost.
- If the job stops between keeping the queue and marking the journal, the next run builds the same reports again; it drops the queued copies a new build repeats (a failure with the same kind, time and message, a usage report starting at the same event), so each is sent once.
- The outbox only grows, by one line per failed `hive doctor` while sharing is on; a run reads only past its offset. Compacting it would have to keep the `report.queued` offset in step (for example by rotating to a new file name recorded in the event), or a doctor append could be lost again.
- The install id is a random UUID made when sharing is first turned on, kept in `install_id` in the config. It fits the endpoint's `[A-Za-z0-9_-]{8,64}` and names no person or machine. There is no account.



## What a report holds

Every report has `install` (the install id), `version` (Hive's version with anything outside `[A-Za-z0-9._+-]` turned into `-`, clipped to 32, so `0.0.0-dev` and `1.2.0+dirty` pass as they are), `type` and `os` (Go's `GOOS/GOARCH`, e.g. `linux/amd64`). The Hive sends no other keys.

A `failure` report adds:

| Field | Meaning |
|---|---|
| `kind` | What failed: `task-blocked` (the worker stopped and said why), `task-stopped` (the Hive gave up: red tests after the rounds, no verdict, conflicts), `run-error:<role>` (a turn ended in an error and was parked to retry; the role is `lead`, `worker`, `reviewer`, ...), `alert` (an alert the Hive raised), `crash` (the daemon started again without having shut down), `doctor` (a `hive doctor` check failed). Lower-cased, with anything outside `[a-z0-9_.:-]` turned into `-`, at most 64. |
| `at` | When it happened, RFC 3339 in UTC to the second. |
| `message` | The reason, scrubbed, on one line, at most 2000 characters. By kind: `task-blocked` and `task-stopped` the task's reason; `run-error` the turn's park reason; `alert` the alert's `<kind>: <text>`; `crash` "the Hive stopped without shutting down"; `doctor` the failed check lines. |
| `log` | The agent's reply (`run-error`) or the task log's last 8 lines (`task-blocked`, `task-stopped`), scrubbed, line breaks kept, at most 4000 characters. Left out when there are none. |

A `usage` report adds `at` (the last journal event the run read), `message` (`harness <name>; events from <time> to <time>`) and `usage`, the counts since the last run: `runs` (agent runs created), `turns` (agent turns ended), `merges` (tasks merged, reverts included), `reverts` (revert tasks merged), `cost_usd` (the harness's reported cost; 0 when it reports none) and, when the cap dropped any, `failures_dropped`. Token counts aren't sent: the harness doesn't report them.

The endpoint may see a report twice if an answer is lost after it stored it; the Hive resends whatever didn't get a 2xx.

Example, a failure and then the usage report:

```json
{"install":"3f0c8a5e-2b7d-4c1e-9a6f-0d4b8e2c7a91","version":"0.1.0","type":"failure","kind":"task-stopped","os":"linux/amd64","at":"2026-10-22T18:30:02Z","message":"stopped after 3 rounds of fixes; what remains: ...","log":"tests failed in TestParseHeader ..."}
{"install":"3f0c8a5e-2b7d-4c1e-9a6f-0d4b8e2c7a91","version":"0.1.0","type":"usage","os":"linux/amd64","at":"2026-10-22T20:58:40Z","message":"harness claude; events from 2026-10-22T15:02:11Z to 2026-10-22T20:58:40Z","usage":{"cost_usd":1.84,"merges":2,"reverts":0,"runs":7,"turns":23}}
```

The tests hold the Hive to the contract with a local endpoint (`internal/mod/report/contracttest`) that refuses any request breaking it.
