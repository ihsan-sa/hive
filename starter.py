#!/usr/bin/env python3
"""Hive starter: watch ~/board.jsonl, start a session per @mention, pace on weekly usage, log, back up ~ to git."""
import datetime as dt, json, os, re, secrets, shutil, signal, subprocess, time, urllib.request, uuid
from pathlib import Path

HOME = Path.home()
CORE, BOARD, LOG, RUN, STATE = HOME / "core", HOME / "board.jsonl", HOME / "log.jsonl", HOME / "run", HOME / ".hive"
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)  # a name in backticks is being written about, not called
MEMINFO, PSI_MEM = "/proc/meminfo", "/proc/pressure/memory"
last_start = [0.0]  # when this process last started a run, for under_pressure
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
    # Keeper and improver dispatches are work requests; operational notices share one thread.
    p["thread"] = thread if thread in ("keeper", "improve") else "starter"
    if thread and thread not in ("keeper", "improve", "starter"):
        p["text"] = f"[source thread: {thread}] {text}"
    with BOARD.open("a") as f: f.write(json.dumps(p) + "\n")

def mentions(p):
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    found = [t.lstrip("@").lower() for t in to] + [m.lower() for m in MENTION.findall(CODE.sub(" ", str(p.get("text", ""))))]
    return list(dict.fromkeys(found))

# words that start a fresh agent: prefix of its code, and how its model is chosen (see main)
SPAWN = {"new": "a", "claude": "a", "codex": "c", "free": "f"}
CODEX_DEFAULT = "codex:gpt-6-astra"

def known_agents(c):
    """Names a word in a post's text may call: new, the warden, anyone who has run, except past accidents.
    `hive post` keeps the same rule; it puts only these in `to`, so anything else in `to` was asked for by --to."""
    names = set(SPAWN) | {"warden"}
    try:
        for line in LOG.open(errors="replace"):
            if '"start"' in line:
                try: names.add(json.loads(line)["name"].lower())
                except (ValueError, KeyError, AttributeError, TypeError): pass
    except OSError: pass
    return names - set(c.get("not_agents", []))

WARDEN = re.compile(r"w[0-9a-f]{4}")

def is_warden(name):
    """`@warden` starts w + 4 hex (as `@new` starts a + 4 hex); waking that code again gets the warden prompt too."""
    return name == "warden" or bool(WARDEN.fullmatch(name))

def callable_names(p, c):
    """(names this post calls, words in its text that look like a call but name nobody)."""
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    to = {t.lstrip("@").lower() for t in to}
    # Broadcasts are delivered only by live sessions' inbox hooks, never queued to wake agents.
    found = [n for n in mentions(p) if n not in {"all", "here"}]
    if all(n in to or n in c["external"] for n in found): return found, []
    known = known_agents(c)
    return [n for n in found if n in to or n in c["external"] or n in known], [n for n in found if n not in to and n not in c["external"] and n not in known]

FREE_AGENT = re.compile(r"f[0-9a-f]{4}")  # @free runs (OpenRouter free models): told not to start agents, enforced here
STOP = re.compile(r"(all |everyone )?stop( it| now| all( of)?( this| that)?| everything| everyone)?")

def is_stop(p):
    """The person telling a thread to stop ("@all stop", "stop all this"): mentions and punctuation aside, nothing else."""
    if p.get("from") != "person": return False
    text = re.sub(r"[^\w ]", " ", MENTION.sub(" ", str(p.get("text", "")))).lower()
    return bool(STOP.fullmatch(" ".join(text.split())))

def drop_spawns(st, thread):
    """Forget queued fresh-agent starts (new/claude/codex/free) from this thread; named agents' wakes stay."""
    gone = []
    for n, ps in list(st["pending"].items()):
        keep = [q for q in ps if not (q.get("spawn") and q.get("thread") == thread)]
        if len(keep) < len(ps): gone.append(n)
        if keep: st["pending"][n] = keep
        else: st["pending"].pop(n)
    return gone

MODELS = ("opus", "sonnet", "haiku", "fable")  # what a post may ask for with `hive post --model`
PROVIDER_MODEL = re.compile(r"^([a-z0-9-]+):([A-Za-z0-9][A-Za-z0-9._/:@-]*)$")  # openrouter:openai/gpt-5

def provider_of(c, model):
    """(provider name, its config, the model id it serves) for a `provider:model` post model, else None. A provider is
    an Anthropic-compatible API (OpenRouter, a LiteLLM proxy, ...) in config.json's providers; Claude Code runs on it."""
    m = PROVIDER_MODEL.match(model or "")
    if not m or m[1] not in c.get("providers", {}): return None
    return m[1], c["providers"][m[1]], m[2]

def model_ok(c, model): return model in MODELS or provider_of(c, model) is not None

_impls = {}
def provider_room(c, name, pc, cache):
    """Why provider `name` can't take a run now, or None. The work is in the module file pc["impl"] (see
    ~/modules/providers): it reads the key and runs the spend guard. A missing or broken module holds only provider runs."""
    try:
        f = str(Path(pc["impl"]).expanduser())
        if f not in _impls:
            import importlib.util
            spec = importlib.util.spec_from_file_location(f"h2_provider_{len(_impls)}", f)
            m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); _impls[f] = m
        pc["_impl"] = _impls[f]
        return pc["_impl"].room(c, pc, cache)
    except Exception as e: return f"provider module {pc.get('impl')} failed ({type(e).__name__}: {e})"[:300]

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
        sid = e.get("session_id") or e.get("thread_id") or sid  # thread_id: a codex run's (thread.started)
        if e.get("type") in ("error", "turn.failed"):  # codex reports a limit as an event, not a result
            text.append(str(e.get("message") or (e.get("error") or {}).get("message", "")))
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

AUTH_TEXT = re.compile(r"not logged in|please run /login|failed to authenticate|authentication_failed|invalid api key|oauth token (has|is|was)", re.I)
AUTH_HOLD_S = 3600  # a rejected login is tried again after this, in case someone signed it back in

def auth_hit(out):
    """If a run ended because its login's credentials were rejected (expired, signed out, revoked), its last session id
    or "", else None. Same reading as limit_hit: a clean final result never counts, whatever its text says."""
    sid, result, said = "", None, []
    try: lines = Path(out).read_text(errors="replace").splitlines()[-200:]
    except (OSError, TypeError): return None
    for line in lines:
        try: e = json.loads(line)
        except ValueError:
            said.append(line); continue
        if not isinstance(e, dict): continue
        sid = e.get("session_id") or sid
        if e.get("type") == "assistant" and e.get("error"): said.append(str(e["error"]))
        if e.get("type") == "result": result = e
    if result is not None and not result.get("is_error"): return None
    return sid if any(AUTH_TEXT.search(t) for t in said[-5:] + ([str(result.get("result", ""))] if result else [])) else None

def seen_usage(out):
    """(weekly pct, reset epoch) from the last rate_limit_event in a run's output, or None. Every API call prints one, so
    this needs no read of the usage API, which answers 429 most of the time while the dash polls it too."""
    try: lines = Path(out).read_text(errors="replace").splitlines()
    except (OSError, TypeError): return None
    for line in reversed(lines):
        if '"rate_limit_event"' not in line: continue
        try: w = json.loads(line)["rate_limit_info"]["unifiedWindows"]["seven_day"]; return float(w["utilization"]) * 100, float(w["resetsAt"])
        except (ValueError, KeyError, TypeError): continue
    return None

def session_in(out):
    """The last session id a run printed (a codex run's own id comes only this way), or None."""
    sid = None
    try:
        for line in Path(out).read_text(errors="replace").splitlines():
            if '"session_id"' in line or '"thread_id"' in line:
                try: e = json.loads(line); sid = e.get("session_id") or e.get("thread_id") or sid
                except (ValueError, AttributeError): pass
    except (OSError, TypeError): pass
    return sid

def finished_but_alive(r, grace):
    """The run printed its final result and nothing since for `grace` s: it is done, and only something it
    left behind (a background shell that never ends, e.g. a `pgrep -f` loop that matches itself) keeps
    `claude -p` alive, since -p waits on background shells. True means: end it so its waiting posts start it."""
    out = Path(r.get("out", ""))
    try:
        if time.time() - out.stat().st_mtime < grace: return False
        with out.open("rb") as f:
            f.seek(max(0, out.stat().st_size - 65536)); last = f.read().strip().splitlines()[-1]
        return json.loads(last).get("type") in ("result", "turn.completed")  # turn.completed: codex
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

def tree_of(pid):
    """pid and every descendant, parents first."""
    kids = {}
    for d in Path("/proc").iterdir():
        try: kids.setdefault(int((d / "stat").read_text().rsplit(")", 1)[1].split()[1]), []).append(int(d.name))
        except (ValueError, OSError, IndexError): pass
    tree, todo = [], [pid]
    while todo:
        q = todo.pop(); tree.append(q); todo += kids.get(q, [])
    return tree

WAITERS = {"bash", "sh", "timeout", "sleep", "tail", "cat", "grep", "wc", "date", "pgrep"}

def only_waiters(pid):
    """Is everything under this run a wait loop? Then ending the run loses nothing but the wait. Jobs started with nohup or
    setsid have left the tree and survive; a job that is still a child of the run shows here, so the run stays where it is."""
    for q in tree_of(pid)[1:]:
        try:
            if Path(f"/proc/{q}/comm").read_text().strip() not in WAITERS: return False
        except OSError: pass
    return True

def out_of_room(c, login, limits):
    """A Claude login a run shouldn't wake up on (paused, past a stop line, or held on a real limit) while another has room."""
    if login not in {str(Path(l).expanduser()) for l in c.get("logins", [])}: return False
    every = load("usage.json", {})
    if not isinstance(every, dict) or login not in every: return False
    room = unpaused(every)
    if login in room and pick_login(c, {login: room[login]}, limits): return False
    return pick_login(c, room, limits) not in (None, login)

def kill_tree(pid):
    """SIGTERM pid and every descendant (they may sit in their own sessions/groups), then SIGKILL survivors."""
    tree = tree_of(pid)
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
        # parked on a waiter on a login that has run out: it would only find out when the waiter ends, up to hours later
        move = st is not None and out_of_room(cfg(), r.get("login"), st.get("limits")) and finished_but_alive(r, min(60, grace)) and only_waiters(r["pid"])
        if move or (finished_but_alive(r, grace) and not working(r, grace)):
            bg = bg_tasks(r.get("out", ""))
            log(event="reaped", name=r["name"], session=r["session"], pid=r["pid"], bg=bg, move=move)
            kill_tree(r["pid"])
            if c: c.wait(timeout=10)
            if st is not None and (bg or move):  # it may have been waiting on something: resume it so it can look
                st.setdefault("resume", {})[r["name"]] = {**r, "session": session_in(r.get("out")) or r["session"], "reset": 0, "reaped": bg or ["(none listed)"], "move": move}
            work.pop(r["name"], None)
        done = c.poll() is not None if c else not Path(f"/proc/{r['pid']}").exists()
        if done:
            f.unlink(); children.pop(r["name"], None)
            log(event="end", name=r["name"], session=r["session"], pid=r["pid"], exit=c.returncode if c else None)
            hit = limit_hit(r.get("out"))
            if c and c.returncode and not hit and r.get("from") == "person" and r.get("fallback") and not posted_since(r["name"], r.get("at", "")):
                # a0f70 died on an API 503 at 15:52 on 2026-11-02 and the person's request just vanished
                post(f"`{r['name']}` on the fallback model died (exit {c.returncode}) without posting, so post {r.get('post')} wasn't done. Post again, or wait for a Claude login.", ["person"], r.get("thread"))
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
            elif st is not None and r.get("login") in {str(Path(l).expanduser()) for l in cfg().get("logins", [])} \
                    and (sid := auth_hit(r.get("out"))) is not None:  # same treatment as a limit, or the run just vanishes
                login, limits = r["login"], st.setdefault("limits", {})
                fresh, until = time.time() >= limits.get(login, 0), time.time() + AUTH_HOLD_S
                limits[login] = max(limits.get(login, 0), until)
                st.setdefault("resume", {})[r["name"]] = {**r, "session": sid or r["session"], "reset": until}
                log(event="login_rejected", name=r["name"], login=login)
                if fresh:
                    post(f"Login {Path(login).name} rejected its credentials (expired or signed out) when `{r['name']}` ran. Its runs move to a login "
                         "with room and it gets no new ones; it's tried again in an hour. It needs signing in again on this machine.", ["person"])
        else: alive[r["name"]] = r
    return alive

def drop_killed(st):
    """The person killed a run from the dashboard: ~/.hive/killed/<name>.json holds its pid, written before the signal.
    Call after running(), which ends the run and may have queued a resume (a limit in its log, open background tasks).
    Once the run is gone, drop that resume and its queued wakes, so the kill stays a kill. While it is still dying it's
    in `alive`, so nothing restarts it. A post after this is a new request and starts it as usual."""
    for f in (STATE / "killed").glob("*.json"):
        n = f.stem
        try: pid = json.loads(f.read_text()).get("pid")
        except (OSError, ValueError, AttributeError): pid = None
        try:
            if json.loads((RUN / f"{n}.json").read_text()).get("pid") == pid: continue
        except (OSError, ValueError, AttributeError): pass
        posts = [p.get("id") for p in st["pending"].pop(n, [])]
        resume = st.get("resume", {}).pop(n, None)
        log(event="kill_dropped", name=n, posts=posts, resume=bool(resume))
        f.unlink(missing_ok=True)

def usage(c, u):
    """Refresh usage per login every usage_check_s. Returns {login: weekly pct or None}; the 5-hour pcts from the same
    call go to ~/.hive/usage_5h.json ({login: pct or None}), and the reset times to ~/.hive/usage_resets.json ({login:
    {"weekly": epoch|None, "five_hour": epoch|None, "has_five_hour": bool}}). Never refreshes tokens."""
    if time.time() - u.get("_checked", 0) < c["usage_check_s"]: return u
    prev, prev5, prevr = load("usage.json", {}), load("usage_5h.json", {}), load("usage_resets.json", {})  # the file, so a restart carries too
    u, five, resets = {"_checked": time.time()}, {}, {}
    for login in c["logins"]:
        d = Path(login).expanduser()
        try:
            tok = json.loads((d / ".credentials.json").read_text())["claudeAiOauth"]["accessToken"]
            req = urllib.request.Request("https://api.anthropic.com/api/oauth/usage", headers={
                "Authorization": f"Bearer {tok}", "anthropic-beta": "oauth-2025-04-20"})
            r = json.load(urllib.request.urlopen(req, timeout=15)); w = r["seven_day"]
            u[str(d)] = w.get("used_percentage", w.get("utilization"))
            five[str(d)] = (r.get("five_hour") or {}).get("utilization")
            resets[str(d)] = {k: epoch((r.get(src) or {}).get("resets_at")) for k, src in (("weekly", "seven_day"), ("five_hour", "five_hour"))}
            resets[str(d)]["has_five_hour"] = r.get("five_hour") is not None  # some plans have no 5-hour limit at all
        except Exception:  # a failed read isn't news: 429s (the dash polls the same API) read as "room" and flapped, 2026-11-02
            u[str(d)], five[str(d)], resets[str(d)] = carried(str(d), prev, prev5, prevr)
            # the runs on this login printed its real figure: fresher than a carried one, and there even with no good read yet
            if (seen := newest_seen(str(d))) and time.time() < seen[1]:
                u[str(d)] = max(seen[0], u[str(d)] or 0); resets[str(d)] = {"five_hour": None, "has_five_hour": False, **resets[str(d)], "weekly": seen[1]}
    save("usage.json", u); save("usage_5h.json", five); save("usage_resets.json", resets)
    return u

def newest_seen(login):
    """seen_usage from the newest live run on this login, or None."""
    rs = []
    for f in RUN.glob("*.json"):
        try: rs.append(json.loads(f.read_text()))
        except (OSError, ValueError): pass
    for r in sorted((r for r in rs if r.get("login") == login), key=lambda r: r.get("at", ""), reverse=True):
        if (seen := seen_usage(r.get("out"))): return seen
    return None

def posted_since(name, at):
    """Did agent `name` post to the board at or after `at`?"""
    try:
        since = dt.datetime.fromisoformat(at).timestamp()
        for line in BOARD.read_text().splitlines()[-2000:]:
            p = json.loads(line)
            if p.get("from") == name and dt.datetime.fromisoformat(p["at"]).timestamp() >= since: return True
    except Exception: return True  # can't tell: stay quiet
    return False

def carried(login, weekly, five, resets):
    """After a failed read: (weekly pct, 5-hour pct, resets) from the last good read, each pct only until the reset that
    read named. Past that reset, or with no good read, it's None: unknown, still usable, tried last."""
    r = resets.get(login) if isinstance(resets, dict) else None
    r = r if isinstance(r, dict) else {}
    def keep(v, reset): return v if isinstance(v, (int, float)) and isinstance(reset, (int, float)) and time.time() < reset else None
    return keep(weekly.get(login), r.get("weekly")), keep(five.get(login) if isinstance(five, dict) else None, r.get("five_hour")), r

def epoch(iso):
    """An ISO time from the usage API as epoch seconds, or None."""
    try: return dt.datetime.fromisoformat(iso.replace("Z", "+00:00")).timestamp()
    except Exception: return None

def clamp(v):
    """A stop line from ~/.hive/login_limits.json as a number in 0-100, or None if it isn't set (or isn't a number)."""
    try: return None if v is None or isinstance(v, bool) else min(100.0, max(0.0, float(v)))
    except (TypeError, ValueError): return None

def stop_lines(c, login):
    """(weekly stop, 5-hour stop or None) for a login. Per login in ~/.hive/login_limits.json, {login path: {"weekly": N,
    "five_hour": N}}, set by `hive login limit` or the dashboard; no weekly line there means the global usage_stop_pct,
    no 5-hour line means no 5-hour stop. A line above the global one is the person's to set, never an agent's."""
    lines = load("login_limits.json", {})
    mine = lines.get(login) if isinstance(lines, dict) else None
    mine = mine if isinstance(mine, dict) else {}
    w = clamp(mine.get("weekly"))
    return (c["usage_stop_pct"] if w is None else w), clamp(mine.get("five_hour"))

def unpaused(u):
    """The usage readings without the logins the person paused (~/.hive/paused.json, a list of login paths; set by `hive login
    pause` or the dashboard). A paused login is still read and still shown; it just never gets a new or resumed run."""
    off = load("paused.json", [])
    return {k: v for k, v in u.items() if k not in off} if isinstance(off, list) else u

def pick_login(c, u, limits=None, stop=None):
    """The login with the most weekly room, skipping any held on a 5-hour/weekly limit (limits: {login: until}) and any
    past its own stop lines (stop_lines). stop: one weekly line for every login instead, and no 5-hour lines
    (login_for's 100 for the person's posts, which go past the stop lines up to a real limit)."""
    five = load("usage_5h.json", {}) if stop is None else {}
    five = five if isinstance(five, dict) else {}
    def room(k, v):
        w, h = stop_lines(c, k) if stop is None else (stop, None)
        f = five.get(k)
        return (v is None or v < w) and (h is None or not isinstance(f, (int, float)) or f < h)
    ok = [(v is None, v or 0, k) for k, v in u.items() if k != "_checked" and room(k, v)
          and time.time() >= (limits or {}).get(k, 0)]
    return min(ok)[2] if ok else None

def under_pressure(c, posts=()):
    """Why a new run should wait a moment, or None. This paces starts, it is not a cap: the run still starts once the
    machine can take it, however many are running (2026-11-02: 44 starts in 20 min took swap to full and load to 292).
    The person, the box and the warden never wait."""
    if any(p.get("from") == "person" or p.get("from") == "hives" for p in posts): return None
    if time.time() - last_start[0] < c.get("start_gap_s", 10): return "gap"
    try:
        avail = int(re.search(r"MemAvailable:\s+(\d+)", Path(MEMINFO).read_text())[1]) // 1024
        stall = float(re.search(r"full avg10=\S+ avg60=([\d.]+)", Path(PSI_MEM).read_text())[1])
    except (OSError, TypeError): return None
    if avail < c.get("start_min_avail_mb", 3000): return f"{avail} MB available"
    if stall >= c.get("start_max_stall", 20): return f"memory stall {stall:.0f}%"
    return None

def paced(c, st, n, posts):
    """True if run n waits for now. Logged once per wait, and only for real pressure, not the gap."""
    why = under_pressure(c, posts)
    if why and why != "gap" and st.setdefault("paced", {}).get(n) is None:
        st["paced"][n] = why; log(event="paced", name=n, why=why)
    if not why: st.get("paced", {}).pop(n, None)
    return bool(why)

def login_for(c, u, limits, posts, login):
    """Past the stop line agents' posts wait, but a post from the person still starts its run: else the person
    has no way into Hive until the reset. That run's own mentions wait like any other. Stops at a real limit."""
    if login: return login
    return pick_login(c, u, limits, 100) if any(p.get("from") == "person" for p in posts) else None

def move_session(sid, src, dst):
    """Copy a session's transcript to another login so --resume finds it there. False if there's nothing to copy."""
    src, dst = Path(src), Path(dst)
    found = list(src.glob(f"projects/*/{sid}.jsonl"))
    for f in found:
        to = dst / f.relative_to(src); to.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(f, to)
        if f.with_suffix("").is_dir(): shutil.copytree(f.with_suffix(""), to.with_suffix(""), dirs_exist_ok=True)
    return bool(found)

def start(c, name, posts, login, resume=None, reaped=None, extra=(), moved=False):
    hook_inbox(c)  # a login can be added to config.json while the starter runs
    sid = resume or str(uuid.uuid4())  # the claude session id, so `claude --resume <session>` works
    first = posts[0]
    model = (next((p["model"] for p in posts if provider_of(c, p.get("model"))), None)  # a provider wins: main loop picked it
             or next((p["model"] for p in posts if p.get("model") in MODELS), None))  # none: the login's default
    prov = provider_of(c, model)
    kind = "warden" if is_warden(name) else "agent"
    system = (CORE / "orientation.md").read_text() + ("\n\n" + (CORE / "warden.md").read_text() if kind == "warden" else "")
    prompt = (f"You are @{name}. You were woken by board post {', '.join(p['id'] for p in posts)} "
              f"(thread {first.get('thread', first['id'])}, from {first.get('from')}). "
              f"Start with `hive read --thread {first.get('thread', first['id'])}`.")
    if resume and reaped:
        prompt = (("The starter moved you to another login, because yours ran out while you were waiting. "
                   if moved else "The starter ended your run: it had printed its result, then nothing for "
                  f"{c.get('done_grace_s', 900) // 60} min, and nothing you started was using CPU. ") + "These background shells "
                  f"of yours were killed: {json.dumps(reaped)}. Jobs you started with nohup/setsid survive. If you were "
                  "waiting on something, check it now and carry on (a background wait on a job that uses CPU is fine; "
                  f"an idle wait is ended again). Check `hive read --thread {first.get('thread', first['id'])}` "
                  "for anything new first. If you were done, just stop."
                  + (f" Posts for you that came in meanwhile: {', '.join(extra)} (`hive read` them)." if extra else ""))
    elif resume:
        prompt = ("Your run stopped on a usage limit and the limit has now reset. Continue where you left off "
                  f"(check `hive read --thread {first.get('thread', first['id'])}` for anything new first).")
    fb = next((p["fallback"] for p in posts if p.get("fallback")), None)
    if fb: prompt += "\n\n" + FALLBACK_NOTE[fb].format(model=model, tier=tier_of(model))
    path = HOME / "sessions" / f"{name}-{dt.datetime.now():%m%d-%H%M%S}.jsonl"
    last_start[0] = time.time()
    tmp = HOME / ".cache" / "hive-tmp"; tmp.mkdir(parents=True, exist_ok=True)  # on disk: /tmp is tmpfs, its files hold RAM and swap
    env, argv = {**os.environ, "HIVE_NAME": name, "CLAUDE_CONFIG_DIR": login, "TMPDIR": str(tmp)}, None
    if prov:  # main loop's provider_hold just loaded its module and found room
        impl = prov[1]["_impl"]; env.update(impl.env(prov[1], prov[2]))
        if prov[1].get("config_dir"): env["CLAUDE_CONFIG_DIR"] = str(Path(prov[1]["config_dir"]).expanduser())
        login = env.get("CODEX_HOME") or env["CLAUDE_CONFIG_DIR"]
        if hasattr(impl, "command") and (argv := impl.command(prov[1], prov[2], prompt, system, sid, resume)):  # another harness (codex)
            env.pop("CLAUDE_CONFIG_DIR", None)
    proc = subprocess.Popen(argv or ["claude", "-p", prompt, "--append-system-prompt", system,
                             *(["--resume", sid] if resume else ["--session-id", sid]),
                             *(["--model", prov[2] if prov else model] if model else []), *c["claude_args"]],
                            cwd=HOME, env=env, stdout=path.open("w"), stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
    children[name] = proc
    (STATE / "delivered" / name).unlink(missing_ok=True)  # the inbox hook starts fresh with this run
    rec = {"name": name, "pid": proc.pid, "session": sid, "at": now(), "post": first["id"], "login": login,
           "thread": first.get("thread"), "from": first.get("from"), "out": str(path), **({"resumed": True} if resume else {}), **({"model": model} if model else {}), **({"fallback": fb} if fb else {}),
           "harness": prov[1].get("harness", "claude-code") if prov else "claude-code", **({"provider": prov[0]} if prov else {}), **({"effort": env["CLAUDE_EFFORT"]} if env.get("CLAUDE_EFFORT") else {})}
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
    if any(r.get("thread") == "keeper" for r in st.get("resume", {}).values()): return  # a limit-stopped round resumes instead
    t = time.time()
    if alive or st["pending"]: st["busy_at"] = t
    idle = t - st.get("busy_at", t) >= c.get("keeper_idle_s", 1800)
    last = st.setdefault("keeper_at", t)  # first boot: wait a full period
    if t - last < every and not (idle and t - last >= max(c.get("keeper_idle_s", 1800), every / 3)): return
    st["keeper_at"] = st["busy_at"] = t
    post(f"@new keeper round ({'idle' if idle else 'periodic'}): read ~/core/keeper.md and follow it.", ["new"], "keeper")
    log(event="keeper", why="idle" if idle else "periodic")

IMPROVE = """@new Figure out how to improve everything in the hive, across all of it: the projects, the quality of the work, failures, how agents work, Hive itself. Judge it yourself: read what you need (the board, ~/sessions, the code, results), then do things yourself or start other agents. `hive read --thread improve` shows what earlier rounds did. Say what you did in this thread."""

def improver(c, st, alive, login):
    """Every improve_every_s, one fresh agent on a deliberately open brief (the person, 2026-11-02: "just a spawn with one line").
    Never while one is still on it, while paused or limited, or while improve_every_s is 0."""
    every = c.get("improve_every_s", 0)
    if not every or not login: return
    if any(isinstance(r, dict) and r.get("thread") == "improve" for r in alive.values()): return
    if any(p.get("thread") == "improve" for ps in st["pending"].values() for p in ps): return
    if any(r.get("thread") == "improve" for r in st.get("resume", {}).values()): return
    t = time.time()
    if t - st.setdefault("improve_at", t) < every: return  # first boot: wait a full period
    st["improve_at"] = t
    post(IMPROVE, ["new"], "improve")
    log(event="improve")

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
    for login in c["logins"] + [pc["config_dir"] for pc in c.get("providers", {}).values() if pc.get("config_dir")]:
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

def provider_hold(c, st, n, pv, cache, thread):
    """True if agent n's provider can't take a run now; says why in its thread once per reason, so a post doesn't wait in silence."""
    why = provider_room(c, pv[0], pv[1], cache)
    held = st.setdefault("provider_held", {})
    if why is None: held.pop(n, None); return False
    if held.get(n) != why:
        held[n] = why; log(event="provider_held", name=n, provider=pv[0], why=why)
        post(f"`{n}` waits for provider `{pv[0]}`: {why}. It starts once that's fixed; the key and its credit are the person's.", ["person"], thread)
    return True

FALLBACK_NOTE = {
    "free": "This run is on a free model ({model}), not Claude: someone asked for a free agent. Keep it light: answer, look "
            "things up, small edits, board and kb chores. No heavy jobs, no builds or sims, and don't start other agents "
            "(@new, @free); if the work needs more, say so in the thread and stop. "
            "Before stopping, send your actual answer using hive post --thread THREAD. If the request came from "
            "person, include @person in that post so it reaches them. A local final answer alone is not delivered.",
    "fallback": "Every Claude login is at its usage limit, so this run is on a fallback model ({model}, {tier}) because the "
                "person's post shouldn't wait for the reset. Do only what's time-critical: answer, give an update, a small fix. "
                "No heavy jobs, no builds or sims, and don't start other agents (they'd wait for Claude anyway). Leave the "
                "rest for when Claude resets and say plainly in the thread what's left.",
}

def tier_of(model):
    m = PROVIDER_MODEL.match(model or "")
    if not m: return "claude"
    return "codex (no extra cost)" if m[1] == "codex" else "free" if m[2].endswith(":free") else "paid, real money"

def fallback_model(c, st, chain, cache):
    """The first model in chain (provider:model strings, config.json fallback) whose provider has room now, else None.
    A provider module may also say no for one model (model_room: OpenRouter free models' daily request count)."""
    for model in chain:
        pv = provider_of(c, model)
        if not pv or provider_room(c, pv[0], pv[1], cache) is not None: continue
        impl = pv[1].get("_impl")
        if impl and hasattr(impl, "model_room"):
            try:
                if impl.model_room(c, pv[1], pv[2], cache) is not None: continue
            except Exception: continue
        return model
    return None

def fallback(c, st, n, posts, claude_ok, cache):
    """For a free agent (@free) or a post from someone in fallback.for (the person) while no Claude login has room: pick a
    fallback model, set it on the posts and mark them, so start() runs on it and tells the agent which tier it is on.
    Returns True if it did; False to let the posts wait (with one note in the thread when nothing has room)."""
    fb = c.get("fallback") or {}
    free = any(p.get("free") for p in posts)
    if free: chain = fb.get("free", [])
    elif not claude_ok and any(p.get("from") in fb.get("for", ["person"]) for p in posts): chain = fb.get("chain", [])
    else: return False
    model = fallback_model(c, st, chain, cache)
    held = st.setdefault("fallback_held", {})
    if not model:
        if not held.get(n):
            held[n] = True; log(event="fallback_held", name=n, chain=chain)
            post(f"`{n}` waits: {'no free model' if free else 'no Claude login and no fallback'} has room now "
                 f"({', '.join(chain) or 'none set in config.json fallback'}). It starts once one does.", [], posts[0].get("thread"))
        return False
    held.pop(n, None)
    for p in posts: p["model"], p["fallback"] = model, "free" if free else "fallback"
    log(event="fallback", name=n, model=model, why="free" if free else "claude limits")
    return True

def main():
    for d in (RUN, STATE, HOME / "sessions"): d.mkdir(exist_ok=True)
    hook_inbox(cfg())
    st = load("starter.json", {"offset": 0, "pending": {}})
    u, paused, pcache = {}, False, {}
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
                called, unknown = callable_names(p, c)
                if is_stop(p) and (gone := drop_spawns(st, p.get("thread"))):
                    log(event="stop_dropped", names=gone, post=p.get("id"), thread=p.get("thread"))
                if FREE_AGENT.fullmatch(str(p.get("from", "")).lower()) and (skip := [n for n in called if n in SPAWN]):
                    log(event="free_spawn_ignored", names=skip, post=p.get("id"), src=p.get("from"))
                    called = [n for n in called if n not in SPAWN]
                for n in unknown:
                    log(event="unknown_mention", name=n, post=p.get("id"), src=p.get("from"))
                    if p.get("from") == "person":  # he can't see hive post's warning, and asked for something
                        post(f"Nobody here is called `{n}`, so nothing started. For a new agent use the word new; for one with that name, `hive post --to {n}`.", ["person"], p.get("thread"))
                for n in called:
                    if n in c["external"]: continue
                    # a self-mention wakes its author only when the author isn't running: a waiter's
                    # "done" post from a run that has already ended; a running agent mentioning itself doesn't
                    if n == str(p.get("from", "")).lower() and runs_now(n): continue
                    q = {k: p.get(k) for k in ("id", "thread", "from", "model")}
                    if n == "warden": n = "w" + secrets.token_hex(2)  # each warden run gets its own code too
                    elif n in SPAWN:
                        word, n = n, SPAWN[n] + secrets.token_hex(2)
                        q["spawn"] = word
                        # @new: the default (or --model); @claude: Claude only, never a provider or a fallback;
                        # @codex: Codex CLI (--model codex:... picks which); @free: an OpenRouter free model (fallback.free)
                        if word == "claude":
                            q["claude"] = True
                            if provider_of(c, q.get("model")): q["model"] = None
                        elif word == "codex" and ((pv := provider_of(c, q.get("model"))) is None or pv[1].get("harness") != "codex"):
                            q["model"] = c.get("codex_model") or CODEX_DEFAULT
                        elif word == "free": q["free"] = True
                    st["pending"].setdefault(n, []).append(q)
        alive = running(st)
        drop_killed(st)
        # the person's post is about to fall back: read again first (17:02 on 2026-11-02 went to a free model 4 s before the read that showed a reset login)
        if pick_login(c, unpaused(u), st.get("limits")) is None and time.time() - u.get("_checked", 0) > 30 \
                and any(p.get("from") == "person" for ps in st["pending"].values() for p in ps):
            u["_checked"] = 0
        u = usage(c, u); room = unpaused(u)
        if (off := sorted(set(u) - set(room))) != st.get("paused_logins", []):
            st["paused_logins"] = off; log(event="logins_paused", logins=off)
        if (pick_login(c, room) is None) != paused:
            paused = not paused
            post("Every login is paused or past its stop line; starting only runs the person asks for, on a login that isn't paused."
                 if paused else "Usage has room again; starting sessions.", ["person"])
            log(event="paused" if paused else "resumed", usage=u)
        login = pick_login(c, room, st.get("limits"))  # None: every login is paused, past the stop line or held on a limit
        for n, r in list(st.get("resume", {}).items()):  # resume limit-stopped sessions first, same name and session
            if (not login and not provider_of(c, r.get("model"))) or n in alive or len(alive) >= (c.get("max_sessions") or float("inf")): continue
            if paced(c, st, n, [{"from": r.get("from")}]): continue
            own, limits = r.get("login"), st.get("limits")
            if (pv := provider_of(c, r.get("model"))):  # a provider run resumes on its provider, never on a Claude login
                if time.time() < r["reset"] or provider_hold(c, st, n, pv, pcache, r.get("thread")): continue
                to = own
            elif own in room and time.time() >= r["reset"] and pick_login(c, {own: room[own]}, limits): to = own  # its login has room again
            elif own != login and move_session(r["session"], own, login):  # or it moves to one that does
                to = login; log(event="moved", name=n, session=r["session"], src=own, dst=to)
            else: continue
            st["resume"].pop(n)
            waiting = st["pending"].pop(n, []) if r.get("reaped") else []  # hand queued posts to the resumed run
            start(c, n, [{"id": r["post"], "thread": r.get("thread"), "from": r.get("from"), "model": r.get("model")}], to,
                  resume=r["session"], reaped=r.get("reaped"), extra=[p["id"] for p in waiting], moved=r.get("move", False))
            alive[n] = {"name": n, "thread": r.get("thread")}  # a dict, so keeper() sees a resumed keeper (two rounds at once, 2026-11-02)
        for n in list(st["pending"]):
            if n in alive or n in st.get("resume", {}): continue
            got = STATE / "delivered" / n  # posts the inbox hook already handed this agent mid-run
            if got.exists():
                seen = set(got.read_text().split()); got.unlink()
                st["pending"][n] = [p for p in st["pending"][n] if p["id"] not in seen]
                if not st["pending"][n]:
                    st["pending"].pop(n); log(event="delivered", name=n); continue
            if any(p.get("free") for p in st["pending"][n]) and not any(p.get("fallback") for p in st["pending"][n]):
                if not fallback(c, st, n, st["pending"][n], True, pcache): continue
            pv = next((pv for p in st["pending"][n] if (pv := provider_of(c, p.get("model")))), None)
            if pv: to = None if provider_hold(c, st, n, pv, pcache, st["pending"][n][0].get("thread")) else "provider"
            else:
                to = login_for(c, room, st.get("limits"), st["pending"][n], login)
                if not to and not any(p.get("claude") for p in st["pending"][n]) and fallback(c, st, n, st["pending"][n], False, pcache):
                    to = None if provider_hold(c, st, n, provider_of(c, st["pending"][n][0]["model"]), pcache, st["pending"][n][0].get("thread")) else "provider"
            if to and len(alive) < (c.get("max_sessions") or float("inf")) and not paced(c, st, n, st["pending"][n]):
                ps = st["pending"].pop(n); start(c, n, ps, to)
                alive[n] = {"name": n, "thread": ps[0].get("thread")}
        keeper(c, st, alive, login)
        improver(c, st, alive, login)
        save("starter.json", st)
        backup(c, st)
        time.sleep(c["poll_s"])

if __name__ == "__main__":
    main()
