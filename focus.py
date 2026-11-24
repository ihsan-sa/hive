#!/usr/bin/env python3
"""hive focus: foci, the person's view of the work (projects/ui-redesign/human-workflow.md, 2026-11-07).

Agents work by thread; a focus is an abstraction for the person: a label a thread carries and a summary agents write by hand.
The dashboard's home is one card per focus (Now, Needs you, Lately, Threads) and a focus page; the summary is the only text on
them that an agent writes, so it stays in plain words: no rung ids, post ids, paths or test names (depth 1 of the four depths).

  hive focus list [--json]              every focus in order: the person's priorities first (kb/priorities.md), then added ones
  hive focus new ID "name" [--off]      add a focus (an agent may; no sub-foci: that is a thread in the focus). --off retires it
  hive focus tag THREAD FOCUS [--off]   put a thread under a focus (--off: take it out). Without a tag a thread is in the focus
                                      whose own thread it is or came from (the dashboard follows parents); the rest is Misc
  hive focus link A B [--off]           two foci that depend on each other (the GTM focus needs the mech lab's result)
  hive focus relate FOCUS thread|agent|file X --why "..." [--off]   a thread, agent or file that bears on a focus without being in
                                      it (a result post that touched another focus: relate it there); the focus's json lists them as relations
  hive focus lead FOCUS ["text"]        In short: the one or two sentences the person reads first, on the home card and at the top of
                                      the focus page, for someone who reads nothing else (the person, 2026-11-16: "I can't get a
                                      good sense of what's going on"). It opens with the state and a colon, then says the one thing
                                      that matters now, measured against the focus's aim: "On track: ...", "At risk: ...",
                                      "Blocked: ...", "Needs you: ...", "Waiting: ...", "Paused: ..." or "Done: ...". The text
                                      comes as arguments or on stdin (a quoted heredoc); without one it prints what is set. It is
                                      kept apart from the lines, so a `set` that leaves it out keeps it. It is read cold, by someone
                                      who sees nothing else: it names the state, the one thing moving, and what is the person's
                                      ("Yours: ..." as its last words; "Nothing is yours." when the card lists nothing under
                                      Needs you), the same as the card's Needs you lines (HA-9); whoever changes what it
                                      says rewrites it, and the same text again says it still holds. --off takes it away
  hive focus set FOCUS <<'X'            the summary, markdown on stdin: `## Now` bullets (what is being done, who, where: @agent
                                      and #thread become pills; at most seven, each opening with its topic and a colon), `## Lately`
                                      bullets (decisions, turns, what changed) and, when something only the person can do or decide
                                      is open, `## Needs you` bullets (one line each, with the #thread): the page shows those as
                                      waiting on them, so never leave such a thing inside a Now line. Agents decide by default and
                                      say `Decided:` in the result instead. An `## In short` part on top is taken as the lead (hive
                                      focus lead). Plain words, high level relative to the focus. Lint runs and prints what it
                                      flags; it never refuses
  hive focus show FOCUS                 the summary as set, In short on top
  hive focus reader FOCUS               how far away the person is: when they last wrote in the focus, how often this week, when
                                      they last opened its page and how many of the card's lines are newer than that.
                                      Write the card for that distance: someone a day away needs what was done and found since,
                                      someone a week away needs where it stands and why. `set` and the stop reminder print it too
  hive focus aim FOCUS "text"           one line on what the focus is for (the name isn't always enough: the person, 2026-11-07);
                                      shown under the name on the home card and at the top of the focus page. Without one, the
                                      text after the thread on the focus's priorities line is used
  hive focus check add FOCUS "text" | list FOCUS | done ID... | untick ID...   the focus page's to-do list. A step only the
                                      person can do starts "For you:"; ticking one names every place that still tells them
                                      it waits (its For you line, an ask, a status bar, the card's Needs you), to close with it
  hive focus lint [FOCUS] [--json]      what reads wrong: ids, paths, rung words, test names, long bullets, a summary older than
                                      the focus's newest result post (stale), a focus with threads and no summary (missing), no
                                      In short (no lead), one with no state word, one too long or days older than the lines under
                                      it, more than seven Now bullets, a Now line that reads as something only the person can do
                                      (hidden ask), a date that has gone by since the line was written (past date), "today" or
                                      "tomorrow" in a line over a day and a half old (says today), an In short that says Needs
                                      you over a focus where nothing waits on them (needs nothing), a For you to-do that no open
                                      For you line, ask or status bar says (to-do off the list), the same For you to-do twice
                                      (to-do twice)
  hive focus seen FOCUS | --home        the person looked at the focus's page, or at the home (the dashboard writes it; an agent
                                      can't): the page marks what is new since the look before, and `reader` tells the writer how
                                      long ago that was
  hive focus order ID ID ...            the order foci show in (rail, home, this list): the listed ones first, the rest after in
                                      their usual order; the newest order wins (the dash's drag writes it). --reset: back to default
  hive focus moved [--since 2d]         threads moved between foci lately, newest first (from, to, who, when; up to a week back);
                                      the dashboard's home shows the same as Recently moved
  hive focus brief FOCUS [--set] [--file P]... [--drop NAME]... [--off]   the group's own instructions and files (everyday E37):
                                      every agent started in a thread of the focus is given them in its opening message (core/starter.py
                                      group_note). --set reads the instructions on stdin (empty: none); --file copies a file into
                                      ~/files/groups/FOCUS/; --drop takes one off; --off clears both. Without options it prints them
  hive focus json                       everything, for the dashboard (/api/foci)

The duty (time-invariant, no cleanup pulses): the agent that posts a result in a thread that is in a focus updates the focus's
summary in the same run. The inbox hook says so once when it sees the result post and an older summary.
Records go to ~/.hive/focus.jsonl, append-only: {kind: focus|tag|link|relation|summary|lead|aim|check|check_done|check_untick|file|order|brief|seen, ..., from, at}.
"""
import argparse, datetime as dt, json, os, re, sys, time
from pathlib import Path

HOME = Path.home()
CORE, STATE, KB = HOME / "core", HOME / ".hive", HOME / "kb"
FILE, BOARD, STATUS, LOG = STATE / "focus.jsonl", HOME / "board.jsonl", STATE / "thread-status.jsonl", HOME / "log.jsonl"
ID = re.compile(r"[a-z][a-z0-9-]{1,40}")
THREAD = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.:-]{0,100}")
FOCUS_LINE = re.compile(r"^\s*(\d+)\.\s+\*\*(.+?)\*\*\s*\((?:thread\s+)?`([^`]+)`\)?[,:]?[ \t]*([^\n]*)", re.M)   # goals.py's FOCUS, plus the rest of the line (a default aim)
RESULT = re.compile(r"^(Done|Blocked|Needs the person|Waiting|Told to stop):\s*\S", re.I)
MISC = "misc"
RELATE = ("thread", "agent", "file")   # what hive focus relate joins to a focus
MAX_SUMMARY, MAX_BULLET = 2500, 220
MAX_LEAD, LEAD_CUT, LEAD_OLD, MAX_NOW = 320, 600, 3 * 86400, 7   # In short is two sentences (flagged past 320, cut at 600) and is flagged when the lines moved on three days past it; past seven Now lines nobody reads the list (the person, 2026-11-16)
SEEN_EVERY, SEEN_VISIT, SEEN_LONG = 60, 900, 3600   # one seen record a minute at most; looks less than a quarter of an hour apart are one visit; in a visit that goes on, a mark lasts an hour
HOME_ID = "*home"                    # `hive focus seen --home`: the person looked at the home. Not a focus id (those open with a letter), so a focus may be called home
# The word an In short opens with, and the state the card shows for it. Seven states, each a different thing for the person to do:
# nothing (on track, done), look closer (at risk), expect nothing until something outside moves (blocked, waiting, paused), act (needs you).
VERDICT = {"on track": "on track", "going well": "on track", "ahead": "on track",
           "at risk": "at risk", "off track": "at risk", "behind": "at risk", "slipping": "at risk",
           "blocked": "blocked", "stuck": "blocked", "stalled": "blocked",
           "needs you": "needs you", "needs the person": "needs you", "waiting on you": "needs you", "waits on you": "needs you",
           "waiting": "waiting", "paused": "paused", "on hold": "paused", "parked": "paused",
           "done": "done", "finished": "done", "complete": "done"}
VERDICT_RX = re.compile(r"^[\s*_]*(" + "|".join(sorted(map(re.escape, VERDICT), key=len, reverse=True)) + r")[\s*_]*(?:[:.—–]|\s-\s)[\s*_]*", re.I)
VERDICTS = "On track, At risk, Blocked, Needs you, Waiting, Paused or Done"
BRIEF_MAX, BRIEF_FILES, BRIEF_FILE_MAX = 4000, 10, 10_000_000   # a group's instructions (characters), how many files, and each file's size
GROUP_FILES = HOME / "files" / "groups"
WINDOWS = {"day": 86400, "week": 7 * 86400}   # the home card's gauge: each focus's share of the hive's work over the last day and week
# What reads as agent talk on the person's pages. Each: (name, regex, what to do instead).
JARGON = [
    ("post id", re.compile(r"(?<!#)\b[0-9a-f]{12}\b"), "say what the post said, or drop it"),   # "#11160014bf8b" is a conversation the page draws by its title, not a post id
    ("path", re.compile(r"(?:^|[\s(`])(?:~/|/home/|[\w.-]+/)[\w./-]+\.(?:py|js|mjs|md|sh|json|html|css|ts|txt|yaml|yml|toml)\b"), "name the thing in words, not its file"),
    ("rung id", re.compile(r"\b(?:R\d[a-z]?|rung\s+\w+|prop\d+|[A-Z]\d\.\d)\b"), "say what the step does"),
    ("test name", re.compile(r"\b(?:drive|test)_\w+(?:\.mjs|\.py)?\b"), "say what was checked"),
    ("agent code", re.compile(r"(?<![@\w])[a-f]\d[0-9a-f]{3}\b"), "write it as @name so it becomes a pill, or leave the agent out"),
    ("tool talk", re.compile(r"\b(?:PR\s*#?\d+|commit\s+[0-9a-f]{6,}|stdin|regex|jsonl|API|endpoint|refactor)\b"), "say what it means for the work"),
]

# A Now or Lately line that reads as something only the person can do: it belongs under `## Needs you`, where the page shows it as
# waiting on them (2026-11-16: the page said "Nothing waits on you here" above a line saying only the person's sign-in would fix it).
ASK = re.compile(r"\b(?:only you(?:rs?)?\b|(?<!what )wait(?:s|ing)? (?:only )?(?:on|for) (?:[^.;:]{0,60}? and )?(?:you(?:rs?)?|the person(?:'s)?)\b"
                 r"|(?<!waits )(?<!wait )(?<!waiting )on you:|needs? your\b|you need to\b|you must\b|you have to\b|(?:until|unless) you\b"
                 r"|(?<!what )the person (?:must|has to|needs to|will have to) (?:approve|agree|decide|sign|pick|choose|confirm|answer|reply|pay|buy|say|send|rule|log in|plug|press|order|register|do (?:this|that|it))\b"
                 r"|(?:needs?|requires?|takes) (?:a human|(?:the person's|their) (?:sign-?in|log-?in|decision|call|approval|go-ahead|answer|reply|say-so|signature|payment|choice|pick|ruling))\b|requires? your\b"
                 r"|(?:sign-?in|log-?in|signature|payment) (?:is |was )?(?:required|needed)\b"
                 r"|once you (?:approve|agree|decide|sign|pick|choose|confirm|answer|reply|pay|say|send)\b|returns? (?:when|once|after) you\b"
                 r"|asked (?:you|the person) to (?:choose|decide|pick|approve|confirm|rule|say|sign)\b"
                 r"|(?:account|sign-?up|order|registration|licen[cs]e) in (?:your|the person's) name\b"
                 r"|(?<!from )your (?:sign-?in|log-?in|decision|call|approval|go-ahead|go(?![\w-])|answer|reply|say-so|signature|payment|choice|pick|part|turn|move|ruling)\b"
                 r"|(?:for you|yours|up to you) to (?:decide|sign|approve|pick|choose|confirm|answer|pay|buy|send|say|do|make|read and judge)\b|(?:is|are|stays?) yours\b)", re.I)
# The same words after a "nothing", "never" or "not" in the same clause say the opposite ("Nothing waits on you here", "it never hides a
# thread waiting on you", "nothing for you to sign"): not an ask. "Nothing runs there until you sign in" still is one, so until and unless
# are read whatever stands before them. (2026-11-16: a home where every ask was answered showed "1 may be yours" on all 18 focuses, from
# the line that said nothing waits; 11 of the 200 card lines ever marked were of this kind.)
NOT_ASK = re.compile(r"\b(?:nothing|no one|nobody|none|never|not|no|no longer|isn't|aren't|doesn't|don't|won't)\b[^.;:,()]{0,40}$", re.I)
def reads_as_ask(t):
    """The words of line `t` that say something is the person's to do, or None."""
    for m in ASK.finditer(t or ""):
        if re.match(r"(?:until|unless)\b", m.group(0), re.I) or not NOT_ASK.search(t[:m.start()]): return m.group(0)
    return None

STEP_ID = re.compile(r"\b(?:R-?\d{2,4}[\w-]*|[A-Z]{2}-\d+[a-z]?)\b")   # a ladder step id (R107, R-1114-c, IM-1b): the to-do list is the person's, ids mean nothing there

# Dates and day words in the person's text (lint's "past date" and "says today", 2026-11-16). A date that was still ahead when
# the line was written and is behind now ("the boards land 11-14", read on the 16th) reads as news that never came, and "today"
# in a line two days old names the wrong day. Forms: 2026-11-18, 11-18 (two digits each, not a count: "10-12 hours"), Nov 18,
# November 18th, 18 Nov. "may" counts as a month only with its capital.
_MONTHS = "jan feb mar apr may jun jul aug sep oct nov dec".split()
_MONTH = r"(?:(?i:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)|May)"
_NTH = r"(?:st|nd|rd|th)?"
_UNIT = r"(?!\s*(?:%|°|(?i:percent|of|hours?|hrs?|h|min(?:ute)?s?|days?|weeks?|months?|years?|times|agents?|posts?|threads?|runs?|items?|lines?|steps?|tests?|checks?|points?|pages?|rows?|files?|words?|px|ms|mm|cm|kg|gb|mb|k?hz|mhz)\b))"
DATE_RX = re.compile(rf"\b(?P<y>20\d\d)-(?P<m1>\d\d)-(?P<d1>\d\d)\b"
                     rf"|\b(?P<mon2>{_MONTH})\.?\s+(?P<d2>\d{{1,2}}){_NTH}(?:\s*(?:to|-|–)\s*(?P<e2>\d{{1,2}}){_NTH})?\b"
                     rf"|\b(?P<d3>\d{{1,2}}){_NTH}\s+(?P<mon3>{_MONTH})\b"
                     rf"|(?<![\w/.:-])(?P<m4>\d\d)-(?P<d4>\d\d)(?![\w/:-]|\.\d){_UNIT}")
DATE_TO = re.compile(r"^\s*(?:to|until|through|and|-|–|—)\s*$", re.I)   # between two dates of one span ("11-14 to 11-18"): only its end can be late
DAY_WORD = re.compile(r"\b(?:today|tonight|tomorrow|yesterday|this (?:morning|afternoon|evening)|last night)\b", re.I)
DAY_WORD_OLD = 36 * 3600            # a day word is wrong once the line is this old
BAR_NAME, BAR_NEEDS = re.compile(r"\s*\(a[0-9a-f]{4,5}\)"), re.compile(r"^Needs the person:\s*", re.I)   # a status bar's last line, as the dash's standLine reads it
BAR_DAYS = 7                        # how long a status bar's wait stays listed (the dash's NEEDS_FOR); a For you line's days are away.py's FOR_YOU_DAYS
BTW = re.compile(r"^btw-(.+)-[0-9a-f]{4}$")   # a side question's thread names the conversation it was asked in
FOR_YOU_TODO = re.compile(r"^\W*for you\W*:\s*", re.I)   # a to-do the agents put on the person ("For you: order the boards")
# A to-do nothing waits for: it is the person's to take up when they like ("if you want it proven", "when a spare machine is free",
# "say if you want one"), or it is held ("don't press yet"). foryou.OFFER reads a line's opening words; a to-do is one clause,
# so the offer may come at its end.
NO_RUSH = re.compile(r"^\W*(?:if|only if|when(?:ever)?|once|after|next time|some ?time|any ?time|optional(?:ly)?)\b"
                     r"|\bif you(?:'d| would)? (?:want|wish|like|prefer|ever)\b|\b(?:say|tell (?:me|us)|let (?:me|us) know) if\b"
                     r"|\bno (?:rush|hurry)\b|\boptional\b|\bwhen(?:ever)? you (?:want|like|can|have|get)\b|\b(?:not|don'?t \w+) yet\b", re.I)
POST_ID = re.compile(r"\b[0-9a-f]{12}\b")   # a post a line names ("post 11160104f457")
TWIN_GONE = ("offer", "ends by itself")   # a line that came off the person's list this way never waited: its to-do is theirs to take up or leave

def now(): return time.time()
def who(): return os.environ.get("HIVE_NAME", "anon")

def records():
    out = []
    try: lines = FILE.read_text().splitlines()
    except OSError: return out
    for ln in lines:
        try: r = json.loads(ln)
        except ValueError: continue
        if isinstance(r, dict) and r.get("kind"): out.append(r)
    return out

def append(rec):
    import fcntl
    rec = {**rec, "from": rec.get("from") or who(), "at": rec.get("at") or now()}
    STATE.mkdir(parents=True, exist_ok=True)
    with open(FILE, "a") as f:
        fcntl.flock(f, fcntl.LOCK_EX); f.write(json.dumps(rec) + "\n")
    return rec

def priorities(text=None):
    """The person's focuses from kb/priorities.md, in their order: [{id, name, n, thread}], id = the focus's own thread."""
    if text is None:
        try: text = (KB / "priorities.md").read_text(errors="replace")
        except OSError: return []
    seen, out = set(), []
    for n, title, th, rest in FOCUS_LINE.findall(text):
        th = th.strip().lower()
        if th in seen: continue
        seen.add(th); out.append({"id": th, "name": title.strip(), "n": int(n), "thread": th, "aim": re.sub(r"\s*\[[^\]]*\]", "", rest).strip(" ,;:.")[:200] or None})
    return out

HEADING = re.compile(r"^(?:#{1,3}\s*)?(in short|in brief|verdict|bottom line|tl;?dr|now|currently|lately|recent(?:ly)?.*|evolution.*|needs? (?:you|me|the person)|decisions?|for you)\s*:?\s*$", re.I)
def heading(line):
    """The part a summary line opens ("lead", "now", "lately", "needs"), or None when it is not a heading."""
    m = HEADING.match(line.strip())
    if not m: return None
    h = m.group(1).lower()
    return "lead" if h.startswith(("in ", "verdict", "bottom", "tl")) else "now" if h in ("now", "currently") else "needs" if h.startswith(("need", "decision", "for")) else "lately"

def parse_summary(text):
    """`## Now`, `## Lately` and `## Needs you` bullets out of the summary's markdown; a bullet-less part is kept as one bullet.
    An `## In short` part is passed over: it is kept in a record of its own (split_lead)."""
    parts, cur = {"now": [], "lately": [], "needs": []}, None
    for ln in (text or "").splitlines():
        s = ln.strip()
        if not s or LEAD_INLINE.match(s): continue
        h = heading(s)
        if h: cur = h; continue
        if cur is None: cur = "now"
        if cur != "lead": parts[cur].append(re.sub(r"^[-*•]\s+", "", s))
    return parts

LEAD_INLINE = re.compile(r"^#{1,3}\s*(?:in short|in brief|verdict|bottom line|tl;?dr)\s*[:.]\s*(\S.*)$", re.I)   # "## In short: On track: ..." on one line
def split_lead(text):
    """(the In short part as one string, the summary without it): the lead is kept in a record of its own, so the lines can be
    rewritten without it and it can be rewritten without them."""
    lead, rest, cur = [], [], None
    for ln in (text or "").splitlines():
        m = LEAD_INLINE.match(ln.strip())
        if m: cur = "lead"; lead.append(m.group(1)); continue
        h = heading(ln) if ln.strip() else None
        if h: cur = h
        if cur == "lead":
            if not h and ln.strip(): lead.append(re.sub(r"^[-*•]\s+", "", ln.strip()))
        else: rest.append(ln)
    return " ".join(" ".join(lead).split()), "\n".join(rest).strip()

def lead_state(text):
    """An In short's state and the sentence after it: "At risk: the board is late" gives ("at risk", "The board is late");
    (None, text) when it opens with no state word."""
    text = " ".join(str(text or "").split())
    m = VERDICT_RX.match(text)
    body = text[m.end():].strip() if m else ""
    if not body: return None, text
    return VERDICT[m.group(1).lower()], body[:1].upper() + body[1:]

def norm(t): return " ".join(str(t).split()).lower()
# The words of an ask that say what it is about: no #thread or @name, no word every ask shares ("only you", "waits on the person").
ASK_STOP = frozenset("the and for you your yours with that this from are was has have had not but can will its into only person persons needs need wait waits waiting until when once here there them they their what which".split())
def ask_words(t): return {w for w in re.findall(r"[a-z0-9]{3,}", re.sub(r"[#@][\w.:-]+", " ", str(t).lower())) if w not in ASK_STOP}
def ask_key(t): return norm(t), ask_words(t)
def same_key(a, b):
    """same_ask on two ask_key()s, for a caller that compares one line against many."""
    n = len(a[1] & b[1])
    return (bool(a[0]) and a[0] == b[0]) or (n >= 3 and n >= 0.6 * min(len(a[1]), len(b[1])))
def near_key(a, b, least=2, share=0.4):
    """same_key with a lower bar, for two lines already known to be in one focus, where fewer other lines could share the words:
    a to-do against the For you line it was copied from, which says the same at three times the length ("decide whether to pay
    for app signing" against a line that sets out both costs)."""
    n = len(a[1] & b[1])
    return same_key(a, b) or (n >= least and n >= share * min(len(a[1]), len(b[1])))
def same_ask(a, b):
    """Whether two lines say the same ask in other words: they share three or more telling words, and those are most of the shorter
    line's. A Needs you line and a reworded Now line then count once (2026-11-16 review: the card said 3 wait where the agents listed 2)."""
    return same_key(ask_key(a), ask_key(b))

def aligned(lead, card):
    """HA-9h: an In short an agent saves gets its state word and "Yours:" tail made to agree with the card's Needs you lines, in code
    (rollup.align), as the timer's own writer already does; a cold reader got these wrong on the live cards the agents wrote."""
    try:
        sys.path.insert(0, str(Path(__file__).resolve().parent)); import rollup   # hive runs this with -I: the folder is not on the path
        return rollup.align(lead, card, MAX_LEAD)[:LEAD_CUT] or lead
    except Exception: return lead

def set_lead(f, text, r):
    """Put the In short of record `r` on focus `f`; no text takes it away. {text, state, body, from, at, since, was, turned}: `since`
    is when it began to say this and `at` when it was last written, so the same text again says "read against the lines, still holds";
    `was` is the state before the one it has and `turned` when that changed (None: it has had one state)."""
    text = " ".join(str(text or "").split())[:LEAD_CUT]
    if not text: f["lead"] = None; return
    prev = f.get("lead") or {}
    st, body = lead_state(text)
    same = prev.get("state") == st or not prev   # the state it had before this one, and when it turned: the card says "was on track" while that is new to the person
    f["lead"] = {"text": text, "state": st, "body": body, "from": r.get("from"), "at": r.get("at"),
                 "since": prev.get("since") if norm(prev.get("text") or "") == norm(text) else r.get("at"),
                 "was": prev.get("was") if same else prev.get("state"), "turned": prev.get("turned") if same else r.get("at")}

def look(p, t):
    """The person's looks at one place (a focus's page, the home) after one more at time `t`; `p` is the record before, or None.
    {at: the newest look, start: the first of its visit, base: the last look of the visit before (None: there was none), looks: this
    visit's looks of the last SEEN_LONG and the one before them}. A visit is looks less than SEEN_VISIT apart: by the gap between
    looks, not by the time since its first, so the marks do not go a quarter of an hour into a read (review, 2026-11-16)."""
    if p and t - float(p.get("at") or 0) <= SEEN_VISIT:
        ls = list(p.get("looks") or [p["at"]]) + [t]
        k = max((i for i, x in enumerate(ls) if x <= t - SEEN_LONG), default=0)
        return {**p, "at": t, "looks": ls[k:]}
    return {"at": t, "start": t, "base": p["at"] if p else None, "looks": [t]}

def since_seen(seen, at=None):
    """The time the person's marks count from: what is newer than this is new to them. During a visit it is their last look of the
    visit before (so what was new when they came stays marked while they read), or, in a visit that has gone on past SEEN_LONG, their
    newest look that old (what they have had an hour with is not new); after the visit, their last look. None: never looked, or
    this is the first visit, and nothing is marked."""
    if not seen: return None
    t = now() if at is None else at
    if t - float(seen.get("at") or 0) > SEEN_VISIT: return seen.get("at")
    old = [x for x in seen.get("looks") or [] if x <= t - SEEN_LONG]
    return max(old) if old else seen.get("base")

def seen_out(seen, at=None):
    """A look record as the pages get it: {at, start, base, since}, `since` worked out here so every page agrees."""
    return {"at": seen.get("at"), "start": seen.get("start"), "base": seen.get("base"), "since": since_seen(seen, at)} if seen else None

def board_posts():
    out = []
    try: lines = BOARD.read_text(errors="replace").splitlines()
    except OSError: return out
    for ln in lines:
        try: p = json.loads(ln)
        except ValueError: continue
        if isinstance(p, dict) and p.get("thread"): out.append(p)
    return out

HASH = re.compile(r"[a-z][0-9a-f]{4,}")  # an agent's name (hive's HASH_NAME)

def last_agent(posts, thread):
    """The agent who last posted in `thread` (board order); when none has, an agent running on the thread (its run file), so a
    copy passed on there doesn't start a second one beside it; else None."""
    last = None
    for p in posts:
        if p.get("thread") == thread and HASH.fullmatch(str(p.get("from") or "").lower()): last = str(p["from"]).lower()
    if last: return last
    for f in sorted((HOME / "run").glob("*.json")):
        try: r = json.loads(f.read_text()); os.kill(int(r["pid"]), 0)
        except (OSError, ValueError, KeyError, TypeError): continue
        if r.get("thread") == thread and HASH.fullmatch(str(r.get("name") or "").lower()): return str(r["name"]).lower()
    return None

def keeper(f, posts):
    """Who keeps a focus, so a post naming the focus reaches someone (the person, 110717432468): the agent who wrote its latest
    summary, unless another agent posted in its own thread since; None when nobody has."""
    s = f.get("summary") or {}
    best = (ts(s.get("at")), str(s["from"]).lower()) if HASH.fullmatch(str(s.get("from") or "").lower()) else (0.0, None)
    th = f.get("thread") or f["id"]
    for p in posts:  # a post passed on into the thread, or one asking another agent there, doesn't make its author the keeper
        if p.get("thread") != th or p.get("relay_of") or not HASH.fullmatch(str(p.get("from") or "").lower()) or ts(p.get("at")) < best[0]: continue
        if any(HASH.fullmatch(str(t).lstrip("@").lower()) and str(t).lstrip("@").lower() != str(p["from"]).lower() for t in (p.get("to") or [])): continue
        best = (ts(p.get("at")), str(p["from"]).lower())
    return best[1]

def reader(f, posts, now=None):
    """How far away the person is from a focus, as one line for whoever writes its card: when they last wrote in any of its
    threads and how often in the last week. The card is written for that distance (the person, 2026-11-10: "reading the summary
    should answer this question, not me having to message you"). A fact for judgement, never a gate."""
    now = now or dt.datetime.now(dt.timezone.utc).timestamp()
    th = set(f.get("threads") or []) | {f.get("thread") or f["id"]}
    at = sorted(ts(p.get("at")) for p in posts if p.get("thread") in th and str(p.get("from", "")).lower() == "person")
    seen, s = (f.get("seen") or {}).get("at"), f.get("summary") or {}
    span = lambda x: f"{x * 60:.0f} minutes" if x < 1 else f"{x:.0f} hours" if x < 48 else f"{x / 24:.0f} days"
    new = sum(1 for a in (s.get("now_at") or []) + (s.get("lately_at") or []) if seen and float(a or 0) > seen)
    look = "" if not seen else f" They last opened its page {span((now - seen) / 3600)} ago" + (f"; {new} of the card's lines are newer than that, and the page marks those as new to them." if new else ", and have seen every line on the card.")
    if not at: return "The person has never written in this focus: the card is all they know of it, so it says what the work is for, where it stands and what changed, before any detail." + look
    h = (now - at[-1]) / 3600; week = sum(1 for t in at if now - t <= 7 * 86400)
    days = len({int(t // 86400) for t in at if now - t <= 7 * 86400})
    back = [t for t in at if at[-1] - t > 3600]            # a post just now after a long gap is someone coming back, not someone close
    away = (at[-1] - back[-1]) / 3600 if h < 3 and back else h
    came = f", coming back after {span(away)} away" if away > h else ""
    how = ("they are following closely, so the card can be about the current step" if away < 3 else
           "its first bullets say what was done and what was found since then, at the level they would ask about, then what is next" if away < 72 else
           "they have been away from it, so it opens with where the whole thing stands and why, and leaves out steps")
    return f"The person last wrote in this focus {span(h)} ago{came} ({week} posts on {days} of the last 7 days): {how}." + look

def ts(at):
    if isinstance(at, (int, float)): return float(at)  # records carry time.time(); posts an ISO string
    try: return dt.datetime.fromisoformat(str(at).replace("Z", "+00:00")).timestamp()
    except (TypeError, ValueError): return 0.0

def state(recs=None, pri=None, posts=None):
    """Everything resolved: {foci: [...], tags: {thread: focus}, order: [ids]}. A focus: {id, name, n?, thread?, own, aim, links, summary, checks, threads, files}."""
    recs = records() if recs is None else recs
    foci, order = {}, []
    for f in (priorities() if pri is None else pri):
        foci[f["id"]] = {"aim": None, **f, "own": True, "links": [], "relations": [], "lead": None, "summary": None, "checks": [], "threads": [], "files": []}; order.append(f["id"])
    for r in recs:
        if r["kind"] == "focus":
            fid = r.get("id")
            if not fid: continue
            if r.get("off"):
                if fid in foci and not foci[fid].get("own"): del foci[fid]; order.remove(fid)
            elif fid not in foci:
                foci[fid] = {"id": fid, "name": r.get("name") or fid, "thread": r.get("thread"), "own": False, "aim": None, "links": [], "relations": [], "lead": None, "summary": None, "checks": [], "threads": [], "files": [], "made": r.get("at")}; order.append(fid)
            else: foci[fid]["name"] = r.get("name") or foci[fid]["name"]
    tags = {}
    for r in recs:
        if r["kind"] == "tag" and r.get("thread"):
            if r.get("off"): tags.pop(r["thread"], None)
            elif r.get("focus") in foci: tags[r["thread"]] = r["focus"]
    moved = moves(recs, foci)
    links = set()
    for r in recs:
        if r["kind"] == "link" and r.get("a") in foci and r.get("b") in foci:
            k = tuple(sorted((r["a"], r["b"])))
            if r.get("off"): links.discard(k)
            else: links.add(k)
    for a, b in links: foci[a]["links"].append(b); foci[b]["links"].append(a)
    rel = {}   # (focus, kind, id) -> the newest relation row; --off removes it
    for r in recs:
        if r["kind"] == "relation" and r.get("focus") in foci and r.get("what") in RELATE and r.get("target"):
            k = (r["focus"], r["what"], r["target"])
            if r.get("off"): rel.pop(k, None)
            else: rel.pop(k, None); rel[k] = {"kind": r["what"], "id": r["target"], "why": r.get("why") or "", "from": r.get("from"), "at": r.get("at")}
    for (fid, _, _), x in rel.items(): foci[fid]["relations"].append(x)
    for r in recs:
        if r["kind"] == "aim" and r.get("focus") in foci: foci[r["focus"]]["aim"] = (r.get("text") or "").strip() or None
    for r in recs:   # In short and the lines, each in the order it was written
        if r["kind"] == "lead" and r.get("focus") in foci: set_lead(foci[r["focus"]], None if r.get("off") else r.get("text"), r)
        elif r["kind"] == "summary" and r.get("focus") in foci:
            f = foci[r["focus"]]; prev = f["summary"] or {}
            lead, text = split_lead(r.get("text"))
            if lead and norm(lead) != norm((f["lead"] or {}).get("text") or ""): set_lead(f, lead, r)   # an In short inside a summary counts when it says something new
            parts = parse_summary(text)
            # needs_at[i]: when the i-th Needs you bullet first showed in an unbroken run of summaries, so a rewrite doesn't make an old
            # ask look new (the person, 110813526b10: a needs-you that sits a day on fast work is likely obsolete).
            was = {norm(t): a for t, a in zip(prev.get("needs") or [], prev.get("needs_at") or [])}
            parts["needs_at"] = [was.get(norm(t), r.get("at")) for t in parts["needs"]]
            # now_at, lately_at: the same for the Now and Lately lines (one map, so a line moved from Now to Lately keeps its time): the
            # page marks what is new since the person last looked.
            was = {norm(t): a for k in ("now", "lately") for t, a in zip(prev.get(k) or [], prev.get(k + "_at") or [])}
            for k in ("now", "lately"): parts[k + "_at"] = [was.get(norm(t), r.get("at")) for t in parts[k]]
            # Now lines that read as the person's to do: the page lists them as waiting on them. One that says in other words what a
            # Needs you line already says is that ask, not a second one (same_ask).
            parts["hidden"] = [i for i, t in enumerate(parts["now"]) if reads_as_ask(t) and not any(same_ask(t, n) for n in parts["needs"])]
            f["summary"] = {"text": text, "from": r.get("from"), "at": r.get("at"), **parts}
    # The person's looks, at a focus's page or at the home (look() above keeps the visit); since_seen() turns a record into the time
    # their "new" marks count from.
    seen = {}
    for r in recs:
        if r["kind"] == "seen" and r.get("from") == "person" and (r.get("focus") in foci or r.get("focus") == HOME_ID):
            seen[r["focus"]] = look(seen.get(r["focus"]), float(r.get("at") or 0))
    for fid, x in seen.items():
        if fid in foci: foci[fid]["seen"] = x
    checks = {}
    for r in recs:
        # The person's tick waits for an agent to confirm it (the person, 1108133400fb): done_by "person" and no ok yet. An agent's
        # own done, or its confirm of the person's tick, sets ok: the step is finished and leaves the open list.
        if r["kind"] == "check" and r.get("focus") in foci and r.get("id"): checks[r["id"]] = {"id": r["id"], "focus": r["focus"], "text": r.get("text") or "", "from": r.get("from"), "at": r.get("at"), "done": None, "done_by": None, "ok": None}
        elif r["kind"] == "check_done" and r.get("id") in checks:
            c = checks[r["id"]]; c["done"] = r.get("at"); c["done_by"] = r.get("from"); c["ok"] = None if r.get("from") == "person" else {"from": r.get("from"), "at": r.get("at")}
        elif r["kind"] == "check_ok" and r.get("id") in checks and checks[r["id"]]["done"]: checks[r["id"]]["ok"] = {"from": r.get("from"), "at": r.get("at")}
        elif r["kind"] == "check_untick" and r.get("id") in checks: checks[r["id"]].update(done=None, done_by=None, ok=None, why=r.get("why") or None)
    for c in checks.values(): foci[c["focus"]]["checks"].append(c)
    for th, fid in tags.items(): foci[fid]["threads"].append(th)
    files = {}   # kb notes filed under a focus (kb file --from POST / --focus): path -> {path, post, from, at}; the newest filing of a path wins, --off removes it
    for r in recs:
        if r["kind"] == "file" and r.get("focus") in foci and r.get("path"):
            if r.get("off"): files.pop((r["focus"], r["path"]), None)
            else: files[(r["focus"], r["path"])] = {"path": r["path"], "post": r.get("post"), "from": r.get("from"), "at": r.get("at")}
    for (fid, _), x in sorted(files.items(), key=lambda kv: -(kv[1].get("at") or 0)): foci[fid]["files"].append(x)
    for r in recs:   # a group's instructions and files (E37): the newest record holds the whole of both
        # Only the person's (an agent's record would steer every chat in the group as if the person said it), only files in the group's
        # own folder (a hand-written record can't inline ~/.hive/...), and none from before the focus was last made (a re-used id starts clean).
        if r["kind"] == "brief" and r.get("focus") in foci and r.get("from") == "person" and (r.get("at") or 0) >= (foci[r["focus"]].get("made") or 0):
            own = (GROUP_FILES / r["focus"]).resolve()
            text = (r.get("text") or "").strip()
            files = [x for x in r.get("files") or [] if isinstance(x, str) and Path(x).resolve().parent == own and not Path(x).is_symlink()]
            foci[r["focus"]]["brief"] = {"text": text, "files": files, "from": r.get("from"), "at": r.get("at")} if text or files else None
    for f in foci.values():
        f.setdefault("brief", None); f.setdefault("seen", None)
        if f.get("thread") and f["thread"] not in f["threads"]: f["threads"].insert(0, f["thread"])
    order = ordered(order, recs)
    # Needs-you items the person put off (the person, 1108133400fb): key -> {key, th, focus, text, t, what: "later"|"moot", from, at};
    # "back" takes one off again. The item's t is kept so a newer question in the same conversation still shows.
    later = {}
    for r in recs:
        if r["kind"] == "later" and r.get("key"):
            if r.get("what") == "back": later.pop(r["key"], None)
            elif r.get("what") in ("later", "moot"): later[r["key"]] = {k: r.get(k) for k in ("key", "th", "focus", "text", "t", "what", "from", "at")}
    return {"foci": [foci[i] for i in order], "tags": tags, "order": order, "later": sorted(later.values(), key=lambda x: -(x.get("at") or 0)), "moved": moved, "home_seen": seen.get(HOME_ID)}

MOVED_FOR, MOVED_MAX, MOVED_MERGE = 7 * 86400, 80, 600
def moves(recs, foci, at=None):
    """Threads moved between foci lately, newest first (the person, 110901466282: hard to know where a thread went once it moved):
    [{thread, was, to, by, at}], `was` the thread's previous tag (None: it had none, so it sat in Other or followed its parent),
    `to` None when it was taken out. A re-tag to where it already was isn't a move; moves of one thread within 10 minutes are one."""
    cur, out, last = {}, [], {}
    for r in recs:
        if r.get("kind") != "tag" or not r.get("thread"): continue
        th = r["thread"]; to = None if r.get("off") else r.get("focus")
        if to is not None and to not in foci: continue
        was = cur.get(th)
        if to == was: continue
        if to is None: cur.pop(th, None)
        else: cur[th] = to
        m = last.get(th); t = r.get("at") or 0
        if m and t - m["at"] < MOVED_MERGE:   # off then on, or a quick correction: one move from where it first was
            m.update(to=to, by=r.get("from"), at=t)
            if m["to"] == m["was"]: out.remove(m); last.pop(th)
            continue
        m = {"thread": th, "was": was, "to": to, "by": r.get("from"), "at": t}; out.append(m); last[th] = m
    since = (now() if at is None else at) - MOVED_FOR
    return sorted((m for m in out if m["at"] >= since), key=lambda m: -m["at"])[:MOVED_MAX]

def ordered(ids, recs):
    """`ids` (the default order) rearranged by the newest order record: the ids it lists that exist first, in its order, then the
    rest in their default order. An empty order record (--reset) gives the default back."""
    want = next((r.get("ids") for r in reversed(recs) if r["kind"] == "order" and isinstance(r.get("ids"), list)), None)
    if not want: return list(ids)
    have, first = set(ids), []
    for i in want:
        if i in have and i not in first: first.append(i)
    return first + [i for i in ids if i not in first]

def find(st, key):
    key = (key or "").strip().lstrip("#").lower()
    if not key: return None   # "" would match the first focus with no thread of its own
    for f in st["foci"]:
        if f["id"] == key or (f.get("thread") or "") == key or f["name"].lower() == key or (f.get("n") and key == str(f["n"])): return f
    return None

def focus_of(st, thread, posts=None):
    """The focus a thread is in: its tag, else the focus whose own thread it is, else its first post's parent's focus; None is Misc."""
    if thread in st["tags"]: return st["tags"][thread]
    for f in st["foci"]:
        if thread in (f.get("thread"), f["id"]): return f["id"]
    if posts is not None:
        mine = [p for p in posts if p.get("thread") == thread]
        first = min(mine, key=lambda p: ts(p.get("at"))) if mine else None
        par = first and first.get("parent")
        if par and par != thread: return focus_of(st, par, None)
    return None

def focus_finder(st, posts):
    """thread -> the focus the pages show it under (None: Other), for a caller that places many threads: one pass over the board,
    then the dash's own rule (foci.js focusOf), up the chain of the conversations it was started from: its tag, else the focus
    whose own thread it is, else the same for its parent, eight steps at most. focus_of looks one parent up and no further."""
    own, first, memo = {}, {}, {}
    for f in st["foci"]:
        for k in (f.get("thread"), f["id"]):
            if k: own.setdefault(k, f["id"])
    for p in posts:
        th = p.get("thread")
        if th and th not in first: first[th] = p.get("parent")   # the board is in time order: a thread's first post is its first line
    def of(thread):
        if thread not in memo:
            t, out = thread, None
            for _ in range(8):
                if not t: break
                if t in st["tags"]: out = st["tags"][t]; break
                if t in own: out = own[t]; break
                m = BTW.match(str(t))
                t = m.group(1) if m else first.get(t)
            memo[thread] = out
        return memo[thread]
    return of

def late_dates(text, written, at=None):
    """The dates in a line that were still ahead on the day it was written and are behind today: [(as written, date)]. Days are
    local. A date with no year is in the year the line was written, or the next when that would put it half a year back."""
    text = str(text or "")
    try: day0, today = dt.date.fromtimestamp(float(written)), dt.date.fromtimestamp(float(at or now()))
    except (TypeError, ValueError, OverflowError, OSError): return []
    found = []
    for m in DATE_RX.finditer(text):
        g = m.groupdict()
        try:
            if g["y"]: d = dt.date(int(g["y"]), int(g["m1"]), int(g["d1"]))
            else:
                mon = int(g["m4"]) if g["m4"] else _MONTHS.index((g["mon2"] or g["mon3"])[:3].lower()) + 1
                day = int(g["d4"] or g["d3"] or (g["e2"] if g["e2"] and int(g["e2"]) > int(g["d2"]) else g["d2"]))
                d = dt.date(day0.year, mon, day)
                if (day0 - d).days > 180: d = d.replace(year=d.year + 1)
        except ValueError: continue
        found.append((m, d))
    out = []
    for i, (m, d) in enumerate(found):
        if i + 1 < len(found) and DATE_TO.match(text[m.end():found[i + 1][0].start()]): continue   # a span's start: its end decides
        if day0 < d < today: out.append((m.group(0).strip(), d))
    return out

def tag_history(recs, thread):
    """The foci a thread was tagged into, oldest first (None where it was taken out)."""
    return [None if r.get("off") else r.get("focus") for r in recs if r.get("kind") == "tag" and r.get("thread") == thread]

def picked_group(first):
    """The group the person picked when they started a chat (a group:ID tag the dash puts on their own web post), or None."""
    if str(first.get("from", "")).lower() != "person" or first.get("via"): return None   # the person's own web post, not a door or relay
    return next((t.split(":", 1)[1] for t in first.get("tags") or [] if str(t).startswith("group:")), None)

def group_for(st, recs, first):
    """The group (a focus with instructions or files) the thread of post `first` is in, as its focus dict, or None: the thread's focus as
    the board files it; only a thread never tagged falls back on its parent's focus or the group the person picked at its start, so a chat
    taken out of a group stays out (E37b)."""
    th = first.get("thread", first["id"])
    fid = focus_of(st, th)
    if not fid and not tag_history(recs, th):
        fid = picked_group(first) or (first.get("parent") and focus_of(st, first["parent"]))   # the person's own pick wins over the parent's
    f = next((x for x in st["foci"] if x["id"] == fid), None) if fid else None
    return f if f and f.get("brief") else None

def openers(threads, board=None):
    """{thread: its first post} for the given threads, read from the board (only lines naming one of them are parsed). A thread whose
    opener isn't on the board is left out."""
    want = {t for t in threads if t}; out = {}
    if not want: return out
    keys = [t.encode() for t in want]
    try:
        with open(board or BOARD, "rb") as fh:
            for ln in fh:
                if not any(k in ln for k in keys): continue
                try: p = json.loads(ln)
                except ValueError: continue
                if not isinstance(p, dict) or not p.get("id"): continue
                th = p.get("thread") or p["id"]
                if th in want and th not in out: out[th] = p
    except OSError: pass
    return out

def opener(first, board=None):
    """The first post of the thread of post `first` (the parent and picked-group fallbacks live there), else `first` itself."""
    th = first.get("thread") or first.get("id")
    if not th or th == first.get("id"): return first
    return openers([th], board).get(th, first)

def moved_after_last_run(recs, thread, starts=None):
    """True when the thread's latest tag record (a move in or out of a focus) is newer than every agent run begun in it, so the next run
    is the first since the move. `starts`: [(t, thread)] as run_starts gives, else read from the log."""
    tags = [r for r in recs if r.get("kind") == "tag" and r.get("thread") == thread]
    if not tags: return False
    t = ts(tags[-1].get("at"))
    starts = run_starts(t) if starts is None else starts
    return not any(th == thread and st >= t for st, th in starts)

def group_left(st, recs, first, once=False, starts=None):
    """The group a chat was last in and is in no longer (moved out, or to another focus), as its focus dict, or None (E37b: an agent that
    slept through the move is told the group's instructions no longer apply). Only a group that still has instructions or files counts.
    once: only when no run has begun in the thread since the move, so the agent is told on its next start or resume, not on every one."""
    th = first.get("thread", first["id"])
    if once and not moved_after_last_run(recs, th, starts): return None
    cur = group_for(st, recs, first)
    seq = [picked_group(first)] + tag_history(recs, th)
    for fid in reversed(seq):
        if fid and fid != (cur or {}).get("id"):
            f = next((x for x in st["foci"] if x["id"] == fid), None)
            if f and f.get("brief"): return f
    return None

def run_starts(since):
    """[(t, thread)] for every agent run the starter began after `since` (the start events in ~/log.jsonl)."""
    out = []
    try:
        with open(LOG, "rb") as fh:
            for ln in fh:
                if b'"event": "start"' not in ln: continue
                try: e = json.loads(ln)
                except ValueError: continue
                t = ts(e.get("at"))
                if t >= since: out.append((t, e.get("thread") or ""))
    except OSError: pass
    return out

def activity(st, posts, starts, at=None):
    """Each focus's share of the hive's work: runs the starter began and posts agents wrote, in its threads, over the last day and week.
    {"foci": {id: {"day": {"runs", "posts"}, "week": {...}}}, "misc": the same for threads in no focus, "total": the hive's,
     "threads": {thread: focus id or None}} for every thread that saw a run or a post this week, so the dash can lay its costs
    (one per run, from the session logs) on the same map. A run on a thread in no focus counts under Misc; every one counts in total."""
    at = now() if at is None else at
    zero = lambda: {w: {"runs": 0, "posts": 0} for w in WINDOWS}
    out = {"windows": dict(WINDOWS), "foci": {f["id"]: zero() for f in st["foci"]}, "misc": zero(), "total": zero(), "threads": {}}
    week = at - WINDOWS["week"]
    def place(thread, t, what):
        if thread not in out["threads"]: out["threads"][thread] = focus_of(st, thread, posts)
        fid = out["threads"][thread]
        for w, span in WINDOWS.items():
            if t >= at - span:
                for b in (out["foci"][fid] if fid else out["misc"], out["total"]): b[w][what] += 1
    for t, th in starts:
        if t >= week and th: place(th, t, "runs")
    for p in posts:
        t = ts(p.get("at"))
        if t >= week and p.get("from") not in (None, "person"): place(p["thread"], t, "posts")
    return out

def newest_result(threads, posts):
    """The newest agent post in these threads whose last line is a result (Done: ...), as (t, post) or (0, None)."""
    best, bt = None, 0.0
    for p in posts:
        if p.get("thread") not in threads or str(p.get("from", "")).lower() == "person": continue
        lines = [l.strip() for l in str(p.get("text", "")).splitlines() if l.strip()]
        if len(lines) > 1 and RESULT.match(lines[-1]):
            t = ts(p.get("at"))
            if t > bt: bt, best = t, p
    return bt, best

def core_module(name):
    """away, asks or foryou, from beside this file: `hive focus` runs isolated (python -I), with no script directory on the path."""
    import importlib
    here = str(Path(__file__).resolve().parent)
    if here not in sys.path: sys.path.insert(0, here)
    return importlib.import_module(name)

def person_waits(st, posts, at=None):
    """What waits on the person by the board's own evidence, and what stopped waiting, for lint to hold the summaries against:
    {"open": [...], "closed": [...]}, rows {thread, text, kind: line|ask|bar, at, post|ask} and, on a closed one, rule (away.py's,
    or old, or closed) and why in words.
    Open is what the person is told waits on them: For you lines open by away.py's rules (open_on_person), asks in state person,
    and status bars ending `Needs the person:` with no word from them since. Closed is every For you line that came off: by a
    rule, by age, or by an agent's `hive foryou close`. None when it can't be known: away.py is missing or fails, or the files in
    use aren't the live ones (a test's own board says nothing about the live asks)."""
    try: away, asks, foryou = core_module("away"), core_module("asks"), core_module("foryou")
    except Exception: return None
    if FILE.parent != away.STATE or BOARD != away.HOME / "board.jsonl": return None
    at = at or now()
    try:
        items, done, shut, dropped = asks.ledger(), away.status_done(), away.closed_for_yous(), []
        kept = away.for_yous(posts, items, away.needs_order()[1], 0, latest_only=True, dropped=dropped)
    except Exception: return None
    line = lambda r, **k: {"thread": r["thread"], "text": r["text"], "kind": "line", "at": r["at"], "post": str(r["post"]), **k}
    off = lambda r, rule: line(r, rule=rule, why=away.RULE_WHY.get(rule, rule))
    open_, closed = [], [off(r, r["rule"]) for r in dropped]
    for r in kept:
        rule = away.view_rule(r, done)
        if rule: closed.append(off(r, rule))
        elif r["at"] < at - away.FOR_YOU_DAYS * 86400: closed.append(line(r, rule="old", why=f"it is over {away.FOR_YOU_DAYS} days old, and a line that old is no longer listed"))
        else: open_.append(line(r))
    said = {}                                           # thread -> when the person last wrote in it
    for p in posts:
        pid = str(p.get("id"))
        if pid in shut:
            e = shut[pid]
            for l in foryou.lines(p.get("text")):
                if not foryou.NOTHING.match(l): closed.append({"thread": p.get("thread"), "text": l, "kind": "line", "at": ts(p.get("at")), "post": pid, "rule": "closed", "why": f"{e.get('by') or 'an agent'} closed it: {' '.join(str(e.get('why') or 'no reason given').split())[:160]}"})
        if str(p.get("from", "")).lower() == "person": said[p.get("thread")] = max(said.get(p.get("thread"), 0), ts(p.get("at")))
    for i, e in items.items():
        if e.get("status") == "person" and e.get("kind") != "direction":
            open_.append({"thread": e.get("thread") or e.get("post") or "asks", "text": str(e.get("ask") or ""), "kind": "ask", "at": away.stamp(e.get("when") or e.get("at")) or 0, "ask": i})
    bars = {}
    for e in away.lines(STATUS):
        if e.get("thread"): bars[e["thread"]] = e
    for th, e in bars.items():
        rows = [l for l in str(e.get("text") or "").splitlines() if l.strip()]
        last = BAR_NAME.sub("", rows[-1]).strip() if rows else ""
        m = BAR_NEEDS.match(last)
        try: t = float(e.get("at") or 0)
        except (TypeError, ValueError): t = 0.0
        if m and at - t < BAR_DAYS * 86400 and said.get(th, 0) <= t: open_.append({"thread": th, "text": last[m.end():], "kind": "bar", "at": t})
    return {"open": open_, "closed": closed}

_WEEKDAYS = "Monday Tuesday Wednesday Thursday Friday Saturday Sunday".split()
def day_meant(word, written):
    """The day a day word meant in a line written at `written`, in words ("Tuesday, 11-10"): the day it was written, the one after
    for "tomorrow", the one before for "yesterday" and "last night". Days are local. None when the time can't be read."""
    try: d = dt.date.fromtimestamp(float(written))
    except (TypeError, ValueError, OverflowError, OSError): return None
    w = " ".join(str(word or "").lower().split())
    d += dt.timedelta(days=1 if w == "tomorrow" else -1 if w in ("yesterday", "last night") else 0)
    return f"{_WEEKDAYS[d.weekday()]}, {d:%m-%d}"

def days(t, at=None):
    """How long ago `t` was, in words: "40 minutes", "5 hours", "3 days"."""
    h = ((at or now()) - float(t or 0)) / 3600
    n, unit = (h * 60, "minute") if h < 1 else (h, "hour") if h < 48 else (h / 24, "day")
    return f"{n:.0f} {unit}" + ("" if f"{n:.0f}" == "1" else "s")

def todo_body(c):
    """A to-do the agents put on the person ("For you: order the boards"): the words after the marker, or None for any other to-do."""
    m = FOR_YOU_TODO.match(c.get("text") or "")
    return c["text"][m.end():] if m else None

def todo_ids(body):
    """The posts and asks a to-do names by id ("see post 11160104f457", "answer k1234")."""
    return set(POST_ID.findall(body)) | set(re.findall(r"\bk\d{4}\b", body))

def todo_rows(body, opens, mine):
    """The open rows (For you lines, asks, status bars) that tell the person what the to-do `body` says: a row anywhere in nearly
    its words, or a row of the to-do's own focus (`mine`) in other words or named by its id. Rows as lint pairs them: (ask_key, row)."""
    k, ids = ask_key(body), todo_ids(body)
    out = [r for o, r in opens if same_key(k, o)]
    return out + [r for o, r in mine if not any(r is x for x in out) and (near_key(k, o) or r.get("post") in ids or r.get("ask") in ids)]

def waits_by_focus(st, posts, waits):
    """person_waits()'s open rows with their ask_key, all of them and by the focus each one's thread shows under: (opens, {focus: [...]})."""
    of, here = focus_finder(st, posts), {}
    opens = [(ask_key(r["text"]), r) for r in waits["open"]]
    for x in opens: here.setdefault(of(x[1]["thread"]), []).append(x)
    return opens, here

def tick_note(st, checks, posts=None, waits=None, at=None):
    """For an agent that ticked or confirmed to-dos the agents had put on the person: one line for each place that still tells the
    person the step waits on them (an open For you line, an ask that is theirs, a status bar ending Needs the person, a Needs you
    line of the card), with the command that takes it off. A to-do and its line are two records of one wait, and closing one left
    the other standing (2026-11-16: six to-dos were open whose line had been closed)."""
    todo = [(c, todo_body(c)) for c in checks if todo_body(c) is not None]
    if not todo: return []
    if posts is None:
        posts = board_posts()
        if waits is None: waits = person_waits(st, posts, at)
    opens, here = waits_by_focus(st, posts, waits) if waits else ([], {})
    cut = lambda t: " ".join(str(t).split())[:70]
    out, ticked = [], {c["id"] for c, _ in todo}
    for c, body in todo:
        k, ids = ask_key(body), todo_ids(body)
        f = next((g for g in st["foci"] if g["id"] == c["focus"]), None)
        # One of a pair (lint's "to-do twice"): the other to-do still puts the step on the person, so every place that says it stays.
        if any(d["id"] not in ticked and not d["done"] and todo_body(d) is not None and near_key(k, ask_key(todo_body(d)), 4, 0.5) for d in (f or {}).get("checks") or []): continue
        rows = todo_rows(body, opens, here.get(c["focus"], []))
        # A row in the to-do's words, or one it names, is the step for sure: with one of those, a row that only shares a few words is left out.
        for r in [r for r in rows if same_key(k, ask_key(r["text"])) or r.get("post") in ids or r.get("ask") in ids] or rows:
            if r["kind"] == "line": out.append(f"{c['id']} is ticked, and a For you line in #{r['thread']} still tells the person it waits on them: \"{cut(r['text'])}\". If that is the same step, close it too: hive foryou close {r['post']} --why \"...\"")
            elif r["kind"] == "ask": out.append(f"{c['id']} is ticked, and ask {r['ask']} still waits on the person: \"{cut(r['text'])}\". If that is the same step: hive asks done {r['ask']} --evidence POST, or hive asks reopen {r['ask']} --why \"...\" when it is back with the agents")
            else: out.append(f"{c['id']} is ticked, and the status bar of #{r['thread']} still ends \"Needs the person: {cut(r['text'])}\". If that is the same step, set the bar again with where the thread stands now: hive status {r['thread']}")
        for n in ((f or {}).get("summary") or {}).get("needs") or []:
            if near_key(k, ask_key(n)): out.append(f"{c['id']} is ticked, and the card of {c['focus']} still lists under Needs you: \"{cut(n)}\". If that is the same step, take the line off: hive focus show {c['focus']}, then hive focus set {c['focus']}")
    return out

def closed_note(ids, st=None, posts=None, waits=None, at=None):
    """For an agent that closed For you lines (hive foryou close): one line for each open to-do that put the same step on the person
    and that nothing they are told about says any more, so the two are closed in one go. Lint's own "to-do off the list", for the
    lines just closed."""
    ids = {str(i) for i in ids}
    st = st or state(posts=posts)
    return [f"to-do {i['check']} of {i['focus']} still puts this on the person: \"{i['in']}\". If the step is settled, tick it too: hive focus check done {i['check']}. If it only stopped being pressing, tick it and add it again as an offer (\"For you: if you want ...\")"
            for i in lint(st, posts, waits=waits, at=at) if i["kind"] == "to-do off the list" and i.get("twin") in ids]

def lint(st, posts=None, only=None, waits=None, at=None):
    """[{focus, kind, what, fix}] for everything that reads wrong on the person's pages. Never refuses: the caller decides.
    waits: person_waits()'s answer, for the checks that hold a card against what waits on the person. Worked out here when lint
    reads the board itself; a caller that brings its own posts brings this too, or goes without those checks."""
    out = []
    at = at or now()
    if posts is None:
        posts = board_posts()
        if waits is None: waits = person_waits(st, posts, at)
    if waits:
        # Each row with its ask_key, by the focus its thread shows under. Closed rows newest first, and one dropped as a repeat
        # last: the line it repeats is the one that says why the wait ended.
        (opens, here), of, off = waits_by_focus(st, posts, waits), focus_finder(st, posts), {}
        gone = sorted(((ask_key(r["text"]), r) for r in waits["closed"]), key=lambda x: (x[1].get("rule") == "repeat", -float(x[1].get("at") or 0)))
        for x in gone: off.setdefault(of(x[1]["thread"]), []).append(x)
    for f in st["foci"]:
        if only and f["id"] != only: continue
        s, lead = f.get("summary"), f.get("lead")
        todo = [(c, todo_body(c)) for c in f["checks"] if not c["done"] and todo_body(c) is not None]
        if waits:
            # One meaning for "needs you" (2026-11-16): the In short, the Needs you lines, the to-do list and the list of what waits on
            # the person are four places that say it, and the person reads whichever is in front of them.
            mine, was = here.get(f["id"], []), off.get(f["id"], [])
            if lead and lead["state"] == "needs you" and not ((s or {}).get("needs") or (s or {}).get("hidden") or todo or mine):
                out.append({"focus": f["id"], "kind": "needs nothing", "part": "lead", "what": "In short says Needs you, and nothing in the focus is listed as waiting on them", "in": lead["text"][:80],
                            "fix": f"if something still waits on them, put it under ## Needs you with its #thread (hive focus set {f['id']}) and write the For you line in that thread, so it is counted where they look; if nothing does, open the In short with the state it is in now"})
            # HA-9 (2026-11-17): a cold reader of the In short alone got what is theirs right on 8 of 19 cards; on 3 it said On track while the card listed asks
            if lead and lead["state"] != "needs you" and (s or {}).get("needs") and not re.search(r"\byours?\b|\bfor you\b|\bneeds you\b|\bwaits? on you\b|\bup to you\b", lead["text"], re.I):
                out.append({"focus": f["id"], "kind": "ask not in In short", "part": "lead", "what": "the card lists something under Needs you, and the In short says nothing is theirs", "in": lead["text"][:80],
                            "fix": f"end the In short with what is theirs, in a few words (\"Yours: ...\"), or open it with Needs you when that is the state: hive focus lead {f['id']} \"...\""})
            # HA-9i (2026-11-18): the one thing an In short names must be a Now line; a cold reader marked the rest wrong
            if lead and (s or {}).get("now"):
                try:
                    sys.path.insert(0, str(Path(__file__).resolve().parent)); import rollup
                    _card = "## Now\n" + "\n".join("- " + n for n in s["now"]) + "\n## Needs you\n" + "\n".join("- " + n for n in s.get("needs") or [])
                    _ok = rollup.on_now(lead["text"], _card) and not rollup.stray_figures(lead["text"], _card)
                except Exception: _ok = True
                if not _ok:
                    out.append({"focus": f["id"], "kind": "one thing not on card", "part": "lead", "what": "the In short names a figure or date that no Now line carries, or shares no words with any Now line", "in": lead["text"][:80],
                                "fix": f"name in the In short the thing a Now line says (a figure or date it states), or add the line: hive focus lead {f['id']} \"...\""})
            keys = [(c, body, ask_key(body)) for c, body in todo]
            for i, (c, body, k) in enumerate(keys):
                # The same step twice on the person's list (2026-11-16: one decision, added by two agents a day apart).
                first = next((d for d, _, o in keys[:i] if near_key(k, o, 4, 0.5)), None)
                if first: out.append({"focus": f["id"], "kind": "to-do twice", "part": "to-do", "what": f"to-do {c['id']} says the same as {first['id']}", "in": c["text"][:80], "fix": f"the person reads one step twice: keep the one that says more and tick the other (hive focus check done {c['id']}, or hive focus check done {first['id']})"})
                if NO_RUSH.search(body): continue
                # Said by something the person is told about: a line anywhere in nearly its words, a line, ask or bar of this focus
                # in other words or named by its id, or a Needs you line of the card.
                ids = todo_ids(body)
                if todo_rows(body, opens, mine) or any(near_key(k, ask_key(n)) for n in (s or {}).get("needs") or []): continue
                twin = (next((r for _, r in was if r.get("post") in ids), None) or next((r for o, r in was if near_key(k, o, 4, 0.5)), None)
                        or next((r for o, r in gone if same_key(k, o)), None))
                if twin and (twin.get("rule") in TWIN_GONE or any(str(twin.get("post")) in r["text"] for _, r in mine)): continue   # it never waited, or an open bar names it
                out.append({"focus": f["id"], "kind": "to-do off the list", "part": "to-do", "what": f"to-do {c['id']} is the person's, and nothing like it is on the list of what waits on them", "in": c["text"][:80],
                            "check": c["id"], "twin": str(twin["post"]) if twin and twin.get("post") else None,
                            "fix": (f"its For you line in #{twin['thread']} came off that list: {twin['why']}. " if twin else "no For you line, ask or status bar says it. ")
                                   + f"Settled or dropped: tick it (hive focus check done {c['id']}). Work stops until they do it: write the For you line in its thread, so they are told. "
                                     "No rush: tick it and add it again as an offer (\"For you: if you want …\"), which stays a to-do and nothing more"})
        # A date that has gone by, and a day word in an old line (2026-11-16): both were right on the day they were written.
        dated = ([("lead", lead["text"], lead.get("since") or lead.get("at"), None)] if lead else []) + [(part, b, a, None) for part in ("now", "needs", "lately") for b, a in zip((s or {}).get(part) or [], (s or {}).get(part + "_at") or [])] + [("to-do", c["text"], c.get("at"), c["id"]) for c in f["checks"] if not c["done"]]
        for part, b, wrote, cid in dated:
            if not wrote: continue
            redo = f"if it happened, tick it (hive focus check done {cid}); if it is still to do, tick it and add it again with the day it is now due" if cid else "say what happened that day, or give the new date"
            for said, d in (late_dates(b, wrote, at) if part != "lately" else []):
                out.append({"focus": f["id"], "kind": "past date", "part": part, "what": f"{said} has passed", "in": b[:80], "fix": f"it was still ahead when this was written {days(wrote, at)} ago: {redo}"})
            m = DAY_WORD.search(b)
            if m and at - float(wrote) > DAY_WORD_OLD:
                out.append({"focus": f["id"], "kind": "says today", "part": part, "what": f"\"{m.group(0)}\" in a line written {days(wrote, at)} ago", "in": b[:80], "fix": (f"write {day_meant(m.group(0), wrote)} in its place" if day_meant(m.group(0), wrote) else "name the day") + ", so it reads right on any day" + (f": tick it and add it again (hive focus check done {cid})" if cid else "")})
        if not s and f["threads"]: out.append({"focus": f["id"], "kind": "missing", "what": "no summary yet", "fix": f"hive focus set {f['id']} <<'X' with ## Now and ## Lately bullets"})
        if not s and not lead: continue
        s = s or {"now": [], "lately": [], "needs": []}
        for part in ("lead", "now", "lately", "needs"):
            for b in ([lead["text"]] if lead else []) if part == "lead" else s[part]:
                for name, rx, fix in JARGON:
                    m = rx.search(b)
                    if m: out.append({"focus": f["id"], "kind": name, "part": part, "what": m.group(0).strip(), "in": b[:80], "fix": fix})
                if part != "lead" and len(b) > MAX_BULLET: out.append({"focus": f["id"], "kind": "long bullet", "part": part, "what": b[:60] + "…", "fix": f"keep a bullet under {MAX_BULLET} characters: one thing, in plain words"})
        if f.get("summary") and not s["now"] and not s["lately"]: out.append({"focus": f["id"], "kind": "empty", "what": "no bullets", "fix": "give at least one ## Now bullet"})
        # In short: the one part a person who reads nothing else gets (2026-11-16). Missing, without its state, too long, or left
        # behind by the lines under it.
        lines_at = [float(a or 0) for a in (s.get("now_at") or []) + (s.get("lately_at") or [])]
        if not lead:
            if len(s["now"]) + len(s["lately"]) >= 3: out.append({"focus": f["id"], "kind": "no lead", "part": "lead", "what": "no In short", "fix": f"say in one or two sentences whether the focus is on track against its aim and the one thing that matters now, for someone who reads nothing else: hive focus lead {f['id']} \"On track: …\" (it opens with {VERDICTS})"})
        else:
            if not lead["state"]: out.append({"focus": f["id"], "kind": "no state", "part": "lead", "what": "In short opens with no state", "in": lead["text"][:80], "fix": f"open it with {VERDICTS} and a colon: the card shows that word first"})
            if len(lead["text"]) > MAX_LEAD: out.append({"focus": f["id"], "kind": "long lead", "part": "lead", "what": f"In short is {len(lead['text'])} characters", "fix": f"keep it under {MAX_LEAD}: the state and the one thing that matters; the rest is what the lines are for"})
            if lines_at and max(lines_at) - float(lead.get("at") or 0) > LEAD_OLD:
                out.append({"focus": f["id"], "kind": "old lead", "part": "lead", "what": f"In short was last written {(max(lines_at) - float(lead.get('at') or 0)) / 86400:.0f} days before the newest line", "fix": f"read it against the lines as they are now: rewrite it, or set the same text again to say it still holds (hive focus lead {f['id']} \"…\")"})
        if len(s["now"]) > MAX_NOW: out.append({"focus": f["id"], "kind": "too many", "part": "now", "what": f"{len(s['now'])} Now lines", "fix": f"keep the {MAX_NOW} that matter at this focus's level; merge the rest, move what is settled to Lately, or leave it to its thread's status"})
        for i in s.get("hidden") or []:
            b = s["now"][i]
            out.append({"focus": f["id"], "kind": "hidden ask", "part": "now", "what": reads_as_ask(b) or "", "in": b[:80], "fix": "if only the person can do it and it is still open, move the line under ## Needs you with its #thread, so the page shows it as waiting on them; if it isn't theirs, say who does it"})
        if not f.get("summary"): continue
        t, p = newest_result(set(f["threads"]), posts)
        if p and t > float(s.get("at") or 0) + 60:
            out.append({"focus": f["id"], "kind": "stale", "what": f"a result was posted in #{p['thread']} by {p.get('from')} after the summary", "fix": f"the agent that posts a result updates the summary in the same run: hive focus set {f['id']}"})
    return out

def asked_only(thread):
    """An "Ask the Hive" conversation answers a question from the notes: it can be filed under a focus, but no work moved, so its answer owes the card nothing (ad775, 2026-11-09; R34)."""
    return str(thread).startswith("kb-ask-")

SUMMARY_SLACK_S = 600   # tidy.SUMMARY_SLACK_S: without it a run that set its card and then ended with `hive post --end` was held to set it again (ad388, 11-22)

def nudge(name, thread, since, posts=None, own=None, st=None):
    """For the inbox hook: one line when `name` posted a result in `thread` since `since` and the thread's focus has an older summary (or none). None otherwise.
    `own` is the run's own thread: any other thread is named in the line, since "this thread" would point the agent at the wrong card."""
    st = state() if st is None else st; posts = board_posts() if posts is None else posts
    fid = focus_of(st, thread, posts)
    if not fid or asked_only(thread): return None
    f = find(st, fid)
    mine = [p for p in posts if p.get("thread") == thread and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since]
    t, p = newest_result({thread}, mine)
    if not p: return None
    s = f.get("summary")
    if s and float(s.get("at") or 0) >= t - SUMMARY_SLACK_S: return None   # the tidy's rule: a card set just before the result (the Stop order, then `hive post --end`) is current
    lead = f.get("lead")
    short = (f"Then read In short against the lines as you left them (now: \"{lead['text'][:200]}\") and rewrite it if it is no longer the verdict: hive focus lead {f['id']} \"On track: …\"" if lead else
             f"Then give it its In short, which the person reads first and often alone, in one or two sentences (is the focus on track against its aim, and the one thing that matters now): hive focus lead {f['id']} \"On track: …\" (it opens with {VERDICTS})")
    return (f"{'This thread' if own in (None, thread) else f'#{thread}, where you posted a result,'} is in the focus \"{f['name']}\" and its summary is {'older than your result' if s else 'not written yet'}. The person reads the focus card, "
            f"not the thread, so update it now, in plain words: what is being done and what changed, each line opening with its topic and a colon, with @agent and #thread where they help. "
            f"Run hive focus show {f['id']} first and keep the others' lines; anything only the person can do goes under ## Needs you, never inside a Now line:\n"
            f"hive focus set {f['id']} <<'X'\n## Now\n- Topic: …\n## Lately\n- …\nX\n{short}.\n"
            f"If you read the card and your result changes nothing it says, don't rewrite it: hive focus set {f['id']} --still \"why\".\n" + reader(f, posts))

FILE_ASKED = STATE / "file-asked.jsonl"     # {thread, name, at, file, title}: which agent was asked to file or title which conversation
META, RENAMED = STATE / "thread-meta.json", STATE / "renamed.jsonl"
HEXID = re.compile(r"[0-9a-f]{12}")

def _titled(thread):
    """True when the thread has a real title: a name that is not a board id, the person's own title (thread-meta) or an `hive rename`."""
    if not HEXID.fullmatch(thread): return True
    try:
        m = json.loads(META.read_text()).get(thread) or {}
        if m.get("title"): return True
    except (OSError, ValueError, AttributeError): pass
    try: return any(json.loads(l).get("thread") == thread for l in RENAMED.read_text().splitlines() if l.strip())
    except (OSError, ValueError): return False

def threads_posted(name, thread, since, posts):
    """The run's own thread first, then any other thread `name` posted in since `since` (a keeper run posting in someone else's thread)."""
    out = [thread]
    for p in posts:
        th = p.get("thread")
        if th and th not in out and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since: out.append(th)
    return out[:20]

def summary_nudges(name, thread, since, posts, st=None):
    """The summary reminders for a run: one per focus, over every thread it posted a result in, as the file ask does (rsi-lab-pickups: a keeper or a lab run posting its result in another focus's thread left that card stale)."""
    st, out, seen = (state() if st is None else st), [], set()
    for th in threads_posted(name, thread, since, posts):
        fid = focus_of(st, th, posts)
        if fid in seen: continue
        m = nudge(name, th, since, posts, own=thread, st=st)
        if m: seen.add(fid); out.append(m)
    return out

RUNS = HOME / "run"

def run_live(r):
    """A ~/run/NAME.json record is still going: its pid lives and, when the record has it, started when the run did (the starter's
    `proc`, /proc/PID/stat field 22), so a pid reused after a reboot is not taken for it."""
    try:
        st_ = Path(f"/proc/{int(r['pid'])}/stat").read_text().rsplit(")", 1)[1].split()
        return not r.get("proc") or st_[19] == str(r["proc"])
    except (OSError, ValueError, KeyError, TypeError, IndexError, AttributeError): return False

def _still_in_asked_run(name, at):
    """True when `name` is still in the run it was asked in (its run file started before the ask and the run lives): it may yet act."""
    try:
        r = json.loads((RUNS / f"{name}.json").read_text())
        return ts(r.get("at")) <= at and run_live(r)
    except (OSError, ValueError, KeyError, TypeError, AttributeError): return False

def _new_run_since(name, at):
    """True when `name` is in a live run that started after `at`: an ask from its earlier run did not reach this one."""
    try:
        r = json.loads((RUNS / f"{name}.json").read_text())
        return ts(r.get("at")) > at and run_live(r)
    except (OSError, ValueError, KeyError, TypeError, AttributeError): return False

def file_nudge(name, thread, since, posts=None, st=None):
    """For the inbox hook: one line, once per conversation ever, when `name` posted in `thread` since `since` and the thread is in no
    focus (nobody ever tagged it, so the person has not put it in Misc on purpose) or still has its board id for a title. The agent
    that worked in it files it and names it at its own hand-off: no hourly tidy pass (the person, 2026-11-08, post 1108153548ec,
    time invariance). Asks are recorded in FILE_ASKED so the audit pass can see who was asked and did not. None otherwise."""
    st = state() if st is None else st; posts = board_posts() if posts is None else posts
    if not any(p.get("thread") == thread and str(p.get("from", "")).lower() == name and ts(p.get("at")) >= since for p in posts): return None
    try:
        # asked within the hour, this agent or one still in the run it was asked in (it may yet act). One whose run is over did not
        # act, so the next agent to stop is asked: 11-23, a79ca was asked at 19:20 for its helpers' threads, stopped at 19:35, and
        # the helpers stopping at 19:51 and 19:59 were never asked, being inside its hour (thread improve, 7 pickups in a day)
        # the same agent is asked again when it is in a new run since the ask (11-23: a79ca, asked 19:20, ended without filing, woken
        # again at 19:50 to post in the same threads, was not asked and the 20:19 pass found two untitled; rsi-lab keeper a6a48)
        if any((r := json.loads(l)).get("thread") == thread and now() - r.get("at", 0) < 3600
               and ((r.get("name") == name and not _new_run_since(name, r.get("at", 0))) or _still_in_asked_run(r.get("name"), r.get("at", 0)))
               for l in FILE_ASKED.read_text().splitlines() if l.strip()): return None
    except (OSError, ValueError): pass
    tagged = False
    try: tagged = any(r.get("kind") == "tag" and r.get("thread") == thread for r in map(json.loads, filter(str.strip, FILE.read_text().splitlines())))
    except (OSError, ValueError): pass
    unfiled = not tagged and focus_of(st, thread, posts) is None
    untitled = not _titled(thread)
    if not (unfiled or untitled): return None
    try:
        STATE.mkdir(parents=True, exist_ok=True)
        with FILE_ASKED.open("a") as f: f.write(json.dumps({"thread": thread, "name": name, "at": now(), "file": unfiled, "title": untitled}) + "\n")
    except OSError: return None
    out = []
    if unfiled:
        names = ", ".join(f"{f['id']} ({f['name']})" for f in st["foci"] if not f.get("off"))[:900]
        out.append(f"This conversation is in no focus, so the person won't find it on a focus page. File it in the focus it serves: "
                   f"hive focus tag {thread} FOCUS. The foci are: {names}. If none fits and the work will last, make one: hive focus new ID \"Name\" --thread {thread}. If it is plumbing the person needn't see, say so: hive focus tag {thread} --off.")
    if untitled:
        out.append(f"The conversation still shows its first words as a title. Give it a short title in plain words that says what it is about: "
                   f"hive rename {thread} \"…\" (the person's own title always wins).")
    return " ".join(out)

def fmt_lint(items, each=0):
    """The flags as lines. each: at most that many of one kind in one focus, then a line counting the rest (`set` prints the
    flags after every summary, and twelve passed dates would bury the one flag about the lines just written)."""
    out, seen, more = [], {}, {}
    for i in items:
        k = (i["focus"], i["kind"]); seen[k] = seen.get(k, 0) + 1
        if each and seen[k] > each: more[i["focus"]] = more.get(i["focus"], 0) + 1; continue
        out.append(f"{i['focus']}: {i['kind']}: {i['what']}" + (f"  (in: {i['in']})" if i.get("in") else "") + f"\n    → {i['fix']}")
    out += [f"{fid}: {n} more of the same kinds: hive focus lint {fid}" for fid, n in more.items()]
    return "\n".join(out) or "nothing flagged"

def check_id(recs): return f"c{sum(1 for r in recs if r['kind'] == 'check') + 1:04d}"

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive focus", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("list"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("new"); p.add_argument("id"); p.add_argument("name", nargs="?"); p.add_argument("--thread"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("tag"); p.add_argument("thread"); p.add_argument("focus", nargs="?"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("link"); p.add_argument("a"); p.add_argument("b"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("relate"); p.add_argument("focus"); p.add_argument("what", choices=RELATE); p.add_argument("target"); p.add_argument("--why", default=""); p.add_argument("--off", action="store_true")
    p = sp.add_parser("set"); p.add_argument("focus"); p.add_argument("--still", metavar="WHY", help="the card is still right after your result: stamp it as checked now without rewriting it, and say why (a keeper round that changed nothing on it)")
    p = sp.add_parser("show"); p.add_argument("focus")
    p = sp.add_parser("reader"); p.add_argument("focus")
    p = sp.add_parser("lead", help="In short: the one or two sentences the person reads first"); p.add_argument("focus"); p.add_argument("text", nargs="*"); p.add_argument("--off", action="store_true")
    p = sp.add_parser("seen", help="the person looked at the focus's page, or at the home (the dashboard writes it)"); p.add_argument("focus", nargs="?"); p.add_argument("--home", action="store_true"); p.add_argument("--json", action="store_true", help="print {noted, key, seen: {at, start, base, since}}")
    p = sp.add_parser("keeper"); p.add_argument("focus")
    p = sp.add_parser("aim"); p.add_argument("focus"); p.add_argument("text", nargs="*")
    p = sp.add_parser("check"); p.add_argument("what", choices=["add", "list", "done", "untick", "confirm", "ticked"]); p.add_argument("args", nargs="*"); p.add_argument("--why", default="")
    p = sp.add_parser("later", help="put off a needs-you item: later (moved to the focus page's Later), moot (not needed), back (shown again)")
    p.add_argument("what", choices=["later", "moot", "back", "list"]); p.add_argument("key", nargs="?"); p.add_argument("--th", default=""); p.add_argument("--focus", default="")
    p.add_argument("--text", default=""); p.add_argument("--t", type=float, default=0)
    p = sp.add_parser("lint"); p.add_argument("focus", nargs="?"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("file"); p.add_argument("path"); p.add_argument("--post"); p.add_argument("--focus"); p.add_argument("--off", action="store_true")   # a kb note filed under a focus (kb file --from/--focus calls this)
    p = sp.add_parser("brief", help="the group's own instructions and files, given to every agent started in it")
    p.add_argument("focus"); p.add_argument("--set", action="store_true", help="the instructions come on stdin (empty: none)")
    p.add_argument("--file", action="append", default=[], help="copy this file into the group"); p.add_argument("--drop", action="append", default=[], help="take this file (its name) off")
    p.add_argument("--off", action="store_true", help="clear the instructions and the files"); p.add_argument("--json", action="store_true")
    p = sp.add_parser("order"); p.add_argument("ids", nargs="*"); p.add_argument("--reset", action="store_true")
    p = sp.add_parser("moved", help="threads moved between foci lately, newest first"); p.add_argument("--since", default="2d", help="how far back: 30m, 12h, 3d (up to a week)")
    sp.add_parser("json")
    argv = sys.argv[1:] if argv is None else list(argv)
    if not argv: argv = ["list"]   # bare `hive focus` lists them, as orientation's "(`hive focus`)" reads; 46 agent calls a day hit "arguments are required: cmd" (af3f2, 2026-11-20)
    a = ap.parse_args(argv)
    st = state()
    if a.cmd == "list":
        if a.json: print(json.dumps(st["foci"], default=str)); return
        for f in st["foci"]:
            s = f.get("summary"); age = f"summary {int((now() - float(s['at'])) / 3600)} h old" if s else "no summary"
            print(f"{f['id']:<16} {f['name'][:50]:<50} {len(f['threads'])} threads, {age}" + (f", linked: {', '.join(f['links'])}" if f["links"] else ""))
        return
    if a.cmd == "new":
        fid = a.id.strip().lower()
        if not ID.fullmatch(fid) or fid == MISC: sys.exit("hive focus new: the id is lowercase letters, digits and dashes (misc is taken)")
        f = find(st, fid)
        if a.off:
            if not f: sys.exit(f"hive focus new: no focus {fid}")
            if f.get("own"): sys.exit(f"hive focus new: {fid} is one of the person's priorities; it goes when kb/priorities.md changes")
            append({"kind": "focus", "id": fid, "off": True}); print(f"retired focus {fid}"); return
        if not a.name: sys.exit('hive focus new: give the name, hive focus new ID "name"')
        append({"kind": "focus", "id": fid, "name": " ".join(a.name.split())[:80], "thread": (a.thread or "").lstrip("#") or None})
        print(("renamed" if f else "new") + f" focus {fid}: {a.name}"); return
    if a.cmd == "tag":
        th = a.thread.lstrip("#")
        if not THREAD.fullmatch(th): sys.exit("hive focus tag: give a thread name")
        if a.off: append({"kind": "tag", "thread": th, "off": True}); print(f"{th} is out of its focus"); return
        if not (a.focus or "").strip():   # no focus named: say where it is and write nothing (an empty key once filed threads at random)
            cur = focus_of(st, th); f = find(st, cur) if cur else None
            print(f"{th} is in {f['id']} ({f['name']})" if f else f"{th} is in no focus (hive focus tag {th} FOCUS files it)"); return
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus tag: no focus {a.focus!r} (hive focus list)")
        append({"kind": "tag", "thread": th, "focus": f["id"]}); print(f"{th} is in {f['id']} ({f['name']})"); return
    if a.cmd == "link":
        fa, fb = find(st, a.a), find(st, a.b)
        if not fa or not fb or fa is fb: sys.exit("hive focus link: give two different foci (hive focus list)")
        append({"kind": "link", "a": fa["id"], "b": fb["id"], **({"off": True} if a.off else {})}); print(("unlinked" if a.off else "linked") + f" {fa['id']} and {fb['id']}"); return
    if a.cmd == "relate":
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus relate: no focus {a.focus!r} (hive focus list)")
        x = a.target.strip().lstrip("@#" if a.what != "file" else "@")
        if a.what == "agent": x = x.lower()
        if not x or (a.what != "file" and not THREAD.fullmatch(x)): sys.exit(f"hive focus relate: give the {a.what}'s name")
        why = " ".join(a.why.split())[:200]
        if not a.off and not why: sys.exit('hive focus relate: say why with --why "..." (one line: what it has to do with the focus)')
        append({"kind": "relation", "focus": f["id"], "what": a.what, "target": x, **({"off": True} if a.off else {"why": why})})
        print(("unrelated" if a.off else "related") + f" {a.what} {x} {'from' if a.off else 'to'} {f['id']}"); return
    if a.cmd == "aim":
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus aim: no focus {a.focus!r} (hive focus list)")
        text = " ".join(" ".join(a.text).split())[:200]
        if not text: print(f.get("aim") or "(no aim yet)"); return
        append({"kind": "aim", "focus": f["id"], "text": text}); print(f"aim of {f['id']}: {text}"); return
    if a.cmd == "seen":
        if who() != "person": sys.exit("hive focus seen: the dashboard records this when the person looks at the home or a focus's page; hive focus reader says when that was")
        if bool(a.home) == bool(a.focus): sys.exit("hive focus seen FOCUS, or hive focus seen --home")
        f = None if a.home else find(st, a.focus.strip().lower())
        if not a.home and not f: sys.exit(f"hive focus seen: no focus {a.focus!r}")
        fid, name = (f["id"], f["id"]) if f else (HOME_ID, "the home")
        cur = st["home_seen"] if not f else f.get("seen")
        noted = now() - (cur or {}).get("at", 0) >= SEEN_EVERY
        if noted: cur = look(cur, append({"kind": "seen", "focus": fid})["at"])
        print(json.dumps({"noted": noted, "key": fid, "seen": seen_out(cur)}) if a.json else f"{name}: seen" if noted else f"{name}: already noted"); return
    if a.cmd in ("set", "show", "keeper", "reader", "lead"):
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus: no focus {a.focus!r} (hive focus list; hive focus new ID \"name\" adds one)")
        cur = f.get("lead")
        if a.cmd == "lead":
            if a.off:
                if cur: append({"kind": "lead", "focus": f["id"], "off": True})
                print(f"{f['id']}: no In short now"); return
            raw = " ".join(a.text).strip()
            if not raw and not sys.stdin.isatty():   # a heredoc is there at once; an agent's idle pipe never is, and must not hang the command
                import select
                try:
                    if select.select([sys.stdin], [], [], 0.3)[0]: raw = sys.stdin.read().strip()
                except (OSError, ValueError): pass
            if not raw:
                print(f"{cur['text']}\n(written {days(cur['at'])} ago by {cur.get('from')}" + ("" if cur["at"] == cur["since"] else f"; it has said this for {days(cur['since'])}") + ")" if cur else
                      f"(no In short yet: hive focus lead {f['id']} \"On track: …\", one or two sentences opening with {VERDICTS})"); return
            text = split_lead(raw)[0] or " ".join(raw.split())
            if len(text) > LEAD_CUT:
                print(f"hive focus lead: {len(text)} characters, In short keeps {LEAD_CUT}: the end was CUT OFF. It is one or two sentences; the rest belongs in the lines.", file=sys.stderr); text = text[:LEAD_CUT]
            try: sys.path.insert(0, str(Path(__file__).resolve().parent)); import rollup; _card = rollup.card_text(f)
            except Exception: _card = (f.get("summary") or {}).get("text") or ""
            text = aligned(text, _card)
            same = bool(cur) and norm(cur["text"]) == norm(text)
            append({"kind": "lead", "focus": f["id"], "text": text})
            st = state(); f = find(st, f["id"])
            flags = [i for i in lint(st, only=f["id"]) if i.get("part") == "lead"]
            print((f"In short on {f['id']} still holds (it has said this for {days(f['lead']['since'])})" if same else f"In short set on {f['id']}" + (f", shown as \"{f['lead']['state']}\"" if f["lead"]["state"] else ""))
                  + (f"\nreads wrong on the person's page, fix when you can:\n{fmt_lint(flags)}" if flags else ""))
            return
        if a.cmd == "show":
            s = f.get("summary")
            print("\n".join(x for x in (f"## In short\n{cur['text']}\n" if cur else "", s["text"] if s else "" if cur else "(no summary yet)") if x)); return
        if a.cmd == "keeper": print(keeper(f, board_posts()) or "nobody"); return
        if a.cmd == "reader": print(reader(f, board_posts())); return
        if getattr(a, "still", None) is not None:   # rsi-lab pickups: a truthful "the card already says this" was still a tidy miss (a1f59 on 11-22)
            why = " ".join(a.still.split())[:200]
            s = f.get("summary")
            if not s: sys.exit(f"hive focus set --still: {f['id']} has no summary yet; write one (hive focus set {f['id']} <<'X' ... X)")
            if len(why) < 8: sys.exit("hive focus set --still \"WHY\": say in a few words why your result changes nothing the card says")
            append({"kind": "summary", "focus": f["id"], "text": s["text"], "still": why})
            print(f"summary on {f['id']} checked and left as it was (written {days(s['at'])} ago by {s.get('from')}); if your result did change what is being done, set the lines instead")
            return
        if sys.stdin.isatty(): sys.exit("hive focus set: the summary comes on stdin, a quoted heredoc with ## Now and ## Lately bullets")
        lead, raw = split_lead(sys.stdin.read()); text = raw[:MAX_SUMMARY]
        if len(raw) > MAX_SUMMARY: print(f"hive focus set: {len(raw)} characters, the card keeps {MAX_SUMMARY}: the end was CUT OFF. Shorten it (fewer bullets, higher level) and set it again.", file=sys.stderr)
        parts = parse_summary(text)
        if not parts["now"] and not parts["lately"]: sys.exit("hive focus set: empty; give ## Now and ## Lately bullets" + (f" (In short alone: hive focus lead {f['id']})" if lead else ""))
        append({"kind": "summary", "focus": f["id"], "text": text})
        if lead: lead = aligned(lead, text)
        new_lead = bool(lead) and not (cur and norm(cur["text"]) == norm(lead))
        if new_lead:
            if len(lead) > LEAD_CUT: print(f"hive focus set: In short is {len(lead)} characters and keeps {LEAD_CUT}: the end was CUT OFF.", file=sys.stderr)
            append({"kind": "lead", "focus": f["id"], "text": lead[:LEAD_CUT]})
        st = state(); flags = [i for i in lint(st, only=f["id"]) if i["kind"] != "stale"]
        f = find(st, f["id"])   # as set: the lines' first-seen times feed the reader line
        still = "" if new_lead or not f.get("lead") else (f"\nIn short still reads, from {days(f['lead']['at'])} ago: \"{f['lead']['text']}\"\nIf the lines you just set change that, rewrite it (hive focus lead {f['id']} \"On track: …\"); if it holds, set the same text again to say so.")
        print(f"summary set on {f['id']} ({'In short, ' if new_lead else ''}{len(parts['now'])} now, {len(parts['lately'])} lately" + (f", {len(parts['needs'])} for the person" if parts["needs"] else "") + ")" + still + "\n" + reader(f, board_posts()) + (f"\nreads wrong on the person's page, fix when you can:\n{fmt_lint(flags, 3)}" if flags else ""))
        return
    if a.cmd == "check":
        recs = records()
        if a.what == "add":
            if len(a.args) < 2: sys.exit('hive focus check add FOCUS "text"')
            f = find(st, a.args[0])
            if not f: sys.exit(f"hive focus check: no focus {a.args[0]!r}")
            r = append({"kind": "check", "focus": f["id"], "id": check_id(recs), "text": " ".join(" ".join(a.args[1:]).split())[:200]}); print(f"{r['id']}: {r['text']}")
            if (m := STEP_ID.search(r["text"])): print(f"hive focus check: {m.group(0)!r} is a ladder step id the person can't use; say what it does in plain words (done {r['id']}, then add it again)", file=sys.stderr)
            return
        if a.what in ("list", "ticked"):   # ticked: the person's ticks no agent has checked yet
            f = find(st, a.args[0]) if a.args else None
            for g in st["foci"]:
                if f and g is not f: continue
                for c in g["checks"]:
                    if a.what == "ticked" and not (c["done"] and not c.get("ok")): continue
                    print(f"[{'x' if c.get('ok') else '?' if c['done'] else ' '}] {c['id']} {g['id']}: {c['text']}" + (" (ticked by the person: check it, then confirm or untick --why)" if c["done"] and not c.get("ok") else ""))
            return
        known = {c["id"]: c for g in st["foci"] for c in g["checks"]}
        for cid in a.args:
            if cid not in known: sys.exit(f"hive focus check: no item {cid}")
            if a.what == "confirm" and not known[cid]["done"]: sys.exit(f"hive focus check: {cid} isn't ticked; use done")
            kind = {"done": "check_done", "untick": "check_untick", "confirm": "check_ok"}[a.what]
            append({"kind": kind, "id": cid, **({"why": " ".join(a.why.split())[:200]} if a.what == "untick" and a.why else {})})
        print(f"{a.what}: {' '.join(a.args)}", flush=True)
        if a.what != "untick" and who() != "person":   # a hint for the agent: never costs the tick that was just written
            try:
                for ln in tick_note(st, [known[cid] for cid in a.args]): print(f"hive focus check: {ln}", file=sys.stderr)
            except Exception: pass
        return
    if a.cmd == "lint":
        f = find(st, a.focus) if a.focus else None
        if a.focus and not f: sys.exit(f"hive focus lint: no focus {a.focus!r}")
        items = lint(st, only=f["id"] if f else None)
        print(json.dumps(items) if a.json else fmt_lint(items)); return
    if a.cmd == "file":
        posts = board_posts(); fid = None
        if a.focus:
            f = find(st, a.focus)
            if not f: sys.exit(f"no focus {a.focus}")
            fid = f["id"]
        elif a.post:
            src = next((p for p in posts if p.get("id") == a.post), None)
            if not src: sys.exit(f"no post {a.post} on the board")
            fid = focus_of(st, src.get("thread"), posts)
        if not fid: print("no focus for it (the post's thread is in Misc; give --focus ID)"); return
        append({"kind": "file", "focus": fid, "path": a.path.strip(), **({"post": a.post} if a.post else {}), **({"off": True} if a.off else {})})
        print(("taken off" if a.off else "listed on") + f" the focus page of {fid}: {a.path.strip()}"); return
    if a.cmd == "brief":
        f = find(st, a.focus)
        if not f: sys.exit(f"hive focus brief: no focus {a.focus!r} (hive focus list)")
        b = f.get("brief") or {"text": "", "files": []}
        if (a.set or a.file or a.drop or a.off) and who() != "person":
            sys.exit("hive focus brief: a group's instructions and files are the person's to set, from its focus page")
        if not (a.set or a.file or a.drop or a.off):
            if a.json: print(json.dumps(b)); return
            print(b["text"] or "(no instructions)")
            for x in b["files"]: print(f"file: {x}")
            return
        text, files = ("", []) if a.off else (b["text"], list(b["files"]))
        gone = list(b["files"]) if a.off else []
        if a.off and (GROUP_FILES / f["id"]).is_dir() and not (GROUP_FILES / f["id"]).is_symlink():   # off empties the group's folder, strays included
            gone += [str(x) for x in (GROUP_FILES / f["id"]).iterdir() if x.is_file() or x.is_symlink()]
        if a.set and not a.off:
            if sys.stdin.isatty(): sys.exit("hive focus brief --set: the instructions come on stdin, a quoted heredoc")
            text = sys.stdin.read().strip()
            if len(text) > BRIEF_MAX: sys.exit(f"hive focus brief: {len(text)} characters; a group's instructions take {BRIEF_MAX} at most")
        for d in a.drop:
            keep = [x for x in files if Path(x).name != Path(d).name]
            if len(keep) == len(files): sys.exit(f"hive focus brief: no file {d!r} in {f['id']}")
            gone += [x for x in files if x not in keep]; files = keep
        add = []
        for src in a.file:
            sp_ = Path(src).expanduser()
            if not sp_.is_file() or sp_.is_symlink(): sys.exit(f"hive focus brief: {src} isn't a file")
            if sp_.stat().st_size > BRIEF_FILE_MAX: sys.exit(f"hive focus brief: {sp_.name} is over {BRIEF_FILE_MAX // 1_000_000} MB")
            add.append(sp_)
        d = GROUP_FILES / f["id"]
        named = {}
        for sp_ in add:
            name = re.sub(r"[^A-Za-z0-9_. -]+", "_", sp_.name).strip(" .") or "file"
            if name in named: sys.exit(f"hive focus brief: {named[name].name} and {sp_.name} would both be saved as {name}; rename one")
            named[name] = sp_
        if len({*files, *(str(d / n) for n in named)}) > BRIEF_FILES: sys.exit(f"hive focus brief: a group takes {BRIEF_FILES} files at most")
        if named:
            import shutil, tempfile
            if d.is_symlink() or GROUP_FILES.is_symlink(): sys.exit(f"hive focus brief: {d} is a link; not writing through it")
            d.mkdir(parents=True, exist_ok=True)
            for name, sp_ in named.items():
                dst = d / name
                files = [x for x in files if x != str(dst)]   # the same name again replaces it
                fd, tmp = tempfile.mkstemp(dir=d, prefix=".up-"); os.close(fd)
                shutil.copyfile(sp_, tmp); os.replace(tmp, dst); files.append(str(dst))   # replace, never write through a link planted at dst
        for x in gone:
            if x not in files and Path(x).parent == d:
                try: Path(x).unlink()
                except OSError: pass
        append({"kind": "brief", "focus": f["id"], "text": text, "files": files})
        print(f"{f['id']}: " + (f"{len(text)} characters of instructions" if text else "no instructions") + f", {len(files)} file" + ("" if len(files) == 1 else "s")); return
    if a.cmd == "order":
        if a.reset: append({"kind": "order", "ids": []}); print("order back to the default"); return
        if not a.ids: print(" ".join(f["id"] for f in st["foci"])); return
        ids = []
        for k in a.ids:
            f = find(st, k)
            if not f: sys.exit(f"hive focus order: no focus {k!r} (hive focus list)")
            if f["id"] not in ids: ids.append(f["id"])
        append({"kind": "order", "ids": ids})
        print("order: " + " ".join(f["id"] for f in state()["foci"])); return
    if a.cmd == "later":
        if a.what == "list":
            for x in st["later"]: print(f"{x['what']:5} {x.get('focus') or '-'} #{x.get('th') or '-'}: {x.get('text') or ''}  [{x['key']}]")
            return
        if not a.key or len(a.key) > 200: sys.exit("hive focus later later|moot|back KEY [--th T --focus F --text X --t SECONDS]")
        if a.what == "back" and a.key not in {x["key"] for x in st["later"]}: sys.exit(f"hive focus later: {a.key!r} isn't put off")
        f = find(st, a.focus) if a.focus else None
        append({"kind": "later", "key": a.key, "what": a.what, **({} if a.what == "back" else {"th": a.th[:80] or None, "focus": f["id"] if f else None, "text": " ".join(a.text.split())[:300], "t": a.t or None})})
        print(f"{a.what}: {a.key}"); return
    if a.cmd == "moved":
        mt = re.fullmatch(r"(\d+(?:\.\d+)?)([mhd])", a.since.strip())
        if not mt: sys.exit("hive focus moved: --since like 30m, 12h or 3d")
        cut = now() - float(mt[1]) * {"m": 60, "h": 3600, "d": 86400}[mt[2]]
        rows = [m for m in st["moved"] if m["at"] >= cut]
        if not rows: print("no thread moved between foci in that time"); return
        for m in rows:
            print(f"{dt.datetime.fromtimestamp(m['at']).strftime('%m-%d %H:%M')}  {m['thread']:<14} {m['was'] or '(untagged)'} -> {m['to'] or MISC}  by {m['by']}")
        return
    if a.cmd == "json":
        posts = board_posts()
        for f in st["foci"]: f["keeper"] = keeper(f, posts)
        home = seen_out(st["home_seen"])
        for f in st["foci"]: f["seen"] = seen_out(f.get("seen"))
        out = {"foci": st["foci"], "tags": st["tags"], "later": st["later"], "moved": st["moved"], "misc": MISC, "home_seen": home, "seen_visit": SEEN_VISIT, "lint": lint(st, posts, waits=person_waits(st, posts)), "activity": activity(st, posts, run_starts(now() - WINDOWS["week"])), "at": now()}
        print(json.dumps(out, default=str)); return

if __name__ == "__main__": main()
