#!/usr/bin/env python3
"""Count agents started in the last N days (default 7) by an agent's post that had a fresh-agent word (@new, @haiku, ...)
in quotation marks: a quote that started an agent by accident. `hive post` refuses these since core 18751e8 (i0431)."""
import json, re, sys, datetime as dt
from pathlib import Path

HOME = Path.home()
QF = re.compile(r"[\"“‘']@([a-z0-9][a-z0-9_-]*)", re.I)
CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)
FRESH = {"new", "claude", "codex", "free", "haiku", "sonnet", "opus", "fable", "astra"}
AGENT = re.compile(r"[a-z][0-9a-f]{4,}")

days = float(sys.argv[1]) if len(sys.argv) > 1 else 7
since = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=days)).isoformat()
bad = set()
for line in open(HOME / "board.jsonl"):
    try: p = json.loads(line)
    except ValueError: continue
    if p.get("at", "") < since or not AGENT.fullmatch(str(p.get("from", ""))): continue
    if any(m.group(1).lower() in FRESH for m in QF.finditer(CODE.sub(" ", str(p.get("text", ""))))): bad.add(p["id"])
n = 0
for line in open(HOME / "log.jsonl"):
    try: e = json.loads(line)
    except ValueError: continue
    if e.get("event") == "start" and e.get("post") in bad and e.get("at", "") >= since: n += 1
print(n)
