#!/usr/bin/env python3
"""What still waits on the person, put to an agent at the moment it can answer: at Stop in a thread it touched (inbox.owns_open), on writing a second wait in a thread (`hive post`, `hive asks person`), and on closing a thread's last one under a bar that still says it waits.
Warns and shows, never refuses; each reminder at most once per run, one line per item. The causes and counts are in projects/rsi-lab/simuse/foryou-pass.md.

`waits.py answered | threads-pct | decidable` prints the numbers `hive improve log --check` reads."""
import json, os, sys, time
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))   # asks.py runs with -I, which leaves this folder off the path
import away, asks, foryou

MAX_ROWS = 8
MAX_SHOWS = 2   # to one agent: at first, and once more after the person next writes there; in a thread they use daily, more would block every run over an old line

def agent():
    """The running agent's name, or None for a person, a door or a free agent (kept light, as hive's remind_asks)."""
    name = os.environ.get("HIVE_NAME", "")
    return name if away.AGENT.match(name) and not name.startswith("f") and not os.environ.get("HIVE_DOOR") else None

def once(kind):
    """True the first time this run asks for this reminder, and marks it. No run record means no run, so no reminder."""
    name = agent()
    if not name: return False
    try: at = json.loads((away.HOME / "run" / f"{name}.json").read_text()).get("at")
    except (OSError, ValueError): return False
    if not at: return False
    mark = away.STATE / "nudged-waits" / f"{name}.{kind}"
    try:
        if mark.read_text() == at: return False
    except OSError: pass
    mark.parent.mkdir(parents=True, exist_ok=True); mark.write_text(at)
    return True

def short(s, n=100): return " ".join(str(s or "").split())[:n]

def rows(o):
    """One dict per open item in an open_on_person answer. A For you tied to an open ask is that ask's line, so the ask alone stands for both."""
    out = [{"id": e["id"], "kind": "ask", "thread": e.get("thread") or e.get("post") or "asks", "at": away.stamp(e.get("at")), "text": e.get("ask")} for e in o["asks"]]
    ids = {r["id"] for r in out}
    return out + [{"id": str(r["post"]), "kind": "for_you", "thread": r["thread"], "at": r["at"], "text": r["text"]} for r in o["for_yous"] if not ids & set(r.get("asks") or [])]

def label(r): return r["id"] if r["kind"] == "ask" else f"For you {r['id']}"

def in_thread(thread, skip_post=None, skip_ask=None):
    """What already waits on the person in this thread, leaving out the item just written: with it on the board, it would replace the older line it restates."""
    posts = [p for p in away.lines(away.HOME / "board.jsonl") if str(p.get("id")) != str(skip_post)]
    items = {i: e for i, e in asks.ledger().items() if i != skip_ask}
    return [r for r in rows(away.open_on_person(posts=posts, items=items)) if r["thread"] == thread]

def same_wait(thread, undo, skip_post=None, skip_ask=None):
    open_here = in_thread(thread, skip_post, skip_ask)
    if not open_here or not once("same-wait"): return None
    said = "\n".join(f"{label(r)}: {short(r['text'])}" for r in open_here[:MAX_ROWS])
    return f"This thread already has open on the person:\n{said}\nIs yours the same wait? Then point to that one instead of opening another: {undo}"

def on_for_you(text, thread, pid):
    """After `hive post` with a For you line, in a thread that already has one open."""
    if not [l for l in foryou.lines(text) if not foryou.NOTHING.match(l)]: return None
    return same_wait(thread, f'`hive foryou close {pid} --why "same wait as ID"`, naming the open one.', skip_post=pid)

def on_person(id_, why):
    """After `hive asks person`: the six-way test on the reason, and any wait already open in the thread."""
    e = asks.ledger().get(id_) or {}
    notes = [foryou.ask_advice(id_)] if foryou.decidable(why) and once("decide") else []
    if same := same_wait(e.get("thread") or e.get("post") or "asks", f'`hive asks drop {id_} --why "same wait as ID"`, naming the open one.', skip_ask=id_): notes.append(same)
    return "\n".join(notes) or None

def open_threads():
    """The threads with something open on the person now: taken before a close, for bar_note."""
    return set(away.open_on_person()["threads"])

def bar_reason(text):
    """The reason word a bar ends on, as away.bar_done reads it."""
    for line in reversed(str(text or "").splitlines()):
        if m := away.REASON.match(line.strip()): return m[1]
    return None

def bar_note(before):
    """After a close: the threads it emptied whose bar still ends `Needs the person:`."""
    if not before: return None
    bars = {e["thread"]: str(e.get("text") or "") for e in away.lines(away.STATE / "thread-status.jsonl") if e.get("thread")}
    gone = [t for t in sorted(before - open_threads()) if bar_reason(bars.get(t)) == "Needs the person"]
    if not gone or not once("bar"): return None
    return "Nothing is open on the person in these threads now, but the bar still ends `Needs the person:`. If the wait is over, write the bar again with `hive status THREAD`:\n" + "\n".join(gone[:MAX_ROWS])

def person_posts(posts):
    """{thread: [(epoch, post id)]} of what the person wrote."""
    heard = {}
    for p in posts:
        if str(p.get("from", "")).lower() in away.PERSON and (t := away.stamp(p.get("at"))): heard.setdefault(p.get("thread"), []).append((t, p.get("id")))
    return heard

def at_stop(name, at, thread, posts, items, seen, now=None):
    """Items to put to a run as it stops: older than the run, and in a thread it posted in, or in the thread it woke in if the person has written there since the item opened. An item comes once per agent, and one time more if the person writes in its thread after that: replaying 500 stops, showing every time blocked 32 and once-unless-they-wrote 21. Updates seen, {item: [time of the person's latest post in its thread when shown, times shown]}."""
    t0, now = away.stamp(at), now or time.time()
    posted = {p.get("thread") for p in posts if p.get("from") == name and (away.stamp(p.get("at")) or 0) >= t0}
    heard, out = person_posts(posts), []
    if not posted and thread not in heard: return out
    live = rows(away.open_on_person(now=now, posts=posts, items=items))
    for gone in set(seen) - {r["id"] for r in live}: del seen[gone]
    for r in live:
        if r["at"] is None or r["at"] >= t0: continue
        said = heard.get(r["thread"], [])
        after = [x for x in said if x[0] > r["at"]]
        if r["thread"] not in posted and not (r["thread"] == thread and after): continue
        last, shows = seen.get(r["id"], (0, 0))
        if shows >= MAX_SHOWS or (shows and not any(x[0] > last for x in said)): continue
        seen[r["id"]] = [max((x[0] for x in said), default=0), shows + 1]
        out.append({**r, "heard": after[-1][1] if after else None})
    return out

def stop_text(items):
    said = "\n".join(f"{label(r)} in {r['thread']}: {short(r['text'])}" + (f" (the person posted {r['heard']} after it)" if r["heard"] else "") for r in items[:MAX_ROWS])
    return ("These wait on the person in threads this run touched. Each one: if it still waits, leave it. If a post settled it, close it with that post: "
            '`hive foryou close POST --why "settled by POSTID"` or `hive asks done ID --evidence POSTID`:\n' + said)

def answered(posts=None, items=None):
    """Open items whose thread got a person post and then an agent post, with the item still open: the week check for the wait that ended."""
    posts = list(away.lines(away.HOME / "board.jsonl")) if posts is None else posts
    heard, n = person_posts(posts), 0
    agents = {}
    for p in posts:
        if away.AGENT.match(str(p.get("from", ""))) and (t := away.stamp(p.get("at"))): agents[p.get("thread")] = max(agents.get(p.get("thread"), 0), t)
    for r in rows(away.open_on_person(posts=posts, items=asks.ledger() if items is None else items)):
        after = [t for t, _ in heard.get(r["thread"], []) if r["at"] and t > r["at"]]
        n += bool(after and agents.get(r["thread"], 0) > min(after))
    return n

def threads_pct():
    """Open threads per 100 open rows: 100 means no thread carries a wait twice."""
    o = away.open_on_person(); n = len(o["asks"]) + len(o["for_yous"])
    return round(100 * len(o["threads"]) / n, 1) if n else 100.0

def decidable():
    """Asks waiting on the person whose reason names none of the six."""
    return sum(foryou.decidable(e.get("why")) for e in asks.ledger().values() if e.get("status") == "person" and e.get("kind") != "direction")

if __name__ == "__main__":
    do = {"answered": answered, "threads-pct": threads_pct, "decidable": decidable}.get((sys.argv[1:] or [""])[0])
    if not do: sys.exit("usage: waits.py answered | threads-pct | decidable")
    print(do())
