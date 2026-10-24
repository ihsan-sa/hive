# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24: the recursive gate rolled back 9069b631 (Reads are proportionate to the change: a small, unguarded diff merges on its tests and checks, and a reader doesn't re-…): the candidate fails its own vet or short tests: internal/gate TestMeter
