#!/usr/bin/env python3
"""hive goals: the hive's goals in one place, each with who is moving it, no model use.

The person's goals are spread over four records: the focuses in ~/kb/priorities.md (their numbered list), the standing
directions in the asks register (kind direction), the goals.md of each project, and the open asks. Nothing joined them, so
"is anyone working toward X" was a search. Built 2026-11-04 for the ask that goals be extracted from every interaction, filed,
and worked toward (post 11040210d8cf). A goal with no thread post for days and nobody running in it is listed as idle, oldest
first, for the keeper to start someone on, or to say in the thread why it waits.
"""
import argparse, datetime as dt, importlib.util, json, re, sys
from pathlib import Path

HOME = Path.home()
CORE, PROJECTS, KB, RUN = HOME / "core", HOME / "projects", HOME / "kb", HOME / "run"
BOARD = HOME / "board.jsonl"
FOCUS = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`", re.M)
MEASURE = re.compile(r"^.*\b(measure|metric|score|yardstick|benchmark|pass rate|p&l|sharpe)\b.*$", re.I | re.M)   # the same words progress.py counts as a measure
THREAD = re.compile(r"\bthreads?\s+`?([0-9a-f]{12}|[a-z][a-z0-9-]{2,})`?", re.I)
NOT_AGENTS = {"person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer"}
IDLE_D = 2.0   # a project goal with no agent post in its thread for this long and nobody running in it is idle
FOCUS_IDLE_H = 2.0   # a focus of the person's goes idle much sooner: it is their current work, and a focus is never held for an idle owner (2026-11-05, post 1104224972e6)

def now(): return dt.datetime.now(dt.timezone.utc)

def jsonl(path):
    out = []
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line: continue
                try: out.append(json.loads(line))
                except ValueError: pass
    except OSError: pass
    return out

def asks_module():
    spec = importlib.util.spec_from_file_location("asks", CORE / "asks.py"); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m

def live_runs():
    out = []
    for p in RUN.glob("*.json"):
        try: out.append(json.loads(p.read_text()))
        except (OSError, ValueError): pass
    return out

def focuses(text):
    """The numbered **title** (thread `x`) items of the newest block in priorities.md that has them."""
    return [{"n": int(n), "title": t.strip(), "thread": th} for n, t, th in FOCUS.findall(text)]

def project_goals():
    out = []
    for g in sorted(PROJECTS.glob("*/goals.md")):   # lowercase only: the GOALS.md of cloned repos (ece298a-pll, resume) are their own files, not Hive goals
        if g.parent.name.startswith("_"): continue
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        head = next((l.lstrip("# ").strip() for l in txt.splitlines() if l.strip()), g.parent.name)
        m = MEASURE.search(txt)
        measure = re.sub(r"^\W*(measure|metric)\W*", "", m.group(0).strip(), flags=re.I)[:160] if m else None
        out.append({"project": g.parent.name, "goal": head[:120], "measure": measure, "threads": [t.lower() for t in THREAD.findall(txt) if t.lower() != "thread"][:8],
                    "edited": dt.datetime.fromtimestamp(g.stat().st_mtime, dt.timezone.utc).isoformat(timespec="seconds")})
    return out

def thread_state(board, runs, t):
    """Per thread: last agent post (at, from), last person post, running agents."""
    st = {}
    for p in board:
        th = p.get("thread") or p.get("id"); s = st.setdefault(th, {"agent_at": "", "agent": "", "person_at": "", "posts": 0, "running": set()})
        s["posts"] += 1
        f = str(p.get("from", "")).lower()
        if f == "person": s["person_at"] = max(s["person_at"], p.get("at", ""))
        elif f not in NOT_AGENTS and p.get("at", "") >= s["agent_at"]: s["agent_at"], s["agent"] = p.get("at", ""), p.get("from")
    for r in runs:
        if r.get("thread"): st.setdefault(r["thread"], {"agent_at": "", "agent": "", "person_at": "", "posts": 0, "running": set()})["running"].add(r.get("name"))
    return st

def days(at, t):
    if not at: return None
    try: return round((t - dt.datetime.fromisoformat(at)).total_seconds() / 86400, 1)
    except ValueError: return None

def goals(t=None):
    t = t or now(); board = jsonl(BOARD); runs = live_runs(); st = thread_state(board, runs, t)
    try: asks = asks_module(); items = asks.ledger(); open_asks = [e for e in asks.open_items(items) if e.get("kind") != "direction"]
    except Exception as e: print(f"asks register unreadable: {e}", file=sys.stderr); items, open_asks = {}, []
    directions = [e for e in items.values() if e.get("kind") == "direction" and e.get("status") == "standing"]
    try: pri = (KB / "priorities.md").read_text(errors="replace")
    except OSError: pri = ""
    rows = []
    projects = project_goals(); pmeasure = {g["project"]: g["measure"] for g in projects}
    for g in projects:  # a focus's measure comes from the project of the same name, else from one whose goals.md names its thread
        for th in g["threads"]: pmeasure.setdefault(th, g["measure"])
    for f in focuses(pri):
        s = st.get(f["thread"], {}); rows.append({"kind": "focus", "name": f"{f['n']}. {f['title']}", "thread": f["thread"], "measure": pmeasure.get(f["thread"]), **state_row(s, f["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)})
    for g in projects:
        th = g["project"]; cands = [th] + [k for k in g["threads"] if k != th] + [k for k in st if k != th and k not in g["threads"] and (k.startswith(th + "-") or k.startswith(th + "_"))]
        best = max(cands, key=lambda k: st.get(k, {}).get("agent_at", ""))
        s = dict(st.get(best, {})); s["running"] = set().union(*(st.get(k, {}).get("running", set()) for k in cands))
        rows.append({"kind": "project", "name": g["project"], "goal": g["goal"], "thread": best, "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, cands, open_asks, t)})
    covered = {r["thread"] for r in rows} | {th for r in rows for th in (r.get("threads") or [])}
    loose = [e for e in open_asks if e.get("thread") not in covered]
    return {"at": t.isoformat(timespec="seconds"), "rows": rows, "directions": len(directions), "open_asks": len(open_asks), "loose_asks": len(loose),
            "loose_threads": sorted({e.get("thread") for e in loose}, key=lambda x: str(x))}

def state_row(s, threads, open_asks, t, idle_d=IDLE_D):
    threads = [threads] if isinstance(threads, str) else list(threads)
    mine = [e for e in open_asks if e.get("thread") in threads]
    limit, idle_d = idle_d, days(s.get("agent_at"), t)
    running = sorted(s.get("running", set()))
    idle = not running and (idle_d is None or idle_d >= limit)
    return {"threads": threads, "last_agent": s.get("agent"), "last_agent_d": idle_d, "person_d": days(s.get("person_at"), t), "running": running,
            "open_asks": len(mine), "owned_asks": sum(1 for e in mine if e.get("status") == "owned"), "idle": idle}

def focus_quiet(t=None, hours=4.0):
    """Focuses of the person's with no agent post for `hours` and nobody running: the number progress.py reports as focus_quiet (lower is better)."""
    t = t or now(); st = thread_state(jsonl(BOARD), live_runs(), t)
    try: pri = (KB / "priorities.md").read_text(errors="replace")
    except OSError: return []
    out = []
    for f in focuses(pri):
        s = st.get(f["thread"], {}); d = days(s.get("agent_at"), t)
        if not s.get("running") and (d is None or d >= hours / 24): out.append(f["thread"])
    return out

def render(g):
    lines = [f"Goals at {g['at'][:16]}: {g['directions']} standing directions (`hive asks directions`), {g['open_asks']} open asks, {g['loose_asks']} of them in threads no focus or project covers."]
    lines.append("\n| goal | thread | last agent post | running | open asks (owned) | measure |"); lines.append("|---|---|---|---|---|---|")
    for r in sorted(g["rows"], key=lambda r: (r["kind"] != "focus", -(r["last_agent_d"] or 999) if r["idle"] else 0)):
        last = "never" if r["last_agent_d"] is None else f"{r['last_agent']} {r['last_agent_d']:g} d ago"
        name = r["name"] if r["kind"] == "focus" else f"{r['name']}: {r.get('goal', '')[:60]}"
        lines.append(f"| {name} | {r['thread']} | {last}{' **idle**' if r['idle'] else ''} | {', '.join(r['running']) or '-'} | {r['open_asks']} ({r['owned_asks']}) | {(r['measure'] or 'none')[:70]} |")
    idle = [r for r in g["rows"] if r["idle"]]
    if idle: lines.append(f"\nIdle (nobody running, no agent post in {FOCUS_IDLE_H:g} h for a focus or {IDLE_D:g} days for a project): " + ", ".join(r["name"].split(":")[0] for r in idle) + ". Start someone on each, or say in its thread why it waits. A focus is never left idle because its owner is: start a fresh agent that reads the thread and takes the next step that does not need the person.")
    nomeasure = [r["name"] for r in g["rows"] if r["kind"] == "project" and not r["measure"]]
    if nomeasure: lines.append("Projects whose goals.md names no measure: " + ", ".join(nomeasure))
    if g["loose_threads"]: lines.append(f"Open asks outside every focus and project: {g['loose_asks']} in {len(g['loose_threads'])} threads (`hive asks list`, `hive asks check`); these are the person's one-off asks and agents' leftovers, moved by the keeper, not by a goal.")
    return "\n".join(lines)

def main():
    ap = argparse.ArgumentParser(prog="hive goals", description=__doc__.split("\n")[0])
    ap.add_argument("--json", action="store_true"); ap.add_argument("--idle", action="store_true", help="only the idle goals")
    a = ap.parse_args()
    g = goals()
    if a.idle: g["rows"] = [r for r in g["rows"] if r["idle"]]
    print(json.dumps(g, indent=1, default=str) if a.json else render(g))

if __name__ == "__main__": main()
