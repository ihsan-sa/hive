# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24: the recursive gate rolled back 0b75e74d ('Nobody in between' is measured: each merge and release counts the outside hands it took): the candidate fails its own vet or short tests. Fix: none was needed in the change. Its own short tests pass in the gate's environment (same env vars, read-only copy of the candidate's source, offline modules) and twice more under CPU load, and the gate's output tail named no failing package. The control at b05143f0 failed its own short tests in 6 of the 8 gate runs around it ("both sides fail"), so this was the gate's flaky short-test step landing on the candidate's side. The gate's report should name the failing package and test, not only the last 600 bytes.
