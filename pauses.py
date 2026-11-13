"""When the hive was paused: ~/.hive/pauses.jsonl, append-only, no model.

A pause is every Claude login paused at once (~/.hive/paused.json covers all of config.json's logins): the person's way of
stopping the hive's work. Lines are either events {"at", "paused": true|false, "why", "post", "by"}, written by `note` when
that all-paused state flips (hive login pause|resume, the dashboard's Pause), or whole spans {"from", "to", "why", "post"}
written by hand for the time before this record existed. A check whose window a pause covers reads the pause, not the
change: progress.autojudge calls such a reading inconclusive, and hive progress and the RSI lab index flag those windows."""
import datetime as dt, json, os
from pathlib import Path

PAUSES = Path(os.environ.get("HIVE_PAUSES") or Path.home() / ".hive" / "pauses.jsonl")


def _t(s):
    try:
        t = dt.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
        return t if t.tzinfo else t.replace(tzinfo=dt.timezone.utc)
    except ValueError: return None


def _rows(path=None):
    try: lines = Path(path or PAUSES).read_text().splitlines()
    except OSError: return []
    out = []
    for l in lines:
        try: r = json.loads(l)
        except ValueError: continue
        if isinstance(r, dict): out.append(r)
    return out


def spans(path=None, until=None):
    """[(from, to, why, post)] as aware datetimes, sorted. An open pause runs to `until` (default now)."""
    until, out, start = until or dt.datetime.now(dt.timezone.utc), [], None
    rows = _rows(path)
    for r in sorted((r for r in rows if "at" in r and _t(r["at"])), key=lambda r: _t(r["at"])):
        if r.get("paused") and start is None: start = r
        elif not r.get("paused") and start is not None:
            out.append((_t(start["at"]), _t(r["at"]), start.get("why", ""), start.get("post"))); start = None
    if start is not None: out.append((_t(start["at"]), until, start.get("why", ""), start.get("post")))
    out += [(_t(r["from"]), _t(r.get("to")) or until, r.get("why", ""), r.get("post")) for r in rows if "from" in r and _t(r["from"])]
    return sorted(out)


def share(a, b, path=None, sp=None):
    """The fraction of the window [a, b] that a pause covers, 0..1 (overlapping spans counted once)."""
    a, b = (_t(a) if isinstance(a, str) else a), (_t(b) if isinstance(b, str) else b)
    if not a or not b or b <= a: return 0.0
    covered, edge = 0.0, a
    for f, t, *_ in (spans(path) if sp is None else sp):
        f, t = max(f, edge), min(t, b)
        if t > f: covered += (t - f).total_seconds(); edge = t
    return covered / (b - a).total_seconds()


def paused_now(path=None):
    rows = [r for r in _rows(path) if "at" in r and _t(r["at"])]
    return bool(rows) and bool(max(rows, key=lambda r: _t(r["at"])).get("paused"))


def note(all_paused, why="", post=None, by=None, path=None):
    """Append an event when the all-logins-paused state flips; nothing when it doesn't. Never raises: a pause must not fail."""
    try:
        if bool(all_paused) == paused_now(path): return False
        p = Path(path or PAUSES); p.parent.mkdir(exist_ok=True)
        r = {"at": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"), "paused": bool(all_paused), "why": why, "post": post,
             "by": by or os.environ.get("HIVE_NAME", "")}
        with p.open("a") as f: f.write(json.dumps(r) + "\n")
        return True
    except OSError: return False
