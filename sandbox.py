"""The sandbox around a consumer install's agents (bubblewrap). See kb/consumer-sandbox.md.

Only with a ~/settings.json: without one on() is False, nothing here runs and the starter behaves as if this file weren't
there. "sandbox": "bare" in it is the person's explicit choice to go without. Anything else means on, and if the check fails
nothing starts: there is no quiet fallback.

Inside, ~ is its own folder (.sandbox/home), so whatever an agent writes to a dotfile is never read by anything outside.
Only what's listed below is mounted. There's no network but a proxy socket to broker.py, which also takes posts and steps.
"""
import errno, fcntl, json, os, re, shutil, socket, struct, subprocess, sys, tempfile, time
from pathlib import Path

HOME = Path.home()
CORE, STATE, RUN = HOME / "core", HOME / ".hive", HOME / "run"
SETTINGS, BOX_HOME, SOCKETS = HOME / "settings.json", HOME / ".sandbox" / "home", STATE / "broker"
INSIDE, PORT = "/run/sandbox", 31280
NAME = re.compile(r"[a-z0-9][a-z0-9_-]{0,63}")
SYSTEM = ("/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc", "/opt")
READ_ONLY = ("core", "modules", "mcp.json", "board.jsonl", "log.jsonl", "run", "sessions", "files", "settings.json", ".local/bin", ".local/share/claude",
             ".hive/starter.json", ".hive/usage.json", ".hive/usage_5h.json", ".hive/usage_resets.json", ".hive/paused.json",
             ".hive/login_limits.json", ".hive/provider_limits.json", ".hive/providers.json", ".hive/done", ".hive/killed")
WRITABLE = ("projects", "kb", "reports/drafts")  # reports/drafts: hive-report contribute; approvals stay in reports/, out of reach
LOGIN_READ_ONLY = ("settings.json", "CLAUDE.md", "skills", "agents", "commands", "plugins", "hooks")  # what a Claude run outside would load
ENV_KEEP = {"PATH", "LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "TERM", "TZ", "USER", "LOGNAME", "COLORTERM", "HIVE_NAME", "HIVE_DOOR", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME",
            "ENABLE_CLAUDEAI_MCP_SERVERS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_EFFORT", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"}

def settings():
    """None without a settings.json (not a consumer install). A file that doesn't parse counts as defaults: on, power 2."""
    try: s = json.loads(SETTINGS.read_text())
    except FileNotFoundError: return None
    except (OSError, ValueError): return {}
    return s if isinstance(s, dict) else {}

def on():
    s = settings()
    return s is not None and s.get("sandbox", "on") != "bare"

def power():
    """1, 2 or 3; 2 if unset. Something that isn't one of those is the most careful one."""
    p = (settings() or {}).get("power", 2)
    return p if p in (1, 2, 3) else 1

def account_home():
    """The person's real home: a Hive inside their account has HOME set to its folder."""
    try: return Path((HOME / ".hive-account").read_text().strip())
    except OSError: return HOME

def bwrap():
    return shutil.which("bwrap")

def _flags():
    """What this bwrap has. --disable-userns can be there and still fail (needs a nested user namespace), so try it."""
    bw, out = bwrap(), ""
    try: out = subprocess.run([bw, "--help"], capture_output=True, text=True, timeout=5).stdout
    except (OSError, subprocess.SubprocessError): pass
    fl = {"size": "--size" in out, "nouserns": False}
    if "--disable-userns" in out:
        try: fl["nouserns"] = subprocess.run([bw, "--unshare-user", "--disable-userns", "--ro-bind", "/", "/", "/bin/true"], capture_output=True, timeout=5).returncode == 0
        except (OSError, subprocess.SubprocessError): pass
    return fl

FLAGS = {}

def _python():
    return shutil.which("python3", path="/usr/bin:/bin") or sys.executable

def _filter():
    """A seccomp program, as a file bwrap reads, or None on a machine we don't ship for. A network namespace doesn't cover vsock, and under WSL or a VM that reaches the host
    outside any proxy, so socket(AF_VSOCK) is refused. io_uring can make sockets without that call, and other ABIs reach socket by another number: both refused, and programs fall back."""
    arch, sock, unshare, clone = {"x86_64": (0xC000003E, 41, 272, 56), "aarch64": (0xC00000B7, 198, 97, 220)}.get(os.uname().machine, (None,) * 4)
    if not arch: return None
    LD, JEQ, JGE, JSET, RET, ERR = 0x20, 0x15, 0x35, 0x45, 0x06, 0x00050000
    # A bwrap without a working --disable-userns (Ubuntu 22.04's 0.6.1) leaves nothing else stopping an agent making a nested user namespace, and with it more of the kernel to attack.
    # clone3 keeps its flags behind a pointer a filter can't read, so it's refused whole and libc falls back to clone. bwrap makes its own namespaces before this filter loads.
    old = not FLAGS.get("nouserns", True)
    code = [(LD, 0, 0, 4), (JEQ, "native", 0, arch), (RET, 0, 0, ERR | errno.ENOSYS), "native",
            (LD, 0, 0, 0), (JGE, "nosys", 0, 0x40000000), (JEQ, "nosys", 0, 425), (JEQ, "nosys", 0, 426), (JEQ, "nosys", 0, 427),
            *([(JEQ, "nosys", 0, 435), (JEQ, "flags", 0, clone), (JEQ, "flags", 0, unshare)] if old else []),
            (JEQ, 0, "allow", sock), (LD, 0, 0, 16), (JEQ, "family", "allow", socket.AF_VSOCK if hasattr(socket, "AF_VSOCK") else 40),
            *(["flags", (LD, 0, 0, 16), (JSET, "perm", "allow", 0x10000000), "perm", (RET, 0, 0, ERR | errno.EPERM)] if old else []),
            "allow", (RET, 0, 0, 0x7FFF0000), "nosys", (RET, 0, 0, ERR | errno.ENOSYS), "family", (RET, 0, 0, ERR | errno.EAFNOSUPPORT)]
    prog, at = [], {}
    for i in code:
        if isinstance(i, str): at[i] = len(prog)
        else: prog.append(i)
    prog = [(c, at[t] - n - 1 if t else 0, at[f] - n - 1 if f else 0, k) for n, (c, t, f, k) in enumerate(prog)]
    data = b"".join(struct.pack("=HBBI", *i) for i in prog)
    p = STATE / "sandbox.bpf"
    try:
        if p.read_bytes() == data: return p
    except OSError: pass
    STATE.mkdir(exist_ok=True); t = p.with_name(f"{p.name}.{os.getpid()}"); t.write_bytes(data); t.replace(p)
    return p

def prepare(name):
    """What a run needs outside before it starts: its socket folder and its own delivered file. Returns the folder."""
    if not NAME.fullmatch(name): raise ValueError(f"bad agent name {name!r}")
    d = SOCKETS / name
    d.mkdir(parents=True, exist_ok=True); d.chmod(0o700)
    (STATE / "delivered").mkdir(exist_ok=True); (STATE / "delivered" / name).touch()
    return d

def ensure_broker():
    """Start broker.py unless one holds its lock. It lives apart from the starter, so a starter restart leaves it serving running agents."""
    STATE.mkdir(exist_ok=True)
    with open(STATE / "broker.lock", "a") as f:
        try: fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB); fcntl.flock(f, fcntl.LOCK_UN)
        except OSError: return
    log = open(STATE / "broker.log", "a")
    subprocess.Popen([_python(), "-I", str(CORE / "broker.py")], cwd=HOME, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True,
                     env={"HOME": str(HOME), "PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": os.environ.get("LANG", "C.UTF-8")})
    log.close()

def _first_login():
    """The first configured login: the one later logins share skills with."""
    try: return Path(json.loads((CORE / "config.json").read_text())["logins"][0]).expanduser().resolve()
    except (OSError, ValueError, KeyError, IndexError, TypeError): return None

def _copy_boxed(binds, code, *args):
    """Run this copy in a sandbox with only `binds` mounted. A login folder is writable from inside, so anything in it may be a link an agent left to
    somewhere else, and a copy done outside would follow it, reading a key or overwriting a dotfile. In here those places don't exist."""
    a = [bwrap(), "--unshare-all", "--new-session", "--die-with-parent"]
    for d in SYSTEM:
        if os.path.islink(d): a += ["--symlink", os.readlink(d), d]
        elif os.path.isdir(d) and d != "/etc": a += ["--ro-bind", d, d]
    for d in binds: a += ["--bind", str(d), str(d)]
    return subprocess.run([*a, "--", _python(), "-I", "-c", code, *map(str, args)], capture_output=True, text=True, timeout=120, env={"PATH": "/usr/bin:/bin"}).returncode

_COPY = r"""
import glob, os, re, shutil, sys
def clear(root, path):
    # every folder below root as a real folder, and nothing at path
    here = root
    for part in os.path.relpath(path, root).split(os.sep)[:-1]:
        here = os.path.join(here, part)
        if os.path.islink(here) or (os.path.lexists(here) and not os.path.isdir(here)): os.unlink(here)
        os.makedirs(here, exist_ok=True)
    if os.path.islink(path) or (os.path.lexists(path) and not os.path.isdir(path)): os.unlink(path)
def one(src, dst, root):
    # a regular file or a folder of them; a link is left behind
    if os.path.islink(src): return
    if os.path.isfile(src):
        clear(root, dst); shutil.rmtree(dst, ignore_errors=True); shutil.copyfile(src, dst); shutil.copystat(src, dst)
    elif os.path.isdir(src):
        clear(root, dst); os.makedirs(dst, exist_ok=True)
        for n in os.listdir(src): one(os.path.join(src, n), os.path.join(dst, n), root)
"""
_MOVE = _COPY + r"""
sid, src, dst = sys.argv[1:]
if not re.fullmatch(r"[A-Za-z0-9_-]+", sid): sys.exit(2)
got = 0
for f in glob.glob(os.path.join(glob.escape(src), "projects", "*", sid + ".jsonl")):
    if os.path.islink(f) or not os.path.isfile(f) or os.path.islink(os.path.dirname(f)): continue
    to = os.path.join(dst, os.path.relpath(f, src)); one(f, to, dst); got += 1
    if os.path.isdir(f[:-6]): one(f[:-6], to[:-6], dst)
sys.exit(0 if got else 1)
"""
_TREE = _COPY + "one(sys.argv[1], sys.argv[2], sys.argv[3])\n"

def move_session(sid, src, dst):
    """The starter's move_session for a sandboxed install. False if there's nothing to copy."""
    src, dst = Path(src).resolve(), Path(dst).resolve()
    dst.mkdir(parents=True, exist_ok=True)
    try: return _copy_boxed([src, dst], _MOVE, sid, src, dst) == 0
    except (OSError, subprocess.SubprocessError): return False

def _memory():
    """The agents' memory, shared by every login. It lives outside them: a folder inside a login can be swapped for a link by any agent on that login,
    in the moment between looking at it and mounting it. The first time, it starts with what the first login has."""
    m = HOME / ".sandbox" / "memory"
    if not m.is_dir():
        m.mkdir(parents=True)
        old = (_first_login() or HOME / ".nowhere") / "projects" / str(HOME).replace("/", "-") / "memory"
        if old.is_dir() and not old.is_symlink():
            try: _copy_boxed([old.resolve(), m], _TREE, old.resolve(), m, m)
            except (OSError, subprocess.SubprocessError): pass
    return m

def _unlink_links(root, rel):
    """rel and its parents below root, if an agent left links there: nothing should resolve through them."""
    here = Path(root)
    for part in Path(rel).parts:
        here = here / part
        if here.is_symlink(): here.unlink()

def _login(d, keep=()):
    """A run's login folder, writable: Claude Code refreshes its sign-in there. Other logins aren't mounted.
    What a run outside would load is read-only, and made if it's missing, so it's a mount in every run and no agent can ever swap it for a link.
    A later login's skills are a link to the first login's, which is that kind of folder. So this mounts the link's checked target, never the link."""
    d, args = Path(d), []
    d.mkdir(parents=True, exist_ok=True)
    args += ["--bind", str(d), str(d)]
    mem = Path("projects") / str(HOME).replace("/", "-") / "memory"
    _unlink_links(d, mem)
    args += ["--bind", str(_memory()), str(d / mem)]
    first, skills = _first_login(), None
    if (d / "skills").is_symlink():
        t = (d / "skills").resolve()
        if first and first != d.resolve() and t == first / "skills" and t.is_dir(): skills = t
        else: (d / "skills").unlink()
    if skills: args += ["--ro-bind", str(skills), str(skills)]
    for rel in LOGIN_READ_ONLY + tuple(keep):
        p = d / rel
        if rel == "skills" and skills: continue
        if p.is_symlink(): p.unlink()
        if not p.exists():
            if "." not in rel: p.mkdir(parents=True, exist_ok=True)
            else: p.write_text("{}\n" if rel.endswith(".json") else "")
        args += ["--ro-bind", str(p), str(p)]
    return args

def ready(name, wait=3.0):
    """Wait for the broker to have this run's sockets (it looks every 0.3 s). True if it has."""
    f, t = SOCKETS / name / "ctl.sock", time.time() + wait
    while time.time() < t:
        try:
            with socket.socket(socket.AF_UNIX) as s: s.connect(str(f))
            return True
        except OSError: time.sleep(0.05)
    return False

def _tool(name, env):
    """Mounts for a program installed in the account but outside this home: ~/.local/bin/claude when the Hive is in a folder of the account, or nvm.
    Only the program's own folder, and for a node package the global packages and node. Never the prefix: ~/.local holds much else."""
    p = shutil.which(name, path=env.get("PATH"))
    if not p: return []
    p, r = Path(p), Path(p).resolve()
    if any(r.is_relative_to(q) for q in (HOME, *SYSTEM)): return []
    pkgs = next((q for q in r.parents if q.name == "node_modules" and q.parent.name == "lib"), None)
    a = ["--ro-bind", str(pkgs or r.parent), str(pkgs or r.parent)]
    if pkgs and (node := pkgs.parent.parent / "bin" / "node").exists(): a += ["--ro-bind", str(node.resolve()), str(node)]
    if p != r and not p.is_relative_to(HOME): a += ["--symlink", str(r), str(p)]  # a link, not a copy of the file: node finds a package's files from the real path
    return a

def wrap(argv, env, name, box_home=None, sockets=None):
    """(argv, env) for this command inside the sandbox. No --die-with-parent: agents outlive a starter restart."""
    bw = bwrap()
    if not FLAGS: FLAGS.update(_flags())
    box_home, sockets = Path(box_home or BOX_HOME), Path(sockets or prepare(name))
    box_home.mkdir(parents=True, exist_ok=True)
    for w in WRITABLE: (HOME / w).mkdir(parents=True, exist_ok=True)
    for r in READ_ONLY + WRITABLE + (".cache/hive-tmp",): _unlink_links(box_home, r)  # a link left in the shared home would send a mount somewhere else
    size = ["--size", str(2 << 30)] if FLAGS["size"] else []
    bpf = _filter()
    a = [bw, "--unshare-user", "--unshare-all", "--new-session", *(["--disable-userns"] if FLAGS["nouserns"] else []), *(["--seccomp", "9"] if bpf else [])]
    for d in SYSTEM:
        if os.path.islink(d): a += ["--symlink", os.readlink(d), d]
        elif os.path.isdir(d): a += ["--ro-bind", d, d]
    a += ["--dev", "/dev", "--proc", "/proc", *size, "--tmpfs", "/dev/shm", *size, "--tmpfs", "/tmp", "--tmpfs", "/var", "--dir", "/var/tmp"]
    a += ["--bind", str(box_home), str(HOME), "--dir", str(HOME / ".cache" / "hive-tmp")]
    for r in READ_ONLY: a += ["--ro-bind-try", str(HOME / r), str(HOME / r)]
    for w in WRITABLE: a += ["--bind", str(HOME / w), str(HOME / w)]
    for t in dict.fromkeys(("claude", argv[0])): a += _tool(t, env)
    if (STATE / "delivered" / name).exists(): a += ["--bind", str(STATE / "delivered" / name), str(STATE / "delivered" / name)]
    for k in ("CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME"):
        if env.get(k): a += _login(env[k], ("config.toml", "hooks.json") if k == "CODEX_HOME" else ())
    a += ["--ro-bind", str(sockets), INSIDE, "--remount-ro", "/", "--chdir", str(HOME), "--"]
    a += [_python(), "-I", str(CORE / "sandbox_inner.py"), f"{INSIDE}/egress.sock", str(PORT), "--", *argv]
    if bpf: a = ["/bin/sh", "-c", 'exec "$@" 9<"$0"', str(bpf), *a]  # sh only opens the file and becomes bwrap, whoever starts this and whatever it closes
    proxy = f"http://127.0.0.1:{PORT}"
    e = {k: v for k, v in env.items() if k in ENV_KEEP or k in env.get("_BOX_KEEP", "").split()}
    e.update(HOME=str(HOME), TMPDIR=str(HOME / ".cache" / "hive-tmp"), HIVE_NAME=name, HIVE_BROKER=f"{INSIDE}/ctl.sock", DISABLE_AUTOUPDATER="1",
             HTTP_PROXY=proxy, HTTPS_PROXY=proxy, http_proxy=proxy, https_proxy=proxy, NO_PROXY="localhost,127.0.0.1", no_proxy="localhost,127.0.0.1")
    return a, e

NOTE = """

# You are in a sandbox

This install runs agents inside a sandbox. What differs from the above:
- `~` here is the sandbox's own folder. `~/projects` and `~/kb` are the real ones and you can write them; `~/core`, `~/modules`, the board and the run records are read-only. Nothing else of the account is here. So no other logins, no `~/.ssh`, no systemd, no `heavy`, no sudo.
- `hive post` and `hive done` still work: they go through a broker outside, which signs them with your name.
- Network goes through a proxy (already set in your environment) that reaches the internet but not this machine or its local network. Tools that ignore the proxy settings, and git over ssh, won't connect.
- You can read the sign-in of the login you run on, because Claude Code can't run without it. Never print it or send it anywhere.
- To do something outside (`git push` with the person's keys, a service, an account setting), write a script and run `hive step ask --why "what and why, one line" --script PATH`. Depending on the install's power level, the person or the warden decides, except that money, credentials, opening the machine, anything sent off it with the person's accounts or keys (`git push`, `gh`, publishing, mail, posting), publishing private details, copying the person's files into anything you can write, and deleting repos or history always go to the person. To carry a project's commits out, the one form accepted is `git clone -q --bare --no-local <project> "$TMPDIR/c" && git -C "$TMPDIR/c" push <url> <full commit hash>:refs/heads/<branch>`, since running git in the project itself is refused. It runs once, outside, as the user, with up to 10 minutes. You get a post with the exit code, and its output stays with the person. Write the script to work on its own and to use nothing writable from in here, because it can't be judged otherwise. `hive step status` shows yours.
- Background shells end with your run: if you're restarted, they are gone, so check and start them again.
"""

DOOR_NOTE = """- You are the door and you're in the sandbox too, because you read what agents write. So you can't change `~/core/config.json`, add modules, or sign in: tell the person to use the app for those. Your posts are marked as coming through the door, and a step's approval word in one counts for nothing: the person types it in the app, or runs `hive step approve ID` in their own terminal.
"""

_AA_PROFILE = '# installed by the Hive\nabi <abi/4.0>,\ninclude <tunables/global>\nprofile bwrap /usr/bin/bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow capability,\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &bwrap//&unpriv_bwrap,\n}\nprofile unpriv_bwrap flags=(attach_disconnected,mediate_deleted) {\n  allow file rwlkm /{**,},\n  allow network,\n  allow unix,\n  allow ptrace,\n  allow signal,\n  allow mqueue,\n  allow io_uring,\n  allow userns,\n  allow mount,\n  allow umount,\n  allow pivot_root,\n  allow dbus,\n  allow pix /** -> &unpriv_bwrap,\n  audit deny capability,\n}\n'

def _userns_fix():
    try: limited = Path("/proc/sys/kernel/apparmor_restrict_unprivileged_userns").read_text().strip() == "1"
    except OSError: limited = False
    if not limited: return "Ask whoever administers this machine to allow unprivileged user namespaces for bubblewrap, then check again."
    f = STATE / "bwrap.apparmor"
    try: STATE.mkdir(exist_ok=True); f.write_text(_AA_PROFILE)
    except OSError: f = None
    why = ("Ask whoever administers this machine to run this, then check again. It lets bubblewrap make its private space: it re-allows unprivileged user namespaces for bubblewrap, a limit Ubuntu turns on by default, for every user of the machine, and that widens the kernel's attack surface")
    if f: return f"{why}. The profile is in {f}: read it first, then run: sudo install -m 644 {f} /etc/apparmor.d/bwrap && sudo apparmor_parser -r /etc/apparmor.d/bwrap"
    return why + ": printf '" + _AA_PROFILE.replace("\n", "\\n") + "' | sudo tee /etc/apparmor.d/bwrap >/dev/null && sudo apparmor_parser -r /etc/apparmor.d/bwrap"

def check():
    """Does it work here, by trying: {"works", "why", "fix"}. Under 15 s; what it makes is in a temp folder and goes when it ends."""
    def no(why, fix=""): return {"works": False, "why": why, "fix": fix}
    if not sys.platform.startswith("linux"): return no("The sandbox only runs on Linux so far.")
    if not bwrap(): return no("The sandbox needs a program called bubblewrap, and it isn't installed on this machine.",
                              "Ask whoever administers this machine to install it (on Ubuntu or Debian: sudo apt install bubblewrap), then check again.")
    d = Path(tempfile.mkdtemp(prefix="sandbox-check-"))
    try:
        (d / "home").mkdir(); (d / "sock").mkdir(); (d / "secret").write_text("x")
        probe = ("import os,socket,sys\n"
                 f"for p in {[str(d / 'secret'), str(HOME / '.ssh'), str(account_home() / '.ssh'), str(HOME / '.config/hive/env'), str(STATE / 'web.json')]!r}:\n"
                 "    if os.path.exists(p): sys.exit('it can see '+p)\n"
                 f"try: open({str(CORE / '.sandbox-probe')!r},'w')\n"
                 "except OSError: pass\n"
                 "else: sys.exit('it can write the core')\n"
                 "s=socket.socket(); s.settimeout(2)\n"
                 "try: s.connect(('1.1.1.1',53))\n"
                 "except OSError: pass\n"
                 "else: sys.exit('it reaches the network directly')\n"
                 "try: import ctypes; made=ctypes.CDLL(None).unshare(0x10000000)==0\n"
                 "except (ImportError,OSError): made=False\n"
                 "if made: sys.exit('it can make a user namespace')\n"
                 "open(os.path.expanduser('~/ok'),'w')\n")
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin")}
        a, e = wrap([_python(), "-I", "-c", probe], env, "check", d / "home", d / "sock")
        r = subprocess.run(a, env=e, capture_output=True, text=True, timeout=10)
        if r.returncode:
            err = (r.stderr or "").strip()
            (CORE / ".sandbox-probe").unlink(missing_ok=True)
            if "bwrap:" in err or "uid_map" in err or "Operation not permitted" in err:
                return no("This machine doesn't let bubblewrap make its private space (user namespaces are restricted): " + err.splitlines()[-1],
                          _userns_fix())
            return no("The sandbox started but didn't hold: " + (err.splitlines()[-1] if err else f"exit {r.returncode}"))
        if not (d / "home" / "ok").exists(): return no("The sandbox started but its home isn't writable.")
        if shutil.which("claude"):
            a, e = wrap(["claude", "--version"], env, "check", d / "home", d / "sock")
            r = subprocess.run(a, env=e, capture_output=True, text=True, timeout=10)
            if r.returncode: return no("Claude Code doesn't start inside the sandbox: " + ((r.stderr or r.stdout).strip().splitlines() or ["no output"])[-1],
                                       "Install Claude Code with its own installer (curl -fsSL https://claude.ai/install.sh | bash), then check again.")
        return {"works": True, "why": "", "fix": ""}
    except subprocess.TimeoutExpired: return no("The sandbox check took too long, so I can't say it works.")
    except OSError as x: return no(f"The sandbox check failed: {x}")
    finally: shutil.rmtree(d, ignore_errors=True)

def call(req, blobs=()):
    """From inside: one request to the broker. A JSON line, then the blobs' bytes; a JSON line back."""
    s = socket.socket(socket.AF_UNIX)
    s.settimeout(120)
    try: s.connect(os.environ["HIVE_BROKER"])
    except OSError as x: sys.exit(f"can't reach the broker outside the sandbox: {x}")
    with s, s.makefile("rb") as r:
        s.sendall((json.dumps({**req, "sizes": [len(b) for b in blobs]}) + "\n").encode())
        for b in blobs: s.sendall(b)
        line = r.readline()
    return json.loads(line) if line else {"err": "the broker closed the connection"}
