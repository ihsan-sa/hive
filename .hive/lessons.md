# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24: the recursive gate rolled back 9069b631 (Reads are proportionate to the change: a small, unguarded diff merges on its tests and checks, and a reader doesn't re-…): the candidate fails its own vet or short tests: internal/gate TestMeter
- 2026-10-24, correction: the recursive gate rolled back eb18745ca (run gate-1792855124-644362ba) and rolled back 0b75e74d8 (run gate-1792856694-b13d92cb) in error: the environment's, not the changes'. The failing test was internal/gate TestMeter, whose unix socket under t.TempDir() passes 108 bytes inside the gate's deep run folder (it failed by the digits of a random name, so the control passed sometimes). TestMeter now uses a short dir, and the gate names each failing test and treats one the control shares as the environment's (#422).
