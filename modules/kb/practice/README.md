# How a Hive works

Default knowledge every Hive starts with: how its agents look things up, decide, write, hand off and measure. When it starts, the Hive files each one into the knowledge base as `$HIVE_KB/practice/<name>.md`, and `practice/.filed.json` records the hash of what it filed. On an update it refreshes a copy that still matches that hash. A copy a person has edited, or one the ledger has no entry for (written by hand or by some other path), is left alone, and the Hive journals the list of such docs when it changes.

- `looking-first.md`: look before you answer, and say what you didn't check.
- `deciding.md`: decide and keep going; report findings after.
- `risk.md`: weigh risk by how far the harm reaches and whether it can be undone.
- `writing.md`: write to a person short and plain, the answer first.
- `briefs.md`: brief a worker with the goal, the bounds and what done is.
- `handoffs.md`: write the notes before you stop.
- `parallel.md`: run independent work side by side.
- `measuring.md`: measure, then keep or revert.
