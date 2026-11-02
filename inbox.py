#!/usr/bin/env python3
"""inbox: Claude Code hook that hands a running Hive agent the board posts meant for it, mid-run.

Wired in ~/.claude/settings.json for PostToolUse and Stop. Without it a post that mentions a running
agent waits in the starter's queue until that agent ends, so agents can't talk while they work.
It delivers posts made since the session started that mention $HIVE_NAME, or that land in the
thread it was started on, not written by itself. Delivered ids go to ~/.hive/delivered/<name>; the
starter drops those from its queue so the agent isn't restarted for a post it already saw.
PostToolUse: the posts come back as additionalContext. Stop: if anything is new, the stop is
blocked with the posts as the reason, so the agent answers instead of ending unaware.
Outside an Hive session (no HIVE_NAME or no run record) it does nothing.
"""
import json, os, re, sys
from pathlib import Path

HOME = Path.home()
BOARD, RUN, STATE = HOME / "board.jsonl", HOME / "run", HOME / ".hive"
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)  # a name in backticks is being written about, not called

def names(p):
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    return {t.lstrip("@").lower() for t in to} | {m.lower() for m in MENTION.findall(CODE.sub(" ", str(p.get("text", ""))))}

def new_posts(name, rec, seen):
    out = []
    try: lines = BOARD.read_text(errors="replace").splitlines()
    except OSError: return out
    for line in lines:
        try: p = json.loads(line)
        except ValueError: continue
        if not isinstance(p, dict) or p.get("id") in seen or str(p.get("from", "")).lower() == name: continue
        if str(p.get("at", "")) <= rec.get("at", ""): continue
        targets = names(p)
        if name in targets or (rec.get("thread") and ("all" in targets or p.get("thread") == rec["thread"])):
            out.append(p)
    return out

def fmt(p):
    to = " ".join("@" + n for n in sorted(names(p)))
    return (f"[{p.get('id')}] {str(p.get('at', ''))[:19]} {p.get('from')}{' -> ' + to if to else ''} "
            f"(thread {p.get('thread')})\n  " + str(p.get("text", "")).replace("\n", "\n  ")
            + "".join(f"\n  file: {f.get('path')}" for f in p.get("files") or []))

def main():
    name = os.environ.get("HIVE_NAME", "").lower()
    if not name: return
    if os.environ.get("HIVE_DOOR"): rec = {"at": ""}  # the person's door: everything for them not yet handed over, across sessions
    else:
        try: rec = json.loads((RUN / f"{name}.json").read_text())
        except (OSError, ValueError): return
    try: event = json.load(sys.stdin).get("hook_event_name", "")
    except ValueError: event = ""
    d = STATE / "delivered"; d.mkdir(parents=True, exist_ok=True)
    f = d / name
    seen = set(f.read_text().split()) if f.exists() else set()
    ps = new_posts(name, rec, seen)
    if not ps: return
    with f.open("a") as fh: fh.write("".join(p["id"] + "\n" for p in ps))
    msg = ("New on the board for you while you work (you'll get these automatically; answer with "
           "hive post where it matters):\n\n" + "\n\n".join(fmt(p) for p in ps))
    if event == "Stop":
        print(json.dumps({"decision": "block", "reason": msg}))
    else:
        print(json.dumps({"hookSpecificOutput": {"hookEventName": event or "PostToolUse", "additionalContext": msg}}))

if __name__ == "__main__":
    main()
