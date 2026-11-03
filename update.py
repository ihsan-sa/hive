#!/usr/bin/env python3
"""hive update: bring an installed Hive up to the newest release, and go back if that fails.

A release is a tag v<major>.<minor>.<patch> on origin; only this major line counts. hive-update.timer ticks every
5 minutes, and `"update_every_s"` in config.json makes it look for a release less often. `"auto_update": false` stops
it installing: it still looks, so the app can say a release is there, and `hive update` by hand or Update now in the app
installs it. Both those settings and the state in update.json (tag, newest, checked, at, failed, error) are read by Settings in the app.
What it keeps: the agents' own commits in ~/core (rebased onto the release) and uncommitted edits such as saved settings.
A conflict with either, a failed install or services that don't come back leave the Hive as it was, skip that release
and tell the person. Restarting the starter doesn't stop running agents.
"""
import argparse, fcntl, json, os, re, shutil, subprocess, sys, time, urllib.error, urllib.request
from pathlib import Path

HOME = Path.home(); CORE = Path(__file__).resolve().parent; STATE = HOME / ".hive"; STATE_F = STATE / "update.json"; NOW = STATE / "update-now"  # NOW: the app's Update now, for the --auto run it starts
MAJOR = 2
TAG = re.compile(rf"^v{MAJOR}\.(\d+)\.(\d+)$")
SETTLE = float(os.environ.get("HIVE_UPDATE_SETTLE", "8"))  # seconds services get to fall over before they count as up
SYSTEMD = (HOME / ".config/systemd/user/hive-starter.service").exists() and bool(shutil.which("systemctl"))

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

def say(text, person=False):
    """A note on the board in thread updates. Only a failure goes to the person, and neither starts an agent."""
    cmd = [str(CORE / "hive"), "post", "--thread", "updates"] + (["--to", "person"] if person else [])
    try: subprocess.run(cmd, input=text, text=True, capture_output=True, timeout=60, env={**os.environ, "HIVE_NAME": "updater"})
    except (OSError, subprocess.TimeoutExpired): pass

def systemctl(*a):
    try: return subprocess.run(["systemctl", "--user", *a], capture_output=True, text=True, timeout=60).returncode
    except (OSError, subprocess.TimeoutExpired): return 1

def install():
    """Run the checked-out release's own install.sh, then refresh bundled modules the way it refreshes the app."""
    env = {**os.environ, "XDG_RUNTIME_DIR": os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")}
    r = subprocess.run([str(CORE / "install.sh")] + ([] if SYSTEMD else ["--no-service"]), capture_output=True, text=True, timeout=900, env=env)
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

def apply(tag, st):
    try:
        git("status", "--porcelain")  # refreshes the index: with a stale one and a clean tree, stash create exits 1
        old = git("rev-parse", "HEAD"); dirty = git("stash", "create"); before = stashes()
    except Failed as e: raise Stuck(e)
    try: git("fetch", "-q", "--no-tags", "origin", f"+refs/tags/{tag}:refs/tags/{tag}", timeout=600)
    except (Failed, subprocess.SubprocessError) as e: raise Later(f"hive update: can't fetch {tag}: {e}")
    if git("merge-base", "--is-ancestor", st["tag"], tag, check=False).returncode if st.get("tag") else not git("merge-base", "HEAD", tag, check=False).stdout.strip():
        raise Failed(f"{tag} doesn't follow from what's installed, so it was left alone")
    mine = my_settings(st.get("tag"))
    if mine: git("checkout", "HEAD", "--", "config.json")
    r = git(*keep_release(), "rebase", "--autostash", tag, check=False)
    if r.returncode or stashes() > before:  # a failed autostash still exits 0, and leaves its stash behind
        if stashes() > before: git("stash", "drop", check=False)
        restore(old, dirty)
        raise Failed(f"{tag} conflicts with local changes in ~/core; nothing changed")
    try:
        if mine: put_settings(mine)
        install(); restart(); healthy()
    except (Failed, OSError, ValueError, subprocess.SubprocessError, AttributeError) as e:
        restore(old, dirty)
        try: clear_failed(); install(); restart(); healthy()
        except (Failed, OSError, subprocess.SubprocessError, AttributeError) as e2: raise Failed(f"{tag} failed ({e}), and so did going back: {e2}. Look at ~/core and rerun ~/core/install.sh")
        raise Failed(f"{tag} failed and the Hive went back to what it was running: {e}")

def main():
    p = argparse.ArgumentParser(prog="hive update", description="Update this Hive to the newest release.")
    p.add_argument("--check", action="store_true", help="say what's installed and what's newest, and install nothing")
    p.add_argument("--auto", action="store_true", help="the timer's run: keeps to update_every_s, and installs nothing if auto_update is off")
    a = p.parse_args()
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
    # A release already seen and not installed (stuck, or offline at the time) doesn't wait out the interval.
    if a.auto and not asked and type(every) is int and every > 300 and time.time() - st.get("checked", 0) < every and (off or not st.get("newest")):
        return print("auto_update is off" if off else f"looked for a release less than {every} seconds ago")
    try: all_tags = tags()
    except (Failed, subprocess.SubprocessError) as e: later(f"hive update: can't read releases: {e}")
    skip = st.get("failed", [])
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
        apply(tag, st)
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
    say(f"Updated to {tag}.")
    print(f"updated to {tag}")

if __name__ == "__main__": main()
