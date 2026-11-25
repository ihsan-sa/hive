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
import base64, bisect, math, ipaddress, socket, urllib.parse, email.utils, shlex, errno, fcntl, gzip, hashlib, hmac, importlib.util, json, os, pty, pwd, re, secrets, select, shutil, signal, sqlite3, stat, struct, subprocess, sys, tempfile, termios, threading, time, zlib
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
APP_FILES = re.compile(r"(?:/new)?/(manifest\.webmanifest|call\.webmanifest|sw\.js|favicon\.ico|icons/[a-z0-9-]+\.(?:png|ico))")  # what lets a browser install the page as an app; plain files, served with no sign-in because the browser fetches them before it has one
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
    return len(bg_shell_pids(pid))


def bg_shell_pids(pid):
    """The topmost background shells under a run (bg_shells' walk), as pids."""
    got, todo = [], []
    def kids(p):
        try:
            for t in Path(f"/proc/{p}/task").iterdir(): todo.extend((t / "children").read_text().split())
        except OSError: pass
    kids(pid)
    while todo:
        q = todo.pop()
        try: shell = "/tasks/" in os.readlink(f"/proc/{q}/fd/1")
        except OSError: shell = False
        if shell: got.append(q)
        else: kids(q)
    return got


def tail_line(path, n=4096):
    """The newest line a job wrote to its output file: the last non-empty line of the file's tail, after its last carriage return
    (a progress bar redraws in place), terminal codes taken out, secrets hidden, cut to 160 characters. None if it wrote nothing yet."""
    try:
        with open(path, "rb") as f:
            f.seek(0, 2); size = f.tell(); f.seek(max(0, size - n)); b = f.read(n)
    except OSError: return None
    for ln in reversed(b.decode(errors="replace").split("\n")):
        for part in reversed(ln.split("\r")):
            t = " ".join(ANSI.sub("", part).replace("\x1b", "").split())
            if t: return short(redact(t), 160)
    return None


def job_out(pid):
    """(path, mtime) of the regular file a job writes to: its stdout, else its stderr, the newer if both are files. None for a pipe or a terminal."""
    got = []
    for fd in (1, 2):
        try:
            p = os.readlink(f"/proc/{pid}/fd/{fd}"); st = os.stat(f"/proc/{pid}/fd/{fd}")
            if stat.S_ISREG(st.st_mode) and not p.endswith(" (deleted)"): got.append((p, st.st_mtime))
        except OSError: pass
    return max(got, key=lambda x: x[1]) if got else None


_btime = None
def proc_since(pid):
    """When a process started, in epoch seconds."""
    global _btime
    try:
        if _btime is None: _btime = next(int(l.split()[1]) for l in Path("/proc/stat").read_text().splitlines() if l.startswith("btime "))
        st = Path(f"/proc/{pid}/stat").read_text(); return _btime + int(st[st.rindex(")") + 2:].split()[19]) / TICK
    except (OSError, ValueError, IndexError, StopIteration): return None


def shell_cmd(pid):
    """What a background shell runs: the command inside Claude Code's wrapper (`... && eval '<cmd>' ...`, quotes as the shell reads them), else its command line."""
    c = proc_cmd(pid); s = c[-1] if c else ""; i = s.find("&& eval ")
    if i >= 0:
        try:
            lx = shlex.shlex(s[i + 8:], posix=True); lx.whitespace_split = True; w = lx.get_token()
            if w: return w
        except ValueError: pass
    return " ".join(c)


def job_lines(runs=None):
    """HA-24: each job an agent started that is running now, with the newest line it wrote, for the head of its conversation.
    Two kinds: a run's background shells (their stdout is the task file) and heavy's lanes (the lane's .pid file names the heavy process,
    whose stdout the job inherits). One job seen both ways is one row, the lane's. A job writing to a pipe has no line: line None.
    [{"agent", "thread", "kind": "shell"|lane name, "cmd", "since", "line", "at": when its file last changed}]."""
    d, out, runs = STATE / "heavy", {}, runs if runs is not None else [jload(f, {}) or {} for f in sorted(RUN.glob("*.json"))]
    thread_of, shell_key = {}, {}
    def lane_since(lane, pid):
        try: return (d / f"{lane}.who").stat().st_mtime
        except OSError: return proc_since(pid)
    def ppid(p):
        try: st = Path(f"/proc/{p}/stat").read_text(); return int(st[st.rindex(")") + 2:].split()[1])
        except (OSError, ValueError, IndexError): return 0
    for r in runs:
        if not alive(r.get("pid")): continue
        name = r.get("name"); thread_of.setdefault(name, r.get("thread"))
        for q in bg_shell_pids(r.get("pid")):
            o = job_out(q); shell_key[int(q)] = k = o[0] if o else f"pid:{q}"
            out[k] = {"agent": name, "thread": r.get("thread"), "kind": "shell", "cmd": short(redact(shell_cmd(q)), 200), "since": proc_since(q),
                       "line": tail_line(o[0]) if o else None, "at": o[1] if o else None}
    for pf in sorted(d.glob("*.pid")):
        lane = pf.stem
        if not re.fullmatch(r"slot\d+|quick\d+|first", lane): continue
        try: pid = int(pf.read_text().split()[0]); who = (d / f"{lane}.who").read_text().split(None, 2)
        except (OSError, ValueError, IndexError): continue
        if not alive(pid) or not any("heavy" in a for a in proc_cmd(pid)[:3]): continue
        o, up = job_out(pid), pid
        for _ in range(12):  # a heavy job started inside a background shell is that shell's row, told by its lane
            up = ppid(up)
            if up <= 1 or up in shell_key: break
        k = shell_key.get(up) or (o[0] if o else f"pid:{pid}"); was = out.pop(k, None) or {}
        if not o and was.get("line"): o = (k, was.get("at"))
        name = who[0] if who and who[0] not in ("?", "") else (was.get("agent") or "?")
        out[k] = {"agent": name, "thread": was.get("thread") or thread_of.get(name), "kind": lane, "cmd": short(redact(who[2] if len(who) > 2 else ""), 200),
                  "since": min([x for x in (was.get("since"), lane_since(lane, pid)) if x] or [None]),
                  "line": tail_line(o[0]) if o else None, "at": o[1] if o else None}
    return sorted(out.values(), key=lambda j: j["since"] or 0)


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

_POST_CMD = re.compile(r"\b(?:hive|hive)\s+post\b([^\n]*?)<<-?\s*(['\"]?)(\w+)\2[^\n]*\n", re.S)


def partial_str(raw, key="command"):
    """The value of KEY in a JSON object cut off part way (a streamed tool input), decoded as far as it goes."""
    m = re.search(r'"%s"\s*:\s*"' % re.escape(key), raw or "")
    if not m: return None
    s, i, out = raw[m.end():], 0, []
    while i < len(s):
        c = s[i]
        if c == '"': break
        if c == "\\":
            esc = s[i:i + 6] if s[i + 1:i + 2] == "u" else s[i:i + 2]
            if len(esc) < (6 if s[i + 1:i + 2] == "u" else 2): break   # an escape cut in half: stop before it
            try: out.append(json.loads('"' + esc + '"'))
            except ValueError: pass
            i += len(esc); continue
        out.append(c); i += 1
    return "".join(out)


def post_draft(cmd):
    """(text, thread flag) of an `hive post ... <<'X'` command still being written, or None. Text stops at the closing X line."""
    m = _POST_CMD.search(cmd or "")
    if not m: return None
    body = cmd[m.end():]
    end = re.search(r"(?m)^%s\s*$" % re.escape(m.group(3)), body)
    if end: body = body[:end.start()]
    else:   # a last line that may yet be the closing word: hold it back so the text only grows
        last = body.rsplit("\n", 1)[-1]
        if last and m.group(3).startswith(last.strip()): body = body[:len(body) - len(last)]
    th = re.search(r"--thread[= ]\s*['\"]?([\w.-]+)", m.group(1))
    return body.rstrip("\n"), th.group(1) if th else None


def activity(out):
    """Last thing the session did, from the tail of its stream-json output."""
    p = Path(out or "")
    res = {"last": None, "doing": "", "said": "", "turns": 0, "cost": None, "draft": None}
    try: m = p.stat().st_mtime
    except OSError: return res
    hit = _activity.get(str(p))
    if hit and hit[0] == m: return hit[1]
    res["last"] = m
    cur = None   # E11: the Bash call being streamed (--include-partial-messages); a post it writes shows as a draft until it runs
    for e in jlines(p, 256 * 1024):
        t = e.get("type")
        if t == "stream_event":
            ev = e.get("event") or {}; et = ev.get("type")
            if et == "content_block_start":
                b = ev.get("content_block") or {}
                if b.get("type") == "tool_use" and b.get("name") == "Bash": cur = {"i": ev.get("index"), "raw": []}
            elif et == "content_block_delta" and cur and ev.get("index") == cur["i"]:
                d = ev.get("delta") or {}
                if d.get("type") == "input_json_delta": cur["raw"].append(d.get("partial_json") or "")
            elif et == "message_start" and cur and not cur.get("done"): cur = None
            elif et == "content_block_stop" and cur and ev.get("index") == cur["i"]: cur["done"] = True
            continue
        if t in ("user", "result"): cur = None   # the tool ran: the post is on the board now
        if t == "assistant":
            for c in e.get("message", {}).get("content", []) or []:
                if c.get("type") == "tool_use":
                    res["doing"] = describe_tool(c.get("name"), c.get("input"))
                elif c.get("type") == "text" and c.get("text", "").strip():
                    res["said"] = c["text"]
        elif t == "result":
            res["turns"] += 1
            if e.get("total_cost_usd") is not None: res["cost"] = e["total_cost_usd"]
    if cur:
        d = post_draft(partial_str("".join(cur["raw"])))
        if d and d[0].strip(): res["draft"] = {"text": d[0][-20000:], "thread": d[1], "at": m}
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
    scoped = jload(STATE / "usage_scoped.json", {}) or {}
    held = (jload(STATE / "starter.json", {}) or {}).get("limits") or {}
    out = []
    for l in c.get("logins") or []:
        p = str(Path(l).expanduser())
        mine = lines.get(p) if isinstance(lines.get(p), dict) else {}
        lv, at = LIVE.get(p), used.get("_checked")
        if lv and lv["at"] > (at or 0):  # the dashboard's own reading (every 30 s at best) is newer than the starter's (every 5 min)
            used, five, resets, at = {**used, p: lv["weekly"]}, {**five, p: lv["five"]}, {**resets, p: lv["resets"]}, lv["at"]
            if lv.get("scoped") is not None: scoped = {**scoped, p: lv["scoped"]}
        out.append({"name": names.get(p) or Path(p).name.removeprefix(".claude-").lstrip("."), "path": p, "weekly": used.get(p),
                    "five": five.get(p) if isinstance(five, dict) else None, "paused": p in off, "read_at": at, "gap": USAGE_GAP.get(p), "read_error": USAGE_ERR.get(p),
                    "resets": (resets.get(p) if isinstance(resets, dict) else None) or {}, "plan": plan_of(p),
                    # no 5-hour limit on this plan: the usage API's five_hour is null on a successful read (cc0 shows 0.0)
                    "no_five_hour": isinstance(resets, dict) and p in resets and (resets[p].get("has_five_hour") is False
                                    or ("has_five_hour" not in resets[p] and isinstance(five, dict) and five.get(p) is None)),
                    # a model's own weekly share inside the weekly limit (Fable's), and the models the starter holds on it now
                    "scoped": scoped.get(p) if isinstance(scoped.get(p), dict) else {},
                    "held_models": sorted(k.partition("#")[2] for k, v in held.items() if isinstance(k, str) and k.startswith(p + "#")
                                          and isinstance(v, (int, float)) and v > time.time()),
                    "stop": {"weekly": mine.get("weekly"), "five_hour": mine.get("five_hour"), "global": c.get("usage_stop_pct")}})
    return out


LIVE = {}  # {login path: {"at", "weekly", "five", "resets"}}: the dashboard's own usage readings, see live_usage()


def scoped_shares(r):
    """{family: {"percent", "resets"}} from a usage reply's limits[] rows of kind weekly_scoped (Fable's own weekly bar), as the starter reads them."""
    out = {}
    for row in (r or {}).get("limits") or []:
        if not isinstance(row, dict) or row.get("kind") != "weekly_scoped" or not isinstance(row.get("percent"), (int, float)): continue
        name = str((((row.get("scope") or {}).get("model") or {}).get("display_name")) or "").lower()
        fam = next((f for f in ("fable", "opus", "sonnet") if f in name), None)
        if not fam: continue
        try: at = datetime.fromisoformat(row["resets_at"]).timestamp() if row.get("resets_at") else None
        except (TypeError, ValueError): at = None
        out[fam] = {"percent": row["percent"], "resets": at}
    return out


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
                           "resets": {"weekly": ep(w), "five_hour": ep(f), "has_five_hour": f is not None}, "scoped": scoped_shares(r)}
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
    try:  # every login paused at once is a hive pause (core/pauses.py), so checks over that window read inconclusive
        sys.path.insert(0, str(HOME / "core")); import pauses
        pauses.note({l["path"] for l in logins()} <= set(v), "dashboard " + ("pause" if on else "resume"), by="dash", path=STATE / "pauses.jsonl")
    except Exception: pass
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

def asks_ledger():
    """~/asks.jsonl merged per id the way core/asks.py ledger() merges it: the first record keeps who asked and when, later ones add
    the state, and `updated` / `done_at` are those later records' times. The file itself never carries the two, so a reader that
    takes the last record per id and waits for `done_at` sees no closed ask at all: until 2026-11-11 this page served none, and the
    home's "Finished since your last visit" had no ask in it (simuse reading 20261110-1900)."""
    items = {}
    try: lines = (HOME / "asks.jsonl").read_text(errors="replace").splitlines()
    except OSError: return items
    for line in lines:
        try: r = json.loads(line)
        except ValueError: continue
        if not isinstance(r, dict) or not r.get("id"): continue
        e = items.get(r["id"])
        if e is None: e = items[r["id"]] = dict(r)
        else:
            e.update({k: v for k, v in r.items() if k not in ("id", "at", "by", "post", "when", "ask", "kind")})
            e["updated"] = r.get("updated") or r.get("at")
        if r.get("status") in ("done", "dropped"): e["done_at"] = r.get("done_at") or r.get("at")
        elif r.get("status"): e.pop("done_at", None)   # reopened
    return items


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
    for r in asks_ledger().values():
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


# ---------- threads an agent brought up for the person (GET /api/shown) ----------
# `hive show THREAD --why ...` appends to ~/.hive/shown.jsonl; the page shows the ones whose time isn't up as "Brought up for you".
LOADTIMES = STATE / "loadtimes.jsonl"  # first-load figures the app reports from the person's own browser (R69)


def record_loadtime(d, route):
    """One line per report: how long the page took to reach the chat in a real browser, so a slow load through the public name is measured, not guessed."""
    keep = {k: round(float(d[k])) for k in ("ttfb", "dom", "boot", "chat", "load", "files", "bytes", "width") if isinstance(d.get(k), (int, float)) and not isinstance(d.get(k), bool)}
    if not keep or any(v < 0 or v > 10**9 for v in keep.values()): raise ValueError("no figures")
    keep.update(t=time.strftime("%Y-%m-%dT%H:%M:%S"), route=route, cold=d.get("cold") is True, hidden=d.get("hidden") is True)
    LOADTIMES.parent.mkdir(parents=True, exist_ok=True)
    try: lines = LOADTIMES.read_text().splitlines()[-199:]
    except OSError: lines = []
    LOADTIMES.write_text("\n".join(lines + [json.dumps(keep)]) + "\n")
    return {"ok": True}


def record_opened(d, route):
    """The person opened a thread in the app (the client sends {thread} on open). Turns "never came back" in the fade replay
    (projects/rsi-lab/thread-retire) from an upper bound into a count. The thread name only, never its text."""
    th = str(d.get("thread") or "")
    if not THREAD_NAME.fullmatch(th): raise ValueError("a thread name is letters, digits and . _ : -")
    THREAD_OPENED.parent.mkdir(parents=True, exist_ok=True)
    with THREAD_OPENED.open("a") as f: f.write(json.dumps({"at": round(time.time(), 3), "thread": th, "from": "app", "route": route}) + "\n")
    return {"ok": True}


SHOWN = STATE / "shown.jsonl"

def shown_now():
    """Newest entry per thread that is still in time, newest first. Only the thread name, who, why and the end time leave."""
    t = time.time(); best = {}
    try: lines = SHOWN.read_text().splitlines()[-500:]
    except OSError: return []
    for ln in lines:
        try: r = json.loads(ln)
        except ValueError: continue
        if not isinstance(r, dict) or not THREAD_NAME.fullmatch(str(r.get("thread") or "")): continue
        if r.get("dismissed"): best.pop(r["thread"], None); continue   # the person closed it; a later `hive show` brings it back
        try: until, at = float(r["until"]), float(r["at"])
        except (KeyError, TypeError, ValueError): continue
        if until > t: best[r["thread"]] = {"thread": r["thread"], "from": str(r.get("from") or "")[:40], "why": str(r.get("why") or "")[:200], "at": at, "until": until}
    return sorted(best.values(), key=lambda r: -r["at"])

def dismiss_shown(d):
    """The person closed one brought-up thread (POST /api/shown): noted in the same file, so every page drops it."""
    th = str(d.get("thread") or "")
    if not THREAD_NAME.fullmatch(th): raise ValueError("a thread name is letters, digits and . _ : -")
    with open(SHOWN, "a") as f: f.write(json.dumps({"thread": th, "dismissed": True, "at": time.time()}) + "\n")
    return {"ok": True}

# ---------- the checklist: threads the person ticked off on the Where things stand list (POST /api/checklist) ----------
CHECKLIST = STATE / "checklist.json"   # needs_you.CHECKS: {"items": [...]}; agents write it with `hive summary check`

FOCUS_ID = re.compile(r"[a-z0-9][a-z0-9_-]{0,60}")
def focus_do(d, full=True, driven=False):
    """POST /api/focus: put a thread in a focus ({thread, focus}) or take it out ({thread, off: true}); focus.py keeps the record (hive focus tag).
    {order: [focus ids]} sets the order the foci show in, rail and home alike (hive focus order).
    {brief: {focus, text?, files?: [upload refs], drop?: [names]}}: the group's own instructions and files, given to every chat in it (E37, hive focus brief).
    {seen: "<focus id>"} or {seen_home: true}: the person is looking at that focus's page, or at the home (hive focus seen); the answer's
    `look` is the record now ({at, start, base, since}), which the page takes as it is, so no browser's clock is in it. The pages
    mark what is newer than their last look, so only the owner's look counts: a limited sign-in's is answered and not kept, and nor is one
    from a browser a script drives (driven: the tests and the agents' screenshots sign in as the owner; the page holds back too, core/foci.js canSee).
    full: the sign-in may change settings; a group's instructions steer every agent in it, so a limited one can't."""
    if "seen" in d or d.get("seen_home"):
        home = d.get("seen_home") is True; key = None if home else str(d.get("seen") or "").lower()   # the home is its own key: a focus may be called home
        if not home and not FOCUS_ID.fullmatch(key): raise ValueError("bad focus")
        if not full or driven: return {"ok": True, "seen": key, "home": home, "noted": False}
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py"), "seen", "--home" if home else key, "--json"], capture_output=True, text=True, timeout=30, env={**os.environ, "HIVE_NAME": "person"})
        if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
        try: out = json.loads(r.stdout)
        except ValueError: out = {}
        noted, look = out.get("noted") is True, out.get("seen") if isinstance(out.get("seen"), dict) else None   # not noted: one record a minute at most, and nothing changed
        # The look goes into the held list as it is: a fresh read of every focus took 2 to 9 s, and each look made the next page wait on one (review, 2026-11-16).
        if noted and look: foci_c.patch(lambda v: foci_look(v, None if home else key, look))
        elif noted: foci_c.t = 0
        return {"ok": True, "seen": key, "home": home, "noted": noted, "look": look}
    if "brief" in d:
        if not full: raise ValueError("only a full sign-in can change a group's instructions")
        x = d.get("brief") if isinstance(d.get("brief"), dict) else {}
        fid = str(x.get("focus") or "").lower()
        if not FOCUS_ID.fullmatch(fid): raise ValueError("bad focus")
        args = ["brief", fid]
        if "text" in x:
            if not isinstance(x["text"], str) or len(x["text"]) > 4000: raise ValueError("a group's instructions take 4000 characters at most")
            args.append("--set")
        refs = [UPLOAD_REF.fullmatch(str(u)) for u in x.get("files") or []]
        if not all(refs) or len(refs) > 10: raise ValueError("a file wasn't recognised")
        paths = [INCOMING / m[1] / m[2] for m in refs]
        if not all(q.is_file() and not q.is_symlink() for q in paths): raise ValueError("a file is gone; add it again")
        for q in paths: args += ["--file", str(q)]
        for n in x.get("drop") or []:
            n = Path(str(n)).name
            if not n or n.startswith("-"): raise ValueError("bad file name")
            args += ["--drop", n]
        if len(args) == 2: raise ValueError("nothing to change")
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py")] + args, input=x.get("text") or "", capture_output=True, text=True, timeout=30, env={**os.environ, "HIVE_NAME": "person"})
        if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
        for q in paths: shutil.rmtree(q.parent, ignore_errors=True)
        foci_c.t = 0
        return {"ok": True, "focus": fid, "said": r.stdout.strip()}
    if "order" in d:
        ids = d.get("order")
        if not isinstance(ids, list) or not ids or len(ids) > 60 or not all(isinstance(i, str) and FOCUS_ID.fullmatch(i) for i in ids): raise ValueError("bad order")
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py"), "order"] + ids, capture_output=True, text=True, timeout=30)
        if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
        foci_c.t = 0
        return {"ok": True, "order": ids, "said": r.stdout.strip()}
    if "check" in d:   # the focus page's to-do boxes (the person, 110812372f5e: "why can I not click these check marks")
        cid = str(d.get("check") or "")
        if not re.fullmatch(r"c\d{1,6}", cid): raise ValueError("bad check")
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py"), "check", "done" if d.get("done") is not False else "untick", cid],
                           capture_output=True, text=True, timeout=30, env={**os.environ, "HIVE_NAME": "person"})
        if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
        foci_c.t = 0
        return {"ok": True, "check": cid, "done": d.get("done") is not False}
    if "later" in d:   # a needs-you item put off: later, moot (not needed) or back (the person, 1108133400fb); focus.py keeps it (hive focus later)
        x = d.get("later") if isinstance(d.get("later"), dict) else {}
        what, key = str(x.get("what") or ""), str(x.get("key") or "")
        if what not in ("later", "moot", "back") or not key or len(key) > 200 or not key.isprintable(): raise ValueError("bad later")
        args = ["later", what, key]
        if what != "back":
            th, fid = str(x.get("th") or ""), str(x.get("focus") or "").lower()
            if th and THREAD_NAME.fullmatch(th): args += ["--th", th]
            if fid and FOCUS_ID.fullmatch(fid): args += ["--focus", fid]
            args += ["--text=" + str(x.get("text") or "")[:300].replace("\n", " ")]
            if isinstance(x.get("t"), (int, float)) and x["t"] > 0: args += ["--t", str(float(x["t"]))]
        r = subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py")] + args, capture_output=True, text=True, timeout=30, env={**os.environ, "HIVE_NAME": "person"})
        if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
        foci_c.t = 0
        return {"ok": True, "later": what, "key": key}
    th = str(d.get("thread") or "").strip(); f = str(d.get("focus") or "").strip().lower(); off = bool(d.get("off"))
    if not THREAD_NAME.fullmatch(th): raise ValueError("bad thread")
    if not off and not FOCUS_ID.fullmatch(f): raise ValueError("bad focus")
    args = ["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "focus.py"), "tag", th] + (["--off"] if off else [f])
    r = subprocess.run(args, capture_output=True, text=True, timeout=30, env={**os.environ, "HIVE_NAME": "person"})   # the person's move: Recently moved says "by you"
    if r.returncode: raise ValueError((r.stderr or r.stdout or "focus.py failed").strip()[:200])
    foci_c.t = 0
    return {"ok": True, "thread": th, "focus": None if off else f, "said": r.stdout.strip()}

def checklist_do(d):
    """The person ticks (or unticks) one checklist item. Only the tick is theirs to change: agents add and remove items (hive summary check)."""
    iid = str(d.get("id") or "")
    nd = importlib.util.spec_from_file_location("needs_you_cl", Path(__file__).with_name("needs_you.py")); m = importlib.util.module_from_spec(nd); nd.loader.exec_module(m)
    c = m.checks_load(); hit = [i for i in c["items"] if i["id"] == iid]
    if not hit: raise ValueError("no such item")
    hit[0]["ticked"] = d.get("ticked") is not False
    if hit[0]["ticked"]: hit[0]["ticked_at"] = time.time()
    else: hit[0].pop("ticked_at", None)
    m.checks_save(c)
    return c

# ---------- which posts the chat shows in full and which as one line (GET /api/thread-shown?thread=T) ----------
# The rule is shown_rule.decide (beside this file), picked by the reader test in projects/rsi-lab/shown (READER-TEST.md, "Step 2").
# Answer: {"thread": T, "posts": {post id: {"show": "full" | "line" | "hidden", "line": "HH:MM who: first line" | None, "also": [lines with a "?"]}}}.
# Only posts the chat shows today get an entry; a post with none is drawn as today. The whole board is decided at once (a run's chains
# cross threads) and kept until the board, the log or the asks file grows: about half a second on 20,000 posts.
ASKS_FILE = HOME / "asks.jsonl"
_shown_rule = [None, {}]   # [(file sizes), {thread: {post id: entry}}]
_shown_rule_lock = threading.Lock()


def shown_rule_mod():
    m = sys.modules.get("h2_shown_rule")
    if not m:
        spec = importlib.util.spec_from_file_location("h2_shown_rule", Path(__file__).with_name("shown_rule.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); sys.modules["h2_shown_rule"] = m
    return m


def thread_shown(thread):
    if not THREAD_NAME.fullmatch(thread or ""): raise ValueError("a thread name is letters, digits and . _ : -")
    def size(p):
        try: return p.stat().st_size
        except OSError: return -1
    key = (size(BOARD), size(LOG), size(ASKS_FILE))
    with _shown_rule_lock:
        if _shown_rule[0] != key:
            board = [r for r in jlines(BOARD) if isinstance(r, dict)]
            every, by_thread = shown_rule_mod().board_shown(board, jlines(LOG), jlines(ASKS_FILE)), {}
            for r in board:
                if r.get("id") in every: by_thread.setdefault(r.get("thread"), {})[r["id"]] = every[r["id"]]
            _shown_rule[:] = [key, by_thread]
        return {"thread": thread, "posts": _shown_rule[1].get(thread, {})}


# ---------- the status bar at the top of a thread (GET /api/thread-status) ----------
# `hive status THREAD` appends to ~/.hive/thread-status.jsonl; newest entry per thread: {thread: {text, from, at}}.
THREAD_STATUS = STATE / "thread-status.jsonl"

def thread_status():
    out = {}
    try: lines = THREAD_STATUS.read_text().splitlines()[-3000:]
    except OSError: return out
    for ln in lines:
        try: r = json.loads(ln)
        except ValueError: continue
        if isinstance(r, dict) and THREAD_NAME.fullmatch(str(r.get("thread") or "")) and r.get("text"):
            try: out[r["thread"]] = {"text": str(r["text"])[:900], "from": str(r.get("from") or "")[:40], "at": float(r.get("at") or 0)}
            except (TypeError, ValueError): pass
    return out

# ---------- what each agent says it is on (GET /api/agent-status) ----------
# `hive me "..."` appends to ~/.hive/agent-status.jsonl; newest line per agent: {name: {text, thread, at}}. Public, like every post.
AGENT_STATUS = STATE / "agent-status.jsonl"

def agent_status():
    """{name: {text, thread, at, about?}}: each agent's newest `hive me` line, and its `hive me --about` line when it set one."""
    out, about = {}, {}
    try: lines = AGENT_STATUS.read_text().splitlines()[-5000:]
    except OSError: return out
    for ln in lines:
        try: r = json.loads(ln)
        except ValueError: continue
        if not (isinstance(r, dict) and re.fullmatch(r"[A-Za-z0-9_.-]{1,40}", str(r.get("name") or ""))): continue
        if r.get("about"): about[r["name"]] = str(r["about"])[:200]   # `hive me --about`: who the agent is, kept apart from what it is on now
        elif r.get("text"):
            try: out[r["name"]] = {"text": str(r["text"])[:200], "thread": str(r.get("thread") or "")[:100] or None, "at": float(r.get("at") or 0)}
            except (TypeError, ValueError): pass
    for n, t in about.items(): out.setdefault(n, {"text": "", "thread": None, "at": 0})["about"] = t
    return out

# ---------- the person's own view of threads (GET/POST /api/thread-meta) ----------
# Pins, archive and new titles the person gives threads on Chat. Kept beside the board, never in it: the board and every link keep
# the thread's id. {thread: {"title": str, "pin": ms, "archived": ms, "back": ms}}; a key that's off is left out, a thread with none is dropped.
# "important" and "faded" (likely not needed any more, greyed in the list) can also come from agents; the person's own word on either wins.
THREAD_META = STATE / "thread-meta.json"
THREAD_META_LOG = STATE / "thread-meta-log.jsonl"   # one line per change made in the app: {at, thread, from, change}
META_LOCK = threading.Lock()

IMPORTANT = STATE / "important.jsonl"   # `hive important THREAD --why ...` appends {thread, from, why, at, off?}; the person's own mark is "important" in thread-meta
RENAMED = STATE / "renamed.jsonl"       # `hive rename THREAD "title"`: {thread, from, title, at}; the person's own title wins
FADED = STATE / "faded.jsonl"           # `hive fade THREAD --why ...`, same records: an agent's word that the person likely needn't look at the thread any more
FADED_RULES = STATE / "faded-rules.jsonl"   # the plain-rule pass (fade_rules.py, E20): same records with from "rule"; `fade_rules.py --undo` takes them all back
THREAD_OPENED = STATE / "thread-opened.jsonl"   # POST /api/opened: one line per thread the person opens in the app {at, thread, from}

def agent_marks(path, keep=1000):
    """Newest agent mark per thread that is still on: {thread: {by, why, at}}."""
    best = {}
    try: lines = path.read_text().splitlines()[-keep:]
    except OSError: return best
    for ln in lines:
        try: r = json.loads(ln); th = str(r["thread"]); at = float(r["at"])
        except (ValueError, KeyError, TypeError): continue
        if not THREAD_NAME.fullmatch(th): continue
        if r.get("off"): best.pop(th, None)
        else: best[th] = {"by": str(r.get("from") or "")[:40], "why": str(r.get("why") or "")[:200], "at": int(at * 1000)}
    return best

def thread_meta():
    try: v = json.loads(THREAD_META.read_text())
    except (OSError, ValueError): v = {}
    if not isinstance(v, dict): v = {}
    for path, key, off, by, why, keep in ((IMPORTANT, "important", "impOff", "impBy", "impWhy", 1000), (FADED, "faded", "fadeOff", "fadedBy", "fadedWhy", 1000),
                                          (FADED_RULES, "faded", "fadeOff", "fadedBy", "fadedWhy", 50000)):   # the rule file is read whole: a mark stays on until its own off
        for th, a in agent_marks(path, keep).items():      # an agent's mark counts unless the person took the mark off after it
            m = dict(v.get(th) or {})
            if a["at"] > m.get(off, 0) and not m.get(key):
                m[key] = a["at"]; m[by] = a["by"]; m[why] = a["why"]; v[th] = m
    best = {}
    try: lines = RENAMED.read_text().splitlines()[-1000:]
    except OSError: lines = []
    for ln in lines:
        try: r = json.loads(ln); best[str(r["thread"])] = " ".join(str(r["title"]).split())[:80]
        except (ValueError, KeyError, TypeError): continue
    for th, t in best.items():
        m = dict(v.get(th) or {})
        if t and not m.get("title") and THREAD_NAME.fullmatch(th): m["title"] = t; m["titleBy"] = "agent"; v[th] = m
    return v

def set_thread_meta(d):
    """d: {thread, and any of title (str, "" takes it off), pin, archived, important, faded (bool)}. Returns the new entry."""
    th = str(d.get("thread") or "")
    if not THREAD_NAME.fullmatch(th): raise ValueError("a thread name is letters, digits and . _ : -")
    with META_LOCK:
        try: all_ = json.loads(THREAD_META.read_text())
        except (OSError, ValueError): all_ = {}
        if not isinstance(all_, dict): all_ = {}
        m = dict(all_.get(th) or {}); now_ms = int(time.time() * 1000)
        if "title" in d:
            t = " ".join(str(d["title"] or "").split())[:80]
            if t and t != th: m["title"] = t
            else: m.pop("title", None)
        for k, off in (("important", "impOff"), ("faded", "fadeOff")):
            if k in d:
                if not isinstance(d[k], bool): raise ValueError(k + " is true or false")
                if d[k]: m[k] = now_ms; m.pop(off, None)
                else: m.pop(k, None); m[off] = now_ms
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
        # The history of what the person set by hand (thread-meta.json keeps only the present state). An un-fade or a "back" here is a
        # wrong fade the person corrected: projects/rsi-lab/thread-retire/replay.py --cases turns each into a test of every fade rule.
        try:
            ch = {k: (True if k == "title" else d[k]) for k in ("title", "pin", "archived", "important", "faded") if k in d}
            with THREAD_META_LOG.open("a") as f: f.write(json.dumps({"at": round(now_ms / 1000, 3), "thread": th, "from": "app", "change": ch}) + "\n")
        except OSError: pass
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

EFFORTS = ("low", "medium", "high", "xhigh", "max")   # `claude --effort`; EFFORTS in core/hive and starter.py

def chosen_efforts(st):
    """name -> the effort each agent wakes on next: the starter's st["efforts"], with picks from the agent panel it hasn't read yet on top."""
    out = {n: e for n, e in (st.get("efforts") or {}).items() if e in EFFORTS}
    for f in (STATE / "effort-set").glob("*.json"):
        try: e = json.loads(f.read_text())["effort"]
        except (OSError, ValueError, KeyError, TypeError): continue
        if e in EFFORTS: out[f.stem] = e
        elif e == "": out.pop(f.stem, None)
    return out

def set_agent_effort(name, effort, restart=False, signer=None, limited=False):
    """The agent panel's Effort menu: `name` runs at `effort` from its next run and stays on it ("" goes back to Claude's default). A drop
    file the starter reads on its next tick (apply_effort_sets in starter.py), so nothing wakes the agent: a run's effort is fixed when it starts.
    restart: ask the running agent to come back at it. A post, not a signal, since killing a run mid-step isn't safe (only --interrupt's single
    foreground command is): it finishes its step and posts to itself with --effort, which the starter queues and resumes the same session on."""
    if not isinstance(name, str) or not AGENT_CODE.fullmatch(name): raise ValueError("that isn't an agent's name")
    if effort != "" and effort not in EFFORTS: raise ValueError("pick one of " + ", ".join(EFFORTS))
    run = None
    if restart:
        if not effort: raise ValueError("pick a level to restart at")
        run = run_to_kill(name)
        if not run: raise ValueError(f"{name} isn't running; its next run starts at {effort}")
        if run.get("provider") or run.get("harness", "claude-code") != "claude-code": raise ValueError("effort applies to Claude runs only")
    d = STATE / "effort-set"; d.mkdir(parents=True, exist_ok=True)
    tmp = d / f".{name}.tmp"; tmp.write_text(json.dumps({"effort": effort, "at": time.time()})); tmp.replace(d / f"{name}.json")
    if run:
        thread = run.get("thread") or run.get("post")
        person_post(f"@{name} The person set your effort to {effort} and asked you to restart at it. A run's effort is fixed when it starts, so finish the step you're on and wait for any background work "
                    f"you still need (ending your turn stops it), then run `hive post --thread {thread} --to {name} --effort {effort} <<< 'Resuming at {effort} effort.'` and end your turn. "
                    f"You come back in this same conversation at that effort.", thread, signer=signer, limited=limited, notice=True)
    return {"ok": True, "effort": effort, "asked": bool(run)}

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


# ---- Wipe the knowledge base (Settings > Wipe the knowledge base; person's ask k2264, thread kb-wipe) ----
# Nothing is deleted: every top-level entry of ~/kb but the two kept files is renamed into ~/.hive/kb-copies/<id>/kb/ (0700), with
# wipe.json beside it, and Put back merges a copy into ~/kb again without overwriting anything. The GET answers the facts the dialog
# shows and a one-use token; the POST needs that token, the typed phrase and every tick. Tests: test_kb_wipe.py, drive_kb_wipe.mjs.
KB_DIR, KB_COPIES = HOME / "kb", STATE / "kb-copies"
KB_KEEP = ("priorities.md", "direction.md")   # the person's order of work and standing directions stay where every agent reads them
KB_HOLD = ("history",)   # made again empty after a wipe: core/kb.py's `kb miss` appends to kb/history/failed-lookups.md without making the folder
KB_TICKS = ("hive", "lose", "space")
KB_TOKEN_FROM, KB_TOKEN_TO = 15, 600   # a token works from 15 s to 10 min after the facts were shown
KB_FRESH_S = 900   # from outside, the full sign-in must be this recent
KB_WIPE_LOCK = threading.Lock()
kb_wipe_tokens = {}   # token -> (sign-in key, issued at)
KB_TOKEN_LOCK = threading.Lock()   # request threads share the table: one at a time adds, ages out or uses up a token
KB_COPY_ID = re.compile(r"\d{8}-\d{6}(-\d{1,3})?")


def kb_user():
    """The account this Hive runs as: two Hives can share one machine, so the machine's name alone doesn't say which."""
    try: return pwd.getpwuid(os.getuid()).pw_name
    except KeyError: return str(os.getuid())


def kb_wipe_name():
    """The name the person types: the Hive's own name from Settings, else its account and machine."""
    return brand_view().get("name") or f"{kb_user()}@{os.uname().nodename}"


def kb_tree_size(p):
    """(files, bytes) under p, links counted as files and never followed."""
    try:
        st = os.lstat(p)
        if not stat.S_ISDIR(st.st_mode): return 1, st.st_size
    except OSError: return 0, 0
    n = b = 0
    for d, sub, files in os.walk(p):
        for f in files + [s for s in sub if os.path.islink(os.path.join(d, s))]:
            try: n += 1; b += os.lstat(os.path.join(d, f)).st_size
            except OSError: pass
    return n, b


def kb_wipe_entries():
    """The top-level names a wipe moves, or raises ValueError with why there is nothing to do or it mustn't be done."""
    if os.path.islink(KB_DIR): raise ValueError("the knowledge base folder is a link to somewhere else, so it isn't wiped from here")
    if not KB_DIR.is_dir(): raise ValueError("there is no knowledge base folder")
    if os.path.islink(STATE) or os.path.islink(KB_COPIES): raise ValueError("the folder for copies is a link to somewhere else")
    names = sorted(e for e in os.listdir(KB_DIR) if e not in KB_KEEP)
    if not [e for e in names if not (e in KB_HOLD and os.path.isdir(KB_DIR / e) and not os.path.islink(KB_DIR / e) and not os.listdir(KB_DIR / e))]:
        raise ValueError("there is nothing in the knowledge base but the files that stay")
    STATE.mkdir(parents=True, exist_ok=True)
    if os.stat(KB_DIR).st_dev != os.stat(STATE).st_dev: raise ValueError("the knowledge base is on another disk from the folder for copies, so it can't be moved in one step")
    return names


def kb_running():
    """Names of the agents running now: they may be reading or writing notes as the wipe moves them."""
    return sorted({r.get("name") for r in (jload(f, {}) or {} for f in RUN.glob("*.json")) if isinstance(r, dict) and r.get("name") and alive(r.get("pid"))})


def kb_wipe_plan(key, issue=True):
    """What the dialog shows before anything is sent, and a one-use token for this sign-in (none when issue is False: it must sign in again first)."""
    now = time.time()
    name = kb_wipe_name()
    plan = {"name": name, "host": os.uname().nodename, "user": kb_user(), "home": str(HOME), "phrase": f"wipe {name}", "keep": [k for k in KB_KEEP if (KB_DIR / k).is_file()],
            "ticks": list(KB_TICKS), "agents": kb_running(), "wait": KB_TOKEN_FROM, "valid": KB_TOKEN_TO, "busy": KB_WIPE_LOCK.locked(), "copies": kb_copies()}
    try: names = kb_wipe_entries()
    except (ValueError, OSError) as e: plan["refuse"] = str(e); return plan
    n = b = 0
    for e in names:
        x, y = kb_tree_size(KB_DIR / e); n += x; b += y
    plan.update(entries=len(names), files=n, bytes=b)
    if issue:
        tok = secrets.token_urlsafe(24)
        with KB_TOKEN_LOCK:
            for t, (_, at) in list(kb_wipe_tokens.items()):
                if now - at > KB_TOKEN_TO: del kb_wipe_tokens[t]
            while len(kb_wipe_tokens) >= 50: del kb_wipe_tokens[next(iter(kb_wipe_tokens))]   # the oldest goes first
            kb_wipe_tokens[tok] = (key, now)
        plan["token"] = tok
    return plan


def kb_take_token(tok, key, now=None):
    """True if tok was issued to this sign-in and is inside its window. It is used up either way."""
    with KB_TOKEN_LOCK: got = kb_wipe_tokens.pop(tok, None) if isinstance(tok, str) else None
    if not got: return False
    k, at = got; age = (now or time.time()) - at
    return hmac.compare_digest(str(k), str(key)) and KB_TOKEN_FROM <= age <= KB_TOKEN_TO


KB_FORGET_WAIT = 20   # seconds the answer waits for the search index; on 2026-11-16 the refresh after a wipe of the Hive's 551 indexed notes took 0.7 s
KB_SEARCH_LATE = "Search hasn't caught up yet: for a while it may answer as if this hadn't happened."   # true after a wipe and after a put back, and whether the refresh is late or failed (the next `kb find` refreshes again)


def kb_forget(wait=None):
    """Drop every cached view of the notes, then bring the search index up to date, so kb find and recall stop returning moved notes.
    refresh() drops each indexed file that is gone, and recall keeps only paths still in the index, so this covers both.
    Never raises, and never holds the answer for more than `wait` seconds: the refresh runs in its own thread (it waits for the index's
    lock, which a first build elsewhere can hold for minutes) and finishes there. `kb find` refreshes at each call of its own anyway.
    Returns None when search is up to date, else the words the window shows."""
    try:
        with kb_index_lock: kb_index_memo.update(t=0, items=[], more=False)
        kb_map_memo.update(t=0, v=None); kb_hist_memo.update(t=0, head="", v=None); kb_tool_memo["fresh"] = 0
        mod = kb_tool()
        if not mod: return None if not (HOME / "core" / "kb.py").is_file() else KB_SEARCH_LATE   # no search tool, so no index to go stale
        out = {}
        def run():
            try:
                c = mod.connect()
                try: mod.refresh(c, force=True)
                finally: c.close()
                kb_tool_memo["fresh"] = time.time(); out["ok"] = True
            except Exception as e: out["err"] = type(e).__name__
        t = threading.Thread(target=run, daemon=True); t.start(); t.join(KB_FORGET_WAIT if wait is None else wait)
        return None if out.get("ok") else KB_SEARCH_LATE
    except Exception: return KB_SEARCH_LATE


def kb_note(event, text, extra):
    """One line in the dash log and one plain post on the board (to nobody, so it wakes no agent). True if both were written; never raises."""
    ok = True
    try:
        with LOG.open("a") as f:
            fcntl.flock(f, fcntl.LOCK_EX); f.write(json.dumps({"at": now_iso(), "event": event, **extra}) + "\n")
    except (OSError, TypeError, ValueError): ok = False
    try:
        p = {"id": datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now_iso(), "from": "dash", "to": [], "thread": "dash", "text": text}
        with BOARD.open("a") as f:
            fcntl.flock(f, fcntl.LOCK_EX); f.write(json.dumps(p) + "\n")
    except (OSError, TypeError, ValueError): ok = False
    return ok


def kb_record(d, info):
    """Write a copy's wipe.json, whole or not at all (a temp file, then a rename). True if written; never raises."""
    try:
        t = d / "wipe.json.tmp"; t.write_text(json.dumps(info, indent=1)); os.replace(t, d / "wipe.json")
        return True
    except (OSError, TypeError, ValueError): return False


def kb_record_read(d):
    """(a copy's wipe.json as a dict, whether a new one may be written). A record that is there but can't be read is the only word on
    what that wipe moved, so before anything writes over it it is kept beside itself under another name; when even that can't be done
    the answer is ({}, False) and the caller leaves the file as it is. Never raises."""
    p = d / "wipe.json"
    try: raw = p.read_bytes()
    except FileNotFoundError: return {}, True
    except OSError: return {}, False
    try:
        w = json.loads(raw)
        if isinstance(w, dict): return w, True
    except Exception: pass
    try: os.link(p, d / f"wipe.json.unreadable-{datetime.now().strftime('%Y%m%d-%H%M%S')}-{secrets.token_hex(2)}", follow_symlinks=False); return {}, True
    except OSError: return {}, False


def kb_wipe_tell(d, info, late=False, recorded=True):
    """Tell the agents about a wipe: one line in the dash log and one on the board, then mark the copy's record so kb_mend doesn't
    write it again. Before the search refresh, which can take its whole wait: the line says nothing about search. True if both were written."""
    n, b, failed = info.get("moved_files") or 0, info.get("moved_bytes") or 0, info.get("error")
    of = len(info.get("entries") or []) - len(info.get("gone") or [])
    noted = kb_note("kb_wiped", f"The person wiped the knowledge base from the dashboard: {n} file{'s' if n != 1 else ''} ({kb_size_words(b)}) "
            f"{'were' if n != 1 else 'was'} moved into a private copy, which Settings can put back. "
            + ("The person's priorities and standing directions stay. " if info.get("kept") else "")
            + ("It stopped part way, so some notes are still in place. " if failed else "")
            + "This was done on purpose, so don't restore the notes from a backup or from the copy: if they are wanted again, the person puts the copy back from Settings."
            + (f" (This line is late: the wipe was at {info.get('at')}, and the line couldn't be written then.)" if late else ""),
            {"id": info.get("id"), "files": n, "bytes": b, "moved": len(info.get("moved") or []), "of": of, "by": info.get("by"),
             "stopped": failed, "recorded": recorded, **({"late": True} if late else {})})
    if noted: info["noted"] = True; kb_record(d, info)
    return noted


def kb_mend():
    """Finish what a wipe left undone when the dashboard stopped in the middle of one. Call under KB_WIPE_LOCK and the lock file (a
    record still "moving" belongs to whoever holds them): at the start, and before each wipe or put back. A record still "moving"
    becomes "stopped" with what its copy holds now, and a wipe that never got its line on the board gets it. True if a record changed; never raises."""
    changed = False
    try: ds = sorted(x for x in KB_COPIES.iterdir() if KB_COPY_ID.fullmatch(x.name) and x.is_dir() and not x.is_symlink())
    except OSError: return changed
    for d in ds:
        try:
            k = d / "kb"
            if not k.is_dir() or k.is_symlink(): continue
            w = jload(d / "wipe.json")
            if not isinstance(w, dict) or w.get("id") != d.name: continue   # no record, or one that can't be read: nothing here to go on, so it is left as it is
            if w.get("state") == "moving":
                moved = sorted(os.listdir(k))
                if not moved:   # it stopped before the first note moved, so nothing happened: take the empty copy away as kb_wipe does (rmdir removes only an empty folder)
                    try: os.unlink(d / "wipe.json"); os.rmdir(k); os.rmdir(d)
                    except OSError: pass
                    continue
                n = b = 0
                for e in moved:
                    x, y = kb_tree_size(k / e); n += x; b += y
                # What it meant to move and is still in place says whether it finished (a folder the wipe makes again empty isn't one of them).
                rest = [e for e in (w.get("entries") or []) if isinstance(e, str) and e not in moved and os.path.lexists(KB_DIR / e)
                        and not (e in KB_HOLD and os.path.isdir(KB_DIR / e) and not os.path.islink(KB_DIR / e) and not os.listdir(KB_DIR / e))]
                w.update(moved=moved, moved_files=n, moved_bytes=b, state="stopped" if rest else "done", error="the dashboard stopped while it was moving the notes" if rest else None, noted=False)
                if not kb_record(d, w): continue
                changed = True
                for e in KB_HOLD:
                    try: (KB_DIR / e).mkdir(exist_ok=True)
                    except OSError: pass
            if w.get("state") in ("done", "stopped") and w.get("moved") and not w.get("noted") and os.listdir(k):
                changed = kb_wipe_tell(d, w, late=True) or changed
        except Exception: pass
    return changed


def kb_mend_start():
    """The mend at the start, off the main thread. It is skipped while another dash holds the lock file: a wipe is running there."""
    try:
        with KB_WIPE_LOCK:
            if not KB_COPIES.is_dir(): return
            with open(STATE / "kb-wipe.lock", "a") as lk:
                try: fcntl.flock(lk, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except OSError: return
                changed = kb_mend()
        if changed: kb_forget()
    except Exception: pass


def kb_rename_new(src, dst):
    """rename(src, dst) that never replaces: FileExistsError when dst is there (renameat2 with RENAME_NOREPLACE). Raises OSError where
    the system or the disk can't do that, since a plain rename could replace a note an agent wrote at that name a moment ago."""
    import ctypes
    fn = getattr(ctypes.CDLL(None, use_errno=True), "renameat2", None)
    if fn is None: raise OSError(errno.ENOSYS, "this system can't move a file without the risk of replacing one")
    if fn(-100, os.fsencode(src), -100, os.fsencode(dst), 1) != 0:   # AT_FDCWD twice, RENAME_NOREPLACE
        e = ctypes.get_errno(); raise OSError(e, os.strerror(e), str(src))


def kb_size_words(b):
    return f"{b / 2**20:.1f} MB" if b >= 2**20 else f"{max(1, round(b / 1024))} KB" if b else "0 KB"


def kb_wipe(by):
    """Move the notes aside. Call under KB_WIPE_LOCK. Two halves, so the answer is never wrong about whether notes moved:
    until the first rename every failure is a ValueError (the handler answers 409 and the window says nothing was moved); from the
    first rename on nothing raises, and what moved is always answered (files and bytes count only what moved)."""
    try:
        names = kb_wipe_entries()
        KB_COPIES.mkdir(mode=0o700, parents=True, exist_ok=True); os.chmod(KB_COPIES, 0o700)
        base = datetime.now().strftime("%Y%m%d-%H%M%S"); cid = base
        for i in range(2, 1000):
            try: (KB_COPIES / cid).mkdir(mode=0o700); break
            except FileExistsError: cid = f"{base}-{i}"
        else: raise ValueError("couldn't make a folder for the copy")
        d = KB_COPIES / cid; os.chmod(d, 0o700); (d / "kb").mkdir(mode=0o700)
        sizes = {e: kb_tree_size(KB_DIR / e) for e in names}
        info = {"id": cid, "at": now_iso(), "by": by, "host": os.uname().nodename, "user": kb_user(), "home": str(HOME), "name": kb_wipe_name(), "entries": names,
                "files": sum(x for x, _ in sizes.values()), "bytes": sum(y for _, y in sizes.values()), "kept": [k for k in KB_KEEP if (KB_DIR / k).exists()], "state": "moving"}
        if not kb_record(d, info): raise ValueError("couldn't write the copy's record")
    except OSError as x: raise ValueError(f"couldn't get the copy ready: {x.strerror or x}")
    moved, gone, failed = [], [], None
    for e in names:
        try: os.rename(KB_DIR / e, d / "kb" / e); moved.append(e)
        except OSError as x:
            if isinstance(x, FileNotFoundError) and not os.path.lexists(KB_DIR / e) and os.path.isdir(d / "kb"): gone.append(e); continue   # an agent removed or renamed it since the list was read: there is nothing to move, so go on
            failed = f"{e}: {x.strerror or x}"; break
    if not moved:   # still nothing moved: take the empty folder for the copy away again (rmdir removes only an empty one), then say so
        try: os.unlink(d / "wipe.json"); os.rmdir(d / "kb"); os.rmdir(d)
        except OSError: pass
        raise ValueError(f"the first note couldn't be moved ({failed})" if failed else "the notes were gone before they could be moved")
    for e in KB_HOLD:
        try: (KB_DIR / e).mkdir(exist_ok=True)
        except OSError: pass
    n, b = sum(sizes[e][0] for e in moved), sum(sizes[e][1] for e in moved)
    info.update(moved=moved, gone=gone, moved_files=n, moved_bytes=b, state="done" if not failed else "stopped", error=failed, noted=False)
    recorded = kb_record(d, info)
    noted = kb_wipe_tell(d, info, recorded=recorded)   # the agents hear of it first: the search refresh below can take its whole wait, and kb_mend writes this line if the dash stops before it
    search = kb_forget()
    return {"ok": not failed, "id": cid, "files": n, "bytes": b, "moved": len(moved), "of": len(names) - len(gone), "stopped": failed, "search": search, "noted": noted}


def kb_copies():
    """The copies a wipe left that still hold something, newest first."""
    out = []
    try: ds = sorted((x for x in KB_COPIES.iterdir() if KB_COPY_ID.fullmatch(x.name) and x.is_dir() and not x.is_symlink()), reverse=True)
    except OSError: return out
    for d in ds:
        k = d / "kb"
        try: left = [e for e in os.listdir(k)] if k.is_dir() and not k.is_symlink() else []
        except OSError: left = []
        if not left: continue
        w = jload(d / "wipe.json", {}) or {}
        n, b = 0, 0
        for e in left:
            x, y = kb_tree_size(k / e); n += x; b += y
        out.append({"id": d.name, "at": w.get("at"), "files": n, "bytes": b, "phrase": f"put back {d.name}", "back": w.get("back"),
                    "state": w.get("state") if w.get("state") in ("moving", "done", "stopped") else None, "error": w.get("error") if isinstance(w.get("error"), str) else None})
    return out


def kb_move_dir(src, dst):
    """Move a folder of the copy to dst by one rename, whatever its own mode, and return how many files are in it.
    A folder that goes to another parent has its ".." rewritten, which needs the right to write in the folder itself: one without
    it (mode 0555, say) fails with EACCES however writable both parents are. And one that can't be listed (0000) would count as no
    files. It is a folder of the private copy, so when one of its owner's rights is missing it gets all three for the count and the
    one rename, and its own mode back where it lands (or where it was, when the rename fails). Only this folder's mode changes,
    never one that is in the knowledge base already. A folder that isn't ours to change is counted and moved as far as the system
    lets it be, and the rename raises what stops it."""
    mode = None
    try:
        st = os.lstat(src)
        if stat.S_ISDIR(st.st_mode) and st.st_mode & 0o700 != 0o700:
            m = stat.S_IMODE(st.st_mode); os.chmod(src, m | 0o700); mode = m
    except OSError: pass
    try: n = kb_tree_size(src)[0]; os.rename(src, dst)
    except OSError:
        if mode is not None:
            try: os.chmod(src, mode)
            except OSError: pass
        raise
    if mode is not None:
        try:
            if not os.path.islink(dst): os.chmod(dst, mode)
        except OSError: pass
    return n


def kb_shut(p):
    """Whether p is a folder of ours that takes no new name: its owner lacks the right to write in it or to go into it."""
    try: st = os.lstat(p)
    except OSError: return False
    return stat.S_ISDIR(st.st_mode) and st.st_uid == os.geteuid() and st.st_mode & 0o300 != 0o300


KB_DEEP = 200   # folders inside folders that a put back merges; past it an entry stays in the copy (each level is two calls deep here)


def kb_drop_name(src):
    """Take away the copy's name for a note that is in the knowledge base already (the same file is at both names)."""
    try: os.unlink(src)
    except OSError as x: raise OSError(x.errno, f"it is back, but its name in the copy couldn't be removed ({x.strerror or x})") from None


def kb_merge(src, dst, t, depth=0):
    """Move src into dst, never over anything: a name already at dst stays in the copy (its path goes in t["clash"]). t counts the
    files moved ("n") and the files left behind a clash ("left") as it goes, and t["any"] says something moved at all (an empty
    folder counts no file).
    It never raises. An entry that can't move stays in the copy, and its path with the system's reason goes in t["stuck"] (the first
    20 of them; t["stuck_n"] counts them all). The entries beside it still go, so one that never can move doesn't hold the rest back.
    When the reason is that the folder it goes into is read-only, it says that in plain words: a put back changes no mode in the
    knowledge base, so making that folder writable is for whoever made it read-only."""
    try: kb_merge_one(src, dst, t, depth)
    except OSError as x:
        t["stuck_n"] += 1
        why = x.strerror or str(x)
        if x.errno in (errno.EACCES, errno.EPERM) and kb_shut(dst.parent):
            why = "the knowledge base folder is read-only" if dst.parent == KB_DIR else "its folder in the knowledge base is read-only"
        if len(t["stuck"]) < 20: t["stuck"].append(f"{dst.relative_to(KB_DIR)}: {why}")


def kb_merge_one(src, dst, t, depth=0):
    """One entry of kb_merge; an OSError it raises is that entry's own (kb_merge writes it down and goes on).
    A file goes by a hard link and then an unlink: unlike a rename, a link can't replace a note an agent wrote at that name a moment
    ago. A folder that isn't there yet goes by one rename, which can only ever replace an empty folder. Where hard links aren't
    allowed, a file goes by a rename that refuses to replace (kb_rename_new). When the same file is at both names (a put back that
    stopped between its link and its unlink), it is back already and only the copy's name goes.
    A read-only folder in the copy would stop both ways out of it (a rename of the folder itself, kb_move_dir; an unlink or a rename
    of what is in it), and every later try at the same place, so it gets the owner's rights for the move and its own mode back after.
    A folder that is in both places is merged entry by entry, KB_DEEP folders down at most."""
    real_dir = lambda p: os.path.isdir(p) and not os.path.islink(p)
    if not os.path.lexists(dst):
        if real_dir(src):
            try: t["n"] += kb_move_dir(src, dst); t["any"] = True; return
            except OSError as x:
                if x.errno not in (errno.ENOTEMPTY, errno.EEXIST, errno.ENOTDIR): raise   # else something appeared there just now: merge into it, or leave it, below
        else:
            try: os.link(src, dst, follow_symlinks=False)
            except FileExistsError: pass   # a note appeared there just now: a clash, below
            except OSError:
                try: kb_rename_new(src, dst)
                except FileExistsError: pass   # the same
                else: t["n"] += 1; t["any"] = True; return
            else: t["n"] += 1; t["any"] = True; kb_drop_name(src); return
    if real_dir(src) and real_dir(dst):
        if depth >= KB_DEEP: raise OSError(errno.ELOOP, f"it is more than {KB_DEEP} folders deep")   # else a tree deep enough ends in a RecursionError, half way through
        mode = None
        try:
            m = stat.S_IMODE(os.lstat(src).st_mode)
            if m & 0o700 != 0o700: os.chmod(src, m | 0o700); mode = m
        except OSError: pass   # not ours to change: the listing or the moves below say what stays
        try:
            for e in sorted(os.listdir(src)): kb_merge(src / e, dst / e, t, depth + 1)
        finally:
            try: os.rmdir(src)
            except OSError:
                if mode is not None:   # something stays in it, so it stays as it was
                    try: os.chmod(src, mode)
                    except OSError: pass
        return
    try: same = not real_dir(src) and os.path.samestat(os.lstat(src), os.lstat(dst))
    except OSError: same = False
    if same: kb_drop_name(src); t["n"] += 1; t["any"] = True; return
    t["clash"].append(str(dst.relative_to(KB_DIR))); t["left"] += max(1, kb_tree_size(src)[0])


def kb_put_back(cid, by):
    """Merge one copy back into ~/kb. Call under KB_WIPE_LOCK. As kb_wipe, the answer is never wrong about whether notes moved:
    when nothing at all came back and something couldn't move, it is a ValueError (the window says nothing was put back); once
    anything is back nothing raises, and the answer says how many came back, the first entry that couldn't move ("stopped", its
    exact path and the reason) and how many couldn't ("stuck"). It goes through every entry whatever happens to one of them. What
    didn't come back is still in the copy, which stays listed."""
    if not isinstance(cid, str) or not KB_COPY_ID.fullmatch(cid): raise ValueError("no such copy")
    if os.path.islink(KB_DIR) or os.path.islink(STATE) or os.path.islink(KB_COPIES): raise ValueError("a folder here is a link to somewhere else")
    d = KB_COPIES / cid; k = d / "kb"
    if not k.is_dir() or k.is_symlink() or d.is_symlink(): raise ValueError("no such copy")
    try:
        KB_DIR.mkdir(exist_ok=True)
        names = sorted(os.listdir(k))
        if os.stat(KB_DIR).st_dev != os.stat(k).st_dev: raise ValueError("the knowledge base is on another disk from the copy, so it can't be put back from here")
    except OSError as x: raise ValueError(f"couldn't read the copy or the knowledge base folder: {x.strerror or x}")
    if not names: raise ValueError("there is nothing left in this copy")
    t = {"n": 0, "left": 0, "clash": [], "any": False, "stuck": [], "stuck_n": 0}
    for e in names: kb_merge(k / e, KB_DIR / e, t)
    n, left, stuck = t["n"], t["left"], t["stuck_n"]
    stopped = t["stuck"][0] if stuck else None
    if stuck and not t["any"]:   # t["any"], not n: an empty folder that went back counts no file, and "nothing was put back" would be wrong
        raise ValueError(f"no note could go back ({stopped}, and {stuck - 1} more)" if stuck > 1 else f"no note could go back ({stopped})")
    w, may = kb_record_read(d)
    w["back"] = (w["back"] if isinstance(w.get("back"), list) else []) + [{"at": now_iso(), "by": by, "files": n, "left": left, "stopped": stopped, "stuck": stuck}]
    recorded = may and kb_record(d, w)
    noted = kb_note("kb_put_back", f"The person put a wiped knowledge base back from the dashboard: {n} file{'s' if n != 1 else ''} returned"
            + (f", and {left} stayed in the copy because a note of the same name is there now." if left else ".")
            + (f" {stuck} entr{'ies' if stuck != 1 else 'y'} couldn't be moved, so {'they are' if stuck != 1 else 'it is'} still in the copy." if stuck else ""),
            {"id": cid, "files": n, "left": left, "by": by, "stopped": stopped, "stuck": stuck, "recorded": recorded})
    search = kb_forget()
    return {"ok": not stuck, "id": cid, "files": n, "left": left, "clash": t["clash"][:20], "stopped": stopped, "stuck": stuck, "search": search, "noted": noted}


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


def signin_waiting(st):
    """Claude accounts whose login has expired, with what waits on each (the starter's own count, auth_watch)."""
    c, out = jload(HOME / "core" / "config.json", {}) or {}, []
    held = st.get("limits") or {}
    every = [str(Path(l).expanduser()) for l in c.get("logins") or []]
    names = {str(Path(k).expanduser()): v for k, v in (c.get("login_names") or {}).items()}
    for login in st.get("auth_told") or {}:
        other = any(l != login and time.time() >= held.get(l, 0) for l in every)
        n = sum(r.get("login") == login for r in (st.get("resume") or {}).values()) + (0 if other else len(st.get("pending") or {}))
        out.append({"name": names.get(login) or Path(login).name.removeprefix(".claude-").lstrip("."), "agents": n, "other": other})
    return out

def limit_hold(st):
    """When the starter's holds end, with every Claude account held by a limit a run hit (an expired login is told apart: signin_waiting)."""
    every = [str(Path(l).expanduser()) for l in (jload(HOME / "core" / "config.json", {}) or {}).get("logins") or []]
    held = [(st.get("limits") or {}).get(l, 0) for l in every]
    return min(held) if held and min(held) > time.time() and not st.get("auth_told") else None

def drafts_in(thread, steps=False):
    """The posts live agents are writing in this thread right now: cheap, as activity() is kept per out file until it changes.
    STEPS (E42b, ?steps=1): each run working in this thread also brings the code it has run since its last post, with or without a draft."""
    if not thread: return []
    got = []
    for f in sorted(RUN.glob("*.json")):
        r = jload(f, {}) or {}
        if not alive(r.get("pid")): continue
        d = activity(r.get("out"))["draft"]
        d = {**d, "thread": thread} if d and d["thread"] == thread else None   # a post with no --thread opens a new conversation: it is not a reply in the one the agent runs in (person 111311262b35)
        ran = ran_live(r.get("out")) if steps and r.get("thread") == thread else {"steps": [], "more": 0}   # an agent running elsewhere that drafts a reply here has no steps here: the shape stays whole (KeyError 'more', 379 times in 14 days)
        if d or ran["steps"]: got.append({"name": r.get("name", f.stem), "draft": d, **({"steps": ran["steps"], "more": ran.get("more", 0)} if steps else {})})
    return [g for g in got if g["draft"] or g.get("steps")] if steps else [g for g in got if g["draft"]]

def live():
    """The cheap part, polled by the web page: who is running and what the starter holds. No board parse."""
    agents, jobs = [], heavy_jobs()
    runs = []
    for f in sorted(RUN.glob("*.json")):
        r = jload(f, {}) or {}; runs.append({**r, "name": r.get("name", f.stem)})
        a = activity(r.get("out"))
        agents.append({
            "name": r.get("name", f.stem), "pid": r.get("pid"), "alive": alive(r.get("pid")),
            "since": ts(r.get("at")), "thread": r.get("thread"), "from": r.get("from"), "post": r.get("post"),
            "last": a["last"], "doing": a["doing"], "said": a["said"], "out": r.get("out"),
            "draft": a["draft"], "effort": run_effort(r.get("pid")),
            "jobs": jobs.get(r.get("name", f.stem)), "shells": bg_shells(r.get("pid")),
        })
    keep = {str(a["out"]) for a in agents}
    for k in [k for k in _activity if k not in keep]: del _activity[k]
    st = jload(STATE / "starter.json", {}) or {}
    pending = [{"name": n, "posts": len(v), "threads": sorted({x.get("thread") or "" for x in v}), "ids": [x.get("id") for x in v][-50:]}
               for n, v in (st.get("pending") or {}).items()]
    usage = {k: v for k, v in (jload(STATE / "usage.json", {}) or {}).items() if not k.startswith("_")}
    return {"signin": signin_waiting(st), "now": time.time(), "agents": agents, "jobs": {"running": sum(j["running"] for j in jobs.values()), "queued": sum(j["queued"] for j in jobs.values())}, "pending": pending, "usage": usage, "logins": logins(),
            "limit_until": limit_hold(st), "chosen": chosen_models(st), "efforts": chosen_efforts(st), "priorities": priorities(),
            "joblines": job_lines(runs)}


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



# ---- Work tiers (core/tiers.py; Settings > Work tiers). The person's own changes: tiers.py runs with no HIVE_NAME, so it logs "person".
WT_KEY = re.compile(r"(focus|thread|job):[a-z0-9][a-z0-9._*?\[\]-]{0,80}")
WT_TIER = re.compile(r"[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}")
WT_OPS = {"set": ("what", "tier"), "unset": ("what",), "stop": ("tier", "pct"), "pause": ("tier",), "resume": ("tier",),
          "new": ("name",), "rename": ("tier", "name"), "drop": ("tier",), "default": ("tier",)}


def _wt_run(args):
    env = {**{k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_DOOR")}, "HIVE_STATE": str(STATE)}
    return subprocess.run(["nice", "-n", "15", sys.executable, "-I", str(HOME / "core" / "tiers.py")] + args, capture_output=True, text=True, timeout=60, env=env)


def _wt_say(e, names):
    """A tiers-log line in plain words: no epochs, tier names instead of ids."""
    w, nm = str(e.get("what") or ""), lambda i: names.get(str(i), f"tier {i}")
    when = lambda u: datetime.fromtimestamp(float(u)).strftime("%a %d %b %H:%M")
    if m := re.fullmatch(r"set (focus|thread|job):(\S+) (\S+) -> (\S+)", w): return f"Moved {m[1]} {m[2]} to {nm(m[4])}"
    if m := re.fullmatch(r"unset (focus|thread|job):(\S+) \(was (\S+)\)", w): return f"Took {m[1]} {m[2]} out of {nm(m[3])}" + (" (it follows its focus now)" if m[1] == "thread" else "")
    if m := re.fullmatch(r"stop (\S+) (\S+) -> (\S+)", w): return f"{nm(m[1])}: " + ("runs to the accounts' own limits" if m[3] == "None" else f"stops at {float(m[3]):g}% of the week")
    if m := re.fullmatch(r"pause (.+?) until (\S+) \((.*)\)", w):
        return "Paused " + ", ".join(nm(x) for x in m[1].split()) + (f" until {when(m[2])}" if m[2] != "None" else " until resumed") + (f": {m[3]}" if m[3] not in ("None", "", "from Settings") else "")
    if m := re.fullmatch(r"resume (.+)", w): return "Resumed " + ", ".join(nm(x) for x in m[1].split())
    if m := re.fullmatch(r"rename (\S+) (.+) -> (.+)", w): return f"Renamed {m[2]} to {m[3]}"
    if m := re.fullmatch(r"default (\S+) -> (\S+)", w): return f"New work with no tier now goes to {nm(m[2])}"
    if m := re.fullmatch(r"drop (\S+); moved .* to (\S+)", w): return f"Removed tier {m[1]}; what it held went to {nm(m[2])}"
    if w.startswith("new "): return "Added a tier"
    return re.sub(r" ?\((brief|post) [0-9a-f]{12}\)", "", re.sub(r"\b1[0-9]{9}(\.[0-9]+)?\b", lambda m: when(m[0]), w))


def work_tiers():
    """Settings > Work tiers: each tier as tiers.py lists it, and the last 10 changes, newest first, in plain words."""
    r = _wt_run(["list", "--json"])
    if r.returncode: raise ValueError((r.stderr or r.stdout).strip()[-300:] or "tiers.py failed")
    tiers = json.loads(r.stdout)
    for t in tiers: (t.get("paused") or {}).pop("by", None)
    names = {str(t["id"]): t["name"] for t in tiers}
    log = []
    for l in plain_file(STATE / "tiers-log.jsonl", tail=True, size=32768).splitlines()[-10:]:
        try: log.append(json.loads(l))
        except ValueError: pass   # a line cut by the tail read
    return {"tiers": tiers, "log": [{"at": e.get("at"), "who": "you" if e.get("who") == "person" else e.get("who"), "text": _wt_say(e, names)} for e in reversed(log)]}


def work_tier_change(d):
    """One change from the page ({op, ...}), then the whole state again. ValueError carries tiers.py's own words."""
    op = d.get("op")
    if op not in WT_OPS: raise ValueError("unknown change")
    a = {k: d.get(k) for k in WT_OPS[op]}
    for k, v in a.items():
        if not isinstance(v, str) or not (WT_KEY if k == "what" else WT_TIER if k in ("tier", "name") else re.compile(r"none|[0-9]{1,3}(\.[0-9]+)?")).fullmatch(v): raise ValueError(f"bad {k}")
    args = [op] + [a[k] for k in WT_OPS[op]]
    if op == "pause":
        u = d.get("until")
        if u is not None:
            if not isinstance(u, (int, float)) or isinstance(u, bool) or not time.time() < u < time.time() + 366 * 86400: raise ValueError("bad until")
            args += ["--until", str(int(u))]
        args += ["--why", "from Settings"]
    if op == "new" and isinstance(d.get("pct"), str) and re.fullmatch(r"[0-9]{1,3}", d["pct"]): args += ["--stop", d["pct"]]
    r = _wt_run(args)
    if r.returncode: raise ValueError(((r.stderr or r.stdout).strip().splitlines() or ["that didn't work"])[-1].removeprefix("hive tier: "))
    return work_tiers()

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
        self.thread = {}  # session log's stem -> the thread its run was started in, for the foci's cost (by_thread)
        self.lock = threading.Lock()
        # Each file's figures carry "v", the count of changes when they last changed, so a page asks /api/usage?since=V&epoch=E for
        # only what changed (a few KB, not the 2 MB of every session ever, every 3 s); epoch is new each start, so V never carries over.
        self.ver, self.epoch = 0, secrets.token_hex(4)

    def scan_file(self, p):
        name = p.stem
        f = self.files.get(name) or {"off": 0, "c": 0.0, "t": [0, 0, 0, 0], "m": {}, "h": {}, "tools": {}, "turns": 0, "ts": None}
        was = f["off"] if name in self.files else -1
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
                if self.scanning:   # the first pass reads every log (17 GB on 2026-11-25), and its page cache was charged to the dash: it sat at its 2G cap
                    try: os.posix_fadvise(fh.fileno(), 0, 0, os.POSIX_FADV_DONTNEED)   # for minutes after each start, reclaiming on every request (a54ea's review)
                    except (OSError, AttributeError): pass
        except OSError:
            return
        if f["off"] != was: self.ver += 1; f["v"] = self.ver
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
                    if e.get("out") and e.get("thread"): self.thread[Path(e["out"]).stem] = e["thread"]
        except OSError: pass

    def by_thread(self, since):
        """{thread: cost} since `since` (a timestamp), from each session log's hourly figures laid on the thread its run was started in;
        "" is the cost of logs whose run no start event names (a session not begun by the starter). Same figures as the Usage page."""
        out = {}
        with self.lock:
            for name, f in self.files.items():
                c = sum(v for hr, v in (f.get("h") or {}).items() if int(hr) + 3600 > since)
                if c: th = self.thread.get(name, ""); out[th] = out.get(th, 0.0) + c
        return out

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
            files = {k: {x: v.get(x) for x in ("c", "t", "m", "mh", "h", "tools", "turns", "init", "v")} for k, v in self.files.items() if v["turns"] or v["tools"] or v.get("init")}
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
            return {"files": files, "logins": by_login, "ver": self.ver, "epoch": self.epoch,
                    "scanning": self.scanning, "stop_pct": cfg.get("usage_stop_pct"), "providers": dict(PROVIDERS)}


class AgentCosts:
    """Each agent's cost and tokens from its transcripts, by hive-cost in its own process (the session logs Usage reads undercount and
    miss killed runs). It takes seconds of disk, so it runs only when somebody asks, at most once per `every` seconds; a request
    never waits for it, and gets the last result with "refreshing" set. No hive-cost: "by" stays empty and the page uses Usage's figures."""
    TOOL = Path(__file__).resolve().parent.parent / "cost" / "cost.py"

    def __init__(self, every=1800):   # half-hourly: a running agent's newest spend comes from its session log meanwhile (model.js agentUsd)
        self.every, self.at, self.by, self.busy, self.lock = every, None, {}, False, threading.Lock()
        # The last read survives a restart: hive-cost takes about 4 min, and until it answered the Agents page showed grey cost tiles (a0ad9's
        # re-check, 2026-11-25). The kept read goes out with its own time, so the page says how old it is, and a fresh one starts as usual.
        self.save = HOME / ".cache" / "hive-dash" / "agent-costs.json"
        try:
            v = json.loads(self.save.read_text())
            if isinstance(v.get("by"), dict) and v.get("at"): self.at, self.by = float(v["at"]), v["by"]
        except (OSError, ValueError, TypeError, AttributeError):
            pass

    def refresh(self):
        try:
            r = subprocess.run(self.scope() + ["nice", sys.executable, "-I", str(self.TOOL), "--by", "agent", "--json"], capture_output=True, text=True, timeout=900)   # 4 min over 17 GB of logs on 2026-11-25: the old 180 s cut it off every time, so it never answered and ran again every 5 min
            rows = json.loads(r.stdout)["rows"] if r.returncode == 0 else None
        except (OSError, ValueError, KeyError, subprocess.SubprocessError):
            rows = None
        with self.lock:
            self.busy = False
            if rows is not None:
                self.at = time.time()
                self.by = {x["key"][0]: {"usd": x["usd"], "calls": x["calls"], "t": x["tokens"], "sub": x["sub_usd"],
                                         "m": {k: v for k, v in x["models"].items() if k != "<synthetic>"}} for x in rows}
                try:
                    self.save.parent.mkdir(parents=True, exist_ok=True); tmp = self.save.with_suffix(".tmp")
                    tmp.write_text(json.dumps({"at": self.at, "by": self.by})); tmp.replace(self.save)
                except OSError:
                    pass

    SCOPE = ["systemd-run", "--user", "--scope", "--quiet", "--collect", "-p", "MemoryMax=1G"]
    _scoped = None

    def scope(self):
        """Run hive-cost in a cgroup of its own (a54ea's review, 2026-11-25): reading 17 GB of logs filled the dash's own 2 GB cgroup with
        page cache and hive-cost's heap, so the dash ran pinned at its ceiling (memory.events max 14777 in a few hours) and every request paid for
        reclaim. In its own scope the cache is charged there, and its 1G cap reclaims from hive-cost, not the dash. No user systemd: run as before."""
        if AgentCosts._scoped is None:
            try:
                AgentCosts._scoped = subprocess.run(self.SCOPE + ["true"], capture_output=True, timeout=20).returncode == 0
            except (OSError, subprocess.SubprocessError):
                AgentCosts._scoped = False
        return list(self.SCOPE) if AgentCosts._scoped else []

    def view(self):
        with self.lock:
            if not self.busy and time.time() - (self.at or 0) > self.every and self.TOOL.exists():
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            return {"at": self.at, "refreshing": self.busy, "by": self.by}


class Subagents:
    """The helpers (subagents) the Hive's agents started inside Claude Code or Codex, last `days` days, from core/subagents.py in this
    process with its memo, so a helper file is read again only when it changed. Like AgentCosts it never makes a request wait:
    a read older than `every` seconds starts a fresh one in a thread and the last result goes out with "refreshing" set.
    No core/subagents.py: rows stay empty. Asked for by the person, thread 111919537629."""
    TOOL = HOME / "core" / "subagents.py"

    def __init__(self, every=45, days=7):
        self.every, self.days, self.at, self.rows, self.busy, self.memo, self.lock, self.mod = every, days, None, [], False, {}, threading.Lock(), None

    def refresh(self):
        rows = None
        try:
            if self.mod is None:
                spec = importlib.util.spec_from_file_location("h2_subagents", self.TOOL); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); self.mod = m
            keep = {"harness", "id", "parent", "type", "desc", "model", "mode", "depth", "start", "end", "state", "calls", "tools", "out_tokens", "usd", "file", "agent", "agent_thread", "moved",
                    "kind", "pid", "by", "cwd"}   # kind process / workflow: a claude or codex process outside an agent's run, or a Workflow script; by: what started it when no agent did
            got = self.mod.collect(time.time() - self.days * 86400, self.memo)
            seen = {r.get("file") for r in got}
            for f in [f for f in self.memo if f not in seen]: del self.memo[f]   # a helper file gone or out of the window
            rows = [{k: v for k, v in r.items() if k in keep} for r in got if r.get("agent") or r.get("by")]
            for r in rows:
                if r.get("file"): r["file"] = redact(r["file"].replace(str(HOME) + "/", "", 1))
                r["desc"] = redact(str(r.get("desc") or ""))[:200]
                for k in ("by", "cwd"):
                    if r.get(k): r[k] = redact(str(r[k]))[:120]   # redact first: a cut can leave half a secret the patterns no longer match
        except Exception as e:
            print(f"subagents: {type(e).__name__}: {e}", file=sys.stderr, flush=True)
        with self.lock:
            self.busy = False
            if rows is not None: self.at, self.rows = time.time(), rows

    def view(self):
        with self.lock:
            if not self.busy and time.time() - (self.at or 0) > self.every and self.TOOL.exists():
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            rows, now = self.rows, time.time()
            if self.at and now - self.at > self.every and any(r.get("state") == "running" for r in rows):
                # an old read while the fresh one runs (after an idle spell): a helper quiet longer than any running one can be is not running
                most = getattr(self.mod, "LIVE_OWNED", 900)
                rows = [{**r, "state": "stopped"} if r.get("state") == "running" and now - (r.get("moved") or 0) > most else r for r in rows]
            return {"at": self.at, "refreshing": self.busy, "days": self.days, "rows": rows}


def subagents_day(rows, now=None):
    """The last 24 hours of helpers in numbers, for the Today card beside the running ones: how many finished (done) or were cut off
    when their owner ended (stopped), what they cost at list prices (already inside their agents' costs), and their working time."""
    now = now or time.time(); since = now - 86400; out = {"done": 0, "stopped": 0, "usd": 0.0, "ms": 0}
    def ts(v):
        try: return datetime.fromisoformat(str(v).replace("Z", "+00:00")).timestamp()
        except (TypeError, ValueError): return None
    for r in rows:
        a, b = ts(r.get("start")), (now if r.get("state") == "running" else ts(r.get("end")))
        if a is not None and b is not None and b > since: out["ms"] += int(1000 * max(0, min(b, now) - max(a, since)))
        if r.get("state") in ("done", "stopped") and b is not None and since <= b <= now:
            out[r["state"]] += 1; out["usd"] += r.get("usd") or 0
    out["usd"] = round(out["usd"], 2)
    return out


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



# ---- everyday E42: the code a reply ran, shown folded under it (GAPS row 4: "the user never sees it run") ----
# A post's run is the agent's session log that holds the tool result printing the post's id (`hive post` prints it). The steps are the
# Bash calls (and code files written) after the run's previous post and before this one, less the hive's own plumbing (hive, kb).
RAN_SKIP = {"hive", "kb", "sleep", "true", ":", "hive"}
RAN_EXT = {".py", ".js", ".mjs", ".ts", ".sh", ".r", ".jl", ".rb", ".go", ".rs", ".c", ".cc", ".cpp", ".sql", ".m", ".pl", ".lua"}
_RAN_LEAD = re.compile(r"^\s*(?:cd\s+(?:\"[^\"]*\"|'[^']*'|\S+)\s*(?:&&|;)\s*|[A-Za-z_][A-Za-z0-9_]*=\S*\s+|heavy\s+(?:--quick\s+|timeout\s+\d+\s+)?|timeout\s+\d+\s+|nice\s+)+")
_RAN_ID = re.compile(r"\b[0-9a-f]{12}\b")
_ran_files = {}   # session log -> ((mtime, size), {"calls": [...], "posts": {post id: call index}})


def _ran_word(cmd):
    """The command's first real word: past cd, env settings, heavy and timeout."""
    m = _RAN_LEAD.match(cmd or ""); rest = (cmd or "")[m.end():] if m else (cmd or "")
    w = rest.split(None, 1)
    return Path(w[0]).name if w else ""


def _ran_text(c):
    c = c.get("content") if isinstance(c, dict) else c
    if isinstance(c, list): c = "\n".join(str(x.get("text", "")) for x in c if isinstance(x, dict) and x.get("type") == "text")
    return c if isinstance(c, str) else ""


def _ran_clip(s, n=4000):
    s = str(s or "")
    return s if len(s) <= n else s[: n * 3 // 4] + f"\n… ({len(s) - n} characters left out) …\n" + s[-n // 4:]


def _ran_parse(p):
    """{"calls": [...], "posts": {post id: call index}} for a session log. Read on from where the last read stopped (E42b: the open
    chat reads a live run's steps every 0.5 s, and its log grows by streamed pieces many times a second)."""
    try: st = p.stat(); key = (st.st_mtime, st.st_size)
    except OSError: return None
    hit = _ran_files.get(str(p))
    if hit and hit[0] == key: return hit[1]
    v = hit[1] if hit and hit[1]["off"] <= st.st_size else None
    if v is None: v = {"calls": [], "posts": {}, "idx": {}, "off": None}
    calls, idx, posts = v["calls"], v["idx"], v["posts"]
    try:
        with open(p, "rb") as f:
            if v["off"] is None:
                f.seek(0, 2); n = f.tell(); f.seek(max(0, n - 64 * 1024 * 1024))
                if n > 64 * 1024 * 1024: f.readline()   # drop a partial line
            else: f.seek(v["off"])
            start = f.tell(); data = f.read()
    except OSError: return None
    cut = data.rfind(b"\n") + 1   # a line still being written is read next time
    v["off"] = start + cut
    for ln in data[:cut].splitlines():
        if ln.startswith(b'{"type":"stream_event"') or ln.startswith(b'{"type":"system"'): continue   # the bulk of a live log, and never a call
        try: e = json.loads(ln)
        except Exception: continue
        t = e.get("type")
        if t == "assistant":
            for c in (e.get("message") or {}).get("content") or []:
                if isinstance(c, dict) and c.get("type") == "tool_use" and c.get("id") not in idx:
                    idx[c.get("id")] = len(calls); calls.append({"name": c.get("name"), "in": c.get("input") or {}, "t": ts(e.get("timestamp")), "out": "", "err": False, "done": False})
        elif t == "user":
            for c in _blocks((e.get("message") or {}).get("content")):
                if c.get("type") == "tool_result" and c.get("tool_use_id") in idx:
                    i = idx[c["tool_use_id"]]; k = calls[i]
                    k["out"], k["err"], k["done"] = _ran_text(c), bool(c.get("is_error")), True
                    if k["name"] == "Bash" and _POST_CMD.search(str(k["in"].get("command") or "") + "\n"):
                        for pid in _RAN_ID.findall(k["out"][:400]): posts.setdefault(pid, i)
    if len(_ran_files) > 64: _ran_files.pop(next(iter(_ran_files)))
    _ran_files[str(p)] = (key, v)
    return v


def _ran_step(c):
    """A tool call as a step to show, or None for one that isn't code (the hive's own plumbing, reading, searching)."""
    i = c["in"]
    if c["name"] == "Bash":
        cmd = str(i.get("command") or "")
        if not cmd.strip() or _POST_CMD.search(cmd + "\n") or _ran_word(cmd) in RAN_SKIP: return None
        lang = "python" if _ran_word(cmd).startswith("python") else "node" if _ran_word(cmd) == "node" else "shell"
        return {"what": short(i.get("description") or "", 160), "lang": lang, "code": redact(_ran_clip(cmd)), "out": redact(_ran_clip(c["out"])), "err": c["err"], "t": c["t"],
                **({} if c.get("done", True) else {"running": True})}
    if c["name"] in ("Write", "Edit"):
        fp = str(i.get("file_path") or "")
        if Path(fp).suffix.lower() not in RAN_EXT: return None
        code = i.get("content") if c["name"] == "Write" else i.get("new_string")
        return {"what": ("Wrote " if c["name"] == "Write" else "Changed ") + Path(fp).name, "lang": "file", "file": Path(fp).name,
                "code": redact(_ran_clip(code)), "out": "", "err": c["err"], "t": c["t"]}
    return None


def ran_for(pid, who=None, at=None, limit=30):
    """{"steps": [...], "more": N} for the code the run that wrote post PID ran before writing it; {"steps": []} when none or not found.
    WHO is the post's author (the board's word wins over the caller's), AT its time (a hint for which logs to look in)."""
    if not re.fullmatch(r"[0-9a-f]{12}", pid or ""): return {"steps": []}
    who = INDEX.authors.get(pid) or who or ""
    if not AGENT_CODE.fullmatch(who.lower()): return {"steps": []}
    try: fs = sorted((HOME / "sessions").glob(who + "-*.jsonl"), key=lambda q: q.stat().st_mtime)
    except OSError: return {"steps": []}
    if at: fs = [q for q in fs if q.stat().st_mtime >= float(at) - 5][:4]
    else: fs = fs[-4:]
    for q in fs:
        try:
            with open(q, "rb") as fh: fh.seek(0, 2); n = fh.tell(); fh.seek(max(0, n - 64 * 1024 * 1024)); has = pid.encode() in fh.read()
        except OSError: continue
        if not has: continue
        v = _ran_parse(q)
        if not v or pid not in v["posts"]: continue
        end = v["posts"][pid]; start = max([i for i in v["posts"].values() if i < end], default=-1) + 1
        steps = [s for s in (_ran_step(c) for c in v["calls"][start:end]) if s]
        return {"steps": steps[-limit:], "more": max(0, len(steps) - limit)}
    return {"steps": []}


_ran_live = {}   # out file -> ((mtime, size), result): the steps of a live run, kept until its log changes


def ran_live(out, limit=30):
    """E42b: {"steps": [...], "more": N} the code a live run has run since its last post (a step with no result yet is "running").
    The same calls, cut the same way, as the fold ran_for() gives the post it writes next, so what showed live is what stays."""
    p = Path(out or "")
    try: st = p.stat(); key = (st.st_mtime, st.st_size)
    except OSError: return {"steps": [], "more": 0}
    hit = _ran_live.get(str(p))
    if hit and hit[0] == key: return hit[1]
    v = _ran_parse(p)
    if not v: return {"steps": [], "more": 0}
    start = max(v["posts"].values(), default=-1) + 1
    steps = [s for s in (_ran_step(c) for c in v["calls"][start:]) if s]
    r = {"steps": steps[-limit:], "more": max(0, len(steps) - limit)}
    if len(_ran_live) > 64: _ran_live.pop(next(iter(_ran_live)))
    _ran_live[str(p)] = (key, r)
    return r


def _blocks(c):
    """A message's content as a list of blocks: a bare string is one text block."""
    return [{"type": "text", "text": c}] if isinstance(c, str) else [b for b in c or [] if isinstance(b, dict)]


def _part_text(b):
    c = b.get("content") if b.get("type") == "tool_result" else b.get("text") or b.get("thinking") or b.get("input") or ""
    if isinstance(c, list): c = " ".join(str(x.get("text", "")) for x in c if isinstance(x, dict))
    return c if isinstance(c, str) else json.dumps(c, default=str)


def agent_input(name, full=True, limit=40, turn=None):
    """What went into each model call of an agent's newest session: the call's exact input tokens (fresh, cache write, cache read)
    from the log, and the content that arrived since the previous call, by source, sized by an estimate (4 characters a token).
    What was already there is 'carried'; on the first call it is the system prompt, tool list, memory and first message, which the log doesn't keep.
    The list carries sizes only, since the page polls it. `turn` (a call's id) returns that one call with its pieces whole, as the log has them, secrets hidden."""
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,40}", name or ""): return {"turns": []}
    if turn is not None and not re.fullmatch(r"[A-Za-z0-9_-]{1,100}", turn): return {"turns": []}
    try: q = sorted((HOME / "sessions").glob(name + "-*.jsonl"), key=lambda x: x.stat().st_mtime)[-1]
    except (OSError, IndexError): return {"turns": []}
    names, turns, pend, seen = {}, [], [], None
    def add(src, label, b):
        x = _part_text(b)
        pend.append({"src": src, "label": label, "n": max(1, len(x) // 4), "x": x})
    for e in jlines(q, 3 * 1024 * 1024):
        k, m = e.get("type"), e.get("message") or {}
        if k == "assistant":
            u = m.get("usage")
            if u and m.get("id") != seen:
                seen = m.get("id")
                fresh, cw, cr = u.get("input_tokens") or 0, u.get("cache_creation_input_tokens") or 0, u.get("cache_read_input_tokens") or 0
                parts, whole = [], full and turn == seen
                for r in pend:  # same source and label merge into one row
                    x = redact(r["x"]) if whole else ""
                    o = next((y for y in parts if y["src"] == r["src"] and y["label"] == r["label"]), None)
                    if o: o["n"] += r["n"]; o["items"].append(x)
                    else: parts.append({**r, "items": [x]})
                for r in parts: r.pop("x", None)
                turns.append({"id": seen, "t": ts(e.get("timestamp")), "fresh": fresh, "cw": cw, "cr": cr, "out": u.get("output_tokens") or 0, "parts": parts})
                pend = []
            for b in _blocks(m.get("content")):
                t = b.get("type")
                if t == "tool_use":
                    names[b.get("id")] = b.get("name"); add("self", "its tool calls", {"input": json.dumps(b.get("input"), default=str)})
                elif t == "text": add("self", "its own words", b)
                elif t == "thinking": add("self", "its thinking", b)
        elif k == "user":
            for b in _blocks(m.get("content")):
                if b.get("type") == "tool_result": add("tool", names.get(b.get("tool_use_id")) or "tool", b)
                elif b.get("type") == "text":
                    x = b.get("text") or ""
                    add("hive", "system reminder" if "<system-reminder>" in x[:200] else "message to it", b)
    turns = turns[-limit:]
    for i, r in enumerate(turns):
        r["total"] = r["fresh"] + r["cw"] + r["cr"]
        r["carried"] = max(0, r["total"] - sum(y["n"] for y in r["parts"]))
    if turn is not None: turns = [r for r in turns if r["id"] == turn]
    return {"turns": turns, "file": q.name, "text": bool(full)}


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


def agent_diff(name, path, full=True):
    """One file an agent changed, as a git diff: what differs from the last commit now, or else the last commit that touched it.
    Only a file in the agent's own work list (agent_work) is read, so this is no way to read other files. Full sign-in only."""
    if not full: return {"error": "your sign-in sees counts only"}
    ok = {r["p"] for r in agent_work(name, True).get("files", [])}
    if path not in ok: return {"error": "not a file this agent changed"}
    f = Path(HOME / path[2:] if path.startswith("~/") else path)
    if not f.is_absolute() or ".." in f.parts: return {"error": "not a plain path"}
    run = lambda *a: subprocess.run(["git", "-C", str(f.parent), *a], capture_output=True, text=True, timeout=10, errors="replace")
    try:
        if not f.parent.is_dir(): return {"error": "folder is gone"}
        if run("rev-parse", "--show-toplevel").returncode: return {"error": "not in a git folder, so no diff to show"}
        if not f.exists(): what, r = "deleted", run("diff", "HEAD", "--", f.name)
        elif run("ls-files", "--error-unmatch", "--", f.name).returncode:
            what, r = "new, not yet committed", run("diff", "--no-index", "--", "/dev/null", f.name)
        else:
            what, r = "not yet committed", run("diff", "HEAD", "--", f.name)
            if not r.stdout.strip(): what, r = "last commit that touched it", run("log", "-1", "-p", "--format=commit %h %s", "--", f.name)
    except (OSError, subprocess.SubprocessError) as e: return {"error": str(e)[:200]}
    d = redact(r.stdout)
    return {"p": path, "what": what, "diff": d[:60000], "cut": len(d) > 60000}


_libc = None


def trim_heap():
    """Hand freed heap back to the system (glibc malloc_trim). A parse that peaked at hundreds of MB otherwise stays charged to the
    service's 2 GB cap as free-but-held pages, across one arena per request thread (24 of them on 2026-11-25, friction-dash)."""
    global _libc
    try:
        if _libc is None:
            import ctypes, ctypes.util
            _libc = ctypes.CDLL(ctypes.util.find_library("c") or "libc.so.6")
        _libc.malloc_trim(0)
    except (OSError, AttributeError): _libc = False


class Steps:
    """Every agent's tool calls and words, from all the session logs, for the Ledger page (/api/ledger). Each log is read on
    from where the last look stopped, so after the first pass a look costs only what was written since. Kept in memory as short
    rows (a few tens of MB for tens of thousands of steps); filtered and paged here, so the page only ever gets one page.
    Codex logs carry no times, so they're left out. Each log's agent and thread come from its start event in ~/log.jsonl."""
    EVERY, STALE = 5, 60
    DAYS = 7   # steps older than this are dropped and older logs not read: all 13,700 logs held 596k rows, 415 MB of a 2 GB service (OOM-killed 28 times in 14 days, friction-dash)

    def __init__(self):
        self.rows, self.off, self.owner, self.log_off, self.at, self.sorted = [], {}, {}, 0, 0, True
        self.lock, self.busy, self.ready, self.trimmed = threading.Lock(), False, False, 0.0

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
        cut = time.time() - self.DAYS * 86400
        for q in files:
            k = q.name
            try: st = q.stat(); size = st.st_size
            except OSError: continue
            if size <= self.off.get(k, 0): continue
            if st.st_mtime < cut: self.off[k] = size; continue   # nothing in it is inside the window
            name, thread = self.owner.get(k) or (k.split("-")[0], "")
            name, thread = sys.intern(name), sys.intern(thread)   # tens of thousands of rows share a few hundred names
            off = self.off.get(k, 0)
            try:
                with open(q, "rb") as fh:
                    fh.seek(off)
                    while True:
                        ln = fh.readline(1 << 20)   # capped read: a user line with a pasted image runs to hundreds of MB, and the old whole-line read spiked the service past its 2G cap (OOM flapping 2026-11-19)
                        if not ln: break
                        if not ln.endswith(b"\n"):
                            if len(ln) < (1 << 20): break   # a line still being written
                            skip = 0
                            while True:   # an over-long line: skip the rest of it in chunks, keep none
                                part = fh.readline(1 << 20)
                                skip += len(part)
                                if not part or part.endswith(b"\n"): break
                            if not part.endswith(b"\n"): break
                            off += len(ln) + skip
                            continue
                        off += len(ln)
                        if not ln.startswith(b'{"type":"assistant"'): continue
                        try: e = json.loads(ln)
                        except ValueError: continue
                        t = ts(e.get("timestamp"))
                        if not t or t < cut: continue
                        for c in (e.get("message") or {}).get("content") or []:
                            if c.get("type") == "tool_use":
                                inp = c.get("input") or {}
                                tool = sys.intern(str(c.get("name") or ""))
                                d = inp.get("command") or inp.get("file_path") or inp.get("notebook_path") or inp.get("pattern") or inp.get("url") or inp.get("query") or inp.get("prompt") or ""
                                x, d = redact(short(describe_tool(tool, inp), 220)), redact(short(d, 400))
                                new.append((t, name, thread, "tool", tool, x, d))
                            elif c.get("type") == "text" and str(c.get("text") or "").strip():
                                x = redact(short(c["text"], 400))
                                new.append((t, name, thread, "said", "", x, ""))
                    if not self.ready:   # the first pass reads a week of sessions (GBs); drop their page cache so it isn't charged to the dash (same as Usage)
                        try: os.posix_fadvise(fh.fileno(), 0, 0, os.POSIX_FADV_DONTNEED)
                        except (OSError, AttributeError): pass
            except OSError: pass
            self.off[k] = off
        return new

    def refresh(self):
        try:
            new = self.scan()
            with self.lock:
                if new: self.rows.extend(new); self.sorted = False
                cut = time.time() - self.DAYS * 86400
                if time.time() - self.trimmed > 3600:   # hourly, sorted first so the old rows are one slice
                    self.trimmed = time.time()
                    if not self.sorted: self.rows.sort(key=lambda r: r[0]); self.sorted = True
                    self.rows = self.rows[bisect.bisect_left(self.rows, cut, key=lambda r: r[0]):]
                self.at, self.ready = time.time(), True
            if new: trim_heap()
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
            if words:   # the search text is built per row here, not kept: it doubled the memory of every step
                hay = " ".join((r[1], r[2], r[4], r[5], r[6])).lower() if r[3] == "tool" else " ".join((r[1], r[2], r[5])).lower()
                if not all(w in hay for w in words): continue
            total += 1
            if len(out) < limit: out.append(list(r[:7]) if full else [*r[:5], "", ""])
        # rows last read over a minute ago (nobody had the page open) are not ready: the page says it is reading and asks again,
        # rather than showing hours-old steps that the fresh events then push out of its newest 300
        return {"rows": out, "total": total, "ready": self.ready and time.time() - self.at < self.STALE}


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

def static_cache(route):
    """Cache-Control for the page's own css and js: a minute of reuse through the tunnel, a revalidation every load on this machine."""
    return "private, max-age=60" if route == "outside" else "no-cache, private"

_index = [None, b""]
def index_html():
    """index.html with a modulepreload link for every module app.js imports, found by following the static imports.
    Without them the browser finds the 120 modules one import level at a time: seven round trips through the tunnel before anything draws."""
    stamp = web_stamp()
    if _index[0] == stamp: return _index[1]
    page = (UI_DIR / "index.html").read_bytes()
    js = (UI_DIR / "js").resolve(); seen, todo = set(), [js / "app.js"]
    imp = re.compile(r'^\s*(?:import|export)\s[^;"]*?from\s+"(\.[^"]+)"|^\s*import\s+"(\.[^"]+)"', re.M)
    while todo:
        f = todo.pop()
        if f in seen or not f.is_file() or not f.is_relative_to(js): continue
        seen.add(f)
        for m in imp.finditer(f.read_text(errors="replace")): todo.append((f.parent / (m[1] or m[2])).resolve())
    seen.discard(js / "app.js")
    links = "".join(f'<link rel="modulepreload" href="js/{f.relative_to(js).as_posix()}">\n' for f in sorted(seen)).encode()
    tag = b'<script type="module" src="js/app.js">'
    out = page.replace(tag, links + tag, 1) if tag in page else page
    # Each stylesheet's address carries its own version, so no cache on the way (the tunnel's edge, the browser) can hand back an old one after a change.
    css = lambda m: b'href="css/%s?v=%x"' % (m[1], (UI_DIR / "css" / m[1].decode()).stat().st_mtime_ns if (UI_DIR / "css" / m[1].decode()).is_file() else 0)
    out = re.sub(rb'href="css/([a-z0-9_-]+\.css)"', css, out)
    _index[:] = [stamp, out]
    return out


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
    so no request waits. Only the first read waits, and a read after .t = 0, which is how a change asks for the truth.
    A change that is known exactly goes in with patch() instead, and then nobody waits at all."""
    busy, stamp = False, 0

    def get(self):
        with self.lock:
            if self.v is None or not self.t: self.v, self.t = self.fn(), time.time()
            elif time.time() - self.t > self.ttl and not self.busy:
                self.busy = True
                threading.Thread(target=self.refresh, daemon=True).start()
            return self.v

    def refresh(self):
        stamp = self.stamp
        try:
            v = self.fn()
            with self.lock:
                if self.t and stamp == self.stamp: self.v, self.t = v, time.time()  # .t = 0 or a patch meanwhile: what was just read may predate the change (the next get reads again)
        finally: self.busy = False

    def patch(self, fn):
        """Put a change that is known exactly into the held answer (fn(old) gives the new one), so the next request neither waits on a
        fresh read nor gets the answer from before the change. With nothing held, or a patch that fails, the next get reads the truth."""
        with self.lock:
            self.stamp += 1
            if self.v is None or not self.t: return
            try: self.v = fn(self.v)
            except Exception: self.t = 0

PROGRESS = CachedStale(progress_state, 300)  # the Progress page's data (progress_state above)


# Defaults of ~/.local/bin/heavy's start gates; test_jobs.py checks they still match the script.
HEAVY_MIN_MEM_MB, HEAVY_JOB_MB, HEAVY_MAX_STALL, HEAVY_LOAD_PER_CPU = 5000, 4500, 20, 3
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
    and what it uses, not the commands, which can carry paths and arguments."""
    if full or not isinstance(view, dict): return view
    return {k: [{f: v for f, v in r.items() if f != "cmd"} if isinstance(r, dict) else r for r in rows] if isinstance(rows, list) else rows
            for k, rows in view.items()}


def agents_for(view, full):
    """/api/live and /json as a caller may see them: the same rule as jobs_for. What an agent is doing or said can quote a command, so a limited sign-in sees who runs, not that."""
    if full or not isinstance(view, dict): return view
    return {**view, "agents": [{f: v for f, v in a.items() if f not in ("doing", "said", "draft")} for a in view.get("agents") or []],
            **({"joblines": [{f: v for f, v in j.items() if f not in ("cmd", "line")} for j in view.get("joblines") or []]} if "joblines" in view else {})}


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


# ---------- share this result (GET /api/share-preview, POST /api/share-build) ----------
# share.py reads the board and log and writes nothing; the page is built here and handed to the browser to save. Nothing leaves the machine.
def _share():
    spec = importlib.util.spec_from_file_location("share", Path(__file__).resolve().with_name("share.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
    m.HOME = HOME
    return m

def share_preview(thread):
    m = _share(); return m.preview(thread, HOME, (thread_names() or {}).get(thread))

def share_build(d):
    th = d.get("thread")
    if not isinstance(th, str) or not th: raise ValueError("which conversation?")
    rm = [x for x in d.get("remove") or [] if isinstance(x, str)]
    m = _share(); page, pv = m.build(th, rm, d.get("files") is not False, HOME, (thread_names() or {}).get(th))
    return {"html": page, "name": (re.sub(r"[^a-z0-9]+", "-", pv["title"].lower()).strip("-")[:40] or "result") + ".html"}


CALL_CHAT = "phone-chat"  # the board thread behind the call page's chat pane: agents post there with `hive post --thread phone-chat`
def call_chat(limit=60, window=3_000_000):
    """The newest posts in the call page's chat thread, read from the end of the board."""
    try:
        with open(BOARD, "rb") as f:
            size = f.seek(0, 2); f.seek(max(0, size - window)); data = f.read()
    except OSError: return {"posts": []}
    out = []
    for ln in data.splitlines():
        if CALL_CHAT.encode() not in ln: continue
        try: d = json.loads(ln)
        except ValueError: continue
        if d.get("thread") == CALL_CHAT: out.append({k: d.get(k) for k in ("id", "at", "from", "text", "files")})
    return {"posts": out[-limit:]}

def usage_since(v, q):
    """/api/usage, whole or, with ?since=V&epoch=E from this run of the dash, only the files that changed after V (delta: true)."""
    since, ep = (q.get("since") or [""])[0], (q.get("epoch") or [""])[0]
    if not since.isdigit() or ep != v.get("epoch"): return v
    n = int(since)
    return {**v, "delta": True, "files": {k: f for k, f in v["files"].items() if (f.get("v") or 0) > n}}


def tail_lines(path, offset, last=0, upto=None):
    """Whole lines appended since byte `offset`, unparsed: the browser parses them, and the file is only ever read.
    last=N: only about the last N bytes, from the first line start in them, so a first load can draw before the rest arrives.
    upto=B: stop at byte B (a line start an earlier tail read gave), for fetching the older part behind it.
    Returns (end offset, lines, start offset)."""
    try:
        with open(path, "rb") as f:
            size = f.seek(0, 2)
            if offset > size: offset = 0
            if last and size - offset > last:
                f.seek(size - last - 1)
                lead = f.read(last + 1)
                nl = lead.find(b"\n")
                if nl >= 0: offset = size - last - 1 + nl + 1
            f.seek(offset)
            data = f.read() if upto is None else f.read(max(0, min(upto, size) - offset))
    except OSError:
        return offset, [], offset
    cut = data.rfind(b"\n") + 1  # a line still being appended waits for the next poll
    return offset + cut, [ln.decode("utf-8", "replace") for ln in data[:cut].splitlines() if ln.strip()], offset


def _json_line(ln):
    try: json.loads(ln); return True
    except ValueError: return False


def tail_json(path, offset, last=0, upto=None, gz=False, step=1 << 20):
    """/api/board and /api/log's answer, '{"offset":..,"start":..,"items":[..]}', yielded a megabyte at a time (gzipped as it
    goes with gz=True); joined, the same bytes tail_lines gives. The whole board is 27 MB and every page load asks for all of
    it: built as one str, then its join, encoding and gzip, a request held ~5 copies at once, and concurrent loads left the
    dash at its 2 GB cap swapping and the machine out of memory (2026-11-15). Streamed, a request holds about one step."""
    z = zlib.compressobj(5, zlib.DEFLATED, 31) if gz else None
    pack = (lambda b: z.compress(b)) if z else (lambda b: b)
    try:
        f = open(path, "rb")
    except OSError:
        yield pack(b'{"offset":%d,"start":%d,"items":[]}' % (offset, offset))
        if z: yield z.flush()
        return
    with f:
        size = f.seek(0, 2)
        if offset > size: offset = 0
        if last and size - offset > last:
            f.seek(size - last - 1)
            nl = f.read(last + 1).find(b"\n")
            if nl >= 0: offset = size - last - 1 + nl + 1
        end = size if upto is None else max(offset, min(upto, size))
        cut = end   # back to the last newline: a line still being appended waits for the next poll
        while cut > offset:
            back = max(offset, cut - 65536); f.seek(back); blk = f.read(cut - back)
            nl = blk.rfind(b"\n")
            if nl >= 0: cut = back + nl + 1; break
            cut = back
        yield pack(b'{"offset":%d,"start":%d,"items":[' % (cut, offset))
        f.seek(offset); pos, carry, first = offset, b"", True
        while pos < cut:
            buf = carry + f.read(min(step, cut - pos)); pos += len(buf) - len(carry)
            k = buf.rfind(b"\n") + 1 if pos < cut else len(buf)
            blk, carry = buf[:k], buf[k:]
            try: blk.decode("utf-8")
            except UnicodeDecodeError: blk = blk.decode("utf-8", "replace").encode()
            lines = [ln for ln in blk.splitlines() if ln.strip() and _json_line(ln)]   # one torn line (a write cut off by a full disk, h4 2026-11-24) must not break the whole answer
            if lines:
                yield pack((b"" if first else b",") + b",".join(lines)); first = False
        yield pack(b"]}")
        if z: yield z.flush()


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
GET_CAP = {"/api/call-chat": "post", "/api/live": "read", "/api/drafts": "read", "/api/ran": "read", "/api/away": "read", "/api/board": "read", "/api/log": "read", "/api/usage": "read", "/api/work-tiers": "read", "/api/jobs": "read", "/api/agent": "read", "/api/agent-work": "read", "/api/agent-input": "read", "/api/agent-diff": "read", "/api/ledger": "read",
           "/api/projects": "read", "/api/agent-costs": "read", "/api/subagents": "read", "/api/thread-names": "read", "/api/thread-meta": "read", "/api/shown": "read", "/api/thread-status": "read", "/api/thread-shown": "read", "/api/foci": "read", "/api/agent-status": "read", "/api/thread-find": "read", "/json": "read", "/api/target": "read", "/api/inbox": "read", "/api/delivered": "read",
           "/api/config": "config", "/api/export": "config", "/api/modules": "modules", "/api/connectors": "modules", "/api/shops": "logins", "/api/mcp-url": "modules", "/api/voice": "modules", "/api/slack-door": "modules", "/api/signin": "logins", "/api/plans": "plans", "/api/secrets": "secrets",
           "/api/settings": "config", "/api/ntfy": "config", "/api/webpush": "config", "/api/update": "config", "/api/signin-codex": "logins", "/api/buy": "logins", "/api/orders": "logins", "/api/accounts": "logins", "/api/design": "logins", "/api/shop-signin": "logins", "/api/shop-signin/frame": "logins", "/api/brand/logo": "read",
           "/api/kb/home": "files", "/api/kb/open": "files", "/api/kb/search": "files", "/api/kb/raw": "files", "/api/kb/map": "files", "/api/kb/history": "files", "/api/kb/wipe": "files", "/api/kb/copies": "files", "/api/progress": "read", "/api/checklist": "read", "/api/share-preview": "read", "/api/approvals": "read", "/api/feedback-drafts": "read", "/api/mail": "files", "/api/docs": "files", "/api/docs/doc": "files", "/api/docs/page": "files"}
POST_CAP = {"/api/webpush": "config", "/api/share-build": "read", "/api/kill": "stop", "/api/kb/wipe": "files", "/api/kb/putback": "files", "/api/docs/notes": "files", "/api/approval": "stop", "/api/tier": "tier", "/api/work-tiers": "tier", "/api/agent-model": "post", "/api/agent-effort": "post", "/api/login-pause": "limits", "/api/login-limit": "limits", "/api/provider-limit": "limits",
            "/api/post": "post", "/api/side-ask": "post", "/api/door": "post", "/api/thread-meta": "post", "/api/shown": "post", "/api/loadtime": "post", "/api/opened": "post", "/api/checklist": "post", "/api/focus": "post", "/api/feedback": "post", "/api/feedback-draft-done": "post", "/api/issue-link": "post", "/api/update": "config", "/api/share-copy": "config", "/api/share-pack": "config", "/api/draft": "config", "/api/import": "config", "/api/upload": "post", "/api/inbox-read": "post", "/api/ntfy": "config", "/api/config": "config", "/api/default-model": "config", "/api/starter": "config",
            "/api/module": "modules", "/api/mcp-url": "modules", "/api/voice": "modules", "/api/signin": "logins", "/api/signin/code": "logins", "/api/signin/cancel": "logins", "/api/plan": "plans", "/api/kb/save": "plans", "/api/secret": "secrets", "/api/slack-door": "modules",
            "/api/signin-codex": "logins", "/api/buy": "logins", "/api/account-key": "logins", "/api/design": "logins", "/api/shop-signin": "logins", "/api/brand": "config", "/api/brand/logo": "config"}
LOCAL_ONLY = {"/api/access", "/api/settings", "/api/import", "/api/mcp-url", "/api/share-copy", "/api/share-pack", "/api/draft"}  # who may do what is never changed from outside, whatever outside.can says


_DOCS = []
def docs_mod():
    """docs.py, loaded once: the Documents page's list, pages and notes."""
    if not _DOCS:
        spec = importlib.util.spec_from_file_location("h2_docs", Path(__file__).resolve().with_name("docs.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); _DOCS.append(m)
    return _DOCS[0]


def word_shell(key="word_shell"):
    """The root-run word shell's origins (projects/word-shell/DESIGN.md): config.json word_shell, one origin or a list (its own name
    and its tailnet port). Unset = off. Any malformed entry turns it all off. The dash only tells the shell which thread is open.
    word_card (same shape): the origins of root's approval card (projects/word-shell/ONE-APP.md). Set it only once root serves /confirm there:
    while it is set, a yes: line's Send opens that card."""
    v = (jload(HOME / "core" / "config.json", {}) or {}).get(key)
    v = [v] if isinstance(v, str) else v
    if not isinstance(v, list) or not v or len(v) > 4: return None
    ok = all(isinstance(o, str) and re.fullmatch(r"https://[a-z0-9-]+(\.[a-z0-9-]+)*(:[0-9]{1,5})?", o) for o in v)
    return list(dict.fromkeys(v)) if ok else None


DOOR_ROUTE = re.compile(r"/api/[a-z][a-z-]{0,30}")


def doors():
    """config.json "doors": local services a project page in the preview may reach, each {port, page, secret, get, post, people}.
    "people" (optional) is a file of emails, one a line, written by the service: an outside sign-in must be one of them, so a second full person here doesn't get the first one's service.
    A door is one page (a project path) and a fixed list of that service's routes on 127.0.0.1; anything malformed is no door.
    The page itself stays in the sandboxed frame: it asks this page by postMessage, and this page calls POST /api/door."""
    out = {}
    v = (jload(HOME / "core" / "config.json", {}) or {}).get("doors")
    for name, x in (v.items() if isinstance(v, dict) else ()):
        if not (isinstance(name, str) and re.fullmatch(r"[a-z][a-z0-9-]{0,30}", name) and isinstance(x, dict)): continue
        port, page, sec, ppl = x.get("port"), x.get("page"), x.get("secret"), x.get("people")
        routes = {k: [r for r in x.get(k) or () if isinstance(r, str) and DOOR_ROUTE.fullmatch(r)] if isinstance(x.get(k), list) else [] for k in ("get", "post")}
        if not (isinstance(port, int) and not isinstance(port, bool) and 1024 <= port <= 65535 and isinstance(page, str) and re.fullmatch(r"[A-Za-z0-9_./-]{1,200}\.html", page) and ".." not in page
                and isinstance(sec, str) and sec.startswith("~/") and ".." not in sec and (ppl is None or (isinstance(ppl, str) and ppl.startswith("~/") and ".." not in ppl))): continue
        out[name] = {"port": port, "page": page, "secret": HOME / sec[2:], "people": HOME / ppl[2:] if ppl else None, **routes}
    return out


def door_people(door):
    """The emails a door is for (lower case), or None when the door doesn't name any. An unreadable file is nobody."""
    if not door.get("people"): return None
    try: return {ln.strip().lower() for ln in door["people"].read_text().splitlines() if ln.strip()}
    except OSError: return set()


def door_for(name, route, email):
    """May this sign-in use (and be told about) this door? A local session always; an outside one only if the door's people file, when it has one, lists its email."""
    door = doors().get(name) if isinstance(name, str) else None
    if not door: return False
    ppl = door_people(door)
    return route == "local" or ppl is None or (bool(email) and email in ppl)


def door_call(name, path, body=None, timeout=45):
    """One call through a door: GET path, or POST it with body (a dict). Returns the service's JSON bytes. KeyError: no such door or route.
    The secret is read from a file only this user can read (mode 600), and sent only to this user's own listener on 127.0.0.1."""
    import http.client, urllib.request, urllib.error
    door = doors().get(name) if isinstance(name, str) else None
    if not door or not isinstance(path, str) or path not in door["get" if body is None else "post"]: raise KeyError(name)
    if body is not None and not isinstance(body, dict): raise ValueError("a door takes a JSON object")
    f = door["secret"]
    try: st = f.stat()
    except OSError: raise ValueError("the door's secret file is missing")
    if f.is_symlink() or st.st_uid != os.getuid() or st.st_mode & 0o077: raise ValueError("the door's secret file must be this user's own, mode 600")
    want = "0100007F:%04X" % door["port"]
    rows = [ln.split() for ln in Path("/proc/net/tcp").read_text().splitlines()[1:]]
    if not any(r[1] == want and r[3] == "0A" and int(r[7]) == os.getuid() for r in rows): raise ValueError("the service behind this door isn't running")
    hd = {"X-Hive-Door": f.read_text().strip()}
    data = None
    if body is not None: data = json.dumps(body).encode(); hd["Content-Type"] = "application/json"
    req = urllib.request.Request(f"http://127.0.0.1:{door['port']}{path}", data=data, headers=hd, method="GET" if body is None else "POST")
    class Stay(urllib.request.HTTPRedirectHandler):   # never follow a redirect: urllib would carry the secret header to wherever it points
        def redirect_request(self, *a, **k): return None
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({}), Stay).open(req, timeout=timeout) as r: out = r.read(4 * 2**20)
    except urllib.error.HTTPError as e: raise ValueError(f"the service answered {e.code}")
    except (http.client.HTTPException, OSError): raise ValueError("the service behind this door didn't answer")
    json.loads(out)   # only JSON goes back to the page
    return out


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


def proof_value(web, port, nonce):
    """What the desktop app asks for before it sends the key (GET /api/proof?n=...): shows that this server holds the secret without
    anyone sending it. Tied to the port, so an answer passed on from a Hive on another port doesn't fit, and to the word "proof",
    so it can never be the session cookie's value."""
    return hmac.new(str(web["secret"]).encode(), f"proof:{port}:{nonce}".encode(), hashlib.sha256).hexdigest()


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
    if host and host == tailnet_host(web): return "tailnet" if peer in ("127.0.0.1", "::1") else "refused"   # tailscale serve on :8446; admit checks who
    if host not in {f"127.0.0.1:{port}", f"localhost:{port}", f"[::1]:{port}"} | outside: return "refused"
    if host in outside or peer not in ("127.0.0.1", "::1") or any(h in headers for h in FORWARDED):
        # With outside.socket the tunnel comes only through that socket. Another account can bind the TCP port while the dash
        # restarts and catch a token the tunnel sends there, so a token on TCP proves nothing and is never looked at.
        return "refused" if (web.get("outside") or {}).get("socket") else "outside"
    return "local"


def tailnet_host(web):
    """web.json tailnet.host: the name and port tailscale serve answers on for the dash (as the word shell frames it). Lowercase, with the port."""
    t = web.get("tailnet") if isinstance(web.get("tailnet"), dict) else {}
    h = str(t.get("host") or "").strip().lower()
    return h if re.fullmatch(r"[a-z0-9-]+(\.[a-z0-9-]+)+:[0-9]{1,5}", h) else None


_QR = {}


def phone_qr(web):
    """E122: the address a phone can actually reach, for the QR code in Settings › This device. The outside address if one is
    set (outside.hosts, then the names in outside.sites), else the tailnet name tailscale serve answers on; None when neither is.
    {"url", "via": "outside"|"tailnet", "qr": rows of "0"/"1"}. It only reads web.json: it opens nothing (hard limit 5)."""
    out = web.get("outside") if isinstance(web.get("outside"), dict) else {}
    sites = out.get("sites") if isinstance(out.get("sites"), dict) else {}
    names = [str(h).strip().lower().rstrip(".") for h in (out.get("hosts") or []) if isinstance(h, str)] + [site_key(k) for k in sites]
    name = next((n for n in names if re.fullmatch(r"[a-z0-9-]+(\.[a-z0-9-]+)+(:[0-9]{1,5})?", n)), None)
    url, via = (f"https://{name}/", "outside") if name else (f"https://{tailnet_host(web)}/", "tailnet") if tailnet_host(web) else (None, None)
    if not url: return None
    if url not in _QR:
        spec = importlib.util.spec_from_file_location("h2_qr", Path(__file__).resolve().with_name("qr.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
        _QR.clear(); _QR[url] = m.matrix(url)
    return {"url": url, "via": via, "qr": _QR[url]}


TS_SOCK = "/run/tailscale/tailscaled.sock"
_whois = {}


def ts_whois(ip, now=None):
    """(stable node id, login) tailscaled gives for a tailnet address, or None. Asked over its local socket; kept a minute."""
    now = now or time.time(); hit = _whois.get(ip)
    if hit and now - hit[0] < 60: return hit[1]
    out = None
    try:
        import http.client
        class C(http.client.HTTPConnection):
            def connect(self): self.sock = socket.socket(socket.AF_UNIX); self.sock.settimeout(3); self.sock.connect(TS_SOCK)
        c = C("local-tailscaled.sock", timeout=3)
        c.request("GET", "/localapi/v0/whois?addr=" + urllib.parse.quote(f"[{ip}]:1" if ":" in ip else f"{ip}:1"), headers={"Sec-Tailscale": "localapi"})
        r = c.getresponse(); d = json.loads(r.read() or b"{}") if r.status == 200 else {}; c.close()
        node, login = (d.get("Node") or {}).get("StableID"), (d.get("UserProfile") or {}).get("LoginName")
        if isinstance(node, str) and node: out = (node, str(login or "").strip().lower())
    except (OSError, ValueError, http.client.HTTPException): out = None
    _whois[ip] = (now, out); return out


def tailnet_person(headers, uid, web, whois=None):
    """The login on the tailnet route, or None. Three things, none of them a header a local account can send:
    the connection's other end is root (tailscale serve; another account connecting to the port isn't), there is exactly one
    X-Forwarded-For address, which serve sets, and tailscaled says that address is a device in web.json tailnet.nodes (stable node
    ids: the person's own devices, never the machine, whose own traffic tailscaled also puts under the person's login)."""
    t = web.get("tailnet") if isinstance(web.get("tailnet"), dict) else {}
    nodes = {n for n in t.get("nodes") or [] if isinstance(n, str) and n} if isinstance(t.get("nodes"), list) else set()
    if uid != 0 or not nodes: return None
    xff = [v for k, v in (headers.items() if isinstance(headers, dict) else headers) if k.lower() == "x-forwarded-for"]   # every copy, so a doubled header is seen
    ip = xff[0].strip() if len(xff) == 1 and "," not in xff[0] else ""
    try: ipaddress.ip_address(ip)
    except ValueError: return None
    w = (whois or ts_whois)(ip)
    if not w or w[0] not in nodes: return None
    return w[1] or "tailnet device"


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
    its own team. Each is read hourly, or when a token names an unknown key, at most once a minute, or every 5 s while the last read
    failed: a dash restart starts with no keys, so one failed read would otherwise refuse every outside request for a minute. If they
    can't be read, tokens signed with an unknown key are refused. One team's keys never answer for another's URL."""
    def __init__(self): self.sets, self.locks, self.lock, self.failed = {}, {}, threading.Lock(), set()  # url -> (keys, read at); url -> its own lock; urls whose last read failed

    def get(self, url, kid):
        import urllib.request
        with self.lock: ulock = self.locks.setdefault(url, threading.Lock())  # held only to find the URL's lock, never during a fetch
        with ulock:  # one team's slow fetch holds up only requests for that team
            keys, t = self.sets.get(url, ({}, 0))
            age = time.time() - t
            if url not in self.sets or age > 3600 or (kid not in keys and age > (5 if url in self.failed else 60)):
                for last in (False, True):
                    try:
                        with urllib.request.urlopen(url, timeout=5) as r: got = json.load(r)["keys"]
                        keys = {k["kid"]: (int.from_bytes(b64u(k["n"]), "big"), int.from_bytes(b64u(k["e"]), "big"))
                                for k in got if k.get("kty") == "RSA" and k.get("kid")}
                        self.failed.discard(url); break
                    except Exception as e:
                        print(f"can't read the Access keys: {type(e).__name__}", file=sys.stderr, flush=True)
                        self.failed.add(url)
                        if not last: time.sleep(1)
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
MAX_FILE = 100 * 2**20  # one request: Cloudflare in front of the dash refuses a single body over 100 MB
MAX_CHUNK = 64 * 2**20  # one piece of a big file (under the cap, with room for headers)
MAX_BIG = 4 * 2**30     # a whole file sent in pieces
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
        self.threads, self.names = set(), None
        self.pfiles = {}   # the person's posts with files: id -> (thread, first version's id, [paths]), so an edit keeps them (everyday E12d)

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
            self.off, lines, _ = tail_lines(BOARD, self.off)
            for ln in lines:
                try: p = json.loads(ln)
                except ValueError: continue
                if not isinstance(p, dict): continue
                who, th = str(p.get("from", "")), p.get("thread") or p.get("id")
                self.authors[p.get("id")] = who; self.threads.add(th)
                if AGENT_CODE.fullmatch(who.lower()): self.last[th] = who.lower()
                if who == PERSON:
                    self.answered[th] = p.get("at", "")
                    if p.get("files"): self.pfiles[p.get("id")] = (th, str(p.get("edit_of") or p.get("id")), [str(f.get("path", "")) for f in p["files"] if isinstance(f, dict)])
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
    if not said: return False
    if said & known_agents(): return True
    return bool(said & focus_and_thread_names())


def focus_and_thread_names():
    """Focus ids and thread names: hive post treats them as names too (a focus reaches its keeper, a thread gets a copy)."""
    out = set()
    try: out |= {str(f.get("id", "")).lower() for f in json.loads(foci_c.get()).get("foci") or []}
    except Exception: pass
    try: INDEX.refresh(); out |= {str(t).lower() for t in INDEX.threads}   # any thread with a post, as hive post counts them
    except Exception: pass
    return out - {""}


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
PAIR_ACCEPTED = HOME / ".local/state/conn/pair-accepted.jsonl"  # what this dashboard took as a pairing: connectors refuse a post marked web-pair that isn't here
PAIR_WORD = re.compile(r"(?:conn )?pair [a-z0-9-]+ [A-Za-z0-9]{4,16}", re.I)  # approves a connector's pairing: bare too, and marked as typed here, the only place one counts


# Simple mode's word to the agents, on each post sent from it. Inside the head's parentheses, so the web app hides it (SOURCE in web/js/core/model.js):
# no ")" and no newline, and no "@", which would call someone. The core and the agents' prompts don't change.
VIDEO_TYPES = {".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".m4v": "video/mp4"}
AUDIO_TYPES = {".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".flac": "audio/flac", ".weba": "audio/webm"}
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

_PHONE = [0.0, False]
def phone_ready():
    """True when the phone module's call service answers on its socket with its models loaded. Checked at most every 20 s."""
    if time.time() - _PHONE[0] > 20:
        try: st, b = phone_http("GET", "/health", timeout=0.4); ok = st == 200 and json.loads(b).get("models") is True
        except (OSError, ValueError): ok = False
        _PHONE[:] = [time.time(), ok]
    return _PHONE[1]


PHONE_SOCK = STATE / "phone.sock"
def phone_http(method, path, body=None, ctype="application/json", timeout=60):
    """One request to the phone module's call service. It listens on a Unix socket in this account's state dir, not on
    loopback, so no other account on the machine can reach it. Returns (status, body)."""
    import http.client, socket
    c = http.client.HTTPConnection("localhost", timeout=timeout)
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.settimeout(timeout)
    try:
        s.connect(str(PHONE_SOCK)); c.sock = s
        c.request(method, path, body=body, headers={"Content-Type": ctype})
        r = c.getresponse(); return r.status, r.read()
    finally: c.close()


def voice_ready():
    """True when the voice module's service has proved itself working: its self-test passed and its heartbeat is under 2 minutes old."""
    st = jload(HOME / ".hive" / "voice-status.json", {})
    return isinstance(st, dict) and st.get("ok") is True and isinstance(st.get("at"), (int, float)) and time.time() - st["at"] < 120


# Where voice calls stand on this install, for Settings > This device (projects/everyday/VOICE.md V1). core/dist/install.sh builds them in the
# background on a first Linux install and leaves its traces in ~/.hive: voice-skipped (why it didn't), voice-setup.pid while the build runs,
# voice-setup.rc (its exit code) when it ends, voice-setup.log. Try again (behind the modules cap) runs the same build. It runs outside this
# app's service (systemd-run), whose memory cap is far below what the build needs. HIVE_VOICE_CMD replaces the build in tests: no download, no unit.
VOICE_DIR = HOME / ".hive"
VOICE_BUILD = 'phone setup; p=$?; voice setup; v=$?; [ $p = 0 ] && exit $v; exit $p'
VOICE_WRAP = 'exec >"$HOME/.hive/voice-setup.log" 2>&1 </dev/null\necho $$ >"$HOME/.hive/voice-setup.pid"\nsh -c {cmd}\necho $? >"$HOME/.hive/voice-setup.rc"\n'
VOICE_NO_RETRY = ("own-account", "wsl", "not-linux", "no-service")  # their own install's choice, or a place the build can't run: said, not offered

def _voice_alive(pid):
    """The build's shell is still running (not a zombie)."""
    try: return pid > 0 and Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0] != "Z"
    except (OSError, IndexError): return False

def voice_view():
    """One of: ready, building, failed, starting, stopped, unset, skipped. With why (a reason code), log (the log's last lines) and since (epoch)."""
    def read(name):
        try: return (VOICE_DIR / name).read_text().strip()
        except OSError: return None
    log, pidf, rc, skip = VOICE_DIR / "voice-setup.log", read("voice-setup.pid"), read("voice-setup.rc"), read("voice-skipped")
    def tail():
        try: lines = [l.rstrip() for l in log.read_text(errors="replace").splitlines() if l.strip()]
        except OSError: return ""
        return "\n".join(lines[-6:])[-700:]
    def mtime(f):
        try: return f.stat().st_mtime
        except OSError: return None
    has = (HOME / "modules" / "phone").is_dir()
    base = {"can_retry": True}
    if phone_ready(): return {**base, "state": "ready", "memos": voice_ready(), "ffmpeg": bool(shutil.which("ffmpeg"))}
    building = pidf is not None and rc is None and _voice_alive(int(pidf) if pidf.isdigit() else 0)
    if building: return {**base, "state": "building", "since": mtime(VOICE_DIR / "voice-setup.pid"), "log": tail(), "can_retry": False}
    if rc is not None:
        if rc != "0": return {**base, "state": "failed", "rc": rc, "since": mtime(VOICE_DIR / "voice-setup.rc"), "log": tail()}
        t = mtime(VOICE_DIR / "voice-setup.rc") or 0
        return {**base, "state": "starting" if time.time() - t < 180 else "stopped", "since": t, "log": tail()}
    if pidf is not None: return {**base, "state": "failed", "rc": None, "since": mtime(VOICE_DIR / "voice-setup.pid"), "log": tail()}  # the build stopped without an exit code: killed, or the machine restarted
    if log.exists():  # a build from an install that kept no pid or exit code
        t = mtime(log) or 0
        return {**base, "state": "building" if time.time() - t < 900 else "failed", "rc": None, "since": t, "log": tail(), "can_retry": time.time() - t >= 900}
    if skip and not has:
        why = skip.split()[0]; out = {**base, "state": "skipped", "why": why, "can_retry": why not in VOICE_NO_RETRY}
        if why == "small":
            n = (skip.split() + ["?"] * 4)[1:4]; out.update(need_gb=n[0], free_gb=n[1], mem_gb=n[2])
        return out
    if has: return {**base, "state": "unset"}
    if (HOME / ".hive-voice-added").exists(): why = "removed"
    elif (HOME / ".hive-account").exists(): why = "own-account"
    elif "microsoft" in (Path("/proc/version").read_text().lower() if Path("/proc/version").exists() else ""): why = "wsl"
    else: why = "older"
    return {**base, "state": "skipped", "why": why, "can_retry": why not in VOICE_NO_RETRY}

def voice_retry():
    """Try again / Set up voice: add the two modules if they are missing and run the build in the background. Never waits on it."""
    v = voice_view()
    if v["state"] == "ready": raise ValueError("voice calls already work")
    if not v.get("can_retry"): raise ValueError("voice is still being set up" if v["state"] == "building" else "voice can't be set up on this install from here")
    for m in ("phone", "voice"):
        if not (HOME / "modules" / m).is_dir(): module_do("add", m)
    VOICE_DIR.mkdir(parents=True, exist_ok=True)
    for n in ("voice-setup.rc", "voice-setup.pid", "voice-skipped"): (VOICE_DIR / n).unlink(missing_ok=True)
    (HOME / ".hive-voice-added").touch()
    _PHONE[0] = 0.0
    test = os.environ.get("HIVE_VOICE_CMD")
    import shlex
    script = VOICE_DIR / "voice-setup.sh"  # a file, so systemd doesn't expand the $s on its command line
    script.write_text(VOICE_WRAP.format(cmd=shlex.quote(test or VOICE_BUILD)))
    path = f"{HOME / '.local' / 'bin'}:{os.environ.get('PATH', '/usr/bin:/bin')}"
    if not test and shutil.which("systemd-run"):
        r = subprocess.run(["systemd-run", "--user", "--collect", "--quiet", f"--unit=hive-voice-setup-{int(time.time())}", f"--setenv=PATH={path}", f"--setenv=HOME={HOME}",
                            "sh", str(script)], capture_output=True, text=True, env=child_env(), timeout=30)
        if r.returncode == 0: return {"ok": True, "via": "systemd-run"}
    subprocess.Popen(["sh", str(script)], env={**child_env(), "PATH": path}, cwd=str(HOME), start_new_session=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return {"ok": True, "via": "test" if test else "child"}


# Must match EDIT_NOTE in web/js/core/model.js, byte for byte: the page signs it.
EDIT_NOTE = "(edited message {v}: this version replaces it. Answer it instead, as if that message and your answer to it were never sent; anything already done for it stays done, so don't redo it.)\n"

def person_post(text, thread=None, reply_to=None, uploads=(), signer=None, limited=False, simple=False, model=None, sig=None, interrupt=False, effort=None, notice=False, edit_of=None, retry_of=None, files_from=None, focus=None):
    """A post as the person. As the Slack mirror does it: a plain message wakes the agent on the thread (thread_agent), a reply wakes the author
    of the post replied to, and no thread (or a thread with no agent yet) starts a new agent. Mentions in the text win.
    signer: who signed in, from a verified Access token, when more than one person can: it heads the text, so an agent and the board can see who wrote it.
    sig: what the browser signed over the text as typed (core/signed.py checks it, in hive post). A /btw is rewritten, so it goes unsigned, as does a post of only attachments.
    notice: the text is the server's own words for a click (the effort restart). It and a /btw go unsigned, so they carry via "web-notice": the board
    and the safety count (projects/rsi-lab/safety.py unsigned_person) can tell them from a typed post that lost its signature, and the broker never takes one as an approval.
    interrupt: the conversation's Interrupt button: hive post --interrupt, so a working agent gets it now (core/starter.py interrupt_step). A post of only
    attachments keeps no text of its own: the files show as files.
    edit_of: the person edited their message with this id (its first version) in this thread; retry_of: they asked for another answer in place
    of the agent's reply with this id. Both go on the board as fields (hive post --edit-of/--retry-of) and the chat draws them as versions; the
    agent reads a one-line note on what to do (everyday E12). A retry has no typed text, so it goes unsigned, like a notice.
    files_from: with edit_of, the version being edited (its id): its files go with the new version, so editing the words keeps them (E12d).
    focus: a new chat started under this group (the composer's picker): the first post carries group:ID, so the starter gives the agent the
    group's instructions even before the thread's tag lands (core/starter.py group_of), and the thread is filed there right after (E37).
    Returns {"id", "thread", "said"}, or raises ValueError with a sentence for the page."""
    text = str(text or "").strip()
    focus = str(focus or "").lower() or None
    if focus and (thread or not FOCUS_ID.fullmatch(focus)): focus = None
    for k, v in (("edit", edit_of), ("retry", retry_of)):
        if v is not None and (not thread or not re.fullmatch(r"[0-9a-f]{6,20}", str(v))): raise ValueError(f"the message to {k} isn't one in this conversation")
    if retry_of: text, notice, interrupt, reply_to, uploads = "", True, False, None, ()
    sig = {**sig, "n": len(text)} if isinstance(sig, dict) and text and not BTW.fullmatch(text) else None
    if edit_of:   # the page puts the note in front itself and signs it with the words, so the edit's signature holds (core/signed.py lets nothing else in front)
        reply_to, note = None, EDIT_NOTE.format(v=edit_of)
        if not text.startswith(note): text = note + text
        if not text[len(note):].strip(): raise ValueError("nothing to send")
    refs = [UPLOAD_REF.fullmatch(str(u)) for u in uploads or []]
    if not all(refs): raise ValueError("an attachment wasn't recognised")
    paths = [INCOMING / m[1] / m[2] for m in refs]
    if not all(p.is_file() and not p.is_symlink() for p in paths): raise ValueError("an attachment is gone; attach it again")
    kept = []
    if edit_of and files_from:
        INDEX.refresh(); kf = INDEX.pfiles.get(str(files_from))
        if not kf or kf[0] != str(thread) or kf[1] != str(edit_of): raise ValueError("the message's files aren't in this conversation")
        kept = [Path(f) for f in kf[2]]
        if not all(f.parent == HOME_FILES / str(files_from) and f.is_file() and not f.is_symlink() for f in kept): raise ValueError("a file of that message is gone")
    if thread and not THREAD_NAME.fullmatch(str(thread)): raise ValueError("a thread name is letters, digits and . _ : -")
    if not text and not paths and not retry_of: raise ValueError("nothing to send")
    if model and model not in PICK_MODELS: raise ValueError("that model isn't one of " + ", ".join(PICK_MODELS))
    if effort and effort not in EFFORTS: raise ValueError("that effort isn't one of " + ", ".join(EFFORTS))
    btw = BTW.fullmatch(text)
    if btw:
        if not thread: raise ValueError("/btw asks aside about a conversation, so send it from one")
        if not (btw[1] or "").strip(): raise ValueError("write the side question after /btw")
        parent = BTW_THREAD.fullmatch(str(thread)); parent = parent[1] if parent else str(thread)   # asked from a side thread: still about the main one
        thread, text, reply_to = f"btw-{parent[:90]}-{secrets.token_hex(2)}", btw[1].strip(), None
    # Approving a step goes to the broker, which takes only a whole line in this form from a plain person post. So it stays bare, with nothing added;
    # a limited sign-in's posts are marked and never count, whatever lines they hold.
    pairing = not limited and not paths and bool(PAIR_WORD.fullmatch(text.strip()))
    word = pairing or (not limited and not paths and bool(STEP_WORD.fullmatch(text.strip())))
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
    if edit_of or retry_of:
        INDEX.refresh(); v = str(edit_of or retry_of); author = INDEX.authors.get(v, "").lower()
        if edit_of and author != PERSON: raise ValueError("only your own messages can be edited")
        if retry_of and not AGENT_CODE.fullmatch(author): raise ValueError("only an agent's reply can be tried again")
        if retry_of: who = author
        if retry_of: text = (f"(retry of reply {v}: the person asked for another answer in its place. Write that answer again from scratch as a new attempt. The chat shows it as a newer version of that reply, so don't mention the earlier one or this note.)")
    routed = False
    if not word and not calls_someone(text):
        who = who or (thread_agent(thread) if thread else None)
        text = f"@{who or 'new'} {text}"
        routed = bool(who)  # we picked the name, so a dead agent's heir may take it; only a typed name brings a dead agent back
    if pairing:  # before the post, so a connector reading the board always finds it
        PAIR_ACCEPTED.parent.mkdir(parents=True, exist_ok=True)
        with PAIR_ACCEPTED.open("a") as f: f.write(json.dumps({"at": time.time(), "text": text}) + "\n")
    cmd = [str(HIVE_BIN), "post", "--as", PERSON] + (["--thread", str(thread)] if thread else []) + (["--to", who] if who else []) + (["--routed"] if routed else []) + (["--reply-to", reply_to] if reply_to else []) + (["--model", PICK_MODELS[model]] if model else []) + (["--effort", effort] if effort and not word else []) + (["--interrupt"] if interrupt and not limited and not word else []) + (["--edit-of", str(edit_of)] if edit_of else []) + (["--retry-of", str(retry_of)] if retry_of else []) + (["--tag", f"group:{focus}"] if focus else [])
    for p in paths + kept: cmd += ["--file", str(p)]
    r = subprocess.run(cmd, input=text, text=True, capture_output=True, env={**child_env(), **({"HIVE_POST_VIA": "web-notice"} if (notice or btw) and not limited else {}), **({"HIVE_POST_VIA": "limited"} if limited else {}), **({"HIVE_POST_VIA": "web-pair"} if pairing else {}), **({"HIVE_POST_SIG": json.dumps(sig)} if sig else {})}, cwd=str(HOME), timeout=60)
    if r.returncode: raise ValueError(short(r.stderr, 300) or "the post failed")
    for p in paths: shutil.rmtree(p.parent, ignore_errors=True)
    pid = r.stdout.strip()
    if focus and not btw:
        try: focus_do({"thread": pid, "focus": focus})
        except (ValueError, OSError, subprocess.SubprocessError): pass   # it stays in Misc, where the title menu can file it
    return {"id": pid, "thread": thread or pid, "said": short(r.stderr.replace("hive post: ", "", 1), 300)}


PAGE_NOTE = "(web app, page edited by hand: nothing to answer. If asked to change this page again, start from this version.) Edited this page by hand."
PAGE_TITLE = re.compile(r'[^"\n]{1,120}')

def page_edit_post(page, thread, limited=False):
    """POST /api/post with page {code, title}: the person edited a page (an html block) by hand (everyday E43). The server builds the post itself: a note,
    then the code in a backtick fence longer than any backtick run in it, so every @ in the code is inside code for hive, the starter and the inbox (their CODE
    regex). It names nobody and goes unsigned via "web-notice" with the tag page:edit, so no agent wakes, no ask is filed (core/asks.py) and no wait ends
    (core/continuation.py person_waiter); the chat draws it as the page's newest version (chatPages), with the old one behind the arrows."""
    if not isinstance(page, dict): raise ValueError("the page wasn't sent")
    code, title = str(page.get("code") or ""), page.get("title")
    if not code.strip(): raise ValueError("the page is empty")
    if len(code) > 2**19: raise ValueError("the page is too big to post")
    if not thread or not THREAD_NAME.fullmatch(str(thread)): raise ValueError("a page is edited in its conversation")
    title = str(title).strip() if title else ""
    if title and not PAGE_TITLE.fullmatch(title): title = ""
    run = max([len(m) for m in re.findall(r"`+", code)] + [2])
    fence = "`" * max(3, run + 1)
    info = "html" + (f' title="{title}"' if title else "")
    text = f"{PAGE_NOTE}\n\n{fence}{info}\n{code.rstrip()}\n{fence}"
    # The fence is longer than any backtick run in the code, and the chat's markdown (ui/markdown.js FENCE) honours that; but hive, the starter and the inbox
    # mask code with a lazy ```.*?``` (same as MENTION_CODE), so ``` inside the code can expose an @name to them: calls_someone sees what they would.
    if calls_someone(text): raise ValueError("the page names an agent with the at sign where the Hive would read it as a call (often after a ``` inside the code); change that @name and save again")
    cmd = [str(HIVE_BIN), "post", "--as", PERSON, "--thread", str(thread), "--tag", "page:edit"]
    r = subprocess.run(cmd, input=text, text=True, capture_output=True, env={**child_env(), "HIVE_POST_VIA": "limited" if limited else "web-notice"}, cwd=str(HOME), timeout=60)
    if r.returncode: raise ValueError(short(r.stderr, 300) or "the post failed")
    return {"id": r.stdout.strip(), "thread": str(thread), "said": short(r.stderr.replace("hive post: ", "", 1), 300)}

# @question in the Chat side search: a normal agent in the hidden thread `side`, answering in one line and bringing threads up with `hive show`.
# Posted as dash, not person: it's not an ask to register or a post the agent owes the person a chat reply on (asks.py, inbox.waker). The same agent
# wakes for each question, keeping what it read, until its context is full; then --routed hands over to its heir. No "@" in the note (see SIMPLE_NOTE).
SIDE_THREAD, SIDE_MODEL = "side", "haiku"
SIDE_NOTE = ("(question typed in the dash's side search, {who}) The answer shows there, not in a chat. Look it up, a few searches at most, with `hive search WORDS`, `hive read --thread T`, `kb find WORDS`. "
             "For each thread that helps, at most 5, run `hive show THREAD --why \"why it fits, under 10 words\"`. Last, post once with `hive post --thread side`: the text is [{tag}] and then one plain line under 25 words that answers it, "
             "saying so if you found nothing. Change nothing, start no work, name nobody with the at sign, set no status bar, then stop.\nQuestion: {q}")

def side_ask(q, signer=None, limited=False):
    """POST /api/side-ask: wakes the sidebar agent. Returns the tag its answer starts with, which the page waits for."""
    q = " ".join(str(q or "").replace("@", " ").split())[:500]
    if not q: raise ValueError("write the question")
    who, tag = thread_agent(SIDE_THREAD), secrets.token_hex(2)
    text = SIDE_NOTE.format(who=f"from {signer}" if signer else "web app", tag=tag, q=q)
    cmd = [str(HIVE_BIN), "post", "--as", "dash", "--thread", SIDE_THREAD, "--to", who or "new", "--model", SIDE_MODEL] + (["--routed"] if who else [])
    r = subprocess.run(cmd, input=text, text=True, capture_output=True, env={**child_env(), **({"HIVE_POST_VIA": "limited"} if limited else {})}, cwd=str(HOME), timeout=60)
    if r.returncode: raise ValueError(short(r.stderr, 300) or "the post failed")
    return {"id": r.stdout.strip(), "tag": tag}


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


def save_chunk(name, tok, off, total, n, src):
    """One piece of a big file, written at its offset in ~/files/incoming/<tok>/<name>. tok "" starts a file. A repeated piece (a retry) rewrites
    from its offset. Returns {"id"} until the last piece lands, then what /api/post takes."""
    if not 0 < total <= MAX_BIG: raise ValueError("a file can be up to 4 GB")
    if not 0 <= off < total or off + n > total: raise ValueError("that piece is outside the file")
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "_", Path(str(name)).name).lstrip(".-") or "file"
    if not tok:
        INCOMING.mkdir(parents=True, exist_ok=True)
        if off: raise ValueError("start at the beginning")
        tok = secrets.token_hex(8)
        (INCOMING / tok).mkdir(mode=0o700)
    elif not re.fullmatch(r"[0-9a-f]{16}", tok) or not (INCOMING / tok).is_dir(): raise ValueError("that upload isn't open any more")
    path = INCOMING / tok / safe
    have = path.stat().st_size if path.exists() else 0
    if off > have: raise ValueError("a piece is missing")
    with open(path, "r+b" if path.exists() else "wb") as f:
        f.seek(off); f.truncate(off); left = n
        while left > 0:
            b = src.read(min(left, 2**20))
            if not b: break
            f.write(b); left -= len(b)
    if left: raise ValueError("the upload stopped early")
    if off + n < total: return {"id": tok}
    return {"ref": f"{tok}/{safe}", "name": safe, "size": total}


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


MAIL_DIR = Path(os.environ.get("HIVE_MAIL_ROOT", "/var/lib/hive-mail")) / "hive"
MAIL_ID = re.compile(r"[0-9A-Za-z._-]{1,80}")


def _ctl(s): return "".join(c for c in str(s) if c in "\n\t" or (c >= " " and c != "\x7f" and not 0x200e <= ord(c) <= 0x200f and not 0x202a <= ord(c) <= 0x202e and not 0x2066 <= ord(c) <= 0x2069))


def mail_view(limit=200):
    """The owner's mail copy (hive-mail inbox, read-only). Plain text only; control and bidi characters stripped; attachments by name and size.
    Held mail (marks stay in the store) shows as a stub. Gated by the `files` capability in GET_CAP; never goes on the board."""
    out, err, unset = [], None, False
    try: names = sorted((f for f in (MAIL_DIR / "inbox").iterdir() if f.suffix == ".json" and MAIL_ID.fullmatch(f.stem)), key=lambda f: f.name, reverse=True)[:limit]
    except FileNotFoundError: names, unset = [], True   # no mail set up on this machine is a state, not a failure
    except OSError: names, err = [], "The mail folder can't be read from here."
    for f in names:
        try:
            if f.is_symlink() or f.stat().st_size > 3_000_000: continue
            d = json.loads(f.read_text("utf-8", "replace"))
        except (OSError, ValueError): continue
        if not isinstance(d, dict): continue
        if d.get("held"): out.append({"id": f.stem, "held": True}); continue
        att = [{"name": _ctl(a.get("name", ""))[:200], "size": a.get("size") if isinstance(a.get("size"), int) else None} for a in (d.get("attachments") or [])[:20] if isinstance(a, dict)]
        out.append({"id": f.stem, "subject": _ctl(d.get("subject", ""))[:300], "date": _ctl(d.get("date", ""))[:80], "received_at": _ctl(d.get("received_at", ""))[:40],
                    "text": _ctl(d.get("text", ""))[:100_000], "truncated": bool(d.get("truncated")), "attachments": att})
    status = jload(MAIL_DIR / "status.json", {}) or {}
    return {"items": out, "error": err, "unset": unset, "checked": status.get("at") if isinstance(status, dict) else None}


def secrets_view():
    """Names and dates only: no route ever reads a value back."""
    try: files = [f for f in SECRETS.iterdir() if f.is_file() and not f.is_symlink() and SECRET_NAME.fullmatch(f.name)]
    except OSError: return []
    return sorted(({"name": f.name, "at": f.stat().st_mtime} for f in files), key=lambda r: r["name"])


# Cloudflare credentials belong to a root-run broker that holds the only key: a copy in the secrets folder is readable (and writable) by
# every agent, so the broker's owner can't trust it. Cloudflare's scannable formats are a prefix, 40 characters and a
# checksum (cfk_ API key, cfut_ user token, cfat_ account token, cfast_ Access service-token secret); older unprefixed tokens are caught
# by a name that says Cloudflare or CF.
CF_SECRET = re.compile(r"(?<![A-Za-z0-9])cf(?:k|ut|at|ast)_[A-Za-z0-9]{40}")
CF_NAME = re.compile(r"(?:^|[^a-z])(?:cf|cloudflare)(?:[^a-z]|$)|cloudflare", re.I)
ROOT_HELD_SAY = ("Cloudflare keys don't go here: every agent could read them. On the machine, in a terminal, run "
                 "`sudo h2cf-broker-admin set-token` and paste the token there only.")


def root_held_secret(name, value):
    return bool(CF_NAME.search(str(name or "")) or CF_SECRET.search(str(value or "")))


def set_secret(name, value, signer, delete=False):
    name = str(name or "")
    if not SECRET_NAME.fullmatch(name): raise ValueError("a secret's name is letters, digits and . _ -, starting with a letter or digit")
    f, who = SECRETS / name, signer or "this machine"
    if delete:
        if not f.is_file(): raise ValueError("no secret has that name")
        f.unlink(); _CONN_CACHE.clear()   # Settings > Connectors reads which secrets exist: a change from the Secrets section shows there at once
        dash_note(f"Secret `{name}` removed by {who}, in the web app.", "secrets")
        return {"ok": True}
    if not isinstance(value, str) or not value.strip(): raise ValueError("the value is empty")
    if len(value.encode()) > MAX_SECRET: raise ValueError("a secret can be up to 64 KB")
    if root_held_secret(name, value): raise ValueError(ROOT_HELD_SAY)
    SECRETS.mkdir(parents=True, exist_ok=True)
    SECRETS.chmod(0o700)
    had = f.exists()
    write_text_private(f, value.strip() + "\n"); _CONN_CACHE.clear()
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
KB_WALK_SKIP = {"archive/transcripts"}  # raw session logs, tens of thousands of folders of ids: still open from the shelf and found by a text search, but not walked for names
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
        raw = head.decode(errors="replace")
        res.update(text=redact(raw), truncated=st.st_size > KB_TEXT_MAX, version=str(st.st_mtime_ns))
        if st.st_size <= KB_TEXT_MAX and kb_editable(rel, p, head): res["editable"] = True
    return res


KB_EDIT_ROOTS, KB_EDIT_EXT = ("kb", "projects", "investigations", "modules"), (".md", ".markdown", ".txt")


def kb_editable(rel, p, data):
    """Whether the preview may write this file back: a whole UTF-8 Markdown or text file in notes, projects, investigations or a
    module's docs, reached by no link on the way, with nothing redact() would hide (the page only ever has the redacted text, so
    saving it would lose the secret). data is the file's bytes."""
    try:
        real = p.resolve().relative_to(HOME.resolve()).parts
        raw = data.decode("utf-8")   # strict: a file in another encoding would be rewritten wrong
        q = HOME   # kb_path has already followed links, so look at the name asked for, part by part
        for part in str(rel or "").strip("/").split("/"):
            q = q / part
            if q.is_symlink(): return False
    except (OSError, ValueError, RuntimeError): return False
    return (real[0] in KB_EDIT_ROOTS and p.suffix.lower() in KB_EDIT_EXT and len(data) <= KB_TEXT_MAX
            and os.access(p, os.W_OK) and redact(raw) == raw)


KB_SAVE_NOTED = {}


def kb_save(rel, text, version, signer):
    """Write a file the preview's Edit tab changed, refusing if it changed on disk since the page read it: agents edit these too."""
    rel = str(rel or "").strip("/"); p = kb_path(rel)
    if not p or not p.is_file(): raise KeyError(rel)
    if not isinstance(text, str): raise ValueError("there's no text to save")
    if len(text.encode()) > KB_TEXT_MAX: raise ValueError("a file can be up to 512 KB here")
    if p.stat().st_size > KB_TEXT_MAX: raise ValueError("this file is too big to edit here")
    with p.open("rb") as src: data = src.read(KB_TEXT_MAX + 1)
    if not kb_editable(rel, p, data): raise ValueError("this file can't be edited here (a link, code, not UTF-8, too big, or it holds something hidden)")
    if str(p.stat().st_mtime_ns) != str(version): raise ValueError("this file changed since you opened it. Your text is kept: copy it, reload, and try again")
    if redact(text) != text: raise ValueError("that text looks like it holds a key or password; it isn't saved through the web app")
    fd, t = tempfile.mkstemp(dir=p.parent, prefix="." + p.name + ".")   # never a name an agent could have put a link at
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as o: os.fchmod(fd, p.stat().st_mode & 0o777); o.write(text)
        os.replace(t, p)
    except BaseException:
        try: os.unlink(t)
        except OSError: pass
        raise
    st = p.stat()
    now = time.time()   # one board note per file per 10 minutes: Ctrl+S every few seconds isn't news each time
    if now - KB_SAVE_NOTED.get(rel, 0) > 600:
        KB_SAVE_NOTED[rel] = now
        try: dash_note(f"`~/{rel}` edited by {signer or 'this machine'} in the web app's file preview.", "kb-edits")
        except Exception: pass   # the file is saved; a note that didn't go out mustn't say otherwise
    return {"ok": True, "version": str(st.st_mtime_ns), "t": int(st.st_mtime), "size": st.st_size}


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
                    if n < depth and f"{r}/{e.name}" not in KB_WALK_SKIP and not kb_is_worktree(Path(e.path)): todo.append((Path(e.path), f"{r}/{e.name}", n + 1))
                elif e.is_file(follow_symlinks=False): out.append((f"{r}/{e.name}", e.stat(follow_symlinks=False).st_mtime))
            except OSError: continue
    return out[:room]


def kb_index_read():
    """Walk the shelves into kb_index_memo. The caller holds kb_index_lock."""
    items = []
    for rel, depth in (("kb", 12), ("core", 12), ("modules", 12), ("files", 2), ("investigations", 8), ("archive", 6), ("projects", 3), ("tools", 2)):
        items += kb_walk(rel, depth, KB_INDEX_MAX - len(items))
    kb_index_memo.update(t=time.time(), items=items, more=len(items) >= KB_INDEX_MAX)


def kb_index_again():
    try: kb_index_read()
    finally: kb_index_lock.release()


def kb_index_warm():
    """Read it once at start so the first Knowledge visit finds it, stamped old so a search still looks again."""
    with kb_index_lock:
        kb_index_read(); kb_index_memo["t"] = 1


def kb_index(wait=False):
    """Every file's path and time, for name search and "changed lately". Bounded. Past a minute old it is handed back as it is while a thread reads the new one, because on a big home the walk takes seconds and a page shouldn't wait on it. A search passes wait, so what it looks through is at most a minute old."""
    if time.time() - kb_index_memo["t"] < 60: return kb_index_memo
    if wait or not kb_index_memo["t"]:
        with kb_index_lock:
            if time.time() - kb_index_memo["t"] >= 60: kb_index_read()
    elif kb_index_lock.acquire(blocking=False): threading.Thread(target=kb_index_again, daemon=True).start()
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


def thread_find():
    """thread_find.py beside this file, loaded once."""
    tf = sys.modules.get("h2_thread_find")
    if not tf:
        spec = importlib.util.spec_from_file_location("h2_thread_find", Path(__file__).with_name("thread_find.py")); tf = importlib.util.module_from_spec(spec); spec.loader.exec_module(tf); sys.modules["h2_thread_find"] = tf
    return tf


def kb_tagged(q, n=40):
    """A tag: search (tag:site, tag:agent:a1b2c): the posts carrying every tag, newest first, as ranked passages that open their thread.
    Exact, like hive search tag:; None when q has no tag: word."""
    try: posts = thread_find().tagged(q)
    except Exception: return None
    if posts is None: return None
    out = [{"path": "board:" + str(p.get("id", "")), "line": 0, "text": KB_BLOB.sub("[hidden]", redact(" ".join(str(p.get("text", "")).split())[:240])), "title": "",
            "source": "board", "date": str(p.get("at", ""))[:10], "thread": p["thread"], "post": p.get("id")} for p in posts[:n]]
    return {"passages": out, "words": [w for w in q.lower().split() if not w.startswith("tag:")], "total": len(posts), "spelling": None}


def kb_search(q):
    """Names first, then lines inside files. A plain string, any case; a hard time limit, and never in worktrees or toolchains.
    A tag: word makes it a tag search (kb_tagged): only posts, no names or lines."""
    q = str(q or "").strip()[:200]
    if len(q) < 2: return {"q": q, "names": [], "hits": [], "more": False}
    tagged = kb_tagged(q)
    if tagged is not None: return {"q": q, "names": [], "hits": [], "more": False, "ranked": tagged}
    ranked = kb_ranked(q)
    ws = q.lower().split(); names = []
    for path, t in kb_index(wait=True)["items"]:
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


_LADDER = {"at": 0.0, "rows": {}}
USAGE = None  # the one Usage, set by serve(): foci_bytes lays its per-session costs on the foci

def foci_costs(act, usage):
    """Add each focus's AI cost over the last day and week to focus.py's activity, on the thread -> focus map it gives (a run's cost goes to
    the focus of the thread it was started in; a thread in no focus is Misc; a session no start event names is "unknown", in the total
    only). So the card's gauge compares like with like: every figure is a share of the hive's total over the same window."""
    if not isinstance(act, dict) or usage is None: return act
    tag = act.get("threads") or {}
    act["unknown"] = {}
    for w, span in (act.get("windows") or {"day": 86400, "week": 7 * 86400}).items():
        for b in list(act.get("foci", {}).values()) + [act.get("misc"), act.get("total")]:
            if isinstance(b, dict) and isinstance(b.get(w), dict): b[w]["cost"] = 0.0
        act["unknown"][w] = {"cost": 0.0}
        for th, c in usage.by_thread(time.time() - span).items():
            fid = tag.get(th) if th else None
            if not th: act["unknown"][w]["cost"] += c
            elif fid and fid in act.get("foci", {}): act["foci"][fid][w]["cost"] += c
            elif isinstance(act.get("misc"), dict): act["misc"][w]["cost"] += c
            if isinstance(act.get("total"), dict): act["total"][w]["cost"] += c
    return act

def foci_look(v, key, look):
    """The held /api/foci answer `v` with the person's look `look` ({at, start, base, since}: focus.py seen --json) at focus `key`, or at the home (None)."""
    d = json.loads(v)
    if key is None: d["home_seen"] = look
    else:
        for f in d.get("foci") or []:
            if f.get("id") == key: f["seen"] = look
    return json.dumps(d).encode()

def foci_bytes():
    """core/focus.py json: the foci (labels, hand-written summaries, to-do, lint) the home's cards are built from (2026-11-07),
    with each one's share of the hive's runs, posts and cost over the last day and week (activity: focus.py's counts, the costs from Usage here)."""
    out = core_json("focus.py", "json", timeout=20)
    if isinstance(out, dict) and out.get("activity"): out["activity"] = foci_costs(out["activity"], USAGE)
    if not isinstance(out, dict) and foci_c.v: return foci_c.v  # a read that failed keeps the last good list: an empty one reads as "no such focus" on every focus page
    return json.dumps(out if isinstance(out, dict) else {"foci": [], "tags": {}, "error": "focus.py gave nothing"}).encode()
foci_c = CachedStale(foci_bytes, 20)  # stale, not Cached: at nice 15 focus.py took 2 to 9 s on a cold read, and every page that opened then waited on it (design round 2026-11-09)

def ladder_now(ttl=20):
    """Each focus's open rung and counts, live from core/goals.py (0.1 s), by row name. The 30-minute summary carries the same fields and lags
    while agents tick rungs, so the Work page's Next comes from here. {} when goals can't answer."""
    if time.time() - _LADDER["at"] > ttl:
        d = core_json("goals.py", "--json", timeout=20) or {}
        _LADDER.update(at=time.time(), rows={r["name"]: {k: r.get(k) for k in ("rung", "rungs_done", "rungs_left")} for r in d.get("rows", []) if isinstance(r, dict) and r.get("name")})
    return _LADDER["rows"]

    f = HOME / "needs-you.json"
    if not f.exists(): return b'{"absent": true}'  # needs_you.py writes it, atomically, every 30 min
    raw = f.read_bytes()
    try:
        d = json.loads(raw); live = ladder_now()
        for st in d.get("states") or []:
            if isinstance(st, dict) and st.get("key") in live: st.update(live[st["key"]])
        return json.dumps(d).encode() if live else raw
    except (ValueError, AttributeError): return raw

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


# ---------- phone alerts (phone.py): a contentless ntfy line when a post for the person goes unread ----------
def _phone():
    spec = importlib.util.spec_from_file_location("hive_phone", Path(__file__).resolve().with_name("phone.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
    return m

PHONE = _phone().PhoneAlert(STATE / "phone-alert.json")


def phone_link(headers):
    """The address the person is using right now, if a phone could open it: not loopback or a bare LAN name. Sent as the tap-through link."""
    host = (headers.get("host") or "").strip().lower()
    if not host or host.split(":")[0] in ("localhost", "127.0.0.1", "[::1]", "::1") or "." not in host.split(":")[0]: return ""
    proto = "http" if headers.get("x-forwarded-proto") == "http" else "https"
    return f"{proto}://{host}/"


def phone_do(what, headers):
    INDEX.refresh()
    latest = max((str(p.get("at") or "") for p in INDEX.mine), default="")
    if what == "on": return PHONE.set_on(True, link=phone_link(headers), latest=latest)
    if what == "off": return PHONE.set_on(False)
    if what == "topic": return PHONE.new_topic(latest)
    if what in ("runs-on", "runs-off"):
        ends = _phone().person_run_ends(jlines(LOG, 512 * 1024))
        return PHONE.set_runs(what == "runs-on", latest=max((str(e.get("at") or "") for e in ends), default=""))
    if what == "test": PHONE.test(); return {**PHONE.view(), "tested": True}
    raise ValueError("on, off, test, topic, runs-on or runs-off")


# ---------- Web Push (module conn-webpush): the browser list and its checks live there. An install without that module has no such switch ----------
def _webpush():
    f = Path(__file__).resolve().parent.parent / "conn-webpush" / "webpush_subs.py"
    if not f.exists(): return None
    spec = importlib.util.spec_from_file_location("hive_webpush_subs", f); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
    return m


def webpush_view():
    m = _webpush()
    return m.view(HOME) if m else {"available": False}


def webpush_do(d, headers):
    m = _webpush()
    if not m: raise ValueError("push isn't set up here")
    return m.act(HOME, d, phone_link(headers))


def phone_watch():
    while True:
        time.sleep(5)
        try:
            if PHONE.view()["on"]:
                INDEX.refresh(); PHONE.tick(list(INDEX.mine[-50:]), delivered())
                PHONE.tick_runs(_phone().person_run_ends(jlines(LOG, 512 * 1024)))
        except Exception: pass


# ---------- config: settings, modules, signing in ----------
_cfg_lock = threading.Lock()


def config_view():
    c = cfg()
    try: style = ANSWER_STYLE.read_text(errors="replace")
    except OSError: style = ""
    return {**{k: c.get(k) for k in ("max_sessions", "keeper_every_s", "keeper_idle_s", "usage_stop_pct", "auto_update", "update_every_s", "update_pace", "max_budget_usd", "max_daily_usd")}, "spend_today": spend_today(),
            "answer_style": style.strip(), "memory": memory_view(c), "default_model": default_model_view()}


# E39b (projects/everyday MEMORY.md): Settings › Everyday › Memory. core/memory.py is the only writer of ~/memory.md; the dash reads it with
# memory.read and deletes through `memory.py forget` run as the person (HIVE_NAME=person: memory.py refuses a run with none). Off is config.json "memory": "off", which memory.py reads.
MEMORY_FILE = HOME / "memory.md"


def memory_mod():
    """core/memory.py, or None on a core without it (the block then says memory isn't on this Hive)."""
    src = HOME / "core" / "memory.py"
    if not src.is_file(): return None
    import importlib.util
    try:
        spec = importlib.util.spec_from_file_location("h2_core_memory", src); mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
        return mod
    except Exception: return None


def memory_view(c=None):
    mem = memory_mod()
    if mem is None: return {"available": False}
    c = cfg() if c is None else c
    on = str(c.get("memory", "on")).lower() not in ("off", "false", "0", "no")   # as memory.is_off
    items = mem.read(MEMORY_FILE)
    try: INDEX.refresh(); have = {str(t) for t in INDEX.threads}
    except Exception: have = None
    meta, names = thread_meta(), thread_names() or {}
    for i in items:
        th = i["chat"]
        i["title"] = (meta.get(th) or {}).get("title") or names.get(th) or None
        i["gone"] = th != "settings" and have is not None and th not in have   # the dash has no chat delete: a chat with no posts left is gone
    return {"available": True, "on": on, "items": items}


def memory_forget(ids):
    """Delete lines (a list of ids) or all of them ("all"), through memory.py as the person."""
    r = subprocess.run([sys.executable, "-I", str(HOME / "core" / "memory.py"), "forget", *(["--all"] if ids == "all" else ids)],
                       env={**child_env(), "HIVE_NAME": "person"}, capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL)
    if r.returncode: raise ValueError((r.stderr or r.stdout).strip().removeprefix("memory: ")[:300] or "memory couldn't be changed")


# E33 (projects/everyday): the person's standing word on how they want answers, from Settings › Everyday. Its own file, not config.json: free text.
# The starter adds it to every new agent's instructions (core starter.py system_for) and rebuilds the warm agent when it changes.
ANSWER_STYLE = HOME / "answer-style.md"
ANSWER_STYLE_MAX = 2000


def spend_today():
    """Dollars the Hive's runs spent today (UTC), from the cost on each run's end in log.jsonl; 0 where core has no spend.py."""
    try:
        sys.path.insert(0, str(HOME / "core")); import spend
        return spend.spend_on(HOME / "log.jsonl")
    except Exception: return 0.0


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
    if "max_budget_usd" in d:
        v = d["max_budget_usd"]
        if v is not None and (isinstance(v, bool) or not isinstance(v, (int, float)) or not 0 < v < 10000): raise ValueError("the most one run may spend is a number of dollars above 0, or none")
        new["max_budget_usd"] = v
    if "max_daily_usd" in d:
        v = d["max_daily_usd"]
        if v is not None and (isinstance(v, bool) or not isinstance(v, (int, float)) or not 0 < v < 100000): raise ValueError("the most the Hive may spend in a day is a number of dollars above 0, or none")
        new["max_daily_usd"] = v
    if "auto_update" in d:
        if type(d["auto_update"]) is not bool: raise ValueError("auto_update is on or off")
        new["auto_update"] = d["auto_update"]
    if "update_every_s" in d:
        v = d["update_every_s"]
        if type(v) is not int or not 300 <= v <= 7 * 86400: raise ValueError("how often to look for a release is 5 minutes to 7 days")
        new["update_every_s"] = v
    if "update_pace" in d:
        if d["update_pace"] not in ("every", "daily"): raise ValueError("how often to update is every release or about once a day")
        new["update_pace"] = d["update_pace"]; new["update_pace_chosen"] = True  # install.sh drops a "daily" nobody chose
    if "memory_on" in d:
        if type(d["memory_on"]) is not bool: raise ValueError("memory is on or off")
        new["memory"] = None if d["memory_on"] else "off"   # on is the default: the key goes away
    forget = None
    if "memory_forget" in d:
        forget = d["memory_forget"]
        if forget != "all" and not (isinstance(forget, list) and 0 < len(forget) <= 60 and all(isinstance(x, str) and re.fullmatch(r"[0-9a-f]{6}", x) for x in forget)):
            raise ValueError("delete names memory lines by id, or all")
        if memory_mod() is None: raise ValueError("memory isn't on this Hive's core yet")
    style = None
    if "answer_style" in d:
        style = d["answer_style"]
        if not isinstance(style, str): raise ValueError("how you want answers is text")
        style = style.replace("\r\n", "\n").strip()
        if len(style) > ANSWER_STYLE_MAX: raise ValueError(f"how you want answers fits in {ANSWER_STYLE_MAX} characters")
    def put_style():  # after everything else in the request has saved, so a refused request changes nothing
        if style is None: return
        if style: write_text_private(ANSWER_STYLE, style + "\n")
        else: ANSWER_STYLE.unlink(missing_ok=True)
    if not new and style is None and forget is None: raise ValueError("nothing to change")
    # A delete goes first: memory.py may still refuse it (a line already gone), and then the switch and the rest stay as they were.
    if forget is not None: memory_forget(forget)
    if not new: put_style(); return config_view()
    with _cfg_lock:
        c = jload(HOME / "core" / "config.json")
        if not isinstance(c, dict): raise ValueError("core/config.json can't be read")
        c.update(new)
        for k in ("max_budget_usd", "max_daily_usd", "memory"):
            if k in c and c[k] is None: c.pop(k, None)
        t = HOME / "core" / f".config.json.{os.getpid()}.{threading.get_ident()}"
        t.write_text(json.dumps(c, indent=2) + "\n"); t.replace(HOME / "core" / "config.json")
    put_style()
    return config_view()


# E34 (projects/everyday): Settings › Download my chats. One zip, a folder per chat: chat.md (plain text, the messages in order) and its files.
EXPORT_FILES_MAX = 500 * 2**20   # files past this are listed in chat.md but left out of the zip, so the download stays sane


EXPORT_LOCK = threading.Lock()   # one export at a time: each is a full pass over the board and a temp file the size of the zip


def export_name(s, n=40):
    """A name that is safe as one folder or file name on every system: letters (any script), digits, - and _ only."""
    import unicodedata
    return re.sub(r"[^\w]+|_+", "-", unicodedata.normalize("NFC", s)).strip("-")[:n].strip("-") or "chat"


def export_file(z, src, arc, left):
    """Copy one of the person's files into the zip, at most left bytes. The bytes come from one open handle, so a swap after
    the checks can't put another file in: no symlinks, a regular file under ~/files, one link only (a hard link could be to anything)."""
    try:
        real = Path(os.path.realpath(src))
        if not real.is_relative_to(HOME_FILES.resolve()) or Path(src).is_symlink(): return "gone"
        fd = os.open(real, os.O_RDONLY | os.O_NOFOLLOW)
    except (OSError, ValueError): return "gone"
    with os.fdopen(fd, "rb") as f:
        st = os.fstat(f.fileno())
        if not stat.S_ISREG(st.st_mode) or st.st_nlink > 1: return "gone"
        if st.st_size > left: return "over"
        with z.open(arc, "w") as w: shutil.copyfileobj(_Capped(f, st.st_size), w)   # no more than was checked, even if it grows
    return st.st_size


class _Capped:
    def __init__(self, f, n): self.f, self.n = f, n
    def read(self, k=-1):
        if self.n <= 0: return b""
        b = self.f.read(self.n if k is None or k < 0 else min(k, self.n)); self.n -= len(b); return b


def export_zip(out):
    """Write the zip of every chat on the board into the binary file out."""
    import zipfile
    threads, order = {}, []
    with BOARD.open(errors="replace") as f:
        for ln in f:
            try: p = json.loads(ln)
            except ValueError: continue
            if not isinstance(p, dict): continue
            th = str(p.get("thread") or p.get("id") or "")
            if not th: continue
            if th not in threads: threads[th] = []; order.append(th)
            threads[th].append(p)
    meta, names, left, now = thread_meta(), thread_names() or {}, EXPORT_FILES_MAX, datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    plain = lambda t: re.sub(r"^reply to message [0-9a-f]+:\s*", "", re.sub(r"^(?:[ \t]*@[\w.-]+[,:]?[ \t]+)+", "", t), flags=re.I)   # the words, without the routing (as the chat shows them)
    used, day = set(), lambda a: str(a)[:10] if re.fullmatch(r"\d{4}-\d{2}-\d{2}", str(a)[:10]) else "undated"
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
        index = []
        for th in order:
            posts = threads[th]
            first = next((p for p in posts if p.get("from") == PERSON), posts[0])
            title = str((meta.get(th) or {}).get("title") or names.get(th) or " ".join(plain(str(first.get("text", ""))).split())[:70] or th)
            base = f"{day(posts[0].get('at', ''))} {export_name(title)}"
            while base.lower() in used: base += "-" + export_name(th, 12)
            used.add(base.lower()); taken = {"chat.md"}
            lines = [f"# {title}", "", f"Chat {th}, {len(posts)} messages. Exported {now}.", ""]
            for p in posts:
                who = "You" if p.get("from") == PERSON else "Agent " + str(p.get("from")) if re.fullmatch(r"[a-z][0-9a-f]{4}", str(p.get("from") or "")) else str(p.get("from") or "?")
                at = str(p.get("at", ""))[:16].replace("T", " ")
                text = str(p.get("text", "")); m = re.match(r"\(edited message (\S+): this version replaces it\..*?\)\n", text)
                if m: text = text[m.end():]   # the note is for the agent; the reader gets a mark instead
                if p.get("from") == PERSON: text = plain(text)
                lines += ["---", f"{who} · {at} UTC" + (f" (edited, replaces {m.group(1)})" if m else ""), "", text.rstrip()]
                for fl in p.get("files") or []:
                    if not isinstance(fl, dict): continue
                    name = re.sub(r"[^\w.-]", "_", str(fl.get("name") or "file")).lstrip(".")[-80:] or "file"
                    leaf = f"{re.sub(r'[^0-9A-Za-z]', '', str(p.get('id', '')))[:12]}-{name}"
                    while leaf.lower() in taken: leaf = "x" + leaf
                    got = export_file(z, str(fl.get("path", "")), f"{base}/{leaf}", left)
                    if isinstance(got, int): left -= got; taken.add(leaf.lower()); lines.append(f"[file: {leaf}]")
                    else: lines.append(f"[file: {name}, " + ("not in this download: the download's size limit was reached]" if got == "over" else "gone]"))
                lines.append("")
            z.writestr(f"{base}/chat.md", "\n".join(lines))
            index.append(f"{base}/chat.md  ({len(posts)} messages)")
        try:   # E39b: what the chats remember about the person (Settings › Everyday › Memory), as the file is
            with MEMORY_FILE.open("rb") as f: mem = f.read(64 * 1024)
        except OSError: mem = None
        if mem is not None: z.writestr("memory.md", mem)
        z.writestr("README.txt", f"Your chats, exported {now}. One folder per chat: chat.md is the chat as plain text, oldest message first, with its files beside it."
                   + (" memory.md holds what your chats remember about you (Settings › Everyday › Memory)." if mem is not None else "") + "\n\n" + "\n".join(index) + "\n")


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
    try: beat = (STATE / "starter.json").stat().st_mtime
    except OSError: return False
    try:  # a clean stop (SIGTERM) leaves ~/.hive/stopped, which the next start removes: down at once, not after the heartbeat's minute
        if (STATE / "stopped").stat().st_mtime >= beat: return False
    except OSError: pass
    try:  # a crash (SIGKILL, OOM) leaves no mark, but starter.lock holds the pid: gone means down now, not after the minute (a slow loop is still up)
        pid = int((STATE / "starter.lock").read_text().strip())
        if b"starter.py" not in Path(f"/proc/{pid}/cmdline").read_bytes(): return False
    except FileNotFoundError as e:
        if e.filename and str(e.filename).startswith("/proc/"): return False
    except (OSError, ValueError): pass  # no lock, or a second starter waiting on it emptied the file, or no /proc: the heartbeat decides
    return time.time() - beat < 60  # the door's test


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
            "error": st.get("error") or st.get("stuck"), "running": running, "auto_update": c.get("auto_update", True), "update_every_s": c.get("update_every_s") or 300, "update_pace": c.get("update_pace") or "every"}


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


def share_pack():
    """Settings' Send everything: `hive-report pack --yes`, one zip of the board, log, projects and post files. The report module refuses unless share.everything is on."""
    f = HOME / "modules" / "report" / "report.py"
    if not SETTINGS.is_file() or not f.is_file(): raise ValueError("this Hive has no way to send a copy to the makers")
    try: r = subprocess.run([sys.executable, "-I", str(f), "pack", "--yes"], env=child_env(), capture_output=True, text=True, timeout=900)
    except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"couldn't send it: {e}")
    said = r.stdout.strip()
    if r.returncode or not said.startswith("everything sent"): raise ValueError(short(said or r.stderr, 300) or "couldn't send it")
    return {"ok": True, "said": said.removeprefix("everything sent: ")}


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


# Settings > Connectors (projects/everyday/CONNECTORS.md, E55): read-only. The rows come from connectors.json; their state is read from
# the module list (is `reach` in), the secrets folder (names only) and `systemctl --user is-active`. Nothing here runs `conn`: turning a
# connector on stays an agent's or a terminal's step, and the only writes on that screen are the existing /api/module and /api/secret.
CONNECTORS = Path(__file__).resolve().parent / "connectors.json"


def connector_repo():
    """The reach bundle's GitHub page, from the module catalog (core/modules.json on an install, core/dist/modules.json on a source tree), or None."""
    for f in (HOME / "core" / "modules.json", HOME / "core" / "dist" / "modules.json"):
        m = re.fullmatch(r"(https://github\.com/[\w.-]+/[\w.-]+?)(?:\.git)?/?", str(((jload(f, {}) or {}).get("reach") or {}).get("repo") or ""))
        if m: return m[1]
    return None


_CONN_CACHE = {}   # {secrets cap (bool): (at, view)}; set_secret empties it, so a token saved from the Secrets section shows at once
UNIT_NAME = re.compile(r"[\w@.-]+")   # connectors.json's unit names go to systemctl as argv; a bad edit there is dropped, not run


def unit_file(u):
    """A table unit as systemctl wants it: arm-watch.timer stays, a bare conn@ntfy gets .service (bare in the table, so the export's address scan passes)."""
    return u if re.search(r"\.(service|timer|socket|path)$", u) else u + ".service"


def connector_states(rows, installed, folder, have, active, repo=None):
    """Each row's state: cant_load, restarting, token_missing, on, two_way, ready, usable, nothing or off. folder(name) is its code
    folder or None; have is the set of secret names present, or None for a caller without the secrets cap (then no state tells which
    secrets exist: no Ready, Set up or Token missing, and no saved flag); active maps a unit to its is-active word; repo is the
    bundle's GitHub page, for each row's README link.
    ready: it has a unit and its secret is saved, so an agent or a terminal can turn it on. usable: no unit, and its secret is saved, so
    the agent can use it now (a pasted calendar link). nothing: no unit and no secret, so there's nothing to turn on (an agent sets it up).
    off: anything else not running, including a unit with no secret (ntfy, web push, feeds), which still needs its own setup."""
    out = []
    for c in rows:
        r = {k: c.get(k) for k in ("name", "kind", "label", "line", "needs", "secret", "can_finish_here", "two_way", "link", "shared")}
        r["readme"] = f"{repo}/blob/main/{c['name']}/README.md" if repo else None
        if not installed: r["state"] = None
        else:
            unit, sec = c.get("unit") or "", c.get("secret")
            d, a = folder(c["name"]), active.get(unit, "")
            has = None if have is None or not sec else sec in have
            if have is not None and sec: r["saved"] = has
            if d is None or not (d / "module.json").is_file() or a == "failed": r["state"] = "cant_load"
            elif a in ("activating", "deactivating", "reloading"): r["state"] = "restarting"   # a crash loop sits in activating (auto-restart)
            elif a == "active": r["state"] = "token_missing" if has is False else "on"
            elif c.get("two_way"): r["state"] = "two_way"
            elif has: r["state"] = "ready" if unit else "usable"
            elif not unit and not sec: r["state"] = "nothing"
            else: r["state"] = "off"
        out.append(r)
    return out


def connectors_view(fresh=False, secrets=True):
    """{installed, removable (reach is a module here, not loose folders), rows}; cached 30 s like the module list's slow parts, unless fresh (asked right after a write on that screen).
    secrets: the caller has the secrets cap; without it the states say nothing about which secrets exist (E55 review 6)."""
    hit = _CONN_CACHE.get(bool(secrets))
    if not fresh and hit and time.time() - hit[0] < 30: return hit[1]
    rows = (jload(CONNECTORS, {}) or {}).get("connectors", [])
    try: mods = {m["name"]: m for m in modules_view()}
    except (OSError, subprocess.SubprocessError, KeyError, TypeError): mods = {}
    bundle, local = HOME / "modules" / "reach", HOME / "modules"
    removable = (mods.get("reach") or {}).get("state") == "installed"
    installed = removable or (local / "conn").is_dir()   # reach as a bundle, or its folders laid out one by one (this box)
    def folder(name):
        for d in (bundle / name, local / name):
            if d.is_dir(): return d
        return None
    have = {s["name"] for s in secrets_view()} if secrets else None
    units = [c["unit"] for c in rows if c.get("unit") and UNIT_NAME.fullmatch(c["unit"])]
    active = {}
    if installed and units:
        try:
            r = subprocess.run(["systemctl", "--user", "is-active", *map(unit_file, units)], capture_output=True, text=True, timeout=10)
            active = dict(zip(units, r.stdout.split()))
        except (OSError, subprocess.SubprocessError): pass
    view = {"installed": installed, "removable": removable, "rows": connector_states(rows, installed, folder, have, active, connector_repo())}
    _CONN_CACHE[bool(secrets)] = (time.time(), view)
    return view


def module_do(what, name):
    name = str(name or "").strip()
    if what not in ("add", "remove"): raise ValueError("add or remove")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*|(https://|ssh://|git@)[A-Za-z0-9_.@:/~+-]+", name): raise ValueError("give a module's name, or a git URL")
    if what == "remove" and name == "dash": raise ValueError("dash is this app, so it can't be removed from here")
    r = subprocess.run([str(HIVE_BIN), "module", what, name], capture_output=True, text=True, env=child_env(), cwd=str(HOME), timeout=900)
    if r.returncode: raise ValueError(short(r.stderr or r.stdout, 500) or f"module {what} failed")
    return r.stdout[-2000:]


# The Slack door, set up from Settings > Modules > slack-door instead of by hand (modules/slack-door/README.md, Setup). The tokens go
# where the door reads them, mode 600, and are never sent back. Everyone in the chosen channel becomes an owner: the channel is the
# trust group, so a big one is refused. HIVE_SLACK_API points the calls at a stub for tests.
SLACK_API = os.environ.get("HIVE_SLACK_API", "https://slack.com/api/")
SLACK_MAX_PEOPLE = 20
SLACK_SAID = {"invalid_auth": "Slack refused the {what}: copy it again", "not_authed": "Slack refused the {what}: copy it again",
              "account_inactive": "the Slack app for the {what} was removed or its workspace closed", "token_revoked": "the {what} was revoked in Slack: make a new one",
              "missing_scope": "the Slack app lacks a permission: make it again from this page's manifest", "not_allowed_token_type": "that isn't the right kind of token for the {what}"}


def slack_dir(): return HOME / ".config/hive"


class SlackUnreachable(ValueError): pass


def slack_call(method, token, what, _timeout=20, **params):
    import urllib.request, urllib.error, urllib.parse
    req = urllib.request.Request(SLACK_API + method, data=urllib.parse.urlencode(params).encode(),
                                 headers={"Authorization": "Bearer " + token, "Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(req, timeout=_timeout) as r: d = json.loads(r.read())
    except (urllib.error.URLError, OSError, ValueError) as e: raise SlackUnreachable(f"couldn't reach Slack ({short(str(e), 120)}): check this computer's connection")
    if not d.get("ok"):
        err = str(d.get("error") or "unknown")
        raise ValueError(SLACK_SAID.get(err, "Slack said {err} for the {what}").format(what=what, err=err))
    return d


_SLACK_TOKEN_SEEN = {}   # (bot token file's mtime_ns, size) -> (when checked, Slack's refusal in words or "")
_SLACK_CHECKING = {}     # the same key -> the thread asking Slack now
_SLACK_LOCK = threading.Lock()
SLACK_RECHECK, SLACK_WAIT = 300, 0.8


def _slack_token_check(key, token):
    try: slack_call("auth.test", token, "bot token (xoxb-)"); said = ""
    except SlackUnreachable: said = ""
    except (ValueError, OSError) as e: said = str(e)
    with _SLACK_LOCK:
        _SLACK_CHECKING.pop(key, None)
        if len(_SLACK_TOKEN_SEEN) > 8: _SLACK_TOKEN_SEEN.clear()   # old token files; a late answer for one must not wipe the current one
        _SLACK_TOKEN_SEEN[key] = (time.time(), said)


def slack_token_problem():
    """Slack's refusal of the stored bot token (revoked in Slack, app removed), or "": one auth.test at most every SLACK_RECHECK s per token file.
    The check runs in its own thread and the page waits at most SLACK_WAIT s for it, so a hung Slack never stalls Settings: the last known
    answer is shown and a later load gets the new one. A Slack that can't be reached says nothing, so a slow network never shows a working door as broken."""
    f = slack_dir() / "slack-door-bot-token"
    try: st = f.stat(); key = (st.st_mtime_ns, st.st_size)
    except OSError: return ""
    with _SLACK_LOCK:
        seen = _SLACK_TOKEN_SEEN.get(key)
        if seen and time.time() - seen[0] < SLACK_RECHECK: return seen[1]
        t = _SLACK_CHECKING.get(key)
        if not t:
            try: token = f.read_text().strip()
            except OSError: return ""
            t = _SLACK_CHECKING[key] = threading.Thread(target=_slack_token_check, args=(key, token), daemon=True); t.start()
    t.join(SLACK_WAIT)
    with _SLACK_LOCK: seen = _SLACK_TOKEN_SEEN.get(key)
    return seen[1] if seen else ""


def door_refusal_file(): return Path(os.environ.get("HIVE_DOOR_STATE") or HOME / ".local/state/slack-door") / "connect-refused.json"


def door_refusal():
    """What Slack said when the door last failed to connect (door.py note_connect), or None. Covers the app token, which the view's own check can't."""
    r = jload(door_refusal_file(), {}) or {}
    said = r.get("said") if isinstance(r, dict) else None
    return said if isinstance(said, str) and said.startswith("Slack refused") else None


def slack_door_view():
    c = jload(slack_dir() / "slack-door.json", {}) or {}
    toks = all((slack_dir() / f"slack-door-{k}-token").is_file() for k in ("bot", "app"))
    v = {"connected": bool(toks and isinstance(c, dict) and c.get("owners")), "channel": str(c.get("channel_name") or "") if isinstance(c, dict) else ""}
    if v["connected"] and (p := slack_token_problem() or door_refusal()): v["problem"] = p
    return v


def slack_door_connect(d, signer=None):
    bot, app, ch = (str(d.get(k) or "").strip() for k in ("bot_token", "app_token", "channel"))
    if not re.fullmatch(r"xoxb-[A-Za-z0-9-]{8,250}", bot) or not re.fullmatch(r"xapp-[A-Za-z0-9-]{8,250}", app): raise ValueError("The bot token starts xoxb- and the app token xapp-.")
    name = ch.lstrip("#").strip().lower()
    chan_ok = bool(re.fullmatch(r"[a-z0-9][a-z0-9_.-]{0,79}", name))
    if ch.startswith("#") and not chan_ok: raise ValueError("Give the channel's name, like #hive.")
    if len(ch) > 80: raise ValueError("That name is too long: give your name as Slack shows it, or a #channel.")
    me = slack_call("auth.test", bot, "bot token (xoxb-)")
    slack_call("apps.connections.open", app, "app token (xapp-)")
    if not ch.startswith("#"):   # empty, or a person's name: direct messages with the app (its Messages tab), no channel to make
        try: return slack_door_connect_dm(bot, app, ch, signer)
        except LookupError as e:   # a bare word that names nobody may be a channel written without its #
            if not chan_ok: raise ValueError(str(e))
    found, cursor = None, ""
    for _ in range(50):
        r = slack_call("conversations.list", bot, "bot token (xoxb-)", types="public_channel,private_channel", exclude_archived="true", limit="200", **({"cursor": cursor} if cursor else {}))
        found = next((c for c in r.get("channels") or [] if c.get("name") == name), None)
        cursor = (r.get("response_metadata") or {}).get("next_cursor") or ""
        if found or not cursor: break
    if not found: raise ValueError(f"The app can't see a channel #{name}. Check the name; a private channel needs /invite @Hive in it first.")
    if not found.get("is_member"): raise ValueError(f"The app isn't in #{name} yet. In Slack, type /invite @Hive there, then Connect again.")
    mem = [u for u in slack_call("conversations.members", bot, "bot token (xoxb-)", channel=found["id"], limit="200").get("members") or [] if u != me.get("user_id")]
    if len(mem) > SLACK_MAX_PEOPLE: raise ValueError(f"#{name} has {len(mem)} members. Everyone in the channel can direct this Hive with this computer's full rights, so pick one with {SLACK_MAX_PEOPLE} or fewer.")
    people = [u for u in mem if u != "USLACKBOT" and not (lambda x: x.get("is_bot") or x.get("deleted"))(slack_call("users.info", bot, "bot token (xoxb-)", user=u).get("user") or {})]
    if not people: raise ValueError(f"Nobody but the app is in #{name}. Join it yourself, then Connect again.")
    started = slack_door_save(bot, app, people, [found["id"]], "#" + name)
    dash_note(f"The Slack door was connected to #{name} by {signer or 'this machine'}, in the web app; {len(people)} owner(s). Tokens are in `~/.config/hive/slack-door-*-token`, mode 600. Don't print them.", "slack-door")
    return {"said": f"Connected to #{name}. {len(people)} {'person' if len(people) == 1 else 'people'} there can use it{started}.", **slack_door_view()}


def slack_door_connect_dm(bot, app, who, signer=None):
    """Connect with no channel: the owner talks to the app in its Messages tab. Who that is comes from the workspace's people: the only
    person in it, or the one whose Slack name matches what was typed. Nobody else is let in (the door answers listed people only)."""
    people, cursor = [], ""
    for _ in range(50):
        r = slack_call("users.list", bot, "bot token (xoxb-)", limit="200", **({"cursor": cursor} if cursor else {}))
        people += [u for u in r.get("members") or [] if u.get("id") != "USLACKBOT" and not u.get("is_bot") and not u.get("deleted") and not u.get("is_app_user")]
        cursor = (r.get("response_metadata") or {}).get("next_cursor") or ""
        if not cursor: break
    key = lambda x: re.sub(r"\s+", " ", str(x or "")).strip().lstrip("@").lower()
    if who.strip():
        want = key(who)
        hit = [u for u in people if want in {key(u.get("name")), key(u.get("real_name")), key((u.get("profile") or {}).get("display_name")), key((u.get("profile") or {}).get("real_name"))} - {""}]
        if not hit: raise LookupError(f"Nobody in this Slack is called {who.strip()}. Give your name as Slack shows it, or a #channel.")
        if len(hit) > 1: raise ValueError(f"{len(hit)} people in this Slack are called {who.strip()}. Use a #channel with just you in it instead.")
    else:
        if not people: raise ValueError("The app can't see anyone in this Slack. Check that the app has the users:read scope (step 1's link adds it).")
        if len(people) > 1: raise ValueError(f"This Slack has {len(people)} people. Type your name as Slack shows it in the box, or a #channel, so only you can use the Hive.")
        hit = people
    owner = hit[0]["id"]
    started = slack_door_save(bot, app, [owner], [], "direct messages")
    dash_note(f"The Slack door was connected to direct messages with the app by {signer or 'this machine'}, in the web app; one owner. Tokens are in `~/.config/hive/slack-door-*-token`, mode 600. Don't print them.", "slack-door")
    return {"said": f"Connected to your direct messages with the app. Open the app in Slack and write to it there{started}.", **slack_door_view()}


def slack_door_save(bot, app, owners, channels, label):
    """Install and start the door with these tokens, owners and channels (an empty list: direct messages only). Returns a note for a test Hive."""
    door = HOME / "modules" / "slack-door" / "door.py"
    if not door.is_file(): module_do("add", "slack-door")
    r = subprocess.run([sys.executable, "-I", str(door), "install"], capture_output=True, text=True, env=child_env(), cwd=str(HOME), timeout=60)
    if r.returncode: raise ValueError(short(r.stderr or r.stdout, 300) or "slack-door install failed")
    sd = slack_dir(); sd.mkdir(parents=True, exist_ok=True)
    write_text_private(sd / "slack-door-bot-token", bot + "\n")
    write_text_private(sd / "slack-door-app-token", app + "\n")
    door_refusal_file().unlink(missing_ok=True)   # Slack just took both tokens: an old refusal no longer holds
    c = jload(sd / "slack-door.json", {}) or {}
    c = c if isinstance(c, dict) else {}
    c.update(owners=owners, members=[u for u in c.get("members") or [] if u not in owners], channels=channels, channel_name=label)
    write_text_private(sd / "slack-door.json", json.dumps(c, indent=1) + "\n")
    # Start it only when systemd's units are this Hive's: a test Hive under another HOME must not restart the machine's real door.
    if HOME.resolve() == Path(pwd.getpwuid(os.getuid()).pw_dir).resolve():
        for a in (["daemon-reload"], ["enable", "--now", "slack-door.path"], ["restart", "slack-door.service"]):
            q = subprocess.run(["systemctl", "--user", *a], capture_output=True, text=True, timeout=60, env=child_env())
            if q.returncode: raise ValueError(f"saved, but the door didn't start: {short(q.stderr, 200)}")
        started = ""
    else: started = " (a test Hive: the door's service wasn't started)"
    return started


def slack_door_disconnect(signer=None):
    """Settings > Modules > slack-door "Disconnect": stop the door, then delete both token files so nothing can answer in Slack. slack-door.json
    stays (owners, channel), so Connect again keeps who's on the list. Like Connect, only the real home's systemd units are touched."""
    stopped = " (a test Hive: no service to stop)"
    if HOME.resolve() == Path(pwd.getpwuid(os.getuid()).pw_dir).resolve():
        for a in (["disable", "--now", "slack-door.path"], ["stop", "slack-door.service"]):
            q = subprocess.run(["systemctl", "--user", *a], capture_output=True, text=True, timeout=60, env=child_env())
            if q.returncode and "not loaded" not in q.stderr and "does not exist" not in q.stderr: raise ValueError(f"the door didn't stop, so the tokens are kept: {short(q.stderr, 200)}")
        stopped = ""
    for k in ("bot", "app"): (slack_dir() / f"slack-door-{k}-token").unlink(missing_ok=True)
    dash_note(f"The Slack door was disconnected by {signer or 'this machine'}, in the web app: its tokens are deleted and it answers nobody in Slack.", "slack-door")
    return {"said": f"Disconnected. Nothing answers in Slack now{stopped}.", **slack_door_view()}

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


class ShopSignin:
    """Sign in to a shop from the dashboard (thread 11241110ade5, warden 11241330fa1c): tools/shop_signin.mjs runs a visible Chrome in the
    shop's own kept profile, in its own unit (memory far over the dash's), and the page shows its screen and sends clicks and typing.
    The person types the password and solves the captcha; nothing keeps or logs what they type. One at a time; it closes itself a few
    seconds after the shop shows signed in, after IDLE_S with no input, or after LIFE_S, so it is never a standing window onto the browser.
    Only the person's own app may use it (buy_person, like Buying): agents on this machine can reach the local route."""
    SITES = {"jlc": "JLCPCB"}
    # site "web": any address the person names (thread 11241110ade5, warden 112500380c83), in the profile `web` uses, so agents can then read
    # that site's pages signed in. Read-only for agents (web navigates and reads, never clicks); no saved passwords (the helper sets that).
    WEB_LOG = HOME / ".hive" / "web-signins.json"   # {base: {"signed_at": epoch}}: hosts only, read by modules/web/web.mjs
    # Banks and payments, email and identity, government: nobody signs in to these here, and `web` never reads them signed in (warden's advice;
    # the list is tools/web_refuse.json, which tools/web_sites.mjs refused() reads too)
    # A copy without tools/ (the release export ships none, and so no sign-in helper either) refuses every site rather than failing to start.
    try: REFUSE = json.loads((Path(__file__).resolve().parent / "tools" / "web_refuse.json").read_text())
    except (OSError, ValueError): REFUSE = None
    SIGNED_DIR = HOME / ".cache" / "web-signed"   # one kept profile per signed-in site (tools/web_sites.mjs signedProfile)
    INPUTS = {"click", "wheel", "drag", "text", "key", "back"}
    LIFE_S, IDLE_S, SIGNED_CLOSE_S = 900, 300, 4
    POLL_S = 90   # no screen asked for this long: the page that showed it is gone (closed tab, lost phone), so the browser goes too
    LOG = HOME / ".hive" / "shop-signin.json"   # {site: {"signed_at": epoch}}: when each shop was last signed in here, for the session-lifetime measure

    def __init__(self):
        self.lock, self.cond, self.wlock = threading.Lock(), threading.Condition(), threading.Lock()
        self.proc = self.site = self.unit = self.closing = None
        self.n = 0
        self.web_base = self.web_href = None
        self.state, self.why, self.frame, self.seq, self.size, self.started, self.touched, self.polled = "idle", "", b"", 0, (0, 0), 0.0, 0.0, 0.0

    @staticmethod
    def base(host):
        """The site a host belongs to, as tools/web_sites.mjs baseOf says it: www.etsy.com -> etsy.com, www.amazon.co.uk -> amazon.co.uk."""
        l = [x for x in str(host or "").lower().rstrip(".").split(".") if x]
        if len(l) >= 3 and len(l[-1]) == 2 and l[-2] in ("co", "com", "org", "net", "ac", "gov", "edu", "ne", "or", "gc", "qc", "on"): return ".".join(l[-3:])
        return ".".join(l[-2:])

    @classmethod
    def refused(cls, base):
        """Why this site is refused, as tools/web_sites.mjs refused() says it, or ""."""
        b = str(base or "").lower(); name = b.split(".")[0]; R = cls.REFUSE
        if not R: return "can't be checked here: this copy has no tools/web_refuse.json"
        for k, l in R["names"].items():
            if name in l: return R["why"][k]
        for k, l in R["bases"].items():
            if b in l: return R["why"][k]
        for k, l in R["ends"].items():
            if any(b.endswith(e) for e in l): return R["why"][k]
        return R["why"]["money"] if "bank" in name else ""

    def web_url(self, url):
        """A public web address the person may sign in to: (href, base). The helper opens it; only its host is ever kept or shown."""
        if not isinstance(url, str) or not url.strip() or len(url) > 2000: raise ValueError("type the site's address, like etsy.com")
        url = url.strip()
        if "://" not in url: url = "https://" + url
        try: u = urlsplit(url); host = (u.hostname or "").lower(); u.port
        except ValueError: raise ValueError("that isn't a web address")
        if u.scheme not in ("http", "https"): raise ValueError("only http and https addresses")
        if u.username is not None or u.password is not None or "@" in u.netloc: raise ValueError("leave the name and password out of the address: type them on the site")
        try: ip = ipaddress.ip_address(host.strip("[]"))
        except ValueError: ip = None
        # a name with a real top-level label: no number address in any spelling Chrome accepts (127.1, 0x7f.1, 2130706433)
        if ip or "." not in host or not re.fullmatch(r"[a-z0-9.-]+", host) or not re.fullmatch(r"[a-z][a-z0-9-]{1,62}", host.rsplit(".", 1)[-1]) or host.endswith((".local", ".internal", ".lan", ".home", ".localhost", ".arpa", ".test", ".invalid")) or host in ("localhost",):
            raise ValueError("a public site's address, like etsy.com")
        b = self.base(host)
        why = self.refused(b)
        if why: raise ValueError(f"{b} {why}")
        return urlunsplit((u.scheme, u.netloc.lower(), u.path or "/", u.query, "")), b

    def name(self, site):
        return self.web_base if site == "web" else self.SITES.get(site, site)

    def web_log(self):
        try: v = json.loads(self.WEB_LOG.read_text()); return v if isinstance(v, dict) else {}
        except Exception: return {}

    def web_mark(self, base, signed=True):
        log = self.web_log()
        if signed: log[base] = {"signed_at": int(time.time())}
        else: log.pop(base, None)
        self.WEB_LOG.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.WEB_LOG.with_suffix(f".tmp{os.getpid()}")
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)   # private from the first byte
        with os.fdopen(fd, "w") as f: f.write(json.dumps(log))
        os.replace(tmp, self.WEB_LOG)

    def cmd(self, site):
        env = os.environ.get("HIVE_SHOP_SIGNIN_CMD")   # tests: the helper on a fake page, no unit
        if env: self.unit = None; return json.loads(env) + [site]
        node, tool = shutil.which("node") or "/usr/bin/node", str(Path(__file__).resolve().parent / "tools" / "shop_signin.mjs")
        self.n += 1; self.unit = f"hive-shop-signin-{int(time.time())}-{self.n}-{secrets.token_hex(3)}"
        return ["systemd-run", "--user", "--quiet", "--collect", "--pipe", f"--unit={self.unit}", "-p", "MemoryMax=1500M",
                "-p", f"RuntimeMaxSec={self.LIFE_S + 120}", f"--setenv=HOME={HOME}", f"--setenv=PATH={os.environ.get('PATH', '/usr/bin:/bin')}", node, tool, site]   # the web address goes on stdin (start), not here where the process list shows it

    def signed_log(self):
        try: return json.loads(self.LOG.read_text())
        except Exception: return {}

    def view(self):
        log = self.signed_log()
        return {"site": self.site, "state": self.state, "why": self.why, "seq": self.seq, "w": self.size[0], "h": self.size[1],
                "left": max(0, int(self.started + self.LIFE_S - time.time())) if self.proc else 0,
                "sites": [{"site": k, "name": v, "signed_at": (log.get(k) or {}).get("signed_at")} for k, v in self.SITES.items()],
                "name": self.name(self.site) if self.site else None,
                "web": sorted(({"base": k, "signed_at": (v or {}).get("signed_at")} for k, v in self.web_log().items() if isinstance(k, str)), key=lambda x: x["base"])}

    def start(self, site, url=None):
        if site == "web": href, base = self.web_url(url)
        elif not isinstance(site, str) or site not in self.SITES: raise ValueError("no such shop")
        old = self.closing
        if old and old.poll() is None:   # the last one is still shutting down (it saves its cookies first): wait for it rather than hand it back
            try: old.wait(timeout=20)
            except subprocess.TimeoutExpired: raise ValueError("the last sign-in is still closing: try again in a moment")
        with self.lock:
            c = self.closing   # a close that began after the wait above (the watchdog): its helper may still hold the profile
            if c and c.poll() is None and c is not old: raise ValueError("the last sign-in is still closing: try again in a moment")
            if self.proc and self.proc.poll() is None and self.proc is not self.closing:
                if self.site == site and (site != "web" or self.web_base == base): return self.view()
                raise ValueError(f"a sign-in to {self.name(self.site)} is open: close it first")
            if site == "web": self.web_href, self.web_base = href, base
            try:
                proc = subprocess.Popen(self.cmd(site), stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
                if site == "web": proc.stdin.write((json.dumps({"t": "url", "url": href}) + "\n").encode()); proc.stdin.flush()
            except (OSError, ValueError) as e: raise ValueError(f"can't start the browser: {getattr(e, 'strerror', None) or e}")
            with self.cond:
                self.proc, self.closing = proc, None
                self.site, self.state, self.why, self.frame, self.size = site, "starting", "", b"", (0, 0)
                self.started = self.touched = self.polled = time.time()
            threading.Thread(target=self.read, args=(self.proc, base if site == "web" else None), daemon=True).start()
            threading.Thread(target=self.watch, args=(self.proc,), daemon=True).start()
        return self.view()

    def read(self, proc, base=None):
        for line in proc.stdout:
            try: m = json.loads(line)
            except Exception: continue
            if proc is not self.proc: break
            if not isinstance(m, dict): continue   # one odd line must not stop the reading (a full pipe would stall the helper)
            if m.get("t") == "frame":
                try: data, size = base64.b64decode(m.get("data") or ""), (int(m.get("w") or 0), int(m.get("h") or 0))
                except Exception: continue
                with self.cond:
                    self.frame, self.seq, self.size = data, self.seq + 1, size
                    if self.state == "starting": self.state = "open"
                    self.cond.notify_all()
                continue
            with self.cond:
                if m.get("t") == "state":
                    st = m.get("state")
                    if st == "signed" and self.state != "signed":   # the helper says so only once the page has held a signed-in address a while
                        self.state = "signed"
                        log = self.signed_log(); log[self.site] = {"signed_at": int(time.time())}
                        try: self.LOG.parent.mkdir(parents=True, exist_ok=True); self.LOG.write_text(json.dumps(log))
                        except OSError: pass
                        threading.Timer(self.SIGNED_CLOSE_S, self.close_if_signed, args=(proc,)).start()
                    elif st == "checking" and self.state == "open": self.state = "checking"   # signed in on the page; waiting to see it hold
                    elif st == "open" and self.state in ("signed", "checking"): self.state = "open"
                    elif st in ("gone", "timeout", "closed") and self.state != "signed": self.state, self.why = "closed", {"timeout": "it was open too long", "gone": "the browser closed"}.get(st, self.why)
                elif m.get("t") == "error": self.state, self.why = "failed", str(m.get("why") or "")[:200]
                self.cond.notify_all()
        proc.wait()
        # a site sign-in closed without Done, for a site not already on the list: its profile goes, so nothing half signed in is kept
        if base and base not in self.web_log():
            try: shutil.rmtree(self.SIGNED_DIR / base)
            except OSError: pass
        with self.cond:
            if proc is self.proc:
                if self.state in ("starting", "open", "checking"): self.state, self.why = "closed", self.why or "the browser closed"
                self.proc, self.frame = None, b""
            if proc is self.closing: self.closing = None
            self.cond.notify_all()

    def watch(self, proc):
        while proc.poll() is None:
            time.sleep(min(5, self.POLL_S / 3))
            if proc is not self.proc or proc is self.closing: continue
            now, why = time.time(), None
            if now - self.touched > self.IDLE_S: why = "nobody used it for a while"
            elif now - self.started > self.LIFE_S + 30: why = "it was open too long"
            elif now - self.polled > self.POLL_S: why = "the page showing it went away"
            if why and proc is self.proc and proc is not self.closing: self.why = why; self.close(proc)

    def tell(self, msg):
        with self.wlock:   # one line at a time: two requests at once must not interleave their bytes
            p = self.proc
            if not p or p.poll() is not None or p is self.closing: raise ValueError("no sign-in is open")
            try: p.stdin.write((json.dumps(msg) + "\n").encode()); p.stdin.flush()
            except (BrokenPipeError, OSError, ValueError): raise ValueError("the browser closed")

    def close_if_signed(self, proc):
        if self.state == "signed": self.close(proc)   # still signed in a few seconds on: the shop didn't send it back to its sign-in page

    def close(self, proc=None, done=False):
        with self.wlock:
            p = proc or self.proc
            if not p or p.poll() is not None or p is not self.proc or p is self.closing: return {"ok": True}
            # any site: only the person's Done marks it signed in; Cancel or Close never does (review 2026-11-25), even if it looked signed in
            if self.site == "web" and self.web_base and done:
                try: self.web_mark(self.web_base); self.state = "signed"
                except OSError: pass
            self.closing, unit = p, self.unit
            try: p.stdin.write(b'{"t":"close"}\n'); p.stdin.flush(); p.stdin.close()
            except (BrokenPipeError, OSError, ValueError): pass
        def hard():
            time.sleep(12)   # the helper gives Chrome 6 s to close cleanly and save its cookies
            if p.poll() is None:
                if unit:   # the helper runs in its own unit: killing systemd-run here would leave it running
                    try: subprocess.run(["systemctl", "--user", "stop", unit], timeout=30, capture_output=True)
                    except (OSError, subprocess.SubprocessError): pass
                try: os.killpg(p.pid, signal.SIGTERM)
                except OSError: pass
        threading.Thread(target=hard, daemon=True).start()
        return {"ok": True}

    def act(self, d):
        what = d.get("do")
        if what == "start": return self.start(d.get("site"), d.get("url"))
        if what == "close": return self.close()
        if what == "done":   # any site: the person says it is signed in now
            if self.site != "web" or not self.proc or self.proc.poll() is not None: raise ValueError("no site sign-in is open")
            self.close(done=True); return self.view()
        if what == "forget": return self.forget(d.get("base"))
        if what == "input":
            t = d.get("t")
            if t not in self.INPUTS: raise ValueError("click, wheel, drag, text, key or back")
            msg = {"t": t}
            for k in ("fx", "fy", "dy"):
                if isinstance(d.get(k), (int, float)) and not isinstance(d[k], bool) and math.isfinite(d[k]): msg[k] = float(d[k])
            if t == "drag":   # the person's path while the mouse was down: up to 80 points of [fx, fy, ms since the last], under 5 s in all
                pts = d.get("pts")
                ok = isinstance(pts, list) and 2 <= len(pts) <= 80 and all(isinstance(q, list) and len(q) == 3 and all(isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v) for v in q) for q in pts)
                if not ok: raise ValueError("drag: 2 to 80 points of [x, y, ms]")
                msg["pts"] = [[min(1.0, max(0.0, float(x))), min(1.0, max(0.0, float(y))), min(200.0, max(0.0, float(ms)))] for x, y, ms in pts]
                if sum(q[2] for q in msg["pts"]) > 5000: raise ValueError("drag: under 5 seconds")
            if t == "text":
                if not isinstance(d.get("s"), str) or not 0 < len(d["s"]) <= 500: raise ValueError("text: 1 to 500 characters")
                msg["s"] = d["s"]
            if t == "key":
                if d.get("key") not in ("Enter", "Backspace", "Tab", "Escape", "Delete", "ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown", "Home", "End"): raise ValueError("no such key")
                msg["key"] = d["key"]
            self.touched = time.time(); self.tell(msg)
            return {"ok": True}
        raise ValueError("start, input, done, forget or close")

    def forget(self, base):
        """Sign out of a site here: take it off the list, then delete its own profile, cookies and all (tools/web_forget.mjs)."""
        log = self.web_log()
        if not isinstance(base, str) or base not in log: raise ValueError("that site isn't on the list")
        if self.site == "web" and self.proc and self.proc.poll() is None: raise ValueError("close the open sign-in first")
        self.web_mark(base, signed=False)   # first: `web` stops treating it as signed in even if the clearing below has to wait
        env = os.environ.get("HIVE_WEB_FORGET_CMD")
        node, tool = shutil.which("node") or "/usr/bin/node", str(Path(__file__).resolve().parent / "tools" / "web_forget.mjs")
        cmd = json.loads(env) + [base] if env else ["systemd-run", "--user", "--quiet", "--collect", "--pipe", "--wait", f"--unit=hive-web-forget-{int(time.time())}-{secrets.token_hex(3)}",
               "-p", "MemoryMax=1500M", "-p", "RuntimeMaxSec=150", f"--setenv=HOME={HOME}", f"--setenv=PATH={os.environ.get('PATH', '/usr/bin:/bin')}", node, tool, base]
        try: r = subprocess.run(cmd, capture_output=True, text=True, timeout=180, stdin=subprocess.DEVNULL)
        except (OSError, subprocess.SubprocessError) as e: return {**self.view(), "forgot": base, "cleared": False, "why": f"can't clear its cookies now: {e}"[:200]}
        last = (r.stdout.strip().splitlines() or [""])[-1]
        try: res = json.loads(last)
        except ValueError: res = {}
        if r.returncode == 0 and res.get("ok"): return {**self.view(), "forgot": base, "cleared": True, "removed": res.get("removed", 0)}
        return {**self.view(), "forgot": base, "cleared": False, "why": str(res.get("why") or "the clearing didn't finish")[:200]}

    def next_frame(self, after, wait=2.0):
        """The newest screen once it is newer than `after` (or after `wait` s, whatever there is): (seq, jpeg bytes)."""
        self.polled = time.time()
        with self.cond:
            self.cond.wait_for(lambda: self.seq > after or not self.proc, timeout=wait)
            self.polled = time.time()
            return self.seq, self.frame


SHOP = ShopSignin()


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
            "claude": bool(shutil.which("claude")), "other": other_account(),
            "person_stop": 100,   # the weekly line for the person's own posts: the fixed 100 in starter.login_for (not a setting; changing it is the warden's call)
            "fallback": bool(((jload(HOME / "core" / "config.json", {}) or {}).get("fallback") or {}).get("agents") or ((jload(HOME / "core" / "config.json", {}) or {}).get("fallback") or {}).get("chain"))}  # runs still start on another provider when no Claude login has room (core/config.json)


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
            "model": v.get("model") if v.get("model") in AGENT_MODELS else cfg().get("spawn_model") or "sonnet",  # what a new agent starts on (the starter's spawn word handling)
            "share": {k: sh.get(k) is True for k in SHARE_KEYS}, "setup_done": v.get("setup_done") is not False}


def install_id():
    """The report module's install id (reports/state.json beside settings.json), or None before it has made one."""
    v = (jload(SETTINGS.parent / "reports" / "state.json", {}) or {}).get("install")
    return v if isinstance(v, str) and re.fullmatch(r"[0-9a-f]{32}", v) else None


def settings_brief():
    v = settings_raw()
    return settings_norm(v) if v is not None else None


def default_model():
    """What a new agent with no model asked for starts on: Settings' choice, else config.json's spawn_model."""
    v = (settings_raw() or {}).get("model")
    return v if v in AGENT_MODELS else cfg().get("spawn_model") or "sonnet"


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


# B32 (projects/friction/REGISTER.md): Settings › Model from the owner's own outside sign-in too. /api/settings stays LOCAL_ONLY (power, sandbox,
# share); this route changes only the model a new agent starts on, by the warden's ruling (post 11250015705e, thread b32-default-model).
SETTINGS_LOG = STATE / "settings-log.jsonl"   # one line per change made here: {at, by, key, was, now}


def default_model_view():
    """What Settings › Model shows: the model, the choices, and where the choice is kept (settings.json where there is one, else config.json)."""
    return {"model": default_model(), "models": list(AGENT_MODELS), "saved_in": "settings.json" if SETTINGS.is_file() else "core/config.json"}


def set_default_model(model, by):
    """The model a new agent nobody asked a model for starts on: settings.json "model" on a Hive with that file (the starter's
    default_model), else core/config.json "spawn_model" (its spawn_model). Only a name from config.json "models": it ends up on a command line."""
    if not isinstance(model, str) or model not in AGENT_MODELS: raise ValueError("the model is " + ", ".join(AGENT_MODELS))
    if SETTINGS.is_file():
        with _settings_lock:
            cur = settings_raw()
            if cur is None: raise ValueError("settings.json can't be read")
            was = cur.get("model"); cur["model"] = model
            write_text_private(SETTINGS, json.dumps(cur, indent=1) + "\n")
        key = "settings.json model"
    else:
        with _cfg_lock:
            c = jload(HOME / "core" / "config.json")
            if not isinstance(c, dict): raise ValueError("core/config.json can't be read")
            was = c.get("spawn_model"); c["spawn_model"] = model
            t = HOME / "core" / f".config.json.{os.getpid()}.{threading.get_ident()}"
            t.write_text(json.dumps(c, indent=2) + "\n"); t.replace(HOME / "core" / "config.json")
        key = "config.json spawn_model"
    try:
        SETTINGS_LOG.parent.mkdir(parents=True, exist_ok=True)
        with open(SETTINGS_LOG, "a") as f: f.write(json.dumps({"at": now_iso(), "by": by or "local", "key": key, "was": was, "now": model}) + "\n")
    except OSError: pass
    return default_model_view()


# ---------- the person's own CLAUDE.md and MCP servers: shown at setup, copied only on a tick ----------
USER_INSTR = HOME / "user-instructions.md"  # the starter adds its text to every agent's instructions
MCP_COPY = HOME / "mcp.json"  # the ticked MCP servers; the starter passes it to claude with --mcp-config
def _person_home():
    try: return Path((HOME / ".hive-account").read_text().strip())  # --this-account installs: the Hive's home is a folder, the person's is this
    except (OSError, ValueError): return HOME


def import_view():
    """What the person has that agents don't: their ~/.claude/CLAUDE.md (offered) and the names of their MCP servers (shown; copying them isn't built)."""
    ph = _person_home()
    try: txt = (ph / ".claude" / "CLAUDE.md").read_text(errors="replace")
    except OSError: txt = ""
    mcp = sorted(_person_mcp(ph))
    try: got = sorted(json.loads(MCP_COPY.read_text()).get("mcpServers") or {})
    except (OSError, ValueError, AttributeError): got = []
    have = _person_mcp(ph)  # names of the env/headers keys only, never their values: ticking copies those values into mcp.json in clear
    secrets_ = {n: sorted(str(k) for key in ("env", "headers") if isinstance(have[n], dict) and isinstance(have[n].get(key), dict) for k in have[n][key]) for n in mcp}
    return {"claude_md": {"found": bool(txt.strip()), "chars": len(txt), "preview": txt.strip()[:400], "copied": USER_INSTR.is_file()}, "mcp": mcp, "mcp_copied": got,
            "mcp_secrets": {n: ks for n, ks in secrets_.items() if ks}}


def _person_mcp(ph):
    try: m = json.loads((ph / ".claude.json").read_text()).get("mcpServers") or {}
    except (OSError, ValueError, AttributeError): return {}
    return m if isinstance(m, dict) else {}


def do_import(d):
    """POST /api/import {"claude_md": true} and/or {"mcp": [names]}: copies the person's CLAUDE.md into the Hive's agents' instructions. {"claude_md": false} removes the copy. Nothing without that key."""
    if "mcp" in d:  # {"mcp": ["name", ...]}: exactly these servers are copied to the Hive's mcp.json; [] removes it
        names = d["mcp"]
        if not isinstance(names, list) or not all(isinstance(n, str) for n in names): raise ValueError("mcp is a list of server names")
        have = _person_mcp(_person_home())
        bad = [n for n in names if n not in have]
        if bad: raise ValueError("no such MCP server: " + ", ".join(bad))
        clash = [n for n in names if n in _mcp_urls()]
        if clash: raise ValueError("a connector added by address already has this name: " + ", ".join(clash))
        _write_mcp({n: have[n] for n in names})   # the connectors added by address stay
        if "claude_md" not in d: return import_view()
    if type(d.get("claude_md")) is not bool: raise ValueError("claude_md is true or false")
    if d["claude_md"]:
        try: txt = (_person_home() / ".claude" / "CLAUDE.md").read_text(errors="replace")
        except OSError: raise ValueError("there's no CLAUDE.md to copy")
        if not txt.strip(): raise ValueError("there's no CLAUDE.md to copy")
        write_text_private(USER_INSTR, txt)
    else:
        USER_INSTR.unlink(missing_ok=True)
    return import_view()


# ---------- connectors added by address (E120a): a remote MCP server by its URL, as Claude's custom connectors ----------
# The list lives in mcp-urls.json (name -> url, and a token if one was given); mcp.json, which the starter passes with --mcp-config,
# is always the person's ticked imports plus these. A token is written into mcp.json as a header (agents need it) and never shown back.
MCP_URLS = HOME / "mcp-urls.json"
MCP_URL_MAX = 20
MCP_LOOPBACK = {"127.0.0.1", "localhost", "::1"}
def _mcp_urls():
    try: v = json.loads(MCP_URLS.read_text())
    except (OSError, ValueError): return {}
    return {n: s for n, s in v.items() if isinstance(s, dict) and isinstance(s.get("url"), str)} if isinstance(v, dict) else {}


def _mcp_entry(s):
    e = {"type": "http", "url": s["url"]}
    if s.get("token"): e["headers"] = {"Authorization": "Bearer " + s["token"]}
    return e


def _write_mcp(imports=None, urls=None):
    """mcp.json = the ticked imports (None: those already in it) + the connectors added by address. Nothing left: no file."""
    urls = _mcp_urls() if urls is None else urls
    if imports is None:
        try: cur = json.loads(MCP_COPY.read_text()).get("mcpServers") or {}
        except (OSError, ValueError, AttributeError): cur = {}
        imports = {n: v for n, v in cur.items() if n not in urls and n not in _mcp_urls()} if isinstance(cur, dict) else {}
    servers = {**imports, **{n: _mcp_entry(s) for n, s in urls.items()}}
    if servers: write_text_private(MCP_COPY, json.dumps({"mcpServers": servers}, indent=1))
    else: MCP_COPY.unlink(missing_ok=True)


def mcp_url_view():
    def signed(s):
        o = s.get("oauth")
        if not o: return ""
        return "lapsed" if o.get("lapsed") or (o.get("expires") and o["expires"] < time.time() and not o.get("refresh")) else "in"
    urls = _mcp_urls(); have = {s["url"] for s in urls.values()}
    opn = {k["url"] for k in mcp_known() if not k["signin"]}   # a listed server known to need no sign-in shows no Sign in button
    return {"servers": [{"name": n, "url": s["url"], "token": bool(s.get("token")) and not s.get("oauth"), "signed": signed(s), "open": s["url"] in opn, "client": bool(s.get("client"))} for n, s in sorted(urls.items())], "max": MCP_URL_MAX,
            "known": [{**k, "added": k["url"] in have} for k in mcp_known()]}


# E132: a short list of official remote servers (mcp-known.json), so connecting one is pick it, then Sign in. Picking one is Add by address
# with the list's own name and address: the browser sends only the id, never an address the list didn't hold.
MCP_KNOWN = Path(__file__).resolve().parent / "mcp-known.json"
def mcp_known():
    v = jload(MCP_KNOWN, {}) or {}
    out = []
    for k in v.get("servers", []) if isinstance(v, dict) else []:
        if not isinstance(k, dict) or not all(isinstance(k.get(f), str) for f in ("id", "label", "url", "line")): continue
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,39}", k["id"]) or not k["url"].startswith("https://"): continue   # a bad edit there is dropped
        e = {f: k[f] for f in ("id", "label", "url", "line")} | {"signin": bool(k.get("signin"))}
        g = k.get("guide")   # E137: how to register an app by hand, for a server that doesn't register clients itself (E134, E135)
        if isinstance(g, dict) and isinstance(g.get("where"), str) and g["where"].startswith("https://") and isinstance(g.get("steps"), list) \
                and 0 < len(g["steps"]) <= 6 and all(isinstance(x, str) and x for x in g["steps"]):
            e["guide"] = {"where": g["where"], "steps": list(g["steps"])}
        sc = k.get("scope")   # E138: the scopes the sign-in asks for, narrower than all the server lists (from the maker's docs)
        if isinstance(sc, str) and 0 < len(sc) <= 2000 and re.fullmatch(r"[\x21-\x7e]+( [\x21-\x7e]+)*", sc): e["scope"] = sc
        out.append(e)
    return out


def _sandboxed():
    """The sandbox module's own rule (core/sandbox.py on()): a settings.json without "sandbox": "bare". No settings.json (Hive): never."""
    v = settings_raw()
    return v is not None and v.get("sandbox", "on") != "bare"


def mcp_url_check(url):
    """An https address with a host, or plain http on this computer only (a test server, and only with the sandbox off). No user:password inside it."""
    if not isinstance(url, str) or not url.strip(): raise ValueError("paste the server's address")
    url = url.strip()
    if len(url) > 500 or any(c.isspace() for c in url): raise ValueError("that address has spaces in it or is too long")
    try: u = urllib.parse.urlsplit(url); host = (u.hostname or "").lower(); u.port
    except ValueError: raise ValueError("that isn't a web address")
    if not host: raise ValueError("that isn't a web address")
    if u.username or u.password: raise ValueError("leave the sign-in out of the address; paste a token in its own box")
    if host in MCP_LOOPBACK and _sandboxed():   # E128: a boxed agent's network is the proxy's, which can't reach this machine (runs/e125)
        raise ValueError("agents run in the sandbox, which can't reach a server on this computer; use its https address")
    if u.scheme == "https": return url
    if u.scheme == "http" and host in MCP_LOOPBACK: return url
    raise ValueError("the address must start with https:// (plain http only for a server on this computer)")


def mcp_url_do(d):
    """POST /api/mcp-url {"do": "add", "name", "url", "token"?} or {"do": "remove", "name"}. Local only: it gives every agent new tools."""
    what, name = d.get("do"), d.get("name")
    urls = _mcp_urls()
    if what == "remove":
        if name not in urls: raise ValueError("no connector added by address has that name")
        urls.pop(name)
    elif what == "add":
        if d.get("known") is not None:
            k = next((k for k in mcp_known() if k["id"] == d.get("known")), None)
            if k is None: raise ValueError("that server isn't on the list")
            if any(s["url"] == k["url"] for s in urls.values()): raise ValueError("that server is already added")
            name, d = k["id"], {"url": k["url"]}
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,39}", name.strip()):
            raise ValueError("give it a short name: letters, digits, - and _ (up to 40)")
        name = name.strip()
        try: cur = json.loads(MCP_COPY.read_text()).get("mcpServers") or {}
        except (OSError, ValueError, AttributeError): cur = {}
        if name in urls or (name in cur) or name in _person_mcp(_person_home()): raise ValueError("a connector with that name is already here")
        if len(urls) >= MCP_URL_MAX: raise ValueError(f"{MCP_URL_MAX} is the most; remove one first")
        url = mcp_url_check(d.get("url"))
        tok = d.get("token") or ""
        if not isinstance(tok, str) or len(tok) > 4000 or any(c in tok for c in "\r\n"): raise ValueError("that token can't be used")
        urls[name] = {"url": url, **({"token": tok.strip()} if tok.strip() else {})}
    elif what == "client":   # E135: an app the person registered by hand at the server's maker, for a server that registers no app itself
        if name not in urls: raise ValueError("no connector added by address has that name")
        cid, sec = d.get("client_id") or "", d.get("client_secret") or ""
        if not isinstance(cid, str) or len(cid.strip()) > 500 or not re.fullmatch(r"[\x21-\x7e]*", cid.strip()): raise ValueError("that client ID can't be used")
        if not isinstance(sec, str) or len(sec.strip()) > 1000 or not re.fullmatch(r"[\x21-\x7e]*", sec.strip()): raise ValueError("that client secret can't be used")
        if cid.strip(): urls[name]["client"] = {"client_id": cid.strip(), **({"client_secret": sec.strip()} if sec.strip() else {})}
        else: urls[name].pop("client", None)
    else: raise ValueError("do: add or remove")
    _write_mcp(urls=urls)   # mcp.json first: if it can't be written, the list doesn't claim a connector agents don't have
    if urls: write_text_private(MCP_URLS, json.dumps(urls, indent=1))
    else: MCP_URLS.unlink(missing_ok=True)
    return mcp_url_view()



# ---------- sign-in for a connector added by address (E129): the MCP authorization flow, run by the dash ----------
# A server that answers 401 names its sign-in (RFC 9728 resource metadata -> RFC 8414 server metadata). The dash registers itself
# (RFC 7591), sends the person to sign in with PKCE, takes the code at /mcp-oauth/callback, and writes the access token into mcp.json as
# the same header a pasted token gets: a sandboxed agent then needs no browser and no callback of its own. It refreshes before expiry.
MCP_OAUTH_PENDING = {}   # state -> one sign-in under way, 10 minutes
_mcp_oauth_lock = threading.Lock()
MCP_OAUTH_PATH = "/mcp-oauth/callback"


def _mcp_http(url, data=None, form=None, headers=None, timeout=15):
    """(status, headers, parsed JSON or None). JSON body for data, urlencoded for form. Never follows a redirect to somewhere else silently."""
    import urllib.request, urllib.error
    hd = {"Accept": "application/json, text/event-stream", "User-Agent": "hive-dash", **(headers or {})}
    body = None
    if data is not None: body = json.dumps(data).encode(); hd["Content-Type"] = "application/json"
    if form is not None: body = urllib.parse.urlencode(form).encode(); hd["Content-Type"] = "application/x-www-form-urlencoded"
    req = urllib.request.Request(url, data=body, headers=hd, method="POST" if body is not None else "GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r: st, rh, raw = r.status, r.headers, r.read(200_000)
    except urllib.error.HTTPError as e: st, rh, raw = e.code, e.headers, e.read(200_000)
    except (OSError, ValueError) as e: raise ValueError(f"couldn't reach {urllib.parse.urlsplit(url).hostname}: {getattr(e, 'reason', e)}")
    try: v = json.loads(raw)
    except ValueError: v = None
    return st, rh, v


def _mcp_oauth_ok(url, server):
    """An endpoint from a server's metadata: https, or plain http on this computer only when the server itself is one (a test server)."""
    u = urllib.parse.urlsplit(url if isinstance(url, str) else "")
    if u.scheme == "https" and u.hostname: return url
    if u.scheme == "http" and (u.hostname or "") in MCP_LOOPBACK and urllib.parse.urlsplit(server).hostname in MCP_LOOPBACK: return url
    raise ValueError("its sign-in gave an address that isn't https")


def _mcp_discover(server):
    """The server's sign-in: {"authorize", "token", "register"?, "scope"?}. Raises ValueError with words the person can act on."""
    st, rh, _ = _mcp_http(server, data={"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "hive-dash", "version": "1"}}})
    if st >= 400 and st not in (401, 403): raise ValueError(f"the server answered {st}, not a sign-in request")
    www = (rh.get("WWW-Authenticate") or "") if st >= 400 else ""
    m = re.search(r'resource_metadata="([^"]+)"', www); scope = re.search(r'scope="([^"]+)"', www)
    u = urllib.parse.urlsplit(server); origin = f"{u.scheme}://{u.netloc}"; path = u.path.rstrip("/")
    prm = None
    for cand in ([m[1]] if m else []) + [origin + "/.well-known/oauth-protected-resource" + path, origin + "/.well-known/oauth-protected-resource"]:
        _st, _, v = _mcp_http(_mcp_oauth_ok(cand, server))
        if _st == 200 and isinstance(v, dict): prm = v; break
    # E137: Google's Gmail and Drive servers answer initialize without a token and ask only at a tool call (runs/e134/probe.md), so a
    # server that lets initialize through still needs a sign-in when it publishes resource metadata (RFC 9728) naming a sign-in server
    if st < 400 and not (isinstance((prm or {}).get("authorization_servers"), list) and prm["authorization_servers"]):
        raise ValueError("this server doesn't ask for a sign-in: agents can use it as it is")
    issuer = ((prm or {}).get("authorization_servers") or [origin])[0]
    iu = urllib.parse.urlsplit(_mcp_oauth_ok(issuer, server)); io = f"{iu.scheme}://{iu.netloc}"; ip = iu.path.rstrip("/")
    cands = [io + "/.well-known/oauth-authorization-server" + ip, io + "/.well-known/openid-configuration" + ip] + ([io + ip + "/.well-known/openid-configuration"] if ip else [])
    meta = None
    for cand in cands:
        _st, _, v = _mcp_http(cand)
        if _st == 200 and isinstance(v, dict) and v.get("authorization_endpoint") and v.get("token_endpoint"): meta = v; break
    if meta is None:
        if prm is not None: raise ValueError("its sign-in server doesn't describe itself, so this app can't sign in; paste a token instead")
        meta = {"authorization_endpoint": io + "/authorize", "token_endpoint": io + "/token", "registration_endpoint": io + "/register"}   # 2025-03-26 servers
    if "S256" not in (meta.get("code_challenge_methods_supported") or ["S256"]): raise ValueError("its sign-in doesn't offer PKCE, so this app won't use it")
    sc = scope[1] if scope else " ".join(x for x in ((prm or {}).get("scopes_supported") or []) if isinstance(x, str))
    out = {"authorize": _mcp_oauth_ok(meta["authorization_endpoint"], server), "token": _mcp_oauth_ok(meta["token_endpoint"], server), "scope": sc,
           "resource": (prm or {}).get("resource") or server}
    if meta.get("registration_endpoint"): out["register"] = _mcp_oauth_ok(meta["registration_endpoint"], server)
    return out


def _mcp_scope(url, listed):
    """E138: a known server's own narrower scope in place of every scope it lists (Gmail lists full mail access, Slack write scopes).
    Only scopes the server lists are kept when it lists any; an unknown server, or none left, keeps what it listed."""
    k = next((x for x in mcp_known() if x["url"] == url and x.get("scope")), None)
    if not k: return listed
    have = set(listed.split())
    keep = [x for x in k["scope"].split() if not have or x in have]
    return " ".join(keep) if keep else listed


def mcp_oauth_start(name, host):
    """POST /api/mcp-url {"do": "signin", "name"}: {"go": the sign-in page}. host is the dash's own Host, so the callback comes back here."""
    urls = _mcp_urls()
    if name not in urls: raise ValueError("no connector added by address has that name")
    s = urls[name]
    if s.get("token") and not s.get("oauth"): raise ValueError("it has a pasted token; remove it and add it again to sign in instead")
    if not isinstance(host, str) or not re.fullmatch(r"(127\.0\.0\.1|localhost|\[::1\]):\d{1,5}", host): raise ValueError("sign in from this computer's own address")
    redirect = f"http://{host}{MCP_OAUTH_PATH}"
    d = _mcp_discover(s["url"])
    d["scope"] = _mcp_scope(s["url"], d["scope"])
    old = s.get("oauth") or {}
    hand = s.get("client") or {}
    if isinstance(hand.get("client_id"), str) and hand["client_id"]: client = {k: hand[k] for k in ("client_id", "client_secret") if isinstance(hand.get(k), str) and hand[k]}
    elif old.get("client_id") and old.get("redirect") == redirect and old.get("token") == d["token"]: client = {k: old[k] for k in ("client_id", "client_secret") if old.get(k)}
    elif d.get("register"):
        st, _, v = _mcp_http(d["register"], data={"client_name": "Hive", "redirect_uris": [redirect], "grant_types": ["authorization_code", "refresh_token"],
                                                   "response_types": ["code"], "token_endpoint_auth_method": "none"})
        if st not in (200, 201) or not isinstance(v, dict) or not isinstance(v.get("client_id"), str): raise ValueError(f"the server wouldn't register this app ({st}); paste a token instead")
        client = {"client_id": v["client_id"], **({"client_secret": v["client_secret"]} if isinstance(v.get("client_secret"), str) else {})}
    else: return {"need_client": True, "redirect": redirect}   # E135: Settings asks for the client ID of an app the person registers there
    verifier = secrets.token_urlsafe(48); state = secrets.token_urlsafe(32)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    q = {"response_type": "code", "client_id": client["client_id"], "redirect_uri": redirect, "state": state, "code_challenge": challenge,
         "code_challenge_method": "S256", "resource": d["resource"], **({"scope": d["scope"]} if d["scope"] else {})}
    now = time.time()
    with _mcp_oauth_lock:
        for k in [k for k, v in MCP_OAUTH_PENDING.items() if v["until"] < now]: MCP_OAUTH_PENDING.pop(k)
        MCP_OAUTH_PENDING[state] = {"name": name, "url": s["url"], "verifier": verifier, "redirect": redirect, "until": now + 600, **client, **d}
    sep = "&" if "?" in d["authorize"] else "?"
    return {"go": d["authorize"] + sep + urllib.parse.urlencode(q)}


def _mcp_oauth_save(name, url, p, v):
    """A token answer into mcp-urls.json and mcp.json. False if the connector went or changed meanwhile."""
    if not isinstance(v, dict) or not isinstance(v.get("access_token"), str) or not v["access_token"] or any(c in v["access_token"] for c in "\r\n"):
        raise ValueError("the server's answer had no token")
    with _mcp_oauth_lock:
        urls = _mcp_urls()
        if name not in urls or urls[name]["url"] != url: return False
        old = urls[name].get("oauth") or {}
        exp = v.get("expires_in")
        urls[name]["token"] = v["access_token"]
        urls[name]["oauth"] = {"client_id": p["client_id"], **({"client_secret": p["client_secret"]} if p.get("client_secret") else {}), "redirect": p["redirect"],
                               "token": p["token"], "resource": p["resource"], "refresh": v.get("refresh_token") or old.get("refresh") or "",
                               "expires": int(time.time() + exp) if isinstance(exp, (int, float)) and exp > 0 else 0, "at": int(time.time())}
        _write_mcp(urls=urls); write_text_private(MCP_URLS, json.dumps(urls, indent=1))
    mcp_unmark_needs_auth([name])
    return True


def _claude_dirs():
    """The Claude config folders agents run with: each login in config.json, else ~/.claude (and this process's own CLAUDE_CONFIG_DIR)."""
    c = jload(HOME / "core" / "config.json", {}) or {}
    dirs = {str(Path(l).expanduser()) for l in c.get("logins") or [] if isinstance(l, str)} or {str(Path.home() / ".claude")}
    if os.environ.get("CLAUDE_CONFIG_DIR"): dirs.add(os.environ["CLAUDE_CONFIG_DIR"])
    return [Path(d) for d in sorted(dirs)]


def mcp_unmark_needs_auth(names):
    """The claude CLI keeps a server that once answered 401 in <config>/mcp-needs-auth-cache.json and skips it (its log says cached needs-auth),
    even once mcp.json gives it a token: so a signed-in connector is taken out of that list. A running CLI can write its old list back,
    so mcp_oauth_loop repeats this. Returns the files changed."""
    changed = []
    for d in _claude_dirs():
        f = d / "mcp-needs-auth-cache.json"
        try: v = json.loads(f.read_text())
        except (OSError, ValueError): continue
        if not isinstance(v, dict) or not any(n in v for n in names): continue
        for n in names: v.pop(n, None)
        try:
            tmp = f.with_name(f.name + f".{os.getpid()}.tmp"); tmp.write_text(json.dumps(v)); os.replace(tmp, f); changed.append(str(f))
        except OSError: pass
    return changed


def mcp_signed_in():
    """Names of the connectors with a live sign-in token."""
    return [n for n, s in _mcp_urls().items() if s.get("token") and (s.get("oauth") or {}) and not s["oauth"].get("lapsed")
            and not (s["oauth"].get("expires") and s["oauth"]["expires"] < time.time())]


def mcp_oauth_callback(q):
    """GET /mcp-oauth/callback?code&state. The state, made by a signed local POST and good once for 10 minutes, is the proof: the dash's
    SameSite=Strict cookie isn't sent on the way back from another site. Returns the words for the page."""
    state = (q.get("state") or [""])[0]
    with _mcp_oauth_lock: p = MCP_OAUTH_PENDING.pop(state, None) if state else None
    if not p or p["until"] < time.time(): return False, "That sign-in link is old or was already used. Start the sign-in again from Settings."
    if q.get("error"): return False, f"The server said no: {(q.get('error_description') or q['error'])[0][:200]}"
    code = (q.get("code") or [""])[0]
    if not code: return False, "The server sent no code back."
    form = {"grant_type": "authorization_code", "code": code, "redirect_uri": p["redirect"], "client_id": p["client_id"], "code_verifier": p["verifier"], "resource": p["resource"]}
    if p.get("client_secret"): form["client_secret"] = p["client_secret"]
    try:
        st, _, v = _mcp_http(p["token"], form=form)
        if st != 200: return False, f"The server wouldn't give a token ({st})."
        if not _mcp_oauth_save(p["name"], p["url"], p, v): return False, "That connector was removed or changed while you signed in."
    except ValueError as e: return False, str(e)
    return True, f"Signed in to {p['name']}. Agents can use it from their next run."


def mcp_oauth_refresh(margin=900):
    """Every connector whose sign-in ends within margin seconds and has a refresh token gets a new one. Returns the names refreshed."""
    done = []
    for name, s in _mcp_urls().items():
        o = s.get("oauth") or {}
        if not o.get("refresh") or not o.get("expires") or o["expires"] > time.time() + margin: continue
        form = {"grant_type": "refresh_token", "refresh_token": o["refresh"], "client_id": o["client_id"], "resource": o.get("resource") or s["url"]}
        if o.get("client_secret"): form["client_secret"] = o["client_secret"]
        try:
            st, _, v = _mcp_http(o["token"], form=form)
            if st == 200 and _mcp_oauth_save(name, s["url"], o, v): done.append(name)
            elif st in (400, 401):   # the refresh token is dead: the row says to sign in again
                with _mcp_oauth_lock:
                    urls = _mcp_urls()
                    if name in urls: urls[name].setdefault("oauth", {})["refresh"] = ""; urls[name]["oauth"]["expires"] = 0; urls[name]["oauth"]["lapsed"] = True; write_text_private(MCP_URLS, json.dumps(urls, indent=1))
        except (ValueError, KeyError): pass
    return done


def mcp_oauth_loop():
    n = 0
    while True:
        try:
            if n % 5 == 0: mcp_oauth_refresh()
            mcp_unmark_needs_auth(mcp_signed_in())
        except Exception: pass
        n += 1; time.sleep(60)


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


# ---- Buying (modules/buy; Settings > Buying). Signing the Hive in to Stripe Link, the delivery details, the buys list.
# Warden 112409231096: only the person's own signed-in app starts the sign-in (outside or tailnet, full: the Handler checks), the phrase and
# link live in memory here and go only to that page, the session token stays in link-cli's own file (only booleans are kept), and nothing
# here approves a buy: Link's app on the person's phone is the only go.
BUY_PY = HOME / "modules" / "buy" / "buy.py"
LINK_CLI = HOME / ".local" / "bin" / "link-cli"


# Orders (the Orders page): one card per order, from `hive order view --json`. Read only: no route changes a card, and nothing here pays.
ORDER_PY = HOME / "modules" / "buy" / "order.py"


def _shops():
    spec = importlib.util.spec_from_file_location("h2_shops", Path(__file__).resolve().with_name("shops.py")); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m
def orders_installed(): return ORDER_PY.is_file() and not ORDER_PY.is_symlink()


def orders_view():
    if not orders_installed(): return {"installed": False}
    try:
        r = subprocess.run([sys.executable, "-I", str(ORDER_PY), "view", "--json"], capture_output=True, text=True, timeout=20, stdin=subprocess.DEVNULL, env=buy_env())
        v = json.loads(r.stdout) if r.returncode == 0 else None
        if not isinstance(v, dict): return {"installed": True, "error": (r.stderr or r.stdout).strip()[-300:] or "order.py view failed"}
    except (OSError, subprocess.SubprocessError, ValueError) as e: return {"installed": True, "error": str(e)[:300]}
    return {**v, "installed": True}


def link_host_ok(url):
    """A sign-in link is only passed on if it is https on link.com or one of its subdomains."""
    try: p = urllib.parse.urlsplit(str(url))
    except ValueError: return False
    h = (p.hostname or "").lower()
    return p.scheme == "https" and not p.username and not p.password and (h == "link.com" or h.endswith(".link.com"))


def buy_env():
    """link-cli and buy.py as the person's own step: no agent name, no door."""
    return {k: v for k, v in os.environ.items() if k not in ("HIVE_NAME", "HIVE_DOOR")}


def link_json(out):
    """link-cli --format json prints one object, or a list of them when it polls: the last one counts."""
    try: j = json.loads(out)
    except ValueError: return None
    if isinstance(j, list): j = j[-1] if j else None
    return j if isinstance(j, dict) else None


class LinkSignIn:
    """`link-cli auth login`: Link gives a link and a phrase; the person opens the link, checks the phrase and approves in the Link app.
    login returns at once (it saves a pending device code); `auth status` then finishes it once approved. One at a time, 15 minutes."""
    def __init__(self): self.lock, self.slock, self.st, self.gen, self.signed, self.signed_t, self.buys, self.buys_t = threading.Lock(), threading.Lock(), {"state": "idle"}, 0, None, 0, None, 0

    def installed(self): return BUY_PY.is_file() and not BUY_PY.is_symlink()

    def status(self, fresh=False):
        """Signed in or not (a cached boolean, 30 s). Never keeps the token preview or anything else link-cli says."""
        with self.slock:   # one `auth status` at a time: it may be finishing the exchange and writing link-cli's token file
            if not fresh and self.signed is not None and time.time() - self.signed_t < 30: return self.signed
            return self._status()

    def _status(self):
        if not LINK_CLI.is_file(): self.signed = False
        else:
            try:
                r = subprocess.run([str(LINK_CLI), "auth", "status", "--format", "json"], capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL, env=buy_env())
                j = link_json(r.stdout) if r.returncode == 0 else None
                self.signed = bool(j and j.get("authenticated") is True)
            except (OSError, subprocess.SubprocessError, ValueError): self.signed = False
        self.signed_t = time.time()
        return self.signed

    def module_view(self):
        """buy.py view --json: the cap, the masked delivery summary and the buys. Read only (no sweep, no Link call); cached 5 s."""
        if self.buys is not None and time.time() - self.buys_t < 5: return self.buys
        try:
            r = subprocess.run([sys.executable, "-I", str(BUY_PY), "view", "--json"], capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL, env=buy_env())
            v = json.loads(r.stdout) if r.returncode == 0 else {"error": (r.stderr or r.stdout).strip()[-300:] or "buy.py view failed"}
        except (OSError, subprocess.SubprocessError, ValueError) as e: v = {"error": str(e)[:300]}
        self.buys, self.buys_t = v, time.time()
        return v

    def view(self, show=False):
        """show: this request is the person's own signed-in app (outside or tailnet, full), so the phrase and link may go to it."""
        if not self.installed(): return {"installed": False}
        with self.lock: st = dict(self.st)
        if not show: st.pop("phrase", None); st.pop("url", None)
        return {**self.module_view(), "installed": True, "here": bool(show), "link_cli": LINK_CLI.is_file(), "signed_in": self.status(), "signin": st}

    def start(self):
        if not self.installed(): raise ValueError("the buy module isn't installed (hive module add buy)")
        if not LINK_CLI.is_file(): raise ValueError("link-cli isn't installed for this account (npm install -g @stripe/link-cli)")
        with self.lock:
            if self.st.get("state") in ("starting", "waiting"): raise ValueError("a Link sign-in is already running")
            self.st = {"state": "starting"}; self.gen += 1; gen = self.gen
        if self.status(fresh=True):
            with self.lock: self.st = {"state": "idle"}
            raise ValueError("this Hive is already signed in to Link: sign out first to sign in again")
        try:
            r = subprocess.run([str(LINK_CLI), "auth", "login", "--client-name", "Hive", "--no-financial-insights", "--format", "json"],
                               capture_output=True, text=True, timeout=60, stdin=subprocess.DEVNULL, env=buy_env())
            j = link_json(r.stdout) if r.returncode == 0 else None
        except (OSError, subprocess.SubprocessError, ValueError): j = None   # ValueError: output that isn't UTF-8
        url, phrase = (j or {}).get("verification_url"), (j or {}).get("phrase")
        with self.lock:   # view() takes self.lock too (a plain Lock): decide here, answer after it's released
            ok = self.gen == gen and isinstance(url, str) and link_host_ok(url) and isinstance(phrase, str) and 0 < len(phrase) < 200
            if ok: self.st = {"state": "waiting", "url": url, "phrase": phrase, "until": int(time.time()) + 900}
            elif self.gen == gen: self.st = {"state": "failed", "error": "Link didn't give a sign-in link on link.com. Try again in a minute."}
        if ok: threading.Thread(target=self.watch, args=(gen,), daemon=True).start()
        return self.view(show=True)

    def watch(self, gen):
        """Asks Link every 5 s whether the person approved, for up to 15 minutes. Keeps only the yes/no."""
        end = time.time() + 900
        while time.time() < end:
            time.sleep(5)
            with self.lock:
                if self.gen != gen or self.st.get("state") != "waiting": return
            if self.status(fresh=True):
                with self.lock:
                    if self.gen == gen: self.st = {"state": "done"}
                return
        with self.lock:
            if self.gen == gen and self.st.get("state") == "waiting": self.st = {"state": "failed", "error": "The sign-in wasn't approved within 15 minutes. Try again."}

    def cancel(self):
        """Stops watching, and drops link-cli's pending code (logout) so a later `auth status` can't finish this sign-in. start() refused
        while signed in, so there is no session to lose."""
        with self.lock:
            self.gen += 1; was = self.st.get("state") == "waiting"
            if self.st.get("state") in ("starting", "waiting"): self.st = {"state": "cancelled"}
        if was and LINK_CLI.is_file():
            try: subprocess.run([str(LINK_CLI), "auth", "logout", "--format", "json"], capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL, env=buy_env())
            except (OSError, subprocess.SubprocessError): pass
            self.status(fresh=True)
        return self.view(show=True)

    def signout(self):
        if not LINK_CLI.is_file(): raise ValueError("link-cli isn't installed for this account")
        with self.lock: self.gen += 1; self.st = {"state": "idle"}
        try: subprocess.run([str(LINK_CLI), "auth", "logout", "--format", "json"], capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL, env=buy_env())
        except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"link-cli logout didn't run: {e}")
        if self.status(fresh=True): raise ValueError("Link still says this Hive is signed in")
        return self.view(show=True)

    def contact(self, d, clear=False):
        """The delivery details: buy.py contact writes ~/.hive/buy-contact.json (0600) and answers with the masked summary only."""
        if not self.installed(): raise ValueError("the buy module isn't installed (hive module add buy)")
        fields = {k: d[k] for k in ("name", "email", "phone", "line1", "line2", "city", "state", "postal_code", "country") if isinstance(d.get(k), str)} if not clear else {}
        if not clear and d.get("clear_line2") is True: fields["clear_line2"] = True
        try:
            r = subprocess.run([sys.executable, "-I", str(BUY_PY), "contact"] + (["--clear"] if clear else []), input=json.dumps(fields),
                               capture_output=True, text=True, timeout=30, env=buy_env())
        except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"buy.py didn't run: {e}")
        if r.returncode != 0: raise ValueError((r.stderr or r.stdout).strip().splitlines()[-1][:300] if (r.stderr or r.stdout).strip() else "couldn't save the details")
        self.buys = None
        return self.view(show=True)

    def renew(self, bid):
        """The card's Ask Link again: buy.py renew sends Link a fresh prompt for the same cart and total (it approves nothing; the
        Link app stays the only go). Allowed only for an ask whose prompt lapsed unanswered, within 24 h of the first ask."""
        if not self.installed(): raise ValueError("the buy module isn't installed (hive module add buy)")
        if not isinstance(bid, str) or not re.fullmatch(r"lsrq_[A-Za-z0-9]{1,64}", bid): raise ValueError("id: a Link spend request id (lsrq_...)")
        try:
            r = subprocess.run([sys.executable, "-I", str(BUY_PY), "renew", bid], capture_output=True, text=True, timeout=120, env=buy_env())
        except (OSError, subprocess.SubprocessError) as e: raise ValueError(f"buy.py didn't run: {e}")
        if r.returncode != 0: raise ValueError(((r.stderr or r.stdout).strip().splitlines() or ["couldn't ask Link again"])[-1].removeprefix("hive buy: ")[:300])
        self.buys = None
        m = re.match(r"(lsrq_\w+)\s+(\w+)", r.stdout.strip())
        return {"ok": True, "id": m.group(1) if m else None, "status": m.group(2) if m else None}


LINKIN = LinkSignIn()
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
    return {"settings": v, "sandbox_check": SANDBOX.get(), "share_text": SHARE_TEXT.get(), "accounts": accounts_view(), "github": GITHUB.get(), "import": import_view()}



LOGIN_PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:30rem;margin:4rem auto;padding:0 1rem;color-scheme:light dark}input{width:100%;font:inherit;padding:.4rem;box-sizing:border-box}button{font:inherit;margin-top:.6rem;padding:.3rem 1rem}code{font-size:.95em}#e{color:#c33}label{display:block;font-weight:600;margin-bottom:.2rem}</style></head>
<body><h1 style="font-size:1.3rem">This Hive needs its secret</h1><p>Run <code>hive web</code> on this machine, as the Hive's user, and open the address it prints.</p>
<form id="f"><label for="s">Secret</label><input name="username" autocomplete="username" value="hive" hidden>
<input id="s" name="password" type="password" placeholder="or paste the secret here" autocomplete="current-password" spellcheck="false" required><button id="b" type="submit">Open</button></form><p id="e" role="alert"></p>
<script>"use strict";
const go=async k=>{const r=await fetch("/login",{method:"POST",headers:{"Content-Type":"application/json","X-Hive-Dash":"1"},body:JSON.stringify({secret:k})});
 if(r.ok)location.replace("/");else document.getElementById("e").textContent="That isn't this Hive's secret. Run hive web again for the current address.";};
document.getElementById("f").addEventListener("submit",e=>{e.preventDefault();go(document.getElementById("s").value.trim());});
(async()=>{const k=location.hash.slice(1);if(/^[A-Za-z0-9_-]{32,}$/.test(k)){history.replaceState(null,"","/login");return go(k);}
 // the session cookie is SameSite=Strict, so a link from elsewhere arrives without it: this request has it, if there is one
 // Link's approval pop-up comes back here that way (/?linkback=1, modules/buy): close it, else carry the linkback on to the app
 const lb=new URLSearchParams(location.search).get("linkback")==="1";if(lb)try{window.close()}catch(e){}
 const r=await fetch("/api/app");if(r.ok)location.replace(lb?"/"+location.search:"/");})();
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


TUNNEL_SOCK_DIR = Path("/run/h2cf-sockets")  # root makes it (tmpfiles): this user, group h2cf-tunnel, 2770; the broker routes only to sockets here


def serve_tunnel_socket(path, handler):
    """web.json outside.tunnel_socket: the same way in as outside.socket, in the root-made directory the Cloudflare broker may
    route to (word-shell broker/README-install.md step 5), so its ledger can name the dash's socket. The directory is root's to
    make and shape: this never creates or chmods it, and refuses one that isn't this user's 2770 directory, a link, or a path
    outside it. The socket is 0660 and takes the directory's group (setgid), so the tunnel's group can connect and nobody else."""
    path = Path(path)
    if path.parent != TUNNEL_SOCK_DIR or not re.fullmatch(r"[a-z0-9-]{1,40}\.sock", path.name):
        raise ValueError(f"outside.tunnel_socket must be {TUNNEL_SOCK_DIR}/NAME.sock")
    st = os.lstat(TUNNEL_SOCK_DIR)
    if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.getuid() or stat.S_IMODE(st.st_mode) != 0o2770:
        raise ValueError(f"{TUNNEL_SOCK_DIR} isn't this user's 2770 directory")
    import socketserver
    class Srv(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
        daemon_threads = True
        request_queue_size = 256
        def get_request(self):
            conn, _ = self.socket.accept()
            return conn, ("unix", 0)  # outside, as on outside.socket: route_of reads this
    path.unlink(missing_ok=True)
    old = os.umask(0o117)
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
    global USAGE
    usage = USAGE = Usage()
    threading.Thread(target=usage.run, daemon=True).start()
    threading.Thread(target=phone_watch, daemon=True).start()
    threading.Thread(target=kb_index_warm, daemon=True).start()
    threading.Thread(target=sync_accounts, daemon=True).start()  # an account already signed in before the app first ran; does nothing without settings.json
    live_c, proj_c, usage_c, jobs_c = Cached(live, 1), CachedStale(projects, 60), Cached(usage.view, 2), Cached(Jobs().view, 2)
    deliv_c = Cached(delivered_view, 2.5)
    # Both run hundreds of git calls or the ladder's checks: seconds inside this busy process, so a page gets the last answer while a new one is read.
    web_c = Cached(web_stamp, 10)
    agent_costs = AgentCosts()
    subagents_c = Subagents()
    steps = Steps()  # the Ledger page's tool calls and words; the first look starts reading the session logs

    class H(BaseHTTPRequestHandler):
        def send_static(self, f, ct):
            """The page's own css and js: revalidated on every load (no-cache), but an unchanged file answers 304 with no body,
            so a reload through the tunnel doesn't download its 1.4 MB of modules again."""
            st = f.stat(); tag = '"%x-%x"' % (st.st_mtime_ns, st.st_size)
            lm = email.utils.formatdate(int(st.st_mtime), usegmt=True)   # Chrome won't revalidate an HTTP/1.0 answer by ETag, only by date
            inm, ims = self.headers.get("If-None-Match"), self.headers.get("If-Modified-Since")
            # Through the tunnel each revalidation is a round trip (152 files took the person 13 s on a reload, 2026-11-10), so the browser may reuse a file for a minute there.
            # The tab's own stamp check (web_stamp) tells it to reload after a change; on this machine every load still revalidates.
            cache = static_cache(getattr(self, "route", ""))
            if (tag in inm) if inm else ims == lm:
                self.send_response(304); self.send_header("ETag", tag); self.send_header("Last-Modified", lm); self.send_header("Cache-Control", cache); self.end_headers(); return
            self.send(f.read_bytes(), ct, cache=cache, etag=tag, modified=lm)   # private: only the browser keeps a copy, never the tunnel's edge

        def send(self, body, ct, status=200, cookie=None, framed_here=False, cache="no-store", etag=None, modified=None, gzipped=False):
            """body may be an iterator of chunks (tail_json, already gzipped when gzipped=True): written as they come, with no
            Content-Length; this server speaks HTTP/1.0, so the closed connection ends the answer."""
            stream = not isinstance(body, (bytes, bytearray))
            gz = gzipped or (not stream and len(body) > 4096 and "gzip" in self.headers.get("Accept-Encoding", ""))
            if gz and not stream: body = gzip.compress(body, 5)
            self.send_response(status)
            self.send_header("Content-Type", ct)
            if not stream: self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", cache)
            if etag: self.send_header("ETag", etag)
            if modified: self.send_header("Last-Modified", modified)
            if cookie: self.send_header("Set-Cookie", cookie)
            # Board text is written by agents: the page takes nothing from elsewhere and sends nothing anywhere.
            framing = "; frame-ancestors 'self'" if framed_here else "" if self.route == "open" else "; frame-ancestors 'none'" if not (self.route != "open" and "post" in self.can and word_shell()) else "; frame-ancestors " + " ".join(word_shell())  # the page can post as the person, so it can't sit in someone's frame; a TV page may sit in this site's own (the deck's single screen)
            frames = ""
            self.send_header("Content-Security-Policy", "default-src 'none'; connect-src 'self'; frame-src 'self'; manifest-src 'self'; worker-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'none'".replace("frame-src 'self'", "frame-src 'self'" + frames) + framing)
            if self.route != "open": self.send_header("Referrer-Policy", "no-referrer")
            if gz: self.send_header("Content-Encoding", "gzip")
            self.end_headers()
            for b in (body if stream else (body,)):
                if b: self.wfile.write(b)

        def send_file(self, path, view=False):
            """A post's file. Agents and Slack write these, so nothing here runs in the page's origin:
            sandboxed, no sniffing, and only plain images open in the tab.
            view=True opens an .html file as a page, which is how someone sees a draft website. Its scripts run in a sandbox with
            no origin (no cookies, no reading the dash) and no network at all, so the page has to carry its own styles and pictures."""
            m = re.fullmatch(r"/files/([0-9a-f]{12}|groups/[A-Za-z0-9_][A-Za-z0-9_-]*)/([A-Za-z0-9_][A-Za-z0-9_.-]*)", path)   # groups/<focus>/: a group's own files (E37b)
            f = HOME_FILES / m[1] / m[2] if m else None
            if not f or f.is_symlink() or f.parent.is_symlink() or not f.is_file() or not f.resolve().is_relative_to(HOME_FILES.resolve()): return self.send_error(404)
            ct = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}.get(f.suffix.lower())
            if view and f.suffix.lower() == ".svg": ct = "image/svg+xml"   # only ?view=1, for the preview's <img>; the sandbox CSP below stops its scripts if opened in a tab
            page = view and f.suffix.lower() in (".html", ".htm")
            if page: ct = "text/html; charset=utf-8"
            pdf = view and f.suffix.lower() == ".pdf"   # only ?view=1: the viewer frames it; the browser's own PDF viewer needs a CSP without "sandbox"
            if pdf: ct = "application/pdf"
            vid = (VIDEO_TYPES.get(f.suffix.lower()) or AUDIO_TYPES.get(f.suffix.lower())) if view else None   # only ?view=1: the preview plays it in a <video>/<audio>, with seeking (Range)
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
            self.send_header("Content-Security-Policy", PAGE_CSP if page else "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'" if pdf else "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:" if ct == "image/svg+xml" else "sandbox; default-src 'none'")
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

        def send_kb_page(self, f):
            """A project's html page, for the preview to frame (?view=1): redacted like any text, then served under the same sandbox as a page from a post (R111)."""
            if f.stat().st_size > KB_TEXT_MAX: return self.send_error(404)
            body = redact(f.read_text(errors="replace")).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Content-Disposition", "inline")
            self.send_header("Content-Security-Policy", PAGE_CSP)
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Referrer-Policy", "no-referrer")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(body)

        def send_kb_raw(self, rel, view=False):
            """An image or PDF for the Knowledge page. Text never comes this way, so it can't skip the redaction; nothing here runs in the page's origin."""
            f = kb_path(rel); ct = KB_IMAGE.get(f.suffix.lower()) if f else None
            if view and f and f.is_file() and f.suffix.lower() in (".html", ".htm"): return self.send_kb_page(f)
            if not f or not f.is_file() or not (ct or f.suffix.lower() == ".pdf") or f.stat().st_size > KB_RAW_MAX: return self.send_error(404)
            name = re.sub(r"[^A-Za-z0-9_.-]", "_", f.name)
            self.send_response(200)
            pdf_view = view and not ct   # a PDF shown in the preview panel (?view=1): inline, frameable by this site only
            self.send_header("Content-Type", "application/pdf" if pdf_view else ct or "application/octet-stream")
            self.send_header("Content-Length", str(f.stat().st_size))
            self.send_header("Content-Disposition", ("inline" if ct or pdf_view else "attachment") + f'; filename="{name}"')
            self.send_header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'" if pdf_view else "sandbox; default-src 'none'")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            with f.open("rb") as src: shutil.copyfileobj(src, self.wfile)

        route, can, signed, signer, mode, full_left, secrets_on, simple = "open", OPEN_CAN, False, None, None, 0, False, False
        email = ""   # the signed claim's email on the outside route: a door's people file is checked against it
        full_iat = signin_key = None

        def deny(self, code, why):
            self.send(json.dumps({"error": why}).encode(), "application/json", status=code)

        def admit(self, path, page=False):
            """Sets route, can and signed for this request, or answers it and returns False. Checked on every request."""
            web = web_cfg()
            if web is None: return True  # the open dash: no web.json here, as on a site that hasn't set one up
            self.route, self.can, self.signed, self.signer, self.simple, self.email = "refused", (), False, None, False, ""; self.own_site = False; self.full_iat = self.signin_key = None
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
                self.signer = signer_of(claims, out); self.email = str(claims.get("email") or "").strip().lower()
                self.own_site = out is (web.get("outside") or {})   # the main profile, not a site's own
                if not self.own_site: self.signer = f"{self.signer} at {site_key(hd.get('host'))}"[:80]  # a site's own profile: the post says which name it came in on
                # web.json "simple": sign-ins that open in simple mode until their browser says otherwise. Presentation only, so a bad value just means no.
                names = web.get("simple")
                self.simple = isinstance(names, list) and str(claims.get("email") or "").strip().lower() in {str(n).lower() for n in names}
                (self.can, self.mode, self.full_left), self.signed = allowed, True
                self.full_iat = claims.get("iat"); self.signin_key = hashlib.sha256(json.dumps([self.email, claims.get("iat"), claims.get("aud"), claims.get("sub")], default=str).encode()).hexdigest()
                # web.json "add_logins": sign-ins that may also sign the Hive in to its accounts, for someone who owns the account but shouldn't have full. Only ever adds that one cap; a bad value means no.
                names = web.get("add_logins")
                if isinstance(names, list) and "logins" not in self.can and str(claims.get("email") or "").strip().lower() in {str(n).lower() for n in names}: self.can = (*self.can, "logins")
            elif route == "tailnet":   # tailscale serve on :8446: one of the person's pinned devices (tailnet_person); no cookie is set or read here
                login = tailnet_person(list(self.headers.items()), peer_uid(self.client_address) if self.client_address[0] in ("127.0.0.1",) else None, web)
                if not login: self.deny(403, "this device isn't one of this app's tailnet devices"); return False
                self.can, self.signed, self.email = CAPS, True, login
                self.signer = f"{signer_of({'email': login}, web.get('tailnet'))} at tailnet"[:80]
                self.own_site = True
                self.signin_key = hashlib.sha256(f"tailnet|{login}".encode()).hexdigest()
            elif has_session(hd.get("cookie"), web, port):
                self.can, self.signed = CAPS, True; self.signin_key = "local"
            sites = (web.get("outside") or {}).get("sites")
            self.secrets_on = secrets_allowed(web, site=route == "outside" and isinstance(sites, dict) and site_key(hd.get("host")) in {site_key(k) for k in sites})
            if not self.secrets_on: self.can = tuple(c for c in self.can if c != "secrets")  # off unless web.json says so, or this is a consumer install; no route can set it
            return True

        def phone(self, u, n=None):
            """The phone module's call service (modules/phone, on its socket; phone_http), passed through: calls, rings, the person's speech."""
            if n is not None and not 0 <= n <= 20_000_000: return self.deny(413, "too big")
            body = self.rfile.read(n) if n else b""
            path = u.path[len("/api/phone"):] + (f"?{u.query}" if u.query else "")
            try: st, b = phone_http("POST" if n is not None else "GET", path, body if n is not None else None, self.headers.get("Content-Type") or "application/octet-stream")
            except OSError: return self.deny(503, "the phone service isn't running (systemctl --user start hive-phone)")
            return self.send(b, "audio/wav" if b[:4] == b"RIFF" else "application/json", status=st)   # /say answers with wav (Read aloud)

        def buy_person(self):
            """Buying's sign-in, sign-out and delivery details: only the person's own app, through Cloudflare (full) or a pinned tailnet device.
            Not the local cookie: agents on this machine can read it, so a curl from here must never start a Link sign-in (warden 112409231096)."""
            return "logins" in self.can and getattr(self, "route", None) in ("outside", "tailnet") and self.full_signin() and getattr(self, "own_site", False)   # not a site's own full person

        def full_signin(self):
            """The owner's full sign-in: outside in full mode, or the local session. The board page's bridge and the doors need it."""
            return (getattr(self, "route", None) == "outside" and getattr(self, "mode", None) == "full") or (getattr(self, "route", None) in ("local", "tailnet") and self.signed)

        def kb_wipe_may(self):
            """Who may wipe the knowledge base or put a copy back: a full sign-in with both the files and settings rights, on this
            Hive's own name (not a site's), from this user's connection (as Kill), from this page. None if allowed, else why not."""
            if not self.full_signin() or not {"files", "config"} <= set(self.can): return "this needs a full sign-in with the files and settings rights"
            if self.route == "outside" and not getattr(self, "own_site", False): return "this isn't offered on a site's own name"
            if not may_kill(os.getuid() if self.client_address[0] == "unix" else peer_uid(self.client_address)): return "this connection belongs to another user on the machine"
            if self.headers.get("Sec-Fetch-Site", "same-origin") != "same-origin": return "request from another site"
            return None

        def kb_fresh_left(self):
            """Seconds left before this sign-in is too old for a wipe: from outside, 15 minutes after the Access sign-in. None on the
            local route: its cookie is the secret and carries no time, so its age can't be told."""
            if self.route != "outside": return None
            iat = getattr(self, "full_iat", None)
            if not isinstance(iat, (int, float)) or isinstance(iat, bool): return 0
            return int(max(0, min(KB_FRESH_S, iat + KB_FRESH_S - time.time())))

        def kb_wipe_get(self, path):
            why = self.kb_wipe_may()
            if why: return self.deny(403, why)
            if path == "/api/kb/copies": return self.send(json.dumps({"copies": kb_copies(), "busy": KB_WIPE_LOCK.locked()}).encode(), "application/json")   # busy: one is running now, so the list isn't final (kbwipe.js waits)
            fresh = self.kb_fresh_left()
            plan = kb_wipe_plan(self.signin_key, issue=fresh != 0); plan["fresh_left"] = fresh
            if fresh == 0: plan["signin"] = True
            return self.send(json.dumps(plan).encode(), "application/json")

        def kb_wipe_post(self, path, d):
            """POST /api/kb/wipe {token, phrase, ticks} and /api/kb/putback {id, confirm}."""
            J = "application/json"
            why = self.kb_wipe_may()
            if why: return self.deny(403, why)
            by = "the person, from the dashboard" + (" (from outside)" if self.route == "outside" else " (from the tailnet)" if self.route == "tailnet" else "")
            ok = path == "/api/kb/wipe" and kb_take_token(d.get("token"), self.signin_key)   # used up whatever follows: a refused try shows the facts again
            if self.kb_fresh_left() == 0:   # put back too: from outside, both need a recent sign-in
                return self.send(json.dumps({"error": "this sign-in is more than 15 minutes old, so sign in again first", "signin": True}).encode(), J, status=403)
            if path == "/api/kb/wipe":
                if not ok: return self.deny(409, "the window was open for less than 15 seconds or more than 10 minutes, or was used already, so open it again")
                if d.get("phrase") != f"wipe {kb_wipe_name()}": return self.deny(400, "the typed words don't match")
                t = d.get("ticks")
                if not isinstance(t, dict) or set(t) != set(KB_TICKS) or any(t[k] is not True for k in KB_TICKS): return self.deny(400, "every box must be ticked")
            elif not isinstance(d.get("id"), str) or d.get("confirm") != f"put back {d['id']}": return self.deny(400, "the typed words don't match")
            if not KB_WIPE_LOCK.acquire(blocking=False): return self.deny(409, "a wipe or a put back is already running")
            try:
                STATE.mkdir(parents=True, exist_ok=True)
                with open(STATE / "kb-wipe.lock", "a") as lk:   # and across processes: a second dash, or a test's
                    try: fcntl.flock(lk, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    except OSError: return self.deny(409, "a wipe or a put back is already running")
                    kb_mend()   # a wipe the dash stopped in the middle of is settled first, so this one starts from records that are true
                    res = kb_wipe(by) if path == "/api/kb/wipe" else kb_put_back(d["id"], by)
            except ValueError as e: return self.deny(409, str(e))
            except OSError as e: return self.deny(409, f"couldn't take the lock for this ({e.strerror or e})")   # only the lock file can raise this: kb_wipe and kb_put_back raise ValueError before a move and nothing after one
            finally: KB_WIPE_LOCK.release()
            live_c.t = 0
            return self.send(json.dumps(res).encode(), J)

        def app_info(self):
            web = web_cfg() or {}
            info = {"route": self.route, "can": list(self.can), "app": self.route != "open", "brand": brand_view()}
            if self.route != "open": info.update(setup={**SETUP.get(), "starter": starter_up()}, secrets_on=self.secrets_on, settings=settings_brief())  # the starter heartbeat is one stat, so read it fresh: from the 20 s cache "isn't running" and "back" took ~35-43 s to show
            if self.route != "open" and "modules" in self.can: info["features"] = ["slack-door"]; info["slack_door"] = slack_door_view()  # Settings > Modules > slack-door "Set up" (POST /api/slack-door)
            if self.route in ("local", "tailnet"): info["phone_link"] = phone_qr(web)   # E122: the QR code in Settings › This device; not to an outside visitor, who may be a friend and needn't learn the other names
            if self.route != "open" and "logins" in self.can and orders_installed(): info["orders"] = True   # the Orders page (views/orders.js)
            if self.route != "open" and install_id(): info["install"] = install_id()   # the report module's install code, shown at the foot of Settings so the owner can match this Hive to its reports
            if self.route != "open": info["default_model"] = default_model(); info["voice"] = voice_ready(); info["phone"] = phone_ready() and "post" in self.can
            if self.route != "open" and "post" in self.can and word_shell(): info["word_shell"] = word_shell()
            if self.route != "open" and "post" in self.can and word_shell("word_card"): info["word_card"] = word_shell("word_card")   # root's approval card (k2992)
            if self.route != "open" and "post" in self.can and "files" in self.can and (self.route != "outside" or self.mode == "full"):
                ds = {x["page"]: n for n, x in doors().items() if door_for(n, self.route, self.email)}
                if ds: info["doors"] = ds   # project page -> door name: the preview relays that page's calls (preview/render.js)
            if self.full_signin() and {"files","config"} <= set(self.can) and (self.route == "local" or getattr(self,"own_site",False)): info["kb_wipe"] = True
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
            page = u.path in ("/", "/index.html", "/login", "/old", "/new", "/new/", "/call", "/call/", MCP_OAUTH_PATH) or display
            if not self.admit(u.path, page=page): return
            if u.path == "/api/proof":   # before sign-in on purpose: the asker has not sent the key yet, and learns nothing it could use as one
                n, w = (q.get("n") or [""])[0], web_cfg()
                if self.route != "local" or not w or not re.fullmatch(r"[A-Za-z0-9_-]{16,128}", n): return self.send_error(404)
                return self.send(proof_value(w, port, n).encode(), "text/plain; charset=utf-8")
            if u.path == MCP_OAUTH_PATH:   # E129: back from a connector's sign-in. No cookie comes on this hop (SameSite=Strict); the one-time state is the proof
                if self.route not in ("local", "open"): return self.send_error(403)
                ok, words = mcp_oauth_callback(q)
                page_ = ('<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta http-equiv="refresh" content="2;url=/#settings">'
                         f'<title>Sign-in</title><body style="font:16px system-ui;margin:3em auto;max-width:34em;padding:0 1em"><p>{words.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")}</p><p><a href="/#settings">Back to Settings</a></p>')
                return self.send(page_.encode(), "text/html; charset=utf-8")
            m = APP_FILES.fullmatch(u.path)
            if m:
                f = UI_DIR / ("icons/hive.ico" if m[1] == "favicon.ico" else m[1])   # /favicon.ico: browsers ask for it on pages without an icon link (sign-in, raw files); E56c
                if not f.is_file() or f.is_symlink(): return self.send_error(404)
                return self.send(f.read_bytes(), UI_CT.get(f.suffix, "text/javascript; charset=utf-8"), cache="no-cache")
            if self.route != "open":  # the open dash never asks; its reads and switches stay as they were
                if u.path == "/login" or (page and not self.signed):
                    return self.send(LOGIN_PAGE.encode(), "text/html; charset=utf-8")
                if not self.signed: return self.deny(401, "sign in: run hive web")
                need = GET_CAP.get(u.path) or ("read" if u.path.startswith("/files/") else "post" if u.path.startswith("/api/phone/") else None)
                if need and need not in self.can: return self.deny(403, f"this route doesn't allow {need}")
            if u.path in ("/api/kb/wipe", "/api/kb/copies"): return self.kb_wipe_get(u.path)
            if u.path.startswith("/api/phone/"): return self.phone(u)
            if display and shown[1] == "/dual-screen":
                self.send_response(302); self.send_header("Location", "/dual-screen/1" + (shown[2] or "")); self.send_header("Content-Length", "0"); self.end_headers(); return
            if display:
                return self.send((UI_DIR / "display.html").read_bytes(), "text/html; charset=utf-8", framed_here=True)
            if u.path == "/new":  # index.html's links are relative, so it needs the slash
                self.send_response(302); self.send_header("Location", "/new/"); self.send_header("Content-Length", "0"); self.end_headers(); return
            if u.path == "/call":
                self.send_response(302); self.send_header("Location", "/call/"); self.send_header("Content-Length", "0"); self.end_headers(); return
            if u.path == "/call/":  # the one-tap call page for a phone's home screen (web/call.html)
                return self.send((UI_DIR / "call.html").read_bytes(), "text/html; charset=utf-8")
            if u.path in ("/", "/index.html", "/new/"):
                return self.send(index_html(), "text/html; charset=utf-8")
            if u.path == "/old":  # the previous page, kept for now
                return self.send(UI.read_bytes(), "text/html; charset=utf-8")
            m = re.fullmatch(r"(?:/new)?/((?:css|js)(?:/[a-zA-Z0-9_-]+)*/[a-zA-Z0-9_-]+\.(?:js|css))", u.path)
            if m:
                f = UI_DIR / m[1]
                if f.is_symlink() or not f.is_file(): return self.send_error(404)
                return self.send_static(f, UI_CT[f.suffix])
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
            elif u.path in ("/api/target", "/api/inbox", "/api/away", "/api/config", "/api/modules", "/api/connectors", "/api/shops", "/api/voice", "/api/signin", "/api/plans", "/api/secrets", "/api/slack-door", "/api/settings", "/api/ntfy", "/api/webpush", "/api/update", "/api/signin-codex", "/api/buy", "/api/orders", "/api/accounts", "/api/design", "/api/shop-signin", "/api/shop-signin/frame", "/api/kb/home", "/api/kb/open", "/api/kb/search", "/api/kb/raw", "/api/kb/map", "/api/kb/history", "/api/mail", "/api/export") and self.route == "open":
                self.send_error(404)
            elif u.path == "/api/kb/home":
                self.send(json.dumps(kb_home()).encode(), J)
            elif u.path == "/api/kb/map":
                self.send(json.dumps(kb_map()).encode(), J)
            elif u.path == "/api/kb/history":
                self.send(json.dumps(kb_history()).encode(), J)
            elif u.path == "/api/call-chat":
                self.send(json.dumps(call_chat()).encode(), J)
            elif u.path == "/api/away":
                since = q.get("since", ["12h"])[0]
                self.send(json.dumps(core_json("away.py", "--json", "--since", since if re.fullmatch(r"\d{1,4}[hd]", since) else "12h") or {}).encode(), J)
            elif u.path == "/api/progress":
                self.send(json.dumps(PROGRESS.get(), default=str).encode(), J)
            elif u.path == "/api/kb/search":
                self.send(json.dumps(kb_search(q.get("q", [""])[0])).encode(), J)
            elif u.path == "/api/kb/open":
                v = kb_open(q.get("path", [""])[0])
                if v is None: return self.deny(404, "that isn't here, or isn't shown")
                self.send(json.dumps(v).encode(), J)
            elif u.path == "/api/kb/raw":
                self.send_kb_raw(q.get("path", [""])[0], q.get("view") == ["1"])
            elif u.path == "/api/mail":
                self.send(json.dumps(mail_view()).encode(), J)
            elif u.path in ("/api/docs", "/api/docs/doc", "/api/docs/page"):   # Documents (docs.py): the library's PDFs with the person's notes
                try:
                    if u.path == "/api/docs": self.send(json.dumps(docs_mod().documents()).encode(), J)
                    elif u.path == "/api/docs/doc": self.send(json.dumps(docs_mod().doc(q.get("id", [""])[0], q.get("rev", [""])[0] or None)).encode(), J)
                    else: self.send(docs_mod().page_png(q.get("id", [""])[0], q.get("rev", [""])[0], q.get("p", ["1"])[0]), "image/png", cache="private, max-age=86400")
                except KeyError: return self.deny(404, "no such document")
                except ValueError as e: return self.deny(400, str(e))
                except (OSError, subprocess.SubprocessError) as e: return self.deny(502, str(e))
            elif u.path == "/api/plans":
                self.send(json.dumps(plans_view()).encode(), J)
            elif u.path == "/api/secrets":
                self.send(json.dumps(secrets_view()).encode(), J)
            elif u.path == "/api/slack-door":
                self.send(json.dumps(slack_door_view()).encode(), J)
            elif u.path == "/api/target":
                self.send(json.dumps(thread_target(q.get("thread", [""])[0])).encode(), J)
            elif u.path == "/api/inbox":
                self.send(json.dumps(inbox_view()).encode(), J)
            elif u.path == "/api/checklist":
                self.send(CHECKLIST.read_bytes() if CHECKLIST.exists() else b'{"items": []}', J)
            elif u.path == "/api/ntfy":
                self.send(json.dumps(PHONE.view()).encode(), J)
            elif u.path == "/api/webpush":
                if self.route == "outside" and self.mode != "full": return self.deny(403, "push is for a full sign-in")
                self.send(json.dumps(webpush_view()).encode(), J)
            elif u.path == "/api/config":
                self.send(json.dumps(config_view()).encode(), J)
            elif u.path == "/api/export":
                if not EXPORT_LOCK.acquire(blocking=False): return self.send(json.dumps({"error": "a download of your chats is already being made; try again in a minute"}).encode(), J, status=429)
                try: t = tempfile.TemporaryFile(dir=STATE); export_zip(t); n = t.tell(); t.seek(0)
                except BaseException: EXPORT_LOCK.release(); raise
                with t:
                    EXPORT_LOCK.release()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/zip")
                    self.send_header("Content-Length", str(n))
                    self.send_header("Content-Disposition", f'attachment; filename="chats-{datetime.now().strftime("%Y-%m-%d")}.zip"')
                    self.send_header("Content-Security-Policy", "sandbox; default-src 'none'")
                    self.send_header("X-Content-Type-Options", "nosniff")
                    self.send_header("Cache-Control", "no-store")
                    self.end_headers()
                    shutil.copyfileobj(t, self.wfile)
            elif u.path == "/api/update":
                self.send(json.dumps(update_view()).encode(), J)
            elif u.path == "/api/modules":
                self.send(json.dumps(modules_view()).encode(), J)
            elif u.path == "/api/shops":   # read-only (shops.py): may start its own read-only sign-in checks in the background
                self.send(json.dumps(_shops().view()).encode(), J)
            elif u.path == "/api/connectors":   # read-only (CONNECTORS.md): there is no POST twin, and nothing here runs `conn`
                self.send(json.dumps(connectors_view(fresh=q.get("fresh") == ["1"], secrets="secrets" in self.can)).encode(), J)
            elif u.path == "/api/mcp-url":
                self.send(json.dumps(mcp_url_view()).encode(), J)
            elif u.path == "/api/voice":
                self.send(json.dumps(voice_view()).encode(), J)
            elif u.path == "/api/signin":
                self.send(json.dumps(SIGNIN.view()).encode(), J)
            elif u.path == "/api/settings":
                self.send(json.dumps(settings_page()).encode(), J)
            elif u.path == "/api/design":
                self.send(json.dumps(DESIGN.view()).encode(), J)
            elif u.path in ("/api/shop-signin", "/api/shop-signin/frame"):   # the person's own app only (buy_person): a local curl must never see or drive it (warden 11241330fa1c)
                if not self.buy_person(): return self.deny(403, "signing in to a shop is only from your own app, signed in")
                if u.path == "/api/shop-signin": return self.send(json.dumps(SHOP.view()).encode(), J)
                after = int(q["after"][0]) if q.get("after", [""])[0].isdigit() else 0
                seq, img = SHOP.next_frame(after)   # waits up to 2 s for a newer screen: the page asks again as each answer comes
                return self.send(json.dumps({**SHOP.view(), "seq": seq, "img": base64.b64encode(img).decode() if img and seq > after else None}).encode(), J)
            elif u.path == "/api/signin-codex":
                self.send(json.dumps(CODEX.view()).encode(), J)
            elif u.path == "/api/orders":
                self.send(json.dumps(orders_view()).encode(), J)
            elif u.path == "/api/buy":   # Settings > Buying; the phrase and link only to the person's own signed-in app (warden 112409231096)
                self.send(json.dumps(LINKIN.view(show=self.buy_person())).encode(), J)
            elif u.path == "/api/accounts":
                self.send(json.dumps(accounts_view()).encode(), J)
            elif u.path == "/api/live":
                lv = {**agents_for(live_c.get(), self.route != "outside" or self.mode == "full"), "web": web_c.get()}
                if self.route != "open": lv["starter"] = starter_up()  # read every 1.5 s with the rest: a stop or restart shows in ~2 s, not at /api/app's next 15 s read
                self.send(json.dumps(lv, default=str).encode(), J)
            elif u.path == "/api/drafts":   # E11b: polled every 0.1 s (E11d) by an open thread awaiting its reply, so the first words show without the 1.5 s live poll
                self.send(json.dumps(drafts_in((q.get("thread") or [""])[0], (q.get("steps") or [""])[0] == "1") if self.route != "outside" or self.mode == "full" else []).encode(), J)
            elif u.path == "/api/jobs":
                self.send(json.dumps(jobs_for(jobs_c.get(), self.route != "outside" or self.mode == "full"), default=str).encode(), J)
            elif u.path in ("/api/board", "/api/log"):
                num = lambda k: int(v) if (v := (q.get(k) or [""])[0]).isdigit() else None
                gz = "gzip" in self.headers.get("Accept-Encoding", "")
                self.send(tail_json(BOARD if u.path == "/api/board" else LOG, off, num("last") or 0, num("upto"), gz), J, gzipped=gz)
            elif u.path == "/api/approvals":
                self.send(json.dumps(approvals_list()).encode(), J)
            elif u.path == "/api/feedback-drafts":
                self.send(json.dumps(feedback_drafts_list()).encode(), J)
            elif u.path == "/api/delivered":
                self.send(json.dumps(deliv_c.get()).encode(), J)
            elif u.path == "/api/usage":
                self.send(json.dumps(usage_since(usage_c.get(), q)).encode(), J)
            elif u.path == "/api/work-tiers":
                try: self.send(json.dumps(work_tiers()).encode(), J)
                except (ValueError, OSError, subprocess.SubprocessError) as e: self.deny(502, str(e))
            elif u.path == "/api/agent-costs":
                self.send(json.dumps(agent_costs.view()).encode(), J)
            elif u.path == "/api/subagents":   # ?running=1: only the running ones, for the counts every page shows (the whole week is ~0.5 MB)
                v = subagents_c.view()
                if (q.get("running") or [""])[0] == "1": v = {**v, "rows": [r for r in v["rows"] if r.get("state") == "running"], "day": subagents_day(v["rows"])}
                self.send(json.dumps(v).encode(), J)
            elif u.path == "/api/share-preview":
                try: self.send(json.dumps(share_preview((q.get("thread") or [""])[0])).encode(), J)
                except KeyError: self.send_error(404)
            elif u.path == "/api/thread-names":
                self.send(json.dumps(thread_names()).encode(), J)
            elif u.path == "/api/thread-meta":
                self.send(json.dumps(thread_meta()).encode(), J)
            elif u.path == "/api/shown":
                self.send(json.dumps(shown_now()).encode(), J)
            elif u.path == "/api/thread-find":
                try:
                    self.send(json.dumps(thread_find().find((q.get("q") or [""])[0])).encode(), J)
                except Exception as e: self.send(json.dumps({"error": str(e)[:200]}).encode(), J)
            elif u.path == "/api/thread-shown":
                try: self.send(json.dumps(thread_shown((q.get("thread") or [""])[0])).encode(), J)
                except ValueError as e: self.send(json.dumps({"error": str(e)}).encode(), J)
            elif u.path == "/api/thread-status":
                self.send(json.dumps(thread_status()).encode(), J)
            elif u.path == "/api/foci":
                self.send(foci_c.get(), J)
            elif u.path == "/api/agent-status":
                self.send(json.dumps(agent_status()).encode(), J)
            elif u.path == "/api/ran":   # everyday E42: ?ids=A,B (with &at=T1,T2 as hints): the code each post's run ran, by post id
                ids, ats = q.get("ids", [""])[0].split(",")[:40], q.get("at", [""])[0].split(",")
                full = self.route != "outside" or self.mode == "full"
                hint = lambda i: float(ats[i]) if i < len(ats) and re.fullmatch(r"\d+(\.\d+)?", ats[i]) else None
                self.send(json.dumps({x: ran_for(x, at=hint(i)) if full else {"steps": []} for i, x in enumerate(ids) if x}).encode(), J)
            elif u.path == "/api/agent-work":
                self.send(json.dumps(agent_work(q.get("name", [""])[0], self.route != "outside" or self.mode == "full")).encode(), J)
            elif u.path == "/api/agent-input":
                self.send(json.dumps(agent_input(q.get("name", [""])[0], self.route != "outside" or self.mode == "full", turn=q.get("turn", [None])[0])).encode(), J)
            elif u.path == "/api/agent-diff":
                self.send(json.dumps(agent_diff(q.get("name", [""])[0], q.get("path", [""])[0], self.route != "outside" or self.mode == "full")).encode(), J)
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
            if u.path == "/share-in":   # a phone share the app's worker didn't catch (E118): open a new conversation, take nothing from the form
                self.send_response(303); self.send_header("Location", "/#comms/~new"); self.send_header("Content-Length", "0"); self.end_headers(); return
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
                need = POST_CAP.get(u.path) or ("post" if u.path.startswith("/api/phone/") else None)
                if not need or need not in self.can: return self.send_error(403)
            if u.path.startswith("/api/phone/"): return self.phone(u, n)
            if u.path == "/api/upload" and "total" in parse_qs(u.query):   # a big file in pieces
                q = parse_qs(u.query)
                if not 0 < n <= MAX_CHUNK: return self.deny(413, "a piece can be up to 64 MB")
                try: return self.send(json.dumps(save_chunk(q.get("name", ["file"])[0], q.get("id", [""])[0], int(q["off"][0]), int(q["total"][0]), n, self.rfile)).encode(), "application/json")
                except (ValueError, OSError, KeyError, IndexError) as e: return self.deny(400, str(e))
            if u.path == "/api/upload":
                if not 0 < n <= MAX_FILE: return self.deny(413, "a file can be up to 100 MB")
                try: return self.send(json.dumps(save_upload(parse_qs(u.query).get("name", ["file"])[0], n, self.rfile)).encode(), "application/json")
                except (ValueError, OSError) as e: return self.deny(400, str(e))
            if u.path == "/api/brand/logo":
                if not 0 < n <= LOGO_MAX: return self.deny(413, "a logo can be up to 2 MB")
                try: return self.send(json.dumps(set_logo(self.rfile.read(n))).encode(), "application/json")
                except (ValueError, OSError) as e: return self.deny(400, str(e))
            if not 0 < n < ({"/api/post": 2**20, "/api/feedback": 2**20, "/api/issue-link": 2**16, "/api/plan": 2**19, "/api/kb/save": 2**20, "/api/secret": 2**18, "/api/door": 2**18, "/api/focus": 2**15}.get(u.path) or (4096 if self.route != "open" else 1024)): return self.send_error(403)
            try: d = json.loads(self.rfile.read(n))
            except ValueError: return self.send_error(400)
            if not isinstance(d, dict): return self.send_error(400)
            num = lambda v: v is None or (isinstance(v, (int, float)) and not isinstance(v, bool))
            J = "application/json"
            if u.path == "/api/door":   # a page in the preview's locked frame reaching its own local service; the owner's full sign-in only
                if not ((self.route == "outside" and self.mode == "full") or (self.route == "local" and self.signed)): return self.deny(403, "a door is for a full sign-in")
                if isinstance(d.get("door"), str) and d["door"] in doors() and not door_for(d["door"], self.route, self.email): return self.deny(403, "this door is for its owner's sign-in")
                try: return self.send(door_call(d.get("door"), d.get("path"), d.get("body")), J)
                except KeyError: return self.send_error(404)
                except (ValueError, OSError) as e: return self.deny(502, str(e))
            if u.path == "/api/buy":   # Settings > Buying (modules/buy). Nothing here approves a buy: the Link app is the only go.
                if not self.buy_person(): return self.deny(403, "Link sign-in and the delivery details work only from your own signed-in app, through Cloudflare or Tailscale, in full mode")
                try:
                    do = d.get("do")
                    if do == "signin": res = LINKIN.start()
                    elif do == "cancel": res = LINKIN.cancel()
                    elif do == "signout": res = LINKIN.signout()
                    elif do == "contact": res = LINKIN.contact(d.get("contact") if isinstance(d.get("contact"), dict) else {})
                    elif do == "contact_clear": res = LINKIN.contact({}, clear=True)
                    elif do == "renew": res = LINKIN.renew(d.get("id"))   # Ask Link again on a lapsed card: a new prompt, same cart; nothing approved here
                    else: raise ValueError("do: signin, cancel, signout, contact, contact_clear or renew")
                except ValueError as e: return self.deny(400, str(e))
                return self.send(json.dumps(res).encode(), J)
            if u.path == "/api/kb/save":   # the file preview's Edit tab (web/js/preview/edit.js): a note or project text file, written back
                try: return self.send(json.dumps(kb_save(d.get("path"), d.get("text"), d.get("version"), self.signer)).encode(), J)
                except KeyError: return self.deny(404, "that isn't here, or isn't shown")
                except ValueError as e: return self.deny(409 if str(e).startswith("this file changed") else 400, str(e))
                except OSError as e: return self.deny(500, "couldn't write the file: " + (e.strerror or "error"))
            if u.path == "/api/docs/notes":
                try: return self.send(json.dumps(docs_mod().notes_do(d)).encode(), J)
                except KeyError as e: return self.deny(404, str(e))
                except (ValueError, TypeError) as e: return self.deny(400, str(e))
            if u.path == "/api/work-tiers":
                try: return self.send(json.dumps(work_tier_change(d)).encode(), J)
                except ValueError as e: return self.deny(400, str(e))
                except (OSError, subprocess.SubprocessError) as e: return self.deny(502, str(e))
            if u.path == "/api/share-build":
                try: return self.send(json.dumps(share_build(d)).encode(), J)
                except KeyError: return self.send_error(404)
                except ValueError as e: return self.deny(400, str(e))
            if u.path in ("/api/post", "/api/side-ask", "/api/thread-meta", "/api/shown", "/api/loadtime", "/api/opened", "/api/checklist", "/api/focus", "/api/feedback", "/api/feedback-draft-done", "/api/issue-link", "/api/update", "/api/inbox-read", "/api/ntfy", "/api/webpush", "/api/agent-model", "/api/agent-effort", "/api/config", "/api/default-model", "/api/starter", "/api/module", "/api/voice", "/api/signin", "/api/signin/code", "/api/signin/cancel", "/api/access", "/api/plan", "/api/secret", "/api/settings", "/api/share-copy", "/api/share-pack", "/api/draft", "/api/import", "/api/mcp-url", "/api/signin-codex", "/api/account-key", "/api/design", "/api/shop-signin", "/api/brand", "/api/slack-door"):
                try:
                    if u.path == "/api/post" and isinstance(d.get("page"), dict): res = page_edit_post(d.get("page"), d.get("thread"), limited="config" not in self.can)
                    elif u.path == "/api/post": res = person_post(d.get("text"), d.get("thread"), d.get("reply_to"), d.get("files"), signer=self.signer, limited="config" not in self.can, simple=d.get("simple") is True, model=d.get("model"), effort=d.get("effort"), sig=d.get("sig"), interrupt=d.get("interrupt") is True, edit_of=d.get("edit_of"), retry_of=d.get("retry_of"), files_from=d.get("files_from"), focus=d.get("focus"))
                    elif u.path == "/api/side-ask": res = side_ask(d.get("q"), signer=self.signer, limited="config" not in self.can)
                    elif u.path == "/api/thread-meta": res = set_thread_meta(d)
                    elif u.path == "/api/shown": res = dismiss_shown(d)
                    elif u.path == "/api/loadtime": res = record_loadtime(d, self.route)
                    elif u.path == "/api/opened": res = record_opened(d, self.route)
                    elif u.path == "/api/checklist": res = checklist_do(d)
                    elif u.path == "/api/focus": res = focus_do(d, full="config" in self.can, driven="Headless" in (self.headers.get("User-Agent") or ""))
                    elif u.path == "/api/feedback": res = send_feedback(d)
                    elif u.path == "/api/feedback-draft-done": res = feedback_draft_done(d)
                    elif u.path == "/api/issue-link": res = issue_link(d)
                    elif u.path == "/api/update": res = update_do(d.get("do"))
                    elif u.path == "/api/plan": res = save_plan(d.get("name"), d.get("text"), d.get("version"), bool(d.get("start")), self.signer)
                    elif u.path == "/api/secret": res = set_secret(d.get("name"), d.get("value"), self.signer, delete=bool(d.get("delete")))
                    elif u.path == "/api/agent-model": res = set_agent_model(d.get("name"), d.get("model"))
                    elif u.path == "/api/agent-effort": res = set_agent_effort(d.get("name"), d.get("effort"), d.get("restart") is True, signer=self.signer, limited="config" not in self.can)
                    elif u.path == "/api/inbox-read": res = {"marked": mark_read(None if d.get("all") else [str(i) for i in d.get("ids") or []])}
                    elif u.path == "/api/ntfy": res = phone_do(d.get("do"), self.headers)
                    elif u.path == "/api/webpush":
                        if self.route == "outside" and self.mode != "full": return self.deny(403, "push is for a full sign-in")
                        res = webpush_do(d, self.headers)
                    elif u.path == "/api/config": res = set_config(d)
                    elif u.path == "/api/default-model": res = set_default_model(d.get("model"), self.signer)
                    elif u.path == "/api/brand": res = set_brand(d)
                    elif u.path == "/api/starter": starter_do(d.get("do")); SETUP.t = 0; res = {"ok": True}
                    elif u.path == "/api/module": res = {"said": module_do(d.get("do"), d.get("name"))}
                    elif u.path == "/api/voice":
                        if d.get("do") != "retry": raise ValueError("do: retry")
                        res = voice_retry()
                    elif u.path == "/api/slack-door": res = slack_door_disconnect(self.signer) if d.get("do") == "disconnect" else slack_door_connect(d, self.signer)
                    elif u.path == "/api/signin": res = SIGNIN.start(d.get("name"), bool(d.get("console")))
                    elif u.path == "/api/signin/code": SIGNIN.code(d.get("code")); res = {"ok": True}
                    elif u.path == "/api/signin/cancel": SIGNIN.cancel(); res = {"ok": True}
                    elif u.path == "/api/settings": res = set_settings(d)
                    elif u.path == "/api/import": res = do_import(d)
                    elif u.path == "/api/mcp-url": res = mcp_oauth_start(d.get("name"), self.headers.get("Host")) if d.get("do") == "signin" else mcp_url_do(d)
                    elif u.path == "/api/share-copy": res = share_copy()
                    elif u.path == "/api/share-pack": res = share_pack()
                    elif u.path == "/api/draft": res = draft_do(d)
                    elif u.path == "/api/design": res = DESIGN.act(d)
                    elif u.path == "/api/shop-signin":
                        if not self.buy_person(): return self.deny(403, "signing in to a shop is only from your own app, signed in")
                        res = SHOP.act(d)
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
            if u.path in ("/api/kb/wipe", "/api/kb/putback"): return self.kb_wipe_post(u.path, d)
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
        def log_error(self, fmt, *a):  # keep 5xx and timeouts in the journal, so a failed page load leaves a trace; 4xx refusals stay quiet
            if a and isinstance(a[0], int) and a[0] < 500: return
            try: sys.stderr.write("hive-dash error %s %s: %s\n" % (self.address_string(), getattr(self, "path", "?")[:120], fmt % a)); sys.stderr.flush()
            except Exception: pass

    threading.Thread(target=live_usage, daemon=True).start()
    threading.Thread(target=record_usage, daemon=True).start()
    threading.Thread(target=provider_status, daemon=True).start()
    steps.busy = True; threading.Thread(target=steps.refresh, daemon=True).start()  # read the session logs now, so the first History open after a restart isn't the cold one
    def warm_progress():  # warm the Progress page's cache (progress.py takes tens of seconds); after the Steps scan, so the two don't fight over the page cache under the service's memory cap
        for _ in range(300):
            if not steps.busy: break
            time.sleep(1)
        PROGRESS.get()
    threading.Thread(target=warm_progress, daemon=True).start()
    threading.Thread(target=lambda: (proj_c.get()), daemon=True).start()
    threading.Thread(target=mcp_oauth_loop, daemon=True).start()  # E129: signed-in connectors get a fresh token before theirs ends
    threading.Thread(target=SETUP.get, daemon=True).start()  # so the first page after a restart doesn't pay for it
    threading.Thread(target=kb_mend_start, daemon=True).start()  # a wipe this dash was stopped in the middle of: its record and its line on the board
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)  # loopback only: never opens the machine
    srv.daemon_threads = True
    cfg = web_cfg() or {}; sock = (cfg.get("outside") or {}).get("socket")
    # Only on this install's own port. A test copy on another port shares the home and so the config: it would take the tunnel's socket and leave it dead when it stopped (2026-11-03).
    if sock and port == int(cfg.get("port") or 8077): threading.Thread(target=serve_socket(Path(sock).expanduser(), H).serve_forever, daemon=True).start()
    tsock = (cfg.get("outside") or {}).get("tunnel_socket")
    if sock and tsock and port == int(cfg.get("port") or 8077):  # only beside outside.socket, so TCP stays refused to outside names
        try: threading.Thread(target=serve_tunnel_socket(tsock, H).serve_forever, daemon=True).start()
        except (OSError, ValueError) as e: print(f"hive-dash: outside.tunnel_socket not served: {e}", flush=True)
    print(f"hive-dash on http://127.0.0.1:{port}/  (from elsewhere: ssh -L {port}:localhost:{port} <this-machine>)", flush=True)
    srv.serve_forever()


def main(argv):
    if "-h" in argv or "--help" in argv: print(__doc__); return
    if argv and argv[0] == "sync-accounts":  # `hive setup`'s Finish: the same as the app's, so a Codex login made in a terminal reaches the starter
        if not SETTINGS.is_file(): print("This Hive has no settings file, so accounts are set by hand in core/config.json."); return
        sync_accounts(); return
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
