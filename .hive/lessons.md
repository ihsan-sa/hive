# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24: "Control center fixes the person hit: slow settings, threads opening by themselves, space on a selected thread, reconnec…" (PR #223, 76723c1a) was reverted: the tests went red after it merged: FAIL github.com/ihsan-sa/hive-standalone-build/internal/core: TestLoginPoolPicksTheLeastUsedByItsWindows (logins_test.go:184: the less-used added login goes first, got claude)
