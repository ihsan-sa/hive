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
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive subagents", description="Helpers the Hive's agents started inside Claude Code (Agent tool) or Codex (multi_agent), grouped by agent.")
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
    rows = [r for r in rows if (a.all or r["agent"]) and (not want or (r["agent"] or "").lower() in want) and (not a.running or r["state"] == "running")]
    rows.sort(key=lambda r: (r["start"] or ""))
    if a.json:
        for r in rows: print(json.dumps(r))
        return
    if not rows:
        print(f"No subagents{' running' if a.running else ''} since {a.since}{' for ' + ', '.join(sorted(want)) if want else ''}."); return
    groups = {}
    for r in rows: groups.setdefault((r["agent"] or "(not an Hive run)", r["agent_thread"], r["harness"], r.get("session") or r.get("thread")), []).append(r)
    tot = sum(r["usd"] or 0 for r in rows)
    print(f"{len(rows)} subagents in {len(groups)} runs since {a.since}" + (f", ${tot:.2f} at list prices" if tot else "") + ". Times UTC.")
    for (name, thread, harness, sess), hs in groups.items():
        print(f"\n@{name}" + (f"  thread {thread}" if thread else "") + f"  {harness}" + (f"  session {sess}" if name.startswith("(") else ""))
        for r in hs:
            bits = [(r["start"] or "?")[5:16].replace("T", " "), f"{dur(r['start'], r['end']):>7}", f"{r['state']:<8}",
                    (r["type"] or "?")[:16], (r["model"] or "?").replace("claude-", ""), {"background": "bg", "foreground": "fg"}.get(r["mode"], "")]
            if r["calls"] is not None: bits.append(f"{r['calls']} calls")
            if r["usd"] is not None: bits.append(f"${r['usd']:.2f}")
            elif r["out_tokens"]: bits.append(f"{r['out_tokens']} tok")
            d = " ".join(str(r["desc"]).split())
            print("  " + "  ".join(b for b in bits if b) + f"  {d[:70]!r}")
            if a.files: print(f"      {r['file']}")


if __name__ == "__main__":
    main()
