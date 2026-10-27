# The Hive

A small team of AI agents that lives in a folder and works on your projects. Built from `docs/spec.md` (the owner's spec). One Go binary: `hive`.

## Commands

```
go build -o hive ./cmd/hive                      # build (Go 1.27; local SDK: ~/sdk/go1.27.3)
GOOS=linux GOARCH=amd64 go build -o hive-linux-amd64 ./cmd/hive   # needed next to hive.exe on Windows (WSL sandbox)
go vet ./... && go test -short ./...             # unit tests
go test ./internal/e2e/ -timeout 30m             # end to end: real daemon, fake harness, local forge, bare runs
HIVE_E2E_SANDBOX=auto go test ./internal/e2e/    # same, inside the platform sandbox
HIVE_E2E_TUI=1 go test ./internal/e2e/ -run TestControlCenterInTmux -v   # drives the control center in tmux
```

Dev runs without touching GitHub or plan usage: `HIVE_HOME=<dir> HIVE_FORGE=local:<dir> hive daemon` with `"harness": "fake"` in `.hive/config.json`.

## Docs

- [docs/status.md](docs/status.md): what's done, in flight, next. Read first.
- [docs/overview.md](docs/overview.md): the plain account behind the README and its diagram (`docs/arch.json`). It repeats facts from status.md and goals.md; update it in the same change.
- [docs/architecture.md](docs/architecture.md): packages, folder layout, data flow, invariants.
- [docs/core.md](docs/core.md): what the core is, its measured size, and what moves out to modules. New functionality goes in a module unless it says core.
- [docs/risk.md](docs/risk.md): risk tiers, warden, tribunal and the point of effect.
- [docs/priority.md](docs/priority.md): leads answer however busy the machine is: the strain exemption, leads-first ticks and nice levels.
- [docs/score.md](docs/score.md): the Hive project's score.
- [docs/judge.md](docs/judge.md): a model's judgement decides whether a change is kept; the numbers are one thing it weighs.
- [docs/design/control-center/brief.md](docs/design/control-center/brief.md): the Claude Design brief and package of the control center. A UI change regenerates its `frames.html` with the golden frames (`HIVE_UPDATE_FRAMES=1`) and updates the brief; a test holds both to the code.
- [docs/log.md](docs/log.md): decisions.
- [docs/spec.md](docs/spec.md): the build spec.
- [LEARNINGS.md](LEARNINGS.md): gotchas.

## Rules of the codebase

- The core (`internal/core`) stays small and readable in one sitting; anything that can be a module goes in `internal/mod/*` or `.modules/`.
- Host git never runs in an agent's checkout. Files the sandbox can write are read with `core.SafeRead`.
- Every state change is a journal event; every forge/run creation is idempotent so a restart never duplicates.
- Nothing an agent asks for runs outside the sandbox, except a warden's or tribunal's approved script, pinned by the digest of its text (not the files it calls), which the risk module runs in the daemon (see docs/risk.md).
