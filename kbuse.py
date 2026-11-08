#!/usr/bin/env python3
"""How the knowledge base gets used: what agents were shown, what they went on to read, and searches worded again.

The `kb` commands write what they showed; inbox.py hands every Claude tool call to observe(). A read counts as following
a search only if its path (and line range, when it has one) is a result that search showed the same agent in the last
10 minutes: time alone proves nothing. Rules follow projects/kb-search/logruler.py: `needle` is its rule for what a later
tool call must hold, and a `touch` record is its "read within 8 tool calls", so its 261 of 434 stays comparable.

Only Claude Read and Bash calls are seen. Codex runs and other routes aren't, so nothing here can say a note went unread,
only that no read was observed. Stdlib only and cheap: the hook runs after every tool call, and never touches the index."""
import calendar, json, os, re, shlex, time
H = os.path.expanduser(os.environ.get("KB_HOME", "~"))
CACHE = os.path.join(os.path.expanduser("~/.cache/hive-kb") if "KB_HOME" not in os.environ else H + "/.cache/hive-kb")
QLOG, LIVE, CANDIDATES = CACHE + "/queries.jsonl", CACHE + "/live", H + "/kb/history/miss-candidates.jsonl"
DRAFT_S = 14 * 86400   # a draft's time to be read (kb-dream.md)
WINDOW, READS, CHAIN, SLACK = 600, 8, 180, 12   # seconds a read can follow a search; tool calls (logruler's READS); seconds between two wordings; lines a passage may have moved (gold_match)
SCOPE = ("kb/", "core/", "projects/", "modules/")
READERS = re.compile(r"\b(cat|head|tail|sed)\b|\bh2\s+read\b|\bcd\b")
WRITES = re.compile(r"(?<![0-9&])>")

def agent(): return os.environ.get("HIVE_NAME", "")

def log(rec):
    who = agent()
    rec.update(at=time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()), who=who, agent=who)
    try:
        with open(QLOG, "a") as f: f.write(json.dumps(rec) + "\n")
    except OSError: pass

def new_id(): return os.urandom(4).hex()

def needle(p):
    """what a later command must hold to count as reading p (logruler.needle)"""
    return p[6:] if p.startswith("board:") else p[2:] if p.startswith("A/") else p

def live_file(who): return f"{LIVE}/{who}.json"

def load(who):
    """the searches still open for this agent: {"live": [entry], "tester": bool}"""
    try:
        st = json.load(open(live_file(who)))
        return st if isinstance(st.get("live"), list) else {"live": []}
    except (OSError, ValueError, AttributeError): return {"live": []}

def save(who, st):
    t = time.time()
    st["live"] = [e for e in st["live"] if t - e["t"] <= WINDOW or e["calls"] < READS][-8:]
    try:
        if not st["live"] and not st.get("tester"):
            try: os.unlink(live_file(who))
            except FileNotFoundError: pass
            return
        os.makedirs(LIVE, exist_ok=True)
        tmp = f"{live_file(who)}.{os.getpid()}"
        with open(tmp, "w") as f: json.dump(st, f)
        os.replace(tmp, live_file(who))
    except OSError: pass

def remember(who, st, id, kind, shown, q="", calls=-1, **more):
    """calls -1: the tool call that ran this search hasn't ended, and what it does beside the search can't follow from it"""
    if who: st["live"].append({"id": id, "kind": kind, "q": q, "t": time.time(), "shown": shown, "calls": calls, "touched": [], **more}); save(who, st)

def shows(e, path, lo, hi):
    """does this search's list hold path, at a line in lo..hi when the read has a range?"""
    for p, line in e["shown"]:
        n = needle(p)
        if (path == p or path == n or path.endswith("/" + n)) and (lo is None or lo - SLACK <= line <= hi + SLACK): return True
    return False

def seg_range(cmd, toks):
    """the lines a reader command prints, None for all or unknown"""
    if cmd == "head":
        n = 10
        for i, t in enumerate(toks):
            if m := re.fullmatch(r"-n?(\d+)", t): n = int(m.group(1))
            elif t == "-n" and i + 1 < len(toks) and toks[i + 1].isdigit(): n = int(toks[i + 1])
        return 1, n
    if cmd == "sed":
        for t in toks:
            if m := re.fullmatch(r"(\d+)(?:,(\d+))?p", t): return int(m.group(1)), int(m.group(2) or m.group(1))
    return None

def reads_of(tool, inp, cwd):
    """[(absolute path or board:ID, first line or None, last line or None)] that this tool call read"""
    if tool == "Read":
        p = inp.get("file_path") or ""
        if not p: return []
        lo = inp.get("offset")
        return [(p, None, None)] if lo is None and inp.get("limit") is None else [(p, int(lo or 1), int(lo or 1) + int(inp.get("limit") or 2000) - 1)]
    cmd = inp.get("command") or ""
    if tool != "Bash" or not isinstance(cmd, str) or not READERS.search(cmd): return []
    out = []; here = cwd or H
    for seg in re.split(r"&&|\|\||[;|\n]", cmd):
        if m := re.search(r"\bh2\s+read\b.*?--id\s+(\S+)", seg): out.append(("board:" + m.group(1), None, None)); continue
        if m := re.search(r"\bh2\s+read\b.*?--thread[=\s]+(\S+)", seg): out.append(("thread:" + m.group(1), None, None)); continue   # a neighbours.py pointer
        try: toks = shlex.split(seg)
        except ValueError: toks = seg.split()
        if not toks: continue
        if toks[0] == "cd" and len(toks) > 1:
            here = os.path.normpath(os.path.join(here, os.path.expanduser(toks[1].replace("$HOME", H)))); continue
        if toks[0] not in ("cat", "head", "tail", "sed") or WRITES.search(seg) or (toks[0] == "sed" and any(t.startswith("-i") for t in toks)): continue
        lines = seg_range(toks[0], toks[1:])
        args = [t for t in toks[1:] if not t.startswith("-") and not t.isdigit() and not re.search(r"[<>*?]", t)]
        if toks[0] == "sed" and "-e" not in toks and "-f" not in toks: args = args[1:]   # the script
        for t in args:
            p = os.path.normpath(os.path.join(here, os.path.expanduser(t.replace("$HOME", H))))
            out.append((p, *(lines or (None, None))))
    return out

def in_scope(rel): return rel.endswith(".md") and rel.startswith(SCOPE)

def rel(p): return p[len(H) + 1:] if p.startswith(H + "/") else p

def observe(who, p):
    """inbox.py, after each tool call. Logs reads, and joins them to what was shown. Never raises into the hook's delivery: its caller guards."""
    if p.get("hook_event_name") != "PostToolUse": return
    tool, inp, sid = p.get("tool_name") or "", p.get("tool_input") or {}, p.get("session_id") or ""
    res = p.get("tool_response")
    ok = not (isinstance(res, dict) and (res.get("is_error") or res.get("interrupted")))
    reads = reads_of(tool, inp, p.get("cwd")) if ok else []
    if not reads and not os.path.exists(live_file(who)) and "projects/kb-search" not in json.dumps(inp): return
    st = load(who); blob = json.dumps(inp) if ok else ""; now = time.time()
    tester = "projects/kb-search" in blob and not st.get("tester")   # runs on the search project: their searches are tests (logruler leaves them out)
    if tester: st["tester"] = True
    for e in st["live"]:
        if e["calls"] < 0: e["calls"] = 0; continue
        e["calls"] += 1
        for path, _ in e["shown"]:
            if path not in e["touched"] and needle(path) in blob:
                e["touched"].append(path); log({"touch": path, "find": e["id"], "calls": e["calls"], "tool": tool, "session": sid})
    for path, lo, hi in reads:
        m = [e for e in reversed(st["live"]) if e["calls"] >= 1 and now - e["t"] <= WINDOW and shows(e, path, lo, hi)]
        r = rel(path)
        if m:
            log({"read": r, "from": m[0]["id"], **({"also": [e["id"] for e in m[1:]]} if m[1:] else {}), "calls": m[0]["calls"], "tool": tool, "session": sid})
        elif in_scope(r) and os.path.isfile(path): log({"read": r, "tool": tool, "session": sid})
    if st["live"] or tester: save(who, st)

def opened(who, target, line, span):
    """`kb open` of target at line (None: no line given). The fields its log record gets, marking the searches it follows.
    The hook sees the same call a moment later; the touch is taken here so it isn't counted twice."""
    if not who: return {}
    path = target.rsplit(":", 1)[0] if line is not None else target
    st = load(who); now = time.time()
    for e in st["live"]:
        if e["calls"] >= 0 and path not in e["touched"] and shows(e, path, None, None):
            e["touched"].append(path); log({"touch": path, "find": e["id"], "calls": e["calls"] + 1, "tool": "kb open"})
    lo, hi = (None, None) if line is None else (line - span // 2, line + span)
    m = [e for e in reversed(st["live"]) if e["calls"] >= 0 and now - e["t"] <= WINDOW and shows(e, path, lo, hi)]
    if st["live"]: save(who, st)
    return {"from": m[0]["id"], "calls": m[0]["calls"] + 1, **({"also": [e["id"] for e in m[1:]]} if m[1:] else {})} if m else {}

def worded_again(st, q, filtered):
    """the search this one rewords if it could be one: the agent's last, under 3 minutes ago, unfiltered, with no read of anything it showed. Whether they share a rare word is the caller's to say."""
    prev = next((e for e in reversed(st["live"]) if e["kind"] == "find"), None)
    if not prev or filtered or st.get("tester") or prev.get("filtered") or prev["calls"] < 0 or prev["touched"]: return None
    return prev if time.time() - prev["t"] <= CHAIN and prev["q"].lower() != q.lower() else None

def file_candidate(prev, q, id, shown):
    """two wordings with nothing read between: maybe a miss, maybe a search narrowing. kb-dream.md judges a sample."""
    rec = {"agent": agent(), "thread": os.environ.get("HIVE_THREAD", ""), "queries": [prev["q"], q], "finds": [prev["id"], id], "shown": [prev["shown"], shown]}
    try:
        with open(CANDIDATES, "a") as f: f.write(json.dumps({"at": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()), **rec}) + "\n")
    except OSError: pass
    log({"candidate": [prev["q"], q], "finds": [prev["id"], id]})

def records():
    try:
        with open(QLOG, errors="replace") as f: lines = f.readlines()
    except OSError: return []
    out = []
    for l in lines:
        try: out.append(json.loads(l))
        except ValueError: pass
    return out

DUD_SHOWN, DUD_PCT, DUD_DAYS, DUD_TTL = 15, 5, 3, 3600   # a note shown this often with a read this rarely is a dud for recall

def duds(now=None, rs=None):
    """Paths recall keeps showing that nobody opens: shown at least DUD_SHOWN times in the last DUD_DAYS days, opened in under DUD_PCT% of them.
    Cached an hour in CACHE/duds.json so a recall doesn't read the whole log. A reader of 2 dozen generic notes (guide, keeper) is why recall_opened_pct sat at 6."""
    now = time.time() if now is None else now; cache = CACHE + "/duds.json"
    if rs is None:
        try:
            c = json.load(open(cache))
            if now - c["at"] < DUD_TTL: return set(c["duds"])
        except (OSError, ValueError, KeyError): pass
        rs = records()
    t0, t1 = iso(now - DUD_DAYS * 86400), iso(now + 1)
    followed = {}
    for r in rs:
        if r.get("from"):
            path = r.get("read") or re.sub(r":\d+$", "", r.get("open", ""))
            for i in [r["from"], *r.get("also", [])]: followed.setdefault(i, set()).add(path)
    shown, opened = {}, {}
    for r in rs:
        if not (r.get("recall") and r.get("id") and r.get("agent") and not r.get("neighbours") and t0 <= r.get("at", "")[:19] < t1): continue
        for p, _ in r.get("shown", []):
            shown[p] = shown.get(p, 0) + 1
            opened[p] = opened.get(p, 0) + any(x == p or x == needle(p) or x.endswith("/" + needle(p)) for x in followed.get(r["id"], ()))
    out = {p for p, n in shown.items() if n >= DUD_SHOWN and 100 * opened[p] < DUD_PCT * n}
    if len(rs) and cache:
        try:
            os.makedirs(CACHE, exist_ok=True); json.dump({"at": now, "duds": sorted(out)}, open(cache, "w"))
        except OSError: pass
    return out

def pct(a, b): return round(100 * a / b) if b else None

def runs_observable(t0, t1):
    """share of the runs started in the window whose reads the hook can see: every run in Claude Code, not Codex's"""
    a = b = 0
    try:
        with open(H + "/log.jsonl", errors="replace") as f:
            for l in f:
                if '"start"' not in l: continue
                try: r = json.loads(l)
                except ValueError: continue
                if r.get("event") == "start" and t0 <= r.get("at", "")[:19] < t1:
                    b += 1; a += "codex" not in (str(r.get("harness")) + str(r.get("provider"))).lower()
    except OSError: pass
    return pct(a, b)

def numbers(t0, t1, rs=None):
    """The window's use of the knowledge base; t0, t1 as YYYY-MM-DDTHH:MM:SS, UTC. Records from before the logging have no id and stay out of every share."""
    rs = records() if rs is None else rs
    win = [r for r in rs if t0 <= r.get("at", "")[:19] < t1]
    followed, near = {}, set()   # search id -> paths read or opened from its list; ids with a shown path in a later call within READS
    for r in rs:
        if "touch" in r and r.get("calls", 99) <= READS: near.add(r.get("find"))
        if r.get("from"):
            path = r.get("read") or re.sub(r":\d+$", "", r.get("open", ""))
            for i in [r["from"], *r.get("also", [])]: followed.setdefault(i, set()).add(path)
    finds = [r for r in win if "q" in r]
    logged = [r for r in finds if r.get("id") and r.get("shown") and not r.get("filtered") and not r.get("tester")]
    recalls = [r for r in win if r.get("recall") and r.get("id") and r.get("agent") and not r.get("neighbours")]
    pointers = [(r["id"], p) for r in recalls for p, _ in r.get("shown", [])]
    opened = sum(1 for i, p in pointers if any(x == p or x == needle(p) or x.endswith("/" + needle(p)) for x in followed.get(i, ())))
    live = [(r["id"], p) for r in win if r.get("neighbours") and r.get("id") and r.get("agent") for p, _ in r.get("shown", [])]   # neighbours.py: counted apart, so the note pointers' baseline stays comparable
    live_read = sum(1 for i, p in live if p in followed.get(i, ()))
    fr = sum(1 for r in logged if r["id"] in followed)
    return {"finds": len(finds), "finds_logged": len(logged), "finds_read": fr, "find_read_pct": pct(fr, len(logged)),
            "finds_read_8": sum(1 for r in logged if r["id"] in near), "find_read_8_pct": pct(sum(1 for r in logged if r["id"] in near), len(logged)),
            "recalls": len(recalls), "recalls_opened": sum(1 for r in recalls if r["id"] in followed), "recall_opened_pct": pct(sum(1 for r in recalls if r["id"] in followed), len(recalls)),
            "pointers": len(pointers), "pointers_opened": opened, "pointer_opened_pct": pct(opened, len(pointers)),
            "neighbour_pointers": len(live), "neighbour_pointers_read": live_read, "neighbour_read_pct": pct(live_read, len(live)),
            "misses": sum(1 for r in win if "miss" in r), "candidates": sum(1 for r in win if "candidate" in r), "runs_observable_pct": runs_observable(t0, t1)}

def iso(t): return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(t))

def drafts(rs=None, now=None):
    """The grader's drafts in kb/_drafts: ([{path, age, read, action}], the measure as one line). A read after the 14 days doesn't count as read in them;
    a draft nobody read is only "no observed read", as everywhere here. Testers' reads and searches aren't in it: `read` and `open` records only."""
    rs = records() if rs is None else rs; now = time.time() if now is None else now; base = H + "/kb/_drafts"
    written = []
    try:
        with open(H + "/.hive/drafts.jsonl", errors="replace") as f:
            for l in f:
                try: written.append(json.loads(l))
                except ValueError: pass
    except OSError: pass
    def t(at): return calendar.timegm(time.strptime(at[:19], "%Y-%m-%dT%H:%M:%S"))
    reads = {}
    for r in rs:
        p = r.get("read") or re.sub(r":\d+$", "", r.get("open") or "")
        i = p.find("kb/_drafts/") if ("read" in r or "open" in r) and not r.get("tester") else -1
        if i >= 0 and "at" in r: reads.setdefault(p[i:], []).append(r["at"])
    at = {rel(w.get("path", "")): t(w["at"]) for w in written if w.get("at")}
    def read_in_time(path, born): return next((x for x in reads.get(path, []) if t(x) - born <= DRAFT_S), "")
    rows = []
    for d, _, fs in os.walk(base):
        for f in sorted(fs):
            path = rel(os.path.join(d, f)); born = at.get(path) or os.path.getmtime(os.path.join(d, f))
            got = read_in_time(path, born); age = now - born
            rows.append({"path": path, "age": int(age // 86400), "read": got, "action": "merge" if got else "drop" if age >= DRAFT_S else "wait"})
    old = [p for p, b in at.items() if now - b >= DRAFT_S]; hit = sum(1 for p in old if read_in_time(p, at[p]))
    days = max(1, (now - min(at.values())) / 86400) if at else 1
    return rows, (f"drafts: {len(at)} written, {len(at) / days:.1f} a day; {hit} of {len(old)} old enough read within 14 d ({'n/a' if pct(hit, len(old)) is None else str(pct(hit, len(old))) + '%'}); "
                  f"{len(rows)} waiting in kb/_drafts, {sum(1 for r in rows if r['read'])} read so far")

def report():
    """the lines `kb stats` prints"""
    rs = records(); now = time.time(); end = iso(now + 1)
    began = next((r["at"][:10] for r in rs if r.get("agent") is not None and "at" in r), "not yet")
    def p(v): return "n/a" if v is None else f"{v}%"
    out = [f"Use, observed from Claude Read and Bash tool events; Codex runs and other routes aren't seen; logged since {began}. 8-call baseline, logruler on old transcripts: 261 of 434."]
    for name, secs in (("24 h", 86400), ("7 d", 7 * 86400)):
        x = numbers(iso(now - secs), end, rs)
        out += [f"{name}: {x['finds']} finds; of {x['finds_logged']} logged with results, {x['finds_read']} ({p(x['find_read_pct'])}) with an observed read of a shown result in 10 min, {x['finds_read_8']} ({p(x['find_read_8_pct'])}) in 8 tool calls",
                f"  {x['recalls']} recall injections, {x['recalls_opened']} ({p(x['recall_opened_pct'])}) with a pointer read or opened; {x['pointers_opened']} of {x['pointers']} pointers ({p(x['pointer_opened_pct'])})",
                f"  {x['neighbour_pointers']} live-neighbour pointers, {x['neighbour_pointers_read']} ({p(x['neighbour_read_pct'])}) with the thread read",
                f"  {x['misses']} misses filed, {x['candidates']} miss candidates; {p(x['runs_observable_pct'])} of runs observable"]
    seen = set(); since = iso(now - 14 * 86400)
    for r in rs:
        if r.get("at", "") >= since and ("read" in r or "open" in r): seen.add(r.get("read") or re.sub(r":\d+$", "", r["open"]))
    notes = [n for n in (os.path.relpath(os.path.join(d, f), H) for d, _, fs in os.walk(H + "/kb") for f in fs if f.endswith(".md")) if not n.startswith(("kb/_drafts/", "kb/_dropped/drafts/"))]
    out.append(f"{sum(1 for n in notes if n not in seen)} of {len(notes)} notes in kb/ with no observed read in 14 d (the log only goes back to {began})")
    out.append(drafts(rs, now)[1])
    return out

if __name__ == "__main__":
    # python3 kbuse.py neighbours [T0]: the live-neighbour read share from T0 (UTC, default 2026-11-08T09:43:00, the 2-pointer shape change, i0229) to now
    import sys
    if sys.argv[1:2] == ["neighbours"]:
        x = numbers(sys.argv[2] if len(sys.argv) > 2 else "2026-11-08T09:43:00", iso(time.time() + 1))
        print(f"neighbour_read_pct {x['neighbour_read_pct']}, {x['neighbour_pointers_read']} of {x['neighbour_pointers']} pointers")
    else: print("\n".join(report()))
