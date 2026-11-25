#!/usr/bin/env python3
"""Overdue waits: a run that stopped on `Waiting: X` (it handed work to a builder, promised a result by a time, or waits on a job) and then
said nothing more in that thread after X was due. The watchdog's wake_waits names the agent there, once per wait, so it picks the work back up
before the person has to ask (thread friction-chase, 2026-11-25: over 7 days 12 of 49 hand-offs and promises never closed, and the person
posted 10 times while one was open; nothing woke the promiser, `hive waits` was only a 12 h lead for the keeper to act on).

A wait is a `Waiting:` stop line, or a promise with a time in an agent's post ("I'll post it here around 23:30 UTC", "results in about 2 h").
When it is due: the latest time its line names ("16:46Z", "by 07:45 UTC", "06:00 EST", "13:00 local", "2026-11-25", "about 30 min",
"in 2 h", "half an hour", "25 Nov", "tomorrow"), else DEFAULT_S after the stop; then a grace of a quarter of that span (10 min to 1 h).
Only a result closes it (a Done:-type line, a delivery, a hand-off); a "still on it" post keeps the time promised.
Never due: one on the person until they speak in the thread; one handed to the keeper's next round (it reads its own threads).

  promises.py due [--now ISO] [--days N]      what wake_waits would wake now (no posting)"""
import datetime as dt, json, re, sys
from pathlib import Path

HOME = Path.home(); STATE = HOME / ".hive"
DEFAULT_S = 3 * 3600   # a wait with no time in it: hand-offs close in a median 0.1 h, 75% within 6 h (followups.py, 7 days to 2026-11-25)
GRACE_S = 60 * 60      # past the named time: a job, a builder or a timer that is a little late is not yet a dropped promise
LOOKBACK_S = 3 * 86400 # waits older than this (by due time) are history: the keeper's stalled list has them
ROLE_THREADS = {"keeper", "watchdog", "improve", "tidy"}   # a round there hands to the next round, which reads its own thread
AGENT = re.compile(r"[a-z][0-9a-f]{4,}")   # hive's HASH_NAME: only an agent's post opens a wait (boxwatch, nightly, rootwarden notices are no promises)
NOT_AGENT = ("person", "hives", "starter", "remind", "watchdog", "box", "progress", "asks", "keeper-timer", "postland", "leftover-router")
ON_PERSON = re.compile(r"\b(the person|person's|your (answer|choice|call|reply|go|test|attempt)|send your)\b|@person\b|\byou\b|\byour\b(?!\s+time)", re.I)
ON_KEEPER = re.compile(r"\b(the keeper|next keeper|keeper's (next )?round|keeper (wake|round)|next round)\b", re.I)
LOCAL = dt.datetime.now().astimezone().tzinfo

TZ = {"z": 0, "utc": 0, "gmt": 0, "est": -5, "edt": -4, "et": None, "local": None, "cet": 1, "pst": -8, "pdt": -7}
ZONE = r"(z|utc|gmt|est|edt|et|local|cet|pst|pdt)\b"
CLOCK = re.compile(r"(?<![\d:.])(\d{1,2})(?::(\d{2}))?(?::\d{2})?(?:\s*(am|pm|a\.m\.|p\.m\.)(?![a-z]))?(?:\s*" + ZONE + ")?", re.I)
DATE = re.compile(r"\b(20\d\d-\d\d-\d\d)(?:[T ](\d{1,2}):(\d{2})(?::\d{2})?(?:\s*(\+00:00|" + ZONE[1:-3] + r")\b)?)?", re.I)
MONTHS = "jan feb mar apr may jun jul aug sep oct nov dec".split()
MON = r"(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\b"
NOT_UNIT = r"(?!\s*(?:h|hrs?|hours?|m|mins?|minutes?|d|days?|w|weeks?|x|%)\b)(?![:\d])"
DAYMON = re.compile(r"\b(?:(\d{1,2})\s+" + MON + r"|" + MON + r"\s+(\d{1,2})\b" + NOT_UNIT + r")", re.I)
SPAN = re.compile(r"\b(?:in|about|around|roughly|within|~|another|takes?|after|up to|for)\s+(?:the next\s+)?(?:about\s+|around\s+|roughly\s+|~)?(\d+(?:\.\d+)?)(?:\s*(?:-|–|to)\s*(\d+(?:\.\d+)?))?\s*(min(?:ute)?s?|m\b|h(?:ou)?rs?\b|h\b|days?\b|d\b|weeks?\b|w\b)", re.I)
WORDS = {"half an": 0.5, "an": 1, "a": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "ten": 10, "fifteen": 15, "twenty": 20,
         "thirty": 30, "forty": 40, "forty-five": 45, "a few": 3, "a couple of": 2, "a couple": 2}
WORDSPAN = re.compile(r"\b(?:in|about|around|roughly|within|another|takes?|after|up to|for)\s+(?:the next\s+)?(?:about\s+|around\s+|roughly\s+)?("
                      + "|".join(sorted(map(re.escape, WORDS), key=len, reverse=True)) + r")\s+(minutes?|hours?|days?|weeks?)\b", re.I)   # "in about half an hour", "two hours"
THE_HOUR = re.compile(r"\b(?:within|in the next) (?:the )?(hour|day)\b", re.I)   # "I'll post it here within the hour" (ad5b1 11-25 03:48: warned, though it named one)
MD = re.compile(r"(?<![\d-])(1[0-2]|0?[1-9])-([0-3]\d)(?![\d-])", re.I)
GLUE = re.compile(r"[\s,]*(?:at|on|by|@)?[\s,]*", re.I)   # what may stand between a date and its clock time: "25 Nov 18:47", "04:15Z 11-25", "11-19 at 15:30"
TOMORROW = re.compile(r"\btomorrow\b", re.I)


def ts(x):
    """Unix seconds from a float, or an ISO string with or without a zone (none = UTC, as the board writes)."""
    if isinstance(x, (int, float)): return float(x)
    d = dt.datetime.fromisoformat(str(x).replace("Z", "+00:00"))
    return (d if d.tzinfo else d.replace(tzinfo=dt.timezone.utc)).timestamp()


def _tz(z):
    off = TZ.get((z or "local").lower())
    return LOCAL if off is None else dt.timezone(dt.timedelta(hours=off))


def due(why, at):
    """named_due(), else at + DEFAULT_S."""
    d = named_due(why, at)
    return at + DEFAULT_S if d is None else d


def named_due(why, at):
    """Unix time the wait `why` (its stop at unix `at`) comes due, before GRACE_S: the latest time it names that is still ahead of the stop,
    else None (review 11-25: comparing due() with at + DEFAULT_S read "in about 3 h" as no time). A date and the clock time beside it are one moment ("by 25 Nov 18:47 UTC", "04:15Z 11-25"); a date alone means by the
    end of it. A time already past when the line was written ("started at 03:10Z") names no deadline: if that is all the line has, the wait
    names none. Otherwise an agent woken for a lapsed wait that restates the same past time would be due again at once, every tick."""
    t0 = dt.datetime.fromtimestamp(at, dt.timezone.utc); text = str(why or ""); found = []
    used = []   # spans already read, so one piece of text is not counted twice
    def free(m): return not any(m.start() < e and s < m.end() for s, e in used)
    clocks = []; dated = []   # dated: (moment, where in the text) of each date given with its time: a later bare time in the line is on that day
    for m in CLOCK.finditer(text):
        h, mi, ap = int(m[1]), int(m[2] or 0), (m[3] or "").lower().replace(".", "")
        if m[2] is None and not ap: continue   # a bare number is no time
        if m[1] == "0" and not ap and not m[4]: continue   # "the case shot at 0:03" is a place in a film, not a clock (a wall clock is 00:03)
        if ap and not 1 <= h <= 12: continue
        if ap: h = h % 12 + (12 if ap == "pm" else 0)
        if h > 23 or mi > 59: continue
        clocks.append((m.start(), m.end(), h, mi, _tz(m[4])))
    def clock_near(s, e):
        """The clock time right after (or right before) a date span."""
        for cs, ce, h, mi, tz in clocks:
            if cs >= e and GLUE.fullmatch(text[e:cs]) or ce <= s and GLUE.fullmatch(text[ce:s]):
                if not any(cs < ue and us < ce for us, ue in used): return (cs, ce, h, mi, tz)
    def on(day, s, e):
        c = clock_near(s, e); used.append((s, e))
        if c:
            used.append(c[:2]); found.append(dt.datetime(day.year, day.month, day.day, c[2], c[3], tzinfo=c[4])); dated.append((found[-1], min(s, c[0])))
        else: found.append(dt.datetime(day.year, day.month, day.day, tzinfo=LOCAL) + dt.timedelta(days=1))   # a date alone: by the end of it
    for m in DATE.finditer(text):
        d = dt.date.fromisoformat(m[1])
        if m[2]: used.append(m.span()); found.append(dt.datetime(d.year, d.month, d.day, int(m[2]), int(m[3]), tzinfo=_tz(m[4]) if m[4] and m[4] != "+00:00" else dt.timezone.utc))   # no zone: board style, UTC as ts()
        if m[2]: dated.append((found[-1], m.start()))
        else: on(d, *m.span())
    for m in DAYMON.finditer(text):
        if not free(m): continue
        day, mon = int(m[1] or m[4]), MONTHS.index((m[2] or m[3]).lower()[:3]) + 1
        try: d = dt.date(t0.year, mon, day)
        except ValueError: continue
        if d < t0.date() - dt.timedelta(days=180): d = d.replace(year=t0.year + 1)
        on(d, *m.span())
    for m in MD.finditer(text):   # "11-19 15:30Z", "on 11-19 at 15:30 UTC", "11-19": only a month-day within 10 days of the stop
        if not free(m) or any(cs <= m.start() < ce for cs, ce, *_ in clocks): continue
        try: d = dt.date(t0.year, int(m[1]), int(m[2]))
        except ValueError: continue
        if abs((d - t0.date()).days) > 10: continue
        on(d, *m.span())
    anchor = max(dated)[0] if dated else None; first = min(x[1] for x in dated) if dated else None; rolled = []
    for cs, ce, h, mi, tz in clocks:
        if any(cs < e and s < ce for s, e in used): continue
        if anchor and cs > first:   # "timer at 04:15Z 11-25, read at 05:30Z": the read is 11-25 05:30Z; a time before every date is read as today's
            c = anchor.astimezone(tz).replace(hour=h, minute=mi, second=0, microsecond=0)
            found.append(c + dt.timedelta(days=1) if c < anchor else c); continue
        base = t0.astimezone(tz); c = base.replace(hour=h, minute=mi, second=0, microsecond=0)
        if c < base - dt.timedelta(hours=1): rolled.append(c + dt.timedelta(days=1))   # "06:00" said at 23:00: the next morning, unless...
        else: found.append(c)
    for m in SPAN.finditer(text):
        n = float(m[2] or m[1]); u = m[3].lower()[0]   # "in about 30-40 min": by the later end
        if u == "h" and (more := re.match(r"\s*(?:and\s+)?(\d{1,2})\s*min", text[m.end():], re.I)): n += int(more[1]) / 60   # "1 h 50 min"
        try: found.append(t0 + (dt.timedelta(minutes=n) if u == "m" else dt.timedelta(hours=n) if u == "h" else dt.timedelta(weeks=n) if u == "w" else dt.timedelta(days=n)))
        except OverflowError: continue   # "for 20251119 days": a number, not a span
    for m in WORDSPAN.finditer(text):
        n = WORDS[m[1].lower()]; u = m[2].lower()[0]
        if m[1].lower() == "a few" and u == "m": n = 5
        found.append(t0 + (dt.timedelta(minutes=n) if u == "m" else dt.timedelta(hours=n) if u == "h" else dt.timedelta(weeks=n) if u == "w" else dt.timedelta(days=n)))
    for m in THE_HOUR.finditer(text): found.append(t0 + (dt.timedelta(hours=1) if m[1].lower() == "hour" else dt.timedelta(days=1)))
    if TOMORROW.search(text): found.append(t0 + dt.timedelta(days=1))
    ahead = [x.timestamp() for x in found if x.timestamp() > at] or [x.timestamp() for x in rolled]   # ...the line names a time ahead
    return max(ahead) if ahead else None                                                            # ("started 03:10Z, result in 30 min")


def jsonl(path):
    try:
        with open(path, errors="replace") as f:
            for ln in f:
                try: yield json.loads(ln)
                except ValueError: pass
    except OSError: return


STOP_LINE = re.compile(r"^\s*\**(?:Waiting|Done|Blocked|Needs the person|Told to stop):", re.M | re.I)
GRACE_MIN_S = 10 * 60  # the grace is a quarter of the promised span, between this and GRACE_S: "about 23:30" said at 23:27 is late by 23:40
# A promise in a post's text, not only on its stop line (review a0ad9, 11-25: replayed, the stop line alone caught none of the person's 8 chases):
# a sentence saying something comes back here, and naming a time ahead. Without a time it is not counted (every post says what comes next).
PROMISE = re.compile(r"\b(?:I'?ll|I will|I'm (?:going to )?(?:post|send)|will (?:post|send|follow|come|land|report|be (?:here|ready|done|back))"
                     r"|should (?:be (?:ready|done|finished|here|back)|land|finish)|I expect|expect(?:ed)? (?:to (?:finish|post|have|land)|it|the)"
                     r"|(?:ready|done|finish(?:es|ed)?|report back)\s+(?:in|by|around|at|within|about|before)"
                     r"|(?:comes?|coming|follows?|lands?) (?:here|to you|in this thread|on its own|next|in|by|about|around|within|before|after)"
                     r"|(?:post|send|share)(?:s|ing)? (?:it |them |that |this |the \w+ |you \w+ )?(?:here|to you|when|once|as soon)"
                     r"|(?:here|in this thread) (?:by|in|around|at|before|within|about)|back (?:here|with)|from now|ETA"
                     r"|(?:results?|read|answer|report|update|preview|film|clip|draft|fix)s? (?:comes?|follows?|here|due"
                     r"|(?:in|by|before|around|about|at|within)\s+(?:about\s+|around\s+|roughly\s+|~\s*)?(?:\d|an?\b|half|one\b|two\b|three\b|a few|the next)))", re.I)
SENTENCE = re.compile(r"(?<=[.!?])\s+|\n+")
FENCE = re.compile(r"```.*?(?:```|$)", re.S)
DELIVERS = re.compile(r"```|https?://|\b(?:projects|kb|modules|core)/\S+\.\w{1,5}\b|\.(?:png|jpe?g|mp4|pdf|svg|csv|zip|html)\b", re.I)
HANDOFF = re.compile(r"\b(?:hand(?:ed|ing)? (?:it |this |that )?(?:over )?to|over to|passing (?:it |this )?to|taking (?:it )?over|now with)\s+`?@?a[0-9a-f]{4}\b", re.I)
BRIEF = re.compile(r"\s*@[\w-]+")
PAST = re.compile(r"\b(?:ago|took|ran|been running|roll(?:s|ed)? back|heads-up|notice:)", re.I)   # a time that was, or a notice, not a promise
FOR_SPAN = re.compile(r"\bfor\s+(?:about\s+|around\s+|roughly\s+|~\s*)?(?:[\d.]+\s*|(?:an?|one|two|three|four|five|ten|fifteen|twenty|thirty|half an?|a few|a couple(?: of)?|few)\s+)"
                      r"(?:s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?|d|days?|w|weeks?)\b", re.I)   # how long a thing lasts ("stops for about a minute"), not when it is due
REVISE = re.compile(r"\b(?:slower|later than|longer than|delayed|pushed|instead|revised|new (?:eta|estimate|time)|now (?:about|around|roughly|due|at|in))\b", re.I)
PROGRESS = re.compile(r"\b(?:still|not (?:yet|done)|in progress|halfway|so far)\b|\d%", re.I)
DELIVER_LEN = 1200     # a post this long is an answer, not a "still on it"


def grace(at, d):
    return min(GRACE_S, max(GRACE_MIN_S, (d - at) / 4))


def promise_in(text, at, revising=False):
    """(sentence, due) for the latest time ahead a promise sentence in `text` (posted at unix `at`) names, else None. Only a sentence that
    promises counts: "it rolls back in a minute", "running for about 12 min" or "(about 5 minutes)" for the person's clicks name no result
    (a 7-day replay that took a time from any sentence while a wait was open woke agents 144 times, against 26). A post that opens with an
    @name is a brief to that agent, and what it quotes is not the writer's promise. revising (a wait is open): a sentence that moves the
    time ("slower than I said, roughly 02:45 UTC") counts too."""
    if BRIEF.match(str(text or "")): return None
    body = "\n".join(l for l in FENCE.sub(" ", str(text or "")).splitlines() if not l.lstrip().startswith(">") and not STOP_LINE.match(l))
    best = None
    for sen in SENTENCE.split(body):
        if not sen.strip() or not (PROMISE.search(sen) or revising and REVISE.search(sen)) or PAST.search(sen): continue
        d = named_due(FOR_SPAN.sub(" ", sen), at)
        if d is not None and d > at and (best is None or d > best[1]): best = (" ".join(sen.split())[:300], d)
    return best


def delivers(p):
    """A post that hands over a result: a file, a block, a link or path, a long answer, or the work handed to another agent by name."""
    text = str(p.get("text", ""))
    if p.get("files") or len(text) >= DELIVER_LEN or HANDOFF.search(text) or FENCE.search(text): return True
    return bool(DELIVERS.search(text)) and not PROGRESS.search(text)   # "still rendering projects/reel/out.mp4, 60%" names a path, delivers nothing


def overdue(now, stops=None, posts=None, reminders=None, lookback=LOOKBACK_S):
    """Waits due by `now` (unix) that nothing has answered: [{name, thread, at, why, due, post}], oldest first.
    stops: ~/.hive/stop-gate.jsonl rows; posts: board posts; reminders: ~/.hive/remind/reminders.json items.
    A wait opens on a `Waiting:` stop (or line in a post), or on a promise with a time in an agent's post text. It closes only on a result from
    that agent in that thread: a Done:/Blocked:/Needs the person:/Told to stop: line, a post that delivers (delivers()), or a hand-off. A later
    promise or Waiting: with a time replaces it; a post that only says it is still on it, or a Waiting: with no time, keeps the time already
    promised (review a0ad9, 11-25: "any later post closes it" let interim notes end the wait). It is not due while: no other agent ended a run
    there since (unless that stop names it); if a stop line waits on the person, they haven't spoken; it has a reminder pending in that thread."""
    stops = list(jsonl(STATE / "stop-gate.jsonl")) if stops is None else stops
    posts = list(jsonl(HOME / "board.jsonl")) if posts is None else posts
    if reminders is None:
        try: reminders = json.loads((STATE / "remind" / "reminders.json").read_text())
        except (OSError, ValueError): reminders = []
    ev, by_thread = {}, {}
    for r in stops:
        if r.get("event") != "passed" or not r.get("thread") or not r.get("name"): continue
        try: at = ts(r.get("at"))
        except (TypeError, ValueError): continue
        if at > now or at < now - 2 * lookback: continue   # older stops and posts can't make a wait due within the lookback
        why = str(r.get("why", ""))
        by_thread.setdefault(r["thread"], []).append((at, r["name"], why))
        ev.setdefault((r["name"], r["thread"]), []).append((at, 0, why, None))
    pt = {}
    for p in posts:
        if not p.get("thread") or not p.get("at"): continue
        try: at = ts(p["at"])
        except (TypeError, ValueError): continue
        if at > now or at < now - 2 * lookback: continue   # promise_in on every post of the board made a tick 35x slower
        who = str(p.get("from", "")).lower()
        pt.setdefault(p["thread"], []).append((at, who, p))
        if AGENT.fullmatch(who): ev.setdefault((who, p["thread"]), []).append((at, 1, None, p))
    remind = {(str(x.get("to", "")).lower(), x.get("thread")) for x in reminders or () if isinstance(x, dict)}

    def opened(at, why, kind, pid, cur):
        d = named_due(why, at)
        if d is None and cur and cur["named"]: return cur   # "Waiting: the render" after "about 23:30": still 23:30
        return {"at": at, "why": why, "due": at + DEFAULT_S if d is None else d, "named": d is not None, "kind": kind, "post": pid}

    out = []
    for (name, thread), es in ev.items():
        if thread in ROLE_THREADS: continue
        try: w = _pair(name, thread, es, opened, now, lookback, pt, by_thread, remind)
        except (ValueError, OverflowError, OSError, TypeError): continue   # one bad row must not stop every wake (watchdog swallows the raise)
        if w: out.append(w)
    return sorted(out, key=lambda w: w["at"])


def _waiting(l): return bool(re.match(r"\s*\**waiting:", l, re.I))


def _pair(name, thread, es, opened, now, lookback, pt, by_thread, remind):
    """The lapsed wait of one agent in one thread, or None (overdue's loop body)."""
    cur = None
    for at, _, why, p in sorted(es, key=lambda e: (e[0], e[1])):
        if p is None:   # a stop
            cur = opened(at, why, "stop", None, cur) if _waiting(why) else None
            continue
        lines = [str(p.get("reason", ""))] + [l.strip().lstrip("*") for l in str(p.get("text", "")).splitlines() if STOP_LINE.match(l)]
        wait = next((l for l in lines if _waiting(l)), None)
        if wait: cur = opened(at, wait, "stop", p.get("id"), cur)
        elif any(STOP_LINE.match(l) for l in lines if l): cur = None
        elif delivers(p): cur = None   # before promise_in: "here it is; I'm around until 18:00Z" delivers, it doesn't promise
        elif (pr := promise_in(p.get("text"), at, revising=cur is not None)):
            cur = {"at": at, "why": pr[0], "due": pr[1], "named": True, "kind": "text", "post": p.get("id")}
        elif cur and not cur["named"]: cur = None   # a wait with no time: any later word from it closes it, as before (only a named time is held)
    if not cur: return None
    why, at, d = cur["why"], cur["at"], cur["due"]
    if ON_KEEPER.search(why): return None
    if now < d + grace(at, d) or now - d > lookback: return None
    after = [x for x in pt.get(thread, ()) if x[0] > at]
    spoke = max((a for a, who, _ in after if who in ("person", "hives")), default=0)
    if cur["kind"] == "stop" and ON_PERSON.search(why) and not spoke: return None
    said = [(a, who, "\n".join([str(p.get("reason", ""))] + [l for l in str(p.get("text", "")).splitlines() if STOP_LINE.match(l)]))
            for a, who, p in after if p.get("reason") or STOP_LINE.search(str(p.get("text", "")))]
    if any(a > max(at, spoke) and who != name and who not in NOT_AGENT and name not in w for a, who, w in by_thread.get(thread, []) + said): return None
    if (name, thread) in remind: return None
    pid = cur["post"] or next((p.get("id") for a, who, p in reversed(pt.get(thread, ())) if who == name and a <= at + 5), None)
    return {"name": name, "thread": thread, "at": at, "why": why, "due": d, "post": pid, "kind": cur["kind"]}


def hm(t): return dt.datetime.fromtimestamp(t, dt.timezone.utc).strftime("%m-%d %H:%MZ")

def wake_text(w, now):
    """The wake post: what was promised, where, and since when, naming the agent so its own session resumes."""
    named = "the time in that line" if w.get("kind") == "text" or named_due(w["why"], w["at"]) is not None else f"{DEFAULT_S // 3600} h with no time named"
    clean = " ".join(w["why"].replace("@", "").replace("`", "'").split())[:300]
    said = "you said here at" if w.get("kind") == "text" else "your run here stopped at"
    return (f"@{w['name']} {said} {hm(w['at'])}" + (f" (post {w['post']})" if w.get("post") else "") + f":\n> {clean}\n"
            f"That came due at {hm(w['due'])} ({named}) and no result has come back here from you in {(now - w['at']) / 3600:.1f} h. "
            "Check what you waited on (the builder's thread, the job, the timer) and post the result here; if it still isn't ready, "
            "say what holds it and end with a new `Waiting:` line that names when it is due.\n"
            "(The watchdog names a lapsed wait once; wake_waits in core/watchdog.py, core/promises.py.)")



# The first reply to the person's ask says when the result comes (ad748, friction-chase 1125022053f9): of the person's chases in the 7 days
# to 11-25, most came after a first reply that named work ahead and no time. Only a warning at `hive post`: the post is on the board already.
AHEAD = re.compile(r"\b(?:I'?ll|I will|I'm (?:now )?\w+ing\b|I am \w+ing\b|taking (?:it|this|that|both|the)\b|on it\b|starting (?:it|now|on|the|a|with|both)\b|(?:working|looking) on\b"
                   r"|will (?:post|send|follow|report|land|have|be)|going to\b|next,? I\b|then I\b|kicking off|queued|running (?:it|the|a)\b)", re.I)
PERSON_ACK = re.compile(r"^\W*(?:(?:perf(?:ect)?|excellent|great|nice|cool|good|awesome|amazing|thanks|thank you|thx|ty|ok(?:ay)?|got it|sounds good|yes|no|yep|nope|go|do it)\W*)+$", re.I)
NOT_ASK = re.compile(r"^(?:@\S+\s+)*\(from [^)]*\)\s*|^(?:@\S+\s+)+")
STOPPING = re.compile(r"\b(?:withdraw(?:ing|n)?|dropping (?:my|the|that|this)|paus(?:e|ed|ing)|stop(?:ped|ping)?|halt(?:ed|ing)?|on hold|pass(?:ed|ing) (?:it |this |that )?(?:on )?to|handed (?:it |this )?to)\b", re.I)   # a pause or a hand-off promises no result
NEXT = re.compile(r"\b(?:next (?:is|up|comes|I)|then I|after that,? I)\b", re.I)
BARE_SPAN = re.compile(r"\W*(?:about\s+|around\s+|roughly\s+|~\s*)?(?:\d+(?:\.\d+)?|" + "|".join(sorted(map(re.escape, WORDS), key=len, reverse=True))
                       + r")\s*(?:mins?|minutes?|h|hrs?|hours?)\W*", re.I)   # a sentence that is only a span ("...I'll cover both in the answer. Two minutes.")
REPLY_DONE = re.compile(r"^\s*\**(?:Done|Told to stop|Needs the person|Blocked):", re.I)


def _ahead_no_time(reply):
    """True when an agent's post (a dict) says work is ahead and names no time for it: not a result (a file, block, path or long answer,
    a Done: line), not a question back, a pause or a hand-off, and no time anywhere in its text or reason line."""
    text, why = str(reply.get("text", "")), str(reply.get("reason", ""))
    if BRIEF.match(text) or delivers(reply) or REPLY_DONE.match(why) or any(REPLY_DONE.match(l) for l in text.splitlines()): return False
    plain = "\n".join(l for l in FENCE.sub(" ", text).splitlines() if not l.lstrip().startswith(">"))
    opening = " ".join([x for x in SENTENCE.split(plain) if x.strip()][:2])   # a reply that opens with the answer ("Yes, in three layers...") is no plan
    if not AHEAD.search(opening) or STOPPING.search(opening) or re.search(r"\?\s*$", plain.strip()): return False
    at = ts(reply.get("at")) if reply.get("at") else dt.datetime.now(dt.timezone.utc).timestamp()
    if any(BARE_SPAN.fullmatch(x.strip()) for x in SENTENCE.split(plain)): return False
    return not (promise_in(text, at) or named_due(FOR_SPAN.sub(" ", plain), at) is not None or named_due(why, at) is not None)


def _real_ask(ask):
    if not ask or str(ask.get("from")) != "person": return False
    body = NOT_ASK.sub("", str(ask.get("text", ""))).strip()
    return len(body) >= 8 and not PERSON_ACK.match(body)


def first_reply_warning(reply, ask):
    """A warning for an agent's first reply (a post dict) to the person's ask (a post dict) that announces work ahead and names no time
    for the result, else None. Quiet for a result (a file, block, path or long answer, a Done: line), a reply that asks the person back,
    one that passes the work on, and a reply with a time anywhere in it or its reason line."""
    if not _real_ask(ask) or not AGENT.fullmatch(str(reply.get("from", ""))) or not _ahead_no_time(reply): return None
    return ("This is your first reply to the person's ask and it names no time for the result. They chase when a reply says work is "
            "ahead but not when (\"??\", \"eta?\"). Post one line with when to expect it (\"by 14:30Z\", \"in about 20 min\"), "
            "or what it waits on and when you will next say.")


def first_ask(thread, pid, posts):
    """The person's ask the post `pid` answers first in `thread`: the latest person post before it, with no agent reply between."""
    ask = None
    for p in posts:
        if p.get("thread") != thread: continue
        if p.get("id") == pid: return ask
        if str(p.get("from")) == "person": ask = p
        elif AGENT.fullmatch(str(p.get("from", ""))) and not BRIEF.match(str(p.get("text", ""))): ask = None
    return None


def progress_warning(post, ask):
    """A warning for an agent's later post in a thread where the person's ask (a post dict) is still open (an agent has replied, nothing
    has delivered since) that says more work is coming and names no time, else None (ad748, friction-chase 11250236a86d: "re-rendering
    now, next post is the film" at 21:36 was chased at 22:22)."""
    if not _real_ask(ask) or not AGENT.fullmatch(str(post.get("from", ""))) or not _ahead_no_time(post): return None
    return ("The person's ask in this thread is still open and this post says more work is coming but not when. They chase when an "
            "update says work is ahead but not when (\"??\", \"eta?\"). Post one line with when to expect the result "
            "(\"by 14:30Z\", \"in about 20 min\"), or what it waits on and when you will next say.")


def _closes(p):
    """A post that delivers, unless its last paragraph says more is coming ("Next is the whole film at this pace...": a25fa 11-18 21:09
    posted a clip and named the film next; its 21:36 "re-rendering now" was chased at 22:22). A Done:-type stop line always closes."""
    t = str(p.get("text", ""))
    if REPLY_DONE.match(str(p.get("reason", ""))) or any(REPLY_DONE.match(l) for l in t.splitlines()): return True
    if not delivers(p): return False
    paras = [x for x in re.split(r"\n\s*\n", FENCE.sub(" ", t)) if x.strip() and not STOP_LINE.match(x)]
    return not (paras and (AHEAD.search(paras[-1]) or NEXT.search(paras[-1])))


def open_ask(thread, pid, posts):
    """The person's ask still open when `pid` is posted in `thread`: the latest real person ask before it that an agent has already
    replied to and that no agent post has delivered since. None when `pid` is the first reply (first_ask has it) or nothing is open."""
    ask, replies, closed, promised = None, 0, False, 0
    for p in posts:
        if p.get("thread") != thread: continue
        if p.get("id") == pid:
            now = ts(p.get("at")) if p.get("at") else dt.datetime.now(dt.timezone.utc).timestamp()
            return ask if ask is not None and replies and not closed and promised <= now else None   # a time named since that still stands: the person has one
        if _real_ask(p): ask, replies, closed, promised = p, 0, False, 0
        elif AGENT.fullmatch(str(p.get("from", ""))) and str(p.get("from")) not in NOT_AGENT and not BRIEF.match(str(p.get("text", ""))):
            replies += 1; closed = closed or _closes(p); promised = max(promised, _named(p))
    return None


def _named(p):
    """The latest time ahead an agent's post names for its work (its text or its reason line), else 0."""
    text, at = str(p.get("text", "")), ts(p["at"]) if p.get("at") else 0
    plain = "\n".join(l for l in FENCE.sub(" ", text).splitlines() if not l.lstrip().startswith(">"))
    got = [d for d in (named_due(FOR_SPAN.sub(" ", plain), at), named_due(str(p.get("reason", "")), at)) if d]
    if any(BARE_SPAN.fullmatch(x.strip()) for x in SENTENCE.split(plain)): got.append(at + 30 * 60)   # "Two minutes.": short, read generously
    return max(got, default=0)


def on_post(p, posts=None):
    """After `hive post` by an agent: the warning for a first reply to the person's ask with no time, or for a later post while that ask
    is still open that says more is coming and not when, else None."""
    posts = list(jsonl(HOME / "board.jsonl")) if posts is None else posts
    ask = first_ask(p.get("thread"), p.get("id"), posts)
    if ask is not None: return first_reply_warning(p, ask)
    return progress_warning(p, open_ask(p.get("thread"), p.get("id"), posts))

if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser(); ap.add_argument("cmd", choices=["due"]); ap.add_argument("--now"); ap.add_argument("--days", type=float, default=LOOKBACK_S / 86400)
    a = ap.parse_args(); now = ts(a.now) if a.now else dt.datetime.now(dt.timezone.utc).timestamp()
    ws = overdue(now, lookback=a.days * 86400)
    for w in ws: print(f"{w['name']}  {w['thread']}  stopped {hm(w['at'])}, due {hm(w['due'])}: {w['why'][:110]}")
    print(f"-- {len(ws)} lapsed waits")
