# Risk: tiers, warden, tribunal, the person

Built in `internal/mod/risk` (spec sections 4, 7 and 15, milestone 6).

## Tiers and routes

A worker asks with `hive agent warden --want --why --steps --tier T1|T2|T3`. `core.Route` picks who settles it:

- **T1** (inside the Hive, recoverable): the worker's own; nothing is filed.
- **T2** (outside but recoverable, or load-bearing inside): a warden. At autonomy 1 the person says yes first; at autonomy 3 the warden settles it and the person is told after.
- **T3** (outside and hard to undo): a warden, which always ends in a tribunal.
- **Always the person** (`core.AlwaysPerson`: money, credentials, opening the machine to the network, outward in their name): straight to the person, at any tier and autonomy. `core.CeilingHit` spots these by keywords and credential-store paths in the text, so it is a best-effort check that errs toward the person: an agent that hides the words (a variable, base64) gets past it.
- **The person's floor** (`floor` in `.hive/config.json`, empty by default): names never touched, paths no script writes, secret globs, service roots. A step whose text touches an entry goes to the person the same way, whatever a warden or panel would say (`core.FloorHit`: an entry with `*?[` matches a word or its last path element, any other entry matches as written). It is the same best-effort text check, so write entries as scripts would spell them (`.ssh/`, not `~/.ssh`).

A decision that would otherwise wait in the person's queue goes the same way through `risk.Weigh`: a merge that changes protected paths (goals, the score, settings; the ask quotes their diff) and a module install. At autonomy 1, or when the ceiling's words show up, the person decides as before. Otherwise a warden weighs it as T2 and may call a tribunal. No script runs: its `do` or decline answers the person's ask (merge or leave open, install or decline), so the ask's own handler acts, and a "told" item tells the person after. The worker that wrote a change never weighs it. A warden that ends without deciding hands the ask to the person.

## Warden

An agent (`modules/agents/warden.md`) spawned per request in a scratch directory. The request reaches it as fenced, quoted data, so the worker's words are information, never instructions. It can research, spawn workers and message the requester, then calls `hive agent decide` once: `do` with a script, `safer` with a route the worker takes itself, `decline`, or `tribunal` with a script and a brief (`--gain large` sends a decline on to the person). A warden that messages another agent waits for the reply, as it does for a helper it spawns. A warden that ends, or goes quiet, without deciding fails the request, and nothing runs.

## Tribunal

Three read-only, isolated runs (`modules/agents/tribunal.md`; no other project and no module in view, only the case) spread over the `tribunal_models` setting, so the panel spans models where the config lists more than one (empty falls back to the main model). A seat is a model name on the project's harness, or `{"harness": ..., "model": ...}` to sit on another; a harness the Hive doesn't have is refused when the config is saved and when a panel convenes. Each gets the step, the script (`script.sh`) and the warden's brief, investigates and votes, as a structured final answer (`VoteSchema`, the way the reviewer gives its verdict, since a read-only turn has no shell); in round 2 each sees the others' votes and reasons and votes again. A vote counts only as exactly one clean structured answer: a second result in the turn's output, a field the schema doesn't name or a wrong type is no vote, which is a decline. A seat that fails to reopen for round 2 three times counts as a decline, so the request still settles. A majority of final votes approves. A decline with a large gain becomes a person ask whose "run" runs that same script.

## Point of effect

`execute` runs in the daemon (the effects process), never in an agent's sandbox:

1. The script must match the sha256 pinned when it was weighed, or it is refused.
2. `core.CeilingGuard` refuses an always-the-person or floor script unless the person said yes, whoever approved it; the step becomes a person ask that keeps the warden's or tribunal's reasoning. It is the same best-effort text check as above, a last check rather than enforcement.
3. The narrow grant: `sh -c <script>` in `.hive/risk/<id>` (Git for Windows' `sh` on Windows), with a 10 minute timeout that kills the script's whole process group (on Windows it ends only `sh`, whose children may outlive it), and only PATH, HOME, USER, LANG and TMPDIR from the daemon's environment, so tokens in that environment never reach the script.
4. The script runs as the daemon's user and can read that user's files, so its output never goes back to the agent: the agent hears only done or failed, and the output is kept on the request for the person. A scratch HOME would not close this, since a script can name the person's home outright, and it would break steps that need the person's own tool logins.

A restart while a step runs marks it failed and doesn't run it again.

A worker that finishes with no commits finishes its task done when every step it asked for ran and succeeded, and the person is told what ran; if any was declined or failed, the task is blocked with each step's state.

## Egress

Every sandboxed run reaches the network only through the Hive's egress proxy, which refuses this machine and private ranges. `egress` in `.hive/config.json` narrows it: `default` is the hosts every run may reach, and `roles.<role>` (warden, tribunal, worker, lead, ...) replaces it for that role; a role set to `[]` reaches only its harness API host. A run's harness API host (`api.anthropic.com` for Claude) is added to any list. A project's checks (tests, builds) are held to `default`, or `roles.check`. With no list a run reaches every public host, as before. So an empty `default` means every public host while an empty role list means the API host only; that asymmetry is deliberate, since an empty `default` is the same as none and a role key set to `[]` is the only way to shut a role in. Bare runs have no proxy, so no egress list holds them; on Windows the list goes into WSL with the rest of the run's spec. A host matches itself and its subdomains. A refused host is recorded next to the turn's output and journalled as `run.egress_refused` when the turn ends. Sandboxed runs can't write the config, so the list changes only by the person's hand or an approved step. A config.json the Hive can't fully read, or with a seat or floor glob it can't use, keeps every setting that did read and says what's wrong in `hive doctor`, the control center's alerts and the daemon's start.

Installing a module stays a fixed `git clone` into `.modules/`, on the install answer of whoever decides it (see above).

## Updates

Everything that grants access lives in two folders covered in every sandbox, `logins/` and `.hive/private/`. A sandbox started by a daemon from before `private/` existed has no cover over it: it mounts all of `.hive` and covers only `logins/` and the old `daemon.json`, so once the new daemon moved a run's home there or wrote its token there, that sandbox could read them. So the first daemon start on the new layout ends every turn still alive from the earlier daemon, with the terminate-then-kill a stop uses, and waits for it to go before it moves anything or publishes its token. Ending the turns rather than waiting them out holds on bwrap, Seatbelt and WSL alike, because every backend starts a turn through the same detached process the stop path kills, while a turn can run for hours and the daemon can't serve without a published token; the runs resume from their sessions on the next tick. A move the daemon can't make (a place already taken keeps what it has, and the old folder goes beside it under `private/`; a Codex home whose place is taken is one ended turn's copy and goes) or a turn it can't end stops the start with the path named, so no agent reads state while a token sits in the readable tree. On macOS the Seatbelt profile holds a home's old path as a string, so a turn that did outlive the update would also lose its writable home; ending them first closes that too.
