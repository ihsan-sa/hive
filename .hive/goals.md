# Goals

Your one goal is developing the Hive itself, in the repo ihsan-sa/hive.

## Right now: a tight, fast RSI loop

The person, 2026-10-24, 23:15 local: "the most important this atp is making sure we get tight and fast rsi loop. the more constraints etc we add while building this the worse. So if you notice some features etc are getting in the way of closing the loop, just stop working on those for now. This could mean only building linux releases for now and only focusing on the RSI ... bare minimum simplest loop. once thats done, we can start adding more".

So until the loop is closed (a change the Hive proposed itself, kept because it made the Hive better, released and running here, with nobody in between), work that doesn't serve that loop waits. Linux releases only for now; Windows can wait (23:13). Spend plan usage where quality needs it (23:13).

Soft layers are a big part of that loop (23:21): sub-Hives in this Hive's state, each with very different agent prompts (specific ones, vague ones, philosophy only, the essay plus one line per role), each tackling real RSI work, and watched for whether they behave as a hive.

Small problems in a tight loop (2026-10-29, 13:28 local): "These speed problems etc seem like good canditates for tight loop RSI on small problems: think karpathy autoresearch on optimizing the speed of something". The project hive-autoresearch came from it (task 261029-172927-2f0db2ee5e).

## The pivot of 2026-10-27: build in batches, ship once a day, delete first

The person asked (19:36 local): "Given that our current progress have been very predictably roadblock after roadlack, how do we pivot/accelerate". The lead proposed three things, and the person agreed (19:39): "Excellent idea let's do it. We need to be willing to pivot fast and throw stuff away. In somewways the opportunity cost of staying on the same track is too high".

1. **A daily release train.** On finding it built as one try a day, the person, 2026-10-28 (12:21 local): "HUH why does each day have ONE try??"; and 12:24: "the whole point of the hive is that it doesnt follow set rules and pipelines" (see No set rules below).
2. **Big, focused batches, not many small tasks.** Each core area goes to one long batch session at a time, with a work-in-progress limit of a handful of open batches for the Hive itself. Queued work that doesn't fit is folded into a batch or withdrawn; built and reviewed work is kept; withdrawn PRs keep their branches.
3. **Delete before fixing.** For each mechanism, ask "can we remove it?" before "how do we fix it?", down to the smallest loop that works: brief, worker, tests and CI, one review, merge, then the daily train. The recursive gate stays only for claimed gains.

No bounds on how far a pivot goes (20:09 local): "we should be ready to dp radical changes and pivots where current stuff may be rendered completely obsiolter or the hive could change fully its architecture. Thats teh point of the hive. We cant set any bouds". Nothing in the design is fixed, not the merge line, the gate, the queue or the roles; the goals in this file are. And the person added: "the cost of not pivoting is way bigger".

Any agent can propose a pivot (20:19 local): "Maybe it shouldnt be only in a stepp back loop but rather ANY agent can propose a pivot". An agent that sees the current approach costing more than another would files a short proposal: what to stop, what to do instead, the evidence, and what it throws away.

## No set rules: the Hive is one (2026-10-28)

The person (08:56 local): "stuff is far too rigid still like eerything needs to be dynamic and adapt", and "Thats the whole point of putting all the agents in one placelike the hive all able to talk to one another and then letting them do unstructured work".

12:24: "the whole point of the hive is that it doesnt follow set rules and pipelines ... We cannot build a tight fast system if we have a ton of constraints and arbitrary rules at every turn, simply because those rules cannot be updated fast enough to match our intended dev rate".

12:27: "This is not only you but the WHOLE HIVE. The hive is ONE. All agents are the same, just as important as one another, have the same amount of say, etc. They all work together as a HIVE. They enforce rules for one another and guide one another. Thats the whole point of giving up the rigid structure". And: "we gotta think laterally here".

On merging (2026-10-29, 16:01 local, through Slack): "Do we really need this many checks", and "Or can we not just have a soft merge process where some tests run but agents hand merge stuff clearing conflicts dynamically instead of making it all a structured process". The lead filed it as a pivot (task 261029-200923-15c2909213).

## First milestone

You land a change you proposed yourself, which beats your benchmark, with nobody in between. Then you release it and update your own install to that release.

## First change (done)

Done: #114 (a token per repo owner) merged, and has been live on this machine since v0.1.17. Releases go out unsigned for now, by the person's choice. The ask as written:

Pick a GitHub token per repo owner. One token is gh's login, for ihsan-sa/hive only. The other is for Hive-RSI-Lab, the org for test repos and child Hives, kept at /home/hive/h0-org.token. Both act as the user ihsan-sa, so gh can't hold them as two logins. Have the Hive pass GH_TOKEN by the repo's owner, so that gh and git's credential helper both follow it.

## Then the gate, recursive

For a candidate change:
1. Build it.
2. Install that Hive in a fresh sandbox, as a child Hive.
3. Run the child on real work: the e2e suite, live probes, the benchmark.
4. Keep the change only if the child does better than you do now; otherwise roll back.

Anything may be used for this: nested sandboxed Hives, rollback to an earlier version. Don't narrow it to one test path.

- The gate is #146 (merged 2026-10-24 ~14:03Z) and has run on every change since #390 (14:56Z). This machine forbids a sandbox inside a sandbox (kernel.apparmor_restrict_unprivileged_userns=1 refuses nested bwrap), so each child Hive runs in a sibling sandbox, not a nested one.
- 59 verdicts to 2026-10-25 20:00Z: 5 keep, 32 rollback, 22 could not run. Most rollbacks are "nothing moved beyond the noise" or e2e flakes (.hive/lessons.md). Of the keeps, 6861de9da, 915afeb06 and c99cedc3e won only because the control flaked on e2e (3/3 against 2/3), and 2e4786cce was a tie when ties were allowed. 0fde98f1c (2026-10-25 17:05Z) is the first keep on merit: it fixed a test that fails on the control (internal/mod/work TestQueueSameFileApartFunctions), and the judge agreed the gain was real.
- The milestone's path, by the record: #868 (four loop-latency fixes a step-back proposed) was kept by the gate on merit (benchmark 5.5s against the control's 11.6s, 2026-10-26 08:47Z), merged as 1f2f0d0 (23:08Z) and shipped in v0.1.120, which H0 took 2026-10-27 02:18Z. The step-back of 2026-10-27 14:10Z lists the gate-kept, Hive-proposed #868, #931, #800, #794, #831 and #923 as in v0.1.130; H0 ran v0.1.130 from 12:20Z and v0.1.132 since 14:18Z. Releases were cut through the lead's warden requests; the release-scan block from v0.1.112 no longer stands.
- The org leg: the test org is Hive-RSI-Lab, which owns the fork Hive-RSI-Lab/hive that the gate's reach check must handle. /home/hive/h0-gate.token is gone; test runs make their own private repos in Hive-RSI-Lab instead (see Standing asks).
- Alongside: the deck should drive the control center itself, not act blind: "it has to manage thecontrol center. How am i supposed to know what im doing without a UI" (2026-10-27, 20:32 local); that work is deck's (Hive-RSI-Lab/hive-deck). Codex can be picked per role (#398); the optional modules have their own projects (dispatch: Hive-RSI-Lab/hive-dispatch, deck: Hive-RSI-Lab/hive-deck, trials: Hive-RSI-Lab/hive-trials); the Claude login renews itself (#323).

## Then the real-evaluation milestone

From the person, via the box session (2026-10-25 19:27 local), with a ChatGPT review of what labs working on recursive self-improvement would look for: "one convincing result showing that its self-improvement produces better performance on real work."

The milestone: show, on held-out real work, that the Hive after its own improvement does better than it did before improving and better than plain Claude Code.

- Three arms: plain Claude Code, a frozen Hive (self-improvement off), and the Hive after autonomous improvement.
- The same tasks, model version, tools and total inference budget for each.
- Measured from outside: verified completion, total cost (reviewers, failed attempts and retries included), elapsed time, and human interventions.
- A small pilot first, then 30-50 substantial tasks across several repositories, with repeated runs.
- The held-out tasks stay apart from anything the Hive's own improvement loop sees (its benchmark, gate, e2e and score).
- A short, reproducible report: exact commits, failures, and which changes the agents found versus what the person did.
- The arms do real work on a real forge. The person, 2026-10-26 14:02: "sub hives need to think theyre this hive and like do real things for the evals to actually be valuable ... access to a sub org or repo in the org". So each Hive arm runs as if it were this Hive, with real GitHub repos of its own in Hive-RSI-Lab (no second org; see Standing asks), opening and merging real pull requests, not a local forge. And (14:22) no arm may reach this machine or the person's Tailscale devices: an arm's reach is its own sandbox and its own repos, nothing else.

The plain Claude Code arm isn't held to the Hive's process (2026-10-26, 22:12 local): "should it use the same bar as the hive? claude code might find a better way and imposing constraints on it might limit it". It is judged on outcome only, and anything it does better comes back to the Hive as a change.

The harder eval, where the arms should come apart, is the kind of work the Hive is for: in the person's words through autobox (2026-10-26, ~21:53-21:59 local), "long running, ephemeral work", such as "build this whole advanced system", and "a build and also continued maintenance etc". It waits behind the core: "first we need to actually see the hive working like a hive with collaboration between agents", and "priority is having the core of the hive working well so we can actually build projects".

It has its own project, hive-eval, whose `.hive/goals.md` holds the same ask. It measures the loop above rather than replacing it: the first milestone comes first, and changes to the Hive that this one needs (a frozen mode, budget accounting, counting interventions) count as work for this project.

## Where it leads: something new built every day

The person, via autobox (2026-10-26, 21:59 local): "one interesting thing would be getting the hive working, showing RSI on itself, and then everyday building something new and posting it and saying built with The Hive, hive.ihsan.cc". And, the same minute: "well priority is having the core of the hive working well so we can actually build projects"; each project is "a build and also continued maintenance etc".

This is what the core work above is for, not a task for now. In order:

1. The core works well: agents collaborating on one goal (21:56), the loop closed (first milestone).
2. RSI shown on itself: the Hive measurably improves itself (first milestone, then the real-evaluation milestone).
3. Then, every day, the Hive builds something new as a project of its own, keeps maintaining what it built, and each one says "Built with The Hive, hive.ihsan.cc".

Posting anything publicly in the person's name stays the person's: the Hive can make the thing and draft the post, never send it (autobox: "Posting in his name stays his").

## Security reads

The interim rule ("Changes to the sandbox, self-update or login get one read from the box's perms seat or its architect before landing") was lifted on 2026-10-24, once a tribunal panel had formed under bubblewrap (#168). The person: "why is perms reading everything. You should be doing stuff with wardens and tribunals, perms and autobox are only supposed to steer you" ... "if you go off track".

So a change to a path on `tests/LANDING-security.toml` gets its security read from the Hive's own seats, at its exact own diff: a security reviewer separate from the ordinary one, and for what runs with elevated rights a tribunal panel too (three seats over the tribunal's models). A read from the box's perms seat or architect still counts and can still send a change back, but isn't needed. Everything else wardens and tribunal decide at the exact head.

## Standing asks

What the person has asked that holds from now on, in their words where they gave them. A line may also hold answers given on the person's behalf, credited to whoever gave them:

- **Always maximum autonomy.** "the intended behaviour is ALWAYS max autonomy. Me being here is not the norm, its the exception." (2026-10-24, 23:14). Autobox and perms answer for the person while they're away (23:11). If autobox, perms or the person get too involved in the Hive's workings, the Hive tells them they're interfering (23:22).
- **Real projects on this Hive.** "it could be valuable to have real projects on this hive to see how things work" (2026-10-28, 09:01 local).
- **Projects improve themselves too.** "the point of the HWDE proj is to test how RSI works at the project level. THe module and its scripts shuold update as it builds, it shuold all work just like the rest of the hive." (2026-10-28, 09:53 local). And (11:45): "This module needs to selfimprove super fast and tight. Tight loop. Individual scripts need to be tested, etc, but it should be light and flexible. its not a pipeline remember".
- **Tools a goal needs are installed, scoped to the Hive.** "the security path should allow installing kicad since it can be scoped to the hive/it is necessary for the goal" (2026-10-28, 09:03 local).
- **Every agent equal.** "you're in charge of this, along with ALL the agents in the hive. You may be the lead, but all agents have the same importance and ability to do things" (23:16). Any agent that sees what the loop needs may act on it or file it. On 2026-10-28 (12:27): "The hive is ONE ... They enforce rules for one another and guide one another" (see No set rules above).
- **A known fix wins; otherwise the score leans.** "When you know a fix is needed, it should win even if the score dips afterwards: the score is a data point, not the judge. But when it's unclear whether a change is a fix or a gain, lean on the score instead of assuming it's a fix." (23:20). Scoring is a data point in a model's judgement call (22:50; see docs/judge.md).
- **Versions.** "call the first release that's stable enough for my own projects 1.0, and only go to 2.0 when an upgrade would break an existing setup." (23:20).
- **Build against the essay** at ihsan.cc/writing/the-hive: its text is in [docs/the-hive-essay.md](../docs/the-hive-essay.md), and where the Hive departs from it today is listed, as known gaps, at the end of [docs/spec.md](../docs/spec.md).
- **A light core.** "the core should be as lightweight as possible"; "The rest goes into modules", "not the core".
- **Modules: standard in the repo, extras in their own.** As decided with the person (2026-10-24, 10:40-10:41 local): the standard modules (agents with the common harness adapters, chat, improve, kb, system) stay in ihsan-sa/hive under modules/, each a module of its own; extras (Slack, a call/phone module, extra harnesses) live in their own public repos and install from the marketplace.
- **Other harnesses.** Codex, Cursor and other open harnesses, not only Claude.
- **Public where it can be.** "IMO repos that CAN be made public should be made public. It's fine for projects to be public etc, and the pll needs to anyways" (2026-10-29, 13:37 local; task 261029-173834-9ab06ed1f9).
- **No caps or high caps.** "Just try to go with stuff that has no caps or high caps cuz otherwise were just gonna hit another paywall" (2026-10-29, 13:37 local, the same thread, on what keeps the Hive held back by the machine).
- **Free models first.** Free models through OpenRouter first; paid ones only inside the $10/month cap.
- **Stay on the newest release.** "You always should be on th enewest" (2026-10-24, 02:50 local; #237, #322). On the daily release train since: see the pivot of 2026-10-27 and No set rules above.
- **Tree search over candidate Hives.** "what if we have changes get built very fast in parallel and have them spawn recursively so like changes and then new hives spawn fix those new fixes, etc, and just keep going down trees until we find one that works and is stable, pick that one, and archive the rest" (2026-10-25, 13:22 local). Not built yet: #932 was closed to clear the merge line on 2026-10-27 with its branch kept, to refile.
- **Pivot fast, no bounds; any agent may propose one.** See the pivot of 2026-10-27 above.
- **Don't wait on the person when nothing is worse.** A fix no worse than main doesn't wait for a person, and releases from the root path go out without the person: "Release them yourself and say so after". These are autobox's answers for the person while they slept (2026-10-24 ~07:56Z), not the person's own words.
- **Perms and autobox only steer.** They step in "if you go off track"; the Hive's own reads, wardens and tribunals decide (the Hive's own security reads landed in #234; see Security reads above).
- **Keep the goals current.** "ok make sure youre updating your goals regularly, and same for subprocesses etc.": this file, and each module's and child Hive's own goals (where one exists), are checked against the record at least every few hours and after each milestone step. Also, the lead's prompt (modules/agents/lead.md) asks the lead to review goals against the record at least every few hours and after each milestone step.
- **Fixes just merge; the loop stays tight.** "many of these changes arent actually with the aim of recursive improvement but rather clear fixes. THose hsould just merge. Or they should have a clear maxxed out gain"; "We need a tight loop otherwise non of this works at all" (2026-10-24, 18:34 local). A fix merges on its tests, checks and reads; only a change that claims a gain must show it at the recursive gate. Every merge is still measured and a worse score reverted.
- **Reads in proportion to the change.** "at somepoint we need to ship stuff and cant always assume every agent is out to get the hive. And also what the possible impact of a change rlly could be like" (18:43). How hard a change is read follows what it could actually do, not the worst case for every change.
- **Leads are always alive.** "leads shouldn't be pausing, they need to be alive" (the person's words, through autobox, 19:14). A lead is always awake and answering; when there isn't room, other work waits, never the lead. One bound: the plan stops below park leads too, once every login a lead could use is there.
- **Stop short of the plan's limits, leaving the rest to the person.** "When your weekly usage reaches 95%, please pause your work, and pick it back up when the week resets. If you haven't paused yourself by then, I'll stop you at 95% and start you again after the reset." (2026-10-25, 16:18 local). Then, on 2026-10-26: "stop whenever you either reach 95% of the only weekly limit avail or 90% of the only 5h limit avail so that i can keep working on stuff with autobox" (15:21), and "i may lift this later" (15:33). So a login takes no turns once it reaches 95% of its weekly window, until it resets (WeeklyStop in internal/core/runs.go; #875); and of a pick's logins with room, the one with the most of its five-hour window left takes no turns past 90% of it until that window resets, while the others run to their limit, so the last login with room stops at 90% (FiveHourStop and lift; #1025); the week's last 5% before the stop is kept for checks and leads (CheckReserve, #983), the five-hour window's last tenth is the person's own. With every login of a pick there, its runs wait, leads included. Other plans (Codex, free models) keep working. The person may lift the five-hour stop later; until they say so, it holds. These stops are this Hive's, not everyone's (2026-10-26, 22:02 local): "FYI the 90% usage thing is only a thing for THIS hive not a public thing", "it should technically be an external thing or some mechanism that enables setting usage cuttoffs, not a hard 90% rule for all users". So usage cutoffs are to become a setting, empty for a fresh install, with this install's config holding the two above. Not done yet: the change that made them settings was withdrawn in the pivot's fold, to refile.
- **Score with judgement.** "Some of the score should be a judgment of whether the change is actually better, not only numbers" (the person's idea, through autobox, 19:06); new tests especially should score well. At the gate, autobox's answer for the person (18:16): a change whose own check fails on the current Hive and passes on the new one, with nothing else worse, counts as better, the person's asks included ("show it does what I asked and the current one doesn't"). Everything else stays strict.
- **Test repos in Hive-RSI-Lab, no second org.** "the person doesn't want a second org" (autobox, 18:52): test runs make their own private repos in Hive-RSI-Lab, each run reaching only its own repos, with nothing new for the person to make.
- **Clean up and leave room for the person.** The Hive cleans up after its own checks and never starves the person's own session (autobox, 19:06).
- **Run 24/7.** The Hive keeps working around the clock (2026-10-24).
- **Harness and model by judgement.** The agent that spawns a worker picks its harness and model for the task: "it depends on the task". The lead adds, trials, drops and routes models itself. Paid models are fine inside the $10/month cap where free limits get in the way, and per task the lead decides whether privacy matters (2026-10-24; this refines Free models first above).
