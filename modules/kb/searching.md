# Searching the knowledge base

`hive agent search --query "<words>"` (or `hive kb <words>` in a shell) returns the pieces that match the most words, newest first among equals: whole notes, and single entries of the record. A living note the dream consolidated (its header has `consolidated:`) comes first when it matches at least half as well as the best hit, and a retired note (`retired:`, with why) comes last. It shows the best 20 and says how many more matched; add `--limit all` (or a number) to see them. A long piece ends with "clipped"; read its file under `$HIVE_KB` for the rest.

The knowledge base holds three things:

- **Notes**, one per file at the top, written with `hive agent remember` and consolidated by the filer. Each has a header (title, tags, project, date, run). A note is context from an earlier run, not a decision: check it against the code before relying on it.
- **The record**, under `record/`, filed on its own from the Hive's journal: one file per subject, such as `record/task/<id>.md`, `record/risk/<id>.md` (requests and the warden's and tribunal's decisions), `record/run/<id>.md` (a run's messages, progress, turns and finish), `record/chat/<chat>.md`, `record/effect/<project>.md` (pushes, checks, reviews, merges) and `record/project/<name>.md`. Each entry is headed with its time, event kind and sequence number. Snapshots such as a task's or request's state show only what changed since the last one, and a field that was emptied shows as `(cleared)`.
- **Living notes and summaries**, made by the module's dream pass (every 4h and at quiet moments): duplicates merged into one note with the newest text first and every older fact under `## History`, the sources and runs in its header; `project-<name>-what-s-true-now.md`, what the record says about each project now; `practice/learned-from-the-record.md`, why changes were rolled back, work blocked and alerts raised, counted; and a `## Related` list on each note, linked both ways. A note the record has since contradicted (a "Blocked:" note whose run finished done, a decision another note revises) is marked `retired:` and kept. `dreams/` holds each pass's digest, what the Hive learned since the last one.
- **Practice**, under `practice/`: how a Hive works, filed from this module's `practice/` folder at install and refreshed on update while the copy still matches what was last filed (`practice/.filed.json` holds those hashes); a copy a person has edited is left alone.

For what state something is in now, read the last entries of its file. For what was decided, search the decision's words with `risk`, `verdict` or `answered`. For history, read the file top to bottom. When nothing matches, say the record doesn't cover it.

To look by title or tag instead, `grep -ril "<word>" "$HIVE_KB"`.
