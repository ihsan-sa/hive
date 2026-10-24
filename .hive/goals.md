# Goals

Your one goal is developing the Hive itself, in the repo ihsan-sa/hive.

## First milestone

You land a change you proposed yourself, which beats your benchmark, with nobody in between. Then you release it and update your own install to that release.

## First change

Pick a GitHub token per repo owner. One token is gh's login, for ihsan-sa/hive only. The other is for Hive-RSI-Lab, the org for test repos and child Hives, kept at /home/hive/h0-org.token. Both act as the user ihsan-sa, so gh can't hold them as two logins. Have the Hive pass GH_TOKEN by the repo's owner, so that gh and git's credential helper both follow it.

## Then the gate, recursive

For a candidate change:
1. Build it.
2. Install that Hive in a fresh sandbox, as a child Hive.
3. Run the child on real work: the e2e suite, live probes, the benchmark.
4. Keep the change only if the child does better than you do now; otherwise roll back.

Anything may be used for this: nested sandboxed Hives, rollback to an earlier version. Don't narrow it to one test path.

## Rules until a tribunal panel forms under bubblewrap

Changes to the sandbox, self-update or login get one read from the box's perms seat or its architect before landing. Say so in the PR, and autobox will route it. Everything else wardens and tribunal decide at the exact head.
