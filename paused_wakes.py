#!/usr/bin/env python3
"""Count graded runs woken into a thread after the person paused it (by anyone but the person), over --since (default 7d).
The measure for the paused-untiered hint (a9357, 2026-11-23): a pause the person gave by word holds only once a paused tier
holds the thread; until then reminders, timers and keeper rounds woke agents there (a3f17 into the Hi-Fi lab, 11-22).
--rows lists them. A post id's first 8 digits are its MMDDHHMM, which orders the wake against the pause."""
import re, subprocess, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import goals

since = sys.argv[sys.argv.index("--since") + 1] if "--since" in sys.argv else "7d"
out = subprocess.run([sys.executable, "-I", str(Path(__file__).resolve().parent / "outcomes.py"), "list", "--since", since],
                     capture_output=True, text=True).stdout
runs = [m.groups() for m in re.finditer(r"^(\S+)\s+(\w+)\s+(\w+)\s+woken by (\S+) in (\S+) \(post ([0-9a-f]{12})\)", out, re.M)]
held = goals.person_held_of(sorted({r[4] for r in runs}))
hit = []
for at, name, grade, by, th, pid in runs:
    m = re.search(r"\b([0-9a-f]{12})\b", held.get(th, ""))
    if by != "person" and m and pid[:8] > m.group(1)[:8]: hit.append((at, name, grade, by, th, pid))
if "--rows" in sys.argv:
    for r in hit: print(*r)
print(len(hit))
