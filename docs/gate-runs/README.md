# Gate run records

These records come from **earlier versions of the gate**. Those versions ran bare on a local org, used the unstamped `0.0.0-dev` version, measured no `commits_landed` and judged the side's own `cost_usd`. They show how the decision rule behaves. They do not show the current code (allowlist bubblewrap sandbox, separate evaluator, benchmark measured from the forge) working. They get replaced by a keep and a rollback run under bubblewrap, and against the test-only org once it exists, on a machine where unprivileged user namespaces work. The worker boxes so far refuse them.

The current code's end-to-end record is `TestBwrapGateKeepsAndRollsBack` in ci's `sandboxed-e2e` job (the gate's bubblewrap step): a keep run and a rollback run under real bubblewrap on a local org, with each report printed in the log.
