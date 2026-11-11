#!/usr/bin/env python3
"""hive update: bring an installed Hive up to the newest release, and go back if that fails.

A release is a tag v<major>.<minor>.<patch> on origin; only this major line counts. hive-update.timer ticks every
5 minutes, and `"update_every_s"` in config.json makes it look for a release less often. `"auto_update": false` stops
it installing: it still looks, so the app can say a release is there, and `hive update` by hand or Update now in the app
installs it. `"update_pace": "daily"` makes the timer's run calmer: at most one release a day, and only one out for CALM_AGE (only if picked in Settings; no key means every release, the default).
`hive update` by hand and Update now always take the newest. Those settings and the state in update.json (tag, newest, checked, at, failed, error) are read by Settings in the app.
What it keeps: the agents' own commits in ~/core (rebased onto the release) and uncommitted edits such as saved settings.
When either conflicts with the release, the release wins so the Hive still updates: the old state goes to a git branch kept/<tag>-<time>
in ~/core, and an agent of this Hive is started to see whether any of it still matters (the person, post 110415432d10).
A failed install or services that don't come back leave the Hive as it was, skip that release and tell the person.
After a good one, selfcheck() looks over the setup at no model cost and starts an agent only when it finds something off. Restarting the starter doesn't stop running agents.
Modules added from git update here too (modules_update): to the newest tag v<N>.<M>.<P> on their own origin that the module key (principal hive-module in release_signers, which can't sign a release of the Hive) signed for that module, under the same settings. No module code runs to do it.
A module with local edits or commits, or in use, is left alone; one whose services don't come back goes back to what it ran and skips that release.
"""
import argparse, calendar, fcntl, json, os, re, shutil, subprocess, sys, tempfile, time, urllib.error, urllib.request
from pathlib import Path, PurePath

HOME = Path.home(); CORE = Path(__file__).resolve().parent; STATE = HOME / ".hive"; STATE_F = STATE / "update.json"; NOW = STATE / "update-now"  # NOW: the app's Update now, for the --auto run it starts
MAJOR = 2
TAG = re.compile(rf"^v{MAJOR}\.(\d+)\.(\d+)$")
SETTLE = float(os.environ.get("HIVE_UPDATE_SETTLE", "8"))  # seconds services get to fall over before they count as up
CALM_AGE = 12 * 3600; CALM_GAP = 23 * 3600  # update_pace "daily": how long a release is out first, and the wait after an install (under a day, so the hour it happens doesn't drift)
SYSTEMD = (HOME / ".config/systemd/user/hive-starter.service").exists() and bool(shutil.which("systemctl"))

SIGNERS = CORE / "release_signers"  # who may sign a release tag (ssh allowed-signers format); a Hive installed before it existed takes the first one on trust
SIG = "-----BEGIN SSH SIGNATURE-----"

class Failed(Exception): pass
class Stuck(Exception): """Something on this machine, not the release, stopped the update: try again next run, and don't skip the release."""
class Later(Exception): """Nothing is wrong: offline for now, say. The next run tries again."""

def later(msg):
    """Not a failure: the unit's SuccessExitStatus keeps 75 from showing as failed, and from reaching the failure reports. By hand it's still non-zero."""
    print(msg, file=sys.stderr); sys.exit(75)

def git(*a, check=True, timeout=300):
    r = subprocess.run(["git", "-C", str(CORE), *a], capture_output=True, text=True, timeout=timeout)
    if check and r.returncode: raise Failed(f"git {a[0]}: {(r.stderr or r.stdout).strip()[-400:]}")
    return r.stdout.strip() if check else r

def key(tag): return tuple(int(x) for x in TAG.match(tag).groups())
def load():
    try: return json.loads(STATE_F.read_text())
    except (OSError, ValueError): return {}
def save(st):
    STATE.mkdir(exist_ok=True); t = STATE_F.with_suffix(f".{os.getpid()}"); t.write_text(json.dumps(st, indent=1) + "\n"); t.replace(STATE_F)
def config():
    try: return json.loads((CORE / "config.json").read_text())
    except (OSError, ValueError): return {}

def claude_update(st):
    """A Hive with a sandbox: agents can't change their own Claude Code, so it's updated from here, once a day.
    With a config folder nothing inside can write: an agent's login folder holds settings that would run commands out here. Failing is fine: it tries tomorrow."""
    if not (HOME / "settings.json").exists() or time.time() - st.get("claude_update", 0) < 86400 or not (claude := shutil.which("claude")): return
    st["claude_update"] = time.time(); save(st)
    d = STATE / "claude-update"; d.mkdir(mode=0o700, exist_ok=True)
    env = {"HOME": str(HOME), "PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": os.environ.get("LANG", "C.UTF-8"), "CLAUDE_CONFIG_DIR": str(d)}
    try: subprocess.run([claude, "update"], cwd=d, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=300)
    except (OSError, subprocess.SubprocessError): pass

def tags():
    out = git("ls-remote", "--tags", "--refs", "origin", f"v{MAJOR}.*", timeout=60)
    return sorted((t for t in (l.rsplit("/", 1)[-1] for l in out.splitlines()) if TAG.match(t)), key=key)

def verify(tag, signers, repo=CORE, principal="hive-release"):
    """None if the tag object is signed by a key in signers (the text of the installed release_signers), else why not.
    Checked by hand rather than with git verify-tag, so any git works; ssh-keygen -Y needs OpenSSH 8.1 (2019)."""
    r = subprocess.run(["git", "-C", str(repo), "cat-file", "tag", f"refs/tags/{tag}"], capture_output=True, text=True, timeout=60)
    if r.returncode: return f"{tag} isn't a signed tag"
    body = r.stdout; i = body.find(SIG)
    if i < 0: return f"{tag} isn't signed"
    payload, sig = body[:i], body[i:]
    if f"\ntag {tag}\n" not in payload: return f"{tag}'s signature is for another tag"  # an old signed tag pushed under a new name
    keygen = shutil.which("ssh-keygen", path=os.pathsep.join(p for p in os.environ.get("PATH", os.defpath).split(os.pathsep) if Path(p) != HOME / ".local/bin"))  # never one a module linked: this is what checks the modules
    if not keygen: raise Stuck("can't check the release's signature: ssh-keygen (package openssh-client) isn't installed")
    with tempfile.TemporaryDirectory() as d:
        sf, kf = Path(d) / "sig", Path(d) / "signers"; sf.write_text(sig); kf.write_text(signers)
        v = subprocess.run([keygen, "-Y", "verify", "-f", str(kf), "-I", principal, "-n", "git", "-s", str(sf)],
                           input=payload, capture_output=True, text=True, timeout=60)
    return None if v.returncode == 0 else f"{tag} isn't signed by the {'Hive' if principal == 'hive-release' else 'module'} release key ({(v.stderr or v.stdout).strip()[-200:]})"

def say(text, person=False, to=None):
    """A note on the board in thread updates. Only a failure goes to the person; to="new" starts an agent, only for a conflict set aside."""
    cmd = [str(CORE / "hive"), "post", "--thread", "updates"] + (["--to", "person"] if person else ["--to", to] if to else [])
    try: subprocess.run(cmd, input=text, text=True, capture_output=True, timeout=60, env={**os.environ, "HIVE_NAME": "updater"})
    except (OSError, subprocess.TimeoutExpired): pass

def run_dir(): return {**os.environ, "XDG_RUNTIME_DIR": os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")}  # a shell or an agent's step may not have it, and without it systemctl --user answers "not running" about a service that is

def systemctl(*a):
    try: return subprocess.run(["systemctl", "--user", *a], capture_output=True, text=True, timeout=60, env=run_dir()).returncode
    except (OSError, subprocess.TimeoutExpired): return 1

def install():
    """Run the checked-out release's own install.sh, then refresh bundled modules the way it refreshes the app."""
    r = subprocess.run([str(CORE / "install.sh")] + ([] if SYSTEMD else ["--no-service"]), capture_output=True, text=True, timeout=900, env=run_dir())
    if r.returncode == 75: raise Stuck("install.sh: " + (r.stderr or r.stdout).strip()[-400:])  # a tool missing on this machine, not a bad release
    if r.returncode: raise Failed("install.sh: " + (r.stderr or r.stdout).strip()[-400:])
    for src in sorted((CORE / "modules").glob("*/")) if (CORE / "modules").is_dir() else []:
        d = HOME / "modules" / src.name
        if src.name != "dash" and d.is_dir() and not (d / ".git").exists(): shutil.copytree(src, d, dirs_exist_ok=True)

def clear_failed():
    """A bad release crash-looping hits systemd's start limit, and then even a good restart is refused until this."""
    if SYSTEMD: systemctl("reset-failed", "hive-starter", "hive-dash")

def restart():
    if not SYSTEMD: return
    systemctl("daemon-reload")
    clear_failed()
    systemctl("restart", "hive-starter")
    for f in sorted((HOME / "modules").glob("*/*.service")):
        if f.name not in ("hive-dash.service",) and systemctl("is-active", "--quiet", f.name) == 0: systemctl("restart", f.name)

def healthy():
    """Raises unless the services stayed up, the app answers and the command runs."""
    time.sleep(SETTLE)
    if SYSTEMD:
        for u in ("hive-starter", "hive-dash"):
            if systemctl("is-active", "--quiet", u): raise Failed(f"{u} isn't running after the update")
    r = subprocess.run([str(CORE / "hive"), "web"], capture_output=True, text=True, timeout=60)
    if r.returncode: raise Failed("hive web: " + r.stderr.strip()[-300:])
    if SYSTEMD:
        port = re.search(r":(\d+)/", r.stdout).group(1)
        try: urllib.request.urlopen(f"http://127.0.0.1:{port}/login", timeout=10).read(1)
        except urllib.error.HTTPError: pass  # it answered
        except OSError as e: raise Failed(f"the web app doesn't answer: {e}")
    r = subprocess.run([str(CORE / "hive"), "who"], capture_output=True, text=True, timeout=60)
    if r.returncode: raise Failed("hive who: " + r.stderr.strip()[-300:])

TIMERS = ("hive-update.timer", "hive-progress.timer", "hive-asks.timer", "hive-outcomes.timer")
PROBES = (["focus", "json"], ["asks", "list"], ["waits"], ["stalled"])

def selfcheck():
    """After an update, a check that costs no model use (the person, 2026-11-08, post 1108182587f3: an agent checking every update
    would be too expensive): the timers are on, config.json and settings.json read and have what the Hive promises (work kept moving,
    stray work sorted into foci, an autonomy level), and the commands the keeper and the sorting pass rely on run. [] when all is well;
    otherwise the problems, and the caller starts an agent on them."""
    bad, tidy = [], (CORE / "tidy.py").exists()
    if SYSTEMD: bad += [f"{u} isn't active" for u in TIMERS if (CORE / u).exists() and systemctl("is-active", "--quiet", u)]
    try:
        c = json.loads((CORE / "config.json").read_text())
        if tidy and not c.get("tidy_every_s"): bad.append("tidy_every_s is off in ~/core/config.json, so stray conversations never get sorted into foci")
        if not isinstance(c.get("keeper_every_s", 0), int) or c.get("keeper_every_s", 0) < 0: bad.append(f"keeper_every_s in ~/core/config.json is {c.get('keeper_every_s')!r}")
    except (OSError, ValueError) as e: bad.append(f"~/core/config.json doesn't read: {e}")
    try:
        p = json.loads((HOME / "settings.json").read_text()).get("power", 2)
        if p not in (1, 2, 3): bad.append(f"power in ~/settings.json is {p!r}, not 1, 2 or 3")
    except FileNotFoundError: pass  # power defaults to 2 without it
    except (OSError, ValueError) as e: bad.append(f"~/settings.json doesn't read: {e}")
    for a in PROBES:
        try:
            r = subprocess.run([str(CORE / "hive"), *a], capture_output=True, text=True, timeout=120)
            if r.returncode: bad.append(f"`hive {' '.join(a)}` fails: " + (r.stderr or r.stdout).strip()[-200:])
        except (OSError, subprocess.TimeoutExpired) as e: bad.append(f"`hive {' '.join(a)}` doesn't finish: {e}")
    if tidy:
        try:
            r = subprocess.run([sys.executable, "-I", str(CORE / "tidy.py"), "due", "--json"], capture_output=True, text=True, timeout=120)
            if r.returncode not in (0, 1): bad.append("the sorting check (tidy.py due) fails: " + r.stderr.strip()[-200:])
        except (OSError, subprocess.TimeoutExpired) as e: bad.append(f"the sorting check (tidy.py due) doesn't finish: {e}")
    return bad

def restore(old, dirty):
    git("rebase", "--abort", check=False)
    git("reset", "--hard", old)
    if dirty: git("stash", "apply", dirty, check=False)

def my_settings(installed=None):
    """What this Hive changed in config.json against the release it runs: (changed, removed), or None if nothing or it can't be read.
    Saved settings rewrite the whole file, so a release touching any line would conflict as text. They go on top of the release's file instead.
    A change committed by hand counts too: the rebase keeps the release's file (keep_release), so this is how it survives."""
    try:
        at = installed if installed and git("rev-parse", "-q", "--verify", f"{installed}^{{commit}}", check=False).stdout.strip() else "HEAD"
        base, cur = json.loads(git("show", f"{at}:config.json")), json.loads((CORE / "config.json").read_text())
    except (Failed, OSError, ValueError): return None
    changed = {k: v for k, v in cur.items() if k not in base or base[k] != v}; removed = [k for k in base if k not in cur]
    return (changed, removed) if changed or removed else None

def put_settings(mine):
    c = json.loads((CORE / "config.json").read_text()); c.update(mine[0])
    for k in mine[1]: c.pop(k, None)
    t = CORE / f".config.json.{os.getpid()}"; t.write_text(json.dumps(c, indent=2) + "\n"); t.replace(CORE / "config.json")

def stashes(): return len(git("stash", "list").splitlines())

def keep_release():
    """Git options under which a rebase takes the release's config.json whenever both sides changed it, so a local commit touching it can't conflict."""
    f = STATE / "update.gitattributes"; f.write_text("/config.json merge=keeprelease\n")
    return "-c", f"core.attributesFile={f}", "-c", "merge.keeprelease.driver=true"

def overlap(tag, old, dirty):
    """Files both this Hive (its commits since the release it came from, and uncommitted edits) and the release changed: where a conflict can be."""
    base = git("merge-base", old, tag, check=False).stdout.strip()
    if not base: return []
    names = lambda *a: set(git("diff", "--name-only", *a, check=False).stdout.split())
    mine = names(base, old) | (names(old, dirty) if dirty else set())
    return sorted((mine & names(base, tag)) - {"config.json"})

def unfinished():
    """A rebase onto a release that a kill or power cut left halfway: undo it, which puts back what the Hive ran before. One an agent started onto anything else is theirs."""
    d = next((d for d in (CORE / ".git" / "rebase-merge", CORE / ".git" / "rebase-apply") if d.is_dir()), None)
    if not d: return
    try: onto = (d / "onto").read_text().strip()
    except OSError: return
    if any(git("rev-parse", "-q", "--verify", f"{t}^{{commit}}", check=False).stdout.strip() == onto for t in git("tag", "--list", "v*").split()):
        git("rebase", "--abort", check=False)

def apply(tag, st):
    if SYSTEMD and systemctl("show-environment"): raise Stuck("can't reach this user's systemd, so the services couldn't be restarted or checked")  # before anything changes: this isn't the release's fault
    unfinished()
    try:
        git("status", "--porcelain")  # refreshes the index: with a stale one and a clean tree, stash create exits 1
        old = git("rev-parse", "HEAD"); dirty = git("stash", "create"); before = stashes()
        signers = SIGNERS.read_text() if SIGNERS.exists() else None  # the installed release's, read before anything of the new one is checked out
    except (Failed, OSError) as e: raise Stuck(e)
    try: git("fetch", "-q", "--no-tags", "origin", f"+refs/tags/{tag}:refs/tags/{tag}", timeout=600)
    except (Failed, subprocess.SubprocessError) as e: raise Later(f"hive update: can't fetch {tag}: {e}")
    if signers is not None and (why := verify(tag, signers)): raise Failed(f"{why}, so it wasn't installed and this Hive stays on what it runs")
    if git("merge-base", "--is-ancestor", st["tag"], tag, check=False).returncode if st.get("tag") else not git("merge-base", "HEAD", tag, check=False).stdout.strip():
        raise Failed(f"{tag} doesn't follow from what's installed, so it was left alone")
    mine = my_settings(st.get("tag"))
    if mine: git("checkout", "HEAD", "--", "config.json")
    def rebase(*a):
        r = git(*keep_release(), *a, "rebase", "--autostash", *(["-i"] if a else []), tag, check=False)
        if not (r.returncode or stashes() > before): return True  # a failed autostash still exits 0, and leaves its stash behind
        if stashes() > before: git("stash", "drop", check=False)
        restore(old, dirty); return False
    dropped, kept, files = [], None, []
    if not rebase():
        # A "local fix:" commit is a Hive's own patch to its core (orientation, "Fixing the Hive itself"). When the release changed the same lines,
        # the release wins: it goes in without the fix, and the person hears which one went, since the release may or may not fix the same thing.
        fixes = [l.split("\t", 1) for l in git("log", "--format=%H%x09%s", f"{tag}..{old}").splitlines() if l.split("\t", 1)[-1].startswith("local fix:")]
        if not fixes or not rebase("-c", "sequence.editor=sed -i -E '/^(pick|p) (%s)[0-9a-f]* /d'" % "|".join(h[:7] for h, _ in fixes)):
            # The release wins, so a Hive never stays stuck on an old one: everything local goes to a branch, and an agent looks at it after.
            kept = f"kept/{tag}-{time.strftime('%Y%m%d%H%M%S', time.gmtime())}"
            git("branch", "-f", kept, dirty or old)
            files = overlap(tag, old, dirty)
            git("reset", "-q", "--hard", tag)
        else: dropped = [t for _, t in fixes]
    try:
        if mine: put_settings(mine)
        install(); restart(); healthy()
    except (Failed, OSError, ValueError, subprocess.SubprocessError, AttributeError) as e:
        restore(old, dirty)
        try: clear_failed(); install(); restart(); healthy()
        except (Failed, OSError, subprocess.SubprocessError, AttributeError) as e2: raise Failed(f"{tag} failed ({e}), and so did going back: {e2}. Look at ~/core and rerun ~/core/install.sh")
        raise Failed(f"{tag} failed and the Hive went back to what it was running: {e}")
    return dropped, kept, files

def settled(new, st):
    """update_pace "daily": the newest of the releases in new that may go in now, or None to wait.
    A release's age is the date in its signed tag, read only once the signature has passed. One that fails is returned as it is: apply checks it again and refuses it, as on every other pace."""
    try:
        if time.time() - calendar.timegm(time.strptime(st["at"], "%Y-%m-%dT%H:%M:%SZ")) < CALM_GAP: return None
    except (KeyError, TypeError, ValueError): pass  # no install recorded yet
    signers = SIGNERS.read_text() if SIGNERS.exists() else None
    for t in reversed(new):
        try:
            if git("rev-parse", "-q", "--verify", f"refs/tags/{t}^{{tag}}", check=False).returncode:  # a copy from an earlier look does: apply fetches it again before it goes in
                git("fetch", "-q", "--no-tags", "origin", f"+refs/tags/{t}:refs/tags/{t}", timeout=600)
            if signers is not None and verify(t, signers): return t
            date = git("for-each-ref", "--format=%(taggerdate:unix)", f"refs/tags/{t}").strip()
        except (Failed, OSError, subprocess.SubprocessError) as e: raise Later(f"hive update: can't read {t}: {e}")
        if date.isdigit() and time.time() - int(date) >= CALM_AGE: return t
    return None

MOD_TAG = re.compile(r"^v(\d{1,9})\.(\d{1,9})\.(\d{1,9})$")  # bounded: a tag of 5000 digits can't be sorted
MOD_STATE = STATE / "module-updates"  # one file per module: tag, commit, at, failed, said; going while it's being changed
MOD_EVERY = 900  # how often to look at the modules' origins, at least; a larger update_every_s wins
MOD_BUSY_SAY = 86400  # say so once if a module has been in use this long with a release waiting
MOD_WALK = 200  # how many tags to look down past unsigned ones for a real release

def mgit(d, *a, check=True, timeout=120):
    r = subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", str(d), *a], capture_output=True, text=True, timeout=timeout, env={**os.environ, "GIT_TERMINAL_PROMPT": "0"})  # no hook in the clone runs
    if check and r.returncode: raise Later(f"git {a[0]}: {(r.stderr or r.stdout).strip()[-300:]}")
    return r.stdout.strip() if check else r

def mod_key(t): return tuple(int(x) for x in MOD_TAG.match(t).groups())
def mod_load(name):
    try: return json.loads((MOD_STATE / f"{name}.json").read_text())
    except (OSError, ValueError): return {}
def mod_save(name, m):
    MOD_STATE.mkdir(parents=True, exist_ok=True); t = MOD_STATE / f".{name}.{os.getpid()}"; t.write_text(json.dumps(m, indent=1) + "\n"); t.replace(MOD_STATE / f"{name}.json")
def mod_note(name, m, text):
    """A note in thread updates, once per thing to say: this runs every few minutes."""
    if m.get("said") != text: m["said"] = text; mod_save(name, m); say(text)

def mod_tags(d):
    out = mgit(d, "ls-remote", "--tags", "--refs", "origin", "v*", timeout=60)
    return sorted((t for t in (l.split()[-1].removeprefix("refs/tags/") for l in out.splitlines()) if MOD_TAG.match(t)), key=mod_key)  # the whole name: refs/tags/x/v9.0.0 isn't v9.0.0

def mod_names(d):
    """The names a release of the module in folder d may carry: the folder's, and the catalog entry that was renamed from it."""
    names = {d.name}
    for f in (CORE / "modules.json", CORE / "dist" / "modules.json"):
        try: names |= {n for n, c in json.loads(f.read_text()).items() if c.get("formerly") == d.name}
        except (OSError, ValueError, AttributeError): pass
    return names

def mod_check(d, tag, signers):
    """Fetch tag into the module's clone. None if the module key signed it for this module, else why not."""
    shallow = mgit(d, "rev-parse", "--is-shallow-repository") == "true"  # --depth on a full clone would cut its history
    mgit(d, "fetch", "-q", "--no-tags", *(["--depth", "1"] if shallow else []), "origin", f"+refs/tags/{tag}:refs/tags/{tag}", timeout=600)
    return mod_signed(d, tag, signers)

def mod_signed(d, tag, signers):
    """None if tag, already here, is signed by the module key as a release of d. The name is inside the signature, so another module's tag can't stand in."""
    if why := verify(tag, signers, d, "hive-module"): return why
    body = mgit(d, "cat-file", "tag", f"refs/tags/{tag}"); payload = body[:body.find(SIG)]
    msg = payload.split("\n\n", 1)[1].strip() if "\n\n" in payload else ""
    m = re.fullmatch(r"Hive module (\S+) (\S+)", msg)
    return None if m and m[2] == tag and m[1] in mod_names(d) else f"{tag} is signed, but not as a release of {d.name}"

def mod_local(d, m):
    """Why this clone holds work a release would disturb, or None. Untracked files are fine: a checkout keeps them, and so does going back."""
    if mgit(d, "status", "--porcelain", "--untracked-files=no"): return "uncommitted changes"
    branch = mgit(d, "symbolic-ref", "-q", "--short", "HEAD", check=False).stdout.strip()
    if branch and branch != mgit(d, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD", check=False).stdout.strip().removeprefix("origin/"): return "another branch checked out"
    base = m.get("commit") or mgit(d, "rev-parse", "-q", "--verify", "refs/remotes/origin/HEAD^{commit}", check=False).stdout.strip()
    return None if mgit(d, "rev-parse", "HEAD") == base else "commits of its own"

def mod_collides(d, new):
    """A file here that git doesn't track (ignored ones too) and commit new would write over, or None."""
    def up(p): return {p, *(str(x) for x in PurePath(p).parents)} - {"."}
    here = set(filter(None, mgit(d, "ls-files", "-z", "-o").split("\0"))); tree = set(filter(None, mgit(d, "ls-tree", "-r", "-z", "--name-only", new).split("\0")))
    return next((p for p in sorted(tree) if up(p) & here), None) or next((p for p in sorted(here) if up(p) & tree), None)

def mod_owned(d):
    """The services this module installed: a link into its folder or, in a Hive inside someone's account, a file of that name. Never one of the Hive's own, whatever a release ships."""
    u, account = HOME / ".config/systemd/user", (HOME / ".hive-account").exists()
    return [f.name for f in sorted(d.glob("*.service")) if not (CORE / f.name).exists() and f.name != "hive-dash.service" and (((u / f.name).is_symlink() and (u / f.name).resolve().is_relative_to(d.resolve())) or (account and (u / f.name).is_file()))]

def mod_busy(d):
    """A pid using the folder (working in it, or named on its command line, or run through one of its commands), or None. Its own services don't count: they get restarted."""
    if not Path("/proc/self").exists(): return None
    root = str(d.resolve()); paths = [root]; own = re.compile("/(" + "|".join(re.escape(u) for u in mod_owned(d)) + ")(/|$)", re.M) if mod_owned(d) else None
    binf = HOME / ".local/bin"
    for l in binf.iterdir() if binf.is_dir() else []:
        try:
            if l.is_symlink() and l.resolve().is_relative_to(d.resolve()): paths.append(str(l))
        except OSError: pass
    hit = re.compile("(" + "|".join(re.escape(p) for p in paths) + r")(/|\0|$)")
    for p in Path("/proc").iterdir():
        if not p.name.isdigit() or int(p.name) == os.getpid(): continue
        try:
            if p.stat().st_uid != os.getuid() or (own and own.search((p / "cgroup").read_text())): continue
            cwd = os.readlink(p / "cwd"); cmd = (p / "cmdline").read_bytes().decode(errors="replace")
        except OSError: continue
        if hit.match(cwd) or hit.search(cmd): return int(p.name)
    return None

def mod_units(d):
    return [u for u in mod_owned(d) if SYSTEMD and systemctl("is-active", "--quiet", u) == 0]

def mod_link(d):
    """Link the commands and skills of what's checked out now. Core code only: nothing of the module runs."""
    json.loads((d / "module.json").read_text())
    r = subprocess.run([str(CORE / "hive"), "module", "add", next(iter(sorted(mod_names(d) - {d.name})), d.name)], capture_output=True, text=True, timeout=120, env={k: v for k, v in os.environ.items() if k != "HIVE_BROKER"})
    if r.returncode: raise Failed("linking it: " + (r.stderr or r.stdout).strip()[-300:])
    binf = HOME / ".local/bin"
    for l in binf.iterdir() if binf.is_dir() else []:  # a command this version doesn't have
        if l.is_symlink() and not l.exists() and l.resolve().is_relative_to(d.resolve()): l.unlink(missing_ok=True)

def mod_restart(units):
    if not units: return
    systemctl("daemon-reload")
    for u in units: systemctl("reset-failed", u); systemctl("restart", u)

def mod_back(d, old, units):
    """Back to the commit it ran before. Raises if even that fails."""
    mgit(d, "checkout", "-q", "--detach", old); mod_link(d); mod_restart(units)

def mod_follows(d, old, new):
    """Whether commit new has old in its history: True, False, or None when it's the other way round (cloned just after a commit that hasn't been released yet: the next release will follow it)."""
    if mgit(d, "rev-parse", "--is-shallow-repository") == "true":  # a shallow clone can't tell until it has the history since then
        when = int(mgit(d, "log", "-1", "--format=%ct", old)) - 86400
        mgit(d, "fetch", "-q", "--no-tags", f"--shallow-since={when}", "origin", new, timeout=600)
    if mgit(d, "merge-base", "--is-ancestor", old, new, check=False).returncode == 0: return True
    return None if mgit(d, "merge-base", "--is-ancestor", new, old, check=False).returncode == 0 else False

def mod_settled(d, new, m, signers):
    """update_pace "daily", as settled() for a release of the Hive: the newest that may go in now, or None to wait."""
    try:
        if time.time() - calendar.timegm(time.strptime(m["at"], "%Y-%m-%dT%H:%M:%SZ")) < CALM_GAP: return None
    except (KeyError, TypeError, ValueError): pass
    for t in reversed(new):
        if mod_check(d, t, signers): return t  # checked again below, and refused there
        date = mgit(d, "for-each-ref", "--format=%(taggerdate:unix)", f"refs/tags/{t}")
        if date.isdigit() and time.time() - int(date) >= CALM_AGE: return t
    return None

def mod_apply(d, tag, signers, m):
    """Put tag in, or leave the module as it was and raise Failed."""
    name = d.name
    if why := mod_check(d, tag, signers): raise Failed(f"{why}, so it wasn't installed")
    old, new = mgit(d, "rev-parse", "HEAD"), mgit(d, "rev-parse", f"refs/tags/{tag}^{{commit}}")
    follows = mod_follows(d, old, new)
    if follows is None: raise Later(f"{tag} is older than what's checked out; waiting for the next release")
    if not follows: raise Failed(f"{tag} doesn't follow from what's installed, so it was left alone")
    if why := mod_local(d, m): raise Later(f"{why} since the look began")  # the fetches above can take minutes
    if pid := mod_busy(d): raise Later(f"process {pid} started using it")
    if f := mod_collides(d, new):
        mod_note(name, m, f"Module {name} has a file of its own, {f}, where {tag} brings one, so it stays as it is. It updates again once ~/modules/{name}/{f} has moved.")
        raise Later(f"{f} is in the way of {tag}")
    units = mod_units(d)
    m["going"] = {"from": old, "to": new, "units": units}; mod_save(name, m)  # a power cut from here on is undone by the next run
    try:
        r = mgit(d, "checkout", "-q", "--detach", new, check=False)
        if r.returncode: raise Failed("checking it out: " + (r.stderr or r.stdout).strip()[-300:])
        mod_link(d); mod_restart(units)
        if units: time.sleep(SETTLE)
        for u in units:
            if systemctl("is-active", "--quiet", u): raise Failed(f"{u} isn't running after the update")
    except (Failed, OSError, ValueError, subprocess.SubprocessError, Later) as e:
        try: mod_back(d, old, units)
        except (Failed, OSError, ValueError, subprocess.SubprocessError, Later) as e2: raise Failed(f"{tag} failed ({e}), and so did going back: {e2}. Look at ~/modules/{name}")
        m.pop("going", None); mod_save(name, m)
        raise Failed(f"{tag} failed and it went back to what it was running: {e}")
    m.pop("going", None); m.pop("said", None); m.pop("busy", None)
    m.update(tag=tag, commit=new, at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
    m["failed"] = [t for t in m.get("failed", []) if mod_key(t) > mod_key(tag)]
    if not m["failed"]: m.pop("failed")
    mod_save(name, m); say(f"Module {name} updated to {tag}.")

def mod_resume(d, m):
    """A run was cut off partway. Undo it, but only if the folder is still as that run left it: anything done since stays."""
    name, g = d.name, m["going"]; head = mgit(d, "rev-parse", "HEAD")
    untouched = not mgit(d, "status", "--porcelain", "--untracked-files=no") and mgit(d, "symbolic-ref", "-q", "HEAD", check=False).returncode != 0
    problem = None
    if untouched and head in (g["from"], g.get("to")):
        try: mod_back(d, g["from"], g.get("units", []))
        except (Failed, OSError, ValueError, subprocess.SubprocessError, Later) as e: problem = f"going back failed: {e}"
    elif head != g["from"]: problem = "the folder has changed since, so it was left as it is"
    m.pop("going"); mod_save(name, m)
    if problem: say(f"Module {name} was being updated when the run stopped, and {problem}. Look at ~/modules/{name}.", person=True)

def mod_one(d, signers, hand, daily):
    name = d.name; m = mod_load(name)
    if m.get("going"): mod_resume(d, m)
    skip = [] if hand else m.get("failed", [])
    new = [t for t in mod_tags(d) if t not in skip and (not m.get("tag") or mod_key(t) > mod_key(m["tag"]))]
    if not new: return
    if why := mod_local(d, m): return mod_note(name, m, f"Module {name} has {why}, so it stays as it is and doesn't take {new[-1]}. It updates again once ~/modules/{name} has none.")
    if (pid := mod_busy(d)):
        m.setdefault("busy", time.time()); mod_save(name, m)
        if time.time() - m["busy"] > MOD_BUSY_SAY: mod_note(name, m, f"Module {name} has been in use for over a day (process {pid} now), so {new[-1]} is waiting for it to stop.")
        return print(f"module {name}: in use by process {pid}; {new[-1]} waits")
    m.pop("busy", None)
    refused = {}
    for t in reversed(new[-MOD_WALK:]):  # down past anything not signed, which takes nobody's time: only a real release that fails tells the person
        if not (why := mod_check(d, t, signers)): break
        refused[t] = why
    if refused:
        m["failed"] = sorted(set(m.get("failed", [])) | set(refused), key=mod_key); mod_save(name, m)
        say(f"Module {name}: ignored {', '.join(sorted(refused, key=mod_key)[-5:])}: {next(iter(refused.values()))}")
    new = [t for t in new[-MOD_WALK:] if t not in refused]
    if not new: return
    tag = new[-1]
    if daily and not (tag := mod_settled(d, new, m, signers) or ""): return
    try: mod_apply(d, tag, signers, m)
    except Failed as e:
        m["failed"] = sorted(set(m.get("failed", [])) | {tag}, key=mod_key); mod_save(name, m)
        say(f"Module {name} didn't update: {e}", person=True)

def modules_update(hand=False):
    """One look at every module added from git. A problem with one never touches the others or the Hive's own update."""
    try:
        signers = SIGNERS.read_text() if SIGNERS.exists() else ""
        if "hive-module " not in signers: return  # no key to check against, and a module is never taken on trust
        c = config(); every = c.get("update_every_s"); stamp = MOD_STATE / ".checked"
        if not hand and stamp.exists() and time.time() - stamp.stat().st_mtime < max(MOD_EVERY, every if type(every) is int else 0): return
        MOD_STATE.mkdir(parents=True, exist_ok=True); stamp.touch()
        daily = not hand and c.get("update_pace") == "daily"
        dirs = sorted((HOME / "modules").glob("*/")) if (HOME / "modules").is_dir() else []
    except (OSError, ValueError) as e: return print(f"modules: {e}", file=sys.stderr)
    for d in dirs:
        if d.is_symlink() or not (d / ".git").exists() or not (d / "module.json").exists(): continue
        try: mod_one(d, signers, hand, daily)
        except Exception as e: print(f"module {d.name}: {e}", file=sys.stderr)  # offline, say: the next run tries again

def module_pin(d):
    """A module just cloned goes onto its newest signed release, so a new install runs released code. With none, or on a Hive that doesn't update, it stays on what was cloned. Exit 3: couldn't tell, so hive module add drops the clone rather than run code nobody checked."""
    if not (HOME / ".hive-installed").exists() or not SIGNERS.exists() or "hive-module " not in SIGNERS.read_text(): return
    STATE.mkdir(exist_ok=True); lock = open(STATE / "update.lock", "w")
    for _ in range(600):  # an update may be running
        try: fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB); break
        except OSError: time.sleep(1)
    else: print(f"{d.name}: an update is taking a long time"); sys.exit(3)
    signers = SIGNERS.read_text()
    try:
        for t in reversed(mod_tags(d)[-MOD_WALK:]):
            if mod_check(d, t, signers): continue
            mgit(d, "checkout", "-q", "--detach", f"refs/tags/{t}^{{commit}}")
            mod_save(d.name, {"tag": t, "commit": mgit(d, "rev-parse", "HEAD"), "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())})
            return print(f"{d.name}: release {t}")
    except (Later, Stuck, OSError, subprocess.SubprocessError) as e: print(f"{d.name}: can't check its releases: {e}"); sys.exit(3)

def main():
    p = argparse.ArgumentParser(prog="hive update", description="Update this Hive to the newest release.")
    p.add_argument("--check", action="store_true", help="say what's installed and what's newest, and install nothing")
    p.add_argument("--verify", nargs=2, metavar=("REPO", "TAG"), help=argparse.SUPPRESS)  # release.sh publish checks its own tag with this before pushing it
    p.add_argument("--pin-module", metavar="DIR", help=argparse.SUPPRESS)  # hive module add, after a clone
    p.add_argument("--verify-module", nargs=2, metavar=("REPO", "TAG"), help=argparse.SUPPRESS)  # the module train checks its own tag with this before pushing it; the folder's name is the module's
    p.add_argument("--selfcheck", action="store_true", help="check this Hive's setup as an update does after it installs, and install nothing")
    p.add_argument("--auto", action="store_true", help="the timer's run: keeps to update_every_s, and installs nothing if auto_update is off")
    a = p.parse_args()
    if a.verify:
        why = verify(a.verify[1], SIGNERS.read_text(), Path(a.verify[0])); sys.exit(f"hive update: {why}" if why else 0)
    if a.pin_module: return module_pin(Path(a.pin_module))
    if a.verify_module:
        why = mod_signed(Path(a.verify_module[0]), a.verify_module[1], SIGNERS.read_text()); sys.exit(f"hive update: {why}" if why else 0)
    if a.selfcheck:
        bad = selfcheck(); print("\n".join(bad) or "setup ok"); sys.exit(1 if bad else 0)
    if not (HOME / ".hive-installed").exists():  # the timer can tick during an install, or after one that stopped partway and will be rerun
        msg = "hive update: this isn't an installed Hive"
        later(msg) if a.auto else sys.exit(msg)
    STATE.mkdir(exist_ok=True)
    lock = open(STATE / "update.lock", "w")
    try: fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError: later("hive update: another update is running")
    st = load()
    asked = a.auto and NOW.exists()
    NOW.unlink(missing_ok=True) if asked else None
    off = a.auto and not asked and config().get("auto_update", True) is False
    if not a.check and not off: claude_update(st)
    try: update_core(a, st, asked, off)
    finally:  # after the Hive's own update, which may bring a new signing key; whatever became of it
        if not a.check and not off: modules_update(hand=asked or not a.auto)

def update_core(a, st, asked, off):
    every = config().get("update_every_s")
    # A release already seen and not installed (stuck, or offline at the time) doesn't wait out the interval. At the daily pace one is nearly always waiting, on purpose, so it does.
    if a.auto and not asked and type(every) is int and every > 300 and time.time() - st.get("checked", 0) < every and (off or not st.get("newest") or config().get("update_pace") == "daily"):
        return print("auto_update is off" if off else f"looked for a release less than {every} seconds ago")
    try: all_tags = tags()
    except (Failed, subprocess.SubprocessError) as e: later(f"hive update: can't read releases: {e}")
    skip = st.get("failed", [])
    if not a.auto and not a.check: skip = []  # someone ran it by hand, after fixing what failed, say: try the skipped releases again
    new = [t for t in all_tags if t not in skip and (not st.get("tag") or key(t) > key(st["tag"]))]
    # A fresh install is already on this tag: nothing to do, and nothing to tell anyone.
    if new and not st.get("tag") and git("rev-parse", "HEAD", check=False).stdout == git("rev-parse", "-q", "--verify", f"{new[-1]}^{{commit}}", check=False).stdout:
        st["tag"] = new[-1]; new = []
    st["checked"] = time.time()
    if new: st["newest"] = new[-1]
    else: st.pop("newest", None)
    save(st)
    if off: return print(f"auto_update is off; {new[-1]} is available" if new else "auto_update is off; up to date")
    if a.check: return print(f"installed: {st.get('tag', 'before the first release')}; newest: {all_tags[-1] if all_tags else 'none'}" + (f"; skipped: {', '.join(skip)}" if skip else ""))
    if not new: return print(f"up to date ({st.get('tag', 'no release yet')})")
    tag = new[-1]
    try:
        if a.auto and not asked and config().get("update_pace") == "daily" and not (tag := settled(new, st) or ""):
            return print(f"{new[-1]} is available; update_pace is daily, so it waits")
        dropped, kept, files = apply(tag, st)
    except Stuck as e:
        st["error"] = str(e); save(st)
        if st.get("stuck") != str(e): st["stuck"] = str(e); save(st); say(f"Can't update, and {tag} will be tried again: {e}", person=True)
        sys.exit(f"hive update: {e}")
    except (Failed, subprocess.SubprocessError) as e:
        st["failed"] = sorted(set(skip) | {tag}, key=key); st["error"] = str(e); st.pop("newest", None); save(st)
        say(f"Update failed: {e}", person=True)
        sys.exit(f"hive update: {e}")
    except Later as e:  # the app can say why it hasn't happened yet
        st["error"] = str(e); save(st); later(str(e))
    st["failed"] = [t for t in st.get("failed", []) if key(t) > key(tag)]  # a skipped release now installed (by hand, say) is no longer skipped
    if not st["failed"]: st.pop("failed")
    st.pop("stuck", None); st.pop("error", None); st.pop("newest", None); st["tag"] = tag; st["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()); save(st)
    if dropped:
        say(f"Updated to {tag}. It changed the same lines as {'this fix' if len(dropped) == 1 else 'these fixes'} your Hive made to itself, so {'it was' if len(dropped) == 1 else 'they were'} dropped: "
            + "; ".join(d.removeprefix("local fix:").strip() for d in dropped) + ". If the problem comes back, ask an agent to look at it again.", person=True)
    elif not kept: say(f"Updated to {tag}.")
    if kept:
        where = f" to {', '.join(files[:8])}" + (" and more" if len(files) > 8 else "") if files else ""
        say(f"Updated to {tag}. This Hive's own changes in ~/core{where} clashed with it, so the release went in and they were set aside, not lost. An agent is checking whether any still matter.", person=True)
        say(f"`hive update` put in {tag}, but this Hive's own changes in ~/core{where} clashed with it. The release won so the update could happen. "
            f"The old state, its commits and any uncommitted edits, is kept in the git branch `{kept}` in ~/core: `git -C ~/core diff {tag} {kept}` shows it, and `git -C ~/core log {tag}..{kept}` its commits.\n\n"
            "Work out what was set aside and whether any of it still matters on top of the release (the release may already do the same thing). "
            "If something does: in bare mode, apply it again as a commit in ~/core whose subject starts `local fix:`; in the sandbox ~/core is read-only, so write the diff to a file and draft it with `hive-report contribute`. "
            "Then say here, in a line or two the person can follow, what was set aside and whether anything was lost. Don't delete the branch.", to="new")
    if bad := selfcheck():
        say(f"Updated to {tag}, but the check after it found something off. An agent is fixing it: " + "; ".join(bad)[:400], person=True)
        say(f"`hive update` put in {tag} and the services came back, but the setup check after it (`hive update --selfcheck`, no model use) found:\n"
            + "\n".join(f"- {b}" for b in bad) + "\n\nFix what's wrong so the Hive works as the release promises, run `hive update --selfcheck` until it says `setup ok`, "
            "and say here in a line or two the person can follow what was wrong and what you changed.", to="new")
    print(f"updated to {tag}")

if __name__ == "__main__": main()
