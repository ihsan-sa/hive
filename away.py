#!/usr/bin/env python3
"""While you were away: what the Hive did in a window, in plain words, with no model use. Delivered, stopped and why, waiting on you.

`hive away [--since 12h] [--json]`. The app's Overview shows the same thing (GET /api/away). Everything comes from the log and the board,
so it holds across a crash or a restart of the Hive. Delivered is an agent's last word in a conversation it has finished with."""
import argparse, datetime as dt, json, re, sys, time
from pathlib import Path

HOME = Path.home()
STATE = HOME / ".hive"
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")   # agents' names are a letter and hex digits; the Hive's own (starter, keeper...) are words
PERSON = {"person", "box"}
SELF_THREADS = {"starter", "keeper", "improve", "watchdog", "progress"}
FOR_YOU = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?for you(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$", re.I | re.M)
# The reasons in the starter's words (WHY_WORDS there; test_away.py holds the two the same)
WHY_WORDS = {"connection": "no connection to Claude", "restart": "the machine restarted", "stalled": "went silent with nothing running", "other": "ended before it finished", "budget": "reached its budget or turn limit"}
STOPS = {"limited": "reached the usage limit", "login_rejected": "the Claude login expired", "stopped_by_hand": "stopped by hand", "kill_dropped": "stopped by hand", "wake_failed": "its session couldn't be resumed"}
BACK = {"start", "wake_resumed", "woken", "heir", "to_heir"}

def stamp(s):
    try: return dt.datetime.fromisoformat(str(s).replace("Z", "+00:00")).timestamp()
    except ValueError: return None

def lines(path):
    try: raw = Path(path).read_text(errors="replace").splitlines()
    except OSError: return
    for l in raw:
        try: e = json.loads(l)
        except ValueError: continue
        if isinstance(e, dict): yield e

def first_words(text, n=200):
    """The start of a post, down to whole sentences of at least 40 characters, so "Done." says what was done."""
    t = re.sub(r"^(@\S+\s+)+|\*\*|__", "", " ".join(FOR_YOU.sub("", text or "").split()))
    m = re.match(r".{40,}?[.!?](?=\s|$)", t)
    t = m.group(0) if m else t
    return t if len(t) <= n else t[:n - 1].rstrip() + "…"

def word(n, one, many=None): return f"{n} {one if n == 1 else many or one + 's'}"

def stops(since):
    """Each agent's last stop in the window: why, and whether it carried on after."""
    last, own = {}, set()
    for e in lines(HOME / "log.jsonl"):
        t, ev, name = stamp(e.get("at")), e.get("event"), e.get("name")
        if ev == "start" and e.get("thread") in SELF_THREADS: own.add(name)
        if not name or name in own or t is None or t < since: continue
        if ev in STOPS or ev in ("died", "stalled"):
            why = WHY_WORDS.get("stalled") if ev == "stalled" else WHY_WORDS.get(e.get("why"), WHY_WORDS["other"]) if ev == "died" else STOPS[ev]
            last[name] = {"name": name, "why": why, "at": t, "carried_on": False, "gave_up": False, "stays": ev in ("stopped_by_hand", "kill_dropped", "wake_failed"), "then": ""}
        elif ev == "given_up" and name in last: last[name]["gave_up"] = True; last[name]["carried_on"] = False
        elif ev in BACK and name in last: last[name]["carried_on"] = True; last[name]["gave_up"] = False
    for x in last.values():
        x["then"] = "carried on by itself" if x["carried_on"] else "gave up after several tries, so it needs a message" if x["gave_up"] else "stays stopped until you write to it" if x.pop("stays") else "will carry on by itself"
    return last

def summary(since, now=None):
    """What /api/away serves and `hive away` prints. The window is since..now, seconds since the epoch."""
    now = now or time.time()
    st = {}
    try: st = json.loads((STATE / "starter.json").read_text())
    except (OSError, ValueError): pass
    running = set()
    for f in (HOME / "run").glob("*.json"):
        try: running.add(json.loads(f.read_text()).get("thread"))
        except (OSError, ValueError): pass
    last, answers, askers = {}, {}, {}    # per thread: an agent's last post; when the person last wrote; each agent's last For you
    names = {}
    for p in lines(HOME / "board.jsonl"):
        th, who, t = p.get("thread"), p.get("from", ""), stamp(p.get("at"))
        if not th or t is None: continue
        if who in PERSON:
            answers[th] = t; askers.pop(th, None); continue
        if not AGENT.match(who) or th in SELF_THREADS: continue
        names[who] = th
        if t < since or "@new" in (p.get("text") or "").split(): continue   # a brief for a fresh agent isn't a result
        last[th] = {"thread": th, "from": who, "at": t, "post": p.get("id"), "text": first_words(p.get("text"))}
        m = FOR_YOU.search(p.get("text") or "")
        if m: askers.setdefault(th, {})[who] = {"kind": "for_you", "thread": th, "from": who, "at": t, "post": p.get("id"), "text": " ".join(m.group(1).split())[:220]}
    stopped = stops(since)
    for s in stopped.values(): s["thread"] = names.get(s["name"])
    still = {s["thread"] for s in stopped.values() if not s["carried_on"]}
    waiting = []
    for login, a in (st.get("auth_told") or {}).items():
        n = sum(r.get("login") == login for r in (st.get("resume") or {}).values()) + len(st.get("pending") or {})
        waiting.append({"kind": "sign_in", "agents": n, "text": f"Sign in again to Claude: {word(n, 'agent')} {'is' if n == 1 else 'are'} waiting."})
    until = [t for t in (st.get("limits") or {}).values() if t > now]
    if until: waiting.append({"kind": "limit", "until": min(until), "text": "Agents are waiting for the usage limit to reset, and carry on by themselves then."})
    for by in askers.values(): waiting += sorted(by.values(), key=lambda x: x["at"], reverse=True)
    delivered = [d for th, d in sorted(last.items(), key=lambda x: -x[1]["at"]) if th not in running and th not in still and th not in askers]
    return {"since": since, "now": now, "delivered": delivered, "stopped": sorted(stopped.values(), key=lambda s: (s["carried_on"], -s["at"])), "waiting": waiting}

def text(s):
    out = [f"While you were away (since {dt.datetime.fromtimestamp(s['since']).astimezone():%a %H:%M}):"]
    if not (s["delivered"] or s["stopped"] or s["waiting"]): return out[0] + "\nNothing happened."
    if s["delivered"]: out += ["", f"Delivered ({len(s['delivered'])}):"] + [f"- {d['thread']}: {d['text']}" for d in s["delivered"]]
    if s["stopped"]:
        out += ["", f"Stopped ({len(s['stopped'])}):"]
        out += [f"- {x['name']}: {x['why']}; {x['then']}." for x in s["stopped"]]
    if s["waiting"]: out += ["", f"Waiting on you ({len(s['waiting'])}):"] + [f"- {w.get('thread') + ': ' if w.get('thread') else ''}{w['text']}" for w in s["waiting"]]
    return "\n".join(out)

def window(s):
    m = re.fullmatch(r"(\d+)([hd])", s or "")
    if not m: sys.exit("away: --since takes hours or days, like 12h or 2d")
    return int(m[1]) * (3600 if m[2] == "h" else 86400)

if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--since", default="12h"); ap.add_argument("--json", action="store_true")
    a = ap.parse_args(); s = summary(time.time() - window(a.since))
    print(json.dumps(s) if a.json else text(s))
