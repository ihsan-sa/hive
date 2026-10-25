# Sharing with the developer

An install can share with the Hive's developer so the improve loop learns from real installs, at one of three levels, and separately contribute to the open-source Hive. Both are off unless the person says so.

- **off** sends nothing.
- **failures** sends the failure and usage reports below.
- **everything** sends those reports and also, in batches, the records: each agent run's brief (`runs/*/ro/brief.md`), notes (`runs/*/box/notes.md`) and turn logs (`runs/*/host/turn-*.jsonl`), the chats (`chats/*.jsonl`), the journal (`events.jsonl`) and the daemon log (`daemon.log`), written from when it was chosen (see [Batches](#batches)).

The batches and everything contributed upstream also pass the publish scan (`internal/mod/scan`, issue #370) after scrubbing, as `hive share` does: its key formats, the deny list and the handle list given at run time (`HIVE_SCAN_DENY` and `HIVE_SCAN_HANDLES`, else `scan-deny.txt` and `scan-handles.txt` in `.hive/private`). With no handle list neither path sends anything, and the failures wait. A batch part with a hit is held, its file isn't marked sent, and it is tried again next run. An issue, comment or offer with a hit isn't filed. The job's result says what was held by place and kind, never the thing itself. Everything sent is scrubbed on this machine first (`report.Scrub`): private keys, credentials in URLs, known token shapes, anything a name says is secret, long mixed letter-and-digit runs, emails, the home folder and the Hive's folder, and the person's user name and the machine's name as whole words (unless a role account such as `root` or `runner`, or under three characters).

The installer and first run ask one question, `report.Question` word for word, which names what each level sends and its caps (`TestQuestionSaysWhatIsSent` holds it to the reports and `TestEverythingSaysWhatIsSent` to the batches, so neither changes alone). The installer asks on the terminal (`/dev/tty` on Linux and macOS, so `curl | sh` works; the console on Windows); with no terminal, or on Enter, the answer is off, and `HIVE_SHARE=off|failures|everything` (or `yes`, which is failures, and `no`) answers ahead of time. A release with the `report` command but not the levels gets `report on`, the failures, when everything was chosen. The answer goes to `share` in `.hive/config.json` (a time when sharing, `off` when not) and, for everything, `share_all` (the time it was chosen); before first run has made the folder, it waits in `share` next to the workspace pointer file (`~/.config/hive/` on Linux) and first run moves it in. First run asks only if nothing has answered, and a reinstall doesn't ask again.

The level changes at any time: Settings → "share with developer" (a level above the current one shows what it sends first, and Enter on that is the yes), `hive report off|failures|everything` (`on` is failures), or asking the lead, whose `share` op sets less at once and asks the person in Waiting before it sets more. A lower level stops what the higher one sent from then on: leaving everything sends no more batches, and choosing it again sends only what is written after; off sends nothing more and drops the reports still waiting. Nothing is sent from before a level was chosen; choosing a level when sharing is already on keeps the time it began, so nothing since the last report is skipped. A failed answer in `install.sh` warns and the install goes on.

This is separate from `hive share`, which sends one bundle of the Hive's chats, runs, notes and logs only when the person runs it, to `share_bundle_url`. They stay two because they differ in kind: reports are small, scrubbed and sent on their own once the person agreed; a bundle is everything, unscrubbed, and each one is agreed to by hand. The bundle's `config.json` leaves out `install_id` and `share_url`, so a bundle can't be matched to the install's reports.

## Contributing

A separate yes or no, `contribute` in the config (a time when yes, `off` when no), asked at first run after the level (`report.ContributeQuestion`; Enter is no) and changed in Settings → "contribute upstream", with `hive report contribute on|off`, or through the lead (a yes waits for the person's). With it, the `report.send` job:

- files each new kind of failure as an issue on the upstream (`release_repo`, `ihsan-sa/hive` by default), once: a failure's fingerprint is its kind and its message with numbers, ids and spacing taken out, and the issue carries `<!-- hive-report:<fingerprint> -->`, so a repeat on this install or another finds it instead of opening another: before filing, the install searches the upstream for the fingerprint (GitHub's issue search, `"hive-report:<fingerprint>" in:body`, open or closed, however old), and a search that fails files nothing that run. The issue holds the kind, version, OS, the scrubbed message and up to 1,500 characters of the scrubbed log; for upstream the scrub also takes out the person's own repositories (`[repo]`) and the accounts that own them (`[owner]`). Each install keeps one comment on it, `<!-- hive-install:<tag> -->`, saying how many times it saw it; the tag is a hash of the install id, so the public issue can't be tied to the reports the endpoint has. A run writes at most 10 times upstream; the rest waits.
- offers the Hive's own fixes upstream: when this Hive's own project (`self`) is a fork, the repo the person chose, and has merged tasks since the yes, it opens one pull request from the fork's branch to the upstream (or finds the open one) and keeps a comment on it listing them. A Hive working in the upstream itself (the steward) offers nothing. Nothing is pushed or opened upstream without the yes; saying no stops it from the next run.

## What reaches the developer's Hive

The developer's own Hive (the one whose `self` project is the upstream, H0) reads what installs sent into its improve sweep's digest, under "What installs sent": the reports the endpoint stored, read from `reports_read_url` with the owner's key in `.hive/logins/report/read.key` (a JSON array of reports or one a line; the owner sets the read up), grouped by fingerprint with how many reports, which installs and which versions; and the open contribute issues upstream (found by the same search, so not only the newest), each with how many installs commented and how many times they saw it. The sweep files fixes from them, saying which installs and how many.

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
- `hive doctor` failures are appended to `.hive/reports/outbox.jsonl` (only while sharing or contributing is on) and go into the next run's reports. The job never rewrites the outbox: the `report.queued` journal event records how far into it a run read, so a failure the doctor appends while a run is reading or sending waits for the next run instead of being lost.
- If the job stops between keeping the queue and marking the journal, the next run builds the same reports again; it drops the queued copies a new build repeats (a failure with the same kind, time and message, a usage report starting at the same event), so each is sent once.
- The outbox only grows, by one line per failed `hive doctor` while sharing or contributing is on; a run reads only past its offset. Compacting it would have to keep the `report.queued` offset in step (for example by rotating to a new file name recorded in the event), or a doctor append could be lost again.
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

## Batches

At everything, each `report.send` run also sends one batch: a zip to `share_bundle_url` (by default `https://hive-api.ihsan.cc/v1/bundle`, the endpoint `hive share` uses), `Content-Type: application/zip`, with `X-Hive-Install` the install id, of at most 8 MB. It holds the files of the set above that changed since the last batch, oldest first, each scrubbed as a contribute issue is: an append-only file (turn logs, chats, the journal, the daemon log) from where the last batch stopped, up to its last whole line, as `.hive/<path>@<offset>`; a brief or notes whole, as `.hive/<path>`. `batch.json` in it lists the parts (`install`, `version`, `type` `batch`, `os`, `at`, and each part's `name`, `from` and `bytes`). What doesn't fit waits for the next run; where the batches got to (`.hive/reports/batches.json`) moves only on a 2xx, and each sent batch's manifest is kept in `.hive/reports/batches/`. Choosing everything marks every file as sent as it stands, so only what is written after goes. Nothing else under `.hive` is ever in a batch: not the logins, the private folder, the config, the binaries, the knowledge base or a run's home and checkout.

The tests hold the Hive to the contract with a local endpoint (`internal/mod/report/contracttest`) that refuses any request breaking it.
