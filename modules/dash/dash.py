#!/usr/bin/env python3
"""hive-dash: a read-only view of the Hive. Agents running, what each is doing, work underway.

Reads only: ~/run/*.json, ~/sessions/*.jsonl (tails), ~/log.jsonl, ~/board.jsonl,
~/.hive/{starter,usage}.json, ~/projects/*. Writes nothing, signals nothing, needs no core change.

  hive-dash                 text snapshot
  hive-dash --watch [S]     redraw every S seconds (default 5)
  hive-dash --html          one HTML page on stdout
  hive-dash serve [--port P] HTML on http://127.0.0.1:P/ (loopback only; default 8077)
  hive-dash --json          the snapshot as JSON
"""
import html, json, os, subprocess, sys, time
from datetime import datetime, timezone
from pathlib import Path

HOME = Path(os.environ.get("HIVE_HOME", Path.home()))
RUN, LOG, BOARD = HOME / "run", HOME / "log.jsonl", HOME / "board.jsonl"
STATE, PROJECTS = HOME / ".hive", HOME / "projects"
THREAD_HOURS = 12


def jload(p, d=None):
    try: return json.loads(Path(p).read_text())
    except Exception: return d


def jlines(p, tail_bytes=None):
    try:
        with open(p, "rb") as f:
            if tail_bytes:
                f.seek(0, 2); n = f.tell(); f.seek(max(0, n - tail_bytes))
                if n > tail_bytes: f.readline()  # drop partial line
            data = f.read()
    except OSError:
        return []
    out = []
    for ln in data.splitlines():
        try: out.append(json.loads(ln))
        except Exception: pass
    return out


def ts(s):
    try: return datetime.fromisoformat(str(s).replace("Z", "+00:00")).timestamp()
    except Exception: return None


def ago(t):
    if t is None: return "?"
    d = int(time.time() - t)
    if d < 0: return "now"
    if d < 90: return f"{d}s"
    if d < 5400: return f"{d // 60}m"
    if d < 172800: return f"{d / 3600:.1f}h"
    return f"{d // 86400}d"


def short(s, n):
    s = " ".join(str(s or "").split())
    return s if len(s) <= n else s[: n - 1] + "…"


def alive(pid):
    return bool(pid) and Path(f"/proc/{pid}").exists()


def describe_tool(name, inp):
    inp = inp or {}
    for k in ("command", "description", "file_path", "pattern", "prompt", "query", "url"):
        if inp.get(k):
            return f"{name}: {inp[k]}"
    return name


def activity(out):
    """Last thing the session did, from the tail of its stream-json output."""
    p = Path(out or "")
    res = {"last": None, "doing": "", "said": "", "turns": 0, "cost": None}
    if not p.exists(): return res
    res["last"] = p.stat().st_mtime
    for e in jlines(p, 256 * 1024):
        t = e.get("type")
        if t == "assistant":
            for c in e.get("message", {}).get("content", []) or []:
                if c.get("type") == "tool_use":
                    res["doing"] = describe_tool(c.get("name"), c.get("input"))
                elif c.get("type") == "text" and c.get("text", "").strip():
                    res["said"] = c["text"]
        elif t == "result":
            res["turns"] += 1
            if e.get("total_cost_usd") is not None: res["cost"] = e["total_cost_usd"]
    return res


def board():
    return jlines(BOARD)


def snapshot():
    posts = board()
    by_id = {p.get("id"): p for p in posts}
    agents = []
    for f in sorted(RUN.glob("*.json")):
        r = jload(f, {}) or {}
        a = activity(r.get("out"))
        woke = by_id.get(r.get("post"), {})
        agents.append({
            "name": r.get("name", f.stem), "pid": r.get("pid"), "alive": alive(r.get("pid")),
            "since": ts(r.get("at")), "thread": r.get("thread"), "from": r.get("from"),
            "task": woke.get("text", ""), "last": a["last"], "doing": a["doing"], "said": a["said"],
            "out": r.get("out"),
        })
    st = jload(STATE / "starter.json", {}) or {}
    pending = [{"name": n, "posts": len(v), "threads": sorted({x.get("thread") or "" for x in v})}
               for n, v in (st.get("pending") or {}).items()]
    usage = {k: v for k, v in (jload(STATE / "usage.json", {}) or {}).items() if not k.startswith("_")}
    limit_until = st.get("limit_until")
    if limit_until and limit_until < time.time(): limit_until = None

    cut = time.time() - THREAD_HOURS * 3600
    threads = {}
    for p in posts:
        t = ts(p.get("at"))
        th = p.get("thread") or "(none)"
        d = threads.setdefault(th, {"thread": th, "n": 0, "recent": 0, "last": None, "who": set()})
        d["n"] += 1
        if t and t >= cut: d["recent"] += 1
        d["who"].add(p.get("from") or "?")
        if t and (d["last"] is None or t >= d["last"]["t"]):
            d["last"] = {"t": t, "from": p.get("from"), "text": p.get("text", "")}
    running_threads = {a["thread"] for a in agents}
    active = [dict(d, who=sorted(d["who"])) for d in threads.values()
              if d["recent"] or d["thread"] in running_threads]
    active.sort(key=lambda d: -(d["last"]["t"] if d["last"] else 0))

    ended = [e for e in jlines(LOG, 128 * 1024) if e.get("event") != "start"][-10:][::-1]

    projects = []
    for d in sorted(PROJECTS.glob("*/")):
        head = ""
        for fn in ("goals.md", "README.md"):
            if (d / fn).exists():
                for ln in (d / fn).read_text(errors="replace").splitlines():
                    if ln.strip(): head = ln.strip("# ").strip(); break
                break
        commit = ""
        try:
            commit = subprocess.run(["git", "-C", str(d), "log", "-1", "--format=%cr · %s"],
                                    capture_output=True, text=True, timeout=5).stdout.strip()
        except Exception: pass
        projects.append({"name": d.name, "head": head, "commit": commit})

    return {"now": time.time(), "agents": agents, "pending": pending, "usage": usage,
            "limit_until": limit_until, "threads": active, "ended": ended, "projects": projects}


# ---------- text ----------

def text(s, width=None):
    w = width or 110
    L = []
    use = ", ".join(("?" if v is None else f"{v:.0f}%") for v in s["usage"].values()) or "?"
    lim = f"  HELD for usage limit until {datetime.fromtimestamp(s['limit_until']).strftime('%H:%M')}" if s["limit_until"] else ""
    L.append(f"Hive dashboard  {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}   weekly usage: {use}{lim}")
    L.append("")
    L.append(f"AGENTS RUNNING ({len(s['agents'])})")
    if not s["agents"]: L.append("  none")
    for a in s["agents"]:
        st = "" if a["alive"] else "  [pid gone]"
        L.append(f"  @{a['name']:<8} {ago(a['since']):>5} up, active {ago(a['last'])} ago   thread {a['thread']}  (from {a['from']}){st}")
        L.append(f"      task:  {short(a['task'], w - 13)}")
        if a["doing"]: L.append(f"      doing: {short(a['doing'], w - 13)}")
        if a["said"]: L.append(f"      said:  {short(a['said'], w - 13)}")
    if s["pending"]:
        L.append("")
        L.append("WAITING TO START")
        for p in s["pending"]:
            L.append(f"  @{p['name']:<8} {p['posts']} post(s) in {', '.join(p['threads'])}")
    L.append("")
    L.append(f"THREADS ACTIVE (last {THREAD_HOURS}h, or with an agent running)")
    for d in s["threads"]:
        lp = d["last"] or {}
        L.append(f"  {d['thread']:<22} {d['recent']:>3} new/{d['n']:<3} {ago(lp.get('t')):>5} ago  {lp.get('from')}: {short(lp.get('text'), w - 50)}")
    L.append("")
    L.append("RECENTLY ENDED")
    for e in s["ended"]:
        L.append(f"  {ago(ts(e.get('at'))):>5} ago  {e.get('event'):<8} @{e.get('name')}  exit {e.get('exit')}")
    if s["projects"]:
        L.append("")
        L.append("PROJECTS")
        for p in s["projects"]:
            L.append(f"  {p['name']:<18} {short(p['head'], 40):<40}  {short(p['commit'], w - 64)}")
    return "\n".join(L)


# ---------- html ----------

CSS = """
body{font:14px/1.45 system-ui,sans-serif;margin:1.5rem;color:#1d2330;background:#f6f7f9}
@media(prefers-color-scheme:dark){body{color:#dfe3ea;background:#14171c}.card{background:#1c2027!important;border-color:#2a303a!important}.dim{color:#8b93a1!important}td,th{border-color:#2a303a!important}}
h1{font-size:1.2rem;margin:0 0 .2rem}h2{font-size:.95rem;text-transform:uppercase;letter-spacing:.05em;margin:1.6rem 0 .5rem}
.dim{color:#667085}.card{background:#fff;border:1px solid #e3e6eb;border-radius:8px;padding:.7rem .9rem;margin:.5rem 0}
.name{font-weight:600}.mono{font-family:ui-monospace,monospace;font-size:12.5px;white-space:pre-wrap;word-break:break-word}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.3rem .5rem;border-bottom:1px solid #e3e6eb;vertical-align:top}
.dot{display:inline-block;width:.6rem;height:.6rem;border-radius:50%;background:#2f9e5b;margin-right:.4rem}.dot.off{background:#c0392b}
"""


def page(s, refresh=10):
    e = html.escape
    use = ", ".join(("?" if v is None else f"{v:.0f}%") for v in s["usage"].values()) or "?"
    lim = f" · <b>starts held for usage limit until {datetime.fromtimestamp(s['limit_until']).strftime('%H:%M')}</b>" if s["limit_until"] else ""
    h = [f"<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width'>"
         f"<meta http-equiv=refresh content={refresh}><title>Hive · {len(s['agents'])} running</title><style>{CSS}</style>",
         f"<h1>Hive</h1><div class=dim>{datetime.now().strftime('%Y-%m-%d %H:%M:%S')} · weekly usage {e(use)}{lim} · read-only, refreshes every {refresh}s</div>",
         f"<h2>Agents running ({len(s['agents'])})</h2>"]
    if not s["agents"]: h.append("<div class=dim>none</div>")
    for a in s["agents"]:
        h.append(f"<div class=card><span class='dot{'' if a['alive'] else ' off'}'></span><span class=name>{e(a['name'])}</span>"
                 f" <span class=dim>· thread <b>{e(str(a['thread']))}</b> · from {e(str(a['from']))} · up {ago(a['since'])} · active {ago(a['last'])} ago"
                 f"{'' if a['alive'] else ' · pid gone'}</span>"
                 f"<div><span class=dim>task</span> {e(short(a['task'], 400))}</div>"
                 + (f"<div class=mono><span class=dim>doing</span> {e(short(a['doing'], 300))}</div>" if a["doing"] else "")
                 + (f"<div><span class=dim>said</span> {e(short(a['said'], 400))}</div>" if a["said"] else "") + "</div>")
    if s["pending"]:
        h.append("<h2>Waiting to start</h2><table>")
        for p in s["pending"]:
            h.append(f"<tr><td class=name>{e(p['name'])}</td><td>{p['posts']} post(s)</td><td>{e(', '.join(p['threads']))}</td></tr>")
        h.append("</table>")
    h.append(f"<h2>Threads active</h2><table><tr><th>thread</th><th>new / all</th><th>last</th><th>latest post</th></tr>")
    for d in s["threads"]:
        lp = d["last"] or {}
        h.append(f"<tr><td class=name>{e(d['thread'])}</td><td>{d['recent']} / {d['n']}</td><td>{ago(lp.get('t'))}</td>"
                 f"<td><b>{e(str(lp.get('from')))}</b>: {e(short(lp.get('text'), 300))}</td></tr>")
    h.append("</table><h2>Recently ended</h2><table>")
    for x in s["ended"]:
        h.append(f"<tr><td>{ago(ts(x.get('at')))} ago</td><td>{e(str(x.get('event')))}</td><td class=name>{e(str(x.get('name')))}</td><td class=dim>exit {e(str(x.get('exit')))}</td></tr>")
    h.append("</table>")
    if s["projects"]:
        h.append("<h2>Projects</h2><table>")
        for p in s["projects"]:
            h.append(f"<tr><td class=name>{e(p['name'])}</td><td>{e(p['head'])}</td><td class=dim>{e(p['commit'])}</td></tr>")
        h.append("</table>")
    return "".join(h)


def serve(port):
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class H(BaseHTTPRequestHandler):
        def do_GET(self):
            s = snapshot()
            if self.path.startswith("/json"):
                body, ct = json.dumps(s, default=str).encode(), "application/json"
            elif self.path in ("/", "/index.html"):
                body, ct = page(s).encode(), "text/html; charset=utf-8"
            else:
                self.send_error(404); return
            self.send_response(200)
            self.send_header("Content-Type", ct); self.send_header("Content-Length", str(len(body)))
            self.end_headers(); self.wfile.write(body)

        def log_message(self, *a): pass

    srv = ThreadingHTTPServer(("127.0.0.1", port), H)  # loopback only: never opens the machine
    print(f"hive-dash on http://127.0.0.1:{port}/  (from elsewhere: ssh -L {port}:localhost:{port} <this-machine>)", flush=True)
    srv.serve_forever()


def main(argv):
    if "-h" in argv or "--help" in argv: print(__doc__); return
    if argv and argv[0] == "serve":
        port = int(argv[argv.index("--port") + 1]) if "--port" in argv else 8077
        return serve(port)
    if "--json" in argv: print(json.dumps(snapshot(), default=str, indent=1)); return
    if "--html" in argv: print(page(snapshot())); return
    if "--watch" in argv:
        i = argv.index("--watch")
        n = float(argv[i + 1]) if len(argv) > i + 1 else 5
        try:
            while True:
                w = os.get_terminal_size().columns if sys.stdout.isatty() else 110
                sys.stdout.write("\033[H\033[2J" + text(snapshot(), w) + "\n"); sys.stdout.flush()
                time.sleep(n)
        except KeyboardInterrupt: return
    w = os.get_terminal_size().columns if sys.stdout.isatty() else 110
    print(text(snapshot(), w))


if __name__ == "__main__":
    main(sys.argv[1:])
