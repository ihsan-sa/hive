#!/usr/bin/env python3
"""hive-report: failure reports, a daily check-in and opt-in usage reports, scrubbed on this machine first.

Reads `share` ({"failures": bool, "usage": bool}) from settings.json. failures, which also carries the check-in, is on by
default and can be turned off. Both false (or no settings.json) means nothing is read, queued or sent. Every report that
leaves is first written byte-for-byte to reports/sent/, and `hive-report show`
prints it. Commands: status | send [--dry-run] | show [N] | question | bundle [--yes] | issue TITLE [--submit] <body on stdin> | feedback <JSON on stdin>

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
    "The check-in is one small message a day that says this Hive is running. "
    "A usage report counts agent runs, restarts, limit hits and fallbacks, with the time span it covers. "
    "Every report has the Hive version, the OS and a random install id. "
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

def base(st, typ):
    return {"install": st["install"], "version": installed_version(), "type": typ, "os": platform.system().lower() + "/" + platform.machine().lower()}

def url():
    try: u = json.load(open(settings_path())).get("share_url")
    except (OSError, ValueError): u = None
    d = "" if DEFAULT_URL.startswith("<") else DEFAULT_URL
    return u or os.environ.get("HIVE_SHARE_URL") or d

# ---- build reports from the hive's own log ----
def logpath():
    return os.path.join(os.path.dirname(settings_path()), "log.jsonl")

# Log events that mean something went wrong. Only the event name is used, never its fields.
EVENT_FAILS = {"lost_subagents": "lost-subagents", "logins_paused": "logins-paused", "provider_held": "provider-held", "killed": "killed"}
EVENT_MSG = {"lost_subagents": "background helpers were lost when an agent ended", "logins_paused": "all logins were paused",
             "provider_held": "a model provider was held back", "killed": "an agent was killed"}

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

def build(st, want_fail, want_use):
    """Reports for log events past the saved offset. Only counts and kinds are read;
    thread names, posts, paths of work and agent output never go in."""
    try: f = open(logpath(), "rb")
    except OSError: return [], st["offset"]
    f.seek(0, 2); size = f.tell()
    off = st["offset"] if st["offset"] <= size else 0
    f.seek(off); raw = f.read(); raw = raw[:raw.rfind(b"\n") + 1]
    fails, use, first, last = [], {"runs": 0, "restarts": 0, "failed_exits": 0, "limit_hits": 0, "fallbacks": 0}, None, None
    for line in raw.splitlines():
        try: e = json.loads(line)
        except ValueError: continue
        at, ev = e.get("at", ""), e.get("event")
        if at < st["since"]: continue
        first, last = first or at, at
        if ev == "start": use["runs"] += 1
        elif ev == "wake_resumed": use["restarts"] += 1
        elif ev == "limited": use["limit_hits"] += 1
        elif ev == "fallback": use["fallbacks"] += 1
        elif ev == "end" and e.get("exit") not in (0, None):
            use["failed_exits"] += 1
            fails.append(("agent-exit", at, "an agent ended with exit code %s" % e.get("exit")))
        if ev == "limited": fails.append(("limited", at, "a login hit its usage limit"))
        elif ev in EVENT_FAILS: fails.append((EVENT_FAILS[ev], at, EVENT_MSG[ev]))
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
    wf, wu = load_share()
    if not (wf or wu): return "sharing is off; nothing read or sent"
    st = installed_state()
    new, off = build(st, wf, wu)
    if wf: new += unit_failures(st)
    if wu and not st.get("hello"):                    # the first send after opting in says so, with counts of nothing, so a quiet Hive can be told from a broken route
        r = base(st, "usage"); r.update(at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), message="sharing is on", usage={"runs": 0}); new.insert(0, r); st["hello"] = True
    q = [r for r in pending() if r["type"] in ("failure", "heartbeat") and wf or (r["type"] == "usage" and wu)] + new
    if wf and time.time() - st.get("beat", 0) >= BEAT_EVERY:
        r = base(st, "heartbeat"); r["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        q = [x for x in q if x["type"] != "heartbeat"] + [r]; st["beat"] = time.time()   # a check-in that waited offline is replaced, and goes last so the cap can't drop it
    if len(q) > MAX_PER_RUN: q = q[-MAX_PER_RUN:]
    if dry: return "would send %d report(s):\n" % len(q) + "\n".join(json.dumps(r) for r in q)
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
            return "sent %d, refused %d; %s; %d wait for the next run" % (sent, refused, ("%s answered %d" % (url(), code)) if code else ("no endpoint set (share_url)" if not url() else "couldn't reach " + url()), len(q) - i)
    keep([])
    return "sent %d, refused %d" % (sent, refused)

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

def main(a):
    cmd = a[0] if a else "status"
    if cmd == "question": print(question())
    elif cmd == "send": print(send("--dry-run" in a))
    elif cmd == "bundle": print(bundle("--yes" in a))
    elif cmd == "feedback": print(feedback(sys.stdin.read()))
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
        if not (wf or wu): print("Nothing is read or sent while both are off.")
    else: print(__doc__); return 2
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
