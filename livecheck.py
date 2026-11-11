"""After an edit, say at once if a file the live web app serves no longer parses.

The dash serves modules/dash/web straight from disk, so a syntax error in one view blanks the person's page ("Reading…") until
someone notices. It happened on 11-04 (dash.py), 11-08 and 11-11 (comms.js, about 25 minutes from 06:55Z, found by a reader test). inbox.py
calls check(payload) after every tool call. It looks only at files the call could have written: an Edit/Write path, or, for Bash,
a dash file changed in the last WINDOW seconds whose name is in the command. node checks JS as a module (stdin, so `.js` isn't
read as CommonJS), Python is compiled without writing bytecode. Cheap (no files, no node) on every other call.
"""
import os, re, subprocess, time
from pathlib import Path

DASH = Path.home() / "modules/dash"
WINDOW = 120


def _served(p):
    try: p = Path(p).resolve()
    except (OSError, ValueError): return False
    try: rel = p.relative_to(DASH.resolve())
    except ValueError: return False
    if rel.parts[:1] == ("web",): return p.suffix in (".js", ".mjs") and "test" not in rel.parts and "node_modules" not in rel.parts
    return len(rel.parts) == 1 and p.suffix == ".py"


def _recent(cmd):
    """Dash files changed in the last WINDOW seconds whose name the Bash command mentions."""
    if "dash" not in cmd and ".js" not in cmd and ".py" not in cmd: return []
    now, out = time.time(), []
    roots = [(DASH / "web", ("*.js", "*.mjs")), (DASH, ("*.py",))]
    for root, pats in roots:
        for pat in pats:
            for f in (root.rglob(pat) if root.name == "web" else root.glob(pat)):
                try:
                    if now - f.stat().st_mtime < WINDOW and f.name in cmd and _served(f): out.append(f)
                except OSError: pass
    return out


def _err(f):
    try:
        if f.suffix == ".py":
            r = subprocess.run(["python3", "-I", "-c", "import sys;compile(open(sys.argv[1]).read(),sys.argv[1],'exec')", str(f)],
                               capture_output=True, text=True, timeout=5)
        else:
            with open(f, "rb") as fh:
                r = subprocess.run(["node", "--input-type=module", "--check"], stdin=fh, capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.TimeoutExpired): return None
    if r.returncode == 0: return None
    lines = [l for l in (r.stderr or "").splitlines() if l.strip()]
    where = next((m.group(1) for l in lines if (m := re.search(r"(?:\[stdin\]|line )[: ]?(\d+)", l))), "?")
    what = next((l.strip() for l in lines if re.search(r"Error:", l)), lines[-1].strip() if lines else "does not parse")
    return f"line {where}: {what[:160]}"


def check(payload):
    tool, ti = payload.get("tool_name", ""), payload.get("tool_input") or {}
    if tool in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
        fs = [Path(ti.get("file_path", ""))] if ti.get("file_path") and _served(ti.get("file_path")) else []
    elif tool == "Bash": fs = _recent(str(ti.get("command", "")))
    else: return None
    bad = [(f, e) for f in fs if (e := _err(f))]
    if not bad: return None
    rows = "\n".join(f"- {f.relative_to(Path.home())} {e}" for f, e in bad)
    return ("The live web app serves this file straight from disk and it no longer parses, so the person's page can be blank "
            f"right now:\n{rows}\nFix it before anything else (or put the last good copy back), then carry on.")
