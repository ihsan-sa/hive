# Models: free ones, routes, fallback and trials

The Hive can run its agents on any model behind an OpenAI-compatible chat
endpoint, besides Claude Code and Codex, so it keeps working when their usage
limits are reached. Built in `internal/openagent` (the open harness),
`internal/core/route.go` (routes), `internal/core/runs.go` (moving at a limit)
and `internal/mod/models` (the lead's ops and trials).

## The open harness

`hive open-agent` is the Hive's own agent loop: it sends the conversation to
`<endpoint>/chat/completions` with tools (`bash`, `read_file`, `write_file`,
`edit_file`; a read-only turn gets `read_file`, `list_dir` and `grep` over its
checkout and run folder only, and a turn with a schema an `answer` tool) and
writes Claude Code's stream-json, so the run manager reads it as it reads
Claude. Its conversation is kept in the run's home (`~/.hive-open/<session>.json`),
so a later turn resumes it. It trims old tool output to fit the model's
`context`, waits out a rate limit that resets within two minutes, and reports
a longer one (HTTP 429, with `Retry-After` or OpenRouter's `X-RateLimit-Reset`)
as a usage limit with its reset. A tool call a small model writes as JSON text
is read as the call.

## Named models

The person names each model once (`hive model add`, or `models` in
`.hive/config.json`):

```
hive model add qwen http://127.0.0.1:11434/v1 qwen2.5-coder:14b
hive egress grant 127.0.0.1:11434          # a model server on this machine (#162)
hive model add gpt-oss https://openrouter.ai/api/v1 openai/gpt-oss-120b:free --key
hive model key gpt-oss                     # the key, read from stdin
```

(A model on an Anthropic-compatible API runs on Claude Code instead: see
below.)

```json
"models": {
  "qwen":    {"endpoint": "http://127.0.0.1:11434/v1", "model": "qwen2.5-coder:14b", "context": 32000},
  "gpt-oss": {"endpoint": "https://openrouter.ai/api/v1", "model": "openai/gpt-oss-120b:free", "key": true}
}
```

No vendor is built in: any endpoint that speaks OpenAI's chat completions
with tool calls works the same way. For example (check each vendor's current
model ids and free tiers):

| Model | Endpoint | Example id |
|---|---|---|
| Kimi (Moonshot) | `https://api.moonshot.ai/v1` | `kimi-k2-0905-preview` |
| DeepSeek | `https://api.deepseek.com/v1` | `deepseek-chat` |
| Qwen (DashScope) | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | `qwen3-coder-plus` |
| GLM (Zhipu) | `https://open.bigmodel.cn/api/paas/v4` | `glm-4.6` |
| OpenRouter's free models | `https://openrouter.ai/api/v1` | `moonshotai/kimi-k2:free` |
| Ollama on this machine | `http://127.0.0.1:11434/v1` | `qwen2.5-coder:14b` |

OpenRouter is free first, paid under the month's cap (#1841): the person's key
there pays for every model but the free variants (an id ending in `:free`). A
paid one is refused by `hive model add` and the config check unless
`openrouter_monthly_usd` (the month's cap, UTC; 0 keeps OpenRouter free-only)
is set and the model has a `price` (`hive model add ... --price IN,OUT`, USD per
million tokens), so its spend is counted (`core.Config.PaidRefused`). The rest
is the models module's (`internal/mod/models/paid.go`): a route puts a paid
OpenRouter model after every other pick, so a run reaches it only when the
free ones (and the plans) are at their limits; it counts what paid turns cost
from the journal, gives each turn no more than is left of the cap less what
the paid turns running were given, and once the month's spend reaches the cap
holds the model until the next month, through the run manager's `Held` and
`TurnBudget` hooks. The open agent sends an unpriced paid model no request
(`TestCheckAllowsAPaidOpenRouterModelUnderTheCap`,
`TestPaidOpenRouterModelUnderTheCap`, `TestPaidOpenRouterModelGoesLastOnARoute`,
`TestModuleHoldsAPickAndLimitsATurn`, `TestModelAddWithAPrice`,
`TestNoRequestForAnUnpricedPaidOpenRouterModel`).

Other harnesses with a CLI of their own (Cursor, OpenCode, Aider) come in as
harnesses through the harness-module seam rather than as more code in the open
agent ([harnesses.md](harnesses.md)).

## Anthropic-compatible models, on Claude Code

Moonshot (Kimi), DeepSeek and Zhipu (GLM) also serve an Anthropic-compatible
API, made for Claude Code. A named model with `"api": "anthropic"` (`--anthropic`
to `hive model add`) runs on Claude Code itself instead of the open agent, so
it gets Claude Code's tools, compaction and stream-json:

```
hive model add kimi https://api.moonshot.ai/anthropic kimi-k2-0905-preview --key --anthropic
hive model key kimi
```

```json
"kimi": {"endpoint": "https://api.moonshot.ai/anthropic", "model": "kimi-k2-0905-preview", "api": "anthropic", "key": true}
```

| Model | Endpoint (Anthropic API) | Example id |
|---|---|---|
| Kimi (Moonshot) | `https://api.moonshot.ai/anthropic` | `kimi-k2-0905-preview` |
| DeepSeek | `https://api.deepseek.com/anthropic` | `deepseek-chat` |
| GLM (Zhipu, Z.ai) | `https://api.z.ai/api/anthropic` | `glm-4.6` |

It is still the named model `kimi` (`open:kimi` on the dashboard) and routes,
falls back and is tried like any other. Its turn runs `claude` with
`ANTHROPIC_BASE_URL` set to the endpoint and `--model` (and the models Claude
Code uses for its own side requests and subagents) set to the endpoint's id;
`context`, if given, is Claude Code's auto-compact window. No Claude login
reaches it: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and
`CLAUDE_CODE_OAUTH_TOKEN` are blanked in its environment, and its egress list
gets the endpoint's host, not Anthropic's. The key goes in the same file in the
run's home, and Claude Code's `apiKeyHelper` (given with `--settings`) is
`hive open-key <file>`, which prints it and deletes the file; Claude Code calls
it once, before its first request, and keeps the key in memory for the turn
(`CLAUDE_CODE_API_KEY_HELPER_TTL_MS`), so no tool finds it in a file or in
its environment. The turn loads no settings file (`--setting-sources ""`):
the checkout's `.claude/settings.json` is the agent's or the repo's to write,
and a hook there could read the key file before the helper runs, or its `env`
send the key to another host; `--settings` pins the helper, the endpoint and
`disableAllHooks`; `--strict-mcp-config` keeps a project `.mcp.json` server
from starting (checked on Claude Code 2.1.310: neither a hook nor such a
server ran). Claude Code's config folder is the run's own
(`CLAUDE_CONFIG_DIR=<run home>/.claude-open`), so a bare run, whose HOME is
the daemon's, finds no Claude login there; and when the helper fails Claude
Code sends no credential at all rather than falling back to a login. The
daemon writes the key file, and makes that folder, without following a link
an earlier turn left; `hive open-key` reads only a regular file, never one a
link points to, and the helper's words are shell-quoted. A vendor's 429 parks
the model (an hour, since Claude Code passes on no reset) and the run moves
to its fallback, as on the open agent. If the endpoint refuses the key
mid-turn (a 401), Claude Code asks the helper again, finds nothing, and the
run parks saying the key was refused mid-turn and the next turn gets it
afresh. A second refusal in a row is a key the endpoint won't take: the model
is marked down like any 401, and the run moves on its route until the stored
key changes.

A known residual: while a turn runs, the key is in Claude Code's memory, and
on a host where Yama's `ptrace_scope` is 0 a tool in the same sandbox, as the
same user, could read it through `/proc/<pid>/mem`. The open agent makes
itself non-dumpable, which closes that for it; Claude Code is a program the
Hive doesn't control, so for it the exposure is the key of the model the run
is already on.

`TestAnthropicModelTurn` holds the turn's command, environment and egress;
`TestAnthropicModelWorker` (e2e) runs a worker on real Claude Code against a
stub Anthropic endpoint when `claude` is installed, with a decoy Claude
login in the run's HOME that must never reach the endpoint. It is a live probe
with `HIVE_E2E_ANTHROPIC="<endpoint> <model id>"` and the key in
`HIVE_E2E_ANTHROPIC_KEY`, or with `HIVE_E2E_ANTHROPIC=<name>` for a model this
machine's Hive has set up, using the key `hive model key <name>` stored (a
live probe spends the vendor's tokens, so it runs only when asked).

A key is a credential: it is stored in `.hive/logins/open/<name>.key` (0600),
which no run sees, and reaches only a turn on that model: the run manager writes it to a file in
the run's home, which the open agent reads and deletes before any tool runs
(and the run manager deletes when the turn ends), so it is never in an
environment a command could read. A run on a hosted model may reach that
endpoint's public host besides its egress list; an endpoint on this machine or
a private network (any IP that isn't public, `localhost`) only through the
person's `hive egress grant`. Ollama's default context is small:
set `OLLAMA_CONTEXT_LENGTH` (or a Modelfile's `num_ctx`) to the `context` given here.

An endpoint holds no user, password, query or fragment (config.json is
refused with one): agents see endpoints in the models op and the person in
`hive model`, so a key goes only through `hive model key`. `hive model add
--key` and `hive model key` refuse a plain `http://` endpoint on a public
host, where the key would cross the network unencrypted; http is for this
machine or a private network. `hive model add` says when it replaces a model
of the same name, and `hive model rm` removes nothing (model or key) while a
route or model setting still names it, and says which.

A hosted tier sees the work it runs: the brief, the checkout's files the agent
reads, its tool output and notes. Route a private or isolated project to one
only on purpose; a project's own route (`projects.<name>.routes`) keeps the
others off it.

### Prices and budgets

A turn's cost is never what the harness's line says (the open agent knows no
prices, and Claude Code prices another vendor's tokens as Claude's). A paid
tier can carry its price, in USD per million tokens:

```json
"deepseek": {"endpoint": "https://api.deepseek.com/v1", "model": "deepseek-chat", "key": true,
             "price": {"input": 0.27, "output": 1.1}}
```

Its turns then cost their tokens at that price (the cache's reads and writes
as input) in the run's cost, the dashboard and the monthly spend. A model with
no price counts $0. A run with a budget (a step-back's) starts on a named
model only if the budget can be held (`OpenModel.Holds`): a model that costs
nothing (no key, or a price of 0 for a free tier), or one with a price on the
open agent, which counts each request's tokens and ends the turn once it
reaches the budget. A keyed model with no price, or a priced one on Claude
Code (which can't hold a turn to a vendor's price), refuses it. A budgeted run
that moves to a named model along its route at a usage limit isn't checked
again there.

## Routes and fallback

`routes` choose the models each role runs on, in order:

```json
"routes": {
  "worker":   ["claude:opus", "codex:gpt-5", "qwen"],
  "fallback": ["gpt-oss", "qwen"]
}
```

Roles: `lead`, `worker`, `reviewer`, `security` (the security seat),
`warden`, `tribunal` (its seats, in place of `tribunal_models`), `sweep`, and
`fallback`. An entry is a named model, `harness:model`, or a model on the
workspace harness. A project's own (`projects.<name>.routes`) comes first. A
role with no route runs on the model settings (`harness`, `model`,
`lead_model`, `reviewer_model`, a project's own). `fallback` follows every
role's route except the checks (reviewer, security, warden, tribunal): a
weaker model needs the checks more, so they move only where the person routes
them.

A run carries its route (`RunSpec.Route`). When its own harness and model are
at their usage limit (every login of that harness parked, or the named model's
own limit), its next turn goes on the first pick with room; changing harness
(or open model) starts a fresh session on its brief, told why and that its
checkout and notes are intact. With no pick free it waits for the soonest
reset. A run that must finish (a worker) moves only to a pick some run has
finished done on with every turn on that pick (a trial, or real work that
started there), or back to its route's first: a model that never finished here
ended turns without finishing while others waited on it. A run proves the pick
as its route names it, since a run starts on its route's first pick. Proof is
Hive-wide and kept from the journal; a worker that skips an unproven pick at
a limit says so in its parked reason ("no worker has finished on open:qwen
yet"), so a trial there lets it fall back. A worker gone quiet moves to a
proven pick only when it is next woken. A lead follows the settings' route at every turn. The dashboard's agent
lines and `hive agent models` say which `harness:model` each agent runs on.

A pick whose workers on a project keep ending without a merge stops being
that project's first worker pick (`Agents.Demote`, from the models module's
tick): when the last two or more ended tasks there (merged, done or blocked;
trials and stopped tasks don't count) whose first worker started on it all
ended blocked, the latest within a week, new workers there start on the rest
of the route and it goes last, still a fallback. A merge or done on it puts
it back in front, and a week after its latest such ending the person's order
gets another try. `hive agent models` lists each one and the tasks behind it
(`demoted`). The person's route in config.json is unchanged.

### A model that can't answer

A pick whose turn ends in an error that isn't a usage limit, and that says
the model can't answer (a 404 such as OpenRouter's "no endpoints matching
your guardrail", a 401 or 403 on a named model's key, a 400 naming the
model) or the same error three turns running, is marked down (`model.down`,
`core.Down`). Runs skip it exactly as they skip a login at its limit: the
turn's run moves at once to the next pick with room, or waits for the first
reset, with no retry backoff. The person is told once per spell and cause
(Done for you), with the model's answer as it gave it and the fix, and the
lead's chat gets the same line once. `hive status`, the dashboard and
`hive agent models` list it with its last answer. A turn tries it again an
hour on, or as soon as its config (the named model's settings or stored key)
changes, and a turn or probe it answers brings it back (`model.up`).

`hive model add`, `hive model key`, the lead's `route` op and `trial` send a
named model one real request first and refuse one that can't answer, saying
why and what would fix it (`hive model add` on a model that needs a key not
yet stored tries it when the key is stored; `--no-probe` skips it, for a
server that isn't up yet). The route op doesn't probe a model already on that
route and not known to fail.

Only a named model is marked down by a 401/403, any 404, or the same error
three straight turns from at least two runs (an empty error never counts).
A Claude or Codex pick, and a 400 on any pick, is marked only when its answer
says the model doesn't exist (not found, unknown or invalid model): their
other errors, and a context too long, are one run's, which backs off alone.
A run parked waiting on a pick that comes back looks again at once.

## Asking the lead

`hive agent route --role worker --models "qwen,claude:opus" [--project p]`
(lead only; a project's lead routes its own project) writes the route into
config.json and journals `config.routed` with who, the role and what it was. A
check seat's route waits for the person's yes in Waiting (ask kind `route`).
The lead never adds a model or its key; those are the person's.

Any agent can spawn a helper on a model by name (`hive agent spawn --model`),
a named model included, so a helper can spend a hosted tier's free quota at
an agent's choice; a plain model name runs on its project's harness. Routes
in Settings are the person's: each change, workspace or a project's own
(listed under Routes once the project has one), is journalled as
`config.routed` by `person (Settings)` with what it was.

## Trying a model

`hive agent trial --model qwen --project lab --title "..." --done "..."` files
a brief whose worker runs on that model alone (no fallback); the checks stay
on their own models. When it ends, `model.trial` records the outcome (merged,
done, blocked, stopped), turns, review rounds, minutes and cost, and
`hive agent models` lists it. A trial belongs on a test project, not real work.
A lead files trials, and so does a worker on its own project: a test project
(trials) has no lead of its own unless the person chats with one, so its trial
rounds are briefs a worker carries out by filing one trial per model and task.
A trial's own worker files none, and one already open on the same model with
the same title is refused with its task id, so a round doesn't file twice.
