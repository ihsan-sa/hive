#!/usr/bin/env python3
"""The tidy audit (the person, 2026-11-08, post 110815388fca): every tidy_every_s the starter runs `tidy.py due --mark`, which costs
no model use. It lists what the agents should have done at their own hand-offs and didn't: a conversation in no focus, one still
titled by its board id, a focus whose summary is older than a result posted in it. Only when it finds something does the starter
start one agent on ~/core/tidy.md, which fixes the misses and, above all, finds out from the responsible agent's run log why it
didn't do it, so the mechanism gets fixed. The hive is time invariant when the passes find nothing: `tidy.py stats` is that measure.

  tidy.py due [--mark] [--json]   what was missed since the last mark (a Hive new to this: the whole board, each pass until one
                                  finds nothing or 2 days pass, so all its earlier work gets sorted)
  tidy.py due --all               what is still unsorted on the whole board, without marking
  tidy.py stats [DAYS]            misses per pass, by kind, oldest first
Records: ~/.hive/tidy.json {at: last mark}, ~/.hive/tidy.jsonl {at, since, n, file, title, summary} per pass."""
import argparse, glob, json, os, sys, time
from pathlib import Path
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import focus as fc

HOME = Path.home(); STATE = HOME / ".hive"; MARK, LOG = STATE / "tidy.json", STATE / "tidy.jsonl"
GRACE_S = 900          # an agent still finishing its run gets its own hand-off first
CATCHUP_S = 2 * 86400   # the whole-board passes of a Hive new to this stop after 2 days, sorted or not

AGENT = __import__("re").compile(r"a[0-9a-f]{4}")
def is_agent(who): return bool(AGENT.fullmatch(str(who or "").lower()))

def runlog(name):
    """Where to read why: the agent's newest Claude transcript (from ~/run/NAME.json) and its newest session output file."""
    out = {}
    try:
        r = json.loads((HOME / "run" / f"{name}.json").read_text())
        hits = glob.glob(os.path.expanduser(r.get("login") or "~/.claude") + f"/projects/*/{r.get('session')}.jsonl")
        if hits: out["transcript"] = hits[0].replace(str(HOME) + "/", "~/")
    except (OSError, ValueError): pass
    s = sorted(glob.glob(str(HOME / "sessions" / f"{name}-*.jsonl")))
    if s: out["session"] = s[-1].replace(str(HOME) + "/", "~/")
    return out

def asked():
    try: return {(r.get("thread"), r.get("name")) for r in map(json.loads, filter(str.strip, fc.FILE_ASKED.read_text().splitlines()))}
    except (OSError, ValueError): return set()

LEAD = __import__("re").compile(r"^\s*(?:@\S+\s+)*(?:\([^)\n]*\)\s*)?")
def quiet(text):
    """A title quoted in the starter's @new brief: drop the leading @names and "(from ...)" and defuse any other @, so quoting a
    thread does not wake its agents again (the first pass, post 110816144744, woke two more agents and rootwarden that way)."""
    return LEAD.sub("", text).replace("@", "\uff20")

def due(since, t=None, posts=None):
    """[{kind, thread|focus, what, agents: [{name, asked, ...runlog}]}] missed since `since`, settled for GRACE_S."""
    t = t or time.time(); st = fc.state(); posts = fc.board_posts() if posts is None else posts
    tagged = set()
    try: tagged = {r.get("thread") for r in map(json.loads, filter(str.strip, fc.FILE.read_text().splitlines())) if r.get("kind") == "tag"}
    except (OSError, ValueError): pass
    by = {}
    for p in posts: by.setdefault(p["thread"], []).append(p)
    ask, out = asked(), []
    def who(th, kind):
        names = []
        for p in sorted(by.get(th, []), key=lambda p: fc.ts(p.get("at"))):
            n = str(p.get("from", "")).lower()
            if is_agent(n) and fc.ts(p.get("at")) >= since and n not in names: names.append(n)
        return [{"name": n, "asked": (th, n) in ask, **runlog(n)} for n in names[-3:]]
    for th, ps in by.items():
        last = max(fc.ts(p.get("at")) for p in ps)
        if last < since or t - last < GRACE_S: continue
        if not any(is_agent(p.get("from")) and fc.ts(p.get("at")) >= since for p in ps): continue
        first = min(ps, key=lambda p: fc.ts(p.get("at"))); title = quiet((str(first.get("text", "")).strip().splitlines() or [""])[0])[:70]
        if th not in tagged and fc.focus_of(st, th, posts) is None:
            out.append({"kind": "file", "thread": th, "what": f"in no focus: {title}", "agents": who(th, "file")})
        if not fc._titled(th):
            out.append({"kind": "title", "thread": th, "what": f"no title, shows: {title}", "agents": who(th, "title")})
    for f in st["foci"]:
        ths = {th for th, fid in st["tags"].items() if fid == f["id"]} | {x for x in (f.get("thread"), f["id"]) if x}
        rt, p = fc.newest_result(ths, posts)
        s = f.get("summary"); sat = float(s.get("at") or 0) if s else 0
        if p and rt >= since and t - rt >= GRACE_S and rt > sat:
            n = str(p.get("from", "")).lower()
            out.append({"kind": "summary", "focus": f["id"], "thread": p.get("thread"), "what": f"summary of {f['name']} is older than {n}'s result {p.get('id')}",
                        "agents": [{"name": n, "asked": None, **runlog(n)}]})
    return out

def fmt(items):
    if not items: return "nothing missed"
    rows = []
    for i in items:
        ag = "; ".join(f"{a['name']} ({'asked at Stop' if a['asked'] else 'never asked' if a['asked'] is False else 'nudged at Stop'}"
                       + "".join(f", {k} {v}" for k, v in a.items() if k in ("transcript", "session")) + ")" for a in i["agents"]) or "no agent"
        rows.append(f"- {i['kind']}: {i.get('focus') or ''}{' ' if i.get('focus') else ''}#{i['thread']}: {i['what']}\n    who: {ag}")
    return "\n".join(rows)

def main(argv=None):
    ap = argparse.ArgumentParser(prog="tidy.py", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("due"); p.add_argument("--mark", action="store_true"); p.add_argument("--json", action="store_true")
    p.add_argument("--all", action="store_true", help="the whole board, whatever the mark: what is still unsorted (a long first list is cut off in the post)")
    p = sp.add_parser("stats"); p.add_argument("days", nargs="?", type=float, default=7)
    a = ap.parse_args(argv); t = time.time()
    if a.cmd == "due":
        # The first pass, as on a Hive that just updated to one with this, looks at the whole board, and so does every pass after it
        # until one finds nothing (at most CATCHUP_S): a Hive's earlier work all gets sorted into foci (the person, 2026-11-08, post 11081821428e).
        try: m = json.loads(MARK.read_text()); since = float(m["at"]) - GRACE_S
        except (OSError, ValueError, KeyError): m = {"catchup": t}; since = 0
        first = bool(m.get("catchup")) and t - m["catchup"] < CATCHUP_S
        if first or a.all: since = 0
        items = due(since, t)
        if a.mark:
            STATE.mkdir(parents=True, exist_ok=True)
            MARK.write_text(json.dumps({"at": t, **({"catchup": m["catchup"]} if first and items else {})}))
            with LOG.open("a") as f:
                f.write(json.dumps({"at": t, "since": since, "n": len(items), **{k: sum(i["kind"] == k for i in items) for k in ("file", "title", "summary")}}) + "\n")
        print(json.dumps(items) if a.json else ("Catch-up pass: this Hive is new to sorting, so this is everything on the board still unsorted, not just since the last pass. Follow the catch-up note in tidy.md.\n" if first and items and not a.all else "") + fmt(items))
        return 0 if items else 1
    try: rows = [json.loads(l) for l in LOG.read_text().splitlines() if l.strip()]
    except (OSError, ValueError): rows = []
    rows = [r for r in rows if r["at"] >= t - a.days * 86400]
    for r in rows: print(time.strftime("%m-%d %H:%M", time.gmtime(r["at"])), f"{r['n']:3d}  file {r['file']}  title {r['title']}  summary {r['summary']}")
    clean = sum(1 for r in rows if not r["n"])
    print(f"{len(rows)} passes, {clean} found nothing" + (f", last {rows[-1]['n']} misses" if rows else ""))
    return 0

if __name__ == "__main__":
    try: sys.exit(main())
    except Exception as e: print(f"tidy.py: {type(e).__name__}: {e}", file=sys.stderr); sys.exit(3)   # 1 means nothing missed: never on a crash
