#!/usr/bin/env python3
"""hive-report: failure reports, a daily check-in and opt-in usage reports, scrubbed on this machine first.

Reads `share` ({"failures": bool, "usage": bool}) from settings.json. failures, which also carries the check-in, is on by
default and can be turned off. Both false (or no settings.json) means nothing is read, queued or sent. Every report that
leaves is first written byte-for-byte to reports/sent/, and `hive-report show`
prints it. Commands: status | send [--dry-run] | show [N] | question | bundle [--yes] | pack [--yes] | issue TITLE [--submit] <body on stdin> | feedback <JSON on stdin>
| upstream draft --repo OWNER/NAME TITLE <gap on stdin> | contribute TITLE [--commit SHA | --diff FILE] <what and why on stdin> | drafts | github | approve ID | discard ID

`share.github` (off unless the person ticks it, at setup or in Settings) lets this Hive use the person's own logged-in `gh`:
failures that fit the public templates below become issues on the Hive's repo (one per kind, found again by a marker,
at most GH_PER_DAY actions a day), and `contribute` drafts a fix an agent made in ~/core. A draft goes only after the person presses Send on it in
Settings (warden's ruling, post 11040224d273), as a pull request from their fork or, failing that, an issue with the patch. Without it, or with no gh login, failures go to the receiver only.

`feedback` is apart from all of that: it runs only when a person presses Send in the app's feedback dialog, which
says what goes and to whom, so it doesn't depend on `share`. A copy lands in reports/sent/ first, like every report.
"""
import json, os, re, sys, time, uuid, urllib.request, urllib.error, platform, getpass, socket

DEFAULT_URL = "https://hive-reports.ihsan.cc/v1/report"   # filled in at export; left as is in a source tree, which counts as no receiver
                                 # (override with share_url in settings.json; schema: v1 report contract in README)
MAX_PER_RUN, MAX_TEXT, MAX_LOG = 25, 2000, 4000

QUESTION = ("Failure reports and a daily check-in are on unless you turn them off, so the Hive developer can fix problems and count active Hives. "
    "Usage reports are off unless you turn them on. "
    "A failure report holds what failed, when and why (up to 2,000 characters). "
    "The check-in is one small message a day that says this Hive is running, and whether an agent has been started in it yet (yes or no). "
    "A usage report counts agent runs, restarts, limit hits and fallbacks, with the time span it covers. "
    "Every report has the Hive version, the OS and a random install id. "
    "The receiver also notes the country, region and internet provider each report comes from, never the IP address. "
    "Secrets, emails, your user name and your home folder are scrubbed on this machine first. "
    "Chat text, file contents and thread names are never sent.")

NOT_SENT = ("Reports are not being sent yet: there is no receiver set up. If you turn this on, they are scrubbed "
    "and kept on this machine only (reports/pending.jsonl), and nothing leaves it until a receiver is set.")

def question():
    return QUESTION if url() else NOT_SENT + " " + QUESTION

def home():
    return os.environ.get("HIVE_HOME") or os.path.expanduser("~")

def settings_path():
    for p in (os.path.join(home(), "settings.json"), os.path.join(home(), "hive", "settings.json")):
        if os.path.exists(p):
            return p
    return os.path.join(home(), "settings.json")

def load_share():
    try:
        s = json.load(open(settings_path())).get("share") or {}
    except (OSError, ValueError):
        s = {}
    return bool(s.get("failures") is True), bool(s.get("usage") is True)

def want_github():
    try: return (json.load(open(settings_path())).get("share") or {}).get("github") is True
    except (OSError, ValueError, AttributeError): return False

DEFAULTS_VERSION, BEAT_EVERY = 1, 23 * 3600   # 23h on a 6h timer comes out at one check-in a day

def apply_defaults():
    """Once only: a Hive from before failures and the check-in were on by default gets them on (the person's call, post 11031858ae74).
    The mark stays in the file, so turning them off afterwards holds. No settings.json, as in a source tree: nothing happens."""
    p = settings_path()
    try: c = json.load(open(p))
    except (OSError, ValueError): return
    if not isinstance(c, dict) or c.get("share_defaults"): return
    sh = c.get("share") if isinstance(c.get("share"), dict) else {}
    c["share"] = {**sh, "failures": True}; c["share_defaults"] = DEFAULTS_VERSION
    fd = os.open(p + ".tmp", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f: f.write(json.dumps(c, indent=1) + "\n")
    os.replace(p + ".tmp", p)

def setup_pending():
    """Nothing leaves before the setup screen, which shows these choices, has been passed."""
    try: return json.load(open(settings_path())).get("setup_done") is False
    except (OSError, ValueError, AttributeError): return False

def rdir():
    d = os.path.join(os.path.dirname(settings_path()), "reports"); os.makedirs(d, exist_ok=True); return d

# ---- scrubbing: patterns from the earlier consumer build, plus user and host names ----
_S = [
 (r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|$)", "[private key]"),
 (r"(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@", r"\1[cred]@"),
 (r"\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|sk-[A-Za-z0-9_-]{16,}|[sr]k_(live|test)_[A-Za-z0-9]{16,}|xox[abposr]-[A-Za-z0-9-]{10,}|(AKIA|ASIA)[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|glpat-[A-Za-z0-9_-]{16,}|npm_[A-Za-z0-9]{30,}|hf_[A-Za-z0-9]{30,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})", "[secret]"),
 (r"(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}", r"\1 [secret]"),
 (r"(?im)\b((set-)?cookie\s*:\s*)[^\r\n]+", r"\1[secret]"),
 (r"(?i)([a-z0-9_.-]*(token|secret|passw(or)?d|pwd|api[_-]?key|access[_-]?key|private[_-]?key|credential|cookie|session)[a-z0-9_.-]*[\"']?\s*[:=]\s*[\"']?)[^\s\"',;}]+", r"\1[secret]"),
 (r"(?i)([a-z0-9_.-]*auth(orization[a-z0-9_.-]*|[_.-]?[a-np-z0-9_.-][a-z0-9_.-]*|o[a-qs-z0-9_.-][a-z0-9_.-]*)?[\"']?\s*[:=]\s*[\"']?)[^\s\"',;}]+", r"\1[secret]"),
 (r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}", "[email]"),
 (r"(?i)(/home/|/Users/)[^/\s\"']+|/root\b|\b[a-z]:\\users\\[^\\\s\"']+", "~"),
]
_S = [(re.compile(a), b) for a, b in _S]
_OUTCOME = re.compile(r"(?i)^authenticat(e|ed|ion)\s*:\s*(failed|false|true|ok|succeeded|required|denied|error)$")
_LONG = re.compile(r"[A-Za-z0-9+/_=-]{32,}")

def scrub(s):
    s = str(s)
    h = os.path.expanduser("~")
    if len(h) > 1: s = s.replace(h, "~")
    for re_, with_ in _S:
        s = re_.sub(lambda m: m.group(0) if _OUTCOME.match(m.group(0)) else re_.sub(with_, m.group(0)), s)
    s = _LONG.sub(lambda m: "[secret]" if re.search(r"\d", m.group(0)) and re.search(r"[A-Za-z]", m.group(0)) else m.group(0), s)
    for name in {getpass.getuser(), socket.gethostname().split(".")[0]}:   # whole words only
        if len(name) >= 3: s = re.sub(r"(?<![A-Za-z0-9])" + re.escape(name) + r"(?![A-Za-z0-9])", "[name]", s)
    return s

def clip(s, n):
    s = " ".join(s.split())
    return s if len(s) <= n else s[:n - 1] + "…"

# ---- state, install id, version ----
def state():
    try: return json.load(open(os.path.join(rdir(), "state.json")))
    except (OSError, ValueError): return {}

def save_state(st):
    p = os.path.join(rdir(), "state.json"); open(p + ".tmp", "w").write(json.dumps(st)); os.replace(p + ".tmp", p)

def version():
    try: v = open(os.path.join(os.path.dirname(settings_path()), "VERSION")).read().strip()
    except OSError: v = "0"
    return re.sub(r"[^A-Za-z0-9._+-]+", "-", v)[:32] or "0"

def host_family():
    """linux, windows or mac: what this Hive runs on top of. The installer leaves ~/.hive-host (windows or mac) when it sets up
    inside WSL or a Mac's VM; a Hive from before that is told apart as Windows by the kernel's own name, and as a Mac by Lima's guest agent. Nothing else is read or sent."""
    try:
        m = open(os.path.join(home(), ".hive-host")).read(16).strip()
        if m in ("windows", "mac"): return m
    except OSError: pass
    try:
        if "microsoft" in open("/proc/sys/kernel/osrelease").read(200).lower(): return "windows"
    except OSError: pass
    # A Mac Hive installed before the marker runs in the Lima VM get-mac.sh makes; only the Mac line uses Lima
    if any(os.path.exists(p) for p in ("/usr/local/bin/lima-guestagent", "/mnt/lima-cidata")): return "mac"
    return platform.system().lower()

def base(st, typ):
    return {"install": st["install"], "version": installed_version(), "type": typ, "os": host_family() + "/" + platform.machine().lower()}

def url():
    try: u = json.load(open(settings_path())).get("share_url")
    except (OSError, ValueError): u = None
    d = "" if DEFAULT_URL.startswith("<") else DEFAULT_URL
    if not (u or os.environ.get("HIVE_SHARE_URL")) and installed_version() == "0": d = ""   # an exported tree that was never released (tests, trials) sends nothing to the makers
    return u or os.environ.get("HIVE_SHARE_URL") or d

# ---- build reports from the hive's own log ----
def logpath():
    return os.path.join(os.path.dirname(settings_path()), "log.jsonl")

# Log events that mean something went wrong. Only the event name is used, never its fields.
EVENT_FAILS = {"lost_subagents": "lost-subagents", "logins_paused": "logins-paused", "provider_held": "provider-held", "killed": "killed"}
EVENT_MSG = {"lost_subagents": "background helpers were lost when an agent ended", "logins_paused": "all logins were paused",
             "provider_held": "a model provider was held back", "killed": "an agent was killed"}

# ---- the public templates: the only wording a failure issue can have, here and in the makers' triage (which imports these) ----
KINDS = {
    "agent-exit": ("An agent ended with exit code {code}{cause}", "An agent's process exited with a non-zero code."),
    "lost-subagents": ("Background helpers were lost when an agent ended", "An agent ended while helpers it started were still running, so their work was lost."),
    "provider-held": ("A model provider was held back", "The starter held back a model provider."),
    "unit-failed": ("Service {unit} is in a failed state", "One of the Hive's own systemd user units was found failed by the doctor check."),
}
EXPECTED = {"limited", "logins-paused", "killed"}   # normal operation: a login at its limit, all logins paused, an agent stopped on purpose
EXPECTED_EXIT = {"-15", "143"}                       # SIGTERM: a stop or restart
# Why an agent exited non-zero, as one fixed label read from the run's own output and the starter's events around it (k1920: a Mac
# install sent 49 failed exits in 67 runs and nothing said why). Only a label from this list is ever sent, never the output's text.
EXIT_CAUSES = ("login rejected", "never started", "resume failed", "stalled", "connection error", "turn budget", "error result", "no result")
MSG = {
    "agent-exit": re.compile(r"^an agent ended with exit code (?P<code>-?\d{1,4})(?:: (?P<cause>%s))?$" % "|".join(map(re.escape, EXIT_CAUSES))),
    "lost-subagents": re.compile(r"^background helpers were lost when an agent ended$"),
    "provider-held": re.compile(r"^a model provider was held back$"),
    "unit-failed": re.compile(r"^systemd unit (?P<unit>(?:hive|hive|\[name\])-[a-z0-9@_.-]{1,60}\.(?:service|timer)) is in a failed state(?P<why>:.*)?$"),
}
MARK = re.compile(r"<!-- hive-auto-report sig=(\S+) -->")

def classify(r):
    """(signature, public fields) for a failure report that may go public, (None, why not) for one that stays private."""
    kind, msg = r.get("kind") or "", " ".join(str(r.get("message") or "").split())
    if kind in EXPECTED: return None, "expected"
    if kind not in KINDS: return None, "unknown kind"
    m = MSG[kind].match(msg)
    if not m: return None, "unknown message"
    f = {k: v for k, v in m.groupdict().items() if v and k != "why"}
    if "unit" in f: f["unit"] = f["unit"].replace("[name]-", "hive-")   # older clients scrubbed a user named hive out of unit names
    if f.get("code") in EXPECTED_EXIT: return None, "expected"
    return kind + "".join(":" + f[k].replace(" ", "-") for k in sorted(f)), f   # no spaces: the issue's marker reads sig=\S+

def title_of(kind, fields):
    """An issue's title from its template; the optional cause reads ": why" after the code."""
    return KINDS[kind][0].format(**{"cause": "", **fields, **({"cause": ": " + fields["cause"]} if fields.get("cause") else {})})

def osname(o):
    return {"linux": "Linux", "darwin": "macOS", "windows": "Windows"}.get((o or "").split("/")[0], "Other")

def vkey(v):
    return [(0, int(x), "") if x.isdigit() else (1, 0, x) for x in re.split(r"[.+-]", str(v).lstrip("v"))]

def issue_text(sig, fields, count, hives, versions, oses, first, last, by="a Hive's failure reporting"):
    """(title, body) of a failure issue, laid out like the repo's bug form. Only the template, counts, versions and OS go in."""
    import collections
    kind = sig.split(":")[0]
    title = title_of(kind, fields)
    vs = sorted(versions, key=vkey) or ["unknown"]
    c = collections.Counter(osname(o) for o in oses)
    b = ("### What happened, and what you expected\n\n%s Reported automatically by Hives that share failure reports. "
         "Expected: this doesn't happen.\n\n### Version\n\n%s\n\n### Where it runs\n\n%s\n\n### How you installed it\n\n_No response_\n\n"
         "### Output or log, if you have it\n\n```text\nkind: %s\n%s\n```\n\n"
         "| | |\n|---|---|\n| Reports | %d |\n| Hives | %d |\n| Versions | %s |\n| OS | %s |\n| First seen | %s |\n| Last seen | %s |\n\n"
         "_Opened by %s: only the failure kind, its message, versions and counts are shown; nothing from anyone's chats or files._\n\n"
         "<!-- hive-auto-report sig=%s -->\n") % (
        KINDS[kind][1], vs[-1], ", ".join(sorted(c)), kind, title.lower(), count, hives, ", ".join(vs),
        ", ".join("%s (%d)" % x for x in c.most_common()), first, last, by, sig)
    return title, b

def update_why(unit):
    """Why the updater failed: its own last lines, which are git's and install.sh's errors, and never the starter's or the app's, which can quote chat."""
    if not unit.endswith("-update.service"): return ""
    import subprocess
    try: out = subprocess.run(["journalctl", "--user", "-u", unit, "-n", "3", "-o", "cat", "--no-pager"], capture_output=True, text=True, timeout=20).stdout.strip()
    except Exception: out = ""
    if not out:
        try: out = json.load(open(os.path.join(home(), ".hive", "update.json"))).get("error") or ""
        except (OSError, ValueError, AttributeError): out = ""
    return ": " + str(out) if out else ""

def unit_failures(st):
    """The doctor check: the Hive's own systemd user units that are in a failed state. Names only (hive-*/hive-*),
    reported once per failure; a unit that recovers is forgotten, so a later failure is reported again."""
    import subprocess
    try: out = subprocess.run(["systemctl", "--user", "list-units", "--failed", "--no-legend", "--plain"], capture_output=True, text=True, timeout=20).stdout
    except Exception: return []
    now = sorted({l.split()[0] for l in out.splitlines() if l.split() and re.match(r"^([h]2|hive)-[a-z0-9@_.-]{1,60}\.(service|timer)$", l.split()[0])})
    seen = st.get("units", []); st["units"] = now
    reps = []
    for u in now:
        if u in seen: continue
        r = base(st, "failure"); r.update(kind="unit-failed", at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), message=clip(("systemd unit %s is in a failed state" % u) + scrub(update_why(u)), MAX_TEXT)); reps.append(r)   # the unit name is checked above; scrubbing it would turn hive-* into [name]-* for a user named hive
    return reps

AUTH_TEXT = re.compile(r"not logged in|login expired|please run /login|failed to authenticate|authentication_failed|invalid api key|oauth token (has|is|was)", re.I)
NET_TEXT = re.compile(r"connection|network|ECONN|ETIMEDOUT|ENOTFOUND|EAI_AGAIN|fetch failed|socket|overloaded|API Error: 5\d\d", re.I)

def exit_cause(out, events=()):
    """One label from EXIT_CAUSES for a run that exited non-zero, read from its output file's tail and the starter's events
    for the same run (stalled, login_rejected, launch_failed, wake_failed). The text is matched here and never leaves."""
    events = set(events)
    if "login_rejected" in events: return "login rejected"
    if "stalled" in events: return "stalled"
    if "launch_failed" in events: return "never started"
    try:
        with open(out, "rb") as f:
            f.seek(0, 2); cut = f.tell() > 262144; f.seek(max(0, f.tell() - 262144)); lines = f.read().decode(errors="replace").splitlines()
    except (OSError, TypeError, ValueError): return None
    lines = lines[1 if cut else 0:][-200:]   # a cut tail starts mid-line
    js, said, result = 0, [], None
    for line in lines:
        try: e = json.loads(line)
        except (ValueError, RecursionError):
            if line.strip(): said.append(line)
            continue
        if not isinstance(e, dict): continue
        js += 1
        t = e.get("type")
        if t in ("result", "turn.failed"): result = e   # turn.failed: a codex run's error end
        if t == "assistant" and e.get("error"): said.append(str(e["error"]))
        if t in ("error", "turn.failed"):
            er = e.get("error"); said.append(str(e.get("message") or (er.get("message", "") if isinstance(er, dict) else er or "")))
    if result is not None and result.get("type") == "result" and not result.get("is_error"): return None   # it finished cleanly: its text is the agent's own words
    if result is not None: said.append(str(result.get("result", "")))
    said = said[-6:]
    if "wake_failed" in events: return "resume failed"   # most often "No conversation found", with no JSON line at all
    if any(AUTH_TEXT.search(x) for x in said): return "login rejected"
    if not js and not cut: return "never started"
    if any(NET_TEXT.search(x) for x in said): return "connection error"
    if result is not None and str(result.get("subtype", "")).startswith("error_max"): return "turn budget"
    return "error result" if result is not None else "no result"

def around(evs, end):
    """Event names the starter logged about one run: from its start to the next start of the same agent, so a stall before
    the end and a rejected login or failed launch just after it are both seen."""
    i = next((j for j, x in enumerate(evs) if x is end), None)
    if i is None: return []
    lo = next((j for j in range(i - 1, -1, -1) if evs[j].get("event") == "start"), 0)
    hi = next((j for j in range(i + 1, len(evs)) if evs[j].get("event") == "start"), len(evs))
    return [x.get("event") for x in evs[lo:hi] if x.get("session") in (None, end.get("session")) or x.get("pid") == end.get("pid")]

def build(st, want_fail, want_use):
    """Reports for log events past the saved offset. Only counts and kinds are read;
    thread names, posts, paths of work and agent output never go in."""
    try: f = open(logpath(), "rb")
    except OSError: return [], st["offset"]
    f.seek(0, 2); size = f.tell()
    off = st["offset"] if st["offset"] <= size else 0
    f.seek(off); raw = f.read(); raw = raw[:raw.rfind(b"\n") + 1]
    fails, use, first, last = [], {"runs": 0, "restarts": 0, "failed_exits": 0, "limit_hits": 0, "fallbacks": 0}, None, None
    evs = []
    for line in raw.splitlines():
        try: evs.append(json.loads(line))
        except ValueError: continue
    # a run that hits the usage limit exits 1 after a "limited" event (same name+session); that is not a crash
    limited = {(x.get("name"), x.get("session")) for x in evs if x.get("event") == "limited"}
    # A run the starter ended itself to hand it new posts (woken, then SIGKILL) is not a crash either
    woken = {(x.get("name"), x.get("pid")) for x in evs if x.get("event") == "woken"}
    outs = st.setdefault("outs", {})   # name:pid -> the run's output file, kept across sends for a run that spans two
    near = {}                          # name -> starter events about that agent, in order, to read a run's cause from
    for x in evs:
        if isinstance(x.get("name"), str): near.setdefault(x["name"], []).append(x)
    for e in evs:
        at, ev = e.get("at", ""), e.get("event")
        if at < st["since"]: continue
        first, last = first or at, at
        if ev == "start":
            use["runs"] += 1
            if e.get("name") and e.get("out"): outs["%s:%s" % (e["name"], e.get("pid"))] = e["out"]
        elif ev == "wake_resumed": use["restarts"] += 1
        elif ev == "limited": use["limit_hits"] += 1
        elif ev == "fallback": use["fallbacks"] += 1
        elif ev == "end" and e.get("exit") not in (0, None) and (e.get("name"), e.get("session")) not in limited and (e.get("name"), e.get("pid")) not in woken:
            use["failed_exits"] += 1
            try: cause = exit_cause(outs.get("%s:%s" % (e.get("name"), e.get("pid"))), around(near.get(e.get("name"), []), e))
            except Exception: cause = None   # a cause is a nicety: never let it stop the reports
            fails.append(("agent-exit", at, "an agent ended with exit code %s" % e.get("exit") + (": " + cause if cause in EXIT_CAUSES else "")))
        if ev == "end": outs.pop("%s:%s" % (e.get("name"), e.get("pid")), None)
        if ev == "limited": fails.append(("limited", at, "a login hit its usage limit"))
        elif ev in EVENT_FAILS: fails.append((EVENT_FAILS[ev], at, EVENT_MSG[ev]))
    if len(outs) > 200:   # runs whose end was never logged (a crash of the starter): keep the newest
        for k in list(outs)[:-200]: outs.pop(k)
    reps = []
    if want_fail:
        for kind, at, msg in fails:
            r = base(st, "failure"); r.update(kind=kind, at=at[:19] + "Z", message=clip(scrub(msg), MAX_TEXT)); reps.append(r)
    if want_use and any(use.values()):
        r = base(st, "usage"); r.update(at=last[:19] + "Z", message="events from %s to %s" % (first[:19] + "Z", last[:19] + "Z"), usage=use); reps.append(r)
    return reps, off + len(raw)

def pend_path(): return os.path.join(rdir(), "pending.jsonl")

def pending():
    try: return [json.loads(l) for l in open(pend_path()) if l.strip()]
    except OSError: return []

def keep(q):
    if q: open(pend_path(), "w").write("".join(json.dumps(r) + "\n" for r in q))
    elif os.path.exists(pend_path()): os.remove(pend_path())

def post(body):
    if not url(): return 0
    req = urllib.request.Request(url(), body, {"Content-Type": "application/json", "User-Agent": "hive-report/1"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r: return r.status
    except urllib.error.HTTPError as e: return e.code
    except Exception: return 0

def agent_started(st):
    """True once this Hive's own log has a start event (an agent session was launched). Only a yes/no leaves the machine.
    The answer is kept in state once it is yes, so the log is read at most until then; call it only after the share check."""
    if st.get("agent_started"): return True
    try:
        with open(logpath(), "rb") as f:
            for line in f:
                if b'"event": "start"' in line or b'"event":"start"' in line:
                    st["agent_started"] = True; return True
    except OSError: pass
    return False

def installed_state():
    """The state, with an install id made on first use. Any first use counts: a bundle can come before the first send."""
    st = state()
    if not st.get("install"):
        st = {"install": uuid.uuid4().hex, "since": time.strftime("%Y-%m-%dT%H:%M:%S+00:00", time.gmtime()), "offset": 0}
        try: st["offset"] = os.path.getsize(logpath())   # nothing from before the opt-in
        except OSError: pass
        save_state(st)
    return st

def send(dry=False):
    if not dry: apply_defaults()
    if setup_pending(): return "setup isn't finished; nothing read or sent"
    wf, wu = load_share(); wg = want_github()
    if not (wf or wu or wg): return "sharing is off; nothing read or sent"
    st = installed_state()
    new, off = build(st, wf or wg, wu)
    if wf or wg: new += unit_failures(st)
    gh_note, fails = "", [r for r in new if r["type"] == "failure"]
    if wg and not dry: gh_note = "; github: " + github(st, fails)
    if not wf: new = [r for r in new if r["type"] != "failure"]
    if wu and not st.get("hello"):                    # the first send after opting in says so, with counts of nothing, so a quiet Hive can be told from a broken route
        r = base(st, "usage"); r.update(at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), message="sharing is on", usage={"runs": 0}); new.insert(0, r); st["hello"] = True
    q = [r for r in pending() if r["type"] in ("failure", "heartbeat") and wf or (r["type"] == "usage" and wu)] + new
    if wf and time.time() - st.get("beat", 0) >= BEAT_EVERY:
        r = base(st, "heartbeat"); r["agent_started"] = agent_started(st); r["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        q = [x for x in q if x["type"] != "heartbeat"] + [r]; st["beat"] = time.time()   # a check-in that waited offline is replaced, and goes last so the cap can't drop it
    if len(q) > MAX_PER_RUN: q = q[-MAX_PER_RUN:]
    if dry: return "would send %d report(s):\n" % len(q) + "\n".join(json.dumps(r) for r in q) + ("\n" + github(st, fails, dry=True) if wg else "")
    st["offset"] = off; save_state(st); keep(q)
    sent = refused = 0; run = time.strftime("%y%m%d-%H%M%S"); os.makedirs(os.path.join(rdir(), "sent"), exist_ok=True)
    for i, r in enumerate(q):
        body = json.dumps(r, sort_keys=True).encode()
        name = os.path.join(rdir(), "sent", "%s-%02d.json" % (run, i))
        open(name, "wb").write(body)                      # the copy exists before the report leaves
        code = post(body)
        if 200 <= code < 300: sent += 1
        elif code in (400, 411, 413, 415): refused += 1  # never taken; the copy stays
        else:
            os.remove(name); keep(q[i:])
            return "sent %d, refused %d; %s; %d wait for the next run" % (sent, refused, ("%s answered %d" % (url(), code)) if code else ("no endpoint set (share_url)" if not url() else "couldn't reach " + url()), len(q) - i) + gh_note
    keep([])
    return "sent %d, refused %d" % (sent, refused) + gh_note

def sent_files():
    d = os.path.join(rdir(), "sent")
    return sorted(os.path.join(d, f) for f in os.listdir(d)) if os.path.isdir(d) else []

# ---- tier 3: "everything" and issues. Neither runs on the timer; each is a command the person runs. ----
def share_cfg():
    try: return json.load(open(settings_path()))
    except (OSError, ValueError): return {}

def bundle_url():
    u = share_cfg().get("share_bundle_url") or os.environ.get("HIVE_SHARE_BUNDLE_URL")
    d = url()
    return u or (d.rsplit("/", 1)[0] + "/bundle" if d.endswith("/report") else "")

def feedback_url():
    u = share_cfg().get("share_feedback_url") or os.environ.get("HIVE_SHARE_FEEDBACK_URL")
    d = url()
    return u or (d.rsplit("/", 1)[0] + "/feedback" if d.endswith("/report") else "")

def installed_version():
    """The release update.py recorded, which is what the app shows; the VERSION file for a Hive without one."""
    try: v = json.load(open(os.path.join(home(), ".hive", "update.json"))).get("tag")
    except (OSError, ValueError, AttributeError): v = None
    return re.sub(r"[^A-Za-z0-9._+-]+", "-", v)[:32] if v else version()

FEEDBACK_REFUSED = {413: "it's too large", 429: "this Hive has sent its limit of feedback for today", 507: "the makers' inbox is full for today"}

def feedback(raw):
    """The app's dialog pressed Send. Comment scrubbed here; the picture is what the person saw and framed in the dialog."""
    try: d = json.loads(raw); text, page, image = d.get("text") or "", d.get("page") or "app", d.get("image")
    except (ValueError, AttributeError): raise SystemExit("not sent: that wasn't a feedback report")
    u = feedback_url()
    if not u: raise SystemExit("not sent: this Hive has no feedback address")
    st = installed_state()
    r = {"install": st["install"], "version": installed_version(), "type": "feedback",
         "page": re.sub(r"[^A-Za-z0-9_-]+", "-", str(page))[:32] or "app", "text": scrub(str(text))[:8000]}
    if image: r["image"] = image
    body = json.dumps(r, ensure_ascii=False).encode()
    sent = os.path.join(rdir(), "sent"); os.makedirs(sent, exist_ok=True)
    with open(os.path.join(sent, time.strftime("%y%m%d-%H%M%S") + "-feedback.json"), "wb") as f: f.write(body)
    req = urllib.request.Request(u, body, {"Content-Type": "application/json", "User-Agent": "hive-report/1"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp: return "sent (%d)" % resp.status
    except urllib.error.HTTPError as e:
        raise SystemExit("not sent: " + FEEDBACK_REFUSED.get(e.code, "the makers' server refused it (%d)" % e.code))
    except Exception:
        raise SystemExit("not sent: couldn't reach the makers' server. Is this machine online?")

def bundle(yes=False):
    """Scrubbed copy of the log tail and board tail (chats included). Needs share.everything and, to send, --yes."""
    c = share_cfg()
    if (c.get("share") or {}).get("everything") is not True: return "share everything is off; nothing collected"
    root = os.path.dirname(settings_path()); parts = {}
    for n in ("log.jsonl", "board.jsonl"):
        try: parts[n] = scrub("".join(open(os.path.join(root, n), errors="replace").readlines()[-500:]))[-150000:]
        except OSError: pass
    st = installed_state(); body = json.dumps({"install": st["install"], "version": version(), "type": "bundle", "files": parts}).encode()
    bd = os.path.join(rdir(), "bundles"); os.makedirs(bd, exist_ok=True)
    name = os.path.join(bd, time.strftime("%y%m%d-%H%M%S") + ".json")
    with open(name, "wb") as f: f.write(body)
    u = bundle_url()
    if not yes or not u: return "bundle kept at %s (%d bytes); read it first. %s" % (name, len(body), "Send with --yes." if u else "No bundle receiver is set, so it is not sent.")
    req = urllib.request.Request(u, body, {"Content-Type": "application/json", "User-Agent": "hive-report/1"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r: return "bundle sent (%d)" % r.status
    except Exception as e: return "bundle not sent: %s" % e

PACK_DIRS = ("projects", "files", "kb")
PACK_SKIP_DIRS = {".git", "node_modules", "__pycache__", ".venv", "venv", ".cache", ".pytest_cache", ".mypy_cache", "target", ".next"}
PACK_SECRET = re.compile(r"(?i)^(\.env(\..*)?|.*\.(pem|key|p12|pfx|keystore)|id_(rsa|ed25519|ecdsa|dsa).*|credentials.*|.*token.*|.*secret.*|\.netrc|\.npmrc|\.pypirc)$")
PACK_FILE_MAX, PACK_MAX, PACK_PART = 50 << 20, 100 << 20, 8 << 20
PACK_REFUSED = {413: "it's too large", 429: "this Hive has already sent everything once today", 507: "the makers' inbox is full for today"}

def pack_url():
    u = share_cfg().get("share_pack_url") or os.environ.get("HIVE_SHARE_PACK_URL")
    d = url()
    return u or (d.rsplit("/", 1)[0] + "/pack" if d.endswith("/report") else "")

def _pack_text(b):
    """Scrubbed text for a file that reads as UTF-8 and is under 4 MB, else None (sent as it is)."""
    if len(b) > 4 << 20 or b"\0" in b[:8192]: return None
    try: t = b.decode("utf-8")
    except UnicodeDecodeError: return None
    return scrub(t).encode("utf-8")

def pack(yes=False):
    """Everything as one zip: the whole board and log, every project folder, post attachments and the kb, text files
    scrubbed. Left out: .git and build caches, key and .env files, any file over 50 MB, anything past 100 MB in all.
    Kept in reports/bundles; sent (in 8 MB pieces) only with --yes and share.everything on."""
    import zipfile
    if yes and (share_cfg().get("share") or {}).get("everything") is not True: return "share everything is off; nothing collected"
    root, hm = os.path.dirname(settings_path()), home()
    bd = os.path.join(rdir(), "bundles"); os.makedirs(bd, exist_ok=True)
    name = os.path.join(bd, "everything-" + time.strftime("%y%m%d-%H%M%S") + ".zip")
    total, n, skipped, seen = 0, 0, [], set()
    def add(z, path, arc):
        nonlocal total, n
        try: size = os.path.getsize(path)
        except OSError: return
        if size > PACK_FILE_MAX or total + size > PACK_MAX: skipped.append(arc); return
        try: b = open(path, "rb").read()
        except OSError: return
        t = _pack_text(b)
        z.writestr(arc, t if t is not None else b); total += size; n += 1
    with zipfile.ZipFile(name, "w", zipfile.ZIP_DEFLATED) as z:
        for f in ("board.jsonl", "log.jsonl"):
            if os.path.isfile(os.path.join(root, f)): add(z, os.path.join(root, f), f)
        for base in dict.fromkeys((root, hm)):
            for d in PACK_DIRS:
                top = os.path.realpath(os.path.join(base, d))
                if top in seen or not os.path.isdir(top): continue
                seen.add(top)
                for dp, dns, fns in os.walk(top):
                    dns[:] = sorted(x for x in dns if x not in PACK_SKIP_DIRS and not os.path.islink(os.path.join(dp, x)))
                    for fn in sorted(fns):
                        p = os.path.join(dp, fn)
                        if os.path.islink(p) or not os.path.isfile(p): continue
                        arc = os.path.join(d, os.path.relpath(p, top))
                        if PACK_SECRET.match(fn): skipped.append(arc); continue
                        add(z, p, arc)
        z.writestr("PACK.txt", "Hive %s, %d files, %d bytes before zipping.\nLeft out (too big, past the 100 MB total, or looked like a key):\n%s\n"
                   % (installed_version(), n, total, "\n".join(skipped) or "nothing"))
    size = os.path.getsize(name)
    said = "%d files (%.1f MB zipped)%s" % (n, size / 1e6, ", %d left out (listed in PACK.txt)" % len(skipped) if skipped else "")
    u = pack_url()
    if not yes: return "everything kept at %s: %s. %s" % (name, said, "Send with --yes." if u else "No receiver is set, so it is not sent.")
    if not u: return "no receiver is set; kept at %s" % name
    data = open(name, "rb").read(); parts = max(1, -(-len(data) // PACK_PART)); pid = uuid.uuid4().hex[:16]
    st = installed_state()
    for i in range(parts):
        h = {"Content-Type": "application/zip", "User-Agent": "hive-report/1", "X-Install": st["install"], "X-Version": installed_version(),
             "X-Pack": pid, "X-Part": str(i), "X-Parts": str(parts)}
        req = urllib.request.Request(u, data[i * PACK_PART:(i + 1) * PACK_PART], h)
        try:
            with urllib.request.urlopen(req, timeout=120) as r: pass
        except urllib.error.HTTPError as e:
            return "everything not sent (piece %d of %d): %s; kept at %s" % (i + 1, parts, PACK_REFUSED.get(e.code, "the makers' server refused it (%d)" % e.code), name)
        except Exception as e: return "everything not sent: %s; kept at %s" % (e, name)
    return "everything sent: %s" % said

def issue(title, text, submit=False):
    """A scrubbed issue draft for the Hive's open-source repo. Default: a prefilled link the person opens and
    submits as themselves, so no login or token is needed. --submit uses an already-logged-in `gh`, and only
    on this explicit command; the Hive never opens one on its own."""
    import urllib.parse, shutil, subprocess
    repo = share_cfg().get("issues_repo") or "https://github.com/ihsan-sa/hive"
    if "<" in repo: return "no repository is set for issues (issues_repo in settings.json)"
    repo = repo.replace("https://github.com/", "").strip("/")
    t, b = clip(scrub(title), 200), scrub(text)[:6000]
    draft = os.path.join(rdir(), "issue-draft.md")
    with open(draft, "w") as f: f.write("# %s\n\n%s\n" % (t, b))
    if submit:
        if not shutil.which("gh") or subprocess.run(["gh", "auth", "status"], capture_output=True).returncode: return "gh isn't logged in; use the link instead (run without --submit)"
        r = subprocess.run(["gh", "issue", "create", "-R", repo, "-t", t, "-b", b], capture_output=True, text=True)
        return r.stdout.strip() or r.stderr.strip()
    q = {"template": "bug.yml", "title": t, "what": b[:2500], "version": installed_version(), "body": b[:2500]}   # the bug form's fields; body is for a repo without the form
    return "draft: %s\nopen this to review and post it as yourself:\nhttps://github.com/%s/issues/new?%s" % (draft, repo, urllib.parse.urlencode(q))

# ---- GitHub from the person's own login: only with share.github ticked and gh logged in; run by the timer, never by an agent ----
GH_PER_DAY, GH_QUEUE, MAX_DIFF, DRAFT_DAYS = 3, 20, 50000, 30

def gh_run(*a, cwd=None, timeout=60):
    import subprocess
    try: return subprocess.run(list(a) if a[0] == "git" else ["gh", *a], capture_output=True, text=True, timeout=timeout, cwd=cwd)
    except Exception as e: return subprocess.CompletedProcess(a, 1, "", str(e))

def gh_ready():
    import shutil
    return bool(shutil.which("gh")) and gh_run("auth", "status", timeout=20).returncode == 0

def issues_repo():
    repo = share_cfg().get("issues_repo") or "https://github.com/ihsan-sa/hive"
    return "" if "<" in repo else repo.replace("https://github.com/", "").strip("/")

def gh_left(st):
    day = time.strftime("%Y-%m-%d", time.gmtime())
    if st.get("gh_day") != day: st["gh_day"], st["gh_n"] = day, 0
    return GH_PER_DAY - st["gh_n"]

def find_issue(repo, sig, title):
    """The open issue with this signature's marker: its number, 0 for none, None if GitHub couldn't be asked.
    Found by its fixed title, then checked by the marker; labels aren't relied on, since only collaborators can set them."""
    r = gh_run("issue", "list", "-R", repo, "--state", "open", "--search", '"%s" in:title' % title, "--json", "number,body", "-L", "30")
    if r.returncode: return None
    try: found = json.loads(r.stdout or "[]")
    except ValueError: return None
    for i in found:
        m = MARK.search(i.get("body") or "")
        if m and m.group(1) == sig: return i["number"]
    return 0

def issue_number(out):
    m = re.search(r"/(?:issues|pull)/(\d+)", out or ""); return int(m.group(1)) if m else 0

def file_one(repo, it):
    """One failure, one GitHub action: a comment on its open issue, or a new issue from the template. Its URL or number, or None."""
    sig, f, v, o, at = it["sig"], it["fields"], it["version"], it["os"], it["at"]
    title, body = issue_text(sig, f, 1, 1, [v], [o], at, at)
    n = find_issue(repo, sig, title)
    if n is None: return None
    if n:
        r = gh_run("issue", "comment", str(n), "-R", repo, "-b", "Seen again by a Hive: version %s, %s, %s." % (v, osname(o), at[:10]))
        return n if r.returncode == 0 else None
    r = gh_run("issue", "create", "-R", repo, "-t", title, "-b", body, "-l", "auto-report")
    if r.returncode: r = gh_run("issue", "create", "-R", repo, "-t", title, "-b", body)   # the label is only for those who may set it
    return (issue_number(r.stdout) or "new") if r.returncode == 0 else None

def github(st, fails, dry=False):
    """Failures that fit a public template, plus fixes queued by `contribute`, sent with the person's gh, GH_PER_DAY at most."""
    repo = issues_repo()
    if not repo: return "no repository set (issues_repo)"
    q, seen = list(st.get("gh_queue", [])), st.setdefault("gh_seen", {})
    for r in fails:
        sig, f = classify(r)
        v = r.get("version") or "unknown"
        if not sig or v in seen.get(sig, {}).get("v", []) or any(x["sig"] == sig and x["version"] == v for x in q): continue
        q.append({"sig": sig, "fields": f, "version": v, "os": r.get("os") or "", "at": r.get("at") or ""})
    q = q[-GH_QUEUE:]; ready = [x["id"] for x in drafts() if x["approved"]]
    if dry: return "github: would file or comment on %d issue(s)%s and send %d approved fix(es), at most %d a day" % (len(q), (" (" + ", ".join(x["sig"] for x in q) + ")") if q else "", len(ready), GH_PER_DAY)
    st["gh_queue"] = q
    if not (q or outbox()): return "nothing to file"
    if not (q or ready):
        for p in outbox(): send_draft(repo, p, st)   # only expires old ones
        return "%d fix(es) wait for the person's Send" % len(outbox())
    if not gh_ready(): return "gh isn't logged in; %d waiting" % (len(q) + len(ready))
    done = []
    while q and gh_left(st) > 0:
        it = q[0]; n = file_one(repo, it)
        if n is None: break                       # GitHub didn't answer: try again next run
        st["gh_n"] += 1; q.pop(0)
        e = seen.setdefault(it["sig"], {"v": []}); e["n"] = n; e["v"] = (e["v"] + [it["version"]])[-20:]
        done.append("#%s" % n)
    st["gh_queue"] = q
    for p in outbox():
        if gh_left(st) <= 0: break
        u = send_draft(repo, p, st)
        if u: done.append(u)
    left = len(q) + len(outbox())
    return ("filed " + ", ".join(done) if done else "nothing filed") + ("; %d waiting" % left if left else "")

# ---- fixes an agent made in this Hive's own core, sent back as a pull request or an issue ----
def root(): return os.path.dirname(settings_path())
def core_dir(): return os.path.join(root(), "core")
def obox(*p):
    """Where drafts end up once they leave the waiting list: sent, failed, expired, discarded. The sandbox can't see it."""
    d = os.path.join(rdir(), "outbox", *p); os.makedirs(d, exist_ok=True); return d
def ddir():
    """The waiting drafts. The sandbox mounts this one folder writable, so an agent inside can draft; it can't approve."""
    d = os.path.join(rdir(), "drafts"); os.makedirs(d, exist_ok=True); return d
DRAFT_ID = re.compile(r"^[0-9]{6}-[0-9]{6}-[0-9a-f]{6}$")
DRAFT_MAX = 20
def outbox(): return sorted(os.path.join(ddir(), f) for f in os.listdir(ddir()) if f.endswith(".json") and DRAFT_ID.match(f[:-5]))

def contribute(title, text, commit=None, diff=None):
    """Draft a fix (or a bug found while working) for the Hive's repo. It goes only after the person reads it and presses Send
    in Settings, and then the reporter's timer sends it with their gh, outside the sandbox. An agent never pushes or approves."""
    if not want_github():
        return ("not drafted: sending to GitHub is off (Settings > Data sharing, 'Open GitHub issues about failures from my GitHub login'). "
                "A fix committed in ~/core with a subject starting 'local fix:' is still kept across updates.")
    if not title.strip() or not text.strip(): return "not drafted: give a title and, on stdin, what was wrong and how the fix works"
    if len(outbox()) >= DRAFT_MAX: return "not drafted: %d drafts already wait for the person; ask them to send or discard some" % DRAFT_MAX
    sha, raw = None, diff or ""
    if commit:
        r = gh_run("git", "-C", core_dir(), "rev-parse", "--verify", "-q", commit + "^{commit}")
        if r.returncode: return "not drafted: %s isn't a commit in %s" % (commit, core_dir())
        sha = r.stdout.strip()
        raw = gh_run("git", "-C", core_dir(), "show", "--format=", sha).stdout
        if not raw.strip(): return "not drafted: %s changes nothing" % commit
    patch = scrub(raw) if raw.strip() else ""
    if len(patch) > MAX_DIFF: return "not drafted: the patch is over %d characters; send a smaller fix" % MAX_DIFF
    clean = bool(sha) and patch == raw
    d = {"title": clip(scrub(title), 200), "text": scrub(text)[:6000], "commit": sha, "patch": patch, "pr": clean,
         "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    p = os.path.join(ddir(), time.strftime("%y%m%d-%H%M%S-") + uuid.uuid4().hex[:6] + ".json")
    with open(p, "w") as f: f.write(json.dumps(d, indent=1))
    return ("drafted %s. Nothing is sent until the person reads it and presses Send in Settings > Data sharing; then it goes %s. "
            "Tell the person it's waiting there. Unsent drafts are dropped after %d days.") % (
        os.path.basename(p)[:-5], "as a pull request from their fork (or an issue with the patch, if it doesn't apply)" if clean else ("as an issue with the patch" if patch else "as an issue"), DRAFT_DAYS)

# ---- gaps in the tools the Hive installs: an issue on the tool's own repo, offered to the person ----
def upstream_repos():
    """{owner/name: label}: this Hive's repo and the repos of the tools it installs (modules.json), nothing the agent names."""
    out = {}
    r = issues_repo()
    if r: out[r.lower()] = "hive"
    for p in (os.path.join(core_dir(), "modules.json"), os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "modules.json")):
        try: mods = json.load(open(p))
        except (OSError, ValueError): continue
        for name, m in (mods.items() if isinstance(mods, dict) else []):
            u = m.get("repo") if isinstance(m, dict) else None
            mt = re.match(r"https://github\.com/([\w.-]+/[\w.-]+?)(?:\.git)?/?$", u or "")
            if mt: out[mt.group(1).lower()] = name
        break
    return out

def denied(repo):
    try: orgs = [l.strip().lower() for l in open(os.path.join(home(), ".cc", "github-deny")) if l.strip() and not l.startswith("#")]
    except OSError: return False
    return repo.split("/")[0].lower() in orgs

def find_dups(repo, title):
    """Open and closed issues on repo that look like this one: [(url, title)], or None if GitHub couldn't be asked."""
    import urllib.parse
    words = re.findall(r"[A-Za-z0-9_.+-]{3,}", title)[:8]
    if not words: return []
    q = urllib.parse.quote("repo:%s is:issue %s" % (repo, " ".join(words)))
    try:
        req = urllib.request.Request("https://api.github.com/search/issues?per_page=10&q=" + q, headers={"Accept": "application/vnd.github+json", "User-Agent": "hive-report"})
        items = json.load(urllib.request.urlopen(req, timeout=15)).get("items", [])
    except Exception: return None
    return [(i["html_url"], i["title"]) for i in items if isinstance(i, dict) and "html_url" in i][:5]

def similar(a, b):
    w = lambda t: set(x.lower() for x in re.findall(r"[A-Za-z0-9]{3,}", t))
    x, y = w(a), w(b)
    return bool(x and y) and len(x & y) / min(len(x), len(y)) >= 0.7

AI_NOTE = ("_Drafted by an AI agent (Claude) working in a user's Hive while using this tool. The user read this text before it was filed "
           "and filed it from their own GitHub account._")

def upstream_draft(repo, title, text):
    """An issue for a tool's own repo, held for the person's Send like any draft. Returns what to say; the exact text is printed for them."""
    if not want_github():
        return ("not drafted: sending to GitHub is off (Settings > Data sharing, 'Open GitHub issues about failures from my GitHub login'). "
                "Tell the person about the gap in your own words and let them decide.")
    repo = (repo or "").replace("https://github.com/", "").strip("/").lower()
    ok = upstream_repos()
    if repo not in ok or denied(repo):
        return "not drafted: %s isn't a project this Hive names as its upstream. Allowed: %s" % (repo or "(no --repo)", ", ".join(sorted(ok)) or "none")
    if not title.strip() or not text.strip(): return "not drafted: give --repo, a title and, on stdin, what is missing and what you saw"
    if len(outbox()) >= DRAFT_MAX: return "not drafted: %d drafts already wait for the person; ask them to send or discard some" % DRAFT_MAX
    t, body = clip(scrub(title), 200), scrub(text)[:5500]
    dups = find_dups(repo, t)
    near = [(u, x) for u, x in (dups or []) if similar(t, x)]
    if near:
        return "not drafted: this looks like an existing issue, so point the person to it instead of filing another: " + "; ".join("%s (%s)" % (u, x) for u, x in near)
    note = "" if dups is not None else "\n\n_The search for an existing issue could not run when this was drafted._"
    d = {"title": t, "text": body + note + "\n\n" + AI_NOTE, "commit": None, "patch": "", "pr": False, "repo": repo,
         "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    p = os.path.join(ddir(), time.strftime("%y%m%d-%H%M%S-") + uuid.uuid4().hex[:6] + ".json")
    with open(p, "w") as f: f.write(json.dumps(d, indent=1))
    return ("drafted %s for %s. Nothing is sent until the person reads it and presses Send in Settings > Data sharing. Show them this, exactly as it would be filed, and say it waits there:\n\n"
            "Repository: %s\nTitle: %s\n\n%s\n") % (os.path.basename(p)[:-5], repo, repo, d["title"], d["text"])

def rp(v):
    """The repo a draft is for: only one this Hive names as upstream (the folder is writable from the sandbox); else none, meaning the Hive's own."""
    v = v.lower() if isinstance(v, str) else ""
    return v if v in upstream_repos() and not denied(v) else None

def load_draft(p):
    """A draft as it would go: scrubbed again on reading, since the folder is writable from inside the sandbox."""
    try: d = json.load(open(p))
    except (OSError, ValueError): return None
    if not isinstance(d, dict): return None
    t = lambda k, n: scrub(d.get(k) if isinstance(d.get(k), str) else "")[:n]
    c = d.get("commit") if isinstance(d.get("commit"), str) and re.fullmatch(r"[0-9a-f]{40}", d.get("commit") or "") else None
    return {"title": clip(t("title", 400), 200), "text": t("text", 6000), "commit": c, "patch": t("patch", MAX_DIFF),
            "pr": d.get("pr") is True and bool(c), "repo": rp(d.get("repo")), "at": d.get("at") if isinstance(d.get("at"), str) else ""}

def seal(d):
    """What the person approved: exactly this title, text and patch. Any change after the press voids it."""
    import hashlib
    return hashlib.sha256(json.dumps([d.get("title"), d.get("text"), d.get("commit"), d.get("patch"), d.get("pr")] + ([d["repo"]] if d.get("repo") else [])).encode()).hexdigest()

def approvals():
    """{draft id: seal}, kept in reports/, which the sandbox doesn't mount: a press in Settings is the only way in."""
    try: v = json.load(open(os.path.join(rdir(), "approved.json")))
    except (OSError, ValueError): v = {}
    return v if isinstance(v, dict) else {}

def set_approval(did, sl):
    a = approvals()
    if sl: a[did] = sl
    else: a.pop(did, None)
    with open(os.path.join(rdir(), "approved.json"), "w") as f: f.write(json.dumps(a, indent=1))

def approved(did, d): return d is not None and approvals().get(did) == seal(d)

def drafts():
    """The drafts waiting, for Settings: exactly what would go out."""
    out = []
    for p in outbox():
        d, did = load_draft(p), os.path.basename(p)[:-5]
        if d is None: continue
        out.append({"id": did, "title": d["title"], "text": d["text"], "patch": d["patch"],
                    "how": "pull request" if d["pr"] else "issue", "repo": d["repo"] or issues_repo(), "at": d["at"], "approved": approved(did, d)})
    return out

def decide(did, yes):
    """The person's press in Settings: Send (approve this exact draft) or Discard."""
    if not DRAFT_ID.match(did or ""): return "no such draft"
    p = os.path.join(ddir(), did + ".json")
    d = load_draft(p)
    if d is None: return "no such draft"
    if not yes:
        os.replace(p, os.path.join(obox("discarded"), did + ".json")); set_approval(did, None); return "discarded"
    set_approval(did, seal(d))
    return "approved; it goes on the reporter's next run" if gh_ready() else "approved; it goes once gh is logged in"

def open_pr(repo, d):
    """(url, None) or (None, why not). The commit is replayed on the installed release in a scratch worktree,
    committed under the person's GitHub noreply address, pushed to their fork and offered to the repo."""
    import tempfile
    sha, core = d["commit"], core_dir()
    try: tag = json.load(open(os.path.join(home(), ".hive", "update.json"))).get("tag")
    except (OSError, ValueError, AttributeError): tag = None
    if not tag: return None, "no release is installed to base it on"
    diff = gh_run("git", "-C", core, "show", "--format=", sha).stdout
    if not diff.strip(): return None, "the commit is gone from this Hive"
    if diff != d.get("patch"): return None, "the commit no longer matches what was approved"
    who = gh_run("api", "user", "--jq", '.login + " " + (.id|tostring)').stdout.split()
    if len(who) != 2: return None, "couldn't read the GitHub login"
    login, uid = who
    gh_run("repo", "fork", repo, "--clone=false", timeout=120)
    fork = "%s/%s" % (login, repo.split("/")[-1])
    base = gh_run("repo", "view", repo, "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name").stdout.strip()
    if not base or gh_run("repo", "view", fork, "--json", "name").returncode: return None, "couldn't make or find the fork " + fork
    wt = tempfile.mkdtemp(prefix="hive-fix-"); os.rmdir(wt)
    br = "hive-fix-" + sha[:10]
    g = ["git", "-C", wt, "-c", "core.hooksPath=/dev/null"]
    try:
        if gh_run("git", "-C", core, "worktree", "add", "-q", "--detach", wt, tag + "^{commit}").returncode: return None, "couldn't check out " + tag
        if gh_run(*g, "cherry-pick", "--no-commit", sha).returncode: return None, "it doesn't apply to " + tag
        if gh_run(*g, "-c", "user.name=" + login, "-c", "user.email=%s+%s@users.noreply.github.com" % (uid, login),
                  "commit", "-q", "--no-verify", "-m", d["title"], "-m", d["text"]).returncode: return None, "couldn't commit it"
        if gh_run(*g, "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential",
                  "push", "-q", "-f", "https://github.com/%s.git" % fork, "HEAD:refs/heads/" + br, timeout=180).returncode: return None, "couldn't push to " + fork
        body = d["text"] + "\n\n_Sent by a Hive's agent through its user's own GitHub login, with their opt-in. Based on %s._\n" % tag
        r = gh_run("pr", "create", "-R", repo, "--base", base, "--head", "%s:%s" % (login, br), "--title", d["title"], "--body", body)
        if r.returncode and "already exists" not in r.stderr: return None, "GitHub refused the pull request"
        return (r.stdout.strip() or r.stderr.strip()).split()[-1], None
    finally:
        gh_run("git", "-C", core, "worktree", "remove", "--force", wt); gh_run("git", "-C", core, "worktree", "prune")

def send_draft(repo, p, st):
    """Only a draft the person approved as it stands. One never approved is dropped after DRAFT_DAYS; none sends by itself."""
    did, d = os.path.basename(p)[:-5], load_draft(p)
    if d is None: os.replace(p, os.path.join(obox("failed"), os.path.basename(p))); return None
    if not approved(did, d):
        if time.time() - os.path.getmtime(p) > DRAFT_DAYS * 86400: os.replace(p, os.path.join(obox("expired"), os.path.basename(p)))
        return None
    repo = d.get("repo") or repo
    url_, why = open_pr(repo, d) if d["pr"] else (None, "the patch had text the scrubber hides, so only the scrubbed copy goes")
    if not url_:
        body = d["text"]
        if d["patch"]:
            body += "\n\nA fix made in a Hive's own core%s:\n\n```diff\n%s\n```\n" % (" (no pull request: %s)" % why if d["commit"] else "", d["patch"])
        if not d.get("repo"): body += "\n_Sent by a Hive's agent through its user's own GitHub login, with their opt-in._\n"
        r = gh_run("issue", "create", "-R", repo, "-t", d["title"], "-b", body)
        url_ = r.stdout.strip().split()[-1] if r.returncode == 0 and r.stdout.strip() else None
    st["gh_n"] = st.get("gh_n", 0) + 1     # an approved draft that was tried counts against the day
    tries = st.setdefault("gh_tries", {})
    if url_:
        d["sent"] = url_; dest = obox("sent"); tries.pop(did, None)
    else:
        tries[did] = tries.get(did, 0) + 1; dest = obox("failed") if tries[did] >= 5 else None
    if dest:
        with open(os.path.join(dest, os.path.basename(p)), "w") as f: f.write(json.dumps(d, indent=1))
        os.remove(p); set_approval(did, None); tries.pop(did, None)
    return url_

def main(a):
    cmd = a[0] if a else "status"
    if cmd == "question": print(question())
    elif cmd == "send": print(send("--dry-run" in a))
    elif cmd == "bundle": print(bundle("--yes" in a))
    elif cmd == "pack": print(pack("--yes" in a))
    elif cmd == "feedback": print(feedback(sys.stdin.read()))
    elif cmd == "contribute":
        opt = lambda k: a[a.index(k) + 1] if k in a and a.index(k) + 1 < len(a) else None
        t = next((x for i, x in enumerate(a[1:], 1) if not x.startswith("--") and a[i - 1] not in ("--commit", "--diff")), "")
        df = opt("--diff")
        try: df = open(df).read() if df else None
        except OSError as e: print("not drafted: can't read %s: %s" % (df, e)); return
        print(contribute(t, sys.stdin.read() if not sys.stdin.isatty() else "", opt("--commit"), df))
    elif cmd == "upstream":
        opt = lambda k: a[a.index(k) + 1] if k in a and a.index(k) + 1 < len(a) else None
        t = next((x for i, x in enumerate(a[2:], 2) if not x.startswith("--") and a[i - 1] != "--repo"), "")
        if a[1:2] != ["draft"]: print("usage: hive-report upstream draft --repo OWNER/NAME TITLE <what is missing and what you saw on stdin>"); return
        print(upstream_draft(opt("--repo"), t, sys.stdin.read() if not sys.stdin.isatty() else ""))
    elif cmd == "drafts": print(json.dumps(drafts(), indent=1))
    elif cmd == "github": print(json.dumps({"on": want_github(), "gh": bool(__import__("shutil").which("gh")), "ready": gh_ready(), "repo": issues_repo() or "", "drafts": drafts()}))   # Settings' view
    elif cmd in ("approve", "discard"): print(decide(a[1] if len(a) > 1 else "", cmd == "approve"))   # Settings' Send and Discard buttons; agents never run these
    elif cmd == "issue": print(issue(a[1] if len(a) > 1 else "", sys.stdin.read() if not sys.stdin.isatty() else "", "--submit" in a))
    elif cmd == "show":
        fs = sent_files(); n = int(a[1]) if len(a) > 1 else 1
        if not fs: print("nothing has been sent")
        for f in fs[-n:]: print(f + "\n" + open(f).read())
    elif cmd == "status":
        wf, wu = load_share()
        print("failure reports and daily check-in: %s, usage reports: %s" % ("on" if wf else "off", "on" if wu else "off"))
        print("endpoint:", url() or "none set")
        if not url(): print(NOT_SENT)
        print("sent so far:", len(sent_files()), "| waiting:", len(pending()))
        print("GitHub issues and fixes from your own login: %s%s" % ("on" if want_github() else "off", (", " + ("gh logged in" if gh_ready() else "but gh isn't logged in, so nothing goes")) if want_github() else ""))
        if want_github(): print("waiting for GitHub: %d failure(s), %d fix(es)" % (len(state().get("gh_queue", [])), len(outbox())))
        if not (wf or wu or want_github()): print("Nothing is read or sent while all are off.")
    else: print(__doc__); return 2
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
