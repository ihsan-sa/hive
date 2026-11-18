#!/usr/bin/env python3
"""The stop judge, a trial (hive-meta, ~/projects/hive-meta/time-invariance.md sections 7 and 8): when a run ends and nothing else carries its work on,
a separate small model, shown the goal, its open rungs and what the run posted, says whether the work is done.

Why: the stop gate already asks a run three times whether it may stop, and the grader shows what comes of it: runs agree there is more to do and stop
anyway (section 7, 2026-11-18). The question is put to the run that wants to stop, which is the one party with a reason to say yes. Here the run does
not get the last word on its own stop. The verdicts: done and blocked accept the stop; wait grants a bounded wait that must name what is awaited, and
wakes the agent when it is over if nothing moved; continue wakes the agent on one named step.

A trial, not a rule: ~/.hive/judge-trial.json {"from", "until", "model", "by", "shadow"} turns it on; absent, nothing here runs. The unit is a
conversation that carries a goal's ladder (a row of `hive goals` with one), with the side conversations of its focus. Each local day half the units,
drawn by a hash of the day and the unit within focus goals and project goals apart, are the judged arm; the rest are the control. A run end in the control arm, or
anywhere while "shadow" is true, is judged and logged but never acted on, so the two arms can be compared verdict for verdict.

The starter never waits for the model: continuation.ask_judge writes a snapshot of the run end and starts `judge.py run SNAPSHOT` detached. A continue
or wait becomes an intent in ~/.hive/continue, which the starter's own path (continuation.enqueue: stop marks, another agent on the thread, a post since,
budgets) turns into one wake or drops. Any error, a timeout or a login at its limit accepts the stop: the judge fails open.

CLI: hive judge stats [--since D] [--json] | log [-n N] | replay [--hours H] [-n N] [--models a,b] [--out FILE] [--same FILE] | case SNAPSHOT
"""
import argparse, datetime as dt, hashlib, json, os, re, sys, time
from collections import Counter, defaultdict
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import continuation as C
import goals as G

HOME = Path.home()
DIR, RUNLOG, REMIND = C.JUDGE_DIR, HOME / "log.jsonl", HOME / ".hive" / "remind" / "reminders.json"
RENAMED = HOME / ".hive" / "renamed.jsonl"
MODEL = "haiku"
MAX_WAIT_MIN = 360   # a longer wait is the floor's or the keeper's to pick up, not a wake to hold for
PER_6H = 3           # judge wakes of one agent in one conversation in six hours: past that it is the judge looping, not the work
MISSED = re.compile(r"^judge missed:", re.I)
VERDICTS = ("done", "blocked", "wait", "continue")
HEX12 = re.compile(r"^[0-9a-f]{12}$")

def now(): return dt.datetime.now(dt.timezone.utc)
def day_of(ts): return dt.datetime.fromtimestamp(ts).strftime("%Y-%m-%d")   # the host's day, the clock post ids (and so rung ticks) are written in

def trial(t=None, path=None):
    """The live trial's settings, or None: no file, unreadable, or outside its from..until. One rule, kept in continuation, which the starter reads too."""
    return C.judge_trial(t, path)

def log(**e):
    DIR.mkdir(parents=True, exist_ok=True)
    with (DIR / "log.jsonl").open("a") as f: f.write(json.dumps({"at": C.iso(), **e}) + "\n")
def entries(): return C.jsonl(DIR / "log.jsonl")

# --- units and arms
def units(rows):
    """{conversation: "focus" | "project"} for every goal row that carries a ladder. Several rows can share one conversation: it is one unit, a focus if any row is."""
    out = {}
    for r in rows:
        if r.get("rungs_left") is None or not r.get("thread"): continue
        if out.get(r["thread"]) != "focus": out[r["thread"]] = r.get("kind") or "project"
    return out

def scan_board(thread=None, path=None):
    """One pass over the whole board: (the posts of one conversation, oldest first; {conversation: {thread, parent} of its first post}).
    continuation.thread_posts reads only the board's tail, and that is not enough here: a goal's conversation is often older than the tail, and
    the focus of a side conversation hangs on the parent of its first post."""
    posts, first = [], {}
    try:
        with open(path or C.BOARD, encoding="utf-8", errors="replace") as f:
            for ln in f:
                try: p = json.loads(ln)
                except ValueError: continue
                th = isinstance(p, dict) and (p.get("thread") or p.get("id"))
                if not th: continue
                if th not in first: first[th] = {"thread": th, "parent": p.get("parent")}
                if th == thread: posts.append(p)
    except OSError: pass
    return posts, first

def finder(first, rows):
    """(of, home): of(conversation) is the focus the pages show it under, home[focus] the conversation that carries that focus's ladder: the
    focus's own, else the one named by the ladder of the project with the focus's name (its main ladder before a section's). Any error: nothing
    is in a focus."""
    try:
        import focus
        st = focus.state(); us = units(rows); home = {}
        for f in st["foci"]:
            own = f.get("thread") or f["id"]
            mine = [] if own in us else sorted((r for r in rows if r.get("thread") in us and r.get("rungs_left") is not None and Path(str(r.get("goals_path") or "")).parent.name == f["id"]),
                                               key=lambda r: bool(r.get("section")))
            home[f["id"]] = mine[0]["thread"] if mine else own
        return focus.focus_finder(st, first.values()), home
    except Exception: return (lambda th: None), {}

def unit_of(thread, us, of=None, home=None):
    """(unit, own) for a conversation: itself when it carries a ladder, else the own conversation of its focus when that carries one, else (None, False)."""
    if thread in us: return thread, True
    try: h = (home or {}).get(of(thread)) if of else None
    except Exception: h = None
    return (h, False) if h in us else (None, False)

def title_of(thread, posts):
    """A conversation's name as people read it: its own name when it has one, else its newest rename, else the first line of its first post."""
    if thread and not HEX12.match(str(thread)): return str(thread)
    t = next((str(x["title"]).strip() for x in reversed(C.jsonl(RENAMED)) if x.get("thread") == thread and str(x.get("title") or "").strip()), "")
    if not t:
        t = next((ln.strip() for ln in str((posts[0] if posts else {}).get("text", "")).splitlines() if ln.strip()), "")
        t = re.sub(r"^\(from [^)]*\)\s*", "", re.sub(r"^(?:@[\w-]+[\s,:]*)+", "", t))
    return t[:160]

def stopped(thread, posts):
    """The person has stopped this conversation. A read-only twin of continuation.stopped_thread, which writes the starter's stop marks: this
    process only reads them, and the starter's own path checks again before any wake."""
    if C.stop_file(thread).exists(): return True
    told = C.newest_status(thread)
    lines = [C.clean(l) for l in str(told.get("text", "")).splitlines() if l.strip()]
    if not lines or not lines[-1].lower().startswith("told to stop:"): return False
    at = told.get("at") if isinstance(told.get("at"), (int, float)) else 0
    return not any(p.get("from") == "person" and (C.epoch(p.get("at")) or 0) > at for p in posts)

def split(unit, us, day):
    """A unit's arm by the hash rule alone: within each kind the units are ranked by a hash of the day and the unit and the first half is judged;
    with an odd count the extra one goes to the judged arm on even days of the year and to the control on odd ones."""
    kind = us[unit]
    rank = sorted((u for u, k in us.items() if k == kind), key=lambda u: hashlib.sha1(f"{day}:{u}".encode()).hexdigest())
    extra = len(rank) % 2 and dt.date.fromisoformat(day).timetuple().tm_yday % 2 == 0
    return "judge" if rank.index(unit) < len(rank) // 2 + extra else "control"

def arms(us, day, store=True):
    """{unit: arm} for a day, fixed at the day's first reading so a goal opened or closed later cannot move a unit between arms: the hash split of
    the units of that moment is kept in DIR/arms/DAY.json, and a unit first seen later goes to the smaller arm of its kind (a tie by the hash rule).
    store=False only reads (stats over past days); a day with no file falls back to the hash split of `us`."""
    p = Path(DIR) / "arms" / f"{day}.json"
    def read():
        try: d = json.loads(p.read_text())
        except (OSError, ValueError): return None
        return {str(k): v for k, v in d.items() if v in ("judge", "control")} if isinstance(d, dict) else None
    if not store:
        got = read()
        return got if got is not None else {u: split(u, us, day) for u in us}
    import fcntl
    p.parent.mkdir(parents=True, exist_ok=True)
    with open(p.parent / ".lock", "w") as lk:
        fcntl.flock(lk, fcntl.LOCK_EX)
        got = read(); fresh = got is None
        if fresh: got = {u: split(u, us, day) for u in us}
        new = sorted((u for u in us if u not in got), key=lambda u: hashlib.sha1(f"{day}:{u}".encode()).hexdigest())
        for u in new:
            n = Counter(got[v] for v in got if us.get(v) == us[u])
            got[u] = split(u, us, day) if n["judge"] == n["control"] else ("judge" if n["judge"] < n["control"] else "control")
        if fresh or new:
            tmp = p.with_suffix(f".{os.getpid()}.tmp"); tmp.write_text(json.dumps(got, indent=0, sort_keys=True)); os.replace(tmp, p)
    return got

def arm(unit, us, day):
    """"judge" or "control" for a unit on a day, as fixed by arms()."""
    return arms(us, day)[unit]

# --- the case
def trim(s, n):
    s = str(s or "").strip()
    return s if len(s) <= n else s[:n * 2 // 3].rstrip() + "\n[...]\n" + s[-(n // 3):].lstrip()

def rung_lines(row, path_text=None):
    """The open rungs of a row's ladder as the judge reads them: ready ones first, then those that wait on a date."""
    try: txt = path_text if path_text is not None else Path(row["goals_path"]).read_text(errors="replace")
    except (OSError, KeyError, TypeError): return []
    lads = [G.ladder(txt)] + G.ladders(txt)
    lad = next((l for l in lads[1:] if row.get("section") and l.get("section") == row["section"]), None) or (lads[0] if not row.get("section") else None)
    if not lad: return []
    ready = {id(r) for r in lad["rungs"] if r["state"] == " " and lad["next"] and (r is lad["next"] or r["text"] in lad["due_more"])}
    out = []
    for r in lad["rungs"]:
        if r["state"] == "x": continue
        tag = "blocked" if r["state"] == "!" else "ready" if id(r) in ready else "no check written" if not r["checked"] else f"not before {r['after']}" if r["after"] else "ready"
        out.append((tag != "ready", f"- [{tag}] {trim(r['text'], 420)}"))
    return [l for _, l in sorted(out, key=lambda x: x[0])]

def reminders(name, thread, t):
    try: rs = json.loads(REMIND.read_text())
    except (OSError, ValueError): return []
    return [f"{r.get('due')} (host time): {trim(r.get('text'), 200)}" for r in rs if isinstance(r, dict) and r.get("to") == name and r.get("thread") == thread]

def open_asks(thread):
    try:
        asks = G.asks_module(); items = asks.ledger()
        return [f"{e.get('id')} [{e.get('status')}{', held by ' + str(e['owner']) if e.get('owner') else ''}] {trim(e.get('ask'), 240)}" for e in asks.open_items(items) if e.get("thread") == thread and e.get("kind") != "direction"]
    except Exception: return []

def build_case(r, rows, posts, statuses, unit=None, is_own=False, title=None, end=None, extras=True):
    """What the judge is shown for one run end, as a dict; None when the run posted nothing in its conversation. `posts` is the whole conversation,
    oldest first, and (unit, is_own) is unit_of's answer for it; `end` cuts it for a replay (seconds), and then the asks and reminders of today are
    left out (extras)."""
    name, thread, since = str(r.get("name", "")).lower(), r.get("thread"), C.epoch(r.get("at", "")) or 0
    end = end or now().timestamp()
    posts = [p for p in posts if (C.epoch(p.get("at")) or 0) <= end]
    own = [p for p in posts if str(p.get("from", "")).lower() == name and (C.epoch(p.get("at")) or 0) >= since]
    if not own: return None
    grows = [x for x in rows if unit and x.get("thread") == unit and x.get("rungs_left") is not None]
    goal = []
    for x in grows:
        state = "parked until " + x["parked"]["until"] if x.get("parked") else "every open rung waits on a date, the first opens " + x["gated_until"] if x.get("gated_until") else ""
        goal.append({"name": x.get("name"), "measure": trim(x.get("measure"), 500), "done": x.get("rungs_done"), "left": x.get("rungs_left"), "state": state, "rungs": rung_lines(x)[:14]})
    wake = next((p for p in posts if p.get("id") == r.get("post")), None)
    bar = [s for s in statuses if s.get("thread") == thread and isinstance(s.get("at"), (int, float)) and s["at"] <= end][-1:]
    others = [p for p in posts if (C.epoch(p.get("at")) or 0) >= since and p not in own and p is not wake][-4:]
    title = title_of(thread, posts) if title is None else title
    return {"now": dt.datetime.fromtimestamp(end, dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC"), "name": name, "thread": thread, "title": title, "unit": unit, "own": is_own,
            "started": str(r.get("at", ""))[:16], "minutes": round((end - since) / 60), "model": r.get("model"), "goal": goal,
            "wake": {"from": wake.get("from"), "at": str(wake.get("at", ""))[:16], "text": trim(wake.get("text"), 2400)} if wake else {"from": r.get("from"), "text": ""},
            "own_posts": [{"id": p.get("id"), "at": str(p.get("at", ""))[:16], "text": trim(p.get("text"), 3200 if p is own[-1] else 900)} for p in own[-3:]], "n_own": len(own),
            "others": [{"from": p.get("from"), "at": str(p.get("at", ""))[:16], "text": trim(p.get("text"), 500)} for p in others],
            "bar": trim(bar[0].get("text"), 700) if bar else "", "asks": open_asks(thread) if extras else [], "reminders": reminders(name, thread, end) if extras else [],
            "post": own[-1].get("id"), "since": C.epoch(own[-1].get("at")) or 0}

RULES = """You are the stop judge of a hive of AI agents that work on long-running goals. An agent's run has just ended: the agent chose to stop. Decide, from the evidence below, whether the work it was on is finished. The agent does not get the last word on its own stop, so treat its closing line ("Done:", "Waiting:") as a claim to check, not as the answer.

Answer with one JSON object and nothing else:
{"verdict": "done" | "blocked" | "wait" | "continue", "minutes": <whole minutes, only for wait>, "step": "<only for continue and wait>", "why": "<one or two sentences naming the evidence you used>"}

- done: what the run was woken for is delivered, and there is no further step this agent should take now in this conversation.
- blocked: work is left, but every part of it needs something no agent can supply: the person's decision, sign-in, payment or hands, or a hold someone placed.
- wait: work is left for this agent that depends on something that arrives by itself (a job that is running, a board or a lease that frees at a stated time, a reading on a date), nothing else can be done meanwhile, and nothing shown will wake this agent when it arrives. "step" names what is awaited and what the agent does then. "minutes" runs from "Time now" to the time the evidence names for it, plus ten; with no time named, 60. It is 360 at most: if what is awaited is further off than that, answer done.
- continue: work is left that this agent can do now with nobody's help. "step" is that one step, concrete enough to act on, in the words of the rung or of the run's own post.

Rules, in order of weight:
1. Makework is worse than a stop. Answer continue only when you can point at the step in the evidence: a rung marked [ready], something the run itself wrote that it would do next or left undone ("next", "left", "remaining", "not yet", "will"), or a part of what the waking post asked for that no post shows delivered. More checking of work whose check passed, re-reading, summaries and status updates are not steps. If you cannot point at one, answer done.
2. The person's steps stay the person's. Never name as the step something that needs the person. If the run left an item for the person and other work is open that needs nobody, answer continue on that other work; if only the person's item is left, answer blocked.
3. In the goal's own conversation, a [ready] rung is work left, even after a run that finished the rung before it and said Done. Rungs marked "not before" a future date, [blocked] rungs, rungs marked "no check written" and a parked ladder are not work left now.
4. In a side conversation the goal's rungs are not this run's job. Answer continue there only on the run's own unfinished work in that conversation.
5. Work that was handed on is not left for this run. Answer done when another agent was started or asked by name and is to answer (its reply wakes this agent), when a reminder is shown as set that will wake this agent (listed below, or the run's post says it set one and for when), or when what is left belongs to another agent: a rung another agent holds is never this agent's wait. A line that "the next round", "the keeper" or "a later wake" will take the next step is not a handoff, because nobody was asked: if that step is one this agent can do now, answer continue; if it opens only at a later time, answer wait.
6. A run that answered a question, reviewed something for another agent or did a routine round has finished when its answer is posted. Most run ends are honest stops; do not look for a reason to go on.
"""

def prompt(case):
    L = [RULES, "\n=== The run that ended ===",
         f"Time now: {case['now']}. Agent {case['name']} ({case.get('model') or 'model unknown'}) ran {str(case['minutes']) + ' min' if case['minutes'] else 'under a minute'} in the conversation \"{case['title']}\" ({case['thread']}) and posted {case['n_own']} time{'s' * (case['n_own'] != 1)} there."]
    if case["unit"] and case["own"]: L.append("This conversation is a goal's own: its ladder of rungs is below.")
    elif case["unit"]: L.append(f"This is a side conversation of the goal that lives in \"{case['unit']}\": the goal is shown for context only (rule 4).")
    for g in case["goal"]:
        L.append(f"\n=== Goal: {g['name']} ===\nMeasure: {g['measure'] or 'none written'}\nRungs done: {g['done']}, open: {g['left']}." + (f" Ladder state: {g['state']}." if g["state"] else ""))
        L.append("Open rungs:\n" + "\n".join(g["rungs"]) if g["rungs"] else "No open rungs.")
    w = case["wake"]
    L.append(f"\n=== The post that woke the run (from {w.get('from')}{', ' + w['at'] if w.get('at') else ''}) ===\n{w.get('text') or '(not on the board: a timer or the starter woke it)'}")
    for p in case["others"]: L.append(f"\n=== Posted meanwhile by {p['from']} ({p['at']}) ===\n{p['text']}")
    for i, p in enumerate(case["own_posts"]):
        last = i == len(case["own_posts"]) - 1
        L.append(f"\n=== {'The run' + chr(39) + 's last post' if last else 'An earlier post of the run'} ({p['at']}) ===\n{p['text']}")
    if case["bar"]: L.append(f"\n=== The conversation's status bar ===\n{case['bar']}")
    if case["asks"]: L.append("\n=== Asks of the person still open in this conversation ===\n" + "\n".join(f"- {a}" for a in case["asks"]))
    if case["reminders"]: L.append("\n=== Reminders set that will wake this agent here ===\n" + "\n".join(f"- {a}" for a in case["reminders"]))
    L.append("\nYour JSON answer:")
    return "\n".join(L)

def parse(text):
    """The model's answer as {verdict, minutes, step, why}, or None when it is not one of the four verdicts or lacks what its verdict needs."""
    try: d = G.asks_module().parse_json(text)
    except Exception: return None
    v = str((d or {}).get("verdict", "")).strip().lower()
    if v not in VERDICTS: return None
    step, why = " ".join(str(d.get("step") or "").split()), " ".join(str(d.get("why") or "").split())[:400]
    try: minutes = int(d.get("minutes") or 0)
    except (TypeError, ValueError): minutes = 0
    if v in ("wait", "continue") and len(step) < 12: return None
    if v == "wait" and minutes <= 0: return None
    return {"verdict": v, "minutes": minutes if v == "wait" else 0, "step": step[:600] if v in ("wait", "continue") else "", "why": why}

def ask(case, model, timeout=240):
    t0 = time.time()
    text, cost = G.asks_module().ask_model(prompt(case), model, timeout)
    return parse(text), cost, round(time.time() - t0, 1), text

# --- one run end
def judge_wakes(name, thread, t):
    return sum(1 for e in C.events() if e.get("event") == "launch" and e.get("judge") and e.get("ran") == name and e.get("thread") == thread and (C.epoch(e.get("at", "")) or 0) > t - 6 * 3600)

def write_intent(r, case, v, t):
    """The judge's continue or wait as an intent the starter's own path turns into one wake, or drops."""
    chain, hop = C.chain_of(r, case["post"])
    wait = v["verdict"] == "wait"
    intent = {"chain": chain, "hop": hop, "run": r.get("session"), "post": case["post"], "name": r.get("name"), "thread": r.get("thread"),
              "line": f"Next: {v['step']}", "since": case["since"], "at": C.iso(), "key": f"judgeq-{C.safe(r.get('name'))}-{C.safe(r.get('at'))}",
              "judge": {"verdict": v["verdict"], "why": v["why"], **({"minutes": v["minutes"]} if wait else {})}, **({"not_before": t + v["minutes"] * 60} if wait else {})}
    C.DIR.mkdir(parents=True, exist_ok=True)
    tmp = C.DIR / f".{C.safe(chain)}.tmp"; tmp.write_text(json.dumps(intent)); tmp.replace(C.DIR / f"{C.safe(chain)}.json")
    C.record("intent", judge=v["verdict"], **{k: intent[k] for k in ("chain", "hop", "thread", "post", "name", "line")})
    return intent

def run(snap, model=None, asker=ask):
    """Judge one run end from the starter's snapshot. Returns the log entry it wrote, or None when the trial is off or the conversation is not in it."""
    t = now().timestamp()
    tr = trial(t)
    r = snap.get("run") or {}
    name, thread = str(r.get("name", "")).lower(), r.get("thread")
    if not tr or not name or not thread or thread in C.SYSTEM_THREADS: return None
    rows = G.goals()["rows"]; us = units(rows)
    posts, first = scan_board(thread)
    unit, own = unit_of(thread, us, *(finder(first, rows) if thread not in us else ()))
    if not unit: return None
    if not C.claim(DIR / "done" / f"judge-{C.safe(name)}-{C.safe(r.get('at'))}"): return None
    day = day_of(t); a = arm(unit, us, day); shadow = bool(tr.get("shadow", True))
    base = {"name": name, "thread": thread, "unit": unit, "kind": us[unit], "own": own, "day": day, "arm": a, "shadow": shadow, "session": r.get("session"), "run_at": r.get("at"), "reasons": snap.get("reasons") or []}
    def skip(reason, **more): e = {**base, "skip": reason, **more}; log(**e); return e
    case = build_case(r, rows, posts, C.jsonl(C.STATUS), unit, own)
    if not case: return skip("silent")
    base["post"] = case["post"]
    last = [C.clean(l) for l in str(case["own_posts"][-1]["text"]).splitlines() if l.strip()]
    if any(MISSED.match(l) for l in last[:1] + last[-2:]): return skip("judge_missed")
    if stopped(thread, posts): return skip("stop")
    if any(who != name and th == thread for who, th in (snap.get("alive") or {}).items()): return skip("other_running")
    if any(who != name and thread in ths for who, ths in (snap.get("pending") or {}).items()): return skip("other_queued")
    if (snap.get("pending") or {}).get(name): return skip("agent_queued")
    if judge_wakes(name, thread, t) >= PER_6H: return skip("capped")
    m = model or tr.get("model") or MODEL
    try: v, cost, secs, text = asker(case, m)
    except Exception as e: return skip("error", why=f"{type(e).__name__}: {e}"[:300], model=m)
    if not v: return skip("unparsed", model=m, text=str(text)[-300:])
    e = {**base, **v, "model": m, "cost": cost, "secs": secs, "acted": False}
    if v["verdict"] == "wait" and v["minutes"] > MAX_WAIT_MIN: e["long_wait"] = True
    elif v["verdict"] in ("wait", "continue") and a == "judge" and not shadow:
        write_intent(r, case, v, t); e["acted"] = True
    log(**e)
    return e

# --- hive judge stats
def section_ticks(row):
    """{day: ticks} for one goal row: its ladder's ticked rungs by the day of the post each tick cites, with the ladder-done.md beside a main ladder."""
    out = Counter()
    try: txt = Path(row["goals_path"]).read_text(errors="replace")
    except (OSError, KeyError, TypeError): return out
    lads = [G.ladder(txt)] + G.ladders(txt)
    lad = next((l for l in lads[1:] if row.get("section") and l.get("section") == row["section"]), None) or (lads[0] if not row.get("section") else None)
    lines = [r["text"] for r in (lad or {}).get("rungs", []) if r["state"] == "x"]
    if not row.get("section"):
        try: lines += [l[5:] for l in (Path(row["goals_path"]).parent / "ladder-done.md").read_text(errors="replace").splitlines() if l.startswith("- [x]")]
        except OSError: pass
    year = now().year
    for l in lines:
        if (m := C.TICK_ID.search(l)): out[f"{year}-{m.group(1)}-{m.group(2)}"] += 1
    return out

def unit_ticks(rows):
    out, seen = defaultdict(Counter), set()
    for r in rows:
        key = (r.get("goals_path"), r.get("section"))
        if r.get("rungs_left") is None or key in seen: continue
        seen.add(key); out[r["thread"]].update(section_ticks(r))
    return out

def stats(since=None, rows=None, board=None):
    """Per day and arm: units, run ends judged, verdicts, wakes, rungs ticked, the grades of judge-woken runs; and what followed a `continue` in each arm."""
    es = [e for e in entries() if e.get("day") and (not since or e["day"] >= since)]
    rows = G.goals()["rows"] if rows is None else rows
    us = units(rows); ticks = unit_ticks(rows)
    days = sorted({e["day"] for e in es})
    day_arms = {d: arms(us, d, store=False) for d in days}
    launches = [e for e in C.events() if e.get("event") == "launch" and e.get("judge")]
    grades = {(o.get("post"), o.get("name")): o.get("grade") for o in C.jsonl(C.OUTCOMES)}
    board = C.jsonl(C.BOARD) if board is None else board
    by_thread = defaultdict(list)
    for p in board:
        if p.get("thread") or p.get("id"): by_thread[p.get("thread") or p.get("id")].append(p)
    out = {"days": [], "since": since}
    for d in days:
        row = {"day": d}
        for a in ("judge", "control"):
            mine = [u for u, x in day_arms[d].items() if x == a]
            ev = [e for e in es if e["day"] == d and e.get("arm") == a]
            ls = [l for l in launches if day_of(C.epoch(l.get("at", "")) or 0) == d]
            gs = Counter(grades.get((l.get("post"), l.get("ran"))) or "ungraded" for l in ls) if a == "judge" else Counter()
            row[a] = {"units": len(mine), "ends": sum(1 for e in ev if e.get("verdict")), "skips": dict(Counter(e["skip"] for e in ev if e.get("skip"))),
                      "verdicts": dict(Counter(e["verdict"] for e in ev if e.get("verdict"))), "acted": sum(1 for e in ev if e.get("acted")),
                      "wakes": len(ls) if a == "judge" else 0, "grades": dict(gs), "ticks": sum(ticks[u][d] for u in mine if u in ticks),
                      "missed": sum(1 for e in ev if e.get("skip") == "judge_missed")}
        out["days"].append(row)
    # what followed a continue: the next substantive agent post in the conversation within 6 h, with no post of the person between
    follow = {}
    for a in ("judge", "control"):
        took = []
        for e in es:
            if e.get("verdict") != "continue" or e.get("arm") != a: continue
            t0 = C.epoch(e["at"]) or 0; nxt = None
            for p in by_thread.get(e["thread"], []):
                at = C.epoch(p.get("at")) or 0
                if at <= t0 or p.get("id") == e.get("post"): continue
                if at > t0 + 6 * 3600 or p.get("from") == "person": break
                if C.AGENT.match(str(p.get("from", ""))) and C.substantive(str(p.get("text", ""))): nxt = (at - t0) / 60; break
            took.append(nxt)
        got = [m for m in took if m is not None]
        follow[a] = {"continues": len(took), "followed_6h": len(got), "median_min": round(C.pct(got, .5), 1) if got else None}
    out["after_continue"] = follow
    # the lift is read over live days only (a judge entry not in shadow); over shadow days the two arms are treated alike, so their gap is the noise (A/A)
    live = {e["day"] for e in es if e.get("shadow") is False}
    for r in out["days"]: r["live"] = r["day"] in live
    def per(rows):
        n = {a: sum(r[a]["units"] for r in rows) for a in ("judge", "control")}
        t = {a: round(sum(r[a]["ticks"] for r in rows) / n[a], 4) if n[a] else None for a in n}
        return t, (round(t["judge"] - t["control"], 4) if None not in t.values() else None)
    out["ticks_per_unit_day"], out["lift"] = per([r for r in out["days"] if r["live"]])
    out["shadow_ticks_per_unit_day"], out["shadow_gap"] = per([r for r in out["days"] if not r["live"]])
    return out

def cmd_stats(a):
    s = stats(a.since)
    if a.json: print(json.dumps(s, indent=1)); return
    if a.lift: print(s["lift"] if s["lift"] is not None else 0); return   # one number for the ledger's check: 0 until a live day is read
    tr = trial()
    print("trial: " + (f"live until {tr.get('until')}, model {tr.get('model') or MODEL}, {'shadow (nothing acted on)' if tr.get('shadow', True) else 'acting in the judged arm'}" if tr else "off"))
    if not s["days"]: print("no run ends judged yet"); return
    for r in s["days"]:
        for arm_ in ("judge", "control"):
            x = r[arm_]
            print(f"{r['day']} {arm_:<7} units {x['units']:>2}  ends judged {x['ends']:>3}  {' '.join(f'{k} {v}' for k, v in sorted(x['verdicts'].items())) or '-':<40} acted {x['acted']}  wakes {x['wakes']}  rungs ticked {x['ticks']}"
                  + (f"  grades {x['grades']}" if x["grades"] else "") + (f"  judge-missed {x['missed']}" if x["missed"] else "") + (f"  skips {x['skips']}" if x["skips"] else ""))
    for arm_, f in s["after_continue"].items():
        print(f"after a continue, {arm_}: {f['continues']} verdicts, {f['followed_6h']} followed by a substantive agent post within 6 h" + (f", median {f['median_min']} min" if f["median_min"] is not None else ""))
    print(f"rungs ticked per unit-day: judged {s['ticks_per_unit_day']['judge']}, control {s['ticks_per_unit_day']['control']}, lift {s['lift']} (live days); shadow days: judged {s['shadow_ticks_per_unit_day']['judge']}, control {s['shadow_ticks_per_unit_day']['control']}, gap {s['shadow_gap']} (noise)")

def cmd_log(a):
    for e in entries()[-a.n:]:
        what = e.get("skip") and f"skip {e['skip']}" or f"{e.get('verdict')}{' ' + str(e['minutes']) + ' min' if e.get('minutes') else ''}{' ACTED' if e.get('acted') else ''}"
        print(f"{str(e.get('at'))[5:16]} {e.get('name'):<6} {str(e.get('thread'))[:22]:<22} {e.get('arm', '-'):<7} {what:<22} {str(e.get('step') or e.get('why') or '')[:110]}")

# --- replay: past run ends put to the judge, with what really followed
def run_ends(hours, t=None):
    """(clean run ends of the last hours, every run of that time) from the run log: each start record with the time its run ended (None: no end written)."""
    t = t or now().timestamp(); starts, out, runs = {}, [], []
    for e in C.jsonl(RUNLOG):
        at = C.epoch(e.get("at", "")) or 0
        if at < t - hours * 3600 - 6 * 3600: continue
        if e.get("event") == "start":
            starts[(e.get("name"), e.get("session"))] = x = {**e, "began": at, "ended": None}; runs.append(x)
        elif e.get("event") == "end" and (s := starts.pop((e.get("name"), e.get("session")), None)):
            s["ended"] = at; s["cost"] = e.get("cost")
            if e.get("exit") == 0 and at >= t - hours * 3600: out.append(s)
    return out, runs

def followed(e, posts, hours=3):
    """What happened in the conversation after a run end: (minutes to the next substantive agent post with no post of the person first, who) or (None, why)."""
    for p in posts:
        at = C.epoch(p.get("at")) or 0
        if at <= e["ended"] + 5: continue
        if at > e["ended"] + hours * 3600: break
        if p.get("from") == "person": return None, "person posted first"
        if C.AGENT.match(str(p.get("from", ""))) and C.substantive(str(p.get("text", ""))): return round((at - e["ended"]) / 60), str(p.get("from"))
    return None, "nothing"

def cmd_replay(a):
    rows = G.goals()["rows"]; us = units(rows)
    board = C.jsonl(C.BOARD); statuses = C.jsonl(C.STATUS)
    by_thread = defaultdict(list)
    for p in board: by_thread[p.get("thread") or p.get("id")].append(p)
    of, home = finder({th: {"thread": th, "parent": ps[0].get("parent")} for th, ps in by_thread.items() if th}, rows)
    ends, runs = run_ends(a.hours)
    ends = [e for e in ends if e.get("thread") and e["thread"] not in C.SYSTEM_THREADS and not str(e.get("name", "")).startswith(("w", "f"))]
    picked = []
    for e in ends:
        unit, own = unit_of(e["thread"], us, of, home)
        if not unit or e["ended"] > now().timestamp() - a.settle * 3600: continue
        if any(o is not e and o.get("thread") == e["thread"] and o.get("name") != e["name"] and o["began"] < e["ended"] < (o["ended"] or 9e12) for o in runs): continue   # another agent was on it: the live judge is not asked
        case = build_case(e, rows, by_thread[e["thread"]], statuses, unit, own, end=e["ended"], extras=False)
        if case: picked.append((e, case))
    if a.own: picked = [x for x in picked if x[1]["own"]]
    if a.same:   # the run ends of an earlier replay again, to read a change of the rules on the same cases
        keep = {(c["name"], c["thread"], c["ended"]) for c in json.loads(Path(a.same).read_text())["cases"]}
        picked = [x for x in picked if (x[0]["name"], x[0]["thread"], dt.datetime.fromtimestamp(x[0]["ended"], dt.timezone.utc).strftime("%m-%d %H:%M")) in keep]
    else:
        step = max(1, len(picked) // a.n) if a.n else 1
        picked = picked[::step][:a.n] if a.n else picked
    models = [m for m in a.models.split(",") if m]
    out = []
    for e, case in picked:
        mins, who = followed(e, by_thread[e["thread"]])
        rec = {"name": e["name"], "thread": e["thread"], "unit": case["unit"], "own": case["own"], "ended": dt.datetime.fromtimestamp(e["ended"], dt.timezone.utc).strftime("%m-%d %H:%M"),
               "followed_min": mins, "followed_by": who, "last": case["own_posts"][-1]["text"][-400:], "verdicts": {}}
        for m in models:
            try: v, cost, secs, text = ask(case, m)
            except Exception as x: v, cost, secs, text = None, None, None, f"{type(x).__name__}: {x}"
            rec["verdicts"][m] = {**(v or {"verdict": "unparsed", "raw": str(text)[-300:]}), "cost": cost, "secs": secs}
        out.append(rec)
        vs = "  ".join(f"{m}: {rec['verdicts'][m]['verdict']}" for m in models)
        print(f"{rec['ended']} {rec['name']:<6} {rec['thread'][:24]:<24} {'own ' if rec['own'] else 'side'} followed {str(mins) + ' min by ' + who if mins is not None else who:<22} {vs}", flush=True)
    if a.out: Path(a.out).write_text(json.dumps({"cases": out, "prompts": [prompt(c) for _, c in picked] if a.prompts else []}, indent=1))
    for m in models:
        cnt = Counter(r["verdicts"][m]["verdict"] for r in out)
        print(f"{m}: {dict(cnt)}  cost {sum(r['verdicts'][m].get('cost') or 0 for r in out):.3f}  mean secs {sum(r['verdicts'][m].get('secs') or 0 for r in out) / max(1, len(out)):.1f}")

def cmd_case(a):
    snap = json.loads(Path(a.snapshot).read_text()); r = snap.get("run") or {}
    rows = G.goals()["rows"]; us = units(rows); posts, first = scan_board(r.get("thread"))
    unit, own = unit_of(r.get("thread"), us, *finder(first, rows))
    case = build_case(r, rows, posts, C.jsonl(C.STATUS), unit, own)
    print(prompt(case) if case else "the run posted nothing in its conversation")

def cmd_run(a):
    try: snap = json.loads(Path(a.snapshot).read_text())
    except (OSError, ValueError) as e: print(f"unreadable snapshot: {e}", file=sys.stderr); return
    try:
        e = run(snap)
        if a.verbose: print(json.dumps(e, indent=1))
    except Exception as e:  # fail open: the stop stands
        log(skip="error", why=f"{type(e).__name__}: {e}"[:300], name=(snap.get("run") or {}).get("name"), thread=(snap.get("run") or {}).get("thread"))
    finally:
        Path(a.snapshot).unlink(missing_ok=True)

def main():
    ap = argparse.ArgumentParser(prog="hive judge", description=__doc__.splitlines()[0])
    sp = ap.add_subparsers(dest="cmd", required=True)
    s = sp.add_parser("stats", help="per day and arm: units, run ends judged, verdicts, wakes, rungs ticked, grades of judge-woken runs; what followed a continue in each arm")
    s.add_argument("--since", help="first day, YYYY-MM-DD"); s.add_argument("--json", action="store_true"); s.add_argument("--lift", action="store_true", help="print only rungs ticked per unit-day on live days, judged minus control (0 before any live day)")
    l = sp.add_parser("log", help="the last verdicts"); l.add_argument("-n", type=int, default=20)
    p = sp.add_parser("replay", help="put past run ends to the judge and show what really followed each")
    p.add_argument("--hours", type=float, default=12); p.add_argument("-n", type=int, default=20); p.add_argument("--models", default=MODEL); p.add_argument("--out")
    p.add_argument("--settle", type=float, default=3, help="leave out run ends newer than this many hours: what followed is not known yet")
    p.add_argument("--own", action="store_true", help="only run ends in a goal's own conversation"); p.add_argument("--prompts", action="store_true")
    p.add_argument("--same", metavar="FILE", help="judge the run ends of an earlier replay's --out file again (they must still be within --hours)")
    c = sp.add_parser("case", help="print what the judge would be shown for a snapshot"); c.add_argument("snapshot")
    r = sp.add_parser("run", help="judge one run end from the starter's snapshot (the starter calls this)"); r.add_argument("snapshot"); r.add_argument("--verbose", action="store_true")
    a = ap.parse_args()
    {"stats": cmd_stats, "log": cmd_log, "replay": cmd_replay, "case": cmd_case, "run": cmd_run}[a.cmd](a)

if __name__ == "__main__": main()
