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

Several of these vendors also serve an Anthropic-compatible API that Claude
Code itself can drive. That route, and other harnesses with a CLI of their own
(Kimi's CLI, OpenCode, Aider), come in as harnesses through the harness-module
seam rather than as more code in the open agent.

A key is a credential: it is stored in `.hive/logins/open/<name>.key` (0600),
which no run sees, and reaches only a turn on that model: the run manager writes it to a file in
the run's home, which the open agent reads and deletes before any tool runs
(and the run manager deletes when the turn ends), so it is never in an
environment a command could read. A run on a hosted model may reach that
endpoint's public host besides its egress list; an endpoint on this machine or
a private network (any IP that isn't public, `localhost`) only through the
person's `hive egress grant`. Ollama's default context is small:
set `OLLAMA_CONTEXT_LENGTH` (or a Modelfile's `num_ctx`) to the `context` given here.

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
reset. A lead follows the settings' route at every turn. The dashboard's agent
lines and `hive agent models` say which `harness:model` each agent runs on.

## Asking the lead

`hive agent route --role worker --models "qwen,claude:opus" [--project p]`
(lead only; a project's lead routes its own project) writes the route into
config.json and journals `config.routed` with who, the role and what it was. A
check seat's route waits for the person's yes in Waiting (ask kind `route`).
The lead never adds a model or its key; those are the person's.

## Trying a model

`hive agent trial --model qwen --project lab --title "..." --done "..."` files
a brief whose worker runs on that model alone (no fallback); the checks stay
on their own models. When it ends, `model.trial` records the outcome (merged,
done, blocked, stopped), turns, review rounds, minutes and cost, and
`hive agent models` lists it. A trial belongs on a test project, not real work.
