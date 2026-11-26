"""One agent's money today, told while it is still running (hive-meta L155). A run's cost is booked when it ends, so a 20-hour run showed as one late spike
(11-25: $1,534 gone before anyone asked). A notice, not a cap (k2555): once per agent per UTC day, to the agent and meta-load. watchdog.py calls check().

Money for a day: a run that started and ended that day counts spend.py's way (the rise of the session's logged cost). A run still going, or begun on an earlier
day, is priced from its session files (subagents too), only the messages written that day, at modules/cost list prices; that reads about 14% under the CLI's own total.
The bar: the 99th percentile of agent-days over the 14 days before, kept between FLOOR and CEIL so a drain can't raise its own bar."""
import collections, datetime as dt, glob, json, os, subprocess, sys
from pathlib import Path

HOME = Path.home()
FLOOR, CEIL = 150.0, 300.0
EVERY_S = 1700   # session files run to hundreds of MB: read them twice an hour, not every watchdog tick


def pricer():
    """modules/cost price(model, usage) in dollars, or None where the module isn't installed (then nothing is checked)."""
    d = HOME / "modules" / "cost"
    if not (d / "cost.py").exists(): return None
    sys.path.insert(0, str(d)); import cost
    return cost.price


def runs_of(log_path, now):
    """Every run begun by `now`: name, session, pid, start, end (None while open), model, effort, usd (spend.py's rise, ended runs)."""
    runs, open_, prior = [], {}, collections.defaultdict(list)
    with open(log_path, errors="replace") as f:
        for ln in f:
            if '"start"' not in ln and '"end"' not in ln: continue
            try: e = json.loads(ln)
            except ValueError: continue
            if e.get("event") not in ("start", "end") or str(e.get("at", "")) > now: continue
            key = (e.get("name"), e.get("pid"))
            if e["event"] == "start":
                r = {"name": e.get("name"), "pid": e.get("pid"), "session": e.get("session"), "start": e["at"], "end": None,
                     "model": e.get("model") or "default", "effort": e.get("effort"), "usd": None}
                open_[key] = r; runs.append(r); continue
            r = open_.pop(key, None)
            if r: r["end"] = e["at"]; r["session"] = e.get("session") or r["session"]
            c = e.get("cost")
            if not isinstance(c, (int, float)) or isinstance(c, bool): continue
            v = prior[e.get("session") or e.get("name")]
            base = 0.0 if not v else v[-1] if c >= v[-1] else max([0.0] + [x for x in v if x <= c])
            v.append(c)
            if r: r["usd"] = c - base
            else: runs.append({"name": e.get("name"), "pid": e.get("pid"), "session": e.get("session"), "start": e["at"], "end": e["at"], "model": "?", "effort": None, "usd": c - base})
    return runs


def session_files(session):
    out = []
    for p in glob.glob(str(HOME / ".claude*" / "projects" / "*")):
        out += glob.glob(f"{p}/{session}.jsonl") + glob.glob(f"{p}/{session}/subagents/**/*.jsonl", recursive=True)
    seen, one = set(), []   # a session moved between logins is a hard link now (starter.link_or_copy): read each file once
    for f in out:
        try: st = os.stat(f)
        except OSError: continue
        if (st.st_dev, st.st_ino) not in seen: seen.add((st.st_dev, st.st_ino)); one.append(f)
    return one


def messages(session, since, price):
    """[(timestamp, dollars)] of the session's model calls written from `since` on, one per message id (a resumed session's files are copied between logins)."""
    seen = {}
    floor = dt.datetime.fromisoformat(since[:19] + "+00:00").timestamp()
    for path in session_files(session):
        try:
            if os.path.getmtime(path) < floor: continue
            with open(path, errors="replace") as f:
                for ln in f:
                    if '"usage"' not in ln: continue
                    try: m = json.loads(ln)
                    except ValueError: continue
                    msg = m.get("message") or {}; ts = str(m.get("timestamp", ""))
                    if m.get("type") == "assistant" and msg.get("usage") and ts[:19] >= since[:19]:
                        seen[msg.get("id") or m.get("uuid")] = (ts, price(msg.get("model"), msg["usage"]))   # the last line of a message has its full usage
        except OSError: pass
    return list(seen.values())


def bar_of(runs, day):
    first = (dt.date.fromisoformat(day) - dt.timedelta(days=14)).isoformat()
    days = collections.Counter()
    for r in runs:
        if r["usd"] is not None and first <= r["end"][:10] < day: days[(r["end"][:10], r["name"])] += r["usd"]
    v = sorted(days.values())
    return min(CEIL, max(FLOOR, v[int(0.99 * (len(v) - 1))] if v else FLOOR))


def over(log_path, now, price, live=None, msgs=messages):
    """(bar, [(name, usd, runs)]) for each agent past the bar on now's UTC day. `now` is an ISO UTC time; `live` limits open runs to these (name, pid),
    so a run that died without an end event isn't priced forever (None, for a replay: every run open at `now`)."""
    day = now[:10]; runs = runs_of(log_path, now); bar = bar_of(runs, day)
    today = collections.defaultdict(list)
    for r in runs:
        if r["end"] is None:
            if live is not None and (r["name"], r["pid"]) not in live: continue
        elif r["end"][:10] != day: continue
        today[r["name"]].append(r)
    out, cache = [], {}
    for name, rs in today.items():
        # Cheap screen first: only an agent that could be over the bar has its files read.
        if all(r["end"] and r["start"][:10] == day for r in rs) and sum(r["usd"] or 0 for r in rs) < bar: continue
        total, got = 0.0, []
        for r in rs:
            if r["end"] and r["start"][:10] == day: usd = r["usd"] or 0.0
            elif r["session"]:
                since = max(r["start"], day + "T00:00:00")
                if r["session"] not in cache: cache[r["session"]] = msgs(r["session"], day + "T00:00:00", price)
                until = r["end"] or now
                usd = sum(c for ts, c in cache[r["session"]] if since[:19] <= ts[:19] <= until[:19])
            else: usd = 0.0
            total += usd; got.append(dict(r, today=usd))
        if total >= bar: out.append((name, total, got))
    return bar, sorted(out, key=lambda x: -x[1])


def notice(name, usd, runs, bar, day, running):
    """The post. The agent is mentioned only while it runs: a mention of a sleeping agent starts it, which costs more of what this reports."""
    who = f"@{name}" if running else f"`@{name}`"
    rows = "\n".join(f"- from {r['start'][5:16].replace('T', ' ')}Z {'running' if r['end'] is None else 'ended ' + r['end'][5:16].replace('T', ' ') + 'Z'}: "
                     f"about ${r['today']:,.0f} today, {r['model']}, effort {r['effort'] or 'default'}" for r in runs if r["today"] >= 1)
    return (f"Spend notice, {day} (UTC): {who} has used about ${usd:,.0f} today at API prices, past the ${bar:,.0f} bar.\n{rows}\n"
            "Not a cap and nothing stops. If it is yours: check that the model, effort and helpers still fit the work. Posted once per agent per day.")


def check(st, t, runs, post):
    """watchdog.py's entry: `runs` are the live run records, `post(thread, text)` posts. Returns [] (never a trip: a trip starts an agent)."""
    s = st.setdefault("day_spend", {})
    if t - s.get("at", 0) < EVERY_S: return []
    s["at"] = t
    price = pricer()
    if not price: return []
    now = dt.datetime.fromtimestamp(t, dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S+00:00")
    live = {(r.get("name"), r.get("pid")) for r in runs}
    bar, found = over(HOME / "log.jsonl", now, price, live)
    told = {k: v for k, v in s.get("told", {}).items() if k.startswith(now[:10])}
    for name, usd, rs in found:
        key = f"{now[:10]} {name}"
        if key in told: continue
        if post("meta-load", notice(name, usd, rs, bar, now[:10], any(r["end"] is None for r in rs))) is not False: told[key] = round(usd, 2)
    s["told"] = told
    return []
