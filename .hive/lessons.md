# Lessons

What was reverted and why.
- 2026-10-23: "Sharing has three levels (sharing-has-three-levels)" (PR #163, 2aef164f) was reverted: the tests went red after it merged
- 2026-10-24, correction: the revert of #111 (PR #115, closed unmerged) was filed in error. Its "tests red" was `/bin/sh: 1: go: not found`: no Go toolchain was visible in the sandbox, not a fault in the change. A check that can't find its own tool is now "can't run", never red (#116).
- 2026-10-24: the recursive gate rolled back 6b7f0102 (Follow up the review of Models with an Anthropic-compatible API (Kimi, DeepSeek, GLM) run through Claude Code): not better than the current Hive: nothing moved beyond the noise. Fix: none in the code; the change hardens Anthropic-compatible model turns (no host Claude login, vendor 429 as a limit, a refused key named, quoting, no-link reads), which no gate probe runs (the benchmark uses no vendor model), so it can only tie. Every probe passed on both sides with no regression; such a change needs a probe that exercises it, or a gate rule that a tie with no regression is kept for a security fix.
