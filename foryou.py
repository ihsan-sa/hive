#!/usr/bin/env python3
"""The For you line: what marks it, and what makes it read as more than a plain ask. `hive post` warns on these and `hive progress` counts them; nothing here refuses anything."""
import re

FOR_YOU = re.compile(r"^[\s>*_#-]*for you[\s*_]*:", re.I | re.M)  # the line that puts a post on the person's card
NOTHING = re.compile(r"^\W*(nothing (is )?needed\b|no action\b|nothing to do\b|nothing\s*(?:[;:,.]|$))", re.I)  # away.py uses this one; needs_you.py copies it
OFFER = re.compile(r"^\W*(?:if you\b|only if\b|optional(?:ly)?\b|(?:say|tell me|let me know) if you\b)", re.I)  # an offer, not a wait: nothing stops until they answer
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")  # as away.py
SELF_THREADS = {"starter", "keeper", "improve", "watchdog", "progress"}  # as away.py
LONG_WORDS = 30
FENCE, SPAN = re.compile(r"```.*?```", re.S), re.compile(r"`[^`\n]*`")
PATH = re.compile(r"(?<!\S)[\w.~-]*/[\w.~/-]*(?=[\s,;:!?)]|$)")
CHAIN = re.compile(r"\bthen\b|\bafter that\b|\bonce you\b|(?:^|\s)1[.)]\s.*(?:^|\s)2[.)]\s", re.I | re.S)
OURS = re.compile(r"\b(?:reload|refresh|test (?:it|this|that|the|on|whether|if)|try it|confirm (?:that )?it works)\b|\bsay (?:if|whether) it looks right\b"
                  r"|\breview (?:the |this |that |our |a |an )?(?:open )?(?:pr|pull request)\b"
                  r"|\bopen (?:the |your |a )?(?:\w+ )?(?:dashboard|app|page|card)\b.*\b(?:say|check|see)\b", re.S)
WHY = {"long": "It is over 30 words.",
       "many": "It says more than one thing: one sentence, one ask.",
       "command": "It holds a command, flag or path: say what you need in plain words, or run it yourself.",
       "chain": "It chains steps: ask for the one step only they can do, and do the rest.",
       "ours": "It asks them to check our work: test it yourself, and ask only for a device or account check you can't do.",
       "offer": "It is an offer, and nothing stops until they answer, so it won't count as waiting on them: say it in the post, and write `Decided:` for the default you took."}

EXAMPLE = {"command": "Instead of `For you: run `hive go` then reload`, write `For you: say yes and I'll switch it on.`",
           "many": "Instead of two asks in one line, keep the one only they can answer: `For you: pick red or blue for the header.`",
           "long": "Cut to one plain sentence: `For you: OK to buy the $12 domain?`",
           "chain": "Ask for the single step and do the rest yourself: `For you: send me the account code.`",
           "ours": "Test it yourself; ask only what needs their device: `For you: tell me if the phone login works.`",
           "offer": "Take the default and say so: `Decided: blue header; one post from you changes it.`"}
ORDER = ["command", "many", "long", "chain", "ours", "offer"]  # commonest first in the last day's board
# The test before a For you line: only these six are the person's; anything else an agent decides and writes up as Decided. A word list, so it warns and never refuses.
THEIRS = re.compile(r"\$|\b(?:pay|buy|purchase|order|checkout|check out|invoice|budget|price|cost|bill|subscription|funds?|spend|quote"  # money
                    r"|account|github|waterlooworks|sign(?:ing)?[ -]?(?:in|up)|log[ -]?in|token|password|credentials?|api key|keys?|cookies?|secret|permission|invite|2fa"  # an account
                    r"|your (?:own )?(?:voice|name|word|number|career|cv|resume|interview)|e-?mail|dm|slack|message|reply to|linkedin|show hn|publish|post on|send"  # their voice
                    r"|phone|iphone|laptop|mac|windows|pc|powershell|print|printer|device|call|usb|browser|bench|solder|hardware|scope"  # a device
                    r"|root|rootwarden|sudo|warden"  # root
                    r"|hard limit|delete|history)\b", re.I)  # a hard limit
DECIDE = ("Test before a For you line: is it money, an account, their voice, a device, root or a hard limit? "
          "This one names none of them, so decide it yourself, write `Decided: <what>` and carry on; keep the For you line only if it really is one of the six.")

CARRIED = re.compile(r"\b(?:supersed\w*|replaced by|restated|same (?:\S+ ){0,3}(?:wait|ask|order|run)|one wait|later line|newer line|carried (?:in|by)|agents? owns?|owned by|agent'?s job|agent work)\b", re.I)
POST_ID, ASK_ID, AGENT_NAME = re.compile(r"\b[0-9a-f]{12}\b"), re.compile(r"\bk\d{4}\b"), re.compile(r"\b[a-z][0-9a-f]{4}\b")

def close_gap(why, closing, open_posts, open_asks, agents):
    """What's missing from a close that says someone else carries the line: it must name a For you still open (not one being closed), an open ask, or the agent. None when the reason doesn't say that, or it names one. Goes by words, so a reason worded otherwise passes; the weekly sample of closes reads those."""
    if not CARRIED.search(why or ""): return None
    live = set(open_posts) - {str(c) for c in closing}
    if live & set(POST_ID.findall(why)) or set(open_asks) & set(ASK_ID.findall(why)) or set(agents) & set(AGENT_NAME.findall(why)): return None
    return "this close says the line is carried elsewhere, so name what carries it: a For you post still open, an open ask (k1234), or the agent's name"

def lines(text):
    """The text after each For you marker in a post, whitespace-joined, backticked spans left in. A marker inside a code fence or backticks isn't one."""
    out, text = [], FENCE.sub(" ", str(text or ""))
    for m in FOR_YOU.finditer(text):
        end = text.find("\n", m.end())
        line = " ".join(text[m.end(): None if end < 0 else end].split()).lstrip("*_ ")
        if line: out.append(line)
    return out

def is_path(tok):
    """A word with a slash, less the ones that are only alternatives: yes/no, and/or, A/B, Windows/Mac."""
    parts = tok.strip("/").split("/")
    return tok.startswith(("/", "~", ".")) or not (all(len(p) <= 3 for p in parts) or all(p[:1].isupper() and p.isalpha() for p in parts))

def flags(line):
    """Which of long, many, command, chain, ours, offer a For you line reads as. A line saying nothing is needed has none."""
    line = str(line or "")
    if NOTHING.match(line): return []
    bare = SPAN.sub(" ", line)
    out = []
    if len(line.split()) > LONG_WORDS: out.append("long")
    if len(re.findall(r"[.!?]\s+[A-Z]", bare)) >= 2 or ";" in bare: out.append("many")
    if SPAN.search(line) or re.search(r"(?<!\S)--[a-z]", bare) or any(is_path(m[0]) for m in PATH.finditer(bare)): out.append("command")
    if CHAIN.search(bare): out.append("chain")
    if OURS.search(bare.lower()): out.append("ours")
    if OFFER.match(bare): out.append("offer")
    return out

def decidable(line):
    """True when a For you line names none of the six things only the person can do, so an agent should have decided it. A line saying nothing is needed isn't one."""
    line = str(line or "")
    return bool(line) and not NOTHING.match(line) and not THEIRS.search(line)

def why(kinds):
    """One plain sentence per flag kind."""
    return " ".join(WHY[k] for k in kinds if k in WHY)

def warning(text):
    """What `hive post` says on stderr after a post whose For you line reads as more than a plain ask; None when none does. The post has landed either way."""
    kinds = list(dict.fromkeys(k for l in lines(text) for k in flags(l)))
    decide = f"{DECIDE} " if any(decidable(l) for l in lines(text)) else ""
    if not kinds: return f"hive post: posted. {decide}".rstrip() if decide else None
    main = next(k for k in ORDER if k in kinds)
    return (f"hive post: posted. {decide}The For you line reads as {', '.join(kinds)}: it goes on the person's card as written, and a plain one-sentence ask under 30 words lands better. {why(kinds)} "
            f"Commonest here ({main}): {EXAMPLE[main]} "
            "What fits: a decision, money, an account or an action only they can take. If you can settle it yourself, do, and say so in a `Decided:` line.")

def tally(posts, t0, t1):
    """(For you lines from agents in the window, of those how many have a flag). The hive's own threads and lines saying nothing is needed don't count, as in away.py."""
    n = flagged = 0
    for p in posts:
        if not t0 <= p.get("at", "") <= t1 or not AGENT.match(str(p.get("from", "")).lower()) or p.get("thread") in SELF_THREADS: continue
        for l in lines(p.get("text")):
            if NOTHING.match(l): continue
            n += 1; flagged += bool(flags(l))
    return n, flagged

def decidable_tally(posts, t0, t1):
    """(For you lines in the window, of those how many an agent could have decided), over the same lines as tally."""
    n = found = 0
    for p in posts:
        if not t0 <= p.get("at", "") <= t1 or not AGENT.match(str(p.get("from", "")).lower()) or p.get("thread") in SELF_THREADS: continue
        for l in lines(p.get("text")):
            if NOTHING.match(l): continue
            n += 1; found += decidable(l)
    return n, found

def kind_tally(posts, t0, t1):
    """{kind: lines carrying it} over the same lines as tally; a line can carry several kinds."""
    out = dict.fromkeys(ORDER, 0)
    for p in posts:
        if not t0 <= p.get("at", "") <= t1 or not AGENT.match(str(p.get("from", "")).lower()) or p.get("thread") in SELF_THREADS: continue
        for l in lines(p.get("text")):
            for k in flags(l): out[k] += 1
    return out

def per_day(days=3, path=None, now=None):
    """(new For you lines per day, the share an agent could have decided in %) over the last `days` days: what `foryou.py per-day` prints for `hive improve log --check`."""
    import datetime as dt, json, os
    now = now or dt.datetime.now(dt.timezone.utc)
    t0, t1 = (now - dt.timedelta(days=days)).isoformat(), now.isoformat()
    posts = []
    with open(path or os.path.expanduser("~/board.jsonl"), errors="replace") as f:
        for raw in f:
            try: posts.append(json.loads(raw))
            except ValueError: continue
    n, found = decidable_tally(posts, t0, t1)
    return round(n / days, 1), round(100 * found / n, 1) if n else 0.0

if __name__ == "__main__":
    import sys
    if sys.argv[1:2] in (["per-day"], ["decidable-pct"]):
        print(per_day()[sys.argv[1] == "decidable-pct"])
    else:
        sys.exit("usage: foryou.py per-day | decidable-pct")
