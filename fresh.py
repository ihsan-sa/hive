#!/usr/bin/env python3
"""hive fresh: the freshness table. One row per downstream artifact with its source(s); a row fails when a source changed after the artifact did,
when a file is missing, or when a docs file is in no row (so the table cannot quietly fall behind the hive). Time of a file in ~/core is its last
commit (its mtime if uncommitted edits sit on it); outside ~/core (kb, modules, projects: the ~ repo is an auto backup) it is the mtime; a directory
is its newest file. The keeper starts a sonnet run per stale row, in the row's thread, with brief(row): the source's diff since the artifact's date.
Usage: hive fresh [--json] [--brief]   exit 1 when any row fails."""
import json, os, subprocess, sys, time
from pathlib import Path
H = Path(os.environ.get("HIVE_HOME", Path.home())); CORE = Path(os.environ.get("HIVE_CORE", H / "core"))

# artifact -> (sources, thread). Paths from home. kind "cmd" rows run a command; "watch" reads ui-design-watch state.
ROWS = [
    ("core/dist/orientation.md", ["core/orientation.md"], "core-docs"),
    ("kb/hive.md", ["core/orientation.md"], "core-docs"),
    ("core/docs/tutorials/09-how-it-works.md", ["core/orientation.md"], "core-docs"),
    ("core/dist/repo/CHANGELOG.md", ["@release-notes"], "core-docs"),
    ("core/docs/guide.md", ["modules/dash/web/js/core/nav.js", "modules/dash/web/js/ui/tour.js"], "ui-docs"),
    ("core/docs/README.md", ["modules/dash/web/js/core/nav.js", "modules/dash/web/js/ui/tour.js"], "ui-docs"),
    ("modules/dash/reel", ["modules/dash/web/js/core/nav.js", "modules/dash/web/js/ui/tour.js"], "ui-docs"),
    ("@docs-fresh-test", ["modules/dash/web/test/test_docs_fresh.mjs"], "ui-docs"),
    ("core/docs/install.md", ["core/dist/install.sh"], "core-docs"),
    ("@ui-design-watch", ["projects/ui-redesign/human-workflow.md"], "ui-redesign"),
]
COVERED_DIRS = ["core/docs"]   # every .md here must be an artifact or source above
# No row for docs.hivelab.ca: dist/site/build.sh copies core/docs at each release, and release.sh runs
# dist/site/docs_release_check.py against live after deploying (a row on projects/hivelab-site went stale on
# every core/docs commit with nothing to update there; removed 24 Nov, i-log in thread improve).

def p(rel): return H / rel
def _git(*a, cwd=CORE):
    r = subprocess.run(["git", "-C", str(cwd), *a], capture_output=True, text=True); return r.stdout.strip() if r.returncode == 0 else ""
def mtime(path):
    path = Path(path)
    if path.is_dir(): return max((f.stat().st_mtime for f in path.rglob("*") if f.is_file() and "__pycache__" not in f.parts and ".git" not in f.parts), default=0)
    return path.stat().st_mtime if path.exists() else 0
def when(rel):
    """Last change of a path from home: commit time inside ~/core (mtime when dirty), else mtime. 0 = missing."""
    f = p(rel)
    if not f.exists(): return 0
    try: inner = f.resolve().relative_to(CORE.resolve())
    except ValueError: return mtime(f)
    ct = _git("log", "-1", "--format=%ct", "--", str(inner))
    if not ct or _git("status", "--porcelain", "--", str(inner)): return mtime(f)
    return float(ct)
def release_note_time():
    ct = _git("log", "-1", "--format=%ct", "--grep=^Release-note:")
    return float(ct) if ct else 0
def src_time(s): return release_note_time() if s == "@release-notes" else when(s)
def iso(t): return time.strftime("%m-%d %H:%M", time.gmtime(t)) if t else "missing"

def diff_since(rel, t0):
    """The source's diff since the artifact's date: git inside ~/core, else the note that only mtime is known."""
    f = p(rel)
    try: inner = f.resolve().relative_to(CORE.resolve())
    except ValueError: return f"(no history for {rel}: changed {iso(when(rel))}; read the file)"
    base = _git("rev-list", "-1", f"--before={int(t0)}", "HEAD")
    d = _git("diff", base, "--", str(inner)) if base else _git("log", "-p", "--", str(inner))
    return d[:6000] or f"(no committed diff for {rel} since {iso(t0)}; uncommitted edits? see git status)"

def check(rows=None, coverage=True):
    out = []
    for art, srcs, thread in rows or ROWS:
        row = dict(artifact=art, sources=srcs, thread=thread, ok=True, why="")
        if art == "@docs-fresh-test":
            r = subprocess.run(["node", str(p(srcs[0]))], capture_output=True, text=True, cwd=str(p("modules/dash/web")))
            row["ok"] = r.returncode == 0; row["why"] = "" if row["ok"] else "test_docs_fresh.mjs: " + " | ".join(l for l in r.stdout.splitlines() if l.startswith("FAIL"))[:300]
            out.append(row); continue
        if art == "@ui-design-watch":
            try: last = json.load(open(H / ".hive/ui-design-watch.json")).get("last_run", 0)
            except (OSError, ValueError): last = 0
            row["art_t"] = last
        else:
            row["art_t"] = when(art)
            if not row["art_t"]: row.update(ok=False, why=f"artifact {art} is missing")
        for s in srcs:
            st = src_time(s)
            if not st: row.update(ok=False, why=row["why"] or f"source {s} is missing")
            elif row["art_t"] and st > row["art_t"] and row["ok"]:
                row.update(ok=False, why=f"source {s} changed {iso(st)}, after {art} ({iso(row['art_t'])})", stale_source=s)
        out.append(row)
    if coverage:
        listed = {a for a, _, _ in (rows or ROWS)} | {s for _, ss, _ in (rows or ROWS) for s in ss}
        for d in COVERED_DIRS:
            for f in sorted(p(d).glob("*.md")):
                rel = str(f.relative_to(H))
                if rel not in listed:
                    out.append(dict(artifact=rel, sources=[], thread="core-docs", ok=False, why=f"{rel} is in no row of the freshness table (core/fresh.py ROWS)"))
    return out

def brief(row):
    """The keeper's brief for a stale row: names the artifact and carries the source's diff since the artifact's date."""
    s = row.get("stale_source") or (row["sources"][0] if row["sources"] else "")
    t = row.get("art_t") or 0
    body = diff_since(s, t) if s and not s.startswith("@") else ("commits with a Release-note: line:\n" + _git("log", f"--since={int(t)}", "--grep=^Release-note:", "--format=%h %s%n%b") if s else "")
    return (f"Refresh {row['artifact']}: {row['why']}\n\nSource {s}, diff since the artifact's date ({iso(t)}):\n\n{body}\n\n"
            f"Update the artifact to match the source change only (not a rewrite), then re-run `hive fresh` and post the row passing in this thread.")

def stale_count():
    try: return sum(not r["ok"] for r in check())
    except Exception: return None

def main(argv):
    rows = check()
    if "--json" in argv: print(json.dumps([{**r, "brief": brief(r)} if not r["ok"] else r for r in rows], indent=1))
    else:
        for r in rows: print(("ok    " if r["ok"] else "STALE ") + r["artifact"] + ("" if r["ok"] else "  <- " + r["why"]))
        if "--brief" in argv:
            for r in rows:
                if not r["ok"]: print("\n--- brief for thread " + r["thread"] + " ---\n" + brief(r))
    return 1 if any(not r["ok"] for r in rows) else 0

if __name__ == "__main__": sys.exit(main(sys.argv[1:]))
