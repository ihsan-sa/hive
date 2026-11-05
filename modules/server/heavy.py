#!/usr/bin/env python3
"""heavy [--quick|--status] cmd...: run a memory-heavy job in turn, so that if memory runs out it's the job that dies, not an agent.
A job gets its own cgroup in the jobs slice when the user's systemd answers. An agent in the sandbox can't reach it, so there the job runs without one: it still queues with the others and is still first to be killed, and the limit on everything the Hive runs (`server limits on`) still holds.
The queue is in ~/projects because that's the one writable folder a sandboxed agent shares with the rest of the Hive."""
import atexit, fcntl, os, shutil, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
DIR = HOME / "projects" / ".heavy"
SLICE = "app-hivejobs.slice"
QUICK_S, QUICK_MB, QUICK_SLOTS = 300, 1500, 2


def slots():
    try: return max(1, int((DIR / "slots").read_text()))
    except (OSError, ValueError): return 1


def take(lane, n, cmd):
    """Waits for a free slot, first free wins, and returns it locked. The lock dies with this process, so nothing goes stale."""
    DIR.mkdir(parents=True, exist_ok=True)
    told, ticket = False, DIR / f"w.{os.getpid()}"
    atexit.register(ticket.unlink, missing_ok=True)
    while True:
        for i in range(n):
            f = open(DIR / f"{lane}-{i}", "a+")
            try: fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError:
                f.close(); continue
            f.seek(0); f.truncate(); f.write(f"{int(time.time())} {' '.join(cmd)[:200]}\n"); f.flush()
            ticket.unlink(missing_ok=True)
            return f
        if not told:
            print(f"heavy: waiting for a turn ({n} at a time)", file=sys.stderr); told = True
        ticket.write_text(os.environ.get("HIVE_NAME", "?"))  # the starter reads a fresh ticket as "waiting its turn, not stuck". A name and a time, because a sandbox has its own pids.
        time.sleep(2)


def status():
    busy = 0
    for p in sorted(DIR.glob("*-[0-9]*")):
        with open(p, "a+") as f:
            try:
                fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
                continue
            except OSError:
                f.seek(0); started, _, what = f.read().strip().partition(" ")
        busy += 1
        print(f"{p.name:<10} {(int(time.time()) - int(started or 0)) // 60} min  {what}")
    if not busy: print("Nothing running.")


def bus_ok():
    if not os.environ.get("XDG_RUNTIME_DIR") or not shutil.which("systemd-run"): return False
    try: return subprocess.run(["systemd-run", "--user", "--scope", "--quiet", "--collect", f"--slice={SLICE}", "true"], capture_output=True, timeout=20).returncode == 0
    except (OSError, subprocess.SubprocessError): return False


def preflight(cmd):
    """Fail now, not after the queue, when the command can't run. Sees through `timeout N`."""
    c = cmd[2:] if cmd[0] == "timeout" and len(cmd) > 2 else cmd
    if "=" not in c[0] and not shutil.which(c[0]): sys.exit(f"heavy: {c[0]}: command not found")


def main():
    a = sys.argv[1:]
    if not a or a[0] in ("-h", "--help"):
        print("usage: heavy [--quick] cmd...   runs a job in turn; if memory runs out, the job is killed first\n       heavy --quick cmd...     for under 5 min: past the queue, stopped at 5 min or 1500 MB\n       heavy --status           what's running")
        return 0
    if a[0] == "--status": status(); return 0
    quick = a[0] == "--quick"
    cmd = a[1:] if quick else a
    if not cmd: sys.exit("heavy: no command")
    preflight(cmd)
    lock = take("quick" if quick else "job", QUICK_SLOTS if quick else slots(), cmd)
    if quick: cmd = ["timeout", str(QUICK_S)] + cmd
    if bus_ok():
        props = [f"--property=MemoryMax={QUICK_MB}M", "--property=MemorySwapMax=0"] if quick else []
        cmd = ["systemd-run", "--user", "--scope", "--quiet", "--collect", f"--slice={SLICE}"] + props + ["--"] + cmd
    def kill_me_first():
        try: Path("/proc/self/oom_score_adj").write_text("300")
        except OSError: pass
    try: rc = subprocess.run(cmd, preexec_fn=kill_me_first).returncode
    except KeyboardInterrupt: rc = 130
    lock.close()
    rc = 128 - rc if rc < 0 else rc
    if rc in (137, 143): print(f"heavy: stopped with code {rc}, which usually means it ran out of memory.", file=sys.stderr)
    if quick and rc == 124: print(f"heavy: stopped at the {QUICK_S} s limit of --quick. Use plain heavy for this job.", file=sys.stderr)
    return rc


if __name__ == "__main__":
    sys.exit(main())
