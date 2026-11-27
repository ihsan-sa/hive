#!/usr/bin/env python3
"""hive mcp: make, try and connect your own MCP server (stdio, standard library only). The page: core/mcp-tools.md.

A tool is a folder with server.py. `connect` copies that folder to ~/mcp-tools/NAME/FINGERPRINT, which agents in the sandbox can read and not
write, and points ~/mcp.json (which the starter passes to every Claude run) at that copy. So what was connected is what runs, until it is
connected again; a copy that was replaced or disconnected stays KEEP seconds for the runs that already use it.
"""
import argparse, hashlib, json, os, queue, re, shlex, shutil, signal, subprocess, sys, tempfile, threading, time
from pathlib import Path

CORE = Path(__file__).resolve().parent
HOME = Path.home()
SOURCES, TOOLS = HOME / "projects" / "mcp", HOME / "mcp-tools"
MCP_JSON, MCP_URLS = HOME / "mcp.json", HOME / "mcp-urls.json"
NAME = re.compile(r"[a-z0-9][a-z0-9_-]{0,39}")
VERSION = "2025-06-18"
MAX_FILES, MAX_BYTES = 200, 5 * 2**20
KEEP = 7 * 86400

TEMPLATE = '''#!/usr/bin/env python3
"""__NAME__: an MCP server over stdio, standard library only. Add tools below; leave the part after them as it is.

Try it:     hive mcp tools __NAME__
            hive mcp call __NAME__ hello who=you
Connect it: hive mcp connect __NAME__
"""
import json, sys

TOOLS = {}


def tool(description, args=None, required=()):
    """Makes the function under it a tool. args: {name: JSON Schema of that argument}. It returns a string (or anything JSON can hold);
    an exception becomes the error the agent reads."""
    def add(f):
        TOOLS[f.__name__] = (f, {"name": f.__name__, "description": description,
                                 "inputSchema": {"type": "object", "properties": args or {}, "required": list(required)}})
        return f
    return add


# ---------- your tools ----------

# When an agent should use this, in the words a person would ask in. Agents read it before they look at any tool: left empty, they may
# do the job some other way. Example: "Use make_chart when asked for a chart as a picture or a file."
WHEN = ""


@tool("Greets someone. Replace this with your own tool.", {"who": {"type": "string", "description": "who to greet"}}, required=["who"])
def hello(who):
    return f"hello, {who}"


# ---------- the protocol: one JSON-RPC message a line on stdin and stdout ----------

def answer(method, params):
    if method == "initialize":
        return {"protocolVersion": params.get("protocolVersion") or "2025-06-18", "capabilities": {"tools": {}},
                "serverInfo": {"name": "__NAME__", "version": "1"}, **({"instructions": WHEN} if WHEN else {})}
    if method == "ping": return {}
    if method == "tools/list": return {"tools": [spec for _, spec in TOOLS.values()]}
    if method == "tools/call":
        name, args = params.get("name"), params.get("arguments") or {}
        if name not in TOOLS: raise LookupError(f"no tool named {name!r}")
        f, spec = TOOLS[name]
        try:
            missing = [a for a in spec["inputSchema"]["required"] if a not in args]
            if missing: raise ValueError("missing argument: " + ", ".join(missing))
            out = f(**args)
            return {"content": [{"type": "text", "text": out if isinstance(out, str) else json.dumps(out)}]}
        except Exception as e:
            return {"content": [{"type": "text", "text": f"{type(e).__name__}: {e}"}], "isError": True}
    raise NotImplementedError(method)


def main():
    out, sys.stdout = sys.stdout, sys.stderr   # a print() in a tool goes to the log, not into the protocol
    def send(**msg): out.write(json.dumps({"jsonrpc": "2.0", **msg}) + "\\n"); out.flush()
    for line in sys.stdin:
        if not line.strip(): continue
        try: m = json.loads(line); method, mid = m.get("method"), m.get("id")
        except (ValueError, AttributeError): send(id=None, error={"code": -32700, "message": "not JSON-RPC"}); continue
        if mid is None or method is None: continue   # a notification, or a reply: nothing to answer
        try: send(id=mid, result=answer(method, m.get("params") or {}))
        except NotImplementedError: send(id=mid, error={"code": -32601, "message": f"no method {method}"})
        except Exception as e: send(id=mid, error={"code": -32602, "message": str(e)})


if __name__ == "__main__": main()
'''


NO_WHEN = "agents are told nothing about when to use it, and may do the job another way: set WHEN in server.py"


class Bad(Exception):
    """Something to tell the caller: the command ends with this and exit 1."""


def boxed_install():
    """A consumer install with the sandbox on (core/sandbox.py's own rule). Hive has no settings.json: False."""
    if not (HOME / "settings.json").exists(): return False
    sys.path.insert(0, str(CORE)); import sandbox
    return sandbox.on()


def in_box():
    return bool(os.environ.get("HIVE_BROKER"))


def python():
    """The system's python3: the one a box has too."""
    return shutil.which("python3", path="/usr/bin:/bin") or sys.executable


def entry_command(server):
    return [python(), "-E", "-s", "-B", str(server)]


# ---------- the client ----------

class Client:
    """Starts a server and speaks to it. Use as `with Client(argv) as c:`; the process and what it started end with the block."""
    def __init__(self, argv, timeout=30):
        self.argv, self.timeout, self.n = argv, timeout, 0

    def __enter__(self):
        self.err = tempfile.TemporaryFile()
        try: self.p = subprocess.Popen(self.argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.err, start_new_session=True)
        except OSError as e: self.err.close(); raise Bad(f"the server didn't start: {e}")
        self.lines = queue.Queue()
        threading.Thread(target=self._read, daemon=True).start()
        try:
            said = self.ask("initialize", {"protocolVersion": VERSION, "capabilities": {}, "clientInfo": {"name": "hive-mcp", "version": "1"}})
            self.when = " ".join(str(said.get("instructions") or "").split()) if isinstance(said, dict) else ""
            self._send({"method": "notifications/initialized"})
        except BaseException: self.__exit__(); raise
        return self

    def __exit__(self, *a):
        try: self.p.stdin.close(); self.p.wait(2)
        except (OSError, subprocess.TimeoutExpired): pass
        try: os.killpg(self.p.pid, signal.SIGKILL)   # its own session: the server and anything it left running
        except (ProcessLookupError, PermissionError): pass
        self.p.wait(); self.err.close()

    def _read(self):
        for line in self.p.stdout: self.lines.put(line)
        self.lines.put(None)

    def _send(self, msg):
        try: self.p.stdin.write((json.dumps({"jsonrpc": "2.0", **msg}) + "\n").encode()); self.p.stdin.flush()
        except OSError: pass   # it has gone: the read says so, with its stderr

    def _fail(self, why):
        self.err.seek(max(0, self.err.seek(0, 2) - 4000))
        said = self.err.read().decode(errors="replace").strip()
        raise Bad(why + (f"\nThe server's own output (stderr):\n{said}" if said else ""))

    def ask(self, method, params=None):
        self.n += 1; mid = self.n
        self._send({"id": mid, "method": method, "params": params or {}})
        end = time.monotonic() + self.timeout
        while True:
            try: line = self.lines.get(timeout=max(0.01, end - time.monotonic()))
            except queue.Empty: self._fail(f"no answer to {method} in {self.timeout} s")
            if line is None:
                try: code = self.p.wait(2)
                except subprocess.TimeoutExpired: code = "unknown"
                self._fail(f"the server ended (exit {code}) before answering {method}")
            try: m = json.loads(line)
            except ValueError: self._fail(f"the server wrote something that isn't JSON to stdout (print to stderr instead): {line[:200]!r}")
            if not isinstance(m, dict) or m.get("id") != mid or "method" in m: continue   # a notification, or a request of its own
            if "error" in m:
                e = m["error"] if isinstance(m["error"], dict) else {}
                raise Bad(f"{method} failed: {e.get('message') or m['error']} (code {e.get('code')})")
            r = m.get("result")
            if not isinstance(r, dict): self._fail(f"{method}: the answer has no result object")
            return r

    def tools(self):
        out, cursor = [], None
        for _ in range(50):
            r = self.ask("tools/list", {"cursor": cursor} if cursor else {})
            got = r.get("tools")
            if not isinstance(got, list) or not all(isinstance(t, dict) and isinstance(t.get("name"), str) for t in got):
                raise Bad("tools/list: expected a list of tools, each with a name")
            out += got; cursor = r.get("nextCursor")
            if not cursor: break
        return out


# ---------- where a tool is ----------

def source(what):
    """The server.py for NAME (in ~/projects/mcp/NAME) or for a path to a folder or to a server.py."""
    p = Path(what).expanduser()
    if "/" not in what and NAME.fullmatch(what) and (SOURCES / what / "server.py").is_file(): p = SOURCES / what
    if p.is_dir(): p = p / "server.py"
    if not p.is_file(): raise Bad(f"no server at {what!r}: give a name made with `hive mcp new`, or a folder with server.py in it")
    if p.name != "server.py": raise Bad("the file must be called server.py, in a folder of its own")
    return p.resolve()


def target(what, connected):
    if connected:
        p = copy_of(read_servers(MCP_JSON).get(what))
        if not p or not p.is_file():
            late = ", or this run started before it was (a new one has it)" if in_box() else ""
            raise Bad(f"{what!r} isn't connected{late} (hive mcp list)")
    else: p = source(what)
    if boxed_install() and not in_box():
        raise Bad("this install runs agents in a sandbox, and that command would run the server's code outside it. Run it as an agent.")
    return p


def files_of(folder):
    """[(relative path, bytes)] of what connect copies: regular files, no links, nothing hidden, no __pycache__."""
    out, size = [], 0
    for root, dirs, names in os.walk(folder):
        dirs[:] = sorted(d for d in dirs if not d.startswith(".") and d != "__pycache__")
        for n in sorted(names):
            p = Path(root) / n
            if n.startswith(".") or p.is_symlink() or not p.is_file(): continue
            size += p.stat().st_size
            if size > MAX_BYTES or len(out) >= MAX_FILES:
                raise Bad(f"{folder} is too big to connect (at most {MAX_FILES} files, {MAX_BYTES >> 20} MB): keep the server in a folder of its own")
            out.append((p.relative_to(folder).as_posix(), p.read_bytes()))
    return out


def digest(files):
    h = hashlib.sha256()
    for rel, data in files: h.update(f"{rel}\0{len(data)}\0".encode()); h.update(data)
    return h.hexdigest()


# ---------- mcp.json ----------

def read_servers(path):
    try: text = path.read_text()
    except FileNotFoundError: return {}
    except OSError as e: raise Bad(f"can't read {path}: {e}")
    if not text.strip(): return {}   # empty: the mount point a later sandbox leaves in the shared box home
    try:
        v = json.loads(text)
        v = v.get("mcpServers", {}) if path == MCP_JSON else v
        if not isinstance(v, dict): raise ValueError
    except (ValueError, AttributeError): raise Bad(f"{path} isn't readable as the connector list; fix it first, nothing was changed")
    return v


def own(entry):
    """True for an entry connect wrote: its server is under ~/mcp-tools. The dash keeps these when it rewrites mcp.json."""
    a = entry.get("args") if isinstance(entry, dict) else None
    return isinstance(a, list) and bool(a) and str(a[-1]).startswith(str(TOOLS) + os.sep)


def copy_of(entry):
    """The server.py an entry of ours starts, else None."""
    return Path(entry["args"][-1]) if own(entry) else None


def retire(name, was, now=None):
    """After NAME's copy changed from `was` to `now`: date `was` from this moment, and delete copies replaced longer ago than KEEP."""
    if was and was != now and was.is_dir(): os.utime(was)
    for d in (TOOLS / name).glob("*"):
        if d not in (was, now) and time.time() - d.stat().st_mtime > KEEP: shutil.rmtree(d, ignore_errors=True)


def write_servers(servers):
    if not servers: MCP_JSON.unlink(missing_ok=True); return
    fd, tmp = tempfile.mkstemp(dir=HOME, prefix=".mcp.json.")   # 0600: other entries may carry tokens
    with os.fdopen(fd, "w") as f: json.dump({"mcpServers": servers}, f, indent=1)
    os.replace(tmp, MCP_JSON)


def person_servers():
    try: home = Path((HOME / ".hive-account").read_text().strip())
    except OSError: home = HOME
    try: v = json.loads((home / ".claude.json").read_text()).get("mcpServers") or {}
    except (OSError, ValueError, AttributeError): return {}
    return v if isinstance(v, dict) else {}


def name_free(name):
    cur = read_servers(MCP_JSON)
    if (name in cur and not own(cur[name])) or name in read_servers(MCP_URLS) or name in person_servers():
        raise Bad(f"another connector is already called {name!r}: use --name")
    return cur


# ---------- the commands ----------

def cmd_new(a):
    if not NAME.fullmatch(a.name): raise Bad("a name is lowercase letters, digits, - and _ (up to 40), starting with a letter or digit")
    d = Path(a.dir).expanduser() / a.name if a.dir else SOURCES / a.name
    if (d / "server.py").exists(): raise Bad(f"{d / 'server.py'} is already there")
    d.mkdir(parents=True, exist_ok=True)
    (d / "server.py").write_text(TEMPLATE.replace("__NAME__", a.name))
    what = a.name if d == SOURCES / a.name else shlex.quote(str(d))
    print(f"made {d / 'server.py'}\nnext: add your tool there, then\n  hive mcp tools {what}\n  hive mcp call {what} hello who=you\n  hive mcp connect {what}")


def cmd_tools(a):
    with Client(entry_command(target(a.what, a.connected)), a.timeout) as c: tools = c.tools()
    if a.json: print(json.dumps(tools, indent=1)); return
    if not tools: raise Bad("the server started and has no tools")
    for t in tools:
        print(f"{t['name']}: {' '.join(str(t.get('description') or '').split())}")
        s = t.get("inputSchema") if isinstance(t.get("inputSchema"), dict) else {}
        req = s.get("required") or []
        for n, v in (s.get("properties") or {}).items():
            v = v if isinstance(v, dict) else {}
            print(f"    {n} ({v.get('type', 'any')}{', required' if n in req else ''}) {v.get('description', '')}".rstrip())
    print(f"agents are told: {c.when}" if c.when else NO_WHEN)


def arguments(a, spec):
    """--args JSON (or - for stdin), then key=value pairs over it. A value stays text where the tool wants text; else it is read as JSON."""
    try: out = json.loads(sys.stdin.read() if a.args == "-" else a.args) if a.args else {}
    except ValueError as e: raise Bad(f"--args isn't JSON: {e}")
    if not isinstance(out, dict): raise Bad("--args is a JSON object, like '{\"who\": \"you\"}'")
    props = (spec.get("inputSchema") or {}).get("properties") or {}
    for pair in a.pairs:
        k, eq, v = pair.partition("=")
        if not eq or not k: raise Bad(f"write arguments as name=value, not {pair!r}")
        if not (isinstance(props.get(k), dict) and props[k].get("type") == "string"):
            try: v = json.loads(v)
            except ValueError: pass
        out[k] = v
    return out


def cmd_call(a):
    with Client(entry_command(target(a.what, a.connected)), a.timeout) as c:
        specs = {t["name"]: t for t in c.tools()}
        if a.tool not in specs: raise Bad(f"no tool named {a.tool!r}; this server has: {', '.join(specs) or 'none'}")
        r = c.ask("tools/call", {"name": a.tool, "arguments": arguments(a, specs[a.tool])})
    if a.json: print(json.dumps(r, indent=1))
    else:
        for part in r.get("content") or []:
            if isinstance(part, dict) and part.get("type") == "text": text = part.get("text", "")
            else: text = f"[{part.get('type') if isinstance(part, dict) else 'unknown'} content, {len(json.dumps(part))} bytes; --json shows it]"
            print(text, file=sys.stderr if r.get("isError") else sys.stdout)
    if r.get("isError"): raise Bad(f"the tool {a.tool} answered with an error (above)")


def cmd_connect(a):
    src = source(a.what)
    name = a.name or src.parent.name
    if not NAME.fullmatch(name): raise Bad(f"{name!r} can't be a connector's name (lowercase letters, digits, - and _): use --name")
    if src.parent in (HOME, HOME / "projects", SOURCES) or src.is_relative_to(TOOLS): raise Bad("put server.py in a folder of its own")
    before = name_free(name).get(name) or {}
    origin = (before.get("env") or {}).get("HIVE_MCP_FROM")
    replaces = f"\nThis replaces the tool connected as {name!r} from {origin}." if origin and origin != str(src.parent) else ""
    files = files_of(src.parent)
    sha = digest(files)
    if a.sha256 and a.sha256 != sha: raise Bad("the files changed since this was asked for (the sha256 differs): nothing connected. Ask again.")
    boxed = boxed_install()
    if boxed and in_box():
        with Client(entry_command(src), a.timeout) as c: tools = c.tools()
        if not tools: raise Bad("the server started and has no tools: nothing to connect")
        script = Path(os.environ.get("TMPDIR") or HOME) / f"mcp-connect-{name}.sh"
        script.write_text(f"#!/bin/sh\nexec {shlex.quote(str(CORE / 'hive'))} mcp connect {shlex.quote(str(src.parent))} --name {name} --sha256 {sha}\n")
        print(f"The server works ({', '.join(t['name'] for t in tools)}). Nothing is connected yet: from inside the sandbox it takes a step outside.{replaces}\n"
              f"  hive step ask --why \"connect my MCP tool {name} from {src.parent} to every agent\" --script {script}\n"
              "It runs the files exactly as they are now: after a change, run connect again.")
        return
    TOOLS.mkdir(exist_ok=True)
    staged = Path(tempfile.mkdtemp(dir=TOOLS, prefix=f".{name}."))
    try:
        for rel, data in files:
            p = staged / rel; p.parent.mkdir(parents=True, exist_ok=True); p.write_bytes(data)
        if boxed: said = "agents have it from their next run (this command doesn't run it outside the sandbox)"
        else:   # try the copy itself, from where it will stand: a server that leans on files left behind fails here
            with Client(entry_command(staged / "server.py"), a.timeout) as c: tools = c.tools()
            if not tools: raise Bad("the server started and has no tools: nothing to connect")
            said = f"tools: {', '.join(t['name'] for t in tools)}. Agents have it from their next run, as mcp__{name}__TOOL"
        final = TOOLS / name / sha[:16]
        if not (final.is_dir() and digest(files_of(final)) == sha):   # the same files again: the copy that is there stays
            final.parent.mkdir(exist_ok=True)
            shutil.rmtree(final, ignore_errors=True)
            staged.chmod(0o755); staged.rename(final)
    finally: shutil.rmtree(staged, ignore_errors=True)
    cur = name_free(name)
    was = copy_of(cur.get(name))
    cmd = entry_command(final / "server.py")
    # the digest is there so a changed server changes mcp.json: the starter then replaces an agent it had waiting with the old one
    cur[name] = {"type": "stdio", "command": cmd[0], "args": cmd[1:], "env": {"HIVE_MCP_SHA": sha[:16], "HIVE_MCP_FROM": str(src.parent)}}
    write_servers(cur)
    retire(name, was and was.parent, final)
    print(f"connected {name}: {said}{replaces}\nundo: hive mcp disconnect {name}")


def cmd_disconnect(a):
    if boxed_install() and in_box():
        script = Path(os.environ.get("TMPDIR") or HOME) / f"mcp-disconnect-{a.name}.sh"
        if not NAME.fullmatch(a.name): raise Bad("no connected tool has that name")
        script.write_text(f"#!/bin/sh\nexec {shlex.quote(str(CORE / 'hive'))} mcp disconnect {a.name}\n")
        print(f"From inside the sandbox this takes a step outside:\n  hive step ask --why \"disconnect the MCP tool {a.name}\" --script {script}"); return
    cur = read_servers(MCP_JSON)
    if a.name not in cur or not own(cur[a.name]):
        raise Bad(f"{a.name!r} isn't a tool connected with `hive mcp connect`" + (" (it was added in Settings: remove it there)" if a.name in cur else ""))
    was = copy_of(cur.pop(a.name))
    write_servers(cur)
    retire(a.name, was.parent)
    print(f"disconnected {a.name}: new agent runs don't have it; one already running keeps it until it ends. Your source is untouched.")


def cmd_list(a):
    cur = {n: v for n, v in read_servers(MCP_JSON).items() if own(v)}
    if not cur: print("no tools connected with `hive mcp connect`")
    for n in sorted(cur): print(f"{n}  {cur[n]['args'][-1]}")
    if in_box():   # the box's mcp.json is the one mounted at its start: a connect outside doesn't reach this run
        print("(the list as it was when this run started: after a connect or disconnect since, `hive step status` says whether it ran, and a new run has it)")


def cmd_guide(a):
    print((CORE / "mcp-tools.md").read_text().strip())


# ---------- the try-it view in Settings (the dash runs outside any sandbox) ----------

TRY_S, TRY_MAX = 60, 2 * 2**20   # seconds for each answer; bytes of output read back


def connected():
    """[{name, from}] of the tools connected with `connect`, for the dash."""
    cur = read_servers(MCP_JSON)
    return [{"name": n, "from": ((cur[n].get("env") or {}).get("HIVE_MCP_FROM") or "")} for n in sorted(cur) if own(cur[n])]


def tryit(name, tool=None, args=None, stop=None):
    """One run of a connected tool for the person: {"tools": [...]}, or with tool {"result": ...}; {"error": text} when it fails.
    Setting stop (a threading.Event) ends the run early: {"error": ..., "stopped": True}.
    It runs this file's own command in a process of its own, and on a sandboxed install that process is inside a sandbox like an agent's
    (no Claude sign-in mounted): the tool's code never runs outside it."""
    if name not in [c["name"] for c in connected()]: raise Bad("no connected tool has that name")
    if tool is not None and (not isinstance(tool, str) or not tool or tool.startswith("-")): raise Bad("that isn't a tool's name")
    cmd = [python(), "-I", str(CORE / "mcptool.py"), "tools" if tool is None else "call", "--connected", "--json", "--timeout", str(TRY_S)]
    if tool is None: cmd += ["--", name]
    else: cmd += ["--args", "-", "--", name, tool]
    env = {"HOME": str(HOME), "PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": os.environ.get("LANG", "C.UTF-8")}
    if os.environ.get("TMPDIR"): env["TMPDIR"] = os.environ["TMPDIR"]
    if boxed_install():
        sys.path.insert(0, str(CORE)); import sandbox
        why = "the sandbox isn't working here, and a tool is only run inside it"
        if not sandbox.bwrap(): return {"error": why}
        sandbox.ensure_broker()
        cmd, env = sandbox.wrap(cmd, env, "tryit")
        cmd.insert(cmd.index(sandbox.bwrap()) + 1, "--die-with-parent")   # unlike an agent, it ends with whoever asked
        if not sandbox.ready("tryit"): return {"error": why}
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        p = subprocess.Popen(cmd, env=env, cwd=HOME, stdin=subprocess.PIPE, stdout=out, stderr=err, start_new_session=True)
        data, end = json.dumps(args or {}).encode() if tool is not None else b"", time.monotonic() + 3 * TRY_S + 10
        try:
            while True:
                if stop is not None and stop.is_set(): return {"error": "Stopped. It may have changed things before it stopped.", "stopped": True}
                try: p.communicate(data, timeout=0.2); break
                except subprocess.TimeoutExpired: data = None   # kept by communicate from the first call
                if time.monotonic() > end: return {"error": f"no answer in {3 * TRY_S} s, so it was stopped"}
        finally:
            if p.poll() is None:   # cut short: asked first, so it can end the server, which has a process group of its own
                try: os.killpg(p.pid, signal.SIGTERM); p.wait(5)
                except (ProcessLookupError, PermissionError, subprocess.TimeoutExpired): pass
            try: os.killpg(p.pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError): pass
            p.wait()
        out.seek(0); got = out.read(TRY_MAX + 1)
        err.seek(max(0, err.seek(0, 2) - 4000)); said = err.read().decode(errors="replace").strip()
    if len(got) > TRY_MAX: return {"error": "the answer is too big to show here (over 2 MB). An agent can still use the tool."}
    try: v = json.loads(got)
    except ValueError: return {"error": re.sub(r"^hive mcp (tools|call): ", "", said) or f"the run ended with exit {p.returncode} and said nothing"}
    return {"tools": v} if tool is None else {"result": v}


def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive mcp", description="Your own MCP tools: make one, try it, connect it to the agents. `hive mcp guide` is the page.")
    sp = ap.add_subparsers(dest="cmd", required=True)
    def add(name, fn, text):
        p = sp.add_parser(name, help=text, description=text); p.set_defaults(f=fn); return p
    p = add("new", cmd_new, "make ~/projects/mcp/NAME/server.py, a working server with one example tool"); p.add_argument("name"); p.add_argument("--dir", help="make the folder NAME in here instead")
    tools = add("tools", cmd_tools, "start a server and print its tools")
    call = add("call", cmd_call, "start a server, call one tool, print the answer or the error")
    for p in (tools, call):
        p.add_argument("what", help="a name, or a folder with server.py"); p.add_argument("--connected", action="store_true", help="the connected copy, not your source")
        p.add_argument("--json", action="store_true"); p.add_argument("--timeout", type=float, default=30, help="seconds to wait for each answer (30)")
    call.add_argument("tool"); call.add_argument("pairs", nargs="*", metavar="name=value"); call.add_argument("--args", help="the arguments as one JSON object; - reads it from stdin")
    p = add("connect", cmd_connect, "give every new agent run this server's tools"); p.add_argument("what"); p.add_argument("--name", help="the connector's name (default: the folder's)")
    p.add_argument("--sha256", help="connect only if the files are exactly these (used by the sandbox's step script)"); p.add_argument("--timeout", type=float, default=30)
    p = add("disconnect", cmd_disconnect, "take a connected tool away from new agent runs"); p.add_argument("name")
    add("list", cmd_list, "the tools connected this way")
    add("guide", cmd_guide, "the page: how to build and connect a tool")
    a, rest = ap.parse_known_args(argv)
    if rest:   # name=value after a flag
        if a.cmd != "call" or any(r.startswith("-") for r in rest): ap.error("unrecognized arguments: " + " ".join(rest))
        a.pairs += rest
    def ended(*_):   # asked to stop (a try-it run in Settings): leave through the `with` blocks, which end the server
        signal.signal(signal.SIGTERM, signal.SIG_IGN); sys.exit(143)
    signal.signal(signal.SIGTERM, ended)
    try: a.f(a)
    except Bad as e: sys.exit(f"hive mcp {a.cmd}: {e}")
    except OSError as e: sys.exit(f"hive mcp {a.cmd}: {e}")


if __name__ == "__main__": main()
