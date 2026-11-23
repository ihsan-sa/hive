#!/usr/bin/env python3
"""Continuation pilot (hive-meta P1, ~/projects/hive-meta/moving.md section 3): a run that ends on `Next: <step, with its check>` is carried on.

Why: over 2026-11-03..06 an eligible step waited a median 14.4 min (p75 119) for the next substantive post, because a stated next step
waits for a mention or a keeper round. Only threads of a project whose goals.md has `Continue: on` under `## Ladder` take part.

The starter calls this module; it never launches anything itself. A clean run end writes one durable intent, ~/.hive/continue/<chain>.json.
The main loop consumes it once (done/<post id>) into st["pending"], so the existing path (model, usage gates, pacing, resume or heir) does
the launch. Both before the intent and right before launch, check() can drop it, and a drop is logged with its reason to log.jsonl.
Duplicates are worse than a lost step: a crash loses a continuation rather than repeating one, and the keeper still picks that thread up.

CLI: hive continue list [--json] | stats [--baseline] [--since D] [--until D]
"""
import argparse, datetime as dt, glob, json, os, re, subprocess, sys
from collections import Counter, defaultdict
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import goals as G

HOME = Path.home()
DIR, BOARD, STATUS, PROJECTS, CONFIG, OUTCOMES = HOME / ".hive" / "continue", HOME / "board.jsonl", HOME / ".hive" / "thread-status.jsonl", HOME / "projects", Path(__file__).resolve().parent / "config.json", HOME / "outcomes.jsonl"
JUDGE_TRIAL, JUDGE_DIR = HOME / ".hive" / "judge-trial.json", HOME / ".hive" / "judge"   # the stop judge's trial (judge.py)
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")
# A step is the person's only when its line says so: it is for them, asks them, or waits on their word. Naming the person is not that: 48 of the 49
# drops this rule made from 2026-11-06 to 2026-11-10 were agent work that mentioned the person ("the right person's section", "as the person gets it").
# Agents also write to the person as "you" ("once you've decided", "needs your go"): a524d's review, 1,318 real lines, 2 waits caught, nothing new dropped.
PERSON_LINE = re.compile(r"""(?<![\w./-])(?:
    for\ you\b(?!\.\w|:?\ (?:lines?|items?|list|box)\b)
  | @person\b
  | (?:ask|asks|asking|asked)\ the\ person\b
  | (?:needs?|waits?\ (?:on|for)|waiting\ (?:on|for)|wait\ (?:on|for)|blocked\ on|up\ to|until|unless|once|when|after|if)\ the\ person\b(?!['’]s\b|-)
  | the\ person's\ (?:go\b|go-ahead|call|decision|answer|reply|ok\b|okay|approval|sign-?off|choice|word|say\b|yes\b|input|pick)
  | (?:once|until|when|after|unless|waits?\ on|waiting\ on|needs?)\ you(?:'ve|'re|'ll)?\ (?:have\ )?(?:decided?|picked?|pick|choose|chosen|signed|sign|approved?|confirmed?|said|say|answered|answer|replied|reply|logged|log)\b
  | (?:needs?|waits?\ (?:on|for)|waiting\ (?:on|for)|until|once|after|on)\ your\ (?:go\b|go-ahead|call|decision|answer|reply|ok\b|approval|sign-?off|choice|word|yes\b|pick)
  | the\ person\ (?:decides?|says?|answers?|replies|approves?|picks?|chooses?|confirms?|signs?|must|has\ to|needs\ to|to\ (?:decide|pick|choose|confirm|approve|sign|answer))\b
)""", re.I | re.X)
SYSTEM_THREADS = {"keeper", "improve", "watchdog", "starter", "kb-dream"}  # their rounds are started on a timer already
STOP_LINE = re.compile(r"^(done|blocked|needs the person|waiting|told to stop):", re.I)
TAIL = 4_000_000  # bytes of the board a check reads: posts since a step that has only just ended
OVERLAP = 0.8
is_stop = None  # the starter's own rule for a person's stop, set when it imports this

def now(): return dt.datetime.now(dt.timezone.utc)
def iso(): return now().isoformat(timespec="seconds")
def epoch(s):
    """A board time as seconds. Board times without a zone are UTC, as the stop gate reads them."""
    try: t = dt.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
    except ValueError: return None
    return (t if t.tzinfo else t.replace(tzinfo=dt.timezone.utc)).timestamp()

def safe(s): return re.sub(r"[^\w.-]", "_", str(s))
def jsonl(path):
    out = []
    try:
        for ln in Path(path).read_text(errors="replace").splitlines():
            try: r = json.loads(ln)
            except ValueError: continue
            if isinstance(r, dict): out.append(r)
    except OSError: pass
    return out

def events(): return jsonl(DIR / "log.jsonl")
def record(event, **e):
    DIR.mkdir(parents=True, exist_ok=True)
    with (DIR / "log.jsonl").open("a") as f: f.write(json.dumps({"at": iso(), "event": event, **e}) + "\n")
_dropped = []  # post ids dropped during the current on_run_end, so the other-thread pass logs each post once
_why = []      # the reasons of those drops, which ask_judge reads: a step another agent or a stop already has is not the judge's
def drop(intent, reason):
    _dropped.append(intent.get("post")); _why.append(reason)
    record("drop", reason=reason, **{k: intent.get(k) for k in ("chain", "hop", "thread", "post", "name", "line")})

def tail_posts(word):
    """The posts in the board's tail with this word (a JSON string) in them, oldest first."""
    try:
        with BOARD.open("rb") as fh:
            fh.seek(max(0, os.fstat(fh.fileno()).st_size - TAIL)); data = fh.read()
    except OSError: return []
    out = []
    needle = json.dumps(str(word))
    for ln in data.decode(errors="replace").splitlines():
        if needle not in ln: continue
        try: p = json.loads(ln)
        except ValueError: continue
        if isinstance(p, dict): out.append(p)
    return out

def thread_posts(thread):
    """The thread's posts in the board's tail, oldest first."""
    return [p for p in tail_posts(thread) if (p.get("thread") or p.get("id")) == thread]

def enabled(thread):
    """The goals.md of the project this thread belongs to, if it has `Continue: on` under `## Ladder`: (project, path, text), or None."""
    if not thread or thread in SYSTEM_THREADS: return None
    th, hit = str(thread).lower(), None
    for g in sorted(PROJECTS.glob("*/goals.md")):
        if g.parent.name.startswith("_"): continue
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        m = G.LADDER.search(txt)
        if not m: continue
        rest = txt[m.end():]
        end = re.search(r"^#{1,2}\s", rest, re.M)
        on = re.search(r"^Continue:\s*on\b", rest[:end.start()] if end else rest, re.M | re.I)
        # the project whose own ladder runs in this thread decides it: llm-playground's rungs name thread `fellows-study`, so its
        # `Continue: on` chained the study conversation that fellows-prep keeps off (ad388 2026-11-19)
        own = {x.lower() for x in G.THREAD.findall(m.group(1))} | {g.parent.name.lower()}
        if th in own: return (g.parent.name, g, txt) if on else None
        if on and not hit and th in {t.lower() for t in G.THREAD.findall(txt)}: hit = g.parent.name, g, txt
    return hit

def set_off(thread):
    """Whether the project whose own ladder runs in this thread says `Continue: off` on purpose. The judge leaves such a thread alone: its shadow
    read (ad388 2026-11-20) found it ruling continue and wait in fellows-study, the study conversation fellows-prep keeps off at the person's pace."""
    if not thread or thread in SYSTEM_THREADS: return False
    th = str(thread).lower()
    for g in sorted(PROJECTS.glob("*/goals.md")):
        if g.parent.name.startswith("_"): continue
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        m = G.LADDER.search(txt)
        if m and th in {x.lower() for x in G.THREAD.findall(m.group(1))} | {g.parent.name.lower()}:
            return str(G.continue_state(txt) or "").startswith("off")
    return False

def clean(line): return line.strip().strip("*_ ").strip()
def is_next(line): return line.lower().startswith("next:") and len(line) - 4 > 12
def post_line(p):
    """What a post says about its end: ("next", line), ("stop", line) or None. A closing stop reason beats an opening Next."""
    lines = [clean(l) for l in str(p.get("text", "")).splitlines() if l.strip()]
    if not lines: return None
    if is_next(lines[-1]): return "next", lines[-1]
    if STOP_LINE.match(lines[-1]): return "stop", lines[-1]
    if is_next(lines[0]): return "next", lines[0]
    return None
def status_line(r):
    lines = [clean(l) for l in str(r.get("text", "")).splitlines() if l.strip()]
    if not lines: return None
    if is_next(lines[-1]): return "next", lines[-1]
    if STOP_LINE.match(lines[-1]): return "stop", lines[-1]
    return None

def newest_status(thread):
    best = {}
    for r in jsonl(STATUS):
        if r.get("thread") == thread: best = r
    return best

def own_said(r):
    """(all the thread's posts, the run's own posts, its newest post and newest status line: newest first as (time, order, kind, post or status))."""
    name, thread, since = str(r.get("name", "")).lower(), r.get("thread"), epoch(r.get("at", "")) or 0
    all_posts = thread_posts(thread)
    posts = [p for p in all_posts if str(p.get("from", "")).lower() == name and (epoch(p.get("at")) or 0) >= since]
    said = [(epoch(p.get("at")) or 0, 0, "post", p) for p in posts[-1:]]
    said += [(s["at"], 1, "status", s) for s in [s for s in jsonl(STATUS) if s.get("thread") == thread and str(s.get("from", "")).lower() == name and isinstance(s.get("at"), (int, float)) and s["at"] >= since][-1:]]
    return all_posts, posts, sorted(said, key=lambda y: y[:2], reverse=True)

def open_rung(thread):
    """The rung the thread's ladder would hand out next, or None: none open, or parked."""
    for row in G.goals()["rows"]:
        if row.get("thread") == thread and row.get("rung") and not row.get("parked"): return row["rung"]
    return None

def carry_on(r):
    """A run that ended `Done:` or on no line at all, with a rung still open on its ladder: the next rung is the step, so the agent finishing it keeps the thread going rather than
    leaving it for a floor start (the person, 2026-11-07, k1303). `Blocked:`, `Needs the person:`, `Waiting:` and `Told to stop:` stay stops; a run that posted nothing isn't carried on."""
    _, posts, said = own_said(r)
    for _, _, kind, x in said:
        got = post_line(x) if kind == "post" else status_line(x)
        if not got: continue
        if not got[1].lower().startswith("done:"): return None
        break
    rung = open_rung(r.get("thread")) if posts else None
    return {"line": f"Next: {rung[:400]}", "post": posts[-1]["id"], "since": epoch(posts[-1].get("at")) or 0} if rung else None

WAIT_CLOCK = re.compile(r"\b(\d{1,2}):(\d{2})\s*(UTC|Z\b)?", re.I)
WAIT_IN = re.compile(r"(?:\bin\s+|~\s*|\babout\s+)(\d+)\s*(h|hr|hrs|hours?|m|mins?|minutes?)\b", re.I)
WAIT_DATE = re.compile(r"\b(20\d\d)-(\d\d)-(\d\d)(?:[T ](\d{2}):(\d{2}))?")
def wait_date(line, base):
    """The earliest calendar date (UTC, 00:00 unless it has a time) after base that a `Waiting:` line names, or None. A date already past names nothing to wait for."""
    best = None
    for m in WAIT_DATE.finditer(line):
        try: t = dt.datetime(int(m[1]), int(m[2]), int(m[3]), int(m[4] or 0), int(m[5] or 0), tzinfo=dt.timezone.utc).timestamp()
        except ValueError: continue
        if t > base and (best is None or t < best): best = t
    return best

def wait_until(line, base):
    """The time a `Waiting:` line names, as seconds, or None. A calendar date wins; a clock time is the next one after base (the host clock unless it says UTC)."""
    if (d := wait_date(line, base)): return d
    if (m := WAIT_IN.search(line)): return base + int(m[1]) * (3600 if m[2][0].lower() == "h" else 60)
    if not (m := WAIT_CLOCK.search(line)) or int(m[1]) > 23 or int(m[2]) > 59: return None
    zone = dt.timezone.utc if m[3] else None
    when = dt.datetime.fromtimestamp(base, zone).replace(hour=int(m[1]), minute=int(m[2]), second=0, microsecond=0)
    return (when if when.timestamp() > base else when + dt.timedelta(days=1)).timestamp()

def wait_holds(line, base, running, cap=7200):
    """Until when a `Waiting:` line holds, as seconds, or None. It holds only if it says who (a running or queued agent) or when (a time)."""
    if set(re.findall(r"\b[a-z][0-9a-f]{4,}\b", line)) & set(running): return base + cap
    when = wait_until(line, base)
    return min(when, base + cap) if when else None

def waits(threads, running, t, cap=7200):
    """{thread: seconds} until which a `Waiting:` holds the floor off it. Only a wait that says who or when holds: a running or queued agent named in the line, or a time; a bare one
    ("for the next keeper round"), or one on an agent that isn't coming, carries on now. Newest of the last agent post and the status bar decides, but a plain bar (no Next, stop or Waiting line) says where things stand and doesn't cancel the post's wait, the rule find_next applies: 37 of 700 floor starts on 2026-11-04..11 were that. No wait outlasts cap after the last agent post, however often the bar is rewritten."""
    out, last = {}, {}
    try:
        with BOARD.open("rb") as fh:
            fh.seek(max(0, os.fstat(fh.fileno()).st_size - TAIL)); data = fh.read()
    except OSError: data = b""
    for ln in data.decode(errors="replace").splitlines():
        try: p = json.loads(ln)
        except ValueError: continue
        th = isinstance(p, dict) and (p.get("thread") or p.get("id"))
        if th in threads and AGENT.match(str(p.get("from", ""))): last[th] = p
    bars = {s.get("thread"): s for s in jsonl(STATUS) if s.get("thread") in threads and isinstance(s.get("at"), (int, float))}
    for th in threads:
        p, bar = last.get(th), bars.get(th)
        at = epoch(p.get("at")) or 0 if p else 0
        def waiting(text): return [clean(l) for l in str(text).splitlines() if clean(l).lower().startswith("waiting:")]
        bar_says = bar and bar["at"] > at and (status_line(bar) or waiting(bar.get("text", "")))
        lines = waiting(bar["text"] if bar_says else p.get("text", "") if p else "")
        if not lines: continue
        base = at or bar["at"]
        if (until := wait_holds(lines[-1], base, running, cap)): out[th] = until
    return out

WAIT_PERSON = re.compile(r"^waiting:.*\b(?:person|person's)\b", re.I)
def person_waiter(p, running=()):
    """The `person` event of moving-p2.md section 3: a person post with no @name that follows an agent's newest `Waiting: ... person ...` line in its thread names that agent,
    so the wait on the person ends when they write, whether or not they remembered the name. None for a stop, a post that names someone (a name a door picked, `routed`, is not typed, so it doesn't count), one in a system thread, an agent
    still running, or a thread whose newest agent line is anything else (the wait is over, or it was never on them)."""
    th = p.get("thread") or p.get("id")
    if p.get("from") != "person" or not th or th in SYSTEM_THREADS or "page:edit" in (p.get("tags") or []) or ((p.get("to") or []) and not p.get("routed")) or (is_stop and is_stop(p)) or re.search(r"(?<![\w.])@\w", str(p.get("text", ""))): return None
    mine = [q for q in thread_posts(th) if AGENT.match(str(q.get("from", ""))) and (epoch(q.get("at")) or 0) <= (epoch(p.get("at")) or 0)]
    if not mine: return None
    last = mine[-1]; name = str(last["from"]).lower()
    lines = [clean(l) for l in str(last.get("text", "")).splitlines() if l.strip()]
    if not lines or not WAIT_PERSON.match(lines[-1]) or name in running: return None
    return name

def find_next(r, busy=()):
    """The step a run ended on, from its newest own post and newest own status line in its thread: newest first, a Next line counts and a stop reason
    wins. A plain newest post does not cancel a Next in the bar; an older post of the run is not read, since the newer one overtook it. {line, post, since}, or None.
    A `Waiting:` in the bar that says neither who (an agent in busy) nor when doesn't cancel a Next in the post, the rule the floor applies (waits): the stop gate makes agents write one
    beside their Next, usually about the person's items, and 26 of 27 lost Next lines on 2026-11-08..10 were that."""
    all_posts, posts, said = own_said(r)
    for at, _, kind, x in said:
        got = post_line(x) if kind == "post" else status_line(x)
        if not got: continue
        if got[0] == "stop":
            if kind == "status" and got[1].lower().startswith("waiting:") and (wait_holds(got[1], at, busy) or 0) <= now().timestamp(): continue
            return None
        src = x["id"] if kind == "post" else (str(x.get("via", "")).removeprefix("post ").strip() or (posts[-1]["id"] if posts else all_posts[-1]["id"] if all_posts else None))
        return {"line": got[1], "post": src, "since": at} if src else None
    return None

def stop_file(thread): return DIR / "stop" / safe(thread)
def stop_at(thread):
    try: return float(json.loads(stop_file(thread).read_text()).get("at") or 0)
    except (OSError, ValueError, AttributeError, TypeError): return None
def hold(thread, at, post=None, why=None):
    f = stop_file(thread); f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(json.dumps({"at": max(at, stop_at(thread) or 0), "post": post, **({"why": why} if why else {})}))

# A same-day end in a stop line: "paused until the person says resume or about midnight", "until tomorrow", "until 3pm".
SHORT_END = re.compile(r"\b(?:midnight|tonight|tomorrow|noon|this (?:morning|afternoon|evening)|later today)\b|\buntil\s+\d{1,2}(?::\d{2})?\s*(?:am|pm|utc)\b", re.I)
def still_held(thread):
    """True while the stop mark holds. A mark born of an agent's `Told to stop:` bar (no person post) whose stop line named a same-day end lapses
    1.5 days on, once the bar no longer says Told to stop and the person's newest post in the thread is not a pause. A phone-relayed "pause until
    about midnight" on 2026-11-11 held fund-rsi out of every keeper, floor and continuation round for 12 days: the person never posted in the thread,
    so nothing cleared it (ad388, 2026-11-23). A person's own stop post, or a stop with no end named, still holds until the person says otherwise."""
    f = stop_file(thread)
    try: mark = json.loads(f.read_text())
    except FileNotFoundError: return False
    except (OSError, ValueError): return True
    at = float(mark.get("at") or 0) if isinstance(mark, dict) else 0
    if not isinstance(mark, dict) or mark.get("post") or now().timestamp() - at < 1.5 * 86400: return True
    why = mark.get("why")
    if why is None:
        why = next((str(r.get("text", "")) for r in jsonl(STATUS) if r.get("thread") == thread and isinstance(r.get("at"), (int, float)) and abs(r["at"] - at) < 2), "")
    stop_lines = [clean(l) for l in str(why).splitlines() if clean(l).lower().startswith("told to stop:")] or [clean(str(why))]
    if not any(SHORT_END.search(l) for l in stop_lines): return True
    lines = [clean(l) for l in str(newest_status(thread).get("text", "")).splitlines() if l.strip()]
    if lines and lines[-1].lower().startswith("told to stop:"): return True
    mine = [p for p in thread_posts(thread) if p.get("from") == "person"]
    if mine and (G.PERSON_HOLD.search(str(mine[-1].get("text", ""))) and not G.PERSON_GO.search(str(mine[-1].get("text", "")))): return True
    f.unlink(missing_ok=True)
    return False
def note_post(p, stopped):
    """Every board post, in order. A person's stop holds the thread until a later non-stop post of theirs; an agent's post never clears it."""
    if p.get("from") != "person": return
    thread, at = p.get("thread") or p.get("id"), epoch(p.get("at")) or 0
    if stopped: hold(thread, at, p.get("id"))
    elif at > (stop_at(thread) or 0): stop_file(thread).unlink(missing_ok=True)

def stopped_thread(thread, posts=None):
    """True if the person has stopped this thread. Reads the board too, so a stop in the poll before this one counts, and persists a `Told to stop:` bar."""
    posts = thread_posts(thread) if posts is None else posts
    for p in posts:  # replayed in board order, so the newest person post decides
        if p.get("from") == "person" and is_stop: note_post(p, is_stop(p))
    told = newest_status(thread)
    lines = [clean(l) for l in str(told.get("text", "")).splitlines() if l.strip()]
    if lines and lines[-1].lower().startswith("told to stop:"):
        at = told.get("at") if isinstance(told.get("at"), (int, float)) else 0
        if not any(p.get("from") == "person" and (epoch(p.get("at")) or 0) > at and not (is_stop and is_stop(p)) for p in posts): hold(thread, at, why=lines[-1])
    return still_held(thread)

def tokens(line):
    words = re.findall(r"\w+", line.lower())
    return set(words[1:] if words[:1] == ["next"] else words)
def overlap(a, b):
    a, b = tokens(a), tokens(b)
    return len(a & b) / len(a | b) if a | b else 0.0

def limits(c):
    """(hops a chain may take in a day, hops the hive may take in a day or None for no cap). Brakes against a loop, not a ration of work: the person
    said limits like the run limit get in the way (k1101), and on 2026-11-07 a cap of 120 a day dropped 106 steps that agents knew how to continue,
    each then waiting for a keeper round (thread 1107141044b9). 48 hops a day on one chain is one every 30 min round the clock: past that it is a loop."""
    k = (c or {}).get("continue_pilot") or {}
    return k.get("hops_per_chain_day", 48), k.get("per_day")

def check(intent, st, c, alive):
    """Why this continuation must not go ahead, or None. Asked before the intent is written and again right before launch."""
    n, thread = intent["name"], intent["thread"]
    posts = thread_posts(thread)
    if stopped_thread(thread, posts): return "stop"
    for who, r in (alive or {}).items():
        if who != n and r.get("thread") == thread: return "other_running"
    for who, ps in (st.get("pending") or {}).items():
        if who != n and any((q.get("thread") or q.get("id")) == thread and not q.get("continue") for q in ps): return "other_queued"
    for p in posts:
        at = epoch(p.get("at")) or 0
        if p.get("id") != intent["post"] and at > intent.get("since", 0) and AGENT.match(str(p.get("from", ""))) and \
                not (str(p.get("from", "")).lower() == n and at <= (epoch(intent.get("at", "")) or 0)): return "superseded"
    if PERSON_LINE.search(re.sub(r"`[^`]*`", "", intent["line"]).split("check:")[0]): return "person_line"
    ev = [e for e in events() if e.get("event") == "launch" and e.get("post") != intent["post"]]
    mine = [e for e in ev if e.get("chain") == intent["chain"]]
    if any(overlap(intent["line"], str(e.get("line", ""))) > OVERLAP for e in mine[-3:]): return "overlap"
    day = iso()[:10]
    per_chain, per_day = limits(c)
    if sum(str(e.get("at", ""))[:10] == day for e in mine) >= per_chain: return "budget_chain"
    if per_day and sum(str(e.get("at", ""))[:10] == day for e in ev) >= per_day: return "budget_day"
    return None

NEXT_AFTER = re.compile(r"\b(?:after:?|on or after|not before|on)\s*(\d{4}-\d{2}-\d{2})(?:[T ](\d{2}:\d{2}))?", re.I)
DEFER_MAX_D = 14   # a Next dated further out than this is held only this long: superseded() drops it sooner if anyone moves the thread
def not_before(line, t=None):
    """When a Next line says its step can run, as seconds, if that is later than t (default now); else None. Read the way the keeper reads a rung's
    after: (goals.AFTER, CHECK_ON): a day means 00:00 UTC, and only the step's own words count, not dates in its check. 45 of 654 hops on 11-14..11-21
    launched on a Next that named a later after-date; graded, 5 of 8 delivered (93% for the rest), with 15 more graded not asked and 3 quiet (aceaa, R58).
    "on DATE" counts too: "Next: on 2026-11-24 run ..." launched meta-person 3 times a day on 11-19..22 to find it not due (ac89a)."""
    t = t or now().timestamp()
    step = re.split(r"check:", line, maxsplit=1, flags=re.I)[0]
    m = NEXT_AFTER.search(step) or G.CHECK_ON.search(line)
    if not m: return short_date(step, t)
    try: when = dt.datetime.fromisoformat(f"{m.group(1)}T{(m.group(2) if m.re is NEXT_AFTER and m.group(2) else '00:00')}+00:00").timestamp()
    except ValueError: return None
    return min(when, t + DEFER_MAX_D * 86400) if when > t else None

JOB_GATE = re.compile(r"\b(?:when|once|after|until)\b[^.;]{0,120}?(?<![/\w])(?:finish(?:es|ed)?|completes?|is done|ends|exists)\b(?!`)", re.I)
JOB_TICK = re.compile(r"`([^`\s]+)`")
JOB_CAP = 4 * 3600   # a marker that never comes holds the step this long after the post, then it launches anyway
def job_marker(line):
    """The `finished` file a Next waits on, as a path that does not exist yet, or None. Read from the step's words only: "when `lead-list-deans2` finishes",
    "read `lead-list-deans3/` once `finished` exists", "when `x/finished` exists". The job dir is looked up as written, under ~ and under projects/ (one level of
    project): an unknown dir, a marker already there, or a dir with no `started` when the words don't name `finished` is no hold. 3 launches on 11-22 (ac009) woke on such a Next minutes after starting the job and could only say
    it hadn't finished: 2 graded nothing (a3a28)."""
    step = re.split(r"check:", line, maxsplit=1, flags=re.I)[0]
    if not (g := JOB_GATE.search(step)): return None
    for tok in JOB_TICK.findall(step[:g.end()]):
        tok = tok.rstrip("/")
        if tok == "finished": continue
        rel = Path(os.path.expanduser(tok)); rel = rel if rel.name == "finished" else rel / "finished"
        for b in [Path("/")] if rel.is_absolute() else [HOME, PROJECTS] + sorted(d for d in PROJECTS.glob("*") if d.is_dir()):
            f = b / rel
            if f.parent.is_dir() and f.parent != HOME and f.parent != PROJECTS:
                said = "finished" in step[:g.end()] or (f.parent / "started").exists()   # the job writes markers: a dir that never will doesn't stall the step
                return None if f.exists() or not said else str(f)
    return None

MONTHS = {m: i + 1 for i, m in enumerate(("jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"))}
SHORT_DATE = re.compile(r"\b(?:after:?|on or after|not before|until|on)\s*(?:(\d{4}-\d{2}-\d{2})|(\d{2})-(\d{2})(?![\d-])|([a-z]{3})[a-z]*\.?\s+(\d{1,2})(?!\d)|(\d{1,2})\s+([a-z]{3})[a-z]*\b)", re.I)
def short_date(step, t):
    """not_before's second reading, for the step's words only: "until 2026-11-23", "on 11-24", "after Nov 28", "after 24 Nov" (a yearless date counts only if it falls
    within 30 days back to 60 days ahead of t, so "after 3-5 rungs" is no date). 5 launches on 11-14..11-21 named a later day in these forms and woke early: a4698 "on 11-16",
    a12a7 "on 11-24", a51fe "after 11-21" and "after 11-27", a5fef "after Nov 28" (ad388, time-invariance.md 2026-11-22 table)."""
    for m in SHORT_DATE.finditer(step):
        try:
            if m.group(1): when = dt.datetime.fromisoformat(m.group(1) + "T00:00+00:00")
            else:
                mo, d = (int(m.group(2)), int(m.group(3))) if m.group(2) else (MONTHS.get(m.group(4).lower()), int(m.group(5))) if m.group(4) else (MONTHS.get(m.group(7).lower()), int(m.group(6)))
                if not mo: continue
                y = dt.datetime.fromtimestamp(t, dt.timezone.utc).year
                near = [w for w in (dt.datetime(y + k, mo, d, tzinfo=dt.timezone.utc) for k in (-1, 0, 1)) if t - 30 * 86400 < w.timestamp() < t + 60 * 86400]
                if not near: continue   # "after 3-5 rungs" is not a March date: a yearless date counts only within 30 days back to 60 ahead
                when = near[0]
        except ValueError: continue
        when = when.timestamp()
        return min(when, t + DEFER_MAX_D * 86400) if when > t else None
    return None

def chain_of(r, post):
    """(chain, hop) for a step: the chain of the launch this run came from, else a new one named for its thread and first post."""
    for e in reversed(events()):
        if e.get("event") == "launch" and e.get("ran") == r.get("name") and r.get("post") in (e.get("posts") or []): return e["chain"], int(e.get("hop") or 0) + 1
    return f"{r.get('thread')}-{post}", 1

def claim(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    try: os.close(os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY)); return True
    except FileExistsError: return False

def stated_next(r):
    """The run's newest post anywhere on the board, if it ended on a Next line, or None."""
    name, since = str(r.get("name", "")).lower(), epoch(r.get("at", "")) or 0
    mine = [p for p in tail_posts(name) if str(p.get("from", "")).lower() == name and (epoch(p.get("at")) or 0) >= since]
    return mine[-1] if mine and (post_line(mine[-1]) or ("",))[0] == "next" else None

def elsewhere_nexts(r):
    """The run's newest post in each thread other than its own (system threads aside), where that post ends on a Next line, oldest first."""
    name, since, own = str(r.get("name", "")).lower(), epoch(r.get("at", "")) or 0, r.get("thread")
    newest = {}
    for p in tail_posts(name):
        th = p.get("thread") or p.get("id")
        if str(p.get("from", "")).lower() == name and (epoch(p.get("at")) or 0) >= since and th != own and th not in SYSTEM_THREADS: newest[th] = p
    return [p for p in newest.values() if (post_line(p) or ("",))[0] == "next"]

def on_run_end(r, c, st, alive):
    """One intent at most, from the run's own thread (_on_run_end). A Next the run left in another thread gets no intent: it is logged as an
    other_thread drop, so the floor or keeper that carries it on is not the only trace (R28, 2026-11-14: a9179 ran in product-test and posted
    its ui-redesign Next there, which left no record at all, since only the run's newest post anywhere was read)."""
    _dropped.clear(); _why.clear()
    out = _on_run_end(r, c, st, alive)
    done = set(_dropped) | {(out or {}).get("post")}
    if r.get("thread") not in SYSTEM_THREADS:
        for p in elsewhere_nexts(r):
            if p["id"] not in done and not (DIR / "done" / safe(p["id"])).exists():
                drop({"thread": p.get("thread") or p.get("id"), "post": p["id"], "name": r.get("name"), "line": post_line(p)[1]}, "other_thread")
    return out

def _on_run_end(r, c, st, alive):
    """A run ended cleanly: write its intent if it ended on a Next line in a thread that takes part, or `Done:` or no line with a rung still open (carry_on). One run gives one at most, however many times it said so.
    A Next line that gets no intent is logged as a drop with the reason, so a lost step shows in the log."""
    on = enabled(r.get("thread"))
    busy = set(alive or {}) | {n for n, ps in (st.get("pending") or {}).items() if ps}
    got = (find_next(r, busy) or carry_on(r)) if on else None
    if not got:
        if r.get("thread") not in SYSTEM_THREADS and (p := stated_next(r)):
            where = p.get("thread") or p.get("id")
            drop({"thread": where, "post": p["id"], "name": r.get("name"), "line": post_line(p)[1]}, "other_thread" if where != r.get("thread") else "stop_line" if on else "off")
        return None
    seen = {"thread": r.get("thread"), "post": got["post"], "name": r.get("name"), "line": got["line"]}
    if (DIR / "done" / safe(got["post"])).exists(): drop(seen, "post_done"); return None
    if not claim(DIR / "done" / f"run-{safe(r.get('name'))}-{safe(r.get('at'))}"): drop(seen, "run_claimed"); return None
    chain, hop = chain_of(r, got["post"])
    intent = {"chain": chain, "hop": hop, "run": r.get("session"), "post": got["post"], "name": r.get("name"), "thread": r.get("thread"), "line": got["line"], "since": got["since"], "at": iso()}
    if (nb := not_before(got["line"])): intent["not_before"] = nb   # a dated step waits for its date in enqueue rather than waking an agent that can only say it isn't due
    if (wf := job_marker(got["line"])): intent["wait_file"] = wf   # a step on a running job waits for its finished marker (JOB_CAP at most)
    why = check(intent, st, c, alive)
    if not why and any(not q.get("continue") for q in (st.get("pending") or {}).get(r.get("name"), [])): why = "agent_queued"  # it wakes anyway
    if why:
        claim(DIR / "done" / safe(got["post"])); drop(intent, why); return None
    DIR.mkdir(parents=True, exist_ok=True)
    tmp = DIR / f".{safe(chain)}.tmp"; tmp.write_text(json.dumps(intent)); tmp.replace(DIR / f"{safe(chain)}.json")
    record("intent", **{k: intent[k] for k in ("chain", "hop", "thread", "post", "name", "line", "not_before", "wait_file") if k in intent})
    return intent

def enqueue(st, c, alive, stays_on):
    """Turn each waiting intent into one pending post, once: the done marker makes a restart's replay a no-op. It waits while its producer is running.
    A judge's intent is marked by its own key (the run end it judged), so a step the run itself stated from the same post stays apart; a wait holds until its not_before."""
    out = []
    for f in sorted(DIR.glob("*.json")):
        try: k = json.loads(f.read_text())
        except (OSError, ValueError): continue
        mark = DIR / "done" / safe(k.get("key") or k.get("post"))
        if mark.exists(): f.unlink(missing_ok=True); continue
        if k["name"] in (alive or {}): continue
        if (k.get("not_before") or 0) > now().timestamp(): continue
        if k.get("wait_file") and not Path(k["wait_file"]).exists() and now().timestamp() < (k.get("since") or 0) + JOB_CAP: continue
        if k.get("judge") and not judge_acts():
            claim(mark); drop(k, "judge_off"); f.unlink(missing_ok=True); continue
        if (why := check(k, st, c, alive)):
            claim(mark); drop(k, why); f.unlink(missing_ok=True); continue
        if not claim(mark): f.unlink(missing_ok=True); continue
        q = {"id": k["post"], "thread": k["thread"], "from": "starter", "continue": {x: k[x] for x in ("chain", "hop", "line", "post", "since", "at", "run", "judge") if x in k}}
        stays_on(st, c, k["name"], q)
        st.setdefault("pending", {}).setdefault(k["name"], []).append(q)
        f.unlink(missing_ok=True)
        record("enqueue", chain=k["chain"], hop=k["hop"], thread=k["thread"], post=k["post"], name=k["name"], line=k["line"], **({"judge": k["judge"]["verdict"]} if k.get("judge") else {}))
        out.append(q)
    return out

def before_launch(n, ps, st, c, alive):
    """The posts about to wake n, without any continuation that may no longer go. Their source was consumed at enqueue, so a drop here is final."""
    keep = []
    for q in ps:
        k = q.get("continue")
        if not k: keep.append(q); continue
        intent = {**k, "name": n, "thread": q.get("thread")}
        if (why := check(intent, st, c, alive)): drop(intent, why)
        else: keep.append(q)
    return keep

def launched(who, ps):
    for q in ps:
        if (k := q.get("continue")):
            record("launch", ran=who, model=q.get("model"), chain=k["chain"], hop=k["hop"], thread=q.get("thread"), post=k["post"], line=k["line"], source_run=k.get("run"), posts=[p["id"] for p in ps],
                   **({"judge": k["judge"].get("verdict")} if k.get("judge") else {}))

def ladder_brief(thread):
    """What `hive goals --brief` prints for this thread, also for a project whose ladder heading names no thread (the CLI finds no row for those)."""
    got = enabled(thread)
    if not got: return ""
    project, path, txt = got
    return G.brief(G.with_ladder({"name": project, "thread": thread}, {"path": str(path), "ladder": G.ladder(txt)})).removeprefix("@new ")

def ladder_path(thread):
    """The goals.md this thread's ladder lives in, written from home, or "the project's goals.md" when none is found. A resumed run used
    to get only the words "the project's goals.md" (the ladder brief goes to heirs alone); hive-meta time-invariance.md section 6."""
    got = enabled(thread)
    if not got: return "the project's goals.md"
    try: return "~/" + str(Path(got[1]).resolve().relative_to(Path.home().resolve()))
    except (ValueError, OSError): return str(got[1])

def wake_note(k, thread, heir):
    if (j := k.get("judge")): return judge_note(k, j, thread, heir)
    head = (f"\n\nThis is a continuation (chain {k['chain']}, hop {k['hop']}). " + ("The agent before you" if heir else "Your last step") + f" (post {k['post']}) ended on: {k['line']}\n"
            "That line is your job: do the step and run its check, unless the thread shows it was overtaken, in which case say so and stop. "
            "End your result post with the next `Next: <one concrete step, with its check>` if there is one you can take, or with a `Done:`, `Blocked:`, `Needs the person:` or `Waiting:` line if not; when that post is your last act, send it with `hive post --end` so the run stops there.\n"
            f"Before you write that line, re-read the ladder in {ladder_path(thread)} against what you just learned: tick what is done, and add, split or strike rungs if the goal "
            "has moved (the person, 2026-10-14 and k0492: goals get re-evaluated as work gets done, by the agent that did it, not by a round that comes later). "
            "Say in the post which rungs changed, on a line starting `Rungs:` (`Rungs: none changed, because ...` when none did).")
    return head + (f"\n\nThe ladder this thread works from, for what comes after that step:\n{b}" if heir and (b := ladder_brief(thread)) else "")

def judge_note(k, j, thread, heir):
    """The wake the stop judge gave: the run stopped, and a separate model read the thread and its goal and said the work was not finished."""
    who = "The agent before you" if heir else "Your last run"
    if j.get("verdict") == "wait":
        body = (f"{who} (post {k['post']}) stopped to wait. The stop judge granted a wait of {j.get('minutes')} min, which is now over, and nothing has been posted in this thread since. "
                f"What it was waiting for: {k['line'].removeprefix('Next: ')}\nThe judge's reason: {j.get('why')}\n"
                "Check whether what was awaited has arrived (the board, the job, the file). If it has, carry the work on. If not, post one line saying what you checked and when to look again, and stop.")
    else:
        body = (f"{who} (post {k['post']}) stopped. The stop judge, a separate model shown the goal, its open rungs and what was posted, says the work is not finished. "
                f"The step it named: {k['line'].removeprefix('Next: ')}\nIts reason: {j.get('why')}\n"
                "Do that step and run its check, unless the thread shows it was overtaken or is not yours to take.")
    tail = ("\nIf you think the judge is wrong (the work is done, or the step waits on the person or someone else), post one line starting `Judge missed:` with why, and stop: that is logged, and it is how the judge gets corrected. "
            "Otherwise end your result post with `Next: <one concrete step, with its check>` if there is one you can take, or with a `Done:`, `Blocked:`, `Needs the person:` or `Waiting:` line if not; "
            "when that post is your last act, send it with `hive post --end` so the run stops there.")
    return f"\n\nThis is a stop-judge wake (chain {k['chain']}, hop {k['hop']}). " + body + tail + (f"\n\nThe ladder this thread works from:\n{b}" if heir and (b := ladder_brief(thread)) else "")

# --- the stop judge (judge.py): the starter asks it at a clean run end that nothing else carries on, and never waits for it
JUDGE_SKIP = {"stop", "other_running", "other_queued", "superseded", "overlap", "budget_chain", "budget_day", "post_done", "run_claimed", "agent_queued"}

def judge_trial(t=None, path=None):
    """The live trial's settings, or None: no file, unreadable, or outside its from..until."""
    try: d = json.loads(Path(path or JUDGE_TRIAL).read_text())
    except (OSError, ValueError): return None
    if not isinstance(d, dict): return None
    t = t or now().timestamp()
    if (epoch(d.get("from", "")) or 0) > t or t >= (epoch(d.get("until", "")) or 0): return None
    return d

def judge_acts():
    """Whether a judge's wake may go: the trial is on and out of shadow."""
    tr = judge_trial()
    return bool(tr) and not tr.get("shadow", True)

def ask_judge(r, c, st, alive, held=False, skip=False):
    """After on_run_end gave no intent: hand the run end to judge.py, detached, so a slow model never holds the starter up. Returns the snapshot path, or None.
    Not for a warden or free agent (skip), a thread its tier holds (held), a system thread, or a step on_run_end dropped because a stop or another agent has it."""
    name, thread = str(r.get("name") or "").lower(), r.get("thread")
    if skip or held or not name or not thread or thread in SYSTEM_THREADS or not judge_trial(): return None
    if set(_why) & JUDGE_SKIP or set_off(thread): return None
    pending = {n: sorted({q.get("thread") or q.get("id") for q in ps if not q.get("continue")}) for n, ps in (st.get("pending") or {}).items()}
    snap = {"run": {k: r.get(k) for k in ("name", "session", "at", "post", "thread", "from", "model")}, "alive": {n: x.get("thread") for n, x in (alive or {}).items()},
            "pending": {n: ts for n, ts in pending.items() if ts}, "reasons": list(_why)}
    (JUDGE_DIR / "snap").mkdir(parents=True, exist_ok=True)
    path = JUDGE_DIR / "snap" / f"{safe(name)}-{safe(r.get('at'))}.json"
    path.write_text(json.dumps(snap))
    env = {k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_THREAD")}
    with (JUDGE_DIR / "run.log").open("a") as err:
        subprocess.run(["setsid", "--fork", sys.executable, "-I", str(Path(__file__).resolve().parent / "judge.py"), "run", str(path)],
                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=err, env=env, cwd=str(HOME), timeout=20)
    return path

# --- hive continue: what it did, and whether it helped. Regexes from kb/why-hive-agents-stop-evidence-2026-11-02-06-meta-moving.md.
ELIGIBLE = re.compile(r"^(next|next rung|next step|left|what is left|still left|remaining|todo|to do)\b.*:", re.I | re.M)
SUBST = re.compile(r"\[x\]|ticked|landed|commit", re.I)
MARK = re.compile(r"taking|i['’]ll take|starting", re.I)
TICK_ID = re.compile(r"\b(\d{2})(\d{2})\d{4}[0-9a-f]{4}\b")
BASE_START, BASE_END = "2026-11-03", "2026-11-06T07:32:00"  # the window moving.md section 6 was measured over
BASE_THREADS = ["rsi-loop", "product-test", "member-hives", "improve-survey", "fund-rsi", "career-plan", "hive-stars", "ui-redesign", "hive-always-improving", "kb-search"]

def eligible(text): return bool(ELIGIBLE.search(text)) or "next rung" in text.lower() or "Next:" in text
def substantive(text): return len(text) > 300 or bool(SUBST.search(text))
def pct(v, q):
    s = sorted(v)
    if not s: return float("nan")
    i = (len(s) - 1) * q; lo, hi = int(i), min(int(i) + 1, len(s) - 1)
    return s[lo] + (s[hi] - s[lo]) * (i - lo)

def goal_files(only=None):
    return [Path(g) for g in sorted(glob.glob(str(PROJECTS / "*" / "goals.md"))) if only is None or Path(g).parent.name in only]

def ladder_files(only=None):
    """Each goals.md, plus the ladder-done.md beside it where its ticked rungs were moved (2026-11-17, rsi-loop): ticks count from both."""
    return [f for g in goal_files(only) for f in (g, g.parent / "ladder-done.md") if f is g or f.exists()]

def tick_lines(only=None):
    out = []
    for g in ladder_files(only):
        try: out += [l for l in g.read_text(errors="replace").splitlines() if l.startswith("- [x]")]
        except OSError: pass
    return out

def list_rows():
    ev = events(); board = jsonl(BOARD); grades = {(o.get("post"), o.get("name")): o.get("grade") for o in jsonl(OUTCOMES)}
    ticks = tick_lines(); rows = defaultdict(list)
    for e in ev:
        if e.get("event") != "launch": continue
        src = next((p for p in board if p.get("id") == e.get("post")), None)
        t0, went = epoch(src.get("at")) if src else None, epoch(e.get("at")) or 0
        mine = [p for p in board if p.get("thread") == e.get("thread") and p.get("from") == e.get("ran") and (epoch(p.get("at")) or 0) >= went]
        first = next((p for p in mine if substantive(str(p.get("text", ""))) or any(p["id"] in l for l in ticks)), None)
        rows[e["chain"]].append({"hop": e.get("hop"), "ran": e.get("ran"), "at": e.get("at"), "line": e.get("line"), "post": e.get("post"), "model": e.get("model"),
                                 "minutes": round((epoch(first["at"]) - t0) / 60, 1) if first and t0 else None,
                                 "rungs": sum(any(p["id"] in l for l in ticks) for p in mine), "grade": grades.get((e.get("post"), e.get("ran"))), "judge": e.get("judge")})
    return rows

def cmd_list(a):
    rows = list_rows()
    if a.json: print(json.dumps(rows, indent=1)); return
    if not rows: print("no chains yet"); return
    for chain, hops in rows.items():
        print(f"{chain}: {len(hops)} hop{'s' * (len(hops) != 1)}")
        for h in hops:
            mins = "-" if h["minutes"] is None else f"{h['minutes']} min"
            print(f"  {h['hop']:>2} {h['ran']:<8} {str(h['at'])[:16]}  to first result {mins:<10} rungs {h['rungs']}  grade {h['grade'] or '-'}  {'judge:' + h['judge'] + ' ' if h.get('judge') else ''}{str(h['line'])[:70]}")

def focus_threads(all_threads):
    """The focus set of the baseline: the explicit threads, plus names on `thread` lines or in `## Ladder` of any goals.md, that exist on the board."""
    found = set()
    for g in goal_files():
        in_ladder = False
        for line in g.read_text(errors="replace").splitlines():
            if line.startswith("## "): in_ladder = line.strip().lower().startswith("## ladder")
            if in_ladder or re.search(r"thread", line, re.I):
                found.update(re.findall(r"`([a-z][a-z0-9-]+)`", line))
                found.update(t.lower() for t in re.findall(r"threads?[ :]+`?([a-z][a-z0-9-]+)`?", line, re.I))
    return set(BASE_THREADS) | {t for t in found if t in all_threads}

def pilot_threads(all_threads):
    return {t for t in all_threads if t and enabled(t)}

def cmd_stats(a):
    start = a.since or (BASE_START if a.baseline else None)
    end = a.until or (BASE_END if a.baseline else None)
    ev = events()
    if not start:
        first = [e["at"] for e in ev if e.get("event") == "launch"]
        start = min(first) if first else iso()
    board = [p for p in jsonl(BOARD) if epoch(p.get("at"))]
    all_threads = {p.get("thread") for p in board}
    focus = focus_threads(all_threads) if a.baseline else pilot_threads(all_threads)
    t0, t1 = epoch(start), epoch(end) if end else float("inf")
    by = defaultdict(list)
    for p in sorted((p for p in board if p.get("thread") in focus and t0 <= epoch(p["at"]) <= t1), key=lambda p: (epoch(p["at"]), p["id"])): by[p["thread"]].append(p)
    is_ag = lambda p: bool(AGENT.match(str(p.get("from", ""))))
    steps = []
    for ps in by.values():
        for i, p in enumerate(ps):
            if is_ag(p) and eligible(str(p.get("text", ""))):
                q = next((x for x in ps[i + 1:] if is_ag(x) and substantive(str(x.get("text", "")))), None)
                steps.append((p, q))
    near = [(p, q, (epoch(q["at"]) - epoch(p["at"])) / 60) for p, q in steps if q and (epoch(q["at"]) - epoch(p["at"])) <= 86400]
    def dist(name, v):
        print(f"  {name}: n={len(v)}" + (f" median={pct(v, .5):.1f} p25={pct(v, .25):.1f} p75={pct(v, .75):.1f} p90={pct(v, .9):.1f} min" if v else ""))
    print(f"{'baseline' if a.baseline else 'pilot'} {start}..{end or 'now'}, {len(focus)} threads")
    print(f"eligible steps: {len(steps)}, with a substantive follow-up within 24 h: {len(near)}")
    dist("minutes to next substantive post", [m for *_, m in near])
    dist("same agent", [m for p, q, m in near if p["from"] == q["from"]])
    dist("different agent", [m for p, q, m in near if p["from"] != q["from"]])
    only = None if a.baseline else {enabled(t)[0] for t in focus}
    days, undated, bang = Counter(), 0, 0
    for g in ladder_files(only):
        for line in g.read_text(errors="replace").splitlines():
            if line.startswith("- [!]"): bang += 1
            elif line.startswith("- [x]"):
                m = TICK_ID.search(line)
                if m: days[f"{m.group(1)}-{m.group(2)}"] += 1
                else: undated += 1
    print("rungs ticked per day:", ", ".join(f"{d} {n}" for d, n in sorted(days.items())[-7:]) or "none", f"| {undated} undated | {bang} [!]")
    pairs, threads = 0, set()
    for th, ps in by.items():
        marked = [p for p in ps if is_ag(p) and MARK.search(str(p.get("text", ""))[:200])]
        for i, p in enumerate(marked):
            for q in marked[i + 1:]:
                if epoch(q["at"]) - epoch(p["at"]) > 600: break
                if q["from"] != p["from"]: pairs += 1; threads.add(th)
    print(f"duplicate launches: {pairs} pairs in {len(threads)} threads")
    if not a.baseline:
        cnt = Counter(e.get("event") for e in ev)
        jl = sum(1 for e in ev if e.get("event") == "launch" and e.get("judge"))
        print(f"intents {cnt['intent']}, enqueued {cnt['enqueue']}, launched {cnt['launch']} ({jl} from the stop judge); drops by reason:", dict(Counter(e.get("reason") for e in ev if e.get("event") == "drop")) or "none")
        graded = [h["grade"] for hops in list_rows().values() for h in hops if h["grade"]]
        if graded: print(f"delivered share of chained runs: {sum(g == 'delivered' for g in graded)}/{len(graded)}")

def main():
    ap = argparse.ArgumentParser(prog="hive continue", description=__doc__.splitlines()[0])
    sp = ap.add_subparsers(dest="cmd", required=True)
    l = sp.add_parser("list", help="chains, their hops, who ran each, minutes to its first result, rungs, grade"); l.add_argument("--json", action="store_true")
    s = sp.add_parser("stats", help="minutes from a stated step to the next substantive post, rungs per day, duplicate launches, drops by reason")
    s.add_argument("--baseline", action="store_true", help="the focus threads over the window of moving.md section 6"); s.add_argument("--since"); s.add_argument("--until")
    a = ap.parse_args()
    (cmd_list if a.cmd == "list" else cmd_stats)(a)

if __name__ == "__main__": main()
