# Goals

Your one goal is developing the Hive itself, in the repo ihsan-sa/hive.

## First milestone

You land a change you proposed yourself, which beats your benchmark, with nobody in between. Then you release it and update your own install to that release.

## First change (done)

Done: #114 (a token per repo owner) merged, and has been live on this machine since v0.1.17. H0 now runs v0.1.57 (since 2026-10-24 14:25Z) and follows pre-releases by itself (#237, #322); releases/latest is v0.1.55. Releases go out unsigned for now, by the person's choice. The ask as written:

Pick a GitHub token per repo owner. One token is gh's login, for ihsan-sa/hive only. The other is for Hive-RSI-Lab, the org for test repos and child Hives, kept at /home/hive/h0-org.token. Both act as the user ihsan-sa, so gh can't hold them as two logins. Have the Hive pass GH_TOKEN by the repo's owner, so that gh and git's credential helper both follow it.

## Then the gate, recursive

For a candidate change:
1. Build it.
2. Install that Hive in a fresh sandbox, as a child Hive.
3. Run the child on real work: the e2e suite, live probes, the benchmark.
4. Keep the change only if the child does better than you do now; otherwise roll back.

Anything may be used for this: nested sandboxed Hives, rollback to an earlier version. Don't narrow it to one test path.

Where it stands (2026-10-24 ~16:00Z): the gate exists and is wired in, but the first milestone is not met yet: nothing has been kept by the gate because it beat the current Hive.

- The gate is #146, merged at ~14:03Z. It must run on this machine, and this machine forbids a sandbox inside a sandbox (kernel.apparmor_restrict_unprivileged_userns=1 refuses nested bwrap), so the child Hive runs in a sibling sandbox, not a nested one. Under real bubblewrap in ci, TestBwrapGateKeepsAndRollsBack kept a trivial candidate (a tie is allowed) and rolled back a broken one, and the recursion worked.
- Since #390 (14:56Z) the gate runs on every change to the Hive. Every gate run since has kept nothing, because the gate's own sandbox fails tests that pass elsewhere (the fix is task 261024-155233-2ea1efb0f2). The project still has no score for the child to beat (task 261024-073323-39b67b4b25, still open). The milestone run itself is task 261024-142208-25ca663e99.
- The org leg: the test org is Hive-RSI-Lab, and the contents-only token for the child exists at /home/hive/h0-gate.token. The org owns the fork Hive-RSI-Lab/hive, which the gate's reach check must handle.
- Alongside: Codex can be picked per role (#398); the optional modules have their own projects (dispatch: Hive-RSI-Lab/hive-dispatch, deck: Hive-RSI-Lab/hive-deck, trials: Hive-RSI-Lab/hive-trials); the Claude login renews itself (#323).

## Security reads

The interim rule ("Changes to the sandbox, self-update or login get one read from the box's perms seat or its architect before landing") was lifted on 2026-10-24, once a tribunal panel had formed under bubblewrap (#168). The person: "why is perms reading everything. You should be doing stuff with wardens and tribunals, perms and autobox are only supposed to steer you" ... "if you go off track".

So a change to a path on `tests/LANDING-security.toml` gets its security read from the Hive's own seats, at its exact own diff: a security reviewer separate from the ordinary one, and for what runs with elevated rights a tribunal panel too (three seats over the tribunal's models). A read from the box's perms seat or architect still counts and can still send a change back, but isn't needed. Everything else wardens and tribunal decide at the exact head.

## Standing asks

What the person has asked that holds from now on, in their words where they gave them:

- **Build against the essay** at ihsan.cc/writing/the-hive.
- **A light core.** "the core should be as lightweight as possible"; "The rest goes into modules", "not the core".
- **Modules: standard in the repo, extras in their own.** As decided with the person (2026-10-24, 10:40-10:41 local): the standard modules (agents with the common harness adapters, chat, improve, kb, system) stay in ihsan-sa/hive under modules/, each a module of its own; extras (Slack, a call/phone module, extra harnesses) live in their own public repos and install from the marketplace.
- **Other harnesses.** Codex, Cursor and other open harnesses, not only Claude.
- **Free models first.** Free models through OpenRouter first; paid ones only inside the $10/month cap.
- **Stay on the newest release.** H0 runs every pre-release as soon as it's published: "You always should be on the newest". (H0 follows pre-releases since #237, #322.)
- **Don't wait on the person when nothing is worse.** A fix no worse than main doesn't wait for a person, and releases from the root path go out without the person: "Release them yourself and say so after". These are autobox's answers for the person while they slept (2026-10-24 ~07:56Z), not the person's own words.
- **Perms and autobox only steer.** They step in "if you go off track"; the Hive's own reads, wardens and tribunals decide (the Hive's own security reads landed in #234; see Security reads above).
- **Keep the goals current.** "ok make sure youre updating your goals regularly, and same for subprocesses etc.": this file, and each module's and child Hive's own goals (where one exists), are checked against the record at least every few hours and after each milestone step. Also, the lead's prompt (modules/agents/lead.md) asks the lead to review goals against the record at least every few hours and after each milestone step.
