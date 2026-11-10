#!/usr/bin/env python3
"""Keep each thread's two-line status bar fresh (`hive status`). Run by hive-thread-status.timer every hour; a run sets its own bars at its end (inbox.run_end_bars), so this only picks up what that missed.
Finds threads with a post newer than their status (and active in the last 6 h), and if there are any posts one brief that starts a
single Haiku agent in thread `thread-status` to read them and set the statuses. No model cost when nothing changed."""
import json, os, subprocess, sys, time
from datetime import datetime, timezone
HOME = os.path.expanduser("~")
SKIP = {"thread-status", "watchdog", "keeper", "improve", "side"}
MAX = 20
WAIT_HOLD = 24 * 3600  # a `Waiting:` bar an agent set is theirs for this long: the agent changes it when the wait ends

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
    st, bar = {}, {}
    try:
        for ln in open(f"{HOME}/.hive/thread-status.jsonl"):
            try: r = json.loads(ln)
            except ValueError: continue
            st[r["thread"]] = r["at"]; bar[r["thread"]] = r
    except OSError: pass
    now = time.time()
    def waiting(t):
        r = bar.get(t, {}); last_line = (str(r.get("text", "")).strip().splitlines() or [""])[-1]
        return last_line.lower().startswith("waiting:") and r.get("from") != "status-timer" and now - r["at"] < WAIT_HOLD
    out = [(t, l) for t, l in last.items() if t not in SKIP and now - l < 6 * 3600 and l > st.get(t, 0) + 60 and (t in st or n[t] > 1) and not waiting(t)]
    out.sort(key=lambda x: -x[1])
    return [t for t, _ in out[:MAX]]

def current_pct(day=24 * 3600, grace=65 * 60):
    """% of threads active in the last day, quiet long enough for the hourly timer to have run, whose bar is as new as their last post. What `thread_status.py current-pct` prints."""
    last, n, st = {}, {}, {}
    for ln in open(f"{HOME}/board.jsonl", errors="replace"):
        try: p = json.loads(ln)
        except ValueError: continue
        t = p.get("thread")
        if t: last[t] = max(last.get(t, 0), ts(p.get("at", ""))); n[t] = n.get(t, 0) + 1
    try:
        for ln in open(f"{HOME}/.hive/thread-status.jsonl", errors="replace"):
            try: r = json.loads(ln)
            except ValueError: continue
            st[r["thread"]] = max(st.get(r["thread"], 0), r["at"])
    except OSError: pass
    now = time.time()
    active = [t for t, l in last.items() if t not in SKIP and grace < now - l < day and (t in st or n[t] > 1)]
    return round(100 * sum(st.get(t, 0) >= last[t] - 60 for t in active) / len(active), 1) if active else 100.0

if __name__ == "__main__":
    if sys.argv[1:] == ["current-pct"]: print(current_pct()); sys.exit(0)
    ts_ = stale()
    if not ts_: sys.exit(0)
    brief = ("Refresh thread status bars. For each thread below: `hive read --thread T -n 12` (and the existing bar with `hive status T --read`), then set it with "
             "`hive status T <<'X'` and exactly two short lines: line 1 what the thread is about; line 2 the current state (done / in progress and who / blocked on what or whom, "
             "and any thread it depends on). Plain words, under 160 chars a line. Keep the old wording if nothing changed. A line 2 starting Done:, Blocked:, Needs the person:, Waiting: or Told to stop: is an agent's own reason for stopping: keep it word for word unless a later post shows the work has moved on. Don't post in those threads. "
             "Finish by posting one line in this thread saying how many you set.\nThreads: " + " ".join(ts_))
    subprocess.run(["hive", "post", "--thread", "thread-status", "--to", "new", "--model", "haiku"], input=brief, text=True,
                   env={**os.environ, "HIVE_NAME": "status-timer"})
