#!/usr/bin/env python3
"""Is a change live? A post that says "live" or "deployed" is checked against what is actually running, not against the file on disk.

Thread friction-chase, 2026-11-25: the person kept finding work that was said to be done but wasn't live. A .py edit to a running
service does nothing until that service restarts. A dash web file is served from disk, but a tab already open keeps its old modules,
and the tunnel lets a browser reuse css and js for 60 s. Until now the only check was livecheck.py, which tests that an edited file
still parses. It doesn't test whether the edit is the code that runs.

  served.py check PATH...         for each file: live, fresh (runs anew each call), restart needed, differs, unserved or unknown
                                  (exit 1 if any is restart, differs, unserved or missing; 2 if the worst is unknown)
  served.py claims [--since 6h]   posts that say a change is live: check the files they name
  (hive post runs on_post on every agent post that names a file and says live, deployed or fixed, and warns the writer)

How a file is checked:
  - a dash web file (modules/dash/web/...): fetched from the local dash with a cache-busting query and `Cache-Control: no-cache`, signed
    in the way a local browser is, then its sha256 compared to the file on disk. index.html is rewritten on the way out, so only its
    status is checked.
  - a .py file that a long-running user service runs or imports from its own folder (found by following its imports): live only if
    the service started after the file last changed, otherwise "restart needed".
  - the script of a timer or one-shot unit: it runs fresh each time, so the next run uses it; but a one-shot still on a run that started
    before the change (the release train runs for hours) holds the old copy: "restart".
  - a process of this user, found in /proc, that runs a .py/.sh by path or `-m` (under heavy's run-*.scope, in tmux, behind a wrapper)
    and imports the file: "live" if it started after the change, "restart" if it started before and has run over 10 minutes.
  - nothing holds it, but a Dockerfile or compose file sits above it (below home): "unknown", since a container's processes aren't ours.
  - anything else (a CLI, a test): no running service holds it, so the next call runs the new copy.
"""
import argparse, ast, datetime as dt, hashlib, hmac, json, os, re, subprocess, sys, time, urllib.error, urllib.request
from pathlib import Path

HOME = Path.home()
DASH = HOME / "modules/dash"; DASH_WEB = DASH / "web"; WEB_JSON = HOME / ".hive/web.json"
TUNNEL_S = 60   # dash.static_cache: through the tunnel a browser may reuse css/js this long
LIVE_CLAIM = re.compile(r"\b(live (now|since|at|on)|is (now )?live|now live|went live|deployed|fixed|in production|shipped to the dash|restarted (the )?(dash|starter|service))\b", re.I)
PATH = re.compile(r"(?<![\w/.~-])(?:~/)?((?:core|modules|projects)/[\w./-]+\.(?:py|js|mjs|css|html|sh))\b")


def run(cmd, timeout=10):
    try: return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout).stdout
    except (OSError, subprocess.SubprocessError): return ""


def units():
    """User services: [{id, type, active, started (unix or 0), argv: [...], timer: bool, cwd, main: MainPID or 0}]."""
    names = [l.split()[0] for l in run(["systemctl", "--user", "list-units", "--type=service", "--all", "--no-legend", "--plain"]).splitlines() if l.split()]
    if not names: return []
    out, cur = [], {}
    raw = run(["systemctl", "--user", "show", "--timestamp=unix", "-p", "Id,Type,ActiveState,ExecMainStartTimestamp,ExecStart,TriggeredBy,WorkingDirectory,MainPID", *names], 20)
    for line in raw.splitlines() + [""]:
        if not line.strip():
            if cur.get("Id"): out.append(cur)
            cur = {}; continue
        k, _, v = line.partition("="); cur[k] = v
    res = []
    for u in out:
        m = re.search(r"argv\[\]=([^;]*)", u.get("ExecStart", ""))
        st = re.match(r"@(\d+)", u.get("ExecMainStartTimestamp", ""))
        res.append({"id": u["Id"], "type": u.get("Type", ""), "active": u.get("ActiveState", ""), "started": int(st[1]) if st else 0,
                    "argv": (m[1].split() if m else []), "timer": is_timer(u.get("TriggeredBy", "")), "cwd": u.get("WorkingDirectory", "").lstrip("!-") or str(HOME),
                    "main": int(u.get("MainPID") or 0) if str(u.get("MainPID", "")).isdigit() else 0})
    return res


def is_timer(triggered_by):
    """A unit a .timer starts runs anew on each tick. A .path or .socket trigger only starts it: hive-slack-mirror, slack-door and
    hive-dash-tunnel are .path-started and then run for days, holding the code they started with (review a0ad9, 1125010056b9)."""
    return any(t.endswith(".timer") for t in str(triggered_by).split())


def local_imports(script, seen=None):
    """script and every .py in its own folder that it imports, followed through those files: what a restart reloads."""
    script = Path(script).resolve(); seen = set() if seen is None else seen
    if script in seen or not script.is_file(): return seen
    seen.add(script)
    for f in _deps(script): local_imports(f, seen)
    return seen


IMPORT = re.compile(r"(?:^|[;:])[ \t]*(?:import[ \t]+([\w., \t]+)|from[ \t]+(\w+)[\w.]*[ \t]+import\b)", re.M)
_DEPS = {}

def _deps(script):
    """The files one script loads directly (see local_imports), cached by mtime. Imports are read by a line regex; only a file that names a
    path loader is parsed: walking the tree of every script a process runs took 17 s a check."""
    try: key = (script, script.stat().st_mtime_ns); text = script.read_text(errors="replace")
    except OSError: return []
    if key in _DEPS: return _DEPS[key]
    out = []
    for m in IMPORT.finditer(text):
        for name in ([x.split()[0] for x in m[1].split(",") if x.split()] if m[1] else [m[2]]):
            f = script.parent / (name.split(".")[0] + ".py")
            if f.is_file(): out.append(f)
    if any(l in text for l in LOADERS):
        try: tree = ast.parse(text)
        except (SyntaxError, ValueError): tree = None
        for n in ast.walk(tree) if tree else ():
            if isinstance(n, ast.Call) and (getattr(n.func, "attr", None) or getattr(n.func, "id", None)) in LOADERS: out += loaded_paths(n, script)
    if script.name == "starter.py": out += config_impls(script.parent / "config.json")
    _DEPS[key] = out
    return out


def config_impls(cfg):
    """The provider modules the starter loads by the path its config.json names (providers.<name>.impl, starter.py provider_room):
    a path read from config, which no reading of starter.py itself can find."""
    try: ps = json.loads(Path(cfg).read_text()).get("providers") or {}
    except (OSError, ValueError, AttributeError): return []
    fs = {Path(str(pc.get("impl"))).expanduser() for pc in ps.values() if isinstance(pc, dict) and pc.get("impl")}
    return [f for f in fs if f.is_absolute() and f.is_file()]


LOADERS = {"spec_from_file_location", "SourceFileLoader", "run_path"}   # a module loaded by its path stays loaded as surely as an import

def loaded_paths(call, script):
    """The .py files a spec_from_file_location(...) call names: `Path(__file__).with_name("x.py")` (beside the script) or a chain like
    `HOME / "modules" / "providers" / "providers.py"` (under home). A path held in a variable is not followed; the paths in the starter's config are (config_impls)."""
    out = []
    def parts(e):
        if isinstance(e, ast.BinOp) and isinstance(e.op, ast.Div): return parts(e.left) + parts(e.right)
        return [e.value] if isinstance(e, ast.Constant) and isinstance(e.value, str) else [None]
    for a in call.args[:2]:
        for e in ast.walk(a):
            if isinstance(e, ast.BinOp) and isinstance(e.op, ast.Div):
                ps = [x for x in parts(e) if x]
                if ps and ps[-1].endswith(".py"): out += [HOME.joinpath(*ps), script.parent.joinpath(*ps)]
            elif isinstance(e, ast.Constant) and isinstance(e.value, str) and e.value.endswith(".py"):
                out += [script.parent / e.value, Path(e.value).expanduser()]
    return [f for f in out if f.is_absolute() and f.is_file()]


def script_of(u):
    """The .py or .sh a unit runs, if any: an absolute path, or one relative to its WorkingDirectory."""
    return script_in(u["argv"][1:] if u["argv"] else [], u.get("cwd") or HOME)


def script_in(argv, cwd):
    """The .py/.sh an argv runs (`python3 -I x.py`, `bash run.sh`, `python3 -m pkg`), resolved against cwd, or None."""
    for i, a in enumerate(argv):
        if a == "-m" and i + 1 < len(argv):
            mod = Path(cwd, *argv[i + 1].split("."))
            return next((f for f in (mod.with_suffix(".py"), mod / "__main__.py") if f.is_file()), None)
        if a.endswith((".py", ".sh")) and not a.startswith("-"):
            f = Path(cwd) / a
            return f if f.is_file() else None
    return None


OLD_RUN_S = 600   # a process that started before the edit this long ago is a run that holds the old copy, not a CLI about to exit
CLI_S = 60        # a process younger than this is a CLI (served.py check itself, a hook): it says nothing live; one still up this long
                  # after the edit holds the old copy, however soon before the edit it started (a0ad9 fourth pass, 112503263733)
TEST = re.compile(r"(?:^|/)(?:tests?/|test_[^/]*$|[^/]*_test\.py$)")   # a test server left running (serve_csp.py since 11-06) is no service

def procs():
    """This user's running processes that run a .py/.sh: [{pid, script, started (unix), unit}]. It finds what systemd's units don't name:
    a script under `heavy` (a run-*.scope), in tmux, behind a wrapper or `python3 -m`, or the one-shot still running (review a0ad9, 112502385891)."""
    try:
        btime = next(int(l.split()[1]) for l in Path("/proc/stat").read_text().splitlines() if l.startswith("btime "))
        hz, uid = os.sysconf("SC_CLK_TCK"), os.getuid()
    except (OSError, StopIteration, ValueError): return []
    out, me = [], set()
    pid = os.getpid()
    while pid > 1 and pid not in me:   # this check and what ran it
        me.add(pid)
        try: pid = int(Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[1])
        except (OSError, ValueError, IndexError): break
    for d in Path("/proc").iterdir():
        if not d.name.isdigit() or int(d.name) in me: continue
        try:
            if d.stat().st_uid != uid: continue
            argv = [a.decode(errors="replace") for a in (d / "cmdline").read_bytes().split(b"\0") if a]
            if not any(a.endswith((".py", ".sh")) or a == "-m" for a in argv): continue
            script = script_in(argv[1:], os.readlink(d / "cwd"))
            if not script or TEST.search(str(script)): continue
            start = int((d / "stat").read_text().rsplit(")", 1)[1].split()[19])   # field 22, starttime in ticks since boot
            cg = (d / "cgroup").read_text().strip().rsplit("/", 1)[-1]
        except (OSError, ValueError, IndexError): continue
        out.append({"pid": int(d.name), "script": script.resolve(), "started": btime + start / hz, "unit": cg})
    return out


DOCKER = ("Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")

def in_docker(p):
    """A Dockerfile or compose file beside p or above it (below home): a container may run it, and a container's processes are not ours to see."""
    for d in p.parents:
        if d == HOME or not d.is_relative_to(HOME): break
        if any((d / n).is_file() for n in DOCKER): return d
    return None


def dash_port(us=None):
    for u in (units() if us is None else us):
        if u["id"] == "hive-dash.service" and "--port" in u["argv"]:
            i = u["argv"].index("--port")
            if i + 1 < len(u["argv"]) and u["argv"][i + 1].isdigit(): return int(u["argv"][i + 1])
    try: return int(json.loads(WEB_JSON.read_text()).get("port") or 8077)
    except (OSError, ValueError, TypeError): return 8077


def own_port(port):
    """True if this user holds the listening socket on 127.0.0.1:port (as dash.local_get checks before it sends the session cookie)."""
    want = "0100007F:%04X" % port
    try: rows = [ln.split() for ln in Path("/proc/net/tcp").read_text().splitlines()[1:]]
    except OSError: return False
    return any(len(f) > 7 and f[1] == want and f[3] == "0A" and f[7] == str(os.getuid()) for f in rows)


def fetch_served(rel, port=None, opener=None, holds=None):
    """(status, bytes) for a dash web file, fetched past every cache, signed in as a local browser is (dash.has_session)."""
    port = port or dash_port()
    headers = {"Cache-Control": "no-cache", "Pragma": "no-cache"}
    try: secret = str(json.loads(WEB_JSON.read_text())["secret"])
    except (OSError, ValueError, KeyError, TypeError): secret = None
    if secret:
        if not (holds or own_port)(port): return 0, b""   # another account can bind the port while the dash restarts: no cookie for it
        headers["Cookie"] = f"app_session_{port}=" + hmac.new(secret.encode(), b"session", hashlib.sha256).hexdigest()
    url = f"http://127.0.0.1:{port}/{'' if rel == 'index.html' else rel}?cb={time.time_ns()}"
    try:
        with (opener or urllib.request.urlopen)(urllib.request.Request(url, headers=headers), timeout=10) as r: return r.status, r.read()
    except urllib.error.HTTPError as e: return e.code, b""
    except (OSError, ValueError): return 0, b""


def check(path, us=None, fetch=fetch_served, now=None, ps=None):
    """{path, state: live|restart|fresh|unknown|differs|unserved|missing, why} for one file. ps: procs(), read here if None."""
    now = now or time.time()
    p = Path(str(path).replace("~/", str(HOME) + "/", 1) if str(path).startswith("~/") else str(path)).expanduser()
    if not p.is_absolute():   # `served.py check starter.py` from ~/core; a deleted cwd falls back to home
        try: cwd = Path.cwd()
        except OSError: cwd = HOME
        p = cwd / p if (cwd / p).exists() else HOME / p
    p = p.resolve()
    if not p.exists(): return {"path": str(p), "state": "missing", "why": "no such file"}
    mtime = p.stat().st_mtime
    if p.is_relative_to(DASH_WEB.resolve()) and "test" not in p.relative_to(DASH_WEB.resolve()).parts:
        rel = p.relative_to(DASH_WEB.resolve()).as_posix()
        st, body = fetch(rel)
        if st != 200: return {"path": str(p), "state": "unserved", "why": f"the dash answered {st or 'nothing'} for /{rel}"}
        if rel == "index.html": return {"path": str(p), "state": "live", "why": "served (index.html is rewritten on the way out, so only its status is checked)"}
        if hashlib.sha256(body).digest() != hashlib.sha256(p.read_bytes()).digest():
            return {"path": str(p), "state": "differs", "mtime": mtime, "why": f"the dash serves /{rel} different from the file on disk"}
        late = f"; through the tunnel a browser may keep the old copy until {time.strftime('%H:%M:%SZ', time.gmtime(mtime + TUNNEL_S))}" if now - mtime < TUNNEL_S else ""
        return {"path": str(p), "state": "live", "why": f"the dash serves /{rel} the same as on disk (a tab open before the edit shows it after a reload){late}"}
    us = units() if us is None else us
    hits, held, seen = [], set(), {}
    imports = lambda s: seen.setdefault(s, local_imports(s))
    hm = lambda t: time.strftime('%m-%d %H:%MZ', time.gmtime(t))
    for u in us:
        s = script_of(u)
        if not s: continue
        if u["timer"] or u["type"] == "oneshot":   # runs anew, the same as no hit, unless a run is in progress: only then follow its imports
            if s.resolve() != p and not (u["active"] == "activating" and p in imports(s)): continue
            held.add(u.get("main"))
            if u["active"] == "activating" and u["started"] and u["started"] < mtime and now - u["started"] > OLD_RUN_S:   # release-train: hours in one run
                hits.append(("restart", f"{u['id']} is a one-shot still on a run that started {hm(u['started'])}, before the change at {hm(mtime)}: "
                                        "that run holds the old copy (the next run takes this one)", u["started"]))
            else: hits.append(("fresh", f"{u['id']} runs it fresh each time"))
            continue
        if u["active"] not in ("active", "activating", "reloading"): continue
        if p in imports(s):
            held.add(u.get("main"))
            if u["started"] and u["started"] >= mtime: hits.append(("live", f"{u['id']} started {hm(u['started'])}, after the last change"))
            else: hits.append(("restart", f"{u['id']} started {hm(u['started'])}, before the change at {hm(mtime)}: restart needed", u["started"]))
    for x in (procs() if ps is None else ps):   # what no unit names: heavy's run-*.scope, tmux, a wrapper, `python3 -m`
        if x["pid"] in held or p not in imports(x["script"]): continue   # a unit's main process is told above; an agent's script in the starter's cgroup is not
        where = f"pid {x['pid']} ({x['script'].name}{', ' + x['unit'] if x['unit'] else ''})"
        if now - x["started"] < CLI_S: continue
        if x["started"] >= mtime:
            if not hits: hits.append(("live", f"{where} started {hm(x['started'])}, after the last change"))   # one is enough to say so
        elif now - x["started"] > OLD_RUN_S or now - mtime >= CLI_S: hits.append(("restart", f"{where} started {hm(x['started'])}, before the change at {hm(mtime)}: it runs the old copy", x["started"]))
    if not hits:
        if (d := in_docker(p)): return {"path": str(p), "state": "unknown", "mtime": mtime,
                                        "why": f"no process of ours holds it, but {d.relative_to(HOME)} has a Dockerfile or compose file: a container may run an old copy"}
        return {"path": str(p), "state": "fresh", "mtime": mtime, "why": "no running service or process holds it, so whatever calls it next runs this copy"}
    worst = next((h for h in hits if h[0] == "restart"), hits[0])
    return {"path": str(p), "state": worst[0], "mtime": mtime, "started": min((h[2] for h in hits if h[0] == "restart"), default=0), "why": "; ".join(h[1] for h in hits)}


LIVE_OK = ("live", "fresh")


NOT_YET = re.compile(r"\b(once|when|whenever|until|till|unless|if|not|isn't|aren't|won't|after|before|to be|will be|would be|goes|go|going|until it'?s)\W+(?:\w+\W+){0,3}$", re.I)
# A file the same post says is not live yet, or names a time or event it goes live at, is no claim (ad748, 112502221ada: "promises.py loads
# when the starter restarts" next to "the served fixes are live now" fired the warning). The claim reaches only the files of its own sentence,
# or of the sentence before it on the same line ("Fixed core/x.py. Live now."), not 200 characters across bullets; a quoted claim is a mention.
PENDING = re.compile(r"\b(not (yet )?live|isn'?t (yet )?live|aren'?t (yet )?live|not live yet|live (once|when|after|from|until)\b|will (be|go) live|goes live|"
                     r"loads? (when|once|after|at|on)\b|picks? (it|this|them|that) up|restart (needed|pending)|needs? an? (\w+ )?restart|"
                     r"(when|once|after|until|till) (the|it|its|a|an|that|this) [\w-]+( [\w-]+)? (re)?starts?|after (the|a|its) restart|"
                     r"(un)?till? the restart|not (yet )?(running|loaded|deployed|restarted)|stays? (\w+ )?old|(?:stays?|staying|left|leave|leaving|keep|keeping|kept|remains?|holds?|wait(?:s|ing)?|parked|on hold)\b[^.;:]{0,30}\bfor now)", re.I)
# "the restart is the person's call, so it stays old for now" (review a0ad9, 112502385891); "for now" only beside a word that
# leaves it as it is: "core/promises.py is fixed and live now; that's all for now" is a claim (a0ad9 fourth pass)
PAST_FIX = re.compile(r"\b(that|which|who)\W+(?:[\w./~-]+\W+){0,3}$", re.I)   # "the bug that core/promises.py fixed yesterday is back": a mention
PAST_AFTER = re.compile(r"\W+(yesterday|earlier|(\w+ ){0,2}ago\b|last (night|week|time)\b)", re.I)
SENT = re.compile(r"(?<=[.!?;])\s+")

def is_result(p):
    return str(p.get("reason", "")).startswith("Done:") or bool(re.search(r"^\s*\**Done:", str(p.get("text", "")), re.M))


def claims(posts, since, us=None, fetch=fetch_served, now=None, results_only=True, max_files=None):
    """Result posts (a Done: reason) since `since` (unix) that say a change is live: [(post, [check results])] for the files named near that
    phrase which were not live when the post went out and still aren't: a service that started before both the edit and the post (restart
    needed), or a dash file served different from the one on disk that was already there at post time. "Live once it restarts", "not live
    yet" and an edit made after the post are not claims to check (review a273c, 11-25: the first version got 0 of 3 real cases right)."""
    out = []
    for p in posts:
        try: at = dt.datetime.fromisoformat(str(p.get("at", "")).replace("Z", "+00:00"))
        except ValueError: continue
        at = (at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp()
        if at < since or (results_only and not is_result(p)): continue
        files = {}   # in the order the post names them: the cap keeps the first
        pending = set()
        for line in str(p.get("text", "")).splitlines():
            sents = SENT.split(line)
            for i, sent in enumerate(sents):
                named = [f[1] for f in PATH.finditer(sent)]
                if PENDING.search(sent): pending.update(named or (i and [f[1] for f in PATH.finditer(sents[i - 1])] or []))   # "Fixed x.py; it isn't live yet"
                for m in LIVE_CLAIM.finditer(sent):
                    before = sent[:m.start()]
                    if NOT_YET.search(before[-40:]) or (before.count('"') + before.count("\u201c") - before.count("\u201d")) % 2: continue
                    if m[0].lower() == "fixed" and (PAST_FIX.search(before[-60:]) or PAST_AFTER.match(sent[m.end():])): continue
                    files.update(dict.fromkeys(named or (i and [f[1] for f in PATH.finditer(sents[i - 1])] or [])))
        files = {f: 1 for f in files if f not in pending}
        if not files: continue
        res = [check("~/" + f, us=us, fetch=fetch, now=now) for f in list(files)[:max_files]]   # a post's paths are from home, not the cwd
        bad = [r for r in res if r["state"] in ("restart", "differs") and r.get("mtime", 0) <= at and r.get("started", 0) < at]
        if bad: out.append((p, bad))   # a file since moved or deleted, or one edited after the post, is no claim to check
    return out


ON_POST_FILES = 5   # hive post waits on this: the first few files named near the claim are enough to catch a stale service


def on_post(p, us=None, fetch=fetch_served, now=None):
    """The warning `hive post` prints when the post it just wrote says a named file is live, deployed or fixed and it isn't (review a0ad9,
    1125010056b9: run by hand only, so "said done but it isn't live" had no mechanism). Any post, not only a Done: one: "fixed, live now"
    in an interim post is the same claim. Cheap unless the text names a file and a live word: then one systemctl call."""
    text = str(p.get("text", ""))
    if not (LIVE_CLAIM.search(text) and PATH.search(text)): return None
    us = units() if us is None else us   # once, not once per file (review 11-25: 0.45 s a file)
    if fetch is fetch_served:
        port = dash_port(us); fetch = lambda rel: fetch_served(rel, port=port)
    found = claims([p], 0, us=us, fetch=fetch, now=now, results_only=False, max_files=ON_POST_FILES)
    if not found: return None
    rows = "\n".join(f"   {r['state']:8} {r['path'].replace(str(HOME) + '/', '')}: {r['why']}" for r in found[0][1])
    return ("hive post: NOT LIVE: this post says a change is live, deployed or fixed, but what runs is still the old copy:\n" + rows +
            "\nRestart it (systemctl --user restart UNIT; check `hive who` first for hive-starter) or reload the page, then say it's live; or post that it isn't yet.")


def _span(s):
    m = re.fullmatch(r"(\d+(?:\.\d+)?)([mhd])", s.strip())
    if not m: raise argparse.ArgumentTypeError("like 30m, 6h or 2d")
    return float(m[1]) * {"m": 60, "h": 3600, "d": 86400}[m[2]]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("check"); c.add_argument("paths", nargs="+"); c.add_argument("--json", action="store_true")
    k = sub.add_parser("claims"); k.add_argument("--since", type=_span, default=6 * 3600)
    a = ap.parse_args(argv)
    if a.cmd == "check":
        us = units(); res = [check(x, us=us) for x in a.paths]
        if a.json: print(json.dumps(res, indent=1))
        else:
            for r in res: print(f"{r['state']:9} {r['path'].replace(str(HOME) + '/', '')}: {r['why']}")
        return 0 if all(r["state"] in LIVE_OK for r in res) else 2 if all(r["state"] in LIVE_OK + ("unknown",) for r in res) else 1
    posts = []
    try:
        with open(HOME / "board.jsonl", errors="replace") as f:
            for ln in f:
                try: posts.append(json.loads(ln))
                except ValueError: pass
    except OSError: pass
    found = claims(posts, time.time() - a.since, us=units())
    for p, bad in found:
        print(f"{p.get('id')} {p.get('from')} in {p.get('thread')}:")
        for r in bad: print(f"   {r['state']:9} {r['path'].replace(str(HOME) + '/', '')}: {r['why']}")
    print(f"-- {len(found)} live claims with a file that isn't live")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
