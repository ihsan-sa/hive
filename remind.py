#!/usr/bin/env python3
"""hive remind: a post into a chat at a time or on a schedule. Use this for any reminder or recurring job; never write a timer or a cron line.
  hive remind at WHEN TEXT [--to NAME] [--thread T]     WHEN: +30m, +2h, +1d, HH:MM (next one), or 2026-11-06T09:00 / "2026-11-06 09:00" (local time)
  hive remind every "M H DOM MON DOW" TEXT [--to NAME] [--thread T]     five-field cron: * , - / lists, ranges, steps; DOW 0-7
  hive remind list [--thread T]     hive remind rm ID     hive remind tick (what the hive-remind timer runs once a minute: posts what is due)
The thread defaults to the one that woke you (the asking chat). --to is person (the default) or an agent's own name.
The post is made as `remind` into that thread, `@NAME TEXT`, addressed to NAME. Moved here from the arm-remind module so it works on a fresh install."""
import datetime as dt, fcntl, json, os, re, secrets, subprocess, sys
from pathlib import Path

HOME = Path.home()
CORE = Path(__file__).resolve().parent
STATE = HOME / ".hive" / "remind"
MAX_ITEMS = 50
MIN_GAP_MIN = 15          # a schedule can't fire more often than this
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
AGENT = re.compile(r"[a-z][0-9a-f]{4,}$")
WARDEN = re.compile(r"w[0-9a-f]{4}$")


def woke_thread():
    """As hive's: HIVE_THREAD, else the run record of HIVE_NAME (a warm agent has no HIVE_THREAD)."""
    if (t := os.environ.get("HIVE_THREAD")): return t
    n = os.environ.get("HIVE_NAME", "").lower()
    if not re.fullmatch(r"[a-z0-9_-]+", n): return None
    try: r = json.loads((HOME / "run" / f"{n}.json").read_text())
    except (OSError, ValueError): return None
    return r.get("thread") or r.get("post")


def fail(msg): raise SystemExit("remind: " + msg)


def check_names(*names):
    """Only person or an ordinary agent code can be named. Not rootwarden, warden or a warden code, and not the words that start fresh agents."""
    for n in (x.lower() for x in names):
        if n != "person" and (not AGENT.match(n) or WARDEN.match(n)):
            fail(f"@{n} can't be named in a reminder (only @person or an agent's own name, never a warden)")


def parse_when(s, now):
    s = s.strip()
    if m := re.fullmatch(r"\+(\d+)([mhd])", s): return now + dt.timedelta(**{{"m": "minutes", "h": "hours", "d": "days"}[m[2]]: int(m[1])})
    if m := re.fullmatch(r"(\d{1,2}):(\d\d)", s):
        try: t = now.replace(hour=int(m[1]), minute=int(m[2]), second=0, microsecond=0)
        except ValueError: fail(f"can't read the time {s!r}")
        return t if t > now else t + dt.timedelta(days=1)
    try: t = dt.datetime.fromisoformat(s.replace(" ", "T"))
    except ValueError: fail(f"can't read the time {s!r}")
    if t.tzinfo: t = t.astimezone().replace(tzinfo=None)
    if t < now - dt.timedelta(minutes=1): fail(f"{s} is in the past (now is {now:%Y-%m-%d %H:%M})")
    return t


def field(spec, lo, hi):
    out = set()
    for part in spec.split(","):
        rng, _, step = part.partition("/")
        a, b = (lo, hi) if rng == "*" else (map(int, rng.split("-")) if "-" in rng else (int(rng),) * 2)
        if not (lo <= a <= b <= hi) or (step and (not step.isdigit() or int(step) < 1)): raise ValueError(spec)
        out |= set(range(a, b + 1, int(step or 1)))
    return out


def parse_cron(s):
    try:
        f = s.split(); assert len(f) == 5
        mi, h, dom, mon, dow = field(f[0], 0, 59), field(f[1], 0, 23), field(f[2], 1, 31), field(f[3], 1, 12), field(f[4], 0, 7)
    except (ValueError, AssertionError): fail(f"can't read the schedule {s!r} (five fields: minute hour day month weekday)")
    return mi, h, dom, mon, {d % 7 for d in dow}, f[2] != "*", f[4] != "*"


def next_after(cron, t):
    """First minute after t that matches. Like cron: day-of-month and weekday are either/or when both are restricted."""
    mi, h, dom, mon, dow, dom_r, dow_r = parse_cron(cron)
    t = t.replace(second=0, microsecond=0) + dt.timedelta(minutes=1)
    for _ in range(366 * 24 * 60 * 2):
        d_ok, w_ok = t.day in dom, (t.isoweekday() % 7) in dow
        day = (d_ok or w_ok) if dom_r and dow_r else d_ok and w_ok
        if t.month in mon and day and t.hour in h and t.minute in mi: return t
        t += dt.timedelta(minutes=1)
    fail("that schedule never comes up")


def too_often(cron, now):
    t = now
    for _ in range(4):
        n = next_after(cron, t)
        if t is not now and (n - t).total_seconds() < MIN_GAP_MIN * 60: return True
        t = n
    return False


def load(path):
    try: return json.loads(Path(path).read_text())
    except (OSError, ValueError): return []


def save(items, path):
    p = Path(path); p.parent.mkdir(parents=True, exist_ok=True)
    tmp = p.with_suffix(".tmp"); tmp.write_text(json.dumps(items, indent=1)); tmp.replace(p)


def lock(path):
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    f = open(str(path) + ".lock", "w"); fcntl.flock(f, fcntl.LOCK_EX); return f


def add(kind, when, text, to="person", thread=None, store=None, now=None, by=""):
    store, now = store or STATE / "reminders.json", now or dt.datetime.now()
    thread = thread or woke_thread() or fail("which thread? add --thread T")
    to = to.lstrip("@").lower(); check_names(to, *MENTION.findall(text))
    if not text.strip(): fail("no text")
    if kind == "at": due = parse_when(when, now); cron = None
    else:
        cron = when; due = next_after(cron, now)
        if too_often(cron, now): fail(f"that fires more than once in {MIN_GAP_MIN} minutes")
    with lock(store):
        items = load(store)
        if len(items) >= MAX_ITEMS: fail("too many reminders; remove some")
        item = {"id": secrets.token_hex(3), "due": due.isoformat(timespec="minutes"), "cron": cron, "to": to, "thread": thread, "text": text, "by": by}
        save(items + [item], store)
    return item


def post_as_remind(item, late=False, run=subprocess.run):
    """Posted as `remind`, never as the person. The setter's own text keeps its mentions; they were checked when set and are checked again now."""
    check_names(item["to"], *MENTION.findall(item["text"]))
    text = f"@{item['to']} " + item["text"] + (" (this was due earlier; the machine was off or busy)" if late else "")
    env = {k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_THREAD")}
    run([sys.executable, "-I", str(CORE / "hive"), "post", "--as", "remind", "--to", item["to"], "--thread", item["thread"]], input=text, text=True, capture_output=True, check=True, env=env, timeout=120)


def tick(store=None, now=None, post=post_as_remind):
    store, now = store or STATE / "reminders.json", now or dt.datetime.now()
    fired = []
    with lock(store):
        keep = []
        for it in load(store):
            try: due = dt.datetime.fromisoformat(it["due"])
            except (KeyError, TypeError, ValueError): print("remind: dropped an unreadable item", json.dumps(it)[:200], file=sys.stderr); continue
            if due > now: keep.append(it); continue
            late = (now - due).total_seconds() > 1800
            try: post(it, late)
            except SystemExit as e: print(f"remind: dropped {it.get('id')}: {e}", file=sys.stderr); continue  # a name it may no longer use: retrying won't help
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, KeyError):
                keep.append(it); continue  # try again next tick
            fired.append(it["id"])
            if it["cron"]: keep.append({**it, "due": next_after(it["cron"], now).isoformat(timespec="minutes")})
        save(keep, store)
    return fired


def options(a):
    """--to NAME / --thread T (or --to=NAME) are read before the words and at the very end only, so text that says "--to" mid-sentence stays text; -- ends them."""
    opts = {}
    def take(i):
        f, eq, v = a[i].partition("=")
        if f not in ("--to", "--thread"): return False
        if not eq:
            if i + 1 >= len(a): fail(f"{f} needs a value")
            v = a.pop(i + 1)
        opts[f] = v; a.pop(i); return True
    while a and take(0): pass
    if a and a[0] == "--": a.pop(0); return a, opts
    while len(a) >= 2 and (take(len(a) - 2) if "=" not in a[-1] else take(len(a) - 1)): pass
    return a, opts


def main(argv):
    a, opts = options(list(argv)); to, thread = opts.get("--to", "person"), opts.get("--thread")
    by = os.environ.get("HIVE_NAME", "person")
    if a[:1] in (["at"], ["every"]) and len(a) >= 3:
        when, rest = a[1], a[2:]
        if a[0] == "at" and len(rest) > 1 and re.fullmatch(r"\d\d:\d\d", rest[0]) and re.fullmatch(r"\d{4}-\d\d-\d\d", when): when, rest = f"{when} {rest[0]}", rest[1:]
        it = add(a[0], when, " ".join(rest), to, thread, by=by); print(it["id"], "next", it["due"], "in", it["thread"], "(stop it: hive remind rm " + it["id"] + ")")
    elif a[:1] == ["list"]:
        items = [it for it in load(STATE / "reminders.json") if not thread or it["thread"] == thread]
        for it in items: print(it["id"], it["due"], it["cron"] or "once", "@" + it["to"], it["thread"], it["text"][:60])
        if not items: print("no reminders" + (f" in {thread}" if thread else ""))
    elif a[:1] == ["rm"] and len(a) == 2:
        with lock(STATE / "reminders.json"):
            items = load(STATE / "reminders.json"); keep = [i for i in items if i["id"] != a[1]]
            if len(keep) == len(items): fail(f"no reminder {a[1]} (see: hive remind list)")
            save(keep, STATE / "reminders.json")
        print("removed", a[1])
    elif a[:1] == ["tick"]: tick()
    else: return print(__doc__) or 2
    return 0


if __name__ == "__main__": sys.exit(main(sys.argv[1:]))
