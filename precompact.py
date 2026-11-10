#!/usr/bin/env python3
"""precompact: Claude Code PreCompact hook behind the compaction summary length trial (projects/hive-meta/load.md, L16).

Whatever a PreCompact hook prints on exit 0 is appended to the compaction's instructions. If ~/.hive/compact_limit.json exists and has not
passed its "until", half the agents, by a fixed hash of trial name and agent name, are asked for a shorter summary. No file, no HIVE_NAME or
any error prints nothing and exits 0, so compaction goes on as before. Delete the file to end the trial; new compactions see it at once.
Config: {"name": "...", "until": ISO, "share": 0.5, "limit": 10000}. Every compaction under a live trial adds a line to ~/.hive/compact_limit.log,
treated or not, with the session id: that is the map from a transcript to its arm.
COMPACT_LIMIT_CONF and COMPACT_LIMIT_LOG move those two files for a test.
"""
import datetime as dt, hashlib, json, os, sys
from pathlib import Path

CONF = Path(os.environ.get("COMPACT_LIMIT_CONF") or Path.home() / ".hive/compact_limit.json")
LOG = Path(os.environ.get("COMPACT_LIMIT_LOG") or Path.home() / ".hive/compact_limit.log")
ASK = ("Keep this summary under {limit} characters in total. Keep the exact ask and any later instructions word for word, the rules still in force, "
       "the file paths and ids needed to continue, what is unfinished and the next step. Drop finished detail, code already saved in files, and anything "
       "that can be read again from a file or the board.")

def arm(trial, name, share):
    return int(hashlib.sha1(f"{trial}:{name}".encode()).hexdigest()[:8], 16) / 2**32 < share

def main():
    name = os.environ.get("HIVE_NAME")
    if not name or not CONF.exists(): return
    c = json.loads(CONF.read_text())
    if dt.datetime.now(dt.timezone.utc) >= dt.datetime.fromisoformat(c["until"]): return
    on = arm(c["name"], name, c.get("share", 0.5))
    try: h = json.load(sys.stdin)
    except ValueError: h = {}
    try:
        with LOG.open("a") as f: f.write(json.dumps({"ts": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"), "trial": c["name"], "name": name, "on": on, "session": h.get("session_id"), "trigger": h.get("trigger")}) + "\n")
    except OSError: pass
    if on: print(ASK.format(limit=c["limit"]))

if __name__ == "__main__":
    try: main()
    except Exception: pass
    sys.exit(0)
