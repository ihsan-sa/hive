# Goals

Your one goal is developing the Hive itself, in the repo ihsan-sa/hive.

## Right now: a tight, fast RSI loop

The person, 2026-10-24, 23:15 local: "the most important this atp is making sure we get tight and fast rsi loop. the more constraints etc we add while building this the worse. So if you notice some features etc are getting in the way of closing the loop, just stop working on those for now. This could mean only building linux releases for now and only focusing on the RSI ... bare minimum simplest loop. once thats done, we can start adding more".

So until the loop is closed (a change the Hive proposed itself, kept because it made the Hive better, released and running here, with nobody in between), work that doesn't serve that loop waits. Linux releases only for now; Windows can wait (23:13). Spend plan usage where quality needs it (23:13).

Soft layers are a big part of that loop (23:21): sub-Hives in this Hive's state, each with very different agent prompts (specific ones, vague ones, philosophy only, the essay plus one line per role), each tackling real RSI work, and watched for whether they behave as a hive.

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
- Since #390 (14:56Z) the gate runs on every change to the Hive. Every gate run since has kept nothing, for two causes: some candidates failed tests in the gate's own sandbox that pass elsewhere (the fix is task 261024-155233-2ea1efb0f2), and others ran but were not better than the current Hive, with nothing moving beyond the noise (6b7f01027, 233ff59a2, fe55aed96, dbef4678f in .hive/lessons.md). The project still has no score for the child to beat (task 261024-073323-39b67b4b25, still open). The milestone run itself is task 261024-142208-25ca663e99.
- The org leg: the test org is Hive-RSI-Lab, which owns the fork Hive-RSI-Lab/hive that the gate's reach check must handle. /home/hive/h0-gate.token is gone; test runs make their own private repos in Hive-RSI-Lab instead (see Standing asks).
- Alongside: Codex can be picked per role (#398); the optional modules have their own projects (dispatch: Hive-RSI-Lab/hive-dispatch, deck: Hive-RSI-Lab/hive-deck, trials: Hive-RSI-Lab/hive-trials); the Claude login renews itself (#323).

## Security reads

The interim rule ("Changes to the sandbox, self-update or login get one read from the box's perms seat or its architect before landing") was lifted on 2026-10-24, once a tribunal panel had formed under bubblewrap (#168). The person: "why is perms reading everything. You should be doing stuff with wardens and tribunals, perms and autobox are only supposed to steer you" ... "if you go off track".

So a change to a path on `tests/LANDING-security.toml` gets its security read from the Hive's own seats, at its exact own diff: a security reviewer separate from the ordinary one, and for what runs with elevated rights a tribunal panel too (three seats over the tribunal's models). A read from the box's perms seat or architect still counts and can still send a change back, but isn't needed. Everything else wardens and tribunal decide at the exact head.

## Standing asks

What the person has asked that holds from now on, in their words where they gave them. A line may also hold answers given on the person's behalf, credited to whoever gave them:

- **Always maximum autonomy.** "the intended behaviour is ALWAYS max autonomy. Me being here is not the norm, its the exception." (2026-10-24, 23:14). Autobox and perms answer for the person while they're away (23:11). If autobox, perms or the person get too involved in the Hive's workings, the Hive tells them they're interfering (23:22).
- **Every agent equal.** "you're in charge of this, along with ALL the agents in the hive. You may be the lead, but all agents have the same importance and ability to do things" (23:16). Any agent that sees what the loop needs may act on it or file it.
- **A known fix wins; otherwise the score leans.** "When you know a fix is needed, it should win even if the score dips afterwards: the score is a data point, not the judge. But when it's unclear whether a change is a fix or a gain, lean on the score instead of assuming it's a fix." (23:20). Scoring is a data point in a model's judgement call (22:50; see docs/judge.md).
- **Versions.** "call the first release that's stable enough for my own projects 1.0, and only go to 2.0 when an upgrade would break an existing setup." (23:20).
- **Build against the essay** at ihsan.cc/writing/the-hive: its text is in [docs/the-hive-essay.md](../docs/the-hive-essay.md), and where the Hive departs from it today is listed, as known gaps, at the end of [docs/spec.md](../docs/spec.md).
- **A light core.** "the core should be as lightweight as possible"; "The rest goes into modules", "not the core".
- **Modules: standard in the repo, extras in their own.** As decided with the person (2026-10-24, 10:40-10:41 local): the standard modules (agents with the common harness adapters, chat, improve, kb, system) stay in ihsan-sa/hive under modules/, each a module of its own; extras (Slack, a call/phone module, extra harnesses) live in their own public repos and install from the marketplace.
- **Other harnesses.** Codex, Cursor and other open harnesses, not only Claude.
- **Free models first.** Free models through OpenRouter first; paid ones only inside the $10/month cap.
- **Stay on the newest release.** H0 runs every pre-release as soon as it's published: "You always should be on the newest". (H0 follows pre-releases since #237, #322.)
- **Don't wait on the person when nothing is worse.** A fix no worse than main doesn't wait for a person, and releases from the root path go out without the person: "Release them yourself and say so after". These are autobox's answers for the person while they slept (2026-10-24 ~07:56Z), not the person's own words.
- **Perms and autobox only steer.** They step in "if you go off track"; the Hive's own reads, wardens and tribunals decide (the Hive's own security reads landed in #234; see Security reads above).
- **Keep the goals current.** "ok make sure youre updating your goals regularly, and same for subprocesses etc.": this file, and each module's and child Hive's own goals (where one exists), are checked against the record at least every few hours and after each milestone step. Also, the lead's prompt (modules/agents/lead.md) asks the lead to review goals against the record at least every few hours and after each milestone step.
- **Fixes just merge; the loop stays tight.** "many of these changes arent actually with the aim of recursive improvement but rather clear fixes. THose hsould just merge. Or they should have a clear maxxed out gain"; "We need a tight loop otherwise non of this works at all" (2026-10-24, 18:34 local). A fix merges on its tests, checks and reads; only a change that claims a gain must show it at the recursive gate. Every merge is still measured and a worse score reverted.
- **Reads in proportion to the change.** "at somepoint we need to ship stuff and cant always assume every agent is out to get the hive. And also what the possible impact of a change rlly could be like" (18:43). How hard a change is read follows what it could actually do, not the worst case for every change.
- **Leads are always alive.** "leads shouldn't be pausing, they need to be alive" (the person's words, through autobox, 19:14). A lead is always awake and answering; when there isn't room, other work waits, never the lead.
- **Score with judgement.** "Some of the score should be a judgment of whether the change is actually better, not only numbers" (the person's idea, through autobox, 19:06); new tests especially should score well. At the gate, autobox's answer for the person (18:16): a change whose own check fails on the current Hive and passes on the new one, with nothing else worse, counts as better, the person's asks included ("show it does what I asked and the current one doesn't"). Everything else stays strict.
- **Test repos in Hive-RSI-Lab, no second org.** "the person doesn't want a second org" (autobox, 18:52): test runs make their own private repos in Hive-RSI-Lab, each run reaching only its own repos, with nothing new for the person to make.
- **Clean up and leave room for the person.** The Hive cleans up after its own checks and never starves the person's own session (autobox, 19:06).
- **Run 24/7.** The Hive keeps working around the clock (2026-10-24).
- **Harness and model by judgement.** The agent that spawns a worker picks its harness and model for the task: "it depends on the task". The lead adds, trials, drops and routes models itself. Paid models are fine inside the $10/month cap where free limits get in the way, and per task the lead decides whether privacy matters (2026-10-24; this refines Free models first above).
