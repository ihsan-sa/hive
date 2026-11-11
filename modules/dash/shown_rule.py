#!/usr/bin/env python3
"""Which agent posts the chat shows in full and which it cuts to one line: the rule picked in thread rsi-lab-shown (posts 111023053d84,
11102315f672). The rule is written here and only here: projects/rsi-lab/shown/replay.py imports it (reader.py through replay), so the test and the app
can't drift. The method, the reader test and its results are in projects/rsi-lab/shown/READER-TEST.md.

In each stretch of a thread (the posts between two of the person's), a post the chat would show stays in full when it is:
  - not by an agent (the person, another Hive, an outside sender), unless it is a script's post that doesn't name the person,
    which the chat leaves out altogether ("hidden");
  - addressed to the person: names them, has a `For you:` or `Needs the person:` line, @person, or replies to a post of theirs;
  - a post an ask was closed with (`asked`);
  - an ask said in passing (PASSING);
  - its run's last post, when a chain from the person reached that run (`reached`);
  - the stretch's newest agent post.
Every other post is one line: its time, who wrote it, and its first line with markdown stripped, cut at 140 characters. With `asking`
(the default) the line also carries the post's other lines that hold a question mark, so an ask phrased past every pattern still shows.

    decide(posts, agents, ...) -> {post id: {"show": "full" | "line" | "hidden", "line": str | None, "also": [str]}}

`posts` are one thread's board rows (dicts as in board.jsonl), oldest first, the person's posts included (they bound the stretches).
`visible` is the ids the chat shows today (default: all); other posts get no entry.

Tests: python3 -I ~/modules/dash/test_shown_rule.py
"""
import bisect, collections, datetime as dt, re

OUTSIDE = {"person", "box", "hives", "h0", "h1", "autobox"}
FOR_PERSON = {"phone", "morning"}                 # scripts that write for the person: never treated as a script here
HEX = re.compile(r"[a-z][0-9a-f]{4,5}")
FOR_YOU = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?for you(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(.+)$", re.I | re.M)
NEEDS = re.compile(r"^\s*(?:[-*>]\s*)?(?:\*\*|__)?needs the person(?:\*\*|__)?\s*:", re.I | re.M)
AT_PERSON = re.compile(r"(?<![\w@])@person\b")
PASSING = re.compile(r"for you\s*:|needs the person|(wait\w*|depends?) on (the person|you)\b", re.I)   # an ask said in passing
CUT = 140
MARK = re.compile(r"^\s*(?:#+|>+|[-*+]|\d+[.)])\s+")
INLINE = re.compile(r"\*\*|__|`|~~")
LINK = re.compile(r"!?\[([^\]]*)\]\([^)]*\)")


def is_script(name, agents):
    return not HEX.fullmatch(name) and name not in OUTSIDE and name not in FOR_PERSON and not name.startswith("hive-") and name not in agents


def mentions(p):
    return [str(x).lstrip("@") for x in p.get("to") or []]


def addresses(p, byid):
    q = byid.get(p.get("reply_to")) if p.get("reply_to") else None
    text = p.get("text") or ""
    return "person" in mentions(p) or bool(FOR_YOU.search(text) or NEEDS.search(text) or AT_PERSON.search(text)) or (q is not None and q.get("from") == "person")


def first_line(text, cut=CUT):
    """The post's first line with words in it, markdown stripped, cut at 140 characters."""
    for l in (text or "").split("\n"):
        l = INLINE.sub("", LINK.sub(r"\1", MARK.sub("", l))).strip()
        if re.search(r"\w", l):
            return l[:cut]
    return ""


def plain(l):
    return INLINE.sub("", MARK.sub("", l)).strip()


def asking_lines(text, cut=CUT):
    """The post's other lines that hold a question mark, each stripped and cut like the first."""
    first = first_line(text, cut)
    return [plain(l)[:cut] for l in (text or "").split("\n") if "?" in l and plain(l) and plain(l)[:cut] != first]


def stretches(posts):
    cur = []
    for p in posts:
        if p.get("from") == "person":
            if cur:
                yield cur
            cur = []
        else:
            cur.append(p)
    if cur:
        yield cur


def decide(posts, agents, visible=None, asked=frozenset(), run_last=frozenset(), reached=None, asking=True, byid=None):
    """posts: one thread's rows, oldest first. agents: names that have run as agents (log start events). asked: ids of posts an ask was
    closed with. run_last: ids of each run's last post. reached: of those, the ids whose run a chain from the person reached (None: all)."""
    byid = byid if byid is not None else {p.get("id"): p for p in posts}
    out = {}
    for st in stretches(posts):
        seen = [p for p in st if visible is None or p.get("id") in visible]
        mine = [p for p in seen if p.get("from") in agents]
        newest = mine[-1].get("id") if mine else None
        for p in seen:
            i, frm, text = p.get("id"), p.get("from") or "", p.get("text") or ""
            if is_script(frm, agents) and "person" not in mentions(p):
                out[i] = {"show": "hidden", "line": None, "also": []}
            elif (frm not in agents or i == newest or addresses(p, byid) or i in asked or PASSING.search(text)
                  or (i in run_last and (reached is None or i in reached))):
                out[i] = {"show": "full", "line": None, "also": []}
            else:
                out[i] = {"show": "line", "line": f"{(p.get('at') or '')[11:16]} {frm}: {first_line(text)}", "also": asking_lines(text) if asking else []}
    return out


# ---------- what the chat shows today, and which runs the person reached ----------
# Moved here from projects/rsi-lab/shown/replay.py (which imports them back) so the app and the reader test compute the same inputs.
# The kinds follow the app's postKind and closingPosts (core/store.js, core/model.js); replay.py --dump checks that parity.
SYS = {"starter", "dash", "anon", "all", "400", "mention", "name"}
OPS = {"watchdog", "memguard"}
HOUSE = {"keeper", "improve", "watchdog"}
SIDE = "side"
WARDEN = re.compile(r"(warden|rootwarden|w[0-9a-f]{4})")
ANSWER_FOR = 24 * 3600
YOU, UPDATES, COMMS = 1, 2, 4
PERSON = {"person", "phone", "box", "autobox"}   # for chains only: a call or the box starts a run for the person (the starter treats phone as the person too). Some phone posts are only "they are calling you", so they are not the person's words: stretches and evidence use `from: person` alone, as the app does
DEPTH, HOURS = 99, 24                            # a run counts as reached by any chain from a person's post at most 24 hours old


def ts(s):
    return dt.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()


class Post:
    __slots__ = ("id", "t", "at", "frm", "men", "th", "text", "reply_to", "routed", "kind", "run", "roots", "closer")

    def __init__(s, r):
        s.id, s.at, s.t, s.frm, s.th = r.get("id"), r["at"], ts(r["at"]), r.get("from") or "", r.get("thread")
        s.men = [str(x).lstrip("@") for x in r.get("to") or []]
        s.text, s.reply_to, s.routed = r.get("text") or "", r.get("reply_to"), bool(r.get("routed"))
        s.kind, s.run, s.roots, s.closer = 0, None, {}, False


class Run:
    __slots__ = ("name", "session", "start", "end", "thread", "post", "frm", "roots", "posts")

    def __init__(s, e):
        s.name, s.session, s.start, s.end = e["name"], e.get("session"), ts(e["at"]), None
        s.thread, s.post, s.frm, s.roots, s.posts = e.get("thread"), e.get("post"), e.get("from"), {}, []


def merge(into, roots, step=0):
    """roots: {depth: latest time the person spoke at the head of a chain that long}."""
    for d, t in roots.items():
        if t > into.get(d + step, -1):
            into[d + step] = t


def classify(board, log):
    """The app's postKind and closingPosts, on the board as it stands. Returns (agents, threads)."""
    agents = {e["name"] for e in log if e.get("event") == "start" and e.get("name")}
    byid = {p.id: p for p in board}
    threads = collections.defaultdict(list)
    last_person = {}
    for p in board:
        threads[p.th].append(p)
        ask = last_person.get(p.th)
        p.kind = kind(p, agents, byid, ask)
        if p.frm == "person":
            last_person[p.th] = p
    return agents, threads, byid


def kind(p, agents, byid, ask):
    if p.th == SIDE:
        return 0
    if p.frm == "person" or "person" in p.men:
        return YOU
    if p.frm in SYS or p.frm in OPS or WARDEN.fullmatch(p.frm) or p.th in HOUSE:
        return COMMS
    if any(m != p.frm and m not in OUTSIDE for m in p.men):
        return COMMS
    if p.frm in agents:
        q = byid.get(p.reply_to) if p.reply_to else None
        if q is not None and q.frm == "person":
            return YOU
        if FOR_YOU.search(p.text):
            return YOU
        if ask is not None and p.frm in ask.men and p.t - ask.t < ANSWER_FOR:
            return YOU
    return UPDATES


def link_runs(board, log, byid):
    """Walk the log and the board in time order: each post gets the run that wrote it, each run the chains from the person that reach it.

    A run is reached by the post that started it, the posts its start batched (`woke`), posts handed to it while live (`woken`, or naming it while it runs).
    A person's post is depth 0, the run it reaches depth 1, a run that run's post reaches depth 2, and so on.
    A start that only continues earlier work (the starter's run-end pass, or the same post starting it again after a limit, or an heir) keeps the earlier run's chains at the same depth.
    A new wake from a timer or a keeper round keeps nothing: that is what the rule is for."""
    ev = [(ts(e["at"]), 1, e) for e in log if e.get("event") in ("start", "end", "woken") and e.get("name")]
    ev += [(p.t, 0, p) for p in board]                # at a tie the post goes first: the board and the log stamp to the second, and the post that starts a run is often in the same second as its start
    ev.sort(key=lambda x: (x[0], x[1]))
    live, last, runs = {}, {}, []
    for t, is_event, x in ev:
        if not is_event:
            write_post(x, t, live, last)
            continue
        name, what = x["name"], x["event"]
        if what == "start":
            r = Run(x)
            prev = last.get(x.get("heir_of") or name)
            cont = prev is not None and (x.get("heir_of") or (x.get("resumed") and (x.get("post") == prev.post or (x.get("post") not in byid and x.get("from") == "starter")) or any(isinstance(s, dict) and s.get("continue") for s in x.get("slept") or [])))
            if cont:
                merge(r.roots, prev.roots)
            for pid in [x.get("post")] + list(x.get("woke") or []):
                q = byid.get(pid)
                if q is not None and not (cont and pid == prev.post):
                    merge(r.roots, q.roots, 1)
            if x.get("from") in PERSON and not r.roots:
                r.roots[1] = t                      # the starter says the person's post was in the batch it woke for
            live[name] = last[name] = r
            runs.append(r)
        elif what == "end":
            r = live.get(name)
            if r is not None and (not x.get("session") or x["session"] == r.session):
                r.end = t
                del live[name]
        elif what == "woken":
            r = live.get(name)
            if r is not None:
                for pid in x.get("posts") or []:
                    q = byid.get(pid)
                    if q is not None:
                        merge(r.roots, q.roots, 1)
    return runs


GRACE = 5


def write_post(p, t, live, last):
    if p.frm in PERSON:
        p.roots = {0: t}
    else:
        r = live.get(p.frm)
        if r is None:                              # the end event can land just before its last post
            q = last.get(p.frm)
            if q is not None and q.end is not None and t <= q.end + GRACE:
                r = q
        if r is not None:
            p.run = r
            p.roots = dict(r.roots)
            r.posts.append(p)
    for m in p.men:                                # reached while it runs (the inbox hook hands it over after a tool call)
        r = live.get(m)
        if r is not None and m != p.frm:
            merge(r.roots, p.roots, 1)


def closers(runs, threads):
    """closingPosts: each finished run's last post in a thread that has a For you post."""
    has_you = {th for th, ps in threads.items() if any(p.kind == YOU for p in ps)}
    out = set()
    for r in runs:
        if r.end is None or not r.thread or r.thread not in has_you:
            continue
        ps = threads[r.thread]
        for p in reversed(ps[:bisect.bisect_right([q.t for q in ps], r.end + GRACE)]):
            if p.t < r.start:
                break
            if p.frm == r.name:
                if p.kind != COMMS:
                    out.add(p.id)
                    p.closer = True
                break
    return out


def rooted(p, depth, hours):
    return any(d <= depth and (hours is None or p.t - t <= hours * 3600) for d, t in p.roots.items())


def board_shown(board_rows, log_rows, ask_rows=(), threads=None, depth=DEPTH, hours=HOURS):
    """The rule on the whole board as it stands: {post id: decide()'s entry}, for the posts the chat shows today.
    board_rows, log_rows: board.jsonl and log.jsonl rows. ask_rows: the asks ledger's rows (a done ask's `evidence` post stays in full).
    threads: only decide these (the chains still need the whole board and log)."""
    board = sorted((Post(r) for r in board_rows if isinstance(r, dict) and r.get("at") and r.get("id")), key=lambda p: p.t)
    agents, ths, byid = classify(board, list(log_rows))
    runs = link_runs(board, [e for e in log_rows if isinstance(e, dict) and e.get("at")], byid)
    closers(runs, ths)
    today = {p.id for p in board if p.frm != "person" and (p.kind & (YOU | UPDATES) or p.closer)}
    asked = {r["evidence"] for r in ask_rows if isinstance(r, dict) and r.get("evidence") and r.get("status") == "done"}
    run_last = {r.posts[-1].id for r in runs if r.posts}
    reached = {i for i in run_last if rooted(byid[i], depth, hours)}
    rows = {p.id: {"id": p.id, "from": p.frm, "to": p.men, "text": p.text, "reply_to": p.reply_to, "at": p.at} for p in board}
    out = {}
    for th, ps in ths.items():
        if threads is None or th in threads:
            out.update(decide([rows[p.id] for p in ps], agents, visible=today, asked=asked, run_last=run_last, reached=reached, byid=rows))
    return out
