#!/usr/bin/env python3
"""Recursive self-improvement, measured. Two things, no model use:

`hive improve`  — the improvements ledger, ~/improvements.jsonl: every change meant to make the hive better (core, briefs,
                orientation, modules, a project's loop) is logged with a hypothesis, the number that would show it, and a
                revisit date. On that date someone who didn't make the change judges it: held, reverted or inconclusive.
                An earlier loop lacked this fifth step ("judge after landing"), so it never learned which of its changes helped.
`hive progress` — the scoreboard: the hive's numbers for a window (runs and failures, the person noticing first, stalled
                threads, improvements landed/judged/overdue, rsi-loop rounds, keeper/improver rounds, spend, core commits,
                projects without a measure) against the window before. `--snapshot` appends to ~/progress.jsonl so trends
                survive; `--post` puts the table in thread `progress`. The keeper reads it every round and moves the worst
                number; hive-progress.timer snapshots it without an agent.

Rules, from an earlier lesson: the loop never edits its own scorers, gates or keep-rules without a human-readable note in the
ledger; the judge is never who proposed; a judgement needs the number, not a feeling. Append-only ledger: a judge adds a
record, the last record per id wins. Every reader of the raw data is optional: a consumer Hive without hive-cost or
rsi-loop still gets a scoreboard."""
import importlib.util, argparse, datetime as dt, json, os, re, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
LEDGER, SNAPS, LOG, BOARD = HOME / "improvements.jsonl", HOME / "progress.jsonl", HOME / "log.jsonl", HOME / "board.jsonl"
CORE, STATE, PROJECTS = HOME / "core", HOME / ".hive", HOME / "projects"
Hive = next((CORE / n for n in ("hive", "hive") if (CORE / n).exists()), CORE / "hive")  # an installed Hive's CLI is called hive
RSI_LEDGER = PROJECTS / "rsi-loop" / "ledger.jsonl"
VERDICTS = ("held", "reverted", "inconclusive")
MEASURE = re.compile(r"measure|metric|score|yardstick|benchmark|passes|pass rate|p&l|sharpe", re.I)

def now(): return dt.datetime.now(dt.timezone.utc)
def iso(t=None): return (t or now()).isoformat(timespec="seconds")

def span(s):
    m = re.fullmatch(r"(\d+)([mhdw])", s or "")
    if not m: sys.exit(f"bad span {s!r}: use 30m, 6h, 1d, 2w")
    return dt.timedelta(**{{"m": "minutes", "h": "hours", "d": "days", "w": "weeks"}[m[2]]: int(m[1])})

def jsonl(path):
    out = []
    try:
        for line in path.open(errors="replace"):
            try: out.append(json.loads(line))
            except ValueError: pass
    except OSError: pass
    return out

def append(path, rec):
    with path.open("a") as f: f.write(json.dumps(rec, ensure_ascii=False) + "\n")

# ---------- ledger ----------

def ledger():
    """Improvements by id, last record wins; judge records fold into the entry they judge."""
    items = {}
    for r in jsonl(LEDGER):
        if r.get("judge"):
            e = items.get(r["judge"])
            if e: e.update(status=r["verdict"], judged_at=r["at"], judged_by=r.get("by"), judged_why=r.get("why", ""))
            continue
        if r.get("id"): items[r["id"]] = {**items.get(r["id"], {}), **r}
    # rsi-loop's landed wins count too: they're the measured ones
    for r in jsonl(RSI_LEDGER):
        if r.get("verdict") != "win": continue
        i = f"rsi-{r.get('arm')}"
        if i in items: continue
        at = r.get("at", "")
        try: rev = (dt.datetime.fromisoformat(at) + dt.timedelta(days=7)).date().isoformat()
        except ValueError: rev = ""
        kind = r.get("kind", "orientation")
        files = r.get("files") or ["orientation.md"]
        items[i] = {"id": i, "at": at, "by": "rsi-loop", "what": f"{kind}: {r.get('arm')} won " + ("the replay gate" if r.get("gate") == "replay" else "the benchmark"), "where": ", ".join("core/" + f for f in files),
                    "measure": "rsi-loop replay delivered score and cost (results/replay-*)" if r.get("gate") == "replay" else "rsi-loop bench passes and cost (results/)", "baseline": r.get("why", ""), "revisit": rev, "status": "landed",
                    "commit": r.get("landed") or r.get("commit", ""), "thread": "rsi-loop"}
        for r2 in jsonl(LEDGER):  # a judge record for an rsi id
            if r2.get("judge") == i: items[i].update(status=r2["verdict"], judged_at=r2["at"], judged_by=r2.get("by"), judged_why=r2.get("why", ""))
    return items

def overdue(items, t=None):
    today = (t or now()).date().isoformat()
    return [e for e in items.values() if e.get("status") == "landed" and e.get("revisit", "9999") <= today]

def next_id(items):
    n = max((int(i[1:]) for i in items if re.fullmatch(r"i\d+", i)), default=0) + 1
    return f"i{n:04d}"

def cmd_log(a):
    items = ledger()
    if not a.measure: sys.exit("hive improve log: --measure is the number that would show the change worked (or not). No measure, no entry.")
    rev = a.revisit or "7d"
    if re.fullmatch(r"\d+[dw]", rev): rev = (now() + span(rev)).date().isoformat()
    rec = {"id": next_id(items), "at": iso(), "by": os.environ.get("HIVE_NAME", os.environ.get("USER", "?")), "what": a.what, "where": a.where or "",
           "hypothesis": a.hypothesis or "", "measure": a.measure, "baseline": a.baseline or "", "revisit": rev, "status": "landed",
           "commit": a.commit or "", "thread": a.thread or os.environ.get("HIVE_THREAD", "")}
    append(LEDGER, rec)
    print(f"{rec['id']} logged; revisit {rev}. Someone else judges it then: hive improve judge {rec['id']} held|reverted|inconclusive --why ...")

def cmd_judge(a):
    items = ledger()
    e = items.get(a.id)
    if not e: sys.exit(f"hive improve judge: no improvement {a.id}")
    me = os.environ.get("HIVE_NAME", os.environ.get("USER", "?"))
    if e.get("by") == me and not a.self:
        sys.exit(f"hive improve judge: {a.id} was logged by you. The judge is never who proposed (the rule); pass --self only if nobody else can, and say why in --why.")
    if not a.why: sys.exit("hive improve judge: --why must give the number you looked at, against the baseline.")
    append(LEDGER, {"judge": a.id, "at": iso(), "by": me, "verdict": a.verdict, "why": a.why})
    print(f"{a.id}: {a.verdict}. " + ("Now revert it (or confirm it was) and say so in its thread." if a.verdict == "reverted" else
                                       "Set a later revisit with hive improve log if the number needs more time." if a.verdict == "inconclusive" else "Kept."))

def table(rows, cols):
    w = [max(len(str(r.get(c, ""))) for r in rows + [{c: c}]) for c in cols]
    print("  ".join(c.ljust(w[i]) for i, c in enumerate(cols)))
    for r in rows: print("  ".join(str(r.get(c, "")).ljust(w[i]) for i, c in enumerate(cols)))

def cmd_list(a):
    items = ledger()
    rows = overdue(items) if a.due else list(items.values())
    if a.json: print(json.dumps(rows, indent=1)); return
    if not rows: print("no overdue revisits" if a.due else "no improvements logged yet: hive improve log --what ... --measure ... --revisit 7d"); return
    rows = [{"id": e["id"], "at": e.get("at", "")[:10], "by": e.get("by"), "status": e.get("status"), "revisit": e.get("revisit", ""),
             "what": e.get("what", "")[:70], "measure": e.get("measure", "")[:50]} for e in sorted(rows, key=lambda e: e.get("at", ""))]
    table(rows, ["id", "at", "by", "status", "revisit", "what", "measure"])
    if not a.due:
        d = overdue(items)
        if d: print(f"\n{len(d)} overdue for judgement: hive improve list --due")

def cmd_show(a):
    e = ledger().get(a.id)
    if not e: sys.exit(f"no improvement {a.id}")
    print(json.dumps(e, indent=1, ensure_ascii=False))

# ---------- scoreboard ----------

def runs_in(log, t0, t1):
    starts = {r["session"]: r for r in log if r.get("event") == "start" and t0 <= r.get("at", "") <= t1 and r.get("session")}
    ends = [r for r in log if r.get("event") == "end" and r.get("session") in starts]
    # The starter's own kill-and-resume (a parked run woken for a post, or a run moved to a model) ends with -9/143 and
    # starts the same name again within moments: that is not a failure, so an end with a "woken" event on its session,
    # or a start for the same name within 3 min after it, doesn't count.
    woken = {r.get("session") for r in log if r.get("event") in ("woken", "wake_resumed", "moved")}
    by_name = {}
    for r in log:
        if r.get("event") == "start" and r.get("name"): by_name.setdefault(r["name"], []).append(r.get("at", ""))
    def resumed(e):
        if e.get("session") in woken: return True
        t = e.get("at", "")
        try: te = dt.datetime.fromisoformat(t).timestamp()
        except ValueError: return False
        for a in by_name.get(e.get("name"), []):
            try: ta = dt.datetime.fromisoformat(a).timestamp()
            except ValueError: continue
            if 0 <= ta - te <= 180: return True
        return False
    failed = sum(1 for r in ends if r.get("exit") not in (0, None) and not resumed(r))
    reaped = sum(1 for r in log if r.get("event") == "reaped" and t0 <= r.get("at", "") <= t1)
    return len(starts), failed, reaped

# the person finding a problem before the hive did: a chase ("hello?", "status?"), a thing not working or not showing, a "why is X not".
# Plain "still" and "why is" matched ordinary feedback and design questions (13 of 29 matches on 2026-11-04), so they need a negative with them.
NOTICED = re.compile(r"\b(?:hello\?|status\?|not seeing|noticed|isn.t working|broken|nothing happen|why am i not|still (?:not|no|isn|doesn|waiting|nothing|broken|missing)\b|why (?:is|are|does|do)\b[^?.!\n]{0,60}?\b(?:not|no|isn.t|doesn.t|don.t|only|gap|wrong|missing)\b)", re.I)

def board_in(posts, t0, t1):
    ps = [p for p in posts if t0 <= p.get("at", "") <= t1]
    person = [p for p in ps if p.get("from") == "person"]
    noticed = sum(1 for p in person if NOTICED.search(str(p.get("text", ""))))
    return len(ps), len(person), noticed

AGENT = re.compile(r"[a-z][0-9a-f]{4}")

def reply_s(posts, t0, t1):
    """Seconds from each of the person's posts in the window that names an agent to that agent's next post in the thread, sorted.
    A post with no reply yet is left out (not a number), so the count is the answered ones. The person says "hello?" after a minute."""
    out, by_th = [], {}
    for i, p in enumerate(posts): by_th.setdefault(p.get("thread") or p.get("id"), []).append((i, p))
    for i, p in enumerate(posts):
        if p.get("from") != "person" or not t0 <= p.get("at", "") <= t1: continue
        to = p.get("to") or []
        if isinstance(to, str): to = to.split()
        to = [n for n in (str(x).lstrip("@").lower() for x in to) if AGENT.fullmatch(n)]
        if not to: continue
        th = p.get("thread") or p.get("id")
        try: tp = dt.datetime.fromisoformat(p["at"]).timestamp()
        except (KeyError, ValueError): continue
        for j, q in by_th.get(th, []):
            if j <= i or str(q.get("from", "")).lower() != to[0]: continue
            try: out.append(dt.datetime.fromisoformat(q["at"]).timestamp() - tp)
            except (KeyError, ValueError): pass
            break
    return sorted(out)

def pct(xs, f): return round(xs[min(len(xs) - 1, int(len(xs) * f))]) if xs else None

def watchdog_in(t0, t1):
    try: st = json.loads((STATE / "watchdog.json").read_text())
    except (OSError, ValueError): return None
    a, b = dt.datetime.fromisoformat(t0).timestamp(), dt.datetime.fromisoformat(t1).timestamp()
    return sum(1 for k, v in st.get("fired", {}).items() if a <= v <= b)

def stalled_now():
    try:
        out = subprocess.run([sys.executable, str(Hive), "stalled"], capture_output=True, text=True, timeout=60).stdout
        return sum(1 for l in out.splitlines() if l and not l.startswith(" "))
    except (OSError, subprocess.TimeoutExpired): return None

def cost_since(t0):
    """What hive-cost (module cost) says the hive's model use since t0 would cost at API list prices; None without the module."""
    exe = HOME / ".local" / "bin" / "hive-cost"
    if not exe.exists(): exe = HOME / "modules" / "cost" / "cost.py"
    if not exe.exists(): return None
    try:
        out = subprocess.run([str(exe), "--since", t0, "--by", "day", "--json"], capture_output=True, text=True, timeout=120).stdout
        return float(json.loads(out).get("total_usd", 0))
    except (OSError, ValueError, subprocess.TimeoutExpired, TypeError, AttributeError): return None

def commits_in(t0, t1):
    try:
        out = subprocess.run(["git", "log", f"--since={t0}", f"--until={t1}", "--oneline"], cwd=CORE, capture_output=True, text=True, timeout=30).stdout
        return len(out.splitlines())
    except OSError: return None

def projects_unmeasured():
    bad = []
    for g in sorted(PROJECTS.glob("*/goals.md")):
        try: txt = g.read_text(errors="replace")
        except OSError: continue
        if not MEASURE.search(txt): bad.append(g.parent.name)
    return bad

def focus_quiet():
    """From core/goals.py: the person's focuses (priorities.md) with no agent post for 4 h and nobody running. The long-horizon work is what goes quiet first (2026-11-05: 6 of 8 focuses silent 11-20 h while 35 short runs an hour cleared asks)."""
    try:
        spec = importlib.util.spec_from_file_location("goals", CORE / "goals.py"); gl = importlib.util.module_from_spec(spec); spec.loader.exec_module(gl)
        return gl.focus_quiet()
    except Exception: return None

def events_in(log, t0, t1, ev): return sum(1 for r in log if r.get("event") == ev and t0 <= r.get("at", "") <= t1)

def model_steps(posts, t0, t1, way):
    """Agents' own moves between models (hive model posts), leaving out the ones another post told them to make."""
    return sum(1 for p in posts if t0 <= p.get("at", "") <= t1 and re.match(rf"(Switching to|Next wake on) \S+ \({way} from ", str(p.get("text", ""))) and "picked for the post above" not in p["text"])

def for_you_numbers(posts, t0, t1):
    """For you lines from agents in the window and how many read as more than a plain ask (core/foryou.py)."""
    try:
        spec = importlib.util.spec_from_file_location("foryou", Path(__file__).resolve().parent / "foryou.py"); f = importlib.util.module_from_spec(spec); spec.loader.exec_module(f)
        n, flagged = f.tally(posts, t0, t1)
    except Exception: return {}
    return {"for_you_lines": n, "for_you_flagged": flagged, "for_you_flagged_pct": round(100.0 * flagged / n, 1) if n else 0.0}

def summary_numbers():
    """From the dash module's lint: stale, duplicate and wrong-owner items in Where things stand, and threads open on the person that it lacks. None where there's no dash module."""
    try:
        spec = importlib.util.spec_from_file_location("needs_you", HOME / "modules" / "dash" / "needs_you.py"); n = importlib.util.module_from_spec(spec); spec.loader.exec_module(n)
        return {"summary_flagged": n.flagged_count(), "needs_gap": n.needs_gap()}
    except Exception: return {"summary_flagged": None, "needs_gap": None}

def window(t0, t1, log, posts, items):
    runs, failed, reaped = runs_in(log, t0, t1)
    nposts, nperson, noticed = board_in(posts, t0, t1)
    rs = reply_s(posts, t0, t1)
    landed = [e for e in items.values() if t0 <= e.get("at", "") <= t1]
    judged = [e for e in items.values() if t0 <= e.get("judged_at", "") <= t1]
    rsi = [r for r in jsonl(RSI_LEDGER) if t0 <= r.get("at", "") <= t1]
    w = {"from": t0, "to": t1, "runs": runs, "runs_failed": failed, "runs_reaped": reaped, "runs_woken": events_in(log, t0, t1, "woken"),
         "fail_pct": round(100.0 * failed / runs, 1) if runs else 0.0,
         "posts": nposts, "person_posts": nperson, "person_noticed_first": noticed, "watchdog_trips": watchdog_in(t0, t1),
         "person_reply_p50_s": pct(rs, 0.5), "person_reply_p90_s": pct(rs, 0.9), "person_replies": len(rs),
         "keeper_rounds": events_in(log, t0, t1, "keeper"), "improve_rounds": events_in(log, t0, t1, "improve"),
         "improvements_landed": len(landed), "improvements_judged": len(judged), **for_you_numbers(posts, t0, t1),
         "held": sum(1 for e in judged if e.get("status") == "held"), "reverted": sum(1 for e in judged if e.get("status") == "reverted"),
         "asks_done": asks_done_in(t0, t1), **leftovers_flow(items, t0, t1), "rsi_rounds": len(rsi), "rsi_wins": sum(1 for r in rsi if r.get("verdict") == "win"),
         "core_commits": commits_in(t0, t1), **outcomes_in(t0, t1), **kb_numbers(t0, t1),
         "model_steps_up": model_steps(posts, t0, t1, "up"), "model_steps_down": model_steps(posts, t0, t1, "down")}
    return w

def outcomes_in(t0, t1):
    """From ~/outcomes.jsonl (core/outcomes.py): of the runs graded in the window, the share that delivered what their post asked, and how many were silent though named."""
    try:
        spec = importlib.util.spec_from_file_location("outcomes", CORE / "outcomes.py"); oc = importlib.util.module_from_spec(spec); spec.loader.exec_module(oc)
        s = oc.stats([r for r in jsonl(HOME / "outcomes.jsonl") if t0 <= r.get("at", "") <= t1])
    except Exception: return {"outcomes_graded": 0, "outcome_delivered_pct": None, "outcomes_silent": None, "outcomes_nothing": None}
    if not s["outcomes_graded"]: return {"outcomes_graded": 0, "outcome_delivered_pct": None, "outcomes_silent": None, "outcomes_nothing": None}  # no grades, so no count: "0 -> 11, worse" on the grader's first day was a false verdict
    return {k: s[k] for k in ("outcomes_graded", "outcome_delivered_pct", "outcomes_silent", "outcomes_nothing")}

def kb_numbers(t0, t1):
    """From the kb query log (core/kbuse.py): searches, how many led to an observed read of what they showed, recall pointers, misses."""
    try:
        spec = importlib.util.spec_from_file_location("kbuse", CORE / "kbuse.py"); ku = importlib.util.module_from_spec(spec); spec.loader.exec_module(ku)
        x = ku.numbers(t0[:19], t1[:19])
    except Exception: return {}
    return {"kb_finds": x["finds"], "kb_find_read_pct": x["find_read_pct"], "kb_recall_injections": x["recalls"], "kb_recall_opened_pct": x["recall_opened_pct"], "kb_misses_explicit": x["misses"], "kb_miss_candidates": x["candidates"]}

LOWER_IS_BETTER = {"fail_pct", "runs_reaped", "person_noticed_first", "person_reply_p50_s", "person_reply_p90_s", "watchdog_trips", "stalled_threads", "improvements_overdue", "projects_unmeasured", "asks_orphaned", "leftovers_orphaned", "asks_stale", "asks_unowned", "leftovers_unowned", "asks_oldest_open_d", "outcomes_silent", "outcomes_nothing", "focus_quiet", "for_you_lines", "for_you_flagged", "for_you_flagged_pct", "person_asks", "summary_flagged", "needs_gap"}
NEUTRAL = {"kb_finds", "kb_find_read_pct", "kb_recall_injections", "kb_recall_opened_pct", "kb_misses_explicit", "kb_miss_candidates", "model_steps_up", "model_steps_down", "runs", "runs_woken", "person_replies", "keeper_rounds", "improve_rounds", "core_commits", "rsi_rounds", "reverted", "spend_usd", "asks_open", "leftovers_open", "leftovers_filed", "leftovers_closed", "outcomes_graded"}  # activity and cost: context, not a score (the person: don't ration work for pace)
HEADLINE = ["kb_finds", "kb_find_read_pct", "kb_recall_injections", "kb_recall_opened_pct", "kb_misses_explicit", "kb_miss_candidates", "runs", "model_steps_up", "model_steps_down", "fail_pct", "runs_reaped", "runs_woken", "person_noticed_first", "person_reply_p50_s", "person_reply_p90_s", "watchdog_trips", "stalled_threads", "improvements_landed", "improvements_judged", "held", "reverted",
            "improvements_overdue", "focus_quiet", "for_you_lines", "for_you_flagged", "for_you_flagged_pct", "person_asks", "summary_flagged", "needs_gap", "asks_open", "leftovers_open", "leftovers_filed", "leftovers_closed", "asks_orphaned", "leftovers_orphaned", "asks_stale", "asks_unowned", "leftovers_unowned", "asks_done", "asks_oldest_open_d", "outcome_delivered_pct", "outcomes_silent", "outcomes_nothing", "outcomes_graded", "rsi_rounds", "rsi_wins", "keeper_rounds", "improve_rounds", "core_commits", "projects_unmeasured", "spend_usd"]

def scoreboard(since):
    log, posts, items = jsonl(LOG), jsonl(BOARD), ledger()
    d = span(since); t1 = now(); t0 = t1 - d; tp = t0 - d
    cur = window(iso(t0), iso(t1), log, posts, items)
    prev = window(iso(tp), iso(t0), log, posts, items)
    both, cur_cost = cost_since(iso(tp)), cost_since(iso(t0))
    cur["spend_usd"] = None if cur_cost is None else round(cur_cost, 2)
    prev["spend_usd"] = None if both is None or cur_cost is None else round(both - cur_cost, 2)
    od = overdue(items)
    fq = focus_quiet()
    cur.update(stalled_threads=stalled_now(), improvements_overdue=len(od), overdue_ids=[e["id"] for e in od],
               focus_quiet=None if fq is None else len(fq), focus_quiet_threads=fq or [],
               projects_unmeasured=len(projects_unmeasured()), projects_unmeasured_names=projects_unmeasured(), at=iso(t1), since=since, **summary_numbers(), **asks_numbers(iso(t1)))
    snaps = jsonl(SNAPS)
    older = next((s for s in reversed(snaps) if s.get("since") == since and s.get("at", "") < iso(t0)), None)
    for k in ("stalled_threads", "improvements_overdue", "focus_quiet", "projects_unmeasured", "person_asks", "summary_flagged", "needs_gap", "asks_open", "leftovers_open", "asks_orphaned", "leftovers_orphaned", "asks_stale", "asks_unowned", "leftovers_unowned", "asks_oldest_open_d"):
        if older and k in older: prev[k] = older[k]
    return cur, prev

def asks_numbers(t1):
    """From the asks register (core/asks.py): open and owned, orphaned (nobody moving them for 6 h), unowned (open with no owner for 6 h, busy thread or not), oldest open in days."""
    try:
        spec = importlib.util.spec_from_file_location("asks", CORE / "asks.py"); asks = importlib.util.module_from_spec(spec); spec.loader.exec_module(asks)
        items = asks.ledger()
    except Exception: return {}
    o = [e for e in asks.open_items(items) if e.get("status") in ("open", "owned") and e.get("kind") != "direction"]
    oldest = min((e.get("when") or e.get("at") for e in o), default=None)
    t = dt.datetime.fromisoformat(t1)
    stale = getattr(asks, "stale_items", lambda items, t=None: [])(items, t=t)
    orph = [x[0] for x in asks.orphaned(items, t=t)]
    un = getattr(asks, "unowned_items", lambda items, t=None: [])(items, t=t)
    return {"asks_open": len(o), "person_asks": sum(1 for e in items.values() if e.get("status") == "person" and e.get("kind") != "direction"), "asks_orphaned": sum(1 for e in orph if e.get("kind") != "leftover"), "asks_stale": len(stale),
            "asks_unowned": sum(1 for e in un if e.get("kind") != "leftover"), "leftovers_unowned": sum(1 for e in un if e.get("kind") == "leftover"),
            "leftovers_open": sum(1 for e in o if e.get("kind") == "leftover"), "leftovers_orphaned": sum(1 for e in orph if e.get("kind") == "leftover"),
            "asks_oldest_open_d": None if not oldest else round((t - dt.datetime.fromisoformat(oldest)).total_seconds() / 86400, 1)}

def leftovers_flow(items, t0, t1):
    """Leftovers filed and closed (done or dropped) in the window, from the asks register, so leftovers_open can be read as a rate."""
    try:
        spec = importlib.util.spec_from_file_location("asks", CORE / "asks.py"); asks = importlib.util.module_from_spec(spec); spec.loader.exec_module(asks)
        L = [e for e in asks.ledger().values() if e.get("kind") == "leftover"]
    except Exception: return {}
    return {"leftovers_filed": sum(1 for e in L if t0 <= e.get("at", "") <= t1), "leftovers_closed": sum(1 for e in L if e.get("status") in ("done", "dropped") and t0 <= (e.get("done_at") or "") <= t1)}

def asks_done_in(t0, t1):
    try: rows = jsonl(HOME / "asks.jsonl")
    except Exception: return 0
    return sum(1 for r in rows if r.get("status") == "done" and t0 <= r.get("at", "") <= t1)

def worst(cur, prev):
    """The numbers that got worse, worst first (by relative change), for the keeper to pick from."""
    out = []
    for k in HEADLINE:
        a, b = cur.get(k), prev.get(k)
        if a is None or b is None or k in NEUTRAL: continue
        d = (a - b) if k in LOWER_IS_BETTER else (b - a)
        if d > 0: out.append((d / (abs(b) + 1), k, b, a))
    return sorted(out, reverse=True)

def text(cur, prev):
    lines = [f"Hive progress, last {cur['since']} vs the {cur['since']} before (snapshot {cur['at'][:16]}Z)", "", "| number | before | now | |", "|---|---|---|---|"]
    for k in HEADLINE:
        a, b = cur.get(k), prev.get(k)
        mark = ""
        if a is not None and b is not None and a != b and k not in NEUTRAL:
            better = (a < b) if k in LOWER_IS_BETTER else (a > b)
            mark = "better" if better else "worse"
        lines.append(f"| {k} | {'-' if b is None else b} | {'-' if a is None else a} | {mark} |")
    if cur.get("overdue_ids"): lines.append(f"\nOverdue for judgement: {', '.join(cur['overdue_ids'])} (`hive improve list --due`)")
    if cur.get("asks_orphaned"): lines.append(f"Asks of the person nobody is moving: {cur['asks_orphaned']} (`hive asks check`)")
    if cur.get("leftovers_orphaned"): lines.append(f"Work agents left undone that nobody is moving: {cur['leftovers_orphaned']} (`hive asks check`, kind leftover)")
    if cur.get("asks_stale"): lines.append(f"Open asks over 3 days old in busy threads: {cur['asks_stale']} (`hive asks check`, stale section)")
    if cur.get("asks_unowned") or cur.get("leftovers_unowned"): lines.append(f"Open asks with no owner for over 6 h in busy threads: {cur.get('asks_unowned', 0)} of the person, {cur.get('leftovers_unowned', 0)} left by agents (`hive asks check`, unowned section)")
    if cur.get("outcomes_silent") or cur.get("outcomes_nothing"): lines.append(f"Runs that were asked something and delivered nothing: {cur.get('outcomes_nothing', 0)} posted but did not deliver, {cur.get('outcomes_silent', 0)} silent though named (`hive outcomes list --grade nothing`)")
    if cur.get("summary_flagged"): lines.append(f"Where things stand has {cur['summary_flagged']} stale, duplicate or wrong-owner items (`hive summary lint`)")
    if cur.get("needs_gap"): lines.append(f"Threads open on the person that Where things stand lacks: {cur['needs_gap']} (`hive summary lint`, count finding)")
    if cur.get("focus_quiet"): lines.append(f"Focuses of the person with nobody on them for over 4 h: {cur['focus_quiet']} ({', '.join(cur.get('focus_quiet_threads', []))}; `hive goals --idle`, start a fresh agent on each)")
    if cur.get("projects_unmeasured_names"): lines.append(f"Projects whose goals.md names no measure: {', '.join(cur['projects_unmeasured_names'])}")
    w = worst(cur, prev)
    if w: lines.append("Got worse, worst first: " + ", ".join(f"{k} {b}->{a}" for _, k, b, a in w[:4]))
    return "\n".join(lines)

def cmd_progress(a):
    cur, prev = scoreboard(a.since)
    if a.snapshot: append(SNAPS, cur)
    if a.json: print(json.dumps({"now": cur, "before": prev, "worse": worst(cur, prev)}, indent=1)); return
    t = text(cur, prev)
    print(t)
    if a.post:
        subprocess.run([sys.executable, str(Hive), "post", "--thread", "progress"], input=t, text=True,
                       env={**os.environ, "HIVE_NAME": os.environ.get("HIVE_NAME", "progress")})

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive improve / hive progress", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(required=True)
    p = sp.add_parser("log", help="log a landed change: --what, --measure (required), --revisit 7d|YYYY-MM-DD, --where, --hypothesis, --baseline, --commit, --thread")
    for f in ("what", "measure", "revisit", "where", "hypothesis", "baseline", "commit", "thread"): p.add_argument("--" + f)
    p.set_defaults(f=cmd_log)
    p = sp.add_parser("judge", help="judge a landed change on its revisit date: ID held|reverted|inconclusive --why 'the number vs baseline'")
    p.add_argument("id"); p.add_argument("verdict", choices=VERDICTS); p.add_argument("--why", default=""); p.add_argument("--self", action="store_true"); p.set_defaults(f=cmd_judge)
    p = sp.add_parser("list", help="the ledger; --due for revisits past their date"); p.add_argument("--due", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("show", help="one entry in full"); p.add_argument("id"); p.set_defaults(f=cmd_show)
    p = sp.add_parser("progress", help="the scoreboard: --since 1d (default), --snapshot appends to ~/progress.jsonl, --post puts it in thread progress, --json")
    p.add_argument("--since", default="1d"); p.add_argument("--snapshot", action="store_true"); p.add_argument("--post", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_progress)
    a = ap.parse_args(argv); a.f(a)

if __name__ == "__main__": main()
