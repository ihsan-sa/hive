#!/usr/bin/env python3
"""Hive starter: watch ~/board.jsonl, start a session per @mention, pace on weekly usage, log, back up ~ to git."""
import concurrent.futures, datetime as dt, json, os, re, secrets, shutil, signal, subprocess, sys, tempfile, time, urllib.request, uuid
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))  # tests load this file by path, under -I
import spend
try: import continuation as cont
except ImportError: cont = None  # a pilot: the starter runs without it

HOME = Path.home()
CORE, BOARD, LOG, RUN, STATE = HOME / "core", HOME / "board.jsonl", HOME / "log.jsonl", HOME / "run", HOME / ".hive"
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)  # a name in backticks is being written about, not called
MEMINFO, PSI_MEM = "/proc/meminfo", "/proc/pressure/memory"
last_start = [0.0]  # when this process last started a run, for under_pressure
recent_starts = []  # when, for under_pressure: runs too young to show in MemAvailable
efforts = [{}]  # st["efforts"], {agent: level}: what start() reads, it has no st of its own
EFFORTS = ("low", "medium", "high", "xhigh", "max")
ended_clean = []  # runs that just ended with nothing owed by the starter: the continuation pilot looks at them once `alive` is complete
children = {}  # name -> Popen, for sessions this process started (others are tracked by pid file only)

def now(): return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
def cfg(): return json.loads((CORE / "config.json").read_text())
def log(**e):
    with LOG.open("a") as f: f.write(json.dumps({"at": now(), **e}) + "\n")
def load(name, default):
    try: return json.loads((STATE / name).read_text())
    except (OSError, ValueError): return default
def save(name, v): (STATE / name).write_text(json.dumps(v, indent=1))

def post(text, to=(), thread=None, model=None):
    p = {"id": dt.datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now(), "from": "starter",
         "to": ["@" + t for t in to], "text": text}
    # Keeper and improver dispatches are work requests; operational notices share one thread.
    if model: p["model"] = model
    p["thread"] = thread if thread in ("keeper", "improve", "kb-dream") else "starter"
    if thread and thread not in ("keeper", "improve", "kb-dream", "starter"):
        p["text"] = f"[source thread: {thread}] {text}"
    with BOARD.open("a") as f: f.write(json.dumps(p) + "\n")

def mentions(p):
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    found = [t.lstrip("@").lower() for t in to] + [m.lower() for m in MENTION.findall(CODE.sub(" ", str(p.get("text", ""))))]
    return list(dict.fromkeys(found))

def _claude_models():
    """The Claude model words, weakest first: config.json "models". A new model is one line there, not an edit in each file."""
    try: m = json.loads((CORE / "config.json").read_text()).get("models")
    except (OSError, ValueError): m = None
    return tuple(m) if isinstance(m, list) and m and all(isinstance(x, str) and re.fullmatch(r"[a-z][a-z0-9.-]*", x) for x in m) else ("haiku", "sonnet", "opus", "fable")
CLAUDE_MODELS = _claude_models()
# words that start a fresh agent: prefix of its code, and how its model is chosen (see main)
CODEX_DEFAULT = "codex:gpt-6-astra"
MODEL_WORDS = {**{m: m for m in CLAUDE_MODELS}, "astra": CODEX_DEFAULT}  # @opus: a new agent on that model (the person, 2026-11-04)
SPAWN = {"new": "a", "claude": "a", "codex": "c", "free": "f", **{w: "c" if m.startswith("codex:") else "a" for w, m in MODEL_WORDS.items()}}

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
    # The rootwarden's gate fills `to` with every @word in its reply, so its `to` asks for nothing beyond the text.
    # Its "@base 21000274", quoted from a test case, started an agent called base (2026-11-06).
    if str(p.get("from", "")).lower() == "rootwarden": to = set()
    # Broadcasts are delivered only by live sessions' inbox hooks, never queued to wake agents.
    # A focus id or a thread name is a name too (hive post resolved it: `foci` and `copies` on the post), not a word naming nobody.
    skip = {"all", "here", *[str(x).lower() for x in (p.get("foci") or [])], *[str(x).lower() for x in (p.get("copies") or [])]}
    found = [n for n in mentions(p) if n not in skip]
    if all(n in to or n in c["external"] for n in found): return found, []
    known = known_agents(c)
    return [n for n in found if n in to or n in c["external"] or n in known], [n for n in found if n not in to and n not in c["external"] and n not in known]

FREE_AGENT = re.compile(r"f[0-9a-f]{4}")  # @free runs (OpenRouter free models): see free_spawns
CODEX_AGENT = re.compile(r"c[0-9a-f]{4}")
STOP = re.compile(r"(all |everyone )?stop( it| now| all( of)?( this| that)?| everything| everyone)?")

def is_stop(p):
    """The person telling a thread to stop ("@all stop", "stop all this"): mentions and punctuation aside, nothing else."""
    if p.get("from") != "person": return False
    text = re.sub(r"[^\w ]", " ", MENTION.sub(" ", str(p.get("text", "")))).lower()
    return bool(STOP.fullmatch(" ".join(text.split())))

if cont: cont.is_stop = is_stop

def drop_spawns(st, thread):
    """Forget queued fresh-agent starts (new/claude/codex/free) from this thread; named agents' wakes stay."""
    gone = []
    for n, ps in list(st["pending"].items()):
        keep = [q for q in ps if not (q.get("spawn") and q.get("thread") == thread)]
        if len(keep) < len(ps): gone.append(n)
        if keep: st["pending"][n] = keep
        else: st["pending"].pop(n)
    return gone

MODELS = CLAUDE_MODELS  # what a post may ask for with `hive post --model`
PROVIDER_MODEL = re.compile(r"^([a-z0-9-]+):([A-Za-z0-9][A-Za-z0-9._/:@-]*)$")  # openrouter:openai/gpt-5

def provider_of(c, model):
    """(provider name, its config, the model id it serves) for a `provider:model` post model, else None. A provider is
    an Anthropic-compatible API (OpenRouter, a LiteLLM proxy, ...) in config.json's providers; Claude Code runs on it."""
    m = PROVIDER_MODEL.match(model or "")
    if not m or m[1] not in c.get("providers", {}): return None
    return m[1], c["providers"][m[1]], m[2]

def family(name, pc):
    """Providers that are the same kind of account: codex, codex-2, codex-3 are three Codex logins (Settings > Accounts adds them)."""
    return pc.get("family") or re.sub(r"-[0-9]+$", "", name)

def home_of(pc):
    """Where a harness provider keeps its login and sessions: its CODEX_HOME or KIMI_CODE_HOME."""
    h = pc.get("codex_home") or pc.get("kimi_home") or {"codex": "~/.codex", "kimi": "~/.kimi-code"}.get(pc.get("harness"))
    return str(Path(h).expanduser()) if h else None

def with_room(c, pv, cache):
    """pv, or else another provider of its family that has room now (a second Codex login when the first is at its limit).
    The model id is kept; the caller rewrites the posts to the new provider name, so a resume finds the same login."""
    if provider_room(c, pv[0], pv[1], cache) is None: return pv
    for name, pc in c.get("providers", {}).items():
        if name != pv[0] and family(name, pc) == family(pv[0], pv[1]) and provider_room(c, name, pc, cache) is None: return name, pc, pv[2]
    return pv

def retarget(c, posts, src, dst):
    """Move posts asking for provider src onto provider dst, same model id."""
    for p in posts:
        if (q := provider_of(c, p.get("model"))) and q[0] == src: p["model"] = f"{dst}:{q[2]}"

def model_ok(c, model): return model in MODELS or provider_of(c, model) is not None

def default_model():
    """The model of a run nobody asked a model for. An installed Hive: the Model choice in Settings, Sonnet until one is made (the person, 2026-11-04;
    Opus before), so it doesn't hang on what the account's plan defaults to. No settings.json (Hive itself): the login's default."""
    try: v = json.loads((HOME / "settings.json").read_text())
    except FileNotFoundError: return None
    except (OSError, ValueError): v = None
    m = v.get("model") if isinstance(v, dict) else None
    return m if m in MODELS else "sonnet"

def spawn_model(c):
    """The model of a new agent nobody asked a model for: None if Settings has a choice (default_model reads it), else config.json's spawn_model.
    Set at the spawn and not in default_model, so an agent that ran before keeps its model on a wake."""
    try: v = json.loads((HOME / "settings.json").read_text())
    except (OSError, ValueError): v = None
    return None if isinstance(v, dict) and v.get("model") in MODELS else c.get("spawn_model")

TYPED_MODEL = re.compile(r"(?:^|\s)(?:--|\u2014|\u2013)model[ =](\S+)")  # a phone may turn -- into a dash

def asked_model(p, c):
    """The model a post asks for: hive post --model, or the person typing --model opus in the text (Slack has no flags)."""
    if p.get("model"): return p["model"]
    if p.get("from") == "person" and (m := TYPED_MODEL.search(CODE.sub(" ", str(p.get("text", ""))))) and model_ok(c, m[1]): return m[1]
    return None

def step_clause(model):
    """Said at each start and wake, the natural point to weigh the model (the person, 2026-11-05, thread model-stepping). Self-judged switches were 0 of 12."""
    if model in ("haiku", "sonnet"): return f' You\'re on {model}: if this turns into design or debugging you\'re stuck on, run `hive model opus "why"` and end your turn.'
    if model in ("opus", "fable"): return f' You\'re on {model}: if this is routine with a good stretch to do, run `hive model sonnet "why"` and end your turn. A short job, just do it.'
    return ""

def is_free_now(st, name):
    """A free agent stays free until a post moves it to another model. No record: it predates st["chosen"]."""
    return bool(FREE_AGENT.fullmatch(name)) and (st.get("chosen", {}).get(name) or {"free": True}).get("free", False)

def stays_on(st, c, n, q):
    """A wake keeps the agent on the model it was started on (the person, 2026-11-02, thread 110216234927), unless the post
    asks for another, which then holds from here on, for posts already queued too. Only what was asked for is kept: a
    run that went to a fallback because Claude was at its limit goes back to Claude next time."""
    if model_ok(c, q.get("model")):
        st.setdefault("chosen", {})[n] = {"model": q["model"]}
        for p in st["pending"].get(n, []):
            p["model"] = q["model"]
            for k in ("free", "claude", "fallback"): p.pop(k, None)
    elif (rec := st.get("chosen", {}).get(n)): q.update(rec)
    elif FREE_AGENT.fullmatch(n): q["free"] = True
    elif CODEX_AGENT.fullmatch(n): q["model"] = c.get("codex_model") or CODEX_DEFAULT


def free_spawns(p, called):
    """Which fresh-agent words in a free agent's post count. It may hand work it can't do to a stronger agent (the person,
    2026-11-02), but only on purpose: `--to new` (or claude, codex) with the word not in its text. A word in its prose starts
    nothing: on 11-01 free agents writing them while refusing a request queued about ten Claude runs. It can't start more
    free agents. No rate limit (the person, 2026-11-05): login stop lines bound the usage. Returns (allowed, ignored)."""
    said = mentions({"text": p.get("text")})
    words = [n for n in called if n in SPAWN]
    ok = [n for n in words if n != "free" and n not in said][:1]
    return ok, [n for n in words if n not in ok]

_impls = {}
_root = (0, False)  # (checked at, answer)

def has_root():
    """Can this account use sudo without a password? Agents all share one uid, so an outside model could read whatever a
    root agent saw. `-l` asks without running anything, so it doesn't log a sudo attempt on an account that has none. Cached 60 s."""
    global _root
    if time.time() - _root[0] < 60: return _root[1]
    try: yes = os.geteuid() == 0 or subprocess.run(["sudo", "-n", "-l", "true"], capture_output=True, timeout=10).returncode == 0
    except FileNotFoundError: yes = False
    except Exception: yes = True  # couldn't tell: hold the outside model
    _root = (time.time(), yes)
    return yes

def provider_room(c, name, pc, cache):
    """Why provider `name` can't take a run now, or None. The work is in the module file pc["impl"] (see
    ~/modules/providers): it reads the key and runs the spend guard. A missing or broken module holds only provider runs.
    Every non-Claude run comes through here, so this is also where root holds them."""
    if has_root(): return "this account has passwordless sudo, and an outside model must not share an account with root"
    if pc.get("paused"): return f"paused: {pc['paused']}"  # config.json providers.<name>.paused; remove the key to unpause
    try:
        f = str(Path(pc["impl"]).expanduser())
        if f not in _impls:
            import importlib.util
            spec = importlib.util.spec_from_file_location(f"h2_provider_{len(_impls)}", f)
            m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); _impls[f] = m
        pc["_impl"] = _impls[f]
        return pc["_impl"].room(c, pc, cache)
    except Exception as e: return f"provider module {pc.get('impl')} failed ({type(e).__name__}: {e})"[:300]

LIMIT_TEXT = re.compile(r"usage limit|rate limit|hit your (\S+ )?limit|limit reached|out of (extra )?usage", re.I)  # the CLI: You've hit your 5-hour limit

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

# A model's own weekly bar (the CLI 2.1.319's rateLimitType values and the words it prints for them): it stops that model only, so the
# login stays open for the others. Fable's is seven_day_overage_included ("Fable limit", the usage API's weekly_scoped row).
SCOPED_TYPES = {"seven_day_opus": "opus", "seven_day_sonnet": "sonnet", "seven_day_overage_included": "fable"}
SCOPED_TEXT = re.compile(r"(?:hit|reached) your (Fable|Opus|Sonnet) limit", re.I)

def limit_scope(out):
    """The model family ("fable", "opus", "sonnet") a run's rejected limit belongs to, or None for a whole-login (5-hour, weekly) one."""
    try: lines = Path(out).read_text(errors="replace").splitlines()[-200:]
    except (OSError, TypeError): return None
    scope = None
    for line in lines:
        try: e = json.loads(line)
        except ValueError:
            if (m := SCOPED_TEXT.search(line)): scope = m[1].lower()
            continue
        if not isinstance(e, dict): continue
        i = e.get("rate_limit_info") if e.get("type") == "rate_limit_event" else None
        if isinstance(i, dict) and i.get("status") == "rejected": scope = SCOPED_TYPES.get(i.get("rateLimitType"))
        elif e.get("type") == "result" and e.get("is_error") and (m := SCOPED_TEXT.search(str(e.get("result", "")))): scope = m[1].lower()
    return scope

def model_family(model):
    return next((f for f in ("fable", "opus", "sonnet") if f in str(model or "").lower()), None)

def scope_held(limits, login, model):
    """True if login is held for this model by a model-scoped limit (limits key "login#family"). A run whose model isn't known
    (the login's default) counts as held: it may be that model."""
    now = time.time()
    return any(now < t and (model is None or model_family(model) == k.rpartition("#")[2])
               for k, t in (limits or {}).items() if isinstance(k, str) and k.startswith(login + "#"))

AUTH_TEXT = re.compile(r"not logged in|login expired|please run /login|failed to authenticate|authentication_failed|invalid api key|oauth token (has|is|was)", re.I)
LAUNCH_HOLD_S = 600  # claude wouldn't start at all: tried again after this
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

woke, slept_was = 0.0, None

def note_sleep():
    """A suspended machine's wall clock jumps, and everything timed by it looks long quiet. So the moment it wakes counts as activity.
    Two signs, since inside a VM (the Mac version) the boot clock stands still while the host sleeps, and only the wall clock moves, when Lima's guest agent sets it."""
    global woke, slept_was
    mono = time.clock_gettime(time.CLOCK_MONOTONIC)
    slept = (time.clock_gettime(time.CLOCK_BOOTTIME) - mono, time.clock_gettime(time.CLOCK_REALTIME) - mono)
    if slept_was is not None and (slept[0] - slept_was[0] > 5 or slept[1] - slept_was[1] > 5): woke = time.time()
    slept_was = slept

def finished_but_alive(r, grace):
    """The run printed its final result and nothing since for `grace` s: it is done, and only something it
    left behind (a background shell that never ends, e.g. a `pgrep -f` loop that matches itself) keeps
    `claude -p` alive, since -p waits on background shells. True means: end it so its waiting posts start it."""
    out = Path(r.get("out", ""))
    try:
        if time.time() - max(out.stat().st_mtime, woke) < grace: return False
        with out.open("rb") as f:
            f.seek(max(0, out.stat().st_size - 65536)); last = f.read().strip().splitlines()[-1]
        return json.loads(last).get("type") in ("result", "turn.completed", "session.resume_hint")  # turn.completed: codex; session.resume_hint: kimi
    except (OSError, IndexError, ValueError): return False

def box():
    """The sandbox module on a consumer install with it on, else None. With no ~/settings.json (Hive itself) it is never even imported."""
    if not (HOME / "settings.json").exists(): return None
    import sys; sys.path.insert(0, str(CORE)); import sandbox
    return sandbox if sandbox.on() else None

BOX_SHAPE = 4  # in a sandbox the run is: bwrap, its init, the supervisor, then the agent's own process. Under that are its jobs.
box_state = {"ok": None, "at": 0.0}

def run_cost(out):
    """A run's total_cost_usd from its last result line, or None (no result yet, another harness)."""
    cost = None
    try:
        with open(out, errors="replace") as f:
            for ln in f:
                if '"total_cost_usd"' in ln:
                    try: v = json.loads(ln).get("total_cost_usd")
                    except ValueError: continue
                    if isinstance(v, (int, float)) and not isinstance(v, bool): cost = v
    except (OSError, TypeError): pass
    return cost

def daily_cap_holds(c, st):
    """Today's spend is at the person's daily cap: nothing new starts. The person is told once a day."""
    cap = spend.over_cap(c, LOG)
    if cap and st is not None and st.get("cap_told") != now()[:10]:
        st["cap_told"] = now()[:10]
        post(f"Today's spend reached your daily cap of ${cap:g}, so no new agents start until tomorrow (UTC). Raise or remove the cap in Settings (Budget) to start again.", ["person"])
    return bool(cap)

def box_holds():
    """On with a failing check: nothing starts, there's no quiet fallback. The person is told once, and it's tried again each minute."""
    if not (sb := box()): box_state["ok"] = None; return False
    sb.ensure_broker()
    if time.time() - box_state["at"] < (600 if box_state["ok"] else 60): return not box_state["ok"]
    r, was = sb.check(), box_state["ok"]
    box_state.update(ok=r["works"], at=time.time())
    if not r["works"] and was is not False:
        post(f"No agent can start: the sandbox doesn't work on this machine. {r['why']} {r['fix']} Agents start once that's fixed, or if you choose to run without it in Settings: they then have the full rights of this account.", ["person"])
        log(event="sandbox-failed", why=r["why"])
    elif r["works"] and was is False: post("The sandbox works again; starting agents.", ["person"])
    return not r["works"]

def cpu_of(name, skip):
    """CPU seconds used so far by live processes that carry HIVE_NAME=name (detached ones too: nohup/setsid
    keep the env) and by their reaped children, except pid `skip` (the agent's own claude)."""
    tot, tick, key = 0.0, os.sysconf("SC_CLK_TCK"), f"HIVE_NAME={name}".encode()
    skips = set(tree_of(skip)[:BOX_SHAPE]) if box() else {skip}
    for d in Path("/proc").iterdir():
        if not d.name.isdigit() or int(d.name) in skips: continue
        try:
            if key not in (d / "environ").read_bytes().split(b"\0"): continue
            st = (d / "stat").read_text().rsplit(")", 1)[1].split()
            # utime+stime, plus cutime+cstime: a reaped child's CPU moves into its parent's,
            # so a job that runs many short subprocesses (KRT, a sim sweep) keeps counting
            tot += (int(st[11]) + int(st[12]) + int(st[13]) + int(st[14])) / tick
        except (OSError, IndexError, ValueError): pass
    return tot

work = {}  # name -> [t of last sample, cpu then, t last seen working]

def queued_heavy(name):
    """A job of this agent's is waiting in the `heavy` queue (tickets w.<ns>.<pid>, f.<pid>). A waiter uses no CPU,
    but it isn't stuck: reaping it kills the ticket and sends the job to the back of a queue that can take an hour,
    so a job not detached could never reach a slot. A frozen queue is the watchdog's to catch."""
    key = f"HIVE_NAME={name}".encode()
    for q in (STATE / "heavy").glob("[wf].*"):
        try:
            if key in Path(f"/proc/{q.name.rsplit('.', 1)[-1]}/environ").read_bytes().split(b"\0"): return True
        except OSError: pass
    for q in (HOME / "projects" / ".heavy").glob("w.*"):  # the server module's queue: a waiter rewrites its ticket every 2 s with its agent's name, since a sandbox has its own pids
        try:
            if time.time() - q.stat().st_mtime < 30 and q.read_text() == name: return True
        except OSError: pass
    return False

def working(r, grace):
    """Something the agent started (a sim, a build) used real CPU within the last `grace` s, or waits in the heavy
    queue: it is waiting on that, not stuck. Sampled once a minute; busy means >= 5% of a core over the minute."""
    t, w = time.time(), work.setdefault(r["name"], [0, 0.0, 0])
    if queued_heavy(r["name"]):
        w[0], w[1], w[2] = t, cpu_of(r["name"], r["pid"]), t; return True
    if not w[0]:  # first look: take a sample and decide after a minute of it. Until that minute is
        # measured it counts as working (w[2] = t); with w[2] = 0 the next poll, seconds later, reaped it
        w[0], w[1], w[2] = t, cpu_of(r["name"], r["pid"]), t; return True
    if t - w[0] >= min(60, grace):
        c = cpu_of(r["name"], r["pid"])
        if w[0] and c - w[1] >= 0.05 * (t - w[0]): w[2] = t
        w[0], w[1] = t, c
    return t - max(w[2], woke) < grace

reap_hist = {}  # name -> (reaps in a row, t of the last)

def grace_for(name, grace):
    """Each reap of an agent within 3 h of its last doubles its quiet time before the next (x2, then x4): an agent
    re-parked on an idle wait (CI, a poll loop) was reaped and resumed every 15 min, 8 times a day for one (2026-11-06:
    reaps 15 -> 55 a day, nearly all agents re-waiting on CI or a job). A stuck loop still dies, just later."""
    n, t = reap_hist.get(name, (0, 0))
    return grace * 2 ** min(n, 2) if time.time() - t < 10800 else grace

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

def lost_subagents(out):
    """Background subagents that ending the turn stopped before they reported. `claude -p` waits on background shells
    but not on these: a7dcc ended its turn on "I'll pick up when the review reports", the review was stopped in the
    same second, the run exited 0, and the board sat for 2 h with nobody on it (6 such runs in 30 h on 2026-11-02)."""
    agents, lost, killed = {}, [], 0
    try:
        for line in Path(out).read_text(errors="replace").splitlines():
            try: e = json.loads(line)
            except ValueError: continue
            if not isinstance(e, dict): continue  # stray stdout that happens to parse: a post id like 110217581e40 is a JSON number
            if e.get("type") == "result": killed = ((e.get("subagent_stats") or {}).get("killed") or {}).get("system", 0)
            if e.get("type") != "system": continue
            if e.get("subtype") == "task_started" and e.get("task_type") == "local_agent" and not e.get("owned_by_subagent"):
                agents[e.get("task_id")] = e.get("description", "")
            elif e.get("subtype") == "task_notification" and e.get("status") == "stopped" and e.get("task_id") in agents:
                lost.append(e.get("summary") or agents[e["task_id"]])
    except OSError: pass
    return lost if killed else []

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
    for q in tree_of(pid)[BOX_SHAPE if box() else 1:]:
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

def open_steps(out):
    """The main agent's tool calls with no result yet, from a run's stream-json output: [(id, name, input)]. A subagent's own calls
    (parent_tool_use_id set) are its business; its Task call shows here as one open step."""
    calls = {}
    try:
        with open(out, "rb") as fh:
            for line in fh:
                if b'"tool_use' not in line and b'"tool_result' not in line: continue
                try: m = json.loads(line)
                except ValueError: continue
                if not isinstance(m, dict) or m.get("parent_tool_use_id") or m.get("type") not in ("assistant", "user"): continue
                for b in ((m.get("message") or {}).get("content") or []):
                    if not isinstance(b, dict): continue
                    if b.get("type") == "tool_use": calls[b.get("id")] = (b.get("id"), b.get("name"), b.get("input") or {})
                    elif b.get("type") == "tool_result": calls.pop(b.get("tool_use_id"), None)
    except OSError: return []
    return list(calls.values())

INTERRUPT_MIN_S = 5  # a command younger than this ends on its own about as soon: let it

def interrupt_step(name, post_id=None):
    """The person's Interrupt (hive post --interrupt): end the command a running Claude Code agent is waiting on, the way Esc
    does, so the hook hands the post over now (the call ends as a failure, and PostToolUseFailure runs inbox.py) instead of
    after it. Only when it can be done cleanly: one open step, and it is a Bash call in the foreground, with exactly one shell
    of that shape under the claude process that is no background shell (each of those is named in the stream by its task
    id). That shell and everything under it get SIGTERM, nothing harder, so a command can tidy up. Anything else (a file
    edit, a web fetch, a subagent, several shells) is left to end on its own. Returns what it did, in a few words."""
    try: rec = json.loads((RUN / f"{name}.json").read_text())
    except (OSError, ValueError): return "no run record"
    if rec.get("harness", "claude-code") != "claude-code": return "not a Claude Code run"
    if run_gone(rec): return "not running"
    steps = open_steps(rec.get("out") or "")
    if len(steps) != 1: return f"{len(steps)} open steps"
    sid, tool, inp = steps[0]
    if tool != "Bash" or inp.get("run_in_background"): return f"the open step is {tool}" + (" in the background" if tool == "Bash" else "")
    try: stream = Path(rec["out"]).read_bytes()
    except (OSError, KeyError): return "no output to read"
    # Claude Code now names a foreground command's shell too, in a task_started line under its own call: that shell is the
    # step's, not a background one (ab2df's 15-min poll went unstopped as "0 foreground shells", 2026-11-09).
    started = {}
    for line in stream.splitlines():
        if b'"task_started"' not in line: continue
        try: m = json.loads(line)
        except ValueError: continue
        if isinstance(m, dict) and m.get("subtype") == "task_started" and m.get("task_id"): started[str(m["task_id"]).encode()] = m.get("tool_use_id")
    tick, up = os.sysconf("SC_CLK_TCK"), float(Path("/proc/uptime").read_text().split()[0])
    shells = []
    for q in tree_of(rec["pid"])[1:]:
        try:
            st_ = Path(f"/proc/{q}/stat").read_text().rsplit(")", 1)[1].split()
            if Path(f"/proc/{st_[1]}/comm").read_text().strip() != "claude": continue   # a direct child of the claude process
            cmd = Path(f"/proc/{q}/cmdline").read_bytes().split(b"\0")
            out = os.readlink(f"/proc/{q}/fd/1")
        except (OSError, IndexError): continue
        if b"shell-snapshots" not in b" ".join(cmd) or "/tasks/" not in out: continue
        tid = Path(out).name.split(".")[0].encode()
        if (started[tid] != sid) if tid in started else tid in stream: continue   # a background shell: its id is in the stream, not as this step's
        shells.append((q, up - int(st_[19]) / tick))
    if len(shells) != 1: return f"{len(shells)} foreground shells"
    q, age = shells[0]
    if age < INTERRUPT_MIN_S: return "the command just started"
    for k in reversed(tree_of(q)):
        try: os.kill(k, signal.SIGTERM)
        except OSError: pass
    return "stopped the running command"

def waiting_posts(st, r):
    """Posts queued for a run that has ended its turn and sits parked on a background shell. The inbox hook fires only on a
    tool call or a stop, so such a run is deaf until the shell ends: a5ea4 was parked on a queued `heavy` job with up to 90 min
    left when the person asked it for a TLDR (2026-11-02). The person's or another Hive's post wakes it after a minute, an agent's after 10."""
    ps = st.get("pending", {}).get(r["name"])
    # already dead, or killed and not gone yet (a run from before a starter restart is no child of ours): the end path takes it
    if not ps or r["name"] in st.get("resume", {}) or not Path(f"/proc/{r['pid']}").exists(): return []
    try: seen = set((STATE / "delivered" / r["name"]).read_text().split())
    except OSError: seen = set()
    ps = [p for p in ps if p["id"] not in seen]
    c = cfg()
    grace = (c.get("wake_phone_s", 10) if any(p.get("from") == "phone" or p.get("interrupt") for p in ps)  # the person is on hold for it (modules/phone: phone answer)
             else c.get("wake_person_s", 60) if any(p.get("from") in ("person", "hives", r["name"]) for p in ps) else c.get("wake_agent_s", 600))  # its own post: `hive model`; hives: another Hive waiting on a reply, paced like the person (line 644)
    return ps if ps and finished_but_alive(r, grace) else []

def release_shells(pid):
    """End the agent's parked claude but leave its background shells running, with their place in the heavy queue. Only SIGKILL
    does that: claude can't clean up, and the shells write to their task files, not to it. Anything else it started is ended."""
    shells, rest = [], []
    for q in tree_of(pid)[1:]:
        try:
            if int(Path(f"/proc/{q}/stat").read_text().rsplit(")", 1)[1].split()[1]) != pid: continue
            out = os.readlink(f"/proc/{q}/fd/1")
            (shells.append({"pid": q, "output": out}) if "/tasks/" in out else rest.append(q))
        except (OSError, ValueError, IndexError): pass
    try: os.kill(pid, signal.SIGKILL)
    except OSError: pass
    for q in rest: kill_tree(q)
    return shells

def runs_now(name):
    """Whether `name` has a run whose claude process is alive right now (no reaping, unlike running())."""
    try: return not run_gone(json.loads((RUN / f'{name}.json').read_text()))
    except (OSError, ValueError, KeyError): return False

def boot_id():
    try: return Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    except OSError: return ""

def proc_start(pid):
    """When a process started (/proc/PID/stat field 22), so a reused pid isn't taken for our run. The command name can hold spaces, hence the rsplit."""
    try: return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
    except (OSError, IndexError): return None

def run_gone(r):
    """A run record's process is gone: no such pid, or the pid is now something else's (after a machine restart a pid can come back
    as anything, and the reaper would kill it). Records from before these fields go by the pid alone."""
    if not Path(f"/proc/{r['pid']}").exists(): return True
    if r.get("boot") and r["boot"] != boot_id(): return True
    return bool(r.get("proc")) and proc_start(r["pid"]) not in (None, r["proc"])

TRANSIENT = re.compile(r"connection|network|ECONN|ETIMEDOUT|ENOTFOUND|EAI_AGAIN|fetch failed|socket|overloaded|API Error: 5\d\d", re.I)
WHY_WORDS = {"connection": "no connection to Claude", "restart": "the machine restarted", "stalled": "went silent with nothing running", "other": "ended before it finished", "budget": "reached its budget or turn limit"}
DIED_RETRY_S = [60, 300, 900, 1800, 3600]  # how long before it's tried again; the last repeats
went_silent = set()  # stalled runs killed but not yet seen gone

def died_unfinished(out):
    """Why a run ended without finishing its turn, or None if it finished. A Claude Code run prints one clean result at the end of a turn;
    with none, it was killed or gave up. Recorded from the real CLI on 2026-11-05: after 10 retries with connections refused it prints
    `API Error: Connection refused ...` as an error result and exits 1, and limit_hit and auth_hit don't apply, so the agent used to vanish."""
    result, last, said = None, None, []
    try: lines = Path(out).read_text(errors="replace").splitlines()[-200:]
    except (OSError, TypeError): return None
    for line in lines:
        try: e = json.loads(line)
        except ValueError:
            said.append(line); continue
        if not isinstance(e, dict): continue
        last = e.get("type")
        if last == "result": result = e
        elif last == "assistant" and e.get("error"): said.append(str(e["error"]) + " " + json.dumps(e.get("message", ""))[:300])
    if result is not None and not result.get("is_error"): return None
    if last in ("turn.completed", "session.resume_hint"): return None
    if result is not None and str(result.get("subtype", "")).startswith("error_max"): return "budget"
    if result is not None: said.append(str(result.get("result", "")))
    return "connection" if any(TRANSIENT.search(t) for t in said[-6:]) else "other"

def no_session(out):
    """The CLI couldn't find the session it was asked to resume."""
    try: return "No conversation found" in Path(out).read_text(errors="replace")[-4000:]
    except (OSError, TypeError): return False

def carries_on(r):
    """Runs the starter brings back by itself: on a Claude login, not on a fallback or provider, whose sessions don't move and which the person is told about already."""
    return r.get("harness", "claude-code") == "claude-code" and not r.get("fallback") and not r.get("provider") and not (STATE / "done" / r["name"]).exists() and not (STATE / "killed" / f"{r['name']}.json").exists()

def stalled(r, quiet):
    """Mid-turn, no output for `quiet` s, and nothing it started uses CPU or waits in the heavy queue: nothing will ever end it, and -p has no timeout of its own."""
    out = Path(r.get("out", ""))
    try:
        if time.time() - max(out.stat().st_mtime, woke, epoch(r.get("at", "")) or 0) < quiet: return False
    except OSError: return False
    return not finished_but_alive(r, quiet) and not working(r, quiet)

def gave_up_text(name, why, n, c):
    """The post to the person when a run stops for good. A budget stop says which limit it hit and where to change it."""
    t = f"`{name}` stopped and won't restart by itself ({WHY_WORDS[why]}{f', {n} times' if n > 1 else ''}). What it did so far is kept. "
    if why == "budget":
        b = c.get("max_budget_usd")
        t += (f"The limit for one run is ${b:g}; " if b else "") + "raise it in Settings (Budget per run) or run `hive budget N`, then "
    return t + ("a message to it carries it on." if why == "budget" else "A message to it carries it on.")

def carry_on(st, r, why, told):
    """Queue a run that stopped early to resume in its own session after a wait. Bounded: a run that keeps dying is tried 3 times and the person is told
    (12 tries for lost connections, about 8 h, and a run that got 10 min of work done between drops starts counting again)."""
    key, tries = f"{r['name']}:{r.get('post')}", st.setdefault("died_tries", {})
    lasted = time.time() - (epoch(r.get("at", "")) or 0)
    n = 1 if why == "connection" and lasted >= 600 else tries.get(key, 0) + 1
    tries[key] = n
    log(event="died", name=r["name"], session=r["session"], why=why, tries=n)
    if n >= {"connection": 12, "budget": 1}.get(why, 3):
        tries.pop(key)
        log(event="given_up", name=r["name"], session=r["session"], why=why, tries=n)
        post(gave_up_text(r["name"], why, n, cfg()), ["person"])
        return
    delays = cfg().get("died_retry_s") or DIED_RETRY_S
    st.setdefault("resume", {})[r["name"]] = {**r, "session": session_in(r.get("out")) or r["session"], "wait": True, "why": why,
                                              "reset": 0 if why == "restart" else time.time() + delays[min(n - 1, len(delays) - 1)]}
    if n == 1: told.setdefault(why, []).append(r["name"])

def running(st=None, by_hand=False):
    """by_hand: the first pass after a deliberate stop in this boot (see stop_stamp): a run that's gone was stopped, not lost."""
    alive, told, stopped = {}, {}, []
    note_sleep()
    grace, quiet = cfg().get("done_grace_s", 900), cfg().get("stall_s", 1800)
    for f in RUN.glob("*.json"):
        r = json.loads(f.read_text())
        c = children.get(r["name"])
        g = grace_for(r["name"], grace)
        gone = not c and run_gone(r)  # checked first: nothing below may signal a pid that isn't this run's
        # parked on a waiter on a login that has run out: it would only find out when the waiter ends, up to hours later
        move = not gone and st is not None and out_of_room(cfg(), r.get("login"), st.get("limits")) and finished_but_alive(r, min(60, grace)) and only_waiters(r["pid"])
        if gone: pass
        elif move or (finished_but_alive(r, g) and not working(r, g)):
            bg = bg_tasks(r.get("out", ""))
            log(event="reaped", name=r["name"], session=r["session"], pid=r["pid"], bg=bg, move=move)
            kill_tree(r["pid"])
            if c: c.wait(timeout=10)
            if not move: reap_hist[r["name"]] = ((reap_hist.get(r["name"], (0, 0))[0] + 1) if g > grace or time.time() - reap_hist.get(r["name"], (0, 0))[1] < 10800 else 1, time.time())
            if st is not None and (bg or move):  # it may have been waiting on something: resume it so it can look
                st.setdefault("resume", {})[r["name"]] = {**r, "session": session_in(r.get("out")) or r["session"], "reset": 0, "reaped": bg or ["(none listed)"], "move": move}
            work.pop(r["name"], None)
        elif st is not None and (ps := waiting_posts(st, r)):
            shells = release_shells(r["pid"])
            log(event="woken", name=r["name"], session=r["session"], pid=r["pid"], posts=[p["id"] for p in ps], shells=shells)
            if c: c.wait(timeout=10)
            st.setdefault("resume", {})[r["name"]] = {**r, "session": session_in(r.get("out")) or r["session"], "reset": 0, "reaped": ["(none: shells left running)"],
                "shells": shells, "from": "person" if any(p.get("from") == "person" for p in ps) else ps[0].get("from")}  # the person's post isn't paced
            work.pop(r["name"], None)
        elif st is not None and quiet and carries_on(r) and stalled(r, quiet):
            log(event="stalled", name=r["name"], session=r["session"], pid=r["pid"], quiet=quiet)
            went_silent.add(r["name"])
            kill_tree(r["pid"])
            if c: c.wait(timeout=10)
            work.pop(r["name"], None)
        done = c.poll() is not None if c else gone or not Path(f"/proc/{r['pid']}").exists()
        if done:
            f.unlink(); children.pop(r["name"], None)
            log(event="end", name=r["name"], session=r["session"], pid=r["pid"], exit=c.returncode if c else None, **({"cost": cost} if (cost := run_cost(r.get("out"))) is not None else {}))
            hit = limit_hit(r.get("out"))
            authed = auth_hit(r.get("out")) is not None
            died = None if hit or authed else died_unfinished(r.get("out"))
            if r["name"] in went_silent: died = "stalled"; went_silent.discard(r["name"])
            elif died and r.get("boot") and r["boot"] != boot_id(): died = "restart"
            if st is not None and not hit and not authed and not died:
                st.get("died_tries", {}).pop(f"{r['name']}:{r.get('post')}", None)
                if (epoch(r.get("at", "")) or 0) >= st.get("auth_told", {}).get(r.get("login"), {}).get("since", float("inf")):
                    auth_back(st, r["login"])  # no credentials file to watch (a Mac's keychain): a run that got through shows it's signed in
            if not hit and not authed and not (died and st is not None and carries_on(r)): relay_final(r, c.returncode if c else None)
            if st is not None and st.get("launch_hold") and r.get("harness", "claude-code") == "claude-code" and wrote_json(r.get("out")):
                st.pop("launch_hold"); log(event="launch_ok", name=r["name"])
                post("Claude Code starts agents again; the posts that waited are being handled.", ["person"])
            if c and c.returncode and not hit and r.get("from") == "person" and r.get("fallback") and not posted_since(r["name"], r.get("at", "")):
                # a0f70 died on an API 503 at 15:52 on 2026-11-02 and the person's request just vanished
                post(f"`{r['name']}` on the fallback model died (exit {c.returncode}) without posting, so post {r.get('post')} wasn't done. Post again, or wait for a Claude login.", ["person"], r.get("thread"))
            if hit and st is not None:  # stopped on a usage limit: resume it when the limit resets
                login, limits = r.get("login", ""), st.setdefault("limits", {})
                scope = limit_scope(r.get("out"))  # a model's own bar (Fable's share) holds that model, not the login
                key = f"{login}#{scope}" if scope else login
                fresh = not any(time.time() < t for k, t in limits.items() if str(k).partition("#")[2] == (scope or ""))  # first stop of this limit episode
                limits[key] = max(limits.get(key, 0), hit[0])
                st.setdefault("resume", {})[r["name"]] = {**r, "session": hit[1] or r["session"], "reset": hit[0]}
                log(event="limited", name=r["name"], session=hit[1] or r["session"],
                    resets=dt.datetime.fromtimestamp(hit[0], dt.timezone.utc).isoformat(timespec="seconds"))
                if fresh:  # one post per episode, not one per stopped agent
                    more = len(cfg().get("logins", [])) > 1
                    post(f"Usage limit reached (`{r['name']}` first{f', the {scope.title()} limit' if scope else ''}). {f'{scope.title()} agents' if scope else 'Agents'}{f' on account {account_name(login)}' if more else ''} wait and carry on by themselves at "
                         f"{dt.datetime.fromtimestamp(limits[key]).astimezone():%H:%M %Z}.{' Other models on it keep working.' if scope else ''}{' Other accounts with room keep working.' if more else ''}", [])  # the person doesn't want these in the inbox (2026-11-08)
            elif st is not None and r.get("login") in {str(Path(l).expanduser()) for l in cfg().get("logins", [])} \
                    and (sid := auth_hit(r.get("out"))) is not None:  # same treatment as a limit, or the run just vanishes
                login, limits = r["login"], st.setdefault("limits", {})
                until = time.time() + AUTH_HOLD_S
                limits[login] = max(limits.get(login, 0), until)
                st.setdefault("resume", {})[r["name"]] = {**r, "session": sid or r["session"], "reset": until, "why": "login"}
                log(event="login_rejected", name=r["name"], login=login)
                st.setdefault("auth_told", {}).setdefault(login, {"since": time.time(), "creds": creds_stamp(login), "told": False})  # auth_watch says it, once, with the count
            elif st is not None and "shells" not in st.get("resume", {}).get(r["name"], {}) and (lost := lost_subagents(r.get("out"))) and st.setdefault("lost_resumes", {}).get(f"{r['name']}:{r.get('post')}", 0) < 2:
                # it may have been waiting on them, and nothing will ever report. Twice at most, so an agent that keeps doing it can't loop.
                key = f"{r['name']}:{r.get('post')}"; st["lost_resumes"][key] = st["lost_resumes"].get(key, 0) + 1
                st.setdefault("resume", {})[r["name"]] = {**r, "session": session_in(r.get("out")) or r["session"], "reset": 0, "reaped": lost, "lost": True}
                log(event="lost_subagents", name=r["name"], session=r["session"], lost=lost)
            elif st is not None and r.get("harness", "claude-code") == "claude-code" and (why := launch_failed(r.get("out"), c.returncode if c else None)):
                # claude refused to start at all (a flag an update dropped, a broken install): not this agent's fault, so it keeps
                # its session and its posts, and nothing starts until it's tried again (tested 2026-11-04: a rejected flag made every woken agent dead)
                ps = r.get("slept") or [{"id": r["post"], "thread": r.get("thread"), "from": r.get("from"), **({"model": r["model"]} if r.get("model") else {})}]
                st["pending"][r["name"]] = ps + [p for p in st["pending"].get(r["name"], []) if p["id"] not in {q["id"] for q in ps}]
                fresh = not st.get("launch_hold")
                st["launch_hold"] = {"until": time.time() + LAUNCH_HOLD_S, "why": why}
                log(event="launch_failed", name=r["name"], session=r["session"], why=why)
                if fresh:
                    post(f"Claude Code won't start agents: `{why}` (exit {c.returncode if c else '?'}, `{r['name']}`). Most likely a Claude Code "
                         f"update changed what it accepts. No agent loses anything: their posts wait, and starting is tried again every "
                         f"{LAUNCH_HOLD_S // 60} min. Running `claude --version` and `hive update` may fix it.", ["person"])
            elif st is not None and r.get("slept") and resume_failed(r.get("out"), c.returncode if c else None):
                mark_done(r["name"], "its session could not be resumed")
                st["pending"][r["name"]] = [{**p, "no_revive": True} for p in r["slept"]] + st["pending"].get(r["name"], [])
                log(event="wake_failed", name=r["name"], session=r["session"])
            elif st is not None and r.get("resumed") and no_session(r.get("out")):  # its session is gone: an heir reads the thread
                mark_done(r["name"], "its session could not be resumed")
                st["pending"][r["name"]] = [{"id": r["post"], "thread": r.get("thread"), "from": r.get("from"), "no_revive": True}] + st["pending"].get(r["name"], [])
                log(event="wake_failed", name=r["name"], session=r["session"])
            elif st is not None and died and carries_on(r) and r["name"] not in st.get("resume", {}):
                if by_hand and died != "stalled": stopped.append(r["name"]); log(event="stopped_by_hand", name=r["name"], session=r["session"])
                else: carry_on(st, r, died, told)
            elif st is not None and not hit and not authed and not died and r["name"] not in st.get("resume", {}) and not (STATE / "killed" / f"{r['name']}.json").exists():
                ended_clean.append(r)
        else: alive[r["name"]] = r
    for why, names in told.items():  # one post per stop, not one per agent
        post(f"{', '.join(f'`{n}`' for n in names)} stopped ({WHY_WORDS[why]}) and will carry on by themselves.", [])
    if stopped:
        post(f"{', '.join(f'`{n}`' for n in stopped)} ended when the Hive was stopped by hand, so they stay stopped. A message to one starts it again.", [])
    return alive

def account_name(login):
    """A login by the name the person gave it in the config, else its folder."""
    names = {str(Path(k).expanduser()): v for k, v in (cfg().get("login_names") or {}).items()}
    return names.get(str(login)) or Path(login).name.removeprefix(".claude-").lstrip(".")

def creds_stamp(login):
    """When a login's credentials last changed, so signing in again is seen at once. None with no file (a Mac keeps them in the keychain)."""
    try: return (Path(login) / ".credentials.json").stat().st_mtime
    except OSError: return None

def auth_back(st, login):
    """A rejected login works again: lift its hold, and the runs that waited on it carry on now."""
    a = st.get("auth_told", {}).pop(login, None)
    st.get("limits", {}).pop(login, None)
    back = [r for r in st.get("resume", {}).values() if r.get("why") == "login" and r.get("login") == login]
    for r in back: r["reset"] = 0
    log(event="login_back", login=login, agents=[r["name"] for r in back])
    if a and a.get("told"):
        post(f"Signed in again. {f'{len(back)} agents carry on.' if len(back) > 1 else '1 agent carries on.'}" if back else "Signed in again.", [])

def auth_watch(c, st):
    """The person's view of a rejected login: one plain post a few seconds after the first rejection, so its count takes in every agent
    stopped by it; and the moment its credentials change (signed in again), everything waiting on it goes, not at the next hourly try."""
    for login, a in list(st.get("auth_told", {}).items()):
        if creds_stamp(login) != a.get("creds"):
            auth_back(st, login); continue
        if a.get("told") or time.time() - a.get("since", 0) < c.get("auth_tell_s", 15): continue
        a["told"] = True
        logins = [str(Path(l).expanduser()) for l in c.get("logins", [])]
        if any(l != login and time.time() >= st.get("limits", {}).get(l, 0) for l in logins):
            post(f"Sign in again to Claude: account {account_name(login)} has expired. Its agents moved to your other account. Settings > Accounts.", ["person"])
        else:
            n = sum(r.get("login") == login for r in st.get("resume", {}).values()) + len(st.get("pending", {}))
            post(f"Sign in again to Claude: {n} agent{' is' if n == 1 else 's are'} waiting. Settings > Accounts.", ["person"])

def guarded(fn, *a):
    """Call continuation.fn. The pilot may not take the starter down with it: an error is logged, and the post goes on without it. Returns None on an error."""
    if not cont: return None
    try: return getattr(cont, fn)(*a)
    except Exception as e:
        log(event="continue_error", fn=fn, why=f"{type(e).__name__}: {e}"[:300]); return None

def continuations(st, c, alive):
    """Runs that ended clean can write an intent (a run that ended on `Next:` in a thread with `Continue: on`), and intents become one wake each."""
    while ended_clean: guarded("on_run_end", ended_clean.pop(0), c, st, alive)
    guarded("enqueue", st, c, alive, stays_on)

def drop_killed(st):
    """The person killed a run from the dashboard: ~/.hive/killed/<name>.json holds its pid, written before the signal.
    Call after running(), which ends the run and may have queued a resume (a limit in its log, open background tasks).
    Once the run is gone, drop that resume and its queued wakes, so the kill stays a kill. While it is still dying it's
    in `alive`, so nothing restarts it. It also goes dead: a later plain message in its thread gets a new agent, and only
    the person typing its name brings it back."""
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
        mark_done(n, "the person killed its last run")
        f.unlink(missing_ok=True)

def apply_model_sets(st, c):
    """The person picked a model in the agent panel: ~/.hive/model-set/<name>.json (set_agent_model in the dash), or the outcomes
    grader stepped an agent up (outcomes.step_up, the file carries a "why"). Held as if a post had passed --model (stays_on): st["chosen"], and the posts already queued for it. Its next run starts on it."""
    for f in (STATE / "model-set").glob("*.json"):
        n = f.stem
        try: d = json.loads(f.read_text()); m, why = d.get("model"), d.get("why")
        except (OSError, ValueError, AttributeError): m, why = None, None
        if not re.fullmatch(r"[a-z][0-9a-f]{4,}", n): m = None
        if model_ok(c, m): stays_on(st, c, n, {"model": m})
        log(event="model_set" if model_ok(c, m) else "model_set_refused", name=n, model=m, **({"why": str(why)[:200]} if why else {}))
        f.unlink(missing_ok=True)

def hold_effort(st, n, e):
    """Hold effort `e` on agent `n`, and on its heir if it has one: wake() sends a replaced agent's posts to the heir, whose own held effort start() reads."""
    efforts[0][n] = e
    if (heir := st.get("heirs", {}).get(n)): efforts[0][heir] = e

def apply_effort_sets(st):
    """The person picked an effort in the agent panel: ~/.hive/effort-set/<name>.json (set_agent_effort in the dash), {"effort": level},
    or "" for Claude's default. Held in st["efforts"], so the agent's next run starts on it and later ones stay on it."""
    for f in (STATE / "effort-set").glob("*.json"):
        n = f.stem
        try: e = json.loads(f.read_text()).get("effort")
        except (OSError, ValueError, AttributeError): e = None
        ok = re.fullmatch(r"[a-z][0-9a-f]{4,}", n) and (e == "" or e in EFFORTS)
        if ok and e: hold_effort(st, n, e)
        elif ok:
            efforts[0].pop(n, None)
            for q in st["pending"].get(n, []): q.pop("effort", None)  # or the next run would still take one off a queued post
        log(event="effort_set" if ok else "effort_set_refused", name=n, effort=e)
        f.unlink(missing_ok=True)

def usage(c, u):
    """Refresh usage per login every usage_check_s. Returns {login: weekly pct or None}; the 5-hour pcts from the same
    call go to ~/.hive/usage_5h.json ({login: pct or None}), and the reset times to ~/.hive/usage_resets.json ({login:
    {"weekly": epoch|None, "five_hour": epoch|None, "has_five_hour": bool}}), and each model's own weekly share (the
    reply's limits[] kind weekly_scoped, e.g. Fable) to ~/.hive/usage_scoped.json ({login: {"fable": {"percent": N, "resets": epoch}}}).
    Never refreshes tokens."""
    if time.time() - u.get("_checked", 0) < c["usage_check_s"]: return u
    prev, prev5, prevr = load("usage.json", {}), load("usage_5h.json", {}), load("usage_resets.json", {})  # the file, so a restart carries too
    prevs = load("usage_scoped.json", {})
    u, five, resets, scoped = {"_checked": time.time()}, {}, {}, {}
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
            scoped[str(d)] = scoped_shares(r)
            extra_usage_notice(d, r.get("extra_usage"))
        except Exception:  # a failed read isn't news: 429s (the dash polls the same API) read as "room" and flapped, 2026-11-02
            u[str(d)], five[str(d)], resets[str(d)] = carried(str(d), prev, prev5, prevr)
            if isinstance(prevs, dict) and isinstance(prevs.get(str(d)), dict): scoped[str(d)] = prevs[str(d)]
            # the runs on this login printed its real figure: fresher than a carried one, and there even with no good read yet
            if (seen := newest_seen(str(d))) and time.time() < seen[1]:
                u[str(d)] = max(seen[0], u[str(d)] or 0); resets[str(d)] = {"five_hour": None, "has_five_hour": False, **resets[str(d)], "weekly": seen[1]}
    save("usage.json", u); save("usage_5h.json", five); save("usage_resets.json", resets); save("usage_scoped.json", scoped)
    return u

def scoped_shares(r):
    """{family: {"percent", "resets"}} from a usage reply's limits[] rows of kind weekly_scoped (in 2026-11 only Fable's,
    display_name "Fable"; its own bar inside the weekly one). Rows for a model family the starter doesn't know are left out."""
    out = {}
    for row in r.get("limits") or []:
        if not isinstance(row, dict) or row.get("kind") != "weekly_scoped": continue
        fam = model_family((((row.get("scope") or {}).get("model") or {}).get("display_name")))
        if fam and isinstance(row.get("percent"), (int, float)):
            out[fam] = {"percent": row["percent"], "resets": epoch(row.get("resets_at"))}
    return out

def share_full(login, model):
    """True if usage_scoped.json reads this model's own weekly share at 100 on login, and its reset hasn't passed. Only a model
    that's known: an unknown one (the login's default) isn't kept off a login on a reading, only on a real limit (scope_held)."""
    fam = model_family(model) if model else None
    if not fam: return False
    sh = load("usage_scoped.json", {})
    sh = ((sh.get(login) or {}).get(fam) if isinstance(sh, dict) and isinstance(sh.get(login), dict) else None) or {}
    pct, reset = sh.get("percent"), sh.get("resets")
    return isinstance(pct, (int, float)) and pct >= 100 and not (isinstance(reset, (int, float)) and time.time() >= reset)

def extra_usage_notice(login, extra):
    """Tell the person once when a login's extra usage is on: past its real limit that login goes on spending money, and only usage_stop_pct's check at 100
    keeps new runs off it (warden ruling 11061816f737). Asked again if it goes off and comes back on. A login with no reading is left as it was."""
    if not isinstance(extra, dict) or "is_enabled" not in extra: return
    told, k = load("extra_usage_told.json", []), str(login)
    told = told if isinstance(told, list) else []
    if not extra["is_enabled"]:
        if k in told: save("extra_usage_told.json", [t for t in told if t != k])
        return
    if k in told: return
    save("extra_usage_told.json", told + [k])
    post(f"Extra usage is switched on for the login {Path(login).name}. If it has credits, runs on it can spend money once the login's own limit is reached; nothing new starts on it past 100%.\n"
         "For you: if you don't want that, turn extra usage off in that account's settings.", to=["person"])

def newest_seen(login):
    """seen_usage from the newest live run on this login, or None."""
    rs = []
    for f in RUN.glob("*.json"):
        try: rs.append(json.loads(f.read_text()))
        except (OSError, ValueError): pass
    for r in sorted((r for r in rs if r.get("login") == login), key=lambda r: r.get("at", ""), reverse=True):
        if (seen := seen_usage(r.get("out"))): return seen
    return None

SLOW_S, SLOW_MAX_S = 240, 900   # a person's mention with no reply after 4 min gets one line saying who is on it; nothing after 15 min (the watchdog's)
NOT_REPLY = {"person", "hives", "box", "starter", "watchdog"}
ACK = re.compile(r"(?:@\S+\s+)*((?:perf|perfect|excellent|great|nice|cool|good|awesome|amazing|thanks|thank you|thx|ty|ok|okay|got it|sounds good)[\s.!,]*)+", re.I)

def slow_replies(posts, st, alive, t=None):
    """One notice per person post that named an agent and has had no reply in its thread after SLOW_S: who is on it (a live run,
    a queued start, or nobody), so the person doesn't have to ask "hello?" (17 such posts a day, 1-4 min after a mention, 2026-11-04).
    Returns (thread, text) pairs and marks them in st["slow_noted"]; the watchdog's own alarm at 10 min ignores starter posts, so this never silences it."""
    t = t or time.time(); noted = set(st.get("slow_noted", [])); out = []
    for p in posts:
        if str(p.get("from", "")).lower() != "person" or p.get("id") in noted: continue
        try: age = t - dt.datetime.fromisoformat(p["at"]).timestamp()
        except (KeyError, ValueError): continue
        if not SLOW_S <= age <= SLOW_MAX_S: continue
        names = [n for n in mentions(p) if n not in ("all", "here", "person", "hives")]
        if not names or ACK.fullmatch(str(p.get("text", "")).strip()): continue
        th = p.get("thread") or p.get("id")
        noted.add(p["id"])
        if any((q.get("thread") or q.get("id")) == th and q.get("at", "") > p["at"] and str(q.get("from", "")).lower() not in NOT_REPLY for q in posts): continue
        run = next((r for r in alive.values() if r.get("post") == p["id"]), None) \
            or next((r for r in alive.values() if str(r.get("name", "")).lower() in names), None)
        if run:
            try: since = (t - dt.datetime.fromisoformat(run["at"]).timestamp()) / 60
            except (KeyError, ValueError): since = age / 60
            out.append((th, f"`{run['name']}` is on this ({run.get('model') or 'claude'}, running {since:.0f} min, no reply yet); a first reply usually follows within a few minutes."))
        elif any(any(q.get("id") == p["id"] for q in ps) or n in names for n, ps in st.get("pending", {}).items()):
            out.append((th, "This is queued: no agent has started on it yet (every login is paused or at its stop line, or the start is waiting its turn). It starts as soon as there is room."))
        else:
            out.append((th, "No agent is running on this post yet; the watchdog looks again at 10 min."))
    st["slow_noted"] = sorted(noted)[-300:]
    return out

def recent_posts(n=400):
    out = []
    try:
        for line in BOARD.read_text().splitlines()[-n:]:
            try: out.append(json.loads(line))
            except ValueError: pass
    except OSError: pass
    return out

def posted_since(name, at):
    """Did agent `name` post to the board at or after `at`?"""
    try:
        since = dt.datetime.fromisoformat(at).timestamp()
        for line in BOARD.read_text().splitlines()[-2000:]:
            p = json.loads(line)
            if p.get("from") == name and dt.datetime.fromisoformat(p["at"]).timestamp() >= since: return True
    except Exception: return True  # can't tell: stay quiet
    return False

FRESH_WORD = re.compile(r"@(new|claude|opus|sonnet|haiku|fable|astra|codex|free)\b", re.I)
RELAY_MAX = 2500

def final_answer(out):
    """The text of a run's last clean result, or "". Its final chat message: what the model wrote as its answer."""
    result = None
    try:
        for line in Path(out).read_text(errors="replace").splitlines()[-400:]:
            try: e = json.loads(line)
            except ValueError: continue
            if isinstance(e, dict) and e.get("type") == "result": result = e
    except (OSError, TypeError): return ""
    if result is None or result.get("is_error"): return ""
    return str(result.get("result") or "").strip()

def woke_posts(r):
    """The board posts that started this run: the first one, and any more it was restarted for."""
    ids = list(dict.fromkeys([r.get("post")] + list(r.get("woke") or [])))
    found = {}
    try:
        for line in BOARD.read_text().splitlines()[-4000:]:
            p = json.loads(line)
            if p.get("id") in ids: found[p["id"]] = p
    except Exception: pass
    return [found[i] for i in ids if i in found]

def relay_final(r, returncode):
    """A run that was named (or woken by the person) and ended cleanly without posting, with a final answer in its
    output: post that answer to the waking post's thread in the agent's name, marked as relayed. 10 of 103 grades on
    2026-11-04 were "silent": each time the agent had written its answer as its last chat message and never run
    hive post (a35f3 three times to a2bf3, a8779 to a16c1, a3d35 to the rootwarden), so the asker got nothing.
    Mentions in it go in backticks: words it wrote about must not start agents now. Returns the post, or None."""
    if returncode not in (0, None) or r.get("thread") in ("starter",) or posted_since(r["name"], r.get("at", "")): return None
    posts = woke_posts(r)
    named = [p for p in posts if r["name"] in mentions(p) or FRESH_WORD.search(CODE.sub(" ", str(p.get("text", "")))) or str(p.get("from", "")).lower() == "person"]
    if not named: return None
    text = final_answer(r.get("out"))
    if not text: return None
    text = re.sub(r"(?<![\w.`])@([a-z0-9][a-z0-9_-]*)", lambda m: f"`{m.group(0)}`", text)  # a name in backticks is written about, not called
    if len(text) > RELAY_MAX: text = text[:RELAY_MAX] + " … (cut; the whole answer is in the run's output, " + Path(r.get("out", "")).name + ")"
    p = {"id": dt.datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now(), "from": r["name"], "to": [],
         "thread": named[0].get("thread") or r.get("thread"), "via": "relay", "reply_to": named[0]["id"],
         "text": "(This run ended without posting, so the starter relays its final answer; what it says was not checked by anyone.)\n\n" + text}
    with BOARD.open("a") as f: f.write(json.dumps(p) + "\n")
    log(event="relayed", name=r["name"], session=r.get("session"), post=p["id"], to=named[0]["id"])
    return p

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

def pick_login(c, u, limits=None, stop=None, model=False):
    """The login with the most weekly room, skipping any held on a 5-hour/weekly limit (limits: {login: until}) and any
    past its own stop lines (stop_lines). stop: one weekly line for every login instead, and no 5-hour lines
    (login_for's 100 for the person's posts, which go past the stop lines up to a real limit). model: the run's model, so a
    login held only for one model ("login#fable") is skipped for that model (or an unknown one, None) and kept for the others;
    left out (False), the scoped holds are ignored."""
    five = load("usage_5h.json", {}) if stop is None else {}
    five = five if isinstance(five, dict) else {}
    def room(k, v):
        w, h = stop_lines(c, k) if stop is None else (stop, None)
        f = five.get(k)
        return (v is None or v < w) and (h is None or not isinstance(f, (int, float)) or f < h)
    ok = [(v is None, v or 0, k) for k, v in u.items() if k != "_checked" and room(k, v)
          and time.time() >= (limits or {}).get(k, 0) and (model is False or not (scope_held(limits, k, model) or share_full(k, model)))]
    return min(ok)[2] if ok else None

def under_pressure(c, posts=()):
    """Why a new run should wait a moment, or None. This paces starts, it is not a cap: the run still starts once the
    machine can take it, however many are running (2026-11-02: 44 starts in 20 min took swap to full and load to 292).
    Runs start at once while memory lasts (the person, 2026-11-03: a fan-out should all spawn instantly). A run started
    moments ago doesn't show in MemAvailable yet, so each one counts as start_reserve_mb until start_settle_s has passed:
    without that a burst reads the same free memory for every start. start_gap_s, off by default, spaces starts as well.
    The person, the box and the warden never wait."""
    if any(p.get("from") == "person" or p.get("from") == "hives" for p in posts): return None
    if time.time() - last_start[0] < c.get("start_gap_s", 0): return "gap"
    try:
        avail = int(re.search(r"MemAvailable:\s+(\d+)", Path(MEMINFO).read_text())[1]) // 1024
        stall = float(re.search(r"full avg10=\S+ avg60=([\d.]+)", Path(PSI_MEM).read_text())[1])
    except (OSError, TypeError): return None
    settle = time.time() - c.get("start_settle_s", 30)
    recent_starts[:] = [t for t in recent_starts if t > settle]
    avail -= len(recent_starts) * c.get("start_reserve_mb", 400)
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

def run_model(posts):
    """The Claude model these posts would start on, or None for the login's default (see start)."""
    return next((p["model"] for p in posts if p.get("model") in MODELS), None) or default_model()

def login_for(c, u, limits, posts, login):
    """Past the stop line agents' posts wait, but a post from the person still starts its run: else the person
    has no way into Hive until the reset. That run's own mentions wait like any other. Stops at a real limit,
    and at a model's own limit for a run on that model (another login may still have it)."""
    model = run_model(posts)
    if login and not scope_held(limits, login, model) and not share_full(login, model): return login
    if login: return pick_login(c, u, limits, model=model)
    return pick_login(c, u, limits, 100, model) if any(p.get("from") == "person" for p in posts) else None

def move_session(sid, src, dst):
    if (sb := box()): return sb.move_session(sid, src, dst)  # a login folder is agents' to write: no following their links out here
    """Copy a session's transcript to another login so --resume finds it there. False if there's nothing to copy."""
    src, dst = Path(src), Path(dst)
    found = list(src.glob(f"projects/*/{sid}.jsonl"))
    for f in found:
        to = dst / f.relative_to(src); to.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(f, to)
        if f.with_suffix("").is_dir(): shutil.copytree(f.with_suffix(""), to.with_suffix(""), dirs_exist_ok=True)
    return bool(found)

# Asleep or dead (the person, 2026-11-02, thread 110216375cac; kb/hive.md): a name means one context. An agent that stopped is asleep:
# the next post resumes its session. A dead one isn't brought back by default: the post goes to an heir, a new agent with a new name.
def mark_done(n, why):
    d = STATE / "done"; d.mkdir(exist_ok=True); (d / n).write_text(why + "\n")

def last_run(n):
    """The start record of n's last run, with the time it ended (`ended`), or None if it never ran."""
    run, tag = None, f'"{n}"'
    try:
        with LOG.open(errors="replace") as f:
            for line in f:
                if tag not in line: continue
                try: e = json.loads(line)
                except ValueError: continue
                if e.get("name") != n: continue
                if e.get("event") == "start": run = e
                elif e.get("event") == "end" and run: run["ended"] = e.get("at")
    except OSError: pass
    return run

def context_of(out):
    """Tokens in the context at a run's last turn: what a resume pays for on every turn. 0 if unknown: codex and kimi runs don't print it."""
    n = 0
    try:
        with open(out, errors="replace") as f:
            for line in f:
                if '"assistant"' not in line or '"usage"' not in line: continue
                try: e = json.loads(line)
                except ValueError: continue
                if e.get("type") == "assistant" and not e.get("parent_tool_use_id") and (u := (e.get("message") or {}).get("usage")):
                    n = sum(u.get(k) or 0 for k in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"))
    except (OSError, TypeError): pass
    return n

def dead_why(c, n, run):
    """Why n isn't woken by default, or None if it's only asleep."""
    try: return (STATE / "done" / n).read_text().strip() or "it said its work was finished"
    except OSError: pass
    size = context_of(run.get("out"))
    return f"it stopped with {size // 1000}k tokens of context" if size > c.get("resume_max_context", 200000) else None

def launch_failed(out, code):
    """Its first line, if a claude run exited with an error before it wrote a single stream-json line (it never got as far as the
    session: `error: unknown option`, a crash on start). A signal (negative code) is a kill, not this. Else None."""
    if not code or code < 0: return None
    try:
        with open(out, errors="replace") as f: lines = [l.strip() for l in f if l.strip()]
    except (OSError, TypeError): return None
    if any(l.startswith("{") or "No conversation found" in l for l in lines): return None  # a missing session is the session's fault, even with no json line
    return (lines[0] if lines else "no output")[:200].replace("`", "'")

def wrote_json(out):
    """Whether a run's output has a stream-json line: claude started and read its arguments."""
    try:
        with open(out, errors="replace") as f: return any(l.lstrip().startswith("{") for l in f)
    except (OSError, TypeError): return False

def launch_held(st):
    """Holding new starts after a launch failure; the hold lapses after LAUNCH_HOLD_S and the next start tries again."""
    h = st.get("launch_hold")
    return bool(h) and time.time() < h.get("until", 0)

def resume_failed(out, code):
    """A woken run that ended in error without one turn: bad session, say. Its post would be lost."""
    try:
        with open(out, errors="replace") as f:
            if any(t in line for line in f for t in ('"type":"assistant"', '"type": "assistant"', '"turn.completed"', '"item.completed"', "session.resume_hint")): return False
    except (OSError, TypeError): pass
    return code != 0

def ago(iso):
    try: s = int(time.time() - epoch(iso))
    except (TypeError, ValueError): return "some time"
    return f"{s // 86400} d" if s >= 172800 else f"{s // 3600} h" if s >= 7200 else f"{max(s, 60) // 60} min"

def wake_home(c, n, run, sid, posts, to, room, limits):
    """The login to resume n's session on, moving its transcript there if needed; None if it can't be resumed.
    Claude Code's sessions move between logins and APIs. A run in another harness (codex, kimi) can't read them, or they it."""
    prov = next((pv for p in posts if (pv := provider_of(c, p.get("model")))), None)
    harness = prov[1].get("harness", "claude-code") if prov else "claude-code"
    if run.get("harness", "claude-code") != harness: return None
    if harness != "claude-code":  # it keeps its own sessions, in the login it ran on; a failed resume goes to an heir
        own = next((name for name, pc in c.get("providers", {}).items() if pc.get("harness") == harness and run.get("login") and home_of(pc) == run["login"]), None)
        if own and own != prov[0]:  # it ran on another login of this kind (codex-2): only that one has its session
            if provider_room(c, own, c["providers"][own], {}) is not None: return None
            retarget(c, posts, prov[0], own)
        return to
    own = run.get("login") or "/nonexistent"
    if prov:
        if not prov[1].get("config_dir"): return None
        where = str(Path(prov[1]["config_dir"]).expanduser())
    else:
        if own in room and pick_login(c, {own: room[own]}, limits, model=run_model(posts)): to = own  # the only login where the cache can still be warm
        where = to
    if own != where: return to if move_session(sid, own, where) else None
    return to if list(Path(own).glob(f"projects/*/{sid}.jsonl")) else None

def fresh_name(st, prefix):
    """A code nobody has used: a code with a past run would resume that agent's session, not start a new one (@new drew a5d1f, 2026-11-03)."""
    while True:
        n = prefix + secrets.token_hex(2)
        if n not in st.get("pending", {}) and n not in st.get("resume", {}) and not last_run(n) and n != WARM.get("agent", {}).get("name"): return n  # nor the warm agent's

def wake(c, st, n, posts, to, room):
    """Start n for these posts: fresh if it never ran, resumed if asleep or named by the person (or itself: its own waiter), else its heir takes them.
    Returns the name that started, or None if the posts went to an heir's queue."""
    run = last_run(n)
    if not run:
        start(c, n, posts, to); return n
    why, forced = dead_why(c, n, run), any(p.get("no_revive") for p in posts)
    named = not forced and any((p.get("from") in ("person", "phone") and not p.get("routed")) or str(p.get("from", "")).lower() == n for p in posts)  # phone: the person called it
    sid = session_in(run.get("out")) or run.get("session")
    if not forced and (not why or named):
        if (home := wake_home(c, n, run, sid, posts, to, room, st.get("limits"))):
            (STATE / "done" / n).unlink(missing_ok=True); st.get("heirs", {}).pop(n, None)
            log(event="wake_resumed", name=n, session=sid, was_dead=why, slept_from=run.get("ended"))
            start(c, n, posts, home, resume=sid, slept=run)
            return n
        why = why or "its session can't be resumed on this login or model"
    if (heir := st.get("heirs", {}).get(n)):  # it has one already, maybe still reading the thread
        st["pending"].setdefault(heir, []).extend(posts)
        log(event="to_heir", name=n, heir=heir, posts=[p["id"] for p in posts]); return None
    new = fresh_name(st, n[0] if re.fullmatch(r"[a-z][0-9a-f]{4}", n) else "a")
    st.setdefault("heirs", {})[n] = new
    if n in st.get("chosen", {}): st["chosen"][new] = st["chosen"][n]
    if n in efforts[0]: efforts[0][new] = efforts[0][n]
    log(event="heir", name=n, heir=new, why=why)
    start(c, new, posts, to, heir_of={"name": n, "why": why, "out": run.get("out")})
    return new

RESUME_WHY = {"login": "Your run stopped because its Claude login had expired or was signed out. It works again now.",
              "restart": "Your run stopped because the machine restarted. Jobs you had running may have stopped too.",
              "connection": "Your run stopped because the connection to Claude was lost. It may be back now.",
              "stalled": "Your run went silent mid-turn with nothing running, so the starter ended it and resumed it.",
              "other": "Your run ended before it finished, and the starter resumed it."}

HARD = re.compile(r"\*\*Hard limits\.\*\*.*?\n\nRoot on the [a-z]+ is held only by[^\n]*\n", re.S)

def trial_orientation(name, live, path=None):
    """A live A/B trial of an orientation change (rsi-loop's trial.py writes ~/.hive/trial.json): half the agents, chosen by a
    stable hash of their name so a resumed run keeps its arm, get the candidate's edit 3-way merged onto today's orientation.
    Returns (text, arm), arm None when no trial runs. A merge that conflicts or touches the hard-limit block gives the live
    text, arm "skip", so it is left out of the count. Never fails a start."""
    try: t = json.loads((path or STATE / "trial.json").read_text())
    except (OSError, ValueError): return live, None
    try:
        if t.get("until") and now() > t["until"]: return live, None
        h = int(__import__("hashlib").sha1(f"{t['name']}:{name}".encode()).hexdigest()[:8], 16) / 2**32
        if h >= t.get("share", .5): return live, "base"
        # a temp file of its own per call: a fixed trial-<name>.md was shared by test suites run side by side (same names), one
        # overwrote or unlinked the other's between write and merge, and the agent fell out of the trial as "skip" (rsi-loop, 2026-11-07)
        d = Path(t["dir"]); fd, tmp = tempfile.mkstemp(prefix=f"trial-{name}-", suffix=".md", dir=os.environ.get("TMPDIR") or None)
        try:
            with os.fdopen(fd, "w") as f: f.write(live)
            r = subprocess.run(["git", "merge-file", "-p", tmp, str(d / "base.md"), str(d / "cand.md")], capture_output=True, text=True, timeout=5)
        finally: Path(tmp).unlink(missing_ok=True)
        if r.returncode != 0 or (m := HARD.search(r.stdout)) is None or m.group(0) != (HARD.search(live) or [None])[0]:
            return live, "skip"
        return r.stdout, "cand"
    except Exception: return live, "skip"

def tool_trial(name, sid="", path=None, quiet=False):
    """A trial of dropping tools almost nobody uses (hive-meta L18/L21, i-number in the log): ~/.hive/tool_drop.json holds
    {"name", "until", "share", "tools": [...]}, half the agents by a stable hash of their name run without those tools (each costs input on every model
    call). Returns the tools to drop, [] for the other arm, None with no trial; every run start under a live trial adds a line to tool_drop.log with its
    arm, which is what carried.py --trial reads. Never fails a start."""
    try:
        p = Path(path or STATE / "tool_drop.json"); t = json.loads(p.read_text())
        if t.get("until") and now() > t["until"]: return None
        on = int(__import__("hashlib").sha1(f"{t['name']}:{name}".encode()).hexdigest()[:8], 16) / 2**32 < t.get("share", .5)
        if not quiet:
            with p.with_name("tool_drop.log").open("a") as f: f.write(json.dumps({"ts": now(), "trial": t["name"], "name": name, "session": sid, "on": on}) + "\n")
        return list(t.get("tools") or []) if on else []
    except Exception: return None

def cache_trial(name, sid="", path=None, quiet=False):
    """A trial of the split prompt cache (hive-meta L26/L27): ~/.hive/cache_split.json holds {"name", "until", "share"}, and that share of agents by a stable
    hash of their name run with the conversation on the 5-minute cache while the system prompt and tools stay on 1 hour (a cache write costs 12.5 read units
    instead of 20, and an idle over 5 minutes re-writes only the conversation). True for the trial arm, False for the other, None with no trial; every run start
    under a live trial adds a line to cache_split.log with its arm, which the trial's reading joins on. Never fails a start."""
    try:
        p = Path(path or STATE / "cache_split.json"); t = json.loads(p.read_text())
        if t.get("until") and now() > t["until"]: return None
        on = int(__import__("hashlib").sha1(f"{t['name']}:{name}".encode()).hexdigest()[:8], 16) / 2**32 < t.get("share", .5)
        if not quiet:
            with p.with_name("cache_split.log").open("a") as f: f.write(json.dumps({"ts": now(), "trial": t["name"], "name": name, "session": sid, "on": on}) + "\n")
        return on
    except Exception: return None

def cut_orientation(name, text, path=None):
    """A removal trial (projects/rsi-lab/rules.md): ~/.hive/cut.json holds {"name", "until", "groups": {group: [exact sentences]}} and each
    group is dropped for half the agents, on a coin of its own per group and agent name. Returns (text, cut): cut is None with no
    trial, "skip" (nobody counted) when a sentence under trial is no longer on the page or the hard-limit block would change, else
    the dropped groups joined by commas ("" for none). Runs beside trial_orientation, on its result. Never fails a start.
    Before any of that, holdout.json beside cut.json ({"share", "page"}) deals a fixed share of names the page in that file whole,
    cut "holdout": the reference every later cut is measured against, so it outlives the rounds. Those agents are in no trial."""
    try:
        h = json.loads((path or STATE / "cut.json").with_name("holdout.json").read_text())
        if int(__import__("hashlib").sha1(f"holdout:{name}".encode()).hexdigest()[:8], 16) / 2**32 < h["share"]:
            page = Path(h["page"]).read_text()
            if HARD.search(page)[0] == HARD.search(text)[0]: return page, "holdout"
    except Exception: pass
    try: t = json.loads((path or STATE / "cut.json").read_text())
    except (OSError, ValueError): return text, None
    try:
        if t.get("until") and now() > t["until"]: return text, None
        gs = t["groups"]
        if any(x not in text for xs in gs.values() for x in xs): return text, "skip"
        cut = sorted(g for g in gs if int(__import__("hashlib").sha1(f"{t['name']}:{g}:{name}".encode()).hexdigest()[:8], 16) % 2)
        out = text
        for g in cut:
            for x in gs[g]: out = out.replace(x, "", 1)
        return (out, ",".join(cut)) if HARD.search(out)[0] == HARD.search(text)[0] else (text, "skip")
    except Exception: return text, "skip"

def neighbours_note(name, posts):
    """Live threads that share uncommon words with the posts being handed over (neighbours.py, hive-meta moving.md P3): up to 3 lines, or
    nothing. Only on a fresh start: a mid-run delivery and a resume already have their work. Never holds up or fails a start."""
    ids = [p["id"] for p in posts if p.get("id") and str(p.get("from", "")).lower() not in ("starter", "watchdog")][:5]
    if not ids: return ""
    try:
        r = subprocess.run([sys.executable, "-I", str(CORE / "neighbours.py"), "--for", name, *(x for i in ids for x in ("--post", i))],
                           capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.SubprocessError): return ""
    return "\n\n" + r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else ""

modules_seen = (0, "")
def modules_note():
    """The modules this Hive could add, so an agent sees them with the job in hand (the person, 11091233e479). Read once every 5 minutes; none or an error: no note."""
    global modules_seen
    if time.time() - modules_seen[0] < 300: return modules_seen[1]
    try:
        rows = json.loads(subprocess.run([sys.executable, "-I", str(CORE / "hive"), "module", "list", "--json"], capture_output=True, text=True, timeout=8, cwd=str(HOME)).stdout)
        free = [f"- {m['name']}: {m['about']}" + (f" (needs {str(m['needs'])[:120]})" if m.get("needs") else "") for m in rows if m.get("state") == "available"][:40]
    except (OSError, ValueError, TypeError, KeyError, subprocess.SubprocessError): free = []
    text = "\n\n# Modules you can add\nNot installed here. If your job is in one of these fields, run `hive module add NAME` before you start, without asking:\n" + "\n".join(free) if free else ""
    modules_seen = (time.time(), text)
    return text

def recall_note(name, posts):
    """Pointers to our own notes that share words with the posts being handed over (`kb recall`), so related notes turn up
    without the agent searching: a few lines, or nothing when kb isn't sure. The starter's own posts (keeper, improver,
    notes) get none. Never holds up or fails a start."""
    ids = [p["id"] for p in posts if p.get("id") and str(p.get("from", "")).lower() not in ("starter", "watchdog")][:5]
    if not ids: return ""
    try:
        r = subprocess.run([sys.executable, "-I", str(CORE / "kb.py"), "recall", "--for", name, *(x for i in ids for x in ("--post", i))],
                           capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.SubprocessError): return ""
    return "\n\n" + r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else ""

def thread_alone(tid, tail=262144, most=4000):
    """The text of post tid if it is still the only post in its thread (read from the board's tail), else None."""
    try:
        with open(BOARD, "rb") as f:
            f.seek(max(0, f.seek(0, 2) - tail)); lines = f.read().decode("utf-8", "replace").splitlines()
    except OSError: return None
    text = None
    for l in lines:
        if tid not in l: continue
        try: p = json.loads(l)
        except ValueError: continue
        if p.get("id") == tid: text = str(p.get("text", ""))
        elif p.get("thread") == tid: return None
    return text if text and len(text) < most else None

def start_model(c, posts):
    """The model a start runs on: a provider wins (main loop picked it), then a model a post asked for, then the default."""
    return (next((p["model"] for p in posts if provider_of(c, p.get("model"))), None)
            or next((p["model"] for p in posts if p.get("model") in MODELS), None)
            or default_model())

def system_for(name, kind, modules):
    """(system prompt, trial, cut) for a run: orientation (with this name's trial and cut), the warden page, the person's instructions, the modules note, the sandbox note."""
    orient, trial = trial_orientation(name, (CORE / "orientation.md").read_text()) if kind != "warden" else ((CORE / "orientation.md").read_text(), None)
    orient, cut = cut_orientation(name, orient) if kind != "warden" else (orient, None)
    if cut == "holdout": trial = None  # a holdout agent is in no trial
    system = orient + ("\n\n" + (CORE / "warden.md").read_text() if kind == "warden" else "")
    try: mine = (HOME / "user-instructions.md").read_text().strip()  # the person's own CLAUDE.md, copied at setup on their tick
    except OSError: mine = ""
    if mine and kind != "warden": system += "\n\n# The person's own instructions (from their CLAUDE.md)\n" + mine
    if kind != "warden": system += modules
    if (sb := box()): system += sb.NOTE + ("\n" + (CORE / "warden-step.md").read_text() if kind == "warden" else "")
    return system, trial, cut

# The warm agent (projects/everyday/WARM_AGENT.md, E11d): the next fresh @new's claude process, booted ahead with its name, session, system prompt
# and sandbox fixed, waiting for its opening message on stdin. It cuts the CLI's own boot (about 0.8 s on a fresh install) out of a new chat's first reply.
# Any start it doesn't fit (another model, an effort, a provider, a resume, an heir, a warden) starts cold as before. It lives in this process's memory
# only: its output goes to ~/.hive/warm/ and moves into sessions/ at handover, and nothing is run/<name>.json until then, so the dash, reaper and wake
# logic only ever see a handed-over run. If the starter dies, EOF on the child's stdin ends it.
WARM = {}
WARM_DIR = STATE / "warm"

def warm_key(c, login, model):
    """What a warm agent was built from: if any of it changed, it is stale."""
    def mt(f):
        try: return (str(f.resolve()), f.stat().st_mtime_ns)
        except OSError: return 0
    files = [CORE / "orientation.md", CORE / "warden-step.md", HOME / "user-instructions.md", CORE / "config.json", HOME / "mcp.json", HOME / "settings.json",
             *(STATE / f for f in ("tool_drop.json", "cache_split.json", "cut.json", "holdout.json", "trial.json")), Path(login or "/").expanduser() / "settings.json"]
    return (login, model, *map(mt, files), mt(Path(shutil.which("claude") or "/")), modules_seen[1], json.dumps(c.get("model_ids") or {}, sort_keys=True),
            json.dumps(c.get("claude_args")), c.get("auto_compact_window"), c.get("max_budget_usd"))

def warm_on(c):
    return bool(c.get("warm_agent", True) and not c.get("only_model") and "stream-json" in c["claude_args"])

def warm_drop(why, w=None):
    """End a warm agent (the one in WARM, or w, already taken out): EOF on its stdin ends it by itself (it never got a message);
    a kill of its own tree only if it is still there. Its output file goes: no run points at it."""
    w = w or WARM.pop("agent", None)
    if not w: return
    try: w["proc"].stdin.close()
    except (OSError, ValueError): pass
    try: w["proc"].wait(timeout=3)
    except subprocess.TimeoutExpired:
        kill_tree(w["proc"].pid)  # boxed, the pid is the sandbox's outer shell
        try: w["proc"].wait(timeout=3)
        except subprocess.TimeoutExpired: pass
    try: Path(w["path"]).unlink()
    except OSError: pass
    log(event="warm_dropped", name=w["name"], why=why)

def warm_name(st, q):
    """The warm agent's name for a fresh @new/@claude post it could take (q as queued), else None; claimed by that post. start() makes the final check."""
    w = WARM.get("agent")
    if not w or w.get("claimed") or w["proc"].poll() is not None or q.get("effort") or q.get("free") or q.get("model") not in (None, w["model"]): return None
    if w["name"] in st.get("pending", {}): return None
    w["claimed"] = True
    return w["name"]

def warm_take(c, name, posts, login, kind, resume, heir_of):
    """The warm agent, if this start is the one it was booted for; else None (and a warm agent booted under this name, which this start can't use, ends)."""
    w = WARM.get("agent")
    if not w or w["name"] != name: return None
    why = ("resume" if resume else "heir" if heir_of else "warden" if kind != "agent" else "off" if not warm_on(c)
           else "exited" if w["proc"].poll() is not None else "login" if login != w["login"]
           else "effort" if any(p.get("effort") in EFFORTS for p in posts) or efforts[0].get(name) else "model" if start_model(c, posts) != w["model"]
           else "provider" if provider_of(c, w["model"]) else "fallback" if any(p.get("fallback") for p in posts)
           else "stale" if warm_key(c, login, w["model"]) != w["key"] else None)
    if why: warm_drop(why); return None
    return WARM.pop("agent")

def warm_boot(c, st, login):
    """Boot the next fresh agent's claude process, if none is warm and nothing is starting. Never fails the loop."""
    try:
        w = WARM.get("agent")
        if w and (code := w["proc"].poll()) is not None:  # it ended on its own: back off, so a CLI that can't start isn't relaunched every pass
            WARM["fails"] = WARM.get("fails", 0) + 1; WARM["after"] = time.time() + min(900, 15 * 2 ** WARM["fails"])
            warm_drop(f"exited {code}" + (f": {f}" if (f := launch_failed(w["path"], code)) else "")); w = None
        elif w and w.get("claimed") and w["name"] not in st["pending"]:  # its post went (a stop, a drop) without starting it
            warm_drop("unclaimed"); w = None
        elif w and not w.get("claimed") and (time.time() - w["at"] > (c.get("warm_max_age_s") or 1800) or not warm_on(c)
                                              or not login or w["login"] != login or warm_key(c, login, w["model"]) != w["key"]):
            warm_drop("old" if time.time() - w["at"] > (c.get("warm_max_age_s") or 1800) else "off" if not warm_on(c) else "login" if w["login"] != login else "stale"); w = None
        if w or not login or not warm_on(c) or time.time() < WARM.get("after", 0) or time.time() - last_start[0] < 2: return
        if under_pressure(c): return
        model = spawn_model(c) or default_model()
        if provider_of(c, model) or model not in MODELS: return
        if not WARM.get("swept"):  # a warm agent from before a restart ended on EOF; its file is left
            for f in WARM_DIR.glob("*.jsonl"): f.unlink(missing_ok=True)
            WARM["swept"] = True
        WARM_DIR.mkdir(parents=True, exist_ok=True)
        name, sid = fresh_name(st, "a"), str(uuid.uuid4())
        system, trial, cut = system_for(name, "agent", modules_note())
        cmd, env, _, _ = claude_launch(c, name, sid, model, None, login, "agent", [], None, system, quiet=True)
        path = WARM_DIR / f"{name}-{dt.datetime.now():%m%d-%H%M%S}.jsonl"
        proc = subprocess.Popen(cmd, cwd=HOME, env=env, stdout=path.open("w"), stderr=subprocess.STDOUT, stdin=subprocess.PIPE, text=True)
        WARM["agent"] = {"name": name, "sid": sid, "model": model, "login": login, "env": env, "path": path, "proc": proc, "at": time.time(),
                         "key": warm_key(c, login, model), "trial": trial, "cut": cut}
        log(event="warm_boot", name=name, pid=proc.pid, model=model)
    except Exception as e: log(event="warm_error", why=f"{type(e).__name__}: {e}"[:300]); WARM["after"] = time.time() + 300

def claude_launch(c, name, sid, model, prov, login, kind, posts, prompt, system, resume=None, quiet=False):
    """(cmd, env, login, effort) for a run: the same for a cold start and for a warm agent booted ahead (prompt None: it reads its
    opening message as stream-json on stdin, and has no post, so no HIVE_THREAD; hive then reads the thread from run/<name>.json)."""
    first = posts[0] if posts else None
    sb = box()
    tmp = HOME / ".cache" / "hive-tmp"; tmp.mkdir(parents=True, exist_ok=True)  # on disk: /tmp is tmpfs, its files hold RAM and swap
    # ENABLE_CLAUDEAI_MCP_SERVERS: the logins' claude.ai connectors (the person's Slack, Vercel buy_domain, ...) are 12k tokens on
    # every reply, and 12 calls in 542 transcripts, all Exa search; they also let an agent act in the person's name (kb/model-cost.md)
    # hive post --effort, or the effort picked in the agent panel: held on the agent like --model, so a resume keeps it. For a Claude run only
    effort = next((e for e in [efforts[0].get(name), *(p.get("effort") for p in posts)] if e in EFFORTS), None)
    env, argv = {**os.environ, "HIVE_NAME": name, **({"HIVE_THREAD": first.get("thread", first["id"])} if first else {}), "CLAUDE_CONFIG_DIR": login, "TMPDIR": str(tmp), "ENABLE_CLAUDEAI_MCP_SERVERS": "false"}, None
    # Opus defaults to a 1M window, so a run grows to 400k+ and pays for all of it on every turn; 36% of spend was above 200k (kb/model-cost.md).
    # Compacting near resume_max_context also keeps a run resumable instead of leaving an heir. 0 or absent: Claude Code's default.
    if c.get("auto_compact_window"): env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = str(c["auto_compact_window"])
    if not prov:  # config "model_ids": {"fable": "claude-fable-5-2"} pins what an alias means, without waiting for a CLI update
        for k, v in (c.get("model_ids") or {}).items(): env[f"ANTHROPIC_DEFAULT_{k.upper()}_MODEL"] = str(v)
    if prov:  # main loop's provider_hold just loaded its module and found room
        impl = prov[1]["_impl"]; keep = impl.env(prov[1], prov[2]); env.update(keep); env["_BOX_KEEP"] = " ".join(keep)
        if prov[1].get("config_dir"): env["CLAUDE_CONFIG_DIR"] = str(Path(prov[1]["config_dir"]).expanduser())
        login = env.get("CODEX_HOME") or env.get("KIMI_CODE_HOME") or env["CLAUDE_CONFIG_DIR"]
        if hasattr(impl, "command") and (argv := impl.command(prov[1], prov[2], prompt, system, sid, resume)):  # another harness (codex)
            env.pop("CLAUDE_CONFIG_DIR", None)
    if effort and not prov: env["CLAUDE_EFFORT"] = effort
    drop = tool_trial(name, sid, quiet=quiet) if kind != "warden" and not prov else None
    if kind != "warden" and not prov and cache_trial(name, sid, quiet=quiet): env["CLAUDE_CODE_MESSAGES_PROMPT_CACHE_TTL"] = "5m"
    cmd = argv or ["claude", "-p", *([prompt] if prompt is not None else ["--input-format", "stream-json"]), "--append-system-prompt", system,
                   *(["--disallowedTools", ",".join(drop)] if drop else []),
                   *(["--effort", effort] if effort and not prov else []),
                   *(["--resume", sid] if resume else ["--session-id", sid]),
                   *(["--model", prov[2] if prov else model] if model else []),
                   *(["--max-budget-usd", str(c["max_budget_usd"])] if c.get("max_budget_usd") and not prov else []), *(["--mcp-config", str(HOME / "mcp.json")] if (HOME / "mcp.json").is_file() and not prov else []), *c["claude_args"],
                   *(["--include-partial-messages"] if "stream-json" in c["claude_args"] and "--include-partial-messages" not in c["claude_args"] else [])]  # per-run cap; 0/absent = none; mcp.json: the servers the person ticked at setup; partial messages: the dash shows a post while it is written (E11)
    if sb:  # the delivered file is mounted by inode, so it's emptied before the run rather than removed after
        sb.prepare(name)
        if not resume: (STATE / "delivered" / name).write_text("")
        sb.ensure_broker(); sb.ready(name)
        cmd, env = sb.wrap(cmd, env, name)
    else: env.pop("_BOX_KEEP", None)
    return cmd, env, login, effort

def start(c, name, posts, login, resume=None, reaped=None, extra=(), moved=False, lost=False, woken=None, all_since=None, slept=None, heir_of=None, why=None):
    kind = "warden" if is_warden(name) else "agent"
    w = warm_take(c, name, posts, login, kind, resume, heir_of)  # the claude process booted ahead for this name (projects/everyday/WARM_AGENT.md), or None: a cold start
    if not w and (sb0 := box()): sb0.prepare(name); sb0.ensure_broker()  # first, so the broker (which looks every 0.1 s) has the run's sockets by the launch below (E11c: ready() waited 0.3 s)
    pool = concurrent.futures.ThreadPoolExecutor(3)  # the notes below are subprocesses: run them side by side, not one after another (E11c: 0.23 s in a row)
    ahead = {"modules": pool.submit(modules_note)} if kind != "warden" and not w else {}
    if kind != "warden" and not resume:  # used below only if the start isn't `asked` too
        ahead |= {"recall": pool.submit(lambda: recall_note(name, posts)), "neighbours": pool.submit(lambda: neighbours_note(name, posts))}
    pool.shutdown(wait=False)
    hook_inbox(c)  # a login can be added to config.json while the starter runs
    wire_deny(c)
    wire_precompact(c)
    sid = w["sid"] if w else resume or str(uuid.uuid4())  # the claude session id, so `claude --resume <session>` works
    first = posts[0]
    model = w["model"] if w else start_model(c, posts)
    prov = provider_of(c, model)
    system, trial, cut = (None, w["trial"], w["cut"]) if w else system_for(name, kind, ahead["modules"].result() if kind != "warden" else "")
    sb = box()
    prompt = (f"You are @{name}. You were woken by board post {', '.join(p['id'] for p in posts)} "
              f"(thread {first.get('thread', first['id'])}, from {first.get('from')}). "
              f"Start with `hive read --thread {first.get('thread', first['id'])}`.")
    if not resume and len(posts) == 1 and first.get("thread", first["id"]) == first["id"] and (said := thread_alone(first["id"])):
        # A fresh thread is just this post: hand it over, so the first reply doesn't wait a model turn on `hive read` (everyday E11b).
        prompt = (f"You are @{name}. You were woken by board post {first['id']} (thread {first['id']}, from {first.get('from')}), "
                  f"which starts its thread, so there is nothing more to read there. It says:\n\n{said}\n\n")
    if heir_of:
        prompt += (f" You are a new agent taking over from @{heir_of['name']}, which isn't being brought back because {heir_of['why']}. You have none of its memory. "
                   f"Its stream log, if a detail isn't in the thread or on disk, is {heir_of['out']}: search it, don't read it whole.")
    asked = resume and slept and any(str(p.get("from", "")).lower() == name and p.get("model") in MODELS for p in posts)
    if asked:
        prompt = (f"You are still @{name}: this is your own session, resumed on {model} because you asked for it with `hive model`. "
                  f"Carry on with the work. Check `hive read --thread {first.get('thread', first['id'])}` for anything new first.")
    elif resume and slept:
        prompt = (f"You are still @{name}: this is your own session, resumed after {ago(slept.get('ended'))} asleep. New board post for you: "
                  + "; ".join(f"{p['id']} (thread {p.get('thread', p['id'])}, from {p.get('from')})" for p in posts)
                  + f". Start with `hive read --thread {first.get('thread', first['id'])}`. Files and threads may have changed while you slept: "
                  "check the current state before relying on what you remember.")
    elif resume and woken:
        new = "; ".join(f"{p['id']} in thread {p.get('thread', p['id'])}, from {p.get('from')}" for p in woken["posts"])
        prompt = (f"You had ended your turn and were parked on a background shell, which can't receive posts, so the starter restarted you to hand you these: {new}. "
                  f"Read them with `hive read --thread <thread>` and answer them first, in those threads. ")
        prompt += (f"Your background shells were NOT killed and keep running, in the heavy queue too: {json.dumps(woken['shells'])}. They are no longer your children, so "
                   "nothing tells you when they end. If you still need a result, then start a background waiter with a time limit "
                   "(`while kill -0 PID 2>/dev/null; do sleep 30; done`, bounded by `timeout`) and read the output file when it ends. "
                   if woken["shells"] else "You had no background shells still running. ")
        prompt += f"You were first working in thread {first.get('thread', first['id'])}: carry that on if it isn't done."
    elif resume and lost:
        prompt = (f"Ending your turn stopped these background subagents before they finished, so they will never report: {json.dumps(reaped)}. "
                  "Unlike background shells, a background subagent doesn't outlive your turn. If you still need that work, redo it and "
                  "wait for it inside the turn (run_in_background: false), or do it yourself. "
                  f"Check `hive read --thread {first.get('thread', first['id'])}` for anything new first. If you were done, just stop."
                  + (f" Posts for you that came in meanwhile: {', '.join(extra)} (`hive read` them)." if extra else ""))
    elif resume and reaped:
        prompt = (("The starter moved you to another login, because yours ran out while you were waiting. "
                   if moved else "The starter ended your run: it had printed its result, then nothing for "
                  f"{c.get('done_grace_s', 900) // 60} min, and nothing you started was using CPU or waiting in the heavy queue. ") + "These background shells "
                  f"of yours were killed: {json.dumps(reaped)}. Jobs you started with nohup/setsid survive. If you were "
                  "waiting on something, check it now and carry on (a background wait on a job that uses CPU is fine; "
                  f"an idle wait is ended again). Check `hive read --thread {first.get('thread', first['id'])}` "
                  "for anything new first. If you were done, just stop."
                  + (f" Posts for you that came in meanwhile: {', '.join(extra)} (`hive read` them)." if extra else ""))
    elif resume:
        prompt = (RESUME_WHY.get(why, "Your run stopped on a usage limit and the limit has now reset.") + " Continue where you left off "
                  f"(check `hive read --thread {first.get('thread', first['id'])}` for anything new first).")
    # Fresh starts only: over 3 days (to 2026-11-06) pointers in a first prompt got a strict follow-up (kb open or a read of the note) 50% of the time,
    # on a resume wake 11% (5 of 44), by hook 21% (hive-meta recall-measure.md). The hook (inbox.recall_note) still delivers them mid-run.
    if kind != "warden" and not asked and not resume:
        prompt += ahead["recall"].result() + ahead["neighbours"].result()
    if (k := next((p["continue"] for p in posts if p.get("continue")), None)):
        prompt += guarded("wake_note", k, first.get("thread"), bool(heir_of)) or ""
    fb = next((p["fallback"] for p in posts if p.get("fallback")), None)
    if fb: prompt += "\n\n" + FALLBACK_NOTE["codex" if fb == "fallback" and tier_of(model).startswith("codex") else fb].format(model=model, tier=tier_of(model))
    elif kind != "warden" and not prov and not asked and (not resume or slept): prompt += step_clause(model)
    path = HOME / "sessions" / f"{name}-{dt.datetime.now():%m%d-%H%M%S}.jsonl"
    last_start[0] = time.time(); recent_starts.append(last_start[0])
    def record():
        rec = {"name": name, "pid": proc.pid, "session": sid, "at": now(), "post": first["id"], "login": login,
               "thread": first.get("thread"), "from": first.get("from"), "out": str(path), "proc": proc_start(proc.pid), "boot": boot_id(), **({"resumed": True} if resume else {}), **({"woke": list(extra)} if extra else {}), **({"slept": posts} if slept else {}), **({"heir_of": heir_of["name"]} if heir_of else {}), **({"all_since": all_since} if all_since else {}), **({"model": model} if model else {}), **({"fallback": fb} if fb else {}), **({"trial": trial} if trial else {}), **({"cut": cut} if cut is not None else {}),
               "harness": prov[1].get("harness", "claude-code") if prov else "claude-code", **({"provider": prov[0]} if prov else {}), **({"effort": env["CLAUDE_EFFORT"]} if env.get("CLAUDE_EFFORT") else {}), **({"warm": True} if w else {})}
        (RUN / f"{name}.json").write_text(json.dumps(rec))
        return rec
    if w:  # hand over: the run record first (hive reads the thread from it: a warm process has no HIVE_THREAD), then the opening message on stdin
        proc, login, env = w["proc"], w["login"], w["env"]  # and EOF, so the CLI runs this one turn and exits like `-p` (it waits for more while stdin is open)
        try: os.replace(w["path"], path); w["path"] = path  # same inode: the child keeps writing to it
        except OSError: path = w["path"]
        rec = record()
        try:
            w["proc"].stdin.write(json.dumps({"type": "user", "message": {"role": "user", "content": prompt}}) + "\n"); w["proc"].stdin.close()
            tool_trial(name, sid); cache_trial(name, sid)  # the arms it was booted on (same name, same trial file: the key), logged now it runs
            WARM["fails"] = 0
            log(event="warm_used", name=name, age=round(time.time() - w["at"], 1))
        except (OSError, ValueError) as e:  # it died since warm_take looked: start cold, on a session id it never held
            warm_drop(f"handover: {type(e).__name__}", w); w = None
            sid, path = str(uuid.uuid4()), HOME / "sessions" / f"{name}-{dt.datetime.now():%m%d-%H%M%S}.jsonl"
            system, trial, cut = system_for(name, kind, modules_note())
    if not w:
        cmd, env, login, _ = claude_launch(c, name, sid, model, prov, login, kind, posts, prompt, system, resume)
        proc = subprocess.Popen(cmd, cwd=HOME, env=env, stdout=path.open("w"), stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
        rec = record()
    children[name] = proc
    if not resume and not sb: (STATE / "delivered" / name).unlink(missing_ok=True)  # the inbox hook starts fresh with this run; a resumed one keeps its list so an @all isn't handed over twice
    log(event="start", **rec)

def keeper(c, st, alive, login):
    """Keep work going without anyone asking: every keeper_every_s, or once the hive has been idle (no runs,
    nothing queued) for keeper_idle_s (and keeper_every_s/3 since the last round), start one fresh agent on ~/core/keeper.md in thread 'keeper'.
    Never while one is still on it, or while paused or limited. Always on: 0 or unset means the 3-hour default, since picking up stopped work is the Hive's promise, not a switch (the person, 2026-11-08)."""
    every = c.get("keeper_every_s") or 10800
    if not login: return
    if any(isinstance(r, dict) and r.get("thread") == "keeper" for r in alive.values()): return
    if any(p.get("thread") == "keeper" for ps in st["pending"].values() for p in ps): return
    # a limit-stopped round resumes instead; but one parked on a fallback (Codex at its limit) must not block rounds while a Claude login has room (keeper stalled 2.6 h, 2026-11-07)
    if any(r.get("thread") == "keeper" and not r.get("fallback") for r in st.get("resume", {}).values()): return
    t = time.time()
    if alive or st["pending"]: st["busy_at"] = t
    idle = t - st.get("busy_at", t) >= c.get("keeper_idle_s", 1800)
    last = st.setdefault("keeper_at", t)  # first boot: wait a full period
    if t - last < every and not (idle and t - last >= max(c.get("keeper_idle_s", 1800), every / 3)): return
    st["keeper_at"] = st["busy_at"] = t
    post(f"@new keeper round ({'idle' if idle else 'periodic'}): read ~/core/keeper.md and follow it.", ["new"], "keeper")
    log(event="keeper", why="idle" if idle else "periodic")

IMPROVE = """@new Figure out how to improve everything in the hive, across all of it: the projects, the quality of the work, failures, how agents work, Hive itself. Judge it yourself: read what you need (the board, ~/sessions, the code, results), then do things yourself or start other agents. `hive read --thread improve` shows what earlier rounds did; `hive progress` shows the hive's numbers and what got worse; `hive improve list` shows which earlier changes were judged held or reverted, so you build on what held. Log each change you land with `hive improve log --what ... --measure ... --revisit 7d`. Say what you did in this thread."""

def drop_stale_resumes(st):
    """A status-bar brief is worthless once the next 20-min pass has run: about 75 of 162 status-timer starts on 2026-11-09 were held runs bounced back on a limit."""
    for n, r in list(st.get("resume", {}).items()):
        if r.get("from") == "status-timer": st["resume"].pop(n); log(event="resume_dropped_stale", name=n)

def floor_gap(c, st, r):
    """How long a first-tier thread rests between floor starts: floor_focus_gap_s, doubling up to floor_gap_s each start that left its ladder unmoved (the fizzle brake)."""
    base, top = c.get("floor_focus_gap_s", 300), c.get("floor_gap_s", 1800)
    seen = st.get("floor_seen", {}).get(r["thread"])
    if not seen or seen.get("sig") != [r.get("rungs_done"), r.get("rungs_left"), r.get("rung")]: return base
    return min(base * 2 ** seen.get("n", 0), max(top, base))

def floor_off(c, t):
    """config floor_off: ["YYYY-MM-DDTHH:MM:SSZ", "...Z"], the floor paused from the first time up to the second. Dated both ends so it lapses by itself with nobody remembering it; anything else leaves the floor on."""
    try: a, b = (dt.datetime.strptime(x, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc).timestamp() for x in c.get("floor_off"))
    except (TypeError, ValueError): return False
    return a <= t < b

def floor(c, st, alive, login):
    """Keep the ladders moving with no gaps on the timeline, one start a minute at most, so work overlaps instead of arriving in bursts (the person, 2026-11-07, direction k1303; before it, post 1106181196b0).
    First tier, the focuses and floor_threads: a thread nobody is on gets its next agent floor_focus_gap_s after its last agent post or floor start, however many runs are going elsewhere, up to floor_focus_max running (floor_gap below backs off an unmoved ladder).
    Second tier, the other ladders: only while under agents_floor runs, floor_gap_s apart. Longest quiet first within a tier.
    Skips a thread with someone on it, a post queued, a `Parked until` line, a `Waiting:` that names a running agent or a time (continuation.waits), or the person's stop. 0 turns a tier off."""
    want, focus_gap = c.get("agents_floor", 0), c.get("floor_focus_gap_s", 0)
    if not (want or focus_gap) or not login or not cont or floor_off(c, time.time()): return
    t = time.time()
    if t - st.get("floor_check", 0) < 60: return
    st["floor_check"] = t
    queued = [p for ps in st["pending"].values() for p in ps]
    running = len(alive) + sum(1 for n, ps in st["pending"].items() if ps and n not in alive)  # posts waiting for a running agent are no extra run
    below, room = want and running < want, focus_gap and running < c.get("floor_focus_max", float("inf"))
    if not (below or room): return
    gap = c.get("floor_gap_s", 1800); started = st.setdefault("floor_at", {}); first_tier = set(c.get("floor_threads", []))
    busy = {p.get("thread") for p in queued} | {r.get("thread") for r in alive.values() if isinstance(r, dict)}
    def first(r): return bool(focus_gap) and (r["kind"] == "focus" or r["thread"] in first_tier)
    def due(r):
        if first(r):
            if not room: return False
            g = floor_gap(c, st, r)
        elif not below: return False
        else: g = gap
        return t - started.get(r["thread"], 0) >= g and (r["quiet_s"] is None or r["quiet_s"] >= g)
    rows = [r for r in cont.G.goals()["rows"] if (r["kind"] == "focus" or r.get("ladder")) and not r["running"] and not r.get("parked") and r["thread"] not in busy and due(r)]
    if not rows: return
    held = cont.waits({r["thread"] for r in rows}, set(alive) | {n for n, ps in st["pending"].items() if ps}, t, c.get("floor_wait_max_s", 7200))
    for r in sorted(rows, key=lambda r: (not first(r), -(r["quiet_s"] if r["quiet_s"] is not None else float("inf")))):
        if held.get(r["thread"], 0) > t or cont.stopped_thread(r["thread"]): continue
        started[r["thread"]] = t
        sig, seen = [r.get("rungs_done"), r.get("rungs_left"), r.get("rung")], st.setdefault("floor_seen", {}).get(r["thread"]) or {}
        st["floor_seen"][r["thread"]] = {"sig": sig, "n": seen.get("n", 0) + 1 if seen.get("sig") == sig else 1}
        text = cont.G.brief(r)   # to @new, or to the thread's last worker (goals.resume_arm), who keeps its own model
        with BOARD.open("a") as f:
            f.write(json.dumps({"id": dt.datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now(), "from": "starter", "to": [text.split()[0]], "thread": r["thread"], **({"model": c.get("floor_model", "sonnet")} if text.startswith("@new ") else {}), "text": text}) + "\n")
        log(event="floor", thread=r["thread"], running=len(alive), tier=1 if first(r) else 2); return

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

def dreamer(c, st, alive, login):
    """Every dream_every_s, one agent on ~/core/kb-dream.md, and only if `kb due` finds misses or filings to work from.
    Same limits as the improver; 0 turns it off. The check costs no model use, so a quiet knowledge base costs nothing."""
    every = c.get("dream_every_s", 0)
    if not every or not login: return
    if any(isinstance(r, dict) and r.get("thread") == "kb-dream" for r in alive.values()): return
    if any(p.get("thread") == "kb-dream" for ps in st["pending"].values() for p in ps): return
    if any(r.get("thread") == "kb-dream" for r in st.get("resume", {}).values()): return
    t = time.time()
    if t - st.setdefault("dream_at", t) < every: return
    st["dream_at"] = t
    try: due = subprocess.run([sys.executable, "-I", str(CORE / "kb.py"), "due", "--mark"], capture_output=True, text=True, timeout=120)
    except (OSError, subprocess.TimeoutExpired): return
    if due.returncode: return
    post("@new Knowledge base dream: read ~/core/kb-dream.md and follow it. Work since the last one:\n" + due.stdout[:3000], ["new"], "kb-dream", "sonnet")
    log(event="kb-dream")

def tidier(c, st, alive, login):
    """Every tidy_every_s, `tidy.py due --mark` (no model use) lists what agents should have done at their own hand-offs and didn't
    (a conversation in no focus, no title, a stale focus summary); only then one agent on ~/core/tidy.md fixes them and finds out why
    from the agents' run logs. The person, 2026-11-08 (post 110815388fca): the passes are there to go away; `tidy.py stats` measures it.
    Same limits as the dreamer; 0 turns it off."""
    every = c.get("tidy_every_s", 0)
    if not every or not login: return
    if any(isinstance(r, dict) and r.get("thread") == "tidy" for r in alive.values()): return
    if any(p.get("thread") == "tidy" for ps in st["pending"].values() for p in ps): return
    if any(r.get("thread") == "tidy" for r in st.get("resume", {}).values()): return
    t = time.time()
    if t - st.setdefault("tidy_at", t) < every: return
    st["tidy_at"] = t
    try: due = subprocess.run([sys.executable, "-I", str(CORE / "tidy.py"), "due", "--mark"], capture_output=True, text=True, timeout=120)
    except (OSError, subprocess.TimeoutExpired): return
    if due.returncode:
        log(event="tidy-clean" if due.returncode == 1 else "tidy-failed"); return
    out = due.stdout if len(due.stdout) <= 3500 else due.stdout[:3500].rsplit("\n- ", 1)[0] + "\n(cut off here: `python3 -I ~/core/tidy.py due --all` lists the rest)"
    post("@new Tidy audit: read ~/core/tidy.md and follow it. Missed since the last pass:\n" + out, ["new"], "tidy", c.get("tidy_model", "sonnet"))
    log(event="tidy", n=due.stdout.count("\n- ") + 1)

def backup(c, st):
    if time.time() - st.get("committed", 0) < c["commit_s"]: return
    st["committed"] = time.time()
    # With a sandbox, kb is agents' to write. A repo in it has its own .git/config, and git would run what that says to check its status, out here.
    git = "GIT_NO_LAZY_FETCH=1 git -c diff.ignoreSubmodules=all -c core.fsmonitor=false" if box() else "git"
    r = subprocess.run(f"{git} add -A && {git} commit -qm 'auto backup'", shell=True, cwd=HOME, capture_output=True, text=True)
    if r.returncode and "nothing to commit" not in r.stdout + r.stderr:  # nothing-to-commit exits 1 too; anything else is a real failure
        log(event="backup-failed", rc=r.returncode, err=(r.stderr or r.stdout)[-300:])

def hook_inbox(c):
    """Make sure each login's settings.json runs inbox.py after every tool call (one that fails too: a command stopped for the
    person's Interrupt ends that way) and on stop (see inbox.py).
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
        wired = str(CORE / "inbox.py") in json.dumps(hooks)
        if wired and str(CORE / "inbox.py") in json.dumps(hooks.get("PostToolUseFailure", [])): continue
        if not wired: hooks.setdefault("PostToolUse", []).append({"matcher": "*", **h}); hooks.setdefault("Stop", []).append(h)
        hooks.setdefault("PostToolUseFailure", []).append({"matcher": "*", **h})
        f.write_text(json.dumps(s, indent=2) + "\n")

def wire_precompact(c):
    """Make sure each login's settings.json runs precompact.py before a compaction. The hook does nothing unless ~/.hive/compact_limit.json
    names a live trial (see precompact.py), so wiring it changes nothing by itself."""
    cmd = str(CORE / "precompact.py")
    if not (CORE / "precompact.py").exists(): return
    for login in c["logins"] + [pc["config_dir"] for pc in c.get("providers", {}).values() if pc.get("config_dir")]:
        f = Path(login).expanduser() / "settings.json"
        f.parent.mkdir(parents=True, exist_ok=True)
        try: s = json.loads(f.read_text()) if f.exists() else {}
        except ValueError: continue
        if cmd in json.dumps(s.get("hooks", {}).get("PreCompact", [])): continue
        s.setdefault("hooks", {}).setdefault("PreCompact", []).append({"hooks": [{"type": "command", "command": cmd, "timeout": 10}]})
        f.write_text(json.dumps(s, indent=2) + "\n")

DENY_SEEN = None
def wire_deny(c):
    """When ~/.cc/github-deny is there, the GitHub orgs on it are refused (bin/cc-github-deny has the layers). The list is
    placed by the machine's admin and never leaves it. Runs at start and with each agent, so a new or edited list is picked up.
    No list: nothing is linked, written or hooked, so an ordinary install has none of this."""
    global DENY_SEEN
    lst, bin_ = HOME / ".cc/github-deny", CORE / "bin"
    if not os.access(lst, os.R_OK): return
    hook = {"matcher": "Bash|WebFetch", "hooks": [{"type": "command", "command": str(bin_ / "cc-github-deny-hook"), "timeout": 10}]}
    for login in c["logins"] + [pc["config_dir"] for pc in c.get("providers", {}).values() if pc.get("config_dir")]:
        f = Path(login).expanduser() / "settings.json"
        f.parent.mkdir(parents=True, exist_ok=True)
        try: s = json.loads(f.read_text()) if f.exists() else {}
        except ValueError: continue
        if "cc-github-deny-hook" in json.dumps(s.get("hooks", {})): continue
        s.setdefault("hooks", {}).setdefault("PreToolUse", []).append(hook)
        f.write_text(json.dumps(s, indent=2) + "\n")
    seen = lst.stat().st_mtime_ns
    if seen == DENY_SEEN: return
    (HOME / ".local/bin").mkdir(parents=True, exist_ok=True)
    for name in ("cc-github-deny", "git-remote-cc-github-deny", "gh", "curl", "wget"):
        link = HOME / ".local/bin" / name
        if link.exists() and not link.is_symlink():  # a real gh lives here: never overwrite it. Git and the hook still hold.
            log(event="github_deny", problem=f"{link} is a real file, so the {name} layer isn't wired"); continue
        tmp = link.with_name(f".{name}.{os.getpid()}"); tmp.symlink_to(bin_ / name); tmp.replace(link)
    r = subprocess.run([str(bin_ / "cc-github-deny"), "install"], capture_output=True, text=True, timeout=60)
    if r.returncode: log(event="github_deny", problem=(r.stderr or r.stdout)[-300:])
    else: DENY_SEEN = seen

def provider_hold(c, st, n, pv, cache, thread):
    """True if agent n's provider can't take a run now; says why in its thread once per reason, so a post doesn't wait in silence."""
    why = provider_room(c, pv[0], pv[1], cache)
    held = st.setdefault("provider_held", {})
    if why is None: held.pop(n, None); return False
    if held.get(n) != why:
        held[n] = why; log(event="provider_held", name=n, provider=pv[0], why=why)
        post(f"`{n}` waits for provider `{pv[0]}`: {why}. It starts once that's fixed; the key and its credit are the person's.", [] if pv[1].get("paused") or "limit at" in why else ["person"], thread)  # a usage limit lifts by itself: no inbox ping
    return True

FALLBACK_NOTE = {
    "free": "This run is on a free model ({model}), not Claude: someone asked for a free agent. Keep it light: answer, look "
            "things up, small edits, board and kb chores. No heavy jobs, no builds or sims. You stay on this model when woken again. "
            "If the work is beyond you, don't guess: either ask @person, or hand it to a stronger agent with "
            "`hive post --to new --thread THREAD` and a clear brief, and choose its model with --model (sonnet if that's enough). Only --to starts one: "
            "from a free agent, the word in the text starts nothing, so leave it out. Don't start more free agents. "
            "Before stopping, send your actual answer using hive post --thread THREAD. A local final answer alone is "
            "not delivered. Mention @person only if their opinion or decision is really needed.",
    "codex": "Every Claude login is at its usage limit, so this run is on a fallback model ({model}, {tier}). Work doesn't wait for "
             "that (the person, 2026-11-07: it doesn't matter if work is done on Claude or Codex): do the whole job as you would on Claude, "
             "builds and sims too. One difference: a fresh agent you start now waits for Claude, so hand on to an agent by name or end on a "
             "`Next:` line. You go back to Claude when you're woken after the reset.",
    "fallback": "Every Claude login is at its usage limit, so this run is on a fallback model ({model}, {tier}) because the "
                "person's post shouldn't wait for the reset. Do only what's time-critical: answer, give an update, a small fix. "
                "No heavy jobs, no builds or sims, and don't start other agents (they'd wait for Claude anyway). Leave the "
                "rest for when Claude resets and say plainly in the thread what's left.",
}

def tier_of(model):
    m = PROVIDER_MODEL.match(model or "")
    if not m: return "claude"
    return "codex (no extra cost)" if m[1] == "codex" else "kimi (subscription, no extra cost)" if m[1] == "kimi" else "free" if m[2].endswith(":free") else "paid, real money"

def fallback_model(c, st, chain, cache):
    """The first model in chain (provider:model strings, config.json fallback) whose provider has room now, else None.
    A provider module may also say no for one model (model_room: OpenRouter free models' daily request count)."""
    for model in chain:
        pv = provider_of(c, model)
        if not pv: continue
        pv = with_room(c, pv, cache)
        if provider_room(c, pv[0], pv[1], cache) is not None: continue
        impl = pv[1].get("_impl")
        if impl and hasattr(impl, "model_room"):
            try:
                if impl.model_room(c, pv[1], pv[2], cache) is not None: continue
            except Exception: continue
        return f"{pv[0]}:{pv[2]}"
    return None

def agents_chain(c):
    """Where the hive's own posts (agents', keeper, floor) go with no Claude login free: fallback.agents, Codex only whatever the config says (warden w7fc9,
    2026-11-07: the person paused OpenRouter, and these runs carry private context). An install with fallback.for "*" uses its chain for everyone."""
    fb = c.get("fallback") or {}
    if "*" in fb.get("for", ["person"]): return fb.get("chain", [])
    return [m for m in fb.get("agents", []) if (pv := provider_of(c, m)) and family(pv[0], pv[1]) == "codex"]

FALLBACK_RUN = {}  # agent name -> (when read, was its last run on a fallback)
def on_fallback(name):
    """Whether this agent's last run was on a fallback; read from the log at most once a minute, since a queued post asks every poll."""
    t, was = FALLBACK_RUN.get(name, (0, False))
    if time.time() - t > 60:
        was = bool((last_run(name) or {}).get("fallback"))
        FALLBACK_RUN[name] = (time.time(), was)
    return was

def fallback(c, st, n, posts, claude_ok, cache):
    """For a free agent (@free), or any post while no Claude login has room: pick a fallback model, set it on the posts and mark them, so start() runs on it
    and tells the agent which tier it is on. A batch with a post from someone in fallback.for (the person) takes fallback.chain; the rest take agents_chain.
    A fresh agent asked for by a run already on a fallback waits for Claude, so runs on a fallback can't multiply. Returns True if it did; False to let the
    posts wait, with one note in the thread when nothing has room for the person or a free agent. The hive's own posts wait quietly (the log says so)."""
    fb = c.get("fallback") or {}
    free, whom = any(p.get("free") for p in posts), fb.get("for", ["person"])
    person = "*" in whom or any(p.get("from") in whom for p in posts)
    if free: chain = fb.get("free", [])
    elif claude_ok: return False
    elif person: chain = fb.get("chain", [])
    elif any(p.get("spawn") and on_fallback(p.get("from")) for p in posts): return False
    else: chain = agents_chain(c)
    model = fallback_model(c, st, chain, cache)
    held = st.setdefault("fallback_held", {})
    if not model:
        if not held.get(n):
            held[n] = True; log(event="fallback_held", name=n, chain=chain)
            if free or person: post(f"`{n}` waits: {'no free model' if free else 'no Claude login and no fallback'} has room now "
                                    f"({', '.join(chain) or 'none set in config.json fallback'}). It starts once one does.", [], posts[0].get("thread"))
        return False
    held.pop(n, None)
    for p in posts: p["model"], p["fallback"] = model, "free" if free else "fallback"
    log(event="fallback", name=n, model=model, why="free" if free else "claude limits")
    return True

def load_stamp():
    """Whether the last starter was told to stop in this boot. Read once: a machine restart has another boot id, so its runs carry on."""
    f = STATE / "stopped"
    try: was = f.read_text(); f.unlink()
    except OSError: return False
    return was == boot_id()

def nap(secs):
    """Sleep up to secs, but wake as soon as the board grows: a new post is seen in 0.05 s (E11c: a stat is cheap, and Send to start is under 0.4 s), not after a whole poll."""
    try: size = BOARD.stat().st_size
    except OSError: size = 0
    end = time.time() + secs
    while time.time() < end:
        time.sleep(0.05)
        try:
            if BOARD.stat().st_size != size: return
        except OSError: pass


def main():
    for d in (RUN, STATE, HOME / "sessions"): d.mkdir(exist_ok=True)
    # One starter per home: two would both read the board and start every agent twice. A second one waits here and takes over
    # when the first ends (a restart that overlaps, a hand-run copy beside the service). The fd isn't inherited by agents.
    import fcntl
    lock = open(STATE / "starter.lock", "w")
    try: fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        print("another starter runs in this home; waiting for it to end", flush=True); fcntl.flock(lock, fcntl.LOCK_EX)
    lock.write(str(os.getpid())); lock.flush()
    def stop_stamp(*_):  # told to stop (`systemctl stop`, the guide's Stop everything): runs found dead at the next start, in this boot, were stopped on purpose
        try: (STATE / "stopped").write_text(boot_id())
        except OSError: pass
        signal.signal(signal.SIGTERM, signal.SIG_DFL); os.kill(os.getpid(), signal.SIGTERM)
    by_hand = load_stamp()
    signal.signal(signal.SIGTERM, stop_stamp)
    hook_inbox(cfg())
    wire_deny(cfg())
    wire_precompact(cfg())
    st = load("starter.json", {"offset": 0, "pending": {}})
    efforts[0] = st.setdefault("efforts", {})
    u, paused, pcache = {}, False, {}
    while True:
        c = cfg()
        size = BOARD.stat().st_size if BOARD.exists() else 0
        if size < st["offset"]:  # board was rewritten (a git stash under ~ did it twice on 2026-11-04): don't replay it, but say so in the log
            log(event="board_shrunk", was=st["offset"], size=size); st["offset"] = size
        if size > st["offset"]:
            with BOARD.open("rb") as f:
                f.seek(st["offset"]); chunk = f.read()
            chunk = chunk[:chunk.rfind(b"\n") + 1]  # only whole lines
            st["offset"] += len(chunk)
            for line in chunk.decode(errors="replace").splitlines():
                try: p = json.loads(line)
                except ValueError: continue
                called, unknown = callable_names(p, c)
                guarded("note_post", p, is_stop(p))
                if is_stop(p) and (gone := drop_spawns(st, p.get("thread"))):
                    log(event="stop_dropped", names=gone, post=p.get("id"), thread=p.get("thread"))
                src_free = is_free_now(st, str(p.get("from", "")).lower())
                if src_free and any(n in SPAWN for n in called):
                    _, skip = free_spawns(p, called)
                    if skip: log(event="free_spawn_ignored", names=skip, post=p.get("id"), src=p.get("from"))
                    called = [n for n in called if n not in skip]
                for n in unknown:
                    log(event="unknown_mention", name=n, post=p.get("id"), src=p.get("from"))
                    if p.get("from") == "person":  # he can't see hive post's warning, and asked for something
                        post(f"Nobody here is called `{n}`, so nothing started. For a new agent use the word new; for one with that name, `hive post --to {n}`.", ["person"], p.get("thread"))
                for n in called:
                    if n in c["external"]: continue
                    # a self-mention wakes its author only when the author isn't running: a waiter's
                    # "done" post from a run that has already ended; a running agent mentioning itself doesn't
                    # ...unless it asks for a stronger model (`hive model`) or an effort: queued, it resumes on it when this run ends
                    if n == str(p.get("from", "")).lower() and runs_now(n) and not (called == [n] and (p.get("model") in MODELS or p.get("effort") in EFFORTS) and not src_free): continue
                    q = {k: p.get(k) for k in ("id", "thread", "from")}
                    if p.get("effort") and not src_free: q["effort"] = p["effort"]  # hive post --effort
                    if p.get("routed"): q["routed"] = True  # a door picked this name; nobody typed it
                    q["model"] = asked_model(p, c)
                    if src_free and tier_of(q["model"]) == "paid, real money": q["model"] = None  # a free agent's post doesn't spend money
                    fresh = n == "warden" or n in SPAWN
                    if n == "warden": n = fresh_name(st, "w")  # each warden run gets its own code too
                    elif n in SPAWN:
                        word, n = n, fresh_name(st, SPAWN[n])
                        q["spawn"] = word
                        # @new: the default (or --model); @claude: Claude only, never a provider or a fallback;
                        # @codex: Codex CLI (--model codex:... picks which); @free: an OpenRouter free model (fallback.free)
                        if word == "claude":
                            q["claude"] = True
                            if provider_of(c, q.get("model")): q["model"] = None
                        elif word == "codex" and ((pv := provider_of(c, q.get("model"))) is None or pv[1].get("harness") != "codex"):
                            q["model"] = c.get("codex_model") or CODEX_DEFAULT
                        elif word == "free": q["free"] = True
                        elif word in MODEL_WORDS: q["model"] = q.get("model") or MODEL_WORDS[word]
                        if word in ("new", "claude") and not q.get("model"): q["model"] = spawn_model(c)  # Sonnet by the person's choice, 2026-11-04
                        if word in ("new", "claude") and not c.get("only_model") and (wn := warm_name(st, q)): n = wn  # the warm agent, if it fits this post as queued (E11d)
                    if is_warden(n):  # the warden rules on the hard limits and may come to hold root: Claude only, like @claude
                        q["claude"] = True
                        if provider_of(c, q.get("model")): q["model"] = None
                    if q.get("effort") in EFFORTS: hold_effort(st, n, q["effort"])  # a run's effort is fixed when it starts: this holds from the next one on
                    if not fresh: stays_on(st, c, n, q)
                    elif (keep := {k: q[k] for k in ("model", "free", "claude") if q.get(k) and (k != "model" or model_ok(c, q[k]))}):
                        st.setdefault("chosen", {})[n] = keep
                    if (om := c.get("only_model")) in MODELS:  # a test hive (rsi-loop bench/replay): every run on this model, whatever the post asked
                        q["model"] = om; q.pop("free", None); st.setdefault("chosen", {})[n] = {"model": om}
                    if p.get("interrupt") and p.get("from") == "person" and not fresh:
                        q["interrupt"] = True   # a run parked on a background shell wakes for it as for the phone (waiting_posts)
                        if runs_now(n):
                            try: did = interrupt_step(n, p.get("id"))
                            except Exception as e: did = f"{type(e).__name__}: {e}"[:200]
                            log(event="interrupt", name=n, post=p.get("id"), did=did)
                    st["pending"].setdefault(n, []).append(q)
        alive = running(st, by_hand)
        by_hand = False
        drop_killed(st)
        auth_watch(c, st)
        apply_model_sets(st, c)
        apply_effort_sets(st)
        continuations(st, c, alive)
        for th, text in slow_replies(recent_posts(), st, alive):   # straight into the thread: post() would move it to the starter thread
            with BOARD.open("a") as f:
                f.write(json.dumps({"id": dt.datetime.now().strftime("%m%d%H%M") + secrets.token_hex(2), "at": now(), "from": "starter", "to": [], "thread": th, "text": text}) + "\n")
            log(event="slow_reply_noted", thread=th)
        # the person's post is about to fall back: read again first (17:02 on 2026-11-02 went to a free model 4 s before the read that showed a reset login)
        if pick_login(c, unpaused(u), st.get("limits")) is None and time.time() - u.get("_checked", 0) > 30 \
                and any(p.get("from") == "person" for ps in st["pending"].values() for p in ps):
            u["_checked"] = 0
        u = usage(c, u); room = unpaused(u)
        if (off := sorted(set(u) - set(room))) != st.get("paused_logins", []):
            st["paused_logins"] = off; log(event="logins_paused", logins=off)
        if (pick_login(c, room) is None) != paused:
            paused = not paused; st["all_paused"] = paused
            signed_in = any((Path(l).expanduser() / ".credentials.json").exists() for l in c["logins"])
            post(("Every login is paused or past its stop line; work carries on on the fallback (Codex), and your own asks also on a login that isn't paused."
                  if signed_in else "No Claude account is signed in yet, so only your own asks start, on another provider if one is set up."
                  " Sign in under Settings > Accounts.")
                 if paused else "Usage has room again; starting sessions.", ["person"])
            log(event="paused" if paused else "resumed", usage=u)
        login = pick_login(c, room, st.get("limits"))  # None: every login is paused, past the stop line or held on a limit
        hold = box_holds() or launch_held(st) or daily_cap_holds(c, st)
        for n, r in ([] if hold else list(st.get("resume", {}).items())):  # a provider run held on its limit, asked since for a Claude model: its session can't move, so the post wakes it as a new run
            if login and provider_of(c, r.get("model")) and any(p.get("model") in MODELS for p in st["pending"].get(n, [])) and provider_room(c, *provider_of(c, r.get("model"))[:2], pcache):
                st["resume"].pop(n); log(event="resume_dropped_for_claude", name=n)
        drop_stale_resumes(st)
        for n, r in ([] if hold else list(st.get("resume", {}).items())):  # resume limit-stopped sessions first, same name and session
            if (not login and not provider_of(c, r.get("model"))) or n in alive or len(alive) >= (c.get("max_sessions") or float("inf")): continue
            if paced(c, st, n, [{"from": r.get("from")}]): continue
            own, limits = r.get("login"), st.get("limits")
            if (pv := provider_of(c, r.get("model"))):  # a provider run resumes on its provider, never on a Claude login
                if time.time() < r["reset"] or provider_hold(c, st, n, pv, pcache, r.get("thread")): continue
                to = own
            elif own in room and time.time() >= r["reset"] and pick_login(c, {own: room[own]}, limits, model=r.get("model")): to = own  # its login has room again
            elif r.get("wait") and time.time() < r["reset"]: continue  # stopped on something other than its login: moving wouldn't help
            elif (dst := login if not (scope_held(limits, login, r.get("model")) or share_full(login, r.get("model"))) else pick_login(c, room, limits, model=r.get("model"))) \
                    and own != dst and move_session(r["session"], own, dst):  # or it moves to one that does (and has its model)
                to = dst; log(event="moved", name=n, session=r["session"], src=own, dst=to)
            else: continue
            st["resume"].pop(n)
            waiting = st["pending"].pop(n, []) if r.get("reaped") else []  # hand queued posts to the resumed run
            # a queued post that asked for a model holds here too: a parked agent woken for it comes back on that model
            model = r.get("model") if pv else next((p["model"] for p in waiting if p.get("model") in MODELS), r.get("model"))
            start(c, n, [{"id": r["post"], "thread": r.get("thread"), "from": r.get("from"), "model": model}], to,
                  resume=r["session"], reaped=r.get("reaped"), extra=[p["id"] for p in waiting], moved=r.get("move", False), lost=r.get("lost", False),
                  woken={"shells": r["shells"], "posts": waiting} if "shells" in r else None, all_since=r.get("all_since") or r.get("at"), why=r.get("why"))
            alive[n] = {"name": n, "thread": r.get("thread")}  # a dict, so keeper() sees a resumed keeper (two rounds at once, 2026-11-02)
        for n in ([] if hold else list(st["pending"])):
            if n in alive or n in st.get("resume", {}): continue
            got = STATE / "delivered" / n  # posts the inbox hook already handed this agent mid-run
            if got.exists():
                # Read it, don't remove it: the dash's read marks come from this file, and with it gone a post the agent had used
                # showed "hasn't seen this" until its next start (the person asked, 110515296d61). A fresh start empties it.
                seen = set(got.read_text().split())
                st["pending"][n] = [p for p in st["pending"][n] if p["id"] not in seen or p.get("continue")]  # a continuation's id is the post it follows, not a post for this agent
                if not st["pending"][n]:
                    st["pending"].pop(n); log(event="delivered", name=n); continue
            if any(p.get("free") for p in st["pending"][n]) and not any(p.get("fallback") for p in st["pending"][n]):
                if not fallback(c, st, n, st["pending"][n], True, pcache): continue
            pv = next((pv for p in st["pending"][n] if (pv := provider_of(c, p.get("model")))), None)
            if pv and (alt := with_room(c, pv, pcache))[0] != pv[0]:  # its login is at its limit: another login of the same kind
                retarget(c, st["pending"][n], pv[0], alt[0]); log(event="provider_moved", name=n, src=pv[0], dst=alt[0]); pv = alt
            if pv and login and all(p.get("fallback") == "fallback" for p in st["pending"][n]) and provider_room(c, pv[0], pv[1], pcache):
                # it went to Codex only because Claude was full; Claude has room now and Codex doesn't: don't wait for Codex's reset
                for p in st["pending"][n]: p.pop("model", None); p.pop("fallback", None)
                log(event="fallback_back_to_claude", name=n); pv = None
            if pv: to = None if provider_hold(c, st, n, pv, pcache, st["pending"][n][0].get("thread")) else "provider"
            else:
                to = login_for(c, room, st.get("limits"), st["pending"][n], login)
                if not to and not any(p.get("claude") for p in st["pending"][n]) and fallback(c, st, n, st["pending"][n], False, pcache):
                    to = None if provider_hold(c, st, n, provider_of(c, st["pending"][n][0]["model"]), pcache, st["pending"][n][0].get("thread")) else "provider"
            if to and len(alive) < (c.get("max_sessions") or float("inf")) and not paced(c, st, n, st["pending"][n]):
                ps = st["pending"].pop(n)
                if any(p.get("continue") for p in ps):
                    ps = guarded("before_launch", n, ps, st, c, alive) or [p for p in ps if not p.get("continue")]  # on an error, a continuation is what goes
                    if not ps: continue
                    save("starter.json", st)  # a crash from here loses this step rather than launching it twice
                if (who := wake(c, st, n, ps, to, room)):
                    alive[who] = {"name": who, "thread": ps[0].get("thread")}
                    if any(p.get("continue") for p in ps): guarded("launched", who, ps)
        if not hold:
            work = login or ("fallback" if fallback_model(c, st, agents_chain(c), pcache) else None)  # the hive's own rounds run on whichever has room
            keeper(c, st, alive, work)
            try: floor(c, st, alive, work)
            except Exception as e: log(event="floor_error", why=f"{type(e).__name__}: {e}"[:300])
            improver(c, st, alive, work)
            dreamer(c, st, alive, login)
            tidier(c, st, alive, login)
            warm_boot(c, st, login)
        save("starter.json", st)
        backup(c, st)
        nap(c["poll_s"])

if __name__ == "__main__":
    main()
