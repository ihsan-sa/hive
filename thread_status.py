#!/usr/bin/env python3
"""Keep each thread's two-line status bar fresh (`hive status`). Run by hive-thread-status.timer every 20 min.
Finds threads with a post newer than their status (and active in the last 6 h), and if there are any posts one brief that starts a
single Haiku agent in thread `thread-status` to read them and set the statuses. No model cost when nothing changed."""
import json, os, subprocess, sys, time
from datetime import datetime, timezone
HOME = os.path.expanduser("~")
SKIP = {"thread-status", "watchdog", "keeper", "improve", "side"}
MAX = 20

def ts(s):
    try: return datetime.fromisoformat(s).replace(tzinfo=datetime.fromisoformat(s).tzinfo or timezone.utc).timestamp()
    except (ValueError, TypeError): return 0

def stale():
    last, n = {}, {}
    for ln in open(f"{HOME}/board.jsonl"):
        try: p = json.loads(ln)
        except ValueError: continue
        t = p.get("thread")
        if t: last[t] = max(last.get(t, 0), ts(p.get("at", ""))); n[t] = n.get(t, 0) + 1
    st = {}
    try:
        for ln in open(f"{HOME}/.hive/thread-status.jsonl"):
            try: r = json.loads(ln)
            except ValueError: continue
            st[r["thread"]] = r["at"]
    except OSError: pass
    now = time.time()
    out = [(t, l) for t, l in last.items() if t not in SKIP and now - l < 6 * 3600 and l > st.get(t, 0) + 60 and (t in st or n[t] > 1)]
    out.sort(key=lambda x: -x[1])
    return [t for t, _ in out[:MAX]]

if __name__ == "__main__":
    ts_ = stale()
    if not ts_: sys.exit(0)
    brief = ("Refresh thread status bars. For each thread below: `hive read --thread T -n 12` (and the existing bar with `hive status T --read`), then set it with "
             "`hive status T <<'X'` and exactly two short lines: line 1 what the thread is about; line 2 the current state (done / in progress and who / blocked on what or whom, "
             "and any thread it depends on). Plain words, under 160 chars a line. Keep the old wording if nothing changed. A line 2 starting Done:, Blocked:, Needs the person:, Waiting: or Told to stop: is an agent's own reason for stopping: keep it word for word unless a later post shows the work has moved on. Don't post in those threads. "
             "Finish by posting one line in this thread saying how many you set.\nThreads: " + " ".join(ts_))
    subprocess.run(["hive", "post", "--thread", "thread-status", "--to", "new", "--model", "haiku"], input=brief, text=True,
                   env={**os.environ, "HIVE_NAME": "status-timer"})
