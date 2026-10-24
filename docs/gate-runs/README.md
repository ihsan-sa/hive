# Gate run records

These two records are from `TestBwrapGateKeepsAndRollsBack` in ci's `sandboxed-e2e` job: check run 120823169714, at e8e6fa4, green, under real bubblewrap on a GitHub runner. The test runs whole gates on a local org with the real probes. Each report was taken verbatim from that job's "gate run keep" and "gate run rollback" annotations.

- `kept-trivial-candidate-bwrap.md`: **KEPT**. A gate from commits: a trivial docs commit (07b6bbf14) on a clone of this tree, against that clone's head (19e49a649). The gate built both at their exact commits, stamped the candidate `0.0.0-gate.07b6bbf14` with its binary's sha256, and ran vet and the short tests in each side's sandbox. It judged both with the base's own suite, which it compiled itself. All four probes pass on both sides:
  - the daemon, with each side reporting its stamped version;
  - the org round trip;
  - the benchmark, inside each side's sandbox, at score 3, with its landed code checked in a sealed sibling sandbox and 0 bytes through the meter;
  - recurse, a nested gate inside the child, which rolled back its self-test.
- `rolled-back-broken-candidate-bwrap.md`: **ROLLED BACK**. A broken prebuilt candidate (it answers `--version`, starts no daemon) regresses on the daemon, the benchmark and recurse, and its lesson was written. The org round trip passes on both sides.

Both runs made 2 local org repositories and removed 2, leaving 0.

The test now also has a third, could-not-run case under bubblewrap (a probe broken on both sides: no keep, no lesson). It was added after that ci run.

Not yet recorded: a run against a real GitHub org. It needs the test-only org and the fine-grained child token, both the person's to make; they have been asked.
