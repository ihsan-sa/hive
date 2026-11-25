#!/usr/bin/env python3
"""What still waits on the person, put to an agent at the moment it can answer: at Stop in a thread it touched (inbox.owns_open), on writing a second wait in a thread (`hive post`, `hive asks person`), and on closing a thread's last one under a bar that still says it waits.
Warns and shows, never refuses; each reminder at most once per run, one line per item. The causes and counts are in projects/rsi-lab/simuse/foryou-pass.md.

`waits.py answered | threads-pct | decidable | repeats` prints the numbers `hive improve log --check` reads."""
import json, os, re, sys, time
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


STOP = set("a an and are as at be but by can do for from has have i if in is it its it's me my no not of on or our so than that the their them then there this to up us was we what when which will with would you your yours yes say said just one".split())
ROUTE = set("warden slack sudo word send sent line box post thread reply type paste message board".split())  # how to send, not what: 3 of 11 replay hits matched only on these (aa008, 2026-11-25)
WINDOW = 14 * 86400

def words(line):
    """The content words of a For you line: lower case, 3 letters or more, common words and route words left out, code spans removed."""
    line = foryou.SPAN.sub(" ", str(line))   # a command or a quoted word is the route, not the thing asked
    return {w for w in (x.strip(".,;:!?()[]{}'\"`*_") for x in line.lower().split()) if len(w) >= 3 and w not in STOP and w not in ROUTE}

def answers(p, post_id, author):
    """The person's post p answers For you post_id from author: it replies to that post, or names the author (@author in the text, or in to)."""
    to = p.get("to") if isinstance(p.get("to"), list) else [p.get("to") or ""]
    return str(p.get("reply_to") or "") == post_id or bool(re.search(rf"@{re.escape(author)}\b", str(p.get("text") or "").lower())) or author in {str(x).lower().lstrip("@") for x in to}

def alike(a, b):
    """Two For you lines ask the same thing: at least 4 content words shared, and 60% of the shorter line's. Replaying 14 days of the board, this caught the sign-in and H4-yes repeats."""
    common = a & b
    return len(common) >= 4 and len(common) >= 0.6 * min(len(a), len(b))

def repeats(text, thread, pid, posts, open_ids, now=None):
    """For you lines in a new post that restate one from the last 14 days which is still open in another thread, or which the person has answered since (as away.for_yous: a reply to it, or a post in its thread naming its author; another person post there isn't an answer). [(kind, earlier post, its thread, the person's post or None)], one per earlier post. The same thread's open line is same_wait's."""
    now = now or time.time()
    new = [words(l) for l in foryou.lines(text) if not foryou.NOTHING.match(l)]
    new = [w for w in new if w]
    if not new: return []
    posts = list(posts)
    out, said = [], {}
    for p in posts:
        if str(p.get("from", "")).lower() in away.PERSON and (t := away.stamp(p.get("at"))): said.setdefault(p.get("thread"), []).append((t, p.get("id"), p))
    for p in posts:
        if str(p.get("id")) == str(pid) or str(p.get("from", "")).lower() in away.PERSON: continue
        at = away.stamp(p.get("at"))
        if not at or at < now - WINDOW or not foryou.FOR_YOU.search(str(p.get("text") or "")): continue
        if not any(alike(n, words(l)) for l in foryou.lines(p.get("text")) for n in new): continue
        t, i = p.get("thread"), str(p.get("id"))
        if i in open_ids:
            if t != thread: out.append(("open", i, t, None))
        elif after := [x for x in said.get(t, []) if x[0] > at and answers(x[2], i, str(p.get("from", "")).lower())]:
            out.append(("answered", i, t, after[0][1]))
    return out

def on_repeat(text, thread, pid):
    """After `hive post` with a For you line: a line asked before and still open elsewhere, or already answered."""
    if not foryou.FOR_YOU.search(str(text or "")): return None
    posts = list(away.lines(away.HOME / "board.jsonl"))
    open_ids = {str(r["post"]) for r in away.open_on_person(posts=posts)["for_yous"]}
    found = repeats(text, thread, pid, posts, open_ids)
    if not found or not once("repeat"): return None
    said = []
    for kind, i, t, heard in found[:MAX_ROWS]:
        if kind == "open": said.append(f"For you {i} in thread {t} already asks this and is still open.")
        else: said.append(f"For you {i} in thread {t} asked this before, and the person wrote there after it ({heard}).")
    return ("Your For you line repeats an earlier one:\n" + "\n".join(said) +
            f"\nIf it is the same wait, close yours (`hive foryou close {pid} --why \"same wait as POST\"`) or point to the open one. If they answered, act on their answer; ask again only if something changed, and say what.")

def repeat_count(days=7, posts=None, now=None):
    """Agent posts in the last `days` days whose For you line restated an earlier one (open now in another thread, or answered by then): the week check for on_repeat. 14 days before it landed (2026-11-25): 11 of 121."""
    now = now or time.time()
    posts = list(away.lines(away.HOME / "board.jsonl")) if posts is None else posts
    open_ids = {str(r["post"]) for r in away.open_on_person(posts=posts)["for_yous"]}
    n = 0
    for k, p in enumerate(posts):
        at = away.stamp(p.get("at"))
        if not at or at < now - days * 86400 or not away.AGENT.match(str(p.get("from", ""))): continue
        n += bool(repeats(p.get("text"), p.get("thread"), p.get("id"), posts[:k], open_ids, now=at))
    return n

if __name__ == "__main__":
    do = {"answered": answered, "threads-pct": threads_pct, "decidable": decidable, "repeats": repeat_count}.get((sys.argv[1:] or [""])[0])
    if not do: sys.exit("usage: waits.py answered | threads-pct | decidable | repeats")
    print(do())
