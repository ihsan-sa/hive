#!/usr/bin/env python3
"""Agentic thread search for the dash side list: `@some description` asks Haiku which threads fit.
find(q) -> {"threads": [{"thread", "why"}]}. A query with tag:x words (tag:focus:site, or tag:site under any kind) is matched exactly against posts' tags, with no model call. Reads the board and the threads' status lines, asks one Haiku call, caches by question for 10 min.
`thread_find.py QUESTION` prints the answer."""
import importlib.util, json, os, sys, time
from pathlib import Path
HOME = Path(os.environ.get("HIVE_HOME") or Path.home()); STATE = HOME / ".hive"
MODEL, MAX_THREADS, TTL = "haiku", 220, 600
_cache = {}

def _jl(path):
    out = []
    try: lines = Path(path).read_text().splitlines()
    except OSError: return out
    for ln in lines:
        try: out.append(json.loads(ln))
        except ValueError: pass
    return out

def _core(name):
    spec = importlib.util.spec_from_file_location(f"h2_{name}", HOME / "core" / f"{name}.py"); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m

def index():
    """One block per thread: name, status, first and last posts, newest threads first."""
    by = {}
    for p in _jl(HOME / "board.jsonl"):
        if p.get("thread") and p["thread"] != "side": by.setdefault(p["thread"], []).append(p)
    status = {}
    for r in _jl(STATE / "thread-status.jsonl"):
        if r.get("thread") and r.get("text"): status[r["thread"]] = " / ".join(str(r["text"]).split("\n"))
    flat = lambda p, n: " ".join(str(p.get("text", "")).split())[:n]
    rows = sorted(by.items(), key=lambda kv: kv[1][-1].get("at", ""), reverse=True)[:MAX_THREADS]
    tags = lambda ps: (lambda x: f"\n  tags: {x[:100]}" if x else "")(" ".join(dict.fromkeys(t for p in ps for t in p.get("tags") or [] if not t.startswith("agent:"))))
    return {t: f"[{t}] {status.get(t, '')[:240]}{tags(ps)}\n  first: {flat(ps[0], 160)}\n  last: {flat(ps[-1], 120)}" for t, ps in rows}

def tag_hit(want, tags):
    """want is kind:id (that tag exactly) or id (that id under any kind); as hive search tag: does."""
    want = want.lower(); tags = [str(t).lower() for t in tags or []]
    return want in tags if ":" in want else any(t.split(":", 1)[-1] == want for t in tags)

def tag_words(q):
    """The tag: words of a query (without tag:) and the rest of its words."""
    ws = str(q).split()
    return [w[4:] for w in ws if w.lower().startswith("tag:") and len(w) > 4], [w for w in ws if not (w.lower().startswith("tag:") and len(w) > 4)]

def tagged(q):
    """Posts carrying every tag: word of q and holding the rest of its words, newest first. None when q has no tag: word."""
    want, rest = tag_words(q)
    if not want: return None
    rest = [w.lower() for w in rest]
    return [p for p in reversed(_jl(HOME / "board.jsonl")) if p.get("thread") and p["thread"] != "side" and all(tag_hit(w, p.get("tags")) for w in want)
            and all(w in str(p.get("text", "")).lower() for w in rest)]

def find(q, ask=None):
    q = " ".join(str(q).split())[:300]
    if not q: return {"threads": []}
    hits = tagged(q)
    if hits is not None:   # tags are exact: no model call, every thread that has one, newest first
        by = {}
        for p in hits: by.setdefault(p["thread"], []).append(p)
        return {"threads": [{"thread": t, "why": f"{len(ps)} {'post' if len(ps) == 1 else 'posts'} tagged {' '.join(tag_words(q)[0])}"} for t, ps in by.items()][:40]}
    hit = _cache.get(q)
    if hit and time.time() - hit[0] < TTL: return hit[1]
    idx = index(); ask = ask or _core("asks")
    prompt = ("You search the conversation threads of a team of AI agents for one person. They described what they are looking for. "
              "Pick the threads that fit, best first, at most 8; pick none if nothing fits. Judge by meaning, not just matching words. Use only thread ids from the list.\n"
              f"Their description: {q}\n"
              'Reply with only JSON: {"threads": [{"thread": "id", "why": "under 12 words, plain, why it fits"}]}\n\nThreads:\n' + "\n".join(idx.values()))
    res = ask.parse_json(ask.ask_model(prompt, MODEL, timeout=90)[0])
    out = {"threads": [{"thread": v["thread"], "why": " ".join(str(v.get("why") or "").split())[:120]} for v in res.get("threads", []) if isinstance(v, dict) and v.get("thread") in idx][:8]}
    _cache[q] = (time.time(), out)
    if len(_cache) > 50: _cache.pop(next(iter(_cache)))
    return out

if __name__ == "__main__": print(json.dumps(find(" ".join(sys.argv[1:])), indent=1))
