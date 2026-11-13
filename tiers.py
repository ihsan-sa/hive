#!/usr/bin/env python3
"""Work tiers (projects/work-tiers/DESIGN.md; the person, 111311199053: "a flexible tiering system so users can themselves set the
tiers and usage stop points").

Every piece of work sits in a tier: a focus (its threads follow it), a thread override (a glob, like hwlab-bldc*), a timer job, or
the Hive's own rounds (the pseudo-focus `hive`). A tier has a stop point (a share of the week's usage past which nothing new in it
starts; None: only the logins' own stop lines) and may be paused, with or without an end. The Off tier is paused with no end.
Unplaced work falls in the default tier. Timers not placed are not gated (tier 0, upkeep, is never placed).

The settings are the person's: ~/.hive/tiers.json, every change also in ~/.hive/tiers-log.jsonl. This module is pure logic plus the
store; the starter and core/tier-gate ask it whether a tier is held. Nothing here prints a usage figure: agents never see one.
"""
import argparse, fnmatch, json, os, re, subprocess, sys, time
from pathlib import Path

STATE = Path(os.environ.get("HIVE_STATE", Path.home() / ".hive"))
CORE = Path(__file__).resolve().parent
FILE = "tiers.json"
LOG = "tiers-log.jsonl"
OFF = "off"

DEFAULT = {
    "default": "2",
    "tiers": [
        {"id": "1", "name": "Core", "stop": None},
        {"id": "2", "name": "Normal", "stop": 85},
        {"id": "3", "name": "Extra", "stop": 60},
        {"id": OFF, "name": "Off", "stop": None, "paused": {"until": None, "why": "off"}},
    ],
    # Placement is each Hive's own: it lives in ~/.hive/tiers.json (hive tier place), not in the shipped default.
    "place": {},
}


def _read(name, default):
    try: return json.loads((STATE / name).read_text())
    except (OSError, ValueError): return default


def load(strict=False):
    """The settings. A missing file is the shipped default; an unreadable one raises when strict (the gate fails closed on it)."""
    p = STATE / FILE
    if not p.exists(): return json.loads(json.dumps(DEFAULT))
    try:
        d = json.loads(p.read_text())
        if not isinstance(d, dict) or not isinstance(d.get("tiers"), list): raise ValueError("no tiers list")
        d.setdefault("place", {}); d.setdefault("default", DEFAULT["default"])
        return d
    except (OSError, ValueError):
        if strict: raise
        return json.loads(json.dumps(DEFAULT))


def save(d, who, what, post=None):
    """Write atomically and log the change (who, what, post id)."""
    STATE.mkdir(parents=True, exist_ok=True)
    tmp = STATE / f".{FILE}.{os.getpid()}"
    tmp.write_text(json.dumps(d, indent=1) + "\n"); tmp.replace(STATE / FILE)
    with open(STATE / LOG, "a") as f:
        f.write(json.dumps({"at": time.time(), "who": who, "what": what, "post": post}) + "\n")


def tier(d, tid):
    tid = str(tid).lower()
    return next((t for t in d["tiers"] if str(t["id"]).lower() == tid or t["name"].lower() == tid), None)


def tier_of_job(d, job):
    """A timer's tier id, or None when it isn't placed (then it is never gated). `job` is a unit name, with or without .service/.timer."""
    job = job.removesuffix(".service").removesuffix(".timer")
    return d["place"].get(f"job:{job}")


def tier_of_focus(d, fid):
    return d["place"].get(f"focus:{fid}", d["default"]) if fid else d["default"]


def tier_of(d, thread, fid=None, why=False):
    """A thread's tier: a thread override (exact, else the longest matching glob), else its focus's, else the default.
    fid: the thread's focus (focus.focus_of), passed in so this stays pure. why: also return how it was decided."""
    pl = d["place"]
    if f"thread:{thread}" in pl:
        r = (pl[f"thread:{thread}"], f"thread {thread} is placed there")
    else:
        globs = sorted((k[7:] for k in pl if k.startswith("thread:") and any(c in k for c in "*?[")), key=len, reverse=True)
        g = next((g for g in globs if fnmatch.fnmatchcase(thread or "", g)), None)
        if g: r = (pl[f"thread:{g}"], f"threads {g} are placed there")
        elif fid and f"focus:{fid}" in pl: r = (pl[f"focus:{fid}"], f"its focus {fid} is placed there")
        elif fid: r = (d["default"], f"its focus {fid} isn't placed, so the default tier")
        else: r = (d["default"], "it is in no focus, so the default tier")
    return r if why else r[0]


def room(stop, usage=None, lines=None, paused=None, base=100):
    """Is there a login a new run could start on under this stop point (each login's own weekly line and this one)?
    No averaging: one login with room is enough. usage: {login: weekly %} (~/.hive/usage.json); lines: login_limits.json;
    paused: logins the person paused; base: config usage_stop_pct (a login with no line of its own)."""
    if stop is None: return True
    usage = _read("usage.json", {}) if usage is None else usage
    lines = _read("login_limits.json", {}) if lines is None else lines
    paused = _read("paused.json", []) if paused is None else paused
    lines = lines if isinstance(lines, dict) else {}
    paused = paused if isinstance(paused, list) else []
    for k, v in (usage or {}).items():
        if k.startswith("_") or k in paused: continue
        own = (lines.get(k) or {}).get("weekly") if isinstance(lines.get(k), dict) else None
        own = base if not isinstance(own, (int, float)) else own
        if v is None or (isinstance(v, (int, float)) and v < min(own, stop)): return True
    return False


def held(d, tid, now=None, **kw):
    """Why a tier starts no new work now, or None when it runs. Reasons carry no number: "off", "paused until <epoch>",
    "paused", "past its stop point". An unknown tier id is the default's."""
    now = time.time() if now is None else now
    t = tier(d, tid) or tier(d, d["default"])
    if t is None: return None
    p = t.get("paused")
    if p:
        u = p.get("until")
        if u is None: return OFF if str(t["id"]) == OFF else "paused"
        if now < u: return f"paused until {u}"
    if not room(t.get("stop"), **kw): return "past its stop point"
    return None


def stop_of(d, tid):
    """The tier's stop point (a weekly %), or None for none; the starter takes it as one more stop line on every login."""
    t = tier(d, tid) or tier(d, d["default"])
    return t.get("stop") if t else None


def when(u):
    return time.strftime("%a %d %b %H:%M", time.localtime(u))


# ---- hive tier: the command (hive dispatches here) ----

UNITS = Path.home() / ".config/systemd/user"
DROPIN = "tier-gate.conf"
HELD_LINE = "Held: this thread's tier isn't starting new work now; it starts by itself when it does."


def foci():
    try:
        sys.path.insert(0, str(CORE)); import focus
        return focus, focus.state(posts=[])
    except Exception:
        return None, {"foci": [], "tags": {}}


def timers():
    """[{unit, service, next}] for every user timer (next: epoch or None when stopped)."""
    try:
        out = subprocess.run(["systemctl", "--user", "list-timers", "--all", "-o", "json"], capture_output=True, text=True, timeout=30).stdout
        return [{"unit": t["unit"], "service": t.get("activates") or t["unit"].replace(".timer", ".service"),
                 "next": (t["next"] / 1e6) if t.get("next") else None} for t in json.loads(out or "[]")]
    except Exception:
        return []


def when_arg(s, now=None):
    """--until: 3h, 2d, 45m, an epoch, or anything `date -d` reads ("mon 09:00", "2026-11-16 09:00"). A bare weekday is its next one, 09:00."""
    now = time.time() if now is None else now
    s = s.strip().lower()
    m = re.fullmatch(r"(\d+(?:\.\d+)?)\s*([mhdw])", s)
    if m: return now + float(m[1]) * {"m": 60, "h": 3600, "d": 86400, "w": 604800}[m[2]]
    if re.fullmatch(r"\d{9,11}", s): return float(s)
    if re.fullmatch(r"(mon|tue|wed|thu|fri|sat|sun)[a-z]*", s): s = f"next {s} 09:00"
    r = subprocess.run(["date", "-d", s, "+%s"], capture_output=True, text=True)
    if r.returncode: sys.exit(f"hive tier: can't read the time {s!r} (try 3h, 2d, mon, or '2026-11-16 09:00')")
    return float(r.stdout)


def who(a):
    """The person, or an agent with the person's post that asked for this change (a change by an agent needs --post)."""
    me = os.environ.get("HIVE_NAME")
    if not me: return "person"
    if not a.post: sys.exit("hive tier: an agent changes tiers only for the person: add --post ID (their post that asked for it). Nothing changed.")
    try:
        with open(Path.home() / "board.jsonl") as f:
            for ln in f:
                if a.post in ln:
                    q = json.loads(ln)
                    if q.get("id") == a.post:
                        if q.get("from") != "person": sys.exit(f"hive tier: post {a.post} isn't the person's. Nothing changed.")
                        return me
    except OSError: pass
    sys.exit(f"hive tier: no post {a.post} on the board. Nothing changed.")


def state_text(d, t, now=None):
    why = held(d, t["id"], now)
    if why is None: return "running"
    if why == OFF: return "off"
    if why.startswith("paused until"): return f"paused until {when(float(why.split()[-1]))}"
    return f"held: {why}"


def stop_text(t):
    return "runs to the logins' own stop lines" if t.get("stop") is None else f"stop point {t['stop']:g}% of the week"


def holdings(d, st):
    """{tier id: {"focus": [...], "thread": [...], "job": [...]}}, unplaced foci under the default."""
    out = {str(t["id"]): {"focus": [], "thread": [], "job": []} for t in d["tiers"]}
    for k, v in d["place"].items():
        kind, _, x = k.partition(":")
        out.setdefault(str(v), {"focus": [], "thread": [], "job": []}).setdefault(kind, []).append(x)
    for f in st["foci"]:
        if f"focus:{f['id']}" not in d["place"]: out.setdefault(str(d["default"]), {"focus": [], "thread": [], "job": []})["focus"].append(f["id"])
    return out


def cmd_list(a, d):
    _, st = foci()
    h = holdings(d, st)
    if a.json:
        print(json.dumps([{**t, "state": state_text(d, t), "holds": h.get(str(t["id"]), {}), "default": str(t["id"]) == str(d["default"])} for t in d["tiers"]], indent=1)); return
    for t in d["tiers"]:
        tid = str(t["id"])
        print(f"Tier {tid} {t['name']}{' (default)' if tid == str(d['default']) else ''}: {stop_text(t) if tid != OFF else 'nothing in it runs'}; {state_text(d, t)}")
        for kind, label in (("focus", "focuses"), ("thread", "threads"), ("job", "jobs")):
            xs = sorted(h.get(tid, {}).get(kind, []))
            if xs: print(f"  {label}: {', '.join(xs)}")
    print("Tier 0 upkeep (backups, guards, the watchdog, every timer not placed) is never held. `hive tier jobs` lists the timers.")


def placement_key(x):
    """focus:ID, thread:NAME or job:UNIT; a bare word is a focus if one has that id, a job if a timer has that name, else a thread."""
    if re.match(r"(focus|thread|job):", x): return x
    focus, st = foci()
    f = focus.find(st, x) if focus else None
    if f: return f"focus:{f['id']}"
    base = x.removesuffix(".service").removesuffix(".timer")
    if any(t["unit"] == base + ".timer" for t in timers()) or (UNITS / f"{base}.timer").exists(): return f"job:{base}"
    return f"thread:{x}"


def cmd_set(a, d):
    by = who(a)
    t = tier(d, a.tier)
    if not t: sys.exit(f"hive tier: no tier {a.tier!r}; `hive tier` lists them")
    k = placement_key(a.what)
    was = d["place"].get(k)
    if k.startswith("job:") and k.split(":", 1)[1] in TIER0: sys.exit(f"hive tier: {k[4:]} is upkeep (tier 0) and is never held")
    d["place"][k] = str(t["id"]); save(d, by, f"set {k} {was} -> {t['id']}", a.post)
    print(f"{k.replace(':', ' ', 1)}: now in tier {t['id']} {t['name']} (was {was or 'not placed / default'}); it is {state_text(d, t)}")
    if k.startswith("job:") and not (UNITS / f"{k[4:]}.service.d" / DROPIN).exists():
        print("  (the job isn't gated yet: run `hive tier install` to put the gate in front of it)")


def cmd_unset(a, d):
    by = who(a)
    k = placement_key(a.what)
    if k not in d["place"]: print(f"{k}: not placed; nothing changed"); return
    was = d["place"].pop(k); save(d, by, f"unset {k} (was {was})", a.post)
    print(f"{k.replace(':', ' ', 1)}: no longer placed (was tier {was}); " + ("a job not placed always runs" if k.startswith("job:") else "it follows its focus or the default"))


def pct(s):
    if s.lower() in ("none", "off", "-"): return None
    v = float(s.rstrip("%"))
    if not 1 <= v <= 100: sys.exit("hive tier: a stop point is 1..100 (% of the week), or none")
    return v


def cmd_stop(a, d):
    by = who(a)
    t = tier(d, a.tier)
    if not t or str(t["id"]) == OFF: sys.exit(f"hive tier: no tier {a.tier!r} with a stop point")
    was = t.get("stop"); t["stop"] = pct(a.pct); save(d, by, f"stop {t['id']} {was} -> {t['stop']}", a.post)
    print(f"Tier {t['id']} {t['name']}: {stop_text(t)}; {state_text(d, t)}")


def cmd_pause(a, d):
    by = who(a)
    until = when_arg(a.until) if a.until else None
    for x in a.tiers:
        t = tier(d, x)
        if not t: sys.exit(f"hive tier: no tier {x!r}")
        if str(t["id"]) == OFF: continue
        t["paused"] = {"until": until, "why": a.why, "by": by, "at": time.time()}
        print(f"Tier {t['id']} {t['name']}: paused " + (f"until {when(until)}; it starts again by itself then" if until else "until `hive tier resume`"))
    save(d, by, f"pause {' '.join(a.tiers)} until {until} ({a.why})", a.post)


def cmd_resume(a, d):
    by = who(a)
    for x in a.tiers:
        t = tier(d, x)
        if not t: sys.exit(f"hive tier: no tier {x!r}")
        if str(t["id"]) == OFF: print("The Off tier stays off: move work out of it with `hive tier set X N`"); continue
        t.pop("paused", None); print(f"Tier {t['id']} {t['name']}: {state_text(d, t)}")
    save(d, by, f"resume {' '.join(a.tiers)}", a.post)


def cmd_new(a, d):
    by = who(a)
    if tier(d, a.name): sys.exit(f"hive tier: a tier {a.name!r} exists")
    ids = [int(t["id"]) for t in d["tiers"] if str(t["id"]).isdigit()]
    t = {"id": str(max(ids, default=0) + 1), "name": a.name, "stop": pct(a.stop) if a.stop else None}
    d["tiers"].insert(len([x for x in d["tiers"] if str(x["id"]) != OFF]), t)
    save(d, by, f"new {t}", a.post); print(f"Tier {t['id']} {t['name']}: {stop_text(t)}")


def cmd_rename(a, d):
    by = who(a)
    t = tier(d, a.tier)
    if not t: sys.exit(f"hive tier: no tier {a.tier!r}")
    was = t["name"]; t["name"] = a.name; save(d, by, f"rename {t['id']} {was} -> {a.name}", a.post)
    print(f"Tier {t['id']}: {was} is now {a.name}")


def cmd_drop(a, d):
    by = who(a)
    t = tier(d, a.tier)
    if not t: sys.exit(f"hive tier: no tier {a.tier!r}")
    tid = str(t["id"])
    if tid in (OFF, str(d["default"])): sys.exit("hive tier: the default tier and Off can't be dropped (make another the default first with `hive tier default N`)")
    moved = [k for k, v in d["place"].items() if str(v) == tid]
    for k in moved: d["place"][k] = str(d["default"])
    d["tiers"].remove(t); save(d, by, f"drop {tid}; moved {moved} to {d['default']}", a.post)
    print(f"Tier {tid} {t['name']} dropped; {len(moved)} placements moved to the default tier {d['default']}")


def cmd_default(a, d):
    by = who(a)
    t = tier(d, a.tier)
    if not t or str(t["id"]) == OFF: sys.exit(f"hive tier: no tier {a.tier!r} to be the default")
    was = d["default"]; d["default"] = str(t["id"]); save(d, by, f"default {was} -> {t['id']}", a.post)
    print(f"Unplaced work is now in tier {t['id']} {t['name']} (was {was})")


# Upkeep: never placed, never gated, whatever a placement says.
TIER0 = {"hive-offsite", "hive-offsite-b2", "hive-tmp-sweep", "hive-watchdog", "hive-boxwatch", "cf-guard", "hive-kb-restore", "hive-mirror-kb",
         "hive-preview-sweep", "hive-modules-pull", "launchpadlib-cache-clean"}


def cmd_jobs(a, d):
    ts = timers()
    rows = []
    for t in sorted(ts, key=lambda t: t["unit"]):
        job = t["unit"].removesuffix(".timer")
        tid = tier_of_job(d, job)
        gated = (UNITS / f"{t['service']}.d" / DROPIN).exists()
        if tid is None or job in TIER0:
            st = "upkeep, never held" if job in TIER0 else "not placed: always runs"
        else:
            tt = tier(d, tid)
            st = f"tier {tid} {tt['name'] if tt else '?'}: {state_text(d, tt) if tt else 'unknown tier, runs as default'}"
            if not gated: st += " (NOT GATED: run `hive tier install`)"
        nxt = when(t["next"]) if t["next"] else "stopped by hand"
        rows.append({"job": job, "tier": tid, "state": st, "next": nxt, "gated": gated})
    if a.json: print(json.dumps(rows, indent=1)); return
    w = max((len(r["job"]) for r in rows), default=10)
    for r in rows: print(f"{r['job']:<{w}}  {r['next']:<17}  {r['state']}")
    placed = {k[4:] for k in d["place"] if k.startswith("job:")}
    missing = sorted(placed - {r["job"] for r in rows})
    if missing: print(f"placed but no such timer: {', '.join(missing)}")


def cmd_why(a, d):
    focus, st = foci()
    k = placement_key(a.what)
    if k.startswith("job:"):
        tid = tier_of_job(d, k[4:]); how = "the job is placed there" if tid else None
        if tid is None: print(f"{k[4:]}: not placed in a tier, so it always runs"); return
    elif k.startswith("focus:"):
        tid = tier_of_focus(d, k[6:]); how = "the focus is placed there" if f"focus:{k[6:]}" in d["place"] else "the focus isn't placed, so the default tier"
    else:
        th = k[7:]
        fid = None
        if focus:
            try: fid = focus.focus_of(st, th, focus.board_posts())
            except Exception: fid = focus.focus_of(st, th)
        tid, how = tier_of(d, th, fid, why=True)
    t = tier(d, tid) or tier(d, d["default"])
    print(f"{a.what}: tier {t['id']} {t['name']} ({how}); " + (f"{stop_text(t)}; " if str(t["id"]) != OFF else "") + state_text(d, t))


def cmd_install(a, d):
    """Write the gate drop-in in front of every placed job's service, take it off the ones no longer placed, reload systemd."""
    placed = {k[4:] for k, v in d["place"].items() if k.startswith("job:")} - TIER0
    have = {p.parent.name.removesuffix(".service.d") for p in UNITS.glob(f"*.service.d/{DROPIN}")}
    added, removed = [], []
    for j in sorted(placed):
        if not (UNITS / f"{j}.service").exists(): print(f"  {j}: no {j}.service in {UNITS}, skipped"); continue
        dd = UNITS / f"{j}.service.d"; f = dd / DROPIN
        body = f"# hive tier install: skip this job while its work tier is held (core/tiers.py)\n[Service]\nExecCondition={CORE}/tier-gate %N\n"
        if f.exists() and f.read_text() == body: continue
        dd.mkdir(exist_ok=True); f.write_text(body); added.append(j)
    for j in sorted(have - placed):
        (UNITS / f"{j}.service.d" / DROPIN).unlink(); removed.append(j)
    if added or removed: subprocess.run(["systemctl", "--user", "daemon-reload"], check=False)
    print(f"gated: {len(placed)} jobs ({len(added)} new{', ' + ', '.join(added) if added else ''}); ungated: {', '.join(removed) or 'none'}")
    def active(j):
        return subprocess.run(["systemctl", "--user", "is-active", "--quiet", f"{j}.timer"]).returncode == 0
    stopped = [j for j in sorted(placed) if (UNITS / f"{j}.timer").exists() and (UNITS / f"{j}.service.d" / DROPIN).exists() and not active(j)]
    safe = [j for j in stopped if held(d, tier_of_job(d, j))]
    if a.start and safe:
        for j in safe: subprocess.run(["systemctl", "--user", "enable", "--now", f"{j}.timer"], check=False)
        print(f"turned back on, held by their tier's gate until it runs: {', '.join(safe)}")
    elif safe:
        print(f"stopped by hand, now held by their tier instead: {', '.join(safe)}. `hive tier install --start` turns these back on")
    rest = [j for j in stopped if j not in safe]
    if rest: print(f"stopped by hand in a tier that runs, left stopped (start them yourself if they should run): {', '.join(rest)}")


def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive tier", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd")
    def chg(p): p.add_argument("--post", help="the person's post that asked for this change (needed when an agent makes it)"); return p
    p = sp.add_parser("list", help="each tier: stop point, state, what it holds"); p.add_argument("--json", action="store_true")
    p = chg(sp.add_parser("set", help="hive tier set X N: put a focus, thread (or glob like hwlab-bldc*) or timer job in tier N")); p.add_argument("what"); p.add_argument("tier")
    p = chg(sp.add_parser("unset", help="hive tier unset X: take a placement off (a thread then follows its focus; a job is no longer gated)")); p.add_argument("what")
    p = chg(sp.add_parser("stop", help="hive tier stop N PCT|none: the tier starts no new work past PCT%% of the week")); p.add_argument("tier"); p.add_argument("pct")
    p = chg(sp.add_parser("pause", help="hive tier pause N... [--until WHEN] [--why ...]: no new work in these tiers; lifts by itself at WHEN"))
    p.add_argument("tiers", nargs="+"); p.add_argument("--until"); p.add_argument("--why", default="")
    p = chg(sp.add_parser("resume", help="hive tier resume N...")); p.add_argument("tiers", nargs="+")
    p = chg(sp.add_parser("new", help="hive tier new NAME [--stop PCT]")); p.add_argument("name"); p.add_argument("--stop")
    p = chg(sp.add_parser("rename", help="hive tier rename N NAME")); p.add_argument("tier"); p.add_argument("name")
    p = chg(sp.add_parser("drop", help="hive tier drop N: its work moves to the default tier")); p.add_argument("tier")
    p = chg(sp.add_parser("default", help="hive tier default N: where unplaced work goes")); p.add_argument("tier")
    p = sp.add_parser("jobs", help="every timer: its tier, held or not, next firing"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("why", help="hive tier why THREAD|FOCUS|JOB: which tier and whether it is held"); p.add_argument("what")
    p = sp.add_parser("install", help="put the tier gate in front of every placed job (idempotent)"); p.add_argument("--start", action="store_true")
    a = ap.parse_args(argv)
    d = load()
    {"list": cmd_list, None: cmd_list, "set": cmd_set, "unset": cmd_unset, "stop": cmd_stop, "pause": cmd_pause, "resume": cmd_resume,
     "new": cmd_new, "rename": cmd_rename, "drop": cmd_drop, "default": cmd_default, "jobs": cmd_jobs, "why": cmd_why,
     "install": cmd_install}[a.cmd](a if a.cmd else argparse.Namespace(json=False), d)


if __name__ == "__main__":
    main()
