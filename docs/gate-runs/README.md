# Gate run records

**These two records come from a red run** (the test failed on the org probe). They are kept until a green run of `TestBwrapGateKeepsAndRollsBack` at this PR's head replaces them. That run's keep is built from commits, with the stamped version and the binary's hash in each report's header.

The current gate's end-to-end record comes from `TestBwrapGateKeepsAndRollsBack`, in ci's `sandboxed-e2e` job, run 40073936934 at 64301d0, under real bubblewrap on a GitHub runner. That test runs two whole gates on a local org with real binaries and the real probes:

- `kept-trivial-candidate-bwrap.md`: the same Hive on both sides, `--allow-tie`. **KEPT**. The daemon, the benchmark (this tree's suite inside each side's sandbox, its landed code checked in a sealed sibling sandbox, score 3, egress 0 through the meter) and recurse (a nested gate inside the child, which rolled back its self-test) pass on both sides.
- `rolled-back-broken-candidate-bwrap.md`: a candidate that answers `--version` but starts no daemon. **ROLLED BACK** on the daemon, the benchmark and recurse, with its lesson written.

Both runs made 2 local org repositories and removed 2, leaving 0. In both, the `org` probe failed on both sides, because the local org's repository was not visible inside the side's allowlist sandbox. That failure was even across the sides, so it did not change either verdict. It is fixed after this run (eb14b6a and the commit after it bind the repository's folder, at its absolute path, into the side's sandbox). The next ci run of the test shows the org round trip passing.

Versions read `0.0.0-dev` because the test hands the gate prebuilt binaries. A gate that builds its sides stamps `0.0.0-gate.<sha>`.

Not yet recorded: a run against a real GitHub org. It needs the test-only org and the fine-grained child token, both the person's to make.
