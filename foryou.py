#!/usr/bin/env python3
"""The For you line: what marks it, and what makes it read as more than a plain ask. `hive post` warns on these and `hive progress` counts them; nothing here refuses anything."""
import re

FOR_YOU = re.compile(r"^[\s>*_#-]*for you[\s*_]*:", re.I | re.M)  # the line that puts a post on the person's card
NOTHING = re.compile(r"^\W*(nothing (is )?needed\b|no action\b|nothing to do\b|nothing\s*(?:[;:,.]|$))", re.I)
STOOD_DOWN = re.compile(r"(?:\bnothing (?:now )?(?:(?:still |now )?waits|is (?:still )?waiting|is pending|is (?:still )?needed|is required|is wanted|needed|required|left) (?:on|from|for) you\b"
                        r"|\bnothing for you to do\b|\byou (?:don't|do not|no longer) need to (?:do anything|reply|answer|act|type|say|send|approve)\b"
                        r"|\b(?:the|your|that|this|my) (?:pending |waiting |earlier |last )?(?:yes:?(?: line)?|go|ok|approval|answer|reply|step|ask|for you(?: line)?)\b[^.\n]{0,40}?\b(?:can wait|is no longer needed|isn't needed any ?more|is not needed any ?more|is moot)\b)"
                        r"(?![^.\n]{0,40}\b(?:except|but|other than|besides|apart from|until you)\b)", re.I)
"""An agent saying the person needn't act now: "Nothing waits on you meanwhile", "the pending yes can wait" (ad5b1, 2026-11-25, while the headline still asked for that yes).
Read on the post's prose only: not inside code, quotes or a For you line of its own (see stood_down).
Not "nothing else/more waits on you": that assumes an earlier step still stands (aa008's review, 2026-11-25: af98e's 112116130ae0 gave a new task, then "Nothing else is waiting on you for this"). The except/apart-from guard covers all three forms."""
OFFER = re.compile(r"^\W*(?:if you\b|only if\b|optional(?:ly)?\b|(?:say|tell me|let me know) if you\b)", re.I)  # an offer, not a wait: nothing stops until they answer
AGENT = re.compile(r"^[a-z][0-9a-f]{4,}$")  # as away.py
AGENT_IN_PROSE = re.compile(r"(?<![\w-])@?a[0-9a-f]{4}(?![\w-])")  # an agent named in prose
SELF_THREADS = {"starter", "keeper", "improve", "watchdog", "progress"}  # as away.py
LONG_WORDS = 30
# Codes the person can't open (friction-asks A10, 2026-11-25: "what is gf26e i dont see it", "remote card (3076f52d743c)"): an agent's name (a letter and four hex, at least one a digit),
# an ask id (k1161) or a 12-hex post id. A link or a path holding one isn't flagged here (a link opens; a path is "command").
IDS = re.compile(r"(?<![\w/.-])@?(?:[a-z](?=[0-9a-f]{0,3}\d)[0-9a-f]{4}|k\d{4,}|(?=[0-9a-f]{0,11}\d)(?=[0-9a-f]{0,11}[a-f])[0-9a-f]{12})(?![\w/-])(?:'s)?")
# A concrete step: the line asks them to do, say or choose something. With none of these, they read it and ask "what do I do now?" (11161514308e, 111620214631, 111611412e74).
STEP = re.compile(r"\b(?:say|reply|answer|tell|pick|choose|decide|approve|confirm|send|sign|log|click|tap|open|go|plug|unplug|press|hold|push|pull|turn|switch|flash|put|take|clear|paste|attach|"
                  r"upload|export|download|copy|give|add|save|enter|type|order|buy|pay|check out|book|install|run|accept|merge|post|write|read|look|print|keep|share|forward|"
                  r"email|call|text|vote|rate|rank|list|fill|submit|pin|dictate|record|review|set|connect|pair|restart|reboot|charge|move|remove|delete|drop|stop|start|ask|let|try|use|visit|make|"
                  r"reload|refresh|close|dm|tag|finish|rename|pause|unpause|enable|disable|renew|update|sign in|log in|scan|test|find|join|wear|mount|plug in)\b|\?(?:\s|$)|\byes or no\b|\bor no\b", re.I)
# A machine-level word has to name its post: the root word command reads the post by id, so the id is the step, not a code to explain (ad748, friction-asks 11250213f5ed).
WORD_STEP = re.compile(r"\bh2ward" r"en-word\b|\b(?:tow" r"er|standing) word\b", re.I)
# "Nothing for you today", "nothing new on you": no step is missing, there is none (ad748, 11250213f5ed). NOTHING itself stays narrow: other code reads it as "the line is empty".
NOTHING_FOR_YOU = re.compile(r"^\W*nothing\W*$|\bnothing (?:else |more |new )?(?:(?:needed|waits|left|to do) )?(?:for|on|from) you\b", re.I)   # not "nothing changed on the build; k2840 is still open" (a0ad9, 112503027c3b)
POST_ID = re.compile(r"(?<![\w/.-])(?=[0-9a-f]{0,11}\d)(?=[0-9a-f]{0,11}[a-f])[0-9a-f]{12}(?![\w/-])")
FENCE, SPAN = re.compile(r"```.*?```", re.S), re.compile(r"`[^`\n]*`")
URL = re.compile(r"^(?:https?://|www\.|[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:ca|com|org|net|io|dev|ai|app|cc|co|uk|de)/)", re.I)
PATH = re.compile(r"(?<!\S)[\w.~-]*/[\w.~/-]*(?=[\s,;:!?)]|$)")
REPORT_BACK = re.compile(r"\bthen (?:tell me|say so|say which|let me know|reply|post)\b|\b(?:until|till|by|since|and|or) then\b", re.I)  # one step plus a report back is one ask, not a chain (2026-11-19: 2 of 8 flagged lines); 'until then' isn't a step (2026-11-21)
CHAIN = re.compile(r"\bthen\b|\bafter that\b|\bonce you\b|(?:^|\s)1[.)]\s.*(?:^|\s)2[.)]\s", re.I | re.S)
OURS = re.compile(r"\b(?<![-\w])(?:reload|refresh(?![- ]tokens?)|test (?:it|this|that|the|on|whether|if)|try it|confirm (?:that )?it works)\b|\bsay (?:if|whether) it looks right\b"
                  r"|\breview (?:the |this |that |our |a |an )?(?:open )?(?:pr|pull request)\b"
                  r"|\bopen (?:the |your |a )?(?:\w+ )?(?:dashboard|app|page|card)\b.*\b(?:say|check|see)\b", re.S)
DEVICE = re.compile(r"\b(?:plug(?:s|ged)? (?:\w+ ){0,4}in(?:to)?\b|unplug|usb|cable|power[- ]cycle|press (?:the )?(?:reset|boot) button)", re.I)  # a hands-on step with hardware only they can reach is theirs, not a check on our work (2026-11-23: a Lite plug-in line read as ours)
WHY = {"long": "It is over 30 words.",
       "many": "It says more than one thing: one sentence, one ask.",
       "command": "It holds a command, flag or path: say what you need in plain words, or run it yourself.",
       "chain": "It chains steps: ask for the one step only they can do, and do the rest.",
       "ours": "It asks them to check our work: test it yourself, and ask only for a device or account check you can't do.",
       "offer": "It is an offer, and nothing stops until they answer, so it won't count as waiting on them: say it in the post, and write `Decided:` for the default you took.",
       "ids": "It leans on a code they can't open (an agent's name, an ask or post id): say the thing itself, or what the page or button is called where they'll see it.",
       "vague": "It names no step they can take: say what to do, where, and what to reply."}

EXAMPLE = {"command": "Instead of `For you: run `hive go` then reload`, write `For you: say yes and I'll switch it on.`",
           "many": "Instead of two asks in one line, keep the one only they can answer: `For you: pick red or blue for the header.`",
           "long": "Cut to one plain sentence: `For you: OK to buy the $12 domain?`",
           "chain": "Ask for the single step and do the rest yourself: `For you: send me the account code.`",
           "ours": "Test it yourself; ask only what needs their device: `For you: tell me if the phone login works.`",
           "offer": "Take the default and say so: `Decided: blue header; one post from you changes it.`",
           "ids": "Instead of `For you: approve post 11060033b3ed`, write `For you: reply yes here to switch the attachments on.`",
           "vague": "Instead of `For you: the Lite 1 firmware question`, write `For you: reply 1 or 2 for which Lite firmware to keep.`"}
ORDER = ["command", "ids", "many", "long", "chain", "vague", "ours", "offer"]  # commonest first in the last day's board
# The test before a For you line: only these six are the person's; anything else an agent decides and writes up as Decided. A word list, so it warns and never refuses.
THEIRS = re.compile(r"\$|\b(?:pay|buy|purchase|order|checkout|check out|invoice|budget|price|cost|bill|subscription|funds?|spend|quote"  # money
                    r"|account|github|waterlooworks|sign(?:ing)?[ -]?(?:in|up)|log[ -]?in|token|password|credentials?|api key|keys?|cookies?|secret|permission|invite|2fa"  # an account
                    r"|your (?:own )?(?:voice|name|word|number|career|cv|resume|interview|site|website|domain|form|application)|in your name|submit(?:s|ting|ted)?|submissions?|upload(?:s|ing|ed)?|e-?mail|dm|slack|message|reply(?: yes| no)? to|(?:h\d|another hive)'s board|linkedin|show hn|publish(?:ing|ed)?|post on|send|own words|dictat(?:e|ion)|(?:your|their) (?:own )?predictions?"  # their voice (own words, dictation, a prediction: 2026-11-23)
                    r"|phone|iphone|laptop|mac|windows|pc|powershell|print|printer|device|call|usb|browser|bench|solder|hardware|scope|tell me (?:what (?:it|each|they) shows?|if it (?:works|loads))"  # a device
                    r"|root|rootwarden|sudo|warden"  # root
                    r"|hard limits?|delete|history|to the outside|outside world|public)\b", re.I)  # a hard limit (opening the machine to the outside: 2026-11-23; your site, in your name, submit, public: the 5 false decidables of 11-24, thread improve-survey)
DECIDE = ("Test before a For you line: is it money, an account, their voice, a device, root or a hard limit? "
          "This one names none of them, so decide it yourself, write `Decided: <what>` and carry on; keep the For you line only if it really is one of the six.")

def ask_advice(id_):
    """The same test put to `hive asks person`, whose reason says what the ask waits on."""
    return (f"Test before waiting on the person: is it money, an account, their voice, a device, root or a hard limit? This reason names none of them. If it is a reversible choice, decide it yourself and write `Decided: <what>`. If it is a fact only they hold, leave it here. "
            f"If it waits on a run, a machine or a later order: `hive asks hold {id_} --why ...`. If only one step is theirs: `hive asks done {id_} --evidence POST --left \"the step\"`.")

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
    if URL.match(tok): return False   # a link to open isn't a command (2026-11-19: hivelab.ca/<project>/ was 2 of 8 flagged lines)
    parts = tok.strip("/").split("/")
    return tok.startswith(("/", "~", ".")) or not (all(len(p) <= 3 for p in parts) or all(p[:1].isupper() and p.isalpha() for p in parts))

def stood_down(text, to=()):
    """The post tells the person nothing waits on them now (STOOD_DOWN in its prose, or a `For you: nothing` line) and puts no new step to them: an earlier wait in its thread is overtaken.
    A post addressed to agents and not the person, or a sentence that names an agent ("Thanks, af9de, nothing more is needed from you"), is talking to an agent: 5 of 25 matches on the board, 11-18 to 11-25."""
    to = [str(x).lower().lstrip("@") for x in (to if isinstance(to, (list, tuple)) else [to] if to else [])]
    if any(AGENT.match(x) for x in to) and "person" not in to: return False
    found = lines(text)
    if any(not NOTHING.match(l) for l in found): return False
    if found: return True
    # a one-word span keeps its word (`ae976`, `yes:`): names are backticked by convention, and the phrase can't hide in one word
    prose = "\n".join(l for l in SPAN.sub(lambda m: m[0].strip("`") if len(m[0].split()) == 1 else " ", FENCE.sub(" ", str(text or ""))).splitlines() if not l.lstrip().startswith(">"))
    for m in STOOD_DOWN.finditer(prose):
        start = max(prose.rfind(c, 0, m.start()) for c in ".!?\n") + 1
        if not AGENT_IN_PROSE.search(prose[start:m.end()]): return True
    return False

def flags(line):
    """Which of long, many, command, chain, ours, offer, ids, vague a For you line reads as. A line saying nothing is needed has none."""
    line = str(line or "")
    if NOTHING.match(line): return []
    bare = SPAN.sub(" ", line)
    out = []
    if len(line.split()) > LONG_WORDS: out.append("long")
    if len(re.findall(r"[.!?]\s+[A-Z]", bare)) >= 2 or ";" in bare: out.append("many")
    if SPAN.search(line) or re.search(r"(?<!\S)--[a-z]", bare) or any(is_path(m[0]) for m in PATH.finditer(bare)): out.append("command")
    if CHAIN.search(REPORT_BACK.sub(" ", bare)): out.append("chain")
    if OURS.search(bare.lower()) and not DEVICE.search(bare): out.append("ours")
    if OFFER.match(bare): out.append("offer")
    said = " ".join(t for t in re.sub(r"`([^`\s]+)`", r"\1", line).split() if not URL.match(t))
    if IDS.search(POST_ID.sub(" ", said) if WORD_STEP.search(line) else said): out.append("ids")   # a word line must name its post; any other id (k2997) still needs saying
    if not STEP.search(bare) and not NOTHING_FOR_YOU.search(line) and not STOOD_DOWN.search(line): out.append("vague")
    return out

def decidable(line):
    """True when a For you line names none of the six things only the person can do, so an agent should have decided it. A line saying nothing is needed isn't one."""
    line = str(line or "")
    return bool(line) and not NOTHING.match(line) and not THEIRS.search(line)

def decidable_lines(text):
    """decidable() over each For you line of a post, where a line ending in a colon is read with the code block after it: `run this once on the machine:` then a sudo block is root (2026-11-24: 1 of the day's 5 false decidables)."""
    text, out = str(text or ""), []
    for l in lines(text):
        block = ""
        if l.rstrip().endswith(":"):
            at = text.find(l.split()[-1])
            m = FENCE.search(text, at) if at >= 0 else None
            block = m.group(0) if m else ""
        # the line runs on below with no blank line (`in a terminal on the host, run` then a root-command line), or points at a line or command
        # shown above it (`paste that line into the box` after a `yes:` word block): 2 of the 6 false decidables of 2026-11-25, thread meta-person
        at = text.find(l)
        if at >= 0: block += "\n" + text[at + len(l):].split("\n\n", 1)[0]
        if re.search(r"\b(?:that|this|the) (?:line|word|command|block)\b", l, re.I): block += "\n" + "\n".join(m.group(0) for m in FENCE.finditer(text))
        out.append(decidable(l) and not THEIRS.search(block))
    return out

def why(kinds):
    """One plain sentence per flag kind."""
    return " ".join(WHY[k] for k in kinds if k in WHY)

def warning(text):
    """What `hive post` says on stderr after a post whose For you line reads as more than a plain ask; None when none does. The post has landed either way."""
    kinds = list(dict.fromkeys(k for l in lines(text) for k in flags(l)))
    decide = f"{DECIDE} " if any(decidable_lines(text)) else ""
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
        for l, d in zip(lines(p.get("text")), decidable_lines(p.get("text"))):
            if NOTHING.match(l): continue
            n += 1; found += d
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
