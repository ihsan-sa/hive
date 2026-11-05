#!/usr/bin/env python3
"""Checks a dashboard post's signature (design: ~/kb, "signed board"; the person, thread signed-board). Standard library only, one file, no Hive imports,
so a warden can read it in a sitting and keep its own copy where the agents can't edit it.

What a mark means. The browser holds a non-extractable P-256 key per device and signs the text the person typed, with the thread and the time.
  pinned    the signature is good and root's key file names this key. A plain append to the board or `hive post --as person` can't make this.
  unpinned  the signature is good but root hasn't pinned the key. Any agent can make its own key, so this is no evidence of who wrote it.
  revoked   root's file names the key and has withdrawn it.
  bad       the signature is wrong, or it doesn't fit the post it's on (other thread, other time, a used nonce, words added in front).
  none      unsigned. That is every old post, every Slack post and every phone post: a claim, neither good nor bad.
It is still evidence, not a word: the agents' user serves the dashboard, so an agent that patches the page's code can have the person's own key sign what it likes.
"""
import base64, hashlib, json, re, stat, sys
from datetime import datetime, timezone
from pathlib import Path

PINS = Path("/var/lib/h2warden/person-keys.json")   # root's, world-readable. Format: {"keys": {"<fingerprint>": {"device": "<name>", "revoked": false}}}
WINDOW = 300     # seconds between the time signed and the time on the post
TAG = "hive-post-v1"
# What person_post may put before the signed words, in the order it adds them. Nothing else may be in front of them.
ADDED = re.compile(r"(?:@[A-Za-z0-9_-]+ )?(?:reply to message [0-9a-f]{6,20}: )?(?:\((?:from [^()\n]{1,100}, )?web app(?:, [^\n]*)?\) )?")

P = 0xffffffff00000001000000000000000000000000ffffffffffffffffffffffff
A = P - 3
B = 0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b
N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551
G = (0x6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296, 0x4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5)

def add(a, b):
    if a is None: return b
    if b is None: return a
    if a[0] == b[0]:
        if (a[1] + b[1]) % P == 0: return None
        m = (3 * a[0] * a[0] + A) * pow(2 * a[1], -1, P) % P
    else:
        m = (b[1] - a[1]) * pow(b[0] - a[0], -1, P) % P
    x = (m * m - a[0] - b[0]) % P
    return (x, (m * (a[0] - x) - a[1]) % P)

def mul(k, pt=G):
    out = None
    while k:
        if k & 1: out = add(out, pt)
        pt = add(pt, pt); k >>= 1
    return out

def unb64(s):
    if not isinstance(s, str) or not re.fullmatch(r"[A-Za-z0-9_-]+", s): raise ValueError("not base64url")
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))

def fingerprint(key):
    """What the person reads off Settings and root pins: 80 bits of the SHA-256 of the raw public key."""
    return hashlib.sha256(unb64(key)).hexdigest()[:20]

def verify(key, msg, sig):
    """ECDSA P-256 with SHA-256. key: 65 raw bytes, sig: r||s, 64 bytes, as WebCrypto gives them."""
    if len(key) != 65 or key[0] != 4 or len(sig) != 64: return False
    q = (int.from_bytes(key[1:33], "big"), int.from_bytes(key[33:], "big"))
    if not (q[0] < P and q[1] < P) or (q[1] * q[1] - q[0] ** 3 - A * q[0] - B) % P: return False
    r, s = int.from_bytes(sig[:32], "big"), int.from_bytes(sig[32:], "big")
    if not (0 < r < N and 0 < s < N): return False
    e = int.from_bytes(hashlib.sha256(msg).digest(), "big")
    w = pow(s, -1, N)
    pt = add(mul(e * w % N), mul(r * w % N, q))
    return pt is not None and pt[0] % N == r

def message(sig, words):
    """The exact bytes the browser signs (web/js/core/sign.js). Newlines can't be in the first five fields: the times and names are checked, the words come last."""
    return "\n".join([TAG, sig["at"], sig["nonce"], sig["thread"], sig["reply_to"], words]).encode()

def pins(path=PINS, need_root=True):
    """Root's key file, or {} if it's missing or anyone but root could have written it."""
    try:
        st = path.stat()
        if need_root and (st.st_uid != 0 or st.st_mode & (stat.S_IWGRP | stat.S_IWOTH)): return {}
        keys = json.loads(path.read_text()).get("keys")
    except (OSError, ValueError, AttributeError): return {}
    return keys if isinstance(keys, dict) else {}

def when(s):
    t = datetime.fromisoformat(s.replace("Z", "+00:00"))
    return t if t.tzinfo else t.replace(tzinfo=timezone.utc)

def first_nonces(posts):
    """nonce -> id of the first post on the board carrying it: a copy later on the board is a replay."""
    out = {}
    for p in posts:
        s = p.get("sig")
        if isinstance(s, dict) and isinstance(s.get("nonce"), str): out.setdefault(s["nonce"], p.get("id"))
    return out

def check(p, first=None, pinned=None):
    """{"state", "why", "fp", "device", "words", "added"} for a post. first: first_nonces(board), pinned: pins()."""
    s = p.get("sig")
    if not s: return {"state": "none"}
    bad = lambda why, **more: {"state": "bad", "why": why, **more}
    if not isinstance(s, dict) or s.get("v") != 1 or not all(isinstance(s.get(k), str) for k in ("key", "at", "nonce", "thread", "reply_to", "s")) or type(s.get("n")) is not int:
        return bad("the signature isn't in the expected form")
    if any("\n" in s[k] for k in ("at", "nonce", "thread", "reply_to")): return bad("the signature isn't in the expected form")
    text = str(p.get("text", ""))
    if not 0 < s["n"] <= len(text): return bad("the signed length doesn't fit the text")
    added, words = text[:-s["n"]], text[-s["n"]:]
    try: fp = fingerprint(s["key"]); good = verify(unb64(s["key"]), message(s, words), unb64(s["s"]))
    except (ValueError, TypeError, OverflowError): return bad("the signature can't be read")
    if not good: return bad("the signature doesn't match the words", fp=fp)
    more = {"fp": fp, "words": words, "added": added}
    if not ADDED.fullmatch(added): return bad("words were added in front of what was signed", **more)
    if s["thread"] != p.get("thread") and not (s["thread"] == "" and p.get("thread") == p.get("id")): return bad(f"signed for thread {s['thread'] or '(a new one)'}", **more)
    if s["reply_to"] and p.get("reply_to") and s["reply_to"] != p["reply_to"]: return bad(f"signed as a reply to {s['reply_to']}", **more)
    if not s["reply_to"] and p.get("reply_to"): return bad("signed as no reply", **more)
    try: gap = abs((when(s["at"]) - when(str(p.get("at", "")))).total_seconds())
    except ValueError: return bad("the signed time can't be read", **more)
    if gap > WINDOW: return bad(f"signed {int(gap)} s from the post's time", **more)
    if first is not None and first.get(s["nonce"], p.get("id")) != p.get("id"): return bad(f"a copy: this signature was first used on {first[s['nonce']]}", **more)
    pin = (pins() if pinned is None else pinned).get(fp)
    if not isinstance(pin, dict): return {"state": "unpinned", **more}
    if pin.get("revoked"): return {"state": "revoked", "device": pin.get("device"), **more}
    return {"state": "pinned", "device": pin.get("device"), **more}

def line(r, p=None):
    """The mark `hive read` prints under a post, or "" for an unsigned one."""
    st = r["state"]
    if st == "none": return ""
    if st == "bad": return f"✗ BAD SIGNATURE: {r['why']}"
    who = f"key {r['fp']}" + (f", {r['device']}" if r.get("device") else "")
    if st == "revoked": return f"✗ signed with a withdrawn key ({who})"
    if st == "unpinned": return f"~ signed, key not pinned ({who}): any agent can make such a key, so this shows nothing about who wrote it"
    tail = "".join([f"; the dash added in front, unsigned: {r['added'][:60]!r}" if r.get("added") else "", "; its files aren't signed" if p and p.get("files") else ""])
    return f"✓ signed ({who}, pinned by root){tail}; evidence only, not a word, and an agent can edit this check or the page that signs"
