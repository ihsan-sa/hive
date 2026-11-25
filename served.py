#!/usr/bin/env python3
"""Is a change live? A post that says "live" or "deployed" is checked against what is actually running, not against the file on disk.

Thread friction-chase, 2026-11-25: the person kept finding work that was said to be done but wasn't live. A .py edit to a running
service does nothing until that service restarts. A dash web file is served from disk, but a tab already open keeps its old modules,
and the tunnel lets a browser reuse css and js for 60 s. Until now the only check was livecheck.py, which tests that an edited file
still parses. It doesn't test whether the edit is the code that runs.

  served.py check PATH...         for each file: live, fresh (runs anew each call), restart needed, differs or unserved (exit 1 if any)
  served.py claims [--since 6h]   posts that say a change is live: check the files they name
  (hive post runs on_post on every agent post that names a file and says live, deployed or fixed, and warns the writer)

How a file is checked:
  - a dash web file (modules/dash/web/...): fetched from the local dash with a cache-busting query and `Cache-Control: no-cache`, signed
    in the way a local browser is, then its sha256 compared to the file on disk. index.html is rewritten on the way out, so only its
    status is checked.
  - a .py file that a long-running user service runs or imports from its own folder (found by following its imports): live only if
    the service started after the file last changed, otherwise "restart needed".
  - the script of a timer or one-shot unit: it runs fresh each time, so the next run uses it.
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
    """User services: [{id, type, active, started (unix or 0), argv: [...], timer: bool}]."""
    names = [l.split()[0] for l in run(["systemctl", "--user", "list-units", "--type=service", "--all", "--no-legend", "--plain"]).splitlines() if l.split()]
    if not names: return []
    out, cur = [], {}
    raw = run(["systemctl", "--user", "show", "--timestamp=unix", "-p", "Id,Type,ActiveState,ExecMainStartTimestamp,ExecStart,TriggeredBy", *names], 20)
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
                    "argv": (m[1].split() if m else []), "timer": is_timer(u.get("TriggeredBy", ""))})
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
    try: tree = ast.parse(script.read_text(errors="replace"))
    except (SyntaxError, ValueError, OSError): return seen
    for n in ast.walk(tree):
        names = [a.name for a in n.names] if isinstance(n, ast.Import) else [n.module] if isinstance(n, ast.ImportFrom) and n.module and not n.level else []
        for name in names:
            f = script.parent / (name.split(".")[0] + ".py")
            if f.is_file(): local_imports(f, seen)
        if isinstance(n, ast.Call) and (getattr(n.func, "attr", None) or getattr(n.func, "id", None)) in LOADERS:
            for f in loaded_paths(n, script): local_imports(f, seen)
    if script.name == "starter.py":
        for f in config_impls(script.parent / "config.json"): local_imports(f, seen)
    return seen


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
    """The .py or .sh a unit runs, if any."""
    return next((Path(a) for a in u["argv"] if a.endswith((".py", ".sh")) and Path(a).is_absolute()), None)


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


def check(path, us=None, fetch=fetch_served, now=None):
    """{path, state: live|restart|fresh|differs|unserved|missing, why} for one file."""
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
    hits = []
    for u in us:
        s = script_of(u)
        if not s: continue
        if u["timer"] or u["type"] == "oneshot":
            if s.resolve() == p: hits.append(("fresh", f"{u['id']} runs it fresh each time"))
            continue
        if u["active"] not in ("active", "activating", "reloading"): continue
        if p in local_imports(s):
            if u["started"] and u["started"] >= mtime: hits.append(("live", f"{u['id']} started {time.strftime('%m-%d %H:%MZ', time.gmtime(u['started']))}, after the last change"))
            else: hits.append(("restart", f"{u['id']} started {time.strftime('%m-%d %H:%MZ', time.gmtime(u['started']))}, before the change at {time.strftime('%m-%d %H:%MZ', time.gmtime(mtime))}: restart needed", u["started"]))
    if not hits: return {"path": str(p), "state": "fresh", "mtime": mtime, "why": "no running service holds it, so whatever calls it next runs this copy"}
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
                     r"(un)?till? the restart|not (yet )?(running|loaded|deployed|restarted))", re.I)
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
        return 0 if all(r["state"] in LIVE_OK for r in res) else 1
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
