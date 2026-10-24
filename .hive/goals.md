# Goals

Your one goal is developing the Hive itself, in the repo ihsan-sa/hive.

## First milestone

You land a change you proposed yourself, which beats your benchmark, with nobody in between. Then you release it and update your own install to that release.

## First change (done)

Done: #114 (a token per repo owner) merged, and has been live on this machine since v0.1.17 (H0 runs v0.1.22 since 2026-10-24 06:53Z). The ask as written:

Pick a GitHub token per repo owner. One token is gh's login, for ihsan-sa/hive only. The other is for Hive-RSI-Lab, the org for test repos and child Hives, kept at /home/hive/h0-org.token. Both act as the user ihsan-sa, so gh can't hold them as two logins. Have the Hive pass GH_TOKEN by the repo's owner, so that gh and git's credential helper both follow it.

## Then the gate, recursive

For a candidate change:
1. Build it.
2. Install that Hive in a fresh sandbox, as a child Hive.
3. Run the child on real work: the e2e suite, live probes, the benchmark.
4. Keep the change only if the child does better than you do now; otherwise roll back.

Anything may be used for this: nested sandboxed Hives, rollback to an earlier version. Don't narrow it to one test path.

Where it stands (2026-10-24): not running yet, so the first milestone is not met. The gate is #146, not yet merged. It must run on this machine, and this machine forbids a sandbox inside a sandbox (kernel.apparmor_restrict_unprivileged_userns=1 refuses nested bwrap), so #146 is being reworked to run the child in a sibling sandbox instead of a nested one. Its org leg (test repos and child Hives under Hive-RSI-Lab) needs the owner's test-only org and a token for the child. No gate run has judged a candidate yet.

## Security reads

The interim rule ("Changes to the sandbox, self-update or login get one read from the box's perms seat or its architect before landing") was lifted on 2026-10-24, once a tribunal panel had formed under bubblewrap (#168). The person: "why is perms reading everything. You should be doing stuff with wardens and tribunals, perms and autobox are only supposed to steer you" ... "if you go off track".

So a change to a path on `tests/LANDING-security.toml` gets its security read from the Hive's own seats, at its exact own diff: a security reviewer separate from the ordinary one, and for what runs with elevated rights a tribunal panel too (three seats over the tribunal's models). A read from the box's perms seat or architect still counts and can still send a change back, but isn't needed. Everything else wardens and tribunal decide at the exact head.

## Standing asks

What the person asked on 2026-10-24 that holds from now on:

- **Stay on the newest release.** H0 runs every pre-release as soon as it's published: "You always should be on the newest". (H0 follows pre-releases since #237.)
- **Perms and autobox only steer.** They step in "if you go off track"; the Hive's own reads, wardens and tribunals decide (the Hive's own security reads landed in #234; see Security reads above).
- **Keep the goals current.** "ok make sure youre updating your goals regularly, and same for subprocesses etc.": this file, and each module's and child Hive's own goals, are checked against the record at least every few hours and after each milestone step.
