# The core

What the core is, what isn't, and the order the rest moves out. The person's words (the essay, ihsan.cc/writing/the-hive; KB note "The Hive essay"):

> The key design principle of The Hive is simplicity and flexibility. At its core, the hive is a skeleton ... Maintaining a lightweight and simple core is critical to ensuring tight self-improvement loops and continued evolution as models progress.
>
> The core gains functionality through modules.

So the core is the skeleton and nothing else. **New functionality goes into a module, not the core, unless this page says it is core.** A change that grows the core says why here first (the worker and reviewer prompts carry this rule).

## What the core is

| Part | Where it lives today |
|---|---|
| Workspace and state: the folder, config, the project registry, restart-safe state | `internal/core` (`workspace.go`, `projects.go`, `core.go`) |
| The journal: every state change is an event | `internal/core/journal.go` |
| Runs and spawning: an agent turn in its own checkout, park/resume, crash retry; spawn | `internal/core/runs.go`, `internal/proc` |
| The mailbox and discovery: an op's request and answer, inbox, progress, finish, spawn, agents; a wake (`Runs.Send`); the person's feed | `internal/core/ops.go`, `internal/core/person.go`, `internal/agentcli` (the agent's end) |
| The effects process (fx): holds every credential and acts for agents: git, the forge, checks, merge guarantees | `internal/core` (`effects.go`, `forge.go`, `git.go`, `tokens.go`, `slots.go`) |
| The permissions ceiling and the sandbox | `internal/core/ceiling.go`, `internal/core/strain.go`, `internal/sandbox` |
| The module seam: how the core gains functionality (the registry and tick list, agent ops, the person's answers, scheduled jobs, module programs, an executor module handed a strained turn's spec, a module's say on whether a turn starts now, a module's own `hive <name>` command) | `internal/mod/modules`, `internal/mod/runner`, `Ops().Register`, `Person.Handle`, `Runs.Executor`, `Runs.Admit` and `sandbox.Delegate` |
| The host process and its wiring: the daemon, its control API, the entry point | `internal/daemon`, `internal/api`, `internal/app`, `cmd/hive` |


What a check sees is part of the merge guarantees, so the choice of its caches is core: `core/disk.go`'s `goBuild` and `stableTimes` (#920, about 80 lines) decide whether a check writes main's build cache or reads it read-only; the cache program itself (`internal/gocache`, `hive gocacheprog`) is outside the core. For the same reason, which packages a change's check runs is core: `RunCheckSince` and `narrow` in `core/effects.go` (#1161, about 50 lines) narrow a Go test command to the packages the change since main can affect; working out which they are (`internal/goscope`) is outside the core. Which modules a check sees is core the same way: `Effects.checkModules` (#2052, about 25 lines) mounts the installed modules read-only at `/hive/modules` (`$HIVE_MODULES`), as the project's agents see them, so a project's check runs a module's program as merged into the module's main (hive-hwde's `bin/hwde` for hifi-receiver-2's hwcheck) with no warden publishing a copy; an isolated project's check sees only the modules granted it (`Effects.Granted`, the modules registry's grants). A module project's score check also reads its module's records: `Effects.moduleRecords` (#2105, about 50 lines) copies the top-level `*.jsonl` of each installed module built from the project (`Effects.ModuleHomes`) into a folder laid out as the Hive's own and mounts it read-only at `HIVE_HOME` (`/hive/records`), so dispatch's score counts its `turns.jsonl`; nothing else of a module's HOME goes, so its key and tokens stay out. Which other projects a check sees is core for the same reason: `Effects.checkProjects` (#2126, about 20 lines) mounts the projects' clones read-only at `/hive/projects` (`$HIVE_PROJECTS`) with every hidden project but the checked one covered, as its agents see them, so a score built on another project's main (hive-fw's reads hifi-receiver-2's) measures the same in a check as in a turn; an isolated project's check sees none.

A score check's cache that outlives the measure is core for the same reason, since what a check may keep between runs decides what its verdict rests on:
`Effects.scoreCache` in `core/disk.go` (#2176, about 45 lines) mounts `cache/score/<project>` writable at `/hive/score-cache` (`$HIVE_SCORE_CACHE`) for a score check of a commit on main's first-parent line only, so a branch's commit can't leave an entry a later measure counts;
the improve module empties it (`Effects.ClearScoreCache`) when it files a revert and when a revert's merge is measured, and a folder past the check cache cap is emptied before the next measure.

Where the daemon's scratch files go is core too: `core/hosttmp.go` (#1914, about 100 lines) sets the daemon's `TMPDIR` before any module starts, and its leftovers are removed by the core's own clean job (`Prune`), beside the check homes it already removes.

What a merge rests on is core, so where a project's tests run is too: `ProjectConfig.ForgeTests` and its few lines in `Effects.Merge` (#1962, about 15 lines) let a project whose forge CI tests every PR name those checks, and the merge then rests on them passing on the exact head in place of a local run; the testing stage that skips the local run is the work module's, and the measure of a merge that takes their pass on main is the improve module's (#2087).

Sharing an identical check in flight is core for the same reason: `Effects.shared` in `core/effects.go` (#1880, about 30 lines) runs one check for every caller asking for the same project, sha, main, kind and command while it waits or runs, since only the check line knows what is in it.

`core/git.go`'s memo of pure git calls (`pureGit`, `gitMemo`, #2044, about 90 lines) is core because every host git call goes through `Git` there: a merge, diff or file read named by full commit ids gives the same answer each time, and on a loaded machine the repeats (a fifth of the work module's git processes) pushed its tests past their 25-minute limit.

The last row is core only as the place the skeleton runs: what a module adds to it (an API route, a CLI verb) is module material and moves out (step 4 below).

The packages measured as the core, read by `tests/core-size.sh`:

<!-- core: internal/core internal/sandbox internal/proc internal/agentcli internal/api internal/daemon internal/app internal/mod/modules internal/mod/runner cmd/hive -->

## Measured on every merge

`sh tests/core-size.sh` prints `core_lines` (non-test Go lines in the packages above), `go_lines` (every non-test Go line) and `binary_bytes` (the binary built as a release is: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`); `--lines` skips the build. The improve module runs it in a clean checkout in the sandbox like the tests, and keeps what it printed with that measure's score (`Measurement.Size` in `.hive/scores/<project>.jsonl` and the `score.measured` event). A merge runs it with `--lines`, a few seconds, so the release build doesn't hold a check slot on every merge; the start (every daemon start and Hive update) runs it whole, so `binary_bytes` moves per release. The dashboard shows each figure's newest under the project, with how much it moved since the one before, and flags the measures since the last size that recorded none (the measure's note says why), so the tracking can't quietly stop. It is shown, never judged: a merge isn't kept or reverted on its size.

A project names its own command as `"size"` in `.hive/project.json` (any command printing `name value` lines); a self project with none runs `tests/core-size.sh` while its tree has it. Like `test` and `score`, it comes from the merged tree and runs only in the sandbox; `.hive/project.json` is a protected path as a whole, so a change to it, `size` included, waits for the person's approval to merge. If the measure ever runs outside the sandbox, this command must be weighed like the others.

The current figures are on the dashboard and in the scores file, or run the script; this page keeps none, so it can't go stale.

## What isn't core, and where it goes

Every other package is module material. Most are compiled-in modules already (`internal/mod/*`, ticked through the registry); "goes to" is the installable module (`modules/`) or client it belongs with.

| Package | Goes to |
|---|---|
| `internal/harness`, `internal/openagent`, `internal/fakeagent` | the agents module: harness adapters (Claude, Codex, open, fake), the open harness's agent loop, the scripted test agent. The core keeps only the `Harness` interface and a registry |
| `internal/mod/agents`, `internal/mod/models` | the agents module: role prompts, the spawner, model routes |
| `internal/tui`, `internal/text` | the control center, a client of the control API: it should import `internal/api` alone, not the core or modules |
| `internal/mod/ui` | with the control center: the bus a client (the deck) steers it through, `/api/ui` (docs/ui-api.md) |
| `internal/mod/release`, `internal/setup` | a release/update module: releases, self-update, install, login, doctor, autostart |
| `internal/mod/improve`, `internal/demo` | the improve module: measure and keep/revert, sweeps, step-backs, and the benchmark project |
| `internal/mod/kb` | the kb module |
| `internal/mod/risk` | a risk module: wardens, tribunals, approved scripts. The ceiling itself stays core |
| `internal/mod/board` | the board module: messaging. Every message is a post on one public board, in threads; agents join threads and search the board, and a post wakes only the runs it names, with where to read (#2235). It left the core whole: the `message` op, who an address means and the routers |
| `internal/mod/chat`, `internal/mod/feedback`, `internal/mod/problems`, `internal/mod/standup` | the chat module |
| `internal/mod/work` | a work module: ask → issue → PR → review → merge. The core keeps runs, spawning and the merge guarantees it calls |
| `internal/mod/projects` | a projects module: adding and onboarding projects (the registry stays core) |
| `internal/mod/system` | the system module: schedules and wake-ups |
| `internal/mod/tools` | a tools module: runtimes agents install |
| `internal/mod/stats`, `internal/mod/report` | the control center's figures; the opt-in sharing report |

Inside the core packages, these parts are module material too: harness specifics in `runs.go` (Codex homes and usage, open-model keys) and `tokens.go` (each harness's credential reader and renewal), `route.go` (model routes, open models), `ceiling.go`'s `Tier` and `Route` (who weighs a step), `forge_local.go` (a dev and test forge), `app/selfgate.go` (the gate's runner), the daemon's routes for chat, feedback, projects, release and work, and `cmd/hive`'s verbs for kb, improve, gate, report, share, model and update.

## Where it goes: the minimal core (pivot, 10-30)

The person, 2026-10-30: "they can freely edit everything including the core with no process"; "EVERYTHING is a module, including messaging. So all messages are public on a board and agents can add themselves"; "they dont read anything but they find whats needed"; "they get given minimal stuff".
Issue #2200 takes that in two steps.

Step 1, no process for any change, is on main: a worker's finish merges at once, and any agent may push to main itself ([status.md](status.md)).
What it left behind is deleted rather than kept switched off: the module review (the runner's read before a git module's program starts, `modules.Review`, `/api/module/review`, the `module-reviewer` prompt) and the lead-held draft wait in the work module (`DraftSince`, `DraftWhy`, `reviveDrafts`).
Kept, because they are the hard limits and the way back, not process: the warden's read of a change to the limits code (`LimitsHeld`), the publish scan, the person's `hold_merges`, keep-history on main, improve's measure-and-revert, H0's fallback build and the nightly backup.
The release's security read stays while it gates what goes out to other machines, which is outward, not a change inside the Hive.

Step 2 is the target for the core, and it is smaller than the table above:
- **Sessions within the spend cap**: start, park, resume and stop an agent's session in its own checkout, and refuse one past the cap (`Runs`, `Runs.Spent`, `internal/proc`).
- **The wardens at the edge**: the sandbox and the permissions ceiling, and the effects process that holds every credential and sends a hard-limit step to a warden (`ceiling.go`, `internal/sandbox`, the credential half of `effects.go`).
- **The event log**: every state change is a journal event (`journal.go`), which every module and agent reads.

Everything else is a module, messaging included:
- **Messaging is a board.** Every message goes to one public board, kept as events; an agent adds itself to a thread, and nothing is pushed into its context. An agent finds what it needs by searching the board, the KB and the log; a mention only wakes it. `hive agent message`, the inbox, the chats and the person's feed become this module's ops, and `ops.go`'s messaging and `person.go` leave the core.
Done in part (#2235): `internal/mod/board` holds the `message` op and who an address means (the routers, "lead", "parent"), and adds `post`, `board`, `join` and `leave`; a post wakes only the runs it names, with where to read and the thread's first line, never the text.
Still in the core: the inbox (`Run.Pending`, which a wake fills) and `person.go`; the chats are the chat module's, and their messages to and from the person aren't on the board yet.
- **Starting context is minimal**: a short orientation and the brief; the rest is found, not handed over (the agents module's prompts).
- **Landing a change is the agent's**: with nothing waiting before a merge, the work module's pipeline (ask, issue, PR, stages) is one way to land among others, not core; the merge guarantees left in `Effects.Merge` are the hard limits above.

Each move deletes first and rewrites after: the old path goes in the same change that its replacement lands, so the core never carries both.

## The order to move them

Each move is one change with no behaviour change, so a regression is easy to see and to revert.

1. **Done: the monthly spend notice.** `Core.tellSpend` became `stats.SpendNotice` on the module tick list. The core keeps the total (`Runs.Spent`) and the person's feed; telling the person about it is the module's. This is the worked example: the core exposes state, and the module acts on it through the seam.
2. **Harness adapters** (open work: Codex, Cursor and free models). #488 lets a module declare a harness; next, the built-in adapters and their credential and usage code in `runs.go`, `tokens.go` and `route.go` move behind the same registry, so a new harness never touches the core.
3. **Risk routing**: `Tier` and `Route` go to `internal/mod/risk`; the core keeps `AlwaysPerson`, `CeilingHit` and `CeilingGuard`. `internal/mod/risk` is a root path, so this one waits for the person's release.
4. **Module routes and verbs**: the daemon and `cmd/hive` stop importing modules; each module registers its API routes and CLI verbs through the seam.
5. **The control center as a client**: `internal/tui` imports `internal/api` only.
6. **The local forge** leaves `internal/core` for a package of its own; the core keeps the `Forge` interface.
7. **Installable modules**: once a module reaches the core only through the seam, it can leave the binary for `modules/` (a module program, docs/architecture.md "Modules that run a program").
