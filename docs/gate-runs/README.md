# Gate run records

## The daemon on the tower

The first `gate.judged` the daemon recorded for real is seq 20937, at 2026-10-24 15:20:07 UTC, by the daemon running v0.1.63 (started 15:11:52), a release with the self gate (#390), with `sandbox: bwrap`.

- `tower-first-daemon-rollback.md`: **ROLLED BACK**, the first one. PR #400 ("Goals say where things stand today"), head eb18745ca against main b05143f01, run `261024-151839-eb18745c`, gate `gate-1792855124-644362ba`. It started 15:18:44 and took 83 s: both sides built, then the candidate's `go test -short` failed, so no probe ran and no org repository was made. The reason named no test: the report kept only the last 300 bytes of `go test`, all `ok` lines and the closing `FAIL`. In the next runs against the same main (b05143f01) the control failed too ("both sides fail"). So it was main's short tests failing in the gate's sandbox, not the PR.
- `tower-first-daemon-keep.md`: **KEPT**, the first keep. PR #441 ("The dashboard shows spend and recent activity…"), head 6861de9da against main d3587fafe, run `261024-192106-6861de9d`, 1959 s (about 33 minutes) for 3 reps of the daemon, org, e2e, benchmark and recurse probes on each side. It was kept on e2e 3/3 against the control's 2/3, and the control's one failure was a test's temp-dir cleanup (`directory not empty`), a flake rather than a gain.
- The org leg did not run. Every run up to seq 28648 has the note "The gate ran on a local org instead of Hive-RSI-Lab: the children's token can write to more than the gate's repositories in Hive-RSI-Lab: dsaidyB/ECE-198, Hive-RSI-Lab/hive, … and 262 more", so `CheckReach` refused the children's token and each gate made its repositories on a local org under the run. The run at seq 30875 (fe55aed96, 23:39) has no note. `orgArgs` gives no note when a token file is missing, so it most likely ran locally without saying why. Its folder doesn't record which org it used.
- Over the first day (to seq 30875) there were 42 verdicts: 2 keep, 18 rollback, 22 could not run. A run that reaches the probes takes 32 to 44 minutes. A run stopped at vet and short tests takes 1.5 to 4.5 minutes.

What these runs surfaced was filed, and most of it is fixed: the reason now names the failing test, and a failure the control shares is could-not-run, not a rollback (#429, 261024-184855-bca90f2978). Main's short tests in the sandbox: 261024-181009-1d22811556, 261024-213942-b2a9266433. A flaky control run counted as a gain: 261024-212941-a6a0506343. `CheckReach` reads the owner's role as the token's reach: 261024-163142-db224b11e8. The person's real-org ask: 261024-162000-95f41e4e3d; a brokered org leg with no child token: 261024-225610-163a3bc0d0. Two daemons on one workspace (seq 23729, a clone into a folder the other daemon's gate already had) were fixed in #449. `internal/gate TestMeter` flaking in the candidate's short tests: 261024-230731-fe510a9896. The silent local fallback: 261024-235147-663b18d20d.

## ci

These two records are from `TestBwrapGateKeepsAndRollsBack` in ci's `sandboxed-e2e` job: check run 120831736203, at 70faa06, green, under real bubblewrap on a GitHub runner. The test runs whole gates on a local org with the real probes. Each report was taken verbatim from that job's "gate run keep" and "gate run rollback" annotations.

- `kept-trivial-candidate-bwrap.md`: **KEPT**. A gate from commits: a trivial docs commit (968033683) on a clone of this tree, against that clone's head (85b2f2618). The gate built both at their exact commits, stamped the candidate `0.0.0-gate.968033683` with its binary's sha256, and ran vet and the short tests in each side's sandbox. It judged both with the base's own suite, which it compiled itself. All four probes pass on both sides:
  - the daemon, with each side reporting its stamped version;
  - the org round trip;
  - the benchmark, inside each side's sandbox, at score 3, with its landed code checked in a sealed sibling sandbox and 0 bytes through the meter;
  - recurse, a nested gate inside the child, which rolled back its self-test.
- `rolled-back-broken-candidate-bwrap.md`: **ROLLED BACK**. A broken prebuilt candidate (it answers `--version`, starts no daemon) regresses on the daemon, the benchmark and recurse, and its lesson was written. The org round trip passes on both sides.

Both runs made 2 local org repositories and removed 2, leaving 0.

The same ci run also passed the test's third case under bubblewrap: a probe broken on both sides is could not run, with no keep and no lesson. Only the keep and rollback reports are annotated.

Not yet recorded: a run against a real GitHub org. It needs the test-only org and the fine-grained child token, both the person's to make; they have been asked.
