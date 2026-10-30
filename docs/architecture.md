# Architecture

One Go binary, `hive`.
A small core plus default modules; the daemon runs both and serves the control center over localhost.

## Packages

- `internal/core`: **The core.** Journal (append-only event log), workspace + config, runs (agent turns, park/resume, crash retry, reattach), mailbox ops, effects process (git/forge, commit extraction, gate guarantees), project registry, token source, person queue, ceiling notes.
  What is and isn't core, its measured size and the order the rest moves out: [core.md](core.md).
- `internal/sandbox`: Linux namespaces (`sandbox-exec` outer + `sandbox-init` pid 1), WSL launcher (Windows), Seatbelt (macOS, untested), bare (opt-in).
  Egress proxy.
- `internal/harness`: Adapter seam.
  Claude Code (`claude -p --output-format stream-json`), Codex (`codex exec --json`; inside our sandbox with its own off, since its bubblewrap can't nest; bare, in its own read-only or workspace-write sandbox; usage read from its session rollout at turn end), open (`hive open-agent`: any OpenAI-compatible endpoint; [models.md](models.md)), fake (scripted), built in;
  any other is a `Spec` a module declares in JSON (`provides.harnesses`: program, login, args per turn, env, API hosts, parse rules), loaded from `.modules/` by `harness.UseModules` ([harnesses.md](harnesses.md)).
- `internal/proc`: Detached processes; liveness by pid + start time, and a zombie is dead, though stopping one still signals its group; the /proc sweep that ends a dead owner's tagged processes (gate, bare checks).
- `internal/agentcli`: `hive agent <op>`: what agents call from inside the sandbox.
- `internal/fakeagent`: `hive fake-agent`: scripted agent for tests and dry runs (FAKE:* keywords).
- `internal/openagent`: `hive open-agent`: the open harness's agent loop over an OpenAI-compatible chat endpoint (Ollama, OpenRouter), with shell and file tools, writing Claude's stream-json.
- `internal/mod/*`: Default modules: `agents` (prompts, spawner), `board` (messaging: every message between agents is a post on one public board, in threads; agents join threads and search it, and a post wakes only the runs it names, with where to read: the `message`, `post`, `board`, `join` and `leave` ops), `chat`, `work` (ask → merge), `improve` (measure, keep/revert, sweep), `kb` (notes, the record filed from the journal, search), `system` (schedules, wake-ups), `risk` (requests past the sandbox → warden, tribunal or person), `projects`, `modules` (registry, installs, updates, the tick list), `runner` (modules that carry a program), `release` (self-update, self-release), `tools` (runtimes agents install for themselves), `forgeread` (the `github` op: agents' reads of GitHub's API on the person's login, cached a minute, 300 an hour for all agents), `feedback` (what agents tell the person about the Hive; [feedback.md](feedback.md)), `problems` (the Hive-wide problems board agents post to and add to, carried into the system prompt of the runs that work on or lead a change; a failing test met by a second task goes on it by itself, and `search` shows its matches first), `models` (the lead's route and trial ops, trials' records; [models.md](models.md)), `report` (opt-in failure reports to the developer, scrubbed; [report.md](report.md)), `scan` (the identity and secret scan on every release and on `hive share`), `stats` (the dashboard's spend, recent activity and all-time totals, a running tally of the journal).
- `internal/app`: Wires core + modules; the tick loop steps the core, then each module the registry lists, every second and 20ms after any journal event (at most 8 such early ticks a second), so a step that ends lets the next one go at once.
- `internal/daemon`: `hive daemon`, control API (127.0.0.1, bearer token in `.hive/private/daemon.json`, 0600).
- `internal/api`: API types + client.
- `internal/tui`: Control center (Bubble Tea): Chats, Dashboard, Waiting, Feedback, Knowledge, Modules, Settings.
- `internal/text`: `Visible`: spells out control, bidi and zero-width characters so text from outside (the usage feed, ask titles) can't rewrite the terminal.
- `internal/setup`: First run, `hive login`, `hive doctor` (which also checks the journal for a turn started while its run's last still ran, issue #1022; `cmd/hive/twins.go`), autostart (HKCU Run / systemd user / LaunchAgent).
- `modules/`: Default module content (orientation + role prompts, manifests), embedded and written to `.modules/` on first run.
- `internal/demo`: Demo project template (textkit: tests + score).
- `internal/softlayer`, `cmd/hive-soft`: The soft-layer experiment ([soft-layer.md](soft-layer.md)): arms of the agents module's prompts, installed into sub-Hives by `TestSoftLayer`, and an observer that reads any Hive's journal (runs, who messaged whom, briefs, helpers, asks, merges).
  Outside the daemon; nothing in the Hive imports it.

## Folder on disk (`~/hive`)

```
.hive/      events.jsonl, config.json, chats/*.jsonl, scores/*.jsonl,
            runs/<id>/{host,ro,box}, kb/ (git repo of notes; record/ filed from the journal, untracked, with record/hours/<UTC hour>.jsonl the journal's last 48 hours as written), gates/, checks/, improve/sweeps/,
            logins/, private/{daemon.json, update.json, tmp/, runs/<id>/{home,tmp,codex-sessions,cc-socks}, keys/<id>/{codex,open}},
            tools/<python|uv|node|go>/bin (agent-installed runtimes; read-only at /hive/tools, on PATH),
            reports/{sent/*.json, outbox.jsonl, pending.jsonl} (only when sharing is on)
.modules/   agents, chat, kb, improve, system, + installed (each with .hive-source.json)
projects/<name>/             host clone (effects process only)
projects/<name>/.checkouts/  agents' own checkouts (excluded from the clone)
```

Run dir: `host/` (turn output, prompts, stop file; never mounted on its own), `ro/` (system prompt, brief, inputs; mounted read-only at `/hive/run`; `run.created` names each of these over 1 KB in `stored` instead of journaling it again, and replay reads it back from here), `box/` (mailbox + notes; rw at `/hive/box`); its home (rw `$HOME`) is `.hive/private/runs/<id>/home`.
Every run but an isolated project's also gets the whole `.hive` read-only at `/hive/state` (`$HIVE_STATE`), so agents read one another's runs, chats and records.
What grants access lives in five folders covered there as a whole (an empty tmpfs; a Seatbelt deny on macOS, after which a run's own writable folders are allowed again): `logins/`, `modules/` (module programs' homes and logs), `private/keys/`, which holds each turn's `CODEX_HOME` and open model key, `private/tmp/`, the daemon's own temp folder that turn specs pass through, and `secrets/`, a folder per project of the project's own secret files (a broker login, a signing key) that the person or a warden's script fills;
and in three files covered one by one, `private/daemon.json` (the daemon's token) and the publish scan's `private/scan-deny.txt` and `scan-handles.txt`.
agents can't write it.
The rest of `private/` (runs' homes and scratch) is open to agents (`Workspace.Covered`, `Workspace.PrivateFiles`).
A project's runs that act for it (not a read-only run, nor a reviewer, security, warden, tribunal or judge seat; `Runs.secretsFor`) read their own project's folder read-only at `/hive/secrets` (`$HIVE_SECRETS`), and no other project's; such a run is never handed to an executor module, which would copy the folder to another machine, so it waits out a strain here.
Checks don't get it.
Covering folders rather than files means a run made mid-turn is covered too; a file cover is bound to the inode it found, so a `daemon.json` a restart renames into place is readable to a turn that began before it, until that turn ends.
On its first start on this layout the daemon ends the turns the earlier daemon left running (their mounts carry no cover over `private/`), then moves homes and Codex homes from where earlier versions kept them and deletes their turn specs (docs/risk.md, Updates).
config.json and daemon.log hold nothing that grants access.
A turn's spec, which carries its token, is written to the temp dir.
A daemon whose temp dir would be the host's `/tmp` on a tmpfs (memory, held until a reboot) sets its own `TMPDIR` to `.hive/private/tmp` on disk before it starts anything (`core.UseDiskTemp`), so everything it starts on the host inherits it; a `TMPDIR` set elsewhere is left alone, and sockets stay in `/tmp` and `/dev/shm` for their short paths.
A project shares at one of three levels (docs/sharing.md): shared, private (covered from the rest as an isolated one is, while its runs mount the shared Hive like a shared project's) or isolated; below, "isolated" covers private too except where a run's own view is meant.
Also covered is everything an isolated project leaves in the shared Hive: its clone under `/hive/projects`, its runs' folders and chats, its `checks/`, `gates/`, `risk/` and `kb/isolated/` folders, its runs' scratch folders, its scores, and while any project is isolated the whole journal, since one file holds every project's events (`Runs.hideIsolated`);
an isolated run sees no other isolated project's notes.
No agent outside an isolated project can spawn into it, file work in it, message or list its agents or see it in `status`, and its agents reach no other project (`Runs.Walled`, judged from the core's record of each run) nor create one, whose name, description and goals would land in the shared record.
Records from before these folders (old notes, `risk/<id>`, `checks/<id>.log`) move into them on start, and whenever main's history in the knowledge base still holds a note naming an isolated project (checked every start, so a project isolated later or a rebuild cut short is caught next time), that history starts afresh and the filer's old clones go, so the old text can't be read back from git (a clone of the knowledge base made before shares no history with it after).
A run or chat that starts mid-turn shows until that agent's next turn, and a bare run (no sandbox) can't cover anything.

Tools: `hive agent install --tool python` runs `hive tools-fetch` in a sandbox of its own that may write `.hive/tools` (network through the egress proxy, held to the asking run's egress list; on Windows inside WSL, so the builds are Linux ones).
uv, node and go come from their official downloads, checked against the official sha256 before anything is replaced; python is `uv python install`, which checks its own.
The person is told after each install.
`--system "<command>"` (root) becomes a person ask with that exact command.
On host paths (bare, Seatbelt) the tools folder's bins come first on PATH, so an installed runtime wins over an older one beside hive.

## Data flow: one ask

1. Person → chat → lead run (persistent Claude session, resumed per message; Claude Code compacts it when full, and the lead keeps its journal in its box `notes.md`, which a replacing lead inherits).
2. Lead → `hive agent brief` → `task.saved` (stage `new`).
   A brief an agent files (`brief` op) that matches an open task no worker has pushed for yet (new, queued or working, not the filer's own) joins it instead of becoming a task (`internal/mod/work/join.go`): a test name (`Test…`, `Benchmark…`, `Fuzz…`) both name in their title or done line (the joined briefs' included), titles sharing at least 80% of their words, or the same named files under titles sharing 30%.
   Its title, done and known are added to the task's known (in full for the first six joins, a line each after; a working task's worker is messaged them), each join is kept in `Task.Joins` (who, why, what it added; a lead's thread too, which then hears the task's outcomes as the task's own thread does), and each one joined after the issue opened is posted on it as a comment keyed `hive-joined:<task>:<n>`, so a restart edits rather than reposts;
   `--apart` (or `--apart true`) files it on its own.
   The aim is read as `File` reads it before matching, so a bad aim or a fix with no failure named is refused rather than joined.
   A working task whose worker run has ended isn't joined, and a busy task is waited on 2s at most, all of them 4s, since every brief waits on filing meanwhile.
   The person's asks and the Hive's own briefs (trials, improve) are filed as they come.
3. work.Tick: issue (idempotent by body marker) → worker run `w-<task>` in a fresh checkout at main → worker `finish`.
4. Effects.Extract: `git bundle` made **inside the sandbox**, fetched into the host clone with fsck, must descend from the recorded base → push `hive/<task>` → PR (idempotent by head branch).
5. No process (the person, 10-30: "they can freely edit everything including the core with no process"; earlier: "just send it, minimal gates, fail fast, stuff breaks but revert").
   The worker merges main into its branch and resolves any conflict in its own turn, and finishes; it may run the tests, and nothing makes it.
   The testing stage runs no test and goes straight to the merge.
6. No reviewer: nothing reads a change before it merges, whatever paths it touches, the core and the security list included; no protected path waits for an approval, and no forge check is waited on.
7. Red tests show only after the merge, in improve's measure of main (step 10), which reverts the merge.
8. Merge: no merge line.
   A PR the forge says conflicts goes straight back to its worker to merge main (`conflict` → `sendBack`, with `main.bundle`); otherwise it merges as it is, a draft readied, and a clash between two PRs is caught after the merge by improve's measurement of main (step 10).
   The publish scan sends back a change that would publish private details (a hard limit), and the person's `hold_merges` still holds.
   An agent may instead push to main itself on its own token; a worker that did and then finishes ends as landed (`alreadyLanded`), and that push is the agent's to watch on CI.
   No read waits, except one: a PR whose changed paths match main's `tests/LANDING-security.toml` `limits` (`Change.Limits`, the code that enforces the hard limits) waits for a warden's read of its own diff (`limitsRead` asks through `risk.Weigh`; `Effects.Merge` refuses without it, `core.LimitsHeld`), and a no sends it back to its worker.
9. Checkpoints (scored projects): while a worker runs, a change to its checkout's reflog makes the host extract its newest commit and score it; one that beats the best kept score (or the last checkpoint's, until main is measured) by more than the tolerance goes through steps 4-8 as a checkpoint on `hive/<task>[-n]`, its PR not closing the issue.
   A conflict drops it with the worker untouched.
   Once merged, the task's base moves to that commit and the worker is told at its next prompt; later commits are laid onto main with that commit as the merge base (`UpdateBranch`, `git merge-tree --merge-base`), since main holds it squashed.
   A finish with nothing past the last checkpoint is done.
   Each merged checkpoint keeps its PR on the task (`Banks`), so a revert of it names that PR.
   A commit is weighed only while no check waits for a slot; one whose score check queued anyway is taken out of the line once its worker finishes, so the finish lands within a tick (issue #1976: on 10-28 ten finished workers sat in `working` 80-171 min behind the check backlog; a refused recheck or `landed` now says since when the step holds the task and what it waits on; unit `TestCheckpointLeavesTheLineWhenTheWorkerFinishes`).
   A commit is weighed once; a worker sent back after its finish, or whose checkpoint the person left open, gets no more checkpoints.
   A worker that rewinds past a merged checkpoint is told to bring it back.
   The merge base needs git 2.40 or later; on an older git it's an error, not a conflict.
10. improve: measure merge sha (tests + score) — unless a main commit already measured contains it (a PR that landed before its task closed): then only `score.covered` names that commit and whether it was kept or red, with no score, alert or revert;
    each project's merges are measured by its own loop, in turn and newest first when it is behind, so one project's slow measure holds only its own (#2175); check slots bound how many run at once;
    measurements are taken to be in main's order, so a merge that comes back late (held for a missing tool, requeued after a failed sync) is covered by a later measurement even when that one is red, and the digest's `red` mark is what says the red may be its — compare with best kept;
    a score past the best kept's tolerance → the merge and its parent scored A B B A in one sitting, and within tolerance of the parent it is kept (issue #1865);
    a merge that changed the score is always paired so, its parent scored under the merge's score (#2090), and starts a new baseline only when the parent gives no number there;
    still past it with green tests after a clear fix → kept, re-based on its paired reading, and a brief to look into the dip (issue #568);
    after anything else → a judge on another model than the worker's weighs it with the change (docs/judge.md), and its keep moves the baseline there;
    worse → revert task (host-made revert + lessons line) → PR → merge;
    original PR commented.
    Red tests are read from the full check log for the failing packages, tests and assertions (that line is the note and the lesson), and what failed runs once more (`retest`: only the failing tests and packages under the project's go test flags): a pass is a flake, so the merge is kept, a `flaky` alert raised and the test filed as work.
    Still red, but every failing test also failed within 72h at a commit without the change (checked before the merge, none of the task's heads or checkpoints), is a flake too → `flake` alert, no revert.
11. release (self projects, one at a time per project): on a kept merge, list the merges after the one the last release was cut at, up to this one, with the reviewer runs that read each exact head;
    a merge a newer release already covers is refused with an alert;
    unless the person said "release", main's own checks (CI) on the kept commit must have passed (`ChecksState` with main's `required_checks`) before it is cut or offered: a failure alerts once (`release.ci_failed`);
    one still running, that can't start or can't be read journals `release.ci_waiting`;
    the tick tries the newest such commit, failed or waiting, again every 10 minutes (`retryWaiting`, a wait raised once after 6h) until it passes (`release.ci_passed`), so a re-run that passes releases it, and the next kept merge covers it too;
    meanwhile, the newest of the 10 merges after it that improve kept (its own kept `score.measured`, or a `score.covered` that isn't red), whose checks passed and whose history holds the waiting one, is cut instead (`greenAfter`), covering the waiting one;
    a pick then held or scan-blocked journals `release.ci_passed` at it, so the hold's alert speaks for the original from then on;
    so a commit whose ci run GitHub cancelled (no check runs, read as pending forever) never stalls releases;
    ci.yml gives each push to main its own concurrency group by commit, since GitHub cancels the older pending run in a shared group whatever `cancel-in-progress` says;
    a stewardship or `merges` refusal is alerted once per commit, however often it is retried;
    a head with no read, or a change to a `root` path that no security read passed (`SecurityOK` on its own diff: the security seat and the whole panel with a majority of lands, or an outside read), is read then by the Hive's own seats (`ReadMerged` in the work module, on the merge's own diff rebuilt from the host clone) while the release waits (`release.reading`, retried by the tick until `release.read_ended`), and waits for the person's "release" only when that read can't pass (a fix, no usable verdict, a diff it can't rebuild or none recorded);
    a root change those reads passed is released and the person is told after (`person.told`), and the cut answers any open release ask it covers;
    the person's "release" is approval kind `release`, kept apart from a `merge` approval of the same sha, and a newer hold supersedes the older ask;
    the identity and secret scan (`internal/mod/scan`: the commit's files that its `.hive/public.txt` names, against the deny and handle lists in `.hive/private/scan-deny.txt` and `scan-handles.txt`; any key, deny word or handle blocks with an alert, once per commit and finding, that names file, line and kind only; no handle list blocks with one standing `scan_handles` ask to the person instead, whose done retries the held commit and which a scan that reads a list answers);
    dry run; release with that list in its notes, as a pre-release, numbered on from the newest release (a pre-release included).
    The release module's tick (`Publish`, every 10 minutes) makes it the latest, which installs and auto-update follow, once `ci` and `update` pass in its own release runs on its commit;
    the newest of the waiting pre-releases that passed wins and older ones stay pre-releases;
    a failed run raises one alert and leaves it a pre-release (a re-run that passes publishes it);
    a failed `release` run (release.yml: scan, build, sign) is named as the cause instead, since no gate had binaries, and while the newest release's release run stays failed, a commit that changes nothing its failed job reads (scan: release.yml, `.hive/public.txt`, the scan's code, since the daemon's own scan passed at that commit; build, sign or none named: release.yml, go.mod/go.sum, non-test Go) is not cut (`release.held` once per tag, an alert and a `release` ask on each such kept merge);
    a commit that changes one, a passing re-run, or the person's "release" lets the cut go on;
    one waiting over 6h is raised once.
    Right after the cut and again before publishing, the tag's commit is read (`TagCommit`, `commits/refs/tags/<tag>` on GitHub); a tag at another commit (made outside the Hive) journals `release.refused` and alerts.
    The release stays a pre-release, its merges wait for the next one, and the alert tells the person to reject that tag's release-signing run, since the pre-release already started release.yml there, and then delete the release and its tag.
    A tag that can't be read at the cut is alerted and recorded as `tag_unread` in `hive.released`, and Publish waits until it reads.
    The Hive that releases the Hive doesn't wait for any of that to run its own release (channel `main`, `release/main.go`): it builds each `hive.released` commit from its host clone and installs it through the update path (waiting for running gates and checks, `.old` kept, failed starts rolled back, `fallback` op to go back at once), and every merge into it copies main's `modules/` into `.modules/` (`release.live`), so prompts and module files are live on merge.
12. Stalls (`improve/stalls.go`, every tick): a task stage, or a run no task owns (warden, tribunal seat, helper), that has gone longer without moving than `max(stall_floor_seconds, 3 × p90 of that stage's recent durations here)` gets its hung turn interrupted once (the core retries it on the same checkout), then is handed to the lead.
    Moving means the stage changed or the run started a turn or wrote output; parked, quiet and waiting runs and person-waiting tasks are left alone.
    Each decision is a `stall.saved` event with the limit and sample count, listed in the sweep's digest, and one `alert.raised` per thing, keyed so it updates in place.
13. Meeting (`work/meet.go`): runs meet where their work overlaps.
    The files a brief names that are in the clone (the person's files and `merge=union` docs aside) are its files;
    when a worker starts, its brief's "Working beside you" names every working run (quiet ones aside) whose brief names one of them too, each such run is told of it by message, and the new worker is told of any its brief missed (two starting at once, `Module.meeting`);
    the meeting is journaled (`work.met`).
    A conflict sent back with main names the merged tasks that changed the branch's files on main since its base, with their run to message while it lives, else their PR.
    The reply to a worker's `finish` lists the runs on its brief's files now.
    A finish account's `Helped-by: <run>` line credits help; stats counts worker-to-worker messages, the pairs met, those that talked, and the runs credited (dashboard: the status tree's all-time `peers` row).
    The problems board (`internal/mod/problems`) decays and adds up the same way: an open problem with no new word for a day leaves the prompts of runs that start (a sweep still sees it, and a module's standing report, `Problem.Standing`, never fades while it stands), and one two or more runs met ranks first, marked for a sweep to file.

## Modules that run a program

A module's `module.json` may name a program and the hooks it serves, so a door (a chat service, mail), voice or a resource lease can be added on top of the released binary instead of a fork of it:

```json
{"name": "door", "exec": ["./door", "--quiet"], "hooks": ["source", "channel"]}
```

The program can be in any language.
The daemon starts it from the module's folder (a first word starting with `.` is relative to it) and keeps it running: one that exits is started again after a pause that doubles from a second up to five minutes, and resets once it has run a minute.
Its output goes to `.hive/modules/<name>/module.log`.
The daemon journals `module.started`, `module.exited` and whatever the module reports.
Built-in modules stay in-process; the tick list is the registry's (`Registry.Builtin`), and the runner is one entry on it.

A manifest's `"schedule": {"<job>": "every 4h"}` names jobs the program serves, with their default schedules (config.json's `schedules` override them, and the control center lists and runs them like any job); a due one is journaled as `module.scheduled` (`module`, `job`), a workspace notice every module reads, so a schedule reaches a program the same way a hook does.
A program started with `go run` (the kb module's `dream/`, standard library only, since a module folder has no go.mod) builds into the person's own Go build cache (`GOCACHE`, else the user cache dir), not a fresh one under its HOME.

**The module calls the API; the daemon never calls the module.** Each start gets a fresh random token in `HIVE_MODULE_TOKEN`, with the API's address in `HIVE_API`.
The daemon keeps the token in memory only, and it dies with the process.
A request with a module's token reaches only the endpoints its hooks name, and anything else gets 403.
Its token also reaches only the projects the person granted the module (`.hive-source.json` `projects`, set from the Modules screen): an ask, a chat, a run or a task in another project is refused, `/api/events` leaves out their events, and anything with no project, such as the lead's chat, the person's asks and the lead's runs, needs a grant of `*`.
The only project-less events every module reads are the workspace notices: `hive.idle`, `hive.started`, `hive.stopped`, `alert.raised` and `module.*`.
A module's chat posts are tagged with its name, and the lead reads them as `Module <name>: …`.
The registry is the one list of which modules exist, what they serve and which projects they reach.
Hooks are read off the journal (`/api/events?after=<seq>`), so a module that was down catches up from where it left off and the daemon never waits on a module.
A module's read returns the first 2000 events after its cursor, so catching up never skips a gap.
Calls the other way would mean the daemon blocking on a slow program, a second listener per module and a second protocol.
A token must come as `Authorization: Bearer <token>`; any other form is refused.

| Hook | What it is for | API it may call |
|---|---|---|
| `source` | asks arrive from outside | `POST /api/ask`; the task's `from` is `<module>/<from it gave>`, so an ask carries where it came from |
| `channel` | the lead's conversation and run notices out; steer and stop lines in | `/api/chat` (read and post), `/api/events`, `/api/run/stop`, `/api/task/stop` |
| `after` | a step after a run or a merge ends | `/api/events` (`run.turn_ended`, `effect.merged`) |
| `status` | read the journal's events | `/api/events` |
| `idle` | act when nothing is running | `/api/events` (`hive.idle`, journaled when the last running or queued run ends) |
| `executor` | run turns elsewhere when this machine is strained, or the pace holds a worker back | none: the core calls it (below) |
| `checks` | run test checks elsewhere when the check line here is full | none: the core calls it (below) |
| `kb` | consolidate the knowledge base (the kb module's dream) | `GET /api/kb/notes` (the shared notes, practice and digests, the shipped practice docs, the hidden projects), `POST /api/kb/dream` (a pass: files to write or remove, each with the hash of what it read, checked like a filer's commit and committed at once; journaled as `kb.dreamed`) |
| (every module) | | `/api/ping`, `POST /api/module/report` (journaled as `module.reported`) |

**Two seams where the core calls the module.** An `executor` module (such as hive-dispatch, which runs turns on another machine the person owns over SSH) answers two commands of its program, run from its folder with its own `HOME` and the scrubbed environment, without `HIVE_API` or a token.
`<exec> room` exits 0 and prints `{"runner": "<name>"}` when it can take a turn now;
the core waits at most 5 seconds; a "no" stands for 15 and a yes for 5, so the work queue's question (`Runs.Room`, which keeps the yes) and the launch after it ask once;
each launch that gets a yes (`Runs.Executor`) spends it as it gets it, whether its turn then goes or not, so two launches in one window never go on one yes (the module counts each yes as one turn handed over).
When a turn's launch finds the machine strained (`Strained`), the local sandbox is an isolated one, and a running executor says it has room, the turn's spec is built exactly as for the local sandbox and handed over as WSL's is: `sandbox.Delegate` writes it to a temp file and starts `<exec> turn --spec <file> --runner <name>` detached.
The module deletes the file once read (it carries the turn's token), writes the turn's output to the spec's `stdout`, stops when its `stop_file` appears, and brings the turn's files back into the checkout, box and home before it exits; the core watches its process, drains the mailbox and ends the turn as for any other.
`run.turn_started` carries `where` (empty is this machine), shown on the run's row.
A test check (`test` or `retest`) of a commit off main's line goes the same way, to a running module that also lists the hook `checks` (`Runs.CheckExecutor`; a module with `executor` alone gets turns, never checks), run to its end (`Delegate.Run`): when the check line here would make it wait (the work module's pace pulled checks back, or every slot is taken) it takes a runner's room instead of a slot, and one already waiting asks every 30 seconds and leaves the line once a runner has room (`Slots.TakeOr`);
each room answer it is given is spent as it is given.
A `checks` module's `turn` must run the spec's command and bring its stdout back; the core wraps the command so it prints its own exit with a nonce as its last line and takes the verdict from that, never from `turn`'s exit code, and output without it is "can't run", never green.
It runs without this machine's shared Go module and build caches, so the runner fetches its own modules through its egress proxy (proxy.golang.org and sum.golang.org must be allowed there); never when the egress config grants a service on this machine; and its record carries `where`.
Main's own checks stay here, as they write the shared build cache.
The work queue lets one task a pass go past a strained machine, or past its pace, while an executor has room;
one started past the pace is an away run (`RunSpec.Away`): its turns go to an executor with room first, strained here or not, and run here when none has room, and it holds no worker slot here while its latest turn ran elsewhere (at most `max_workers`, or 4, are away at once);
a machine over `load_max` alone holds tasks as it does without an executor (from the workers' start on), since launch would run them here.
A manifest with `"command": true` gives `hive <name> <args...>`: the CLI runs its program with the arguments, attached to the terminal, from its folder with its own `HOME` and the scrubbed environment, only for a module it would run (any whose commit it can read).
A built-in command of the same name wins.

**Trust.** A module's program is not sandboxed: a door needs the network and the API on localhost, and a lease needs devices.
It runs as the person, so it can read anything the person can, including `.hive/daemon.json` (which holds the person's own API token) and the person's `gh` and `ssh` config on disk.
That is no more than an agent's run in home reaches (it runs as the hive user, with the user bus), so no review reads a module's code before it runs (the person, 10-30: "freely edit everything including the core with no process").
The scoped token and the scrubbed environment only keep a well-behaved module to its hooks; they don't stop a hostile one.
A module installed from git starts at the commit checked out, and an update moves the commit, so it stops and starts again at the new one.
A clone whose install failed is removed, and a folder with `.git` but no source record runs at the commit it holds.
A module the person put in `.modules/` by hand, with no `.git`, runs as it is, and so does a shipped module (the kb module's `dream/`).
The program never holds the forge credential: its environment drops forge and model tokens and the person's config folders, `HOME` is its own `.hive/modules/<name>/` (covered in every agent's `/hive/state`, so no run reads its login or log), and no scope pushes, merges or reads secrets, so effects stays the only holder.

**Against an in-process hook table.** The other way to build this is a hook table: the loop loads each listed module's file by path and calls its hooks in its own process, a door's hooks come from the registry, and a run gets only the modules its row grants.
Each credentialed relay stays in the effects process as a verb with a checked shape.
The Hive keeps two parts of that: the registry is the only list (the tick list and the grants both come from it), and the credential never leaves effects.
It parts ways on who calls whom.
An in-process hook table means every module is trusted code in the daemon, written in its language.
Here a module can be any program, so the daemon can't call into it safely or cheaply.
Instead the module pulls a scoped slice of the API and reads the journal, and a slow or dead module never holds the daemon up.
Built-in modules still run in-process, as a hook table would.

## Invariants (enforced by structure)

- A module program's environment carries no credential, and its token reaches only its hooks' endpoints; its review, not the token, keeps it from the person's files.
- Agents hold no credentials.
  Sandbox gets `CLAUDE_CODE_OAUTH_TOKEN` = short-lived access token only, read fresh per turn; each Codex turn gets a fresh `CODEX_HOME` made on the host side (`private/keys/<id>/codex`, mounted at `/hive/codex`), so nothing the agent planted steers the write; its `auth.json` holds the access token with the refresh token blanked and its `config.toml` the Hive's instructions (argv has a size cap).
  At turn end it is deleted except `sessions/`, kept in `private/runs/<id>/codex-sessions` for resume; folders the agent locked are opened first, a removal that still fails blocks the run, and daemon start deletes any left by a crash before a turn began.
  A bare run whose machine login has no readable credential file gets no token, so the harness's own host login works.
  Each turn goes on the least-used login of the pool (by the fullest of its usage windows still running, `Plan.Used`; the machine's own plus `.hive/logins/<harness>/<name>` from `hive login add`, which refuses a Codex API-key login since it has no access token to hand a turn) not parked at its usage limit, nor at one of the person's usage stops (`usage_stops` in config.json, `core.UsageStops`: a share of the week and of the five hours, none on a new install; a config from before the setting keeps this Hive's own 95% of the week and 90% of the five hours, `OldUsageStops`): a login stops taking turns there until that window resets, `Plan.ParkedUntil`;
  with every login of a pick there, its runs wait for the first reset, and `hive status` and the control center say the login is paused.
  Below the week's ceiling sits a reserve (`CheckReserve`, issue #983): every other role stops at 90% of a login's week, and only the checks and the leads (`Reserved`: reviewer, security, warden, tribunal, judge, lead) run on to 95%, so a change already written is still read, judged and merged.
  95% stays everyone's ceiling: the last 5% is the person's, as is the five-hour window's last tenth, which has no reserve.
  The host's own logins are renewed by the daemon (`Tokens.Watch`, every minute) in their last minutes (Claude Code renews only within five of the expiry, Codex only once the token is turned away): the harness is found by absolute path (PATH, then `~/.local/bin` and the other usual folders, since a service's PATH lacks them), and a renewal counts only if it exits 0 and the expiry moves.
  Its harness is also tried hourly with `--version`, so one that can't run is told hours ahead.
  Every failure is a `login.renew_failed` event and a daemon.log line, backs off 1 to 15 minutes, and the first of a run raises one alert (ref `login:<key>`) and a `login` ask with what to run; a later success, or an expiry that moves past the one it failed at (renewed by hand), replaces the alert and closes the ask.
  A restart reads an unresolved failure and its count back from the journal, so it isn't told afresh.
  A renewal turned away by the plan's usage limit (`harness.LooksLimited` on its output) is no failure: logging in again wouldn't change it, so it raises no alert or ask (and closes one an earlier failure left open); it is one `login.limited` event and the login is parked until the reset the harness names (`harness.LimitResets`, an hour when it names none), across a restart too.
  Only harnesses some route uses are watched (`Config.Uses`); a harness binary inside `.hive/` or `projects/`, which runs can write, is refused, and the harness's output is scrubbed of token-shaped strings before it reaches the journal.
  The control center lists each login's expiry and last renewal.
  Every login, the machine's own included, can carry a name the person gives or changes with `hive login name <login> <name>` (`.hive/logins/names.json`, by key; `setup.NameLogin`, a module's, not the core's): `hive status` and the control center show it beside the key (`cc2 (claude)`), while records, usage and folders keep the key, so a rename loses no history.
- Host git never runs in an agent's checkout (only in host clones and fresh gate clones; hooks disabled, fsck on fetch).
- The effects process pushes only `hive/*` branches (`PushInitial` only into an empty repo with a host-made commit); an agent may push to main itself on its own token, and the keep-history ruleset refuses a force-push or deletion of main.
- Merge requires nothing but the person's hold being off and the steward record (`Effects.Merge`): no test, read, approval or forge check (10-30).
  What follows on reads still holds where reads are used, at release.
  A read is bound to the diff its reviewer was given (`core.DiffID`: the change's own diff from its merge base with main, `--binary --full-index`, hunk positions and text files' blob-id lines left out; a binary file keeps its index line, since its delta is relative to the old blob), so it carries to a head with main merged in only while that own diff is unchanged;
  a merge whose conflict resolution touches the change's own files needs a new read.
  Any `.gitattributes` is high class, since it picks merge drivers.
  A high class needs two land runs since the last fix.
- Files the sandbox can write are read with `SafeRead` (no symlinks, same-file check); the host decides what to kill/clean from its own records.
- Nothing an agent requests runs outside the sandbox, except a script a warden or tribunal approved, run by the risk module in the daemon as exactly that digest-pinned script text (the pin covers the text, not files it calls); what is always the person's goes to the person (see [risk.md](risk.md)).
- Every state change is a journal event; restart = replay + reconcile; issue/PR/run creation is idempotent.
- A person's stop, retry or recheck stands over a step running on an older copy of the task (`work.steer`): the step's later saves are dropped, and it starts no worker, pushes nothing, posts no test or hold comment and doesn't merge once `overtaken`.
  The merge asks last of all, inside `core.Effects.Merge` just before the forge's merge call (`MergeRequest.Stale`), but not atomically with it: a stop that lands while the forge merges can't be refused, and the merge is then recorded, since the forge holds it.
  A worker started as a stop lands is stopped by whichever comes second, the step or the stop.
- One daemon per workspace: `hive daemon` takes an exclusive lock on `.hive/private/daemon.lock` (flock; LockFileEx on Windows) before it touches anything, and a second one exits, naming the pid, start and starter written there (`daemon.AlreadyRunning`), whatever started it: as soon as a daemon there answers, or after 30s.
  A holder that doesn't answer is on its way out (ping fails once shutdown begins) or still starting, so a plain start waits for its lock, and says in daemon.log which pid it waits on after 2s; a lock file it can't open names the file's owner (another user's launcher made it first).
  A command that finds no daemon has the Hive's user unit start one when it serves the workspace (matched on the cleaned, symlink-resolved `HIVE_HOME`), and starts one itself if the unit hasn't taken the lock within 8s (failed, at its start limit, or naming a moved binary).
  A daemon that starts beside the unit removes a leftover `hive.desktop`.
  A turn claim another daemon holds is journalled once per daemon as an `alert.raised` (ref `turn-claim`), and a turn started again after a daemon died mid-launch as one too.
  An update's restart (`--wait-for`) waits up to 2m for the daemon it replaces to let go, then ends that daemon (only that pid, and only while it still holds the lock) and takes the workspace, so a shutdown that hangs never leaves the Hive down; the replaced daemon itself exits 1m after its restart began whether or not its shutdown finished.
  A hand-off to systemd or to a renamed binary lets go first.
- One launcher: a command that finds no daemon waits for one that holds the lock but hasn't written `daemon.json` yet, and when the Hive's systemd user unit serves the workspace it has systemd start the unit instead of a detached daemon beside it; enabling the unit removes a desktop autostart left from a start without systemd.
- One process per turn: before a turn starts, the daemon claims it in `runs/<id>/host/turn.claim` (its pid and start, the turn, then the turn's process) under a lock on `turn.lock`; a claim for that turn or a later one by another live daemon makes it back off, and a dead daemon's claim is taken over only once the turn it started has ended (`core/claim.go`).
  Nor does a daemon start a run's next turn while its own last turn's process runs (alert ref `turn-live-<id>`): a `run.status` to idle or queued doesn't take a running run off running, so a second reopen 2 s after the first (a fix round sent twice, issue #1022) only queues its message; if the turn then finishes done, it ends idle with the finish cleared and the next turn hands the message over.
  A reopen with the text the running turn was handed, or already has waiting, does nothing: that turn is working on it, so its finish ends the run (issue #1157; `Run.Given`).
- A change to the Hive aimed at a gain never merges in parts: on a self project such a task opens no checkpoints (`noBankGain`, logged once), and a checkpoint opened while it was a fix is dropped at the recursive gate unjudged, so the gate judges its whole diff once its worker finishes (issue #759; unit `TestCheckpointRefusedForAGain`, `TestGainCheckpointDroppedFinalHeadGated`).

## Left alone on a shared machine

Settings in `.hive/config.json`; none of them stops or parks work.

- `turn_mb` (240): one memory budget paces the Hive ([priority.md](priority.md)).
  A turn, and a waiting brief's worker, starts while the Hive's cgroup limit (or the machine's memory, with none) leaves `turn_mb` for it and for each one starting beside it (`core.MemBudget`); a lead's, warden's or tribunal seat's turn never waits.
  Each tick steps the leads before the other runs, and leads run at a higher priority (nice levels).
- `disk_full_max` (40): on a saturated disk (IO pressure `full avg60` over it, `core.DiskBusy`) the work module starts no new worker while one works and runs one check at a time; below 0 is off.
- `max_output_tokens` (16000): sets `CLAUDE_CODE_MAX_OUTPUT_TOKENS` on each Claude turn, because a spend budget is checked only between turns and one reply can otherwise run long.
  0 leaves Claude's own default.
- `monthly_usd` (off): each turn's `cost_usd` is already in its `run.turn_ended` event; the runs fold it into a total per UTC month.
  Past the figure, the person is told once that month in the feed (`Person.Tell`, id `spend-YYYY-MM`) by `stats.SpendNotice` on the module tick list (the core keeps the total, `Runs.Spent`).
- `max_checks` (0 is cores/6, at least 2): the gate's test, score and dry-run checks and improve's measures (`Effects.RunCheck`) take a slot first (`core.Slots`);
  the rest wait in the order they asked, except that the work module ranks them (`Slots.Prioritize`): main's own measurement goes first (`MeasuringMain`, issue #1078): after a merge or at the start, main measured again for a fall, main's red tests run again before they hold a change, and the run of a red merge's failures on main just before it (issue #1713);
  it ranks urgent, so it passes even a check out of the one-hour patience that otherwise lets the oldest go first;
  then, while main is red, the check of a fix whose brief names one of main's failing tests (`mainFails`, kept by the testing stage from main's red run and dropped when it passes; urgent too, issue #1877: on 10-28 the red main's two fixes sat untested behind 29 other fixes' checks failing on the same red), then any other fix's (issue #838);
  within a rank the check expected to be shortest starts first (what its project's runs of its kind took, learnt by the line as each ends and weighed half and half with the runs before, kept in memory; one not known yet first), and one expected under 2 minutes (`SlotUse.Quick`) goes past even a check out of patience, which it delays by no more than that (issue #1933: hive-hwde's checks of seconds waited ~50 minutes behind ~30 hive checks), shown under **checks** in the control center and in `hive agent status`.
  The stall watcher times a check from when it got its slot.
  Each `effect.checked` records what the check's command read from and wrote to the disk (`disk_read`, `disk_wrote`, bytes, every run of it together: the rusage block counts of the host-side wait, `sandbox.Spec.Disk`; Linux only), so the checks that load a disk-bound machine can be named from the record.
  The pace sets the number of slots (`Slots.Resize`).
- Shared checks (`Effects.shared`): A check asked while an identical one (project, sha, kind, command and, for `RunCheckSince`, the main it is narrowed against) waits or runs takes no slot of its own: it waits for that one and gets its result, journaled once (issue #1880: every main sha was tested 4-5 times over, side by side, by the merge's measure, main's own test and the heads at main, each in its own slot and checkout; 115 of 623 test checks from 10-27 06:04 repeated a sha and kind already run).
  One asked after it ends runs again, so the score's readings in one sitting stay separate.
- `check_cache_max_mb` (8192): checks have room (`core/disk.go`).
  A check's home is `.hive/cache/homes/home-*`, its own and removed after, and holds its `TMPDIR` (`/hive/home/tmp`, which the sandbox also binds at `/tmp`);
  a synchronous sandboxed command (a check) dies with the daemon (Pdeathsig, as the unit keeps children across restarts for agents' turns) and whatever it left running in its process group ends when it returns;
  past this cap its stop file ends it as can't-run.
  A check runs for at most 30 minutes; one cut off there (`sandbox.ErrTimeout`) is can't-run too, marked `slow` (a slow check, not a red one: work holds the change and tries again, improve keeps or reverts nothing on it), and the next try of that commit and kind gets 60 (`checkLimit`).
  Records from before `slow` read the same from the `timed out` the core put first in their output.
  A Go project's check (on a Linux sandbox with its own paths) gets `.hive/cache/gomod` read-only at `/hive/gomod` with `GOMODCACHE`, `GOPROXY=off` and `GOFLAGS=-mod=readonly`, filled by the effects process with `go mod download` (`GOTOOLCHAIN=local`, `GOVCS=*:off`, `GOWORK=off`, no code of the commit's run) once per go.mod+go.sum (`.hive/cache/gomod-sums/<hash>`);
  a prefetch that fails leaves the check to fetch its own, as before;
  a check fetching its own (its go.mod+go.sum marked `.own`, or its second run after an offline miss) still gets the cache read-only as its first proxy (`GOPROXY=file:///hive/gomod/cache/download,https://proxy.golang.org,direct`).
  The build cache is shared only from main (`goBuild`): a check of a commit on main's first-parent line writes `.hive/cache/gobuild/<project>` (mounted read-write at `/hive/gobuild`, plain `GOCACHE`);
  a check of any other commit gets it read-only and reads it through `GOCACHEPROG=/hive/bin/hive gocacheprog /hive/gobuild /hive/home/gobuild` (`internal/gocache`), which serves main's entries and keeps what this check builds in its own home, so no commit's check can plant outputs another trusts.
  Before either, the clean checkout's tracked files and folders get times derived from their git object IDs (`stableTimes`), since go reuses a passed test only when every file it opened has the size and time it had: a check reuses every package build and passed test main's checks already made for the same code.
  A check that fails mentioning `GOCACHEPROG` runs once more without the shared build cache; the hourly sweep empties a project's build cache past 10 GB while no check uses it.
  A check whose output says the disk or a quota is full (`disk quota exceeded`, `no space left on device`, ENOSPC, EDQUOT) is can't-run, with one `disk` alert naming what uses the room until a check runs again, and it starts the `hive.clean` job (hourly and at start): agents' checkouts whose run ended and whose task is merged or done (stopped or blocked ones after 7 days; reviewers', and runs with no task, after 24 hours; leads' after 7 days), check homes this daemon isn't running (at once: only one daemon runs on a workspace, and the home's stop file is written first, which ends a check a dead daemon left), clean checkouts and prefetch folders over 3 hours old, dead sandboxes' runtime folders in `/dev/shm` (a live `sandbox-exec` holds a lock in its own) and specs no sandbox read in an hour, recursive gate runs a dead gate left untidy (`gate.Reap`, never the one in flight), the Hive's leftovers in the host's `/tmp` (`hive-*`, `TestSoftLayer*`; not `hive-sbx-*`, which the sandbox sweep keeps by their locks) and everything in the daemon's own temp folder once nothing in them has changed for 24 hours (`pruneTemp`), runs' own `/tmp` (`Runs.pruneTmp`: an ended or unknown run's at once, a waiting run's once quiet for 24 hours, never one a turn is using), the module cache past 20 GB while no check runs;
  An ended run's home is cut down to its harness's sessions (`.claude`, `.claude-open`, `.codex`, `.hive-open`, and its files) six hours after it ended, so its clones, venvs and build and package caches go (`Runs.pruneHomes`, marked `home-pruned` until the run moves again; 630 GB of them on 10-30).
When a place the Hive writes has under 5% of its room free, the clean takes temp leftovers and runs' `/tmp` quiet for 3 hours, not 24, and ended runs' homes 30 minutes after they ended, not six hours (`tight`).
The clean's line names the daemon's temp folder as a part of its own, and what no part names in `.hive/private` by its subfolder (`topOf`), so the space no part names is traced to the folder that holds it.
  then it measures the Hive's disk use and the room on the workspace's and temp filesystems for the dashboard (`State.Disk`).
  A task's checkouts also go the moment it is merged or done.
- Narrowed checks: a change's check (the work module's testing stage, `RunCheckSince`) runs only what the change can affect (#1161): when the test command is only `go vet ./...` and `go test [flags] ./...` steps, `internal/goscope` reads the clean checkout (parsing, no go command) and names the packages whose result can differ from the merge base's: a package whose build changed (a non-test file or an embedded file), each one importing it, each one whose tests import one, hold a changed file in their folder, or name a changed path (above their own folder, or in or below it where the path is there: `"."`, `"docs/core.md"`; a `"../.."` literal, a `filepath.Join("..", ...)` of literals, constants and local names; a path built by a test counts the built package's changes);
  go.mod, go.sum or vendor changes run everything.
  The check's output says what it ran and `Check.Scope` names it; main's own result (`mainTests`) is always a whole run, a whole pass of a commit stands over a later narrowed run (a whole red is rechecked, so a narrowed pass after it is the answer), go.mod is read only as a regular file of the checkout under 1 MB, and a narrowing the go command refuses (no Go files) runs everything.

## Sandbox (Linux / WSL)

Outer (`sandbox-exec`, host side): egress proxy on a unix socket in `/dev/shm`, stop-file watcher, starts init with `CLONE_NEWUSER|NEWNS|NEWPID|NEWNET|NEWIPC|NEWUTS` (uid 0 → person's uid).
Init (pid 1): tmpfs root; binds `/usr /bin /lib* /etc /opt /var /srv` read-only (nosuid, nodev, locked flags kept);
fresh `/proc`, minimal `/dev` (`/dev/shm` a tmpfs capped at 1 GB);
`/tmp` bound from the spec's `Tmp` folder on disk, emptied before the command and removed after, read-only folders in it too (a turn's is `.hive/private/runs/<id>/tmp`, removed again when the run ends in case a killed launcher left it; a check's its home's `tmp`), since a tmpfs is memory that can only be swapped;
with none, a tmpfs capped at 4 GB; the install of each host `python3`/`python`/`node`/`uv`/`go` that lives elsewhere, read-only at its own path (its install dir if it has a `lib/` beside `bin`, else the bin alone; never the home, a dot-dir right under it such as `~/.local` or `~/.cargo`, a dir holding the workspace, one of its covered folders (`Spec.Guard`, `Workspace.Guarded`) or a mount's host path (a python at /data/bin with the workspace under /data is bound as its bin alone), nor one inside a covered folder (`Spec.Cover`, `Workspace.Covered`, the same list the run spec hides, and every hidden mount); nothing at all when HOME is unset or can't be resolved, the spec has no guard, or a guarded or covered path is relative), with its dir on PATH after the `/hive/` dirs;
run mounts; tools at `/hive/bin` (one already on the inside PATH under a system dir, like a Node launcher, runs in place);
`pivot_root`; root remounted ro; `lo` up; forwarder 127.0.0.1:3128 → egress socket, and one per granted local address (`egress.local`) that relays a CONNECT to it over the same socket.
Command runs in a nested user namespace (no caps); init reaps every orphan reparented to it while it waits for the command, so none stays a zombie until the sandbox ends.
Not present: `/home`, `/mnt` (Windows drives), `/run` (sockets), WSL interop.
Egress refuses loopback/private/link-local/CGNAT/ULA and dials the IP it vetted;
with an `egress` list for the run's role it also refuses every host off the list and records each refusal and its reason in `turn-NNN.jsonl.egress`;
a granted loopback ip:port is dialled as that literal address, and a host:port granted to the run's project (`egress.projects`, `Spec.Outside`) passes the list on that port, a name only while it resolves to public addresses (macOS runs one proxy per command for the same).

## Gaps (v1)

- Risk module: tiers route a step to the worker, a warden, a tribunal or the person; approved scripts run in the daemon after a digest and ceiling check ([risk.md](risk.md)).
- macOS Seatbelt profile untested.
- Codex adapter: not yet run live with a real Codex login (the fake path, `hive fake-codex`, runs in e2e).
