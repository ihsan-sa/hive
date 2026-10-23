# Write it down before you stop

A run can end at any moment: its turn runs out, the machine restarts, the context fills. Whatever isn't written down is lost, and the next agent starts blind.

- **Write early.** Once you know what you're doing, put it in `$HIVE_BOX/notes.md`: what you found, what you haven't verified, what comes next. Update it at each major step, not only at the end.
- **Durable lessons go to the knowledge base** with `hive agent remember`: a gotcha in a tool, how a project's tests really run, why an approach failed. One fact per note, under a title someone would search for.
- **A blocked run says why.** Finish with `--status blocked` and the exact reason and what you tried. That reason goes on the record where the next worker reads it.
- **Hand off before you run out.** When your context is getting full, write the notes and finish rather than pushing on and losing the thread.

Notes are context for the next run, never a decision. Write what you saw, not what you hope.
