#!/usr/bin/env python3
"""hive remind: a post into a chat at a time or on a schedule. Use this for any reminder or recurring job; never write a timer or a cron line.
  hive remind at WHEN TEXT [--to NAME] [--thread T]     WHEN: +30m, +2h, +1d, HH:MM (next one), or 2026-11-06T09:00 / "2026-11-06 09:00" (local time)
  hive remind every "M H DOM MON DOW" TEXT [--to NAME] [--thread T]     five-field cron: * , - / lists, ranges, steps; DOW 0-7
  hive remind list [--thread T]     hive remind rm ID     hive remind tick (what the hive-remind timer runs once a minute: posts what is due)
  --if "CMD" [--wait 24h]   (with at or every) post only once the shell check CMD exits 0: a reminder that waits on something outside
                            (a job's `finished` file, a printer back on line, a key pinned) wakes nobody to say "not yet". At the due time
                            and every 15 min after, CMD runs (20 s cap, in ~); a one-shot posts when it passes, or after --wait (default
                            24h, at most 7d) anyway, saying the check still failed. A schedule skips a firing whose check fails.
                            e.g. hive remind at +30m "read deans3/REVIEW.md" --to a1b2c --if "test -e ~/projects/x/deans3/finished"
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
# A reminder worded as a check on something outside ("should be done", "check whether X is free") woke its agent to find
# "not yet" 12 of 22 times in the week to 2026-11-22 (10 of 69 for other reminders): creating one without --if gets a hint.
WAITS_ON = re.compile(r"\b(should be (done|free|finished|ready|back)|check (whether|if|that)\b|once (it|the|they|[\w-]+ (is|are|has|have))\b"
                      r"|is (it|.{0,30}) (finished|done|free|back)\b|has .{0,40}(been|finished|pinned|landed)"
                      r"|if .{0,40}(is|are) (free|done|finished|ready|back|up)|(finished|done)[:,;] (start|read|run)"
                      r"|when .{0,30}(finishes|is done|is free|lands))", re.I)


def if_hint(text, cond, by):
    """The nudge printed when an agent sets a reminder that reads like a wait on an outside state but has no --if."""
    if cond or by == "person" or not WAITS_ON.search(text or ""): return ""
    return ("remind: this reads like a wait on something outside; reminders worded so found it not ready about half the time. "
            "If a shell check can tell (a `finished` file, `serialtab lease list | grep -q`, a printer ping), set it again with "
            "--if \"CMD\" so it posts only once that passes (hive remind rm the one above).")


# An agent that starts its own long job (a replay, a lead list) and sets itself a reminder to read it was graded part-done
# on the first run and woke again for the reading: 5 of 35 misses over 48 h to 2026-11-23 (ab5b4, ac009 x4), 16 in the week.
# A background waiter wakes it at the job's end and the run is graded as one (a reaped waiter resumes on the same post).
OWN_JOB = re.compile(r"^\s*(read|check|judge|look at)\b.{0,80}\b(finished|results?|output|replay|run\d*|lead-list|REVIEW|log)\b"
                     r"|\(finished( file)?\)|\bcheck .{0,60} finished\b", re.I)
OWN_JOB_MIN = 180
NOT_OWN = re.compile(r"\bcheck (whether|if)\b|\bthe person\b|\b(released|lease)\b", re.I)  # outside waits: the --if hint's


def own_job_hint(text, to, by, minutes):
    """The nudge printed when an agent reminds itself, within 3 h, to read the result of a job it just started."""
    if by == "person" or to != by or minutes is None or minutes > OWN_JOB_MIN or not OWN_JOB.search(text or "") or NOT_OWN.search(text): return ""
    return ("remind: this reads like a reminder to read your own job's result. Runs that start a job and remind themselves are graded "
            "part-done and wake again for the reading. Instead, wait on the job in this run: a background shell with a time limit "
            "(e.g. `timeout 3600 sh -c 'until test -e .../finished; do sleep 30; done'`, run_in_background) wakes you when it ends, "
            "and a waiter that gets reaped is resumed on the same post. If you keep this reminder, hive remind rm it once the waiter is set.")


# R67's hint reads the reminder's words, so an own-job wait set with --if got none: on 2026-11-26 nine runs set themselves an --if
# reminder on their own CI run, finished file or unit (a884d x6, a11e9, a0f2b, ac395), each was graded part-done, and in 8 of 9
# the run the reminder woke delivered. The check itself says it is the agent's own job; it can be the loop of a background waiter.
IF_JOB = re.compile(r"\bgh (run|api)\b.{0,80}\b(runs?|jobs?)\b|actions/(runs|jobs)|\btest -[ef]\b|\[ -[ef] |\bls\b.{0,80}\bwc -l"
                    r"|\bgrep -\w*q\w*\b.{0,40}\b(finished|done|ALL DONE|ok)\b|\bsystemctl\b.{0,40}\b(is-active|ActiveState)\b"
                    r"|\bcurl\b.{0,80}\|\s*grep -\w*q|\bps -p\b|\bgrep -\w*q\w* ['\"]?rc=", re.I)
IF_NOT_JOB = re.compile(r"\b(hive|h4) (read|search)\b|board\.jsonl|\bprinter\b|\blease\b|\bserialtab\b|\bgh (pr|release)\b|\bping\b", re.I)  # others' posts, a print, a bench


def if_job_hint(cond, to, by, wait=None):
    """The nudge printed when an agent sets itself an --if reminder whose check is the state of its own job (CI run, finished file, unit)."""
    if not cond or by == "person" or to != by or not IF_JOB.search(cond) or IF_NOT_JOB.search(cond): return ""
    try:
        if wait and parse_wait(wait) > OWN_JOB_MIN / 60: return ""
    except SystemExit: return ""
    return ("remind: this --if reminder waits on your own job (its check reads a CI run, a finished file or a unit). A run that ends on "
            "it is graded part-done and wakes again for the reading (9 such runs on 2026-11-26). Instead, wait in this run: a background "
            "shell with your check as its loop, e.g. `timeout 3600 sh -c 'until YOUR_CHECK; do sleep 30; done'` with run_in_background, "
            "wakes you when it passes, and a waiter that gets reaped is resumed on the same post. Then hive remind rm the reminder above.")


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
        own = n == os.environ.get("HIVE_NAME", "").lower() and (HOME / "run" / f"{n}.json").exists()   # a named agent (nightly) may remind itself, as the help says
        if n != "person" and not own and (not AGENT.match(n) or WARDEN.match(n)) or n in ("warden", "rootwarden"):
            fail(f"@{n} can't be named in a reminder (only @person or an agent's own name, never a warden)")


THREAD = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.:-]{0,100}")
MAX_FAILS = 30
CHECK_EVERY_MIN = 15      # an unmet --if is looked at again this often
CHECK_TIMEOUT_S = 20
WAIT_DEFAULT_H, WAIT_MAX_H = 24, 7 * 24
# i0423 (a3810, 2026-11-22): 15 of the 21 reminder wakes graded nothing or partial in the 7 days to 11-22 were polls of an outside
# condition that had not changed (printer off line, key not pinned, lease held, replay not finished): a whole model run to say "not yet".  # a post that fails this many ticks in a row (a thread hive refuses) is dropped, not retried forever


def mine(it, by, here):
    """The person may touch every reminder; an agent the ones it set and the ones in the thread it is working in (whoever answers "stop it" there)."""
    return by == "person" or it.get("by") == by or (here is not None and it.get("thread") == here)


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


def parse_wait(s):
    m = re.fullmatch(r"\+?(\d+)([mhd])", str(s).strip())
    if not m: fail(f"can't read --wait {s!r} (e.g. 6h, 2d)")
    h = int(m[1]) * {"m": 1 / 60, "h": 1, "d": 24}[m[2]]
    if not 0 < h <= WAIT_MAX_H: fail(f"--wait is at most {WAIT_MAX_H // 24}d")
    return h


def check(cmd, run=subprocess.run):
    """True when the --if shell check exits 0. A check that hangs or can't start counts as not met."""
    env = {k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_THREAD")}
    try: return run(["bash", "-c", cmd], cwd=str(HOME), env=env, capture_output=True, timeout=CHECK_TIMEOUT_S, stdin=subprocess.DEVNULL).returncode == 0
    except (subprocess.TimeoutExpired, OSError): return False


def add(kind, when, text, to="person", thread=None, store=None, now=None, by="", cond=None, wait=None):
    store, now = store or STATE / "reminders.json", now or dt.datetime.now()
    thread = thread or woke_thread() or fail("which thread? add --thread T")
    if not THREAD.fullmatch(thread): fail(f"{thread!r} is not a thread id")
    to = to.lstrip("@").lower(); check_names(to, *MENTION.findall(text))
    if by and by != "person" and to not in ("person", by.lower()): fail(f"an agent's reminder goes to @person or to itself, not @{to}")
    if not text.strip(): fail("no text")
    if kind == "at": due = parse_when(when, now); cron = None
    else:
        cron = when; due = next_after(cron, now)
        if too_often(cron, now): fail(f"that fires more than once in {MIN_GAP_MIN} minutes")
    with lock(store):
        items = load(store)
        if sum(i.get("by") == by for i in items) >= MAX_ITEMS or len(items) >= 10 * MAX_ITEMS: fail("too many reminders; remove some")
        item = {"id": secrets.token_hex(3), "due": due.isoformat(timespec="minutes"), "cron": cron, "to": to, "thread": thread, "text": text, "by": by}
        if cond:
            item["if"] = cond
            if not cron: item["until"] = (due + dt.timedelta(hours=parse_wait(wait) if wait else WAIT_DEFAULT_H)).isoformat(timespec="minutes")
        elif wait: fail("--wait goes with --if")
        save(items + [item], store)
    return item


def post_as_remind(item, late=False, run=subprocess.run):
    """Posted as `remind`, never as the person. The setter's own text keeps its mentions; they were checked when set and are checked again now."""
    check_names(item["to"], *MENTION.findall(item["text"]))
    text = (f"@{item['to']} " + item["text"] + (" (this was due earlier; the machine was off or busy)" if late else "")
            + (f" (the check `{item['if']}` still fails; posted because its wait ran out)" if item.get("gave_up") else f" (its check `{item['if']}` now passes)" if item.get("if") else ""))
    env = {k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_THREAD")}
    run([sys.executable, "-I", str(CORE / "hive"), "post", "--as", "remind", "--to", item["to"], "--thread=" + item["thread"]], input=text, text=True, capture_output=True, check=True, env=env, timeout=120)


def tick(store=None, now=None, post=post_as_remind, checker=check):
    store, now = store or STATE / "reminders.json", now or dt.datetime.now()
    fired = []
    with lock(store):
        keep = []
        for it in load(store):
            try: due = dt.datetime.fromisoformat(it["due"])
            except (KeyError, TypeError, ValueError): print("remind: dropped an unreadable item", json.dumps(it)[:200], file=sys.stderr); continue
            if due > now: keep.append(it); continue
            late = (now - due).total_seconds() > 1800
            if it.get("if"):
                if checker(it["if"]): it.pop("gave_up", None)
                elif it["cron"]: keep.append({**it, "due": next_after(it["cron"], now).isoformat(timespec="minutes")}); continue  # this firing skipped
                elif now < dt.datetime.fromisoformat(it.get("until") or it["due"]):
                    keep.append({**it, "due": (now + dt.timedelta(minutes=CHECK_EVERY_MIN)).isoformat(timespec="minutes"), "first_due": it.get("first_due", it["due"])}); continue
                else: it = {**it, "gave_up": True}
                if it.get("first_due"): late = False   # held on purpose, not missed
            try: post(it, late)
            except SystemExit as e: print(f"remind: dropped {it.get('id')}: {e}", file=sys.stderr); continue  # a name it may no longer use: retrying won't help
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, KeyError):
                if it.get("fails", 0) + 1 >= MAX_FAILS: print(f"remind: dropped {it.get('id')} after {MAX_FAILS} failed posts", file=sys.stderr)
                else: keep.append({**it, "fails": it.get("fails", 0) + 1})
                continue  # try again next tick
            fired.append(it["id"]); it.pop("fails", None)
            if it["cron"]: keep.append({**it, "due": next_after(it["cron"], now).isoformat(timespec="minutes")})
        save(keep, store)
    return fired


def options(a):
    """--to NAME / --thread T (or --to=NAME) are read before the words, right after WHEN (`at 13:00 --to me TEXT`: else the flags were text and the
    reminder went to the person), and at the very end only, so text that says "--to" mid-sentence stays text; -- ends them."""
    opts = {}
    def take(i):
        f, eq, v = a[i].partition("=")
        if f not in ("--to", "--thread", "--if", "--wait"): return False
        if not eq:
            if i + 1 >= len(a): fail(f"{f} needs a value")
            v = a.pop(i + 1)
        opts[f] = v; a.pop(i); return True
    while a and take(0): pass
    if a and a[0] == "--": a.pop(0); return a, opts
    if a[:1] in (["at"], ["every"]):  # the text starts after WHEN, two words for "at 2026-11-06 09:00"
        i = 3 if a[0] == "at" and len(a) > 3 and re.fullmatch(r"\d{4}-\d\d-\d\d", a[1]) and re.fullmatch(r"\d\d:\d\d", a[2]) else 2
        while len(a) > i and take(i): pass
    # the last word is a flag only in the --to=NAME form; a value with "=" in it (`--if "grep -q rc= log"`) is still the value of the
    # flag before it, else every flag became text and the reminder went to the person (a12a5 2026-11-24)
    tail = lambda: take(len(a) - 1) if "=" in a[-1] and a[-1].partition("=")[0] in ("--to", "--thread", "--if", "--wait") else take(len(a) - 2)
    while len(a) >= 2 and tail(): pass
    return a, opts


def main(argv):
    a, opts = options(list(argv)); to, thread = opts.get("--to", "person"), opts.get("--thread")
    cond, wait = opts.get("--if"), opts.get("--wait")
    if cond is not None and not cond.strip(): fail("--if needs a shell check")
    if cond is not None and os.environ.get("HIVE_REMIND_NO_IF"): fail("--if can't be set from inside the sandbox (its check runs outside it)")
    by = os.environ.get("HIVE_NAME") or "person"
    if a[:1] in (["at"], ["every"]) and len(a) >= 3:
        when, rest = a[1], a[2:]
        if a[0] == "at" and len(rest) > 1 and re.fullmatch(r"\d\d:\d\d", rest[0]) and re.fullmatch(r"\d{4}-\d\d-\d\d", when): when, rest = f"{when} {rest[0]}", rest[1:]
        it = add(a[0], when, " ".join(rest), to, thread, by=by, cond=cond, wait=wait); print(it["id"], "next", it["due"], "in", it["thread"], (f"once `{cond}` passes" + (f", or at {it['until']} anyway " if it.get("until") else " ")) if cond else "", "(stop it: hive remind rm " + it["id"] + ")")
        try: mins = (dt.datetime.fromisoformat(it["due"]) - dt.datetime.now(dt.datetime.fromisoformat(it["due"]).tzinfo)).total_seconds() / 60 if a[0] == "at" else None
        except (ValueError, TypeError): mins = None
        if (h := own_job_hint(it["text"], it["to"], by.lower(), mins) or if_job_hint(cond, it["to"], by.lower(), wait)): print(h, file=sys.stderr)
        elif not os.environ.get("HIVE_REMIND_NO_IF") and (h := if_hint(it["text"], cond, by)): print(h, file=sys.stderr)  # the sandbox can't set --if
    elif a[:1] == ["list"]:
        here = woke_thread() if by != "person" else None
        items = [it for it in load(STATE / "reminders.json") if mine(it, by, here) and (not thread or it["thread"] == thread)]
        for it in items: print(it["id"], it["due"], it["cron"] or "once", "@" + it["to"], it["thread"], it["text"][:60] + (f"  [if {it['if'][:50]}]" if it.get("if") else ""))
        if not items: print("no reminders" + (f" in {thread}" if thread else ""))
    elif a[:1] == ["rm"] and len(a) == 2:
        with lock(STATE / "reminders.json"):
            items = load(STATE / "reminders.json"); here = woke_thread() if by != "person" else None
            keep = [i for i in items if i["id"] != a[1] or not mine(i, by, here)]
            if len(keep) == len(items): fail(f"no reminder {a[1]} of yours or in this thread (see: hive remind list)")
            save(keep, STATE / "reminders.json")
        print("removed", a[1])
    elif a[:1] == ["tick"]: tick()
    else: return print(__doc__) or 2
    return 0


if __name__ == "__main__": sys.exit(main(sys.argv[1:]))
