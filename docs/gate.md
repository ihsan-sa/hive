# The recursive gate (retired)

The recursive gate built a candidate Hive and the current one in child Hives and kept the candidate only if it did better. Soft merge (the person, 10-30: "just send it, minimal gates, fail fast, stuff breaks but revert") took it out of the merge path, and issue #2142 deleted what was left: `internal/gate`, `hive gate`, `core/selfgate.go` and the reviewer-era fields. A change to the Hive now merges on its tests like any other and is measured after the merge (docs/score.md, docs/judge.md); a break or a score drop is reverted.

The gate's design and its recorded runs are in git history before #2142.
