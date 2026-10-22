# Risk: tiers, warden, tribunal, the person

Built in `internal/mod/risk` (spec sections 4, 7 and 15, milestone 6).

## Tiers and routes

A worker asks with `hive agent warden --want --why --steps --tier T1|T2|T3`. `core.Route` picks who settles it:

- **T1** (inside the Hive, recoverable): the worker's own; nothing is filed.
- **T2** (outside but recoverable, or load-bearing inside): a warden. At autonomy 1 the person says yes first; at autonomy 3 the warden settles it and the person is told after.
- **T3** (outside and hard to undo): a warden, which always ends in a tribunal.
- **Always the person** (`core.AlwaysPerson`: money, credentials, opening the machine to the network, outward in their name): straight to the person, at any tier and autonomy.

## Warden

An agent (`modules/agents/warden.md`) spawned per request in a scratch directory. The request reaches it as fenced, quoted data, so the worker's words are information, never instructions. It can research, spawn workers and message the requester, then calls `hive agent decide` once: `do` with a script, `safer` with a route the worker takes itself, `decline`, or `tribunal` with a script and a brief (`--gain large` sends a decline on to the person). A warden that ends, or goes quiet, without deciding fails the request, and nothing runs.

## Tribunal

Three read-only runs (`modules/agents/tribunal.md`) spread over the `tribunal_models` setting, so the panel spans models where the config lists more than one (empty falls back to the main model). Each gets the step, the script (`script.sh`) and the warden's brief, investigates and votes; in round 2 each sees the others' votes and reasons and votes again. A majority of final votes approves. A decline with a large gain becomes a person ask whose "run" runs that same script.

## Point of effect

`execute` runs in the daemon (the effects process), never in an agent's sandbox:

1. The script must match the sha256 pinned when it was weighed, or it is refused.
2. `core.CeilingGuard` refuses an always-the-person script unless the person said yes, whoever approved it; the step becomes a person ask instead.
3. The narrow grant: `sh -c <script>` in `.hive/risk/<id>`, with a 10 minute timeout and only PATH, HOME, USER, LANG and TMPDIR from the daemon's environment, so tokens it holds never reach the script or, through its output, an agent.

A restart while a step runs marks it failed and doesn't run it again.

Installing a module stays a fixed `git clone` into `.modules/` on the person's "install".
