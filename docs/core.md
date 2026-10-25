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
| Messaging and discovery: message, inbox, progress, finish, spawn, agents; the person's feed | `internal/core/ops.go`, `internal/core/person.go`, `internal/agentcli` (the agent's end) |
| The effects process (fx): holds every credential and acts for agents: git, the forge, checks, merge guarantees | `internal/core` (`effects.go`, `forge.go`, `git.go`, `tokens.go`, `selfgate.go`, `slots.go`) |
| The permissions ceiling and the sandbox | `internal/core/ceiling.go`, `internal/core/strain.go`, `internal/sandbox` |
| The module seam: how the core gains functionality (the registry and tick list, agent ops, the person's answers, scheduled jobs, module programs, an executor module handed a strained turn's spec, a module's own `hive <name>` command) | `internal/mod/modules`, `internal/mod/runner`, `Ops().Register`, `Person.Handle`, `Runs.Executor` and `sandbox.Delegate` |
| The host process and its wiring: the daemon, its control API, the entry point | `internal/daemon`, `internal/api`, `internal/app`, `cmd/hive` |

The executor hook is core because only launch knows a turn's spec and when the machine holds it; what runs the turn elsewhere (hive-dispatch's SSH runners) is the module's. `hive <name>` is the seam step 4 below asks for, for module programs.

The last row is core only as the place the skeleton runs: what a module adds to it (an API route, a CLI verb) is module material and moves out (step 4 below).

The packages measured as the core, read by `tests/core-size.sh`:

<!-- core: internal/core internal/sandbox internal/proc internal/agentcli internal/api internal/daemon internal/app internal/mod/modules internal/mod/runner cmd/hive -->

## Measured on every merge

`sh tests/core-size.sh` prints `core_lines` (non-test Go lines in the packages above), `go_lines` (every non-test Go line) and `binary_bytes` (the binary built as a release is: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`); `--lines` skips the build. The improve module runs it on each merge and at the start, in a clean checkout in the sandbox like the tests, and keeps what it printed with that merge's score (`Measurement.Size` in `.hive/scores/<project>.jsonl` and the `score.measured` event). The dashboard shows the newest under the project, with how much it moved since the merge before. It is shown, never judged: a merge isn't kept or reverted on its size.

A project names its own command as `"size"` in `.hive/project.json` (any command printing `name value` lines); a self project with none runs `tests/core-size.sh` while its tree has it.

On main as this page landed (2026-10-25): core 16,356 lines, every package 41,356, binary 16.4 MB (linux/amd64).

## What isn't core, and where it goes

Every other package is module material. Most are compiled-in modules already (`internal/mod/*`, ticked through the registry); "goes to" is the installable module (`modules/`) or client it belongs with.

| Package | Lines | Goes to |
|---|---|---|
| `internal/harness`, `internal/openagent`, `internal/fakeagent` | 726, 673, 863 | the agents module: harness adapters (Claude, Codex, open, fake), the open harness's agent loop, the scripted test agent. The core keeps only the `Harness` interface and a registry |
| `internal/mod/agents`, `internal/mod/models` | 105, 309 | the agents module: role prompts, the spawner, model routes |
| `internal/tui`, `internal/text` | 4,777, 36 | the control center, a client of the control API: it should import `internal/api` alone, not the core or modules |
| `internal/mod/release`, `internal/setup` | 1,508, 457 | a release/update module: releases, self-update, install, login, doctor, autostart |
| `internal/mod/improve`, `internal/gate`, `internal/demo` | 1,729, 2,894, 27 | the improve module: measure and keep/revert, sweeps, step-backs, the recursive gate and its benchmark project |
| `internal/mod/kb` | 919 | the kb module |
| `internal/mod/risk` | 1,016 | a risk module: wardens, tribunals, approved scripts. The ceiling itself stays core |
| `internal/mod/chat`, `internal/mod/feedback` | 578, 211 | the chat module |
| `internal/mod/work` | 3,741 | a work module: ask → issue → PR → review → merge. The core keeps runs, spawning and the merge guarantees it calls |
| `internal/mod/projects` | 320 | a projects module: adding and onboarding projects (the registry stays core) |
| `internal/mod/system` | 260 | the system module: schedules and wake-ups |
| `internal/mod/tools` | 411 | a tools module: runtimes agents install |
| `internal/mod/stats`, `internal/mod/report` | 338, 613 | the control center's figures; the opt-in sharing report |

Inside the core packages, these parts are module material too: harness specifics in `runs.go` (Codex homes and usage, open-model keys) and `tokens.go` (each harness's credential reader and renewal), `route.go` (model routes, open models), `ceiling.go`'s `Tier` and `Route` (who weighs a step), `forge_local.go` (a dev and test forge), `app/selfgate.go` (the gate's runner), the daemon's routes for chat, feedback, projects, release and work, and `cmd/hive`'s verbs for kb, improve, gate, report, share, model and update.

## The order to move them

Each move is one change with no behaviour change, so a regression is easy to see and to revert.

1. **Done: the monthly spend notice.** `Core.tellSpend` became `stats.SpendNotice` on the module tick list. The core keeps the total (`Runs.Spent`) and the person's feed; telling the person about it is the module's. This is the worked example: the core exposes state, and the module acts on it through the seam.
2. **Harness adapters** (open work: Codex, Cursor and free models). #488 lets a module declare a harness; next, the built-in adapters and their credential and usage code in `runs.go`, `tokens.go` and `route.go` move behind the same registry, so a new harness never touches the core.
3. **Risk routing**: `Tier` and `Route` go to `internal/mod/risk`; the core keeps `AlwaysPerson`, `CeilingHit` and `CeilingGuard`. `internal/mod/risk` is a root path, so this one waits for the person's release.
4. **Module routes and verbs**: the daemon and `cmd/hive` stop importing modules; each module registers its API routes and CLI verbs through the seam.
5. **The control center as a client**: `internal/tui` imports `internal/api` only.
6. **The gate's runner** (`app/selfgate.go`) joins `internal/gate` in the improve module.
7. **The local forge** leaves `internal/core` for a package of its own; the core keeps the `Forge` interface.
8. **Installable modules**: once a module reaches the core only through the seam, it can leave the binary for `modules/` (a module program, docs/architecture.md "Modules that run a program").
