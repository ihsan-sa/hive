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
A completion wake (an agent's result post named this agent, asked it nothing, and it ticked no rung) and a self-wake (the agent's own post named it) are
only counted, every one of them, and marked on a ledger row that gets graded anyway: hive-meta moving.md P4, measuring before anyone suppresses them. The
signal is in the waking post, and it fires on about a quarter of runs that did deliver, so it must not set a grade. `stats` shows both shares of agent-code wakes.
`hive progress` shows outcome_delivered_pct and outcomes_silent; the rsi loop and the keeper read them.
The same model call also picks reusable facts out of result posts: at most one draft a day per thread in ~/kb/_drafts, which the dream merges or drops (hive-meta knowledge.md P6, `kb drafts`).

  hive outcomes grade [--limit N] [--sample N] [--model sonnet] [--dry]   grade runs ended since the last run (hive-outcomes.timer, hourly)
  hive outcomes list [--since 24h] [--grade G] [--name NAME]
  hive outcomes stats [--since 24h] [--json]
"""
import argparse, datetime as dt, importlib.util, json, os, random, re, subprocess, sys
from pathlib import Path

HOME = Path.home()
CORE, STATE = HOME / "core", HOME / ".hive"
LEDGER, LOG, BOARD = HOME / "outcomes.jsonl", HOME / "log.jsonl", HOME / "board.jsonl"
CURSOR = STATE / "outcomes-cursor.json"
SAMPLE = 10                # agent-woken runs with posts graded by the model per hour, on top of every person-woken run (4 until 2026-11-10: only 15% of pulse roots had a grade, so away_ratio read low)
OTHER_MAX = 12             # runs on a non-Claude provider graded per hour on top of that: at 13% sampled, 1 of 21 agent-woken Codex runs had a grade, so nobody could say whether Codex delivers
SETTLE_S = 180             # a run's end must be this old: its last post may still be landing
TAIL_S = 90                # posts this long after the end still count as the run's
BAD = ("silent", "nothing")
GOOD = ("delivered",)
GRADED = ("delivered", "partial", "nothing", "silent")   # the denominator of outcome_delivered_pct; quiet and not_asked are neither
SHORT = ("partial", "nothing")   # graded runs that fell short of what was asked
CHEAP = (None, "", "sonnet", "haiku")   # models the step-up moves off
STEP_UP_AFTER = 2          # this many short runs in a row by one agent in one thread, on a cheap model, and its next wake is on opus
STEP_UP_TO = "opus"
NOT_AGENTS = {"person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer", "postland", "leftover-router"}   # as goals.py
LONG_POST = 300   # chars: a waking post this long is result-shaped
ORDERS = set("add answer apply ask build change check commit continue do find fix go implement land look make merge please post read remove rerun review run "
             "see set start stop take tell test try update use write".split())   # a waking post that opens with one of these is an order, however long
RESULT_LAST = re.compile(r"(done:|landed|committed)", re.I)
DRAFTS, DRAFT_LOG = HOME / "kb" / "_drafts", STATE / "drafts.jsonl"   # kbuse.py reads the log: the measure is the share of these read in 14 days
DRAFT_LINES = (3, 10)      # a draft's body, in lines
OWN_WORDS = 0.7            # share of a draft's words that must be in its source post: it quotes, it doesn't summarise
GRADE_RE = re.compile(r'"run"\s*:\s*"([^"]+)"\s*,\s*"grade"\s*:\s*"(\w+)"\s*,\s*"why"\s*:\s*"((?:[^"\\]|\\.)*)"')
POST_CLIP, RESULT_CLIP = 900, 2500   # chars of a post shown to the model; the last one is usually the result and holds the fact

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
                    "also": (s.get("woke") or [])[1:], **({"heir_of": s["heir_of"]} if s.get("heir_of") else {}),
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

ROUND = re.compile(r"Keeper round on ")   # a brief from the floor or a keeper: a short round on an empty ladder says nothing about the model, so it doesn't count toward a step-up

CONTENTLESS = re.compile(r"[A-Za-z0-9]")

def contentless(post):
    """The waking post said nothing beyond names and punctuation ("???", "!!", a bare mention): there is no ask to grade against."""
    return bool(post) and not CONTENTLESS.search(re.sub(r"@\w+", "", str(post.get("text") or "")))

def mechanical(run, post, mine):
    if mine: return "not_asked" if contentless(post) else None   # "???" answered with where things stand was graded nothing on 2026-11-04
    return "silent" if named(run, post) else "quiet"

def names_run(run, post):
    """An agent's post that names this run's agent code, or the code of the dead agent it inherited. Not a fresh-agent word, not the person."""
    if str(post.get("from", "")).lower() in NOT_AGENTS: return False
    codes = {c for c in (run.get("name"), run.get("heir_of")) if c}
    to = post.get("to") if isinstance(post.get("to"), list) else [post.get("to")]
    return any(str(x).lstrip("@") in codes for x in to) or any(re.search(rf"@{re.escape(c)}\b", str(post.get("text") or "")) for c in codes)

def result_shaped(text):
    """Over LONG_POST chars, or its last line (past any line of bare mentions) starts Done:, landed or committed."""
    text = str(text or "")
    lines = [l.strip() for l in text.splitlines() if CONTENTLESS.search(re.sub(r"@\w+", "", l))]
    return len(text) > LONG_POST or bool(lines and RESULT_LAST.match(lines[-1]))

def ticked_rungs():
    """Every ticked rung of every project: a tick carries the id of its result post (goals.py brief)."""
    out = []
    for f in (HOME / "projects").glob("*/goals.md"):
        try: out += [l for l in f.read_text(errors="replace").splitlines() if re.match(r"\s*- \[x\]", l, re.I)]
        except OSError: pass
    return "\n".join(out)

def asks_nothing(text):
    """No question outside backticks, and it doesn't open (past any @names) with an order."""
    bare = re.sub(r"```.*?```|`[^`]*`", "", str(text or ""), flags=re.S)
    first = re.search(r"[A-Za-z][\w-]*", re.sub(r"@\w+", "", bare))
    return "?" not in bare and not (first and first.group(0).lower() in ORDERS)

def self_wake(run, post):
    """Its own post named it: a model-switch, run-ended or flood-finished notice, a class of its own. Keeper-style runs resume on these on purpose."""
    return str(post.get("from", "")) in {c for c in (run.get("name"), run.get("heir_of")) if c} and names_run(run, post)

def completion_wake(run, by_id, mine, ticks):
    """Woken by another agent's result, naming it, asking it nothing, and it ticked no rung. Judged on the waking post, not the reply: woken agents answer
    with a 200-1500 char status post, so reply length caught 1 of 21 wasted wakes (af3c9, post 1106035341df). `run` may have been woken by several posts;
    each has to qualify, so an ask that arrived alongside keeps it out. A count, not a grade (see the top of this file)."""
    first, *rest = [by_id.get(i) or {} for i in [run["post"], *run.get("also", [])]]
    if not first or not names_run(run, first) or self_wake(run, first): return False
    if not all(str(p.get("from", "")).lower() not in NOT_AGENTS and result_shaped(p.get("text")) and asks_nothing(p.get("text")) for p in [first, *rest]): return False
    return not any(p.get("id") and p["id"] in ticks for p in mine)

def completion_share(since, log=None, posts=None):
    """(completion wakes, self-wakes, agent-code wakes) among runs ended in the span, worked out from the log and the board, since the ledger holds a sample of the rest."""
    log, posts = jsonl(LOG) if log is None else log, jsonl(BOARD) if posts is None else posts
    by_id, ticks, wakes, done, own = {p.get("id"): p for p in posts}, ticked_rungs(), 0, 0, 0
    t0 = now() - span(since); near = iso(t0 - dt.timedelta(days=1)); nearby = [p for p in posts if p.get("at", "") >= near]
    for r in runs(log, iso(t0)):
        post = by_id.get(r["post"]) or {"from": "person"}
        if not names_run(r, post): continue
        wakes += 1; own += self_wake(r, post); done += completion_wake(r, by_id, posts_of(r, nearby, by_id)[1], ticks)
    return done, own, wakes

def clip(s, n): s = str(s or "").strip(); return s if len(s) <= n else s[:n] + " …"

def block(run, post, mine, after):
    lines = [f"RUN {run['key']}: agent {run['name']}, woken by {run['from']} in thread {run['thread']}, ran {run['started'][11:16]} to {run['ended'][11:16]} UTC, exit {run['exit']}",
             f"  WAKING POST ({post.get('from') if post else '?'}): {clip((post or {}).get('text'), 1500)}"]
    shown = mine if len(mine) <= 6 else mine[:5] + mine[-1:]   # the first few, and the last: the result comes at the end
    for i, p in enumerate(shown):
        if i == 5 and len(mine) > 6: lines.append(f"  ... and {len(mine) - 6} more posts")
        lines.append(f"  AGENT POSTED (thread {p.get('thread')}, post {p.get('id')}): {clip(p.get('text'), RESULT_CLIP if p is shown[-1] else POST_CLIP)}")
    for p in after: lines.append(f"  AFTER, {p.get('from')} said: {clip(p.get('text'), 400)}")
    return "\n".join(lines)

PROMPT = """You grade whether a hive agent's run did what the post that woke it asked. The hive is a group of coding agents sharing a message board; each run is one agent woken by one post. You see the waking post, what the agent posted during the run, and what others said after.

Grades:
  delivered  its posts show the ask done, with how it was checked (a result, not a promise)
  partial    some of it done, or started and handed over with a clear plan and owner
  nothing    it posted, but no delivery and no handover: status only, questions back, or the wrong thing
  not_asked  the post asked this agent nothing concrete (a thread update, an acknowledgement, a hand-off to someone else)

Judge only against what the post asked, not whether the work was good. A post that says "stop" or "thanks" and a short reply is not_asked; so is a chase ("???", "hello?", "status?", "did you do it") answered with where things stand. Reply with JSON only:
{"grades":[{"run":"<RUN key>","grade":"delivered|partial|nothing|not_asked","why":"<at most 20 words>"}]}

One more job, on delivered and partial runs only. If a post by the agent holds a fact another agent could use months from now in other work, add to that run's grade:
"note":{"post":"<the post id>","title":"<what the note answers, at most 12 words>","lines":["3 to 10 lines, copied from that post in its own words, each understandable without the thread"]}
Worth a note: how something was made to work, a number with how it was measured, a gotcha with its cause. Not a note: status, thanks, a plan, what was changed in this one job with nothing to learn from it, a number that goes stale within days, one person's private or one-off matter such as a purchase. Most runs have no note: leave the field out.

{blocks}"""

def provider(r):
    """Which provider a run or ledger row ran on, from its model: "codex:gpt-6-astra" is codex, "sonnet" (or none) is claude."""
    m = str(r.get("model") or "")
    return m.split(":")[0] if ":" in m else "claude"

def pick(todo, sample, rng=random, other_max=OTHER_MAX):
    """Every person-woken run, up to `other_max` agent-woken runs on another provider, plus `sample` Claude runs with posts."""
    run = lambda x: x[0] if isinstance(x, tuple) else x
    person = [x for x in todo if str(run(x).get("from", "")).lower() == "person"]
    rest = [x for x in todo if x not in person]
    rng.shuffle(rest)
    other = [x for x in rest if provider(run(x)) != "claude"]
    return person + other[:other_max] + [x for x in rest if provider(run(x)) == "claude"][:sample]

def write_draft(g, run, mine):
    """A grade's note as ~/kb/_drafts/<thread>-<date>.md, at most one a day per thread, or None. The model proposes; these rules decide. Never ~/kb proper."""
    n = g.get("note")
    if not isinstance(n, dict) or g.get("grade") not in ("delivered", "partial"): return None
    src = next((p for p in mine if p.get("id") and p.get("id") == n.get("post")), None)
    lines = [str(l).strip() for l in n.get("lines") or [] if str(l).strip()] if isinstance(n.get("lines"), list) else []
    title = re.sub(r"\s+", " ", str(n.get("title") or "")).strip()[:120]
    if not src or not title or not DRAFT_LINES[0] <= len(lines) <= DRAFT_LINES[1]: return None
    text, have = "\n".join(lines), set(re.findall(r"\w+", str(src.get("text")).lower()))
    said = re.findall(r"\w+", text.lower())
    if not said or sum(w in have for w in said) < OWN_WORDS * len(said): return None
    spec = importlib.util.spec_from_file_location("kb", CORE / "kb.py"); kb = importlib.util.module_from_spec(spec); spec.loader.exec_module(kb)
    if kb.SECRET.search(title + " " + text): return None   # the rule `kb file` holds notes to
    thread, day = str(src.get("thread") or run.get("thread") or "none"), iso()[:10]
    path = DRAFTS / f"{re.sub(r'[^A-Za-z0-9_.-]+', '-', thread)[:80]}-{day}.md"
    if any(r.get("path") == str(path) for r in jsonl(DRAFT_LOG)): return None   # counted from the log too: a draft the dream already moved still used the day
    DRAFTS.mkdir(parents=True, exist_ok=True)
    try:
        with open(path, "x") as f: f.write(f"# {title}\n\n{text}\n\nDraft by the hourly grader, not checked. Source: post {src.get('id')} by {src.get('from')}, thread {thread}, {day}. `hive read --id {src.get('id')}` for all of it.\n")
    except FileExistsError: return None
    append(DRAFT_LOG, {"at": iso(), "path": str(path), "post": src.get("id"), "thread": thread, "run": run.get("key")})
    return path

def cmd_grade(a):
    asks = asks_mod()
    try: cur = json.loads(CURSOR.read_text())
    except (OSError, ValueError): cur = {}
    other = getattr(a, "other_since", "")  # older callers build the Namespace without it
    since = other or cur.get("at", iso(now() - dt.timedelta(hours=6))); until = iso(now() - dt.timedelta(seconds=SETTLE_S))
    log, posts = jsonl(LOG), jsonl(BOARD); by_id = {p.get("id"): p for p in posts}
    done = {r.get("key") for r in jsonl(LEDGER)}
    todo = [r for r in runs(log, since, until) if r["key"] not in done and (not other or provider(r) != "claude")]
    if a.limit: todo = todo[:a.limit]
    graded, model_runs, cost, ticks, drafts, limited = 0, [], 0.0, ticked_rungs(), 0, False
    for r in todo:
        post, mine, after = posts_of(r, posts, by_id)
        r = {**r, **({"completion_wake": True} if completion_wake(r, by_id, mine, ticks) else {}), **({"self_wake": True} if post and self_wake(r, post) else {}), **({"round": True} if post and ROUND.search(str(post.get("text", ""))[:80]) else {})}   # marks for the spot-check and the step-up; the grade doesn't look at them
        g = mechanical(r, post, mine)
        if g:
            why = "the post asked nothing" if g == "not_asked" else "posted nothing" + (" though named" if g == "silent" else "")
            if not a.dry: append(LEDGER, {**r, "at": iso(), "grade": g, "why": why, "posts": len(mine), "by": "rule"})
            graded += 1
        else: model_runs.append((r, post, mine, after))
    chosen = pick(model_runs, a.sample) if not (a.all or other) else model_runs
    for i in range(0, len(chosen), 8):
        batch = chosen[i:i + 8]
        prompt = PROMPT.replace("{blocks}", "\n\n".join(block(*b) for b in batch))
        if a.dry: print(prompt[:3000] + ("\n..." if len(prompt) > 3000 else "")); print(f"... {len(batch)} runs, {len(prompt)} chars"); continue
        try: out, c = asks.ask_model(prompt, a.model)
        except Exception as e:
            limited = isinstance(e, getattr(asks, "Limited", ()))
            print(f"outcomes: model call failed: {e}", file=sys.stderr); break
        cost += c or 0.0
        byk = {r["key"]: (r, post, mine) for r, post, mine, _ in batch}
        for g in parse_grades(out):
            r, post, mine = byk.get(g.get("run"), (None, None, None))
            if not r or g.get("grade") not in ("delivered", "partial", "nothing", "not_asked"): continue
            append(LEDGER, {**r, "at": iso(), "grade": g["grade"], "why": clip(g.get("why"), 200), "posts": len(mine), "by": a.model, "cost": round(cost / len(batch), 4)}); graded += 1
            try: drafts += bool(write_draft(g, r, mine))
            except OSError as e: print(f"outcomes: draft not written: {e}", file=sys.stderr)
    # Claude at its limit is when fallback runs most need grading: hold the cursor, and the next hour picks them up (the ledger's keys stop a run being graded twice)
    if not a.dry and todo and not limited and not other: CURSOR.write_text(json.dumps({"at": max(r["ended"] for r in todo), "run": iso()}))
    if not a.dry and graded and not other: step_up(graded)
    print(f"{graded} runs graded ({len(chosen)} by the model, {len(model_runs) - len(chosen)} left unsampled), {drafts} drafts, ${cost:.2f}")

def step_up(new, post=None):
    """An agent graded short STEP_UP_AFTER times in a row in one thread while on a cheap model gets its next wake on STEP_UP_TO:
    a model-set file for the starter (the same thing `hive model opus --next` writes) and one line in the thread. Self-judged step-ups
    were 0 of 12 in the first week (kb/model-cost.md), so the grader does it. Returns the names stepped up."""
    rows = jsonl(LEDGER); fresh, earlier = rows[-new:] if new else [], rows[:-new] if new else rows
    stepped, seen = [], set()
    for r in reversed(fresh):
        name, thread = r.get("name"), r.get("thread")
        if not name or not thread or (name, thread) in seen: continue
        seen.add((name, thread))
        if r.get("grade") not in SHORT or r.get("model") not in CHEAP or r.get("round"): continue
        prev = [x for x in earlier if x.get("name") == name and x.get("thread") == thread and x.get("grade") in GRADED]
        run = [r] + [x for x in reversed(prev)][:STEP_UP_AFTER - 1]
        if len(run) < STEP_UP_AFTER or any(x.get("grade") not in SHORT or x.get("model") not in CHEAP or x.get("round") for x in run): continue
        f = STATE / "model-set" / f"{name}.json"
        if f.exists(): continue
        was = r.get("model") or "sonnet"
        why = f"outcomes: {STEP_UP_AFTER} runs in a row graded short on {was} in thread {thread}"
        f.parent.mkdir(parents=True, exist_ok=True); f.write_text(json.dumps({"model": STEP_UP_TO, "why": why}))
        text = (f"Next wake on {STEP_UP_TO} (up from {was}): the grader marked {name}'s last {STEP_UP_AFTER} runs here as not delivering what was asked "
                f"({', '.join(x.get('grade', '') for x in run)}). Step back down with `hive model sonnet --next \"why\"` once the hard part is done.")
        (post or post_board)(thread, text); stepped.append(name)
    return stepped

def post_board(thread, text):
    import subprocess
    subprocess.run([str(CORE / "hive"), "post", "--thread", thread], input=text, text=True, env=dict(os.environ, HIVE_NAME="outcomes"), check=False, timeout=60)

def parse_grades(out):
    try: return asks_mod().parse_json(out).get("grades", [])
    except Exception:   # copied post text can break the JSON (an unescaped quote): keep the grades, a run's ledger row is only written once, and lose the notes
        return [{"run": r, "grade": g, "why": w} for r, g, w in GRADE_RE.findall(out or "")]

def recent(since):
    t0 = iso(now() - span(since))
    return [r for r in jsonl(LEDGER) if r.get("at", "") >= t0]

INTENT = re.compile(r"intent[- ]only|only an intent|intent to|only (posted|said|says|stated|announced|promised)\b.*\b(taking|will|would|intent|plan|start)", re.I)

def intent_only(r):
    """A graded run that did not deliver and whose grade says all it posted was that it is taking the work (a60b7, 2026-11-08)."""
    return r.get("grade") in ("nothing", "partial", "silent") and bool(INTENT.search(str(r.get("why", ""))))

def stats(rows):
    n = {g: sum(1 for r in rows if r.get("grade") == g) for g in ("delivered", "partial", "nothing", "silent", "quiet", "not_asked")}
    den = sum(n[g] for g in GRADED)
    return {"outcomes_graded": len(rows), "outcome_delivered_pct": round(100.0 * n["delivered"] / den, 1) if den else None,
            "outcomes_silent": n["silent"], "outcomes_nothing": n["nothing"], "outcomes_intent_only": sum(1 for r in rows if intent_only(r)), **{f"outcomes_{g}": v for g, v in n.items()}}

def cmd_list(a):
    rows = [r for r in recent(a.since) if (not a.grade or r.get("grade") == a.grade) and (not a.name or r.get("name") == a.name)]
    for r in rows: print(f"{r.get('at', '')[:16]}  {r.get('name')}  {r.get('grade'):<10} woken by {r.get('from')} in {r.get('thread')} (post {r.get('post')}): {r.get('why', '')}")
    print(f"-- {len(rows)} runs; " + ", ".join(f"{k[9:]} {v}" for k, v in stats(rows).items() if k.startswith("outcomes_") and k != "outcomes_graded"))

def by_provider(rows):
    """Delivery per provider and who woke the run, since a person's post and an agent's are different asks: compare like with like."""
    out = {}
    for r in rows: out.setdefault((provider(r), "person" if str(r.get("from", "")).lower() == "person" else "agent"), []).append(r)
    return {f"{k[0]}/{k[1]}": stats(v) for k, v in sorted(out.items())}

def cmd_stats(a):
    if getattr(a, "by", "") == "provider":
        rows = by_provider(recent(a.since))
        if a.json: print(json.dumps(rows)); return
        for k, s in rows.items(): print(f"{k:<20} delivered {s['outcome_delivered_pct']}% of {sum(s['outcomes_' + g] for g in GRADED)} asked (delivered {s['outcomes_delivered']}, partial {s['outcomes_partial']}, nothing {s['outcomes_nothing']}, silent {s['outcomes_silent']}); quiet {s['outcomes_quiet']}, not asked {s['outcomes_not_asked']}")
        return
    s = stats(recent(a.since)); done, own, wakes = completion_share(a.since)
    share, own_share = (round(100.0 * done / wakes, 1), round(100.0 * own / wakes, 1)) if wakes else (None, None)
    if a.json: print(json.dumps({**s, "completion_wakes": done, "self_wakes": own, "agent_code_wakes": wakes, "completion_wake_pct": share, "self_wake_pct": own_share})); return
    print(f"{s['outcomes_graded']} runs graded in the last {a.since}: delivered {s['outcome_delivered_pct']}% of the {sum(s['outcomes_' + g] for g in GRADED)} that were asked something "
          f"(delivered {s['outcomes_delivered']}, partial {s['outcomes_partial']}, nothing {s['outcomes_nothing']}, silent {s['outcomes_silent']}); quiet {s['outcomes_quiet']}, not asked {s['outcomes_not_asked']}")
    print(f"completion wakes: {done} of {wakes} agent-code wakes ended in the span ({share}%); self-wakes: {own} ({own_share}%). Counts only, no grade follows from them")

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive outcomes", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("grade"); p.add_argument("--limit", type=int, default=0); p.add_argument("--sample", type=int, default=SAMPLE); p.add_argument("--all", action="store_true", help="every run with posts, not a sample")
    p.add_argument("--model", default="sonnet"); p.add_argument("--dry", action="store_true"); p.add_argument("--other-since", default="", metavar="DATE", help="backfill: every ungraded run on a non-Claude provider ended after DATE; the hourly cursor doesn't move"); p.set_defaults(f=cmd_grade)
    p = sp.add_parser("list"); p.add_argument("--since", default="24h"); p.add_argument("--grade"); p.add_argument("--name"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("stats"); p.add_argument("--since", default="24h"); p.add_argument("--json", action="store_true"); p.add_argument("--by", choices=["provider"], help="delivery per provider, person-woken and agent-woken apart"); p.set_defaults(f=cmd_stats)
    a = ap.parse_args(argv); a.f(a)
    if a.cmd == "grade" and not a.dry: subprocess.run([sys.executable, "-I", str(Path(__file__).parent / "progress.py"), "autojudge"], capture_output=True)  # the same hour's other scoring: changes whose check is due

if __name__ == "__main__": main()
