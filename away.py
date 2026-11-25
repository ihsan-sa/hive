#!/usr/bin/env python3
"""While you were away: a brief of at most 15 lines on what the Hive did in a window, with no model use. Made, decided for you, needs you, broke, counts of the rest.

`hive away [--since 12h] [--full] [--json]`. The app's Chat home and Overview show the same thing (GET /api/away), and --full and the JSON hold every row.
Everything comes from the log, the board and the asks register, so it holds across a crash or a restart of the Hive. Made goes by evidence and each row
says which (a file, a closed ask, a status of Done): a file can be a failure screenshot, so none of it is called shipped. An agent's last word is only an update."""
import argparse, datetime as dt, json, math, re, subprocess, sys, time
from pathlib import Path
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))   # run with -I, which leaves this folder off the path
import asks, foryou, promises

HOME = Path.home()
STATE = HOME / ".hive"
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")   # agents' names are a letter and hex digits; the Hive's own (starter, keeper...) are words
PERSON = {"person", "box"}
WATCHERS = {"rootwarden", "boxwatch"}     # the Hive's own that ask the person for things only they can do: their For you lines count like an agent's
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
        for x in [r] + [a for a in r.get("also") or [] if isinstance(a, dict)]:   # also: rows the summary merged into this one (needs_you.merge_dups): the same thing to do, so its post links to the row's ask
            for key in (x.get("ask"), x.get("post"), norm(x.get("action"))):
                if key: rank.setdefault(key, i)
            if x.get("post") and (x.get("ask") or r.get("ask")): links.setdefault(str(x["post"]), set()).add(x.get("ask") or r["ask"])
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
    A later post in the thread from the same agent (or the owner of the line's ask) that tells the person nothing waits on them now (foryou.stood_down) overtakes its earlier lines there.
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
        if not (AGENT.match(who) or who in WATCHERS) or th in SELF_THREADS or t < cut: continue
        if foryou.stood_down(body, p.get("to")):   # "nothing waits on you" (ad5b1, 2026-11-25) overtakes that agent's own earlier lines, or ones whose ask it owns:
            for r in in_thread.get(th, []):         # not another agent's (aa008's review: a86e6 on its own fix had cleared ae6a2's E17 decision)
                if r["from"] == who or any(str(items[k].get("owner") or "").lower() == who for k in r["asks"]): r.setdefault("stood", p.get("id"))
        if (pg := premise_line(body)):   # "Premise gone: why" (ad748, 2026-11-25: a printer unplugged, a project paused): the speaker's own earlier lines, and any it names by ask or post id
            for r in in_thread.get(th, []):
                if premise_covers(pg, who, r["from"], {str(items[k].get("owner") or "").lower() for k in r["asks"]}, set(r["asks"]) | {str(r["post"])}): r.setdefault("gone", p.get("id"))
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
        repeat, answered, stood, gone = r.pop("repeat", False), r.pop("answered", False), r.pop("stood", None), r.pop("gone", None)
        if str(r["post"]) in closed: continue
        rule = ("repeat" if repeat else "answered" if answered else "premise gone" if gone else "stood down" if stood else "ask moved" if r["asks"] and all(items[k].get("status") != "person" for k in r["asks"])
                else "newer line" if latest_only and newest.get((r["thread"], r["from"])) != r["post"] else None)
        if rule:
            if dropped is not None: dropped.append({**r, "rule": rule})
            continue
        r["asks"] = [k for k in r["asks"] if items[k].get("status") == "person"]
        out.append(r)
    return out

# A stated deadline (friction-asks A10, 2026-11-25). Read narrowly, since a wrong close hides a live ask and a missed one only stays on the list (a0ad9's and the
# independent review's cases): the marker must be followed straight away by the time or date ("by 26 November", "before about 06:33Z", "due 11-27 3:13 pm"), with only
# filler words between. "until" and "expires"/"closes" are left out: "ran until 13:00Z" tells what happened, and a token that expires matters more after, not less.
DEADLINE = re.compile(r"\b(?:by|before|no later than|deadline(?:\s+is)?|due(?!\s+to\b)(?:\s+(?:by|on|at))?)\b", re.I)
WORDSPAN = re.compile(r"\b(?:half an|an|a|one|two|three|four|five|six|ten|fifteen|twenty|thirty|forty|forty-five|a few|a couple(?: of)?)\s+(?:minutes?|hours?|days?|weeks?)\b", re.I)   # "two hours": a length of time, not a deadline (own copy: promises.py has none committed)
FILLER = re.compile(r"(?:\s|,|~|\b(?:about|around|roughly|approximately|approx|on|at|the|end|of|this|today|tonight|noon)\b)*", re.I)
WHEN_START = re.compile(r"\d|\btomorrow\b|\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\b", re.I)
CLAUSE_END = re.compile(r"[;!?\n]|\.(?=\s+[A-Z(]|\s*$)(?<!\b[ap]\.m\.)|\bor\b|\band\b|,\s+(?!\d)")
FALLBACK = re.compile(r"\b(?:after (?:that|this|then)\b|later,|otherwise\b|or after\b|if (?:you )?miss\b|failing that\b)", re.I)   # the ask stands past the time; only the route changes
DEADLINE_GRACE_S = 12 * 3600   # past a stated deadline before the line leaves the list: the person may be a little late, and a timezone guess may be off
PREMISE = re.compile(r"^premise gone:[ \t]*(.+)$", re.I | re.M)   # an agent's line: what it asked of the person no longer applies. Only the bare label at the start of a line: a bullet or bold label is someone explaining the rule (a0ad9, 1125021150a6: ac178's own 1125015472ab parsed as one)
PREMISE_IDS = re.compile(r"(?<![\w/])(k\d{4,}|[0-9a-f]{12})(?![\w/])")

def deadline(text, at):
    """Unix time of the deadline a line states ("by 26 November", "before about 06:33Z", "due 11-27"), or None. Only a marker word followed at once (filler aside) by a clock time,
    a date or "tomorrow" counts, read up to the end of its clause. None when the line names a fallback ("after that, ..."), when a bare clock time was already past when the line was
    written (at), and for a length of time ("takes about two hours")."""
    out = deadlines(text, at)
    return max(w for w, _, _ in out) if out else None

def deadlines(text, at, roll=False):
    """[(unix time, start, end)] of each deadline the line states, as deadline() reads them, with the span of its words ("before about 06:33Z") in text.
    roll: a bare clock time already past at `at` is the next day's instead of none, for a line whose `at` is later than when it was first said (a restored ask's status_at)."""
    if not at: return []
    text = str(text or "")
    if FALLBACK.search(text): return []
    out = []
    for m in DEADLINE.finditer(text):
        rest = text[m.end():]
        lead = FILLER.match(rest).end()
        if not WHEN_START.match(rest, lead): continue
        end = CLAUSE_END.search(rest, lead); bit = rest[lead:end.start() if end else len(rest)][:40]
        if promises.SPAN.search(bit) or WORDSPAN.search(bit): continue
        bit = bit + (rest[len(bit) + lead:len(bit) + lead + 1] if rest[len(bit) + lead:len(bit) + lead + 1] == "." else "")   # "3:30 p.m." keeps its last dot
        dated = any(r.search(bit) for r in (promises.DATE, promises.DAYMON, promises.MD, promises.TOMORROW))
        clocks = [x for x in promises.CLOCK.finditer(bit) if x[2] is not None or x[3]]
        if not dated:
            c = clocks[0] if clocks else None
            if not c: continue
            h, mi, ap = int(c[1]), int(c[2] or 0), (c[3] or "").lower().replace(".", "")
            if ap: h = h % 12 + (12 if ap == "pm" else 0)
            if h > 23 or mi > 59: continue
            base = dt.datetime.fromtimestamp(at, promises._tz(c[4]))
            when = base.replace(hour=h, minute=mi, second=0, microsecond=0).timestamp()
            if when < at and not roll: continue   # a clock time already past when it was written: "next morning" or a slip; not read as a deadline
            if when < at: when += 86400
            out.append((when, m.start(), m.end() + lead + c.end())); continue
        t = promises.due(bit, at)
        ends = [x.end() for r in (promises.DATE, promises.DAYMON, promises.MD, promises.TOMORROW) for x in r.finditer(bit)] + [x.end() for x in clocks]
        if t != at + promises.DEFAULT_S and t > at: out.append((t, m.start(), m.end() + lead + max(ends)))
    return out

def passed_said(text, at, now):
    """The line with each deadline it states that has already passed taken out, and a note saying it passed: "Approve the change before about 06:33Z so it can go in" reads
    "Approve the change so it can go in (its deadline passed)". A restored ask can outlive its deadline, and a passed time shown as live reads as urgent (a0ad9, 1125021150a6).
    `at` may be later than the line was first said (k1161's status_at is 10:57Z, its "before about 06:33Z" was said at 02:58Z), so a bare clock time is the latest day it can mean:
    past `at`, the next one. No date in the note: which day a bare time meant is a guess. The line unchanged when no deadline in it has passed."""
    text = str(text or ""); said = deadlines(text, at, roll=True); gone = [d for d in said if d[0] <= now]
    if not gone or len(gone) < len(said): return text   # one still ahead ("by 26 November, before about 06:33Z"): the line stands as said (a0ad9, 112503027c3b)
    for _, a, b in sorted(gone, key=lambda d: -d[1]): text = text[:a].rstrip() + text[b:]
    text = re.sub(r"\s+([.,;:!?)])", r"\1", re.sub(r"[ \t]{2,}", " ", text)).strip()
    text = re.sub(r"[,;:]+(?=[.!?]|$)", "", text).strip()   # "Approve the change," + "." left by the cut
    return f"{text} (its deadline passed)"

def past_deadline(text, at, now):
    """The stated deadline when it passed more than DEADLINE_GRACE_S before now, else None."""
    d = deadline(text, at)
    return d if d and now > d + DEADLINE_GRACE_S else None

def premise_line(body):
    """(why, {ids named}) from an agent post's "Premise gone: why" line, or None. Quoted lines and code blocks don't count: a review quoting one closes nothing."""
    body = re.sub(r"```.*?```", " ", str(body or ""), flags=re.S)
    body = "\n".join(l for l in body.splitlines() if not l.lstrip().startswith(">"))
    m = PREMISE.search(body)
    if not m or not " ".join(m[1].split()) or NOTHING.match(m[1]): return None
    return clip(" ".join(m[1].split()), 160), set(PREMISE_IDS.findall(m[1]))

def premise_covers(pg, who, author, owners, ids):
    """Whether a Premise gone line from `who` reaches a line or ask: the speaker's own (it wrote it or owns its ask), or one the line names by ask or post id."""
    return who == str(author or "").lower() or who in owners or bool(pg[1] & set(map(str, ids)))

def premise_gone(posts):
    """{thread: [(unix time, post id, from, why, ids)]} of agent "Premise gone: why" lines in each thread: what the speaker asked of the person before it no longer applies
    (the device is unplugged, the project finished or paused), plus any ask or post it names by id. The line itself is the evidence and stays on the board."""
    out = {}
    for p in posts:
        who, th, t = str(p.get("from", "")).lower(), p.get("thread"), stamp(p.get("at"))
        if not th or t is None or not (AGENT.match(who) or who in WATCHERS): continue
        pg = premise_line(p.get("text"))
        if pg: out.setdefault(th, []).append((t, p.get("id"), who, pg[0], pg[1]))
    return out

SHOP_STATUS = HOME / ".hive" / "shop-status.json"   # written by modules/dash/shops.py: {shop id: {"state": "in"|"out"|"unknown", "at": epoch of the check}}
SHOP_NAMES = {"jlc": ("JLCPCB", r"\bJLC(?:PCB)?\b"), "lcsc": ("LCSC", r"\bLCSC\b"), "inventree": ("InvenTree", r"\bInvenTree\b"),
              "link": ("Link", r"\b(?:Stripe )?Link(?= (?:app|account|sign|log|cookie|session|wallet))")}
SHOP_SIGNIN = re.compile(r"\bcookies?\b|\bsign(?:ed|s|ing)?[- ]?in\b|\blog(?:ged|s|ging)?[- ]?in\b|\blogin\b|\bsession\b|\bre-?auth", re.I)

def shop_status():
    """shops.py's last check of each shop, or {} when there is none."""
    try: d = json.loads(SHOP_STATUS.read_text())
    except (OSError, ValueError): return {}
    return d if isinstance(d, dict) else {}

SHOP_SIGNIN_ASK = re.compile(r"^\W*(?:please\s+)?(?:(?:sign|log)(?:\s+(?:back|again))?[- ]?(?:in|on)\b|re-?(?:auth\w*|log\s?in|sign[- ]?in)\b|refresh\s+(?:the\s+)?(?:\w+\s+){0,2}(?:session|login|sign[- ]?in|cookies?)\b)", re.I)
SHOP_SPEND = re.compile(r"\b(?:pay|paid|paying|payment|check(?:ing)?[- ]?out|order(?:s|ed|ing)?|buy|bought|purchase|approv\w*|charg\w*|invoice|confirm\w*|submit\w*|accept\w*|"
                        r"upload\w*|repl(?:y|ies|ied)|answer\w*|choose|pick|decide|review\w*|agree\w*|sign(?:s|ed)? (?:off|the)\b)\b|\$|€|£", re.I)   # a step of the person's own besides the sign-in
SHOP_ONLY_SIGNIN = set("""sign signs signed signing log logs logged logging login in on into back again re-auth reauth reauthenticate refresh session sessions cookie cookies browser profile
    account accounts site the a an to your its both please now with and then also too expired dead new fresh jlc jlcpcb lcsc inventree link stripe app
    on from through via using here machine computer desktop pc server box laptop phone iphone dashboard dash page today tonight tomorrow morning soon possible as when you can""".split())   # where and when: "on the machine", "as soon as you can" (a0ad9, 112503535a3a)
def _machine_words():
    """This machine's own names, read at runtime so no site name sits in the source (ad748, 1125040184c6): the hostname and the words in ~/.hive/machine-names (what people call it)."""
    import socket
    w = set(re.findall(r"[a-z][a-z0-9-]*", socket.gethostname().split(".")[0].lower()))
    try: w |= set(re.findall(r"[a-z][a-z0-9-]*", (HOME / ".hive" / "machine-names").read_text().lower()))
    except OSError: pass
    return w
SHOP_ONLY_SIGNIN |= _machine_words()
SHOP_PERSON_ASK = re.compile(r"^\W*(?:(?:please|can you|could you|would you)\s+)?(?:look|check|see|view|read|test|try|listen|watch|tell|say|let|open|inspect|compare|choose|pick|decide|answer|reply|"
                             r"confirm|review|give|send|share|note|plug|press|walk|click|tap|fill|enter|type|paste|add|install|run|set|update|allow|accept|upload)\b|\byour?\b", re.I)   # the ask is a step of the person's own, whatever its why says
SHOP_ONLY_WHY = re.compile(r"\b(?:only|just(?!\s+(?:now|expired|ended|died|lapsed|timed|logged|ran|went|got|been|happened|dropped|broke)\b)|all (?:that's|that is|it) (?:left|needs?)|nothing (?:else|more)|no other)\b", re.I)   # the why says the sign-in is the person's whole part
SHOP_THEN_PERSON = re.compile(r"\b(?:then|afterwards?|after that|later|next)\b[^.;]*\b(?:the person|you)\b(?!')", re.I)   # "... then the person picks the finish", "next you choose the colour" (a0ad9, 11250411323b)
SHOP_PERSON_SUBJ = re.compile(r"\b(?:the person|you(?:'(?:ll|d|ve|re))?)\b(?!')", re.I)   # "they" is mostly the cookies ("they expire every few days"), so it isn't a subject here (a0ad9, 11250428cd6d); "you'll" is (112504420137)
SHOP_THEN_THEY = re.compile(r"\bthen,?\s+they\b", re.I)   # "..., then they pick the finish": "they" after "then" is the person (a0ad9, 112504420137)
SHOP_STEP_SKIP = set("can could will would must should may might shall need needs needed has have had to do does did only just then also still afterwards afterward later next first simply again now please be".split())
SHOP_STEP_NOT = set("""sign signs signing signed sign-in log logs logging logged login log-in re-auth reauth reauthenticate re-sign resign refresh refreshes refreshing in on into back out
    is are was were the a an their its this that it it's them those these i we""".split())   # the word after the person that is no step of theirs: the sign-in itself, or no verb at all
SHOP_SIGNIN_ORDER = re.compile(r"(?:^|[.;:!]\s*|\b(?:just|only|please)\s+)(?:sign|log)(?:\s+back)?\s+in\b", re.I)   # "just sign in, and then check the render": the sign-in as an order to the person, no subject (a0ad9, 112504469c01)
SHOP_THEN_ORDER = re.compile(r"(?:^|[.;:!?,-]\s*)(?:and\s+)?(?:then|afterwards?|after that|next)\b,?", re.I)   # "just sign in to JLC; then check the render": a step as an order opening the next clause (a0ad9, 11250502652f)
SHOP_STEP_JOIN = {"and", "then", "but", "plus"}   # "sign in and pick the finish", "sign in, then they pick": a second step in the same clause
SHOP_SUBJ_WORDS = {"they", "you", "you'll", "you'd", "you've", "you're"}

def _shop_word(w): return bool(re.fullmatch(r"[a-z][a-z'-]*", w))

def _shop_step_at(toks, k):
    """True when toks[k:] begins with the person's step: an optional subject, then modals and adverbs, then a verb that is no sign-in word."""
    if toks[k:k + 2] == ["the", "person"]: k += 2
    elif k < len(toks) and toks[k] in SHOP_SUBJ_WORDS: k += 1
    while k < len(toks) and toks[k] in SHOP_STEP_SKIP: k += 1
    return k < len(toks) and _shop_word(toks[k]) and toks[k] not in SHOP_STEP_NOT

def _shop_clause_step(toks, k):
    """After a sign-in word at toks[k], True when the same clause (up to . ; or a bracket) joins a second step: "sign in and approve the quote", "signs in, then picks the finish"."""
    for n in range(k + 1, len(toks)):
        if toks[n] in (".", ";", "?", "!"): return False   # a bracket goes on: "only you can sign in (then pick the finish)" (a0ad9, 112504469c01)
        if toks[n] in SHOP_STEP_JOIN and _shop_step_at(toks, n + 1): return True
    return False

def shop_person_step(why):
    """True when the why has the person doing something besides the sign-in: "the person then approves it", "afterwards the person picks the finish", "only you can sign in and confirm the cart".
    After "the person" or "you", modals and adverbs are skipped ("can", "only needs to", "will then"); the next word is their step unless it is a sign-in word or no verb,
    so "only the person can sign in again", "you just need to sign in", "as soon as you can." and "which the person does in the browser" name no step (a0ad9, 11250428cd6d).
    After a sign-in word the clause is read on: "and", "then" or "but" followed by another verb is a second step (a0ad9, 112504420137)."""
    if SHOP_THEN_PERSON.search(why): return True
    low = why.lower()
    for m in SHOP_THEN_THEY.finditer(low):
        if _shop_step_at(re.findall(r"[a-z][a-z'-]*|\S", low[m.end():]), 0): return True
    for m in SHOP_THEN_ORDER.finditer(low):
        if _shop_step_at(re.findall(r"[a-z][a-z'-]*|\S", low[m.end():]), 0): return True
    for m in SHOP_SIGNIN_ORDER.finditer(low):
        if _shop_clause_step(re.findall(r"[a-z][a-z'-]*|\S", low[m.end():]), -1): return True
    for m in SHOP_PERSON_SUBJ.finditer(low):
        toks = re.findall(r"[a-z][a-z'-]*|\S", low[m.end():])
        for k, w in enumerate(toks):
            if w in SHOP_STEP_SKIP: continue
            if _shop_word(w) and w not in SHOP_STEP_NOT: return True
            if _shop_word(w) and _shop_clause_step(toks, k): return True
            break
    return False

SHOP_REASON = re.compile(r"\b(?:so|because|since|as)\b(?![^.;]*\byour?\b)[^.;]*", re.I)   # "... so the cart can be refreshed": the agent's step, not the person's; "so you can confirm" is theirs
SHOP_NOT_SIGNIN = re.compile(r"\bpass(?:word|code|key)s?\b|\b2fa\b|\btwo[- ]factor\b|\bverif\w*|\bOTP\b|\bphone\b|\bexport\w*|\bcookies?\.txt\b|\bcookies?\s+(?:file|to)\b|~/|/home/", re.I)

def shop_signed(e, shops):
    """(shop label, check epoch) when person ask e waits only on a shop's sign-in and shops.py's checks of every shop it names, made after the ask went to the person, say signed in; else None.
    A sign-in wait is an ask whose verb is the sign-in itself ("Sign in to JLCPCB again", "Refresh the JLC session") or whose why says the wait is the sign-in or cookies (k1484: an agent
    blocked on the JLC cart), only when that why says the sign-in is the person's only part ("only", "just", "nothing else"), names no step of theirs after it, and the ask is the agent's own job, not
    a step of the person's ("Look at the JLC board render", anything to "you"). Never one that also spends or approves (pay, check out, order, buy, approve, charge, a price), nor one about a password, 2FA or verification, nor one that
    wants a cookie export or a file, nor a sign-in ask with any step of the person's after it ("and confirm the quote", "then upload the gerbers"): shops.py reads the browser profile, not a file, and a signed-in shop still leaves those to the person (a0ad9, 112503027c3b). Every shop the ask
    names must be in: "Sign in to JLCPCB and LCSC" stands while LCSC is out. Only a check newer than the wait counts: an older "in" doesn't answer an agent that hit a dead session since
    (k1484: fresh JLC cookies asked for at 05:38 while the person had signed in the night before; a4d66 and ad748, friction-asks 11250232ed0b)."""
    ask, why = str(e.get("ask") or ""), str(e.get("why") or "")
    text, since = f"{ask} {why}", stamp(e.get("status_at") or e.get("when") or e.get("at"))
    if since is None or SHOP_SPEND.search(ask) or SHOP_NOT_SIGNIN.search(text): return None
    if not (SHOP_SIGNIN_ASK.search(ask) or SHOP_SIGNIN.search(why)): return None
    if not SHOP_SIGNIN_ASK.search(ask) and (SHOP_PERSON_ASK.search(ask) or not SHOP_ONLY_WHY.search(why) or shop_person_step(why)): return None   # a why-only wait drops only when
    # the why says the sign-in is the person's only part: a verb list always misses one ("Click Allow on the JLC page", "Enter the JLC coupon code"; a0ad9, 112503360d00 and 112503535a3a)
    if SHOP_SIGNIN_ASK.search(ask) and set(re.findall(r"[a-z][a-z0-9-]*", SHOP_REASON.sub(" ", ask).lower())) - SHOP_ONLY_SIGNIN: return None   # "Sign in to JLCPCB and upload the gerbers": a second step stays theirs (a0ad9, 11250326d281); digits stay in a word so a hostname like the machine-names words matches (11250502652f)
    named = [(k, label) for k, (label, rx) in SHOP_NAMES.items() if re.search(rx, text, re.I if k != "link" else 0)]
    checks = [((shops or {}).get(k) or {}) for k, _ in named]
    if not named or not all(st.get("state") == "in" and (st.get("at") or 0) > since for st in checks): return None
    return " and ".join(label for _, label in named), min(st["at"] for st in checks)

def ask_rule(e, gone, now, shops=None):
    """Why a person ask is no longer the person's to do, by code's rules, or None: an agent said its premise is gone after it went to them (its owner, or naming it), or its stated
    deadline passed, or it waits on a shop sign-in that a check since shows signed in (shop_signed; shops: shop_status(), read when None). The deadline is read from when the ask
    was written. An ask a rule dropped once and someone put back to the person (it still carries the rule) isn't dropped again by its deadline: they put it back knowing the date."""
    since = stamp(e.get("status_at") or e.get("when") or e.get("at"))
    for g in gone.get(e.get("thread") or e.get("post")) or []:
        if since is not None and g[0] > since and premise_covers((g[3], g[4]), g[2], e.get("owner"), set(), {e.get("id"), e.get("post")}): return "premise gone"
    if shop_signed(e, shop_status() if shops is None else shops): return "shop signed in"
    if e.get("rule"): return None
    written = stamp(e.get("when") or e.get("at"))
    return "deadline passed" if written is not None and past_deadline(e.get("ask"), written, now) else None

def premise_of(e, gone):
    """The Premise gone entry that closed ask e (see ask_rule), or None."""
    since = stamp(e.get("status_at") or e.get("when") or e.get("at"))
    return next((g for g in gone.get(e.get("thread") or e.get("post")) or [] if since is not None and g[0] > since and premise_covers((g[3], g[4]), g[2], e.get("owner"), set(), {e.get("id"), e.get("post")})), None)

def view_rule(r, done, now=None):
    """Why open_on_person leaves out a For you that for_yous kept, or None. With now, a line whose stated deadline passed (past_deadline) goes too, even one tied to an open ask."""
    if r["thread"] in done: return "done bar"
    if now is not None and past_deadline(r["text"], r["at"], now): return "deadline passed"
    if r["asks"]: return None
    return "ends by itself" if BY_THEMSELVES.search(r["text"]) else "offer" if foryou.OFFER.match(r["text"]) else None

RULE_WHY = {"repeat": "a later line in the thread says nearly the same", "stood down": "a later agent post in the thread says nothing waits on the person", "answered": "the person replied or named its author in the thread", "ask moved": "the ask it belongs to left the person",
            "newer line": "the same agent wrote a newer For you in the thread", "done bar": "the thread's status bar says Done", "ends by itself": "a wait that ends with no one's help", "offer": "an offer: nothing stops until it is answered",
            "deadline passed": "the deadline it stated has passed", "premise gone": "an agent said in the thread that what it asked no longer applies",
            "shop signed in": "it waited on a shop sign-in, and the shop check since says signed in"}

def rule_closes(now=None, posts=None, items=None, links=None, done=None):
    """Every For you line within the horizon that code, not an agent, keeps off the person's list, each with its rule."""
    now = now or time.time()
    posts = list(lines(HOME / "board.jsonl")) if posts is None else posts
    items = asks.ledger() if items is None else items
    links = needs_order()[1] if links is None else links
    done = status_done() if done is None else done
    out = []
    for r in for_yous(posts, items, links, now - FOR_YOU_DAYS * 86400, latest_only=True, dropped=out):
        if rule := view_rule(r, done, now): out.append({**r, "rule": rule})
    return out

def unlogged_closes(rows=None):
    """The rule closes with no row yet in the closes file."""
    rows = rule_closes() if rows is None else rows
    seen = {(str(e.get("post")), e.get("line")) for e in lines(CLOSED) if e.get("rule")}
    return [r for r in rows if (str(r["post"]), r["text"]) not in seen]

def log_rule_closes(now=None, posts=None, items=None, links=None, done=None, written=None):
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
                if written is not None and not first: written.append(r)   # a backfill is history: nobody is told of it
    return len(rows)

def fmt_t(t): return dt.datetime.fromtimestamp(t, dt.timezone.utc).strftime("%m-%d %H:%MZ")

def expire_notes(rows, now):
    """{thread: text} for the code closes that deserve a word to whoever asked: a line whose deadline passed. One line each, naming its author so they can restate it with a new date.
    A Premise gone close needs none: an agent said it in that thread."""
    out = {}
    for r in rows:
        if r.get("rule") == "shop signed in":   # the ask is the agent's again: wake it (a4d66/ad748, friction-asks)
            who = r.get("from") or ""; name = f"@{who}" if AGENT.match(who) else f"`{who}`" if who else "whoever owns it"
            out.setdefault(r["thread"], []).append(f"Off the person's list and back to its agent, {name}: ask {r['id']} (\"{clip(r.get('text') or '', 90)}\") waited on them for a sign-in, and {r.get('why') or 'the shop check says signed in'}. Carry on; if the shop still refuses, say what failed and put it back to them.")
            continue
        if r.get("rule") != "deadline passed": continue
        d = deadline(r.get("text"), r.get("at")); who = r.get("from") or ""
        name = f"@{who}" if AGENT.match(who) else f"`{who}`" if who else ""
        what = f"ask {r['id']}" if r.get("id") else f"For you {r.get('post')}"
        out.setdefault(r["thread"], []).append(f"Off the person's list: {what} (\"{clip(r.get('text') or '', 90)}\") stated a deadline, {fmt_t(d) if d else 'a date'}, that passed. If it still matters, put it to them again with a new date{', ' + name if name else ''}.")
    return {th: "\n".join(v) for th, v in out.items()}

def expire_person(now=None, posts=None, items=None, by="code", shops=None):
    """Moves the register to match ask_rule: each person ask whose stated deadline passed, or whose thread an agent marked "Premise gone:", is dropped with its why; one that
    waited on a shop sign-in the shop check since shows signed in goes back to its owner (owned, or open with none).
    Returns the rows dropped ({id, thread, rule, why, text, from, at}); `hive asks person ID --why ...` puts one back."""
    now = now or time.time()
    posts = list(lines(HOME / "board.jsonl")) if posts is None else posts
    items = asks.ledger() if items is None else items
    gone, out, shops = premise_gone(posts), [], shop_status() if shops is None else shops
    for i, e in items.items():
        if e.get("status") != "person" or e.get("kind") == "direction": continue
        rule = ask_rule(e, gone, now, shops)
        if not rule: continue
        th = e.get("thread") or e.get("post")
        if rule == "shop signed in":   # not dropped: the work still needs doing, by its agent (ad748, friction-asks 11250232ed0b)
            label, at = shop_signed(e, shops)
            why = f"{label} {'show' if ' and ' in label else 'shows'} signed in (shop check {fmt_t(at)}, after this went to the person)"
            asks.append(asks.LEDGER, {"id": i, "at": dt.datetime.fromtimestamp(now, dt.timezone.utc).isoformat(timespec="seconds"), "by": by, "status": "owned" if e.get("owner") else "open", "why": why, "rule": rule})
            out.append({"id": i, "thread": th, "rule": rule, "why": why, "text": e.get("ask") or "", "from": str(e.get("owner") or ""), "at": stamp(e.get("when") or e.get("at"))})
            continue
        g = premise_of(e, gone) if rule == "premise gone" else None
        why = (f"its stated deadline passed ({fmt_t(deadline(e.get('ask'), stamp(e.get('when') or e.get('at'))))})" if rule == "deadline passed"
               else f"premise gone, post {g[1]} by {g[2]}: {g[3]}")
        asks.append(asks.LEDGER, {"id": i, "at": dt.datetime.fromtimestamp(now, dt.timezone.utc).isoformat(timespec="seconds"), "by": by, "status": "dropped", "why": why, "rule": rule})
        out.append({"id": i, "thread": th, "rule": rule, "why": why, "text": e.get("ask") or "", "from": str(e.get("owner") or ""), "at": stamp(e.get("when") or e.get("at"))})
    return out

def open_on_person(now=None, posts=None, items=None, links=None, done=None):
    """The one answer to "what waits on the person": every surface states pair(this). {"threads": {thread: {"asks": [ids], "for_yous": [post ids], "newest": epoch}}, "asks": rows, "for_yous": rows}.
    A thread is open when it has an ask in state person, or a For you open by evidence: for_yous' rules, the latest from each agent, not in a thread whose status bar says Done, not a wait that ends by itself, not an offer ("if you want...") with no ask behind it.
    An ask stays until the register moves it, whatever the thread's bar says, unless its stated deadline passed or an agent wrote "Premise gone:" in its thread after it went to the person (ask_rule). links and done default to the live files, as posts and items do."""
    now = now or time.time()
    posts = list(lines(HOME / "board.jsonl")) if posts is None else posts
    items = asks.ledger() if items is None else items
    links = needs_order()[1] if links is None else links
    done = status_done() if done is None else done
    gone = premise_gone(posts)
    person = [{**e, "id": i} for i, e in items.items() if e.get("status") == "person" and e.get("kind") != "direction"]
    shops = shop_status(); dead = {e["id"] for e in person if ask_rule(e, gone, now, shops)}   # the register catches up on the next sweep (asks.expire_person); the list doesn't wait for it
    person = [e for e in person if e["id"] not in dead]
    rows = [r for r in for_yous(posts, items, links, now - FOR_YOU_DAYS * 86400, latest_only=True) if not view_rule(r, done, now) and not (r["asks"] and all(k in dead for k in r["asks"]))]
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
