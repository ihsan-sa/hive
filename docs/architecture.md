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
| `internal/mod/*` | Default modules: `agents` (prompts, spawner), `chat`, `work` (ask → merge), `improve` (measure, keep/revert, sweep), `kb` (notes, the record filed from the journal, search), `system` (schedules, wake-ups), `risk` (requests past the sandbox → warden, tribunal or person), `projects`, `modules` (registry, installs, updates), `release` (self-update, self-release). |
| `internal/app` | Wires core + modules; tick loop. |
| `internal/daemon` | `hive daemon`, control API (127.0.0.1, bearer token in `.hive/daemon.json`, 0600). |
| `internal/api` | API types + client. |
| `internal/tui` | Control center (Bubble Tea): Chats, Dashboard, Waiting, Modules, Settings. |
| `internal/setup` | First run, `hive login`, `hive doctor`, autostart (HKCU Run / systemd user / LaunchAgent). |
| `modules/` | Default module content (orientation + role prompts, manifests), embedded and written to `.modules/` on first run. |
| `internal/demo` | Demo project template (textkit: tests + score). |

## Folder on disk (`~/hive`)

```
.hive/      events.jsonl, config.json, daemon.json, chats/*.jsonl, scores/*.jsonl,
            runs/<id>/{host,ro,box,home}, kb/ (git repo of notes; record/ filed from the journal, untracked), gates/, checks/, improve/sweeps/
.modules/   agents, chat, kb, improve, system, + installed (each with .hive-source.json)
projects/<name>/             host clone (effects process only)
projects/<name>/.checkouts/  agents' own checkouts (excluded from the clone)
```

Run dir: `host/` (turn output, prompts, stop file; never mounted on its own), `ro/` (system prompt, brief, inputs; mounted read-only at `/hive/run`), `box/` (mailbox + notes; rw at `/hive/box`), `home/` (rw `$HOME`). Every run but an isolated project's also gets the whole `.hive` read-only at `/hive/state` (`$HIVE_STATE`), so agents read one another's runs, chats and records; `logins/` and `daemon.json` are covered there (empty tmpfs, `/dev/null`; a Seatbelt deny on macOS), and a turn's spec, which carries its token, is written to the temp dir, not beside its output. Also covered is everything an isolated project leaves in the shared Hive: its clone under `/hive/projects`, its runs' folders and chats, its `checks/`, `gates/`, `risk/` and `kb/isolated/` folders, its runs' scratch folders, its scores, and while any project is isolated the whole journal, since one file holds every project's events (`Runs.hideIsolated`); an isolated run sees no other isolated project's notes. No agent outside an isolated project can spawn into it, file work in it, message or list its agents or see it in `status`, and its agents reach no other project (`Runs.Walled`, judged from the core's record of each run). Records from before these folders (old notes, `risk/<id>`, `checks/<id>.log`) move into them on start, and when notes moved, the knowledge base's git history starts afresh and the filer's old clones go, so the old text can't be read back from git (a clone of the knowledge base made before shares no history with it after). A run or chat that starts mid-turn shows until that agent's next turn, and a bare run (no sandbox) can't cover anything.

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
11. Stalls (`improve/stalls.go`, every tick): a task stage, or a run no task owns (warden, tribunal seat, helper), that has gone longer without moving than `max(stall_floor_seconds, 3 × p90 of that stage's recent durations here)` gets its hung turn interrupted once (the core retries it on the same checkout), then is handed to the lead. Moving means the stage changed or the run started a turn or wrote output; parked, quiet and waiting runs and person-waiting tasks are left alone. Each decision is a `stall.saved` event with the limit and sample count, listed in the sweep's digest, and one `alert.raised` per thing, keyed so it updates in place.

## Invariants (enforced by structure)

- Agents hold no credentials. Sandbox gets `CLAUDE_CODE_OAUTH_TOKEN` = short-lived access token only, read fresh per turn; each Codex turn gets a fresh `CODEX_HOME` made on the host side (`host/codex`, mounted at `/hive/codex`), so nothing the agent planted steers the write; its `auth.json` holds the access token with the refresh token blanked and its `config.toml` the Hive's instructions (argv has a size cap). At turn end it is deleted except `sessions/`, kept in `host/codex-sessions` for resume; folders the agent locked are opened first, a removal that still fails blocks the run, and daemon start deletes any left by a crash before a turn began. A bare run whose machine login has no readable credential file gets no token, so the harness's own host login works. Each turn goes on the least-used login of the pool (the machine's own plus `.hive/logins/<harness>/<name>` from `hive login add`, which refuses a Codex API-key login since it has no access token to hand a turn) not parked at its usage limit.
- Host git never runs in an agent's checkout (only in host clones and fresh gate clones; hooks disabled, fsck on fetch).
- Only `hive/*` branches are pushed; main changes only by merge; `PushInitial` only into an empty repo with a host-made commit.
- Merge requires: host-recorded passing test on that sha + (small | land verdict on that sha from a core-started read-only reviewer | host-made update of a reviewed sha | host-made revert/setup) + an approval if protected paths changed (the person's at autonomy 1, else a warden's; `risk.Weigh`). For a self project or a high-class path the read must be on that exact sha (nothing carries), and a high class needs two land runs since the last fix.
- Files the sandbox can write are read with `SafeRead` (no symlinks, same-file check); the host decides what to kill/clean from its own records.
- Nothing an agent requests runs outside the sandbox, except a script a warden or tribunal approved, run by the risk module in the daemon as exactly that digest-pinned script; what is always the person's goes to the person (see [risk.md](risk.md)).
- Every state change is a journal event; restart = replay + reconcile; issue/PR/run creation is idempotent.

## Left alone on a shared machine

Settings in `.hive/config.json`; none of them stops or parks work.

- `max_output_tokens` (16000): sets `CLAUDE_CODE_MAX_OUTPUT_TOKENS` on each Claude turn, because a spend budget is checked only between turns and one reply can otherwise run long. 0 leaves Claude's own default.
- `monthly_usd` (off): each turn's `cost_usd` is already in its `run.turn_ended` event; the runs fold it into a total per UTC month. Past the figure, the person gets one `told` ask for that month (id `spend-YYYY-MM`).
- `mem_floor_mb` (1024), `mem_pressure_max` (10), `cpu_pressure_max` (40): before a turn starts, Linux's `MemAvailable` and memory and CPU pressure (`/proc/pressure/memory` and `cpu`, some avg10) are read. Past any limit the run waits, keeping its status, backoff and nudges, with the reason shown in the control center (`run.held`), and starts on the first tick after the machine recovers. Where those files don't exist (macOS, Windows) nothing is held; 0 turns a check off.

## Sandbox (Linux / WSL)

Outer (`sandbox-exec`, host side): egress proxy on a unix socket in `/dev/shm`, stop-file watcher, starts init with `CLONE_NEWUSER|NEWNS|NEWPID|NEWNET|NEWIPC|NEWUTS` (uid 0 → person's uid). Init (pid 1): tmpfs root; binds `/usr /bin /lib* /etc /opt /var /srv` read-only (nosuid, nodev, locked flags kept); fresh `/proc`, `/tmp`, minimal `/dev`; run mounts; tools at `/hive/bin` (one already on the inside PATH under a system dir, like a Node launcher, runs in place); `pivot_root`; root remounted ro; `lo` up; forwarder 127.0.0.1:3128 → egress socket. Command runs in a nested user namespace (no caps). Not present: `/home`, `/mnt` (Windows drives), `/run` (sockets), WSL interop. Egress refuses loopback/private/link-local/CGNAT/ULA and dials the IP it vetted; with an `egress` list for the run's role it also refuses every host off the list and records each refusal in `turn-NNN.jsonl.egress` (macOS runs one proxy per command for the same).

## Gaps (v1)

- Risk module: tiers route a step to the worker, a warden, a tribunal or the person; approved scripts run in the daemon after a digest and ceiling check ([risk.md](risk.md)).
- macOS Seatbelt profile untested.
- Codex adapter: not yet run live with a real Codex login.
