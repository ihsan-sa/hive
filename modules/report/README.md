# report
Sharing. `share` in `settings.json` is `{"failures": bool, "usage": bool}`. `failures` is on by default and also carries the daily check-in; `usage` is off by default. Anyone can turn either off in Settings or setup. With both false nothing is read, queued or sent, and nothing goes before setup is finished.

`hive-report status | send [--dry-run] | show [N] | question`

- `question` prints the text the setup screen shows before the person says yes; it lists everything a report holds.
- Reports are built from `log.jsonl` events after the opt-in (kinds and counts only: never chat, files, thread names or agent output), scrubbed locally (keys, tokens, emails, home folder, user and host name), at most 25 per run.
- Each report is copied to `reports/sent/` before it leaves; `show` prints the latest. A 400/411/413/415 answer drops a report for good; anything else keeps it for the next run.
- Endpoint: `share_url` in settings.json, else `HIVE_SHARE_URL`. The default is the project's own receiver (set at export; a plain source tree has none, so sends wait in the queue and `status` says so). One report per POST, JSON, `type` failure or usage, fields as in `show`; 2xx is sent.
- Run it on a schedule (for example every 6h) with the job runner.

## Daily check-in
With `failures` on, `send` adds one report of type `heartbeat` about once a day: the random install id, the version, the OS (`linux`, `windows` or `mac`, then the chip type: a Windows or Mac Hive knows its family from a `~/.hive-host` marker its installer writes, WSL is told apart by the kernel, and a Hive installed before that release says `linux`), the time and `agent_started` (a yes/no: true once this Hive's own log shows an agent session was launched; it is looked up only when `failures` is on, and kept once it is yes), nothing else. It is how the makers count active Hives. A Hive from before this default gets `failures` turned on once by `send`, marked with `share_defaults` in settings.json, so turning it off later holds.

## What counts as a failure
`failures` sends only the kind and a one-line message, never fields from the log: an agent ending with a non-zero exit, a login hitting its limit, lost helpers, all logins paused, a provider held back, an agent killed, and (the doctor check) one of the Hive's own systemd user units in a failed state, by unit name, once per failure.

## Feedback
`hive-report feedback` reads `{text, page, image}` from stdin and sends it at once: the app's Send feedback calls it when the person presses Send, so it needs no opt-in in `share` and works with sharing off. It sends the scrubbed comment, the JPEG picture, the page, the version and the install id, with the same copy kept in `reports/sent/`. Refused or offline, it says so on stderr, exits 1 and keeps nothing to retry: the person still has their words on screen. Endpoint: `share_feedback_url` in settings.json, else `HIVE_SHARE_FEEDBACK_URL`, else the report endpoint with `/report` changed to `/feedback`.

## "Everything" bundle
Only Send a copy now in Settings or `hive-report bundle --yes` sends it, never the timer; run without `--yes` it writes the scrubbed copy to `reports/bundles/` for the user to read first. It carries the last 500 lines of the log and the board (chats included), scrubbed. The receiver is `/v1/bundle` beside `/v1/report` (override with `share_bundle_url`). Retention and access rule: stored 30 days then deleted automatically, 3 per install per day, no read endpoint, readable only by the project owner, never copied elsewhere, and deleted early on request by install id.

Turning failures or usage on in Settings sends at once rather than at the next 6-hour timer. With usage on, the first send carries one report with the message `sharing is on` and zero counts, so the makers can tell an idle Hive from a broken route.

## Opening a GitHub issue yourself
`hive-report issue TITLE < TEXT` writes a scrubbed draft to `reports/issue-draft.md` and prints a prefilled new-issue link on the project's repo (`issues_repo` in settings.json, set at export), filled into the bug form. The person reviews it and posts it from their own GitHub account: no token is used or shipped. `--submit` posts it with an already-logged-in `gh`, only on that explicit command. In the app, Send feedback has "Open as a GitHub issue", which makes the same link from the comment (the picture isn't included) and sends nothing to the makers.

## Gaps in installed tools: `hive-report upstream draft`

`hive-report upstream draft --repo OWNER/NAME TITLE` (what is missing and what was seen on stdin) drafts an issue for the repo of a tool the Hive installed (from modules.json) or the Hive's own. It needs the same GitHub opt-in as `contribute`. The text is scrubbed, ends with a note that an AI agent drafted it, and the command prints it exactly as it would be filed. If an existing issue on that repo looks the same, it prints the link and drafts nothing. The draft waits in Settings > Data sharing (which shows the repo); only the person's Send files it, from their own login. A repo that isn't in modules.json, or whose owner is in `~/.cc/github-deny`, is refused again when sending.
