#!/usr/bin/env python3
"""inbox: Claude Code hook that hands a running Hive agent the board posts meant for it, mid-run.

Wired in ~/.claude/settings.json for PostToolUse and Stop. Without it a post that mentions a running
agent waits in the starter's queue until that agent ends, so agents can't talk while they work.
It delivers posts made since the session started that mention $HIVE_NAME, or that land in the
thread it was started on, not written by itself. Delivered ids go to ~/.hive/delivered/<name>; the
starter drops those from its queue so the agent isn't restarted for a post it already saw.
PostToolUse: the posts come back as additionalContext. Stop: if anything is new, the stop is
blocked with the posts as the reason, so the agent answers instead of ending unaware. Stop also
blocks once per run when the person started it and the agent is ending with no post since: its
plain-text answer reaches nobody (the person, 2026-11-04: a24ba answered twice into nothing).
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

def read_new(name, rec):
    """The board lines this agent hasn't been through yet. For one run record the filter below is fixed, so a line it passed over
    once never matches later: keep a byte cursor per agent and read only past it. A new record (a restart, a new session) or a
    shorter board (rotation) reads it all again. The cursor stops at the last whole line, so a post half-written is read next time."""
    cur = STATE / "cursor" / name
    key = json.dumps([rec.get("at"), rec.get("session"), rec.get("thread"), rec.get("all_since")])
    try: k, off = cur.read_text().split("\n")[:2]; off = int(off) if k == key else 0
    except (OSError, ValueError): off = 0
    try:
        with BOARD.open("rb") as fh:
            if off > os.fstat(fh.fileno()).st_size: off = 0
            fh.seek(off); data = fh.read()
    except OSError: return [], lambda: None
    end = data.rfind(b"\n") + 1
    def done():  # called once what was read is delivered, so a crash in between reads it again rather than losing it
        try: cur.parent.mkdir(parents=True, exist_ok=True); cur.write_text(f"{key}\n{off + end}\n")
        except OSError: pass
    return data[:end].decode(errors="replace").splitlines(), done

def new_posts(name, rec, seen, lines=None):
    out = []
    for line in lines if lines is not None else BOARD.read_text(errors="replace").splitlines():
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

def _claude_models():
    """The Claude model words, weakest first: config.json "models". A new model is one line there, not an edit in each file."""
    try: m = json.loads((Path(__file__).resolve().parent / "config.json").read_text()).get("models")
    except (OSError, ValueError): m = None
    return tuple(m) if isinstance(m, list) and m and all(isinstance(x, str) and re.fullmatch(r"[a-z][a-z0-9.-]*", x) for x in m) else ("haiku", "sonnet", "opus", "fable")
CLAUDE_MODELS = _claude_models()
STRENGTH = CLAUDE_MODELS  # weakest first

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

def person_note(ps):
    """A post from the person among those handed over: ask for a reply with the next tool call, not after the current job. A
    running agent answers the person in a median 104 s and a p90 of 13 min against 46 s and 4 min for one woken for the post
    (48 h to 2026-11-04), and the person posts "hello?" after about a minute of silence."""
    if not any(str(p.get("from", "")).lower() == "person" for p in ps): return ""
    return ("\n\nOne of these is from the person. Make your next tool call an hive post in its thread: one or two lines, the answer "
            "if you have it, else what you heard and what you'll do about it. Then carry on. They expect a reply within a minute, "
            "and until you post, the dash shows that thread waiting on you.")

NAMED = re.compile(r"[a-z][0-9a-f]{4}|hives")

def waker(rec, name):
    """Who this run answers to: "person", "hives", or an agent's name (its own after hive model or its waiter's post: the task is still
    owed a result); None for a scheduled run (keeper, watchdog, improver), which has its own brief."""
    src = str(rec.get("from") or "").lower()
    return src if src == "person" or NAMED.fullmatch(src) else None

def unanswered(name, rec):
    """Why this stop should wait, if a post from the person, another Hive or a named agent started the run and the agent hasn't
    posted since. Once per run. 10 of 2026-11-04's graded runs ended with the answer in plain text only (hive outcomes: silent)."""
    src = waker(rec, name)
    if os.environ.get("HIVE_DOOR") or not src or not rec.get("at"): return None
    mark = STATE / "nudged" / name
    try:
        if mark.read_text() == rec["at"]: return None
    except OSError: pass
    try:
        for line in BOARD.read_text().splitlines()[-3000:]:
            if f'"from": "{name}"' in line or f'"from":"{name}"' in line:
                p = json.loads(line)
                if p.get("from") == name and p.get("at", "") >= rec["at"]: return None
    except (OSError, ValueError): return None  # can't tell: let it stop
    mark.parent.mkdir(parents=True, exist_ok=True); mark.write_text(rec["at"])
    who = ("the person sees" if src == "person" else "the other Hive sees" if src == "hives" else "whoever waits on the task sees"
           if src == name else f"{src}, who named you, sees")
    return (f"You are ending this run without posting anything to the board, so {who} no answer: text outside hive post "
            f"reaches nobody. Post your answer now in thread {rec.get('thread') or 'the thread you were started in'} with "
            "hive post --thread T and a quoted heredoc, then stop. If you truly have nothing to say, just stop again.")

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
    lines, done = read_new(name, rec)
    ps = new_posts(name, rec, seen, lines)
    if ps:
        with f.open("a") as fh: fh.write("".join(p["id"] + "\n" for p in ps))
    done()
    if not ps:
        if event == "Stop" and (why := unanswered(name, rec)):
            print(json.dumps({"decision": "block", "reason": why}))
        return
    msg = ("New on the board for you while you work (you'll get these automatically; answer with "
           "hive post where it matters):\n\n" + "\n\n".join(fmt(p) for p in ps) + person_note(ps) + model_note(name, rec, ps))
    if event == "Stop":
        print(json.dumps({"decision": "block", "reason": msg}))
    else:
        print(json.dumps({"hookSpecificOutput": {"hookEventName": event or "PostToolUse", "additionalContext": msg}}))

if __name__ == "__main__":
    main()
