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
  hive focus set FOCUS <<'X'            the summary, markdown on stdin: `## Now` bullets (what is being done, who, where: @agent and
                                      #thread become pills) and `## Lately` bullets (decisions, turns, what changed). Plain words,
                                      high level relative to the focus. Lint runs and prints what it flags; it never refuses
  hive focus show FOCUS                 the summary as set
  hive focus check add FOCUS "text" | list FOCUS | done ID... | untick ID...   the focus page's to-do list
  hive focus lint [FOCUS] [--json]      what reads wrong: ids, paths, rung words, test names, long bullets, a summary older than
                                      the focus's newest result post (stale), a focus with threads and no summary (missing)
  hive focus json                       everything, for the dashboard (/api/foci)

The duty (time-invariant, no cleanup pulses): the agent that posts a result in a thread that is in a focus updates the focus's
summary in the same run. The inbox hook says so once when it sees the result post and an older summary.
Records go to ~/.hive/focus.jsonl, append-only: {kind: focus|tag|link|summary|check|check_done|check_untick, ..., from, at}.
"""
import argparse, datetime as dt, json, os, re, sys, time
from pathlib import Path

HOME = Path.home()
CORE, STATE, KB = HOME / "core", HOME / ".hive", HOME / "kb"
FILE, BOARD, STATUS = STATE / "focus.jsonl", HOME / "board.jsonl", STATE / "thread-status.jsonl"
ID = re.compile(r"[a-z][a-z0-9-]{1,40}")
THREAD = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.:-]{0,100}")
FOCUS_LINE = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`", re.M)   # goals.py's FOCUS
RESULT = re.compile(r"^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*\S", re.I)
MISC = "misc"
MAX_SUMMARY, MAX_BULLET = 2500, 220
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
    for n, title, th in FOCUS_LINE.findall(text):
        th = th.strip().lower()
        if th in seen: continue
        seen.add(th); out.append({"id": th, "name": title.strip(), "n": int(n), "thread": th})
    return out

def parse_summary(text):
    """`## Now` and `## Lately` bullets out of the summary's markdown; a bullet-less part is kept as one bullet."""
    parts, cur = {"now": [], "lately": []}, None
    for ln in (text or "").splitlines():
        s = ln.strip()
        if not s: continue
        m = re.match(r"^(?:#{1,3}\s*)?(now|currently|lately|recent(?:ly)?.*|evolution.*)\s*:?\s*$", s, re.I)
        if m: cur = "now" if m.group(1).lower() in ("now", "currently") else "lately"; continue
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

def ts(at):
    try: return dt.datetime.fromisoformat(str(at).replace("Z", "+00:00")).timestamp()
    except (TypeError, ValueError): return 0.0

def state(recs=None, pri=None, posts=None):
    """Everything resolved: {foci: [...], tags: {thread: focus}, order: [ids]}. A focus: {id, name, n?, thread?, own, links, summary, checks, threads}."""
    recs = records() if recs is None else recs
    foci, order = {}, []
    for f in (priorities() if pri is None else pri):
        foci[f["id"]] = {**f, "own": True, "links": [], "summary": None, "checks": [], "threads": []}; order.append(f["id"])
    for r in recs:
        if r["kind"] == "focus":
            fid = r.get("id")
            if not fid: continue
            if r.get("off"):
                if fid in foci and not foci[fid].get("own"): del foci[fid]; order.remove(fid)
            elif fid not in foci:
                foci[fid] = {"id": fid, "name": r.get("name") or fid, "thread": r.get("thread"), "own": False, "links": [], "summary": None, "checks": [], "threads": [], "made": r.get("at")}; order.append(fid)
            else: foci[fid]["name"] = r.get("name") or foci[fid]["name"]
    tags = {}
    for r in recs:
        if r["kind"] == "tag" and r.get("thread"):
            if r.get("off"): tags.pop(r["thread"], None)
            elif r.get("focus") in foci: tags[r["thread"]] = r["focus"]
    links = set()
    for r in recs:
        if r["kind"] == "link" and r.get("a") in foci and r.get("b") in foci:
            k = tuple(sorted((r["a"], r["b"])))
            if r.get("off"): links.discard(k)
            else: links.add(k)
    for a, b in links: foci[a]["links"].append(b); foci[b]["links"].append(a)
    for r in recs:
        if r["kind"] == "summary" and r.get("focus") in foci:
            foci[r["focus"]]["summary"] = {"text": r.get("text") or "", "from": r.get("from"), "at": r.get("at"), **parse_summary(r.get("text"))}
    checks = {}
    for r in recs:
        if r["kind"] == "check" and r.get("focus") in foci and r.get("id"): checks[r["id"]] = {"id": r["id"], "focus": r["focus"], "text": r.get("text") or "", "from": r.get("from"), "at": r.get("at"), "done": None}
        elif r["kind"] == "check_done" and r.get("id") in checks: checks[r["id"]]["done"] = r.get("at")
        elif r["kind"] == "check_untick" and r.get("id") in checks: checks[r["id"]]["done"] = None
    for c in checks.values(): foci[c["focus"]]["checks"].append(c)
    for th, fid in tags.items(): foci[fid]["threads"].append(th)
    for f in foci.values():
        if f.get("thread") and f["thread"] not in f["threads"]: f["threads"].insert(0, f["thread"])
    return {"foci": [foci[i] for i in order], "tags": tags, "order": order}

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
        for part in ("now", "lately"):
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

def nudge(name, thread, since, posts=None):
    """For the inbox hook: one line when `name` posted a result in `thread` since `since` and the thread's focus has an older summary (or none). None otherwise."""
    st = state(); posts = board_posts() if posts is None else posts
    fid = focus_of(st, thread, posts)
    if not fid: return None
    f = find(st, fid)
    mine = [p for p in posts if p.get("thread") == thread and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since]
    t, p = newest_result({thread}, mine)
    if not p: return None
    s = f.get("summary")
    if s and float(s.get("at") or 0) >= t: return None
    return (f"This thread is in the focus \"{f['name']}\" and its summary is {'older than your result' if s else 'not written yet'}. The person reads the focus card, "
            f"not the thread, so update it now, in plain words (what is being done and by whom, what changed), with @agent and #thread where they help:\n"
            f"hive focus set {f['id']} <<'X'\n## Now\n- …\n## Lately\n- …\nX")

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
    p = sp.add_parser("set"); p.add_argument("focus")
    p = sp.add_parser("show"); p.add_argument("focus")
    p = sp.add_parser("check"); p.add_argument("what", choices=["add", "list", "done", "untick"]); p.add_argument("args", nargs="*")
    p = sp.add_parser("lint"); p.add_argument("focus", nargs="?"); p.add_argument("--json", action="store_true")
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
    if a.cmd in ("set", "show"):
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus: no focus {a.focus!r} (hive focus list; hive focus new ID \"name\" adds one)")
        if a.cmd == "show": print((f.get("summary") or {}).get("text") or "(no summary yet)"); return
        if sys.stdin.isatty(): sys.exit("hive focus set: the summary comes on stdin, a quoted heredoc with ## Now and ## Lately bullets")
        text = sys.stdin.read().strip()[:MAX_SUMMARY]
        parts = parse_summary(text)
        if not parts["now"] and not parts["lately"]: sys.exit("hive focus set: empty; give ## Now and ## Lately bullets")
        append({"kind": "summary", "focus": f["id"], "text": text})
        st = state(); flags = [i for i in lint(st, only=f["id"]) if i["kind"] != "stale"]
        print(f"summary set on {f['id']} ({len(parts['now'])} now, {len(parts['lately'])} lately)" + (f"\nreads as agent talk, fix when you can:\n{fmt_lint(flags)}" if flags else ""))
        return
    if a.cmd == "check":
        recs = records()
        if a.what == "add":
            if len(a.args) < 2: sys.exit('hive focus check add FOCUS "text"')
            f = find(st, a.args[0])
            if not f: sys.exit(f"hive focus check: no focus {a.args[0]!r}")
            r = append({"kind": "check", "focus": f["id"], "id": check_id(recs), "text": " ".join(" ".join(a.args[1:]).split())[:200]}); print(f"{r['id']}: {r['text']}"); return
        if a.what == "list":
            f = find(st, a.args[0]) if a.args else None
            for g in st["foci"]:
                if f and g is not f: continue
                for c in g["checks"]: print(f"[{'x' if c['done'] else ' '}] {c['id']} {g['id']}: {c['text']}")
            return
        known = {c["id"] for g in st["foci"] for c in g["checks"]}
        for cid in a.args:
            if cid not in known: sys.exit(f"hive focus check: no item {cid}")
            append({"kind": "check_done" if a.what == "done" else "check_untick", "id": cid})
        print(f"{a.what}: {' '.join(a.args)}"); return
    if a.cmd == "lint":
        f = find(st, a.focus) if a.focus else None
        if a.focus and not f: sys.exit(f"hive focus lint: no focus {a.focus!r}")
        items = lint(st, only=f["id"] if f else None)
        print(json.dumps(items) if a.json else fmt_lint(items)); return
    if a.cmd == "json":
        posts = board_posts()
        out = {"foci": st["foci"], "tags": st["tags"], "misc": MISC, "lint": lint(st, posts), "at": now()}
        print(json.dumps(out, default=str)); return

if __name__ == "__main__": main()
