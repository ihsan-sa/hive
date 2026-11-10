#!/usr/bin/env python3
"""While you were away: a brief of at most 15 lines on what the Hive did in a window, with no model use. Made, decided for you, needs you, broke, counts of the rest.

`hive away [--since 12h] [--full] [--json]`. The app's Chat home and Overview show the same thing (GET /api/away), and --full and the JSON hold every row.
Everything comes from the log, the board and the asks register, so it holds across a crash or a restart of the Hive. Made goes by evidence and each row
says which (a file, a closed ask, a status of Done): a file can be a failure screenshot, so none of it is called shipped. An agent's last word is only an update."""
import argparse, datetime as dt, json, math, re, subprocess, sys, time
from pathlib import Path
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))   # run with -I, which leaves this folder off the path
import asks, foryou

HOME = Path.home()
STATE = HOME / ".hive"
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")   # agents' names are a letter and hex digits; the Hive's own (starter, keeper...) are words
PERSON = {"person", "box"}
SELF_THREADS = {"starter", "keeper", "improve", "watchdog", "progress"}
FOR_YOU = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?for you(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$", re.I | re.M)
DECIDED = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?decided(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$", re.I | re.M)   # as FOR_YOU
NOTHING = foryou.NOTHING
BY_THEMSELVES = re.compile(r"usage limit|carr(?:y|ies) on by (?:it|them)sel", re.I)   # waits an agent comes back from with no one's help
ASK_ID = re.compile(r"\bk\d{4,}\b")
REASON = re.compile(r"(Done|Blocked|Needs the person|Waiting|Told to stop):")   # the reason words a status bar ends on
CLOSED, SAME_WORDS = STATE / "for-you-closed.jsonl", 0.6   # a For you an agent retracted: {post, at, by, why}
NOT_DELIVERED = ("silent", "nothing")   # as BAD in outcomes.py
BRIEF_LINES, NEEDS_SHOWN, FOR_YOU_DAYS = 15, 5, 14
# The reasons in the starter's words (WHY_WORDS there; test_away.py holds the two the same)
WHY_WORDS = {"connection": "no connection to Claude", "restart": "the machine restarted", "stalled": "went silent with nothing running", "other": "ended before it finished", "budget": "reached its budget or turn limit"}
STOPS = {"limited": "reached the usage limit", "login_rejected": "the Claude login expired", "stopped_by_hand": "stopped by hand", "kill_dropped": "stopped by hand", "wake_failed": "its session couldn't be resumed"}
BACK = {"start", "wake_resumed", "woken", "heir", "to_heir"}

def stamp(s):
    try: return dt.datetime.fromisoformat(str(s).replace("Z", "+00:00")).timestamp()
    except ValueError: return None

def lines(path):
    try: raw = Path(path).read_text(errors="replace").splitlines()
    except OSError: return
    for l in raw:
        try: e = json.loads(l)
        except ValueError: continue
        if isinstance(e, dict): yield e

def first_words(text, n=200):
    """The start of a post, down to whole sentences of at least 40 characters, so "Done." says what was done."""
    t = re.sub(r"^(@\S+\s+)+|\*\*|__", "", " ".join(FOR_YOU.sub("", text or "").split()))
    m = re.match(r".{40,}?[.!?](?=\s|$)", t)
    t = m.group(0) if m else t
    return t if len(t) <= n else t[:n - 1].rstrip() + "…"

def word(n, one, many=None): return f"{n} {one if n == 1 else many or one + 's'}"

def stops(since):
    """Each agent's last stop in the window: why, and whether it carried on after."""
    last, own = {}, set()
    for e in lines(HOME / "log.jsonl"):
        t, ev, name = stamp(e.get("at")), e.get("event"), e.get("name")
        if ev == "start" and e.get("thread") in SELF_THREADS: own.add(name)
        if not name or name in own or t is None or t < since: continue
        if ev in STOPS or ev in ("died", "stalled"):
            why = WHY_WORDS.get("stalled") if ev == "stalled" else WHY_WORDS.get(e.get("why"), WHY_WORDS["other"]) if ev == "died" else STOPS[ev]
            last[name] = {"name": name, "why": why, "at": t, "carried_on": False, "gave_up": False, "stays": ev in ("stopped_by_hand", "kill_dropped", "wake_failed"), "then": "", "event": ev, "reaped": ev in ("died", "stalled")}
        elif ev == "given_up" and name in last: last[name]["gave_up"] = True; last[name]["carried_on"] = False
        elif ev in BACK and name in last: last[name]["carried_on"] = True; last[name]["gave_up"] = False
    for x in last.values():
        x["then"] = "carried on by itself" if x["carried_on"] else "gave up after several tries, so it needs a message" if x["gave_up"] else "stays stopped until you write to it" if x.pop("stays") else "will carry on by itself"
    return last

def norm(t): return re.sub(r"\W+", " ", re.sub(r"(^|\s)@\S+", " ", str(t or "")).lower()).strip()

def clip(t, n): return t if len(t) <= n else t[:n - 1].rstrip() + "…"

def done_evidence(items):
    """{post id: [ask ids it closed]}, from the register: `hive post --done` records the post as the ask's evidence."""
    out = {}
    for i, e in items.items():
        if e.get("status") == "done" and e.get("evidence"): out.setdefault(str(e["evidence"]), []).append(i)
    return out

def bar_done(text):
    """A status bar's latest reason line says Done. A bar can hold detail lines after it, or `Done:` for one part and `Needs the person:` below it; the last reason wins."""
    for line in reversed(str(text or "").splitlines()):
        if m := REASON.match(line.strip()): return m[1] == "Done"
    return False

def status_done():
    """Threads whose status bar says Done. It says the agent called it finished, not that it shipped."""
    last = {}
    for e in lines(STATE / "thread-status.jsonl"):
        if e.get("thread"): last[e["thread"]] = str(e.get("text") or "")
    return {th for th, t in last.items() if bar_done(t)}

def needs_order():
    """The summary's order and its links from a For you post to its ask, from needs-you.json. Only an order and a link: the facts are read here."""
    try: rows = json.loads((HOME / "needs-you.json").read_text()).get("waiting") or []
    except (OSError, ValueError, AttributeError): rows = []
    rank, links = {}, {}
    for i, r in enumerate(rows):
        for key in (r.get("ask"), r.get("post"), norm(r.get("action"))):
            if key: rank.setdefault(key, i)
        if r.get("post") and r.get("ask"): links.setdefault(str(r["post"]), set()).add(r["ask"])
    return rank, links

def stalled_threads(since, now):
    """Threads that went quiet in the window with something still open: core's own test (`hive stalled`), so the two can't differ."""
    bin_ = next((HERE / n for n in ("hive", "hive") if (HERE / n).exists()), None)
    if not bin_: return []
    try: out = subprocess.run([str(bin_), "stalled", "--quiet", "2h", "--within", f"{max(1, math.ceil((now - since) / 60))}m"], capture_output=True, text=True, timeout=30, cwd=str(HOME)).stdout
    except (OSError, subprocess.SubprocessError): return []
    return [{"thread": m[1], "why": m[2]} for m in re.finditer(r"^(\S+)  last .*?open: (.*)$", out, re.M)]

def closed_for_yous():
    """{post id: the record} of the For you lines an agent closed with `hive foryou close`. A row with a rule is code's log of a line it dropped, not a retraction: a Done bar can flip back and the line returns."""
    out = {}
    for e in lines(CLOSED):
        if not e.get("post") or e.get("rule"): continue
        if e.get("reopened"): out.pop(str(e["post"]), None)
        else: out[str(e["post"])] = e
    return out

def close_for_yous(posts, by, why):
    """Takes these For you posts off the person's list for good: the one way to retract a line, so a sweep that finds one obsolete changes the evidence and not only the view."""
    STATE.mkdir(parents=True, exist_ok=True)
    with open(CLOSED, "a") as f:
        for p in posts: f.write(json.dumps({"post": p, "at": time.time(), "by": by, "why": why}, ensure_ascii=False) + "\n")

def reopen_for_yous(posts, by, why):
    """Puts wrongly closed For you lines back on the person's list; the close stays in the file with this row after it."""
    with open(CLOSED, "a") as f:
        for p in posts: f.write(json.dumps({"post": p, "at": time.time(), "by": by, "why": why, "reopened": True}, ensure_ascii=False) + "\n")

def done_but_open(th, text):
    """What `hive status` warns about when a bar says Done and something in the thread still waits on the person: `Done:` hides a For you, and an ask stays on their list under a finished bar. None when it's consistent."""
    if not bar_done(text): return None
    t = open_on_person(done=set())["threads"].get(th)
    if not t: return None
    said = ", ".join(t["asks"] + [f"For you {p}" for p in t["for_yous"]])
    return f"hive status: warning: this bar says Done, and {th} still has open on the person: {said}. If it's finished, close them (`hive asks done|hold|drop`, `hive foryou close POST --why ...`). If your part is done and theirs isn't, end the bar `Needs the person: what`."

def for_yous(posts, items, links, cut, latest_only=False, dropped=None):
    """Every For you line still outstanding: one row a line, gone only on evidence. The ask it names or belongs to left state person,
    the person replied to that post, or the person wrote in the thread after it and named its author. Another person post in the thread clears nothing.
    An agent's `hive foryou close` retracts a line. dropped, a list, collects each line code took off, with its rule.
    latest_only also drops a line when the same agent has a later post in the thread with a For you of its own, even an answered one: that agent has moved on from it. So does a later line in the thread with nearly the same words from anyone: a new agent restating a wait is one wait."""
    rows, in_thread, newest = [], {}, {}
    for p in posts:
        th, who, t, body = p.get("thread"), str(p.get("from", "")).lower(), stamp(p.get("at")), p.get("text") or ""
        if not th or t is None: continue
        if who in PERSON:
            said, to = body.lower(), " ".join(str(x).lower() for x in (p.get("to") if isinstance(p.get("to"), list) else [p.get("to") or ""]))
            for r in rows:
                if p.get("reply_to") == r["post"]: r["answered"] = True
            for r in in_thread.get(th, []):
                if re.search(rf"@{r['from']}\b", said) or re.search(rf"\b{r['from']}\b", to): r["answered"] = True
            continue
        if not AGENT.match(who) or th in SELF_THREADS or t < cut: continue
        for m in FOR_YOU.finditer(body):
            newest[(th, who)] = p.get("id")
            line = " ".join(m[1].split())
            if NOTHING.match(line): continue
            asks = {k for k in ASK_ID.findall(line) if k in items} | {k for k in links.get(str(p.get("id")), ()) if k in items}
            r = {"kind": "for_you", "thread": th, "from": who, "at": t, "post": p.get("id"), "text": clip(line, 220), "asks": sorted(asks)}
            rows.append(r); in_thread.setdefault(th, []).append(r)
    if latest_only:
        later = {}
        for r in reversed(rows):
            w, seen = set(norm(r["text"]).split()), later.setdefault(r["thread"], [])
            r["repeat"] = any(len(w & o) / max(1, len(w | o)) > SAME_WORDS for o in seen)
            seen.append(w)
    out, closed = [], closed_for_yous()
    for r in rows:
        repeat, answered = r.pop("repeat", False), r.pop("answered", False)
        if str(r["post"]) in closed: continue
        rule = ("repeat" if repeat else "answered" if answered else "ask moved" if r["asks"] and all(items[k].get("status") != "person" for k in r["asks"])
                else "newer line" if latest_only and newest.get((r["thread"], r["from"])) != r["post"] else None)
        if rule:
            if dropped is not None: dropped.append({**r, "rule": rule})
            continue
        r["asks"] = [k for k in r["asks"] if items[k].get("status") == "person"]
        out.append(r)
    return out

def view_rule(r, done):
    """Why open_on_person leaves out a For you that for_yous kept, or None."""
    if r["thread"] in done: return "done bar"
    if r["asks"]: return None
    return "ends by itself" if BY_THEMSELVES.search(r["text"]) else "offer" if foryou.OFFER.match(r["text"]) else None

RULE_WHY = {"repeat": "a later line in the thread says nearly the same", "answered": "the person replied or named its author in the thread", "ask moved": "the ask it belongs to left the person",
            "newer line": "the same agent wrote a newer For you in the thread", "done bar": "the thread's status bar says Done", "ends by itself": "a wait that ends with no one's help", "offer": "an offer: nothing stops until it is answered"}

def rule_closes(now=None, posts=None, items=None, links=None, done=None):
    """Every For you line within the horizon that code, not an agent, keeps off the person's list, each with its rule."""
    now = now or time.time()
    posts = list(lines(HOME / "board.jsonl")) if posts is None else posts
    items = asks.ledger() if items is None else items
    links = needs_order()[1] if links is None else links
    done = status_done() if done is None else done
    out = []
    for r in for_yous(posts, items, links, now - FOR_YOU_DAYS * 86400, latest_only=True, dropped=out):
        if rule := view_rule(r, done): out.append({**r, "rule": rule})
    return out

def unlogged_closes(rows=None):
    """The rule closes with no row yet in the closes file."""
    rows = rule_closes() if rows is None else rows
    seen = {(str(e.get("post")), e.get("line")) for e in lines(CLOSED) if e.get("rule")}
    return [r for r in rows if (str(r["post"]), r["text"]) not in seen]

def log_rule_closes(now=None, posts=None, items=None, links=None, done=None):
    """Writes one row per line code closed, once, in the same file as agents' closes (review B1), so they can be counted and audited. The first run backdates what it finds to the post's time and marks it, since when each dropped isn't known."""
    now = now or time.time()
    rows = unlogged_closes(rule_closes(now, posts, items, links, done))
    first = not any(e.get("rule") for e in lines(CLOSED))
    if rows:
        STATE.mkdir(parents=True, exist_ok=True)
        with open(CLOSED, "a") as f:
            for r in rows:
                rec = {"post": r["post"], "at": r["at"] if first else now, "by": "code", "why": RULE_WHY[r["rule"]], "rule": r["rule"], "line": r["text"], "thread": r["thread"], "from": r["from"]}
                if first: rec["backfill"] = True
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    return len(rows)

def open_on_person(now=None, posts=None, items=None, links=None, done=None):
    """The one answer to "what waits on the person": every surface states pair(this). {"threads": {thread: {"asks": [ids], "for_yous": [post ids], "newest": epoch}}, "asks": rows, "for_yous": rows}.
    A thread is open when it has an ask in state person, or a For you open by evidence: for_yous' rules, the latest from each agent, not in a thread whose status bar says Done, not a wait that ends by itself, not an offer ("if you want...") with no ask behind it.
    An ask stays until the register moves it, whatever the thread's bar says. links and done default to the live files, as posts and items do."""
    now = now or time.time()
    posts = list(lines(HOME / "board.jsonl")) if posts is None else posts
    items = asks.ledger() if items is None else items
    links = needs_order()[1] if links is None else links
    done = status_done() if done is None else done
    rows = [r for r in for_yous(posts, items, links, now - FOR_YOU_DAYS * 86400, latest_only=True) if not view_rule(r, done)]
    person = [{**e, "id": i} for i, e in items.items() if e.get("status") == "person" and e.get("kind") != "direction"]
    threads = {}
    def thread(th, at):
        t = threads.setdefault(th, {"asks": [], "for_yous": [], "newest": 0})
        t["newest"] = max(t["newest"], at or 0)
        return t
    for e in person: thread(e.get("thread") or e.get("post") or "asks", stamp(e.get("when") or e.get("at")))["asks"].append(e["id"])
    for r in rows:
        ids = thread(r["thread"], r["at"])["for_yous"]
        if r["post"] not in ids: ids.append(r["post"])
    return {"threads": threads, "asks": person, "for_yous": rows}

def pair(o): return f"{word(len(o['threads']), 'thread')}, {word(len(o['asks']), 'ask')}"

def distinct(rows):
    """One row per action: the same ask id, then the same words. Sharing a thread alone merges nothing. Every ask id and thread is kept on the row."""
    out, by_ask, by_text = [], {}, {}
    for r in rows:
        key = norm(r["text"])
        hit = next((by_ask[k] for k in r.get("asks", []) if k in by_ask), None) or by_text.get(key)
        if hit is None:
            hit = {**r, "asks": list(r.get("asks", [])), "threads": [r["thread"]] if r.get("thread") else []}
            out.append(hit)
        else:
            hit["asks"] += [k for k in r.get("asks", []) if k not in hit["asks"]]
            if r.get("thread") and r["thread"] not in hit["threads"]: hit["threads"].append(r["thread"])
            if r["kind"] == "for_you" and hit["kind"] == "ask": hit.update(kind="for_you", text=r["text"], post=r["post"], thread=r["thread"], at=r["at"])   # the agent's line says what to do
        for k in hit["asks"]: by_ask[k] = hit
        by_text[key] = hit
    return out

def budget(s):
    """How many rows of each section fit in BRIEF_LINES, title and the closing counts line included. Needs you gets its 5 first."""
    size = {k: len(s[k]) for k in ("made", "decided", "needs", "broke")}
    left = BRIEF_LINES - 2 - sum(n > 0 for n in size.values()) - bool(s["agents_wait"])
    show = {k: 0 for k in size}
    def give(k):
        nonlocal left
        if left > 0 and show[k] < size[k] and (k != "needs" or show[k] < NEEDS_SHOWN): show[k] += 1; left -= 1; return True
    for k in ("needs", "made", "broke", "decided"): give(k)
    while give("needs"): pass
    while any([give(k) for k in ("made", "broke", "decided")]): pass
    return show

def summary(since, now=None):
    """What /api/away serves and `hive away` prints. The window is since..now, seconds since the epoch. Each list is whole; "show" says how many of it make the brief."""
    now = now or time.time()
    st = {}
    try: st = json.loads((STATE / "starter.json").read_text())
    except (OSError, ValueError): pass
    items = asks.ledger()
    rank, links = needs_order()
    closed, finished = done_evidence(items), status_done()
    posts = list(lines(HOME / "board.jsonl"))
    made, decided, last, names, latest = {}, [], {}, {}, {}
    for p in posts:
        th, who, t, body = p.get("thread"), str(p.get("from", "")).lower(), stamp(p.get("at")), p.get("text") or ""
        if not th or t is None: continue
        latest[th] = max(latest.get(th, 0), t)
        if not AGENT.match(who) or who in PERSON: continue
        names[who] = th
        if t < since or "@new" in body.split(): continue     # a brief for a fresh agent isn't a result
        decided += [{"thread": th, "from": who, "at": t, "post": p.get("id"), "text": clip(" ".join(m[1].split()), 220)} for m in DECIDED.finditer(body)]
        if th in SELF_THREADS: continue
        row = {"thread": th, "from": who, "at": t, "post": p.get("id"), "text": first_words(body)}
        last[th] = row
        done = p.get("done") or []
        shut = list(dict.fromkeys(([done] if isinstance(done, str) else list(done)) + closed.get(str(p.get("id")), [])))
        evidence = [f"closed ask {k}" for k in shut] + (["posted a file"] if p.get("files") else [])
        if evidence: made[p.get("id")] = {**row, "evidence": evidence}
    for th in finished & set(last):     # the thread's latest agent post carries the status
        r = last[th]; made.setdefault(r["post"], {**r, "evidence": []})["evidence"].append("status Done")
    seen, firsts, rest = set(), [], []
    strength = lambda r: 0 if r["evidence"][0].startswith("closed") else 1 if r["evidence"][0] == "posted a file" else 2
    for r in sorted(made.values(), key=lambda x: (strength(x), -x["at"])):   # the firmest evidence first, one a thread, so a busy thread can't fill the brief
        (rest if r["thread"] in seen else firsts).append(r); seen.add(r["thread"])
    made_rows = firsts + rest
    updates = sorted((r for th, r in last.items() if th not in seen), key=lambda x: -x["at"])

    stopped = stops(since)
    for x in stopped.values(): x["thread"] = names.get(x["name"])
    needs, agents_wait = [], []
    for login, a in (st.get("auth_told") or {}).items():
        n = sum(r.get("login") == login for r in (st.get("resume") or {}).values()) + len(st.get("pending") or {})
        needs.append({"kind": "sign_in", "agents": n, "at": now, "asks": [], "text": f"Sign in again to Claude: {word(n, 'agent')} {'is' if n == 1 else 'are'} waiting."})
    until = [t for k, t in (st.get("limits") or {}).items() if t > now and "#" not in str(k)]  # "login#fable": one model's hold, not everyone's
    if until: agents_wait.append({"kind": "limit", "until": min(until), "text": "the usage limit; they carry on by themselves when it resets"})
    lines_for_you = []
    for r in for_yous(posts, items, links, now - FOR_YOU_DAYS * 86400):
        if not r["asks"] and BY_THEMSELVES.search(r["text"]): agents_wait.append({**r, "kind": "for_you"})
        else: lines_for_you.append(r)
    for i, e in items.items():
        if e.get("status") != "person": continue
        why = str(e.get("why") or "")
        lines_for_you.append({"kind": "ask", "thread": e.get("thread") or e.get("post"), "at": stamp(e.get("when") or e.get("at")) or now, "post": e.get("post"), "asks": [i], "text": clip(f"{e.get('ask', '')}. Why it waits: {why}" if why else str(e.get("ask", "")), 220)})
    def order(r):
        keys = [rank[k] for k in [*r.get("asks", []), str(r.get("post")), norm(r["text"])] if k in rank]
        return (min(keys), 0) if keys else (len(rank), r["at"])
    needs += sorted(distinct(lines_for_you), key=order)

    broke = {}
    def fault(th, name, at, why, **more):
        b = broke.setdefault(th or name, {"thread": th, "from": name, "at": at, "text": why, "why": [], **more})
        if why not in b["why"]: b["why"].append(why)
        b["text"] = "; ".join(b["why"]); b["at"] = max(b["at"], at)
    runs = {}
    for o in lines(HOME / "outcomes.jsonl"): runs[(o.get("thread"), o.get("name"))] = o    # the latest grade of each agent in each thread
    for (th, name), o in runs.items():
        t = stamp(o.get("ended") or o.get("at"))
        if o.get("grade") in NOT_DELIVERED and t and t >= since and th not in SELF_THREADS: fault(th, name, t, f"graded as not delivering ({o['grade']})", post=o.get("post"))
    for x in stopped.values():
        if not x["carried_on"] and x["event"] == "limited": agents_wait.append({"kind": "limit", "from": x["name"], "thread": x.get("thread"), "at": x["at"], "text": f"{x['name']}, stopped at the usage limit"})
        elif not x["carried_on"] and x["reaped"] and latest.get(x["thread"], 0) <= x["at"]: fault(x["thread"], x["name"], x["at"], f"run ended: {x['why']}")
    for x in stalled_threads(since, now): fault(x["thread"], None, latest.get(x["thread"], now), f"went quiet with this open: {x['why']}")

    s = {"version": 2, "since": since, "now": now, "made": made_rows, "decided": sorted(decided, key=lambda x: -x["at"]), "needs": needs, "agents_wait": agents_wait,
         "broke": sorted(broke.values(), key=lambda x: -x["at"]), "updates": updates, "stopped": sorted(stopped.values(), key=lambda x: (x["carried_on"], -x["at"]))}
    s["counts"] = {k: len(s[k]) for k in ("made", "decided", "needs", "agents_wait", "broke", "updates", "stopped")}
    on_person = open_on_person(now, posts, items, links, finished)
    s["counts"].update(threads=len(on_person["threads"]), asks=len(on_person["asks"]))
    s["needs_head"] = f"{pair(on_person)} need you" if on_person["threads"] else "Needs you"   # a sign-in has no thread, so it can be all there is
    s["show"] = budget(s)
    return s

def row(r, n=0):
    label = ", ".join(r.get("evidence") or [])
    out = "- " + (f"{r['thread']}: " if r.get("thread") else "") + (f"{label}: " if label else "") + r["text"]
    return clip(out, n) if n else out

def text(s, full=False):
    head = f"While you were away (since {dt.datetime.fromtimestamp(s['since']).astimezone():%a %H:%M}):"
    if not any(s["counts"].values()): return head + "\nNothing happened."
    out, n = [head], 0 if full else 118
    def part(title, key):
        rows, show = s[key], len(s[key]) if full else s["show"][key]
        if rows: out.extend([f"{title} ({len(rows) if show == len(rows) else f'{show} of {len(rows)}'}):"] + [row(r, n) for r in rows[:show]])
    part("Made", "made"); part("Decided for you", "decided")
    if s["needs"]:   # the head is the one count; the rows are lines of it, and several can share a thread
        show = len(s["needs"]) if full else s["show"]["needs"]
        out.extend([f"{s['needs_head']} ({word(show, 'line')}{' shown' if show < len(s['needs']) else ''}):"] + [row(r, n) for r in s["needs"][:show]])
    w = s["agents_wait"]
    if w and full: out.extend([f"Agents wait on ({len(w)}):"] + [row(r) for r in w])
    elif w: out.append(clip("Agents wait on: " + w[0]["text"] + (f"; and {len(w) - 1} more" if len(w) > 1 else "") + ".", n))
    part("Broke", "broke")
    if full:
        if s["updates"]: out.extend([f"Other updates ({len(s['updates'])}):"] + [row(r) for r in s["updates"]])
        if s["stopped"]: out.extend([f"Stopped ({len(s['stopped'])}):"] + [f"- {x['name']}: {x['why']}; {x['then']}." for x in s["stopped"]])
    else:
        back = sum(x["carried_on"] for x in s["stopped"])
        out.append(f"Also: {word(len(s['updates']), 'other thread')} with updates, {word(len(s['stopped']), 'agent')} stopped ({back} carried on). All of it: hive away --full")
    return "\n".join(out)

def window(s):
    m = re.fullmatch(r"(\d+)([hd])", s or "")
    if not m: sys.exit("away: --since takes hours or days, like 12h or 2d")
    return int(m[1]) * (3600 if m[2] == "h" else 86400)

if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--since", default="12h"); ap.add_argument("--full", action="store_true", help="every row, not the brief"); ap.add_argument("--json", action="store_true"); ap.add_argument("--unlogged", action="store_true", help="count For you lines code closed with no row in the closes file, and stop")
    a = ap.parse_args()
    if a.unlogged: print(len(unlogged_closes())); sys.exit(0)
    s = summary(time.time() - window(a.since))
    print(json.dumps(s) if a.json else text(s, a.full))
