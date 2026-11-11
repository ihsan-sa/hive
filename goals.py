#!/usr/bin/env python3
"""hive goals: the hive's goals in one place, each with who is moving it, no model use.

The person's goals are spread over four records: the focuses in ~/kb/priorities.md (their numbered list), the standing
directions in the asks register (kind direction), the goals.md of each project, and the open asks. Nothing joined them, so
"is anyone working toward X" was a search. Built 2026-11-04 for the ask that goals be extracted from every interaction, filed,
and worked toward (post 11040210d8cf). A goal with no thread post for days and nobody running in it is listed as idle, oldest
first, for the keeper to start someone on, or to say in the thread why it waits.

Ladders (2026-11-06, thread focus-ladders): a `## Ladder` section in a goals.md (or a `## <Topic> ladder` section naming its own thread, one goal each) lists next steps that need nobody's sign-off, each with a
`check:` that says when it is done. HW projects keep moving because their gates define the next step; focuses stopped because a fresh agent
with a thin brief re-read "nothing left" and repeated it. `hive goals --brief THREAD` hands a round its rung, and `hive goals --rounds` counts how
many rounds did work rather than report a wait.
"""
import argparse, datetime as dt, importlib.util, json, re, subprocess, sys, zlib
from pathlib import Path

HOME = Path.home()
CORE, PROJECTS, KB, RUN = HOME / "core", HOME / "projects", HOME / "kb", HOME / "run"
BOARD = HOME / "board.jsonl"
FOCUS = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`", re.M)
MEASURE = re.compile(r"^.*\b(measure|metric|score|yardstick|benchmark|pass rate|p&l|sharpe)\b.*$", re.I | re.M)   # the same words progress.py counts as a measure
THREAD = re.compile(r"\bthreads?\s+`?([0-9a-f]{12}|[a-z][a-z0-9-]{2,})`?", re.I)
NOT_AGENTS = {"person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer", "postland", "leftover-router"}
IDLE_D = 2.0   # a project goal with no agent post in its thread for this long and nobody running in it is idle
FOCUS_IDLE_H = 2.0   # a focus of the person's goes idle much sooner: it is their current work, and a focus is never held for an idle owner (2026-11-05, post 1104224972e6)

LADDER = re.compile(r"^##\s+Ladder\b(.*)$", re.M)
LADDER_ANY = re.compile(r"^##\s+(.*\bladder\b.*)$", re.M | re.I)   # a topic ladder of its own: `## Reflection ladder (thread meta-reflect)` moves at a focus's pace in that thread
RUNG = re.compile(r"^\s*-\s+\[([ x!])\]\s+(.+)$", re.M)
AFTER = re.compile(r"\bafter:(\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2})?)Z?\b")   # a day, or a UTC minute (after:2026-11-08T09:50Z) for a rung that waits on a timed run
GATE = re.compile(r"\b(?:on or after|not before|after|on|from|until|revisits?|re-?recheck|re-?check|recheck|read(?:ing)?|judge)\b[^.;:()]{0,24}?\b(\d{4}-\d{2}-\d{2})\b", re.I)   # the same wait in words: 38 of 47 floor rounds that did nothing (2026-11-09/10) woke on a reading written with a date but no after:
CHECK_ON = re.compile(r"check:\s*(?:on or after|not before|on|after|from)\s+(\d{4}-\d{2}-\d{2})\b", re.I)   # a check that opens with its date ("check: on 2026-11-18 ...") can't pass before it; other dates in a check don't gate (rsi-lab 11-11: 3 such rungs woke the keeper every 2 h)
PARKED = re.compile(r"^Parked until (\d{4}-\d{2}-\d{2}):\s*(.+)$", re.M)
PARK_MAX_D = 3   # a parked ladder comes back within this many days; a date further out doesn't count, so nobody parks a focus for good
WAIT = re.compile(r"no step left|changed nothing|nothing (has )?changed|nothing left|all with the person|no new person posts|started no round|(?<!nothing )\bwaits? (only )?on (the person|a sign-in)", re.I)   # how a round says it did no work
ROUND = re.compile(r"\bkeeper round\b", re.I)
BUILD = re.compile(r"\b(built|landed|committed|shipped|implemented|prototype|first version)\b", re.I)   # a post that says something was built
BUILD_D = 1.0   # a project with no ladder and a build post this recent needs one now (Nov 6 build rule), not after IDLE_D
BRIEF = re.compile(r"^@\S+ keeper round\b", re.I)   # a round's brief: to @new, or to the thread's last worker

def now(): return dt.datetime.now(dt.timezone.utc)

def ladder(txt, t=None):
    """The `## Ladder` section of a goals.md, or None. Rungs are `- [ ] what. check: how to tell`; `[x]` done, `[!]` blocked, `after:YYYY-MM-DD` not before that day (`after:YYYY-MM-DDTHH:MMZ`, not before that UTC minute)."""
    m = LADDER.search(txt)
    return section(txt, m, m.group(1), t) if m else None

def ladders(txt, t=None):
    """Every other `## <Topic> ladder ...` section that names a thread (in backticks, after the word thread, or the first hyphenated name in the heading):
    a topic ladder of a project with several, like hive-meta's. Each is a goal of its own, at a focus's pace in its thread (2026-11-07: meta-reflect sat
    2.4 h with open rungs because only `## Ladder` was read). The plain `## Ladder` is `ladder()`, not here."""
    out = []
    for m in LADDER_ANY.finditer(txt):
        if LADDER.match(m.group(0)): continue
        lad = section(txt, m, m.group(1), t)
        if lad["thread"]: lad["section"] = re.split(r"[(:]", m.group(1), maxsplit=1)[0].strip(); out.append(lad)
    return out

def section(txt, m, heading, t=None):
    t = t or now()
    body = txt[m.end():]; end = re.search(r"^#{1,2}\s", body, re.M); body = body[:end.start()] if end else body
    th = re.search(r"`([^`]+)`", heading) or THREAD.search(heading) or re.search(r"(?<![\w-])([a-z][a-z0-9]*-[a-z0-9-]+)(?![\w-])", heading)
    rungs = []; today = t.date().isoformat()
    for state, text in RUNG.findall(body):
        a = AFTER.search(text) or GATE.search(text.split("check:")[0]) or CHECK_ON.search(text)
        rungs.append({"state": state, "text": text.strip(), "after": a.group(1) if a else None, "checked": "check:" in text})
    nxt = next((r for r in rungs if r["state"] == " " and r["checked"] and (not r["after"] or r["after"] <= (today if len(r["after"]) == 10 else t.strftime("%Y-%m-%dT%H:%M")))), None)
    p = PARKED.search(body); parked = None
    if p and today < p.group(1) <= (t.date() + dt.timedelta(days=PARK_MAX_D)).isoformat(): parked = {"until": p.group(1), "why": p.group(2).strip()}
    return {"thread": th.group(1).lower() if th else None, "rungs": rungs, "next": nxt, "parked": parked, "done": sum(r["state"] == "x" for r in rungs),
            "left": sum(r["state"] == " " for r in rungs), "no_check": sum(r["state"] == " " and not r["checked"] for r in rungs)}

def rounds(board, threads, since):
    """Keeper rounds in these threads since `since` (ISO): one per brief, graded on the last post of the agent that took it. {thread: [work, wait, silent]}"""
    out = {th: [0, 0, 0] for th in threads}; by = {}
    for p in board:
        if p.get("thread") in out: by.setdefault(p["thread"], []).append(p)
    for th, posts in by.items():
        for i, p in enumerate(posts):
            if p.get("at", "") < since or not BRIEF.search(p.get("text", "")): continue
            later = [q for q in posts[i + 1:] if str(q.get("from", "")).lower() not in NOT_AGENTS and q.get("from") != p.get("from")]
            if not later or (i + 2 < len(posts) and BRIEF.search(later[0].get("text", ""))): out[th][2] += 1; continue
            who = later[0].get("from"); last = [q for q in later if q.get("from") == who][-1]
            out[th][1 if WAIT.search(last.get("text", "")) else 0] += 1
    return out

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
                    "edited": dt.datetime.fromtimestamp(g.stat().st_mtime, dt.timezone.utc).isoformat(timespec="seconds"), "ladder": ladder(txt), "ladders": ladders(txt), "path": str(g)})
    return out

def thread_state(board, runs, t):
    """Per thread: last agent post (at, from), last person post, running agents."""
    st = {}
    for p in board:
        th = p.get("thread") or p.get("id"); s = st.setdefault(th, {"agent_at": "", "agent": "", "person_at": "", "posts": 0, "running": set()})
        s["posts"] += 1
        f = str(p.get("from", "")).lower()
        if f == "person": s["person_at"] = max(s["person_at"], p.get("at", ""))
        elif f not in NOT_AGENTS and p.get("at", "") >= s["agent_at"]:
            s["agent_at"], s["agent"] = p.get("at", ""), p.get("from")
            if not BRIEF.search(p.get("text", "")): s["worker"] = p.get("from")
            if BUILD.search(p.get("text", "")) and not BRIEF.search(p.get("text", "")): s["build_at"] = p.get("at", "")   # who did the thread's work, not who posted its last brief
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
    claimed = {}   # focus thread -> the project whose ladder it takes: same name, or the ladder heading names the thread
    fs = focuses(pri); fth = {f["thread"] for f in fs}
    for g in projects:
        if g["ladder"]:
            for th in (g["ladder"]["thread"], g["project"]):
                if th in fth: claimed.setdefault(th, g); break
    for f in fs:
        s = st.get(f["thread"], {}); g = claimed.get(f["thread"]); row = {"kind": "focus", "name": f"{f['n']}. {f['title']}", "thread": f["thread"], "measure": pmeasure.get(f["thread"]), **state_row(s, f["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)}
        rows.append(with_ladder(row, g))
    taken = {g["project"] for g in claimed.values()}
    for g in projects:
        for lad in g.get("ladders") or []:   # a topic ladder of its own (hive-meta's Reflection ladder, thread meta-reflect): a goal at a focus's pace in that thread
            if lad["thread"] in fth or any(r["thread"] == lad["thread"] for r in rows): continue
            s = dict(st.get(lad["thread"], {})); rows.append(with_ladder({"kind": "project", "name": f"{g['project']}: {lad['section']}", "goal": g["goal"], "thread": lad["thread"], "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, lad["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)}, {"ladder": lad, "path": g["path"]}))
        lad = g["ladder"] if g["project"] not in taken else None   # a project with a ladder no focus takes moves at a focus's pace, in the thread its heading names
        if lad and lad["thread"]:
            s = dict(st.get(lad["thread"], {})); rows.append(with_ladder({"kind": "project", "name": g["project"], "goal": g["goal"], "thread": lad["thread"], "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, lad["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)}, g)); continue
        th = g["project"]; cands = [th] + [k for k in g["threads"] if k != th] + [k for k in st if k != th and k not in g["threads"] and (k.startswith(th + "-") or k.startswith(th + "_"))]
        best = max(cands, key=lambda k: st.get(k, {}).get("agent_at", ""))
        s = dict(st.get(best, {})); s["running"] = set().union(*(st.get(k, {}).get("running", set()) for k in cands))
        row = {"kind": "project", "name": g["project"], "goal": g["goal"], "thread": best, "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, cands, open_asks, t)}
        built = [d for d in (days(st.get(k, {}).get("build_at"), t) for k in cands) if d is not None]
        if not row["running"] and built and min(built) <= BUILD_D:   # built within a day and no ladder: idle now, and the round writes it a ladder
            row.update(idle=True, needs_ladder=True, ladder=False, goals_path=g["path"], rung=None, last_agent_d=min(built))
        rows.append(row)
    covered = {r["thread"] for r in rows} | {th for r in rows for th in (r.get("threads") or [])}
    loose = [e for e in open_asks if e.get("thread") not in covered]
    return {"at": t.isoformat(timespec="seconds"), "rows": rows, "directions": len(directions), "open_asks": len(open_asks), "loose_asks": len(loose),
            "loose_threads": sorted({e.get("thread") for e in loose}, key=lambda x: str(x))}

def with_ladder(row, g):
    lad = g["ladder"] if g else None
    row["ladder"] = bool(lad); row["goals_path"] = g["path"] if g else None
    if lad:
        row.update(rung=lad["next"]["text"] if lad["next"] else None, rungs_left=lad["left"], rungs_done=lad["done"], rungs_no_check=lad["no_check"], parked=lad["parked"], **({"section": lad["section"]} if lad.get("section") else {}))
        if lad["parked"]: row["idle"] = False
    return row

def resume_arm(thread):
    """Keeper briefs wake the thread's last worker, who keeps what it learned there (the starter gives an heir to one that is done or over
    resume_max_context). The 11-06..11-09 A/B (hive-meta/continuity.md "A/B result") found resume 52% delivered vs fresh 32%, no kill, so it is the default."""
    return True

def post_model_args(text, model):
    """A brief's model is for a fresh agent only: on a woken worker the starter would hold it from then on, undoing a grader step-up or
    the worker's own `hive model` (review 110618506919)."""
    return ["--model", model] if model and text.startswith("@new ") else []

def brief(row):
    """The brief a keeper round posts for one focus. Every clause is here because a thin hand-written brief produced the 'nothing changed' rounds."""
    to = row["last_worker"] if row.get("last_worker") and resume_arm(row.get("thread")) else "new"
    head = f"@{to} Keeper round on {row['name']}. The ladder is the `## {row.get('section') or 'Ladder'}` section of {row.get('goals_path') or 'this focus project goals.md (create it, one line a rung)'}: read that whole file, and obey any notes in the section.\n\n"
    rung = f"**Your rung:** {row['rung']}\n\nDo this rung. Land real work, then run its check." if row.get("rung") else "**The ladder has no rung you can take.** Your work this round is to write the next 3 to 5 rungs from the measure in that file, then do the first one."
    return head + rung + """

Rules for the round:
- Read the last 5 posts of this thread for context, not for a conclusion. Earlier posts here may say nothing is left: that was a check-in, not a finding. "Nothing changed" is not a result, and you are not here to re-check the state.
- A rung is work that needs nobody's sign-off and moves the measure in goals.md, with a `check:` someone else could run. Hard limits hold (no money, no acting in the person's name, no credentials handed out). Drafting, building, simulating and testing are all open while an account or a go waits.
- Check passes: in goals.md change `- [ ]` to `- [x]` and append your result post's id. Too big for one run: split it into rungs, land the first, tick that.
- Rung truly needs the person or is blocked: change it to `- [!]` with why (add a `For you:` line in your post if they must act), then take the next rung in this same run. A `[!]` costs you nothing and is not your result.
- Keep the focus's to-do list, which the person sees and ticks on its page: the next two or three steps in plain words (`hive focus check add FOCUS "text"`), ticked as each lands (`hive focus check done ID`; `hive focus check list FOCUS` shows them).
- A rung that cannot pass before a date or time (a full day's reading, a window that ends later) carries `after:YYYY-MM-DD` or `after:YYYY-MM-DDTHH:MMZ` in its text: the keeper skips it until then. Without the tag it wakes an agent every hour to find nothing to do.
- When what is left waits on the person or a date, ask whether the goal or its Measure is still right; then either write a step that moves the Measure, with a `check:`, or end with the wait.
- Park only when the measure is at target, or every way left to move it needs the person (name which): add a line `Parked until YYYY-MM-DD: why` to the section (at most 3 days out, later dates are ignored) and say so. The keeper skips it until then. Waiting on a date or a later reading is no reason to park: write a rung that moves the measure today, or if the measure has stopped saying anything, rewrite it and keep the old one and the reason beside it.
- Your last post says: the rung, the check's output, the measure now, and ends with the line `Next: <the next rung, with its check>` (the starter carries it on), or a `Blocked:` or `Needs the person:` line if nothing is left you can do, or `Waiting:` naming who or when (`Waiting: a1b2c's review`, `Waiting: the sim, about 04:20 UTC`). The next agent comes within minutes unless your wait names a running agent or a time. If the work turns hard, `hive model opus "why"`."""

def state_row(s, threads, open_asks, t, idle_d=IDLE_D):
    threads = [threads] if isinstance(threads, str) else list(threads)
    mine = [e for e in open_asks if e.get("thread") in threads]
    limit, idle_d = idle_d, days(s.get("agent_at"), t)
    running = sorted(s.get("running", set()))
    idle = not running and (idle_d is None or idle_d >= limit)
    at = s.get("agent_at")
    return {"threads": threads, "last_agent": s.get("agent"), "last_worker": s.get("worker"), "last_agent_d": idle_d, "quiet_s": (t - dt.datetime.fromisoformat(at)).total_seconds() if at and idle_d is not None else None, "person_d": days(s.get("person_at"), t), "running": running,
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
    rungs = [r for r in g["rows"] if r.get("ladder") or r["kind"] == "focus"]
    if rungs: lines.append("\nNext rung (`hive goals --brief THREAD` writes the round's brief):")
    for r in rungs:
        state = "NO LADDER" if not r.get("ladder") else f"parked until {r['parked']['until']}: {r['parked']['why'][:90]}" if r.get("parked") else (r.get("rung") or "LADDER EMPTY: the round writes new rungs")[:130]
        warn = f" ({r['rungs_no_check']} open rungs have no check)" if r.get("rungs_no_check") else ""
        lines.append(f"- {r['thread']} ({r.get('rungs_done', 0)} done, {r.get('rungs_left', 0)} left): {state}{warn}")
    idle = [r for r in g["rows"] if r["idle"]]
    if idle: lines.append(f"\nIdle (nobody running, no agent post in {FOCUS_IDLE_H:g} h for a focus or {IDLE_D:g} days for a project): " + ", ".join(r["name"].split(":")[0] for r in idle) + ". Start someone on each, or say in its thread why it waits. A focus is never left idle because its owner is: start a fresh agent that reads the thread and takes the next step that does not need the person.")
    nomeasure = [r["name"] for r in g["rows"] if r["kind"] == "project" and not r["measure"]]
    if nomeasure: lines.append("Projects whose goals.md names no measure: " + ", ".join(nomeasure))
    if g["loose_threads"]: lines.append(f"Open asks outside every focus and project: {g['loose_asks']} in {len(g['loose_threads'])} threads (`hive asks list`, `hive asks check`); these are the person's one-off asks and agents' leftovers, moved by the keeper, not by a goal.")
    return "\n".join(lines)

def main():
    ap = argparse.ArgumentParser(prog="hive goals", description=__doc__.split("\n")[0])
    ap.add_argument("--json", action="store_true"); ap.add_argument("--idle", action="store_true", help="only the idle goals")
    ap.add_argument("--brief", metavar="THREAD", help="print the keeper round's brief for this focus or ladder thread, with its next rung"); ap.add_argument("--ladder-threads", action="store_true", help="idle focus and ladder threads, one a line: each gets a round")
    ap.add_argument("--post", metavar="THREAD", help="post that brief to its thread; --model applies only to a fresh agent, so a woken worker keeps its own (a grader step-up, its own hive model)")
    ap.add_argument("--model", help="with --post: the model for a fresh agent")
    ap.add_argument("--force", action="store_true", help="with --post: post even to a parked ladder")
    ap.add_argument("--rounds", metavar="DAYS", type=float, help="keeper rounds on focuses over the last DAYS: work, wait, silent")
    a = ap.parse_args()
    g = goals()
    if a.rounds is not None:
        ths = [r["thread"] for r in g["rows"] if r["kind"] == "focus" or r.get("ladder")]; res = rounds(jsonl(BOARD), ths, (now() - dt.timedelta(days=a.rounds)).isoformat(timespec="seconds"))
        tot = [sum(v[i] for v in res.values()) for i in range(3)]; n = sum(tot)
        for th, v in res.items(): print(f"{th}: {v[0]} work, {v[1]} wait, {v[2]} silent")
        print(f"all: {tot[0]} of {n} rounds did work ({100 * tot[0] // n if n else 0}%), {tot[1]} wait, {tot[2]} silent, over {a.rounds:g} days"); return
    if a.ladder_threads:
        for r in g["rows"]:
            if r["idle"] and (r["kind"] == "focus" or r.get("ladder") or r.get("needs_ladder")): print(r["thread"])
        return
    if a.brief or a.post:
        th = a.post or a.brief
        r = next((r for r in g["rows"] if r["thread"] == th and (r["kind"] == "focus" or r.get("ladder"))), None)
        if not r: sys.exit(f"no focus or ladder in thread {th}")
        b = brief(r)
        if not a.post: print(b); return
        if r.get("parked") and not a.force:   # a keeper hand-picking threads posted rounds into a parked ladder every 4 h (fund-rsi, 2026-11-08)
            sys.exit(f"{th} is parked until {r['parked']['until']}: {r['parked']['why'][:120]}\nNo round posted; --force posts one anyway.")
        cmd = ["hive", "post", "--thread", th] + post_model_args(b, a.model)
        sys.exit(subprocess.run(cmd, input=b, text=True).returncode)
    if a.idle: g["rows"] = [r for r in g["rows"] if r["idle"]]
    print(json.dumps(g, indent=1, default=str) if a.json else render(g))

if __name__ == "__main__": main()
