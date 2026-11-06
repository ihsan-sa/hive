#!/usr/bin/env python3
"""server check: read-only posture report for the Hive on an always-on server. Exit 1 on any FAIL.
Facts are gathered in gather(); verdicts come from judge(facts), so tests feed facts directly."""
import os, sys, json, shutil, subprocess, grp, pwd

BAD_GROUPS = ("sudo", "docker", "lxd", "wheel", "admin")

def run(cmd):
    try: return subprocess.run(cmd, capture_output=True, text=True, timeout=10).stdout
    except Exception: return ""

def gather(app_port=None):
    user = pwd.getpwuid(os.getuid()).pw_name
    groups = [g.gr_name for g in grp.getgrall() if user in g.gr_mem] + [grp.getgrgid(pwd.getpwnam(user).pw_gid).gr_name]
    listens = []  # (address, port)
    for ln in run(["ss", "-H", "-ltn"]).splitlines():
        f = ln.split()
        if len(f) >= 4 and ":" in f[3]:
            a, _, p = f[3].rpartition(":"); listens.append((a.strip("[]"), int(p) if p.isdigit() else 0))
    sudo_n = subprocess.run(["sudo", "-n", "true"], capture_output=True).returncode == 0
    ssh = run(["sshd", "-T"]).lower() if shutil.which("sshd") else ""
    du = shutil.disk_usage(os.path.expanduser("~"))
    return {"user": user, "groups": groups, "listens": listens, "app_port": app_port, "passwordless_sudo": sudo_n,
            "linger": os.path.exists(f"/var/lib/systemd/linger/{user}"),
            "ssh_password": ("passwordauthentication yes" in ssh) if ssh else None,
            "disk_pct": round(100 * du.used / du.total), "this_account": os.environ.get("HIVE_THIS_ACCOUNT") == "1"}

def judge(f):
    """-> list of (FAIL|WARN|ok, text)"""
    out = []
    def add(level, ok_text, bad_text, bad): out.append((level if bad else "ok", bad_text if bad else ok_text))
    wide = [(a, p) for a, p in f["listens"] if a not in ("127.0.0.1", "::1", "localhost")]
    add("FAIL", "app port is loopback only", f"app port {f['app_port']} is open beyond loopback",
        f["app_port"] is not None and any(p == f["app_port"] for _, p in wide))
    others = sorted({p for _, p in wide if p != f["app_port"]})
    add("WARN", "no other ports open to the world", f"ports open to the world: {others}", bool(others))
    bad = [g for g in f["groups"] if g in BAD_GROUPS]
    add("FAIL", "Hive user holds no admin group", f"Hive user is in {bad}", bool(bad))
    add("FAIL", "no passwordless sudo", "Hive user has passwordless sudo", f["passwordless_sudo"])
    add("FAIL", "lingering on", "lingering is off: the Hive stops at logout", not f["linger"])
    add("FAIL", "own dedicated account", "installed in the owner's own account", f["this_account"])
    add("WARN", "ssh password login off or unknown", "ssh password login is on", f["ssh_password"] is True)
    add("WARN", "disk under 85%", f"disk {f['disk_pct']}% full", f["disk_pct"] > 85)
    return out

def main(argv):
    port = int(argv[argv.index("--port") + 1]) if "--port" in argv else None
    res = judge(gather(port))
    for lvl, t in res: print(f"{lvl:4} {t}")
    return 1 if any(l == "FAIL" for l, _ in res) else 0

if __name__ == "__main__": sys.exit(main(sys.argv[1:]))
