#!/usr/bin/env python3
"""skills: learned skills, the skill-learning loop (projects/hive-meta/moving.md section 8).

A run that solved a step which keeps failing across conversations files the method as a skill, so the next run on that step
loads it instead of finding the way again. A learned skill is ~/kb/skills/NAME/SKILL.md (git-tracked by the ~ backup), linked
into the first login's skills folder as learned-NAME, which every later login shares (core/starter.py, core/sandbox.py).

Skills in the description list alone almost never load (about 36 `Skill` loads in about 11,700 transcripts over 7 days, 2026-11-18,
none of them learned), so each skill names a `trigger:` regex over a Bash command, and core/inbox.py points the run at the skill
the first time in a run a command matches. That pointer is behind config.json "skill_pointer" (off until the stop judge
has had a week of live days, so the two effects are not measured in the same week).

  hive skill file NAME   SKILL.md on stdin: frontmatter description and trigger, then the steps and a check. Replaces an older version.
  hive skill list        the learned skills, each with its trigger and whether it is linked
  hive skill rm NAME     remove one (the ~ backup keeps its history)
  hive skill match CMD   the skills whose trigger matches a command (what the pointer uses)
"""
import json, os, re, sys
from pathlib import Path

HOME = Path.home()
CORE = Path(__file__).resolve().parent
STORE = Path(os.environ.get("HIVE_SKILLS_STORE", HOME / "kb" / "skills"))
MAX_LINES, MAX_SKILLS, PREFIX = 60, 20, "learned-"
NAME = re.compile(r"[a-z0-9][a-z0-9-]{2,39}")
FRONT = re.compile(r"\A---\n(.*?)\n---\n", re.S)

def config():
    try: return json.loads((CORE / "config.json").read_text())
    except (OSError, ValueError): return {}

def pointer_on(): return config().get("skill_pointer") is True

def link_dir():
    """The first login's skills folder: the one later logins link to (HIVE_SKILLS_LINKS overrides, for tests)."""
    if (d := os.environ.get("HIVE_SKILLS_LINKS")): return Path(d)
    try: return Path(config()["logins"][0]).expanduser() / "skills"
    except (KeyError, IndexError, TypeError): return HOME / ".claude" / "skills"

def front(text):
    """The frontmatter as a dict of its plain `key: value` lines, or None if there is none."""
    m = FRONT.match(text)
    if not m: return None
    out = {}
    for line in m.group(1).splitlines():
        k, sep, v = line.partition(":")
        if sep and k.strip() and not k.startswith((" ", "\t")): out[k.strip()] = v.strip()
    return out

def check(name, text, existing):
    """Why this skill can't be filed, or None."""
    if not NAME.fullmatch(name): return f"name {name!r}: 3 to 40 of a-z, 0-9 and -, starting with a letter or digit"
    if name.startswith(PREFIX): return f"leave out the {PREFIX} prefix: it is added to the link"
    fm = front(text)
    if fm is None: return "no frontmatter: start with ---, then description: and trigger: lines, then ---"
    for k in ("description", "trigger"):
        if not fm.get(k): return f"frontmatter has no {k}:"
    try: re.compile(fm["trigger"])
    except re.error as e: return f"trigger is not a regex: {e}"
    if re.search(fm["trigger"], ""): return "trigger matches an empty command, so it would fire on everything"
    n = len(text.rstrip("\n").splitlines())
    if n > MAX_LINES: return f"{n} lines, over the cap of {MAX_LINES}: keep the method, cut the story"
    if name not in existing and len(existing) >= MAX_SKILLS: return f"{MAX_SKILLS} learned skills already: remove a stale one first (hive skill rm)"
    return None

def skills():
    """{name: frontmatter} for every learned skill in the store."""
    out = {}
    if not STORE.is_dir(): return out
    for d in sorted(STORE.iterdir()):
        f = d / "SKILL.md"
        if d.is_dir() and f.is_file():
            out[d.name] = front(f.read_text(errors="replace")) or {}
    return out

def file(name, text):
    if (why := check(name, text, skills())): return why
    fm = front(text)
    body = FRONT.sub("", text, count=1)
    head = {"name": PREFIX + name, "description": fm["description"], "trigger": fm["trigger"]}
    head.update({k: v for k, v in fm.items() if k not in head})
    d = STORE / name; d.mkdir(parents=True, exist_ok=True)
    (d / "SKILL.md").write_text("---\n" + "".join(f"{k}: {v}\n" for k, v in head.items()) + "---\n" + body)
    links = link_dir(); links.mkdir(parents=True, exist_ok=True)
    ln = links / (PREFIX + name)
    if ln.is_symlink() or ln.exists():
        if not ln.is_symlink(): return f"{ln} exists and is not a link: left alone"
        ln.unlink()
    ln.symlink_to(d)
    return None

def rm(name):
    d, ln = STORE / name, link_dir() / (PREFIX + name)
    if not (d / "SKILL.md").is_file(): return f"no learned skill {name}"
    if ln.is_symlink(): ln.unlink()
    (d / "SKILL.md").unlink()
    try: d.rmdir()
    except OSError: pass
    return None

def match(cmd):
    """Names of the learned skills whose trigger matches this command."""
    out = []
    for n, fm in skills().items():
        try:
            if fm.get("trigger") and re.search(fm["trigger"], cmd): out.append(n)
        except re.error: pass
    return out

def pointer(payload, seen_file):
    """The line core/inbox.py adds after a Bash call whose command matches a learned skill's trigger: once per skill per run
    (names already pointed at are kept in seen_file). Empty while config.json "skill_pointer" is not true."""
    if not pointer_on() or payload.get("tool_name") != "Bash": return ""
    cmd = (payload.get("tool_input") or {}).get("command", "")
    hits = match(cmd) if cmd else []
    if not hits: return ""
    Path(seen_file).parent.mkdir(parents=True, exist_ok=True)
    try: seen = set(Path(seen_file).read_text().split())
    except OSError: seen = set()
    new = [n for n in hits if n not in seen]
    if not new: return ""
    try:
        with open(seen_file, "a") as fh: fh.write("".join(n + "\n" for n in new))
    except OSError: pass
    return "\n".join(f"A learned skill covers this step: load it with the Skill tool ({PREFIX}{n}) before you try again." for n in new)

def main(argv):
    if not argv or argv[0] in ("-h", "--help"): print(__doc__.strip()); return 0
    verb, rest = argv[0], argv[1:]
    if verb == "list":
        ss, links = skills(), link_dir()
        if not ss: print(f"no learned skills (store {STORE}; pointer {'on' if pointer_on() else 'off'})"); return 0
        for n, fm in ss.items():
            ok = (links / (PREFIX + n)).is_symlink()
            print(f"{PREFIX}{n}  trigger {fm.get('trigger', '?')!r}  {'linked' if ok else 'NOT linked'}  {fm.get('description', '')[:80]}")
        print(f"pointer {'on' if pointer_on() else 'off'}")
        return 0
    if verb == "file" and len(rest) == 1:
        why = file(rest[0], sys.stdin.read())
        if why: print(f"hive skill file: {why}", file=sys.stderr); return 1
        print(f"filed {PREFIX}{rest[0]}: {STORE / rest[0] / 'SKILL.md'}, linked in {link_dir()}"); return 0
    if verb == "rm" and len(rest) == 1:
        why = rm(rest[0])
        if why: print(f"hive skill rm: {why}", file=sys.stderr); return 1
        print(f"removed {PREFIX}{rest[0]}"); return 0
    if verb == "match" and rest:
        print("\n".join(match(" ".join(rest)))); return 0
    print(__doc__.strip(), file=sys.stderr); return 2

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
