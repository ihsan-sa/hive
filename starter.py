#!/usr/bin/env python3
"""Hive starter: watch ~/board.jsonl, start a session per @mention, pace on weekly usage, log, back up ~ to git."""
import datetime as dt, json, os, re, secrets, shutil, signal, subprocess, time, urllib.request, uuid
from pathlib import Path

HOME = Path.home()
CORE, BOARD, LOG, RUN, STATE = HOME / "core", HOME / "board.jsonl", HOME / "log.jsonl", HOME / "run", HOME / ".hive"
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
children = {}  # name -> Popen, for sessions this process started (others are tracked by pid file only)

def now(): return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
def cfg(): return json.loads((CORE / "config.json").read_text())
def log(**e):
    with LOG.open("a") as f: f.write(json.dumps({"at": now(), **e}) + "\n")
def load(name, default):
    try: return json.loads((STATE / name).read_text())
    except (OSError, ValueError): return default
def save(name, v): (STATE / name).write_text(json.dumps(v, indent=1))

def post(text, to=(), thread=None):
    p = {"id": dt.datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now(), "from": "starter",
         "to": ["@" + t for t in to], "text": text}
    p["thread"] = thread or p["id"]
    with BOARD.open("a") as f: f.write(json.dumps(p) + "\n")

def mentions(p):
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    found = [t.lstrip("@").lower() for t in to] + [m.lower() for m in MENTION.findall(str(p.get("text", "")))]
    return list(dict.fromkeys(found))

MODELS = ("opus", "sonnet", "haiku", "fable")  # what a post may ask for with `hive post --model`

LIMIT_TEXT = re.compile(r"usage limit|rate limit|hit your limit|limit reached|out of (extra )?usage", re.I)

def limit_hit(out):
    """If a session stopped on a rejected usage limit, return (reset epoch, last session id), else None.
    A run whose final result is a clean success is never limited, whatever its text says."""
    reset, sid, result = None, None, None
    try: lines = Path(out).read_text(errors="replace").splitlines()[-200:]
    except (OSError, TypeError): return None
    text = []  # non-JSON lines (the CLI's own error output, since stderr goes to the same file)
    for line in lines:
        try: e = json.loads(line)
        except ValueError:
            text.append(line); continue
        if not isinstance(e, dict): continue
        sid = e.get("session_id") or sid
        if e.get("type") == "rate_limit_event":
            i = e.get("rate_limit_info") or {}
            reset = i.get("resetsAt") if i.get("status") == "rejected" else None
        if e.get("type") == "result": result = e
    if result is not None and not result.get("is_error"): return None
    if reset is None:  # no rejected event: only an error result or CLI text that names a usage limit counts
        said = ([str(result.get("result", ""))] if result else []) + text[-5:]
        if any(LIMIT_TEXT.search(t) for t in said):
            reset = time.time() + 1800  # limit text without a reset time: try again in 30 min
    return (float(reset), sid) if reset else None

def finished_but_alive(r, grace):
    """The run printed its final result and nothing since for `grace` s: it is done, and only something it
    left behind (a background shell that never ends, e.g. a `pgrep -f` loop that matches itself) keeps
    `claude -p` alive, since -p waits on background shells. True means: end it so its waiting posts start it."""
    out = Path(r.get("out", ""))
    try:
        if time.time() - out.stat().st_mtime < grace: return False
        with out.open("rb") as f:
            f.seek(max(0, out.stat().st_size - 65536)); last = f.read().strip().splitlines()[-1]
        return json.loads(last).get("type") == "result"
    except (OSError, IndexError, ValueError): return False

def cpu_of(name, skip):
    """CPU seconds used so far by live processes that carry HIVE_NAME=name (detached ones too: nohup/setsid
    keep the env) and by their reaped children, except pid `skip` (the agent's own claude)."""
    tot, tick, key = 0.0, os.sysconf("SC_CLK_TCK"), f"HIVE_NAME={name}".encode()
    for d in Path("/proc").iterdir():
        if not d.name.isdigit() or int(d.name) == skip: continue
        try:
            if key not in (d / "environ").read_bytes().split(b"\0"): continue
            st = (d / "stat").read_text().rsplit(")", 1)[1].split()
            # utime+stime, plus cutime+cstime: a reaped child's CPU moves into its parent's,
            # so a job that runs many short subprocesses (KRT, a sim sweep) keeps counting
            tot += (int(st[11]) + int(st[12]) + int(st[13]) + int(st[14])) / tick
        except (OSError, IndexError, ValueError): pass
    return tot

work = {}  # name -> [t of last sample, cpu then, t last seen working]

def working(r, grace):
    """Something the agent started (a sim, a build) used real CPU within the last `grace` s: it is waiting
    on that, not stuck. Sampled once a minute; busy means >= 5% of a core over the minute."""
    t, w = time.time(), work.setdefault(r["name"], [0, 0.0, 0])
    if not w[0]:  # first look: take a sample and decide after a minute of it. Until that minute is
        # measured it counts as working (w[2] = t); with w[2] = 0 the next poll, seconds later, reaped it
        w[0], w[1], w[2] = t, cpu_of(r["name"], r["pid"]), t; return True
    if t - w[0] >= min(60, grace):
        c = cpu_of(r["name"], r["pid"])
        if w[0] and c - w[1] >= 0.05 * (t - w[0]): w[2] = t
        w[0], w[1] = t, c
    return t - w[2] < grace

def bg_tasks(out):
    """Descriptions of background tasks the run started and that had not ended when it went quiet."""
    open_ = {}
    try:
        for line in Path(out).read_text(errors="replace").splitlines():
            try: e = json.loads(line)
            except ValueError: continue
            if e.get("type") != "system": continue
            if e.get("subtype") == "task_started": open_[e.get("task_id")] = e.get("description", "")
            elif e.get("subtype") == "task_notification": open_.pop(e.get("task_id"), None)
    except OSError: pass
    return list(open_.values())

def kill_tree(pid):
    """SIGTERM pid and every descendant (they may sit in their own sessions/groups), then SIGKILL survivors."""
    kids = {}
    for d in Path("/proc").iterdir():
        try: kids.setdefault(int((d / "stat").read_text().rsplit(")", 1)[1].split()[1]), []).append(int(d.name))
        except (ValueError, OSError, IndexError): pass
    tree, todo = [], [pid]
    while todo:
        q = todo.pop(); tree.append(q); todo += kids.get(q, [])
    for sig in (signal.SIGTERM, signal.SIGKILL):
        for q in reversed(tree):
            try: os.kill(q, sig)
            except OSError: pass
        time.sleep(2)

def runs_now(name):
    """Whether `name` has a run whose claude process is alive right now (no reaping, unlike running())."""
    try: return Path(f"/proc/{json.loads((RUN / f'{name}.json').read_text())['pid']}").exists()
    except (OSError, ValueError, KeyError): return False

def running(st=None):
    alive = {}
    grace = cfg().get("done_grace_s", 900)
    for f in RUN.glob("*.json"):
        r = json.loads(f.read_text())
        c = children.get(r["name"])
        if finished_but_alive(r, grace) and not working(r, grace):
            bg = bg_tasks(r.get("out", ""))
            log(event="reaped", name=r["name"], session=r["session"], pid=r["pid"], bg=bg)
            kill_tree(r["pid"])
            if c: c.wait(timeout=10)
            if st is not None and bg:  # it may have been waiting on something: resume it so it can look
                st.setdefault("resume", {})[r["name"]] = {**r, "reset": 0, "reaped": bg}
            work.pop(r["name"], None)
        done = c.poll() is not None if c else not Path(f"/proc/{r['pid']}").exists()
        if done:
            f.unlink(); children.pop(r["name"], None)
            log(event="end", name=r["name"], session=r["session"], pid=r["pid"], exit=c.returncode if c else None)
            hit = limit_hit(r.get("out"))
            if hit and st is not None:  # stopped on a usage limit: resume it when the limit resets
                login, limits = r.get("login", ""), st.setdefault("limits", {})
                fresh = not any(time.time() < t for t in limits.values())  # first stop of this limit episode
                limits[login] = max(limits.get(login, 0), hit[0])
                st.setdefault("resume", {})[r["name"]] = {**r, "session": hit[1] or r["session"], "reset": hit[0]}
                log(event="limited", name=r["name"], session=hit[1] or r["session"],
                    resets=dt.datetime.fromtimestamp(hit[0], dt.timezone.utc).isoformat(timespec="seconds"))
                if fresh:  # one post per episode, not one per stopped agent
                    post(f"Usage limit hit ({r['name']} first); holding new starts until "
                         f"{dt.datetime.fromtimestamp(limits[login], dt.timezone.utc):%H:%M} UTC on {login}, then resuming "
                         "every limit-stopped run (see ~/log.jsonl event=limited). Other logins with room carry on.", ["person"])
        else: alive[r["name"]] = r
    return alive

def usage(c, u):
    """Refresh weekly usage per login every usage_check_s. Returns {login: pct or None}. Never refreshes tokens."""
    if time.time() - u.get("_checked", 0) < c["usage_check_s"]: return u
    u = {"_checked": time.time()}
    for login in c["logins"]:
        d = Path(login).expanduser()
        try:
            tok = json.loads((d / ".credentials.json").read_text())["claudeAiOauth"]["accessToken"]
            req = urllib.request.Request("https://api.anthropic.com/api/oauth/usage", headers={
                "Authorization": f"Bearer {tok}", "anthropic-beta": "oauth-2025-04-20"})
            w = json.load(urllib.request.urlopen(req, timeout=15))["seven_day"]
            u[str(d)] = w.get("used_percentage", w.get("utilization"))
        except Exception: u[str(d)] = None  # unknown (expired token, network): still usable, tried last
    save("usage.json", u)
    return u

def pick_login(c, u, limits=None):
    """The login with the most weekly room, skipping any held on a 5-hour/weekly limit (limits: {login: until})."""
    ok = [(v is None, v or 0, k) for k, v in u.items() if k != "_checked" and (v is None or v < c["usage_stop_pct"])
          and time.time() >= (limits or {}).get(k, 0)]
    return min(ok)[2] if ok else None

def move_session(sid, src, dst):
    """Copy a session's transcript to another login so --resume finds it there. False if there's nothing to copy."""
    src, dst = Path(src), Path(dst)
    found = list(src.glob(f"projects/*/{sid}.jsonl"))
    for f in found:
        to = dst / f.relative_to(src); to.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(f, to)
        if f.with_suffix("").is_dir(): shutil.copytree(f.with_suffix(""), to.with_suffix(""), dirs_exist_ok=True)
    return bool(found)

def start(c, name, posts, login, resume=None, reaped=None, extra=()):
    hook_inbox(c)  # a login can be added to config.json while the starter runs
    sid = resume or str(uuid.uuid4())  # the claude session id, so `claude --resume <session>` works
    first = posts[0]
    model = next((p["model"] for p in posts if p.get("model") in MODELS), None)  # none: the login's default
    kind = "warden" if name == "warden" else "agent"
    system = (CORE / "orientation.md").read_text() + ("\n\n" + (CORE / "warden.md").read_text() if kind == "warden" else "")
    prompt = (f"You are @{name}. You were woken by board post {', '.join(p['id'] for p in posts)} "
              f"(thread {first.get('thread', first['id'])}, from {first.get('from')}). "
              f"Start with `hive read --thread {first.get('thread', first['id'])}`.")
    if resume and reaped:
        prompt = ("The starter ended your run: it had printed its result, then nothing for "
                  f"{c.get('done_grace_s', 900) // 60} min, and nothing you started was using CPU. These background shells "
                  f"of yours were killed: {json.dumps(reaped)}. Jobs you started with nohup/setsid survive. If you were "
                  "waiting on something, check it now and carry on (a background wait on a job that uses CPU is fine; "
                  f"an idle wait is ended again). Check `hive read --thread {first.get('thread', first['id'])}` "
                  "for anything new first. If you were done, just stop."
                  + (f" Posts for you that came in meanwhile: {', '.join(extra)} (`hive read` them)." if extra else ""))
    elif resume:
        prompt = ("Your run stopped on a usage limit and the limit has now reset. Continue where you left off "
                  f"(check `hive read --thread {first.get('thread', first['id'])}` for anything new first).")
    path = HOME / "sessions" / f"{name}-{dt.datetime.now():%m%d-%H%M%S}.jsonl"
    env = {**os.environ, "HIVE_NAME": name, "CLAUDE_CONFIG_DIR": login}
    proc = subprocess.Popen(["claude", "-p", prompt, "--append-system-prompt", system,
                             *(["--resume", sid] if resume else ["--session-id", sid]), *(["--model", model] if model else []), *c["claude_args"]],
                            cwd=HOME, env=env, stdout=path.open("w"), stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
    children[name] = proc
    (STATE / "delivered" / name).unlink(missing_ok=True)  # the inbox hook starts fresh with this run
    rec = {"name": name, "pid": proc.pid, "session": sid, "at": now(), "post": first["id"], "login": login,
           "thread": first.get("thread"), "from": first.get("from"), "out": str(path), **({"resumed": True} if resume else {}), **({"model": model} if model else {})}
    (RUN / f"{name}.json").write_text(json.dumps(rec))
    log(event="start", **rec)

def keeper(c, st, alive, login):
    """Keep work going without anyone asking: every keeper_every_s, or once the hive has been idle (no runs,
    nothing queued) for keeper_idle_s (and keeper_every_s/3 since the last round), start one fresh agent on ~/core/keeper.md in thread 'keeper'.
    Never while one is still on it, while paused or limited, or while keeper_every_s is 0."""
    every = c.get("keeper_every_s", 0)
    if not every or not login: return
    if any(isinstance(r, dict) and r.get("thread") == "keeper" for r in alive.values()): return
    if any(p.get("thread") == "keeper" for ps in st["pending"].values() for p in ps): return
    t = time.time()
    if alive or st["pending"]: st["busy_at"] = t
    idle = t - st.get("busy_at", t) >= c.get("keeper_idle_s", 1800)
    last = st.setdefault("keeper_at", t)  # first boot: wait a full period
    if t - last < every and not (idle and t - last >= max(c.get("keeper_idle_s", 1800), every / 3)): return
    st["keeper_at"] = st["busy_at"] = t
    post(f"@new keeper round ({'idle' if idle else 'periodic'}): read ~/core/keeper.md and follow it.", ["new"], "keeper")
    log(event="keeper", why="idle" if idle else "periodic")

def backup(c, st):
    if time.time() - st.get("committed", 0) < c["commit_s"]: return
    st["committed"] = time.time()
    subprocess.run("git add -A && git commit -qm 'auto backup' || true", shell=True, cwd=HOME,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

def hook_inbox(c):
    """Make sure each login's settings.json runs inbox.py after every tool call and on stop (see inbox.py).
    Later logins share the first login's skills and the agents' memory, so an agent is the same on any of them."""
    h = {"hooks": [{"type": "command", "command": str(CORE / "inbox.py"), "timeout": 10}]}
    first = Path(c["logins"][0]).expanduser()
    slug = str(HOME).replace("/", "-")
    for login in c["logins"]:
        f = Path(login).expanduser() / "settings.json"
        f.parent.mkdir(parents=True, exist_ok=True)  # a fresh install may not have logged in yet
        for rel in ("skills", f"projects/{slug}/memory"):
            to, src = f.parent / rel, first / rel
            if f.parent != first and src.exists() and not to.exists() and not to.is_symlink():
                to.parent.mkdir(parents=True, exist_ok=True); to.symlink_to(src)
        try: s = json.loads(f.read_text()) if f.exists() else {}
        except ValueError: continue  # don't clobber a settings file we can't parse
        hooks = s.setdefault("hooks", {})
        if str(CORE / "inbox.py") in json.dumps(hooks): continue
        hooks.setdefault("PostToolUse", []).append({"matcher": "*", **h}); hooks.setdefault("Stop", []).append(h)
        f.write_text(json.dumps(s, indent=2) + "\n")

def main():
    for d in (RUN, STATE, HOME / "sessions"): d.mkdir(exist_ok=True)
    hook_inbox(cfg())
    st = load("starter.json", {"offset": 0, "pending": {}})
    u, paused = {}, False
    while True:
        c = cfg()
        size = BOARD.stat().st_size if BOARD.exists() else 0
        if size < st["offset"]: st["offset"] = size  # board was rewritten; don't replay it
        if size > st["offset"]:
            with BOARD.open("rb") as f:
                f.seek(st["offset"]); chunk = f.read()
            chunk = chunk[:chunk.rfind(b"\n") + 1]  # only whole lines
            st["offset"] += len(chunk)
            for line in chunk.decode(errors="replace").splitlines():
                try: p = json.loads(line)
                except ValueError: continue
                for n in mentions(p):
                    if n in c["external"]: continue
                    # a self-mention wakes its author only when the author isn't running: a waiter's
                    # "done" post from a run that has already ended; a running agent mentioning itself doesn't
                    if n == str(p.get("from", "")).lower() and runs_now(n): continue
                    if n == "new": n = "a" + secrets.token_hex(2)
                    st["pending"].setdefault(n, []).append({k: p.get(k) for k in ("id", "thread", "from", "model")})
        alive = running(st)
        u = usage(c, u)
        if (pick_login(c, u) is None) != paused:
            paused = not paused
            post("Weekly usage is past the stop line on every login; not starting sessions until it resets."
                 if paused else "Usage has room again; starting sessions.", ["person"])
            log(event="paused" if paused else "resumed", usage=u)
        login = pick_login(c, u, st.get("limits"))  # None: every login is past the stop line or held on a limit
        for n, r in list(st.get("resume", {}).items()):  # resume limit-stopped sessions first, same name and session
            if not login or n in alive or len(alive) >= (c.get("max_sessions") or float("inf")): continue
            own, limits = r.get("login"), st.get("limits")
            if own in u and time.time() >= r["reset"] and pick_login(c, {own: u[own]}, limits): to = own  # its login has room again
            elif own != login and move_session(r["session"], own, login):  # or it moves to one that does
                to = login; log(event="moved", name=n, session=r["session"], src=own, dst=to)
            else: continue
            st["resume"].pop(n)
            waiting = st["pending"].pop(n, []) if r.get("reaped") else []  # hand queued posts to the resumed run
            start(c, n, [{"id": r["post"], "thread": r.get("thread"), "from": r.get("from"), "model": r.get("model")}], to,
                  resume=r["session"], reaped=r.get("reaped"), extra=[p["id"] for p in waiting])
            alive[n] = True
        for n in list(st["pending"]):
            if n in alive or n in st.get("resume", {}): continue
            got = STATE / "delivered" / n  # posts the inbox hook already handed this agent mid-run
            if got.exists():
                seen = set(got.read_text().split()); got.unlink()
                st["pending"][n] = [p for p in st["pending"][n] if p["id"] not in seen]
                if not st["pending"][n]:
                    st["pending"].pop(n); log(event="delivered", name=n); continue
            if login and len(alive) < (c.get("max_sessions") or float("inf")):
                start(c, n, st["pending"].pop(n), login)
                alive[n] = True
        keeper(c, st, alive, login)
        save("starter.json", st)
        backup(c, st)
        time.sleep(c["poll_s"])

if __name__ == "__main__":
    main()
