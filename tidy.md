# Tidy audit

The person, 2026-11-08 (post 110815388fca): agents should keep the hive organised at their own hand-offs (file a conversation in a focus, give it a title, update the focus summary after a result). This pass tidies what slipped through and, more importantly, finds out **why** the agent that should have done it didn't, so that cause gets fixed. The goal is for this pass to stop being needed: the hive is time invariant when `python3 -I ~/core/tidy.py stats` shows passes that find nothing.

The post that started you lists the misses since the last pass, each with the agents who worked in that thread, whether the Stop hook asked them (`asked at Stop`, `never asked`), and the run log to read. Keep it to about 30 minutes.

1. **Fix each miss**, in the agent's place:
   - `file`: `hive focus tag THREAD FOCUS` with the focus it serves (`hive focus list`). Plumbing the person needn't see (keeper, improve, thread-status, release-train, watch feeds, side asks): `hive focus tag THREAD --off`, once, and it is never listed again.
   - `title`: `hive rename THREAD "short plain title"`. Never touch a title the person set.
   - `summary`: read the result and the focus card, then `hive focus set FOCUS` in plain words (Now / Lately).
2. **Find why**, for up to 5 misses (prefer `asked at Stop` ones, then the agent with most misses). Read the end of its run log (`tail -c 60000 FILE`, or `grep` for `focus tag`, `rename`, `focus set`, `file-asked`): was it asked, did it see the ask, did it think about it, did it decide against, did it run out of turn, or was it never asked? Write one line per agent: `agent, thread, kind: cause`.
3. **Fix the cause** when it is small and clear: the ask's wording in `~/core/focus.py` (`file_nudge`, `nudge`), a case the hook misses in `~/core/inbox.py` (`focus_nudge`), a thread kind `tidy.py` should not count. Run `python3 -I ~/core/test_focus_file_nudge.py` and commit only the files you changed in ~/core (`git add FILE...`, never `-a`). A bigger change: a rung in `~/projects/hive-meta/goals.md` with a `check:`.
4. **Report** in thread `tidy`: misses found and fixed by kind, the cause lines, what you changed, and the last few lines of `python3 -I ~/core/tidy.py stats`. End with `Done:`. Don't start other agents.
