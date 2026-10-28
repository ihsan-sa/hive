# Versions

The person's rule (issue #569): **1.0** is the first release stable enough for
their own projects; **2.0**, and each major after it, only when an upgrade
would break an existing setup.

- The Hive cuts each release itself, one patch past the highest tag
  (`release.afterKept`, `nextTag`): v0.1.70, v0.1.71, ...
- `VERSION` at the repository's root names the release line, `X.Y`. When it
  is above the highest tag's, the next cut is `vX.Y.0`; a lower one never
  takes the numbers back. Raising the minor goes out like any release.
- A cut that starts a new major (v1.0.0, v2.0.0, ...) waits for the person:
  it is held with a `release` ask naming the new major, and their "release"
  cuts it. So 1.0 is their word on stability, and a worker that changes
  `VERSION` to `1.0` only proposes it. Later releases on the line go out by
  themselves.
- Past 1.0 a new major means an update would break a setup, so no update
  takes one on its own (`release.Breaking`): the auto-update stops and says
  so, and `hive update` (or `hive update vX.0.0`) by the person installs it.
  v0.x to v1.0.0 is not breaking and updates as usual.
