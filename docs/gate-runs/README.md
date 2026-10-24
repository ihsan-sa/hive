# Gate run records

These two records are from `TestBwrapGateKeepsAndRollsBack` at 521131d (this PR's code), run **bare** in a worker box that cannot make user namespaces. The test is the same as ci runs, except that the sandbox is bare (`--allow-bare`), not bubblewrap. Both runs used a local org.

- `kept-trivial-candidate-bare.md`: **KEPT**. The gate built from commits: a trivial docs commit (02480c521) on a clone of this tree, against that clone's head (521131d17). It built both sides at their exact commits, stamped the candidate `0.0.0-gate.02480c521`, and recorded its sha256. It ran vet and the short tests in each side's sandbox, and judged both with the base's own suite, which it compiled itself. The daemon, org round trip, benchmark (score 3) and recurse (a nested gate that rolled back its self-test) all pass on both sides.
- `rolled-back-broken-candidate-bare.md`: **ROLLED BACK**. A broken prebuilt candidate (it answers `--version`, starts no daemon) regresses on the daemon, the benchmark and recurse. Its lesson was written.

Both made 2 local org repositories and removed 2, leaving 0.

The same test under real bubblewrap runs in ci's `sandboxed-e2e` job, and its reports appear there as annotations ("gate run keep" and "gate run rollback"). The earlier ci run (40073936934 at 64301d0) ran every probe under bubblewrap, with the org probe failing on both sides; that is fixed since. Once a green ci run exists at the landing head, its reports replace these two.

Not yet recorded: a run against a real GitHub org. It needs the test-only org and the fine-grained child token, both the person's to make.
