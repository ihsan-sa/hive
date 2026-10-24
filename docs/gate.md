# The recursive gate

A candidate Hive replaces the current one only if it does better, measured the way the current one is: in a child Hive, on real work, on the same day. `hive gate` runs that. Code: `internal/gate` (decision, child sandbox, probes, install) and `cmd/hive/gate.go`; the benchmark is `TestBenchmark` in `internal/e2e`.

## What it does

For a candidate commit `--head` of the Hive's source `--repo`, and the current Hive's commit `--base`:

1. **Build.** For each side, a fresh clone, `git checkout --detach <sha>`, verified to be exactly that sha and clean; then `go vet`, `go test -short` and `go build`. The binary is the Hive. A side that does not build ends the gate: rollback.
2. **Child.** Each side is installed as a child Hive in a fresh sandbox: its own `HOME`, its own `HIVE_HOME` workspace and local forge under the run's `--out`, and an environment made from a whitelist (`PATH`, proxy, Go cache paths). The only credential it holds is the org token (`HIVE_GATE_ORG_TOKEN` or `--org-token-file`), never the login of the repository being gated. Under bubblewrap the machine is read-only, the real home is hidden and only the child's root, its checkout and the Go caches are writable. Where bubblewrap cannot make namespaces (a container, or Ubuntu with `apparmor_restrict_unprivileged_userns=1` and no profile) the child runs **bare**: the same scrubbed environment and fresh home, without filesystem isolation. The report says which (`sandbox`).
3. **Org repo.** With `--org Hive-RSI-Lab`, each side gets a fresh private repository (`gate-<run>-candidate|control`), made after the builds pass and deleted when the run ends however it ends. A run makes at most `MaxRepos` (4) of them; the report lists what was made, removed and, loudly, left. Deleting needs the token to carry `delete_repo`. `--local-org <dir>` uses bare repositories in a folder instead, for dry runs and tests.
4. **Real work, judged by one evaluator.** The `e2e` and `benchmark` probes run the tests of a third checkout, the **evaluator** (`--eval <sha>`, default the base), against each side's own binary (`HIVE_E2E_BIN`), so a candidate cannot delete a failing test, change the asks or the timer, and win. The candidate's own tree is built and unit-tested but never grades itself. A change to the e2e suite or the benchmark therefore takes effect for the gate only once it is the base. Probes, interleaved candidate/control for `--reps` repeats (default 3), so the day's weather hits both:

   | probe | what it does |
   |---|---|
   | `daemon` | the child's daemon starts on its fresh workspace, answers `status`, shuts down |
   | `org` | the child clones its org repo with the org token, pushes a commit |
   | `e2e` | the evaluator's whole `internal/e2e` suite against the child's own binary |
   | `benchmark` | `TestBenchmark`: a fixed set of asks on the demo `textkit` project (`HIVE_BENCH=<asks>`); measures **score**, **asks_finished**, **seconds** and **cost_usd**. `HIVE_E2E_HARNESS=claude` makes it real work on real agents (plan usage); the default fake harness measures the pipeline's own time |
   | `recurse` | the child runs `hive gate --self-test`: the gate inside a child, gating a trivial change through a grandchild (its binary as both sides, so the right verdict is "not better"), on its own local org repos, which it must clean up |

   More probes are added by name in `Config.Probe`; the live probes (`TestLive*`) are the e2e probes on a real harness.
5. **Decide.** `Decide` compares probe by probe and metric by metric. A probe the candidate passes less often than the control, or a metric worse beyond the noise, is a **regression**; the mirror is a **gain**. Noise is the larger of 2% of the control's mean and twice the standard error of the difference of the two means, so more repeats tighten it. **Keep only if there is at least one gain and no regression.** A tie is a rollback; `--allow-tie` keeps a change that is meant to move nothing (a docs edit) when it breaks nothing, and the caller says so. Without it a docs edit is rolled back as "not better".
6. **Record.** `report.json` and `report.md` in the run's folder, a line in `gates.jsonl` beside it. A rollback appends the reason to `--lessons` (`.hive/lessons.md`), where the next worker reads it. Exit code 0 keeps, 1 rolls back (also when the gate could not finish: no decision is a rollback).

## Recursion and its caps

`--depth N` (default 1, at most `MaxDepth` = 2) is how many levels of gating the children may do; the child learns it as `HIVE_GATE_DEPTH` and `--self-test` refuses to run at 0. Each level makes at most 2 org repositories (the self-test uses local ones), and `--timeout` (default 2h) stops a run between probes.

## Installing a kept change, and going back

`hive gate ... --install <path>` (or `hive gate install --bin <new> --target <path> [--version v]`) runs only after a keep. It copies the current binary to `<path>.prev`, puts the new one in place, runs the health check (`--version` answers and names the wanted version), and **restores the previous binary if the check fails**. It does not touch the daemon's own updater (`internal/mod/release`), which keeps installing releases the way it did; wiring a gate in front of a release, and a health check after the updater's restart, is the next step.

## What is not yet done

- The gate is run by hand or by a caller; the improve module does not yet call it before it merges a change to this repository or releases.
- `e2e` runs inside the child's sandbox with the host's `go`; a machine without a Go toolchain cannot gate.
- The benchmark is four asks. On the fake harness the score stays at its baseline, so only a real harness shows a score gain.
- Org runs need the per-owner token (brief 261024-015755-a3f383a160); no org run has been made yet.
