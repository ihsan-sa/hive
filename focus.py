#!/usr/bin/env python3
"""hive focus: foci, the person's view of the work (projects/ui-redesign/human-workflow.md, 2026-11-07).

Agents work by thread; a focus is an abstraction for the person: a label a thread carries and a summary agents write by hand.
The dashboard's home is one card per focus (Now, Needs you, Lately, Threads) and a focus page; the summary is the only text on
them that an agent writes, so it stays in plain words: no rung ids, post ids, paths or test names (depth 1 of the four depths).

  hive focus list [--json]              every focus in order: the person's priorities first (kb/priorities.md), then added ones
  hive focus new ID "name" [--off]      add a focus (an agent may; no sub-foci: that is a thread in the focus). --off retires it
  hive focus tag THREAD FOCUS [--off]   put a thread under a focus (--off: take it out). Without a tag a thread is in the focus
                                      whose own thread it is or came from (the dashboard follows parents); the rest is Misc
  hive focus link A B [--off]           two foci that depend on each other (the GTM focus needs the mech lab's result)
  hive focus relate FOCUS thread|agent|file X --why "..." [--off]   a thread, agent or file that bears on a focus without being in
                                      it (a result post that touched another focus: relate it there); the focus's json lists them as relations
  hive focus set FOCUS <<'X'            the summary, markdown on stdin: `## Now` bullets (what is being done, who, where: @agent and
                                      #thread become pills), `## Lately` bullets (decisions, turns, what changed) and, only when a
                                      decision really needs the person, `## Needs you` bullets (one line each, with the #thread):
                                      the home card shows those and nothing else under Needs you. Agents decide by default and say
                                      `Decided:` in the result instead. Plain words, high level relative to the focus. Lint runs
                                      and prints what it flags; it never refuses
  hive focus show FOCUS                 the summary as set
  hive focus aim FOCUS "text"           one line on what the focus is for (the name isn't always enough: the person, 2026-11-07);
                                      shown under the name on the home card and at the top of the focus page. Without one, the
                                      text after the thread on the focus's priorities line is used
  hive focus check add FOCUS "text" | list FOCUS | done ID... | untick ID...   the focus page's to-do list
  hive focus lint [FOCUS] [--json]      what reads wrong: ids, paths, rung words, test names, long bullets, a summary older than
                                      the focus's newest result post (stale), a focus with threads and no summary (missing)
  hive focus order ID ID ...            the order foci show in (rail, home, this list): the listed ones first, the rest after in
                                      their usual order; the newest order wins (the dash's drag writes it). --reset: back to default
  hive focus moved [--since 2d]         threads moved between foci lately, newest first (from, to, who, when; up to a week back);
                                      the dashboard's home shows the same as Recently moved
  hive focus json                       everything, for the dashboard (/api/foci)

The duty (time-invariant, no cleanup pulses): the agent that posts a result in a thread that is in a focus updates the focus's
summary in the same run. The inbox hook says so once when it sees the result post and an older summary.
Records go to ~/.hive/focus.jsonl, append-only: {kind: focus|tag|link|relation|summary|aim|check|check_done|check_untick|file|order, ..., from, at}.
"""
import argparse, datetime as dt, json, os, re, sys, time
from pathlib import Path

HOME = Path.home()
CORE, STATE, KB = HOME / "core", HOME / ".hive", HOME / "kb"
FILE, BOARD, STATUS, LOG = STATE / "focus.jsonl", HOME / "board.jsonl", STATE / "thread-status.jsonl", HOME / "log.jsonl"
ID = re.compile(r"[a-z][a-z0-9-]{1,40}")
THREAD = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.:-]{0,100}")
FOCUS_LINE = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`\)?[,:]?[ \t]*([^\n]*)", re.M)   # goals.py's FOCUS, plus the rest of the line (a default aim)
RESULT = re.compile(r"^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*\S", re.I)
MISC = "misc"
RELATE = ("thread", "agent", "file")   # what hive focus relate joins to a focus
MAX_SUMMARY, MAX_BULLET = 2500, 220
WINDOWS = {"day": 86400, "week": 7 * 86400}   # the home card's gauge: each focus's share of the hive's work over the last day and week
# What reads as agent talk on the person's pages. Each: (name, regex, what to do instead).
JARGON = [
    ("post id", re.compile(r"\b[0-9a-f]{12}\b"), "say what the post said, or drop it"),
    ("path", re.compile(r"(?:^|[\s(`])(?:~/|/home/|[\w.-]+/)[\w./-]+\.(?:py|js|mjs|md|sh|json|html|css|ts|txt|yaml|yml|toml)\b"), "name the thing in words, not its file"),
    ("rung id", re.compile(r"\b(?:R\d[a-z]?|rung\s+\w+|prop\d+|[A-Z]\d\.\d)\b"), "say what the step does"),
    ("test name", re.compile(r"\b(?:drive|test)_\w+(?:\.mjs|\.py)?\b"), "say what was checked"),
    ("agent code", re.compile(r"(?<![@\w])[a-f]\d[0-9a-f]{3}\b"), "write it as @name so it becomes a pill, or leave the agent out"),
    ("tool talk", re.compile(r"\b(?:PR\s*#?\d+|commit\s+[0-9a-f]{6,}|stdin|regex|jsonl|API|endpoint|refactor)\b"), "say what it means for the work"),
]

def now(): return time.time()
def who(): return os.environ.get("HIVE_NAME", "anon")

def records():
    out = []
    try: lines = FILE.read_text().splitlines()
    except OSError: return out
    for ln in lines:
        try: r = json.loads(ln)
        except ValueError: continue
        if isinstance(r, dict) and r.get("kind"): out.append(r)
    return out

def append(rec):
    import fcntl
    rec = {**rec, "from": rec.get("from") or who(), "at": rec.get("at") or now()}
    STATE.mkdir(parents=True, exist_ok=True)
    with open(FILE, "a") as f:
        fcntl.flock(f, fcntl.LOCK_EX); f.write(json.dumps(rec) + "\n")
    return rec

def priorities(text=None):
    """The person's focuses from kb/priorities.md, in their order: [{id, name, n, thread}], id = the focus's own thread."""
    if text is None:
        try: text = (KB / "priorities.md").read_text(errors="replace")
        except OSError: return []
    seen, out = set(), []
    for n, title, th, rest in FOCUS_LINE.findall(text):
        th = th.strip().lower()
        if th in seen: continue
        seen.add(th); out.append({"id": th, "name": title.strip(), "n": int(n), "thread": th, "aim": re.sub(r"\s*\[[^\]]*\]", "", rest).strip(" ,;:.")[:200] or None})
    return out

def parse_summary(text):
    """`## Now`, `## Lately` and `## Needs you` bullets out of the summary's markdown; a bullet-less part is kept as one bullet."""
    parts, cur = {"now": [], "lately": [], "needs": []}, None
    for ln in (text or "").splitlines():
        s = ln.strip()
        if not s: continue
        m = re.match(r"^(?:#{1,3}\s*)?(now|currently|lately|recent(?:ly)?.*|evolution.*|needs? (?:you|me|the person)|decisions?|for you)\s*:?\s*$", s, re.I)
        if m: cur = "now" if m.group(1).lower() in ("now", "currently") else "needs" if m.group(1).lower().startswith(("need", "decision", "for")) else "lately"; continue
        if cur is None: cur = "now"
        parts[cur].append(re.sub(r"^[-*•]\s+", "", s))
    return parts

def board_posts():
    out = []
    try: lines = BOARD.read_text(errors="replace").splitlines()
    except OSError: return out
    for ln in lines:
        try: p = json.loads(ln)
        except ValueError: continue
        if isinstance(p, dict) and p.get("thread"): out.append(p)
    return out

HASH = re.compile(r"[a-z][0-9a-f]{4,}")  # an agent's name (hive's HASH_NAME)

def last_agent(posts, thread):
    """The agent who last posted in `thread` (board order); when none has, an agent running on the thread (its run file), so a
    copy passed on there doesn't start a second one beside it; else None."""
    last = None
    for p in posts:
        if p.get("thread") == thread and HASH.fullmatch(str(p.get("from") or "").lower()): last = str(p["from"]).lower()
    if last: return last
    for f in sorted((HOME / "run").glob("*.json")):
        try: r = json.loads(f.read_text()); os.kill(int(r["pid"]), 0)
        except (OSError, ValueError, KeyError, TypeError): continue
        if r.get("thread") == thread and HASH.fullmatch(str(r.get("name") or "").lower()): return str(r["name"]).lower()
    return None

def keeper(f, posts):
    """Who keeps a focus, so a post naming the focus reaches someone (the person, 110717432468): the agent who wrote its latest
    summary, unless another agent posted in its own thread since; None when nobody has."""
    s = f.get("summary") or {}
    best = (ts(s.get("at")), str(s["from"]).lower()) if HASH.fullmatch(str(s.get("from") or "").lower()) else (0.0, None)
    th = f.get("thread") or f["id"]
    for p in posts:  # a post passed on into the thread, or one asking another agent there, doesn't make its author the keeper
        if p.get("thread") != th or p.get("relay_of") or not HASH.fullmatch(str(p.get("from") or "").lower()) or ts(p.get("at")) < best[0]: continue
        if any(HASH.fullmatch(str(t).lstrip("@").lower()) and str(t).lstrip("@").lower() != str(p["from"]).lower() for t in (p.get("to") or [])): continue
        best = (ts(p.get("at")), str(p["from"]).lower())
    return best[1]

def ts(at):
    if isinstance(at, (int, float)): return float(at)  # records carry time.time(); posts an ISO string
    try: return dt.datetime.fromisoformat(str(at).replace("Z", "+00:00")).timestamp()
    except (TypeError, ValueError): return 0.0

def state(recs=None, pri=None, posts=None):
    """Everything resolved: {foci: [...], tags: {thread: focus}, order: [ids]}. A focus: {id, name, n?, thread?, own, aim, links, summary, checks, threads, files}."""
    recs = records() if recs is None else recs
    foci, order = {}, []
    for f in (priorities() if pri is None else pri):
        foci[f["id"]] = {"aim": None, **f, "own": True, "links": [], "relations": [], "summary": None, "checks": [], "threads": [], "files": []}; order.append(f["id"])
    for r in recs:
        if r["kind"] == "focus":
            fid = r.get("id")
            if not fid: continue
            if r.get("off"):
                if fid in foci and not foci[fid].get("own"): del foci[fid]; order.remove(fid)
            elif fid not in foci:
                foci[fid] = {"id": fid, "name": r.get("name") or fid, "thread": r.get("thread"), "own": False, "aim": None, "links": [], "relations": [], "summary": None, "checks": [], "threads": [], "files": [], "made": r.get("at")}; order.append(fid)
            else: foci[fid]["name"] = r.get("name") or foci[fid]["name"]
    tags = {}
    for r in recs:
        if r["kind"] == "tag" and r.get("thread"):
            if r.get("off"): tags.pop(r["thread"], None)
            elif r.get("focus") in foci: tags[r["thread"]] = r["focus"]
    moved = moves(recs, foci)
    links = set()
    for r in recs:
        if r["kind"] == "link" and r.get("a") in foci and r.get("b") in foci:
            k = tuple(sorted((r["a"], r["b"])))
            if r.get("off"): links.discard(k)
            else: links.add(k)
    for a, b in links: foci[a]["links"].append(b); foci[b]["links"].append(a)
    rel = {}   # (focus, kind, id) -> the newest relation row; --off removes it
    for r in recs:
        if r["kind"] == "relation" and r.get("focus") in foci and r.get("what") in RELATE and r.get("target"):
            k = (r["focus"], r["what"], r["target"])
            if r.get("off"): rel.pop(k, None)
            else: rel.pop(k, None); rel[k] = {"kind": r["what"], "id": r["target"], "why": r.get("why") or "", "from": r.get("from"), "at": r.get("at")}
    for (fid, _, _), x in rel.items(): foci[fid]["relations"].append(x)
    for r in recs:
        if r["kind"] == "aim" and r.get("focus") in foci: foci[r["focus"]]["aim"] = (r.get("text") or "").strip() or None
    for r in recs:
        if r["kind"] == "summary" and r.get("focus") in foci:
            prev = foci[r["focus"]]["summary"] or {}
            parts = parse_summary(r.get("text"))
            # needs_at[i]: when the i-th Needs you bullet first showed in an unbroken run of summaries, so a rewrite doesn't make an old
            # ask look new (the person, 110813526b10: a needs-you that sits a day on fast work is likely obsolete).
            was = {" ".join(t.split()).lower(): a for t, a in zip(prev.get("needs") or [], prev.get("needs_at") or [])}
            parts["needs_at"] = [was.get(" ".join(t.split()).lower(), r.get("at")) for t in parts.get("needs") or []]
            foci[r["focus"]]["summary"] = {"text": r.get("text") or "", "from": r.get("from"), "at": r.get("at"), **parts}
    checks = {}
    for r in recs:
        # The person's tick waits for an agent to confirm it (the person, 1108133400fb): done_by "person" and no ok yet. An agent's
        # own done, or its confirm of the person's tick, sets ok: the step is finished and leaves the open list.
        if r["kind"] == "check" and r.get("focus") in foci and r.get("id"): checks[r["id"]] = {"id": r["id"], "focus": r["focus"], "text": r.get("text") or "", "from": r.get("from"), "at": r.get("at"), "done": None, "done_by": None, "ok": None}
        elif r["kind"] == "check_done" and r.get("id") in checks:
            c = checks[r["id"]]; c["done"] = r.get("at"); c["done_by"] = r.get("from"); c["ok"] = None if r.get("from") == "person" else {"from": r.get("from"), "at": r.get("at")}
        elif r["kind"] == "check_ok" and r.get("id") in checks and checks[r["id"]]["done"]: checks[r["id"]]["ok"] = {"from": r.get("from"), "at": r.get("at")}
        elif r["kind"] == "check_untick" and r.get("id") in checks: checks[r["id"]].update(done=None, done_by=None, ok=None, why=r.get("why") or None)
    for c in checks.values(): foci[c["focus"]]["checks"].append(c)
    for th, fid in tags.items(): foci[fid]["threads"].append(th)
    files = {}   # kb notes filed under a focus (kb file --from POST / --focus): path -> {path, post, from, at}; the newest filing of a path wins, --off removes it
    for r in recs:
        if r["kind"] == "file" and r.get("focus") in foci and r.get("path"):
            if r.get("off"): files.pop((r["focus"], r["path"]), None)
            else: files[(r["focus"], r["path"])] = {"path": r["path"], "post": r.get("post"), "from": r.get("from"), "at": r.get("at")}
    for (fid, _), x in sorted(files.items(), key=lambda kv: -(kv[1].get("at") or 0)): foci[fid]["files"].append(x)
    for f in foci.values():
        if f.get("thread") and f["thread"] not in f["threads"]: f["threads"].insert(0, f["thread"])
    order = ordered(order, recs)
    # Needs-you items the person put off (the person, 1108133400fb): key -> {key, th, focus, text, t, what: "later"|"moot", from, at};
    # "back" takes one off again. The item's t is kept so a newer question in the same conversation still shows.
    later = {}
    for r in recs:
        if r["kind"] == "later" and r.get("key"):
            if r.get("what") == "back": later.pop(r["key"], None)
            elif r.get("what") in ("later", "moot"): later[r["key"]] = {k: r.get(k) for k in ("key", "th", "focus", "text", "t", "what", "from", "at")}
    return {"foci": [foci[i] for i in order], "tags": tags, "order": order, "later": sorted(later.values(), key=lambda x: -(x.get("at") or 0)), "moved": moved}

MOVED_FOR, MOVED_MAX, MOVED_MERGE = 7 * 86400, 80, 600
def moves(recs, foci, at=None):
    """Threads moved between foci lately, newest first (the person, 110901466282: hard to know where a thread went once it moved):
    [{thread, was, to, by, at}], `was` the thread's previous tag (None: it had none, so it sat in Other or followed its parent),
    `to` None when it was taken out. A re-tag to where it already was isn't a move; moves of one thread within 10 minutes are one."""
    cur, out, last = {}, [], {}
    for r in recs:
        if r.get("kind") != "tag" or not r.get("thread"): continue
        th = r["thread"]; to = None if r.get("off") else r.get("focus")
        if to is not None and to not in foci: continue
        was = cur.get(th)
        if to == was: continue
        if to is None: cur.pop(th, None)
        else: cur[th] = to
        m = last.get(th); t = r.get("at") or 0
        if m and t - m["at"] < MOVED_MERGE:   # off then on, or a quick correction: one move from where it first was
            m.update(to=to, by=r.get("from"), at=t)
            if m["to"] == m["was"]: out.remove(m); last.pop(th)
            continue
        m = {"thread": th, "was": was, "to": to, "by": r.get("from"), "at": t}; out.append(m); last[th] = m
    since = (now() if at is None else at) - MOVED_FOR
    return sorted((m for m in out if m["at"] >= since), key=lambda m: -m["at"])[:MOVED_MAX]

def ordered(ids, recs):
    """`ids` (the default order) rearranged by the newest order record: the ids it lists that exist first, in its order, then the
    rest in their default order. An empty order record (--reset) gives the default back."""
    want = next((r.get("ids") for r in reversed(recs) if r["kind"] == "order" and isinstance(r.get("ids"), list)), None)
    if not want: return list(ids)
    have, first = set(ids), []
    for i in want:
        if i in have and i not in first: first.append(i)
    return first + [i for i in ids if i not in first]

def find(st, key):
    key = (key or "").strip().lstrip("#").lower()
    for f in st["foci"]:
        if f["id"] == key or (f.get("thread") or "") == key or f["name"].lower() == key or (f.get("n") and key == str(f["n"])): return f
    return None

def focus_of(st, thread, posts=None):
    """The focus a thread is in: its tag, else the focus whose own thread it is, else its first post's parent's focus; None is Misc."""
    if thread in st["tags"]: return st["tags"][thread]
    for f in st["foci"]:
        if thread in (f.get("thread"), f["id"]): return f["id"]
    if posts is not None:
        mine = [p for p in posts if p.get("thread") == thread]
        first = min(mine, key=lambda p: ts(p.get("at"))) if mine else None
        par = first and first.get("parent")
        if par and par != thread: return focus_of(st, par, None)
    return None

def run_starts(since):
    """[(t, thread)] for every agent run the starter began after `since` (the start events in ~/log.jsonl)."""
    out = []
    try:
        with open(LOG, "rb") as fh:
            for ln in fh:
                if b'"event": "start"' not in ln: continue
                try: e = json.loads(ln)
                except ValueError: continue
                t = ts(e.get("at"))
                if t >= since: out.append((t, e.get("thread") or ""))
    except OSError: pass
    return out

def activity(st, posts, starts, at=None):
    """Each focus's share of the hive's work: runs the starter began and posts agents wrote, in its threads, over the last day and week.
    {"foci": {id: {"day": {"runs", "posts"}, "week": {...}}}, "misc": the same for threads in no focus, "total": the hive's,
     "threads": {thread: focus id or None}} for every thread that saw a run or a post this week, so the dash can lay its costs
    (one per run, from the session logs) on the same map. A run on a thread in no focus counts under Misc; every one counts in total."""
    at = now() if at is None else at
    zero = lambda: {w: {"runs": 0, "posts": 0} for w in WINDOWS}
    out = {"windows": dict(WINDOWS), "foci": {f["id"]: zero() for f in st["foci"]}, "misc": zero(), "total": zero(), "threads": {}}
    week = at - WINDOWS["week"]
    def place(thread, t, what):
        if thread not in out["threads"]: out["threads"][thread] = focus_of(st, thread, posts)
        fid = out["threads"][thread]
        for w, span in WINDOWS.items():
            if t >= at - span:
                for b in (out["foci"][fid] if fid else out["misc"], out["total"]): b[w][what] += 1
    for t, th in starts:
        if t >= week and th: place(th, t, "runs")
    for p in posts:
        t = ts(p.get("at"))
        if t >= week and p.get("from") not in (None, "person"): place(p["thread"], t, "posts")
    return out

def newest_result(threads, posts):
    """The newest agent post in these threads whose last line is a result (Done: ...), as (t, post) or (0, None)."""
    best, bt = None, 0.0
    for p in posts:
        if p.get("thread") not in threads or str(p.get("from", "")).lower() == "person": continue
        lines = [l.strip() for l in str(p.get("text", "")).splitlines() if l.strip()]
        if len(lines) > 1 and RESULT.match(lines[-1]):
            t = ts(p.get("at"))
            if t > bt: bt, best = t, p
    return bt, best

def lint(st, posts=None, only=None):
    """[{focus, kind, what, fix}] for everything that reads wrong on the person's pages. Never refuses: the caller decides."""
    out = []
    posts = board_posts() if posts is None else posts
    for f in st["foci"]:
        if only and f["id"] != only: continue
        s = f.get("summary")
        if not s:
            if f["threads"]: out.append({"focus": f["id"], "kind": "missing", "what": "no summary yet", "fix": f"hive focus set {f['id']} <<'X' with ## Now and ## Lately bullets"})
            continue
        for part in ("now", "lately", "needs"):
            for b in s[part]:
                for name, rx, fix in JARGON:
                    m = rx.search(b)
                    if m: out.append({"focus": f["id"], "kind": name, "what": m.group(0).strip(), "in": b[:80], "fix": fix})
                if len(b) > MAX_BULLET: out.append({"focus": f["id"], "kind": "long bullet", "what": b[:60] + "…", "fix": f"keep a bullet under {MAX_BULLET} characters: one thing, in plain words"})
        if not s["now"] and not s["lately"]: out.append({"focus": f["id"], "kind": "empty", "what": "no bullets", "fix": "give at least one ## Now bullet"})
        t, p = newest_result(set(f["threads"]), posts)
        if p and t > float(s.get("at") or 0) + 60:
            out.append({"focus": f["id"], "kind": "stale", "what": f"a result was posted in #{p['thread']} by {p.get('from')} after the summary", "fix": f"the agent that posts a result updates the summary in the same run: hive focus set {f['id']}"})
    return out

def asked_only(thread):
    """An "Ask the Hive" conversation answers a question from the notes: it can be filed under a focus, but no work moved, so its answer owes the card nothing (ad775, 2026-11-09; R34)."""
    return str(thread).startswith("kb-ask-")

def nudge(name, thread, since, posts=None):
    """For the inbox hook: one line when `name` posted a result in `thread` since `since` and the thread's focus has an older summary (or none). None otherwise."""
    st = state(); posts = board_posts() if posts is None else posts
    fid = focus_of(st, thread, posts)
    if not fid or asked_only(thread): return None
    f = find(st, fid)
    mine = [p for p in posts if p.get("thread") == thread and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since]
    t, p = newest_result({thread}, mine)
    if not p: return None
    s = f.get("summary")
    if s and float(s.get("at") or 0) >= t: return None
    return (f"This thread is in the focus \"{f['name']}\" and its summary is {'older than your result' if s else 'not written yet'}. The person reads the focus card, "
            f"not the thread, so update it now, in plain words (what is being done and by whom, what changed), with @agent and #thread where they help:\n"
            f"hive focus set {f['id']} <<'X'\n## Now\n- …\n## Lately\n- …\nX")

FILE_ASKED = STATE / "file-asked.jsonl"     # {thread, name, at, file, title}: which agent was asked to file or title which conversation
META, RENAMED = STATE / "thread-meta.json", STATE / "renamed.jsonl"
HEXID = re.compile(r"[0-9a-f]{12}")

def _titled(thread):
    """True when the thread has a real title: a name that is not a board id, the person's own title (thread-meta) or an `hive rename`."""
    if not HEXID.fullmatch(thread): return True
    try:
        m = json.loads(META.read_text()).get(thread) or {}
        if m.get("title"): return True
    except (OSError, ValueError, AttributeError): pass
    try: return any(json.loads(l).get("thread") == thread for l in RENAMED.read_text().splitlines() if l.strip())
    except (OSError, ValueError): return False

def threads_posted(name, thread, since, posts):
    """The run's own thread first, then any other thread `name` posted in since `since` (a keeper run posting in someone else's thread)."""
    out = [thread]
    for p in posts:
        th = p.get("thread")
        if th and th not in out and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since: out.append(th)
    return out[:5]

def file_nudge(name, thread, since, posts=None):
    """For the inbox hook: one line, once per conversation ever, when `name` posted in `thread` since `since` and the thread is in no
    focus (nobody ever tagged it, so the person has not put it in Misc on purpose) or still has its board id for a title. The agent
    that worked in it files it and names it at its own hand-off: no hourly tidy pass (the person, 2026-11-08, post 1108153548ec,
    time invariance). Asks are recorded in FILE_ASKED so the audit pass can see who was asked and did not. None otherwise."""
    st = state(); posts = board_posts() if posts is None else posts
    if not any(p.get("thread") == thread and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since for p in posts): return None
    try:
        if any(json.loads(l).get("thread") == thread and now() - json.loads(l).get("at", 0) < 3600 for l in FILE_ASKED.read_text().splitlines() if l.strip()): return None  # asked within the hour; a first ask nobody acted on is repeated to the next agent
    except (OSError, ValueError): pass
    tagged = False
    try: tagged = any(r.get("kind") == "tag" and r.get("thread") == thread for r in map(json.loads, filter(str.strip, FILE.read_text().splitlines())))
    except (OSError, ValueError): pass
    unfiled = not tagged and focus_of(st, thread, posts) is None
    untitled = not _titled(thread)
    if not (unfiled or untitled): return None
    try:
        STATE.mkdir(parents=True, exist_ok=True)
        with FILE_ASKED.open("a") as f: f.write(json.dumps({"thread": thread, "name": name, "at": now(), "file": unfiled, "title": untitled}) + "\n")
    except OSError: return None
    out = []
    if unfiled:
        names = ", ".join(f"{f['id']} ({f['name']})" for f in st["foci"] if not f.get("off"))[:900]
        out.append(f"This conversation is in no focus, so the person won't find it on a focus page. File it in the focus it serves: "
                   f"hive focus tag {thread} FOCUS. The foci are: {names}. If none fits and the work will last, make one: hive focus new ID \"Name\" --thread {thread}. If it is plumbing the person needn't see, say so: hive focus tag {thread} --off.")
    if untitled:
        out.append(f"The conversation still shows its first words as a title. Give it a short title in plain words that says what it is about: "
                   f"hive rename {thread} \"…\" (the person's own title always wins).")
    return " ".join(out)

def fmt_lint(items):
    return "\n".join(f"{i['focus']}: {i['kind']}: {i['what']}" + (f"  (in: {i['in']})" if i.get("in") else "") + f"\n    → {i['fix']}" for i in items) or "nothing flagged"

def check_id(recs): return f"c{sum(1 for r in recs if r['kind'] == 'check') + 1:04d}"

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive focus", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("list"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("new"); p.add_argument("id"); p.add_argument("name", nargs="?"); p.add_argument("--thread"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("tag"); p.add_argument("thread"); p.add_argument("focus", nargs="?"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("link"); p.add_argument("a"); p.add_argument("b"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("relate"); p.add_argument("focus"); p.add_argument("what", choices=RELATE); p.add_argument("target"); p.add_argument("--why", default=""); p.add_argument("--off", action="store_true")
    p = sp.add_parser("set"); p.add_argument("focus")
    p = sp.add_parser("show"); p.add_argument("focus")
    p = sp.add_parser("keeper"); p.add_argument("focus")
    p = sp.add_parser("aim"); p.add_argument("focus"); p.add_argument("text", nargs="*")
    p = sp.add_parser("check"); p.add_argument("what", choices=["add", "list", "done", "untick", "confirm", "ticked"]); p.add_argument("args", nargs="*"); p.add_argument("--why", default="")
    p = sp.add_parser("later", help="put off a needs-you item: later (moved to the focus page's Later), moot (not needed), back (shown again)")
    p.add_argument("what", choices=["later", "moot", "back", "list"]); p.add_argument("key", nargs="?"); p.add_argument("--th", default=""); p.add_argument("--focus", default="")
    p.add_argument("--text", default=""); p.add_argument("--t", type=float, default=0)
    p = sp.add_parser("lint"); p.add_argument("focus", nargs="?"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("file"); p.add_argument("path"); p.add_argument("--post"); p.add_argument("--focus"); p.add_argument("--off", action="store_true")   # a kb note filed under a focus (kb file --from/--focus calls this)
    p = sp.add_parser("order"); p.add_argument("ids", nargs="*"); p.add_argument("--reset", action="store_true")
    p = sp.add_parser("moved", help="threads moved between foci lately, newest first"); p.add_argument("--since", default="2d", help="how far back: 30m, 12h, 3d (up to a week)")
    sp.add_parser("json")
    a = ap.parse_args(argv)
    st = state()
    if a.cmd == "list":
        if a.json: print(json.dumps(st["foci"], default=str)); return
        for f in st["foci"]:
            s = f.get("summary"); age = f"summary {int((now() - float(s['at'])) / 3600)} h old" if s else "no summary"
            print(f"{f['id']:<16} {f['name'][:50]:<50} {len(f['threads'])} threads, {age}" + (f", linked: {', '.join(f['links'])}" if f["links"] else ""))
        return
    if a.cmd == "new":
        fid = a.id.strip().lower()
        if not ID.fullmatch(fid) or fid == MISC: sys.exit("hive focus new: the id is lowercase letters, digits and dashes (misc is taken)")
        f = find(st, fid)
        if a.off:
            if not f: sys.exit(f"hive focus new: no focus {fid}")
            if f.get("own"): sys.exit(f"hive focus new: {fid} is one of the person's priorities; it goes when kb/priorities.md changes")
            append({"kind": "focus", "id": fid, "off": True}); print(f"retired focus {fid}"); return
        if not a.name: sys.exit('hive focus new: give the name, hive focus new ID "name"')
        append({"kind": "focus", "id": fid, "name": " ".join(a.name.split())[:80], "thread": (a.thread or "").lstrip("#") or None})
        print(("renamed" if f else "new") + f" focus {fid}: {a.name}"); return
    if a.cmd == "tag":
        th = a.thread.lstrip("#")
        if not THREAD.fullmatch(th): sys.exit("hive focus tag: give a thread name")
        if a.off: append({"kind": "tag", "thread": th, "off": True}); print(f"{th} is out of its focus"); return
        f = find(st, a.focus or "")
        if not f: sys.exit(f"hive focus tag: no focus {a.focus!r} (hive focus list)")
        append({"kind": "tag", "thread": th, "focus": f["id"]}); print(f"{th} is in {f['id']} ({f['name']})"); return
    if a.cmd == "link":
        fa, fb = find(st, a.a), find(st, a.b)
        if not fa or not fb or fa is fb: sys.exit("hive focus link: give two different foci (hive focus list)")
        append({"kind": "link", "a": fa["id"], "b": fb["id"], **({"off": True} if a.off else {})}); print(("unlinked" if a.off else "linked") + f" {fa['id']} and {fb['id']}"); return
    if a.cmd == "relate":
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus relate: no focus {a.focus!r} (hive focus list)")
        x = a.target.strip().lstrip("@#" if a.what != "file" else "@")
        if a.what == "agent": x = x.lower()
        if not x or (a.what != "file" and not THREAD.fullmatch(x)): sys.exit(f"hive focus relate: give the {a.what}'s name")
        why = " ".join(a.why.split())[:200]
        if not a.off and not why: sys.exit('hive focus relate: say why with --why "..." (one line: what it has to do with the focus)')
        append({"kind": "relation", "focus": f["id"], "what": a.what, "target": x, **({"off": True} if a.off else {"why": why})})
        print(("unrelated" if a.off else "related") + f" {a.what} {x} {'from' if a.off else 'to'} {f['id']}"); return
    if a.cmd == "aim":
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus aim: no focus {a.focus!r} (hive focus list)")
        text = " ".join(" ".join(a.text).split())[:200]
        if not text: print(f.get("aim") or "(no aim yet)"); return
        append({"kind": "aim", "focus": f["id"], "text": text}); print(f"aim of {f['id']}: {text}"); return
    if a.cmd in ("set", "show", "keeper"):
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus: no focus {a.focus!r} (hive focus list; hive focus new ID \"name\" adds one)")
        if a.cmd == "show": print((f.get("summary") or {}).get("text") or "(no summary yet)"); return
        if a.cmd == "keeper": print(keeper(f, board_posts()) or "nobody"); return
        if sys.stdin.isatty(): sys.exit("hive focus set: the summary comes on stdin, a quoted heredoc with ## Now and ## Lately bullets")
        text = sys.stdin.read().strip()[:MAX_SUMMARY]
        parts = parse_summary(text)
        if not parts["now"] and not parts["lately"]: sys.exit("hive focus set: empty; give ## Now and ## Lately bullets")
        append({"kind": "summary", "focus": f["id"], "text": text})
        st = state(); flags = [i for i in lint(st, only=f["id"]) if i["kind"] != "stale"]
        print(f"summary set on {f['id']} ({len(parts['now'])} now, {len(parts['lately'])} lately" + (f", {len(parts['needs'])} for the person" if parts["needs"] else "") + ")" + (f"\nreads as agent talk, fix when you can:\n{fmt_lint(flags)}" if flags else ""))
        return
    if a.cmd == "check":
        recs = records()
        if a.what == "add":
            if len(a.args) < 2: sys.exit('hive focus check add FOCUS "text"')
            f = find(st, a.args[0])
            if not f: sys.exit(f"hive focus check: no focus {a.args[0]!r}")
            r = append({"kind": "check", "focus": f["id"], "id": check_id(recs), "text": " ".join(" ".join(a.args[1:]).split())[:200]}); print(f"{r['id']}: {r['text']}"); return
        if a.what in ("list", "ticked"):   # ticked: the person's ticks no agent has checked yet
            f = find(st, a.args[0]) if a.args else None
            for g in st["foci"]:
                if f and g is not f: continue
                for c in g["checks"]:
                    if a.what == "ticked" and not (c["done"] and not c.get("ok")): continue
                    print(f"[{'x' if c.get('ok') else '?' if c['done'] else ' '}] {c['id']} {g['id']}: {c['text']}" + (" (ticked by the person: check it, then confirm or untick --why)" if c["done"] and not c.get("ok") else ""))
            return
        known = {c["id"]: c for g in st["foci"] for c in g["checks"]}
        for cid in a.args:
            if cid not in known: sys.exit(f"hive focus check: no item {cid}")
            if a.what == "confirm" and not known[cid]["done"]: sys.exit(f"hive focus check: {cid} isn't ticked; use done")
            kind = {"done": "check_done", "untick": "check_untick", "confirm": "check_ok"}[a.what]
            append({"kind": kind, "id": cid, **({"why": " ".join(a.why.split())[:200]} if a.what == "untick" and a.why else {})})
        print(f"{a.what}: {' '.join(a.args)}"); return
    if a.cmd == "lint":
        f = find(st, a.focus) if a.focus else None
        if a.focus and not f: sys.exit(f"hive focus lint: no focus {a.focus!r}")
        items = lint(st, only=f["id"] if f else None)
        print(json.dumps(items) if a.json else fmt_lint(items)); return
    if a.cmd == "file":
        posts = board_posts(); fid = None
        if a.focus:
            f = find(st, a.focus)
            if not f: sys.exit(f"no focus {a.focus}")
            fid = f["id"]
        elif a.post:
            src = next((p for p in posts if p.get("id") == a.post), None)
            if not src: sys.exit(f"no post {a.post} on the board")
            fid = focus_of(st, src.get("thread"), posts)
        if not fid: print("no focus for it (the post's thread is in Misc; give --focus ID)"); return
        append({"kind": "file", "focus": fid, "path": a.path.strip(), **({"post": a.post} if a.post else {}), **({"off": True} if a.off else {})})
        print(("taken off" if a.off else "listed on") + f" the focus page of {fid}: {a.path.strip()}"); return
    if a.cmd == "order":
        if a.reset: append({"kind": "order", "ids": []}); print("order back to the default"); return
        if not a.ids: print(" ".join(f["id"] for f in st["foci"])); return
        ids = []
        for k in a.ids:
            f = find(st, k)
            if not f: sys.exit(f"hive focus order: no focus {k!r} (hive focus list)")
            if f["id"] not in ids: ids.append(f["id"])
        append({"kind": "order", "ids": ids})
        print("order: " + " ".join(f["id"] for f in state()["foci"])); return
    if a.cmd == "later":
        if a.what == "list":
            for x in st["later"]: print(f"{x['what']:5} {x.get('focus') or '-'} #{x.get('th') or '-'}: {x.get('text') or ''}  [{x['key']}]")
            return
        if not a.key or len(a.key) > 200: sys.exit("hive focus later later|moot|back KEY [--th T --focus F --text X --t SECONDS]")
        if a.what == "back" and a.key not in {x["key"] for x in st["later"]}: sys.exit(f"hive focus later: {a.key!r} isn't put off")
        f = find(st, a.focus) if a.focus else None
        append({"kind": "later", "key": a.key, "what": a.what, **({} if a.what == "back" else {"th": a.th[:80] or None, "focus": f["id"] if f else None, "text": " ".join(a.text.split())[:300], "t": a.t or None})})
        print(f"{a.what}: {a.key}"); return
    if a.cmd == "moved":
        mt = re.fullmatch(r"(\d+(?:\.\d+)?)([mhd])", a.since.strip())
        if not mt: sys.exit("hive focus moved: --since like 30m, 12h or 3d")
        cut = now() - float(mt[1]) * {"m": 60, "h": 3600, "d": 86400}[mt[2]]
        rows = [m for m in st["moved"] if m["at"] >= cut]
        if not rows: print("no thread moved between foci in that time"); return
        for m in rows:
            print(f"{dt.datetime.fromtimestamp(m['at']).strftime('%m-%d %H:%M')}  {m['thread']:<14} {m['was'] or '(untagged)'} -> {m['to'] or MISC}  by {m['by']}")
        return
    if a.cmd == "json":
        posts = board_posts()
        for f in st["foci"]: f["keeper"] = keeper(f, posts)
        out = {"foci": st["foci"], "tags": st["tags"], "later": st["later"], "moved": st["moved"], "misc": MISC, "lint": lint(st, posts), "activity": activity(st, posts, run_starts(now() - WINDOWS["week"])), "at": now()}
        print(json.dumps(out, default=str)); return

if __name__ == "__main__": main()
