# Harnesses as modules

A harness is the coding agent a run drives: Claude Code, Codex, the Hive's own
open harness, the fake one tests use, and any a module adds. Claude, Codex,
open and fake are built in (`internal/harness`); every other harness is a JSON
file a module ships, so adding one changes no Go code (issue #257).

The default `workers` module ships two: **cursor** (Cursor's CLI agent) and
**opencode** (OpenCode, open source). `hive harness` lists what is installed,
whether each program is on PATH and whether it has a key.

## Picking one

The same as Claude or Codex: `harness` in Settings for the whole Hive,
`projects.<name>.harness` for a project, and `harness:model` in a role's route
(`opencode:opencode/big-pickle`, `cursor:auto`) for a role. The pickers list
every installed harness and the models its spec names; a model the spec doesn't
name can still be typed.

A Claude model name left in the settings (`opus`) isn't passed to a harness
whose spec doesn't list it; it runs on its own default.

## Logins

- **Bare runs** use the harness's own login on this machine:
  `hive harness login cursor` runs `cursor-agent login`.
- **Sandboxed runs** have a home of their own, so the host's login isn't there.
  A harness whose spec names `key_env` gets the key the person stored with
  `hive harness key <name>` (kept in `.hive/logins/<name>/key`, which no run
  sees) in that variable, as Claude Code gets its token. With no key stored a
  sandboxed turn waits, saying what to run, unless the spec says `key_optional`.
- OpenCode's free models (`opencode/...`) need no login at all.

## Writing one

A module lists its harness files in `module.json`:

```json
{"name": "my-harness", "provides": {"harnesses": ["goose.json"]}}
```

Installing the module goes through the warden like any module (it brings
outside code: the spec decides which program runs and which hosts it may
reach). The program itself is the person's to install.

A spec (see `modules/workers/cursor.json` and `opencode.json`):

| Field | Meaning |
| --- | --- |
| `name` | what settings and routes call it; lower-case, not a built-in name |
| `title`, `description` | how the person sees it |
| `program` | a plain program name found on PATH (never a path) |
| `login`, `status` | its own login command, and one that exits 0 when logged in |
| `key_env`, `key_optional` | the variable it reads an API key from (see Logins) |
| `api_hosts` | hosts its client must reach, added to every run's egress (a host covers its subdomains) |
| `models` | models it knows, the default first |
| `prompt` | `stdin` (default) or `arg`, where `{prompt}` in the args carries it |
| `system` | `args` (default: the args or env name `{system_file}` / `{system_text}`) or `prompt`: the Hive's system prompt goes ahead of a fresh session's first prompt |
| `args` | `start`, then `new` or `resume`, `model` (when the turn names one), `read_only` or `write`, `sandboxed` or `bare`, `schema` (when the turn has one), `end` |
| `env` | `NAME=value` entries |
| `parse` | rules mapping its JSON output lines to the Hive's events |

Placeholders in args and env: `{session}`, `{model}`, `{prompt}`,
`{system_file}`, `{system_text}`, `{run_dir}`, `{schema_file}`, `{config_dir}`.
A harness with no `schema` args is told the schema in its prompt, and its
answer is the JSON object its last message ends with.

### Parse rules

Each rule has a `match` (path to value; a value starting with `!` matches
anything else, a missing path included) and an `event`; every rule that matches
a line adds its event, in order.

| Event | Fields |
| --- | --- |
| `init` | `session`, `model`: the session started (the id `resume` gets) |
| `text` | `text`: a message; `*` in the path gives one per element |
| `tool` | `tool`, `text`: a tool call and a summary of its input |
| `context` | `context`: what the session's context held |
| `usage` | `tokens`, `context`: one step's tokens, summed into a result with none |
| `result` | `text`, `session`, `subtype`, `cost`, `tokens`, `structured`, `error` (always) or `error_path` (true there): the turn's end. With no text the turn's answer is its last message. An error whose text reads as a usage limit parks the login until it resets. |

Paths are dotted keys: `message.content.*.text`; `*` takes every array element
or object value, `@key` an object's first key (Cursor names a tool call by its
key: `tool_call.@key`), and `a|b` the first path that has a value. `tokens`
takes `input`, `output`, `cache_read`, `cache_write`, and `input_has_cache` when
the input count includes the cached part.

## What the Hive does around a module's harness

- The checkout is `PWD` as well as the working directory (OpenCode works in
  `$PWD`).
- A read-only turn (reviewer, tribunal) gets the read-only args, and inside the
  sandbox its checkout and mailbox are read-only: unlike Claude's read tools, a
  module's harness may run commands.
- A run held to a budget won't start on one: the Hive can't cap its spend.
- Its plan usage isn't read (only Claude's and Codex's are); a usage-limit
  error still parks it.

## Tests

`internal/harness/spec_test.go` parses each shipped spec's output
(`testdata/opencode-*.jsonl` captured from OpenCode 1.19.4; `cursor-turn.jsonl`
is the stream Cursor's docs print, since no Cursor login was at hand), checks
args, env and refusals, and loads a module's harness from a modules folder.
`TestLiveModuleHarness` (`HIVE_LIVE_HARNESS=cursor,opencode` or `all`) runs each
for real where it is installed and logged in: a fresh turn that reads a file,
then a resumed one. `TestLiveWorker` with `HIVE_E2E_HARNESS=opencode
HIVE_E2E_MODEL=opencode/big-pickle` runs the whole ask-to-merge path on it.
