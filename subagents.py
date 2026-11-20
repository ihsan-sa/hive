#!/usr/bin/env python3
"""hive subagents: the helpers the Hive's agents started inside their own harness, which the board never sees.

Claude Code writes each Agent-tool helper to <login>/projects/<dir>/<session>/subagents/agent-<id>.jsonl, with a
.meta.json beside it (type, description, model, foreground or background, finished). Codex (multi_agent on) records
each spawned thread in ~/.codex/state_5.sqlite: thread_spawn_edges links parent to child, threads holds the child.
Sessions map to Hive agent names through the start lines in ~/log.jsonl (a Codex run's thread id is the
thread.started line of its stream file). Asked for by the person, thread 111919537629 (2026-11-20)."""
import argparse, datetime as dt, glob, importlib.util, json, os, sqlite3, sys, time
from pathlib import Path

HOME = Path.home()
LIVE = 180  # an unfinished helper whose file moved in the last 3 min counts as running
LIVE_OWNED = 900  # or in the last 15 min while its owner's run is still alive: a helper inside a long Bash call or test writes nothing meanwhile
CODEX_OPEN = {"open", "running", "pending", "active"}  # thread_spawn_edges.status of a child still going; "closed" (and anything else) is done


def _cost():
    try:
        spec = importlib.util.spec_from_file_location("h2cost", HOME / "modules" / "cost" / "cost.py")
        cm = importlib.util.module_from_spec(spec); spec.loader.exec_module(cm); return cm
    except Exception: return None


def starts():
    """session id -> the latest start line (name, thread, harness, out, login)."""
    out = {}
    try:
        for line in (HOME / "log.jsonl").open(errors="replace"):
            if '"start"' not in line: continue
            try: e = json.loads(line)
            except ValueError: continue
            if e.get("event") == "start" and e.get("session"): out[e["session"]] = e
    except OSError: pass
    return out


def alive():
    """Session ids (and Codex out files) of the Hive runs still running: ~/run/<name>.json whose pid is up."""
    out = set()
    for f in (HOME / "run").glob("*.json"):
        try: r = json.loads(f.read_text())
        except (OSError, ValueError): continue
        try: os.kill(int(r["pid"]), 0)
        except (KeyError, TypeError, ValueError, OSError): continue
        out.update(x for x in (r.get("session"), r.get("out")) if x)
    return out


def settle(h, up, now=None):
    """A row's state once its owner is known: a helper that went quiet but whose owner still runs is running, up to LIVE_OWNED."""
    if h["state"] != "stopped" or not up: return h
    return {**h, "state": "running"} if (now or time.time()) - (h.get("moved") or 0) < LIVE_OWNED else h


def codex_thread(out_file):
    try:
        with open(out_file, errors="replace") as f:
            for i, line in enumerate(f):
                if '"thread.started"' in line: return json.loads(line).get("thread_id")
                if i > 20: break
    except (OSError, ValueError): pass
    return None


def iso(ts): return dt.datetime.fromtimestamp(ts, dt.timezone.utc).isoformat(timespec="seconds")


def claude_helpers(since, cm, memo=None):
    """memo: a dict kept between calls (the dashboard's) so a helper file is read again only when it changed."""
    logins = cm.logins() if cm else [str(HOME / ".claude")]
    for login in logins:
        for f in glob.glob(os.path.join(login, "projects", "*", "*", "subagents", "agent-*.jsonl")):
            try: st = os.stat(f); mt = st.st_mtime
            except OSError: continue
            if mt < since: continue
            key = (mt, st.st_size, os.path.getmtime(f[:-6] + ".meta.json") if os.path.exists(f[:-6] + ".meta.json") else 0)
            hit = memo.get(f) if memo is not None else None
            if hit and hit[0] == key:
                r = hit[1]; yield {**r, "state": "done" if r["state"] == "done" else "running" if time.time() - mt < LIVE else "stopped", "moved": mt}; continue
            try: meta = json.loads(Path(f[:-6] + ".meta.json").read_text())
            except (OSError, ValueError): meta = {}
            first = last = None; msgs = {}; tools = 0
            try:
                for line in open(f, errors="replace"):
                    try: e = json.loads(line)
                    except ValueError: continue
                    ts = e.get("timestamp")
                    if ts: first = first or ts; last = ts
                    m = e.get("message") or {}
                    if e.get("type") == "assistant" and m.get("usage"):
                        msgs[m.get("id") or len(msgs)] = (m.get("model"), m["usage"])
                        tools += sum(1 for c in m.get("content") or [] if isinstance(c, dict) and c.get("type") == "tool_use")
            except OSError: continue
            usd = sum(cm.price(mo, u) for mo, u in msgs.values()) if cm else None
            out_tok = sum((u.get("output_tokens") or 0) for _, u in msgs.values())
            fin = meta.get("finished")
            state = "done" if fin else "running" if time.time() - mt < LIVE else "stopped"
            r = {"harness": "claude-code", "session": Path(f).parent.parent.name, "id": Path(f).stem[6:],
                 "parent": meta.get("parentAgentId"), "type": meta.get("agentType"), "desc": meta.get("description", ""),
                 "model": meta.get("pickedEffortModel") or meta.get("model"), "mode": meta.get("requestShape"),
                 "depth": meta.get("spawnDepth"), "start": first, "end": last, "state": state, "calls": len(msgs),
                 "tools": tools, "out_tokens": out_tok, "usd": usd, "file": f, "moved": mt}
            if memo is not None: memo[f] = (key, r)
            yield r


def codex_helpers(since, codex_home=HOME / ".codex"):
    db = codex_home / "state_5.sqlite"
    if not db.exists(): return
    try:
        c = sqlite3.connect(f"file:{db}?mode=ro", uri=True, timeout=5)
        c.row_factory = sqlite3.Row
        rows = c.execute("""select e.parent_thread_id p, e.status st, t.* from thread_spawn_edges e
                            join threads t on t.id = e.child_thread_id where t.updated_at >= ?""", (int(since),)).fetchall()
    except sqlite3.Error as ex:
        print(f"(Codex state unreadable: {ex})", file=sys.stderr); return
    for r in rows:
        k = r.keys()
        ms = lambda col, sec: (r[col] / 1000 if col in k and r[col] else r[sec])
        moved = ms("updated_at_ms", "updated_at") or 0
        state = "done" if str(r["st"] or "").lower() not in CODEX_OPEN else "running" if time.time() - moved < LIVE else "stopped"
        yield {"harness": "codex", "thread": r["p"], "id": r["id"], "parent": None,
               "type": (r["agent_role"] if "agent_role" in k else None) or (r["agent_nickname"] if "agent_nickname" in k else None),
               "desc": r["title"] or (r["first_user_message"] if "first_user_message" in k else "") or "",
               "model": r["model"] if "model" in k else None, "mode": None, "depth": None,
               "start": iso(ms("created_at_ms", "created_at")), "end": iso(ms("updated_at_ms", "updated_at")),
               "state": state, "calls": None, "tools": None, "out_tokens": r["tokens_used"], "usd": None,
               "file": r["rollout_path"], "moved": moved}


SUBCMD_SKIP = {"auth", "login", "logout", "update", "upgrade", "doctor", "mcp", "mcp-server", "config", "setup-token", "install", "completion",
               "migrate-installer", "plugin", "plugins", "features", "sandbox", "apply", "debug", "help", "cloud", "proto", "generate-ts"}
NOT_A_PROGRAM = {"bash", "sh", "dash", "zsh", "timeout", "heavy", "nice", "ionice", "env", "flock", "su", "sudo", "setsid", "stdbuf", "systemd-run",
                 "xargs", "script", "unbuffer", "time", "nohup"}  # wrappers a process tree passes through on its way to the program that started a claude


def _proc(pid):
    """(ppid, start ticks, argv, uid) of one /proc entry, or None if it went or isn't readable."""
    try:
        with open(f"/proc/{pid}/stat") as f: st = f.read()
        rest = st[st.rindex(")") + 2:].split()
        argv = [a for a in Path(f"/proc/{pid}/cmdline").read_bytes().decode(errors="replace").split("\0")]
        while argv and argv[-1] == "": argv.pop()
        return int(rest[1]), int(rest[19]), argv, os.stat(f"/proc/{pid}").st_uid
    except (OSError, ValueError, IndexError): return None


def _which(argv):
    """'claude' or 'codex' for a claude or codex harness process (node wrappers too); None for anything else."""
    if not argv: return None
    b = os.path.basename(argv[0])
    if b in ("claude", "codex"): return b
    if b in ("node", "bun", "nodejs") and len(argv) > 1:
        s = argv[1]
        if "claude-code" in s and s.endswith(".js") or os.path.basename(s) == "claude": return "claude"
        if os.path.basename(s) in ("codex", "codex.js") or "/@openai/codex/" in s: return "codex"
    return None


def _args(argv):
    """argv after the program (and after node's script)."""
    return argv[2:] if os.path.basename(argv[0]) in ("node", "bun", "nodejs") else argv[1:]


def _kind(which, args):
    """What sort of agentic process it is ('claude -p', 'codex exec', ...), or None for a quick non-agentic call (auth, --version, mcp)."""
    if any(a in ("--version", "-v", "--help", "-h", "-V") for a in args[:2]): return None
    first = next((a for a in args if not a.startswith("-")), None)
    if which == "claude":
        if first in SUBCMD_SKIP: return None
        return "claude -p" if ("-p" in args or "--print" in args) else "claude session"
    sub = args[0] if args and not args[0].startswith("-") else None
    if sub in SUBCMD_SKIP or (sub and sub not in ("exec", "e", "app-server", "resume")): return None
    return {"exec": "codex exec", "e": "codex exec", "app-server": "codex app-server", "resume": "codex resume"}.get(sub, "codex session")


def _opt(args, *names):
    for i, a in enumerate(args):
        for n in names:
            if a == n and i + 1 < len(args): return args[i + 1]
            if a.startswith(n + "="): return a[len(n) + 1:]
    return None


VALUED = {"--model", "-m", "--append-system-prompt", "--system-prompt", "--permission-mode", "--output-format", "--input-format", "--tools",
          "--allowedTools", "--allowed-tools", "--disallowedTools", "--setting-sources", "--settings", "--mcp-config", "--resume", "-r",
          "--session-id", "--max-turns", "--max-budget-usd", "--effort", "--add-dir", "--agents", "--fallback-model", "-c", "--config",
          "-C", "--cd", "-s", "--sandbox", "-a", "--ask-for-approval", "-i", "--image", "--profile", "-p", "--output-last-message", "-o"}


def _prompt(which, args):
    """The task it was given: claude's -p value or last bare word, codex exec's prompt; first line, cut short. '' when on stdin."""
    if which == "claude":
        v = _opt(args, "-p", "--print")
        if v and not v.startswith("-"): return v.strip().split("\n")[0][:240]
    bare, skip = [], False
    for a in args:
        if skip: skip = False; continue
        if a.startswith("-"):
            skip = a in VALUED and not (which == "claude" and a == "-p"); continue
        bare.append(a)
    if which == "codex" and bare and bare[0] in ("exec", "e", "app-server", "resume"): bare = bare[1:]
    return bare[-1].strip().split("\n")[0][:240] if bare else ""


def _unit(pid):
    """The systemd unit a process runs in (hive-starter, tmux, a login shell), from its cgroup."""
    try: cg = Path(f"/proc/{pid}/cgroup").read_text().strip().split("\n")[-1].rsplit("/", 1)[-1]
    except OSError: return None
    if cg.startswith("tmux-spawn-"): return "tmux"
    if cg.startswith("session-") and cg.endswith(".scope"): return "a login shell"
    if cg.startswith("run-") and cg.endswith(".scope"): return "systemd-run"
    return cg.removesuffix(".service").removesuffix(".scope") or None


def _program(argv):
    """A readable name for a non-agentic ancestor: the script a python/node/bash runs, a tmux session, or the program itself.
    None for a wrapper (bash -c, timeout, heavy) the tree only passes through."""
    if not argv: return None
    b = os.path.basename(argv[0])
    if b.startswith("python") or b in ("node", "bun", "nodejs", "uv", "deno") or b in ("bash", "sh"):
        for a in argv[1:]:
            if a == "-c" and b in ("bash", "sh"): return None
            if a == "-m" or a.startswith("-"): continue
            return os.path.basename(a) if "/" in a or "." in a else a
        return None if b in ("bash", "sh") else b
    if b == "tmux": return "tmux session " + (_opt(argv[1:], "-s", "-t") or "?")
    if b in NOT_A_PROGRAM or b.startswith("systemd"): return None
    return b


def processes(runs=None, now=None):
    """Every claude or codex process of this user that is not itself an Hive agent's run: a `claude -p` or `codex exec` an agent ran from
    its shell, a script's model call (the grader, the Slack titler, the phone brain), the starter's warm agent, a session someone opened in
    tmux. Each says who started it: the agent whose run it sits under (agent), or else the program and unit it came from (by).
    runs: pid -> run dict (default ~/run/*.json). Asked for by the person, thread 111919537629 (2026-11-20): "show all alive agentic processes"."""
    now = now or time.time()
    if runs is None:
        runs = {}
        for f in (HOME / "run").glob("*.json"):
            try: r = json.loads(f.read_text()); runs[int(r["pid"])] = r
            except (OSError, ValueError, KeyError, TypeError): pass
    me = os.getuid()
    procs = {}
    for d in os.listdir("/proc"):
        if d.isdigit() and (p := _proc(int(d))) and p[3] == me: procs[int(d)] = p
    try: boot = float(next(l for l in open("/proc/stat") if l.startswith("btime")).split()[1])
    except (OSError, StopIteration, ValueError): boot = 0
    tick = os.sysconf("SC_CLK_TCK") if hasattr(os, "sysconf") else 100
    agentic = {}
    for pid, (ppid, _, argv, _) in procs.items():
        w = _which(argv)
        if not w or not (k := _kind(w, _args(argv))): continue
        par = procs.get(ppid)
        if par and _which(par[2]) == w and _args(par[2]) == _args(argv): continue   # the native binary under its node wrapper: one process
        agentic[pid] = (w, k)
    out = []
    for pid, (w, k) in agentic.items():
        if pid in runs: continue                         # an agent's own run: on the board already
        ppid, start, argv, _ = procs[pid]
        agent = thread = None; prog = None; anc = []; a, hops = ppid, 0
        while a and a in procs and hops < 64:
            if a in runs:
                if a not in agentic:                         # a run whose pid is a wrapper: its first claude/codex below is the run itself
                    if not anc: anc = None
                    else: anc.pop()
                agent, thread = runs[a].get("name"), runs[a].get("thread"); break
            if a in agentic: anc.append(a)
            elif prog is None: prog = _program(procs[a][2])
            a, hops = procs[a][0], hops + 1
        if anc is None: continue                             # this process is that run
        if not agent:                                        # its shell went (a `cmd &` in a subshell is handed to systemd): the agent's name is in its env
            try: env = dict(x.split("=", 1) for x in Path(f"/proc/{pid}/environ").read_bytes().decode(errors="replace").split("\0") if "=" in x)
            except OSError: env = {}
            r = next((r for r in runs.values() if r.get("name") and r.get("name") == env.get("HIVE_NAME")), None)
            if r: agent, thread = r["name"], r.get("thread")
        parent, depth = (f"pid{anc[0]}", 2) if anc else (None, 1)
        args = _args(argv)
        desc = _prompt(w, args)
        try: cwd = os.readlink(f"/proc/{pid}/cwd")
        except OSError: cwd = None
        if cwd: cwd = "~" + cwd[len(str(HOME)):] if cwd == str(HOME) or cwd.startswith(str(HOME) + "/") else cwd
        typ = k
        by = None
        if not agent:
            pp = procs.get(ppid)
            if k == "claude -p" and "stream-json" in (_opt(args, "--input-format") or "") and pp and any("starter.py" in x for x in pp[2]) and not desc:
                typ, desc, by = "warm agent", "booted ahead for the next new agent; waits for its first post", "starter"
            else:
                unit = _unit(pid)
                by = prog if not unit or unit == prog or (prog or "").startswith("tmux") else f"{prog} ({unit})" if prog else unit
        if not desc: desc = "task on stdin" + (f", from {prog}" if prog else "") + (f", in {cwd}" if cwd and cwd != "~" else "")
        t0 = boot + start / tick if boot else None
        out.append({"harness": "codex" if w == "codex" else "claude-code", "kind": "process", "id": f"pid{pid}", "pid": pid, "parent": parent,
                    "type": typ, "desc": desc, "model": _opt(args, "--model", "-m"), "mode": "process", "depth": depth,
                    "start": iso(t0) if t0 else None, "end": iso(now), "state": "running", "calls": None, "tools": None, "out_tokens": None,
                    "usd": None, "file": None, "moved": now, "agent": agent, "agent_thread": thread, "by": by, "cwd": cwd})
    return out


WF_DONE = {"completed", "failed", "error", "killed", "stopped", "cancelled", "canceled"}


def workflows(since, cm=None, st=None, up=None):
    """Workflow runs (Claude Code's Workflow tool) an Hive run started: <login>/projects/<dir>/<session>/workflows/wf_*.json.
    Their agents are helpers already; this row is the script that drove them."""
    logins = cm.logins() if cm else [str(HOME / ".claude")]
    st = st if st is not None else starts(); up = up if up is not None else alive()
    for login in logins:
        for f in glob.glob(os.path.join(login, "projects", "*", "*", "workflows", "wf_*.json")):
            try: mt = os.path.getmtime(f)
            except OSError: continue
            if mt < since: continue
            try: d = json.loads(Path(f).read_text())
            except (OSError, ValueError): continue
            sess = Path(f).parent.parent.name; e = st.get(sess) or {}
            t0 = (d.get("startTime") or 0) / 1000 or mt
            status = str(d.get("status") or "").lower()
            state = "done" if status in WF_DONE else "running" if sess in up else "stopped"
            yield {"harness": "claude-code", "kind": "workflow", "session": sess, "id": d.get("runId") or Path(f).stem, "parent": None,
                   "type": "workflow", "desc": d.get("workflowName") or d.get("summary") or "", "model": d.get("defaultModel"), "mode": "workflow",
                   "depth": 1, "start": iso(t0), "end": iso(t0 + (d.get("durationMs") or 0) / 1000) if state == "done" else iso(mt), "state": state,
                   "calls": d.get("agentCount"), "tools": d.get("totalToolCalls"), "out_tokens": d.get("totalTokens"), "usd": None, "file": f,
                   "moved": mt, "agent": e.get("name"), "agent_thread": e.get("thread")}


def dur(a, b):
    try: s = int((dt.datetime.fromisoformat(b.replace("Z", "+00:00")) - dt.datetime.fromisoformat(a.replace("Z", "+00:00"))).total_seconds())
    except (AttributeError, ValueError, TypeError): return "?"
    return f"{s}s" if s < 60 else f"{s // 60}m{s % 60:02d}s" if s < 3600 else f"{s // 3600}h{s % 3600 // 60:02d}m"


def collect(since, memo=None):
    cm = _cost()
    st = starts(); up = alive()
    by_codex = {}
    for s, e in st.items():
        if e.get("harness") == "codex" and e.get("out"):
            t = codex_thread(e["out"])
            if t: by_codex[t] = e
    out, seen = [], {}
    for h in claude_helpers(since, cm, memo):
        # a run that moves to another login takes its session folder along, so one helper can sit under several logins: keep the
        # copy that went furthest (most calls, then latest), once
        k = (h["session"], h["id"]); o = seen.get(k)
        if o is not None and (o["calls"], o["end"] or "") >= (h["calls"], h["end"] or ""): continue
        e = st.get(h["session"]) or {}
        seen[k] = settle({**h, "agent": e.get("name"), "agent_thread": e.get("thread")}, h["session"] in up)
    out.extend(seen.values())
    for h in codex_helpers(since):
        e = by_codex.get(h["thread"]) or {}
        out.append(settle({**h, "agent": e.get("name"), "agent_thread": e.get("thread")}, bool(e.get("out")) and e["out"] in up))
    out.extend(workflows(since, cm, st, up))
    try: out.extend(processes())
    except OSError as ex: print(f"(processes unreadable: {ex})", file=sys.stderr)
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive subagents", description="Helpers the Hive's agents started inside Claude Code (Agent tool) or Codex (multi_agent), and every live claude or codex process outside the agents' own runs (claude -p, codex exec, workflow scripts, a tmux session), grouped by who started each.")
    ap.add_argument("names", nargs="*", help="only these agents (e.g. a1b2c)")
    ap.add_argument("--since", default="24h", help="how far back by last activity: 2h, 3d, 1w or an ISO time (default 24h)")
    ap.add_argument("--running", action="store_true", help="only helpers still running")
    ap.add_argument("--all", action="store_true", help="also helpers of sessions Hive didn't start (your own CLI sessions)")
    ap.add_argument("--json", action="store_true", help="one JSON object per helper")
    ap.add_argument("-v", "--files", action="store_true", help="also each helper's transcript path")
    a = ap.parse_args(argv)
    cm = _cost()
    cut = cm.parse_since(a.since).timestamp() if cm else time.time() - 86400
    rows = collect(cut)
    want = {n.lstrip("@").lower() for n in a.names}
    rows = [r for r in rows if (a.all or r["agent"] or r.get("by")) and (not want or (r["agent"] or "").lower() in want) and (not a.running or r["state"] == "running")]
    rows.sort(key=lambda r: (r["start"] or ""))
    if a.json:
        for r in rows: print(json.dumps(r))
        return
    if not rows:
        print(f"No subagents{' running' if a.running else ''} since {a.since}{' for ' + ', '.join(sorted(want)) if want else ''}."); return
    groups = {}
    for r in rows: groups.setdefault((r["agent"] and "@" + r["agent"] or r.get("by") and "started by " + r["by"] or "(not an Hive run)", r["agent_thread"], r["harness"], r.get("session") or r.get("thread")), []).append(r)
    tot = sum(r["usd"] or 0 for r in rows)
    print(f"{len(rows)} subagents in {len(groups)} runs since {a.since}" + (f", ${tot:.2f} at list prices" if tot else "") + ". Times UTC.")
    for (name, thread, harness, sess), hs in groups.items():
        print(f"\n{name}" + (f"  thread {thread}" if thread else "") + f"  {harness}" + (f"  session {sess}" if name.startswith("(") else ""))
        for r in hs:
            bits = [(r["start"] or "?")[5:16].replace("T", " "), f"{dur(r['start'], r['end']):>7}", f"{r['state']:<8}",
                    (r["type"] or "?")[:16], (r["model"] or "?").replace("claude-", ""), {"background": "bg", "foreground": "fg", "process": "proc"}.get(r["mode"], "")]
            if r["calls"] is not None: bits.append(f"{r['calls']} calls")
            if r["usd"] is not None: bits.append(f"${r['usd']:.2f}")
            elif r["out_tokens"]: bits.append(f"{r['out_tokens']} tok")
            d = " ".join(str(r["desc"]).split())
            print("  " + "  ".join(b for b in bits if b) + f"  {d[:70]!r}")
            if a.files: print(f"      {r['file'] or ('pid %s in %s' % (r.get('pid'), r.get('cwd')) if r.get('pid') else '')}")


if __name__ == "__main__":
    main()
