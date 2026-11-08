#!/usr/bin/env python3
"""Live neighbours: threads other than its own that a starting agent may want to ask, borrow from or stand down for (hive-meta
moving.md P3). The word scoring of `kb recall`, over the last 24 h of board posts: a post is a passage, a thread counts by its
best post, so a long thread doesn't win on length. Only the words of the waking post are looked for, in other threads' posts.

  neighbours.py --post ID [--for NAME]   up to 2 lines for the start prompt, or nothing; each pointer is logged like a recall pointer
Stdlib only. The starter runs it on a fresh start with a time limit, and a failure here only means no lines."""
import argparse, datetime as dt, json, math, os, re, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import kb

# A day of board is one conversation, so kb.recall's 12% lets `token` and `plain` through: replayed on 623 starts it fired on 97% of them.
# At 2% of posts, at most 8 threads and 3 words in one post it fires on 25%, keeps gtm-content -> gtm-site and hwde-build-784 -> hwde-hdi-route, and drops the template every keeper round shares.
CAP, WINDOW_H, MIN_WORDS, COMMON, SPREAD, DOC_WORDS = 2, 24, 3, 0.02, 8, 400
NOT_AGENTS = {"person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer", "postland", "leftover-router"}  # as goals.py
SYSTEM_THREADS = {"keeper", "improve", "watchdog", "starter", "kb-dream"}  # as continuation.py: rounds on a timer, no one to ask
BOARD, STATUS = kb.H + "/board.jsonl", kb.H + "/.hive/thread-status.jsonl"

def jsonl(path):
    out = []
    try:
        with open(path, errors="replace") as f:
            for l in f:
                try: out.append(json.loads(l))
                except ValueError: pass
    except OSError: pass
    return out

def stamp(s):
    try: return dt.datetime.fromisoformat(str(s)).timestamp()
    except ValueError: return 0.0

def thread_of(p): return str(p.get("thread") or p.get("id"))

def words_of(text):
    """kb.recall_words without its cap of 40: that cap bounds a query, and here a long report is what's searched."""
    keep, kb.RECALL_WORDS = kb.RECALL_WORDS, DOC_WORDS
    try: return set(kb.recall_words(text))
    finally: kb.RECALL_WORDS = keep

def score(text, own, board, t):
    """[(thread, score, shared words)] best first: threads not in `own` with a post of the last 24 h before t that holds at least
    MIN_WORDS of the message's uncommon words. Posts after t don't exist yet (a replay gives t)."""
    since = t - WINDOW_H * 3600; docs = []  # (a word in more threads than SPREAD can't pick out 3 of them: it is a template's or the day's word)
    for p in board:
        if str(p.get("from", "")).lower() in NOT_AGENTS or not since <= (at := stamp(p.get("at"))) <= t: continue
        docs.append((thread_of(p), at, words_of(str(p.get("text") or ""))))
    df, spread = {}, {}
    for th, _, ws in docs:
        for w in ws: df[w] = df.get(w, 0) + 1; spread.setdefault(w, set()).add(th)
    n = len(docs)
    idf = {w: math.log(1 + n / (1 + df[w])) for w in kb.recall_words(text) if w in df and df[w] <= max(2, n * COMMON) and len(spread[w]) <= SPREAD}
    best = {}
    for th, at, ws in docs:
        if th in own or th in SYSTEM_THREADS: continue
        shared = idf.keys() & ws
        if len(shared) < MIN_WORDS: continue
        s = sum(idf[w] for w in shared)
        if th not in best or s > best[th][0]: best[th] = (s, sorted(shared, key=lambda w: -idf[w]))
    return [(th, s, ws) for th, (s, ws) in sorted(best.items(), key=lambda x: (-x[1][0], x[0]))]

def last_agent(thread, board, t):
    """Who moved the thread last and when: its newest post by an agent, or by anyone but the machine if no agent has posted."""
    posts = [p for p in board if thread_of(p) == thread and stamp(p.get("at")) <= t and str(p.get("from", "")).lower() not in NOT_AGENTS]
    agents = [p for p in posts if str(p.get("from", "")).lower() != "person"]
    p = (agents or posts)[-1]
    return p.get("from"), stamp(p.get("at"))

def status_of(thread):
    """The first line of the thread's newest status bar."""
    text = next((str(r.get("text") or "") for r in reversed(jsonl(STATUS)) if r.get("thread") == thread), "")
    return next((l.strip() for l in text.split("\n") if l.strip()), "")

def lines_for(hits, board, t):
    out = []
    for th, _, ws in hits:
        who, at = last_agent(th, board, t); st = status_of(th)
        out.append(f"- {th}: {who}, {max(0, int((t - at) // 60))} min ago, shares {', '.join(ws[:3])}" + (f": {st[:140]}" if st else ""))
    return out

def main():
    ap = argparse.ArgumentParser(prog="neighbours"); ap.add_argument("--post", action="append", required=True); ap.add_argument("--for", dest="who"); a = ap.parse_args()
    t0 = time.time(); board = jsonl(BOARD); ids = set(a.post)
    mine = [p for p in board if p.get("id") in ids]
    if not mine: return
    hits = score("\n".join(str(p.get("text") or "") for p in mine), {thread_of(p) for p in mine}, board, t0)[:CAP]
    if not hits: return  # like a recall that finds nothing, it leaves no pointer to count
    print("Live threads that share words with this (matched on words, not checked; `hive read --thread T` reads one; ask, borrow or stand down, and the minutes show whether it has stalled):")
    print("\n".join(lines_for(hits, board, t0)))
    if a.who: os.environ["HIVE_NAME"] = a.who
    # the shape of a recall pointer, marked, so kbuse follows it the same way and counts it apart from the notes
    rec = {"recall": a.post, "neighbours": True, "top": ["thread:" + h[0] for h in hits], "ms": int((time.time() - t0) * 1000)}
    if kb.kbuse:
        rec.update(id=kb.kbuse.new_id(), shown=[["thread:" + h[0], 0] for h in hits])
        if a.who: kb.kbuse.remember(a.who, kb.kbuse.load(a.who), rec["id"], "recall", rec["shown"], calls=0)
    kb.log(rec)

if __name__ == "__main__": main()
