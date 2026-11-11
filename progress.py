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
import importlib.util, argparse, datetime as dt, fcntl, json, math, os, re, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
LEDGER, SNAPS, LOG, BOARD = HOME / "improvements.jsonl", HOME / "progress.jsonl", HOME / "log.jsonl", HOME / "board.jsonl"
CORE, STATE, PROJECTS = HOME / "core", HOME / ".hive", HOME / "projects"
Hive = next((CORE / n for n in ("hive", "hive") if (CORE / n).exists()), CORE / "hive")  # an installed Hive's CLI is called hive
RSI_LEDGER = PROJECTS / "rsi-loop" / "ledger.jsonl"
VERDICTS = ("held", "reverted", "inconclusive", "reverted-candidate", "unjudgeable")  # reverted-candidate is the script's; unjudgeable: nothing could ever credit this one change: the number got worse, the owner decides
MEASURE = re.compile(r"measure|metric|score|yardstick|benchmark|passes|pass rate|p&l|sharpe", re.I)

CHECK_TIMEOUT_S = 120


def woke_thread():
    """HIVE_THREAD, else the agent's run record (a warm agent was booted before its post; a boxed agent's env never had it): as hive's woke_thread."""
    if (t := os.environ.get("HIVE_THREAD")): return t
    n = os.environ.get("HIVE_NAME", "").lower()
    if not re.fullmatch(r"[a-z0-9_-]+", n): return ""
    try: r = json.loads(open(os.path.expanduser(f"~/run/{n}.json")).read())
    except (OSError, ValueError): return ""
    return r.get("thread") or r.get("post") or ""

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

def due_groups(items, t=None):
    """Overdue landings, one list per item: landings linked by --also or a shared number (shares) are listed together,
    each only once its own revisit date has come."""
    today, out, seen = (t or now()).date().isoformat(), [], set()
    for e in sorted(items.values(), key=lambda e: e.get("at", "")):
        if e["id"] in seen or e.get("status") != "landed": continue
        g = [items[i] for i in [e["id"]] + judged_with(items, e) if i in items and items[i].get("status") == "landed"]
        seen.update(x["id"] for x in g)
        g = [x for x in g if x.get("due_at", "") <= iso(t) and (x.get("revisit") or "9999") <= today]  # a check runs at its hour, not at the start of its day
        if g: out.append(g)
    return out

def overdue(items, t=None):
    return [e for g in due_groups(items, t) for e in g]

def next_id(items):
    n = max((int(i[1:]) for i in items if re.fullmatch(r"i\d+", i)), default=0) + 1
    return f"i{n:04d}"

OVERLAP_DAYS = 7  # floor: a landing this recent counts as open even when its revisit date has passed unjudged

def named_numbers(measure):
    """The numbers a measure names: snake_case names (stalled_threads, kb_recall_opened_pct), not script names (x_y.py)."""
    return set(re.findall(r"\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b(?!\.py)", (measure or "").lower()))

def overlaps(items, measure, t=None, skip=()):
    """Open landings whose measure names a number this measure names: {id: [numbers]}.
    Open = status landed and its revisit still ahead, or landed within OVERLAP_DAYS (revisit passed but unjudged)."""
    t = t or now()
    mine, t0, today, out = named_numbers(measure), t - dt.timedelta(days=OVERLAP_DAYS), t.date().isoformat(), {}
    if not mine: return out
    for e in items.values():
        if e.get("status") != "landed" or e["id"] in skip: continue
        shared = mine & named_numbers(e.get("measure"))
        if not shared: continue
        try: at = dt.datetime.fromisoformat(e.get("at", "").replace("Z", "+00:00"))
        except ValueError: at = None
        if str(e.get("revisit", "")) >= today or (at and at >= t0): out[e["id"]] = sorted(shared)
    return out

def links(e): return set(e.get("also") or []) | set(e.get("shares") or [])

def same_check(items, x):
    """Rows reading the very same check command over a window that overlaps x's: one number, so it can't credit either."""
    e = items.get(x, {})
    if not e.get("check"): return set()
    return {o["id"] for o in items.values() if o["id"] != x and o.get("check") == e["check"] and o.get("at", "") <= e.get("due_at", "") and e.get("at", "") <= o.get("due_at", "")}

def judged_with(items, e):
    """Ids judged together with e: its own --also and shares, rows on the same check, entries that named it in theirs, and theirs in turn."""
    seen, todo = {e["id"]}, [e["id"]]
    while todo:
        x = todo.pop()
        near = links(items.get(x, {})) | same_check(items, x) | {o["id"] for o in items.values() if x in links(o)}
        todo += sorted(near - seen); seen |= near
    return sorted(seen - {e["id"]})

# ---------- the measure as a command: a script judges it ----------

def run_check(cmd):
    """(number, None), or (None, why not). The check must print exactly one number; it runs in ~ with nothing but HOME and PATH."""
    env = {"HOME": str(HOME), "PATH": f"{HOME}/.local/bin:{CORE}:" + os.environ.get("PATH", "/usr/bin:/bin"), "LANG": "C.UTF-8"}
    try: r = subprocess.run(["bash", "-c", cmd], cwd=HOME, env=env, capture_output=True, text=True, timeout=CHECK_TIMEOUT_S)
    except (OSError, subprocess.SubprocessError) as e: return None, f"did not finish: {type(e).__name__}"
    if r.returncode: return None, f"exit {r.returncode}: {r.stderr.strip()[-120:]}"
    try: n = float(r.stdout.strip())
    except ValueError: return None, f"printed {r.stdout.strip()[:60]!r}, not one number"
    return (n, None) if math.isfinite(n) else (None, f"printed {n}")

def read_check(e):
    """((number, control number or None), None), or (None, why not)."""
    n, err = run_check(e["check"])
    if n is None or not e.get("control"): return ((n, None), None) if n is not None else (None, err)
    c, err = run_check(e["control"])
    return ((n, c), None) if c is not None else (None, f"control: {err}")

def score(base, after, want, band=None):
    """The verdict for a move from base to after. band is the move that counts as noise: 5% of the baseline unless stated.
    Inside it a number says nothing, so inconclusive, except a count that wanted down and stayed at 0."""
    band = abs(base) * 0.05 if band is None else band
    gain = (after - base) if want == "up" else (base - after)
    if gain > band: return "held"
    if gain < -band: return "reverted-candidate"
    return "held" if want == "down" and base == after == 0 else "inconclusive"

def check_fields(a):
    if a.want not in ("up", "down"): sys.exit("hive improve: --check needs --want up|down, the way the number should move.")
    return {"check": a.check, "want": a.want, **({"band": a.band} if a.band is not None else {})}

def autojudge(post=True, t=None):
    """Judge every landed change whose check is due, with no model and no agent. Runs hourly after hive outcomes grade and at
    hive improve list --due. Never reverts: a number that got worse is named to its owner in thread improve."""
    STATE.mkdir(exist_ok=True)
    with (STATE / "autojudge.lock").open("w") as lock:
        try: fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError: return []  # another run is on it
        t, items, out = t or now(), ledger(), []
        for e in sorted(items.values(), key=lambda e: e.get("at", "")):
            if e.get("status") != "landed" or not e.get("check") or e.get("due_at", "") > iso(t) or str(e.get("revisit") or "") > iso(t)[:10]: continue
            base, (after, err) = e.get("before"), read_check(e)
            try: retries_over = t - dt.datetime.fromisoformat(e["due_at"]) >= dt.timedelta(days=1)
            except (KeyError, ValueError): retries_over = True
            if after is None and not retries_over: continue  # a check that fails once gets a day of hourly retries
            if after is not None and base is not None and e.get("control") and e.get("control_before"): base = base * after[1] / e["control_before"]  # what the untouched thing says would have happened anyway
            control = after[1] if after is not None else None; after = after[0] if after is not None else None
            reading = "inconclusive" if after is None or base is None else score(base, after, e.get("want"), e.get("band"))
            why = (f"check failed: {err}" if after is None else "no baseline" if base is None else f"{e.get('want')}: {e.get('before'):g} -> {after:g}" + (f" (expected {base:g} from control {e['control_before']:g} -> {control:g})" if e.get("control") else "") + (f" (noise band {e['band']:g})" if e.get("band") is not None else ""))
            seen = None if after is None or base is None else (after - base if e.get("want") == "up" else base - after)
            sized = seen is not None and e.get("gain") is not None and not e.get("late")  # a check attached later is not the number the size was written for
            if sized: why += f"; sized at {e['gain']:g} before landing, came to {seen:g}" + (" net of the control" if e.get("control") else "")
            if not e.get("late"): why += "".join(f"; {label}: {str(e[k])[:100]}" for k, label in (("needs", "rests on"), ("beats", "to beat")) if e.get(k))
            w, v = judged_with(items, e), reading
            if e.get("late"): v, why = "inconclusive", why + f"; the check was written after landing, so this read {reading} is not a verdict"
            if w: v, why = "inconclusive", why + f"; shares its number with {', '.join(w)}, so this cannot credit one change"
            append(LEDGER, {"judge": e["id"], "at": iso(t), "by": "late-check" if e.get("late") else "check", "verdict": v, "reading": reading, "why": why, "before": e.get("before"), "after": after,
                            **({"gain_expected": e["gain"], "gain_seen": round(seen, 6)} if sized else {}), **({"with": w} if w else {})})
            out.append((e, v, why))
    bad = [(e, why) for e, v, why in out if v == "reverted-candidate"]
    if bad and post:
        owner = lambda e: "@" + e["by"] if re.fullmatch(r"[a-z][0-9a-f]{4}", str(e.get("by"))) else str(e.get("by"))
        body = "A check says these changes made their number worse. Nothing was reverted; each owner decides, then records it with `hive improve judge ID reverted|held --why ...`.\n" + "\n".join(f"- {e['id']} {owner(e)}: {why}. {e.get('what', '')[:100]}" for e, why in bad)
        subprocess.run([sys.executable, "-I", str(Hive), "post", "--thread", "improve"], input=body, text=True, capture_output=True, env={**os.environ, "HIVE_NAME": "improve-judge"})
    return out

def cmd_autojudge(a):
    out = autojudge(post=not a.no_post)
    for e, v, why in out: print(e["id"], v, why)
    if not out: print("no checks due")

def cmd_attach(a):
    """Give a logged change a check after the fact. Its number is recorded, but it is late-check and inconclusive, never held: the scorer is not chosen after the change."""
    e = ledger().get(a.id)
    if not e or not re.fullmatch(r"i\d+", a.id): sys.exit(f"hive improve attach: no improvement {a.id}")
    if e.get("status") != "landed": sys.exit(f"hive improve attach: {a.id} already has a verdict ({e.get('status')})")
    rec, n = {"id": a.id, "late": True, **check_fields(a)}, a.before
    if n is None:
        n, err = run_check(a.check)
        if n is None: sys.exit(f"hive improve attach: the check doesn't work yet ({err})")
    rec.update(before=n, due_at=max(iso(now() if a.before is not None else now() + span("48h")), str(e.get("revisit") or "")[:10] + "T00:00:00+00:00"))
    append(LEDGER, rec); print(f"{a.id}: check attached, before {n:g}, due {rec['due_at']}")

def cmd_num(a):
    """One number from the scoreboard's snapshots, for a --check: the latest, or the mean over --from .. --to."""
    v = [s[a.field] for s in jsonl(SNAPS) if s.get("since") == "1d" and isinstance(s.get(a.field), (int, float)) and (a.frm or "") <= s.get("at", "") and (not a.to or s.get("at", "") <= a.to)]
    if not v: sys.exit(f"hive improve num: no snapshot has {a.field} in that window")
    print(f"{sum(v) / len(v):.4g}" if a.frm or a.to else v[-1])

def cmd_log(a):
    items = ledger()
    if a.expect and not a.check:  # a gain no number reads (the person 11101138a853, 2026-11-10): written down before it lands, read by someone else at the revisit
        a.measure, a.no_check = a.measure or "judged: " + a.expect, a.no_check or "a gain read by eye, not by a number"
    if not a.measure: sys.exit("hive improve log: say how anyone will know it worked. --measure with --check CMD when a command can print the number; when the gain is one a reader sees and no number reads, --expect 'what will be visibly better, who could see it, and what would count as not better', and someone who didn't build it judges that at the revisit.")
    if a.control and not a.check: sys.exit("hive improve log: --control needs --check.")
    if not a.check and not a.no_check: sys.exit("hive improve log: give --check CMD --want up|down, a command that prints the number now, so a script judges the change at its revisit and nobody grades their own. Written later it can't be a verdict. If no command can read this number, say why with --no-check WHY; if the gain is not a number at all, use --expect instead of forcing one.")
    if a.kind == "behaviour" and a.check and not a.control: sys.exit("hive improve log: a change meant to shift behaviour is compared against something it didn't touch: add --control CMD, printing the same number for that.")
    a.needs, a.beats = ((x or "").strip() or None for x in (a.needs, a.beats))
    if a.gain is not None and not (math.isfinite(a.gain) and a.gain > 0): sys.exit("hive improve log: --gain is a size, above 0, in the check's own units; --want says which way.")
    rev, fields = a.revisit or ("48h" if a.check and a.kind != "behaviour" else "7d"), {}
    if a.check:
        fields = {**check_fields(a), **({"kind": a.kind} if a.kind else {})}; fields["before"], err = run_check(a.check)
        if err: sys.exit(f"hive improve log: the check doesn't work yet ({err}). It must print one number, now, in ~.")
        if a.control:
            fields["control"] = a.control; fields["control_before"], err = run_check(a.control)
            if err: sys.exit(f"hive improve log: the control doesn't work yet ({err}).")
        fields["due_at"] = iso(now() + span(rev)) if re.fullmatch(r"\d+[hdw]", rev) else rev + "T00:00:00+00:00"
        # The trial rule (from the fund's forecast trials, projects/rsi-lab/fund.md): the size expected, what the number rests on and the
        # cheap alternative to beat are written before landing, and the check reads once, at the cut-off. A row without them is marked, not refused.
        # --control is the do-nothing comparison, not the cheap alternative, so it does not stand in for --beats.
        if a.gain is not None: fields["gain"] = a.gain
        unsized = [k for k in ("gain", "needs", "beats") if getattr(a, k) is None] + (["cutoff"] if fields["due_at"] <= iso() else [])
        if unsized: fields["unsized"] = unsized
    fields.update({k: getattr(a, k) for k in ("needs", "beats") if getattr(a, k)})  # kept on every row: whoever judges a row no command reads wants them too
    if re.fullmatch(r"\d+[hdw]", rev): rev = (now() + span(rev)).date().isoformat()
    also = sorted({i.strip().lower() for x in (a.also or []) for i in x.split(",") if i.strip()})
    unknown = [i for i in also if i not in items]
    rec = {"id": next_id(items), "at": iso(), "by": os.environ.get("HIVE_NAME", os.environ.get("USER", "?")), "what": a.what, "where": a.where or "",
           "hypothesis": a.hypothesis or "", "measure": a.measure, "baseline": a.baseline or "", "revisit": rev, "status": "landed",
           "commit": a.commit or "", "thread": a.thread or woke_thread(), **fields}
    if also: rec["also"] = also
    if a.no_check and not a.check: rec["no_check"] = a.no_check
    if a.expect: rec["expect"] = a.expect
    if a.reviewed_by: rec["reviewed_by"] = a.reviewed_by.lstrip("@").lower(); rec["review_model"] = (a.model or "").lower()
    ov = overlaps(items, a.measure, skip=set(also))
    if ov: rec["shares"] = sorted(ov)  # linked, never refused (meta-reflect, reflect-p0.md; rsi-loop rung 133): judged together
    append(LEDGER, rec)
    print(f"{rec['id']} logged; before {fields['before']:g}; its check judges it at {fields['due_at']}, no agent needed." if a.check else f"{rec['id']} logged; revisit {rev}. Someone else judges it then: hive improve judge {rec['id']} held|reverted|inconclusive --why ...")
    if a.check:
        noise = abs(fields["before"]) * 0.05 if a.band is None else a.band
        if a.gain is not None and a.gain <= noise: print(f"note: the gain you expect ({a.gain:g}) is inside this check's noise band ({noise:g}): if the number moves by that much the check reads inconclusive, and only a move above {noise:g} reads held. A later cut-off, a steadier number, or --expect with a reader would say more.")
        missing = [k for k in fields.get("unsized", []) if k != "cutoff"]
        if missing: print("marked unsized (no " + ", ".join("--" + k for k in missing) + "): logged all the same. The verdict will say which way the number went, with nothing written beforehand to hold it against.")
        if "cutoff" in fields.get("unsized", []): print(f"note: the cut-off ({fields['due_at'][:10]}) has already come, so the check reads again within the hour and the change has had no time to show. Marked; give --revisit a later date to let it run.")
    elif a.gain is not None: print("note: --gain is read only with --check (it is a move in the check's number), so it was not kept. Say the size you expect in --expect.")
    same = sorted(same_check({**items, rec["id"]: rec}, rec["id"]))
    if same: print(f"linked: {', '.join(same)} read the same check over the same days, so a held here is recorded as inconclusive. Give it a number of its own to judge it apart.")
    if unknown: print(f"warning: --also {', '.join(unknown)}: not in the ledger (hive improve list); the overlap is stored as given", file=sys.stderr)
    if ov:
        print(f"linked: {rec['id']}'s measure names a number open landings already name: "
              + "; ".join(f"{i} ({', '.join(n)})" for i, n in sorted(ov.items()))
              + ". One revisit cannot credit one change, so a held on any of them is recorded as inconclusive."
              " To judge it apart, give it a number of its own (an A/B arm, a sub-number) in the measure.")

def cmd_judge(a):
    items = ledger()
    e = items.get(a.id)
    if not e: sys.exit(f"hive improve judge: no improvement {a.id}")
    me = os.environ.get("HIVE_NAME", os.environ.get("USER", "?"))
    w = [i for i in judged_with(items, e) if items.get(i, {}).get("status") == "landed"]
    mine = [x["id"] for x in [e] + [items[i] for i in w] if x.get("by") == me]
    if mine and not a.self:
        sys.exit(f"hive improve judge: {', '.join(mine)} was logged by you" + (f" and is judged with {a.id}" if mine != [a.id] else "")
                 + ". The judge is never who proposed (the rule); pass --self only if nobody else can, and say why in --why.")
    if not a.why: sys.exit("hive improve judge: --why must give the number you looked at, against the baseline.")
    if e.get("check") and e.get("status") == "landed": sys.exit(f"hive improve judge: {a.id} has a check, and its script reads it once, at {e.get('due_at', 'its revisit')} (hive improve autojudge, hourly). No verdict by hand before that; afterwards the owner's call on what the script found goes here.")
    today = iso()[:10]
    if str(e.get("revisit") or "") > today: sys.exit(f"hive improve judge: {a.id}'s revisit is {e['revisit']}. No verdict before it.")
    w = [i for i in w if str(items[i].get("revisit") or "") <= today]
    if a.verdict == "held" and judged_with(items, e):
        a.verdict, a.why = "inconclusive", a.why + "; shares its number, so it cannot credit one change"
        print(f"{a.id} shares its number with {', '.join(judged_with(items, e))}: recorded as inconclusive, not held. Give it a number of its own to judge it apart.")
    for i in [a.id] + w:
        append(LEDGER, {"judge": i, "at": iso(), "by": me, "verdict": a.verdict, "why": a.why if i == a.id else f"judged with {a.id}: {a.why}"})
    if w: print(f"judged with {', '.join(w)}: they share a number, so this verdict is recorded for each of them.")
    print(f"{a.id}: {a.verdict}. " + ("Now revert it (or confirm it was) and say so in its thread." if a.verdict == "reverted" else
                                       "Set a later revisit with hive improve log if the number needs more time." if a.verdict == "inconclusive" else "Kept."))

def table(rows, cols):
    w = [max(len(str(r.get(c, ""))) for r in rows + [{c: c}]) for c in cols]
    print("  ".join(c.ljust(w[i]) for i, c in enumerate(cols)))
    for r in rows: print("  ".join(str(r.get(c, "")).ljust(w[i]) for i, c in enumerate(cols)))

def cmd_list(a):
    if a.due: autojudge()
    items = ledger()
    rows = due_groups(items) if a.due else list(items.values())
    if a.json and not a.due: print(json.dumps(rows, indent=1)); return
    if not rows: print("no overdue revisits" if a.due else "no improvements logged yet: hive improve log --what ... --measure ... --revisit 7d"); return
    if a.due:  # one row per item: landings judged together show as one, with both changes
        groups = due_groups(items)
        if a.json: print(json.dumps(groups, indent=1)); return
        rows = [{"id": "+".join(x["id"] for x in g), "at": g[0].get("at", "")[:10], "by": ",".join(dict.fromkeys(str(x.get("by")) for x in g)),
                 "status": "landed", "revisit": min(x.get("revisit") or "9999" for x in g),
                 "what": " | ".join(x.get("what", "")[:70 // len(g) + 10] for x in g), "measure": g[0].get("measure", "")[:50], "with": ""} for g in groups]
    else:
        rows = [{"id": e["id"], "at": e.get("at", "")[:10], "by": e.get("by"), "status": e.get("status"), "revisit": e.get("revisit", ""),
                 "what": e.get("what", "")[:70], "measure": e.get("measure", "")[:50], "with": ",".join(judged_with(items, e))}
                for e in sorted(rows, key=lambda e: e.get("at", ""))]
    table(rows, ["id", "at", "by", "status", "revisit", "what", "measure"] + (["with"] if any(r["with"] for r in rows) else []))
    if not a.due:
        d = due_groups(items)
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
    # A run stopped by its provider's usage limit is held and resumed (or handed to an heir) by the starter: not a
    # failure. Its "limited" event names the run but may carry another session id (a codex thread id), so match on
    # name and time: a limited event for the same name within 60 s after the end. (2026-11-07: 32 codex runs stopped
    # on Codex's limit read as fail_pct 0.0 -> 8.2.)
    lim = {}
    for r in log:
        if r.get("event") == "limited" and r.get("name"): lim.setdefault(r["name"], []).append(r.get("at", ""))
    def limited(e):
        try: te = dt.datetime.fromisoformat(e.get("at", "")).timestamp()
        except ValueError: return False
        for a in lim.get(e.get("name"), []):
            try: ta = dt.datetime.fromisoformat(a).timestamp()
            except ValueError: continue
            if 0 <= ta - te <= 60: return True
        return False
    bad = [r for r in ends if r.get("exit") not in (0, None) and not resumed(r)]
    nlim = sum(1 for r in bad if limited(r))
    reaped = sum(1 for r in log if r.get("event") == "reaped" and t0 <= r.get("at", "") <= t1)
    return len(starts), len(bad) - nlim, reaped, nlim

def reaped_repeat(log, t0, t1):
    """Reaps of an agent already reaped within the 3 h before (the reaper doubles that agent's quiet time, i0156), counted in the window."""
    last, n = {}, 0
    for r in sorted((r for r in log if r.get("event") == "reaped"), key=lambda r: r.get("at", "")):
        try: t = dt.datetime.fromisoformat(r["at"]).timestamp()
        except (KeyError, ValueError): continue
        if r.get("name") in last and t - last[r["name"]] <= 3 * 3600 and t0 <= r["at"] <= t1: n += 1
        last[r.get("name")] = t
    return n

def reaped_signalled(log, posts, t0, t1):
    """Reaps of an agent whose latest post before the reap ended with a Waiting: line (it said what it waited on), against bare parked shells."""
    n = 0
    bar_rows = jsonl(HOME / ".hive" / "thread-status.jsonl")
    for r in log:
        if r.get("event") != "reaped" or not (t0 <= r.get("at", "") <= t1): continue
        mine = [p for p in posts if p.get("from") == r.get("name") and p.get("at", "") <= r.get("at", "")]
        if mine and re.search(r"(?mi)^\s*waiting:", max(mine, key=lambda p: p["at"]).get("text", "")): n += 1; continue
        try: tr = dt.datetime.fromisoformat(r["at"]).timestamp()
        except (KeyError, ValueError): continue
        # the stop gate also takes the reason from the status bar's last line (`hive status`), so a bar that says Waiting: counts
        bars = [b for b in bar_rows if str(b.get("from", "")).lower() == r.get("name") and b.get("at", 0) <= tr]
        if bars and (str(max(bars, key=lambda b: b["at"]).get("text", "")).strip().splitlines() or [""])[-1].lower().startswith("waiting:"): n += 1
    return n

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
        kinds = {f"for_you_{k}": v for k, v in f.kind_tally(posts, t0, t1).items()}
    except Exception: return {}
    return {"for_you_lines": n, "for_you_flagged": flagged, "for_you_flagged_pct": round(100.0 * flagged / n, 1) if n else 0.0, "decided_lines": decided_lines(posts, t0, t1), **reversal_numbers(posts, t0, t1), **kinds}

DECIDED_LINE = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?decided(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*\S", re.I | re.M)

def decided_lines(posts, t0, t1):
    """`Decided:` lines from agents in the window (hive-meta P4; the convention's measure against person_asks). Same agent filter as For you: not the person, not the hive's own threads."""
    return sum(len(DECIDED_LINE.findall(str(p.get("text", "")))) for p in posts if t0 <= p.get("at", "") <= t1 and p.get("from") not in ("person", "starter", "keeper") and p.get("thread") not in ("keeper", "improve", "watchdog"))

REVERSE = re.compile(r"\b(revers\w*|undo|revert\w*|instead|don'?t do|do not|wrong|no,|stop doing|take (?:that|it) back)\b", re.I)

def reversal_numbers(posts, t0, t1, within_h=24):
    """Reversals per 100 `Decided:` lines (hive-meta P4): decisions in the window whose thread got a person post within 24 h that reads as a reversal (keyword match, so a count to watch, not a verdict). Empty when there are no decisions."""
    skip = ("person", "starter", "keeper"); n = rev = 0
    stamp = lambda a: dt.datetime.fromisoformat(str(a)[:19]).timestamp()
    for p in posts:
        if not t0 <= p.get("at", "") <= t1 or p.get("from") in skip or p.get("thread") in ("keeper", "improve", "watchdog"): continue
        k = len(DECIDED_LINE.findall(str(p.get("text", ""))))
        if not k: continue
        n += k; t = stamp(p["at"])
        if any(q.get("from") == "person" and q.get("thread") == p.get("thread") and 0 < stamp(q["at"]) - t <= within_h * 3600 and REVERSE.search(str(q.get("text", ""))) for q in posts if q.get("at", "") > p["at"]): rev += k
    return {"reversals_per_100": round(100.0 * rev / n, 1) if n else 0.0} if n else {}

def summary_numbers():
    """From the dash module's lint: stale, duplicate and wrong-owner items in Where things stand, and threads open on the person that it lacks. None where there's no dash module."""
    try:
        spec = importlib.util.spec_from_file_location("needs_you", HOME / "modules" / "dash" / "needs_you.py"); n = importlib.util.module_from_spec(spec); spec.loader.exec_module(n)
        c = n.flagged_count()
        if c is not None and c > 5:   # a high count (22 at 2026-11-10 21:08Z; 2 at 22:55Z, nothing measured between): keep the findings so the cause can be read
            try:
                away = n.core("away"); cur = n.load_json(n.OUT, {}) or {}
                f = [x for x in n.lint(cur.get("brief") or {}, [], n.core("asks").ledger(), list(away.lines(HOME / "board.jsonl")), n.status_bars(), time.time(), links=away.needs_order()[1]) if x["kind"] in n.LINT_FOCUS]
                with open(HOME / ".cache" / "summary-flagged-spikes.jsonl", "a") as fh: fh.write(json.dumps({"at": dt.datetime.utcnow().isoformat()[:19], "n": c, "findings": [str(x)[:200] for x in f]}) + "\n")
            except Exception: pass
        return {"summary_flagged": c, "needs_gap": n.needs_gap()}
    except Exception: return {"summary_flagged": None, "needs_gap": None}

def indep_review(e):
    """A ledger entry that records a review by someone other than the builder, on opus or fable (the Nov 6 rule: a design is reviewed on a stronger model by an agent that did not build it)."""
    r = (e.get("reviewed_by") or "").lstrip("@").lower()
    # the reviewer often logs its own review (by == reviewed_by), so that is allowed; the entry's builder is not recorded, independence is the logger's claim
    return bool(r) and e.get("review_model") in ("opus", "fable")

def window(t0, t1, log, posts, items):
    runs, failed, reaped, limited = runs_in(log, t0, t1)
    nposts, nperson, noticed = board_in(posts, t0, t1)
    rs = reply_s(posts, t0, t1)
    landed = [e for e in items.values() if t0 <= e.get("at", "") <= t1]
    judged = [e for e in items.values() if t0 <= e.get("judged_at", "") <= t1 and e.get("judged_by") != "late-check" and e.get("status") != "unjudgeable"]
    rsi = [r for r in jsonl(RSI_LEDGER) if t0 <= r.get("at", "") <= t1]
    w = {"from": t0, "to": t1, "runs": runs, "runs_failed": failed, "runs_reaped": reaped, "runs_reaped_signalled": reaped_signalled(log, posts, t0, t1), "runs_reaped_repeat": reaped_repeat(log, t0, t1), "runs_limited": limited, "runs_woken": events_in(log, t0, t1, "woken"),
         "fail_pct": round(100.0 * failed / runs, 1) if runs else 0.0,
         "posts": nposts, "person_posts": nperson, "person_noticed_first": noticed, "watchdog_trips": watchdog_in(t0, t1),
         "person_reply_p50_s": pct(rs, 0.5), "person_reply_p90_s": pct(rs, 0.9), "person_replies": len(rs),
         "keeper_rounds": events_in(log, t0, t1, "keeper"), "improve_rounds": events_in(log, t0, t1, "improve"),
         "improvements_landed": len(landed), "improvements_judged": len(judged), "improvements_sized": sum(1 for e in landed if e.get("check") and e.get("gain") is not None and not e.get("unsized")), "reviews_independent": sum(1 for e in landed if indep_review(e)), **for_you_numbers(posts, t0, t1),
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
    except Exception: return {"outcomes_graded": 0, "outcome_delivered_pct": None, "outcomes_silent": None, "outcomes_nothing": None, "outcomes_intent_only": None}
    if not s["outcomes_graded"]: return {"outcomes_graded": 0, "outcome_delivered_pct": None, "outcomes_silent": None, "outcomes_nothing": None, "outcomes_intent_only": None}  # no grades, so no count: "0 -> 11, worse" on the grader's first day was a false verdict
    return {k: s[k] for k in ("outcomes_graded", "outcome_delivered_pct", "outcomes_silent", "outcomes_nothing", "outcomes_intent_only")}

def kb_numbers(t0, t1):
    """From the kb query log (core/kbuse.py): searches, how many led to an observed read of what they showed, recall pointers, misses."""
    try:
        spec = importlib.util.spec_from_file_location("kbuse", CORE / "kbuse.py"); ku = importlib.util.module_from_spec(spec); spec.loader.exec_module(ku)
        x = ku.numbers(t0[:19], t1[:19])
    except Exception: return {}
    return {"kb_finds": x["finds"], "kb_find_read_pct": x["find_read_pct"], "kb_recall_injections": x["recalls"], "kb_recall_opened_pct": x["recall_opened_pct"], "kb_misses_explicit": x["misses"], "kb_miss_candidates": x["candidates"]}

LOWER_IS_BETTER = {"fail_pct", "runs_reaped", "runs_reaped_repeat", "person_noticed_first", "person_reply_p50_s", "person_reply_p90_s", "watchdog_trips", "stalled_threads", "improvements_overdue", "projects_unmeasured", "asks_orphaned", "leftovers_orphaned", "asks_stale", "asks_unowned", "leftovers_unowned", "asks_oldest_open_d", "outcomes_silent", "outcomes_nothing", "outcomes_intent_only", "focus_quiet", "for_you_lines", "for_you_flagged", "for_you_flagged_pct", "for_you_command", "for_you_many", "for_you_long", "for_you_chain", "for_you_ours", "person_asks", "summary_flagged", "needs_gap"}
NEUTRAL = {"decided_lines", "reversals_per_100", "kb_finds", "kb_find_read_pct", "kb_recall_injections", "kb_recall_opened_pct", "kb_misses_explicit", "kb_miss_candidates", "model_steps_up", "model_steps_down", "runs", "runs_woken", "person_replies", "keeper_rounds", "improve_rounds", "core_commits", "rsi_rounds", "reverted", "spend_usd", "asks_open", "leftovers_open", "leftovers_filed", "leftovers_closed", "outcomes_graded"}  # activity and cost: context, not a score (the person: don't ration work for pace)
HEADLINE = ["kb_finds", "kb_find_read_pct", "kb_recall_injections", "kb_recall_opened_pct", "kb_misses_explicit", "kb_miss_candidates", "runs", "model_steps_up", "model_steps_down", "fail_pct", "runs_reaped", "runs_reaped_signalled", "runs_reaped_repeat", "runs_woken", "person_noticed_first", "person_reply_p50_s", "person_reply_p90_s", "watchdog_trips", "stalled_threads", "improvements_landed", "improvements_judged", "improvements_sized", "reviews_independent", "held", "reverted",
            "improvements_overdue", "reviews_independent", "focus_quiet", "for_you_lines", "for_you_flagged", "for_you_flagged_pct", "for_you_command", "for_you_many", "for_you_long", "for_you_chain", "for_you_ours", "decided_lines", "reversals_per_100", "person_asks", "summary_flagged", "needs_gap", "asks_open", "leftovers_open", "leftovers_filed", "leftovers_closed", "asks_orphaned", "leftovers_orphaned", "asks_stale", "outcomes_intent_only", "asks_unowned", "leftovers_unowned", "asks_done", "asks_oldest_open_d", "outcome_delivered_pct", "outcomes_silent", "outcomes_nothing", "outcomes_graded", "rsi_rounds", "rsi_wins", "keeper_rounds", "improve_rounds", "core_commits", "projects_unmeasured", "spend_usd"]

def scoreboard(since):
    log, posts, items = jsonl(LOG), jsonl(BOARD), ledger()
    d = span(since); t1 = now(); t0 = t1 - d; tp = t0 - d
    cur = window(iso(t0), iso(t1), log, posts, items)
    prev = window(iso(tp), iso(t0), log, posts, items)
    both, cur_cost = cost_since(iso(tp)), cost_since(iso(t0))
    cur["spend_usd"] = None if cur_cost is None else round(cur_cost, 2)
    prev["spend_usd"] = None if both is None or cur_cost is None else round(both - cur_cost, 2)
    od = due_groups(items)
    fq = focus_quiet()
    cur.update(stalled_threads=stalled_now(), improvements_overdue=len(od), overdue_ids=[e["id"] for g in od for e in g],
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

def steer():
    """The few numbers the hive steers by, in the order the person named their pain points (the RSI lab's index; the table below is for diagnosis)."""
    try:
        r = subprocess.run([sys.executable, "-I", str(HOME / "projects" / "rsi-lab" / "index.py"), "--brief"], capture_output=True, text=True, timeout=60)
        return r.stdout.strip() if r.returncode == 0 else ""
    except Exception: return ""

def cmd_progress(a):
    cur, prev = scoreboard(a.since)
    if a.snapshot: append(SNAPS, cur)
    if a.json: print(json.dumps({"now": cur, "before": prev, "worse": worst(cur, prev)}, indent=1)); return
    t = text(cur, prev)
    s = steer()
    if s: t = s + "\n\n" + t
    print(t)
    if a.post:
        subprocess.run([sys.executable, str(Hive), "post", "--thread", "progress"], input=t, text=True,
                       env={**os.environ, "HIVE_NAME": os.environ.get("HIVE_NAME", "progress")})

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive improve / hive progress", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(required=True)
    p = sp.add_parser("log", help="log a landed change: --what, --measure (required), --check CMD --want up|down [--band N] [--kind fix|behaviour] [--control CMD] [--gain N --needs TEXT --beats TEXT: the size, written before landing] (written at landing, a command printing the number: a script judges it; a fix is due in 48h, a behaviour change in 7d against a --control it didn't touch), --revisit 7d|YYYY-MM-DD, --where, --hypothesis, --baseline, --commit, --thread, --also IDS (warns when the measure names a number an open landing, revisit still ahead, names)")
    for f in ("what", "measure", "revisit", "where", "hypothesis", "baseline", "commit", "thread", "check", "want", "kind", "control"): p.add_argument("--" + f)
    p.add_argument("--band", type=float, help="the move that counts as noise (default 5%% of the baseline)")
    p.add_argument("--gain", type=float, help="the size: the move you expect in the check's number by the cut-off (--revisit), in its own units and above 0 (--want gives the direction). The check reads at landing and once more at the cut-off, not between, and the verdict prints this beside what came. A --check row without --gain, --needs and --beats is logged all the same and marked unsized")
    p.add_argument("--needs", metavar="TEXT", help="what the number will rest on by the cut-off, e.g. '40 graded runs' or '12 days of readings'")
    p.add_argument("--beats", metavar="TEXT", help="the strongest cheap alternative the change must beat, e.g. 'last week's number' or 'the one-line version' (--control CMD is the do-nothing comparison and does not stand in for it). The verdict names it, for whoever reads the number")
    p.add_argument("--reviewed-by", dest="reviewed_by", help="the agent that reviewed this design or build and did not build it (counted as reviews_independent when --model is opus or fable)")
    p.add_argument("--model", help="the reviewer's model: opus|fable count as an independent review, anything else does not")
    p.add_argument("--no-check", metavar="WHY", help="why no command can read this number")
    p.add_argument("--expect", metavar="TEXT", help="for a gain no number reads: what will be visibly better, who could see it, and what would count as not better, written before it lands; a non-builder judges it at the revisit. A wrong or unchecked number is worse than none")
    p.add_argument("--also", action="append", help="ids of open landings on the same number, judged together with this one at the revisit (silences the overlap warning)")
    p.set_defaults(f=cmd_log)
    p = sp.add_parser("judge", help="judge a landed change on its revisit date: ID held|reverted|inconclusive --why 'the number vs baseline'")
    p.add_argument("id"); p.add_argument("verdict", choices=VERDICTS); p.add_argument("--why", default=""); p.add_argument("--self", action="store_true"); p.set_defaults(f=cmd_judge)
    p = sp.add_parser("list", help="the ledger; --due for revisits past their date"); p.add_argument("--due", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("attach", help="give a logged change a check after the fact, on or after its revisit date; the number is kept but never counts as held (late-check): ID --check CMD --want up|down [--band N] [--before N: the number from before the change; without it, measured now and read in 48 h]")
    p.add_argument("id"); p.add_argument("--check", required=True); p.add_argument("--want"); p.add_argument("--band", type=float); p.add_argument("--before", type=float); p.set_defaults(f=cmd_attach)
    p = sp.add_parser("autojudge", help="run the checks that are due and write verdicts, no model (hourly, after hive outcomes grade)"); p.add_argument("--no-post", action="store_true"); p.set_defaults(f=cmd_autojudge)
    p = sp.add_parser("num", help="one scoreboard number for a --check: FIELD, the latest snapshot's, or the mean over --from ISO --to ISO")
    p.add_argument("field"); p.add_argument("--from", dest="frm"); p.add_argument("--to"); p.set_defaults(f=cmd_num)
    p = sp.add_parser("show", help="one entry in full"); p.add_argument("id"); p.set_defaults(f=cmd_show)
    p = sp.add_parser("progress", help="the scoreboard: --since 1d (default), --snapshot appends to ~/progress.jsonl, --post puts it in thread progress, --json")
    p.add_argument("--since", default="1d"); p.add_argument("--snapshot", action="store_true"); p.add_argument("--post", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_progress)
    a = ap.parse_args(argv); a.f(a)

if __name__ == "__main__": main()
