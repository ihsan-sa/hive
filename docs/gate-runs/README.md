# Gate run records

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

## Milestone run (#373), 2026-10-24, on the host under bubblewrap 0.11.1

Run by approved warden request 261024-192751-b95f4c67fc (journal seq 24906 approved, 24913 done) as a detached systemd user unit; 8 cores, load 14 to 23 during the run. Probes daemon+benchmark (+recurse on the candidate run), fake harness, local org. Reports are the gate's own signed output (`sig` in the json).

- `milestone-baseline-aa-d3587fa-bwrap.{md,json}`: **(1) baseline**, main d3587fa against itself, 6 reps. Rolled back, nothing beyond the noise, as an A/A should. Noise per metric: score 6 every rep, asks_finished 4 every rep, egress_bytes 0 every rep (no band: they never move); benchmark seconds 55.7 to 56.2 in 10 of 12 reps, with two load spikes at 73.9 and 77.9.
- **(2) target**: benchmark seconds. Score, asks_finished and egress are already at their ceiling/floor, so seconds is the only metric with room: ~14 s per ask, almost all of it the daemon's 1 s tick between steps.
- `milestone-candidate-1b1efe1-kept-bwrap.{md,json}`: **(3) candidate**, 1b1efe1 (carrying 7b83bfe: the loop wakes 20 ms after any journal event) against d3587fa, 5 reps, no `--allow-tie`. **KEPT**: benchmark seconds 12.4 against 56.61 (noise ±1.87, n=5/5), score 6, asks 4, egress 0 on both sides, recurse passes on both. 1b1efe1's other change (a `# hive-timeout:` line for approved scripts) was reverted after (ea0cc16) and touches no probe.
