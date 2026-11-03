# report
Opt-in sharing. `share` in `settings.json` is `{"failures": bool, "usage": bool}`, both false by default; with both false nothing is read, queued or sent.

`hive-report status | send [--dry-run] | show [N] | question`

- `question` prints the text the setup screen shows before the person says yes; it lists everything a report holds.
- Reports are built from `log.jsonl` events after the opt-in (kinds and counts only: never chat, files, thread names or agent output), scrubbed locally (keys, tokens, emails, home folder, user and host name), at most 25 per run.
- Each report is copied to `reports/sent/` before it leaves; `show` prints the latest. A 400/411/413/415 answer drops a report for good; anything else keeps it for the next run.
- Endpoint: `share_url` in settings.json, else `HIVE_SHARE_URL`. The default is the project's own receiver (set at export; a plain source tree has none, so sends wait in the queue and `status` says so). One report per POST, JSON, `type` failure or usage, fields as in `show`; 2xx is sent.
- Run it on a schedule (for example every 6h) with the job runner.

## What counts as a failure
`failures` sends only the kind and a one-line message, never fields from the log: an agent ending with a non-zero exit, a login hitting its limit, lost helpers, all logins paused, a provider held back, an agent killed, and (the doctor check) one of the Hive's own systemd user units in a failed state, by unit name, once per failure.

## Feedback
`hive-report feedback` reads `{text, page, image}` from stdin and sends it at once: the app's Send feedback calls it when the person presses Send, so it needs no opt-in in `share` and works with sharing off. It sends the scrubbed comment, the JPEG picture, the page, the version and the install id, with the same copy kept in `reports/sent/`. Refused or offline, it says so on stderr, exits 1 and keeps nothing to retry: the person still has their words on screen. Endpoint: `share_feedback_url` in settings.json, else `HIVE_SHARE_FEEDBACK_URL`, else the report endpoint with `/report` changed to `/feedback`.

## "Everything" bundle
Only `hive-report bundle --yes` sends it, never the timer; run without `--yes` it writes the scrubbed copy to `reports/bundles/` for the user to read first. It carries the last 500 lines of the log and the board (chats included), scrubbed. The receiver is `/v1/bundle` beside `/v1/report` (override with `share_bundle_url`). Retention and access rule: stored 30 days then deleted automatically, 3 per install per day, no read endpoint, readable only by the project owner, never copied elsewhere, and deleted early on request by install id.
