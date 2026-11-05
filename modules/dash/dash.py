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
import base64, fcntl, gzip, hashlib, hmac, importlib.util, json, os, pty, pwd, re, secrets, select, shutil, signal, sqlite3, stat, struct, subprocess, sys, tempfile, termios, threading, time
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

HOME = Path(os.environ.get("HIVE_HOME", Path.home()))
RUN, LOG, BOARD = HOME / "run", HOME / "log.jsonl", HOME / "board.jsonl"
HOME_FILES = HOME / "files"
STATE, PROJECTS = HOME / ".hive", HOME / "projects"
PRIORITIES = HOME / "kb" / "priorities.md"
UI = Path(__file__).resolve().parent / "ui.html"
UI_DIR = Path(__file__).resolve().parent / "web"  # the rebuilt page: index.html, css/, js/, vendor/ (local copies of the libraries the chat draws with). Only those are served, not its preview or fixtures.
DISPLAY_PAGES = ("/single-screen", "/dual-screen", "/dual-screen/1", "/dual-screen/2")  # TV pages: web/display.html; which tiles each shows is in js/display/layouts.js
UI_CT = {".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".woff2": "font/woff2",
         ".webmanifest": "application/manifest+json; charset=utf-8", ".png": "image/png", ".ico": "image/x-icon"}
APP_FILES = re.compile(r"(?:/new)?/(manifest\.webmanifest|sw\.js|icons/[a-z0-9-]+\.(?:png|ico))")  # what lets a browser install the page as an app; plain files, served with no sign-in because the browser fetches them before it has one
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


def bg_shells(pid):
    """How many background shells a run is parked on. Their stdout is a task file (the starter's release_shells uses the same test), and what
    they start inherits it, so only the topmost counts. The heavy queue can't see these, so without this a run waiting on one looks stuck."""
    n, todo = 0, []
    def kids(p):
        try:
            for t in Path(f"/proc/{p}/task").iterdir(): todo.extend((t / "children").read_text().split())
        except OSError: pass
    kids(pid)
    while todo:
        q = todo.pop()
        try: shell = "/tasks/" in os.readlink(f"/proc/{q}/fd/1")
        except OSError: shell = False
        if shell: n += 1
        else: kids(q)
    return n


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


USAGE_HISTORY = STATE / "usage_history.jsonl"  # one line every 10 minutes: each login's weekly and 5-hour use, for the Usage page's history


def week_now(l, t=None):
    """A login's weekly use in percent as of now: 0 once its weekly reset has passed with no newer reading. None if never read."""
    w, r = l.get("weekly"), (l.get("resets") or {}).get("weekly")
    if w is None: return None
    return 0.0 if r and r < (t or time.time()) else w


def record_usage(every=600):
    """Append each login's use to USAGE_HISTORY every `every` seconds, so the history holds for logins no session has
    used lately (the session logs only report the login they ran on). Runs in its own thread; a failed write skips that tick."""
    while True:
        t = time.time()
        try:
            row = {}
            for l in logins():
                w = week_now(l, t)
                if w is None: continue
                r5 = (l.get("resets") or {}).get("five_hour")
                row[l["path"]] = {"w": w, "f": 0.0 if r5 and r5 < t else l.get("five")}
            if row:
                with open(USAGE_HISTORY, "a") as fh: fh.write(json.dumps({"t": round(t), "l": row}) + "\n")
        except (OSError, ValueError): pass
        time.sleep(every)


_hist = {"key": None, "rows": []}


def usage_history():
    """USAGE_HISTORY's rows, re-read only when the file changes."""
    try: st = USAGE_HISTORY.stat(); key = (st.st_mtime, st.st_size)
    except OSError: return []
    if _hist["key"] != key: _hist.update(key=key, rows=jlines(USAGE_HISTORY))
    return _hist["rows"]


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


# ---------- progress: the scoreboard, the asks register, runs' outcomes and the improvements ledger (GET /api/progress) ----------
def last_per_id(path, limit=None):
    """An append-only ledger where the last record per id wins, as a dict in first-seen order."""
    out = {}
    try:
        for line in path.read_text(errors="replace").splitlines():
            try: r = json.loads(line)
            except ValueError: continue
            if isinstance(r, dict) and r.get("id"): out[r["id"]] = {**out.get(r["id"], {}), **r}
    except OSError: pass
    return out

def core_json(script, *args, timeout=90):
    """One core script's --json output, at low priority so the dash never slows an agent. None when it can't answer."""
    f = HOME / "core" / script
    if not f.exists(): return None
    try:
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(f), *args], capture_output=True, text=True, timeout=timeout, cwd=str(HOME), env={**os.environ, "HOME": str(HOME)})
        return json.loads(r.stdout) if r.returncode == 0 and r.stdout.strip() else None
    except (OSError, ValueError, subprocess.TimeoutExpired): return None

def progress_state():
    """What /api/progress serves. The scoreboard comes from core/progress.py (a few seconds: cached stale, so a page never waits
    past the first read), with the last snapshots in ~/progress.jsonl as its trend; the rest is read straight from the ledgers."""
    now = time.time(); day = datetime.now(timezone.utc).date().isoformat()
    board = core_json("progress.py", "progress", "--json")
    snaps = []
    try: snaps = [json.loads(l) for l in (HOME / "progress.jsonl").read_text(errors="replace").splitlines()[-48:] if l.strip()]
    except (OSError, ValueError): snaps = []
    if not board and snaps: board = {"now": snaps[-1], "before": snaps[-2] if len(snaps) > 1 else {}, "worse": [], "stale": True}
    asks = []
    for r in last_per_id(HOME / "asks.jsonl").values():
        if r.get("kind") == "direction" or r.get("status") == "dropped": continue
        if r.get("status") == "done" and (r.get("done_at") or r.get("updated") or "") < (datetime.fromtimestamp(now - 2 * 86400, timezone.utc).isoformat()): continue
        asks.append({k: r.get(k) for k in ("id", "when", "thread", "post", "ask", "status", "owner", "evidence", "why", "done_at", "updated")})
    orphaned = core_json("asks.py", "check", "--json", timeout=20) or []
    orphan_ids = {x.get("id") for x in orphaned if isinstance(x, dict)}
    for a in asks: a["orphaned"] = a["id"] in orphan_ids
    imps = []
    for r in last_per_id(HOME / "improvements.jsonl").values():
        imps.append({**{k: r.get(k) for k in ("id", "at", "by", "what", "measure", "baseline", "revisit", "status", "why", "judged_by", "commit", "thread")},
                     "due": r.get("status") == "landed" and (r.get("revisit") or "9999") <= day})
    outs, counts = [], {}
    try:
        for line in (HOME / "outcomes.jsonl").read_text(errors="replace").splitlines()[-600:]:
            try: r = json.loads(line)
            except ValueError: continue
            ended = r.get("ended") or r.get("at") or ""
            try: age = now - datetime.fromisoformat(ended).timestamp()
            except ValueError: continue
            if age > 86400: continue
            counts[r.get("grade", "?")] = counts.get(r.get("grade", "?"), 0) + 1
            outs.append({k: r.get(k) for k in ("key", "name", "thread", "post", "from", "grade", "why", "ended", "by")})
    except OSError: pass
    outs.sort(key=lambda r: r.get("ended") or "", reverse=True)
    return {"at": now, "scoreboard": board, "snapshots": snaps, "asks": asks, "improvements": imps, "outcomes": {"counts": counts, "recent": outs[:80]}}



# ---------- readable thread names (GET /api/thread-names) ----------
# The Slack mirror's indexer names each number thread (modules/slack-mirror/threads.py). The board keeps its ids; the page shows these
# in their place. No mirror, or a database it can't read, means no names, and the page shows ids as it always did.
THREAD_NAMES_DB = Path(os.environ.get("HIVE_MIRROR_STATE", HOME / ".local/state/hive-slack-mirror")) / "threads.db"
THREAD_ID = re.compile(r"[0-9a-f]{12}")
_names = {"mtime": None, "names": {}}

def thread_names():
    """{thread id: name} for number threads, reread only when the indexer's database changes."""
    try: mtime = THREAD_NAMES_DB.stat().st_mtime
    except OSError: return {}
    if mtime != _names["mtime"]:
        db = None
        try:
            db = sqlite3.connect(f"file:{THREAD_NAMES_DB}?mode=ro", uri=True, timeout=2)
            names = {t: n for t, n in db.execute("select thread, name from label") if n and THREAD_ID.fullmatch(t)}
        except sqlite3.Error: return _names["names"]
        finally:
            if db: db.close()
        _names.update(mtime=mtime, names=names)
    return _names["names"]


# ---------- the person's own view of threads (GET/POST /api/thread-meta) ----------
# Pins, archive and new titles the person gives threads on Chat. Kept beside the board, never in it: the board and every link keep
# the thread's id. {thread: {"title": str, "pin": ms, "archived": ms, "back": ms}}; a key that's off is left out, a thread with none is dropped.
THREAD_META = STATE / "thread-meta.json"
META_LOCK = threading.Lock()

def thread_meta():
    try: v = json.loads(THREAD_META.read_text())
    except (OSError, ValueError): return {}
    return v if isinstance(v, dict) else {}

def set_thread_meta(d):
    """d: {thread, and any of title (str, "" takes it off), pin (bool), archived (bool)}. Returns the new entry."""
    th = str(d.get("thread") or "")
    if not THREAD_NAME.fullmatch(th): raise ValueError("a thread name is letters, digits and . _ : -")
    with META_LOCK:
        all_ = thread_meta(); m = dict(all_.get(th) or {}); now_ms = int(time.time() * 1000)
        if "title" in d:
            t = " ".join(str(d["title"] or "").split())[:80]
            if t and t != th: m["title"] = t
            else: m.pop("title", None)
        for k in ("pin", "archived"):
            if k in d:
                if not isinstance(d[k], bool): raise ValueError(k + " is true or false")
                if d[k]: m[k] = now_ms
                else: m.pop(k, None)
        if d.get("archived") is False: m["back"] = now_ms       # taken out of the archive by hand: the automatic one waits a full quiet spell again
        if d.get("archived") is True: m.pop("back", None)
        if d.get("pin") is True: m.pop("archived", None)        # pinning brings it back
        if d.get("archived") is True: m.pop("pin", None)
        if m: all_[th] = m
        else: all_.pop(th, None)
        STATE.mkdir(parents=True, exist_ok=True)
        t = STATE / f".thread-meta.json.{os.getpid()}.{threading.get_ident()}"
        t.write_text(json.dumps(all_, indent=1)); t.replace(THREAD_META)
    return m


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


def chosen_models(st):
    """name -> the model each agent wakes on next: the starter's st["chosen"], with picks from the agent panel it hasn't read yet on top."""
    out = {n: r["model"] for n, r in (st.get("chosen") or {}).items() if isinstance(r, dict) and isinstance(r.get("model"), str)}
    for f in (STATE / "model-set").glob("*.json"):
        try: out[f.stem] = json.loads(f.read_text())["model"]
        except (OSError, ValueError, KeyError, TypeError): pass
    return out

def set_agent_model(name, model):
    """The agent panel's Model menu: `name` wakes on `model` from its next run and stays on it, as if a post had passed --model
    (the person, 2026-11-04, thread feedback). A drop file the starter reads on its next tick (apply_model_sets in starter.py),
    so no post is made and nothing wakes the agent. A run under way keeps its model: a harness can't switch mid-run."""
    if not isinstance(name, str) or not AGENT_CODE.fullmatch(name): raise ValueError("that isn't an agent's name")
    if model not in PICK_MODELS: raise ValueError("pick one of " + ", ".join(PICK_MODELS))
    d = STATE / "model-set"; d.mkdir(parents=True, exist_ok=True)
    tmp = d / f".{name}.tmp"; tmp.write_text(json.dumps({"model": PICK_MODELS[model], "at": time.time()})); tmp.replace(d / f"{name}.json")
    return {"ok": True, "model": PICK_MODELS[model]}

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


def may_kill(uid, me=None):
    """Whose connection may kill: this user's, or root's. A Mac's browser arrives through Lima's guest agent, which runs as root, so
    without root the Kill button could never work there. Root can already end anything here, so this hands nothing to the other users."""
    return uid is not None and uid in (os.getuid() if me is None else me, 0)


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
            "jobs": jobs.get(r.get("name", f.stem)), "shells": bg_shells(r.get("pid")),
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
            "limit_until": limit_until, "chosen": chosen_models(st), "priorities": priorities()}


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


def plain_file(path, tail=False, size=8192):
    """Part of a file agents wrote, or "": never through a link at its end, never waiting on a pipe, only a regular file."""
    try: fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    except OSError: return ""
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode): return ""
        if tail: os.lseek(fd, max(0, st.st_size - size), os.SEEK_SET)
        return os.read(fd, size).decode(errors="replace")
    except OSError: return ""
    finally: os.close(fd)


def last_commit_boxed(d):
    """(time, subject) from .git/logs/HEAD. With a sandbox, projects are written from inside it and this process is outside: git obeys a repo's own .git/config
    even for `git log` (fsmonitor, gpg.program, a partial clone's uploadpack), so it's not run here."""
    lines = plain_file(d / ".git/logs/HEAD", tail=True).splitlines()
    m = re.search(r" (\d+) [-+]\d{4}\t(?:[^:\t]*: )?(.*)$", lines[-1]) if lines else None
    return (float(m[1]), m[2]) if m else (None, "")


def projects():
    res = []
    boxed = SETTINGS.exists()
    for d in sorted(PROJECTS.glob("*/")):
        head = ""
        for fn in ("goals.md", "README.md"):
            if boxed:
                if (text := plain_file(d / fn)):
                    head = next((ln.strip("# ").strip() for ln in text.splitlines() if ln.strip()), "")
                    break
            elif (d / fn).exists():
                for ln in (d / fn).read_text(errors="replace").splitlines():
                    if ln.strip(): head = ln.strip("# ").strip(); break
                break
        commit, t = "", None
        if boxed:
            t, commit = last_commit_boxed(d)
            res.append({"name": d.name, "head": head, "commit": commit, "t": t})
            continue
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

SECRET = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\Z)"
                    r"|(?:gh[pousr]_|github_pat_|xox[abp]-|\bsk-(?:ant|or|proj|svcacct|admin)-)[\w-]+|\bsk-[A-Za-z0-9]{20,}|\bAKIA[0-9A-Z]{16}\b|\bAIza[\w-]{30,}"
                    r"|Bearer\s+[\w.+/=-]{8,}|(?P<label>(?:api[_-]?key|passw(?:or)?d|secret|token)[\"']?\s*[=:]\s*[\"']?)[\w.+/=-]{8,}", re.I | re.S)


def redact(s):
    return SECRET.sub(lambda m: (m["label"] or "") + "[hidden]", str(s or ""))


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
            dm = max(0.0, (u.get("costUSD") or 0) - was.get("c", 0))
            f["m"][m] = f["m"].get(m, 0) + dm
            if dm: b = f.setdefault("mh", {}).setdefault(hour, {}); b[m] = b.get(m, 0) + dm  # per model per hour, so the page can range it
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
            files = {k: {x: v.get(x) for x in ("c", "t", "m", "mh", "h", "tools", "turns", "init")} for k, v in self.files.items() if v["turns"] or v["tools"] or v.get("init")}
            cfg = jload(HOME / "core" / "config.json", {}) or {}
            hist = {}  # the recorded history fills the 10-minute slots the session logs left empty
            since = time.time() - 8 * 86400  # a week's chart and a day to spare; the file keeps all of it
            for r in usage_history():
                if not isinstance(r, dict) or (r.get("t") or 0) < since: continue
                for path, v in (r.get("l") or {}).items():
                    if isinstance(v, dict) and v.get("w") is not None:
                        f = v.get("f")
                        hist.setdefault(path, {})[int(r["t"] // 600)] = [r["t"], None if f is None else f / 100, v["w"] / 100]
            by_login = [{**l, "limits": self.limits.get(l["path"], {}),
                         "series": [v for _, v in sorted({**hist.get(l["path"], {}), **self.series.get(l["path"], {})}.items())]} for l in logins()]
            return {"files": files, "logins": by_login,
                    "scanning": self.scanning, "stop_pct": cfg.get("usage_stop_pct"), "providers": dict(PROVIDERS)}


class AgentCosts:
    """Each agent's cost and tokens from its transcripts, by hive-cost in its own process (the session logs Usage reads undercount and
    miss killed runs). It takes seconds of disk, so it runs only when somebody asks, at most once per `every` seconds; a request
    never waits for it, and gets the last result with "refreshing" set. No hive-cost: "by" stays empty and the page uses Usage's figures."""
    TOOL = Path(__file__).resolve().parent.parent / "cost" / "cost.py"

    def __init__(self, every=300):
        self.every, self.at, self.by, self.busy, self.lock = every, None, {}, False, threading.Lock()

    def refresh(self):
        try:
            r = subprocess.run(["nice", sys.executable, "-I", str(self.TOOL), "--by", "agent", "--json"], capture_output=True, text=True, timeout=180)
            rows = json.loads(r.stdout)["rows"] if r.returncode == 0 else None
        except (OSError, ValueError, KeyError, subprocess.SubprocessError):
            rows = None
        with self.lock:
            self.busy = False
            if rows is not None:
                self.at = time.time()
                self.by = {x["key"][0]: {"usd": x["usd"], "calls": x["calls"], "t": x["tokens"], "sub": x["sub_usd"],
                                         "m": {k: v for k, v in x["models"].items() if k != "<synthetic>"}} for x in rows}

    def view(self):
        with self.lock:
            if not self.busy and time.time() - (self.at or 0) > self.every and self.TOOL.exists():
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            return {"at": self.at, "refreshing": self.busy, "by": self.by}


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



_work = {}  # session log path -> {"off", "files": {path: [n, last, kind]}, "commits": [...], "prs": [...]}: read on from where it stopped
WORK_BUCKET = 300  # tokens over time, in 5-minute steps: [start, input, output, cache read, cache write, model calls]
EDIT_TOOLS = {"Edit": "edit", "MultiEdit": "edit", "Write": "write", "NotebookEdit": "edit"}
SHELL_WRITE = re.compile(r"(?:\bcat\s*>{1,2}\s*|\btee\s+(?:-a\s+)?)([~/\w.\-]+)[^\n]*<<|\bsed\s+-i(?:\s+-E)?\s+(?:'[^'\n]*'|\"[^\"\n]*\")\s+([~/\w.\-]+)\s*(?:$|[;&|\n])")
COMMIT_MSG = re.compile(r"""\bcommit\b[^\n]*?(?:-m\s*(?:"([^"\n]+)|'([^'\n]+)')|<<'?(\w+)'?\s*\n([^\n]+))""")
PR_TITLE = re.compile(r"""\bgh\s+pr\s+create\b[^\n]*?--title\s+(?:"([^"\n]+)"|'([^'\n]+)')""")


def _work_scan(q):
    k = str(q)
    w = _work.get(k) or {"off": 0, "files": {}, "commits": [], "prs": [], "b": {}, "mid": None}
    home = str(HOME)
    def path(x):
        x = str(x or "").strip()
        if not x: return None
        if x.startswith(home + "/"): x = "~" + x[len(home):]
        return x if len(x) < 300 else None
    try:
        with open(q, "rb") as fh:
            fh.seek(w["off"])
            for ln in fh:
                if not ln.endswith(b"\n"): break
                w["off"] += len(ln)
                if not ln.startswith(b'{"type":"assistant"'): continue
                try: e = json.loads(ln)
                except ValueError: continue
                t, msg = ts(e.get("timestamp")), e.get("message") or {}
                u = msg.get("usage")
                if t and u and msg.get("id") != w["mid"]:  # one model call can span several lines, each with the same usage
                    w["mid"] = msg.get("id")
                    tk = [u.get("input_tokens") or 0, u.get("output_tokens") or 0, u.get("cache_read_input_tokens") or 0, u.get("cache_creation_input_tokens") or 0]
                    b = w["b"].setdefault(int(t // WORK_BUCKET * WORK_BUCKET), [0, 0, 0, 0, 0])
                    for i in range(4): b[i] += tk[i]
                    b[4] += 1
                if b'"tool_use"' not in ln: continue
                for c in e.get("message", {}).get("content") or []:
                    if c.get("type") != "tool_use": continue
                    nm, inp = c.get("name"), c.get("input") or {}
                    hits = []
                    if nm in EDIT_TOOLS: hits.append((inp.get("file_path") or inp.get("notebook_path"), EDIT_TOOLS[nm]))
                    elif nm == "Bash":
                        cmd = str(inp.get("command") or "")
                        for m in SHELL_WRITE.finditer(cmd): hits.append((m.group(1) or m.group(2), "shell"))
                        if re.search(r"\bgit\b[^\n|;&]*\bcommit\b", cmd) and "--dry-run" not in cmd:
                            m = COMMIT_MSG.search(cmd)
                            msg = m and (m.group(1) or m.group(2) or m.group(4))
                            if msg and not msg.startswith("$("): w["commits"].append({"t": t, "x": redact(short(msg, 140))})
                        m = PR_TITLE.search(cmd)
                        if m: w["prs"].append({"t": t, "x": redact(short(m.group(1) or m.group(2), 140))})
                    for f, kind in hits:
                        f = path(f)
                        if not f or f.startswith("/tmp/") or "/.claude" in f: continue
                        r = w["files"].setdefault(f, [0, t, kind])
                        r[0] += 1; r[1] = t or r[1]
                        if kind == "write" and r[2] == "shell": r[2] = kind
    except OSError:
        return w
    _work[k] = w
    return w


def agent_work(name, full=True):
    """What an agent made or changed, from all its session logs: files it edited or wrote (by tool, or a shell heredoc or sed -i),
    commits it made and PRs it opened, with when. Best effort: a file written by a script it ran isn't seen.
    A limited sign-in gets the counts only, since paths and messages can say what a private job is."""
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,40}", name or ""): return {}
    try: ps = sorted((HOME / "sessions").glob(name + "-*.jsonl"), key=lambda q: q.stat().st_mtime)
    except OSError: return {}
    files, commits, prs, buckets = {}, [], [], {}
    for q in ps:
        w = _work_scan(q)
        for t0, b in w["b"].items():
            r = buckets.setdefault(t0, [0, 0, 0, 0, 0])
            for i in range(5): r[i] += b[i]
        for f, (n, t, kind) in w["files"].items():
            r = files.setdefault(f, [0, 0, kind]); r[0] += n; r[1] = max(r[1], t or 0)
            if kind == "write": r[2] = kind
        commits += w["commits"]; prs += w["prs"]
    rows = sorted(({"p": f, "n": n, "t": t, "k": k} for f, (n, t, k) in files.items()), key=lambda r: -(r["t"] or 0))
    out = {"files": rows[:60], "nfiles": len(rows), "commits": commits[-30:][::-1], "ncommits": len(commits), "prs": prs[-20:][::-1],
           "step": WORK_BUCKET, "b": [[t0, *b] for t0, b in sorted(buckets.items())]}
    if not full: out = {**out, "files": [], "commits": [], "prs": []}
    return out


class Steps:
    """Every agent's tool calls and words, from all the session logs, for the Ledger page (/api/ledger). Each log is read on
    from where the last look stopped, so after the first pass a look costs only what was written since. Kept in memory as short
    rows (a few tens of MB for tens of thousands of steps); filtered and paged here, so the page only ever gets one page.
    Codex logs carry no times, so they're left out. Each log's agent and thread come from its start event in ~/log.jsonl."""
    EVERY = 5

    def __init__(self):
        self.rows, self.off, self.owner, self.log_off, self.at, self.sorted = [], {}, {}, 0, 0, True
        self.lock, self.busy, self.ready = threading.Lock(), False, False

    def scan(self):
        try:
            with open(LOG, "rb") as fh:
                fh.seek(self.log_off)
                for ln in fh:
                    if not ln.endswith(b"\n"): break
                    self.log_off += len(ln)
                    if b'"start"' not in ln: continue
                    try: e = json.loads(ln)
                    except ValueError: continue
                    if e.get("event") == "start" and e.get("out"): self.owner[Path(e["out"]).name] = (e.get("name") or "", e.get("thread") or "")
        except OSError: pass
        new = []
        try: files = list((HOME / "sessions").glob("*.jsonl"))
        except OSError: files = []
        for q in files:
            k = q.name
            try: size = q.stat().st_size
            except OSError: continue
            if size <= self.off.get(k, 0): continue
            name, thread = self.owner.get(k) or (k.split("-")[0], "")
            off = self.off.get(k, 0)
            try:
                with open(q, "rb") as fh:
                    fh.seek(off)
                    for ln in fh:
                        if not ln.endswith(b"\n"): break
                        off += len(ln)
                        if not ln.startswith(b'{"type":"assistant"'): continue
                        try: e = json.loads(ln)
                        except ValueError: continue
                        t = ts(e.get("timestamp"))
                        if not t: continue
                        for c in (e.get("message") or {}).get("content") or []:
                            if c.get("type") == "tool_use":
                                inp = c.get("input") or {}
                                tool = str(c.get("name") or "")
                                d = inp.get("command") or inp.get("file_path") or inp.get("notebook_path") or inp.get("pattern") or inp.get("url") or inp.get("query") or inp.get("prompt") or ""
                                x, d = redact(short(describe_tool(tool, inp), 220)), redact(short(d, 400))
                                new.append((t, name, thread, "tool", tool, x, d, " ".join((name, thread, tool, x, d)).lower()))
                            elif c.get("type") == "text" and str(c.get("text") or "").strip():
                                x = redact(short(c["text"], 400))
                                new.append((t, name, thread, "said", "", x, "", " ".join((name, thread, x)).lower()))
            except OSError: pass
            self.off[k] = off
        return new

    def refresh(self):
        try:
            new = self.scan()
            with self.lock:
                if new: self.rows.extend(new); self.sorted = False
                self.at, self.ready = time.time(), True
        finally: self.busy = False

    def query(self, agent="", thread="", kind="", q="", before=None, since=None, limit=300, full=True):
        with self.lock:
            if time.time() - self.at > self.EVERY and not self.busy:
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            if not self.sorted: self.rows.sort(key=lambda r: r[0]); self.sorted = True
            rows = self.rows
        words = [w for w in str(q or "").lower().split() if w][:8]
        limit = max(1, min(int(limit or 300), 3000))
        out, total = [], 0
        for r in reversed(rows):
            if before and r[0] >= before: continue
            if since and r[0] < since: break
            if agent and r[1] != agent: continue
            if thread and r[2] != thread: continue
            if kind in ("tool", "said") and r[3] != kind: continue
            if not full and r[3] != "tool": continue
            if words and not all(w in r[7] for w in words): continue
            total += 1
            if len(out) < limit: out.append(list(r[:7]) if full else [*r[:5], "", ""])
        return {"rows": out, "total": total, "ready": self.ready}


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


def web_stamp():
    """Changes when the page's files do, so a tab left open can tell it's running old code: modules load once, so a landed fix doesn't reach it until a reload."""
    files = [UI_DIR / "index.html", *(UI_DIR / "css").rglob("*.css"), *(UI_DIR / "js").rglob("*.js")]
    return hashlib.sha256(repr(sorted((str(f), f.stat().st_mtime_ns, f.stat().st_size) for f in files if f.exists())).encode()).hexdigest()[:12]

class Cached:
    """At most one computation per `ttl` seconds, however many tabs are open."""
    def __init__(self, fn, ttl): self.fn, self.ttl, self.t, self.v, self.lock = fn, ttl, 0, None, threading.Lock()
    def get(self):
        with self.lock:
            if time.time() - self.t > self.ttl:
                self.v, self.t = self.fn(), time.time()
            return self.v


class CachedStale(Cached):
    """For an answer that's slow to get and rarely changes: past `ttl` the old answer goes out while a new one is read behind it,
    so no request waits. Only the first read waits, and a read after .t = 0, which is how a change asks for the truth."""
    busy = False

    def get(self):
        with self.lock:
            if self.v is None or not self.t: self.v, self.t = self.fn(), time.time()
            elif time.time() - self.t > self.ttl and not self.busy:
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            return self.v

    def refresh(self):
        try:
            v = self.fn()
            with self.lock:
                if self.t: self.v, self.t = v, time.time()  # .t = 0 meanwhile: what was just read may predate the change
        finally: self.busy = False

PROGRESS = CachedStale(progress_state, 300)  # the Progress page's data (progress_state above)


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


def jobs_for(view, full):
    """/api/jobs as a caller may see it: command lines only with full rights. A limited or expired sign-in from outside sees who runs what
    and what it uses, not the commands, which can carry paths and arguments. (From H3, b52266fe.)"""
    if full or not isinstance(view, dict): return view
    return {k: [{f: v for f, v in r.items() if f != "cmd"} if isinstance(r, dict) else r for r in rows] if isinstance(rows, list) else rows
            for k, rows in view.items()}


def agents_for(view, full):
    """/api/live and /json as a caller may see them: the same rule as jobs_for. What an agent is doing or said can quote a command, so a limited sign-in sees who runs, not that."""
    if full or not isinstance(view, dict): return view
    return {**view, "agents": [{f: v for f, v in a.items() if f not in ("doing", "said")} for a in view.get("agents") or []]}


def feed_for(rows, full):
    """/api/agent by the same rule: a limited sign-in sees when an agent acted and with which tool, not the arguments or what it said."""
    return rows if full else [{**r, "x": ""} for r in rows if r.get("k") == "tool"]


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

        # Running: the heavy shell holding that lane's lock file open and with no ticket left in the queue. Any fd, because sh moves
        # fd 9 elsewhere while it runs the job with 9>&-. /proc/locks
        # can't say: the lock is taken by the flock binary, which exits at once, so it names a dead pid.
        waiting = {q.name.rsplit(".", 1)[-1] for q in [*d.glob("w.*"), *d.glob("q.*"), *d.glob("f.*")]}
        holders = {}
        for pid, p in procs.items():
            if p["uid"] != me or str(pid) in waiting or not p["comm"].startswith(("sh", "dash", "bash", "heavy")): continue
            if not any(a.endswith("/heavy") for a in proc_cmd(pid)[:2]): continue
            try:
                for fd in os.scandir(f"/proc/{pid}/fd"):
                    try: link = os.readlink(fd.path)
                    except OSError: continue
                    if os.path.dirname(link) == str(d): holders[link] = pid
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
                row = {"who": name, "agent": kind == "outside", "cores": round(c, 2), "mem": round(m), "procs": len(pids)}
                if kind != "other" or procs[pids[0]]["uid"] == me: row["cmd"] = top_cmd(pids)  # another account's command lines are theirs, not this app's to show
                outside.append(row)
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
CAPS = ("read", "post", "stop", "tier", "limits", "logins", "config", "modules", "plans", "secrets", "files")
# outside.people {email: "limited" or "full"} in web.json: what each signed-in person may do from outside. Not listed: refused.
# outside.sites {host: {access, people, names}}: a name with its own Access team, aud and list. A token counts only for its own
# host's team and aud, so one Cloudflare account's sign-in can't be used on another's name. Anything malformed refuses everyone.
LIMITED_CAN = ("read", "post", "stop")
LIMITED_FILES = STATE / "limited-files"  # this file present: limited sign-ins here may also read the Knowledge page. Off unless a Hive has decided to, since limited people differ per install.
FULL_FOR_S = 3600  # full rights need an Access token issued this recently; after that the same session is limited until they sign in again,
FULL_IDLE_S = 1800  # unless it stays in use: a full session that sees the person's own input (X-Hive-Active, or a POST) at least this often stays full
FULL_KEEP = STATE / "full-active.json"  # {sha256 of email|iat|aud: last input}, so a dash restart doesn't drop a busy person to limited
OPEN_CAN = ("read", "stop", "tier", "limits")
WEB = STATE / "web.json"
INSTALLED = HOME / ".hive-installed"
FORWARDED = ("cf-ray", "cf-connecting-ip", "cf-access-jwt-assertion", "cf-ipcountry", "cf-visitor", "cdn-loop", "x-forwarded-for",
             "x-forwarded-host", "x-forwarded-proto", "x-real-ip", "forwarded", "via", "true-client-ip")
GET_CAP = {"/api/live": "read", "/api/board": "read", "/api/log": "read", "/api/usage": "read", "/api/jobs": "read", "/api/agent": "read", "/api/agent-work": "read", "/api/ledger": "read",
           "/api/projects": "read", "/api/agent-costs": "read", "/api/thread-names": "read", "/api/thread-meta": "read", "/json": "read", "/api/target": "read", "/api/inbox": "read", "/api/delivered": "read",
           "/api/config": "config", "/api/modules": "modules", "/api/signin": "logins", "/api/plans": "plans", "/api/secrets": "secrets",
           "/api/settings": "config", "/api/update": "config", "/api/signin-codex": "logins", "/api/accounts": "logins", "/api/design": "logins", "/api/brand/logo": "read",
           "/api/kb/home": "files", "/api/kb/open": "files", "/api/kb/search": "files", "/api/kb/raw": "files", "/api/kb/map": "files", "/api/kb/history": "files", "/api/progress": "read", "/api/approvals": "read", "/api/feedback-drafts": "read"}
POST_CAP = {"/api/kill": "stop", "/api/approval": "stop", "/api/tier": "tier", "/api/agent-model": "post", "/api/login-pause": "limits", "/api/login-limit": "limits", "/api/provider-limit": "limits",
            "/api/post": "post", "/api/thread-meta": "post", "/api/feedback": "post", "/api/feedback-draft-done": "post", "/api/issue-link": "post", "/api/update": "config", "/api/share-copy": "config", "/api/draft": "config", "/api/upload": "post", "/api/inbox-read": "post", "/api/config": "config", "/api/starter": "config",
            "/api/module": "modules", "/api/signin": "logins", "/api/signin/code": "logins", "/api/signin/cancel": "logins", "/api/plan": "plans", "/api/secret": "secrets",
            "/api/signin-codex": "logins", "/api/account-key": "logins", "/api/design": "logins", "/api/brand": "config", "/api/brand/logo": "config"}
LOCAL_ONLY = {"/api/access", "/api/settings", "/api/share-copy", "/api/draft"}  # who may do what is never changed from outside, whatever outside.can says


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


def site_key(host):
    """A name as outside.sites compares it: a hand-written "A.example. " is the site a.example, not a miss that falls through."""
    return str(host or "").strip().lower().rstrip(".")


def outside_hosts(out):
    """Every outside name: outside.hosts (which share the top-level profile) and the keys of outside.sites."""
    sites = out.get("sites") if isinstance(out.get("sites"), dict) else {}
    return {str(h).lower() for h in out.get("hosts") or []} | {site_key(h) for h in sites}


REFUSE_ALL = {"access": {}, "people": {}}  # no team or aud, so verify_access refuses every token


def profile_for(out, host):
    """The access profile (access, people, names) for one outside Host. A name in outside.sites gets only its own entry, never
    the top-level one. Hosts not in sites (outside.hosts, or a forwarded request to a local address) get the top-level profile,
    as before sites existed. Only Host chooses: X-Forwarded-Host can be sent by anyone. Fails closed on a hand-edited file: sites
    present but not a dict, an entry that isn't a dict, one name written twice, or an entry with no people all refuse everyone,
    since person_can lets in anyone Access passed when a profile has no people."""
    if "sites" not in out: return out
    if not isinstance(out["sites"], dict): return REFUSE_ALL
    found = [v for k, v in out["sites"].items() if site_key(k) == site_key(host)]
    if not found: return out
    site = found[0] if len(found) == 1 else None
    return site if isinstance(site, dict) and isinstance(site.get("people"), dict) and site["people"] else REFUSE_ALL


def route_of(headers, peer, port, web):
    """"local", "outside" or "refused". headers: lowercased names. Host is checked for DNS rebinding: a page on evil.example
    that rebinds to 127.0.0.1 still sends its own Host. Loopback alone proves nothing, since a tunnel connects over loopback.
    peer "unix": this came through outside.socket, so it's outside whatever it says, and only for an outside name."""
    host = (headers.get("host") or "").lower()
    outside = outside_hosts(web.get("outside") or {})
    if peer == "unix": return "outside" if host in outside else "refused"
    if host not in {f"127.0.0.1:{port}", f"localhost:{port}", f"[::1]:{port}"} | outside: return "refused"
    if host in outside or peer not in ("127.0.0.1", "::1") or any(h in headers for h in FORWARDED):
        # With outside.socket the tunnel comes only through that socket. Another account can bind the TCP port while the dash
        # restarts and catch a token the tunnel sends there, so a token on TCP proves nothing and is never looked at.
        return "refused" if (web.get("outside") or {}).get("socket") else "outside"
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
    """Cloudflare Access's public keys, from https://<team>/cdn-cgi/access/certs, kept per URL, since each outside host can have
    its own team. Each is read hourly, or when a token names an unknown key, at most once a minute. If they can't be read,
    tokens signed with an unknown key are refused. One team's keys never answer for another's URL."""
    def __init__(self): self.sets, self.locks, self.lock = {}, {}, threading.Lock()  # url -> (keys, read at); url -> its own lock

    def get(self, url, kid):
        import urllib.request
        with self.lock: ulock = self.locks.setdefault(url, threading.Lock())  # held only to find the URL's lock, never during a fetch
        with ulock:  # one team's slow fetch holds up only requests for that team
            keys, t = self.sets.get(url, ({}, 0))
            age = time.time() - t
            if url not in self.sets or age > 3600 or (kid not in keys and age > 60):
                try:
                    with urllib.request.urlopen(url, timeout=10) as r: got = json.load(r)["keys"]
                    keys = {k["kid"]: (int.from_bytes(b64u(k["n"]), "big"), int.from_bytes(b64u(k["e"]), "big"))
                            for k in got if k.get("kty") == "RSA" and k.get("kid")}
                except Exception as e:
                    print(f"can't read the Access keys: {type(e).__name__}", file=sys.stderr, flush=True)
                self.sets[url] = (keys, time.time())
            return keys.get(kid)


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


def limited_can():
    return LIMITED_CAN + ("files",) if LIMITED_FILES.exists() else LIMITED_CAN


class FullKeep:
    """When each full sign-in last saw its person's input. Keyed by a hash of the signed email, iat and aud, so one Access session
    is one key and a new sign-in starts its own. Only a session that is full now can be kept full; one already limited needs a new sign-in."""
    def __init__(self, path):
        self.path, self.lock, self.at, self.saved = path, threading.Lock(), None, 0

    def _load(self):
        if self.at is None:
            d = jload(self.path, {}) if self.path.exists() else {}
            self.at = {k: v for k, v in d.items() if isinstance(k, str) and isinstance(v, (int, float))} if isinstance(d, dict) else {}

    def last(self, key):
        with self.lock:
            self._load(); return self.at.get(key, 0)

    def touch(self, key, now):
        with self.lock:
            self._load(); self.at[key] = now
            if now - self.saved < 30: return  # written at most twice a minute; losing the last 30 s to a restart costs nothing
            self.at = {k: v for k, v in self.at.items() if now - v <= FULL_IDLE_S}
            self.saved = now
            try:
                tmp = self.path.with_suffix(".tmp"); tmp.write_text(json.dumps(self.at)); tmp.chmod(0o600); tmp.replace(self.path)
            except OSError: pass


FULL_ACTIVE = FullKeep(FULL_KEEP)


def person_can(claims, out, now=None, active=False, keep=FULL_ACTIVE):
    """(can, mode, seconds of full left) for a verified token, or None if this person isn't let in. Without outside.people: outside.can, for anyone Access let through.
    Full lasts FULL_FOR_S after sign-in, and after that for as long as the person keeps using the app (active: this request carries their input), up to FULL_IDLE_S apart."""
    if "people" not in out: return tuple(c for c in out.get("can", ["read"]) if c in CAPS), None, 0
    people = out["people"] if isinstance(out["people"], dict) else {}
    email = str(claims.get("email") or "").strip().lower()
    mode = {str(k).strip().lower(): v for k, v in people.items()}.get(email) if email else None
    if mode == "limited": return limited_can(), "limited", 0
    if mode != "full": return None
    iat, now = claims.get("iat"), now or time.time()
    if not isinstance(iat, (int, float)) or isinstance(iat, bool): return limited_can(), "expired", 0
    left = iat + FULL_FOR_S - now
    if left > FULL_FOR_S + 60: return limited_can(), "expired", 0  # a token dated well ahead is no proof of a recent sign-in
    key = hashlib.sha256(f"{email}|{iat}|{claims.get('aud')}".encode()).hexdigest()
    idle = FULL_IDLE_S - (now - keep.last(key))
    if left <= 0 and idle <= 0: return limited_can(), "expired", 0
    if active: keep.touch(key, now); idle = FULL_IDLE_S
    return CAPS, "full", int(max(left, idle))


def site_profile(v):
    """One outside.sites entry as /api/access stores it; None if any part is malformed or it would refuse everyone anyway. It must
    have a team, an aud and people. No `can`: with people, can never applies, so accepting one would promise a limit that isn't there."""
    if not isinstance(v, dict) or "can" in v: return None
    one = {}
    if "names" in v:
        if not isinstance(v["names"], dict) or not all(isinstance(k, str) and isinstance(x, str) for k, x in v["names"].items()): return None
        one["names"] = v["names"]
    p = v.get("people")
    if not isinstance(p, dict) or not p or any(not isinstance(k, str) or "@" not in k or x not in ("limited", "full") for k, x in p.items()): return None
    one["people"] = {k.strip().lower(): x for k, x in p.items()}
    a = v.get("access")
    if not isinstance(a, dict) or any(not isinstance(a.get(k), str) for k in ("team", "aud")): return None
    team, aud = a["team"].strip(), a["aud"].strip()
    if not re.fullmatch(r"[A-Za-z0-9.-]+", team) or not aud: return None  # team goes into the URL the keys are read from
    one["access"] = {"team": team, "aud": aud}
    emails = [str(e).strip() for e in a.get("emails") or [] if str(e).strip()]
    if emails: one["access"]["emails"] = emails
    return one


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
    if "sites" in d:
        if not isinstance(d["sites"], dict): return False
        clean = {}
        for h, v in d["sites"].items():
            one = site_profile(v)
            if one is None or not re.fullmatch(r"[A-Za-z0-9.-]+(:\d+)?", h) or not site_key(h) or site_key(h) in clean: return False
            clean[site_key(h)] = one
        out["sites"] = clean
    if "access" in d:
        a = d["access"]
        if not isinstance(a, dict) or any(not isinstance(a.get(k, ""), str) for k in ("team", "aud")): return False
        if a.get("team", "").strip() and not re.fullmatch(r"[A-Za-z0-9.-]+", a["team"].strip()): return False  # it goes into the URL the keys are read from
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
SPAWNERS = {"new", "claude", "codex", "free", "warden", "opus", "sonnet", "haiku", "fable", "astra"}


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


def thread_agent(thread):
    """The agent a plain message in this thread reaches: the last to post if it's running, else the newest run working on the thread
    (it may not have posted yet, and a running agent gets the thread's posts anyway, so starting another would double the work), else the last to post, else None."""
    INDEX.refresh()
    last = INDEX.last.get(thread)
    if last and alive((jload(RUN / f"{last}.json") or {}).get("pid")): return last
    runs = [r for r in (jload(f, {}) or {} for f in RUN.glob("*.json")) if r.get("thread") == thread and r.get("name") and alive(r.get("pid"))]
    return max(runs, key=lambda r: r.get("at") or "")["name"] if runs else last


def thread_target(thread):
    """Who a post in this thread would reach: {"who": name or None, "running": bool}."""
    who = thread_agent(thread)
    return {"who": who, "running": bool(who and alive((jload(RUN / f"{who}.json") or {}).get("pid")))}


STEP_WORD = re.compile(r"(approve|deny) step [0-9a-f]{6}-[0-9a-f]{8}", re.I)


# Simple mode's word to the agents, on each post sent from it. Inside the head's parentheses, so the web app hides it (SOURCE in web/js/core/model.js):
# no ")" and no newline, and no "@", which would call someone. The core and the agents' prompts don't change.
VIDEO_TYPES = {".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".m4v": "video/mp4"}
class _Limit:
    """Reads at most n bytes from a file: the slice a Range request asks for."""
    def __init__(self, f, n): self.f, self.n = f, n
    def read(self, k=-1):
        k = self.n if k is None or k < 0 else min(k, self.n); b = self.f.read(k); self.n -= len(b); return b
PAGE_CSP = "sandbox allow-scripts; default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; form-action 'none'; base-uri 'none'"
SIMPLE_NOTE = ("simple mode: this person isn't technical. Plain words, no agent names or jargon, speak as one assistant, one question at a time with your recommendation, "
               "show results as links or files, a draft website as one .html file holding its own styles and pictures. For anything they must approve, say what it does, what it costs and whether it can be undone")

# /btw QUESTION in a conversation: a side question. It goes to a new thread btw-<parent>-<4 hex>, so the parent's agent never gets it
# (its inbox hook hands over only its own thread and its name), and starts a fresh agent there with the brief below in the head.
# No ")", newline or "@" in the brief (see SIMPLE_NOTE). Follow-ups in the side thread wake that same agent, as in any thread.
BTW = re.compile(r"/btw(?:\s+(.*))?", re.S | re.I)
BTW_THREAD = re.compile(r"btw-(.+)-[0-9a-f]{4}")
BTW_NOTE = ("side question, asked with /btw about thread {p}: read it with `hive read --thread {p}` and whatever logs or files you need, then answer here, briefly, with `hive post` in this thread (an answer left only in your final message never reaches the person). "
            "It's a side question, so post nothing in {p}, don't name its agents with the at sign, change nothing and start no work. If the answer calls for action, say what you'd do and let the person decide")



# ---------- approval requests (hive approve ask; the card on the thread) ----------
APPROVALS = STATE / "approvals"
APPROVAL_ID = re.compile(r"[0-9a-f]{8}")

def approvals_list():
    """Requests agents have made for the person's yes: pending ones first, then the last few answered."""
    out = []
    for f in sorted(APPROVALS.glob("*.json"), key=lambda f: f.stat().st_mtime, reverse=True)[:60] if APPROVALS.is_dir() else []:
        try: out.append(json.loads(f.read_text()))
        except (OSError, ValueError): pass
    return sorted(out, key=lambda a: a.get("state") != "pending")

def approval_answer(d):
    """The person's click on a request card. A major request needs the typed x. Writes the answer and tells the agent as the person."""
    aid = str(d.get("id") or "")
    if not APPROVAL_ID.fullmatch(aid): raise ValueError("no such request")
    f = APPROVALS / (aid + ".json")
    with KILL_LOCK:
        try: a = json.loads(f.read_text())
        except (OSError, ValueError): raise ValueError("no such request")
        if a.get("state") != "pending": raise ValueError("that request was already answered")
        yes = d.get("decision") == "approve"
        if d.get("decision") not in ("approve", "deny"): raise ValueError("approve or deny")
        if yes and a.get("major") and d.get("confirm") != "x": raise ValueError("type x to approve a major change")
        a.update(state="approved" if yes else "denied", answered=datetime.now(timezone.utc).isoformat(timespec="seconds"))
        f.write_text(json.dumps(a))
    person_post(f"@{a['from']} {'APPROVED' if yes else 'DENIED'} request {aid} (answered with the dashboard button): {a.get('why', '')[:300]}", a.get("thread"))
    return {"ok": True, "state": a["state"]}

# ---------- feedback drafts (hive feedback-draft; the Send feedback window opens prefilled, the person presses Send) ----------
FB_DRAFTS = STATE / "feedback-drafts"

def feedback_drafts_list():
    out = []
    for f in sorted(FB_DRAFTS.glob("*.json"), key=lambda f: f.stat().st_mtime)[-10:] if FB_DRAFTS.is_dir() else []:
        try: out.append(json.loads(f.read_text()))
        except (OSError, ValueError): pass
    return out

def feedback_draft_done(d):
    aid = str(d.get("id") or "")
    if not APPROVAL_ID.fullmatch(aid): raise ValueError("no such draft")
    (FB_DRAFTS / (aid + ".json")).unlink(missing_ok=True)
    return {"ok": True}

def voice_ready():
    """True when the voice module's service has proved itself working: its self-test passed and its heartbeat is under 2 minutes old."""
    st = jload(HOME / ".hive" / "voice-status.json", {})
    return isinstance(st, dict) and st.get("ok") is True and isinstance(st.get("at"), (int, float)) and time.time() - st["at"] < 120


def person_post(text, thread=None, reply_to=None, uploads=(), signer=None, limited=False, simple=False, model=None):
    """A post as the person. As the Slack mirror does it: a plain message wakes the agent on the thread (thread_agent), a reply wakes the author
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
    if model and model not in PICK_MODELS: raise ValueError("that model isn't one of " + ", ".join(PICK_MODELS))
    if not text: text = "Attached: " + ", ".join(p.name for p in paths)
    btw = BTW.fullmatch(text)
    if btw:
        if not thread: raise ValueError("/btw asks aside about a conversation, so send it from one")
        if not (btw[1] or "").strip(): raise ValueError("write the side question after /btw")
        parent = BTW_THREAD.fullmatch(str(thread)); parent = parent[1] if parent else str(thread)   # asked from a side thread: still about the main one
        thread, text, reply_to = f"btw-{parent[:90]}-{secrets.token_hex(2)}", btw[1].strip(), None
    # Approving a step goes to the broker, which takes only a whole line in this form from a plain person post. So it stays bare, with nothing added;
    # a limited sign-in's posts are marked and never count, whatever lines they hold.
    word = not limited and not paths and bool(STEP_WORD.fullmatch(text.strip()))
    if word: text, reply_to, signer, simple = text.strip(), None, None, False
    if signer or simple or btw: text = "(" + (f"from {signer}, " if signer else "") + "web app" + (f", {SIMPLE_NOTE}" if simple else "") + (", " + BTW_NOTE.format(p=parent) if btw else "") + f") {text}"
    INDEX.refresh()
    who = None
    if reply_to:
        reply_to = str(reply_to)
        if not re.fullmatch(r"[0-9a-f]{6,20}", reply_to) or reply_to not in INDEX.authors: raise ValueError("the post replied to isn't on the board")
        author = INDEX.authors[reply_to].lower()
        who = author if AGENT_CODE.fullmatch(author) else None
        text = f"reply to message {reply_to}: {text}"
    routed = False
    if not word and not calls_someone(text):
        who = who or (thread_agent(thread) if thread else None)
        text = f"@{who or 'new'} {text}"
        routed = bool(who)  # we picked the name, so a dead agent's heir may take it; only a typed name brings a dead agent back
    cmd = [str(HIVE_BIN), "post", "--as", PERSON] + (["--thread", str(thread)] if thread else []) + (["--to", who] if who else []) + (["--routed"] if routed else []) + (["--reply-to", reply_to] if reply_to else []) + (["--model", PICK_MODELS[model]] if model else [])
    for p in paths: cmd += ["--file", str(p)]
    r = subprocess.run(cmd, input=text, text=True, capture_output=True, env={**child_env(), **({"HIVE_POST_VIA": "limited"} if limited else {})}, cwd=str(HOME), timeout=60)
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
    fd, t = tempfile.mkstemp(dir=d, prefix=".goals.md.")  # agents write in this folder: never a name they could have put a link at
    with os.fdopen(fd, "w") as o: os.fchmod(fd, 0o644); o.write(text)
    os.replace(t, f)
    res = {"version": str(f.stat().st_mtime_ns)}
    if start: res.update(person_post(f"Plan saved in `~/projects/{name}/goals.md`. Read it and start the work.", signer=signer))
    else: dash_note(f"Plan for `{name}` saved by {signer or 'this machine'}, in the web app. Nothing was started.", "plans")
    return res


# ---------- the Knowledge page: read-only browsing of what the Hive keeps ----------
# Paths are relative to home and always start with one of KB_ROOTS. Nothing here writes; plans are saved by save_plan.
KB_ROOTS = ("kb", "modules", "core", "tools", "projects", "files", "investigations", "archive")
KB_SHELVES = (("kb", "Notes", "What agents learned and wrote down"), ("modules", "Modules", "Extras and shared tools, each with its README"),
              ("modules/bin", "Scripts", "The commands agents run"), ("core", "Core", "The starter, hive and the rules"),
              ("projects", "Projects", "Plans and code, one folder each"), ("files", "Post files", "What was attached to posts"),
              ("investigations", "Investigations", "Longer digs and their evidence"), ("tools", "Tools", "Installed toolchains"), ("archive", "Archive", "Kept from older Hives"))
KB_SKIP = {"node_modules", "__pycache__", "venv", "site-packages"}
KB_DENY = re.compile(r"^id_(rsa|dsa|ecdsa|ed25519)|\.(pem|key|p12|pfx|keystore|jks|gpg|asc|kdbx|env)$|^(web|auth|logins?)\.json$|^hosts\.ya?ml$|^(authorized_keys|known_hosts|htpasswd)$|credential", re.I)
KB_WORDS = re.compile(r"secret|token|passw(or)?d|api[_-]?key", re.I)
KB_CODE = {".py", ".go", ".js", ".mjs", ".ts", ".tsx", ".css", ".md", ".html", ".c", ".h", ".rs"}  # a name like tokens.css is code, not a token
KB_BLOB = re.compile(r"[A-Za-z0-9+/=_-]{40,}")  # a search shows one line, so a key's middle line has none of the marks redact() looks for
KB_IMAGE = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}
KB_RAW_MAX = 200 * 1024 * 1024
KB_TEXT_MAX, KB_LIST_MAX, KB_INDEX_MAX, KB_HITS_MAX = 512 * 1024, 2000, 40000, 200
KB_SEARCH = threading.Semaphore(2)
kb_index_memo = {"t": 0, "items": [], "more": False}
kb_index_lock = threading.Lock()


def kb_name_ok(n):
    """A file or folder name that may be shown: no dotfiles, nothing named like a key. Code about tokens stays readable."""
    if not n or n.startswith(".") or KB_DENY.search(n): return False
    return not KB_WORDS.search(n) or Path(n).suffix.lower() in KB_CODE


def kb_parts_ok(parts):
    return bool(parts) and parts[0] in KB_ROOTS and all(p not in ("", ".", "..") and kb_name_ok(p) for p in parts)


def kb_path(rel):
    """The real path for a home-relative one, or None. Checks the name asked for and, after following links, where it really is."""
    rel = str(rel or "").strip("/")
    if not rel or len(rel) > 1024 or "\0" in rel or not kb_parts_ok(rel.split("/")): return None
    try:
        real, base = (HOME / rel).resolve(), HOME.resolve()
        if not kb_parts_ok(real.relative_to(base).parts) or not real.exists(): return None
    except (OSError, ValueError, RuntimeError): return None
    return real


def kb_is_worktree(d):
    try: return (d / ".git").is_file()
    except OSError: return False


def kb_title(f, text=None):
    """A note's first heading, or the first line of prose, for lists."""
    try:
        if text is None:
            with f.open(errors="replace") as src: text = src.read(4096)
    except OSError: return ""
    for line in text.splitlines():
        line = line.strip()
        if line and not line.startswith(("---", "```", "<!--", "![")): return redact(line.lstrip("# ").strip())[:160]
    return ""


def kb_kind(f, head):
    suf = f.suffix.lower()
    if suf in KB_IMAGE: return "image"
    if suf == ".pdf": return "pdf"
    if b"\0" in head: return "binary"
    return "md" if suf in (".md", ".markdown") else "text"


def kb_list(p, rel):
    out, more = [], False
    try: found = sorted(os.scandir(p), key=lambda e: e.name.lower())
    except OSError: found = []
    md_titles = sum(e.name.endswith(".md") for e in found) <= 300
    for e in found:
        if not kb_name_ok(e.name) or e.name in KB_SKIP: continue
        if len(out) >= KB_LIST_MAX: more = True; break
        child = f"{rel}/{e.name}"
        real = kb_path(child)
        if not real: continue
        st = real.stat(); row = {"name": e.name, "path": child, "t": int(st.st_mtime)}
        if real.is_dir():
            row["dir"] = True
            if kb_is_worktree(real): row["worktree"] = True
            elif rel in ("modules", "projects"):
                for doc in ("goals.md", "README.md", "readme.md"):
                    if (real / doc).is_file() and kb_path(f"{child}/{doc}"):
                        row["title"] = kb_title(real / doc)
                        if doc == "goals.md": row["plan"] = True
                        break
        else:
            row["size"] = st.st_size
            if md_titles and e.name.endswith(".md"): row["title"] = kb_title(real)
        out.append(row)
    out.sort(key=lambda r: (bool(r.get("worktree")), not r.get("plan"), not r.get("dir")))
    return out, more


def kb_doc(p, rel):
    """The note that explains a folder: a project's goals.md, or its README."""
    for doc in ("goals.md", "README.md", "readme.md", "README.txt", "README"):
        f = p / doc
        if f.is_file() and kb_path(f"{rel}/{doc}"):
            with f.open(errors="replace") as src: text = src.read(KB_TEXT_MAX)
            return {"path": f"{rel}/{doc}", "name": doc, "text": redact(text), "t": int(f.stat().st_mtime), "kind": "text" if doc in ("README", "README.txt") else "md"}
    return None


def kb_linked(name, rel):
    """Notes that name this file."""
    if not shutil.which("grep") or not (HOME / "kb").is_dir(): return []
    try: r = subprocess.run(["grep", "-rlIF", "--include=*.md", "--", name, "kb"], cwd=HOME, capture_output=True, text=True, timeout=3)
    except (OSError, subprocess.SubprocessError): return []
    return [x for x in r.stdout.splitlines() if x != rel and kb_path(x)][:20]


def kb_open(rel):
    """A folder with its listing, or a file ready to read."""
    rel = str(rel or "").strip("/"); p = kb_path(rel)
    if not p: return None
    st = p.stat()
    res = {"path": rel, "name": rel.rsplit("/", 1)[-1], "t": int(st.st_mtime)}
    if p.is_dir():
        res["entries"], res["more"] = kb_list(p, rel)
        res.update(type="dir", doc=kb_doc(p, rel))
        return res
    if not p.is_file(): return None
    with p.open("rb") as src: head = src.read(KB_TEXT_MAX)
    kind = kb_kind(p, head[:8192])
    res.update(type=kind, size=st.st_size, linked=kb_linked(p.name, rel) if rel.startswith(("kb/", "modules/", "core/")) else [])
    if kind in ("md", "text"):
        res.update(text=redact(head.decode(errors="replace")), truncated=st.st_size > KB_TEXT_MAX)
    return res


def kb_walk(rel, depth, room):
    """(path, mtime) of the files under rel, nearest first, skipping worktrees and links."""
    start = HOME / rel
    if not start.is_dir() or start.is_symlink(): return []
    out, todo = [], [(start, rel, 0)]
    while todo and len(out) < room:
        d, r, n = todo.pop(0)
        try: found = list(os.scandir(d))
        except OSError: continue
        for e in found:
            if not kb_name_ok(e.name) or e.name in KB_SKIP or e.is_symlink(): continue
            try:
                if e.is_dir(follow_symlinks=False):
                    if n < depth and not kb_is_worktree(Path(e.path)): todo.append((Path(e.path), f"{r}/{e.name}", n + 1))
                elif e.is_file(follow_symlinks=False): out.append((f"{r}/{e.name}", e.stat(follow_symlinks=False).st_mtime))
            except OSError: continue
    return out[:room]


def kb_index():
    """Every file's path and time, for name search and "changed lately". Bounded, and at most a minute old."""
    with kb_index_lock:
        if time.time() - kb_index_memo["t"] < 60: return kb_index_memo
        items = []
        for rel, depth in (("kb", 12), ("core", 12), ("modules", 12), ("files", 2), ("investigations", 8), ("archive", 6), ("projects", 3), ("tools", 2)):
            items += kb_walk(rel, depth, KB_INDEX_MAX - len(items))
        kb_index_memo.update(t=time.time(), items=items, more=len(items) >= KB_INDEX_MAX)
        return kb_index_memo


def kb_home():
    shelves = []
    for rel, label, about in KB_SHELVES:
        p = kb_path(rel)
        if not p or not p.is_dir(): continue
        try: n = sum(kb_name_ok(e.name) for e in os.scandir(p))
        except OSError: n = 0
        shelves.append({"path": rel, "label": label, "about": about, "n": n})
    busy = [x for x in kb_index()["items"] if x[0].split("/")[0] in ("kb", "modules", "core", "investigations") and x[0].endswith((".md", ".py", ".sh", ".txt", ".json", ".go", ".mjs", ".js"))]
    recent = []
    for path, t in sorted(busy, key=lambda x: -x[1])[:14]:
        f = HOME / path
        recent.append({"path": path, "name": f.name, "t": int(t), "title": kb_title(f) if f.suffix == ".md" else ""})
    return {"shelves": shelves, "recent": recent}


def kb_search(q):
    """Names first, then lines inside files. A plain string, any case; a hard time limit, and never in worktrees or toolchains."""
    q = str(q or "").strip()[:200]
    if len(q) < 2: return {"q": q, "names": [], "hits": [], "more": False}
    ranked = kb_ranked(q)
    ws = q.lower().split(); names = []
    for path, t in kb_index()["items"]:
        low = path.lower()  # the index only holds names kb_walk let through, and no links
        if all(w in low for w in ws): names.append({"path": path, "t": int(t)})
        if len(names) >= 60: break
    names.sort(key=lambda x: (not x["path"].startswith("kb/"), x["path"].count("/"), -x["t"]))
    if not KB_SEARCH.acquire(timeout=4): return {"q": q, "names": names, "hits": [], "more": True, "busy": True, "ranked": ranked}
    hits, more, deadline = [], False, time.time() + 6
    try:
        dirs = [d for d in ("kb", "core", "modules", "investigations", "archive", "files") if (HOME / d).is_dir() and not (HOME / d).is_symlink()]
        try: dirs += [f"projects/{e.name}" for e in os.scandir(PROJECTS) if kb_name_ok(e.name) and e.is_dir(follow_symlinks=False) and not kb_is_worktree(Path(e.path))]
        except OSError: pass
        skip = sorted(KB_SKIP)
        cmd = (["rg", "--no-config", "--no-ignore-parent", "-FinH", "--no-heading", "--color=never", "--max-count=3", "--max-filesize=1M", "--max-columns=400", *[f"--glob=!{d}" for d in skip], "-e", q, "--", *dirs]
               if shutil.which("rg") else ["grep", "-rFinIH", "-m3", *[f"--exclude-dir={d}" for d in skip], "--exclude-dir=.*", "-e", q, "--", *dirs])
        if not dirs: return {"q": q, "names": names, "hits": [], "more": False, "ranked": ranked}
        pr = subprocess.Popen(cmd, cwd=HOME, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, errors="replace")
        timer = threading.Timer(6, pr.kill); timer.start()
        try:
            for line in pr.stdout:
                m = re.match(r"([^:\n]+):(\d+):(.*)", line)
                if not m or not kb_path(m[1]): continue
                if len(hits) >= KB_HITS_MAX: more = True; break
                hits.append({"path": m[1], "line": int(m[2]), "text": KB_BLOB.sub("[hidden]", redact(m[3]))[:400]})
            more = more or time.time() > deadline
        finally:
            timer.cancel(); pr.kill(); pr.wait(); pr.stdout.close()
    except OSError: pass
    finally: KB_SEARCH.release()
    return {"q": q, "names": names, "hits": hits, "more": more, "ranked": ranked}


# Ranked search and the notes map. Ranked search reuses kb's own index (~/core/kb.py, SQLite FTS5 under ~/.cache/hive-kb): the same
# passages agents get from `kb find`, with snippets. No kb.py or no index yet: the page falls back to kb_search's plain matches.
kb_tool_memo = {"mod": None, "tried": 0.0, "fresh": 0.0, "busy": False}
kb_map_memo = {"t": 0, "v": None}


def kb_tool():
    """kb.py loaded as a module, or None. Tried again at most once a minute."""
    m = kb_tool_memo
    if m["mod"] or time.time() - m["tried"] < 60: return m["mod"]
    m["tried"] = time.time(); f = HOME / "core" / "kb.py"
    if not f.is_file(): return None
    try:
        spec = importlib.util.spec_from_file_location("h2_kb_tool", f); mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
        m["mod"] = mod
    except Exception: m["mod"] = None
    return m["mod"]


def kb_freshen(mod):
    """Bring kb's index up to date in the background, at most once a minute; a search reads what is there."""
    m = kb_tool_memo
    if m["busy"] or time.time() - m["fresh"] < 60: return
    m["busy"], m["fresh"] = True, time.time()
    def run():
        try:
            c = mod.connect()
            try: mod.refresh(c)
            finally: c.close()
        except Exception: pass
        finally: m["busy"] = False
    threading.Thread(target=run, daemon=True).start()


def kb_searchable(path):
    """A file plain search would look in: kb_path's guards, under the same roots, never in a worktree or a skipped folder."""
    parts = path.split("/")
    if not kb_path(path) or parts[0] not in ("kb", "core", "modules", "investigations", "archive", "files", "projects") or KB_SKIP & set(parts[:-1]): return False
    d = HOME
    for x in parts[:-1]:
        d = d / x
        if kb_is_worktree(d): return False
    return True


def kb_ranked(q, n=40):
    """kb's best passages for q: [{path, line, title, text, source, date, thread, post}], or None when there is no index to ask."""
    mod = kb_tool()
    if not mod or not os.path.isfile(mod.DB):
        if mod: kb_freshen(mod)
        return None
    kb_freshen(mod)
    try:
        c = mod.connect()
        try:
            if not c.execute("SELECT 1 FROM doc LIMIT 1").fetchone(): return None
            hits, tried, words, total = mod.search(c, q)
            out = []
            for x in mod.diversify(hits, n * 2):
                if len(out) >= n: break
                path = x["path"]; thread = post = None
                if path.startswith("board:"):
                    r = c.execute("SELECT title FROM doc WHERE path=?", (path,)).fetchone()
                    thread, post = (r[0] if r else "")[7:], path[6:]
                elif not kb_searchable(path): continue
                text, line = mod.snippet(c, x, words, 240)
                title = "" if thread else kb_title(HOME / path) if path.endswith(".md") else ""
                out.append({"path": path, "line": line, "text": KB_BLOB.sub("[hidden]", redact(text)), "title": title, "source": x["source"],
                            "date": x["date"], "thread": thread, "post": post})
            return {"passages": out, "words": words, "total": total, "spelling": next((t[10:] for t in tried if t.startswith("spelling: ")), None)}
        finally: c.close()
    except Exception: return None


def kb_sources():
    """What ranked search covers: files and passages per source in kb's index."""
    mod = kb_tool()
    if not mod or not os.path.isfile(mod.DB): return []
    try:
        c = mod.connect()
        try: return [{"source": s, "files": n, "t": int(t or 0)} for s, n, t in c.execute("SELECT source, count(*), max(mtime) FROM doc GROUP BY source ORDER BY count(*) DESC")]
        finally: c.close()
    except Exception: return []


KB_LINK = re.compile(r"\]\(([^)\s#]+\.md)(?:#[^)]*)?\)|~/kb/([\w./-]+\.md)|\[\[([\w.-]+)\]\]|`([\w./-]+\.md)`")


def kb_topic(rel, prefixes):
    parts = rel.split("/")
    if len(parts) > 2: return parts[1]
    first = re.split(r"[-_.]", parts[-1])[0].lower()
    return first if prefixes.get(first, 0) >= 2 else "general"


def kb_map():
    """Every note under ~/kb: title, size, time, topic, and the notes it links to. At most a minute old."""
    if time.time() - kb_map_memo["t"] < 60 and kb_map_memo["v"]: return kb_map_memo["v"]
    notes = {}
    for rel, t in kb_walk("kb", 6, 4000):
        if rel.endswith(".md") and "/boards-site-pdfs/" not in rel: notes[rel] = t
    by_name, by_stem, prefixes = {}, {}, {}
    for rel in notes:
        name = rel.rsplit("/", 1)[-1]; by_name.setdefault(name, rel); by_stem.setdefault(name[:-3].lower(), rel)
        if rel.count("/") == 1: k = re.split(r"[-_.]", name)[0].lower(); prefixes[k] = prefixes.get(k, 0) + 1
    out = []
    for rel, t in notes.items():
        f = HOME / rel
        try:
            with f.open(errors="replace") as src: text = src.read(KB_TEXT_MAX)
        except OSError: continue
        links = set()
        for m in KB_LINK.finditer(text):
            if m[1]:
                target = m[1][2:] if m[1].startswith("~/") else None
                if target is None:
                    parts = []
                    for part in (rel.rsplit("/", 1)[0] + "/" + m[1]).split("/"):
                        if part == "..": parts and parts.pop()
                        elif part not in ("", "."): parts.append(part)
                    target = "/".join(parts)
            elif m[2]: target = "kb/" + m[2]
            elif m[3]: target = by_stem.get(m[3].lower())
            else: target = by_name.get(m[4].rsplit("/", 1)[-1]) if "/" not in m[4] or m[4].startswith(("kb/", "~/kb/", "history/")) else None
            if target in notes and target != rel: links.add(target)
        out.append({"path": rel, "title": kb_title(f, text), "size": len(text), "t": int(t), "topic": kb_topic(rel, prefixes), "links": sorted(links)})
    back = {}
    for x in out:
        for l in x["links"]: back.setdefault(l, []).append(x["path"])
    for x in out: x["back"] = sorted(back.get(x["path"], []))
    out.sort(key=lambda x: x["path"])
    v = {"notes": out, "sources": kb_sources()}
    kb_map_memo.update(t=time.time(), v=v)
    return v


kb_hist_memo, kb_blob_memo, kb_hist_lock = {"t": 0, "head": "", "v": None}, {}, threading.Lock()
KB_STATED = re.compile(r"^Filed (\d{4}-\d\d-\d\d) by ([\w-]+)\.|^## (.+?) \((\d{4}-\d\d-\d\d), ([\w-]+)\)\s*$", re.M)
KB_SUPER = re.compile(r"supersed|replaced by|replaces\b", re.I)
KB_GAP = 1800  # a stretch this long with no backup commit at all is shown as unrecorded


def kb_git(*args, data=None):
    try: r = subprocess.run(["git", "-C", str(HOME), *args], input=data, capture_output=True, timeout=30)
    except (OSError, subprocess.SubprocessError): return b""
    return r.stdout if r.returncode == 0 else b""


def kb_blobs(shas):
    """{sha: (title, lines, [(group, text, superseding line)], [stated])} for note versions, read once each and kept."""
    want = [s for s in dict.fromkeys(shas) if s not in kb_blob_memo]
    if want:
        out, i = kb_git("cat-file", "--batch", data=("\n".join(want) + "\n").encode()), 0
        while i < len(out):
            nl = out.find(b"\n", i)
            if nl < 0: break
            head = out[i:nl].split(); i = nl + 1
            if len(head) < 3: continue
            n = int(head[2]); text = out[i:i + min(n, KB_TEXT_MAX)].decode(errors="replace"); i += n + 1
            raw = []
            for line in text.splitlines():
                sup = bool(KB_SUPER.search(line))
                for m in KB_LINK.finditer(line): raw.append((m.lastindex, m[m.lastindex], sup))
            stated = []
            for m in KB_STATED.finditer(text):
                stated.append({"day": m[1], "who": m[2], "what": "filed"} if m[1] else {"day": m[4], "who": m[5], "what": redact(m[3])[:120]})
            kb_blob_memo[head[0].decode()] = (kb_title(None, text), text.count("\n") + 1, raw, stated)
    return kb_blob_memo


def kb_resolve(rel, g, v, notes, by_name, by_stem):
    """A link's target note and whether the note wrote its path (documented) or only a name we matched (inferred)."""
    if g == 1:
        if v.startswith("~/"): return v[2:], "doc"
        parts = []
        for part in (rel.rsplit("/", 1)[0] + "/" + v).split("/"):
            if part == "..": parts and parts.pop()
            elif part not in ("", "."): parts.append(part)
        return "/".join(parts), "doc"
    if g == 2: return "kb/" + v, "doc"
    if g == 3: return by_stem.get(v.lower()), "inf"
    if "/" in v and not v.startswith(("kb/", "~/kb/", "history/")): return None, "inf"
    if v.startswith(("kb/", "~/kb/")): return "kb/" + v.split("kb/", 1)[1], "doc"
    return by_name.get(v.rsplit("/", 1)[-1]), "inf"


def kb_note_ok(rel): return rel.startswith("kb/") and rel.endswith(".md") and "/boards-site-pdfs/" not in rel and not rel.startswith("kb/_dropped/")


def kb_history():
    """How ~/kb changed, from what was recorded: backup commits (recorded time, minutes apart), `kb file` records
    (exact), dates the notes state themselves (event time, a day), links written in them, and stretches with no record."""
    with kb_hist_lock:
        if time.time() - kb_hist_memo["t"] < 60 and kb_hist_memo["v"]: return kb_hist_memo["v"]
        head = kb_git("rev-parse", "HEAD").strip().decode()
        if head and head == kb_hist_memo["head"] and kb_hist_memo["v"]:
            kb_hist_memo["t"] = time.time(); return kb_hist_memo["v"]
        commits, cur = [], None
        for line in kb_git("log", "--reverse", "--format=C %h %ct", "--raw", "-M", "--no-abbrev", "--", "kb").decode(errors="replace").splitlines():
            if line.startswith("C "):
                _, h, t = line.split(); cur = {"c": h[:10], "t": int(t), "ch": []}; commits.append(cur)
            elif line.startswith(":") and cur:
                meta, _, paths = line.partition("\t"); m = meta.split(); paths = paths.split("\t")
                if m[1] == "120000": continue
                cur["ch"].append((m[4][0], m[3], paths))
        if not commits:
            v = {"start": None, "now": int(time.time()), "notes": [], "links": [], "gaps": [], "commits": 0}
            kb_hist_memo.update(t=time.time(), head=head, v=v); return v
        start, ever = commits[0]["t"], set()
        for c in commits:
            for k, sha, paths in c["ch"]:
                for p in paths:
                    if kb_note_ok(p): ever.add(p)
        by_name, by_stem, prefixes = {}, {}, {}
        for rel in sorted(ever):
            name = rel.rsplit("/", 1)[-1]; by_name.setdefault(name, rel); by_stem.setdefault(name[:-3].lower(), rel)
            if rel.count("/") == 1: k = re.split(r"[-_.]", name)[0].lower(); prefixes[k] = prefixes.get(k, 0) + 1
        blobs = kb_blobs(sha for c in commits for k, sha, paths in c["ch"] if k != "D" and set(sha) != {"0"})
        notes, live, links, open_ = {}, {}, [], {}

        def note(p):
            return notes.setdefault(p, {"path": p, "topic": kb_topic(p, prefixes), "title": "", "spans": [], "v": [], "moves": [], "filed": [], "stated": [], "pre": False})

        def close(p, t):
            if p in live: note(p)["spans"][-1][1] = t; del live[p]
            for key in [k for k in open_ if k[0] == p]: links[open_.pop(key)]["spans"][-1][1] = t

        def version(p, sha, c):
            n = note(p); b = blobs.get(sha)
            if p not in live: n["spans"].append([c["t"], None]); live[p] = 1
            if not b: n["v"].append([c["t"], None, c["c"]]); return
            n["title"] = b[0] or n["title"]; n["v"].append([c["t"], b[1], c["c"]])
            for s in b[3]:
                if s not in n["stated"]: n["stated"].append(s)
            now = {}
            for g, val, sup in b[2]:
                to, kind = kb_resolve(p, g, val, ever, by_name, by_stem)
                if to in ever and to != p:
                    key = (p, to, kind); now[key] = now.get(key, False) or sup
            for key in [k for k in open_ if k[0] == p and k not in now]: links[open_.pop(key)]["spans"][-1][1] = c["t"]
            for key, sup in now.items():
                if key not in open_:
                    old = next((i for i, x in enumerate(links) if (x["a"], x["b"], x["kind"]) == key), None)
                    if old is None: links.append({"a": key[0], "b": key[1], "kind": key[2], "sup": False, "spans": [], "first": c["c"]}); old = len(links) - 1
                    links[old]["spans"].append([c["t"], None]); open_[key] = old
                links[open_[key]]["sup"] = links[open_[key]]["sup"] or sup

        for i, c in enumerate(commits):
            for k, sha, paths in c["ch"]:
                if k == "R":
                    a, b = paths[0], paths[-1]
                    if kb_note_ok(a):
                        close(a, c["t"]); note(a)["moves"].append({"t": c["t"], "to": b, "c": c["c"], "dropped": b.startswith("kb/_dropped/")})
                    if kb_note_ok(b):
                        version(b, sha, c); note(b)["moves"].append({"t": c["t"], "from": a, "c": c["c"]})
                    continue
                p = paths[-1]
                if not kb_note_ok(p): continue
                if k == "D": close(p, c["t"]); continue
                version(p, sha, c)
                if i == 0: note(p)["pre"] = True
        try:
            for line in (Path(os.environ.get("KB_HOME", HOME)) / ".cache/hive-kb/queries.jsonl").open(errors="replace"):
                try: r = json.loads(line)
                except ValueError: continue
                p = r.get("filed")
                if not isinstance(p, str) or p not in notes: continue
                try: t = int(datetime.fromisoformat(str(r.get("at"))).timestamp())
                except ValueError: continue
                notes[p]["filed"].append({"t": t, "who": redact(str(r.get("who", "")))[:40], "added": bool(r.get("to"))})
        except OSError: pass
        times = [int(x) for x in kb_git("log", "--format=%ct", f"--since={start}").split()]
        times.sort(); now = int(time.time())
        gaps = []
        for a, b in zip(times, times[1:] + [now]):
            if b - a <= KB_GAP: continue
            if gaps and gaps[-1][1] == a: gaps[-1][1] = b
            else: gaps.append([a, b])
        for n in notes.values(): n["title"] = redact(n["title"]) or n["path"].rsplit("/", 1)[-1]
        v = {"start": start, "now": now, "commits": len(commits), "gap": KB_GAP, "gaps": gaps,
             "notes": sorted(notes.values(), key=lambda n: n["path"]), "links": links}
        kb_hist_memo.update(t=time.time(), head=head, v=v)
        return v


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
    return {k: c.get(k) for k in ("max_sessions", "keeper_every_s", "keeper_idle_s", "usage_stop_pct", "auto_update", "update_every_s")}


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
    if "update_every_s" in d:
        v = d["update_every_s"]
        if type(v) is not int or not 300 <= v <= 7 * 86400: raise ValueError("how often to look for a release is 5 minutes to 7 days")
        new["update_every_s"] = v
    if not new: raise ValueError("nothing to change")
    with _cfg_lock:
        c = jload(HOME / "core" / "config.json")
        if not isinstance(c, dict): raise ValueError("core/config.json can't be read")
        c.update(new)
        t = HOME / "core" / f".config.json.{os.getpid()}.{threading.get_ident()}"
        t.write_text(json.dumps(c, indent=2) + "\n"); t.replace(HOME / "core" / "config.json")
    return config_view()


# The hive's name and logo, from Settings. Only how this app looks: nothing else reads them, and agents keep their names.
BRAND = STATE / "brand.json"
BRAND_LOGO = STATE / "brand-logo"
LOGO_MAX = 2 * 2**20
NAME_MAX = 40


def logo_type(b):
    """The picture's type from its bytes, never from the name it came with. None if it isn't one we show."""
    if b.startswith(b"\x89PNG\r\n\x1a\n"): return "image/png"
    if b.startswith(b"\xff\xd8\xff"): return "image/jpeg"
    if b[:6] in (b"GIF87a", b"GIF89a"): return "image/gif"
    if b[:4] == b"RIFF" and b[8:12] == b"WEBP": return "image/webp"
    if re.match(rb"(?:\xef\xbb\xbf)?\s*(?:<\?xml[^>]*>\s*|<!--.*?-->\s*|<!DOCTYPE[^>]*>\s*)*<svg[\s>]", b[:8192], re.S): return "image/svg+xml"
    return None


def brand_view():
    """What /api/app carries on every route. logo is an address that changes with the file, so a new one shows at once and an old one can be cached."""
    d = jload(BRAND)
    name = d.get("name") if isinstance(d, dict) else None
    try: logo = f"/api/brand/logo?v={BRAND_LOGO.stat().st_mtime_ns}"
    except OSError: logo = None
    return {"name": name if isinstance(name, str) and name else None, "logo": logo}


def set_brand(d):
    """{name}: a name, or empty for the default. {logo: null}: back to no logo."""
    if "name" not in d and d.get("logo", 0) is not None: raise ValueError("nothing to change")
    if "name" in d:
        v = d["name"]
        if v is not None and not isinstance(v, str): raise ValueError("the name is text")
        v = " ".join((v or "").split())
        if len(v) > NAME_MAX: raise ValueError(f"the name can be up to {NAME_MAX} characters")
        if any(not c.isprintable() for c in v): raise ValueError("the name has characters that can't be shown")
        STATE.mkdir(parents=True, exist_ok=True)
        t = STATE / f".brand.json.{os.getpid()}.{threading.get_ident()}"
        t.write_text(json.dumps({"name": v}) + "\n"); t.replace(BRAND)
    if "logo" in d and d["logo"] is None: BRAND_LOGO.unlink(missing_ok=True)
    return brand_view()


def set_logo(b):
    if not logo_type(b): raise ValueError("a logo has to be a PNG, JPEG, GIF, WebP or SVG picture")
    STATE.mkdir(parents=True, exist_ok=True)
    t = STATE / f".brand-logo.{os.getpid()}.{threading.get_ident()}"
    t.write_bytes(b); t.replace(BRAND_LOGO)
    return brand_view()


def starter_up():
    try: return time.time() - (STATE / "starter.json").stat().st_mtime < 60  # the door's test
    except OSError: return False


def starter_do(what):
    """Start or stop the service that starts agents. Stopping it lets running agents finish their turn."""
    if what not in ("start", "stop", "restart"): raise ValueError("start, stop or restart")
    try: r = subprocess.run(["systemctl", "--user", what, "hive-starter"], capture_output=True, text=True, timeout=30, env=child_env())
    except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"no systemd here: run python3 ~/core/starter.py ({type(e).__name__})")
    if r.returncode: raise ValueError(short(r.stderr, 300) or f"systemctl {what} failed")


def update_view():
    """What Settings shows about releases: update.py's own record, and whether it's running now (it holds this lock)."""
    st = jload(STATE / "update.json", {})
    if not isinstance(st, dict): st = {}
    running = False
    try:
        with open(STATE / "update.lock", "a") as f:
            try: fcntl.flock(f, fcntl.LOCK_SH | fcntl.LOCK_NB)
            except OSError: running = True
    except OSError: pass
    tag = st.get("tag")
    if not tag and INSTALLED.exists():  # the timer hasn't run yet on a fresh install
        try: tag = subprocess.run(["git", "-C", str(HOME / "core"), "describe", "--tags", "--abbrev=0"], capture_output=True, text=True, timeout=10).stdout.strip() or None
        except (OSError, subprocess.SubprocessError): pass
    c = cfg()
    return {"installed": INSTALLED.exists(), "tag": tag, "newest": st.get("newest"), "checked": st.get("checked"), "at": st.get("at"), "failed": st.get("failed") or [],
            "error": st.get("error") or st.get("stuck"), "running": running, "auto_update": c.get("auto_update", True), "update_every_s": c.get("update_every_s") or 300}


def update_do(what):
    """Check now waits for the answer. Update now runs outside this app, in the timer's own service: an update restarts this app."""
    if not INSTALLED.exists(): raise ValueError("this Hive wasn't installed from a release, so it has nothing to update")
    f = HOME / "core" / "update.py"
    if what == "check":
        try: r = subprocess.run([sys.executable, "-I", str(f), "--check"], env=child_env(), capture_output=True, text=True, timeout=120)
        except OSError as e: raise ValueError(f"couldn't look for a release: {e}")
        if r.returncode: raise ValueError(short(r.stderr, 300))
    elif what == "install":
        now = STATE / "update-now"
        now.write_text("")
        try: done = subprocess.run(["systemctl", "--user", "start", "--no-block", "hive-update.service"], capture_output=True, timeout=30, env=child_env()).returncode == 0
        except (OSError, subprocess.SubprocessError): done = False
        if not done:
            now.unlink(missing_ok=True)
            try: subprocess.Popen([sys.executable, "-I", str(f)], env=child_env(), start_new_session=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except OSError as e: raise ValueError(f"couldn't start the update: {e}")
    else: raise ValueError("check or install")
    return update_view()


def send_feedback(d):
    """A consumer Hive's Send feedback: to the makers through the report module, which scrubs the comment and keeps a copy. Never onto this board."""
    f = HOME / "modules" / "report" / "report.py"
    if not SETTINGS.is_file() or not f.is_file(): raise ValueError("this Hive has no way to send feedback to the makers")
    text, page, image = d.get("text"), d.get("page"), d.get("image")
    if not isinstance(text, str) or len(text) > 8000: raise ValueError("a comment can be up to 8,000 characters")
    if image is not None and not (isinstance(image, str) and image.startswith("/9j/") and len(image) <= 450000): raise ValueError("the picture is too large to send")
    if not text.strip() and not image: raise ValueError("write a comment or add a picture first")
    try: r = subprocess.run([sys.executable, "-I", str(f), "feedback"], input=json.dumps({"text": text, "page": page, "image": image}), env=child_env(), capture_output=True, text=True, timeout=45)
    except OSError as e: raise ValueError(f"couldn't send it: {e}")
    if r.returncode: raise ValueError(short(r.stderr, 300).removeprefix("not sent: ") or "couldn't send it")
    return {"ok": True}



def issue_link(d):
    """A consumer Hive's "Open as a GitHub issue": a scrubbed, prefilled new-issue link on the project's repo, which the person
    reviews and posts from their own GitHub account. Nothing is sent from here and no token is used."""
    f = HOME / "modules" / "report" / "report.py"
    if not SETTINGS.is_file() or not f.is_file(): raise ValueError("this Hive has no repository to open issues on")
    text, page = d.get("text"), d.get("page")
    if not isinstance(text, str) or not text.strip() or len(text) > 8000: raise ValueError("write what went wrong first (up to 8,000 characters)")
    page = page if isinstance(page, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,40}", page) else "app"
    title = " ".join(text.split())[:80] or "Problem on the %s page" % page
    try: r = subprocess.run([sys.executable, "-I", str(f), "issue", title], input="%s\n\n(from the %s page)" % (text, page), env=child_env(), capture_output=True, text=True, timeout=20)
    except OSError as e: raise ValueError(f"couldn't make the link: {e}")
    m = re.search(r"https://github\.com/\S+/issues/new\?\S+", r.stdout)
    if not m: raise ValueError(short(r.stdout or r.stderr, 300) or "couldn't make the link")
    return {"ok": True, "url": m.group(0)}


def report_now():
    """Sharing was just turned on: send now instead of at the next 6-hour timer, so the makers and the person needn't wonder. Best effort; the timer is still there."""
    f = HOME / "modules" / "report" / "report.py"
    if not f.is_file(): return
    try: subprocess.Popen([sys.executable, "-I", str(f), "send"], env=child_env(), start_new_session=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except OSError: pass


def share_copy():
    """Settings' Send a copy now: the same scrubbed bundle as `hive-report bundle --yes`, for someone who never opens a terminal. The report module refuses unless share.everything is on."""
    f = HOME / "modules" / "report" / "report.py"
    if not SETTINGS.is_file() or not f.is_file(): raise ValueError("this Hive has no way to send a copy to the makers")
    try: r = subprocess.run([sys.executable, "-I", str(f), "bundle", "--yes"], env=child_env(), capture_output=True, text=True, timeout=90)
    except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"couldn't send it: {e}")
    said = r.stdout.strip()
    if r.returncode or not said.startswith("bundle sent"): raise ValueError(short(said or r.stderr, 300) or "couldn't send it")
    return {"ok": True}


def module_repo(name):
    """The GitHub page of a module that is its own clone, or None. Built from owner/repo alone, so a token in the remote's URL can't reach the page."""
    d = HOME / "modules" / name
    if not (d / ".git").exists(): return None  # otherwise git would answer for ~, the repo around it
    r = subprocess.run(["git", "-C", str(d), "config", "--get", "remote.origin.url"], capture_output=True, text=True, timeout=10)
    m = re.fullmatch(r"(?:https://(?:[^@/]+@)?github\.com/|ssh://git@github\.com/|git@github\.com:)([\w.-]+/[\w.-]+?)(?:\.git)?/?", r.stdout.strip())
    return f"https://github.com/{m[1]}" if m else None


def module_where(name):
    """Where a module's code lives, for the page: "github" (repo holds the link), "git" (a remote elsewhere, never named here, since a URL can hold a token), or "local"."""
    d = HOME / "modules" / name
    if module_repo(name): return "github"
    if not (d / ".git").exists(): return "local"
    r = subprocess.run(["git", "-C", str(d), "config", "--get", "remote.origin.url"], capture_output=True, text=True, timeout=10)
    return "git" if r.stdout.strip() else "local"


def modules_view():
    """`hive module list --json`, with where each installed one's code lives: {name, state, source, about, needs, default, recommended, repo, where}."""
    r = subprocess.run([str(HIVE_BIN), "module", "list", "--json"], capture_output=True, text=True, env=child_env(), cwd=str(HOME), timeout=30)
    try: rows = json.loads(r.stdout)
    except ValueError: return []
    for m in rows:
        m["repo"] = module_repo(m["name"])
        m["where"] = module_where(m["name"]) if m["state"] == "installed" else None
    return rows


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
        sync_accounts()

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


class DesignRoom:
    """Claude Design from the Design page: a real interactive Claude Code on a login, because /design needs one (artifacts are off under -p and for
    agents). The person starts it and types into it. It runs in tmux in its own unit, so it outlives a dash restart and has its own memory ceiling
    (the dash's is 300M). One at a time, fixed command, a short list of keys, gone after 6 hours. No agent drives it: see kb/claude-design-access.md."""
    SOCK, CONF, DIR, UNIT = HOME / ".hive" / "design.tmux", HOME / ".hive" / "design.tmux.conf", HOME / "designs", "hive-design"
    KEYS = {"enter": "Enter", "esc": "Escape", "up": "Up", "down": "Down", "tab": "Tab", "stop": "C-c"}
    LINK = re.compile(r"https://claude\.ai/(?:code/)?artifact/[\w-]+")
    # Claude Code's own words on the screen. Standalone Claude Design, and with it this sign-in and grant, closes by this date (its message, 2026-11-03).
    NEEDS_SIGNIN, SIGNED_IN, GRANTED, REVOKED = "run /design-login", "Design-system access authorized", "Design agent access granted", "Design agent access revoked"
    CONNECT_UNTIL = "2026-12-14"
    # The sign-in page Claude Code gives, on Anthropic's hosts only, because anything can print a link on that screen. No sample of it has survived, so this is forgiving.
    SIGNIN_LINK = re.compile(r"https://(?:[\w-]+\.)*(?:claude\.ai|claude\.com|anthropic\.com)/[^\s\"'<>]*")
    STEP_KEEP_S = 120  # how long "approved" or "that failed" stays on the page

    def __init__(self):
        self.lock = threading.Lock()
        self.connecting = threading.Lock()
        self.who = None
        self.step = None

    def set_step(self, login, phase, why=""):
        self.step = {"login": login, "phase": phase, "why": why, "at": time.time()}

    @classmethod
    def signin_link(cls, screen):
        """The sign-in link the last /design-login put on the screen, or None. Claude Code can break a long link across lines, so a full line runs on into the next."""
        at = max(screen.rfind("\u276f /design-login"), screen.rfind("> /design-login"))
        if at < 0: at = screen.rfind("/design-login")
        if at < 0: return None
        lines, url = screen[at:].split("\n"), None
        for i, line in enumerate(lines[1:], 1):
            m = cls.SIGNIN_LINK.search(line)
            if not m: continue
            url = m.group(0)
            if re.match(r"https://[^/]+/(?:code/)?artifact/", url): url = None; continue  # a design link on the screen is not a sign-in page
            for more in lines[i + 1:]:
                if len(line.rstrip()) < 90 or not re.fullmatch(r"\s*[\w\-.~:/?#\[\]@!$&()*+,;=%]+\s*", more): break
                url += more.strip(); line = more
        return url

    @staticmethod
    def design_on(plan):
        """Can this plan use Claude Design? Anthropic's admin guide, read 2026-11-03: Free, Pro, Max, Ultra, Team and Enterprise, and on the last two an admin can turn it off."""
        word = (plan or "").split(" ")[0]
        if word in ("Free", "Pro", "Max", "Ultra"): return "yes"
        if word in ("Team", "Enterprise"): return "admin"
        return "unknown"

    def accounts(self, connected, links):
        """Every login, including those it can't start on now, and why not. Never the path: this goes to the page."""
        used = {l.get("login") for l in links} | set(connected)
        open_now = {l["name"] for l in self.open_logins()}
        out = []
        for l in logins():
            line = (l.get("stop") or {}).get("weekly") or (l.get("stop") or {}).get("global") or 100
            out.append({"name": l["name"], "plan": l.get("plan"), "design": self.design_on(l.get("plan")), "used_here": l["name"] in used, "connected": l["name"] in connected,
                        "weekly": l.get("weekly"), "stop": line, "open": l["name"] in open_now, "closed_why": "" if l["name"] in open_now else "paused" if l.get("paused") else "past its weekly line"})
        return out

    def tmux(self, *args, stdin=None):
        return subprocess.run(["tmux", "-u", "-S", str(self.SOCK), "-f", str(self.CONF), *args], input=stdin, capture_output=True, text=True, errors="replace", timeout=10)

    @staticmethod
    def open_logins():
        """The logins it may start on: the same stops as agents, so not one that's paused or past its weekly line."""
        def room(l):
            line = (l.get("stop") or {}).get("weekly") or (l.get("stop") or {}).get("global") or 100
            return not l.get("paused") and (l.get("weekly") or 0) < line
        return [l for l in logins() if room(l)]

    def running(self):
        return self.SOCK.exists() and self.tmux("has-session", "-t", "design").returncode == 0

    def links(self, screen=""):
        """The designs seen so far, newest first, kept so they outlast the session."""
        path, have = self.DIR / "links.json", []
        try: have = json.loads(path.read_text())
        except (OSError, ValueError): pass
        new = [u for u in dict.fromkeys(self.LINK.findall(screen)) if u not in {x.get("url") for x in have}]
        if new:
            who = self.session_login()
            have = [{"url": u, "at": int(time.time()), **({"login": who} if who else {})} for u in reversed(new)] + have
            self.DIR.mkdir(exist_ok=True); path.write_text(json.dumps(have))
        return have

    def view(self):
        run, screen = self.running(), ""
        if run:
            screen = self.tmux("capture-pane", "-p", "-J", "-S", "-5000", "-t", "design").stdout.rstrip()[-200000:]
        links, connected = self.links(screen), self.connected(screen)
        step = self.step
        if step and (not run or (step["phase"] in ("done", "revoked", "failed") and time.time() - step["at"] > self.STEP_KEEP_S)): step = self.step = None
        if step and step["phase"] == "signin": step = {**step, "link": self.signin_link(screen)}
        return {"running": run, "screen": screen, "links": [l for l in links if not l.get("gone")], "accounts": self.accounts(connected, links), "dir": str(self.DIR), "step": step,
                "connected": connected, "session_login": self.session_login() if run else None, "can_connect": time.strftime("%Y-%m-%d") <= self.CONNECT_UNTIL, "connect_until": self.CONNECT_UNTIL, "agents_design": shutil.which("hive-design") is not None}

    def session_login(self):
        """The login the running session is on, from its unit's command line."""
        if self.who: return self.who
        r = subprocess.run(["systemctl", "--user", "show", self.UNIT + ".service", "-p", "ExecStart", "--value"], capture_output=True, text=True, timeout=10)
        self.who = next((l["name"] for l in logins() if f"CLAUDE_CONFIG_DIR={l['path']} " in r.stdout + " "), None)
        return self.who

    def connected(self, screen=""):
        """The logins whose Claude Design projects agents may use, as last seen on a session's screen, kept so they outlast it."""
        path, have = self.DIR / "connected.json", []
        try: have = json.loads(path.read_text())
        except (OSError, ValueError): pass
        got, gone = screen.rfind(self.GRANTED), screen.rfind(self.REVOKED)
        who = self.session_login() if got >= 0 or gone >= 0 else None
        if who and (got > gone) != (who in have):
            have = sorted(set(have) | {who}) if got > gone else [x for x in have if x != who]
            self.DIR.mkdir(exist_ok=True); path.write_text(json.dumps(have))
        return have

    def start(self, name):
        with self.lock:
            if self.running(): return {"ok": True}
            self.who = None
            hit = [l for l in self.open_logins() if l["name"] == name]
            if not hit: raise ValueError("that login isn't open: it's paused, past its weekly line, or not a login")
            self.DIR.mkdir(exist_ok=True)
            self.CONF.write_text("set -g status off\nset -g history-limit 20000\n")
            subprocess.run(["systemctl", "--user", "reset-failed", self.UNIT + ".service"], capture_output=True, timeout=10)
            # env -i: not the dash's environment. An API key there would bill the key and turn artifacts off.
            cmd = ["env", "-i", f"HOME={HOME}", f"PATH={os.environ.get('PATH', '')}", "TERM=xterm-256color", "LANG=C.UTF-8", f"TMPDIR={os.environ.get('TMPDIR') or HOME / '.cache' / 'hive-tmp'}", f"CLAUDE_CONFIG_DIR={hit[0]['path']}", "claude"]
            r = subprocess.run(["systemd-run", "--user", "--quiet", "--collect", f"--unit={self.UNIT}", "-p", "Type=forking", "-p", "MemoryMax=2G", "-p", "RuntimeMaxSec=21600", f"--working-directory={self.DIR}",
                                "tmux", "-u", "-S", str(self.SOCK), "-f", str(self.CONF), "new-session", "-d", "-s", "design", "-x", "110", "-y", "40", *cmd], capture_output=True, text=True, timeout=30)
            if r.returncode: raise ValueError("couldn't start it: " + (r.stderr.strip()[:300] or "no reason given"))
            return {"ok": True}

    def send(self, text):
        text = "".join(c for c in str(text or "") if c >= " " or c in "\n\t").strip()
        if not text or len(text) > 20000: raise ValueError("type something to send, under 20,000 characters")
        if not self.running(): raise ValueError("no session is running")
        with self.lock:
            self.tmux("load-buffer", "-b", "typed", "-", stdin=text)
            self.tmux("paste-buffer", "-d", "-p", "-b", "typed", "-t", "design")
            time.sleep(0.4)  # or Enter lands inside the paste and starts a new line
            self.tmux("send-keys", "-t", "design", "Enter")
        return {"ok": True}

    def grant(self, name):
        """The person's button, and only theirs: one press types the commands for them in the page's session, with every answer on its screen. Never called by anything else."""
        self.ready_on(name)
        threading.Thread(target=self.connect, daemon=True).start()
        return {"ok": True}

    def revoke(self, name):
        """Also the person's button and only theirs: takes agents' access back on this account."""
        self.ready_on(name)
        threading.Thread(target=self.take_back, daemon=True).start()
        return {"ok": True}

    def ready_on(self, name):
        """A session on that login. Consent is per account, so never type it into a session on another one."""
        if not self.running():
            self.start(name)
        elif name and name != self.session_login():
            raise ValueError(f"the session is on {self.session_login()}: end it first, so this goes to {name}")
        if not self.connecting.locked(): self.set_step(name or self.session_login(), "starting")

    def count(self, words):
        return self.tmux("capture-pane", "-p", "-J", "-S", "-5000", "-t", "design").stdout.count(words)

    def until(self, test, seconds):
        end = time.time() + seconds
        while time.time() < end:
            if self.running() and test(): return True
            time.sleep(self.TICK)
        return False

    TICK = 0.5

    def connect(self, ask_s=120, signin_s=330):
        """Try the grant. If it says to sign in, start the sign-in, which the person answers on the screen, and try again once it has worked. A second press while this runs does nothing."""
        if not self.connecting.acquire(blocking=False): return
        who = None
        try:
            who = self.session_login()
            if not self.until(lambda: "\u276f" in self.tmux("capture-pane", "-p", "-t", "design").stdout, 40):
                return self.set_step(who, "failed", "The session didn't get ready.")
            self.set_step(who, "asking")
            asked, done = self.count(self.NEEDS_SIGNIN), self.count(self.GRANTED)
            self.send("/design consent")
            if not self.until(lambda: self.count(self.NEEDS_SIGNIN) > asked or self.count(self.GRANTED) > done, ask_s):
                return self.set_step(who, "failed", "Claude didn't answer in time. The session's screen shows where it got to.")
            if self.count(self.GRANTED) > done: return self.set_step(who, "done")
            self.set_step(who, "signin")
            signed = self.count(self.SIGNED_IN)
            self.send("/design-login")
            if not self.until(lambda: self.count(self.SIGNED_IN) > signed, signin_s):
                return self.set_step(who, "failed", "The sign-in wasn't finished in time.")
            self.set_step(who, "asking")
            time.sleep(self.TICK); asked = self.count(self.NEEDS_SIGNIN); self.send("/design consent")
            if not self.until(lambda: self.count(self.NEEDS_SIGNIN) > asked or self.count(self.GRANTED) > done, ask_s) or self.count(self.GRANTED) <= done:
                return self.set_step(who, "failed", "You signed in, but Claude still didn't grant it. The session's screen has its answer.")
            self.set_step(who, "done")
        except (OSError, ValueError, subprocess.SubprocessError): self.set_step(who, "failed", "The session ended before this finished.")
        finally: self.connecting.release()

    def take_back(self, ask_s=120):
        if not self.connecting.acquire(blocking=False): return
        who = None
        try:
            who = self.session_login()
            if not self.until(lambda: "\u276f" in self.tmux("capture-pane", "-p", "-t", "design").stdout, 40):
                return self.set_step(who, "failed", "The session didn't get ready.")
            self.set_step(who, "revoking")
            gone = self.count(self.REVOKED)
            self.send("/design revoke")
            if self.until(lambda: self.count(self.REVOKED) > gone, ask_s): return self.set_step(who, "revoked")
            self.set_step(who, "failed", "Claude didn't confirm it. The session's screen shows where it got to.")
        except (OSError, ValueError, subprocess.SubprocessError): self.set_step(who, "failed", "The session ended before this finished.")
        finally: self.connecting.release()

    def forget(self, url):
        """Takes a design off the page's list. The design itself stays on claude.ai. Kept as gone, so a link still on the screen doesn't come back."""
        path = self.DIR / "links.json"
        with open(STATE / "design-links.lock", "w") as lock:  # hive-design writes this list too
            fcntl.flock(lock, fcntl.LOCK_EX)
            have = json.loads(path.read_text())
            if url not in {l.get("url") for l in have}: raise ValueError("that design isn't on the list")
            path.write_text(json.dumps([{**l, "gone": True} if l.get("url") == url else l for l in have]))
        return {"ok": True}

    def act(self, d):
        what = d.get("do")
        if what == "start": return self.start(d.get("login"))
        if what == "grant": return self.grant(d.get("login"))
        if what == "revoke": return self.revoke(d.get("login"))
        if what == "forget": return self.forget(d.get("url"))
        if what == "send": return self.send(d.get("text"))
        if what == "key":
            if d.get("key") not in self.KEYS: raise ValueError("that key isn't on the list")
            if not self.running(): raise ValueError("no session is running")
            self.tmux("send-keys", "-t", "design", self.KEYS[d["key"]]); return {"ok": True}
        if what == "stop":
            subprocess.run(["systemctl", "--user", "stop", self.UNIT + ".service"], capture_output=True, timeout=30); return {"ok": True}
        raise ValueError("start, send, key, grant, revoke, forget or stop")


DESIGN = DesignRoom()


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
            "claude": bool(shutil.which("claude")), "other": other_account()}


SETUP = CachedStale(setup_state, 20)  # set .t = 0 to read again. Stale, since every login is a `claude auth status`: up to 16 s on a busy machine, and the page has no tabs of its own until /api/app answers

# ---------- consumer setup: settings.json, the walkthrough's and Settings' file. The starter and the report module read it. ----------
# No file (Hive itself, and every install from before it) means today's behaviour: nothing here writes, and no route can create it.
SETTINGS = HOME / "settings.json"
def _agent_models():
    try: m = cfg().get("models")
    except Exception: m = None
    return tuple(m) if isinstance(m, list) and m and all(isinstance(x, str) for x in m) else ("haiku", "sonnet", "opus", "fable")
AGENT_MODELS = _agent_models()  # the Claude models a post may ask for too (config.json "models", the starter's MODELS)
PICK_MODELS = {**{m: m for m in AGENT_MODELS}, "astra": "codex:gpt-6-astra"}  # the message box's model menu
SHARE_KEYS = ("failures", "usage", "everything", "github")
_settings_lock = threading.Lock()


def settings_raw():
    try: v = json.loads(SETTINGS.read_text())
    except (OSError, ValueError): return None
    return v if isinstance(v, dict) else None


def settings_norm(v):
    """What the app shows. A missing field reads the way the contract says: sandbox on, power 2, nothing shared, setup done."""
    sh = v.get("share") if isinstance(v.get("share"), dict) else {}
    p = v.get("power")
    return {"power": p if type(p) is int and 1 <= p <= 3 else 2, "sandbox": "bare" if v.get("sandbox") == "bare" else "on",
            "model": v.get("model") if v.get("model") in AGENT_MODELS else cfg().get("spawn_model") or "opus",  # what a new agent starts on (the starter's spawn word handling)
            "share": {k: sh.get(k) is True for k in SHARE_KEYS}, "setup_done": v.get("setup_done") is not False}


def settings_brief():
    v = settings_raw()
    return settings_norm(v) if v is not None else None


def default_model():
    """What a new agent with no model asked for starts on: Settings' choice, else config.json's spawn_model."""
    v = (settings_raw() or {}).get("model")
    return v if v in AGENT_MODELS else cfg().get("spawn_model") or "opus"


def secrets_allowed(web, site=False):
    """site: an outside.sites name. Those need secrets_on itself: settings.json appearing must not hand a public name the secrets."""
    return web.get("secrets_on") is True or (not site and SETTINGS.is_file())


def set_settings(d):
    """The fields the app may change. Anything else already in the file stays, and share_url can't be set from here."""
    new = {}
    for k in d:
        if k not in ("power", "sandbox", "model", "share", "setup_done"): raise ValueError("that isn't a setting")
    if "power" in d:
        if type(d["power"]) is not int or not 1 <= d["power"] <= 3: raise ValueError("power is 1, 2 or 3")
        new["power"] = d["power"]
    if "sandbox" in d:
        if d["sandbox"] not in ("on", "bare"): raise ValueError("the sandbox is on or bare")
        new["sandbox"] = d["sandbox"]
    if "model" in d:
        if d["model"] not in AGENT_MODELS: raise ValueError("the model is " + ", ".join(AGENT_MODELS))
        new["model"] = d["model"]
    if "setup_done" in d:
        if type(d["setup_done"]) is not bool: raise ValueError("setup_done is true or false")
        new["setup_done"] = d["setup_done"]
    share = d.get("share")
    if "share" in d and (not isinstance(share, dict) or not share or any(k not in SHARE_KEYS or type(v) is not bool for k, v in share.items())):
        raise ValueError("share is failures, usage, everything and github, each on or off")
    if not new and not share: raise ValueError("nothing to change")
    with _settings_lock:
        cur = settings_raw()
        if cur is None: raise ValueError("this Hive has no settings file, so there's nothing to change")
        was = settings_norm(cur)["share"]
        if share: new["share"] = {**was, **share}
        cur.update(new)
        write_text_private(SETTINGS, json.dumps(cur, indent=1) + "\n")
    SETUP.t = SHARE_TEXT.t = GITHUB.t = 0
    if share and any(share.get(k) and not was.get(k) for k in ("failures", "usage", "github")): report_now()
    if d.get("setup_done") is True and settings_norm(cur)["share"].get("failures"): report_now()  # the first check-in: nothing goes before setup is passed
    if "setup_done" in d: sync_accounts()  # a Codex login made in a terminal counts too: nothing else would tell the starter
    return settings_page()


def sandbox_state():
    """The sandbox's own answer, from its check. Anything but a clear answer is `unknown`: the app then claims no limit."""
    try:
        r = subprocess.run([str(HIVE_BIN), "sandbox", "check", "--json"], env=child_env(), capture_output=True, text=True, timeout=20)
        v = json.loads(r.stdout)
        if r.returncode == 0 and isinstance(v, dict) and type(v.get("works")) is bool:
            return {"state": "works" if v["works"] else "broken", "why": str(v.get("why") or ""), "fix": str(v.get("fix") or "")}
    except (OSError, ValueError, subprocess.SubprocessError): pass
    return {"state": "unknown", "why": "", "fix": ""}


SANDBOX = Cached(sandbox_state, 60)


def share_text():
    """The report module's own words for the question, so this page can't say something it doesn't do. None: not installed."""
    f = HOME / "modules" / "report" / "report.py"
    if not f.is_file(): return None
    try:
        r = subprocess.run([sys.executable, "-I", str(f), "question"], env=child_env(), capture_output=True, text=True, timeout=10)
        return r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else None
    except (OSError, subprocess.SubprocessError): return None


SHARE_TEXT = Cached(share_text, 30)


def report_cmd(*args, timeout=30):
    """report.py's answer to a Settings question or press, as (returncode, stdout). None: the module isn't installed."""
    f = HOME / "modules" / "report" / "report.py"
    if not f.is_file(): return None
    try: r = subprocess.run([sys.executable, "-I", str(f), *args], env=child_env(), capture_output=True, text=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError): return None
    return r.returncode, r.stdout.strip()


def github_view():
    """Settings' GitHub part: whether gh is there and signed in, and every fix an agent drafted, exactly as it would go. None: the report module can't say."""
    r = report_cmd("github")
    try: v = json.loads(r[1]) if r and r[0] == 0 else None
    except ValueError: v = None
    return v if isinstance(v, dict) else None


GITHUB = Cached(github_view, 30)


def draft_do(d):
    """Settings' Send or Discard on one drafted fix. Only this press approves a draft; the reporter's timer sends it later, outside the sandbox."""
    did, do = d.get("id"), d.get("do")
    if not isinstance(did, str) or not re.fullmatch(r"[0-9]{6}-[0-9]{6}-[0-9a-f]{6}", did): raise ValueError("no such draft")
    if do not in ("send", "discard"): raise ValueError("do is send or discard")
    if do == "send" and not (settings_brief() or {}).get("share", {}).get("github"): raise ValueError("turn on GitHub issues first")
    r = report_cmd("approve" if do == "send" else "discard", did)
    if not r or r[0] or not r[1].startswith("approved" if do == "send" else "discarded"): raise ValueError((r and short(r[1], 300)) or "couldn't do that")
    GITHUB.t = 0
    if do == "send": report_now()
    return {"said": r[1], **settings_page()}


def has_root():
    """The starter holds outside models on an account with passwordless sudo; the app has to say so."""
    try: return subprocess.run(["sudo", "-n", "-l", "true"], env=child_env(), capture_output=True, timeout=5).returncode == 0
    except (OSError, subprocess.SubprocessError): return False


ROOT = Cached(has_root, 300)
ANSI = re.compile(r"\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]")
CODEX_CODE = re.compile(r"\b[A-Z0-9]{4}-[A-Z0-9]{4,}\b")
CODEX_LINK = re.compile(r"https://auth\.openai\.com/\S+")


def codex_env(home=None): return {**child_env(), "CODEX_HOME": str(home or HOME / ".codex"), "BROWSER": "true"}


def codex_homes():
    """Every Codex login of this account, as (provider name, folder): ~/.codex, then ~/.codex-2, ~/.codex-3 that Settings added.
    The starter treats codex, codex-2, ... as one family and moves a run to the next one with room."""
    more = sorted((int(p.name[7:]), p) for p in HOME.glob(".codex-*") if p.name[7:].isdigit() and int(p.name[7:]) >= 2 and p.is_dir())
    return [("codex", HOME / ".codex")] + [(f"codex-{n}", p) for n, p in more]


def codex_signed_in(home=None):
    home = Path(home or HOME / ".codex")
    if not shutil.which("codex") or not home.is_dir(): return False
    try: return subprocess.run(["codex", "login", "status"], env=codex_env(home), capture_output=True, timeout=10, stdin=subprocess.DEVNULL).returncode == 0
    except (OSError, subprocess.SubprocessError): return False


class CodexIn:
    """Whether each Codex folder is signed in, asked at most every 20 s per folder. clear() is how a change asks for the truth."""
    def __init__(self): self.lock, self.seen = threading.Lock(), {}

    def get(self, home=None):
        home = str(home or HOME / ".codex")
        with self.lock: v = self.seen.get(home)
        if v and time.time() - v[0] < 20: return v[1]
        r = codex_signed_in(home)
        with self.lock: self.seen[home] = (time.time(), r)
        return r

    def clear(self):
        with self.lock: self.seen.clear()


CODEX_IN = CodexIn()


def codex_logins():
    return [{"name": n, "signed_in": CODEX_IN.get(p), "extra": n != "codex"} for n, p in codex_homes()]


def config_providers():
    c = jload(HOME / "core" / "config.json", {})
    p = c.get("providers") if isinstance(c, dict) else None
    return p if isinstance(p, dict) else {}


class CodexSignIn:
    """`codex login --device-auth`: Codex prints a link and a one-time code, the person enters it in their own browser. One at a time,
    into one folder: ~/.codex, or a new ~/.codex-N for "Add another Codex login"."""
    def __init__(self): self.lock, self.proc, self.st = threading.Lock(), None, {"state": "idle"}

    def view(self):
        with self.lock: st = dict(self.st)
        return {**st, "installed": bool(shutil.which("codex")), "signed_in": CODEX_IN.get(), "logins": codex_logins()}

    def start(self, login=None, add=False):
        if not shutil.which("codex"): raise ValueError("Codex isn't installed for this account")
        with self.lock:
            if self.proc and self.proc.poll() is None: raise ValueError("a Codex sign-in is already running")
            homes = dict(codex_homes())
            if add:
                taken = {n for n, v in config_providers().items() if hand_made(n, v)}
                n = next(n for n in range(2, 100) if f"codex-{n}" not in taken and not (HOME / f".codex-{n}").exists())
                login, home = f"codex-{n}", HOME / f".codex-{n}"
            else:
                login = login or "codex"
                if login not in homes: raise ValueError("there's no such Codex login")
                home = homes[login]
            home.mkdir(mode=0o700, exist_ok=True)
            self.proc = subprocess.Popen(["codex", "login", "--device-auth"], env=codex_env(home), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                         stderr=subprocess.STDOUT, text=True, errors="replace", start_new_session=True)
            self.st = {"state": "starting", "login": login}
            threading.Thread(target=self.watch, args=(self.proc,), daemon=True).start()
            t = threading.Timer(960, self.stop, args=(self.proc,)); t.daemon = True; t.start()  # the code lasts 15 minutes
        return self.view()

    def watch(self, proc):
        for line in proc.stdout:
            text = ANSI.sub("", line)
            with self.lock:
                if self.proc is not proc: continue
                link, code = CODEX_LINK.search(text), CODEX_CODE.search(text)
                if link: self.st["url"] = link[0]
                if code and "code" not in self.st: self.st["code"] = code[0]
                if self.st.get("url") and self.st.get("code"): self.st["state"] = "waiting"
        rc = proc.wait()
        CODEX_IN.clear()
        with self.lock:
            if self.proc is not proc: return
            if self.st.get("state") == "cancelled": return
            login = self.st.get("login")
            self.st = {"state": "done", "login": login} if rc == 0 else {"state": "failed", "login": login, "error": "Codex's sign-in ended before it finished. Try again."}
        if rc == 0: sync_accounts()

    def stop(self, proc):
        if proc.poll() is None:
            try: os.killpg(proc.pid, signal.SIGTERM)
            except OSError: pass

    def cancel(self):
        with self.lock:
            proc = self.proc
            if proc and proc.poll() is None: self.st = {**self.st, "state": "cancelled"}
        if proc: self.stop(proc)
        return self.view()

    def remove(self, login):
        """Sign an added Codex login out and put its folder aside (its sessions with it), never ~/.codex. Runs on it start again elsewhere."""
        homes = dict(codex_homes())
        if login == "codex" or login not in homes: raise ValueError("only a Codex login added here can be removed")
        with self.lock:
            if self.proc and self.proc.poll() is None and self.st.get("login") == login: raise ValueError("cancel its sign-in first")
        home = homes[login]
        if shutil.which("codex"):
            try: subprocess.run(["codex", "logout"], env=codex_env(home), capture_output=True, timeout=15, stdin=subprocess.DEVNULL)
            except (OSError, subprocess.SubprocessError): pass
        aside = STATE / "removed"; aside.mkdir(parents=True, exist_ok=True); aside.chmod(0o700)
        home.rename(aside / f"{login}-{int(time.time())}")
        CODEX_IN.clear()
        with self.lock:
            if self.st.get("login") == login: self.st = {"state": "idle"}
        sync_accounts()
        return self.view()


CODEX = CodexSignIn()
KEY_DIR = STATE / "keys"
# The kinds of API key Settings takes. Each saved key is its own provider, named by kind (anthropic, openrouter-2, ...), so `--model NAME:MODEL` reaches it.
ACCOUNT_KINDS = {
    "anthropic": {"label": "Anthropic API key", "wrong": "sk-or-", "base_url": "https://api.anthropic.com", "key_env": "api"},
    "openrouter": {"label": "OpenRouter API key", "wrong": "sk-ant-", "base_url": "https://openrouter.ai/api", "key_check": "https://openrouter.ai/api/v1/key", "min_credit": 1},
    "api": {"label": "API key", "own_url": True},  # any other Anthropic-compatible API (z.ai, DeepSeek, a LiteLLM proxy): the person gives its address
}
KEY_KINDS = {k: v["label"] for k, v in ACCOUNT_KINDS.items()}
NAME_RE = re.compile(r"[a-z][a-z0-9]{1,23}(-[0-9]{1,2})?")
RESERVED = {"claude", "codex", "kimi", "free", "new", "all", "opus", "sonnet", "haiku", "fable", "astra", "default", "person", "hives", "warden", "rootwarden", "api"}
MAX_KEY = 2048


def key_meta(name):
    """What a saved key is. A key file with no note beside it was put there by hand (Hive's own), and is the hand-made provider's, not Settings'.
    The two first keys of the old Settings had no note: on a Hive with settings.json, anthropic and openrouter are still those."""
    m = jload(KEY_DIR / (name + ".json"), None)
    if isinstance(m, dict) and m.get("kind") in ACCOUNT_KINDS: return m
    if SETTINGS.is_file() and name in ("anthropic", "openrouter"): return {"kind": name}
    return None


def key_label(name, m):
    k = m["kind"]
    if k == "api": return f"API key for {urlsplit(m.get('base_url', '')).hostname or 'an API'}"
    n = re.fullmatch(r"[a-z0-9]+-([0-9]+)", name)
    return ACCOUNT_KINDS[k]["label"] + (f" {n[1]}" if n else "")


def key_list():
    out = []
    try: files = sorted(KEY_DIR.iterdir())
    except OSError: return out
    for f in files:
        if not NAME_RE.fullmatch(f.name) or not (m := key_meta(f.name)): continue
        try: v, at = f.read_text().strip(), f.stat().st_mtime
        except OSError: continue
        out.append({"name": f.name, "kind": m["kind"], "label": key_label(f.name, m), "base_url": m.get("base_url"),
                    "ends": v[-4:] if len(v) >= 24 else "", "at": at})  # the last four, and only of a long key
    return out


def api_url(u):
    """An Anthropic-compatible API's address: https, or http only on this machine. No user, query or fragment."""
    if not isinstance(u, str) or len(u) > 300: raise ValueError("give the API's address, like https://api.example.com/anthropic")
    s = urlsplit(u.strip().rstrip("/"))
    local = s.hostname in ("127.0.0.1", "localhost", "::1")
    if not s.hostname or s.username or s.password or s.query or s.fragment or not (s.scheme == "https" or (s.scheme == "http" and local)):
        raise ValueError("the address must start with https:// (or http:// on this machine) and have no password or ? part")
    return urlunsplit(s)


def hand_made(n, v):
    """A provider entry the person made by hand, which Settings never changes. On a Hive with settings.json, codex, anthropic and openrouter are Settings' own from before entries were marked."""
    return not (isinstance(v, dict) and (v.get("added") == "settings" or (SETTINGS.is_file() and n in ("codex", "anthropic", "openrouter"))))


def free_name(base):
    taken = {n for n, v in config_providers().items() if hand_made(n, v)} | {n for n, _ in codex_homes()} | RESERVED
    if not NAME_RE.fullmatch(base) or base in RESERVED: base = "api"
    for n in range(1, 100):
        cand = base if n == 1 else f"{base}-{n}"
        if cand not in taken and not (KEY_DIR / cand).exists() and not (KEY_DIR / (cand + ".json")).exists(): return cand
    raise ValueError("too many keys of that kind")


def accounts_answer():
    """What a change to the accounts sends back: the whole Settings page where there is one, else just the accounts."""
    return settings_page() if SETTINGS.is_file() else {"settings": None, "accounts": accounts_view()}


def set_account_key(d):
    """Add, replace or remove an API key. Each key is a file of mode 600 with a note of its kind beside it. Written here, read by
    providers.py, never sent back or put on the board. Errors never hold the value."""
    if not isinstance(d, dict): raise ValueError("send an object")
    have = {k["name"]: k for k in key_list()}
    if d.get("delete") is True:
        name = d.get("name") or d.get("kind")
        if name not in have: raise ValueError("there's no such key")
        (KEY_DIR / name).unlink(); (KEY_DIR / (name + ".json")).unlink(missing_ok=True)
    else:
        value = d.get("value")
        if not isinstance(value, str) or not value.strip(): raise ValueError("the key is empty")
        value = value.strip()
        if not 8 <= len(value) <= MAX_KEY or re.search(r"\s", value): raise ValueError("that doesn't look like an API key: wrong length, or it has spaces in it")
        if d.get("name") is not None:  # Replace: a new value for a key already here, same kind and address
            name = d["name"]
            if name not in have: raise ValueError("there's no such key")
            meta = {"kind": have[name]["kind"], **({"base_url": have[name]["base_url"]} if have[name]["base_url"] else {})}
        else:
            kind = d.get("kind")
            if kind not in ACCOUNT_KINDS: raise ValueError("that isn't a kind of key this app knows")
            meta = {"kind": kind}
            if kind == "api":
                meta["base_url"] = api_url(d.get("base_url"))
                parts = (urlsplit(meta["base_url"]).hostname or "").split(".")
                base = re.sub(r"[^a-z0-9]", "", parts[-2])[:24] if len(parts) > 1 else "api"  # api.deepseek.com is deepseek
                name = free_name(d.get("label") if isinstance(d.get("label"), str) and NAME_RE.fullmatch(d["label"]) else base)
            else: name = free_name(kind)
        wrong = ACCOUNT_KINDS[meta["kind"]].get("wrong")
        if wrong and value.startswith(wrong): raise ValueError(f"that looks like a key from another provider, not an {ACCOUNT_KINDS[meta['kind']]['label']}")
        KEY_DIR.mkdir(parents=True, exist_ok=True); KEY_DIR.chmod(0o700)
        write_text_private(KEY_DIR / name, value + "\n")
        if SETTINGS.is_file() and name in ("anthropic", "openrouter") and meta == {"kind": name}: pass  # the old form, read the same
        else: write_text_private(KEY_DIR / (name + ".json"), json.dumps(meta) + "\n")
    sync_accounts()
    SETUP.t = 0
    return accounts_answer()


def other_account():
    """An account that isn't a Claude login. False on every Hive without settings.json, without asking Codex."""
    return SETTINGS.is_file() and (bool(key_list()) or any(CODEX_IN.get(p) for _, p in codex_homes()))


PROVIDER_IMPL = "~/modules/providers/providers.py"
FALLBACK_MODELS = (("codex", "default"), ("anthropic", "claude-sonnet-5-5"), ("openrouter", "anthropic/claude-haiku-5.5"))  # free before paid; an "api" key has no model we could guess


def home_rel(p): return "~/" + str(Path(p).relative_to(HOME))


def owned_entries():
    """The provider entries the accounts in Settings stand for, by name, in the order they're tried."""
    owned = {}
    for name, home in codex_homes():
        if CODEX_IN.get(home): owned[name] = {"impl": PROVIDER_IMPL, "harness": "codex", "codex_home": home_rel(home), "stop_pct": 95, "kind": "codex"}
    for k in key_list():
        kd = ACCOUNT_KINDS[k["kind"]]
        e = {"impl": PROVIDER_IMPL, "base_url": k["base_url"] or kd["base_url"], "config_dir": f"~/.claude-{k['name']}" if k["name"] in ("anthropic", "openrouter") else f"~/.claude-key-{k['name']}", "key_file": home_rel(KEY_DIR / k["name"]), "kind": k["kind"]}
        e.update({x: kd[x] for x in ("key_env", "key_check", "min_credit") if x in kd})
        owned[k["name"]] = e
    return owned


def sync_accounts():
    """Make core/config.json match the accounts: one provider per account, each marked "added": "settings". With settings.json, they are
    also the fallback, tried in order when no Claude login can take a run, for every post (agents' too, or a Codex-only Hive would never
    start a helper), free before paid, and a Claude login nobody has signed in to is paused while another account exists, or the starter
    would pick it. Without settings.json (Hive, a Hive set up by hand) only the added entries change: hand-made ones and the fallback are
    the person's, and a paid key starts nothing until a post names it."""
    CODEX_IN.clear()
    settings = SETTINGS.is_file()
    owned = owned_entries()
    with _cfg_lock:
        path = HOME / "core" / "config.json"
        c = jload(path, {})
        if not isinstance(c, dict): return
        old = c.get("providers") if isinstance(c.get("providers"), dict) else {}
        prov = {k: v for k, v in old.items() if hand_made(k, v)}
        added = {}
        for n, e in owned.items():
            if n in prov: continue  # a hand-made entry of that name: left as it is
            was = old.get(n) if isinstance(old.get(n), dict) else {}
            added[n] = {**e, "added": "settings", **{x: was[x] for x in ("paused", "stop_pct") if x in was}}
        prov.update(added)
        new = dict(c)
        if prov: new["providers"] = prov
        else: new.pop("providers", None)
        if settings:
            chain = []
            for kind, model in FALLBACK_MODELS:
                n = next((n for n, e in added.items() if e["kind"] == kind), None)
                if n: chain.append(f"{n}:{model}")
            if chain: new["fallback"] = {"for": ["*"], "chain": chain, "free": []}
            else: new.pop("fallback", None)
        if new != c:
            t = path.parent / f".config.json.{os.getpid()}.{threading.get_ident()}"
            t.write_text(json.dumps(new, indent=2) + "\n"); t.replace(path)
    if not settings: return
    held_f = STATE / "setup_paused.json"
    held = jload(held_f, [])
    held = held if isinstance(held, list) else []
    for l in logins():
        signed = login_ok(l["path"])
        if owned and not signed and l["path"] not in paused_logins():
            set_paused(l["name"], True); held.append(l["path"]) if l["path"] not in held else None
        elif l["path"] in held and (signed or not owned):
            set_paused(l["name"], False); held.remove(l["path"])
    write_text_private(held_f, json.dumps(held))
    SETUP.t = 0


def accounts_view():
    shown = {n for n, _ in codex_homes()} | {k["name"] for k in key_list()}
    by_hand = [{"name": n, "paused": bool(v.get("paused"))} for n, v in config_providers().items() if n not in shown and hand_made(n, v)]
    return {"claude": [{"name": l["name"], "signed_in": login_ok(l["path"])} for l in logins()], "codex": CODEX.view(), "keys": key_list(),
            "kinds": [{"kind": k, "label": v["label"], "own_url": bool(v.get("own_url"))} for k, v in ACCOUNT_KINDS.items()],
            "by_hand": by_hand, "root": ROOT.get(), "settings_file": SETTINGS.is_file()}


def settings_page():
    """GET /api/settings: the settings, and the facts the page must state truthfully. Null on a Hive with no file."""
    v = settings_brief()
    if v is None: return {"settings": None}
    return {"settings": v, "sandbox_check": SANDBOX.get(), "share_text": SHARE_TEXT.get(), "accounts": accounts_view(), "github": GITHUB.get()}



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


def serve_socket(path, handler):
    """The tunnel's way in when web.json sets outside.socket: a unix socket in a directory only this user can enter. Nobody else
    can connect to it or take its name, which they could do to the TCP port during a restart. Run with restart to pick up a change."""
    import socketserver
    class Srv(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
        daemon_threads = True
        request_queue_size = 256  # the default is 5, and cloudflared's connect doesn't wait: a page's burst of requests past that came back as 502s
        def get_request(self):
            conn, _ = self.socket.accept()
            return conn, ("unix", 0)  # a unix peer has no address; route_of reads this
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True); path.parent.chmod(0o700)
    path.unlink(missing_ok=True)
    old = os.umask(0o177)
    try: return Srv(str(path), handler)
    finally: os.umask(old)


def local_get(path, port=None, timeout=10):
    """GET from this user's own dash, for tools on this machine: sends the session cookie when web.json has a secret, and only after
    checking this user holds the port, since another account can bind it while the dash restarts. Returns the response body."""
    import urllib.request
    web = web_cfg() or {}
    port = port or web.get("port") or 8077
    hd = {}
    if web.get("secret"):
        want = "0100007F:%04X" % port
        rows = [ln.split() for ln in Path("/proc/net/tcp").read_text().splitlines()[1:]]
        if not any(f[1] == want and f[3] == "0A" and int(f[7]) == os.getuid() for f in rows):
            raise RuntimeError(f"port {port} isn't held by this user, so the session cookie isn't sent")
        hd["Cookie"] = f"app_session_{port}={session_value(web)}"
    with urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{port}{path}", headers=hd), timeout=timeout) as r:
        return r.read()


def serve(port):
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from urllib.parse import parse_qs, urlparse
    usage = Usage()
    threading.Thread(target=usage.run, daemon=True).start()
    threading.Thread(target=PROGRESS.get, daemon=True).start()  # warm the Progress page's cache; the first read runs progress.py (tens of seconds on a busy machine)
    threading.Thread(target=sync_accounts, daemon=True).start()  # an account already signed in before the app first ran; does nothing without settings.json
    live_c, proj_c, usage_c, jobs_c = Cached(live, 1), Cached(projects, 60), Cached(usage.view, 2), Cached(Jobs().view, 2)
    deliv_c = Cached(delivered_view, 2.5)
    web_c = Cached(web_stamp, 10)
    agent_costs = AgentCosts()
    steps = Steps()  # the Ledger page's tool calls and words; the first look starts reading the session logs

    class H(BaseHTTPRequestHandler):
        def send(self, body, ct, status=200, cookie=None, framed_here=False, cache="no-store"):
            gz = len(body) > 4096 and "gzip" in self.headers.get("Accept-Encoding", "")
            if gz: body = gzip.compress(body, 5)
            self.send_response(status)
            self.send_header("Content-Type", ct); self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", cache)
            if cookie: self.send_header("Set-Cookie", cookie)
            # Board text is written by agents: the page takes nothing from elsewhere and sends nothing anywhere.
            framing = "; frame-ancestors 'self'" if framed_here else "" if self.route == "open" else "; frame-ancestors 'none'"  # the page can post as the person, so it can't sit in someone's frame; a TV page may sit in this site's own (the deck's single screen)
            self.send_header("Content-Security-Policy", "default-src 'none'; connect-src 'self'; frame-src 'self'; manifest-src 'self'; worker-src 'self'; img-src 'self' blob: data:; media-src 'self'; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'none'" + framing)
            if self.route != "open": self.send_header("Referrer-Policy", "no-referrer")
            if gz: self.send_header("Content-Encoding", "gzip")
            self.end_headers(); self.wfile.write(body)

        def send_file(self, path, view=False):
            """A post's file. Agents and Slack write these, so nothing here runs in the page's origin:
            sandboxed, no sniffing, and only plain images open in the tab.
            view=True opens an .html file as a page, which is how someone sees a draft website. Its scripts run in a sandbox with
            no origin (no cookies, no reading the dash) and no network at all, so the page has to carry its own styles and pictures."""
            m = re.fullmatch(r"/files/([0-9a-f]{12})/([A-Za-z0-9_][A-Za-z0-9_.-]*)", path)
            f = HOME_FILES / m[1] / m[2] if m else None
            if not f or f.is_symlink() or not f.is_file() or not f.resolve().is_relative_to(HOME_FILES.resolve()): return self.send_error(404)
            ct = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif"}.get(f.suffix.lower())
            page = view and f.suffix.lower() in (".html", ".htm")
            if page: ct = "text/html; charset=utf-8"
            pdf = view and f.suffix.lower() == ".pdf"   # only ?view=1: the viewer frames it; the browser's own PDF viewer needs a CSP without "sandbox"
            if pdf: ct = "application/pdf"
            vid = VIDEO_TYPES.get(f.suffix.lower()) if view else None   # only ?view=1: the viewer plays it in a <video>, with seeking (Range)
            if vid: ct = vid
            size = f.stat().st_size; lo, hi = 0, size - 1
            rm = vid and re.fullmatch(r"bytes=(\d*)-(\d*)", self.headers.get("Range") or "")
            if rm and (rm[1] or rm[2]):
                if rm[1]: lo = int(rm[1]); hi = min(int(rm[2]), size - 1) if rm[2] else size - 1
                else: lo = max(0, size - int(rm[2]))
                if lo > hi or lo >= size: return self.send_error(416)
            else: rm = None
            self.send_response(206 if rm else 200)
            if rm: self.send_header("Content-Range", f"bytes {lo}-{hi}/{size}")
            if vid: self.send_header("Accept-Ranges", "bytes")
            self.send_header("Content-Type", ct or "application/octet-stream")
            self.send_header("Content-Length", str(hi - lo + 1))
            self.send_header("Content-Disposition", ("inline" if ct else "attachment") + f'; filename="{f.name}"')
            self.send_header("Content-Security-Policy", PAGE_CSP if page else "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'" if pdf else "sandbox; default-src 'none'")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Referrer-Policy", "no-referrer")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            with f.open("rb") as src:
                src.seek(lo)
                if rm or vid: shutil.copyfileobj(_Limit(src, hi - lo + 1), self.wfile)
                else: shutil.copyfileobj(src, self.wfile)

        def send_logo(self):
            """Sandboxed like any file from a person: an SVG opened by its address can't run in the app's origin."""
            try: b = BRAND_LOGO.read_bytes()
            except OSError: return self.send_error(404)
            ct = logo_type(b)
            if not ct: return self.send_error(404)
            self.send_response(200)
            self.send_header("Content-Type", ct)
            self.send_header("Content-Length", str(len(b)))
            self.send_header("Content-Security-Policy", "sandbox; default-src 'none'")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Cache-Control", "private, max-age=86400")
            self.end_headers()
            self.wfile.write(b)

        def send_kb_raw(self, rel):
            """An image or PDF for the Knowledge page. Text never comes this way, so it can't skip the redaction; nothing here runs in the page's origin."""
            f = kb_path(rel); ct = KB_IMAGE.get(f.suffix.lower()) if f else None
            if not f or not f.is_file() or not (ct or f.suffix.lower() == ".pdf") or f.stat().st_size > KB_RAW_MAX: return self.send_error(404)
            name = re.sub(r"[^A-Za-z0-9_.-]", "_", f.name)
            self.send_response(200)
            self.send_header("Content-Type", ct or "application/octet-stream")
            self.send_header("Content-Length", str(f.stat().st_size))
            self.send_header("Content-Disposition", ("inline" if ct else "attachment") + f'; filename="{name}"')
            self.send_header("Content-Security-Policy", "sandbox; default-src 'none'")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            with f.open("rb") as src: shutil.copyfileobj(src, self.wfile)

        route, can, signed, signer, mode, full_left, secrets_on, simple = "open", OPEN_CAN, False, None, None, 0, False, False

        def deny(self, code, why):
            self.send(json.dumps({"error": why}).encode(), "application/json", status=code)

        def admit(self, path, page=False):
            """Sets route, can and signed for this request, or answers it and returns False. Checked on every request."""
            web = web_cfg()
            if web is None: return True  # the open dash: no web.json here, as on a site that hasn't set one up
            self.route, self.can, self.signed, self.signer, self.simple = "refused", (), False, None, False
            if web.get("broken"): self.deny(500, "web.json is missing or can't be read, so nothing is served. Fix or delete it, then run hive web --setup"); return False
            hd = {k.lower(): v for k, v in self.headers.items()}
            route = route_of(hd, self.client_address[0], port, web)
            if route == "refused": self.deny(403, "this address isn't one of this app's"); return False
            if not same_origin(hd, page_nav=page): self.deny(403, "request from another site"); return False
            self.route = route
            if route == "outside":
                out = profile_for(web.get("outside") or {}, hd.get("host"))
                claims = verify_access(hd.get("cf-access-jwt-assertion"), out.get("access") or {})
                if not claims:
                    self.deny(403, "outside access needs a valid Cloudflare Access token"); return False
                # the person's own input keeps full going: the page says so on the requests it makes just after a click or key, and any POST is one
                allowed = person_can(claims, out, active=self.command == "POST" or hd.get("x-hive-active") == "1")
                if not allowed: self.deny(403, "this sign-in isn't on this app's list"); return False
                self.signer = signer_of(claims, out)
                if out is not (web.get("outside") or {}): self.signer = f"{self.signer} at {site_key(hd.get('host'))}"[:80]  # a site's own profile: the post says which name it came in on
                # web.json "simple": sign-ins that open in simple mode until their browser says otherwise. Presentation only, so a bad value just means no.
                names = web.get("simple")
                self.simple = isinstance(names, list) and str(claims.get("email") or "").strip().lower() in {str(n).lower() for n in names}
                (self.can, self.mode, self.full_left), self.signed = allowed, True
                # web.json "add_logins": sign-ins that may also sign the Hive in to its accounts, for someone who owns the account but shouldn't have full. Only ever adds that one cap; a bad value means no.
                names = web.get("add_logins")
                if isinstance(names, list) and "logins" not in self.can and str(claims.get("email") or "").strip().lower() in {str(n).lower() for n in names}: self.can = (*self.can, "logins")
            elif has_session(hd.get("cookie"), web, port):
                self.can, self.signed = CAPS, True
            sites = (web.get("outside") or {}).get("sites")
            self.secrets_on = secrets_allowed(web, site=route == "outside" and isinstance(sites, dict) and site_key(hd.get("host")) in {site_key(k) for k in sites})
            if not self.secrets_on: self.can = tuple(c for c in self.can if c != "secrets")  # off unless web.json says so, or this is a consumer install; no route can set it
            return True

        def app_info(self):
            web = web_cfg() or {}
            info = {"route": self.route, "can": list(self.can), "app": self.route != "open", "brand": brand_view()}
            if self.route != "open": info.update(setup=SETUP.get(), secrets_on=self.secrets_on, settings=settings_brief())
            if self.route != "open": info["default_model"] = default_model(); info["voice"] = voice_ready()
            dm = (jload(HOME / "core" / "config.json", {}) or {}).get("rootwarden_dm") if self.route != "open" else None
            if isinstance(dm, str) and re.fullmatch(r"https://[a-z0-9-]+\.slack\.com/archives/D[A-Z0-9]+", dm): info["rootwarden_dm"] = dm
            if self.mode: info.update(mode=self.mode, full_left=self.full_left)
            if self.signer: info["signer"] = self.signer
            if self.simple: info["simple"] = True
            if self.route == "local":
                out = web.get("outside") or {}
                acc = out.get("access") or {}
                info["outside"] = {"can": out.get("can", ["read"]), "hosts": out.get("hosts") or [], "team": acc.get("team", ""), "aud": acc.get("aud", ""),
                                   "emails": acc.get("emails") or [], "people": out.get("people") or {}, "caps": [c for c in CAPS if c != "secrets" or secrets_allowed(web)],
                                   "sites": out.get("sites") if isinstance(out.get("sites"), dict) else {}}
            return info

        def do_GET(self):
            u = urlparse(self.path)
            q = parse_qs(u.query)
            shown = re.fullmatch(r"(.+?)(/light|/dark)?/?", u.path)   # a display page takes /light or /dark on the end
            display = bool(shown) and shown[1] in DISPLAY_PAGES
            page = u.path in ("/", "/index.html", "/login", "/old", "/new", "/new/") or display
            if not self.admit(u.path, page=page): return
            m = APP_FILES.fullmatch(u.path)
            if m:
                f = UI_DIR / m[1]
                if not f.is_file() or f.is_symlink(): return self.send_error(404)
                return self.send(f.read_bytes(), UI_CT.get(f.suffix, "text/javascript; charset=utf-8"), cache="no-cache")
            if self.route != "open":  # the open dash never asks; its reads and switches stay as they were
                if u.path == "/login" or (page and not self.signed):
                    return self.send(LOGIN_PAGE.encode(), "text/html; charset=utf-8")
                if not self.signed: return self.deny(401, "sign in: run hive web")
                need = GET_CAP.get(u.path) or ("read" if u.path.startswith("/files/") else None)
                if need and need not in self.can: return self.deny(403, f"this route doesn't allow {need}")
            if display and shown[1] == "/dual-screen":
                self.send_response(302); self.send_header("Location", "/dual-screen/1" + (shown[2] or "")); self.send_header("Content-Length", "0"); self.end_headers(); return
            if display:
                return self.send((UI_DIR / "display.html").read_bytes(), "text/html; charset=utf-8", framed_here=True)
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
            m = re.fullmatch(r"(?:/new)?/(vendor(?:/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*)+\.(?:js|css|woff2))", u.path)  # no segment starts with a dot, so no way up
            if m:
                f = UI_DIR / m[1]
                if f.is_symlink() or not f.is_file(): return self.send_error(404)
                return self.send(f.read_bytes(), UI_CT[f.suffix], cache="private, max-age=86400")  # Mermaid is 1.6 MB: fetched once a day, not per page
            off = int(q["from"][0]) if q.get("from", [""])[0].isdigit() else 0
            J = "application/json"
            if u.path == "/api/app":
                self.send(json.dumps(self.app_info(), default=str).encode(), J)
            elif u.path == "/api/brand/logo":
                self.send_logo()
            elif u.path in ("/api/target", "/api/inbox", "/api/config", "/api/modules", "/api/signin", "/api/plans", "/api/secrets", "/api/settings", "/api/update", "/api/signin-codex", "/api/accounts", "/api/design", "/api/kb/home", "/api/kb/open", "/api/kb/search", "/api/kb/raw", "/api/kb/map", "/api/kb/history") and self.route == "open":
                self.send_error(404)
            elif u.path == "/api/kb/home":
                self.send(json.dumps(kb_home()).encode(), J)
            elif u.path == "/api/kb/map":
                self.send(json.dumps(kb_map()).encode(), J)
            elif u.path == "/api/kb/history":
                self.send(json.dumps(kb_history()).encode(), J)
            elif u.path == "/api/progress":
                self.send(json.dumps(PROGRESS.get(), default=str).encode(), J)
            elif u.path == "/api/kb/search":
                self.send(json.dumps(kb_search(q.get("q", [""])[0])).encode(), J)
            elif u.path == "/api/kb/open":
                v = kb_open(q.get("path", [""])[0])
                if v is None: return self.deny(404, "that isn't here, or isn't shown")
                self.send(json.dumps(v).encode(), J)
            elif u.path == "/api/kb/raw":
                self.send_kb_raw(q.get("path", [""])[0])
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
            elif u.path == "/api/update":
                self.send(json.dumps(update_view()).encode(), J)
            elif u.path == "/api/modules":
                self.send(json.dumps(modules_view()).encode(), J)
            elif u.path == "/api/signin":
                self.send(json.dumps(SIGNIN.view()).encode(), J)
            elif u.path == "/api/settings":
                self.send(json.dumps(settings_page()).encode(), J)
            elif u.path == "/api/design":
                self.send(json.dumps(DESIGN.view()).encode(), J)
            elif u.path == "/api/signin-codex":
                self.send(json.dumps(CODEX.view()).encode(), J)
            elif u.path == "/api/accounts":
                self.send(json.dumps(accounts_view()).encode(), J)
            elif u.path == "/api/live":
                self.send(json.dumps({**agents_for(live_c.get(), self.route != "outside" or self.mode == "full"), "web": web_c.get()}, default=str).encode(), J)
            elif u.path == "/api/jobs":
                self.send(json.dumps(jobs_for(jobs_c.get(), self.route != "outside" or self.mode == "full"), default=str).encode(), J)
            elif u.path in ("/api/board", "/api/log"):
                off, lines = tail_lines(BOARD if u.path == "/api/board" else LOG, off)
                self.send(('{"offset":%d,"items":[%s]}' % (off, ",".join(lines))).encode(), J)
            elif u.path == "/api/approvals":
                self.send(json.dumps(approvals_list()).encode(), J)
            elif u.path == "/api/feedback-drafts":
                self.send(json.dumps(feedback_drafts_list()).encode(), J)
            elif u.path == "/api/delivered":
                self.send(json.dumps(deliv_c.get()).encode(), J)
            elif u.path == "/api/usage":
                self.send(json.dumps(usage_c.get()).encode(), J)
            elif u.path == "/api/agent-costs":
                self.send(json.dumps(agent_costs.view()).encode(), J)
            elif u.path == "/api/thread-names":
                self.send(json.dumps(thread_names()).encode(), J)
            elif u.path == "/api/thread-meta":
                self.send(json.dumps(thread_meta()).encode(), J)
            elif u.path == "/api/agent-work":
                self.send(json.dumps(agent_work(q.get("name", [""])[0], self.route != "outside" or self.mode == "full")).encode(), J)
            elif u.path == "/api/ledger":
                g = lambda k, d="": q.get(k, [d])[0]
                num = lambda k: float(g(k)) if re.fullmatch(r"\d+(\.\d+)?", g(k)) else None
                self.send(json.dumps(steps.query(g("agent"), g("thread"), g("kind"), g("q")[:200], num("before"), num("since"),
                                                 int(num("limit") or 300), self.route != "outside" or self.mode == "full")).encode(), J)
            elif u.path == "/api/agent":
                self.send(json.dumps(feed_for(agent_feed(q.get("name", [""])[0]), self.route != "outside" or self.mode == "full")).encode(), J)
            elif u.path == "/api/projects":
                self.send(json.dumps(proj_c.get(), default=str).encode(), J)
            elif u.path == "/json":
                self.send(json.dumps(agents_for(snapshot(), self.route != "outside" or self.mode == "full"), default=str).encode(), J)
            elif u.path.startswith("/files/"):
                self.send_file(u.path, view=parse_qs(u.query).get("view") == ["1"])
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
            if u.path == "/api/brand/logo":
                if not 0 < n <= LOGO_MAX: return self.deny(413, "a logo can be up to 2 MB")
                try: return self.send(json.dumps(set_logo(self.rfile.read(n))).encode(), "application/json")
                except (ValueError, OSError) as e: return self.deny(400, str(e))
            if not 0 < n < ({"/api/post": 2**20, "/api/feedback": 2**20, "/api/issue-link": 2**16, "/api/plan": 2**19, "/api/secret": 2**18}.get(u.path) or (4096 if self.route != "open" else 1024)): return self.send_error(403)
            try: d = json.loads(self.rfile.read(n))
            except ValueError: return self.send_error(400)
            if not isinstance(d, dict): return self.send_error(400)
            num = lambda v: v is None or (isinstance(v, (int, float)) and not isinstance(v, bool))
            J = "application/json"
            if u.path in ("/api/post", "/api/thread-meta", "/api/feedback", "/api/feedback-draft-done", "/api/issue-link", "/api/update", "/api/inbox-read", "/api/agent-model", "/api/config", "/api/starter", "/api/module", "/api/signin", "/api/signin/code", "/api/signin/cancel", "/api/access", "/api/plan", "/api/secret", "/api/settings", "/api/share-copy", "/api/draft", "/api/signin-codex", "/api/account-key", "/api/design", "/api/brand"):
                try:
                    if u.path == "/api/post": res = person_post(d.get("text"), d.get("thread"), d.get("reply_to"), d.get("files"), signer=self.signer, limited="config" not in self.can, simple=d.get("simple") is True, model=d.get("model"))
                    elif u.path == "/api/thread-meta": res = set_thread_meta(d)
                    elif u.path == "/api/feedback": res = send_feedback(d)
                    elif u.path == "/api/feedback-draft-done": res = feedback_draft_done(d)
                    elif u.path == "/api/issue-link": res = issue_link(d)
                    elif u.path == "/api/update": res = update_do(d.get("do"))
                    elif u.path == "/api/plan": res = save_plan(d.get("name"), d.get("text"), d.get("version"), bool(d.get("start")), self.signer)
                    elif u.path == "/api/secret": res = set_secret(d.get("name"), d.get("value"), self.signer, delete=bool(d.get("delete")))
                    elif u.path == "/api/agent-model": res = set_agent_model(d.get("name"), d.get("model"))
                    elif u.path == "/api/inbox-read": res = {"marked": mark_read(None if d.get("all") else [str(i) for i in d.get("ids") or []])}
                    elif u.path == "/api/config": res = set_config(d)
                    elif u.path == "/api/brand": res = set_brand(d)
                    elif u.path == "/api/starter": starter_do(d.get("do")); SETUP.t = 0; res = {"ok": True}
                    elif u.path == "/api/module": res = {"said": module_do(d.get("do"), d.get("name"))}
                    elif u.path == "/api/signin": res = SIGNIN.start(d.get("name"), bool(d.get("console")))
                    elif u.path == "/api/signin/code": SIGNIN.code(d.get("code")); res = {"ok": True}
                    elif u.path == "/api/signin/cancel": SIGNIN.cancel(); res = {"ok": True}
                    elif u.path == "/api/settings": res = set_settings(d)
                    elif u.path == "/api/share-copy": res = share_copy()
                    elif u.path == "/api/draft": res = draft_do(d)
                    elif u.path == "/api/design": res = DESIGN.act(d)
                    elif u.path == "/api/signin-codex":
                        if d.get("cancel"): res = CODEX.cancel()
                        elif isinstance(d.get("remove"), str): res = CODEX.remove(d["remove"])
                        else: res = CODEX.start(login=d.get("login") if isinstance(d.get("login"), str) else None, add=d.get("add") is True)
                    elif u.path == "/api/account-key" and not self.secrets_on: return self.deny(403, "secrets are off on this name")
                    elif u.path == "/api/account-key": res = set_account_key(d)
                    else:
                        if not set_access(d): raise ValueError("that isn't a valid access setting")
                        res = {"ok": True}
                except (ValueError, subprocess.SubprocessError) as e: return self.deny(400, str(e))
                live_c.t = usage_c.t = 0
                return self.send(json.dumps(res).encode(), J)
            if u.path == "/api/approval":  # like kill: this user's own connection, same origin
                if not may_kill(os.getuid() if self.client_address[0] == "unix" else peer_uid(self.client_address)) or self.headers.get("Sec-Fetch-Site", "same-origin") != "same-origin":
                    return self.send_error(403)
                try: res = approval_answer(d)
                except ValueError as e: return self.deny(400, str(e))
                return self.send(json.dumps(res).encode(), "application/json")
            if u.path == "/api/kill":  # the one switch that can't be undone: more checks than the others
                if not may_kill(os.getuid() if self.client_address[0] == "unix" else peer_uid(self.client_address)) or self.headers.get("Sec-Fetch-Site", "same-origin") != "same-origin":
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
    threading.Thread(target=record_usage, daemon=True).start()
    threading.Thread(target=provider_status, daemon=True).start()
    threading.Thread(target=SETUP.get, daemon=True).start()  # so the first page after a restart doesn't pay for it
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)  # loopback only: never opens the machine
    srv.daemon_threads = True
    cfg = web_cfg() or {}; sock = (cfg.get("outside") or {}).get("socket")
    # Only on this install's own port. A test copy on another port shares the home and so the config: it would take the tunnel's socket and leave it dead when it stopped (2026-11-03).
    if sock and port == int(cfg.get("port") or 8077): threading.Thread(target=serve_socket(Path(sock).expanduser(), H).serve_forever, daemon=True).start()
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
