#!/usr/bin/env python3
"""The For you line: what marks it, and what makes it read as more than a plain ask. `hive post` warns on these and `hive progress` counts them; nothing here refuses anything."""
import re

FOR_YOU = re.compile(r"^[\s>*_#-]*for you[\s*_]*:", re.I | re.M)  # the line that puts a post on the person's card
NOTHING = re.compile(r"^\W*(nothing (is )?needed|no action|nothing to do)\b", re.I)  # as away.py
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
       "ours": "It asks them to check our work: test it yourself, and ask only for a device or account check you can't do."}

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
    """Which of long, many, command, chain, ours a For you line reads as. A line saying nothing is needed has none."""
    line = str(line or "")
    if NOTHING.match(line): return []
    bare = SPAN.sub(" ", line)
    out = []
    if len(line.split()) > LONG_WORDS: out.append("long")
    if len(re.findall(r"[.!?]\s+[A-Z]", bare)) >= 2 or ";" in bare: out.append("many")
    if SPAN.search(line) or re.search(r"(?<!\S)--[a-z]", bare) or any(is_path(m[0]) for m in PATH.finditer(bare)): out.append("command")
    if CHAIN.search(bare): out.append("chain")
    if OURS.search(bare.lower()): out.append("ours")
    return out

def why(kinds):
    """One plain sentence per flag kind."""
    return " ".join(WHY[k] for k in kinds if k in WHY)

def warning(text):
    """What `hive post` says on stderr after a post whose For you line reads as more than a plain ask; None when none does. The post has landed either way."""
    kinds = list(dict.fromkeys(k for l in lines(text) for k in flags(l)))
    if not kinds: return None
    return (f"hive post: posted. The For you line reads as {', '.join(kinds)}: it goes on the person's card as written, and a plain one-sentence ask under 30 words lands better. {why(kinds)} "
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
