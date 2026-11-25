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
import argparse, datetime as dt, importlib.util, json, re, subprocess, sys, time, zlib
from collections import Counter
from pathlib import Path

HOME = Path.home()
CORE, PROJECTS, KB, RUN = HOME / "core", HOME / "projects", HOME / "kb", HOME / "run"
BOARD = HOME / "board.jsonl"
OUTCOMES = HOME / "outcomes.jsonl"   # hive outcomes' grade of each run: the brief tells a woken worker when its last round in the thread was graded nothing
FOCUS = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`", re.M)
MEASURE = re.compile(r"^.*\b(measure|metric|score|yardstick|benchmark|pass rate|p&l|sharpe)\b.*$", re.I | re.M)   # the same words progress.py counts as a measure
THREAD = re.compile(r"\bthreads?\s+`?([0-9a-f]{12}|[a-z][a-z0-9-]{2,})`?", re.I)
NOT_AGENTS = {"person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer", "postland", "leftover-router"}
IDLE_D = 2.0   # a project goal with no agent post in its thread for this long and nobody running in it is idle
FOCUS_IDLE_H = 2.0   # a focus of the person's goes idle much sooner: it is their current work, and a focus is never held for an idle owner (2026-11-05, post 1104224972e6)

LADDER = re.compile(r"^##\s+Ladder\b(.*)$", re.M)
LADDER_ANY = re.compile(r"^##\s+(.*\bladder\b.*)$", re.M | re.I)   # a topic ladder of its own: `## Reflection ladder (thread meta-reflect)` moves at a focus's pace in that thread
RUNG = re.compile(r"^\s*-\s+\[([ x!])\]\s+(.+)$", re.M)
AFTER = re.compile(r"\bafter:\s*(\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2})?)Z?\b")   # a day, or a UTC minute (after:2026-11-08T09:50Z) for a rung that waits on a timed run; a space after the colon gates too (kb-search's "after: 2026-11-26" came up 7 days early, a518b 2026-11-19)
GATE = re.compile(r"\b(?:on or after|not before|after|on|from|until|revisits?|re-?recheck|re-?check|recheck|read(?:ing)?|judge)\b[^.;:()]{0,24}?\b(\d{4}-\d{2}-\d{2})\b", re.I)   # the same wait in words: 38 of 47 floor rounds that did nothing (2026-11-09/10) woke on a reading written with a date but no after:
CHECK_ON = re.compile(r"check:\s*(?:on or after|not before|on|after|from)\s+(\d{4}-\d{2}-\d{2})\b", re.I)   # a check that opens with its date ("check: on 2026-11-18 ...") can't pass before it; other dates in a check don't gate (rsi-lab 11-11: 3 such rungs woke the keeper every 2 h)
PARKED = re.compile(r"^Parked until (\d{4}-\d{2}-\d{2}):\s*(.+)$", re.M)
FINISHED = re.compile(r"^Finished:\s*(.+)$", re.M)   # `Finished: <the person's post id>: why`; the keeper and the floor skip the ladder for good (friction D1)
POST_ID = re.compile(r"\b[0-9a-f]{12}\b")
PARK_MAX_D = 3   # a parked ladder comes back within this many days; a date further out doesn't count, so nobody parks a focus for good
WAIT = re.compile(r"no step left|changed nothing|nothing (has )?changed|nothing left|all with the person|no new person posts|started no round|(?<!nothing )\bwaits? (only )?on (the person|a sign-in)", re.I)   # how a round says it did no work
ROUND = re.compile(r"\bkeeper round\b", re.I)
BUILD = re.compile(r"\b(built|landed|committed|shipped|implemented|prototype|first version)\b", re.I)   # a post that says something was built
BUILD_D = 1.0   # a project with no ladder and a build post this recent needs one now (Nov 6 build rule), not after IDLE_D
BRIEF = re.compile(r"^@\S+ keeper round\b", re.I)   # a round's brief: to @new, or to the thread's last worker
ASK = "Every open rung on this ladder waits on a date"   # how the brief for a ladder with only dated rungs opens (brief()); thread_state reads it back from the board as asked_at
ASK_AHEAD_H = 6    # no ask when the first dated rung opens within this many hours: that rung is the next round's work
ASK_EVERY_H = 24   # one ask a day for a ladder: a round that weighed it and found nothing is not asked again every hour

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
    due = [r for r in rungs if r["state"] == " " and r["checked"] and (not r["after"] or r["after"] <= (today if len(r["after"]) == 10 else t.strftime("%Y-%m-%dT%H:%M")))]
    nxt = due[0] if due else None
    # every park line counts, not just the first: a lapsed line above a fresh one left fellows-prep unparked and woke a1f59 three times in 90 min on 11-22 (ad388)
    live = [p for p in PARKED.finditer(body) if today < p.group(1) <= (t.date() + dt.timedelta(days=PARK_MAX_D)).isoformat()]
    p = max(live, key=lambda m: m.group(1), default=None); parked = {"until": p.group(1), "why": p.group(2).strip()} if p else None
    fin = FINISHED.search(body); finished = {"why": fin.group(1).strip(), "posts": POST_ID.findall(fin.group(1))} if fin else None
    return {"thread": th.group(1).lower() if th else None, "rungs": rungs, "next": nxt, "parked": parked, "finished": finished, "done": sum(r["state"] == "x" for r in rungs),
            "due_more": [r["text"] for r in due[1:]], "blocked": [r["text"] for r in rungs if r["state"] == "!"][:5], "left": sum(r["state"] == " " for r in rungs), "no_check": sum(r["state"] == " " and not r["checked"] for r in rungs)}

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

def recent_round(thread, secs=300, board=None):
    """The id of a `Keeper round on` brief posted in `thread` within `secs`, else None. Read from the board's tail just before a
    round is posted: the starter's floor and a keeper's `hive goals --post` each read the board, then post, and twice-started rounds
    followed (hive-stars 2026-11-23 14:37:34 and :36, ae0ba and a8958 on one brief)."""
    path = Path(board or BOARD); cut = dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=secs)
    try:
        with path.open("rb") as f:
            f.seek(0, 2); n = f.tell(); f.seek(max(0, n - 400_000)); tail = f.read().decode("utf-8", "replace").splitlines()[1 if n > 400_000 else 0:]
    except OSError: return None
    for line in reversed(tail):
        try: p = json.loads(line)
        except ValueError: continue
        try: at = dt.datetime.fromisoformat(p.get("at", ""))
        except ValueError: continue
        if at.tzinfo is None: at = at.replace(tzinfo=dt.timezone.utc)
        if at < cut: break
        if p.get("thread") == thread and re.match(r"@\S+ Keeper round on ", p.get("text") or ""): return p.get("id")
    return None

def live_runs():
    out = []
    for p in RUN.glob("*.json"):
        try: out.append(json.loads(p.read_text()))
        except (OSError, ValueError): pass
    return out

def focuses(text):
    """The numbered **title** (thread `x`) items of the newest block in priorities.md that has them."""
    return [{"n": int(n), "title": t.strip(), "thread": th} for n, t, th in FOCUS.findall(text)]

DONE_FILE = "ladder-done.md"   # ticked rungs moved out of a long goals.md (rsi-loop, 2026-11-17: 149 ticked rungs were 78 KB every keeper round read)

def archived_done(g):
    """Ticked rungs kept in ladder-done.md beside this goals.md: they still count as done."""
    try: return sum(l.startswith("- [x]") for l in (g.parent / DONE_FILE).read_text(errors="replace").splitlines())
    except OSError: return 0

CONTINUE = re.compile(r"^Continue:[ \t]*(on|off)\b[^\w\n]*(.*)$", re.M | re.I)

def continue_state(txt):
    """Whether a goals.md's `## Ladder` carries a `Next:` on (continuation.enabled reads the same line): "on", "off: reason", "off" with no
    reason, or None when the section has no Continue line or there is no `## Ladder`. hive-meta Moving ladder, ad388 2026-11-19: the 11-06
    widening put `Continue: on` in every focus ladder, but hive-app's came later without it, so 20 of its Next: lines in two days were dropped
    as "off" and each delivered step waited ~30 min for a floor start. `hive goals` now names a focus ladder with neither line."""
    m = LADDER.search(txt)
    if not m: return None
    rest = txt[m.end():]; end = re.search(r"^#{1,2}\s", rest, re.M); sec = rest[:end.start()] if end else rest
    c = CONTINUE.search(sec)
    if not c: return None
    if c.group(1).lower() == "on": return "on"
    why = c.group(2).strip().strip("()").strip()
    return f"off: {why}" if len(why) >= 8 else "off"

def continue_missing(projects, focus_of):
    """[(project, thread, focus)] for each project whose `## Ladder` has open rungs and runs in a thread under a focus, with neither
    `Continue: on` nor `Continue: off (reason)`. focus_of(thread) -> focus id or None."""
    out = []
    for g in projects:
        lad = g.get("ladder")
        if not lad or not lad.get("left") or str(g.get("continue") or "").startswith(("on", "off:")): continue
        for th in [x for x in (lad.get("thread"), g["project"]) if x]:
            fid = focus_of(th)
            if fid: out.append((g["project"], th, fid)); break
    return out

def project_goals():
    out = []
    for g in sorted(PROJECTS.glob("*/goals.md")):   # lowercase only: the GOALS.md of cloned repos (ece298a-pll, resume) are their own files, not Hive goals
        if g.parent.name.startswith("_"): continue
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        head = next((l.lstrip("# ").strip() for l in txt.splitlines() if l.strip()), g.parent.name)
        m = MEASURE.search(txt)
        measure = re.sub(r"^\W*(measure|metric)\W*", "", m.group(0).strip(), flags=re.I)[:160] if m else None
        lad = ladder(txt)
        if lad: lad["done"] += archived_done(g)
        out.append({"project": g.parent.name, "goal": head[:120], "measure": measure, "threads": [t.lower() for t in THREAD.findall(txt) if t.lower() != "thread"][:8],
                    "edited": dt.datetime.fromtimestamp(g.stat().st_mtime, dt.timezone.utc).isoformat(timespec="seconds"), "ladder": lad, "ladders": ladders(txt), "path": str(g), "continue": continue_state(txt)})
    return out

HASH_NAME = re.compile(r"[a-z][0-9a-f]{4,}")   # as in hive: what new/claude/codex/warden hand out; anything else is a named identity

def homes(board):
    """A named identity (nightly, remind, weekly-fresh) runs a lane of its own, often from a timer script: its home is the thread that
    holds over half its posts. Elsewhere its posts are reports, not rounds, so they don't make it the thread's worker. 11-10..17: the
    dash gate posts as `nightly` into rsi-loop, so 18 rsi-loop briefs woke the product-test agent and 2 were delivered (meta-moving, ad388)."""
    c = {}
    for p in board:
        f = str(p.get("from", "")).lower()
        if f and f not in NOT_AGENTS and not HASH_NAME.fullmatch(f): c.setdefault(f, Counter())[p.get("thread") or p.get("id")] += 1
    return {f: th for f, n in c.items() for th, k in n.most_common(1) if k * 2 > sum(n.values())}

def started(log=None):
    """Names the starter has ever started (a `start` event in ~/log.jsonl): the only ones a mention can wake (starter.known_agents). A named identity that
    never ran is a script's posting name. 11-17/18: `weekly-fresh` posted once in hive-stars and became its worker, so 14 keeper briefs went to
    `@weekly-fresh`, were logged unknown_mention and started nobody; the focus sat 39 h with a rung due (ae49e, thread 11181219a2f8)."""
    out = set()
    try:
        with open(log or LOG, "rb") as f:
            for line in f:
                if b'"start"' not in line: continue
                try: out.add(str(json.loads(line)["name"]).lower())
                except (ValueError, KeyError, TypeError): pass
    except OSError: pass
    return out

def thread_state(board, runs, t, ran=None):
    """Per thread: last agent post (at, from), last person post, running agents. `ran`: the names that have ever run (started(), read when not given)."""
    st = {}; home = homes(board); ran = started() if ran is None else ran
    for p in board:
        th = p.get("thread") or p.get("id"); s = st.setdefault(th, {"agent_at": "", "agent": "", "person_at": "", "posts": 0, "running": set()})
        s["posts"] += 1
        f = str(p.get("from", "")).lower()
        if ASK in (p.get("text") or "") and BRIEF.match(p["text"]): s["asked_at"] = max(s.get("asked_at", ""), p.get("at", ""))   # the floor posts its briefs as `starter`, a keeper as itself: either counts
        if f == "person": s["person_at"] = max(s["person_at"], p.get("at", ""))
        elif f not in NOT_AGENTS and p.get("at", "") >= s["agent_at"]:
            s["agent_at"], s["agent"] = p.get("at", ""), p.get("from")
            if not BRIEF.search(p.get("text", "")) and (HASH_NAME.fullmatch(f) or (home.get(f) == th and f in ran)): s["worker"] = p.get("from")   # a brief has to reach someone: a name that never ran can't be woken
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
        rows.append(with_ladder(row, g, t))
    taken = {g["project"] for g in claimed.values()}
    for g in projects:
        for lad in g.get("ladders") or []:   # a topic ladder of its own (hive-meta's Reflection ladder, thread meta-reflect): a goal at a focus's pace in that thread
            if lad["thread"] in fth or any(r["thread"] == lad["thread"] for r in rows): continue
            s = dict(st.get(lad["thread"], {})); rows.append(with_ladder({"kind": "project", "name": f"{g['project']}: {lad['section']}", "goal": g["goal"], "thread": lad["thread"], "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, lad["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)}, {"ladder": lad, "path": g["path"]}, t))
        lad = g["ladder"] if g["project"] not in taken else None   # a project with a ladder no focus takes moves at a focus's pace, in the thread its heading names
        if lad and lad["thread"]:
            s = dict(st.get(lad["thread"], {})); rows.append(with_ladder({"kind": "project", "name": g["project"], "goal": g["goal"], "thread": lad["thread"], "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, lad["thread"], open_asks, t, idle_d=FOCUS_IDLE_H / 24)}, g, t)); continue
        th = g["project"]; cands = [th] + [k for k in g["threads"] if k != th] + [k for k in st if k != th and k not in g["threads"] and (k.startswith(th + "-") or k.startswith(th + "_"))]
        best = max(cands, key=lambda k: st.get(k, {}).get("agent_at", ""))
        s = dict(st.get(best, {})); s["running"] = set().union(*(st.get(k, {}).get("running", set()) for k in cands))
        row = {"kind": "project", "name": g["project"], "goal": g["goal"], "thread": best, "measure": g["measure"], "edited_d": days(g["edited"], t), **state_row(s, cands, open_asks, t)}
        built = [d for d in (days(st.get(k, {}).get("build_at"), t) for k in cands) if d is not None]
        # "no ladder" has to be checked, not assumed: a ladder a focus row carries, one of its own with rungs left, or topic ladders all count (ae49e, 2026-11-18:
        # five projects with a ladder were listed to the keeper as needing one, and hive-app's six open rungs dropped off its row for a day after every build post)
        has_ladder = g["project"] in taken or (g["ladder"] and g["ladder"]["left"]) or g.get("ladders")
        if not row["running"] and built and min(built) <= BUILD_D and not has_ladder:   # built within a day and no ladder: idle now, and the round writes it a ladder
            row.update(idle=True, needs_ladder=True, ladder=False, goals_path=g["path"], rung=None, last_agent_d=min(built))
        if g["ladder"] and g["ladder"]["left"] and g["project"] not in taken and not row.get("needs_ladder"):
            # a `## Ladder` whose heading names no thread lives in the project's own thread: without this the row had no ladder, so the floor
            # never started it (the voice board and its film sat unstarted after the person's resume, 2026-11-12, post 11121012cbf5)
            with_ladder(row, g, t)
        rows.append(row)
    try:   # one focus state for every lookup; a broken focus store leaves the list empty rather than failing hive goals
        sys.path.insert(0, str(CORE)); import focus as _F; _fst = _F.state(); _fof = lambda th: _F.focus_of(_fst, th)
    except Exception: _fof = lambda th: None
    nocont = continue_missing(projects, _fof); nopaths = {str(PROJECTS / p / "goals.md") for p, _, _ in nocont}
    for r in rows:
        if r.get("goals_path") and str(r["goals_path"]) in nopaths: r["no_continue"] = True
    covered = {r["thread"] for r in rows} | {th for r in rows for th in (r.get("threads") or [])}
    loose = [e for e in open_asks if e.get("thread") not in covered]
    return {"at": t.isoformat(timespec="seconds"), "rows": rows, "directions": len(directions), "open_asks": len(open_asks), "loose_asks": len(loose), "no_continue": [{"project": p, "thread": th, "focus": f} for p, th, f in nocont],
            "loose_threads": sorted({e.get("thread") for e in loose}, key=lambda x: str(x))}

PILE = 8   # card lint flags that make a card's own cleanup a round's first job (card_flags)
PILE_EVERY_H = 6

def card_pile_due(row, t):
    """HA-4 (2026-11-19): hive-meta and rsi-loop held 35 passed-date flags because every open rung was dated, so no keeper round was ever dispatched
    to read the brief that lists them. A gated ladder whose card carries PILE or more flags gets a round every PILE_EVERY_H hours, not one a day."""
    try: last = dt.datetime.fromisoformat(row.get("asked_at") or "")
    except ValueError: last = None
    if last and (t - (last if last.tzinfo else last.replace(tzinfo=dt.timezone.utc))).total_seconds() < PILE_EVERY_H * 3600: return False
    return len(card_mine(row.get("thread"))[1]) >= PILE

def ask_due(row, t, every_h=ASK_EVERY_H):
    """True when a ladder whose open rungs all wait on a date is owed its daily ask: the first of them opens ASK_AHEAD_H or more hours from now and no ask was posted in its thread in the last ASK_EVERY_H hours.
    every_h: the floor asks sooner while the person is away and it runs under agents_floor (starter floor_ask_away_h)."""
    try: gate = dt.datetime.fromisoformat(row["gated_until"]).replace(tzinfo=dt.timezone.utc)
    except ValueError: return False
    if (gate - t).total_seconds() < ASK_AHEAD_H * 3600: return False
    try: last = dt.datetime.fromisoformat(row.get("asked_at") or "")
    except ValueError: return True
    return (t - (last if last.tzinfo else last.replace(tzinfo=dt.timezone.utc))).total_seconds() >= every_h * 3600

_PERSON_POSTS = {}

def person_said(ids, board=None):
    """The first of these post ids that the board holds as a post from the person, or None. Only the person's word finishes a ladder:
    an agent that could write `Finished:` alone could retire a focus for good, which PARK_MAX_D exists to stop."""
    ids = [i for i in ids if i]
    if not ids: return None
    key = (tuple(ids), str(board or BOARD))
    try: mt = Path(board or BOARD).stat().st_mtime
    except OSError: return None
    if key in _PERSON_POSTS and _PERSON_POSTS[key][0] == mt: return _PERSON_POSTS[key][1]
    found, want = None, set(ids)
    try:
        with open(board or BOARD, encoding="utf-8", errors="replace") as f:
            for line in f:
                if '"person"' not in line or not any(i in line for i in want): continue
                try: p = json.loads(line)
                except ValueError: continue
                if p.get("id") in want and p.get("from") == "person": found = p["id"]; break
    except OSError: pass
    _PERSON_POSTS[key] = (mt, found)
    return found

def with_ladder(row, g, t=None):
    lad = g["ladder"] if g else None
    row["ladder"] = bool(lad); row["goals_path"] = g["path"] if g else None
    # friction D1 (abundant-take-home, 11-24): a project the person called finished still got "write the next 3 to 5 rungs" rounds, and
    # `Continue: off` only stops Next: chaining. A `Finished: <person's post id>: why` line takes the ladder out like a park with no end date.
    if lad and lad.get("finished") and (pid := person_said(lad["finished"]["posts"])):
        row.update(rung=None, due_more=[], blocked=[], rungs_left=lad["left"], rungs_done=lad["done"], rungs_no_check=lad["no_check"], idle=False,
                   finished={"post": pid, "why": lad["finished"]["why"]}, parked={"until": "finished", "why": f"the person called it finished ({pid}): {lad['finished']['why']}"},
                   **({"section": lad["section"]} if lad.get("section") else {}))
        return row
    if lad:
        row.update(rung=lad["next"]["text"] if lad["next"] else None, due_more=lad.get("due_more", []), blocked=lad.get("blocked", []), rungs_left=lad["left"], rungs_done=lad["done"], rungs_no_check=lad["no_check"], parked=lad["parked"], **({"section": lad["section"]} if lad.get("section") else {}))
        if lad["parked"]: row["idle"] = False
        # every open rung carries a future after: gate and none is open now: a round could only write more rungs, so hold the keeper until the earliest gate (ae5ec, 2026-11-15: 18 idle rounds a day on improve-survey)
        open_ = [r for r in lad["rungs"] if r["state"] == " "]
        if not lad["next"] and open_ and all(r["after"] and r["checked"] for r in open_):
            row["gated_until"] = min(r["after"] for r in open_)  # UTC "YYYY-MM-DD[THH:MM]"; the floor starts nobody before it (ae5ec 11-16: a Waiting: cap of 2 h let a keeper in every 2 h on a ladder gated 11 h)
            # a date is when a reading can be taken, not a reason for the goal to stand still until then: once a day such a ladder gets one round that is asked what else
            # would move its measure (ae49e, 2026-11-18: 17 of 28 ladders with an open rung had only dated ones, and nothing in the hive asked any of them for more)
            if not lad["parked"] and (ask_due(row, t or now()) or card_pile_due(row, t or now())): row["ask_more"] = True
            else: row["idle"] = False
    return row

LOG = HOME / "log.jsonl"
CAP = HOME / ".hive" / "resume_cap.json"   # L134 (a5234, 2026-11-16): {"name", "since", "until", "max_context"}; absent or past `until`: no cap

def context_of(out):
    """Tokens in the context at a run's last main turn, from its stream-json `out` (as starter.context_of). 0 if unknown (codex and kimi don't print it)."""
    n = 0
    try:
        with open(out, errors="replace") as f:
            for line in f:
                if '"assistant"' not in line or '"usage"' not in line: continue
                try: e = json.loads(line)
                except ValueError: continue
                if e.get("type") == "assistant" and not e.get("parent_tool_use_id") and (u := (e.get("message") or {}).get("usage")):
                    n = sum(u.get(k) or 0 for k in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"))
    except (OSError, TypeError): pass
    return n

def last_context(worker, log=None, tail=3_000_000):
    """Context at the last turn of `worker`'s newest run (its newest start event in log.jsonl), 0 if none is found."""
    try:
        with open(log or LOG, "rb") as f:
            f.seek(0, 2); f.seek(max(0, f.tell() - tail)); lines = f.read().decode(errors="replace").splitlines()
    except OSError: return 0
    for l in reversed(lines):
        if '"start"' not in l or f'"name": "{worker}"' not in l: continue
        try: e = json.loads(l)
        except ValueError: continue
        if e.get("event") == "start" and e.get("name") == worker: return context_of(e.get("out"))
    return 0

def capped(thread, worker, now=None, cap=None, ctx=None, prev=None):
    """L134 A/B: the context `worker` would carry into a resumed round if the cap sends this round to a fresh agent instead, else 0.
    Only threads with crc32 odd are capped; the even half resumes as before, so the arms run side by side (read by keeper_cost.py, L134b)."""
    if not thread or not worker or worker == "new": return 0
    if cap is None:
        try: cap = json.loads(CAP.read_text())
        except (OSError, ValueError): return 0
    now = now or dt.datetime.now(dt.timezone.utc)
    try:
        if not (dt.datetime.fromisoformat(cap["since"]) <= now < dt.datetime.fromisoformat(cap["until"])): return 0
    except (KeyError, TypeError, ValueError): return 0
    if zlib.crc32(thread.encode()) % 2 != 1: return 0
    if cap.get("after_delivered"):   # L134d (ad467, 2026-11-19): rc-1119 sends a round fresh only when the worker's last graded round here was delivered; unknown or any other grade resumes
        r = last_graded(worker, thread) if prev is None else prev
        if (r.get("grade") if isinstance(r, dict) else r) != "delivered": return 0
    n = last_context(worker) if ctx is None else ctx
    return n if n and n >= int(cap.get("max_context", 50_000)) else 0

def resume_arm(thread, worker=None, now=None, cap=None, ctx=None, prev=None):
    """Keeper briefs wake the thread's last worker, who keeps what it learned there (the starter gives an heir to one that is done or over
    resume_max_context). The 11-06..11-09 A/B (hive-meta/continuity.md "A/B result") found resume 52% delivered vs fresh 32%, no kill, so it is the default.
    L133 (hive-meta load.md) priced that A/B: per delivered round a fresh agent cost 0.60 of a resumed one starting at 50 k or more (95% 0.34..1.16),
    so the L134 A/B (`capped`, ~/.hive/resume_cap.json) sends odd-hashed threads to @new when the worker carries max_context or more."""
    return not capped(thread, worker, now, cap, ctx, prev)

def post_model_args(text, model):
    """A brief's model is for a fresh agent only: on a woken worker the starter would hold it from then on, undoing a grader step-up or
    the worker's own `hive model` (review 110618506919)."""
    return ["--model", model] if model and text.startswith("@new ") else []

NO_RUNG_MODEL, NO_RUNG_FROM = "opus", ("sonnet", "haiku")
def no_rung_model(row, text, model, chosen=None, sets=None):
    """A keeper round with no rung to take asks for judgement (what else would move the Measure), not a step to carry out, and over
    11-15..11-22 Sonnet delivered 45 of 59 such rounds (76%) to Opus's 29 of 31 (94%); 14 of the 17 misses were Sonnet (hive-meta R59,
    misses.py --days 7). The step-up never caught them: outcomes.step_up skips keeper rounds. So a no-rung brief to a fresh agent asks
    for NO_RUNG_MODEL, and one to a woken worker held on a cheaper model writes the model-set file the grader's step-up writes (the
    starter holds it from the next run; the worker steps down with `hive model sonnet --next`). A worker on Opus, Fable or a provider
    model is left alone. Returns (model for post_model_args, model-set path written or None)."""
    if row.get("rung"): return model, None
    if text.startswith("@new "): return NO_RUNG_MODEL, None
    name = text.split()[0].lstrip("@") if text.startswith("@") else ""
    if not re.fullmatch(r"[a-z][0-9a-f]{4,}", name): return model, None
    try: held = (chosen if chosen is not None else json.loads((HOME / ".hive" / "starter.json").read_text()).get("chosen", {})).get(name) or {}
    except (OSError, ValueError): return model, None
    if held.get("model") not in NO_RUNG_FROM or held.get("free"): return model, None
    f = Path(sets or HOME / ".hive" / "model-set") / f"{name}.json"
    if f.exists(): return model, None
    f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(json.dumps({"model": NO_RUNG_MODEL, "why": f"goals: keeper round on {row.get('thread')} with no rung to take, on {held['model']} (hive-meta R59)"}))
    return model, f

def graded(worker, thread, n=1, path=None, tail=600_000):
    """The newest `n` runs of `worker` in `thread` that hive outcomes graded on its work (delivered, partial, nothing, silent), newest first.
    Reads only the file's tail: the brief wants the last rounds, not the history."""
    path = path or OUTCOMES; out = []
    try:
        with open(path, "rb") as f:
            f.seek(0, 2); f.seek(max(0, f.tell() - tail)); lines = f.read().decode(errors="replace").splitlines()
    except OSError: return out
    for l in reversed(lines):
        try: r = json.loads(l)
        except ValueError: continue
        if r.get("name") == worker and r.get("thread") == thread and r.get("grade") in ("delivered", "partial", "nothing", "silent"):
            out.append(r)
            if len(out) >= n: break
    return out

def last_graded(worker, thread, path=None, tail=600_000):
    """The newest graded run of `worker` in `thread`, or None."""
    r = graded(worker, thread, 1, path, tail); return r[0] if r else None

STREAK_NEW = 2   # R45 (hive-meta continuity.md R45): after this many nothing rounds in a row in a thread, the round goes to @new. 11-12..15: a worker
                 # re-woken after a nothing there repeated it 4 of 11 times (all runs: 3 of 111); the R34 line did not stop a65da (07:12, 09:08 11-15)

def stuck(worker, thread):
    """True when `worker`'s last STREAK_NEW graded rounds in `thread` were all nothing: the next round goes to a fresh agent."""
    if not worker or worker == "new" or not thread: return False
    r = graded(worker, thread, STREAK_NEW)
    return len(r) == STREAK_NEW and all(x.get("grade") == "nothing" for x in r)

def last_nothing(to, thread):
    """A line for a worker woken back on a thread where its last graded round was `nothing` (R34, hive-meta continuity.md R33): in the 11-13/14
    window keeper rounds on Sonnet delivered 57%, and 7 of the 8 wait misses were the same two workers re-reading a rung whose after: date had passed
    but which could not pass yet, posting 'nothing due' every two hours. Nothing told them the last one had counted as nothing."""
    r = last_graded(to, thread) if to != "new" and thread else None
    if not r or r.get("grade") != "nothing": return ""
    why = re.sub(r"\s+", " ", r.get("why") or "").strip()[:200]
    return (f"\n\n**Your last round here ({(r.get('started') or '')[11:16]} UTC) was graded nothing**" + (f": {why}" if why else ".") +
            " A wait post is not a result. If the rung cannot pass yet, move its `after:` tag to the time it can, in this run, then write a rung that moves the measure today and do it.")

CARD_KINDS = ("past date", "to-do off the list", "says today", "hidden ask", "long bullet", "long lead")   # the lint findings a ladder agent can clear in a minute

_FSTATE = {"at": 0.0, "st": None}

def _focus_id(thread):
    """The focus a thread is in, by tag first (focus.focus_of). HA-4 2026-11-19: matching only a focus's own thread or id left hive-meta's ladder
    threads (meta-load, meta-reflect ...) with no focus, so their card flags never reached a brief and card_pile_due never fired."""
    try:
        sys.path.insert(0, str(Path(__file__).parent)); import focus
        if _FSTATE["st"] is None or time.time() - _FSTATE["at"] > LINT_TTL_S: _FSTATE.update(at=time.time(), st=focus.state())   # 1.3 s a build, once per ladder per starter pass before (112210364131)
        return focus.focus_of(_FSTATE["st"], thread)
    except Exception: return None

_LINT = {"at": 0.0, "items": None}
LINT_TTL_S = 300

def _lint_items():
    """`hive focus lint --json`, run at most once per LINT_TTL_S. The starter's floor calls card_mine for every gated ladder on each 3 s pass;
    uncached, ~30 lint runs of 1.5 s each made a pass take ~50 s, and a person's post to a sleeping agent waited for it (the person, 112210364131).
    Card flags change on a scale of hours (a round every PILE_EVERY_H), so five minutes old is fresh enough."""
    if _LINT["items"] is None or time.time() - _LINT["at"] > LINT_TTL_S:
        items = json.loads(subprocess.run(["hive", "focus", "lint", "--json"], capture_output=True, text=True, timeout=30).stdout)
        _LINT.update(at=time.time(), items=items)
    return _LINT["items"]

def card_mine(thread, run=None):
    """(focus id, the card's own clearable lint findings) for a thread; (None, []) when lint cannot run."""
    try:
        items = json.loads(run()) if run else _lint_items()
        fid = thread if run else _focus_id(thread)
    except Exception: return None, []
    return fid, [i for i in items if i.get("focus") == fid and i.get("kind") in CARD_KINDS]

def card_flags(thread, run=None):
    """The brief's line on the focus card's own lint (HA-4, 2026-11-19): `hive focus set` prints the flags only to the agent that saves a
    summary, and `check add` prints none, so 25 of 29 passed dates sat on to-dos that nobody who owned the ladder was ever shown (hive-meta 13,
    rsi-loop 12 of 52 findings). A keeper round on the focus now carries them. Empty when the card is clean or lint cannot run."""
    fid, mine = card_mine(thread, run)
    if not mine: return ""
    n = 12 if len(mine) >= 8 else 6
    lines = "\n".join(f"- {i['kind']}: {i['what']} (in: {(i.get('in') or '')[:60]}) → {i['fix'][:110]}" for i in mine[:n])
    more = (f"\n- and {len(mine) - n} more (`hive focus lint {fid}` lists all)" if len(mine) > n else "")
    if len(mine) >= 8:
        # 2026-11-19 (HA-4 read, 52 flags left a day after the brief carried them): rounds took their rung and left a "before or after" line; a pile this size is the first job.
        return (f"\n\n**First job this run, before your rung: the focus card carries {len(mine)} lint flags** (`hive focus lint {fid}`; over 7 means earlier rounds skipped them). "
                f"Go through them: tick a to-do that happened (`hive focus check done ID`), re-date one still ahead, or say what happened on a passed day. Finish with the count under 5 and say the before and after numbers in your post:\n" + lines + more)
    return (f"\n\n**The focus card's own lint flags {len(mine)} line{'s' if len(mine) != 1 else ''} on your focus** (`hive focus lint {fid}`); you own this ladder, so clear them in this run "
            f"(tick a to-do that happened, re-date one still ahead, say what happened on a passed day), before or after your rung:\n" + lines + more)

def brief(row):
    """The brief a keeper round posts for one focus. Every clause is here because a thin hand-written brief produced the 'nothing changed' rounds."""
    big = capped(row.get("thread"), row.get("last_worker")) if row.get("last_worker") else 0
    to = row["last_worker"] if row.get("last_worker") and not big else "new"
    prev = to if to != "new" and stuck(to, row.get("thread")) else None
    if prev: to = "new"
    head = f"@{to} Keeper round on {row['name']}. The ladder is the `## {row.get('section') or 'Ladder'}` section of {row.get('goals_path') or 'this focus project goals.md (create it, one line a rung)'}: read that whole file, and obey any notes in the section.\n\n"
    rung = f"**Your rung:** {row['rung']}\n\nDo this rung. Land real work, then run its check." if row.get("rung") else "**The ladder has no rung you can take.** Your work this round is to write the next 3 to 5 rungs from the measure in that file, then do the first one. Each rung is a line `- [ ] what. check: how to tell`; a numbered list is not read, so convert one that is there. First read the thread: if the person has said this project is finished or told the keeper to stop on it, write no rungs; add the line `Finished: <their post id>: their reason` under the section heading (the keeper then skips it for good; delete the line if they ask for more) and end with `Done:`."
    if row.get("ask_more") and not row.get("rung"):
        rung = (f"**The ladder has no rung you can take.** {ASK}; the first opens {row['gated_until'].replace('T', ' ')} UTC. A date is when a reading can be taken, not a reason for the goal to stand still until then. "
                "Your work this round is to ask what else would move the Measure in that file before then (a cause nobody has tried, a check that can run on what exists now, a part of the goal the dated rungs leave out), "
                "write the next 3 to 5 rungs from that, each with a `check:`, and do the first one. If you weigh real candidates and none would move it, write one line under the rungs, in place of any older one, "
                "`Weighed YYYY-MM-DD: what you considered and why each would not move the Measure`, so tomorrow's round starts from there. \"The rungs are dated\" is not a finding.")
        # a `[!]` whose block lifted is a rung nobody is shown: 2 of the focus lanes' 24 `[!]` rungs were stale on 11-24 (fund-rsi sealed rows, its nightly back on; ui-redesign `hive me` count, blocked only by a past date) (ad388, meta-moving)
        if row.get("blocked"):
            rung += (f"\n\n**Blocked rungs on this ladder ({len(row['blocked'])}):** " + "; ".join('"' + re.sub(r"\s+", " ", x.replace("`", ""))[:100] + '"' for x in row["blocked"][:3]) +
                     ". Check whether each block still holds (a date passed, a job back on, a fix landed); reopen any that no longer does as `- [ ]` and take it before writing new rungs.")
    more = row.get("due_more") or []   # hive-meta Moving ladder, ad388 11-17: rounds took one rung and ended `Waiting:` while more were due (rsi-loop a0efb 10:04, 4 due since 08:00Z; rsi-lab a12a7 13:04, 2 due since 12:00Z): Goal 1 ran 7.1% of the day
    if more and row.get("rung"):
        rung += (f"\n\n**Also due now on this ladder ({len(more)} more):** " + "; ".join('"' + re.sub(r"\s+", " ", x.replace("`", ""))[:110] + '"' for x in more[:3]) +
                 ". When your rung is done, marked `[!]` or re-dated, take the next due one in this run, or end with `Next:` naming it so the starter carries it on. `Waiting:` is only for when no rung is due.")
    fresh = (f"\n\n**You take this fresh:** `{prev}`'s last {STREAK_NEW} rounds here were graded nothing (they posted a wait instead of doing the rung)."
             " Read the thread for context, then do the rung yourself; do not hand it back.") if prev else (
        f"\n\n**You take this fresh:** `{row['last_worker']}` would have carried {big // 1000}k tokens of context into this round (a context-cap trial, hive-meta L134)."
        " Read the thread's last posts and the files they name for what it learned, then do the rung yourself.") if big else ""
    nocont = ("\n\n**This ladder has no `Continue:` line** under its `## Ladder`, so the starter drops every `Next:` here and the next step waits for a floor start."
              " Add `Continue: on` in this run (or `Continue: off (why)` if steps here must not chain).") if row.get("no_continue") else ""
    return head + rung + fresh + last_nothing(to, row.get("thread")) + card_flags(row.get("thread")) + nocont + """

Rules for the round:
- Read the last 5 posts of this thread for context, not for a conclusion. Earlier posts here may say nothing is left: that was a check-in, not a finding. "Nothing changed" is not a result, and you are not here to re-check the state.
- A rung is work that needs nobody's sign-off and moves the measure in goals.md, with a `check:` someone else could run. Hard limits hold (no money, no acting in the person's name, no credentials handed out). Drafting, building, simulating and testing are all open while an account or a go waits.
- Check passes: post your result, then `hive goals --tick POST_ID --rung "words from the rung"` (it checks the id is on the board and writes `[x]` and the id). Too big for one run: split it into rungs, land the first, tick that.
- Rung truly needs the person or is blocked: change it to `- [!]` with why (add a `For you:` line in your post if they must act), then take the next rung in this same run. A `[!]` costs you nothing and is not your result.
- Keep the focus's to-do list, which the person sees and ticks on its page: the next two or three steps in plain words (`hive focus check add FOCUS "text"`), ticked as each lands (`hive focus check done ID`; `hive focus check list FOCUS` shows them).
- A rung that cannot pass before a date or time (a full day's reading, a window that ends later) carries `after:YYYY-MM-DD` or `after:YYYY-MM-DDTHH:MMZ` in its text: the keeper skips it until then. Without the tag it wakes an agent every hour to find nothing to do.
- You landed a fix but its check needs time to show (a later count, a second daily reading): add the fix as its own `- [x]` rung with your post id, and turn the open rung into the reading with an `after:` tag. Left open with "waiting", it wakes a round every hour that can only re-read the same number.
- When what is left waits on the person or a date, ask whether the goal or its Measure is still right; then either write a step that moves the Measure, with a `check:`, or end with the wait.
- Park only when the measure is at target, or every way left to move it needs the person (name which): add a line `Parked until YYYY-MM-DD: why` to the section (at most 3 days out, later dates are ignored) and say so. The keeper skips it until then. Waiting on a date or a later reading is no reason to park: write a rung that moves the measure today, or if the measure has stopped saying anything, rewrite it and keep the old one and the reason beside it.
- Your last post says: the rung, the check's output, the measure now, and ends with the line `Next: <the next rung, with its check>` (the starter carries it on), or a `Blocked:` or `Needs the person:` line if nothing is left you can do, or `Waiting:` naming who or when (`Waiting: a1b2c's review`, `Waiting: the sim, about 04:20 UTC`). The next agent comes within minutes unless your wait names a running agent or a time. If the work turns hard, `hive model opus "why"`.
- When that post is your last act (status bar and focus already set), send it with `hive post --end`: the run stops there with no further model call."""

def state_row(s, threads, open_asks, t, idle_d=IDLE_D):
    threads = [threads] if isinstance(threads, str) else list(threads)
    mine = [e for e in open_asks if e.get("thread") in threads]
    limit, idle_d = idle_d, days(s.get("agent_at"), t)
    running = sorted(s.get("running", set()))
    idle = not running and (idle_d is None or idle_d >= limit)
    at = s.get("agent_at")
    return {"threads": threads, "last_agent": s.get("agent"), "last_worker": s.get("worker"), "last_agent_d": idle_d, "quiet_s": (t - dt.datetime.fromisoformat(at)).total_seconds() if at and idle_d is not None else None, "person_d": days(s.get("person_at"), t), "running": running,
            "open_asks": len(mine), "owned_asks": sum(1 for e in mine if e.get("status") == "owned"), "idle": idle, "asked_at": s.get("asked_at") or None}

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
    if g.get("no_continue"): lines.append("Focus ladders with no `Continue:` line, so every `Next:` in them is dropped and waits for a floor start: " + ", ".join(f"{x['project']} (thread {x['thread']}, focus {x['focus']})" for x in g["no_continue"]) + ". Add `Continue: on` under its `## Ladder`, or `Continue: off (why)`; `hive goals --continue` lists them all.")
    if g["loose_threads"]: lines.append(f"Open asks outside every focus and project: {g['loose_asks']} in {len(g['loose_threads'])} threads (`hive asks list`, `hive asks check`); these are the person's one-off asks and agents' leftovers, moved by the keeper, not by a goal.")
    return "\n".join(lines)

def tick(txt, words, post):
    """Tick the one open or blocked rung whose text holds `words` (any case), appending `(post ID)`. Returns (new text, the rung line) or raises ValueError.
    Rounds ticked rungs by hand and left the id off: 1073 of 1601 ticks across project goals.md files had none (2026-11-11), and the
    rsi-lab bench's s5u replay showed runs that knew their id was wrong stopping anyway. A tool that checks the id makes it a step, not a memory."""
    lines = txt.split("\n"); w = words.lower().strip()
    hits = [i for i, l in enumerate(lines) if re.match(r"\s*-\s+\[[ !]\]\s", l) and w in l.lower()]
    if len(hits) != 1:
        cands = [lines[i].strip()[:100] for i in hits]
        raise ValueError(f"{len(hits)} open rungs hold {words!r}" + ("; say more of it:\n  " + "\n  ".join(cands) if cands else ""))
    i = hits[0]; l = re.sub(r"\[[ !]\]", "[x]", lines[i], count=1).rstrip()
    lines[i] = l + f" (post {post})"
    return "\n".join(lines), lines[i]


PERSON_HOLD = re.compile(r"\b(pause[ds]?|pausing|stop|stopped|halt|hold off|on hold|don'?t (run|start|touch))\b", re.I)
PERSON_GO = re.compile(r"\b(un-?pause[ds]?|resume[ds]?|carry on|go ahead|start again|continue)\b", re.I)

# A question about pausing is not a pause: "Should we pause the voice board? what is most active" held its thread
# (11160857c8f0), as "hello? why is this paused" would have. "Can you pause X?" is still a request, so it holds.
HOLD_QUESTION = re.compile(r"^\W*(?:\w+\W+){0,2}?(?:why|should|shall|is|are|was|were|did|does|what|how|has|have)\b", re.I)

def hold_is_question(text):
    """True when every sentence that names a pause or stop is a question that asks about it rather than asks for it."""
    hits = [s for s in re.split(r"(?<=[.!?\n])\s*", text) if PERSON_HOLD.search(s)]
    return bool(hits) and all((s.rstrip().endswith("?") and HOLD_QUESTION.search(s)) or re.match(r"\W*(?:why|how come)\b", s, re.I) for s in hits)

def person_held_of(threads, board=None):
    """{thread: why} for threads whose newest post from the person reads as a pause or stop ("Pause work please pause it"), or that the
    starter holds as stopped. The starter's stop rule takes only a bare "stop", so the keeper twice dispatched rounds into a paused project thread
    after the person's "pause it" there (11170143212c, 2026-11-21 01:43 and 03:01). A later post of theirs that says resume clears it."""
    want, newest = set(threads), {}
    try:
        with open(board or BOARD, encoding="utf-8", errors="replace") as f:
            for line in f:
                if '"person"' not in line: continue
                try: p = json.loads(line)
                except ValueError: continue
                if p.get("from") != "person": continue
                th = p.get("thread") or p.get("id")
                if th in want: newest[th] = p
    except OSError: pass
    out = {}
    try:
        sys.path.insert(0, str(CORE)); import continuation as _C
        for th in want:
            if _C.still_held(th): out[th] = "stopped by the person (the starter's stop mark)"
    except Exception: pass
    for th, p in newest.items():
        if th in out: continue
        text = re.sub(r"^(?:@[\w-]+\s+)*(?:reply to message [0-9a-f]+:\s*)?", "", str(p.get("text", "")))
        text = re.sub(r"^\((?:from [^,()]+, )?web app[^()]*\)", "", text.strip()).strip()
        try: age_d = (now() - dt.datetime.fromisoformat(str(p.get("at")).replace("Z", "+00:00"))).total_seconds() / 86400
        except (TypeError, ValueError): age_d = 0
        # Only a pause with a short stated deadline lapses by age: a clock time or tomorrow/tonight after 1.5 days, a weekday after 8.
        # "Until I say", conditions, "next month" and undated pauses never expire by age (i0411); "until 3pm"/"until tomorrow" held forever
        # once the same-day-only rule landed (review a6e64, 2026-11-23).
        if age_d > 1.5 and re.search(r"\buntil\s+(?:\d{1,2}(?::\d{2})?\s*(?:am|pm)\b|noon\b|midnight\b|tomorrow\b|tonight\b|this (?:morning|afternoon|evening)\b|later today\b)", text, re.I): continue
        if age_d > 8 and re.search(r"\buntil\s+(?:mon|tues|wednes|thurs|fri|satur|sun)day\b", text, re.I): continue
        if PERSON_HOLD.search(text) and not PERSON_GO.search(text) and not hold_is_question(text):
            out[th] = f"the person's newest post here, {p.get('id')}, reads as a pause: \"{' '.join(text.split())[:80]}\""
    return out

def tier_held_of(threads):
    """{thread: why} for threads whose work tier starts no new run now (paused, off, past its stop point); {} on any error.
    The keeper briefed the paused voice-board tier's ladder every few hours and then chased the held worker for a reply
    (ha-voice-sbc, 2026-11-19: 03:04 brief, 10:04 "got no reply", both held by the starter; hive stalled counted it)."""
    try:
        sys.path.insert(0, str(CORE)); import tiers, focus
        d = tiers.load()
        try: st = focus.state(posts=[])
        except Exception: st = {"foci": [], "tags": {}}
        try: bp = focus.board_posts()
        except Exception: bp = None
        out = {}
        for th in threads:
            try: fid = focus.focus_of(st, th, bp) if bp is not None else focus.focus_of(st, th)
            except Exception: fid = None
            why = tiers.held(d, tiers.tier_of(d, th, fid))
            if why: out[th] = why
        return out
    except Exception: return {}

def main():
    ap = argparse.ArgumentParser(prog="hive goals", description=__doc__.split("\n")[0])
    ap.add_argument("--json", action="store_true"); ap.add_argument("--idle", action="store_true", help="only the idle goals")
    ap.add_argument("--brief", metavar="THREAD", help="print the keeper round's brief for this focus or ladder thread, with its next rung"); ap.add_argument("--ladder-threads", action="store_true", help="idle focus and ladder threads, one a line: each gets a round")
    ap.add_argument("--post", metavar="THREAD", nargs="?", const="", help="post that brief to its thread; --model applies only to a fresh agent, so a woken worker keeps its own (a grader step-up, its own hive model). Bare: print the command for each --ladder-threads line, posting nothing")
    ap.add_argument("--model", help="with --post: the model for a fresh agent")
    ap.add_argument("--force", action="store_true", help="with --post: post even to a parked ladder")
    ap.add_argument("--continue", dest="cont", action="store_true", help="each focus ladder (a `## Ladder` with open rungs in a focus thread): on, off with its reason, or MISSING")
    ap.add_argument("--rounds", metavar="DAYS", type=float, help="keeper rounds on focuses over the last DAYS: work, wait, silent")
    ap.add_argument("--tick", metavar="POST", help="tick the rung --rung names, appending this result post's id after checking it is on the board")
    ap.add_argument("--rung", metavar="WORDS", help="with --tick: words from the rung's text, enough to pick one open rung")
    ap.add_argument("--file", metavar="GOALS_MD", help="with --tick: the goals.md (default: the one whose ladder runs in the post's thread)")
    a = ap.parse_args()
    if a.tick:
        if not a.rung: sys.exit("--tick needs --rung WORDS")
        post = next((e for e in jsonl(BOARD) if e.get("id") == a.tick), None)
        if not post: sys.exit(f"post {a.tick} is not on the board: tick with your result post's id (`hive read --thread T -n 3` shows it)")
        path = a.file
        if not path:
            r = next((r for r in goals()["rows"] if r["thread"] == post.get("thread") and r.get("goals_path")), None)
            th = str(post.get("thread") or "")   # a lane thread (rsi-lab-bench) falls back to the project it is named after
            pj = max((d for d in PROJECTS.iterdir() if (d / "goals.md").exists() and (th == d.name or th.startswith(d.name + "-"))), key=lambda d: len(d.name), default=None)
            if not r and not pj: sys.exit(f"no goals.md has a ladder in thread {th}: give --file")
            path = r["goals_path"] if r else pj / "goals.md"
        f = Path(path).expanduser()
        try: new, line = tick(f.read_text(errors="replace"), a.rung, a.tick)
        except ValueError as e: sys.exit(f"{f}: {e}")
        f.write_text(new); print(f"ticked in {f}:\n{line.strip()}"); return
    if a.cont:
        try: sys.path.insert(0, str(CORE)); import focus as _F; _fst = _F.state(); fof = lambda th: _F.focus_of(_fst, th)
        except Exception as e: sys.exit(f"focus store unreadable: {e}")
        miss = {p for p, _, _ in continue_missing(project_goals(), fof)}
        for pg in project_goals():
            lad = pg.get("ladder")
            if not lad or not lad.get("left"): continue
            th = next((x for x in (lad.get("thread"), pg["project"]) if x and fof(x)), None)
            if not th: continue
            print(f"{pg['project']:<22} {th:<16} {fof(th):<16} {'MISSING' if pg['project'] in miss else pg['continue']}")
        sys.exit(1 if miss else 0)
    g = goals()
    if a.rounds is not None:
        ths = [r["thread"] for r in g["rows"] if r["kind"] == "focus" or r.get("ladder")]; res = rounds(jsonl(BOARD), ths, (now() - dt.timedelta(days=a.rounds)).isoformat(timespec="seconds"))
        tot = [sum(v[i] for v in res.values()) for i in range(3)]; n = sum(tot)
        for th, v in res.items(): print(f"{th}: {v[0]} work, {v[1]} wait, {v[2]} silent")
        print(f"all: {tot[0]} of {n} rounds did work ({100 * tot[0] // n if n else 0}%), {tot[1]} wait, {tot[2]} silent, over {a.rounds:g} days"); return
    if a.ladder_threads or a.post == "":
        # bare `--post`: 7 keeper rounds in 24 h put it in their opening batch of checks and lost a turn to argparse's "expected one argument"
        # (cli_errors.py, 2026-11-24); it now prints the round commands to run, posting nothing, so the keeper still picks --model per rung
        seen, out = set(), []   # a thread once: two rows can share one (a focus and a project whose latest thread it is), and two lines were two rounds within seconds
        for r in g["rows"]:
            if r["idle"] and (r["kind"] == "focus" or r.get("ladder") or r.get("needs_ladder")) and r["thread"] not in seen: seen.add(r["thread"]); out.append(r["thread"])
        held, ph = tier_held_of(out), person_held_of(out)
        if a.post == "": print("--post needs a thread; nothing posted. One round per idle ladder (--model opus for a design or a build from a spec):", file=sys.stderr)
        for th in out:
            if th in held: print(f"skipped {th}: its work tier is {held[th]} (`hive tier why {th}`)", file=sys.stderr)
            elif th in ph: print(f"skipped {th}: {ph[th]}", file=sys.stderr)
            else: print(f"hive goals --post {th} --model sonnet" if a.post == "" else th)
        return
    if a.brief or a.post:
        th = a.post or a.brief
        # needs_ladder rows are on --ladder-threads, so their round has to post: x-comment-finder was refused here for four keeper rounds (2026-11-24)
        r = next((r for r in g["rows"] if r["thread"] == th and (r["kind"] == "focus" or r.get("ladder") or r.get("needs_ladder"))), None)
        if not r: sys.exit(f"no focus or ladder in thread {th}")
        b = brief(r)
        if not a.post: print(b); return
        if r.get("parked") and not a.force:   # a keeper hand-picking threads posted rounds into a parked ladder every 4 h (fund-rsi, 2026-11-08)
            sys.exit(f"{th} is parked until {r['parked']['until']}: {r['parked']['why'][:120]}\nNo round posted; --force posts one anyway.")
        if r.get("gated_until") and not r.get("rung") and not r.get("ask_more") and not a.force:   # every open rung gated to a later date and today's ask already made: another round could only repeat it (improve-survey, 2026-11-16: keeper rounds at 04:03 and 07:03 after the floor fix)
            sys.exit(f"{th} has no rung before {r['gated_until']}Z (every open rung carries after:). No round posted; --force posts one anyway.")
        if not a.force and (why := tier_held_of([th]).get(th)):   # the starter would only hold the brief, and the next keeper chase it
            sys.exit(f"{th}: its work tier is {why} (`hive tier why {th}`), so the starter would hold the brief. No round posted; --force posts one anyway.")
        if not a.force and (why := person_held_of([th]).get(th)):
            sys.exit(f"{th}: {why}. No round posted; --force posts one anyway (only once the person has said to go on).")
        if not a.force and (dup := recent_round(th)):
            sys.exit(f"{th}: a round was posted there under 5 min ago ({dup}), so a second would start a second agent on the same brief. No round posted; --force posts one anyway.")
        model, _ = no_rung_model(r, b, a.model)
        cmd = ["hive", "post", "--thread", th] + post_model_args(b, model)
        sys.exit(subprocess.run(cmd, input=b, text=True).returncode)
    if a.idle: g["rows"] = [r for r in g["rows"] if r["idle"]]
    print(json.dumps(g, indent=1, default=str) if a.json else render(g))

if __name__ == "__main__": main()
