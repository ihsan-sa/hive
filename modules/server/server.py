#!/usr/bin/env python3
"""The server module: a Hive on a machine that stays on. No root, nothing runs until asked. See README.md."""
import argparse, grp, ipaddress, json, os, pwd, re, secrets, shutil, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
STATE = HOME / ".hive" / "server"
UNITS = HOME / ".config" / "systemd" / "user"
ROOT_GROUPS = {"sudo", "wheel", "admin", "root", "docker", "lxd", "incus", "libvirt", "disk"}
BACKUP_NAMES = ["projects", "kb", "files", "board.jsonl", "log.jsonl", "asks.jsonl", "settings.json"]
BACKUP_EXCLUDES = [".heavy", "node_modules", ".venv", "__pycache__", ".env", ".env.*", "*.pem", "*.key", "*.token", "id_rsa*", "id_ed25519*", "id_ecdsa*", ".netrc", ".npmrc", ".pypirc", ".git-credentials", ".credentials.json"]
BACKUP_MAX_AGE_H = 48
TIMER = "hive-server-backup.timer"
JOBS_SLICE = "app-hivejobs.slice"
APP_DROPIN = UNITS / "app.slice.d" / "hive-server.conf"
TMP_ENV = HOME / ".config" / "environment.d" / "hive-server-tmp.conf"
HEAVY_SLOTS = HOME / "projects" / ".heavy" / "slots"  # in projects so that a sandboxed agent shares the queue: its own ~ is a separate folder


def run(*cmd, **kw):
    """The command's result, or None when it isn't there or hangs: a check never stops over a missing tool."""
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=kw.pop("timeout", 20), **kw)
    except (OSError, subprocess.SubprocessError):
        return None


def out(*cmd):
    r = run(*cmd)
    return r.stdout.strip() if r and r.returncode == 0 else ""


def app_port():
    try:
        return int(json.loads((HOME / ".hive" / "web.json").read_text())["port"])
    except (OSError, ValueError, KeyError, TypeError):
        return None


def listeners():
    """(address, port) of each TCP socket listening on this machine, or None when ss can't say."""
    r = run("ss", "-H", "-ltn")
    if not r or r.returncode != 0: return None
    found = []
    for line in r.stdout.splitlines():
        f = line.split()
        if len(f) < 4 or ":" not in f[3]: continue
        addr, port = f[3].rsplit(":", 1)
        if port.isdigit(): found.append((addr.strip("[]").split("%")[0], int(port)))
    return found


def is_local(addr):
    try: return ipaddress.ip_address(addr).is_loopback
    except ValueError: return False


def is_private(addr):
    """On a private network or a tailnet: not reachable from the internet by itself."""
    try:
        a = ipaddress.ip_address(addr)
        if a.is_unspecified: return False  # Python counts the any-address as private, and it listens on every network
        return a.is_private or (a.version == 4 and a.packed[0] == 100 and 64 <= a.packed[1] < 128)  # tailnets use that range
    except ValueError: return False


def mem_mb():
    m = {}
    for line in Path("/proc/meminfo").read_text().splitlines():
        k, v = line.split(":", 1); m[k] = int(v.split()[0]) // 1024
    return m


def fs_type(path):
    best, kind = "", ""
    for line in Path("/proc/self/mountinfo").read_text().splitlines():
        left, right = line.split(" - ", 1)
        mount = left.split()[4]
        if (str(path) == mount or str(path).startswith(mount.rstrip("/") + "/")) and len(mount) >= len(best):
            best, kind = mount, right.split()[0]
    return kind


def ssh_password_login():
    """True or False from sshd's config files where this user may read them, else None. The first setting wins, and the drop-ins come first."""
    files = sorted(Path("/etc/ssh/sshd_config.d").glob("*.conf")) if Path("/etc/ssh/sshd_config.d").is_dir() else []
    for f in files + [Path("/etc/ssh/sshd_config")]:
        try: text = f.read_text()
        except OSError: return None
        m = re.search(r"^\s*PasswordAuthentication\s+(\S+)", text, re.M | re.I)
        if m: return m.group(1).lower() == "yes"
    return True if Path("/etc/ssh/sshd_config").exists() else None


def backup_config():
    try: return json.loads((STATE / "backup.json").read_text())
    except (OSError, ValueError): return None


def check():
    """(level, what, what to do): level is fail, warn or ok."""
    r = []
    def add(level, what, fix=""): r.append((level, what, fix))
    me = pwd.getpwuid(os.getuid())
    groups = {grp.getgrgid(g).gr_name for g in os.getgroups()} | {grp.getgrgid(me.pw_gid).gr_name}
    bad = sorted(groups & ROOT_GROUPS)
    if bad: add("fail", f"This user is in groups that lead to root: {', '.join(bad)}. An agent could take over the machine.", f"As the owner: sudo gpasswd -d {me.pw_name} GROUP")
    s = run("sudo", "-n", "true", stdin=subprocess.DEVNULL) if shutil.which("sudo") else None
    if s and s.returncode == 0: add("fail", "This user has sudo without a password. An agent could take over the machine.", "As the owner, remove that sudoers rule.")
    if not bad and not (s and s.returncode == 0): add("ok", "This user has no way to root that this check can see.")
    if (HOME / ".hive-account").exists(): add("fail", "The Hive is in a folder of your own account, so only the sandbox keeps agents from your files.", "On a server, install it as its own user: the install line without --this-account.")
    linger = out("loginctl", "show-user", me.pw_name, "-p", "Linger", "--value")
    if linger == "no": add("fail", "The Hive stops when you log out and doesn't come back after a reboot.", f"As the owner: sudo loginctl enable-linger {me.pw_name}")
    elif linger == "yes": add("ok", "The Hive survives logout and reboot.")
    port, ls = app_port(), listeners()
    if ls is None: add("warn", "Couldn't list listening ports: ss isn't working here.")
    else:
        open_app = [a for a, p in ls if p == port and not is_local(a)]
        if open_app: add("fail", f"The app's port {port} is open on {', '.join(open_app)}. Its address is the key, so this must not face a network.", "Stop whatever opened it. `server reach` shows the safe ways in.")
        elif port: add("ok", f"The app answers only on this machine (port {port}).")
        world = sorted({p for a, p in ls if not is_local(a) and not is_private(a) and p not in (22, port)})
        if world: add("warn", f"Other ports listening beyond this machine: {', '.join(map(str, world))}. A firewall may still block them; check that you meant these.")
    if ssh_password_login(): add("warn", "SSH allows password login.", "Use a key, then set PasswordAuthentication no in /etc/ssh/sshd_config.")
    m = mem_mb()
    if m.get("SwapTotal", 0) == 0 and m["MemTotal"] < 6000: add("warn", f"No swap with {m['MemTotal']} MB of memory: one big job can stop everything.", "As the owner, add a swap file.")
    tmp = os.environ.get("TMPDIR") or "/tmp"
    if fs_type(tmp) == "tmpfs" and not TMP_ENV.exists(): add("warn", f"{tmp} is held in memory, so files there use up what the agents need.", "server limits on")
    du = shutil.disk_usage(HOME)
    pct = du.used * 100 // du.total
    if pct >= 85: add("warn", f"The disk is {pct}% full.")
    else: add("ok", f"Disk {pct}% full.")
    if not (UNITS / JOBS_SLICE).exists(): add("warn", "No memory limits: a big job can take the agents down with it.", "server limits on")
    else: add("ok", "Memory limits are on.")
    if not backup_config(): add("warn", "No backup: the Hive's own history is on this same disk.", "server backup setup DESTINATION")
    else:
        age = backup_age_h()
        if age is None or age > BACKUP_MAX_AGE_H: add("warn", "The last backup is over 2 days old." if age is not None else "No backup has finished yet.", "server backup now")
        else: add("ok", f"Last backup {int(age)} h ago.")
    if Path("/etc/apt/apt.conf.d").is_dir():
        auto = Path("/etc/apt/apt.conf.d/20auto-upgrades")
        try: on = re.search(r'Unattended-Upgrade\s+"1"', auto.read_text())
        except OSError: on = None
        if not on: add("warn", "Security updates for the system aren't automatic.", "As the owner: sudo apt install unattended-upgrades && sudo dpkg-reconfigure -plow unattended-upgrades")
    timer = out("systemctl", "--user", "is-enabled", "hive-update.timer")
    if timer and timer != "enabled": add("warn", "The Hive's automatic updates are off.", "Turn them on in Settings.")
    return r


SUDO_NOTE = "\nLines with sudo are yours to run, not an agent's. Read each one first and compare it with the guide (Server module): agents can edit this tool."


def cmd_check(a):
    results = check()
    if a.json:
        print(json.dumps([{"level": l, "what": w, "fix": f} for l, w, f in results]))
    else:
        for level in ("fail", "warn", "ok"):
            for l, w, f in results:
                if l == level:
                    print(f"{level.upper():<4}  {w}")
                    if f: print(f"      {f}")
        if any("sudo" in f for _, _, f in results): print(SUDO_NOTE)
    return 1 if any(l == "fail" for l, _, _ in results) else 0


def cmd_reach(_):
    port = app_port()
    p = port or "PORT"
    print(f"""The app answers only on this machine. Three ways in, safest first:

1. SSH forward. It opens nothing new. On your own computer:
     ssh -L {p}:127.0.0.1:{p} you@THIS-SERVER
   Use the login you normally use for SSH. Then open the address that `hive web` prints here, as this Hive's user. That address is the key, so keep it to yourself.
2. Tailscale, if you use it: put this server on your tailnet and run the same line to its tailnet name. Don't open the port to the tailnet itself.
3. Cloudflare Tunnel with Access, on your own account, for a phone: Settings > Access from outside.

Never open the port itself, in a firewall or by binding the app to another address. `server check` fails if it finds that.""")
    if not port: print("\nThe app hasn't started yet: its port isn't known.")
    return 0


def restic_env():
    c = backup_config()
    if not c: sys.exit("No backup is set up. Run: server backup setup DESTINATION")
    env = dict(os.environ, RESTIC_REPOSITORY=c["dest"], RESTIC_PASSWORD_FILE=str(STATE / "backup.key"))
    extra = STATE / "backup.env"
    if extra.exists():
        for line in extra.read_text().splitlines():
            if "=" in line and not line.lstrip().startswith("#"):
                k, v = line.split("=", 1); env[k.strip()] = v.strip()
    return env


def need_restic():
    if not shutil.which("restic"):
        sys.exit("Backups use restic, which isn't installed. It's for the owner to add: sudo apt install restic (or your system's package manager). Then run this again.")


def backup_age_h():
    try: return (time.time() - (STATE / "backup.last").stat().st_mtime) / 3600
    except OSError: return None


def same_disk(dest):
    if re.match(r"^[a-z0-9]+:", dest): return False
    p = Path(dest).expanduser().absolute()
    while not p.exists(): p = p.parent
    return p.stat().st_dev == HOME.stat().st_dev


def backup_setup(dest):
    need_restic()
    STATE.mkdir(parents=True, exist_ok=True)
    key = STATE / "backup.key"
    if not key.exists():
        fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as f: f.write(secrets.token_urlsafe(32) + "\n")
    (STATE / "backup.json").write_text(json.dumps({"dest": dest}))
    r = run("restic", "init", env=restic_env(), timeout=300)
    if not r or (r.returncode != 0 and "already" not in (r.stderr or "")):
        (STATE / "backup.json").unlink()
        sys.exit(f"Couldn't set up a backup at {dest}: {(r.stderr if r else 'restic did not finish').strip()}\nNothing is scheduled. For a destination that needs a login, put its settings in {STATE}/backup.env.")
    UNITS.mkdir(parents=True, exist_ok=True)
    (UNITS / "hive-server-backup.service").write_text("[Unit]\nDescription=Hive backup (module server)\n\n[Service]\nType=oneshot\nEnvironment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin\nExecStart=%h/.local/bin/server backup now\nNice=15\nIOSchedulingClass=idle\n")
    (UNITS / TIMER).write_text("[Unit]\nDescription=Hive backup every day (module server)\n\n[Timer]\nOnCalendar=daily\nRandomizedDelaySec=2h\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n")
    run("systemctl", "--user", "daemon-reload")
    t = run("systemctl", "--user", "enable", "--now", TIMER)
    print(f"Backups go to {dest} every day. Run one now with: server backup now")
    if not t or t.returncode != 0: print("The daily timer couldn't start here (no user systemd), so run backups by hand for now.")
    if same_disk(dest): print("Warning: that's on this same disk, so it won't survive the disk failing. Use another machine or a storage service.")
    print(f"""
Keep a copy of the key somewhere off this server, because without it the backups can't be read. It's the file {key}: read it yourself, not through an agent.
Agents run as this same user, so they can read that key and reach the destination. If they must not be able to delete old backups, use a destination set to append-only. Logins and tokens are left out of the backup. That goes by file name, so a secret pasted into a post or kept under an ordinary name in a project still goes in.""")


def backup_now():
    need_restic()
    env = restic_env()
    paths = [str(HOME / n) for n in BACKUP_NAMES if (HOME / n).exists()]
    if not paths: sys.exit("There's nothing to back up yet.")
    cmd = ["restic", "backup", "--quiet", "--tag", "hive"]
    for x in BACKUP_EXCLUDES: cmd += ["--exclude", x]
    r = subprocess.run(cmd + paths, env=env)
    if r.returncode not in (0, 3):  # 3: done, with some files unreadable
        sys.exit(f"The backup failed (restic {r.returncode}).")
    (STATE / "backup.last").touch()
    k = subprocess.run(["restic", "forget", "--quiet", "--tag", "hive", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6", "--prune"], env=env, capture_output=True, text=True)
    if k.returncode != 0: print("Old backups weren't pruned, which is expected on an append-only destination.")
    print("Backup done." + (" Some files couldn't be read." if r.returncode == 3 else ""))


def backup_restore(target):
    need_restic()
    t = Path(target or HOME / f"restored-{time.strftime('%Y%m%d')}").expanduser()
    if t.exists() and any(t.iterdir()): sys.exit(f"{t} isn't empty. A restore never writes over files: pick another folder.")
    r = subprocess.run(["restic", "restore", "latest", "--target", str(t)], env=restic_env())
    if r.returncode != 0: sys.exit("The restore failed.")
    print(f"Restored into {t}. Nothing live was changed: copy back what you need.")


def backup_status():
    c = backup_config()
    if not c:
        print("No backup is set up. Run: server backup setup DESTINATION")
        return
    age = backup_age_h()
    print(f"Destination: {c['dest']}\nLast backup: {'none yet' if age is None else f'{int(age)} h ago'}")


def cmd_backup(a):
    if a.do == "setup":
        if not a.arg: sys.exit("Say where: server backup setup DESTINATION (a restic destination, such as sftp:you@host:/backups or s3:...)")
        backup_setup(a.arg)
    elif a.do == "now": backup_now()
    elif a.do == "restore": backup_restore(a.arg)
    else: backup_status()
    return 0


def limits_for(total_mb, swap_mb):
    """Sized from this box. A share is kept for the system, agents throttle before the hard stop, and jobs hit their own stop first.
    Jobs get no MemoryHigh: it only throttles, and a throttled job with swap full stalls the whole machine. Little swap for the same reason."""
    keep = max(512, total_mb * 15 // 100)
    app_max = max(256, total_mb - keep)
    return {"app_high": app_max * 90 // 100, "app_max": app_max, "app_swap": max(256, swap_mb // 2), "jobs_max": app_max * 60 // 100, "jobs_swap": 256, "slots": max(1, (app_max * 60 // 100) // 2048)}


def cgroup_memory_ok():
    uid = os.getuid()
    p = Path(f"/sys/fs/cgroup/user.slice/user-{uid}.slice/user@{uid}.service/cgroup.subtree_control")
    try: return "memory" in p.read_text().split()
    except OSError: return False


def limits_on():
    m = mem_mb()
    l = limits_for(m["MemTotal"], m.get("SwapTotal", 0))
    if not cgroup_memory_ok():
        sys.exit("This machine doesn't let a user set memory limits (the memory controller isn't delegated), so nothing changed. `heavy` still queues jobs and marks them to be killed first.")
    APP_DROPIN.parent.mkdir(parents=True, exist_ok=True)
    APP_DROPIN.write_text(f"# Module server: limits for everything the Hive runs. Remove with: server limits off\n[Slice]\nMemoryHigh={l['app_high']}M\nMemoryMax={l['app_max']}M\nMemorySwapMax={l['app_swap']}M\n")
    (UNITS / JOBS_SLICE).write_text(f"[Unit]\nDescription=Hive heavy jobs (module server)\n\n[Slice]\nMemoryMax={l['jobs_max']}M\nMemorySwapMax={l['jobs_swap']}M\nCPUWeight=50\n")
    STATE.mkdir(parents=True, exist_ok=True)
    (STATE / "limits.json").write_text(json.dumps(l))
    HEAVY_SLOTS.parent.mkdir(parents=True, exist_ok=True)
    HEAVY_SLOTS.write_text(f"{l['slots']}\n")
    tmp = os.environ.get("TMPDIR") or "/tmp"
    note = ""
    if fs_type(tmp) == "tmpfs":
        d = HOME / ".hive" / "tmp"; d.mkdir(parents=True, exist_ok=True)
        TMP_ENV.parent.mkdir(parents=True, exist_ok=True)
        TMP_ENV.write_text(f"TMPDIR={d}\n")
        note = f"\nTemporary files move to {d} after the next restart, because {tmp} is held in memory."
    r = run("systemctl", "--user", "daemon-reload")
    print(f"Limits on, sized from this machine's {m['MemTotal']} MB: everything the Hive runs stops at {l['app_max']} MB, and heavy jobs at {l['jobs_max']} MB, {l['slots']} at a time. If jobs run over, the kernel kills a job, not an agent.{note}")
    if not r or r.returncode != 0: print("They take effect after the next restart: systemd couldn't be reached from here.")


def limits_off():
    for f in (APP_DROPIN, UNITS / JOBS_SLICE, STATE / "limits.json", TMP_ENV, HEAVY_SLOTS):
        f.unlink(missing_ok=True)
    run("systemctl", "--user", "daemon-reload")
    print("Limits off.")


def cmd_limits(a):
    if a.do == "on": limits_on()
    elif a.do == "off": limits_off()
    else:
        try:
            l = json.loads((STATE / "limits.json").read_text())
            print(f"On: everything the Hive runs stops at {l['app_max']} MB, heavy jobs at {l['jobs_max']} MB, {l['slots']} at a time.")
        except (OSError, ValueError):
            print("Off. Turn on with: server limits on")
    return 0


def cmd_updates(_):
    t = out("systemctl", "--user", "is-enabled", "hive-update.timer")
    print(f"Hive updates: {'automatic, and each one is checked against its signature before it installs' if t == 'enabled' else 'not automatic. Turn them on in Settings, or run: hive update'}.")
    print("System updates are the owner's. On Debian or Ubuntu: sudo apt install unattended-upgrades && sudo dpkg-reconfigure -plow unattended-upgrades")
    print(SUDO_NOTE)
    return 0


def main():
    if sys.platform != "linux": sys.exit("The server module is for Linux with systemd.")
    p = argparse.ArgumentParser(prog="server", description="A Hive on a server that stays on. Nothing here needs root or runs until you ask.")
    s = p.add_subparsers(dest="cmd", required=True)
    c = s.add_parser("check", help="what is safe and what isn't; exits 1 if something fails"); c.add_argument("--json", action="store_true"); c.set_defaults(fn=cmd_check)
    s.add_parser("reach", help="how to get to the app without opening a port").set_defaults(fn=cmd_reach)
    b = s.add_parser("backup", help="encrypted copies somewhere else, with restic"); b.add_argument("do", nargs="?", choices=["setup", "now", "status", "restore"], default="status"); b.add_argument("arg", nargs="?", help="setup: where to; restore: an empty folder"); b.set_defaults(fn=cmd_backup)
    l = s.add_parser("limits", help="memory limits so a big job can't take the agents down"); l.add_argument("do", nargs="?", choices=["on", "off", "status"], default="status"); l.set_defaults(fn=cmd_limits)
    s.add_parser("updates", help="how this machine stays up to date").set_defaults(fn=cmd_updates)
    a = p.parse_args()
    sys.exit(a.fn(a))


if __name__ == "__main__":
    main()
