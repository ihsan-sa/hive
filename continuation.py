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
import argparse, datetime as dt, glob, json, os, re, sys
from collections import Counter, defaultdict
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import goals as G

HOME = Path.home()
DIR, BOARD, STATUS, PROJECTS, CONFIG, OUTCOMES = HOME / ".hive" / "continue", HOME / "board.jsonl", HOME / ".hive" / "thread-status.jsonl", HOME / "projects", Path(__file__).resolve().parent / "config.json", HOME / "outcomes.jsonl"
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")
PERSON_LINE = re.compile(r"(?<![\w./-])(?:for you|person)\b(?!\.\w| would| items?\b)", re.I)   # not a file or flag named for-person.md or --person, 13 of 18 drops from 2026-11-09
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
def drop(intent, reason):
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
    for g in sorted(PROJECTS.glob("*/goals.md")):
        if g.parent.name.startswith("_"): continue
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        m = G.LADDER.search(txt)
        if not m: continue
        rest = txt[m.end():]
        end = re.search(r"^#{1,2}\s", rest, re.M)
        if not re.search(r"^Continue:\s*on\b", rest[:end.start()] if end else rest, re.M | re.I): continue
        names = {t.lower() for t in G.THREAD.findall(txt)} | {g.parent.name}
        if str(thread).lower() in names: return g.parent.name, g, txt
    return None

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
def wait_until(line, base):
    """The time a `Waiting:` line names, as seconds, or None. A clock time is the next one after base (the host clock unless it says UTC)."""
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
    ("for the next keeper round"), or one on an agent that isn't coming, carries on now. Newest of the last agent post and the status bar decides; no wait outlasts cap after the last agent post, however often the bar is rewritten."""
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
        text = bar["text"] if bar and bar["at"] > at else p.get("text", "") if p else ""
        lines = [clean(l) for l in str(text).splitlines() if clean(l).lower().startswith("waiting:")]
        if not lines: continue
        base = at or bar["at"]
        if (until := wait_holds(lines[-1], base, running, cap)): out[th] = until
    return out

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
def hold(thread, at, post=None):
    f = stop_file(thread); f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(json.dumps({"at": max(at, stop_at(thread) or 0), "post": post}))
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
        if not any(p.get("from") == "person" and (epoch(p.get("at")) or 0) > at and not (is_stop and is_stop(p)) for p in posts): hold(thread, at)
    return stop_file(thread).exists()

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

def on_run_end(r, c, st, alive):
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
    why = check(intent, st, c, alive)
    if not why and any(not q.get("continue") for q in (st.get("pending") or {}).get(r.get("name"), [])): why = "agent_queued"  # it wakes anyway
    if why:
        claim(DIR / "done" / safe(got["post"])); drop(intent, why); return None
    DIR.mkdir(parents=True, exist_ok=True)
    tmp = DIR / f".{safe(chain)}.tmp"; tmp.write_text(json.dumps(intent)); tmp.replace(DIR / f"{safe(chain)}.json")
    record("intent", **{k: intent[k] for k in ("chain", "hop", "thread", "post", "name", "line")})
    return intent

def enqueue(st, c, alive, stays_on):
    """Turn each waiting intent into one pending post, once: the done marker makes a restart's replay a no-op. It waits while its producer is running."""
    out = []
    for f in sorted(DIR.glob("*.json")):
        try: k = json.loads(f.read_text())
        except (OSError, ValueError): continue
        if (DIR / "done" / safe(k.get("post"))).exists(): f.unlink(missing_ok=True); continue
        if k["name"] in (alive or {}): continue
        if (why := check(k, st, c, alive)):
            claim(DIR / "done" / safe(k["post"])); drop(k, why); f.unlink(missing_ok=True); continue
        if not claim(DIR / "done" / safe(k["post"])): f.unlink(missing_ok=True); continue
        q = {"id": k["post"], "thread": k["thread"], "from": "starter", "continue": {x: k[x] for x in ("chain", "hop", "line", "post", "since", "at", "run")}}
        stays_on(st, c, k["name"], q)
        st.setdefault("pending", {}).setdefault(k["name"], []).append(q)
        f.unlink(missing_ok=True)
        record("enqueue", chain=k["chain"], hop=k["hop"], thread=k["thread"], post=k["post"], name=k["name"], line=k["line"])
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
            record("launch", ran=who, model=q.get("model"), chain=k["chain"], hop=k["hop"], thread=q.get("thread"), post=k["post"], line=k["line"], source_run=k.get("run"), posts=[p["id"] for p in ps])

def ladder_brief(thread):
    """What `hive goals --brief` prints for this thread, also for a project whose ladder heading names no thread (the CLI finds no row for those)."""
    got = enabled(thread)
    if not got: return ""
    project, path, txt = got
    return G.brief(G.with_ladder({"name": project, "thread": thread}, {"path": str(path), "ladder": G.ladder(txt)})).removeprefix("@new ")

def wake_note(k, thread, heir):
    head = (f"\n\nThis is a continuation (chain {k['chain']}, hop {k['hop']}). " + ("The agent before you" if heir else "Your last step") + f" (post {k['post']}) ended on: {k['line']}\n"
            "That line is your job: do the step and run its check, unless the thread shows it was overtaken, in which case say so and stop. "
            "End your result post with the next `Next: <one concrete step, with its check>` if there is one you can take, or with a `Done:`, `Blocked:`, `Needs the person:` or `Waiting:` line if not.\n"
            "Before you write that line, re-read the ladder in the project's goals.md against what you just learned: tick what is done, and add, split or strike rungs if the goal "
            "has moved (the person, 2026-10-14 and k0492: goals get re-evaluated as work gets done, by the agent that did it, not by a round that comes later). Say in the post which rungs changed.")
    return head + (f"\n\nThe ladder this thread works from, for what comes after that step:\n{b}" if heir and (b := ladder_brief(thread)) else "")

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

def tick_lines(only=None):
    out = []
    for g in goal_files(only):
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
                                 "rungs": sum(any(p["id"] in l for l in ticks) for p in mine), "grade": grades.get((e.get("post"), e.get("ran")))})
    return rows

def cmd_list(a):
    rows = list_rows()
    if a.json: print(json.dumps(rows, indent=1)); return
    if not rows: print("no chains yet"); return
    for chain, hops in rows.items():
        print(f"{chain}: {len(hops)} hop{'s' * (len(hops) != 1)}")
        for h in hops:
            mins = "-" if h["minutes"] is None else f"{h['minutes']} min"
            print(f"  {h['hop']:>2} {h['ran']:<8} {str(h['at'])[:16]}  to first result {mins:<10} rungs {h['rungs']}  grade {h['grade'] or '-'}  {str(h['line'])[:70]}")

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
    for g in goal_files(only):
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
        print(f"intents {cnt['intent']}, enqueued {cnt['enqueue']}, launched {cnt['launch']}; drops by reason:", dict(Counter(e.get("reason") for e in ev if e.get("event") == "drop")) or "none")
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
