# Architecture

One Go binary, `hive`. A small core plus default modules; the daemon runs both and serves the control center over localhost.

## Packages

| Package | Role |
|---|---|
| `internal/core` | **The core.** Journal (append-only event log), workspace + config, runs (agent turns, park/resume, crash retry, reattach), mailbox ops, effects process (git/forge, commit extraction, gate guarantees), project registry, token source, person queue, ceiling notes. ~3k lines. |
| `internal/sandbox` | Linux namespaces (`sandbox-exec` outer + `sandbox-init` pid 1), WSL launcher (Windows), Seatbelt (macOS, untested), bare (opt-in). Egress proxy. |
| `internal/harness` | Adapter seam. Claude Code (`claude -p --output-format stream-json`), Codex (`codex exec --json`, in its own read-only or workspace-write sandbox inside ours), fake (scripted). |
| `internal/proc` | Detached processes; liveness by pid + start time. |
| `internal/agentcli` | `hive agent <op>`: what agents call from inside the sandbox. |
| `internal/fakeagent` | `hive fake-agent`: scripted agent for tests and dry runs (FAKE:* keywords). |
| `internal/mod/*` | Default modules: `agents` (prompts, spawner), `chat`, `work` (ask → merge), `improve` (measure, keep/revert, sweep), `kb`, `system` (schedules, wake-ups), `risk` (requests past the sandbox → warden, tribunal or person), `projects`, `modules` (registry, installs, updates, the tick list), `runner` (modules that carry a program), `release` (self-update, self-release). |
| `internal/app` | Wires core + modules; the tick loop steps the core, then each module the registry lists. |
| `internal/daemon` | `hive daemon`, control API (127.0.0.1, bearer token in `.hive/daemon.json`, 0600). |
| `internal/api` | API types + client. |
| `internal/tui` | Control center (Bubble Tea): Chats, Dashboard, Waiting, Modules, Settings. |
| `internal/setup` | First run, `hive login`, `hive doctor`, autostart (HKCU Run / systemd user / LaunchAgent). |
| `modules/` | Default module content (orientation + role prompts, manifests), embedded and written to `.modules/` on first run. |
| `internal/demo` | Demo project template (textkit: tests + score). |

## Folder on disk (`~/hive`)

```
.hive/      events.jsonl, config.json, daemon.json, chats/*.jsonl, scores/*.jsonl,
            runs/<id>/{host,ro,box,home}, kb/ (git repo), gates/, checks/, improve/sweeps/
.modules/   agents, chat, kb, improve, system, + installed (each with .hive-source.json)
projects/<name>/             host clone (effects process only)
projects/<name>/.checkouts/  agents' own checkouts (excluded from the clone)
```

Run dir: `host/` (turn output, prompts, stop file; never mounted on its own), `ro/` (system prompt, brief, inputs; mounted read-only at `/hive/run`), `box/` (mailbox + notes; rw at `/hive/box`), `home/` (rw `$HOME`). Every run but an isolated project's also gets the whole `.hive` read-only at `/hive/state` (`$HIVE_STATE`), so agents read one another's runs, chats and records; `logins/` and `daemon.json` are covered there (empty tmpfs, `/dev/null`; a Seatbelt deny on macOS), and a turn's spec, which carries its token, is written to the temp dir, not beside its output.

## Data flow: one ask

1. Person → chat → lead run (persistent Claude session, resumed per message).
2. Lead → `hive agent brief` → `task.saved` (stage `new`).
3. work.Tick: issue (idempotent by body marker) → worker run `w-<task>` in a fresh checkout at main → worker `finish`.
4. Effects.Extract: `git bundle` made **inside the sandbox**, fetched into the host clone with fsck, must descend from the recorded base → push `hive/<task>` → PR (idempotent by head branch).
5. Effects.RunCheck: tests on the exact sha in a fresh clean clone, sandboxed; command read from **main's** `.hive/project.json`.
6. Small (docs-only, ≤40 lines, no protected or high paths, not a self project) → merge. Else reviewer run (read-only tools, JSON-schema verdict) bound to the sha; a change to a `high` or `root` path in `.hive/project.json` needs a second land from a separate reviewer run on the same sha.
7. Fix verdict / red tests → same worker, same checkout, bounded rounds (default 3) → stop with PR open. A host-made revert has no worker: its reviewer gets a revert brief, and findings stop it with the PR open.
8. Merge: main moved → host merges main into the branch (host-made commit; verdict carries) → retest. `gh pr merge --squash --match-head-commit <sha>`.
9. improve: measure merge sha (tests + score), compare with best kept; worse → revert task (host-made revert + lessons line) → PR → merge; original PR commented.
10. release (self projects, one at a time per project): on a kept merge, list the merges after the one the last release was cut at, up to this one, with the reviewer runs that read each exact head; a merge a newer release already covers is refused with an alert; a head with no read or a change to a `root` path waits for the person's "release" (approval kind `release`, kept apart from a `merge` approval of the same sha), and a newer hold supersedes the older ask; dry run; release with that list in its notes.

## Modules that run a program

A module's `module.json` may name a program and the hooks it serves, so a door (a chat service, mail), voice or a resource lease can be added on top of the released binary instead of a fork of it:

```json
{"name": "door", "exec": ["./door", "--quiet"], "hooks": ["source", "channel"]}
```

The program can be in any language. The daemon starts it from the module's folder (a first word starting with `.` is relative to it) and keeps it running: one that exits is started again after a pause that doubles from a second up to five minutes, and resets once it has run a minute. Its output goes to `.hive/modules/<name>/module.log`. The daemon journals `module.started`, `module.exited` and whatever the module reports. Built-in modules stay in-process; the tick list is the registry's (`Registry.Builtin`), and the runner is one entry on it.

**The module calls the API; the daemon never calls the module.** Each start gets a fresh random token in `HIVE_MODULE_TOKEN`, with the API's address in `HIVE_API`. The daemon keeps the token in memory only, and it dies with the process. A request with a module's token reaches only the endpoints its hooks name, and anything else gets 403. Its token also reaches only the projects the person granted the module (`.hive-source.json` `projects`, set from the Modules screen): an ask, a chat, a run or a task in another project is refused, `/api/events` leaves out their events, and anything with no project, such as the lead's chat, the person's asks and the lead's runs, needs a grant of `*`. The only project-less events every module reads are the workspace notices: `hive.idle`, `hive.started`, `hive.stopped`, `alert.raised` and `module.*`. A module's chat posts are tagged with its name, and the lead reads them as `Module <name>: …`. The registry is the one list of which modules exist, what they serve and which projects they reach. Hooks are read off the journal (`/api/events?after=<seq>`), so a module that was down catches up from where it left off and the daemon never waits on a module. A module's read returns the first 2000 events after its cursor, so catching up never skips a gap. Calls the other way would mean the daemon blocking on a slow program, a second listener per module and a second protocol. A token must come as `Authorization: Bearer <token>`; any other form is refused.

| Hook | What it is for | API it may call |
|---|---|---|
| `source` | asks arrive from outside | `POST /api/ask`; the task's `from` is `<module>/<from it gave>`, so an ask carries where it came from |
| `channel` | the lead's conversation and run notices out; steer and stop lines in | `/api/chat` (read and post), `/api/events`, `/api/run/stop`, `/api/task/stop` |
| `after` | a step after a run or a merge ends | `/api/events` (`run.turn_ended`, `effect.merged`) |
| `status` | read the journal's events | `/api/events` |
| `idle` | act when nothing is running | `/api/events` (`hive.idle`, journaled when the last running or queued run ends) |
| (every module) | | `/api/ping`, `POST /api/module/report` (journaled as `module.reported`) |

**Trust.** A module's program is not sandboxed: a door needs the network and the API on localhost, and a lease needs devices. It runs as the person, so it can read anything the person can, including `.hive/daemon.json` (which holds the person's own API token) and the person's `gh` and `ssh` config on disk. The review gate below is therefore the trust boundary. The scoped token and the scrubbed environment only keep a well-behaved module to its hooks; they don't stop a hostile one. A module installed from git is cloned only on the person's "install", and its program starts only once a read-only review run (`module-reviewer`, scratch copy of its files, structured `approve`/`decline`) has read it at the commit checked out, and the brief names that commit. An update moves the commit, so it stops and is read again before it runs. A decline raises an alert and the program stays stopped. A review that ends with no vote (it failed or crashed) is not a decline: it runs again, up to three times in one life of the daemon, then raises one alert and waits, and restarting the Hive reads it again. A clone whose install failed is removed, and a folder with `.git` but no source record is read like any other git module. A module the person put in `.modules/` by hand, with no `.git`, runs as it is. The program never holds the forge credential: its environment drops forge and model tokens and the person's config folders, `HOME` is its own `.hive/modules/<name>/`, and no scope pushes, merges or reads secrets, so effects stays the only holder.

**Against the box's Python dispatch.** Dispatch's held plan for the same idea is a hook table: the loop loads each `modules.txt` row's file by path and calls its hooks in its own process, the door's table comes from the registry, and a member's run gets only the modules its row grants. Each credentialed relay stays in the effects process as a verb with a checked shape. The Hive keeps two parts of that: the registry is the only list (the tick list and the grants both come from it), and the credential never leaves effects. It parts ways on who calls whom. An in-process hook table means every module is trusted code in the daemon, written in its language. Here a module can be any program, so the daemon can't call into it safely or cheaply. Instead the module pulls a scoped slice of the API and reads the journal, and a slow or dead module never holds the daemon up. Built-in modules still run in-process, like dispatch's table.

## Invariants (enforced by structure)

- A module program's environment carries no credential, and its token reaches only its hooks' endpoints; its review, not the token, keeps it from the person's files.
- Agents hold no credentials. Sandbox gets `CLAUDE_CODE_OAUTH_TOKEN` = short-lived access token only, read fresh per turn; each Codex turn gets a fresh `CODEX_HOME` made on the host side (`host/codex`, mounted at `/hive/codex`), so nothing the agent planted steers the write; its `auth.json` holds the access token with the refresh token blanked and its `config.toml` the Hive's instructions (argv has a size cap). At turn end it is deleted except `sessions/`, kept in `host/codex-sessions` for resume. A bare run whose machine login has no readable credential file gets no token, so the harness's own host login works. Each turn goes on the least-used login of the pool (the machine's own plus `.hive/logins/<harness>/<name>` from `hive login add`) not parked at its usage limit.
- Host git never runs in an agent's checkout (only in host clones and fresh gate clones; hooks disabled, fsck on fetch).
- Only `hive/*` branches are pushed; main changes only by merge; `PushInitial` only into an empty repo with a host-made commit.
- Merge requires: host-recorded passing test on that sha + (small | land verdict on that sha from a core-started read-only reviewer | host-made update of a reviewed sha | host-made revert/setup) + person approval if protected paths changed. For a self project or a high-class path the read must be on that exact sha (nothing carries), and a high class needs two land runs since the last fix.
- Files the sandbox can write are read with `SafeRead` (no symlinks, same-file check); the host decides what to kill/clean from its own records.
- Nothing an agent requests runs outside the sandbox, except a script a warden or tribunal approved, run by the risk module in the daemon as exactly that digest-pinned script; what is always the person's goes to the person (see [risk.md](risk.md)).
- Every state change is a journal event; restart = replay + reconcile; issue/PR/run creation is idempotent.

## Left alone on a shared machine

Settings in `.hive/config.json`; none of them stops or parks work.

- `max_output_tokens` (16000): sets `CLAUDE_CODE_MAX_OUTPUT_TOKENS` on each Claude turn, because a spend budget is checked only between turns and one reply can otherwise run long. 0 leaves Claude's own default.
- `monthly_usd` (off): each turn's `cost_usd` is already in its `run.turn_ended` event; the runs fold it into a total per UTC month. Past the figure, the person gets one `told` ask for that month (id `spend-YYYY-MM`).
- `mem_floor_mb` (1024), `pressure_max` (40): before a turn starts, Linux's `MemAvailable` and memory pressure (`/proc/pressure/memory`, some avg10) are read. Past either floor the run stays queued with the reason shown in the control center and starts on the first tick after the machine recovers. Where those files don't exist (macOS, Windows) nothing is held; 0 turns a check off.

## Sandbox (Linux / WSL)

Outer (`sandbox-exec`, host side): egress proxy on a unix socket in `/dev/shm`, stop-file watcher, starts init with `CLONE_NEWUSER|NEWNS|NEWPID|NEWNET|NEWIPC|NEWUTS` (uid 0 → person's uid). Init (pid 1): tmpfs root; binds `/usr /bin /lib* /etc /opt /var /srv` read-only (nosuid, nodev, locked flags kept); fresh `/proc`, `/tmp`, minimal `/dev`; run mounts; tools at `/hive/bin` (one already on the inside PATH under a system dir, like a Node launcher, runs in place); `pivot_root`; root remounted ro; `lo` up; forwarder 127.0.0.1:3128 → egress socket. Command runs in a nested user namespace (no caps). Not present: `/home`, `/mnt` (Windows drives), `/run` (sockets), WSL interop. Egress refuses loopback/private/link-local/CGNAT/ULA and dials the IP it vetted; with an `egress` list for the run's role it also refuses every host off the list and records each refusal in `turn-NNN.jsonl.egress` (macOS runs one proxy per command for the same).

## Gaps (v1)

- Risk module: tiers route a step to the worker, a warden, a tribunal or the person; approved scripts run in the daemon after a digest and ceiling check ([risk.md](risk.md)).
- macOS Seatbelt profile untested.
- Codex adapter: not yet run live with a real Codex login.
