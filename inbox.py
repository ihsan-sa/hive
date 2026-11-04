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
WARDEN = re.compile(r"w[0-9a-f]{4}")

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
        # A warden judging a step hears its step's thread and nothing else: an @all, or its name in any spelling, would steer the judge from outside.
        if WARDEN.fullmatch(name) and str(rec.get("thread")).startswith("step-") and p.get("thread") != rec["thread"]: continue
        at, targets = str(p.get("at", "")), names(p)
        if at <= rec.get("at", ""):
            # A restart moves "at" to now; an @all posted while the interrupted run couldn't hear it must still arrive.
            if "all" in targets and rec.get("thread") and at > rec.get("all_since", "~"): out.append(p)
            continue
        if name in targets or (rec.get("thread") and ("all" in targets or p.get("thread") == rec["thread"])):
            out.append(p)
    return out

STRENGTH = ("haiku", "sonnet", "opus", "fable")

def model_note(name, rec, ps):
    """A post naming this running agent that picks a stronger model (the menu, or --model): it should be answered on that model, so say how. A weaker one just holds from the next run."""
    now_on = rec.get("model")
    if now_on not in STRENGTH and now_on is not None: return ""
    up = [m for p in ps if name in names(p) and (m := p.get("model")) in STRENGTH and (now_on is None or STRENGTH.index(m) > STRENGTH.index(now_on))]
    return (f"\n\nThat post picks {up[-1]}, and this run is on {now_on or 'another model'}. Finish the step you're on and don't answer it here: "
            f'run `hive model {up[-1]} "picked for the post above"` and end your turn. You resume on {up[-1]} with your memory, and answer it there.') if up else ""

def fmt(p):
    to = " ".join("@" + n for n in sorted(names(p)))
    return (f"[{p.get('id')}] {str(p.get('at', ''))[:19]} {p.get('from')}{' -> ' + to if to else ''} "
            f"(thread {p.get('thread')})" + (f" replying to {p['reply_to']}" if p.get("reply_to") else "") + "\n  " + str(p.get("text", "")).replace("\n", "\n  ")
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
           "hive post where it matters):\n\n" + "\n\n".join(fmt(p) for p in ps) + model_note(name, rec, ps))
    if event == "Stop":
        print(json.dumps({"decision": "block", "reason": msg}))
    else:
        print(json.dumps({"hookSpecificOutput": {"hookEventName": event or "PostToolUse", "additionalContext": msg}}))

if __name__ == "__main__":
    main()
