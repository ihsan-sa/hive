#!/usr/bin/env python3
"""hive-cost: what the Hive's model use would have cost at API list prices, per project and board.

Read-only. Reads the transcripts Claude Code already writes
(<login>/projects/*/<session>.jsonl and their subagents/) plus ~/log.jsonl
(which agent/thread each session run was). Nothing is added to agents.

Attribution of one model call (one assistant message):
  project/board  from the call's cwd: ~/projects/<p>[-<agent>...]/boards/<b>/...
                 (worktrees like proj-a73b6 fold into proj), ~/modules/<m> -> module:<m>.
                 If cwd is ~ (agent working by absolute paths), the session's last
                 known project/board is carried forward; before any, it's "(home)".
                 If cwd gives a project but no board, the board is taken from the git
                 branch when it names one of the project's boards (hive/bms-4s-route).
  agent/thread   the starter run (log.jsonl 'start') the call falls in.
Prices are a table below (per MTok), checked against the CLI's own costUSD.
"""
import argparse, collections, datetime as dt, glob, json, os, re, sys

HOME = os.path.expanduser("~")
PROJ = os.path.join(HOME, "projects")

# $/MTok: input, output, cache read. Cache writes: 5m = 1.25x input, 1h = 2x input.
PRICES = {
    "claude-fable-5-2": (8, 40, 0.20), "claude-mythos-5-2": (8, 40, 0.20),
    "claude-fable-5-1": (10, 50, 0.25), "claude-mythos-5-1": (10, 50, 0.25),
    "claude-fable-5": (10, 50, 1.0),
    "claude-opus-5-5": (4, 20, 0.20), "claude-opus-5": (5, 25, 0.50),
    "claude-opus-4-8": (5, 25, 0.50), "claude-opus-4-7": (5, 25, 0.50), "claude-opus-4-6": (5, 25, 0.50),
    "claude-sonnet-5-5": (2, 10, 0.20), "claude-sonnet-5": (2, 10, 0.20), "claude-sonnet-4-6": (3, 15, 0.30),
    "claude-haiku-5-5": (0.10, 0.50, 0.01), "claude-haiku-4-5": (1, 5, 0.10),
}
WEB_SEARCH = 10 / 1000  # $ per request
unknown_models = collections.Counter()


def price(model, u):
    if model == "<synthetic>" or not model:
        return 0.0
    base = re.sub(r"-\d{8}$", "", model)
    p = PRICES.get(base)
    if p is None:
        unknown_models[model] += 1
        p = PRICES["claude-opus-5-5"]
    pin, pout, pread = p
    cc = u.get("cache_creation") or {}
    w1 = cc.get("ephemeral_1h_input_tokens", 0) or 0
    w5 = cc.get("ephemeral_5m_input_tokens", 0) or 0
    if not cc:
        w5 = u.get("cache_creation_input_tokens", 0) or 0
    inp, out, rd = u.get("input_tokens", 0) or 0, u.get("output_tokens", 0) or 0, u.get("cache_read_input_tokens", 0) or 0
    if base == "claude-haiku-5-5" and inp + w1 + w5 + rd > 100_000:
        pin, pout, pread = 0.50, 2.50, 0.05
    mult = 2.0 if u.get("speed") == "fast" else 1.0
    usd = (inp * pin + out * pout + w5 * pin * 1.25 + w1 * pin * 2 + rd * pread) / 1e6 * mult
    stu = u.get("server_tool_use") or {}
    return usd + (stu.get("web_search_requests", 0) or 0) * WEB_SEARCH


def tokens_of(u):
    """[input, output, cache read, cache write] of one call."""
    cc = u.get("cache_creation") or {}
    write = (cc.get("ephemeral_1h_input_tokens", 0) or 0) + (cc.get("ephemeral_5m_input_tokens", 0) or 0) if cc else u.get("cache_creation_input_tokens", 0) or 0
    return [u.get("input_tokens", 0) or 0, u.get("output_tokens", 0) or 0, u.get("cache_read_input_tokens", 0) or 0, write]


_boards = {}
def boards_of(project):
    if project not in _boards:
        d = os.path.join(PROJ, project, "boards")
        _boards[project] = sorted(os.listdir(d), key=len, reverse=True) if os.path.isdir(d) else []
    return _boards[project]


_bases = {}
def base_project(name):
    """Fold worktrees and per-agent clones into their project:
    a git worktree -> the repo it belongs to;
    a clone named <base>-a<4 hex>... -> <base>."""
    if name in _bases:
        return _bases[name]
    out = name
    try:
        import subprocess
        cd = subprocess.run(["git", "-C", os.path.join(PROJ, name), "rev-parse", "--path-format=absolute", "--git-common-dir"],
                            capture_output=True, text=True, timeout=5).stdout.strip()
        if cd.endswith("/.git") and os.path.dirname(os.path.dirname(cd)) == PROJ:
            out = os.path.basename(os.path.dirname(cd))
    except Exception:
        pass
    m = re.match(r"^(.*?)-a[0-9a-f]{4}(?:[-a-z0-9]*)?$", out)
    if m and os.path.isdir(os.path.join(PROJ, m.group(1))):
        out = m.group(1)
    _bases[name] = out
    return out


def _norm(x):
    return re.sub(r"[^a-z0-9]", "", x.lower())


def board_from_thread(project, thread):
    """hwlab-bms4s -> bms-4s when that's one of the project's boards."""
    t = _norm(thread.split("-", 1)[1] if "-" in thread else thread)
    for b in boards_of(project):
        if _norm(b) == t:
            return b
    return None


def place(cwd, branch):
    """-> (project, board) or None when cwd says nothing (home, /tmp...)."""
    if not cwd:
        return None
    if cwd.startswith(PROJ + "/"):
        parts = cwd[len(PROJ) + 1:].split("/")
        proj = base_project(parts[0])
        board = parts[2] if len(parts) > 2 and parts[1] == "boards" else None
        if board is None and branch:
            for b in boards_of(proj):
                if b in branch:
                    board = b
                    break
        return proj, board
    mod = os.path.join(HOME, "modules") + "/"
    if cwd.startswith(mod):
        return "module:" + cwd[len(mod):].split("/")[0], None
    return None


def load_runs():
    """session -> sorted [(start_ts, name, thread)] from ~/log.jsonl."""
    runs = collections.defaultdict(list)
    try:
        for line in open(os.path.join(HOME, "log.jsonl")):
            try:
                e = json.loads(line)
            except ValueError:
                continue
            if e.get("event") == "start" and e.get("session"):
                runs[e["session"]].append((e["at"][:19], e.get("name", "?"), e.get("thread") or "-"))
    except FileNotFoundError:
        pass
    for v in runs.values():
        v.sort()
    return runs


def logins():
    try:
        cfg = json.load(open(os.path.join(HOME, "core", "config.json")))
        return [os.path.expanduser(l) for l in cfg.get("logins", ["~/.claude"])]
    except Exception:
        return [os.path.join(HOME, ".claude")]


def calls(since=None):
    """Yield dict per model call, deduped by message id, in session order."""
    runs = load_runs()
    for login in logins():
        for main in glob.glob(os.path.join(login, "projects", "*", "*.jsonl")):
            session = os.path.basename(main)[:-6]
            files = [main] + sorted(glob.glob(os.path.join(main[:-6], "subagents", "*.jsonl")))
            if since and max(os.path.getmtime(f) for f in files) < since.timestamp():
                continue
            msgs = {}  # id -> record (last write wins; usage is final on every copy)
            for f in files:
                try:
                    fh = open(f, errors="replace")
                except OSError:
                    continue
                with fh:
                    for line in fh:
                        if '"assistant"' not in line or '"usage"' not in line:
                            continue
                        try:
                            d = json.loads(line)
                        except ValueError:
                            continue
                        if d.get("type") != "assistant":
                            continue
                        m = d.get("message") or {}
                        if not m.get("usage"):
                            continue
                        msgs[m.get("id") or d.get("uuid")] = (d.get("timestamp") or "", m.get("model"), m["usage"], d.get("cwd"), d.get("gitBranch"), f != main)
            rs = runs.get(session, [])
            last, lastboard = None, {}
            for ts, model, u, cwd, branch, sub in sorted(msgs.values(), key=lambda r: r[0]):
                name, thread = "(not started by hive)", "-"
                for at, n, t in rs:
                    if at <= ts[:19]:
                        name, thread = n, t
                    else:
                        break
                pl = place(cwd, branch) or last
                if pl and pl[1] is None and not pl[0].startswith("module:"):
                    b = board_from_thread(pl[0], thread) or lastboard.get(pl[0])
                    pl = (pl[0], b)
                if pl:
                    last = pl
                    if pl[1]:
                        lastboard[pl[0]] = pl[1]
                else:
                    pl = ("(home)", "thread " + thread)
                if since and ts and ts[:19] < since.strftime("%Y-%m-%dT%H:%M:%S"):  # transcripts are UTC
                    continue
                yield {"ts": ts, "session": session, "model": model, "usd": price(model, u),
                       "project": pl[0], "board": pl[1], "agent": name, "thread": thread, "sub": sub,
                       "out": u.get("output_tokens", 0) or 0, "tokens": tokens_of(u)}


def local_day(ts):
    try:
        return dt.datetime.fromisoformat(ts.replace("Z", "+00:00")).astimezone().strftime("%Y-%m-%d")
    except ValueError:
        return ts[:10]


def parse_since(s):
    if not s:
        return None
    m = re.fullmatch(r"(\d+)([hdw])", s)
    if m:
        n, k = int(m.group(1)), m.group(2)
        return dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=n * {"h": 1, "d": 24, "w": 168}[k])
    d = dt.datetime.fromisoformat(s)  # local time, like the board, unless it carries an offset
    return (d if d.tzinfo else d.astimezone()).astimezone(dt.timezone.utc)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--since", help="24h, 7d, 2w or an ISO date/time (local, like the board)")
    ap.add_argument("--by", default="board", choices=["board", "project", "thread", "agent", "day", "model"])
    ap.add_argument("--project", help="only this project")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    since = parse_since(a.since)
    tot = collections.defaultdict(lambda: [0.0, 0, 0])  # usd, calls, sessions(set later)
    sess = collections.defaultdict(set)
    toks = collections.defaultdict(lambda: [0, 0, 0, 0])
    models = collections.defaultdict(collections.Counter)  # key -> model -> usd
    sub_usd = collections.Counter()
    grand = 0.0
    for c in calls(since):
        if a.project and c["project"] != a.project:
            continue
        key = {"board": (c["project"], c["board"] or "(project-wide)"), "project": (c["project"],),
               "thread": (c["thread"],), "agent": (c["agent"],), "day": (local_day(c["ts"]),),
               "model": (c["model"] or "?",)}[a.by]
        t = tot[key]
        t[0] += c["usd"]; t[1] += 1
        sess[key].add(c["session"])
        grand += c["usd"]
        for i, n in enumerate(c["tokens"]):
            toks[key][i] += n
        models[key][c["model"] or "?"] += c["usd"]
        if c["sub"]:
            sub_usd[key] += c["usd"]
    rows = sorted(tot.items(), key=lambda kv: (-kv[1][0]) if a.by != "day" else kv[0])
    if a.by == "board":  # group boards under their project, projects by total
        ptot = collections.Counter()
        for k, v in tot.items():
            ptot[k[0]] += v[0]
        rows = sorted(tot.items(), key=lambda kv: (-ptot[kv[0][0]], kv[0][0], -kv[1][0]))
    if a.json:
        print(json.dumps({"since": a.since, "by": a.by, "total_usd": round(grand, 2),
                          "rows": [{"key": list(k), "usd": round(v[0], 4), "calls": v[1], "sessions": len(sess[k]),
                                   "tokens": toks[k], "models": {m: round(u, 4) for m, u in models[k].items()}, "sub_usd": round(sub_usd[k], 4)} for k, v in rows],
                          "unknown_models": dict(unknown_models)}, indent=1))
        return
    print(f"API-list-price cost of Hive model use{' since ' + a.since if a.since else ''} (by {a.by}); Hive is on a plan, nobody is billed this.")
    lastp = None
    for k, v in rows:
        if a.by == "board":
            if k[0] != lastp:
                lastp = k[0]
                print(f"\n{k[0]:<38} ${ptot[k[0]]:>9.2f}")
            print(f"  {k[1]:<36} ${v[0]:>9.2f}  {v[1]:>6} calls  {len(sess[k]):>3} sessions")
        else:
            print(f"{' / '.join(k):<40} ${v[0]:>9.2f}  {v[1]:>6} calls  {len(sess[k]):>3} sessions")
    print(f"\n{'total':<40} ${grand:>9.2f}")
    if unknown_models:
        print("unpriced models (charged at opus-5-5 rates):", dict(unknown_models), file=sys.stderr)


if __name__ == "__main__":
    main()
