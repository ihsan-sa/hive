# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24, correction: the recursive gate rolled back eb18745ca (run gate-1792855124-644362ba) and rolled back 0b75e74d8 (run gate-1792856694-b13d92cb) in error: the environment's, not the changes'. The failing test was internal/gate TestMeter, whose unix socket under t.TempDir() passes 108 bytes inside the gate's deep run folder (it failed by the digits of a random name, so the control passed sometimes). TestMeter now uses a short dir, and the gate names each failing test and treats one the control shares as the environment's (#422).
- 2026-10-24, correction: the recursive gate rolled back a35c9380 (the follow-up to #390's review) with only a bare FAIL in its report, most likely the same TestMeter socket-path flake in the gate's deep run folder (#422), not the change; it passed its short tests under the gate's environment outside bwrap.
