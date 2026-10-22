# Searching the knowledge base

Notes live one per file under `$HIVE_KB`, named by a slug of their title, each with a header:

    ---
    title: ...
    tags: a, b
    project: demo
    date: 2026-10-22
    run: <run id>
    ---

Search titles and tags first (`grep -ril "<word>" "$HIVE_KB"`), then bodies. The `practice-*.md` files are how a Hive works, filed from this module's `practice/` folder on every start; change them there. A note is context from an earlier run, not a decision: check it against the code before relying on it.
