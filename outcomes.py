#!/usr/bin/env python3
"""hive outcomes: did each run do what the post that woke it asked? The per-session outcome signal of the hive.

Every run the starter logs (~/log.jsonl start/end) gets a grade in ~/outcomes.jsonl:
  silent     the post named the agent and it posted nothing anywhere during the run          (no model; the bad kind of quiet)
  quiet      a thread wake that did not name the agent, and it posted nothing                 (no model; often fine)
  delivered  its posts show the ask done, with how it was checked                            (a Sonnet call, 2 to 3 cents a batch)
  partial    some of it, or a handover with a plan
  nothing    it posted, but no delivery and no handover: status only, questions back, the wrong thing
  not_asked  the post asked this agent nothing concrete
Every run woken by the person is graded; of the rest, a sample per hour (SAMPLE), so the model cost stays near a dollar a day.
`hive progress` shows outcome_delivered_pct and outcomes_silent; the rsi loop and the keeper read them.

  hive outcomes grade [--limit N] [--sample N] [--model sonnet] [--dry]   grade runs ended since the last run (hive-outcomes.timer, hourly)
  hive outcomes list [--since 24h] [--grade G] [--name NAME]
  hive outcomes stats [--since 24h] [--json]
"""
import argparse, datetime as dt, importlib.util, json, os, random, re, sys
from pathlib import Path

HOME = Path.home()
CORE, STATE = HOME / "core", HOME / ".hive"
LEDGER, LOG, BOARD = HOME / "outcomes.jsonl", HOME / "log.jsonl", HOME / "board.jsonl"
CURSOR = STATE / "outcomes-cursor.json"
SAMPLE = 4                 # agent-woken runs with posts graded by the model per hour, on top of every person-woken run
SETTLE_S = 180             # a run's end must be this old: its last post may still be landing
TAIL_S = 90                # posts this long after the end still count as the run's
BAD = ("silent", "nothing")
GOOD = ("delivered",)
GRADED = ("delivered", "partial", "nothing", "silent")   # the denominator of outcome_delivered_pct; quiet and not_asked are neither

def now(): return dt.datetime.now(dt.timezone.utc)
def iso(t=None): return (t or now()).isoformat(timespec="seconds")
def span(s):
    m = re.fullmatch(r"(\d+)([hdm])", s.strip())
    if not m: raise SystemExit(f"bad span {s!r}: use 6h, 2d or 30m")
    return dt.timedelta(**{{"h": "hours", "d": "days", "m": "minutes"}[m.group(2)]: int(m.group(1))})

def jsonl(path):
    out = []
    try:
        for line in Path(path).read_text().splitlines():
            try: out.append(json.loads(line))
            except ValueError: pass
    except OSError: pass
    return out

def append(path, rec):
    with open(path, "a") as f: f.write(json.dumps(rec, ensure_ascii=False) + "\n")

def asks_mod():
    spec = importlib.util.spec_from_file_location("asks", CORE / "asks.py"); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m

def runs(log, since="", until=""):
    """Ended runs with their start record, keyed by session+pid, ended after `since` and before `until`."""
    starts = {(r.get("session"), r.get("pid")): r for r in log if r.get("event") == "start"}
    out = []
    for r in log:
        if r.get("event") != "end" or r.get("at", "") <= since or (until and r.get("at", "") > until): continue
        s = starts.get((r.get("session"), r.get("pid")))
        if not s or not s.get("post"): continue
        # a parked run restarted for new posts keeps the post it was first started on; "woke" lists the ones that actually woke it
        out.append({"key": f"{s.get('session', '')[:8]}-{s.get('pid')}", "name": s.get("name"), "session": s.get("session"), "post": (s.get("woke") or [s.get("post")])[0],
                    "also": (s.get("woke") or [])[1:],
                    "thread": s.get("thread"), "from": s.get("from"), "model": s.get("model"), "started": s.get("at"), "ended": r.get("at"), "exit": r.get("exit")})
    return out

def posts_of(run, posts, by_id):
    """The waking post, the agent's posts during the run (plus TAIL_S), and what others said in the thread after."""
    t0, t1 = run["started"], (dt.datetime.fromisoformat(run["ended"]) + dt.timedelta(seconds=TAIL_S)).isoformat(timespec="seconds")
    mine = [p for p in posts if p.get("from") == run["name"] and t0 <= p.get("at", "") <= t1]
    after = [p for p in posts if p.get("thread") == run["thread"] and p.get("from") != run["name"] and p.get("at", "") > run["ended"]][:2]
    post = by_id.get(run["post"])
    if post and run.get("also") is not None and (run.get("from"), run.get("thread")) != (post.get("from"), post.get("thread")):
        run["from"], run["thread"] = post.get("from"), post.get("thread")  # a restarted run answers to whoever woke it, in that thread
        after = [p for p in posts if p.get("thread") == run["thread"] and p.get("from") != run["name"] and p.get("at", "") > run["ended"]][:2]
    if post and run.get("also"):  # the other posts that woke it, shown with the first so the grade covers them all
        post = {**post, "text": "\n".join([str(post.get("text", ""))] + [f"(also, {by_id[i].get('from')}): {by_id[i].get('text', '')}" for i in run["also"] if i in by_id])}
    return post, mine, after

FRESH = re.compile(r"@(new|claude|opus|sonnet|haiku|fable|astra|codex|free)\b")

def named(run, post):
    """Did the post address this run? By name, by a fresh-agent word (the run is the agent it started), or from the person (whose posts always do)."""
    text = str((post or {}).get("text", ""))
    return (post or {}).get("to") == run["name"] or f"@{run['name']}" in text or bool(FRESH.search(text)) or str((post or {}).get("from", "")).lower() == "person"

def mechanical(run, post, mine):
    if mine: return None
    return "silent" if named(run, post) else "quiet"

def clip(s, n): s = str(s or "").strip(); return s if len(s) <= n else s[:n] + " …"

def block(run, post, mine, after):
    lines = [f"RUN {run['key']}: agent {run['name']}, woken by {run['from']} in thread {run['thread']}, ran {run['started'][11:16]} to {run['ended'][11:16]} UTC, exit {run['exit']}",
             f"  WAKING POST ({post.get('from') if post else '?'}): {clip((post or {}).get('text'), 1500)}"]
    for p in mine[:6]: lines.append(f"  AGENT POSTED (thread {p.get('thread')}): {clip(p.get('text'), 900)}")
    if len(mine) > 6: lines.append(f"  ... and {len(mine) - 6} more posts")
    for p in after: lines.append(f"  AFTER, {p.get('from')} said: {clip(p.get('text'), 400)}")
    return "\n".join(lines)

PROMPT = """You grade whether a hive agent's run did what the post that woke it asked. The hive is a group of coding agents sharing a message board; each run is one agent woken by one post. You see the waking post, what the agent posted during the run, and what others said after.

Grades:
  delivered  its posts show the ask done, with how it was checked (a result, not a promise)
  partial    some of it done, or started and handed over with a clear plan and owner
  nothing    it posted, but no delivery and no handover: status only, questions back, or the wrong thing
  not_asked  the post asked this agent nothing concrete (a thread update, an acknowledgement, a hand-off to someone else)

Judge only against what the post asked, not whether the work was good. A post that says "stop" or "thanks" and a short reply is not_asked. Reply with JSON only:
{"grades":[{"run":"<RUN key>","grade":"delivered|partial|nothing|not_asked","why":"<at most 20 words>"}]}

{blocks}"""

def pick(todo, sample, rng=random):
    """Every person-woken run, plus `sample` others with posts."""
    run = lambda x: x[0] if isinstance(x, tuple) else x
    person = [x for x in todo if str(run(x).get("from", "")).lower() == "person"]
    rest = [x for x in todo if x not in person]
    rng.shuffle(rest)
    return person + rest[:sample]

def cmd_grade(a):
    asks = asks_mod()
    try: cur = json.loads(CURSOR.read_text())
    except (OSError, ValueError): cur = {}
    since = cur.get("at", iso(now() - dt.timedelta(hours=6))); until = iso(now() - dt.timedelta(seconds=SETTLE_S))
    log, posts = jsonl(LOG), jsonl(BOARD); by_id = {p.get("id"): p for p in posts}
    done = {r.get("key") for r in jsonl(LEDGER)}
    todo = [r for r in runs(log, since, until) if r["key"] not in done]
    if a.limit: todo = todo[:a.limit]
    graded, model_runs, cost = 0, [], 0.0
    for r in todo:
        post, mine, after = posts_of(r, posts, by_id)
        g = mechanical(r, post, mine)
        if g:
            if not a.dry: append(LEDGER, {**r, "at": iso(), "grade": g, "why": "posted nothing" + (" though named" if g == "silent" else ""), "by": "rule"})
            graded += 1
        else: model_runs.append((r, post, mine, after))
    chosen = pick(model_runs, a.sample) if not a.all else model_runs
    for i in range(0, len(chosen), 8):
        batch = chosen[i:i + 8]
        prompt = PROMPT.replace("{blocks}", "\n\n".join(block(*b) for b in batch))
        if a.dry: print(prompt[:3000] + ("\n..." if len(prompt) > 3000 else "")); print(f"... {len(batch)} runs, {len(prompt)} chars"); continue
        try: out, c = asks.ask_model(prompt, a.model)
        except Exception as e: print(f"outcomes: model call failed: {e}", file=sys.stderr); break
        cost += c or 0.0
        byk = {r["key"]: (r, post, mine) for r, post, mine, _ in batch}
        for g in parse_grades(out):
            r, post, mine = byk.get(g.get("run"), (None, None, None))
            if not r or g.get("grade") not in ("delivered", "partial", "nothing", "not_asked"): continue
            append(LEDGER, {**r, "at": iso(), "grade": g["grade"], "why": clip(g.get("why"), 200), "posts": len(mine), "by": a.model, "cost": round(cost / len(batch), 4)}); graded += 1
    if not a.dry and todo: CURSOR.write_text(json.dumps({"at": max(r["ended"] for r in todo), "run": iso()}))
    print(f"{graded} runs graded ({len(chosen)} by the model, {len(model_runs) - len(chosen)} left unsampled), ${cost:.2f}")

def parse_grades(out):
    try: return asks_mod().parse_json(out).get("grades", [])
    except Exception: return []

def recent(since):
    t0 = iso(now() - span(since))
    return [r for r in jsonl(LEDGER) if r.get("at", "") >= t0]

def stats(rows):
    n = {g: sum(1 for r in rows if r.get("grade") == g) for g in ("delivered", "partial", "nothing", "silent", "quiet", "not_asked")}
    den = sum(n[g] for g in GRADED)
    return {"outcomes_graded": len(rows), "outcome_delivered_pct": round(100.0 * n["delivered"] / den, 1) if den else None,
            "outcomes_silent": n["silent"], "outcomes_nothing": n["nothing"], **{f"outcomes_{g}": v for g, v in n.items()}}

def cmd_list(a):
    rows = [r for r in recent(a.since) if (not a.grade or r.get("grade") == a.grade) and (not a.name or r.get("name") == a.name)]
    for r in rows: print(f"{r.get('at', '')[:16]}  {r.get('name')}  {r.get('grade'):<10} woken by {r.get('from')} in {r.get('thread')} (post {r.get('post')}): {r.get('why', '')}")
    print(f"-- {len(rows)} runs; " + ", ".join(f"{k[9:]} {v}" for k, v in stats(rows).items() if k.startswith("outcomes_") and k != "outcomes_graded"))

def cmd_stats(a):
    s = stats(recent(a.since))
    if a.json: print(json.dumps(s)); return
    print(f"{s['outcomes_graded']} runs graded in the last {a.since}: delivered {s['outcome_delivered_pct']}% of the {sum(s['outcomes_' + g] for g in GRADED)} that were asked something "
          f"(delivered {s['outcomes_delivered']}, partial {s['outcomes_partial']}, nothing {s['outcomes_nothing']}, silent {s['outcomes_silent']}); quiet {s['outcomes_quiet']}, not asked {s['outcomes_not_asked']}")

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive outcomes", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("grade"); p.add_argument("--limit", type=int, default=0); p.add_argument("--sample", type=int, default=SAMPLE); p.add_argument("--all", action="store_true", help="every run with posts, not a sample")
    p.add_argument("--model", default="sonnet"); p.add_argument("--dry", action="store_true"); p.set_defaults(f=cmd_grade)
    p = sp.add_parser("list"); p.add_argument("--since", default="24h"); p.add_argument("--grade"); p.add_argument("--name"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("stats"); p.add_argument("--since", default="24h"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_stats)
    a = ap.parse_args(argv); a.f(a)

if __name__ == "__main__": main()
