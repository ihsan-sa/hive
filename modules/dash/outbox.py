#!/usr/bin/env python3
"""The Outbox page's server side (projects/outward/DESIGN.md "The Outbox page"): every outward item, and the two buttons that act here.

It only calls the outward module's own command line (`hive-out`, modules/outward/outward.py), so every rule stays in one place:
    view()            hive-out outbox: items newest first, the door, the kinds that can be automatic
    thumb(id, n)      hive-out file: one item's file, served only when its bytes are a PNG or JPEG
    act(d)            {"do": "reject", "id", "why"} -> hive-out reject;  {"do": "off", "kind", "why"} -> hive-out off, at once
Approve and Auto on never come here: the page opens root's /confirm card, and the person signs there what root shows.
"""
import json, os, re, subprocess, sys
from pathlib import Path

HOME = Path.home()
OUT = Path(os.environ.get("HIVE_OUT_PY") or HOME / "modules" / "outward" / "outward.py")
ID_RE = re.compile(r"out-[0-9a-f]{8}")
AUTO_KINDS = ("quote", "followup")
THREAD = "hive-mail-outbound"
CTL = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f​-‏‪-‮⁦-⁩﻿]")
MAX_THUMB = 25 << 20


def _run(*argv, binary=False, timeout=30):
    env = {**os.environ, "HIVE_NAME": "hive-mail"}   # its posts say what the page did; they are not the person's words
    env.pop("HIVE_THREAD", None)
    p = subprocess.run([sys.executable, "-I", str(OUT), *argv], capture_output=True, timeout=timeout, env=env)
    if p.returncode:
        raise ValueError(CTL.sub("", p.stderr.decode("utf-8", "replace")).strip().removeprefix("hive-out: ")[:400] or "hive-out failed")
    return p.stdout if binary else p.stdout.decode("utf-8", "replace")


def _clean(v):
    if isinstance(v, str): return CTL.sub("", v)
    if isinstance(v, list): return [_clean(x) for x in v]
    if isinstance(v, dict): return {_clean(k): _clean(x) for k, x in v.items()}
    return v


def view():
    if not OUT.is_file(): return {"unset": True, "items": [], "auto": [], "paused": None, "root": False}
    try: v = json.loads(_run("outbox"))
    except (ValueError, OSError, subprocess.SubprocessError) as e: return {"error": f"The outbox can't be read: {e}"[:300], "items": [], "auto": []}
    return _clean(v) if isinstance(v, dict) else {"error": "The outbox gave an odd answer.", "items": [], "auto": []}


def thumb(iid, n):
    if not ID_RE.fullmatch(iid or "") or not re.fullmatch(r"[1-5]", n or ""): raise ValueError("no such file")
    data = _run("file", iid, n, binary=True)
    if len(data) > MAX_THUMB: raise ValueError("too big to show")
    if data.startswith(b"\x89PNG\r\n\x1a\n"): return data, "image/png"
    if data.startswith(b"\xff\xd8\xff"): return data, "image/jpeg"
    raise ValueError("only PNG and JPEG files have a picture here")


def act(d):
    if not isinstance(d, dict): raise ValueError("send {do, ...}")
    why = " ".join(str(d.get("why") or "").split())[:300]
    if not why: raise ValueError("say why")
    if d.get("do") == "reject":
        iid = str(d.get("id") or "")
        if not ID_RE.fullmatch(iid): raise ValueError("no such item")
        said = _run("reject", iid, "--why", "rejected on the Outbox page: " + why)
    elif d.get("do") == "off":
        k = d.get("kind")
        if k not in AUTO_KINDS: raise ValueError(f"only {' and '.join(AUTO_KINDS)} can be automatic")
        said = _run("off", "--kind", k, "--why", "turned off on the Outbox page: " + why, "--thread", THREAD)
    else: raise ValueError("do: reject or off")
    return {"ok": True, "said": CTL.sub("", said).strip()[:600]}
