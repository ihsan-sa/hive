"""Keeper step 4's short checks in one call, no model cost: `python3 -I ~/core/keeper_checks.py [--since ISO]`.

Keeper rounds on Sonnet run about a minute, and from 11-18 21:42 to 11-19 15:04 every round but two wrote the same
"Not done: no lint fixes, no outcomes check, no unwatched-complaint search ... Next keeper: those" and the next round
skipped them again (thread improve, a5122). Each of these was a few hand-run commands; here they are one, so the round
can name each and act on what it lists.

Prints, since the last keeper result post (or 3 h back):
  watchdog: age of ~/.hive/watchdog.json and the posts in thread watchdog
  noticed: the person's posts that read as finding a problem first (progress.NOTICED), each with id and thread
  runs: runs graded silent or nothing, each with name, thread and why
  lint: the `hive summary lint` count line
"""
import datetime as dt, json, re, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
NOTICED = re.compile(r"\b(?:hello\?|status\?|not seeing|noticed|isn.t working|broken|nothing happen|why am i not|still (?:not|no|isn|doesn|waiting|nothing|broken|missing)\b|why (?:is|are|does|do)\b[^?.!\n]{0,60}?\b(?:not|no|isn.t|doesn.t|don.t|only|gap|wrong|missing)\b)", re.I)
try:  # the one definition, when progress.py imports cleanly; the copy above is its 11-19 text
    sys.path.insert(0, str(HOME / "core")); from progress import NOTICED  # noqa: F811
except Exception: pass

def rows(path):
    try:
        for line in open(path):
            try: yield json.loads(line)
            except ValueError: pass
    except OSError: return

def clip(s, n): s = " ".join(str(s).split()); return s if len(s) <= n else s[:n - 1] + "…"

def since_default(posts, now):
    """The last keeper result post (an agent's, not the starter's brief), else 3 h back."""
    last = [p["at"] for p in posts if p.get("thread") == "keeper" and p.get("from") not in ("starter", "person") and p.get("at")]
    return max(last) if last else dt.datetime.fromtimestamp(now - 3 * 3600, dt.timezone.utc).isoformat(timespec="seconds")

def report(posts, outcomes, watchdog, since, now, lint_line=""):
    out = []
    at = (watchdog or {}).get("at")
    age = f"{(now - at) / 60:.0f} min old" + (" (over 10: the watchdog is not running)" if now - at > 600 else "") if isinstance(at, (int, float)) else "no `at` (the watchdog is not running)"
    wd = [p for p in posts if p.get("thread") == "watchdog" and p.get("at", "") > since]
    out.append(f"watchdog: watchdog.json {age}; {len(wd)} posts in thread watchdog since {since[:16]}")
    for p in wd[-5:]: out.append(f"  {p['id']} {p.get('from')}: {clip(p.get('text', ''), 110)}")
    hits = [p for p in posts if p.get("from") == "person" and p.get("at", "") > since and NOTICED.search(str(p.get("text", "")))]
    out.append(f"noticed: {len(hits)} person posts read as finding a problem first; each real one is a missing watchdog check")
    for p in hits: out.append(f"  {p['id']} ({p.get('thread')}): {clip(p.get('text', ''), 110)}")
    bad = [r for r in outcomes if r.get("grade") in ("silent", "nothing") and str(r.get("started", "")) > since]
    out.append(f"runs: {len(bad)} graded silent or nothing since {since[:16]} (a person's one is an ask with no answer: check `hive asks list` has it and someone owns it)")
    for r in bad: out.append(f"  {r.get('name')} {r.get('grade')} ({r.get('thread')}, from {r.get('from')}): {clip(r.get('why', ''), 100)}")
    if lint_line: out.append(lint_line)
    return out

def main(argv):
    now = time.time()
    posts = list(rows(HOME / "board.jsonl"))
    since = argv[argv.index("--since") + 1] if "--since" in argv else since_default(posts, now)
    try: watchdog = json.load(open(HOME / ".hive" / "watchdog.json"))
    except (OSError, ValueError): watchdog = {}
    try:
        lint = subprocess.run(["hive", "summary", "lint"], capture_output=True, text=True, timeout=60).stdout.strip().splitlines()
        lint_line = "lint: " + (lint[-1].removeprefix("lint: ") if lint else "no output") + " (`hive summary lint` lists each)"
    except Exception as e: lint_line = f"lint: couldn't run: {clip(e, 80)}"
    print("\n".join(report(posts, rows(HOME / "outcomes.jsonl"), watchdog, since, now, lint_line)))

if __name__ == "__main__": main(sys.argv[1:])
