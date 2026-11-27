#!/usr/bin/env python3
"""hive export NAME [--to DIR] and hive import ZIP [--as NAME]: move one project between Hives.

export writes ~/projects/NAME as one zip, git history included (the whole .git), to ~/exports by default,
and prints its full path. import puts such a zip back as ~/projects/NAME, never over a project that is
already there. A zip from anywhere is checked first: every entry under the one project folder, no links,
no '..', no absolute paths, and the sizes it claims are the sizes written (design: hive-app
DESIGN-many-hives.md, "Bring projects in"; step 7e).
A .git from elsewhere can carry commands (hooks, core.fsmonitor, filters, pagers in .git/config) that
git would run later, so import keeps only its history: hooks are left out, .git/config keeps an
allowlist of plain settings (remotes, branches, format), a nested repository (a .git folder or a bare
one), alternates or commondir is refused, rebase/sequencer state and submodule copies are left out,
modes never give others write, and every git call here names its repository so git never walks up.
Standard library only; run with python3 -I.
"""
import argparse, ctypes, datetime, errno, json, os, re, shutil, stat, subprocess, sys, time, zipfile, zlib
from pathlib import Path

HOME = Path.home()
PROJECTS = HOME / "projects"
NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}")
MANIFEST = "HIVE-EXPORT.json"
MAX_ENTRIES = 500_000
CHUNK = 1 << 20
MAX_MANIFEST = 64 << 10
KEEP_CONFIG = re.compile(r"core\.(repositoryformatversion|filemode|logallrefupdates|ignorecase|precomposeunicode|symlinks)"
                         r"|extensions\.(objectformat|refstorage|partialclone|worktreeconfig|preciousobjects|noop)"
                         r"|remote\..+\.(url|pushurl|fetch|push|tagopt|prune|promisor|partialclonefilter)"
                         r"|branch\..+\.(remote|merge|rebase)|user\.(name|email)|init\.defaultbranch")


def die(msg):
    sys.exit(f"hive {CMD}: {msg}")


def good_name(n):
    return bool(NAME.fullmatch(n)) and n not in (".", "..") and not n.endswith(".lock")


def git(repo, *args):
    """git on exactly repo/.git: never a repository further up, never its hooks or fsmonitor."""
    env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(repo.parent), GIT_CONFIG_NOSYSTEM="1")
    return subprocess.run(["git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
                           f"--git-dir={repo / '.git'}", f"--work-tree={repo}", *args],
                          capture_output=True, text=True, env=env, cwd=repo)


def git_head(d):
    if not (d / ".git").is_dir(): return None
    r = git(d, "rev-parse", "-q", "--verify", "HEAD")
    return r.stdout.strip() or None if r.returncode == 0 else None


def clean_git_config(src, cfg):
    """Write .git/config (cfg) from the zip's copy (src, unpacked under a name git never reads), keeping only KEEP_CONFIG;
    git config --file --list runs nothing and follows no include. Until this runs the folder has no config, so a git run in it meanwhile obeys nothing from the zip."""
    if not src.exists(): return 0, False
    env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CEILING_DIRECTORIES=str(cfg.parent))
    r = subprocess.run(["git", "config", "--file", str(src), "--list", "-z"], capture_output=True, env=env, cwd=cfg.parent)
    if r.returncode: die("its .git/config isn't readable: " + r.stderr.decode(errors="replace").strip()[:200])
    keep, dropped, lfs = [], 0, False
    for item in r.stdout.split(b"\0"):
        if not item: continue
        k, _, v = item.decode(errors="surrogateescape").partition("\n")
        if k.startswith("extensions.") and not KEEP_CONFIG.fullmatch(k):
            die(f"its git uses {k}, which this Hive doesn't know; update this Hive first (hive update)")
        lfs |= k.startswith("filter.lfs.")
        if KEEP_CONFIG.fullmatch(k) or (k == "core.bare" and v.lower() in ("false", "no", "off", "0")): keep.append((k, v or "true"))
        else: dropped += 1
    new = cfg.with_name("config.new")
    new.write_text("")
    for k, v in keep:
        w = subprocess.run(["git", "config", "--file", str(new), "--add", k, v], capture_output=True, env=env, cwd=cfg.parent)
        if w.returncode: die(f"couldn't keep the setting {k} from its .git/config")
    os.replace(new, cfg)
    src.unlink()
    return dropped, lfs


HELD = ".git/config.from-zip"   # the zip's .git/config while it unpacks: git never reads it, and clean_git_config writes the real one from it


def looks_head(p):
    """Whether git would read this file as a repository's HEAD (setup.c validate_headref): it starts with "ref:" or an object id,
    whatever follows (a 64-hex id starts with 40)."""
    try:
        with open(p, "rb") as f: b = f.read(256)
    except OSError: return True
    return b.startswith(b"ref:") or bool(re.match(rb"[0-9a-fA-F]{40}", b))


def sweep_imports():
    """Remove .import-NAME-PID folders left by an import that was killed (a timeout, a restart): nothing else cleans them."""
    for d in PROJECTS.glob(".import-*"):
        pid = d.name.rsplit("-", 1)[-1]
        if not pid.isdigit() or d.is_symlink(): continue
        try: os.kill(int(pid), 0); continue    # still unpacking
        except ProcessLookupError: shutil.rmtree(d, ignore_errors=True)
        except OSError: continue


def left_out(parts):
    """Parts of a .git that are commands or another repository: submodule copies with their own config and hooks,
    rebase todo lists that run 'exec' lines, linked worktrees."""
    return parts[:1] == [".git"] and parts[1:2] != [] and parts[1] in ("modules", "rebase-merge", "rebase-apply", "sequencer", "worktrees")


def rename_noreplace(a, b):
    """rename(2) a folder to b only if b doesn't exist (a plain rename would replace an empty folder b)."""
    try:
        f = ctypes.CDLL(None, use_errno=True).renameat2
        f.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    except (OSError, AttributeError):
        f = None
    if f:
        if f(-100, os.fsencode(a), -100, os.fsencode(b), 1) == 0: return   # AT_FDCWD, RENAME_NOREPLACE
        e = ctypes.get_errno()
        if e == errno.EEXIST: die(f"a project named {b.name} appeared meanwhile; nothing changed")
        if e not in (errno.ENOSYS, errno.EINVAL): raise OSError(e, os.strerror(e))
    if os.path.lexists(b): die(f"a project named {b.name} appeared meanwhile; nothing changed")
    os.rename(a, b)


def walk_error(e):
    raise OSError(e.errno, f"can't read {e.filename}: {e.strerror}; nothing exported")


def open_inside(p, real_src):
    """p opened for reading, or None when it is no longer a plain file inside the project. The bytes come from this one handle, and the
    handle's own path is checked, so a file or folder swapped for a link after the walk saw it can't pull in a file from outside."""
    try: fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError as e:
        if e.errno in (errno.ELOOP, errno.ENXIO, errno.ENOENT): return None
        raise
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode): os.close(fd); return None
        if Path(f"/proc/self/fd/{fd}").exists() and not Path(os.readlink(f"/proc/self/fd/{fd}")).is_relative_to(real_src): os.close(fd); return None
    except BaseException: os.close(fd); raise
    return os.fdopen(fd, "rb")


def cmd_export(a):
    if not good_name(a.name): die(f"no project named {a.name!r} (a project name is letters, digits and . _ -)")
    src = PROJECTS / a.name
    if src.is_symlink() or not src.is_dir(): die(f"no project folder {src}")
    out = Path(a.to).expanduser() if a.to else HOME / "exports"
    if out.resolve().is_relative_to(src.resolve()): die("--to can't be inside the project itself")
    out.mkdir(parents=True, exist_ok=True)
    real_src = src.resolve()
    when = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    dest = out / f"{a.name}-{when}.zip"
    n = 1
    while dest.exists():
        n += 1; dest = out / f"{a.name}-{when}-{n}.zip"
    tmp = out / f".{dest.name}.part"
    files = links = other = 0
    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as fh, zipfile.ZipFile(fh, "w", zipfile.ZIP_DEFLATED, allowZip64=True) as z:
            for root, dirs, names in os.walk(src, onerror=walk_error, followlinks=False):
                r = Path(root)
                rel = r.relative_to(src)
                keep = []
                for d in sorted(dirs):
                    if (r / d).is_symlink(): links += 1
                    else: keep.append(d)
                dirs[:] = keep
                arc_dir = f"{a.name}/{rel.as_posix()}/" if str(rel) != "." else f"{a.name}/"
                zi = zipfile.ZipInfo(arc_dir); zi.external_attr = (stat.S_IFDIR | (os.lstat(r).st_mode & 0o777 | 0o700)) << 16
                z.writestr(zi, b"")
                for f in sorted(names):
                    p = r / f
                    st = os.lstat(p)
                    if stat.S_ISLNK(st.st_mode): links += 1; continue
                    if not stat.S_ISREG(st.st_mode): other += 1; continue
                    zi = zipfile.ZipInfo.from_file(p, f"{a.name}/{(rel / f).as_posix()}", strict_timestamps=False)
                    zi.external_attr = (stat.S_IFREG | (st.st_mode & 0o777 | 0o400)) << 16   # no setuid; git's packs stay read-only
                    zi.compress_type = zipfile.ZIP_DEFLATED
                    fin = open_inside(p, real_src)
                    if fin is None: links += 1; continue   # swapped for a link or a special file after the walk saw it
                    with fin, z.open(zi, "w", force_zip64=True) as w:
                        shutil.copyfileobj(fin, w, CHUNK)
                    files += 1
            meta = {"v": 1, "kind": "project", "name": a.name, "files": files, "head": git_head(src),
                    "made": datetime.datetime.now().astimezone().isoformat(timespec="seconds")}
            z.writestr(MANIFEST, json.dumps(meta, indent=1) + "\n")
        os.replace(tmp, dest)
    except BaseException:
        try: os.unlink(tmp)
        except OSError: pass
        raise
    print(f"exported {a.name}: {files} files{' and its git history' if (src / '.git').is_dir() else ''}")
    if links or other: print(f"left out: {links} links, {other} other special files (a zip holds plain files only)")
    print(dest)


def check_zip(z):
    """The manifest, and every entry's name, kind and size, before a byte is written."""
    infos = z.infolist()
    if len(infos) > MAX_ENTRIES: die(f"more than {MAX_ENTRIES} entries; not a project export")
    try:
        if z.getinfo(MANIFEST).file_size > MAX_MANIFEST: die(f"its {MANIFEST} is too large; not a project export")
        meta = json.loads(z.read(MANIFEST))
    except KeyError: die(f"no {MANIFEST} in it, so it isn't a project export from a Hive")
    except (ValueError, zipfile.BadZipFile, NotImplementedError, RuntimeError, zlib.error, EOFError):
        die(f"{MANIFEST} isn't readable")
    if not isinstance(meta, dict) or meta.get("kind") != "project": die("not a project export")
    if meta.get("v") != 1: die(f"made by a newer Hive (format {meta.get('v')!r}); update this Hive first (hive update)")
    orig = meta.get("name")
    if not isinstance(orig, str) or not good_name(orig): die("the export names no usable project")
    total, seen, plan = 0, set(), []
    for zi in infos:
        n = zi.filename
        if n == MANIFEST: continue
        if "\\" in n or "\0" in n or n.startswith("/") or not n.startswith(orig + "/"): die(f"entry {n!r} is outside the project folder")
        rel = n[len(orig) + 1:]
        parts = [p for p in rel.split("/") if p]
        if any(p in (".", "..") for p in parts): die(f"entry {n!r} climbs out of the project folder")
        if not n.endswith("/") and len(parts) > 1 and parts[-1] == ".git":
            plan.append((parts, False, zi, -1)); continue   # a submodule's link file: left out (git submodule update --init)
        if any(p.lower() == ".git" and (i or p != ".git") for i, p in enumerate(parts)):
            die(f"entry {n!r} is a second git repository inside the project (its .git could run commands); not brought in")
        mode = zi.external_attr >> 16
        isdir = n.endswith("/")
        kind = stat.S_IFMT(mode)   # 0 when the zip tool wrote no file type (Python's writestr, many others)
        if kind and kind != (stat.S_IFDIR if isdir else stat.S_IFREG): die(f"entry {n!r} is a link or special file")
        key = "/".join(parts)
        if key == ".git" and not isdir: die(".git in it is a file, not a folder (it could point at another repository)")
        if key == HELD: die(f"entry {n!r} has a name this Hive keeps for itself")
        if key in (".git/commondir", ".git/objects/info/alternates", ".git/objects/info/http-alternates"):
            die(f"its {key} points git at another repository; not brought in")
        if key in seen and not isdir: die(f"entry {n!r} is in it twice")
        seen.add(key)
        if zi.flag_bits & 0x1: die(f"entry {n!r} is encrypted")
        total += zi.file_size + 4096   # and a block for each entry
        plan.append((parts, isdir, zi, mode))
    return meta, orig, total, plan


def cmd_import(a):
    zp = Path(a.zip).expanduser()
    if not zp.is_file(): die(f"no file {zp}")
    try: z = zipfile.ZipFile(zp)
    except (zipfile.BadZipFile, OSError) as e: die(f"not a zip ({e})")
    with z:
        meta, orig, total, plan = check_zip(z)
        name = a.as_name or orig
        if not good_name(name): die(f"{name!r} can't be a project name (letters, digits and . _ -)")
        PROJECTS.mkdir(parents=True, exist_ok=True)
        dest = PROJECTS / name
        if os.path.lexists(dest): die(f"a project named {name} is already here; bring it in under another name with --as NEW")
        free = shutil.disk_usage(PROJECTS).free
        if total + (512 << 20) > free: die(f"it unpacks to {total >> 20} MB and only {free >> 20} MB is free")
        sweep_imports()
        tmp = PROJECTS / f".import-{name}-{os.getpid()}"
        os.mkdir(tmp, 0o700)
        files = hooks = subs = 0
        try:
            um = os.umask(0); os.umask(um)
            for parts, isdir, zi, mode in plan:
                if mode == -1: subs += 1; continue
                if left_out(parts): continue
                if isdir:
                    (tmp.joinpath(*parts) if parts else tmp).mkdir(parents=True, exist_ok=True); continue
                if parts[:1] == [".git"] and (parts[1:] == ["config.worktree"]
                                              or (parts[1:2] == ["hooks"] and not parts[-1].endswith(".sample"))):
                    hooks += parts[1:2] == ["hooks"]; continue   # commands git would run, or links to elsewhere
                p = tmp.joinpath(*(HELD.split("/") if parts == [".git", "config"] else parts))
                p.parent.mkdir(parents=True, exist_ok=True)
                fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                got = 0
                with os.fdopen(fd, "wb") as out, z.open(zi) as src:
                    while True:
                        b = src.read(CHUNK)
                        if not b: break
                        got += len(b)
                        if got > zi.file_size: die(f"entry {zi.filename!r} unpacks larger than it says")
                        out.write(b)
                t = time.mktime(zi.date_time + (0, 0, -1))
                os.utime(p, (t, t))
                os.chmod(p, (mode & 0o777 & ~um | 0o400) if mode else 0o644 & ~um)
                files += 1
            dropped, lfs = 0, False
            for root, dirs, names in os.walk(tmp):
                r = Path(root)
                if r == tmp: dirs[:] = [d for d in dirs if d != ".git"]
                here = set(dirs) | set(names)   # git only asks that objects and refs can be entered, so a file with x counts
                if "HEAD" in names and (looks_head(r / "HEAD") or {"objects", "refs"} <= here):   # git needs HEAD to read as one (setup.c is_git_directory), commondir or not
                    die(f"{r.relative_to(tmp)} in it is a second git repository (its config could run commands); not brought in")
            g = tmp / ".git"
            if os.path.lexists(g):
                if g.is_symlink() or not g.is_dir(): die(".git in it isn't a folder")
                if not ((g / "HEAD").is_file() and (g / "objects").is_dir() and (g / "refs").is_dir()):
                    die("its .git isn't a whole git repository (no HEAD, objects or refs)")
                dropped, lfs = clean_git_config(tmp / HELD, g / "config")
                r = git(tmp, "fsck", "--no-dangling", "--no-progress")
                if r.returncode: die("its git history doesn't check out (git fsck): " + (r.stderr or r.stdout).strip()[:300])
            for parts, isdir, zi, mode in sorted((x for x in plan if x[1] and not left_out(x[0])), key=lambda x: -len(x[0])):
                d = tmp.joinpath(*parts) if parts else tmp
                os.chmod(d, (mode & 0o777 & ~um | 0o700) if mode else 0o755 & ~um)
                t = time.mktime(zi.date_time + (0, 0, -1)); os.utime(d, (t, t))
            rename_noreplace(tmp, dest)
        except (zipfile.BadZipFile, zlib.error, EOFError, NotImplementedError, RuntimeError, OverflowError, ValueError, OSError) as e:
            shutil.rmtree(tmp, ignore_errors=True)
            die(f"the zip is damaged, nothing brought in ({e})")
        except BaseException:
            shutil.rmtree(tmp, ignore_errors=True)
            raise
    head = git_head(dest)
    print(f"brought in {name}: {files} files" + (f", git at {head[:12]}" if head else ""))
    if hooks or dropped:
        print(f"left out, as commands from elsewhere: {hooks} git hooks, {dropped} .git/config settings (remotes and branches kept)")
    if subs: print(f"left out: {subs} submodules; run git submodule update --init in it to fetch them again")
    if lfs: print("it used git LFS; run git lfs install in it to get the large files back")
    if meta.get("head") and head != meta["head"]: print(f"note: the export was made at {meta['head'][:12]}")
    print(dest)


CMD = sys.argv[1] if sys.argv[1:2] in (["export"], ["import"]) else "export"
ap = argparse.ArgumentParser(prog="hive")
sp = ap.add_subparsers(dest="cmd", required=True)
p = sp.add_parser("export", help="hive export NAME [--to DIR]: ~/projects/NAME as one zip with its git history (to ~/exports)")
p.add_argument("name"); p.add_argument("--to"); p.set_defaults(f=cmd_export)
p = sp.add_parser("import", help="hive import ZIP [--as NAME]: a project export back under ~/projects, never over one already there")
p.add_argument("zip"); p.add_argument("--as", dest="as_name"); p.set_defaults(f=cmd_import)
if __name__ == "__main__":
    a = ap.parse_args(); a.f(a)
