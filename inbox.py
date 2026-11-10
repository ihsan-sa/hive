#!/usr/bin/env python3
"""inbox: Claude Code hook that hands a running Hive agent the board posts meant for it, mid-run.

Wired in ~/.claude/settings.json for PostToolUse, PostToolUseFailure and Stop. Without it a post that mentions a running
agent waits in the starter's queue until that agent ends, so agents can't talk while they work.
It delivers posts made since the session started that mention $HIVE_NAME, or that land in the
thread it was started on, not written by itself. Delivered ids go to ~/.hive/delivered/<name>, one a line, each batch
followed by a line "@<unix seconds>" saying when it was handed over (the dash's "Reached a5c83 · 12:03"); the
starter drops those from its queue so the agent isn't restarted for a post it already saw.
PostToolUseFailure counts as a step too: a command the starter stopped for the person's Interrupt ends as a failure.
PostToolUse: the posts come back as additionalContext. Stop: if anything is new, the stop is
blocked with the posts as the reason, so the agent answers instead of ending unaware. Stop also
blocks once per run when the person started it and the agent is ending with no post since: its
plain-text answer reaches nobody (the person, 2026-11-04: a24ba answered twice into nothing).
Stop also holds a run that gives no reason for stopping (stop_gate): the hive keeps moving, so a run says in its thread's
status bar why it stops (done, blocked, needs the person, waiting, told to stop) or carries on (the person, 2026-11-06).
PostToolUse also asks, once per run, for a first line when the person started the run FIRST_S ago and
nothing has been posted since: the person chases after 1 to 3 min of silence ("hello?", 5 times on 2026-11-04).
Outside an Hive session (no HIVE_NAME or no run record) it does nothing.
"""
import datetime as dt, hashlib, json, os, re, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
BOARD, RUN, STATE = HOME / "board.jsonl", HOME / "run", HOME / ".hive"
MENTION = re.compile(r"(?<![\w.])@([a-z0-9][a-z0-9_-]*)", re.I)
CODE = re.compile(r"```.*?```|`[^`\n]*`", re.S)  # a name in backticks is being written about, not called
WARDEN = re.compile(r"w[0-9a-f]{4}")
KB = Path(__file__).resolve().parent / "kb.py"

def names(p):
    to = p.get("to") or []
    if isinstance(to, str): to = to.replace(",", " ").split()
    return {t.lstrip("@").lower() for t in to} | {m.lower() for m in MENTION.findall(CODE.sub(" ", str(p.get("text", ""))))}

def read_new(name, rec):
    """The board lines this agent hasn't been through yet. For one run record the filter below is fixed, so a line it passed over
    once never matches later: keep a byte cursor per agent and read only past it. A new record (a restart, a new session) or a
    shorter board (rotation) reads it all again. The cursor stops at the last whole line, so a post half-written is read next time."""
    cur = STATE / "cursor" / name
    key = json.dumps([rec.get("at"), rec.get("session"), rec.get("thread"), rec.get("all_since")])
    try: k, off = cur.read_text().split("\n")[:2]; off = int(off) if k == key else 0
    except (OSError, ValueError): off = 0
    try:
        with BOARD.open("rb") as fh:
            if off > os.fstat(fh.fileno()).st_size: off = 0
            fh.seek(off); data = fh.read()
    except OSError: return [], lambda: None
    end = data.rfind(b"\n") + 1
    def done():  # called once what was read is delivered, so a crash in between reads it again rather than losing it
        try: cur.parent.mkdir(parents=True, exist_ok=True); cur.write_text(f"{key}\n{off + end}\n")
        except OSError: pass
    return data[:end].decode(errors="replace").splitlines(), done

def new_posts(name, rec, seen, lines=None):
    out = []
    for line in lines if lines is not None else BOARD.read_text(errors="replace").splitlines():
        try: p = json.loads(line)
        except ValueError: continue
        if not isinstance(p, dict) or p.get("id") in seen or str(p.get("from", "")).lower() == name: continue
        # A warden judging a step hears its step's thread and nothing else: an @all, or its name in any spelling, would steer the judge from outside.
        if WARDEN.fullmatch(name) and str(rec.get("thread")).startswith("step-") and p.get("thread") != rec["thread"]: continue
        at, targets = str(p.get("at", "")), names(p)
        if at <= rec.get("at", ""):
            # A restart moves "at" to now; an @all posted while the interrupted run couldn't hear it must still arrive.
            if "all" in targets and rec.get("thread") and at > rec.get("all_since", "~"): out.append(p)
            continue
        if name in targets or (rec.get("thread") and ("all" in targets or p.get("thread") == rec["thread"])):
            out.append(p)
    return out

def _claude_models():
    """The Claude model words, weakest first: config.json "models". A new model is one line there, not an edit in each file."""
    try: m = json.loads((Path(__file__).resolve().parent / "config.json").read_text()).get("models")
    except (OSError, ValueError): m = None
    return tuple(m) if isinstance(m, list) and m and all(isinstance(x, str) and re.fullmatch(r"[a-z][a-z0-9.-]*", x) for x in m) else ("haiku", "sonnet", "opus", "fable")
CLAUDE_MODELS = _claude_models()
STRENGTH = CLAUDE_MODELS  # weakest first

def model_note(name, rec, ps):
    """A post naming this running agent that picks a stronger model (the menu, or --model): it should be answered on that model, so say how. A weaker one just holds from the next run."""
    now_on = rec.get("model")
    if now_on not in STRENGTH and now_on is not None: return ""
    up = [m for p in ps if name in names(p) and (m := p.get("model")) in STRENGTH and (now_on is None or STRENGTH.index(m) > STRENGTH.index(now_on))]
    return (f"\n\nThat post picks {up[-1]}, and this run is on {now_on or 'another model'}. Finish the step you're on and don't answer it here: "
            f'run `hive model {up[-1]} "picked for the post above"` and end your turn. You resume on {up[-1]} with your memory, and answer it there.') if up else ""

def fmt(p):
    to = " ".join("@" + n for n in sorted(names(p)))
    tags = (f" for focus {', '.join(map(str, p['foci']))}" if p.get("foci") else "") + (f" (passed on by {p['relay_by']})" if p.get("relay_of") else "")
    return (f"[{p.get('id')}] {str(p.get('at', ''))[:19]} {p.get('from')}{' -> ' + to if to else ''} "
            f"(thread {p.get('thread')})" + tags + (f" replying to {p['reply_to']}" if p.get("reply_to") else "") + "\n  " + str(p.get("text", "")).replace("\n", "\n  ")
            + "".join(f"\n  file: {f.get('path')}" for f in p.get("files") or [])
            + ("\n  (The person pressed Interrupt on this: read it before carrying on. If a command you were waiting on was stopped, that was for this; run it again only if this still wants it.)" if p.get("interrupt") else ""))

WORD = re.compile(r"[a-z][a-z0-9-]{3,}")
STOPWORDS = {"this", "that", "with", "from", "have", "what", "when", "where", "which", "also", "like", "just", "about", "into", "than",
             "then", "them", "they", "their", "there", "these", "those", "will", "would", "could", "should", "your", "some", "more", "make",
             "thread", "focus", "agent", "agents", "hive", "post", "posts", "maybe", "think", "know", "need", "needs", "want", "good", "well"}

def focus_hint(ps, st=None):
    """For a person's post: the foci whose id, name or aim share a word with it, with their keepers, and the standing ask to pass on
    what belongs elsewhere (the person, 11071747b02b: a post should be tagged with the agents it concerns, or agents make the
    connections). Word overlap only; an agentic search can slot in here later if words miss too much."""
    mine = [p for p in ps if str(p.get("from", "")).lower() == "person"]
    if not mine: return ""
    try:
        import focus
        if st is None: st = focus.state()
    except Exception: return ""
    words = set()
    for p in mine: words |= {w for w in WORD.findall(str(p.get("text", "")).lower()) if w not in STOPWORDS}
    here = {str(p.get("thread")) for p in mine} | {str(x).lower() for p in mine for x in (p.get("foci") or [])}
    hits = []
    for f in st["foci"]:
        if f["id"] in here or (f.get("thread") or "") in here: continue
        own = {f["id"], *WORD.findall(str(f.get("name", "")).lower()), *WORD.findall(str(f.get("aim") or "").lower())} - STOPWORDS
        if (shared := own & words):
            k = focus.keeper(f, focus.board_posts())
            hits.append(f"- {f['id']} ({f['name']}; " + (f"kept by {k}" if k else "nobody keeping it") + f"): shares {', '.join(sorted(shared)[:3])}")
    if not hits: return ""
    return ("\n\nFoci its words also touch (matched on words, not checked):\n" + "\n".join(hits[:4]) +
            "\nIf part of it belongs there, pass it on with `hive relay POSTID FOCUS --why \"...\"` and say in your reply where it went.")

def person_note(ps):
    """A post from the person among those handed over: ask for a reply with the next tool call, not after the current job. A
    running agent answers the person in a median 104 s and a p90 of 13 min against 46 s and 4 min for one woken for the post
    (48 h to 2026-11-04), and the person posts "hello?" after about a minute of silence."""
    if not any(str(p.get("from", "")).lower() == "person" for p in ps): return ""
    return ("\n\nOne of these is from the person. Make your next tool call an hive post in its thread: one or two lines, the answer "
            "if you have it, else what you heard and what you'll do about it. Then carry on. They expect a reply within a minute, "
            "and until you post, the dash shows that thread waiting on you.")

NAMED = re.compile(r"[a-z][0-9a-f]{4}|hives")

def waker(rec, name):
    """Who this run answers to: "person", "hives", or an agent's name (its own after hive model or its waiter's post: the task is still
    owed a result); None for a scheduled run (keeper, watchdog, improver), which has its own brief."""
    src = str(rec.get("from") or "").lower()
    return src if src == "person" or NAMED.fullmatch(src) else None

def unanswered(name, rec):
    """Why this stop should wait, if a post from the person, another Hive or a named agent started the run and the agent hasn't
    posted since. Once per run. 10 of 2026-11-04's graded runs ended with the answer in plain text only (hive outcomes: silent)."""
    src = waker(rec, name)
    if os.environ.get("HIVE_DOOR") or not src or not rec.get("at"): return None
    mark = STATE / "nudged" / name
    try:
        if mark.read_text() == rec["at"]: return None
    except OSError: pass
    # the person and the other Hives read the thread they asked in (its Slack channel), so a post elsewhere is silence to them;
    # an agent that named you gets your posts anywhere through its own inbox hook. af9cc answered a screenshot in another
    # thread on 2026-11-04 and the person's thread tripped the watchdog.
    thread = rec.get("thread") if src in ("person", "hives") else None
    if posted_since(name, rec["at"], thread): return None
    mark.parent.mkdir(parents=True, exist_ok=True); mark.write_text(rec["at"])
    who = ("the person sees" if src == "person" else "the other Hive sees" if src == "hives" else "whoever waits on the task sees"
           if src == name else f"{src}, who named you, sees")
    where = f"thread {rec.get('thread') or 'the thread you were started in'}"
    if thread and posted_since(name, rec["at"]):
        return (f"You posted in another thread, but nothing in {where}, where the ask was made: {who} no answer there (they read "
                f"that thread's channel). Post one or two lines in {where} now (the answer, or where it is), then stop.")
    return (f"You are ending this run without posting anything to the board, so {who} no answer: text outside hive post "
            f"reaches nobody. Post your answer now in {where} with "
            "hive post --thread T and a quoted heredoc, then stop. If you truly have nothing to say, just stop again.")

def owns_open(name, rec):
    """Once per run: asks this agent owns that are still open as it stops. Watchdog trips on 2026-11-04 (k0558, k0559, k0486)
    were owners that delivered and stopped without `hive asks done`/`person`. An ask already shown in an earlier run comes again
    only if this run posted in its thread (it may have just been delivered): over 2026-11-03..06, 147 of 244 of these blocks
    were the same list again and led to no tool call at all (~/projects/agent-weight)."""
    if os.environ.get("HIVE_DOOR") or not rec.get("at"): return None
    mark = STATE / "nudged-asks" / name
    try: lines = mark.read_text().splitlines()
    except OSError: lines = []
    if lines and lines[0] == rec["at"]: return None
    shown = set(lines[1].split()) if len(lines) > 1 else set()
    try:
        sys.path.insert(0, str(Path(__file__).parent)); import asks
        mine = [e for e in asks.ledger().values() if e.get("owner") == name and e.get("status") not in asks.CLOSED + asks.WAITING]
    except Exception: return None
    threads = {p.get("thread") for p in own_posts(name, rec["at"]) or []}
    new = [e for e in mine if e["id"] not in shown or e.get("thread") in threads]
    mark.parent.mkdir(parents=True, exist_ok=True); mark.write_text(rec["at"] + "\n" + " ".join(sorted(shown | {e["id"] for e in mine})))
    mine = new
    if not mine: return None
    rows = "\n".join(f"{e['id']}: {str(e.get('ask'))[:120]}" for e in mine[:8])
    try: (STATE / STOP_LOG).open("a").write(json.dumps({"at": time.time(), "name": name, "run": rec["at"], "thread": rec.get("thread"), "event": "owns_open"}) + "\n")
    except OSError: pass
    return ("You still own these open asks as you stop. Close each one that is delivered with `hive asks done ID --evidence POSTID`, "
            "mark `hold|person|drop ID --why ...` if it waits or is moot, or leave it if work remains (say so in your thread):\n" + rows)

STOP_WORDS = ("Done", "Blocked", "Needs the person", "Waiting", "Told to stop", "Next")  # how a stop reason starts, as the last line of the status bar
STOP_FRESH_S = 900  # a reason older than this was written before the work that came after it
STOP_ASKS = 3       # asked this often with no reason given, a run is let go: a hook that never lets go is a loop
STOP_LOG = "stop-gate.jsonl"
STOP_HOLD_S = 600   # another agent's bar this fresh is theirs: a reason given by post is accepted and the bar left alone (hive-meta load P8)

def _keep_bar_from():
    """When reason_from_post stops overwriting another agent's fresh bar: config.json "stop_gate_keep_bar_from" (YYYY-MM-DD, UTC),
    a measured trial from 2026-11-08 unless the person says no (review.md bundle item 1, post 1106025535b3); "never" turns it off."""
    try: v = json.loads((Path(__file__).resolve().parent / "config.json").read_text()).get("stop_gate_keep_bar_from", "2026-11-08")
    except (OSError, ValueError): v = "2026-11-08"
    try: return dt.datetime.fromisoformat(str(v)).replace(tzinfo=dt.timezone.utc).timestamp()
    except ValueError: return float("inf")
KEEP_BAR_FROM = _keep_bar_from()

def last_status(thread):
    """The newest status bar record for a thread (`hive status` appends to ~/.hive/thread-status.jsonl), or {}."""
    best = {}
    try:
        for ln in (STATE / "thread-status.jsonl").read_text().splitlines():
            try: r = json.loads(ln)
            except ValueError: continue
            if isinstance(r, dict) and r.get("thread") == thread: best = r
    except OSError: pass
    return best

# a Waiting reason must name what wakes the run: a clock time or date, a post id, an agent name, or a job/shell/review/answer
WAIT_SIGNAL = re.compile(r"\b\d{1,2}:\d{2}\b|\b20\d\d-\d\d-\d\d\b|\b[0-9a-f]{12}\b|\b[a-f][0-9a-f]{4}\b|@\w|\b(heavy|job|shell|waiter|sims?|ci|build|review\w*|reply|answers?|wakes?|agent|queue|deploy|run)\b", re.I)

def stop_reason(name, st, since):
    """The reason this agent gave for stopping: the last line of the thread's status bar `st`, if this agent set it at or after
    `since` and the line starts with one of STOP_WORDS and says something after it. (word, line), or None."""
    if str(st.get("from", "")).lower() != name or not isinstance(st.get("at"), (int, float)) or st["at"] < since: return None
    line = (str(st.get("text", "")).strip().splitlines() or [""])[-1].strip()
    word = next((w for w in STOP_WORDS if line.lower().startswith(w.lower() + ":") and len(line) - len(w) > 12), None)
    if word == "Waiting" and not WAIT_SIGNAL.search(line.split(":", 1)[1]): return None  # intent only ("back in 10 min"): no signal behind it
    return (word, line) if word else None

def reason_from_post(name, rec, thread, st, since, t=None):
    """A stop reason given as the last line of this agent's newest post in the thread in this run (fresh, as for the bar), copied
    into the status bar so the person reads it there. Saves the turn an `hive status` call costs: 44 of 51 gate asks over
    2026-11-03..06 led to nothing but that call (~/projects/agent-weight). When another agent set the bar within STOP_HOLD_S
    (from KEEP_BAR_FROM), the bar is theirs: the reason is accepted from the post and the bar left alone, so the newest stopper
    no longer wipes the last one's reason (61 such overwrites in 3 days; hive-meta load P8). (word, line, via), or None;
    via is "post" or "post, bar kept"."""
    ps = [p for p in own_posts(name, rec["at"]) or [] if p.get("thread") == thread]
    if not ps: return None
    p = ps[-1]
    try: pat = dt.datetime.fromisoformat(p["at"]); pat = (pat if pat.tzinfo else pat.replace(tzinfo=dt.timezone.utc)).timestamp()
    except (KeyError, ValueError): return None
    lines = [l.strip().strip("*_ ").strip() for l in str(p.get("text", "")).strip().splitlines() if l.strip()]
    while len(lines) > 1 and re.fullmatch(r"(?:@[\w.-]+[\s,.]*)+", lines[-1]): lines.pop()  # "Done: …" then the "@name" hand-back line (R25)
    got = stop_reason(name, {"from": name, "at": pat, "text": lines[-1] if lines else ""}, since)
    if not got: return None
    t = t or time.time()
    held = str(st.get("from", "")).lower() not in ("", name) and isinstance(st.get("at"), (int, float)) and t - st["at"] <= STOP_HOLD_S
    if held and t >= KEEP_BAR_FROM: return got + ("post, bar kept",)
    line1 = (str(st.get("text", "")).strip().splitlines() or [""])[0].strip() or (lines[0][:120] if len(lines) > 1 else thread)
    try:
        with (STATE / "thread-status.jsonl").open("a") as fh:
            fh.write(json.dumps({"thread": thread, "from": name, "text": f"{line1}\n{got[1]}"[:400], "at": t, "via": "post " + str(p.get("id", ""))}) + "\n")
    except OSError: pass
    return got + ("post",)

BAR_SKIP = {"thread-status", "watchdog", "keeper", "improve", "side"}  # as thread_status.SKIP

def bar_line(p):
    """Line 2 for a bar written from post `p`: its last line when that is a stop or Next line, else its first sentence. Names lose the @ so a bar never reads as a mention."""
    lines = [l.strip().strip("*_ ").strip() for l in str(p.get("text", "")).strip().splitlines() if l.strip()]
    while len(lines) > 1 and re.fullmatch(r"(?:@[\w.-]+[\s,.]*)+", lines[-1]): lines.pop()
    if not lines: return ""
    last = lines[-1]
    line = last if any(last.lower().startswith(w.lower() + ":") for w in STOP_WORDS) else re.split(r"(?<=[.!?])\s", lines[0], maxsplit=1)[0]
    return re.sub(r"@([\w.-]+)", r"\1", line)[:160]

def has_stop_line(text):
    """Does this post's last line (a trailing "@name" hand-back line aside) start with a stop word? What `hive post --end` requires."""
    lines = [l.strip().strip("*_ ").strip() for l in str(text).strip().splitlines() if l.strip()]
    while len(lines) > 1 and re.fullmatch(r"(?:@[\w.-]+[\s,.]*)+", lines[-1]): lines.pop()
    return bool(lines) and any(lines[-1].lower().startswith(w.lower() + ":") for w in STOP_WORDS)

END_POST_S = 120  # the --end post this tool call just made: newer than this
END_CMD = re.compile(r"\bh2\s+post\b(?:\\\n|[^\n])*\s--end\b")
END_FRESH_S = 30  # a delivery this close before the post may be one the model has not read yet: tool calls can run side by side

def post_time(p):
    at = dt.datetime.fromisoformat(p["at"])
    return (at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp()

def end_post(name, rec, payload, t=None):
    """The post this tool call made with `hive post --end`, or None. The command must say so, the agent's newest post of this run must carry the flag, and
    the post's id must be in this call's output: so "--end" in text or in a grep does nothing, a refused post can't end the run on an older one, and a
    post that was held once never ends it later. A subagent's post is not the run's."""
    if os.environ.get("HIVE_DOOR") or not rec.get("at") or payload.get("agent_id") or payload.get("tool_name") != "Bash": return None
    if not END_CMD.search(str((payload.get("tool_input") or {}).get("command", ""))): return None
    ps = own_posts(name, rec["at"]) or []
    if not ps or not ps[-1].get("end") or ps[-1].get("id", "\0") not in str(payload.get("tool_response")): return None
    try:
        if (t or time.time()) - post_time(ps[-1]) > END_POST_S: return None
        if (STATE / "end-held" / name).read_text().strip() == ps[-1]["id"]: return None
    except (KeyError, ValueError): return None
    except OSError: pass  # no held post yet
    return ps[-1]

def delivered_since(name, at):
    """Was anything delivered to this agent at or after this time? The delivered file's "@<unix>" lines say when."""
    try: stamps = [int(l[1:]) for l in (STATE / "delivered" / name).read_text().split() if l.startswith("@") and l[1:].isdigit()]
    except OSError: return False
    return bool(stamps) and max(stamps) >= at

def end_decision(name, rec, post):
    """A run asked to end on its post (`hive post --end`, L48 in projects/hive-meta/load.md). Ending from PostToolUse skips the Stop hook, so this is the
    Stop path run early: the same holds in the same order, the bars at the end. Returns None to end, else what holds it, handed to the run at once
    (these checks are once-only, so what they say can't wait for a Stop that would no longer repeat it). A post handed over by a tool call beside this
    one holds it first: ending would throw it away unread, and the starter counts it as delivered. The reply call this saves is 1.46% of run money."""
    if delivered_since(name, post_time(post) - END_FRESH_S): why = "A post reached you around this one, so read it first."
    else: why = "\n\n".join(x for x in (unanswered(name, rec) or owns_open(name, rec), stop_gate(name, rec), focus_nudge(name, rec)) if x)
    if why:
        d = STATE / "end-held"; d.mkdir(parents=True, exist_ok=True); (d / name).write_text(post["id"])
        return "Not ended on that post, because:\n\n" + why
    try: run_end_bars(name, rec)
    except Exception: pass  # a bar never holds up a stop
    return None

def run_end_bars(name, rec, t=None):
    """At a run's real end, the status bar of each thread it posted in, from its newest post there, when the run didn't set one itself.
    The bar stays current without a model (the lab, 2026-11-10, post 111002559ce8: the timer spent ~165 agent starts a day on it). A bar this run set by hand is left; so is one another agent set
    after the post or within STOP_HOLD_S, as in reason_from_post. Returns the threads written."""
    if os.environ.get("HIVE_DOOR") or not rec.get("at") or WARDEN.fullmatch(name) or name == "status-timer": return []
    try: at = dt.datetime.fromisoformat(rec["at"]); start = (at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp()
    except ValueError: return []
    t, newest, wrote = t or time.time(), {}, []
    for p in own_posts(name, rec["at"]) or []:
        if p.get("thread") and p["thread"] not in BAR_SKIP: newest[p["thread"]] = p
    for thread, p in newest.items():
        try: pat = dt.datetime.fromisoformat(p["at"]); pat = (pat if pat.tzinfo else pat.replace(tzinfo=dt.timezone.utc)).timestamp()
        except (KeyError, ValueError): continue
        st = last_status(thread)
        sat = st.get("at") if isinstance(st.get("at"), (int, float)) else 0
        mine = str(st.get("from", "")).lower() == name
        if mine and sat >= start and not (str(st.get("via", "")).startswith("run-end") and sat < pat): continue  # set by hand this run, or already written for this post
        if not mine and st.get("from") and (sat > pat or t - sat <= STOP_HOLD_S): continue  # another agent's newer or fresh bar is theirs
        line = bar_line(p)
        if not line: continue
        line1 = (str(st.get("text", "")).strip().splitlines() or [""])[0].strip() or thread
        try:
            with (STATE / "thread-status.jsonl").open("a") as fh:
                fh.write(json.dumps({"thread": thread, "from": name, "text": f"{line1}\n{line}"[:400], "at": t, "via": "run-end " + str(p.get("id", ""))}) + "\n")
            wrote.append(thread)
        except OSError: pass
    return wrote

def gate_off(name, t):
    """True when this agent is in the no-ask arm of the stop-gate trial: ~/.hive/gate-trial.json {"name", "from", "until"} (ISO),
    a coin on the trial and agent name. The gate still reads and logs the reason; it only doesn't ask for a missing one
    (projects/rsi-lab/rules.md section 9: of 406 asks, 95% bought the line and nothing more, and run_end_bars now writes the bar)."""
    try:
        g = json.loads((STATE / "gate-trial.json").read_text())
        ts = lambda v: (lambda d: d if d.tzinfo else d.replace(tzinfo=dt.timezone.utc))(dt.datetime.fromisoformat(v)).timestamp()
        return ts(g["from"]) <= t < ts(g["until"]) and int(hashlib.sha1(f"{g['name']}:gate:{name}".encode()).hexdigest(), 16) % 2 == 1
    except (OSError, ValueError, KeyError, TypeError): return False

def stop_gate(name, rec, t=None):
    """Why this stop should wait: the run has given no reason for stopping. The person, 2026-11-06 (post 11052128aa48): before
    ending work an agent justifies the stop in the thread's status bar, against the goal of moving forward and stopping only
    when truly blocked or when it needs them on something critical; one that can't keeps going. The hook checks that a reason
    was given and is fresh, and the agent judges whether it holds. Runs woken by the person, another Hive or a named agent;
    not scheduled runs (keeper, watchdog, improver, status: their brief bounds them), wardens, free agents (kept light), or
    a stop that is a model switch (`hive model X`, which resumes at once). Every ask and pass goes to ~/.hive/stop-gate.jsonl."""
    thread = rec.get("thread")
    if os.environ.get("HIVE_DOOR") or not rec.get("at") or not thread or not waker(rec, name) or WARDEN.fullmatch(name) or name.startswith("f"): return None
    try: at = dt.datetime.fromisoformat(rec["at"])
    except ValueError: return None
    t = t or time.time()
    if any(p.get("model") and name in names(p) for p in own_posts(name, rec["at"]) or []): return None  # hive model: resumes on the other model
    f = STATE / "stop-asked" / name
    try: k, n = f.read_text().split(); n = int(n) if k == rec["at"] else 0
    except (OSError, ValueError): n = 0
    st = last_status(thread)
    since = max((at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp(), t - STOP_FRESH_S)
    got = stop_reason(name, st, since) or reason_from_post(name, rec, thread, st, since, t)
    def log(event):
        try:
            with (STATE / STOP_LOG).open("a") as fh:
                fh.write(json.dumps({"at": t, "name": name, "run": rec["at"], "thread": thread, "event": event, "asked": n, "why": got[1] if got else "", "via": (got[2] if len(got) > 2 else "bar") if got else ""}) + "\n")
        except OSError: pass
    if got or n >= STOP_ASKS:
        log("passed" if got else "let go"); return None
    if gate_off(name, t): log("not asked"); return None
    try: f.parent.mkdir(parents=True, exist_ok=True); f.write_text(f"{rec['at']} {n + 1}")
    except OSError: return None  # can't count the asks: don't risk a loop
    log("asked")
    line1 = (str(st.get("text", "")).strip().splitlines() or [""])[0].strip() or "what this thread is about, in one line"
    return ("Before you stop, give the reason in this thread's status bar, where the person reads it. The hive keeps moving forward, "
            "so a run stops only for one of these:\n"
            "- Done: what was asked is delivered, checked and posted, and no next step toward the goal behind it is yours to take now.\n"
            "- Blocked: the next step needs something only someone else can give (say what and who), and nothing else toward the goal is left that you can do meanwhile.\n"
            "- Needs the person: a critical decision only they can make, asked in a post with a `For you:` line, and nothing else is left meanwhile.\n"
            "- Waiting: on a job or an agent's answer that wakes you (say which, and about when).\n"
            "- Told to stop: say by whom.\n"
            "If one of them honestly holds, end your last post in the thread with it as a line of its own (e.g. `Blocked: on what, "
            "and who has it`; it is copied to the bar, or read from the post when another agent set the bar just now), or set it and stop again:\n"
            f"hive status {thread} <<'X'\n{line1}\nBlocked: on what, and who has it\nX\n"
            "If none holds (a step is finished but the goal has a next one you can take, or the work got hard or long), don't stop: "
            "take the next step, and give the reason when one is true.")

def focus_nudge(name, rec):
    """A reminder at Stop when the run posted a result in a thread that is in a focus whose summary is older than that result
    (projects/ui-redesign/human-workflow.md, 2026-11-07: the agent that posts the result updates the focus summary in the same
    run; no cleanup pulses), and, once per conversation, when that thread is in no focus or still has no title (focus.file_nudge,
    throttled by its own record; the person, 2026-11-08, post 1108153548ec). Joined into the stop block when there is one.
    The summary reminder comes once per result, not once per run: the mark holds when it was last given, and only a result posted
    after that is asked about, so an agent that ignores it is not held. Once per run missed a result posted after the first
    reminder (a70a4, chipflow-build-195) and a thread filed at this very Stop, whose ask had used the run's one reminder
    (ad78e, a552f): projects/rsi-lab/pickup_causes.py, thread rsi-lab-pickups."""
    thread = rec.get("thread")
    if os.environ.get("HIVE_DOOR") or not rec.get("at") or not thread or WARDEN.fullmatch(name): return None
    f = STATE / "focus-asked" / name
    try:
        import focus
        at = dt.datetime.fromisoformat(rec["at"]); since = (at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp()
        results_from = since
        try:
            run, last = f.read_text().split()
            if run == rec["at"]: results_from = max(since, float(last))
        except (OSError, ValueError): pass
        posts = focus.board_posts()
        summary = "\n\n".join(focus.summary_nudges(name, thread, results_from, posts))
        msg = "\n\n".join(x for x in (summary, *(focus.file_nudge(name, th, since, posts) for th in focus.threads_posted(name, thread, since, posts))) if x)
    except Exception: return None
    if not msg: return None
    if summary:
        try: f.parent.mkdir(parents=True, exist_ok=True); f.write_text(f"{rec['at']} {time.time()}")
        except OSError: return None
    return msg

def stops(since_h=24.0):
    """`inbox.py stops [HOURS]`: what the stop gate saw. Per run: asked or not, the reason it stopped on, and whether it kept working after being asked (5 min or more to its last pass)."""
    runs = {}
    try: rows = [json.loads(l) for l in (STATE / STOP_LOG).read_text().splitlines() if l.strip()]
    except (OSError, ValueError): rows = []
    for r in rows:
        if r.get("at", 0) < time.time() - since_h * 3600 or r.get("event") == "owns_open": continue  # that one is measure_asks.py's, not the gate's
        x = runs.setdefault((r.get("name"), r.get("run")), {"asked": None, "last": None, "why": "", "event": ""})
        if r.get("event") == "asked" and x["asked"] is None: x["asked"] = r["at"]
        x["last"], x["event"] = r["at"], r.get("event")
        if r.get("event") == "passed": x["why"] = r.get("why", "")
    asked = [x for x in runs.values() if x["asked"] is not None]
    kept = [x for x in asked if x["last"] - x["asked"] >= 300]
    by = {}
    for x in runs.values():
        w = next((w for w in STOP_WORDS if x["why"].lower().startswith(w.lower() + ":")), "let go" if x["event"] == "let go" else "still running or ended without a reason")
        by[w] = by.get(w, 0) + 1
    print(f"stop gate, last {since_h:g} h: {len(runs)} runs, {len(asked)} asked for a reason, {len(kept)} of those kept working 5 min or more after the ask")
    for w, c in sorted(by.items(), key=lambda kv: -kv[1]): print(f"  {c:4d}  {w}")

MODEL_CHECK_AT = 40   # tool calls into a run: past Sonnet's median run (10 replies) and Opus's (25), where a wrong model costs most
FIRST_S = 120   # a run the person started and that has said nothing for this long is asked for one line

def own_posts(name, at, tail=300_000):
    """This agent's posts at or after `at`. Reads only the board's tail (the board is 5 MB; a run's own recent post is there). None if the board can't be read."""
    try:
        with BOARD.open("rb") as fh:
            fh.seek(max(0, os.fstat(fh.fileno()).st_size - tail)); data = fh.read()
    except OSError: return None
    out = []
    for line in data.decode(errors="replace").splitlines():
        if f'"from": "{name}"' not in line and f'"from":"{name}"' not in line: continue
        try: p = json.loads(line)
        except ValueError: continue
        if p.get("from") == name and p.get("at", "") >= at: out.append(p)
    return out

def posted_since(name, at, thread=None, tail=300_000):
    """Has this agent posted at or after `at` (in `thread`, if given)?"""
    ps = own_posts(name, at, tail)
    if ps is None: return True  # can't tell: stay quiet
    return any(not thread or p.get("thread") == thread for p in ps)

def first_line(name, rec, t=None):
    """Why a run the person started, FIRST_S ago or more, with no post from it since, should post a line now. Once per run,
    checked at the first tool call past FIRST_S. The person posts "hello?" after 1 to 3 min of silence (5 times on
    2026-11-04) and the watchdog starts an agent at 10 min: a line from the agent itself (what it took, when to expect the
    result) is cheaper than either and the only one that knows."""
    if os.environ.get("HIVE_DOOR") or waker(rec, name) != "person" or not rec.get("at"): return None
    try:
        at = dt.datetime.fromisoformat(rec["at"])
        age = (t or time.time()) - (at if at.tzinfo else at.replace(tzinfo=dt.timezone.utc)).timestamp()
    except ValueError: return None
    if age < FIRST_S: return None
    mark = STATE / "first" / name
    try:
        if mark.read_text() == rec["at"]: return None
    except OSError: pass
    mark.parent.mkdir(parents=True, exist_ok=True); mark.write_text(rec["at"])
    if posted_since(name, rec["at"], rec.get("thread")): return None
    return (f"The person's post started this run {age / 60:.0f} min ago and you have not posted since: they are waiting and see "
            f"nothing (text outside hive post reaches nobody). Make your next tool call an hive post in thread "
            f"{rec.get('thread') or 'you were started in'}: one or two lines, the answer if you have it, else what you are doing "
            "and when to expect the result. Then carry on.")

def model_check(name, rec):
    """Once per run, at the MODEL_CHECK_AT-th tool call: is this still the right model? Context only, no reply forced, and it
    asks both ways. Self-judged switches were 0 of 12 to 2026-11-05 (kb/model-cost.md): nothing asked at the moment work shifts."""
    now_on = rec.get("model")
    if os.environ.get("HIVE_DOOR") or not rec.get("at") or now_on not in STRENGTH or rec.get("fallback") or rec.get("harness", "claude-code") != "claude-code" or name.startswith("f"): return None
    f = STATE / "model-check" / name
    try: at, n = f.read_text().split(); n = int(n) if at == rec["at"] else 0
    except (OSError, ValueError): n = 0
    if n > MODEL_CHECK_AT: return None
    n += 1
    try: f.parent.mkdir(parents=True, exist_ok=True); f.write_text(f"{rec['at']} {n}")
    except OSError: return None
    if n != MODEL_CHECK_AT: return None
    move = (f'if what\'s left is routine with a good stretch to go, `hive model sonnet "why"` and end your turn, or add `--next` if you\'re nearly done' if now_on in ("opus", "fable")
            else 'if this has become design or debugging you\'re stuck on, `hive model opus "why"` and end your turn')
    return f"You're {MODEL_CHECK_AT} tool calls into this run on {now_on}. Still the right model? {move[0].upper() + move[1:]}. Otherwise carry on; no need to reply to this."

def recall_note(name, ps):
    """Pointers to our own notes that share words with these posts (`kb recall`), so related notes turn up without a search:
    a few lines, or nothing when kb isn't sure. Not for wardens, the person's door or the starter's own posts. Never holds up a delivery."""
    ids = [p["id"] for p in ps if str(p.get("from", "")).lower() not in ("starter", "watchdog")][:5]
    if not ids or WARDEN.fullmatch(name) or os.environ.get("HIVE_DOOR"): return ""
    try:
        r = subprocess.run([sys.executable, "-I", str(KB), "recall", "--for", name, *(x for i in ids for x in ("--post", i)), "--midrun"],
                           capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.SubprocessError): return ""
    return "\n\n" + r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else ""

def nested(rec, payload):
    """True for the hooks of a `claude -p` an agent started inside its own run: it inherits HIVE_NAME, so its Stop used up the run's one
    filing ask and it could take the agent's posts (a44e1, 2026-11-09: five stops in one run, the ask went to its test of a model; R34).
    A real run's session is the record's, or one the run's own output names if it changed; a tool result quoting one is escaped, so it can't match."""
    sid, own = payload.get("session_id"), rec.get("session")
    if not sid or not own or sid == own or rec.get("harness") != "claude-code": return False
    try: return f'"session_id":"{sid}"'.encode() not in Path(rec["out"]).read_bytes()
    except (OSError, KeyError, TypeError): return False

def main():
    name = os.environ.get("HIVE_NAME", "").lower()
    if not name: return
    if os.environ.get("HIVE_DOOR"): rec = {"at": ""}  # the person's door: everything for them not yet handed over, across sessions
    else:
        try: rec = json.loads((RUN / f"{name}.json").read_text())
        except (OSError, ValueError): return
    try: payload = json.load(sys.stdin)
    except ValueError: payload = {}
    if nested(rec, payload): return
    event = payload.get("hook_event_name", "")
    if not os.environ.get("HIVE_DOOR"):  # what this agent read, for `kb stats`. Must never stop a post being delivered
        try:
            import kbuse
            kbuse.observe(name, payload)
        except Exception: pass
    d = STATE / "delivered"; d.mkdir(parents=True, exist_ok=True)
    f = d / name
    seen = set(f.read_text().split()) if f.exists() else set()
    lines, done = read_new(name, rec)
    ps = new_posts(name, rec, seen, lines)
    if ps:
        with f.open("a") as fh: fh.write("".join(p["id"] + "\n" for p in ps) + f"@{int(time.time())}\n")
    done()
    first = first_line(name, rec) if event != "Stop" else None
    check = model_check(name, rec) if event != "Stop" else None
    if not ps:
        if event == "PostToolUse" and not first and not check and (end := end_post(name, rec, payload)):
            why = end_decision(name, rec, end)
            print(json.dumps({"continue": False, "stopReason": "ended on its post (hive post --end)"} if why is None else {"hookSpecificOutput": {"hookEventName": event, "additionalContext": why}}))
            return
        if event == "Stop" and (why := "\n\n".join(x for x in (unanswered(name, rec) or owns_open(name, rec), stop_gate(name, rec)) if x)):
            print(json.dumps({"decision": "block", "reason": "\n\n".join(x for x in (why, focus_nudge(name, rec)) if x)}))
        elif event == "Stop" and (fn := focus_nudge(name, rec)):
            print(json.dumps({"decision": "block", "reason": fn}))
        elif event == "Stop":
            try: run_end_bars(name, rec)
            except Exception: pass  # a bar never holds up a stop
        elif first or check:
            print(json.dumps({"hookSpecificOutput": {"hookEventName": event or "PostToolUse", "additionalContext": "\n\n".join(x for x in (first, check) if x)}}))
        return
    note = person_note(ps)
    msg = ("New on the board for you while you work (you'll get these automatically; answer with "
           "hive post where it matters):\n\n" + "\n\n".join(fmt(p) for p in ps) + recall_note(name, ps) + focus_hint(ps) + note + model_note(name, rec, ps))
    if event == "Stop":
        print(json.dumps({"decision": "block", "reason": msg}))
    else:
        if first and not note: msg += "\n\n" + first   # the person note already asks for a post
        if check: msg += "\n\n" + check
        print(json.dumps({"hookSpecificOutput": {"hookEventName": event or "PostToolUse", "additionalContext": msg}}))

if __name__ == "__main__":
    stops(float(sys.argv[2]) if sys.argv[2:] else 24.0) if sys.argv[1:2] == ["stops"] else main()
