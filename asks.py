#!/usr/bin/env python3
"""hive asks: the register of what the person asked for, so nothing is dropped.

The person's posts are the hive's goals. Each ask they make becomes an entry in ~/asks.jsonl (append-only; later records
with the same id update the earlier one), and every entry stays open until someone marks it done with evidence, or the
person says it's done or withdrawn. Standing guidance ("keep it simple", "don't ration work for pace") is filed as a
direction: never done, listed by `hive asks directions`, and written to ~/kb/direction.md for every agent to read.

  hive asks list [--open|--all|--json]      what is owed (default: open and owned entries, oldest first)
  hive asks check [--json]                  no model: open entries nobody is moving (no agent post in the thread for 6 h,
                                          owner not running). The keeper starts someone on each; the watchdog trips after 3 h.
  hive asks add --post ID --ask "..." [--kind ask|direction] [--thread T]
  hive asks own ID [--owner NAME]           you (or NAME) are on it
  hive asks done ID --evidence POSTID [--why ...]   finished, and the thread says so
  hive asks hold|person|drop ID --why ...   held by kb/priorities.md | waits on the person | withdrawn or merged
  hive asks show ID
  hive asks directions [--write]            standing guidance; --write refreshes ~/kb/direction.md
  hive asks reconcile [IDS] [--all]         a model reads the thread of each open ask that agents have posted in since the last run and
                                          marks what they delivered (done, with the post as evidence), who owns it, or that it waits on
                                          the person. Runs hourly after extract; --all re-reads every open ask.
  hive asks extract [--all] [--model sonnet] [--dry]   a model reads the person's posts since the last run (or all of them)
                                          against the register and adds new asks and directions, and marks what the
                                          person's own words closed. Runs hourly from hive-asks.timer.

Entry: {id "kNNNN", at, by, post, when (the post's time), thread, ask, kind, status, owner, evidence, why, done_at}.
Statuses: open, owned, done, held, person, dropped; directions are "standing".
"""
import argparse, datetime as dt, json, os, re, shutil, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
CORE, STATE = HOME / "core", HOME / ".hive"
LEDGER, BOARD, RUN = HOME / "asks.jsonl", HOME / "board.jsonl", HOME / "run"
CURSOR, DIRECTION = STATE / "asks-cursor.json", HOME / "kb" / "direction.md"
Hive = next((CORE / n for n in ("hive", "hive") if (CORE / n).exists()), CORE / "hive")
MOVING_S = 6 * 3600       # an agent post in the ask's thread this recently means someone is on it
OWN_GRACE_S = 3600        # a fresh `own` counts as moving even before the owner has posted
CLOSED = ("done", "dropped", "standing")
WAITING = ("held", "person")
NOT_AGENTS = ("person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer")
ACK = re.compile(r"^(?:@\S+\s+)*(?:\(from [^)\n]*\)\s+)?((?:perf|perfect|excellent|great|nice|cool|good|awesome|amazing|thanks|thank you|thx|ty|ok|okay|got it|sounds good|yes|no|yep|nope)[\s.!,]*)+$", re.I)
FROM = re.compile(r"^\s*(?:@\S+\s+)*\(from [^)\n]*\)\s*")

def now(): return dt.datetime.now(dt.timezone.utc)
def iso(t=None): return (t or now()).isoformat(timespec="seconds")
def me(): return os.environ.get("HIVE_NAME", "asks")

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

def ledger():
    items = {}
    for r in jsonl(LEDGER):
        i = r.get("id")
        if not i: continue
        if i not in items: items[i] = dict(r)
        else:
            e = items[i]
            e.update({k: v for k, v in r.items() if k not in ("id", "at", "by", "post", "when", "ask", "kind")})
            e["updated"] = r.get("at")
            if r.get("status") in ("done", "dropped"): e["done_at"] = r.get("at")
    return items

def next_id(items): return f"k{(max((int(i[1:]) for i in items if i[:1] == 'k' and i[1:].isdigit()), default=0) + 1):04d}"

def board(): return jsonl(BOARD)

def live_names():
    names = set()
    for f in RUN.glob("*.json"):
        try: r = json.loads(f.read_text()); os.kill(r["pid"], 0); names.add(r.get("name"))
        except (OSError, ValueError, KeyError): pass
    return names

def open_items(items): return [e for e in items.values() if e.get("status") not in CLOSED]

def moving(items, posts=None, t=None, live=None):
    """For each entry not closed: when it last moved (an agent post in its thread, or its owner alive), as ISO or None."""
    posts = board() if posts is None else posts; t = t or now(); live = live_names() if live is None else live
    last = {}
    for p in posts:
        if str(p.get("from", "")).lower() in NOT_AGENTS: continue
        th = p.get("thread") or p.get("id")
        if p.get("at", "") > last.get(th, ""): last[th] = p["at"]
    out = {}
    for e in open_items(items):
        th = e.get("thread") or e.get("post")
        m = last.get(th)
        if e.get("owner") in live: m = iso(t)
        if e.get("status") == "owned" and e.get("updated") and (t - dt.datetime.fromisoformat(e["updated"])).total_seconds() < OWN_GRACE_S: m = max(m or "", e["updated"])
        out[e["id"]] = m
    return out

def orphaned(items, posts=None, t=None, live=None, after_s=MOVING_S):
    """Open or owned entries nobody has moved for after_s: (entry, last move ISO or None, idle seconds)."""
    t = t or now(); mv = moving(items, posts, t, live); out = []
    for e in open_items(items):
        if e.get("status") in WAITING: continue
        m = mv.get(e["id"]); since = m or e.get("when") or e.get("at") or iso(t)   # when: the post's time, so a backfilled ask isn't "fresh"
        idle = (t - dt.datetime.fromisoformat(since)).total_seconds()
        if idle >= after_s: out.append((e, m, idle))
    return sorted(out, key=lambda x: -x[2])

STALE_S = 3 * 86400       # an ask open this long, however busy its thread, needs a look: busy threads hide answered asks from the moving rule

def stale_items(items, t=None, after_s=STALE_S):
    """Open or owned entries older than after_s, oldest first: the moving rule never surfaces them while their thread stays busy."""
    t = t or now(); out = []
    for e in open_items(items):
        if e.get("status") in WAITING or e.get("kind") == "direction": continue
        when = e.get("when") or e.get("at")
        if when and (t - dt.datetime.fromisoformat(when)).total_seconds() >= after_s: out.append(e)
    return sorted(out, key=lambda e: e.get("when") or e.get("at") or "")

UNOWNED_S = 6 * 3600      # an ask open this long with no owner is nobody's, however busy its thread: posts there are about other things

def unowned_items(items, t=None, after_s=UNOWNED_S):
    """Entries still status open (never owned, held, answered or dropped) for after_s, oldest first. The moving rule hides these
    whenever any agent posts in the thread (2026-11-04: 32 such asks, most in keeper, improve and feedback, with `check` saying nothing orphaned)."""
    t = t or now(); out = []
    for e in open_items(items):
        if e.get("status") != "open" or e.get("owner") or e.get("kind") == "direction": continue
        when = e.get("when") or e.get("at")
        if when and (t - dt.datetime.fromisoformat(when)).total_seconds() >= after_s: out.append(e)
    return sorted(out, key=lambda e: e.get("when") or e.get("at") or "")

def fmt(e, idle=None):
    tag = e.get("status", "open") + (f" by {e['owner']}" if e.get("owner") else "")
    s = f"{e['id']}  {e.get('when', e.get('at', ''))[:16]}  [{tag}]  {e.get('ask', '')}  (thread {e.get('thread') or e.get('post')})"
    if idle is not None: s += f"  idle {idle/3600:.1f} h"
    return s

def cmd_list(a):
    items = ledger()
    rows = [e for e in items.values() if (a.all or (e.get("status") not in CLOSED and e.get("kind") != "direction"))]
    rows.sort(key=lambda e: e.get("when", e.get("at", "")))
    if a.json: print(json.dumps(rows, indent=1, ensure_ascii=False)); return
    for e in rows: print(fmt(e))
    o = open_items(items)
    print(f"-- {len(rows)} shown; {sum(1 for e in o if e.get('status') in ('open', 'owned'))} open or owned, {sum(1 for e in o if e.get('status') in WAITING)} held or waiting on the person, "
          f"{sum(1 for e in items.values() if e.get('status') == 'done')} done, {sum(1 for e in items.values() if e.get('kind') == 'direction')} directions")

def cmd_check(a):
    items = ledger(); orph = orphaned(items, after_s=a.after * 3600)
    if a.json: print(json.dumps([{**e, "last_move": m, "idle_h": round(i / 3600, 1)} for e, m, i in orph] + [{**e, "unowned": True} for e in unowned_items(items, after_s=a.unowned * 3600) if e["id"] not in {x[0]["id"] for x in orph}], indent=1, ensure_ascii=False)); return
    if not orph: print("nothing orphaned: every open ask has someone moving it")
    else:
        print(f"{len(orph)} open asks nobody is moving (no agent post in the thread, owner not running), longest idle first:")
        for e, m, i in orph: print(fmt(e, i))
        print("Start an agent on one with a brief and `hive asks own ID --owner NAME`, or mark it: done --evidence POST | hold | person | drop --why.")
    stale = [e for e in stale_items(items, after_s=a.stale * 86400) if e["id"] not in {x[0]["id"] for x in orph}]
    if stale:
        print(f"\n{len(stale)} open asks older than {a.stale:g} days in threads that stay busy (nothing marks them, nothing proves them done), oldest first:")
        for e in stale[:a.limit]: print(fmt(e))
        if len(stale) > a.limit: print(f"  ... and {len(stale) - a.limit} more (--limit N)")
        print("Read each one's thread: `done --evidence POST` if it was delivered, else own it or start an agent on it; `hold|person|drop --why` if it waits or no longer applies.")
    shown = {x[0]["id"] for x in orph} | {e["id"] for e in stale}
    un = [e for e in unowned_items(items, after_s=a.unowned * 3600) if e["id"] not in shown]
    if un:
        print(f"\n{len(un)} open asks with no owner for over {a.unowned:g} h (their threads are busy, but nobody has taken the ask), oldest first:")
        for e in un[:a.limit]: print(fmt(e))
        if len(un) > a.limit: print(f"  ... and {len(un) - a.limit} more (--limit N)")
        print("Take each one or give it an agent: `own ID --owner NAME` with a brief in its thread; `done --evidence POST` if the thread shows it delivered; `hold|person|drop --why` otherwise. An ask that stays open is dropped work.")

def cmd_show(a):
    e = ledger().get(a.id)
    if not e: sys.exit(f"no entry {a.id}")
    print(json.dumps(e, indent=1, ensure_ascii=False))
    for r in jsonl(LEDGER):
        if r.get("id") == a.id and r.get("status"): print(f"  {r.get('at', '')[:16]} {r.get('by', '')}: {r.get('status')} {r.get('why', '') or ''}")

def cmd_add(a):
    items = ledger(); posts = {p["id"]: p for p in board() if p.get("id")}
    p = posts.get(a.post)
    rec = {"id": next_id(items), "at": iso(), "by": me(), "post": a.post, "when": p.get("at") if p else iso(),
           "thread": a.thread or (p.get("thread") or p.get("id") if p else a.post), "ask": a.ask.strip(), "kind": a.kind,
           "status": "standing" if a.kind == "direction" else "open"}
    append(LEDGER, rec); print(fmt(rec))

def set_status(id_, status, **kw):
    items = ledger()
    if id_ not in items: sys.exit(f"no entry {id_}")
    if items[id_].get("kind") == "direction" and status != "dropped": sys.exit(f"{id_} is a direction: it is never done; `drop` it if the person withdrew it")
    rec = {"id": id_, "at": iso(), "by": me(), "status": status, **{k: v for k, v in kw.items() if v}}
    append(LEDGER, rec); return rec

def cmd_own(a): r = set_status(a.id, "owned", owner=a.owner or me()); print(f"{a.id}: owned by {r['owner']}")
def cmd_done(a):
    if not a.evidence: sys.exit("No evidence, not done: --evidence POSTID, the post in the thread that says what was delivered.")
    set_status(a.id, "done", evidence=a.evidence, why=a.why); print(f"{a.id}: done ({a.evidence})")
def cmd_hold(a): need_why(a); set_status(a.id, "held", why=a.why); print(f"{a.id}: held ({a.why})")
def cmd_person(a): need_why(a); set_status(a.id, "person", why=a.why); print(f"{a.id}: waits on the person ({a.why})")
def cmd_drop(a): need_why(a); set_status(a.id, "dropped", why=a.why); print(f"{a.id}: dropped ({a.why})")
def need_why(a):
    if not a.why: sys.exit("Say why: --why ...")

def directions_text(items):
    ds = [e for e in items.values() if e.get("kind") == "direction" and e.get("status") != "dropped"]
    ds.sort(key=lambda e: e.get("when", ""))
    lines = ["# The person's standing directions", "",
             "Written by `hive asks directions --write` from ~/asks.jsonl (the person's own words, filed by the asks extractor or an agent). "
             "Each holds until the person says otherwise; the newest wins where they differ. `hive asks show ID` gives the post.", ""]
    for e in ds: lines.append(f"- {e['ask']} ({e['id']}, {e.get('when', '')[:10]}, post {e.get('post')})")
    return "\n".join(lines) + "\n"

GEN_LINE = re.compile(r"^(# The person's standing directions$|Written by `hive asks directions --write`|- .*\(k\d{4}, [\d-]*, post )")

def kept_notes(old):
    """Hand-written notes in direction.md (anything not generated from the register), kept across --write."""
    keep = "\n".join(l for l in old.splitlines() if not GEN_LINE.match(l)).strip()
    return keep + "\n" if keep else ""

def cmd_directions(a):
    items = ledger(); t = directions_text(items)
    if a.write:
        notes = kept_notes(DIRECTION.read_text()) if DIRECTION.exists() else ""
        DIRECTION.write_text(t + ("\n" + notes if notes else "")); print(f"wrote {DIRECTION}")
    else: print(t, end="")

# --- extraction: a model reads the person's posts against the register ---------------------------------------------------

PROMPT = """You keep the register of what the person (the owner of this hive; their posts are marked `person`) asked the hive for, so that nothing they asked is ever dropped. Below are the register's open entries and standing directions, then the person's posts in order (id, time, thread, text). Reply with JSON only, no prose, no code fence:
{{"new": [{{"post": "<post id>", "ask": "<one line, imperative, concrete, at most 160 characters>", "kind": "ask" or "direction"}}],
 "done": [{{"id": "k0012", "evidence": "<post id>", "why": "<the person's words>"}}],
 "dropped": [{{"id": "k0012", "evidence": "<post id>", "why": "superseded by ... / the person withdrew it"}}]}}
Rules. An ask is something the person wants done: a deliverable, a fix, a question to answer, a decision to carry out. A direction is standing guidance about how work should be done or what matters ("keep the UI simple, not vibe-coded", "don't ration work for pace", "don't ask me, just keep implementing"); directions are never done. A post asking several things gives several entries; keep each ask self-contained (name the thing, not "it"). A post that only thanks, acknowledges, answers an agent's question, or gives information gives nothing. A post that refines an open entry gives a new entry only if it adds a new deliverable. Never repeat an entry that is already in the register below, in the same words or others. Mark done or dropped only when the person's own words say the thing is done, no longer wanted, or replaced; agents record their own completions elsewhere. Write nothing private (keys, tokens, addresses, phone numbers). Keep the person's own wording for anything ambiguous.

Register:
{register}

The person's posts:
{posts}
"""

LEFT_PROMPT = """You keep the register of work this hive owes, so that nothing an agent leaves undone is dropped. Below are the register's open entries, then agents' posts in order (id, time, thread, agent, text). Find work an agent reports as left undone with nobody taking it: a step it did not get to, a problem it found and did not fix, a follow-up it says someone should do, a thing it says is still broken or missing. Reply with JSON only, no prose, no code fence:
{{"new": [{{"post": "<post id>", "ask": "<one line, imperative, concrete, at most 160 characters>"}}]}}
Rules. Skip what the agent says it is doing itself next (its run goes on), what it handed to a named agent, what waits on the person, what is already in the register (same words or others), optional ideas and suggestions, and anything a watchdog, keeper or review merely lists about other threads. Keep each entry self-contained: name the thing and the project, not "it". At most three per post; most posts give nothing. Write nothing private (keys, tokens, addresses, phone numbers).

Register:
{register}

Agents' posts:
{posts}
"""

LEFT = re.compile(r"\b(left( to| over|:)|what is left|remaining|remains|not (yet )?(done|finished)|still (needs?|open|to do|missing|broken|fails)|todo|to-do|next steps?|nobody (is|has|took)|no one (is|has)|someone (should|needs|has to)|waits? on|waiting on|unfinished|follow-?ups?|open questions?|outstanding|not implemented|deferred|punted|out of scope|didn.t get to|ran out of)\b", re.I)
NOT_AGENTS = ("person", "hives", "box", "starter", "watchdog", "progress", "asks", "keeper-timer")

def agent_posts(posts, since=""):
    """Agents' posts after `since` that talk of something left, one per agent and thread (the last), clipped: the cheap pass before the model."""
    last = {}
    for p in posts:
        if str(p.get("from", "")).lower() in NOT_AGENTS or p.get("at", "") <= since: continue
        text = str(p.get("text", "")).strip()
        if len(text) < 80 or not LEFT.search(text): continue
        last[(p.get("from"), p.get("thread"))] = {**p, "clean": text[:1500]}
    return sorted(last.values(), key=lambda p: p.get("at", ""))

def cmd_leftovers(a):
    """File what agents' posts say is left undone (kind leftover): the person's asks come from extract, these from the agents' own words."""
    posts = board()
    try: cur = json.loads(CURSOR.read_text())
    except (OSError, ValueError): cur = {}
    since = "" if a.all else cur.get("left_at") or (now() - dt.timedelta(hours=a.hours)).isoformat()
    todo = agent_posts(posts, since)
    if not todo: print("no new agent posts that speak of something left"); return
    by_id = {p["id"]: p for p in posts if p.get("id")}
    added = 0; cost = 0.0
    for k, b in enumerate(batches(todo)):
        if a.limit and k >= a.limit: print(f"stopped after {a.limit} batches (--limit); cursor at {b[0]['at']}"); break
        items = ledger()
        ptxt = "\n".join(f"- {p['id']} {p['at'][:16]} thread {p.get('thread') or p['id']} {p.get('from')}: {p['clean']}" for p in b)
        prompt = LEFT_PROMPT.format(register=register_text(items), posts=ptxt)
        if a.dry: print(prompt[-3000:]); print(f"... {len(b)} posts, {len(prompt)} chars"); continue
        out, c = ask_model(prompt, a.model); cost += c or 0
        try: j = parse_json(out)
        except ValueError: j = None
        if not j: print(f"unparsable reply, skipped batch ending {b[-1]['id']}: {out[:200]}", file=sys.stderr); continue
        for n in j.get("new", [])[:3 * len(b)]:
            if not n.get("ask"): continue
            p = by_id.get(n.get("post")) or b[-1]
            rec = {"id": next_id(items), "at": iso(), "by": "leftovers", "post": p["id"], "when": p.get("at"), "thread": p.get("thread") or p["id"],
                   "ask": n["ask"].strip()[:300], "kind": "leftover", "status": "open", "said_by": p.get("from")}
            append(LEDGER, rec); items[rec["id"]] = rec; added += 1; print("+ " + fmt(rec))
        cur = {**cur, "left_at": b[-1]["at"], "left_post": b[-1]["id"], "left_run": iso()}; CURSOR.write_text(json.dumps(cur))
    if not a.dry: print(f"{added} leftovers filed from {len(todo)} posts, ${cost:.2f}")

def login_dir():
    """The Claude login with the most weekly room, judged the way the starter does (pick_login): ~/.hive/usage.json and usage_5h.json
    (its readings), login_limits.json (the person's stop lines), paused.json, and starter.json's limits (logins held on a hit limit).
    The first login in config order was wrong half the time: a weekly-limited one answers 429 to every call, silently, 2026-11-04."""
    try: cfg = json.loads((CORE / "config.json").read_text())
    except (OSError, ValueError): cfg = {}
    def state(name, default):
        try: v = json.loads((STATE / name).read_text()); return v if isinstance(v, type(default)) else default
        except (OSError, ValueError): return default
    st, u, five, lines, paused = state("starter.json", {}), state("usage.json", {}), state("usage_5h.json", {}), state("login_limits.json", {}), state("paused.json", [])
    t = time.time(); paused = set(paused) | set(st.get("paused_logins", [])); held = st.get("limits", {})
    stop = cfg.get("usage_stop_pct", 100)
    ok = []
    for l in cfg.get("logins", []):
        d = str(Path(l).expanduser())
        if d in paused or held.get(d, 0) > t or not Path(d).is_dir(): continue
        mine = lines.get(d) if isinstance(lines.get(d), dict) else {}
        w, h, v, f = mine.get("weekly", stop), mine.get("five_hour"), u.get(d), five.get(d)
        if v is not None and w is not None and v >= w: continue
        if h is not None and isinstance(f, (int, float)) and f >= h: continue
        ok.append((v is None, v or 0, d))
    if ok: return min(ok)[2]
    return os.environ.get("CLAUDE_CONFIG_DIR") or str(HOME / ".claude")

LIMITED = re.compile(r"hit your .*limit|usage limit|rate.?limit|resets \d", re.I)
class Limited(RuntimeError):
    """The login is at its limit: nothing was read, the cursor stays, the next timer run retries. Not a failure of the unit."""

def ask_model(prompt, model, timeout=900):
    tmp = HOME / ".cache" / "hive-tmp"; tmp.mkdir(parents=True, exist_ok=True)
    env = {**os.environ, "CLAUDE_CONFIG_DIR": login_dir(), "TMPDIR": str(tmp), "ENABLE_CLAUDEAI_MCP_SERVERS": "false"}
    env.pop("CLAUDECODE", None)
    exe = shutil.which("claude") or str(HOME / ".local" / "bin" / "claude")  # systemd's PATH has no ~/.local/bin
    cmd = [exe, "-p", prompt, "--model", model, "--output-format", "json", "--tools", "", "--setting-sources", ""]
    p = subprocess.run(cmd, env=env, capture_output=True, text=True, timeout=timeout, cwd=tmp)
    try: res = json.loads(p.stdout)
    except ValueError: raise RuntimeError(f"claude gave no JSON (exit {p.returncode}): {p.stdout[-300:]} {p.stderr[-300:]}")
    if res.get("is_error") or res.get("subtype", "success") != "success" or not res.get("result"):
        # a limited login or an overlong prompt comes back as a JSON error with no braces in it; before 2026-11-04 that
        # parsed as {} and the run printed "0 entries updated, $0.00" as if every ask were still open
        msg = str(res.get("result") or res.get("error") or p.stderr)
        if LIMITED.search(msg): raise Limited(msg[-200:].strip())
        raise RuntimeError(f"claude answered with an error ({res.get('subtype')}): {str(res.get('result') or res.get('error') or p.stderr)[-300:]}")
    return res.get("result", ""), res.get("total_cost_usd")

def parse_json(s):
    s = s.strip()
    if s.startswith("```"): s = s.split("\n", 1)[1] if "\n" in s else s[3:]
    s = s.strip("`").strip()
    i, j = s.find("{"), s.rfind("}")
    return json.loads(s[i:j + 1]) if i >= 0 and j > i else {}

def person_posts(posts, since=""):
    out = []
    for p in posts:
        if str(p.get("from", "")).lower() != "person" or p.get("at", "") <= since: continue
        text = FROM.sub("", str(p.get("text", ""))).strip()
        if not text or ACK.match(text): continue
        out.append({**p, "clean": text})
    return out

def register_text(items):
    rows = [e for e in items.values() if e.get("status") not in ("done", "dropped")]
    rows.sort(key=lambda e: e.get("when", ""))
    return "\n".join(f"- {e['id']} [{e.get('kind')}, {e.get('status')}] {e.get('ask')} (thread {e.get('thread')})" for e in rows) or "(empty)"

def batches(posts, max_chars=24000, max_posts=60):
    b, n = [], 0
    for p in posts:
        if b and (n + len(p["clean"]) > max_chars or len(b) >= max_posts): yield b; b, n = [], 0
        b.append(p); n += len(p["clean"])
    if b: yield b

def cmd_extract(a):
    posts = board()
    try: cur = json.loads(CURSOR.read_text())
    except (OSError, ValueError): cur = {}
    since = "" if a.all else cur.get("at", "")
    todo = person_posts(posts, since)
    if not todo: print("no new posts from the person"); return
    by_id = {p["id"]: p for p in posts if p.get("id")}
    added = closed = 0; cost = 0.0
    for k, b in enumerate(batches(todo)):
        if a.limit and k >= a.limit: print(f"stopped after {a.limit} batches (--limit); cursor at {b[0]['at']}"); break
        items = ledger()
        ptxt = "\n".join(f"- {p['id']} {p['at'][:16]} thread {p.get('thread') or p['id']}: {p['clean'][:1500]}" for p in b)
        prompt = PROMPT.format(register=register_text(items), posts=ptxt)
        if a.dry: print(prompt[-3000:]); print(f"... {len(b)} posts, {len(prompt)} chars"); continue
        out, c = ask_model(prompt, a.model); cost += c or 0
        try: j = parse_json(out)
        except ValueError: j = None
        if not j: print(f"unparsable reply, skipped batch ending {b[-1]['id']}: {out[:200]}", file=sys.stderr); continue
        for n in j.get("new", []):
            if not n.get("ask") or n.get("kind") not in ("ask", "direction"): continue
            p = by_id.get(n.get("post")) or b[-1]
            rec = {"id": next_id(items), "at": iso(), "by": "extract", "post": p["id"], "when": p.get("at"), "thread": p.get("thread") or p["id"],
                   "ask": n["ask"].strip()[:300], "kind": n["kind"], "status": "standing" if n["kind"] == "direction" else "open"}
            append(LEDGER, rec); items[rec["id"]] = rec; added += 1; print("+ " + fmt(rec))
        for key in ("done", "dropped"):
            for d in j.get(key, []):
                e = items.get(d.get("id"))
                if not e or e.get("status") in CLOSED: continue
                rec = {"id": e["id"], "at": iso(), "by": "extract", "status": key, "evidence": d.get("evidence"), "why": (d.get("why") or "")[:300]}
                append(LEDGER, rec); e.update(rec); closed += 1; print(f"x {e['id']} {key}: {rec['why']}")
        CURSOR.write_text(json.dumps({**cur, "at": b[-1]["at"], "post": b[-1]["id"], "run": iso()}))  # keep left_*/reconciled
    if not a.dry:
        cmd_directions(argparse.Namespace(write=True))
        print(f"{added} added, {closed} closed from {len(todo)} posts, ${cost:.2f}")

RECONCILE = """You judge, from the board, which of the person's asks below have been delivered. For each ask you get the agents' posts in its thread after the ask was made (person posts are marked). Reply with JSON only, no prose, no code fence:
{{"done": [{{"id": "k0012", "evidence": "<id of the post that reports the delivery>", "why": "<what was delivered, one line>"}}],
 "owned": [{{"id": "k0012", "owner": "<agent name>"}}],
 "person": [{{"id": "k0012", "why": "<what it waits on from the person>"}}],
 "held": [{{"id": "k0012", "why": "<why it was deliberately set aside>"}}]}}
Rules. done only when an agent post reports the thing itself finished and checked (a fix landed, an answer given, a file delivered): a plan, a partial, "working on it", or a claim with nothing shown is not done. owned when an agent says it is on it and nothing reports it finished. person when the work waits for the person's answer, decision, money or credentials. held when an agent says it was deliberately put aside (priorities, the person's hold). Say nothing about an ask the posts don't settle: it stays open. Prefer the person's own words over an agent's where they conflict.

{body}
"""

def thread_posts(posts):
    by = {}
    for p in posts: by.setdefault(p.get("thread") or p.get("id"), []).append(p)
    return by

def cmd_reconcile(a):
    posts = board(); by = thread_posts(posts); items = ledger()
    try: cur = json.loads(CURSOR.read_text())
    except (OSError, ValueError): cur = {}
    since = "" if (a.all or a.ids) else cur.get("reconciled", "")
    started = iso()
    todo = [e for e in open_items(items) if e.get("kind") != "direction" and e.get("status") in ("open", "owned") and (not a.ids or e["id"] in a.ids)
            and any(p.get("at", "") > since and str(p.get("from", "")).lower() not in NOT_AGENTS for p in by.get(e.get("thread") or e.get("post"), []))]
    todo.sort(key=lambda e: e.get("when", ""))
    if not todo: print("nothing open to reconcile" + ("" if since == "" else f" (no agent posts since {since[:16]} in a thread with an open ask; --all looks at every open ask)")); return
    groups = {}
    for e in todo: groups.setdefault(e.get("thread") or e.get("post"), []).append(e)
    blocks = []
    for th, es in groups.items():
        first = min(e.get("when") or "" for e in es)
        after = [p for p in by.get(th, []) if p.get("at", "") > first]
        if not after: continue
        # Both ends of the thread: the answer to an old ask sits right after it, and a busy thread (keeper, hive) has hundreds of posts since.
        if len(after) > 40: after = after[:20] + after[-20:]
        lines = [f"  {p['id']} {p['at'][:16]} {p.get('from')}{' (person)' if str(p.get('from','')).lower()=='person' else ''}: {str(p.get('text',''))[:700]}" for p in after]
        txt = "\n".join(lines)
        if len(txt) > a.chars: txt = txt[:a.chars // 2] + "\n  [...]\n" + txt[-a.chars // 2:]
        head = "\n".join(f"- {e['id']} ({e.get('when','')[:16]}): {e['ask']}" for e in es)
        blocks.append((es, f"## thread {th}, asks:\n{head}\nposts after them:\n{txt}"))
    print(f"{len(todo)} open in {len(groups)} threads, {len(blocks)} threads with posts after them")
    changed = 0; cost = 0.0
    batch, n = [], 0
    def flush():
        nonlocal changed, cost, batch, n
        if not batch: return
        prompt = RECONCILE.format(body="\n\n".join(b for _, b in batch))
        if a.dry: print(prompt[:1500], f"\n... {len(batch)} asks, {len(prompt)} chars"); batch, n = [], 0; return
        out, c = ask_model(prompt, a.model); cost += c or 0
        try: j = parse_json(out)
        except ValueError: j = None
        if not j: print(f"unparsable reply, skipped batch of {len(batch)}: {out[:200]}", file=sys.stderr); batch, n = [], 0; return
        ids = {e["id"] for es, _ in batch for e in es}
        for key in ("done", "owned", "person", "held"):
            for d in j.get(key, []):
                e = items.get(d.get("id"))
                if not e or e["id"] not in ids or e.get("status") in CLOSED: continue
                if key == "done" and not d.get("evidence"): continue
                rec = {"id": e["id"], "at": iso(), "by": "reconcile", "status": key, "evidence": d.get("evidence"), "owner": d.get("owner"), "why": (d.get("why") or "")[:300]}
                rec = {k: v for k, v in rec.items() if v}
                append(LEDGER, rec); e.update(rec); changed += 1; print(f"= {e['id']} {key}: {rec.get('why') or rec.get('owner') or ''}")
        batch, n = [], 0
    for es, b in blocks:
        if batch and (n + len(b) > 40000 or len(batch) >= 12): flush()
        batch.append((es, b)); n += len(b)
    flush()
    if not a.dry:
        if not a.ids: CURSOR.write_text(json.dumps({**cur, "reconciled": started, "reconcile_run": iso()}))
        print(f"{changed} entries updated, ${cost:.2f}")

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive asks", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(required=True)
    p = sp.add_parser("list"); p.add_argument("--open", action="store_true"); p.add_argument("--all", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("leftovers", help="file what agents' posts say is left undone (kind leftover)"); p.add_argument("--all", action="store_true"); p.add_argument("--hours", type=float, default=24, help="first run: how far back")
    p.add_argument("--model", default="sonnet"); p.add_argument("--dry", action="store_true"); p.add_argument("--limit", type=int, default=0); p.set_defaults(f=cmd_leftovers)
    p = sp.add_parser("check"); p.add_argument("--json", action="store_true"); p.add_argument("--after", type=float, default=MOVING_S / 3600, help="hours idle")
    p.add_argument("--stale", type=float, default=STALE_S / 86400, help="days open after which an ask is listed even in a busy thread"); p.add_argument("--unowned", type=float, default=UNOWNED_S / 3600, help="hours open with no owner after which an ask is listed even in a busy thread"); p.add_argument("--limit", type=int, default=15); p.set_defaults(f=cmd_check)
    p = sp.add_parser("show"); p.add_argument("id"); p.set_defaults(f=cmd_show)
    p = sp.add_parser("add"); p.add_argument("--post", required=True); p.add_argument("--ask", required=True); p.add_argument("--kind", default="ask", choices=("ask", "direction")); p.add_argument("--thread"); p.set_defaults(f=cmd_add)
    p = sp.add_parser("own"); p.add_argument("id"); p.add_argument("--owner"); p.set_defaults(f=cmd_own)
    p = sp.add_parser("done"); p.add_argument("id"); p.add_argument("--evidence"); p.add_argument("--why", default=""); p.set_defaults(f=cmd_done)
    for name, f in (("hold", cmd_hold), ("person", cmd_person), ("drop", cmd_drop)):
        p = sp.add_parser(name); p.add_argument("id"); p.add_argument("--why", default=""); p.set_defaults(f=f)
    p = sp.add_parser("directions"); p.add_argument("--write", action="store_true"); p.set_defaults(f=cmd_directions)
    p = sp.add_parser("extract"); p.add_argument("--all", action="store_true"); p.add_argument("--model", default="sonnet"); p.add_argument("--dry", action="store_true"); p.add_argument("--limit", type=int, default=0, help="batches to run, 0 = all"); p.set_defaults(f=cmd_extract)
    p = sp.add_parser("reconcile", help="a model reads each open ask's thread and marks what agents delivered (done with evidence), own, or waits on the person")
    p.add_argument("ids", nargs="*"); p.add_argument("--all", action="store_true", help="every open ask, not only threads with agent posts since the last run")
    p.add_argument("--model", default="sonnet"); p.add_argument("--dry", action="store_true"); p.add_argument("--chars", type=int, default=9000); p.set_defaults(f=cmd_reconcile)
    a = ap.parse_args(argv)
    try: a.f(a)
    except Limited as e: print(f"login at its limit, nothing changed; the next run retries: {e}")   # exit 0: a wait, not a failed unit

if __name__ == "__main__": main()
