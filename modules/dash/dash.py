#!/usr/bin/env python3
"""hive-dash: a view of the Hive: agents, comms, the mesh between them, work underway, and the control center. What it may do depends on the
route, see "who may do what" below. With no ~/.hive/web.json it is read-only but for the person's switches: pausing a login,
its stop lines, a thread's tier, and killing a running agent. With one (an installed Hive) it's also the chat and the settings.

Reads only: ~/run/*.json, ~/sessions/*.jsonl (tails), ~/log.jsonl, ~/board.jsonl, ~/kb/priorities.md,
~/.hive/{starter,usage,usage_5h,paused,login_limits}.json, ~/projects/*. Writes only ~/.hive/paused.json (POST /api/login-pause),
~/.hive/login_limits.json (POST /api/login-limit) and the "Set from the dashboard" section of ~/kb/priorities.md
(POST /api/tier). POST /api/kill is the only thing that signals: it kills one agent's run, writes ~/.hive/killed/<name>.json for
the starter, and appends a line each to ~/log.jsonl and ~/board.jsonl.
The web page is web/ next to this file (ui.html, the previous page, is at /old); it does all its own counting in the browser.

  hive-dash                 text snapshot
  hive-dash --watch [S]     redraw every S seconds (default 5)
  hive-dash serve [--port P] web UI on http://127.0.0.1:P/ (loopback only; default 8077)
  hive-dash --json          the snapshot as JSON
"""
import base64, fcntl, gzip, hashlib, hmac, importlib.util, json, os, pty, pwd, re, secrets, select, shutil, signal, struct, subprocess, sys, termios, threading, time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit

HOME = Path(os.environ.get("HIVE_HOME", Path.home()))
RUN, LOG, BOARD = HOME / "run", HOME / "log.jsonl", HOME / "board.jsonl"
HOME_FILES = HOME / "files"
STATE, PROJECTS = HOME / ".hive", HOME / "projects"
PRIORITIES = HOME / "kb" / "priorities.md"
UI = Path(__file__).resolve().parent / "ui.html"
UI_DIR = Path(__file__).resolve().parent / "web"  # the rebuilt page: index.html, css/, js/. Only those are served, not its preview or fixtures.
DISPLAY_PAGES = ("/single-screen", "/dual-screen", "/dual-screen/1", "/dual-screen/2")  # TV pages: web/display.html; which tiles each shows is in js/display/layouts.js
UI_CT = {".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}
THREAD_HOURS = 12


def jload(p, d=None):
    try: return json.loads(Path(p).read_text())
    except Exception: return d


def jlines(p, tail_bytes=None):
    try:
        with open(p, "rb") as f:
            if tail_bytes:
                f.seek(0, 2); n = f.tell(); f.seek(max(0, n - tail_bytes))
                if n > tail_bytes: f.readline()  # drop partial line
            data = f.read()
    except OSError:
        return []
    out = []
    for ln in data.splitlines():
        try: out.append(json.loads(ln))
        except Exception: pass
    return out


def ts(s):
    try: return datetime.fromisoformat(str(s).replace("Z", "+00:00")).timestamp()
    except Exception: return None


def ago(t):
    if t is None: return "?"
    d = int(time.time() - t)
    if d < 0: return "now"
    if d < 90: return f"{d}s"
    if d < 5400: return f"{d // 60}m"
    if d < 172800: return f"{d / 3600:.1f}h"
    return f"{d // 86400}d"


def short(s, n):
    s = " ".join(str(s or "").split())
    return s if len(s) <= n else s[: n - 1] + "…"


def alive(pid):
    return bool(pid) and Path(f"/proc/{pid}").exists()


_effort = {}
def run_effort(pid):
    """The effort a running agent works at: CLAUDE_EFFORT in its environment if the starter set one, else the value
    Claude Code exports to its tool shells (seen while a tool runs; kept per pid). None until one is seen."""
    try: pid = int(pid)
    except (TypeError, ValueError): return None
    for k in [k for k in _effort if not alive(k)]: del _effort[k]
    if pid in _effort: return _effort[pid]
    def env_of(p):
        try: return next((v[14:].decode(errors="replace") for v in Path(f"/proc/{p}/environ").read_bytes().split(b"\0")
                          if v.startswith(b"CLAUDE_EFFORT=")), None)
        except OSError: return None
    kids = []
    try:
        for t in Path(f"/proc/{pid}/task").iterdir(): kids += (t / "children").read_text().split()
    except OSError: pass
    v = env_of(pid) or next((e for k in kids if (e := env_of(k))), None)
    if v: _effort[pid] = v
    return v


def describe_tool(name, inp):
    """What a tool call is doing, in plain words where the call gives us any."""
    inp = inp or {}
    fn = Path(str(inp.get("file_path") or "")).name
    if name == "Bash": return inp.get("description") or f"Running: {inp.get('command', '')}"
    if name in ("Agent", "Task"): return f"Delegating: {inp.get('description') or inp.get('prompt', '')}"
    if name in ("Read", "Edit", "Write") and fn:
        return {"Read": "Reading ", "Edit": "Editing ", "Write": "Writing "}[name] + fn
    for k in ("description", "pattern", "query", "url", "skill"):
        if inp.get(k): return f"{name}: {inp[k]}"
    return name


_activity = {}  # out path -> (mtime, result): a session's tail is parsed again only once it has changed


def activity(out):
    """Last thing the session did, from the tail of its stream-json output."""
    p = Path(out or "")
    res = {"last": None, "doing": "", "said": "", "turns": 0, "cost": None}
    try: m = p.stat().st_mtime
    except OSError: return res
    hit = _activity.get(str(p))
    if hit and hit[0] == m: return hit[1]
    res["last"] = m
    for e in jlines(p, 256 * 1024):
        t = e.get("type")
        if t == "assistant":
            for c in e.get("message", {}).get("content", []) or []:
                if c.get("type") == "tool_use":
                    res["doing"] = describe_tool(c.get("name"), c.get("input"))
                elif c.get("type") == "text" and c.get("text", "").strip():
                    res["said"] = c["text"]
        elif t == "result":
            res["turns"] += 1
            if e.get("total_cost_usd") is not None: res["cost"] = e["total_cost_usd"]
    res["said"] = short(res["said"], 600); res["doing"] = short(res["doing"], 300)
    _activity[str(p)] = (m, res)
    return res


def board():
    return jlines(BOARD)


def logins():
    """[{name, path, weekly}] for each login in the config, with the starter's last usage reading."""
    c = jload(HOME / "core" / "config.json", {}) or {}
    names = {str(Path(k).expanduser()): v for k, v in (c.get("login_names") or {}).items()}
    used = jload(STATE / "usage.json", {}) or {}
    five = jload(STATE / "usage_5h.json", {}) or {}
    resets = jload(STATE / "usage_resets.json", {}) or {}
    off, lines = paused_logins(), login_limits()
    out = []
    for l in c.get("logins") or []:
        p = str(Path(l).expanduser())
        mine = lines.get(p) if isinstance(lines.get(p), dict) else {}
        lv, at = LIVE.get(p), used.get("_checked")
        if lv and lv["at"] > (at or 0):  # the dashboard's own reading (every 30 s at best) is newer than the starter's (every 5 min)
            used, five, resets, at = {**used, p: lv["weekly"]}, {**five, p: lv["five"]}, {**resets, p: lv["resets"]}, lv["at"]
        out.append({"name": names.get(p) or Path(p).name.removeprefix(".claude-").lstrip("."), "path": p, "weekly": used.get(p),
                    "five": five.get(p) if isinstance(five, dict) else None, "paused": p in off, "read_at": at, "gap": USAGE_GAP.get(p), "read_error": USAGE_ERR.get(p),
                    "resets": (resets.get(p) if isinstance(resets, dict) else None) or {}, "plan": plan_of(p),
                    # no 5-hour limit on this plan: the usage API's five_hour is null on a successful read (cc0 shows 0.0)
                    "no_five_hour": isinstance(resets, dict) and p in resets and (resets[p].get("has_five_hour") is False
                                    or ("has_five_hour" not in resets[p] and isinstance(five, dict) and five.get(p) is None)),
                    "stop": {"weekly": mine.get("weekly"), "five_hour": mine.get("five_hour"), "global": c.get("usage_stop_pct")}})
    return out


LIVE = {}  # {login path: {"at", "weekly", "five", "resets"}}: the dashboard's own usage readings, see live_usage()


USAGE_ERR = {}  # {login path: why the last read failed}
USAGE_GAP = {}  # {login path: seconds between reads}: live_usage()'s pace per login, shown on /api/usage


def live_usage(base=30, most=900):
    """Read the usage API for each login not paused, as often as the API allows (every `base` seconds at best), so the Usage page
    is live. Same call as the starter's usage(); the token is only sent to api.anthropic.com. On a 429 that login's gap
    doubles (or takes Retry-After, up to `most`); each good read after that takes 10% off again, so it settles at the
    fastest pace the API takes. Other errors (expired token, network) keep the last reading. Runs in its own thread."""
    import urllib.request, urllib.error
    due = {}  # {login path: next read time}
    while True:
        c = jload(HOME / "core" / "config.json", {}) or {}
        off = paused_logins()
        for l in c.get("logins") or []:
            p = str(Path(l).expanduser())
            if p in off or due.get(p, 0) > time.time(): continue
            gap = USAGE_GAP.get(p, base)
            try:
                tok = json.loads((Path(p) / ".credentials.json").read_text())["claudeAiOauth"]["accessToken"]
                req = urllib.request.Request("https://api.anthropic.com/api/oauth/usage", headers={
                    "Authorization": f"Bearer {tok}", "anthropic-beta": "oauth-2025-04-20"})
                r = json.load(urllib.request.urlopen(req, timeout=15))
                ep = lambda w: datetime.fromisoformat(w["resets_at"]).timestamp() if w and w.get("resets_at") else None
                w, f = r.get("seven_day") or {}, r.get("five_hour")
                LIVE[p] = {"at": time.time(), "weekly": w.get("utilization"), "five": (f or {}).get("utilization"),
                           "resets": {"weekly": ep(w), "five_hour": ep(f), "has_five_hour": f is not None}}
                gap = max(base, gap * 0.9); USAGE_ERR.pop(p, None)
            except urllib.error.HTTPError as e:
                if e.code == 429:
                    try: ra = float(e.headers.get("Retry-After") or 0)
                    except ValueError: ra = 0
                    gap = min(most, max(gap * 2, ra))
                else: gap = max(gap, 60)
                USAGE_ERR[p] = f"HTTP {e.code}"
            except Exception as e: gap, USAGE_ERR[p] = max(gap, 60), type(e).__name__
            USAGE_GAP[p], due[p] = gap, time.time() + gap
        time.sleep(1)


def plan_of(p):
    """A login's plan as a short label ("Pro", "Max 20x"), from the non-secret fields of its credentials file; None if unknown.
    Reads only subscriptionType and rateLimitTier, never the tokens."""
    try: o = json.loads((Path(p) / ".credentials.json").read_text())["claudeAiOauth"]
    except (OSError, ValueError, KeyError, TypeError): return None
    sub, tier = str(o.get("subscriptionType") or ""), str(o.get("rateLimitTier") or "")
    if not sub: return None
    m = re.search(r"_(\d+x)$", tier)
    if sub == "max" and m and m.group(1) == "20x": return "Ultra"  # the 20x Max plan is sold as Ultra now; the login still says max_20x
    return sub.capitalize() + (f" {m.group(1)}" if m and sub == "max" else "")


def login_limits():
    v = jload(STATE / "login_limits.json", {})
    return v if isinstance(v, dict) else {}


def set_limits(name, d):
    """Set or clear the stop lines of the login called `name` (the starter's stop_lines reads them every poll).
    d: {"weekly": N|None, "five_hour": N|None}, only the keys given; numbers are clamped to 0-100. True if the login exists."""
    hit = [l["path"] for l in logins() if l["name"] == name]
    if not hit: return False
    allv = login_limits(); mine = dict(allv.get(hit[0]) or {})
    for k in ("weekly", "five_hour"):
        if k not in d: continue
        if d[k] is None: mine.pop(k, None)
        else: mine[k] = min(100.0, max(0.0, float(d[k])))
    if mine: allv[hit[0]] = mine
    else: allv.pop(hit[0], None)
    t = STATE / f".login_limits.json.{os.getpid()}.{threading.get_ident()}"
    t.write_text(json.dumps(allv, indent=1)); t.replace(STATE / "login_limits.json")
    return True


def paused_logins():
    v = jload(STATE / "paused.json", [])
    return v if isinstance(v, list) else []


def set_paused(name, on):
    """Pause or resume the login called `name`: the only thing this program writes. The starter reads the file every poll.
    True if there's a login by that name."""
    hit = [l["path"] for l in logins() if l["name"] == name]
    if not hit: return False
    v = [x for x in paused_logins() if x != hit[0]] + ([hit[0]] if on else [])
    t = STATE / f".paused.json.{os.getpid()}.{threading.get_ident()}"
    t.write_text(json.dumps(v, indent=1)); t.replace(STATE / "paused.json")
    return True


# ---------- killing an agent's run (POST /api/kill) ----------
AGENT_CODE = re.compile(r"[a-z][0-9a-f]{4,}")  # every agent's name is a letter and hex digits, so this is also safe in a path
KILL_LOCK = threading.Lock()


def proc_info(pid):
    """(state, parent pid, start time) of a process, None if it's gone. The start time tells it from a later one with its pid."""
    try: f = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    except (OSError, IndexError): return None
    return f[0], int(f[1]), f[19]


def has_name(pid, name):
    """The process carries HIVE_NAME=name. The starter sets that on a run; the starter, dash and bridge don't have it, and
    another user's processes can't be read at all."""
    try: return f"HIVE_NAME={name}".encode() in Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
    except OSError: return False


def run_to_kill(name):
    """The record of `name`'s run if it really is a live run of that agent, else None. ~/run/<name>.json says which pid; the
    process itself has to agree, so a stale file or a reused pid can't aim this at anything else."""
    if not isinstance(name, str) or not AGENT_CODE.fullmatch(name): return None
    r = jload(RUN / f"{name}.json")
    if not isinstance(r, dict) or r.get("name") != name: return None
    pid = r.get("pid")
    if type(pid) is not int or pid <= 1 or pid == os.getpid(): return None
    i = proc_info(pid)
    if not i or i[0] in "ZX" or not has_name(pid, name): return None
    try: return r if Path(f"/proc/{pid}").stat().st_uid == os.getuid() else None
    except OSError: return None


def pids():
    return [int(d.name) for d in Path("/proc").iterdir() if d.name.isdigit()]


def tree_of(pid):
    """{pid: start time} of pid and its descendants. The tree and not the process group: the starter doesn't give a run its
    own group, so a run's group is the starter's, and killpg would take down the starter and every other agent."""
    kids, start = {}, {}
    for q in pids():
        if (i := proc_info(q)): kids.setdefault(i[1], []).append(q); start[q] = i[2]
    tree, todo = {}, [pid]
    while todo:
        q = todo.pop()
        if q in start and q != os.getpid(): tree[q] = start[q]; todo += kids.get(q, [])
    return tree


def signal_procs(procs, sig):
    """Signal those still alive, leaves first. Returns the ones that were; a zombie is already dead."""
    left = {}
    for q, t in reversed(list(procs.items())):
        i = proc_info(q)
        if i and i[2] == t and i[0] not in "ZX":
            left[q] = t
            if sig:
                try: os.kill(q, sig)
                except OSError: pass
    return left


def kill_run(r):
    """End one run: SIGTERM its process tree, SIGKILL what's left after 3 s. Marks it for the starter first, so it can't
    be resumed or woken from its queue however the two interleave. Jobs that left the tree (nohup, then the shell ended) keep
    running, as they do when the starter reaps a run; they're reported, not touched. Returns what was killed and what's left."""
    name, pid = r["name"], r["pid"]
    d = STATE / "killed"; d.mkdir(parents=True, exist_ok=True)
    t = d / f".{name}.{os.getpid()}"
    t.write_text(json.dumps({"pid": pid, "session": r.get("session"), "at": now_iso()})); t.replace(d / f"{name}.json")
    procs = tree_of(pid)
    signal_procs(procs, signal.SIGTERM)
    for _ in range(30):
        time.sleep(.1)
        if not signal_procs(procs, 0): break
    procs.update(tree_of(pid))  # anything it started while it was dying
    if signal_procs(procs, 0):
        signal_procs(procs, signal.SIGKILL); time.sleep(.3)
    stuck = sorted(signal_procs(procs, 0))
    detached = sorted(q for q in pids() if q not in procs and has_name(q, name) and (proc_info(q) or "Z")[0] not in "ZX")
    with LOG.open("a") as f:
        f.write(json.dumps({"at": now_iso(), "event": "killed", "name": name, "session": r.get("session"), "pid": pid,
                            "by": "person, from the dashboard", "procs": sorted(procs), "stuck": stuck, "detached": detached}) + "\n")
    text = f"The person killed `{name}` from the dashboard ({len(procs)} process{'es' if len(procs) != 1 else ''}). " \
           "The starter won't resume it and drops any wakes queued for it. A new post that mentions it starts it again."
    if stuck: text += f" These didn't die: {stuck}."
    if detached: text += f" Detached jobs it left running: {detached}."
    p = {"id": datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now_iso(), "from": "dash", "to": [],
         "thread": r.get("thread") or r.get("post") or "starter", "text": text}
    with BOARD.open("a") as f:
        fcntl.flock(f, fcntl.LOCK_EX); f.write(json.dumps(p) + "\n")
    return {"ok": not stuck, "name": name, "killed": len(procs), "stuck": stuck, "detached": detached}


def now_iso(): return datetime.now(timezone.utc).isoformat(timespec="seconds")


def peer_uid(addr):
    """Which user owns the other end of a loopback connection, from /proc/net/tcp; None if it can't be told. cloudflared runs
    as this account. Other users on this machine can reach this port but must not be able to kill anything through it."""
    want = "%s:%04X" % ("".join(f"{int(b):02X}" for b in reversed(addr[0].split("."))), addr[1])
    try:
        for ln in Path("/proc/net/tcp").read_text().splitlines()[1:]:
            f = ln.split()
            if f[1] == want: return int(f[7])
    except (OSError, IndexError, ValueError): pass
    return None


def heavy_jobs(t=None):
    """{agent: {"running": n, "queued": n, "waited": s of its longest wait}} from the `heavy` queue's own files: what a quiet agent is waiting on."""
    t, d, jobs = t or time.time(), STATE / "heavy", {}
    def job(name): return jobs.setdefault(name or "?", {"running": 0, "queued": 0, "waited": 0})
    for who in d.glob("*.who"):
        try:
            with open(d / who.stem, "a") as lock:  # the job holds this lock while it runs; a free lock is a leftover .who
                try: fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except OSError: job((who.read_text().split() or ["?"])[0])["running"] += 1
        except OSError: pass
    for q in [*d.glob("w.*"), *d.glob("q.*"), *d.glob("f.*")]:  # a waiter's ticket ends in its pid
        try:
            env = Path(f"/proc/{q.name.rsplit('.', 1)[-1]}/environ").read_bytes().split(b"\0")
            name = next((e[8:].decode() for e in env if e.startswith(b"HIVE_NAME=")), "?")
            j = job(name); j["queued"] += 1; j["waited"] = max(j["waited"], int(t - q.stat().st_mtime))
        except OSError: pass
    return jobs


def live():
    """The cheap part, polled by the web page: who is running and what the starter holds. No board parse."""
    agents, jobs = [], heavy_jobs()
    for f in sorted(RUN.glob("*.json")):
        r = jload(f, {}) or {}
        a = activity(r.get("out"))
        agents.append({
            "name": r.get("name", f.stem), "pid": r.get("pid"), "alive": alive(r.get("pid")),
            "since": ts(r.get("at")), "thread": r.get("thread"), "from": r.get("from"), "post": r.get("post"),
            "last": a["last"], "doing": a["doing"], "said": a["said"], "out": r.get("out"), "effort": run_effort(r.get("pid")),
            "jobs": jobs.get(r.get("name", f.stem)),
        })
    keep = {str(a["out"]) for a in agents}
    for k in [k for k in _activity if k not in keep]: del _activity[k]
    st = jload(STATE / "starter.json", {}) or {}
    pending = [{"name": n, "posts": len(v), "threads": sorted({x.get("thread") or "" for x in v}), "ids": [x.get("id") for x in v][-50:]}
               for n, v in (st.get("pending") or {}).items()]
    usage = {k: v for k, v in (jload(STATE / "usage.json", {}) or {}).items() if not k.startswith("_")}
    limit_until = st.get("limit_until")
    if limit_until and limit_until < time.time(): limit_until = None
    return {"now": time.time(), "agents": agents, "jobs": {"running": sum(j["running"] for j in jobs.values()), "queued": sum(j["queued"] for j in jobs.values())}, "pending": pending, "usage": usage, "logins": logins(),
            "limit_until": limit_until, "priorities": priorities()}


_prio = (None, {})


# kb/priorities.md names some work in words ("high-current supply"), not by thread: tier_aliases.json next to this file, if any, reads those.
TIER_ALIASES = jload(Path(__file__).resolve().parent / "tier_aliases.json", {}) or {}


def priorities():
    """Thread names for each tier in kb/priorities.md, as the (thread-name) lists its lines carry, plus the aliases."""
    global _prio
    try: m = PRIORITIES.stat().st_mtime
    except OSError: return {}
    if _prio[0] == m: return _prio[1]
    out, sect = {"tier1": [], "tier2": [], "held": [], "set": {}}, None
    for ln in PRIORITIES.read_text(errors="replace").splitlines():
        m = SET_LINE.match(ln)
        if m and m[2].lower() in SET_WORDS: out["set"][m[1]] = SET_WORDS[m[2].lower()]; continue
        if ln.startswith("**Tier 1"): sect = "tier1"
        elif ln.startswith("**Tier 2"): sect = "tier2"
        elif ln.startswith("**Held"): sect = "held"
        elif ln.startswith(("**", "#")): sect = None
        if not sect: continue
        for group in re.findall(r"\(([^)]*)\)", ln):
            out[sect] += [w for w in (x.strip() for x in group.split(",")) if re.fullmatch(r"[a-z0-9-]+\*?", w)]
    for k, v in TIER_ALIASES.items(): out[k] += v
    _prio = (m, out)
    return out


SET_HEAD = "## Set from the dashboard"
SET_NOTE = ("The person set these tiers on the dashboard. Each line names one thread exactly and beats anything above for that "
            "thread, held included. Only the person changes them: an agent doesn't edit this section.")
SET_LINE = re.compile(r"^- `([a-z0-9][a-z0-9._-]*)`: (tier 1|tier 2|held|not in the order)\b", re.I)
SET_WORDS = {"tier 1": "tier1", "tier 2": "tier2", "held": "held", "not in the order": "other"}
SAY = {"tier1": "Tier 1", "tier2": "Tier 2", "held": "Held", "other": "Not in the order"}
_tier_lock = threading.Lock()


def set_tier(thread, tier, who="the person"):
    """Write one thread's tier into the dashboard section of kb/priorities.md, or take it out (tier None).
    The rest of the file is left as it is. True on success."""
    if not re.fullmatch(r"[a-z0-9][a-z0-9._-]{0,80}", thread or "") or (tier is not None and tier not in SAY): return False
    with _tier_lock:
        try: lines = PRIORITIES.read_text(errors="replace").splitlines()
        except FileNotFoundError: lines = []
        except OSError: return False
        try: i = lines.index(SET_HEAD)
        except ValueError:
            while lines and not lines[-1].strip(): lines.pop()
            lines += ["", SET_HEAD, SET_NOTE, ""]; i = len(lines) - 3
        j = i + 1  # the section runs to the next heading
        while j < len(lines) and not lines[j].startswith("#"): j += 1
        body = [l for l in lines[i + 1:j] if not ((m := SET_LINE.match(l)) and m[1] == thread)]
        if tier is not None:
            at = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
            while body and not body[-1].strip(): body.pop()
            body.append(f"- `{thread}`: {SAY[tier]} (set by {who} on the dashboard, {at})")
            body.append("")
        out = lines[:i + 1] + body + lines[j:]
        PRIORITIES.parent.mkdir(parents=True, exist_ok=True)
        t = PRIORITIES.with_name(f".priorities.md.{os.getpid()}.{threading.get_ident()}")
        t.write_text("\n".join(out).rstrip("\n") + "\n"); t.replace(PRIORITIES)
    return True


def projects():
    res = []
    for d in sorted(PROJECTS.glob("*/")):
        head = ""
        for fn in ("goals.md", "README.md"):
            if (d / fn).exists():
                for ln in (d / fn).read_text(errors="replace").splitlines():
                    if ln.strip(): head = ln.strip("# ").strip(); break
                break
        commit, t = "", None
        try:
            o = subprocess.run(["git", "-C", str(d), "log", "-1", "--format=%ct %s"],
                               capture_output=True, text=True, timeout=5).stdout.strip()
            if o: t, commit = float(o.split(" ", 1)[0]), o.split(" ", 1)[1]
        except Exception: pass
        res.append({"name": d.name, "head": head, "commit": commit, "t": t})
    return res


def snapshot():
    s = live()
    posts = board()
    by_id = {p.get("id"): p for p in posts}
    agents = s["agents"]
    for a in agents: a["task"] = by_id.get(a["post"], {}).get("text", "")
    pending, usage, limit_until = s["pending"], s["usage"], s["limit_until"]
    logs = s["logins"]

    cut = time.time() - THREAD_HOURS * 3600
    threads = {}
    for p in posts:
        t = ts(p.get("at"))
        th = p.get("thread") or "(none)"
        d = threads.setdefault(th, {"thread": th, "n": 0, "recent": 0, "last": None, "who": set()})
        d["n"] += 1
        if t and t >= cut: d["recent"] += 1
        d["who"].add(p.get("from") or "?")
        if t and (d["last"] is None or t >= d["last"]["t"]):
            d["last"] = {"t": t, "from": p.get("from"), "text": p.get("text", "")}
    running_threads = {a["thread"] for a in agents}
    active = [dict(d, who=sorted(d["who"])) for d in threads.values()
              if d["recent"] or d["thread"] in running_threads]
    active.sort(key=lambda d: -(d["last"]["t"] if d["last"] else 0))

    ended = [e for e in jlines(LOG, 128 * 1024) if e.get("event") != "start"][-10:][::-1]

    jobs = heavy_jobs()
    return {"now": time.time(), "agents": agents, "jobs": {"running": sum(j["running"] for j in jobs.values()), "queued": sum(j["queued"] for j in jobs.values())}, "pending": pending, "usage": usage, "logins": logs,
            "limit_until": limit_until, "threads": active, "ended": ended, "projects": projects()}


# ---------- text ----------

def text(s, width=None):
    w = width or 110
    L = []
    use = ", ".join(f"{l['name']} " + ("?" if l["weekly"] is None else f"{l['weekly']:.0f}%") for l in s["logins"]) or "?"
    lim = f"  HELD for usage limit until {datetime.fromtimestamp(s['limit_until']).strftime('%H:%M')}" if s["limit_until"] else ""
    L.append(f"Hive dashboard  {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}   weekly usage: {use}{lim}")
    L.append("")
    L.append(f"AGENTS RUNNING ({len(s['agents'])})")
    if not s["agents"]: L.append("  none")
    for a in s["agents"]:
        st = "" if a["alive"] else "  [pid gone]"
        L.append(f"  @{a['name']:<8} {ago(a['since']):>5} up, active {ago(a['last'])} ago   thread {a['thread']}  (from {a['from']}){st}")
        L.append(f"      task:  {short(a['task'], w - 13)}")
        if a["doing"]: L.append(f"      doing: {short(a['doing'], w - 13)}")
        if a["said"]: L.append(f"      said:  {short(a['said'], w - 13)}")
    if s["pending"]:
        L.append("")
        L.append("WAITING TO START")
        for p in s["pending"]:
            L.append(f"  @{p['name']:<8} {p['posts']} post(s) in {', '.join(p['threads'])}")
    L.append("")
    L.append(f"THREADS ACTIVE (last {THREAD_HOURS}h, or with an agent running)")
    for d in s["threads"]:
        lp = d["last"] or {}
        L.append(f"  {d['thread']:<22} {d['recent']:>3} new/{d['n']:<3} {ago(lp.get('t')):>5} ago  {lp.get('from')}: {short(lp.get('text'), w - 50)}")
    L.append("")
    L.append("RECENTLY ENDED")
    for e in s["ended"]:
        L.append(f"  {ago(ts(e.get('at'))):>5} ago  {e.get('event'):<8} @{e.get('name')}  exit {e.get('exit')}")
    if s["projects"]:
        L.append("")
        L.append("PROJECTS")
        for p in s["projects"]:
            L.append(f"  {p['name']:<18} {short(p['head'], 40):<40}  {ago(p['t']):>5}  {short(p['commit'], w - 70)}")
    return "\n".join(L)


# ---------- cost, tokens, limits, from the session logs ----------

SECRET = re.compile(r"(gh[pousr]_|github_pat_|sk-ant-|xox[abp]-)[\w-]+|(Bearer\s+|token[=:]\s*)[\w.+/=-]{8,}", re.I)


def redact(s):
    return SECRET.sub("[hidden]", str(s or ""))


class Usage:
    """Keeps a running count by reading only what each session log has gained since the last look.

    A result line's total_cost_usd and modelUsage are cumulative for the session, across resumes, so one
    run's share is the rise since the last figure for that session. They are written at the end of a turn,
    so a turn still under way isn't counted yet. Costs are the logs' own, at API list prices.
    Limits and their history are kept per login: each session log's login comes from its start event in ~/log.jsonl."""

    def __init__(self):
        self.files, self.sess, self.limits, self.series, self.scanning = {}, {}, {}, {}, True
        self.owner, self.log_off = {}, 0  # session log's file name -> login path; how far ~/log.jsonl has been read
        self.lock = threading.Lock()

    def scan_file(self, p):
        name = p.stem
        f = self.files.get(name) or {"off": 0, "c": 0.0, "t": [0, 0, 0, 0], "m": {}, "h": {}, "tools": {}, "turns": 0, "ts": None}
        try:
            with open(p, "rb") as fh:
                fh.seek(f["off"])
                for ln in fh:
                    if not ln.endswith(b"\n"): break  # still being written
                    f["off"] += len(ln)
                    if ln.startswith(b'{"type":"system","subtype":"init"'):  # the harness says what it runs: model, version
                        try: e = json.loads(ln)
                        except ValueError: continue
                        f["init"] = {"model": e.get("model"), "cc": e.get("claude_code_version"), "perm": e.get("permissionMode"),
                                     "fast": e.get("fast_mode_state"), "tools": len(e.get("tools") or []), "mcp": len(e.get("mcp_servers") or [])}
                    elif ln.startswith(b'{"type":"assistant"'):
                        try: e = json.loads(ln)
                        except ValueError: continue
                        f["ts"] = ts(e.get("timestamp")) or f["ts"]
                        for c in e.get("message", {}).get("content") or []:
                            if c.get("type") == "tool_use":
                                f["tools"][c.get("name")] = f["tools"].get(c.get("name"), 0) + 1
                    elif b'"total_cost_usd"' in ln:
                        try: self.result(f, json.loads(ln), p)
                        except (ValueError, AttributeError, TypeError): continue
                    elif ln.startswith(b'{"type":"rate_limit_event"'):
                        try: self.limit(f, json.loads(ln), p)
                        except (ValueError, AttributeError, TypeError): continue
        except OSError:
            return
        self.files[name] = f

    def result(self, f, e, p):
        if e.get("type") != "result": return
        when = f["ts"] or p.stat().st_mtime
        s = self.sess.setdefault(e.get("session_id"), {"c": 0.0, "mu": {}})
        rise = max(0.0, (e.get("total_cost_usd") or 0) - s["c"])
        s["c"] = max(s["c"], e.get("total_cost_usd") or 0)
        f["c"] += rise; f["turns"] += 1
        hour = str(int(when // 3600 * 3600))
        f["h"][hour] = f["h"].get(hour, 0) + rise
        for m, u in (e.get("modelUsage") or {}).items():
            was = s["mu"].get(m, {})
            now = [u.get("inputTokens", 0), u.get("outputTokens", 0), u.get("cacheReadInputTokens", 0), u.get("cacheCreationInputTokens", 0)]
            old = was.get("t", [0, 0, 0, 0])
            for i in range(4): f["t"][i] += max(0, now[i] - old[i])
            f["m"][m] = f["m"].get(m, 0) + max(0.0, (u.get("costUSD") or 0) - was.get("c", 0))
            s["mu"][m] = {"t": now, "c": u.get("costUSD") or 0}

    def read_owners(self):
        """Learn each session log's login from the start events added to ~/log.jsonl since the last look."""
        try:
            with open(LOG, "rb") as fh:
                fh.seek(self.log_off)
                for ln in fh:
                    if not ln.endswith(b"\n"): break
                    self.log_off += len(ln)
                    if b'"event": "start"' not in ln: continue
                    try: e = json.loads(ln)
                    except ValueError: continue
                    if e.get("out") and e.get("login"): self.owner[Path(e["out"]).name] = e["login"]
        except OSError: pass

    def limit(self, f, e, p):
        info = e.get("rate_limit_info") or {}
        when = f["ts"] or p.stat().st_mtime
        win = info.get("unifiedWindows") or {}
        if not win: return
        login = self.owner.get(p.name) or str(HOME / ".claude")  # logs from before the start event named a login
        had = self.limits.get(login, {})
        if when >= had.get("at", 0):
            self.limits[login] = {**had, "at": when, **{k: {"u": v.get("utilization"), "r": v.get("resetsAt"), "st": v.get("status")} for k, v in win.items()}}
        key = int(when // 600)
        self.series.setdefault(login, {})[key] = [when, (win.get("five_hour") or {}).get("utilization"), (win.get("seven_day") or {}).get("utilization")]

    def run(self, every=3):
        """A daemon thread: it sleeps between files during the first pass, and afterwards opens only changed ones."""
        sizes = {}
        while True:
            self.read_owners()
            try: ps = sorted(((q.stat().st_mtime, q) for q in (HOME / "sessions").glob("*.jsonl")), key=lambda x: x[0])
            except OSError: ps = []
            for _, q in ps:
                try: n = q.stat().st_size
                except OSError: continue
                if sizes.get(q.name) == n: continue
                sizes[q.name] = n
                with self.lock: self.scan_file(q)
                if self.scanning: time.sleep(0.005)
            self.scanning = False
            time.sleep(every)

    def view(self):
        with self.lock:
            files = {k: {x: v.get(x) for x in ("c", "t", "m", "h", "tools", "turns", "init")} for k, v in self.files.items() if v["turns"] or v["tools"] or v.get("init")}
            cfg = jload(HOME / "core" / "config.json", {}) or {}
            by_login = [{**l, "limits": self.limits.get(l["path"], {}),
                         "series": [v for _, v in sorted(self.series.get(l["path"], {}).items())]} for l in logins()]
            return {"files": files, "logins": by_login,
                    "scanning": self.scanning, "stop_pct": cfg.get("usage_stop_pct"), "providers": dict(PROVIDERS)}


def agent_feed(name, limit=70):
    """What an agent has been doing: tool calls and what it said, from the end of its newest session log."""
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,40}", name or ""): return []
    try: ps = sorted((HOME / "sessions").glob(name + "-*.jsonl"), key=lambda q: q.stat().st_mtime)[-2:]
    except OSError: return []
    out = []
    for q in ps:
        for e in jlines(q, 384 * 1024):
            if e.get("type") != "assistant": continue
            t = ts(e.get("timestamp"))
            for c in e.get("message", {}).get("content") or []:
                if c.get("type") == "tool_use":
                    out.append({"t": t, "k": "tool", "tool": c.get("name"), "x": redact(short(describe_tool(c.get("name"), c.get("input")), 400))})
                elif c.get("type") == "text" and c.get("text", "").strip():
                    out.append({"t": t, "k": "said", "x": redact(c["text"][:1500])})
    return out[-limit:]


# ---------- web ----------

PROVIDERS = {}  # providers.status(): OpenRouter spend and Codex limits, refreshed by provider_status() (no keys in it)


def provider_status(every=20, once=False):
    """Every 20 s, providers.status() (it calls OpenRouter's key check and `codex app-server` at most once a minute and
    caches in ~/.hive/providers.json), off the request path since a fresh check blocks a second or more."""
    global PROVIDERS
    import importlib.util
    while True:
        try:
            spec = importlib.util.spec_from_file_location("h2_providers", HOME / "modules" / "providers" / "providers.py")
            m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
            new = m.status(jload(HOME / "core" / "config.json", {}) or {}, max_age=0 if once else 60)
        except Exception as e:
            new = {**PROVIDERS, "_error": f"providers check failed ({type(e).__name__})"}
        PROVIDERS = new
        if once: return
        time.sleep(every)


def set_provider_line(name, k, v):
    """The person's stop line for a provider, in ~/.hive/provider_limits.json (providers.lines() lays it over config.json):
    stop_pct (Codex, 0-100) or monthly_usd (OpenRouter, 0-1000); None clears it back to config.json's. Then re-reads
    the provider so the block shows it. True if it was a known provider and a valid line."""
    c = jload(HOME / "core" / "config.json", {}) or {}
    pc = (c.get("providers") or {}).get(name)
    want = "stop_pct" if (pc or {}).get("harness") == "codex" else "monthly_usd"
    if not pc or k != want or not (v is None or (isinstance(v, (int, float)) and not isinstance(v, bool) and 0 <= v <= (100 if k == "stop_pct" else 1000))):
        return False
    f = STATE / "provider_limits.json"
    d = jload(f, {}) or {}
    if not isinstance(d, dict): d = {}
    mine = {**(d.get(name) or {}), k: v}
    d[name] = {x: y for x, y in mine.items() if y is not None}
    f.write_text(json.dumps(d, indent=1))
    threading.Thread(target=provider_status, kwargs={"once": True}, daemon=True).start()
    return True


class Cached:
    """At most one computation per `ttl` seconds, however many tabs are open."""
    def __init__(self, fn, ttl): self.fn, self.ttl, self.t, self.v, self.lock = fn, ttl, 0, None, threading.Lock()
    def get(self):
        with self.lock:
            if time.time() - self.t > self.ttl:
                self.v, self.t = self.fn(), time.time()
            return self.v


# Defaults of ~/.local/bin/heavy's start gates; test_jobs.py checks they still match the script.
HEAVY_MIN_MEM_MB, HEAVY_JOB_MB, HEAVY_MAX_STALL, HEAVY_LOAD_PER_CPU = 5000, 3000, 20, 3
JOB_ROW_CORES, JOB_ROW_MB = 0.25, 200  # below both, a group outside the queue isn't worth a row
TICK = os.sysconf("SC_CLK_TCK")


def read_proc():
    """{pid: fields} for every process, from /proc/<pid>/stat and status. mem is private memory, in RAM or swap, so
    summing it over processes doesn't count shared libraries many times; ticks is CPU used since the process started."""
    out = {}
    for e in os.scandir("/proc"):
        if not e.name.isdigit(): continue
        try:
            st = Path(e.path, "stat").read_bytes().decode(errors="replace")
            comm, f = st[st.index("(") + 1:st.rindex(")")], st[st.rindex(")") + 2:].split()
            mem, uid = 0, None
            for ln in Path(e.path, "status").read_text(errors="replace").splitlines():
                if ln.startswith("Uid:"): uid = int(ln.split()[1])
                elif ln.startswith(("RssAnon:", "VmSwap:")): mem += int(ln.split()[1])
            out[int(e.name)] = {"comm": comm, "ppid": int(f[1]), "ticks": int(f[11]) + int(f[12]), "start": f[19], "uid": uid, "mem": mem / 1024}
        except (OSError, ValueError, IndexError): pass  # it ended while we read it
    return out


def proc_cmd(pid):
    try: return [a.decode(errors="replace") for a in Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0") if a]
    except OSError: return []


def proc_agent(pid):
    """The HIVE_NAME in a process's environment; None if it has none or isn't ours to read."""
    try: env = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
    except OSError: return None
    return next((e[8:].decode(errors="replace") for e in env if e.startswith(b"HIVE_NAME=")), None)


def nvidia():
    if not shutil.which("nvidia-smi"): return []
    try:
        r = subprocess.run(["nvidia-smi", "--query-gpu=name,utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits"],
                           capture_output=True, text=True, timeout=3)
        rows = [[c.strip() for c in ln.split(",")] for ln in r.stdout.splitlines()]
        return [{"name": a, "util": int(b), "mem": int(c), "total": int(d)} for a, b, c, d in rows]
    except (OSError, ValueError, subprocess.SubprocessError): return []


gpu_c = Cached(nvidia, 15)


class Jobs:
    """What /api/jobs serves: heavy's running and queued jobs, what each uses, and the machine's resources.
    Reads /proc and heavy's files only; computed only while a tab has the Jobs view open."""
    def __init__(self): self.prev_t, self.prev_cpu, self.prev_ticks, self.names = 0, None, {}, {}

    def agent(self, pid, p):
        k = (pid, p["start"])
        if k not in self.names: self.names[k] = proc_agent(pid)
        return self.names[k]

    def view(self):
        t, d, me = time.time(), STATE / "heavy", os.getuid()
        procs, dt = read_proc(), t - self.prev_t
        ticks = {(pid, p["start"]): p["ticks"] for pid, p in procs.items()}
        for pid, p in procs.items():  # first look: 0, not its whole life's CPU
            was = self.prev_ticks.get((pid, p["start"]))
            p["cores"] = max(0, p["ticks"] - was) / TICK / dt if was is not None and dt > 0 else 0
        self.prev_ticks, self.prev_t = ticks, t
        self.names = {k: v for k, v in self.names.items() if k in ticks}
        kids = {}
        for pid, p in procs.items(): kids.setdefault(p["ppid"], []).append(pid)
        def tree(pid):
            out, todo = [], [pid]
            while todo:
                x = todo.pop()
                if x in procs: out.append(x); todo += kids.get(x, [])
            return out
        def top_cmd(pids):
            big = max(pids, key=lambda x: (procs[x]["cores"] + procs[x]["mem"] / 1024, x))
            return redact(" ".join(proc_cmd(big)) or procs[big]["comm"])[:200]

        # Running: the heavy shell whose fd 9 is that lane's lock file and that has no ticket left in the queue. /proc/locks
        # can't say: the lock is taken by the flock binary, which exits at once, so it names a dead pid.
        waiting = {q.name.rsplit(".", 1)[-1] for q in [*d.glob("w.*"), *d.glob("q.*"), *d.glob("f.*")]}
        holders = {}
        for pid, p in procs.items():
            if p["uid"] != me or str(pid) in waiting or not p["comm"].startswith(("sh", "dash", "bash", "heavy")): continue
            if not any(a.endswith("/heavy") for a in proc_cmd(pid)[:2]): continue
            try: holders[os.readlink(f"/proc/{pid}/fd/9")] = pid
            except OSError: pass
        running, in_jobs = [], set()
        for who in d.glob("*.who"):
            holder = holders.get(str(d / who.stem))
            if holder is None: continue  # a leftover .who
            try: f = who.read_text(errors="replace").split(None, 2)
            except OSError: continue
            pids = tree(holder); in_jobs.update(pids)
            try: since = datetime.fromisoformat(f[1]).timestamp()
            except (ValueError, IndexError): since = who.stat().st_mtime
            running.append({"lane": who.stem, "agent": f[0] if f else "?", "since": since, "cmd": redact(f[2].strip() if len(f) > 2 else ""),
                            "cores": round(sum(procs[x]["cores"] for x in pids), 2), "mem": round(sum(procs[x]["mem"] for x in pids)), "procs": len(pids)})
        running.sort(key=lambda r: r["since"])

        # Queued, in the order they will start: the first lane skips the line; then the oldest ticket.
        queued = []
        for q in sorted(d.glob("f.*")) + sorted([*d.glob("w.*"), *d.glob("q.*")]):
            try:
                pid, at = int(q.name.rsplit(".", 1)[-1]), q.stat().st_mtime
            except (OSError, ValueError): continue
            args = proc_cmd(pid)
            hv = next((i for i, a in enumerate(args[:3]) if a.endswith("/heavy") or a == "heavy"), None)
            if pid not in procs or hv is None: continue  # dead waiter, or its pid was reused
            cmd = args[hv + 1:]
            if cmd[:1] == ["--first"]: cmd = cmd[1:]
            queued.append({"agent": self.agent(pid, procs[pid]) or "?", "since": at, "cmd": redact(" ".join(cmd))[:200], "first": q.name.startswith("f.")})

        # Everything else, by who owns it: an agent's own process, its work outside the queue, the rest.
        own = set()
        for f in RUN.glob("*.json"):
            try: own.add(int(json.loads(f.read_text()).get("pid") or 0))
            except (OSError, ValueError, AttributeError): pass
        groups = {}
        split = {k: {"cores": 0, "mem": 0} for k in ("jobs", "outside", "agents", "other")}
        for pid, p in procs.items():
            name = None
            if pid in in_jobs: kind = "jobs"
            elif pid in own: kind = "agents"
            else:
                name = self.agent(pid, p) if p["uid"] == me else None
                kind = "outside" if name else "other"
                if kind == "other":
                    try: name = "user " + pwd.getpwuid(p["uid"]).pw_name
                    except (KeyError, TypeError): name = "user ?"
            split[kind]["cores"] += p["cores"]; split[kind]["mem"] += p["mem"]
            if name: groups.setdefault((kind, name), []).append(pid)
        outside = []
        for (kind, name), pids in groups.items():
            c, m = sum(procs[x]["cores"] for x in pids), sum(procs[x]["mem"] for x in pids)
            if c >= JOB_ROW_CORES or m >= JOB_ROW_MB:
                outside.append({"who": name, "agent": kind == "outside", "cores": round(c, 2), "mem": round(m), "procs": len(pids), "cmd": top_cmd(pids)})
        outside.sort(key=lambda r: (r["agent"], r["cores"] + r["mem"] / 1024), reverse=True)

        # The machine.
        stat = {ln.split()[0]: ln.split()[1:] for ln in Path("/proc/stat").read_text().splitlines() if ln.startswith(("cpu ", "procs_"))}
        cpu = [int(x) for x in stat["cpu"]]
        busy = {"user": 0, "system": 0, "iowait": 0, "idle": 100}
        if self.prev_cpu:
            a = [x - y for x, y in zip(cpu, self.prev_cpu)]; tot = sum(a[:8]) or 1
            busy = {"user": round((a[0] + a[1]) * 100 / tot), "system": round((a[2] + a[5] + a[6]) * 100 / tot), "iowait": round(a[4] * 100 / tot), "idle": round(a[3] * 100 / tot)}
        self.prev_cpu = cpu
        mem = {ln.split(":")[0]: int(ln.split()[1]) // 1024 for ln in Path("/proc/meminfo").read_text().splitlines() if ln.startswith(("MemTotal", "MemAvailable", "SwapTotal", "SwapFree"))}
        psi = {}
        for k in ("cpu", "io", "memory"):
            try:
                for ln in Path(f"/proc/pressure/{k}").read_text().splitlines():
                    f = ln.split()
                    psi[f"{k}_{f[0]}"] = float(f[2].split("=")[1])
            except (OSError, IndexError, ValueError): pass
        cpus, load = os.cpu_count() or 1, float(Path("/proc/loadavg").read_text().split()[0])
        disks = []
        for label, path in (("home", HOME), ("/tmp", Path("/tmp"))):
            try: u = shutil.disk_usage(path); disks.append({"name": label, "used": u.used // 2**30, "total": u.total // 2**30})
            except OSError: pass
        cg, slice_used, slice_high = Path(f"/sys/fs/cgroup/user.slice/user-{me}.slice/user@{me}.service/app.slice/app-h2jobs.slice"), None, None
        try:
            slice_used = sum(int(ln.split()[1]) for ln in (cg / "memory.stat").read_text().splitlines() if ln.startswith(("anon ", "shmem "))) // 2**20
            slice_high = int((cg / "memory.high").read_text()) // 2**20
        except (OSError, ValueError): pass
        held = mem.get("MemTotal", 0) - mem.get("MemAvailable", 0) + mem.get("SwapTotal", 0) - mem.get("SwapFree", 0)
        for k, v in split.items(): v["cores"] = round(v["cores"], 2); v["mem"] = round(v["mem"])

        # heavy's gates for the next start, in its own order. They only hold a job back while some slot is busy.
        gates = [{"what": "memory available", "now": mem.get("MemAvailable", 0), "need": f"at least {HEAVY_MIN_MEM_MB} MB", "unit": " MB", "ok": mem.get("MemAvailable", 0) >= HEAVY_MIN_MEM_MB}]
        if slice_used is not None and slice_high:
            gates.append({"what": "queue jobs' memory", "now": slice_used, "need": f"at most {slice_high - HEAVY_JOB_MB} MB", "unit": " MB", "ok": slice_used + HEAVY_JOB_MB <= slice_high})
        if "memory_full" in psi:  # avg60, the figure heavy reads
            gates.append({"what": "memory stall", "now": int(psi["memory_full"]), "need": f"under {HEAVY_MAX_STALL}%", "unit": "%", "ok": int(psi["memory_full"]) < HEAVY_MAX_STALL})
        gates.append({"what": "load", "now": int(load), "need": f"under {cpus * HEAVY_LOAD_PER_CPU}", "unit": "", "ok": int(load) < cpus * HEAVY_LOAD_PER_CPU})

        done = []
        try:
            with open(d / "jobs.tsv", "rb") as f:
                f.seek(0, 2); f.seek(max(0, f.tell() - 8192))
                for ln in f.read().decode(errors="replace").splitlines()[-10:][::-1]:
                    c = ln.split("\t")
                    if len(c) >= 7: done.append({"end": datetime.fromisoformat(c[0]).timestamp(), "agent": c[1], "lane": c[2], "waited": int(c[3]), "ran": int(c[4]), "rc": int(c[5]), "cmd": redact(c[6])})
        except (OSError, ValueError): pass
        return {"at": t, "running": running, "queued": queued, "outside": outside, "done": done, "gates": gates, "split": split,
                "slots": cpus, "held": held,
                "machine": {"cpus": cpus, "load": load, "busy": busy, "blocked": int(stat.get("procs_blocked", [0])[0]), "mem": mem, "psi": psi,
                          "disks": disks, "slice_used": slice_used, "slice_high": slice_high, "gpus": gpu_c.get()}}


def tail_lines(path, offset):
    """Whole lines appended since byte `offset`, unparsed: the browser parses them, and the file is only ever read."""
    try:
        with open(path, "rb") as f:
            size = f.seek(0, 2)
            if offset > size: offset = 0
            f.seek(offset)
            data = f.read()
    except OSError:
        return offset, []
    cut = data.rfind(b"\n") + 1  # a line still being appended waits for the next poll
    return offset + cut, [ln.decode("utf-8", "replace") for ln in data[:cut].splitlines() if ln.strip()]


# ---------- who may do what ----------
# One app, one list of capabilities, and every request is checked here on the server. The page only hides what's refused.
# No ~/.hive/web.json: the open loopback dash this has always been, with the switches it has always had (ROUTE_OPEN).
# With one: full rights rest only on the secret (local route); anything that looks forwarded is the outside route, which needs
# a signed Cloudflare Access token and gets only outside.can, read-only by default. A forwarding header can only ever lower
# what a request may do, and nobody gets the local route without the secret, so spoofing headers gains nothing.
CAPS = ("read", "post", "stop", "tier", "limits", "logins", "config", "modules", "plans", "secrets")
# outside.people {email: "limited" or "full"} in web.json: what each signed-in person may do from outside. Not listed: refused.
LIMITED_CAN = ("read", "post", "stop")
FULL_FOR_S = 3600  # full rights need an Access token issued this recently; after that the same session is limited until they sign in again
OPEN_CAN = ("read", "stop", "tier", "limits")
WEB = STATE / "web.json"
INSTALLED = HOME / ".hive-installed"
FORWARDED = ("cf-ray", "cf-connecting-ip", "cf-access-jwt-assertion", "cf-ipcountry", "cf-visitor", "cdn-loop", "x-forwarded-for",
             "x-forwarded-host", "x-forwarded-proto", "x-real-ip", "forwarded", "via", "true-client-ip")
GET_CAP = {"/api/live": "read", "/api/board": "read", "/api/log": "read", "/api/usage": "read", "/api/jobs": "read", "/api/agent": "read",
           "/api/projects": "read", "/json": "read", "/api/target": "read", "/api/inbox": "read", "/api/delivered": "read",
           "/api/config": "config", "/api/modules": "modules", "/api/signin": "logins", "/api/plans": "plans", "/api/secrets": "secrets"}
POST_CAP = {"/api/kill": "stop", "/api/tier": "tier", "/api/login-pause": "limits", "/api/login-limit": "limits", "/api/provider-limit": "limits",
            "/api/post": "post", "/api/upload": "post", "/api/inbox-read": "post", "/api/config": "config", "/api/starter": "config",
            "/api/module": "modules", "/api/signin": "logins", "/api/signin/code": "logins", "/api/signin/cancel": "logins", "/api/plan": "plans", "/api/secret": "secrets"}
LOCAL_ONLY = {"/api/access"}  # who may do what is never changed from outside, whatever outside.can says


def web_cfg():
    """~/.hive/web.json, or None if there isn't one and this isn't an installed Hive (Hive's open dash). A file that can't be read,
    or one missing where install has run (it writes the marker last, after making the file), is {"broken": True}, which
    refuses everything: a typo or a deleted file must not fall back to the open dash."""
    try: v = json.loads(WEB.read_text())
    except FileNotFoundError: return {"broken": True} if INSTALLED.exists() else None
    except (OSError, ValueError): return {"broken": True}
    return v if isinstance(v, dict) and v.get("secret") else {"broken": True}


def session_value(web):
    """The cookie: derived from the secret, so it survives a restart and a new secret ends every session."""
    return hmac.new(str(web["secret"]).encode(), b"session", hashlib.sha256).hexdigest()


def cookie_name(port): return f"app_session_{port}"  # cookies ignore ports, so two installs on one machine mustn't share a name


def has_session(cookies, web, port):
    want = f"{cookie_name(port)}={session_value(web)}"
    return any(hmac.compare_digest(c.strip().encode(), want.encode()) for c in (cookies or "").split(";"))


def route_of(headers, peer, port, web):
    """"local", "outside" or "refused". headers: lowercased names. Host is checked for DNS rebinding: a page on evil.example
    that rebinds to 127.0.0.1 still sends its own Host. Loopback alone proves nothing, since a tunnel connects over loopback."""
    host = (headers.get("host") or "").lower()
    outside = {str(h).lower() for h in (web.get("outside") or {}).get("hosts") or []}
    if host not in {f"127.0.0.1:{port}", f"localhost:{port}", f"[::1]:{port}"} | outside: return "refused"
    if host in outside or peer not in ("127.0.0.1", "::1") or any(h in headers for h in FORWARDED): return "outside"
    return "local"


def same_origin(headers, page_nav=False):
    """Origin, if sent, must be this Host. Sec-Fetch-Site, if sent, must be same-origin, or none for a typed address.
    page_nav: a link from elsewhere may open the page itself, which does nothing by being opened."""
    o = headers.get("origin")
    if o is not None:
        p = urlsplit(o)
        if p.scheme not in ("http", "https") or p.netloc.lower() != (headers.get("host") or "").lower(): return False
    return page_nav or headers.get("sec-fetch-site", "same-origin") in ("same-origin", "none")


def b64u(s): return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def rs256_ok(n, e, signed, sig):
    """RSASSA-PKCS1-v1_5 with SHA-256, in the standard library: the key is public, so timing gives nothing away."""
    k = (n.bit_length() + 7) // 8
    if len(sig) != k: return False
    digest_info = bytes.fromhex("3031300d060960864801650304020105000420") + hashlib.sha256(signed).digest()
    want = b"\0\1" + b"\xff" * (k - 3 - len(digest_info)) + b"\0" + digest_info
    return pow(int.from_bytes(sig, "big"), e, n).to_bytes(k, "big") == want


class AccessKeys:
    """Cloudflare Access's public keys, from https://<team>/cdn-cgi/access/certs. Read hourly, or when a token names an unknown
    key, at most once a minute. If they can't be read, tokens signed with an unknown key are refused."""
    def __init__(self): self.keys, self.url, self.t, self.lock = {}, None, 0, threading.Lock()

    def get(self, url, kid):
        import urllib.request
        with self.lock:
            age = time.time() - self.t
            if url != self.url or age > 3600 or (kid not in self.keys and age > 60):
                self.t = time.time()
                try:
                    with urllib.request.urlopen(url, timeout=10) as r: got = json.load(r)["keys"]
                    self.keys = {k["kid"]: (int.from_bytes(b64u(k["n"]), "big"), int.from_bytes(b64u(k["e"]), "big"))
                                 for k in got if k.get("kty") == "RSA" and k.get("kid")}
                    self.url = url
                except Exception as e:
                    print(f"can't read the Access keys: {type(e).__name__}", file=sys.stderr, flush=True)
                    if url != self.url: self.keys = {}
            return self.keys.get(kid) if url == self.url else None


ACCESS_KEYS = AccessKeys()


def verify_access(token, access, keys=ACCESS_KEYS):
    """The token's claims if Cf-Access-Jwt-Assertion is good for this install: signed by the team's key, for our audience, in
    date, and from a listed email if any are listed. None otherwise. access: {"team": "x.cloudflareaccess.com", "aud": "...", "emails": [...]}."""
    team, aud = access.get("team"), access.get("aud")
    if not token or not team or not aud: return None
    try:
        head64, body64, sig64 = token.split(".")
        head, claims = json.loads(b64u(head64)), json.loads(b64u(body64))
        if head.get("alg") != "RS256" or not isinstance(claims, dict): return None  # the alg is ours to pick, never the token's
        key = keys.get(access.get("certs_url") or f"https://{team}/cdn-cgi/access/certs", head.get("kid"))
        if not key or not rs256_ok(key[0], key[1], f"{head64}.{body64}".encode(), b64u(sig64)): return None
        now_t, want_aud = time.time(), claims.get("aud")
        if claims.get("iss") != f"https://{team}" or aud not in (want_aud if isinstance(want_aud, list) else [want_aud]): return None
        if not isinstance(claims.get("exp"), (int, float)) or claims["exp"] <= now_t or claims.get("nbf", 0) > now_t + 60: return None
        emails = access.get("emails")
        if emails and str(claims.get("email", "")).lower() not in {str(x).lower() for x in emails}: return None
        return claims
    except (ValueError, TypeError, AttributeError, KeyError):
        return None


def signer_of(claims, out):
    """Who a verified token belongs to, for the label on their posts: outside.names {email: name} if listed, else the email. Only ever from the signed claims."""
    email = str(claims.get("email") or "").strip().lower()
    names = {str(k).lower(): str(v) for k, v in (out.get("names") or {}).items()} if isinstance(out.get("names"), dict) else {}
    return re.sub(r"[^\w .@+-]", "", names.get(email, email))[:60] or "unknown"


def person_can(claims, out, now=None):
    """(can, mode, seconds of full left) for a verified token, or None if this person isn't let in. Without outside.people: outside.can, for anyone Access let through."""
    if "people" not in out: return tuple(c for c in out.get("can", ["read"]) if c in CAPS), None, 0
    people = out["people"] if isinstance(out["people"], dict) else {}
    email = str(claims.get("email") or "").strip().lower()
    mode = {str(k).strip().lower(): v for k, v in people.items()}.get(email) if email else None
    if mode == "limited": return LIMITED_CAN, "limited", 0
    if mode != "full": return None
    iat = claims.get("iat")
    left = iat + FULL_FOR_S - (now or time.time()) if isinstance(iat, (int, float)) and not isinstance(iat, bool) else 0
    if 0 < left <= FULL_FOR_S + 60: return CAPS, "full", int(left)  # a token dated well ahead is no proof of a recent sign-in
    return LIMITED_CAN, "expired", 0


def set_access(d):
    """The outside profile in web.json, from the local route only. Fields not given stay. The secret is never touched."""
    web = web_cfg()
    if not web or web.get("broken"): return False
    out = dict(web.get("outside") or {})
    if "can" in d:
        if not isinstance(d["can"], list) or any(c not in CAPS for c in d["can"]): return False
        out["can"] = d["can"]
    if "hosts" in d:
        if not isinstance(d["hosts"], list) or not all(isinstance(h, str) and re.fullmatch(r"[A-Za-z0-9.-]+(:\d+)?", h) for h in d["hosts"]): return False
        out["hosts"] = d["hosts"]
    if "names" in d:
        if not isinstance(d["names"], dict) or not all(isinstance(k, str) and isinstance(v, str) for k, v in d["names"].items()): return False
        out["names"] = d["names"]
    if "people" in d:
        if not isinstance(d["people"], dict) or any(not isinstance(k, str) or "@" not in k or v not in ("limited", "full") for k, v in d["people"].items()): return False
        out["people"] = {k.strip().lower(): v for k, v in d["people"].items()}
    if "access" in d:
        a = d["access"]
        if not isinstance(a, dict) or any(not isinstance(a.get(k, ""), str) for k in ("team", "aud")): return False
        out["access"] = {k: v for k, v in {"team": a.get("team", "").strip(), "aud": a.get("aud", "").strip(),
                                           "emails": [str(e).strip() for e in a.get("emails") or [] if str(e).strip()]}.items() if v}
    write_private(WEB, {**web, "outside": out})
    return True


def write_private(path, value):
    t = path.with_name(f".{path.name}.{os.getpid()}.{threading.get_ident()}")
    fd = os.open(t, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f: f.write(json.dumps(value, indent=1) + "\n")
    t.replace(path)


# ---------- chat: posting as the person, and what's addressed to them ----------
HIVE_BIN = HOME / "core" / "hive"
PERSON = "person"
MAX_FILE = 100 * 2**20  # as hive post's own cap
INCOMING = HOME_FILES / "incoming"
THREAD_NAME = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.:-]{0,100}")
UPLOAD_REF = re.compile(r"([0-9a-f]{16})/([A-Za-z0-9_][A-Za-z0-9_.-]*)")
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
MENTION_CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)  # a name in backticks is being written about, not called
SPAWNERS = {"new", "claude", "codex", "free", "warden"}


def child_env():
    return {**{k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_DOOR")}, "HOME": str(HOME)}


def cfg():
    v = jload(HOME / "core" / "config.json", {})
    return v if isinstance(v, dict) else {}


class BoardIndex:
    """What chat needs from the board, read only as far as it has grown: the last agent on each thread, each post's author, posts
    for the person. inbox.py decides what's addressed to whom, so this page and the door agree."""
    def __init__(self):
        self.off, self.last, self.authors, self.mine, self.answered, self.lock = 0, {}, {}, [], {}, threading.Lock()
        self.names = None

    def targets(self, p):
        if self.names is None:
            try:
                spec = importlib.util.spec_from_file_location("hive_inbox", HOME / "core" / "inbox.py")
                m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); self.names = m.names
            except Exception:
                self.names = lambda q: {str(t).lstrip("@").lower() for t in q.get("to") or []}
        return self.names(p)

    def refresh(self):
        with self.lock:
            self.off, lines = tail_lines(BOARD, self.off)
            for ln in lines:
                try: p = json.loads(ln)
                except ValueError: continue
                if not isinstance(p, dict): continue
                who, th = str(p.get("from", "")), p.get("thread") or p.get("id")
                self.authors[p.get("id")] = who
                if AGENT_CODE.fullmatch(who.lower()): self.last[th] = who.lower()
                if who == PERSON: self.answered[th] = p.get("at", "")
                elif PERSON in self.targets(p):
                    self.mine.append({"id": p.get("id"), "at": p.get("at"), "from": who, "thread": th,
                                      "text": str(p.get("text", ""))[:600], "files": len(p.get("files") or [])})


INDEX = BoardIndex()


def known_agents():
    """Who a mention can call, as starter.known_agents has it: the words that start one, and anyone who has run."""
    found = set(SPAWNERS)
    for e in jlines(LOG):
        if e.get("event") == "start" and e.get("name"): found.add(str(e["name"]).lower())
    return found - set(cfg().get("not_agents") or [])


def calls_someone(text):
    """Does the text itself call an agent? @person, @all or a word that names nobody wakes no one, so the app still adds a mention."""
    said = {m.lower() for m in MENTION.findall(MENTION_CODE.sub(" ", text))} - set(cfg().get("external") or []) - {"all", "here"}
    return bool(said & known_agents()) if said else False


def thread_target(thread):
    """Who a post in this thread would reach: {"who": name or None, "running": bool}."""
    INDEX.refresh()
    who = INDEX.last.get(thread)
    return {"who": who, "running": bool(who and alive((jload(RUN / f"{who}.json") or {}).get("pid")))}


def person_post(text, thread=None, reply_to=None, uploads=(), signer=None):
    """A post as the person. As the Slack mirror does it: a plain message wakes the agent last on the thread, a reply wakes the author
    of the post replied to, and no thread (or a thread with no agent yet) starts a new agent. Mentions in the text win.
    signer: who signed in, from a verified Access token, when more than one person can: it heads the text, so an agent and the board can see who wrote it.
    Returns {"id", "thread", "said"}, or raises ValueError with a sentence for the page."""
    text = str(text or "").strip()
    refs = [UPLOAD_REF.fullmatch(str(u)) for u in uploads or []]
    if not all(refs): raise ValueError("an attachment wasn't recognised")
    paths = [INCOMING / m[1] / m[2] for m in refs]
    if not all(p.is_file() and not p.is_symlink() for p in paths): raise ValueError("an attachment is gone; attach it again")
    if thread and not THREAD_NAME.fullmatch(str(thread)): raise ValueError("a thread name is letters, digits and . _ : -")
    if not text and not paths: raise ValueError("nothing to send")
    if not text: text = "Attached: " + ", ".join(p.name for p in paths)
    if signer: text = f"(from {signer}, web app) {text}"
    INDEX.refresh()
    who = None
    if reply_to:
        reply_to = str(reply_to)
        if not re.fullmatch(r"[0-9a-f]{6,20}", reply_to) or reply_to not in INDEX.authors: raise ValueError("the post replied to isn't on the board")
        author = INDEX.authors[reply_to].lower()
        who = author if AGENT_CODE.fullmatch(author) else None
        text = f"reply to message {reply_to}: {text}"
    routed = False
    if not calls_someone(text):
        who = who or (INDEX.last.get(thread) if thread else None)
        text = f"@{who or 'new'} {text}"
        routed = bool(who)  # we picked the name, so a dead agent's heir may take it; only a typed name brings a dead agent back
    cmd = [str(HIVE_BIN), "post", "--as", PERSON] + (["--thread", str(thread)] if thread else []) + (["--to", who] if who else []) + (["--routed"] if routed else []) + (["--reply-to", reply_to] if reply_to else [])
    for p in paths: cmd += ["--file", str(p)]
    r = subprocess.run(cmd, input=text, text=True, capture_output=True, env=child_env(), cwd=str(HOME), timeout=60)
    if r.returncode: raise ValueError(short(r.stderr, 300) or "the post failed")
    for p in paths: shutil.rmtree(p.parent, ignore_errors=True)
    pid = r.stdout.strip()
    return {"id": pid, "thread": thread or pid, "said": short(r.stderr.replace("hive post: ", "", 1), 300)}


def save_upload(name, length, src):
    """Write a request body to ~/files/incoming/<token>/<name> without holding it in memory. Returns what /api/post takes."""
    INCOMING.mkdir(parents=True, exist_ok=True)
    for d in INCOMING.iterdir():  # attached and never sent
        try:
            if time.time() - d.stat().st_mtime > 86400: shutil.rmtree(d, ignore_errors=True)
        except OSError: pass
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "_", Path(str(name)).name).lstrip(".-") or "file"
    tok = secrets.token_hex(8)
    (INCOMING / tok).mkdir(mode=0o700)
    left = length
    with open(INCOMING / tok / safe, "wb") as f:
        while left > 0:
            b = src.read(min(left, 2**20))
            if not b: break
            f.write(b); left -= len(b)
    if left:
        shutil.rmtree(INCOMING / tok, ignore_errors=True)
        raise ValueError("the upload stopped early")
    return {"ref": f"{tok}/{safe}", "name": safe, "size": length}


# ---------- plans and secrets ----------
SECRETS = HOME / ".config/hive/secrets"
SECRET_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}")
MAX_SECRET = 64 * 1024
PROJECT_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}")
MAX_PLAN = 128 * 1024


def dash_note(text, thread):
    """A line on the board from the dash that wakes nobody."""
    p = {"id": datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now_iso(), "from": "dash", "to": [], "thread": thread, "text": text}
    with open(BOARD, "a") as f: f.write(json.dumps(p) + "\n")


def secrets_view():
    """Names and dates only: no route ever reads a value back."""
    try: files = [f for f in SECRETS.iterdir() if f.is_file() and not f.is_symlink() and SECRET_NAME.fullmatch(f.name)]
    except OSError: return []
    return sorted(({"name": f.name, "at": f.stat().st_mtime} for f in files), key=lambda r: r["name"])


def set_secret(name, value, signer, delete=False):
    name = str(name or "")
    if not SECRET_NAME.fullmatch(name): raise ValueError("a secret's name is letters, digits and . _ -, starting with a letter or digit")
    f, who = SECRETS / name, signer or "this machine"
    if delete:
        if not f.is_file(): raise ValueError("no secret has that name")
        f.unlink()
        dash_note(f"Secret `{name}` removed by {who}, in the web app.", "secrets")
        return {"ok": True}
    if not isinstance(value, str) or not value.strip(): raise ValueError("the value is empty")
    if len(value.encode()) > MAX_SECRET: raise ValueError("a secret can be up to 64 KB")
    SECRETS.mkdir(parents=True, exist_ok=True)
    SECRETS.chmod(0o700)
    had = f.exists()
    write_text_private(f, value.strip() + "\n")
    dash_note(f"Secret `{name}` {'replaced' if had else 'set'} by {who}, in the web app. It's in `~/.config/hive/secrets/{name}`, mode 600. Don't print it.", "secrets")
    return {"ok": True}


def write_text_private(path, text):
    t = path.with_name(f".{path.name}.{os.getpid()}.{threading.get_ident()}")
    fd = os.open(t, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f: f.write(text)
    t.replace(path)


def plans_view():
    out = []
    for d in sorted(PROJECTS.glob("*/")):
        f = d / "goals.md"
        if d.is_symlink() or f.is_symlink() or not PROJECT_NAME.fullmatch(d.name): continue
        try: out.append({"name": d.name, "text": f.read_text(errors="replace")[:MAX_PLAN], "version": str(f.stat().st_mtime_ns)})
        except OSError: out.append({"name": d.name, "text": "", "version": None})
    return out


def save_plan(name, text, version, start, signer):
    """Write ~/projects/<name>/goals.md, refusing if it changed since the page read it: agents edit these too."""
    name = str(name or "")
    if not PROJECT_NAME.fullmatch(name): raise ValueError("a project's name is letters, digits, _ and -")
    if not isinstance(text, str) or not text.strip(): raise ValueError("the plan is empty")
    if len(text.encode()) > MAX_PLAN: raise ValueError("a plan can be up to 128 KB")
    d, f = PROJECTS / name, PROJECTS / name / "goals.md"
    if d.is_symlink() or f.is_symlink(): raise ValueError("that project's plan is a link, so it can't be changed here")
    now_v = str(f.stat().st_mtime_ns) if f.exists() else None
    if now_v != version: raise ValueError("this plan changed since you opened it. Copy your text, reload and try again")
    d.mkdir(parents=True, exist_ok=True)
    t = f.with_name(f".goals.md.{os.getpid()}.{threading.get_ident()}")
    t.write_text(text); t.replace(f)
    res = {"version": str(f.stat().st_mtime_ns)}
    if start: res.update(person_post(f"Plan saved in `~/projects/{name}/goals.md`. Read it and start the work.", signer=signer))
    else: dash_note(f"Plan for `{name}` saved by {signer or 'this machine'}, in the web app. Nothing was started.", "plans")
    return res


def delivered_view(days=3):
    """{agent: post ids the inbox hook handed it mid-run}, for read marks on messages. Only files touched lately, and only their ends."""
    out, cut, d = {}, time.time() - days * 86400, STATE / "delivered"
    try: files = [f for f in d.iterdir() if f.is_file() and AGENT_CODE.fullmatch(f.name) and f.stat().st_mtime > cut]
    except OSError: return out
    for f in files:
        try:
            with f.open("rb") as h:
                h.seek(max(0, f.stat().st_size - 8192)); out[f.name] = h.read().decode(errors="replace").split()
        except OSError: pass
    return out


def delivered():
    try: return set((STATE / "delivered" / PERSON).read_text().split())
    except OSError: return set()


def inbox_view(limit=200):
    """Posts addressed to the person, newest first. "read" means handed over, by this page or by the door: both use the door's
    file (~/.hive/delivered/person), so what's been read in one isn't handed over again in the other."""
    INDEX.refresh()
    seen = delivered()
    items = [{**p, "read": p["id"] in seen, "answered": INDEX.answered.get(p["thread"], "") > str(p.get("at", ""))} for p in INDEX.mine]
    return {"unread": sum(not x["read"] for x in items), "items": items[-limit:][::-1]}


def mark_read(ids=None):
    """Mark posts as handed over to the person. ids None: all of them."""
    INDEX.refresh()
    mine = {p["id"] for p in INDEX.mine}
    new = [i for i in (mine if ids is None else ids) if i in mine and i not in delivered()]
    if not new: return 0
    d = STATE / "delivered"; d.mkdir(parents=True, exist_ok=True)
    with (d / PERSON).open("a") as f:
        fcntl.flock(f, fcntl.LOCK_EX); f.write("".join(i + "\n" for i in new))
    return len(new)


# ---------- config: settings, modules, signing in ----------
_cfg_lock = threading.Lock()


def config_view():
    c = cfg()
    return {k: c.get(k) for k in ("max_sessions", "keeper_every_s", "keeper_idle_s", "usage_stop_pct", "auto_update")}


def set_config(d):
    """The few settings the app changes, in core/config.json (the starter reads it every poll). Everything else in it stays."""
    new = {}
    if "max_sessions" in d:
        v = d["max_sessions"]
        if v is not None and (type(v) is not int or not 1 <= v <= 200): raise ValueError("the session limit is a whole number from 1 to 200, or none")
        new["max_sessions"] = v
    if "keeper_every_s" in d:
        v = d["keeper_every_s"]
        if type(v) is not int or not (v == 0 or 600 <= v <= 7 * 86400): raise ValueError("the keeper's pace is off, or 10 minutes to 7 days")
        new["keeper_every_s"] = v
    if "usage_stop_pct" in d:
        v = d["usage_stop_pct"]
        if isinstance(v, bool) or not isinstance(v, (int, float)) or not 1 <= v <= 100: raise ValueError("the usage stop is 1 to 100")
        new["usage_stop_pct"] = v
    if "auto_update" in d:
        if type(d["auto_update"]) is not bool: raise ValueError("auto_update is on or off")
        new["auto_update"] = d["auto_update"]
    if not new: raise ValueError("nothing to change")
    with _cfg_lock:
        c = jload(HOME / "core" / "config.json")
        if not isinstance(c, dict): raise ValueError("core/config.json can't be read")
        c.update(new)
        t = HOME / "core" / f".config.json.{os.getpid()}.{threading.get_ident()}"
        t.write_text(json.dumps(c, indent=2) + "\n"); t.replace(HOME / "core" / "config.json")
    return config_view()


def starter_up():
    try: return time.time() - (STATE / "starter.json").stat().st_mtime < 60  # the door's test
    except OSError: return False


def starter_do(what):
    """Start or stop the service that starts agents. Stopping it lets running agents finish their turn."""
    if what not in ("start", "stop", "restart"): raise ValueError("start, stop or restart")
    try: r = subprocess.run(["systemctl", "--user", what, "hive-starter"], capture_output=True, text=True, timeout=30, env=child_env())
    except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"no systemd here: run python3 ~/core/starter.py ({type(e).__name__})")
    if r.returncode: raise ValueError(short(r.stderr, 300) or f"systemctl {what} failed")


def modules_view():
    """`hive module list`, parsed: {name, state, about, needs}."""
    r = subprocess.run([str(HIVE_BIN), "module", "list"], capture_output=True, text=True, env=child_env(), cwd=str(HOME), timeout=30)
    out = []
    for ln in r.stdout.splitlines():
        if not (m := re.match(r"(\S+)\s+(installed|available)\s*(.*)$", ln)): continue
        about = m[3]; needs = re.search(r" \[needs (.*)\]$", about)
        out.append({"name": m[1], "state": m[2], "about": about[:needs.start()] if needs else about, "needs": needs[1] if needs else None})
    return out


def module_do(what, name):
    name = str(name or "").strip()
    if what not in ("add", "remove"): raise ValueError("add or remove")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*|(https://|ssh://|git@)[A-Za-z0-9_.@:/~+-]+", name): raise ValueError("give a module's name, or a git URL")
    if what == "remove" and name == "dash": raise ValueError("dash is this app, so it can't be removed from here")
    r = subprocess.run([str(HIVE_BIN), "module", what, name], capture_output=True, text=True, env=child_env(), cwd=str(HOME), timeout=900)
    if r.returncode: raise ValueError(short(r.stderr or r.stdout, 500) or f"module {what} failed")
    return r.stdout[-2000:]


class SignIn:
    """`claude auth login` needs a terminal, so this gives it one: a pty the page reads the link from and types the code into. One at a
    time. It holds no token: Claude Code writes its own credentials into the login's directory."""
    def __init__(self):
        self.lock = threading.Lock()
        self.proc, self.fd, self.st = None, None, {"state": "idle"}

    def view(self):
        with self.lock: return dict(self.st)

    def start(self, name=None, console=False):
        name = (name or "").strip() or None
        if name and not re.fullmatch(r"[a-z0-9][a-z0-9-]*", name): raise ValueError("a login's name is lowercase letters, digits and dashes")
        ls = {l["name"]: l["path"] for l in logins()}
        if not name: name = next(iter(ls), None)
        path, new = ls.get(name), False
        if not path:
            if not name: raise ValueError("no login is configured in core/config.json")
            path, new = str(HOME / f".claude-{name}"), True
        with self.lock:
            if self.proc and self.proc.poll() is None: raise ValueError("a sign-in is already under way")
            self.st = {"state": "starting", "name": name, "new": new, "command": f"CLAUDE_CONFIG_DIR={path} claude auth login", "out": ""}
            Path(path).mkdir(mode=0o700, exist_ok=True)
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 2000, 0, 0))  # wide, so the link isn't wrapped
            try:
                self.proc = subprocess.Popen(["claude", "auth", "login", "--console" if console else "--claudeai"], stdin=slave, stdout=slave, stderr=slave,
                                             env={**{k: v for k, v in child_env().items() if k != "ANTHROPIC_API_KEY"}, "CLAUDE_CONFIG_DIR": path, "BROWSER": "true", "TERM": "xterm"},
                                             cwd=str(HOME), start_new_session=True, close_fds=True)
            except OSError as e:
                os.close(master); os.close(slave)
                self.st = {**self.st, "state": "failed", "why": f"couldn't run claude ({type(e).__name__})"}
                return self.st
            os.close(slave); self.fd = master
        threading.Thread(target=self.watch, args=(self.proc, master, name, path, new), daemon=True).start()
        return self.view()

    def watch(self, proc, fd, name, path, new):
        raw, end = b"", time.time() + 900
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], 1)
            if r:
                try: b = os.read(fd, 4096)
                except OSError: b = b""
                if not b: break
                raw += b
                text = raw.decode(errors="replace")
                url = re.search(r"https://[^\s\x07\x1b]+", text)
                plain = re.sub(r"\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]", "", text)
                with self.lock:
                    if url: self.st.update(url=url[0], state="waiting")
                    self.st["out"] = plain[-600:]
            elif proc.poll() is not None: break
        try: proc.wait(10 if time.time() < end else 0)  # its terminal closed because it's finishing: let it write its credentials
        except subprocess.TimeoutExpired:
            proc.terminate()
            try: proc.wait(5)
            except subprocess.TimeoutExpired: proc.kill(); proc.wait()
        try: os.close(fd)
        except OSError: pass
        with self.lock:
            self.fd = None
            if self.st.get("state") == "cancelled": return
            self.st["state"] = "checking"
        SETUP.t = 0
        ok = login_ok(path)
        res = {"state": "done" if ok else "failed"}
        if not ok: res["why"] = "Claude Code didn't report a sign-in. Try again, or run the command below in a terminal as this account."
        elif new:
            r = subprocess.run([str(HIVE_BIN), "login", "add", name], capture_output=True, text=True, env=child_env(), cwd=str(HOME), stdin=subprocess.DEVNULL, timeout=300)
            if r.returncode: res = {"state": "failed", "why": short(r.stderr or r.stdout, 500)}
            else: res["said"] = short(r.stdout, 500)
        with self.lock: self.st.update(res); self.st.pop("url", None)
        SETUP.t = 0

    def code(self, code):
        code = str(code or "").strip()
        if not code or len(code) > 4096 or "\n" in code: raise ValueError("paste the code from the sign-in page")
        with self.lock:
            if self.fd is None or not self.proc or self.proc.poll() is not None: raise ValueError("no sign-in is waiting for a code")
            os.write(self.fd, code.encode() + b"\r")

    def cancel(self):
        with self.lock:
            if self.proc and self.proc.poll() is None:
                self.st["state"] = "cancelled"; self.proc.terminate()
            elif self.st.get("state") != "idle": self.st = {"state": "idle"}


SIGNIN = SignIn()


def login_ok(path):
    """Is this login directory signed in? `claude auth status`, which uses no model usage."""
    env = {**{k: v for k, v in child_env().items() if k != "ANTHROPIC_API_KEY"}, "CLAUDE_CONFIG_DIR": str(path)}
    try: return bool(json.loads(subprocess.run(["claude", "auth", "status", "--json"], env=env, capture_output=True, text=True, timeout=30).stdout).get("loggedIn"))
    except (OSError, ValueError, AttributeError, subprocess.SubprocessError): return False


def setup_state():
    """What the first-run page and the header need: are the logins signed in, is there anything on the board, is the starter up."""
    ls = [{"name": l["name"], "signed_in": login_ok(l["path"])} for l in logins()]
    return {"logins": ls, "signed_in": any(l["signed_in"] for l in ls), "api_key": bool(os.environ.get("ANTHROPIC_API_KEY")),
            "has_posts": BOARD.exists() and BOARD.stat().st_size > 0, "starter": starter_up(),
            "claude": bool(shutil.which("claude"))}


SETUP = Cached(setup_state, 20)  # set .t = 0 to read again


LOGIN_PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:30rem;margin:4rem auto;padding:0 1rem;color-scheme:light dark}input{width:100%;font:inherit;padding:.4rem;box-sizing:border-box}button{font:inherit;margin-top:.6rem;padding:.3rem 1rem}code{font-size:.95em}#e{color:#c33}</style></head>
<body><h1 style="font-size:1.3rem">This Hive needs its secret</h1><p>Run <code>hive web</code> on this machine, as the Hive's user, and open the address it prints.</p>
<input id="s" type="password" placeholder="or paste the secret here" autocomplete="off" aria-label="Secret"><button id="b">Open</button><p id="e" role="alert"></p>
<script>"use strict";
const go=async k=>{const r=await fetch("/login",{method:"POST",headers:{"Content-Type":"application/json","X-Hive-Dash":"1"},body:JSON.stringify({secret:k})});
 if(r.ok)location.replace("/");else document.getElementById("e").textContent="That isn't this Hive's secret. Run hive web again for the current address.";};
document.getElementById("b").addEventListener("click",()=>go(document.getElementById("s").value.trim()));
document.getElementById("s").addEventListener("keydown",e=>{if(e.key==="Enter")go(e.target.value.trim());});
(async()=>{const k=location.hash.slice(1);if(k){history.replaceState(null,"","/login");return go(k);}
 // the session cookie is SameSite=Strict, so a link from elsewhere arrives without it: this request has it, if there is one
 const r=await fetch("/api/app");if(r.ok)location.replace("/");})();
</script></body></html>
"""


def serve(port):
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from urllib.parse import parse_qs, urlparse
    usage = Usage()
    threading.Thread(target=usage.run, daemon=True).start()
    live_c, proj_c, usage_c, jobs_c = Cached(live, 1), Cached(projects, 60), Cached(usage.view, 2), Cached(Jobs().view, 2)
    deliv_c = Cached(delivered_view, 2.5)

    class H(BaseHTTPRequestHandler):
        def send(self, body, ct, status=200, cookie=None):
            gz = len(body) > 4096 and "gzip" in self.headers.get("Accept-Encoding", "")
            if gz: body = gzip.compress(body, 5)
            self.send_response(status)
            self.send_header("Content-Type", ct); self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            if cookie: self.send_header("Set-Cookie", cookie)
            # Board text is written by agents: the page takes nothing from elsewhere and sends nothing anywhere.
            framing = "" if self.route == "open" else "; frame-ancestors 'none'"  # the page can post as the person, so it can't sit in someone's frame
            self.send_header("Content-Security-Policy", "default-src 'none'; connect-src 'self'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'none'" + framing)
            if self.route != "open": self.send_header("Referrer-Policy", "no-referrer")
            if gz: self.send_header("Content-Encoding", "gzip")
            self.end_headers(); self.wfile.write(body)

        def send_file(self, path):
            """A post's file. Agents and Slack write these, so nothing here runs in the page's origin:
            sandboxed, no sniffing, and only plain images open in the tab."""
            m = re.fullmatch(r"/files/([0-9a-f]{12})/([A-Za-z0-9_][A-Za-z0-9_.-]*)", path)
            f = HOME_FILES / m[1] / m[2] if m else None
            if not f or f.is_symlink() or not f.is_file(): return self.send_error(404)
            ct = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif"}.get(f.suffix.lower())
            self.send_response(200)
            self.send_header("Content-Type", ct or "application/octet-stream")
            self.send_header("Content-Length", str(f.stat().st_size))
            self.send_header("Content-Disposition", ("inline" if ct else "attachment") + f'; filename="{f.name}"')
            self.send_header("Content-Security-Policy", "sandbox; default-src 'none'")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            with f.open("rb") as src: shutil.copyfileobj(src, self.wfile)

        route, can, signed, signer, mode, full_left = "open", OPEN_CAN, False, None, None, 0

        def deny(self, code, why):
            self.send(json.dumps({"error": why}).encode(), "application/json", status=code)

        def admit(self, path, page=False):
            """Sets route, can and signed for this request, or answers it and returns False. Checked on every request."""
            web = web_cfg()
            if web is None: return True  # the open dash: no web.json here, as on a site that hasn't set one up
            self.route, self.can, self.signed, self.signer = "refused", (), False, None
            if web.get("broken"): self.deny(500, "web.json is missing or can't be read, so nothing is served. Fix or delete it, then run hive web --setup"); return False
            hd = {k.lower(): v for k, v in self.headers.items()}
            route = route_of(hd, self.client_address[0], port, web)
            if route == "refused": self.deny(403, "this address isn't one of this app's"); return False
            if not same_origin(hd, page_nav=page): self.deny(403, "request from another site"); return False
            self.route = route
            if route == "outside":
                out = web.get("outside") or {}
                claims = verify_access(hd.get("cf-access-jwt-assertion"), out.get("access") or {})
                if not claims:
                    self.deny(403, "outside access needs a valid Cloudflare Access token"); return False
                allowed = person_can(claims, out)
                if not allowed: self.deny(403, "this sign-in isn't on this app's list"); return False
                self.signer = signer_of(claims, out)
                (self.can, self.mode, self.full_left), self.signed = allowed, True
            elif has_session(hd.get("cookie"), web, port):
                self.can, self.signed = CAPS, True
            if web.get("secrets_on") is not True: self.can = tuple(c for c in self.can if c != "secrets")  # off unless web.json says so; no route can set it
            return True

        def app_info(self):
            web = web_cfg() or {}
            info = {"route": self.route, "can": list(self.can), "app": self.route != "open"}
            if self.route != "open": info.update(setup=SETUP.get(), secrets_on=web.get("secrets_on") is True)
            if self.mode: info.update(mode=self.mode, full_left=self.full_left)
            if self.route == "local":
                out = web.get("outside") or {}
                acc = out.get("access") or {}
                info["outside"] = {"can": out.get("can", ["read"]), "hosts": out.get("hosts") or [], "team": acc.get("team", ""), "aud": acc.get("aud", ""),
                                   "emails": acc.get("emails") or [], "people": out.get("people") or {}, "caps": [c for c in CAPS if c != "secrets" or web.get("secrets_on") is True]}
            return info

        def do_GET(self):
            u = urlparse(self.path)
            q = parse_qs(u.query)
            shown = re.fullmatch(r"(.+?)(/light|/dark)?/?", u.path)   # a display page takes /light or /dark on the end
            display = bool(shown) and shown[1] in DISPLAY_PAGES
            page = u.path in ("/", "/index.html", "/login", "/old", "/new", "/new/") or display
            if not self.admit(u.path, page=page): return
            if self.route != "open":  # the open dash never asks; its reads and switches stay as they were
                if u.path == "/login" or (page and not self.signed):
                    return self.send(LOGIN_PAGE.encode(), "text/html; charset=utf-8")
                if not self.signed: return self.deny(401, "sign in: run hive web")
                need = GET_CAP.get(u.path) or ("read" if u.path.startswith("/files/") else None)
                if need and need not in self.can: return self.deny(403, f"this route doesn't allow {need}")
            if display and shown[1] == "/dual-screen":
                self.send_response(302); self.send_header("Location", "/dual-screen/1" + (shown[2] or "")); self.send_header("Content-Length", "0"); self.end_headers(); return
            if display:
                return self.send((UI_DIR / "display.html").read_bytes(), "text/html; charset=utf-8")
            if u.path == "/new":  # index.html's links are relative, so it needs the slash
                self.send_response(302); self.send_header("Location", "/new/"); self.send_header("Content-Length", "0"); self.end_headers(); return
            if u.path in ("/", "/index.html", "/new/"):
                return self.send((UI_DIR / "index.html").read_bytes(), "text/html; charset=utf-8")
            if u.path == "/old":  # the previous page, kept for now
                return self.send(UI.read_bytes(), "text/html; charset=utf-8")
            m = re.fullmatch(r"(?:/new)?/((?:css|js)(?:/[a-zA-Z0-9_-]+)*/[a-zA-Z0-9_-]+\.(?:js|css))", u.path)
            if m:
                f = UI_DIR / m[1]
                if f.is_symlink() or not f.is_file(): return self.send_error(404)
                return self.send(f.read_bytes(), UI_CT[f.suffix])
            off = int(q["from"][0]) if q.get("from", [""])[0].isdigit() else 0
            J = "application/json"
            if u.path == "/api/app":
                self.send(json.dumps(self.app_info(), default=str).encode(), J)
            elif u.path in ("/api/target", "/api/inbox", "/api/config", "/api/modules", "/api/signin", "/api/plans", "/api/secrets") and self.route == "open":
                self.send_error(404)
            elif u.path == "/api/plans":
                self.send(json.dumps(plans_view()).encode(), J)
            elif u.path == "/api/secrets":
                self.send(json.dumps(secrets_view()).encode(), J)
            elif u.path == "/api/target":
                self.send(json.dumps(thread_target(q.get("thread", [""])[0])).encode(), J)
            elif u.path == "/api/inbox":
                self.send(json.dumps(inbox_view()).encode(), J)
            elif u.path == "/api/config":
                self.send(json.dumps(config_view()).encode(), J)
            elif u.path == "/api/modules":
                self.send(json.dumps(modules_view()).encode(), J)
            elif u.path == "/api/signin":
                self.send(json.dumps(SIGNIN.view()).encode(), J)
            elif u.path == "/api/live":
                self.send(json.dumps(live_c.get(), default=str).encode(), J)
            elif u.path == "/api/jobs":
                self.send(json.dumps(jobs_c.get(), default=str).encode(), J)
            elif u.path in ("/api/board", "/api/log"):
                off, lines = tail_lines(BOARD if u.path == "/api/board" else LOG, off)
                self.send(('{"offset":%d,"items":[%s]}' % (off, ",".join(lines))).encode(), J)
            elif u.path == "/api/delivered":
                self.send(json.dumps(deliv_c.get()).encode(), J)
            elif u.path == "/api/usage":
                self.send(json.dumps(usage_c.get()).encode(), J)
            elif u.path == "/api/agent":
                self.send(json.dumps(agent_feed(q.get("name", [""])[0])).encode(), J)
            elif u.path == "/api/projects":
                self.send(json.dumps(proj_c.get(), default=str).encode(), J)
            elif u.path == "/json":
                self.send(json.dumps(snapshot(), default=str).encode(), J)
            elif u.path.startswith("/files/"):
                self.send_file(u.path)
            else:
                self.send_error(404)

        def do_POST(self):
            """The person's switches. Access guards the public name; the custom header makes a cross-site form or fetch fail
            (a browser would need a preflight, and there's no OPTIONS here), so another site can't drive it from his browser."""
            u = urlparse(self.path)
            try: n = int(self.headers.get("Content-Length") or 0)
            except ValueError: n = -1
            if not self.admit(u.path): return
            if self.headers.get("X-Hive-Dash") != "1": return self.send_error(403)
            if u.path == "/login":
                if self.route != "local" or not 0 < n < 1024: return self.send_error(403)
                web = web_cfg()
                try: given = str(json.loads(self.rfile.read(n)).get("secret", ""))
                except (ValueError, AttributeError): return self.send_error(400)
                if not hmac.compare_digest(given.encode(), str(web["secret"]).encode()): return self.deny(401, "wrong secret")
                return self.send(b'{"ok":true}', "application/json", cookie=f"{cookie_name(port)}={session_value(web)}; Path=/; HttpOnly; SameSite=Strict; Max-Age=2592000")
            if self.route != "open" and not self.signed: return self.deny(401, "sign in: run hive web")
            if u.path in LOCAL_ONLY:
                if self.route != "local": return self.send_error(403)
            else:
                need = POST_CAP.get(u.path)
                if not need or need not in self.can: return self.send_error(403)
            if u.path == "/api/upload":
                if not 0 < n <= MAX_FILE: return self.deny(413, "a file can be up to 100 MB")
                try: return self.send(json.dumps(save_upload(parse_qs(u.query).get("name", ["file"])[0], n, self.rfile)).encode(), "application/json")
                except (ValueError, OSError) as e: return self.deny(400, str(e))
            if not 0 < n < ({"/api/post": 2**20, "/api/plan": 2**19, "/api/secret": 2**18}.get(u.path) or (4096 if self.route != "open" else 1024)): return self.send_error(403)
            try: d = json.loads(self.rfile.read(n))
            except ValueError: return self.send_error(400)
            if not isinstance(d, dict): return self.send_error(400)
            num = lambda v: v is None or (isinstance(v, (int, float)) and not isinstance(v, bool))
            J = "application/json"
            if u.path in ("/api/post", "/api/inbox-read", "/api/config", "/api/starter", "/api/module", "/api/signin", "/api/signin/code", "/api/signin/cancel", "/api/access", "/api/plan", "/api/secret"):
                try:
                    if u.path == "/api/post": res = person_post(d.get("text"), d.get("thread"), d.get("reply_to"), d.get("files"), signer=self.signer)
                    elif u.path == "/api/plan": res = save_plan(d.get("name"), d.get("text"), d.get("version"), bool(d.get("start")), self.signer)
                    elif u.path == "/api/secret": res = set_secret(d.get("name"), d.get("value"), self.signer, delete=bool(d.get("delete")))
                    elif u.path == "/api/inbox-read": res = {"marked": mark_read(None if d.get("all") else [str(i) for i in d.get("ids") or []])}
                    elif u.path == "/api/config": res = set_config(d)
                    elif u.path == "/api/starter": starter_do(d.get("do")); SETUP.t = 0; res = {"ok": True}
                    elif u.path == "/api/module": res = {"said": module_do(d.get("do"), d.get("name"))}
                    elif u.path == "/api/signin": res = SIGNIN.start(d.get("name"), bool(d.get("console")))
                    elif u.path == "/api/signin/code": SIGNIN.code(d.get("code")); res = {"ok": True}
                    elif u.path == "/api/signin/cancel": SIGNIN.cancel(); res = {"ok": True}
                    else:
                        if not set_access(d): raise ValueError("that isn't a valid access setting")
                        res = {"ok": True}
                except (ValueError, subprocess.SubprocessError) as e: return self.deny(400, str(e))
                live_c.t = usage_c.t = 0
                return self.send(json.dumps(res).encode(), J)
            if u.path == "/api/kill":  # the one switch that can't be undone: more checks than the others
                if peer_uid(self.client_address) != os.getuid() or self.headers.get("Sec-Fetch-Site", "same-origin") != "same-origin":
                    return self.send_error(403)
                if d.get("confirm") != d.get("name"): return self.send_error(400)  # the code the person typed
                with KILL_LOCK:
                    r = run_to_kill(d.get("name"))
                    if not r: return self.send_error(409)  # not a running agent: nothing is touched
                    res = kill_run(r)
                live_c.t = usage_c.t = 0
                return self.send(json.dumps(res).encode(), "application/json")
            if u.path == "/api/login-pause":
                if not isinstance(d.get("paused"), bool): return self.send_error(400)
                if not set_paused(d.get("name"), d["paused"]): return self.send_error(404)
            elif u.path == "/api/provider-limit":
                if not set_provider_line(d.get("name"), d.get("key"), d.get("value")): return self.send_error(400)
            elif u.path == "/api/login-limit":
                d2 = {k: d[k] for k in ("weekly", "five_hour") if k in d}
                if not d2 or not all(num(v) for v in d2.values()): return self.send_error(400)
                if not set_limits(d.get("name"), d2): return self.send_error(404)
            else:
                if d.get("tier") not in (None, "tier1", "tier2", "held", "other"): return self.send_error(400)
                if not set_tier(d.get("thread"), d.get("tier")): return self.send_error(400)
            live_c.t = usage_c.t = 0  # show it on the next poll
            self.send(b'{"ok":true}', "application/json")

        def log_message(self, *a): pass

    threading.Thread(target=live_usage, daemon=True).start()
    threading.Thread(target=provider_status, daemon=True).start()
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)  # loopback only: never opens the machine
    srv.daemon_threads = True
    print(f"hive-dash on http://127.0.0.1:{port}/  (from elsewhere: ssh -L {port}:localhost:{port} <this-machine>)", flush=True)
    srv.serve_forever()


def main(argv):
    if "-h" in argv or "--help" in argv: print(__doc__); return
    if argv and argv[0] == "serve":
        port = int(argv[argv.index("--port") + 1]) if "--port" in argv else int((web_cfg() or {}).get("port") or 8077)
        return serve(port)
    if "--json" in argv: print(json.dumps(snapshot(), default=str, indent=1)); return
    if "--watch" in argv:
        i = argv.index("--watch")
        n = float(argv[i + 1]) if len(argv) > i + 1 else 5
        try:
            while True:
                w = os.get_terminal_size().columns if sys.stdout.isatty() else 110
                sys.stdout.write("\033[H\033[2J" + text(snapshot(), w) + "\n"); sys.stdout.flush()
                time.sleep(n)
        except KeyboardInterrupt: return
    w = os.get_terminal_size().columns if sys.stdout.isatty() else 110
    print(text(snapshot(), w))


if __name__ == "__main__":
    main(sys.argv[1:])
