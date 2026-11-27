# Your own MCP tools

An MCP tool is something an agent can call by name, with arguments, and get an answer from: look up a record, work out a price, make a file.
This page makes one on your own computer, tries it, and connects it so agents have it. No packages, no account, no network port.
Read it any time with `hive mcp guide`. To connect a service somebody else runs (it has a web address), use Settings, Connectors, instead.

## The four steps

```
hive mcp new dice                          # ~/projects/mcp/dice/server.py, a working server with one example tool
hive mcp tools dice                        # starts it and lists its tools and their arguments
hive mcp call dice hello who=you           # starts it, calls one tool, prints the answer or the error
hive mcp connect dice                      # every agent run that starts from now on has it
```

Between `new` and `call`, edit `server.py`: that is where the work is.

## Writing a tool

Below the line `# ---------- your tools ----------`, a tool is a function with `@tool` above it:

```python
@tool("Rolls dice and says each result.",
      {"count": {"type": "integer", "description": "how many dice, 1 to 20"},
       "sides": {"type": "integer", "description": "sides on each, 6 if left out"}},
      required=["count"])
def roll(count, sides=6):
    if not 1 <= count <= 20: raise ValueError("count is 1 to 20")
    return [random.randint(1, sides) for _ in range(count)]
```

- The first argument of `@tool` is what the agent reads to decide whether to call it. Say what it does and what comes back.
- The second names each argument, with its type (`string`, `integer`, `number`, `boolean`, `array`, `object`) and a description.
  The schema is a description for the agent, not a check: test the values yourself, as above.
- Return text, or anything JSON can hold. Raise an exception for an error: the agent reads its type and message.
- `print()` goes to stderr: `hive mcp call` shows it when the server crashes or stops answering. For a log you can read any time, write a file.
  Don't write to the real stdout: it carries the protocol.
- Add the imports your tool needs (the example above uses `import random`); the new file imports only `json` and `sys`.
- Standard library only: a package this computer happens to have may be missing on another, and one imported inside a tool fails only when
  called. If you must use one, import it at the top (so `hive mcp tools` fails without it) and tell the person.
- Try the awkward values the tool will meet (`$` and other symbols, long names, negatives, an empty list) and look at what comes back.
- More files may sit beside `server.py`; the whole folder is copied when you connect it
  (at most 200 files, 5 MB; hidden files and links are left out). Read them relative to `__file__`.
- The server is started for each agent run and ends with it. It keeps nothing between runs unless it writes a file.
  Agents in a sandbox can write in `~/projects` and `~/kb`, and reach the network only through the sandbox's own proxy.
- A tool runs with the rights of the agent that calls it. Don't put a password or token in the folder.

## Saying when to use it

An agent sees only your tools' names until it loads one, so for an everyday job (a chart, a date, a file) it may do it its own way and
never look. Fill in `WHEN`, above your tools in `server.py`, with one or two sentences in the words a person would ask in:

```python
WHEN = "Use make_chart when asked for a chart as a picture or a file."
```

The server sends it as its MCP `instructions`, which agents read before their first step. `hive mcp tools` shows it. A server made before
this line existed needs `**({"instructions": WHEN} if WHEN else {})` added to what it answers to `initialize`.
Give the person only words you tested: ask exactly those, with nothing added (no "use the tool", no "say which tool"), in a new chat on
the model their chats use. If you haven't, say so. Words the app can already answer by itself (a chart drawn in the reply) may not reach your tool.

## Trying it

```
hive mcp call dice roll count=3 sides=20
hive mcp call dice roll --args '{"count": 3}'      # the arguments as JSON; --args - reads them from stdin
hive mcp tools dice --json                         # exactly what the server declares
```

Where a command takes `dice`, the path of a folder with `server.py` in it works too (`hive mcp new dice --dir FOLDER` makes one elsewhere).
`connect` takes `--timeout` as well.

With `name=value` (it wins over the same name in `--args`), a value stays text where the tool wants a `string`; otherwise it is read as JSON (`3`, `true`, `[1,2]`), else text.
An error from the tool, a server that crashes or says nothing, and a wrong tool name each end with exit code 1 and say why,
with the server's stderr when it ended or stopped answering. `--timeout SECONDS` (default 30) is the wait for each answer.

## Trying it in Settings

The person can run a connected tool without asking an agent: **Settings**, **Connectors**, **Try your own tools**.
**See its tools** starts the server and lists them; **Try it** opens a form made from the tool's arguments; **Run it** shows the answer,
or the error the tool raised. A saved file under `~/projects` or `~/files` named in the answer opens from there, and a picture shows under it.
While it runs, **Stop** ends the tool and the programs it started; whatever it changed before then stays changed.

- Only tools connected with `hive mcp connect` are listed. A service with a web address isn't.
- Running one needs the full sign-in. With the sandbox on, the run is inside it. With it off, the tool can read the account's files and
  sign-ins, and the page says so and asks first. There, a program the tool starts in a session of its own can outlive **Stop**.
- Unless a tool marks itself as only reading (`readOnlyHint` in its `annotations`), each run asks to be confirmed.
- What the form shows comes from your schema: describe each argument, give `examples` or a `default`, and give an array its `items` type
  (a list of text or numbers is typed one per line, or as a JSON list).

## Connecting it

`hive mcp connect dice` copies the folder into `~/mcp-tools/dice/` and adds it to `~/mcp.json`, the list every Claude run is started with.
So what you connected is what runs: edit the source as much as you like, then connect again to put the change in.

- Agents started after this have the tool, named `mcp__dice__roll` (connector, then tool). One already running does not.
- `--name other` connects it under another name. A name somebody else's connector has is refused; connecting another folder of your own
  under a name you used replaces that tool, and says so.
- `hive mcp list` shows what is connected; `hive mcp call dice roll count=1 --connected` calls the connected copy.
- `hive mcp disconnect dice` takes it away from new runs. Your source stays. A run already going keeps the copy it started with, data files
  and all, after a disconnect or a new connect; old copies are deleted at a later connect or disconnect of that name, after 7 days.
- It reaches agents that run on Claude. Agents on another provider are not given `~/mcp.json`.

**From inside a sandbox** (an agent on an install with the sandbox on) the list of connectors can be read and not changed.
`connect` still starts the server and checks its tools, then writes a short script and prints the `hive step ask` line that gets it run
outside, once somebody allows it. The script names a fingerprint of the files: if they change before it runs, nothing is connected.
Run that line, say what the tool does, and check `hive step status`.
The run that asked keeps the list it started with: `hive mcp list` there doesn't show the new tool, even after the step ran.

## When it doesn't work

| What you see | What to do |
|---|---|
| `the server ended (exit 1) before answering initialize` | A Python error at start; the traceback is under it. |
| `wrote something that isn't JSON to stdout` | Something writes to the real stdout (a subprocess, say): give it `stdout=subprocess.DEVNULL` or capture it. |
| `no answer to tools/call in 30 s` | The tool is slow or waiting for input: add `--timeout`, and never read stdin in a tool. |
| Connected, but an agent doesn't have it | The agent was started before. Start a new one. |
| Agents have it but do the job another way | Fill in `WHEN` (above), connect again, start a new chat. |
| `another connector is already called ...` | Pick another name with `--name`. |
