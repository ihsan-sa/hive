# Searching the knowledge base

`hive agent search --query "<words, ids, #PR numbers, file paths>"` (or `hive kb <words>` in a shell, or `/` in the control center's Knowledge tab) returns the best 10 files of the knowledge base, each once, best first. Add `--limit all` (or a number) for more. It is the kb module's own program (`search/` in this module), which the Hive runs for each call. A living note the dream consolidated (its header has `consolidated:`) ranks higher, and a retired note (`retired:`, with why) lower.

Each hit says **why** it matched:

- `is about #111`: the file is the subject the question names (the task whose PR is #111, the run or request with that id, the note of that name).
- `names #111`: it mentions it.
- `words: …`: the question's words it holds (a word and its forms count as one: "reverted", "reverts", "revert"; a few of the Hive's words stand for each other: a revert and a rollback, a block and a hold).
- `connected by <entity>, from <file>`: it came in through a better hit, by an id, PR number, commit, file or test both name, by a revert or follow-up of it, or by a `[[note]]` link either way (the consolidation pass's Related lists included).

Under each hit are the lines of its best entries that hold the question's words or entities, and for a task or request its state now (`now: stage …`). A task's other reviewer runs fold into its best one (`also N more reviews`). Lines that many files repeat, such as the lessons every brief quotes, count for nothing.

Name the things you know: an id (`261024-015726-45d2d27ed7`, `w-…`, `gate-…`), a PR (`#111`, `PR 111`), a commit, a path (`internal/mod/kb/kb.go`), a test (`TestModuleSeam`). They are matched exactly and connect files that share no words.

## Deep search

`hive agent search --deep "a question"` (or `hive kb --deep <question>`, or `?` in the Knowledge tab) sends a small research agent (the module's `researcher.md`, on a small model, within about 8 minutes and $0.40) that searches, follows ids through the record and the journal, reads what it needs and answers in a few sentences with its sources. Use it when the answer is spread over several files and you would otherwise read them all; it costs cents, so the plain search comes first for a quick look.

## What it holds

- **Notes**, one per file at the top, written with `hive agent remember` and consolidated by the filer. Each has a header (title, tags, project, date, run). A note is context from an earlier run, not a decision: check it against the code before relying on it.
- **The record**, under `record/`, filed on its own from the Hive's journal: one file per subject, such as `record/task/<id>.md`, `record/risk/<id>.md` (requests and the warden's and tribunal's decisions), `record/run/<id>.md` (a run's messages, progress, turns and finish), `record/chat/<chat>.md`, `record/effect/<project>.md` (pushes, checks, reviews, merges) and `record/project/<name>.md`. Each entry is headed with its time, event kind and sequence number. Snapshots such as a task's or request's state show only what changed since the last one, and a field that was emptied shows as `(cleared)`.
- **Living notes and summaries**, made by the module's dream pass (every 4h and at quiet moments): duplicates merged into one note with the newest text first and every older fact under `## History`, the sources and runs in its header; `project-<name>-what-s-true-now.md`, what the record says about each project now; `practice/learned-from-the-record.md`, why changes were rolled back, work blocked and alerts raised, counted; and a `## Related` list on each note, linked both ways. A note the record has since contradicted (a "Blocked:" note whose run finished done, a decision another note revises) is marked `retired:` and kept. `dreams/` holds each pass's digest, what the Hive learned since the last one.
- **Practice**, under `practice/`: how a Hive works, filed from this module's `practice/` folder at install and refreshed on update while the copy still matches what was last filed (`practice/.filed.json` holds those hashes); a copy a person has edited is left alone.

For what state something is in now, read the last entries of its file. For what was decided, search the decision's words with `risk`, `verdict` or `answered`. For history, read the file top to bottom. When nothing matches, say the record doesn't cover it.

To look by title or tag instead, `grep -ril "<word>" "$HIVE_KB"`.

## How good it is

`search/bench/` holds real questions about the Hive's record with the files that answer them, and the search's recall and precision on them (`go run ./modules/kb/search bench --kb "$HIVE_KB" modules/kb/search/bench/questions.json` in the Hive's repository, `--engine words` for the word match it replaced). A change to the search reports both sets before and after in `search/bench/README.md`.
