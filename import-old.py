#!/usr/bin/env python3
"""Bring an old Hive's (v0.x) things into this Hive: import-old.py STAGING [OLD_FOLDER]

STAGING is a copy made by the installer, on the same disk as $HOME, laid out as the old folder is:
projects/, .hive/kb/, .hive/chats/, .hive/config.json, plus projects.jsonl (the old journal's project records).
Run as the Hive's own user, with $HOME its home. Safe to run again: each item moves once, and nothing already here is overwritten.
Sign-ins, tokens and keys are never in STAGING, and this prints counts and names of projects, never contents.
Git repos come with their history but not their hooks or settings that run commands or sign in: another account wrote them, and the agents here would run them. The old folder keeps them.
"""
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

CHAT_IGNORE = "/kb/old-hive/chats/"


def git(gitdir, *args):
    return subprocess.run(["git", f"--git-dir={gitdir}", *args], capture_output=True, text=True, stdin=subprocess.DEVNULL)


def registry(staging):
    """The old project records, replayed the way the old Hive did. A bad line is skipped."""
    out = {}
    try:
        lines = open(staging / "projects.jsonl", encoding="utf-8", errors="replace")
    except OSError:
        return out
    with lines:
        for line in lines:
            try:
                e = json.loads(line)
                name = e["data"]["name"]
                if e["kind"] == "project.removed":
                    out.pop(name, None)
                elif e["kind"] in ("project.created", "project.updated"):
                    out[name] = e["data"]
            except (ValueError, KeyError, TypeError):
                pass
    return out


# The only settings a moved repo keeps. Anything else can run a command (core.hookspath, core.fsmonitor, core.sshcommand, diff and filter drivers, aliases, include.path...), carry a sign-in (credential.*, http.*.extraheader, url.*.insteadof), or send it elsewhere.
KEEP_GIT = re.compile(r"(core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks|autocrlf|eol|safecrlf|longpaths|quotepath)"
                      r"|extensions\..*"
                      r"|remote\..*\.(url|pushurl|fetch|tagopt|mirror|prune|promisor|partialclonefilter)"
                      r"|branch\..*\.(remote|merge|rebase|pushremote)"
                      r"|submodule\..*\.(url|active|branch)"
                      r"|user\.(name|email)|pull\.(rebase|ff)|push\.default|init\.defaultbranch)", re.DOTALL)


def clean_gitdir(g):
    """One repo's settings and hooks. Returns (tokens taken out, hooks taken out)."""
    cfg, hooks = g / "config", g / "hooks"
    tokens = removed = 0
    keys = None
    # This would make git take its settings and hooks from another folder.
    (g / "commondir").unlink(missing_ok=True)
    if cfg.is_file() and not cfg.is_symlink():
        r = git(g, "config", "--local", "--null", "--name-only", "--list")
        if r.returncode == 0:
            keys = [k for k in r.stdout.split("\0") if k]
    if keys is None:
        # A link, or nothing git can read: start it again. The repo's own data doesn't depend on it.
        if cfg.is_dir() and not cfg.is_symlink():
            force_rm(cfg)
        else:
            cfg.unlink(missing_ok=True)
        cfg.write_text("[core]\n\trepositoryformatversion = 0\n\tbare = false\n")
        keys = []
    for k in dict.fromkeys(keys):
        if not KEEP_GIT.fullmatch(k):
            if re.fullmatch(r"(http\.(.*\.)?extraheader|url\..*\.(insteadof|pushinsteadof))", k, re.DOTALL):
                tokens += 1
            git(g, "config", "--local", "--unset-all", k)
    urls = git(g, "config", "--local", "--null", "--get-regexp", r"^remote\..*\.(url|pushurl)$").stdout
    for item in urls.split("\0"):
        key, _, url = item.partition("\n")
        if not key:
            continue
        # ext::, fd:: and the like run a command when someone fetches.
        if re.match(r"[A-Za-z0-9][A-Za-z0-9+.-]*::", url):
            git(g, "config", "--local", "--unset-all", key)
            continue
        # ssh://git@host is a user name, not a sign-in, so only the web addresses.
        bare = re.sub(r"^(https?://)[^/@]*@", r"\1", url)
        if bare != url and git(g, "config", "--local", "--replace-all", key, bare).returncode == 0:
            tokens += 1
    w = g / "config.worktree"
    if w.is_dir() and not w.is_symlink():
        force_rm(w)
    else:
        w.unlink(missing_ok=True)
    if hooks.is_symlink():
        hooks.unlink()
    if hooks.is_dir():
        for h in hooks.iterdir():
            if h.name.endswith(".sample") and h.is_file() and not h.is_symlink():
                continue
            if h.is_dir() and not h.is_symlink():
                force_rm(h)
            else:
                h.unlink()
            removed += 1
    git(g, "worktree", "prune")
    return tokens, removed


def is_gitdir(d, dirs, files):
    # Git accepts more shapes than a normal repo has: HEAD can be a link to a folder, and objects can be a file.
    names = {*dirs, *files}
    return "HEAD" in names and ("objects" in names or "commondir" in names)


def clean_git(p):
    """Every repo in a moved project, however deep: its own, submodules', and any bare one, which git uses as soon as you're in its folder. Returns (tokens, hooks)."""
    top = os.path.realpath(p)
    tokens = hooks = 0
    for d, dirs, files in os.walk(p):
        dot = Path(d) / ".git"
        if dot.is_symlink():
            dot.unlink()
        elif ".git" in files and not dot.is_file():
            dot.unlink()
        elif ".git" in files:
            # A submodule's pointer to its repo under .git/modules. Anywhere else, git would use settings and hooks from there.
            m = re.match(r"gitdir: *(.*)", dot.read_text(errors="replace").strip())
            to = os.path.realpath(os.path.join(d, m.group(1))) if m else ""
            if not (to.startswith(top + os.sep) and "/.git/modules/" in to[len(top):]):
                dot.unlink()
        if is_gitdir(d, dirs, files):
            t, h = clean_gitdir(Path(d))
            tokens += t
            hooks += h
    return tokens, hooks


def goals(p, rec):
    """This Hive reads a project's goals from goals.md at its top. The old one kept them in .hive/goals.md or the registry."""
    g = p / "goals.md"
    if g.exists() or g.is_symlink():
        return
    old = p / ".hive" / "goals.md"
    text = ""
    if old.is_file() and not old.is_symlink():
        text = old.read_text(encoding="utf-8", errors="replace")
    if not text.strip():
        text = "\n\n".join(x for x in (rec.get("description"), rec.get("goals")) if x)
    if not text.strip():
        return
    g.write_text(text if text.endswith("\n") else text + "\n", encoding="utf-8")
    # Not a change to their repo, so it doesn't show up as one.
    ex = p / ".git" / "info" / "exclude"
    if ex.parent.is_dir() and not ex.parent.is_symlink() and not ex.is_symlink():
        with open(ex, "a", encoding="utf-8") as f:
            f.write("/goals.md\n")


def free_name(root, name):
    if not (root / name).exists() and not (root / name).is_symlink():
        return name
    for i in range(1, 1000):
        n = f"{name}-old-hive" if i == 1 else f"{name}-old-hive-{i}"
        if not (root / n).exists() and not (root / n).is_symlink():
            return n
    raise SystemExit(f"no free name for project {name}")


def merge_tree(src, dst):
    """Files from src that dst doesn't have yet. Links are skipped: one could point anywhere. Returns how many."""
    n = 0
    for d, dirs, files in os.walk(src):
        dirs[:] = [x for x in dirs if not os.path.islink(os.path.join(d, x))]
        to = dst / Path(d).relative_to(src)
        for f in files:
            s = Path(d) / f
            if s.is_symlink() or (to / f).exists():
                continue
            to.mkdir(parents=True, exist_ok=True)
            os.replace(s, to / f)
            n += 1
    return n


def chat_to_md(src, dst):
    """A chat as a page to read. It isn't replayed onto the board, because a mention there would wake agents. Returns its messages."""
    n = 0
    tmp = dst.with_suffix(".tmp")
    with open(src, encoding="utf-8", errors="replace") as f, open(tmp, "w", encoding="utf-8") as out:
        out.write(f"# Chat from the old Hive: {src.stem}\n\n")
        for line in f:
            try:
                m = json.loads(line)
                text = m["text"]
            except (ValueError, KeyError, TypeError):
                continue
            out.write(f"**{m.get('name') or m.get('from') or '?'}** {m.get('at', '')}\n\n{text}\n\n")
            n += 1
    if n:
        os.replace(tmp, dst)
    else:
        tmp.unlink()
    return n


def force_rm(path):
    subprocess.run(["chmod", "-R", "u+rwx", str(path)], stderr=subprocess.DEVNULL)
    shutil.rmtree(path, ignore_errors=True)


def main():
    staging = Path(sys.argv[1])
    old_folder = sys.argv[2] if len(sys.argv) > 2 else ""
    home = Path(os.environ["HOME"])
    dest = home / "kb" / "old-hive"
    dest.mkdir(parents=True, exist_ok=True)
    (home / "projects").mkdir(exist_ok=True)
    state_file = dest / "imported.json"
    state = json.loads(state_file.read_text()) if state_file.exists() else {"projects": {}, "chats": 0, "notes": 0, "left": [], "tokens": 0}

    def save():
        tmp = state_file.with_suffix(".tmp")
        tmp.write_text(json.dumps(state, indent=1) + "\n")
        os.replace(tmp, state_file)

    reg = registry(staging)
    src_projects = staging / "projects"
    for p in sorted(src_projects.iterdir()) if src_projects.is_dir() else []:
        name = p.name
        if name.startswith(".") or p.is_symlink() or not p.is_dir() or name in state["projects"]:
            continue
        rec = reg.get(name, {})
        if not isinstance(rec, dict):
            rec = {}
        # The old Hive's own source and its demo aren't your work.
        if rec.get("self") or rec.get("demo"):
            if name not in state["left"]:
                state["left"].append(name)
            continue
        to = free_name(home / "projects", name)
        tokens, hooks = clean_git(p)
        state["tokens"] += tokens
        state["hooks"] = state.get("hooks", 0) + hooks
        goals(p, rec)
        os.replace(p, home / "projects" / to)
        state["projects"][name] = to
        save()

    kb = staging / ".hive" / "kb"
    if kb.is_dir():
        # Notes kept for one walled-off project go with that project, if it came.
        iso = kb / "isolated"
        if iso.is_dir() and not iso.is_symlink():
            for d in sorted(iso.iterdir()):
                to = state["projects"].get(d.name)
                if to and d.is_dir() and not d.is_symlink():
                    state["notes"] += merge_tree(d, home / "projects" / to / "old-hive-notes")
        state["notes"] += merge_tree(kb, dest)
        save()

    chats = staging / ".hive" / "chats"
    if chats.is_dir():
        out = dest / "chats"
        for c in sorted(chats.glob("*.jsonl")):
            if c.is_symlink() or not c.is_file():
                continue
            out.mkdir(exist_ok=True)
            chat_to_md(c, out / (re.sub(r"[^A-Za-z0-9._-]+", "-", c.stem) + ".md"))
            c.unlink()
        state["chats"] = len(list(out.glob("*.md"))) if out.is_dir() else 0
        # Chats can be big, and the backup commits ~/kb.
        ign = home / ".gitignore"
        have = ign.read_text() if ign.exists() else ""
        if CHAT_IGNORE not in have.splitlines():
            ign.write_text(have + ("" if not have or have.endswith("\n") else "\n") + CHAT_IGNORE + "\n")
        save()

    readme(dest, state, reg, staging, old_folder)
    save()
    force_rm(staging)

    n = len(state["projects"])
    def many(k, what):
        return f"{k} {what}{'' if k == 1 else 's'}"
    print(f"Brought across from the old Hive: {many(n, 'project')}, {many(state['notes'], 'knowledge base file')}, {many(state['chats'], 'chat')}.")
    for name, to in sorted(state["projects"].items()):
        if name != to:
            print(f"  {name} is now projects/{to}, because a project called {name} was already here.")
    if state["left"]:
        print(f"  Left in the old folder (the old Hive's own): {', '.join(state['left'])}")
    if state["tokens"]:
        print(f"  Took {many(state['tokens'], 'old sign-in token')} out of project git settings.")
    if state.get("hooks"):
        print(f"  Took {many(state['hooks'], 'git hook')} out of the projects. They're still in the old folder.")


def readme(dest, state, reg, staging, old_folder):
    cfg = {}
    try:
        raw = json.loads((staging / ".hive" / "config.json").read_text())
        for k in ("model", "lead_model", "reviewer_model", "harness", "autonomy", "sandbox"):
            if isinstance(raw.get(k), (str, int, float, bool)):
                cfg[k] = raw[k]
    except (OSError, ValueError, AttributeError):
        pass
    if cfg:
        state["settings"] = cfg
    cfg = state.get("settings", {})
    walled = sorted(n for n, r in reg.items() if isinstance(r, dict) and (r.get("isolated") or r.get("private")) and n in state["projects"])
    lines = ["# What came from the old Hive (v0.x)", ""]
    if old_folder:
        lines += [f"The old folder is still at `{old_folder}`, whole, as the backup. Delete it when you're happy.", ""]
    lines += ["## Where things went", "", "- Projects: `~/projects/<name>`. Git history came with them.",
              "- Goals: `goals.md` at the top of each project, when the old Hive had them.",
              "- The old knowledge base: here, in `kb/old-hive/`. Notes kept for one walled-off project: that project's `old-hive-notes/`.",
              "- Chats: `kb/old-hive/chats/`, as pages to read. They aren't on the board, so nothing in them wakes an agent.", ""]
    if state["projects"]:
        lines += ["Projects: " + ", ".join(f"`{v}`" for _, v in sorted(state["projects"].items())), ""]
    if walled:
        lines += ["These were walled off in the old Hive. This Hive has no walls between projects, so other agents can see them now: "
                  + ", ".join(f"`{state['projects'][n]}`" for n in walled), ""]
    lines += ["## What didn't come", "", "- Sign-ins, tokens and keys. Sign in again in this Hive. Projects use this Hive's sign-in now.",
              "- Git hooks, and git settings that run commands or sign in. The repos' own settings in the old folder still have them. Read one before you copy it back, because agents here would run it.",
              "- Running work, past runs, task lists and schedules. Tell the Hive what you want next, and point it at `goals.md`.",
              "- `.checkouts` folders: agents' working copies. Anything not pushed from one is still in the old folder.", ""]
    if state["left"]:
        lines += ["Left in the old folder, because they were the old Hive's own: " + ", ".join(f"`{x}`" for x in state["left"]), ""]
    if cfg:
        lines += ["## Old settings, for reference", "", "This Hive doesn't apply these. It sets its own in the app.", ""] + [f"- {k}: {cfg[k]}" for k in cfg] + [""]
    (dest / "README.md").write_text("\n".join(lines), encoding="utf-8")


if __name__ == "__main__":
    main()
