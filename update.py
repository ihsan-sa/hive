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
"""
import argparse, calendar, fcntl, json, os, re, shutil, subprocess, sys, tempfile, time, urllib.error, urllib.request
from pathlib import Path

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

def verify(tag, signers, repo=CORE):
    """None if the tag object is signed by a key in signers (the text of the installed release_signers), else why not.
    Checked by hand rather than with git verify-tag, so any git works; ssh-keygen -Y needs OpenSSH 8.1 (2019)."""
    r = subprocess.run(["git", "-C", str(repo), "cat-file", "tag", f"refs/tags/{tag}"], capture_output=True, text=True, timeout=60)
    if r.returncode: return f"{tag} isn't a signed tag"
    body = r.stdout; i = body.find(SIG)
    if i < 0: return f"{tag} isn't signed"
    payload, sig = body[:i], body[i:]
    if f"\ntag {tag}\n" not in payload: return f"{tag}'s signature is for another tag"  # an old signed tag pushed under a new name
    if not shutil.which("ssh-keygen"): raise Stuck("can't check the release's signature: ssh-keygen (package openssh-client) isn't installed")
    with tempfile.TemporaryDirectory() as d:
        sf, kf = Path(d) / "sig", Path(d) / "signers"; sf.write_text(sig); kf.write_text(signers)
        v = subprocess.run(["ssh-keygen", "-Y", "verify", "-f", str(kf), "-I", "hive-release", "-n", "git", "-s", str(sf)],
                           input=payload, capture_output=True, text=True, timeout=60)
    return None if v.returncode == 0 else f"{tag} isn't signed by the Hive's release key ({(v.stderr or v.stdout).strip()[-200:]})"

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

def main():
    p = argparse.ArgumentParser(prog="hive update", description="Update this Hive to the newest release.")
    p.add_argument("--check", action="store_true", help="say what's installed and what's newest, and install nothing")
    p.add_argument("--verify", nargs=2, metavar=("REPO", "TAG"), help=argparse.SUPPRESS)  # release.sh publish checks its own tag with this before pushing it
    p.add_argument("--selfcheck", action="store_true", help="check this Hive's setup as an update does after it installs, and install nothing")
    p.add_argument("--auto", action="store_true", help="the timer's run: keeps to update_every_s, and installs nothing if auto_update is off")
    a = p.parse_args()
    if a.verify:
        why = verify(a.verify[1], SIGNERS.read_text(), Path(a.verify[0])); sys.exit(f"hive update: {why}" if why else 0)
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
