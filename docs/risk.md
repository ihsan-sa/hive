# Risk: tiers, warden, tribunal, the person

Built in `internal/mod/risk` (spec sections 4, 7 and 15, milestone 6).

## Tiers and routes

A worker asks with `hive agent warden --want --why --steps --tier T1|T2|T3`. `core.Route` picks who settles it:

- **T1** (inside the Hive, recoverable): the worker's own; nothing is filed.
- **T2** (outside but recoverable, or load-bearing inside): a warden. At autonomy 1 the person says yes first; at autonomy 3 the warden settles it and the person is told after.
- **T3** (outside and hard to undo): a warden, which always ends in a tribunal.
- **Always the person** (`core.AlwaysPerson`: money, credentials, opening the machine to the network, outward in their name): straight to the person, at any tier and autonomy. `core.CeilingHit` spots these by keywords and credential-store paths in the text, so it is a best-effort check that errs toward the person: an agent that hides the words (a variable, base64) gets past it.

## Warden

An agent (`modules/agents/warden.md`) spawned per request in a scratch directory. The request reaches it as fenced, quoted data, so the worker's words are information, never instructions. It can research, spawn workers and message the requester, then calls `hive agent decide` once: `do` with a script, `safer` with a route the worker takes itself, `decline`, or `tribunal` with a script and a brief (`--gain large` sends a decline on to the person). A warden that messages another agent waits for the reply, as it does for a helper it spawns. A warden that ends, or goes quiet, without deciding fails the request, and nothing runs.

## Tribunal

Three read-only runs (`modules/agents/tribunal.md`) spread over the `tribunal_models` setting, so the panel spans models where the config lists more than one (empty falls back to the main model). Each gets the step, the script (`script.sh`) and the warden's brief, investigates and votes, as a structured final answer (`VoteSchema`, the way the reviewer gives its verdict, since a read-only turn has no shell); in round 2 each sees the others' votes and reasons and votes again. A seat that fails to reopen for round 2 three times counts as a decline, so the request still settles. A majority of final votes approves. A decline with a large gain becomes a person ask whose "run" runs that same script.

## Point of effect

`execute` runs in the daemon (the effects process), never in an agent's sandbox:

1. The script must match the sha256 pinned when it was weighed, or it is refused.
2. `core.CeilingGuard` refuses an always-the-person script unless the person said yes, whoever approved it; the step becomes a person ask that keeps the warden's or tribunal's reasoning. It is the same best-effort text check as above, a last check rather than enforcement.
3. The narrow grant: `sh -c <script>` in `.hive/risk/<id>` (Git for Windows' `sh` on Windows), with a 10 minute timeout that kills the script's whole process group (on Windows it ends only `sh`, whose children may outlive it), and only PATH, HOME, USER, LANG and TMPDIR from the daemon's environment, so tokens in that environment never reach the script.
4. The script runs as the daemon's user and can read that user's files, so its output never goes back to the agent: the agent hears only done or failed, and the output is kept on the request for the person. A scratch HOME would not close this, since a script can name the person's home outright, and it would break steps that need the person's own tool logins.

A restart while a step runs marks it failed and doesn't run it again.

A worker that finishes with no commits finishes its task done when every step it asked for ran and succeeded, and the person is told what ran; if any was declined or failed, the task is blocked with each step's state.

Installing a module stays a fixed `git clone` into `.modules/` on the person's "install". A module that carries a program starts only after a read-only review run approves its code at the commit checked out, and again after each update. The program runs as the person outside the sandbox, so that review is the trust boundary (see [architecture.md](architecture.md#modules-that-run-a-program)).
