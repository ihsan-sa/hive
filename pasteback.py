#!/usr/bin/env python3
"""Paste-back asks: an agent's post that has the person run something and paste or send back what it prints. The agent should run it
itself (on the machine, or on the laptop through the laptop worker) and read the output there. On 2026-11-26 the HFSS start-up fault took
six such rounds in thread 112008283ab6 (112610335f83 "When it ends, paste the last 20 lines it prints", 11261045332e, 1126105044f1,
11261103afdc) before the person wrote "at this point setup the env so you can troubleshoot yourself" (112611317b28); friction register
D17, thread friction-chase.

A sentence asks for it when a paste-back verb opens a clause there ("paste", "send back", "share", "post", "reply with", "tell me", "let me
know", or "you send ...", "can you paste ...") and its object is output ("the output", "the last 20 lines", "what it prints", "the log",
"the result files", "the error", a `.log` or `.json` file). Nothing in code, in a quote, or after "don't", "no need to", "won't have
to"... counts, and a post addressed to other agents (a brief to `@new`, a job for `@rootwarden`) is not asking the person.

  pasteback.py replay [--days 14]     every agent post on the board that would have been warned, with the sentence"""
import json, re, sys, datetime as dt
from pathlib import Path

HOME = Path.home()
AGENT = re.compile(r"[a-z][0-9a-f]{4}")   # hive's HASH_NAME
WORKER = "projects/via-surrogate/hfss/queue/README.md"

FENCED = re.compile(r"```.*?(?:```|\Z)", re.S)
INLINE = re.compile(r"`([^`\n]*)`")
QUOTED = re.compile(r"[\"“]([^\"”\n]{0,300})[\"”]")
LOGFILE = re.compile(r"\.(log|txt|json|out|csv|err)\b", re.I)
ELSEWHERE = re.compile(r"^[^.!?\n]{0,60}?\b(?:into|under|in)\s+(?:the\s+|a\s+|your\s+)?(?:[\w-]+\s+){0,3}?(?:drafts?|notes|heading|section|rows?|"
                       r"doc|document|page|spreadsheet|sheet|form|LOGFILE|CODESPAN|[\w-]+\.md)\b", re.I)   # "paste its output into the drafts": a step of the work, not a hand-back
SENT = re.compile(r"(?<=[.!?])\s+|\n+")

OPEN = (r"(?:^|[,;:(]\s*|\b(?:then|and|please|just|also|simply|or|so)\s+|\b(?:you|you'll|you can|can you|could you|would you|will you|"
        r"you could|you'd)\s+(?:then\s+|also\s+|just\s+|please\s+)?)")
VERB = (r"(paste|send(?:\s+(?:back|me|us|over|along))?|share|post(?=[^.!?\n,;]{0,60}\b(?:here|back|in this thread))|reply\s+with|tell\s+me|let\s+me\s+know|give\s+me|"
        r"copy(?=[^.]{0,60}\b(?:here|back|to me|into (?:this|the|a) (?:thread|chat|reply|post))))(?![-\w])")
OBJECT = (r"(output|(?:last|first|final|\d+)\s+lines|lines (?:it|that|they) print|what (?:it|that|this|they|the \w+) (?:prints?|printed|lists|returns|reports|logs)|"
          r"the end of (?:the|its) (?:output|log|run)|the tail|the logs?\b|its logs?\b|logs\b|log files?|result files?|results? back|"
          r"(?:the |any )(?:error|traceback|stack trace)s?\b(?! page)|(?:CODESPAN|LOGFILE)\s+back)")
ASK = re.compile(OPEN + VERB + r"\b(?:(?:(?!\b(?:I|I'll|I'd|we|we'll|and|then|while|so)\b)[^.!?\n,;]){0,80}?\b" + OBJECT +
                 r"|\s+(?:me\s+|us\s+|back\s+)?(?:the\s+(?:file|files|log)\s+|both\s+)?LOGFILE)", re.I)   # "post code here and I'll post what it prints": the object is the agent's
NEGATED = re.compile(r"\b(don't|do not|doesn't|won't|will not|no need|needn't|never|without|instead of|no longer|not have to|"
                     r"won't have to|don't have to|not need|rather than|stop)\b", re.I)
FIRST = re.compile(r"\b(I|I'll|I'm|I'd|I've|we|we'll|we're)\b")   # "I'll merge it and post the result": the agent's own step
REPORTED = re.compile(r"\b(?:has|have|had|having|makes?|made|lets?|asks?|asked|asking|gets?|tells?|wants?)\s+(?:the person|someone|people|them|users?)\b|\b(?:an?|the|each|such|real|this|that)\s+(?:\w+\s+)?(?:ask|asks|request)\s+to\s+\w", re.I)   # "warns an agent whose post has the person paste ...": about the ask, not one (a real ask says "you")
SELF = re.compile(r"\b(I|I'll|I'd|we|we'll|I will|I can|it|the worker|the script|the agent)\s+(?:then\s+|also\s+)?$", re.I)


def plain(text):
    """The post's own words: no fenced blocks, no lines quoted with '>', no quotations; code spans become a word (a log file name
    stays an object: "paste `out\\run.log`")."""
    t = FENCED.sub("\n", text or "")
    t = "\n".join(l for l in t.splitlines() if not l.lstrip().startswith(">"))
    t = INLINE.sub(lambda m: " LOGFILE " if LOGFILE.search(m.group(1)) else " CODESPAN ", t)
    return QUOTED.sub(" ", t)


def asks(text):
    """The sentences of `text` that ask someone to paste or send back what a run prints, in order."""
    out = []
    for s in SENT.split(plain(text)):
        for m in ASK.finditer(s):
            before = s[:m.start(1)]
            if NEGATED.search(before) or REPORTED.search(before) or SELF.search(before) or FIRST.search(before) or ELSEWHERE.search(s[m.end(1):]): continue
            out.append(s.strip()); break
    return out


def for_person(p):
    """True when an agent wrote the post and it is not addressed only to other agents (a brief or a root job may ask for output)."""
    me = str(p.get("from", "")).lower()
    if not AGENT.fullmatch(me): return False
    to = {str(n).lstrip("@").lower() for n in p.get("to") or []} - {me}
    return not to or "person" in to


def warning(p):
    """The line `hive post` prints after an agent's post that asks the person to paste back output, else None."""
    if not for_person(p): return None
    hit = asks(p.get("text", ""))
    if not hit: return None
    s = re.sub(r"\s*\b(?:CODESPAN|LOGFILE)\b\s*", " `…` ", hit[0]).strip()
    s = s if len(s) <= 140 else s[:137] + "..."
    return (f"hive post: this asks the person to run something and paste or send back what it prints (\"{s}\"). Run it yourself and read "
            f"the output: on the machine, or for work that needs the laptop (AEDT, Windows, the bench) through the laptop worker "
            f"({WORKER}) once it has checked in. Ask the person only for what no agent can do, such as a sign-in or a click on their "
            f"screen, and say why (friction register D17).")


def on_post(p):
    try: return warning(p)
    except Exception: return None


def replay(days=14, board=None, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    cut = (now - dt.timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%S")
    rows = []
    for line in open(board or HOME / "board.jsonl", encoding="utf-8"):
        try: p = json.loads(line)
        except ValueError: continue
        if str(p.get("at", "")) < cut or not for_person(p): continue
        hit = asks(p.get("text", ""))
        if hit: rows.append((p, hit))
    return rows


if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser(); ap.add_argument("cmd", choices=["replay"]); ap.add_argument("--days", type=float, default=14)
    a = ap.parse_args()
    rows = replay(a.days)
    for p, hit in rows: print(f"{p['id']}  {p['from']}  {str(p.get('thread'))[:16]:16}  {hit[0][:150]}")
    print(f"-- {len(rows)} posts in {a.days:g} days")
