#!/usr/bin/env python3
"""Documents: the library's PDFs in the dashboard, with notes the person pins on them (projects/doc-review-view).

The dash reads the library ledger (kb/library-filed.json: sha256 -> status, doc id, path) and groups it by document (006-0002) with its
revisions (CL, CM, ...). Pages are drawn by pdftoppm and cached by sha; the words and their boxes come from pdftotext -bbox-layout, so the
page can be selected like text. Notes live in one JSON per document, kb/doc-notes/<doc>.json; numbers are per document and never reused,
so a note keeps its number on every revision until the agent marks it addressed.

The agent that edits the document uses the command line (projects/doc-review-view/AGENT.md):
    docs.py show 006-0002                      open notes, with page, quote, box and text
    docs.py pending 006-0002 path/to/new.pdf   the page shows this PDF as the next revision until the library files it
    docs.py addressed 006-0002 1 3 [--rev CM] [--pdf new.pdf]  these notes are done in the new revision; the rest stay open
                                               (--pdf also does `pending` with that PDF, in the same call)
    docs.py check                              open every document as the page would (words, page 1); names any that fail
    docs.py source 006-0002                    the files under projects/ the newest PDF was likely built from, best first
    docs.py source --all                       how many documents have a source found (exit 1 names those with none)
    docs.py md-pdf SRC.md OUT.pdf              print a markdown source as the library does; file that PDF, not the .md
    docs.py verify 006-0002                    each addressed note against the revision it names: exit 1 when a note names the
                                               revision it was made on; lists notes whose words, links, icons and bold are
                                               unchanged there, to look at
    docs.py looked 006-0002 21 30 [--why W]    mark addressed notes as seen by eye (spacing, an answered question): verify skips them
"""
import bisect, fcntl, hashlib, json, os, re, subprocess, sys, threading, time
from pathlib import Path

HOME = Path.home()
LEDGER = HOME / "kb" / "library-filed.json"
NOTES = HOME / "kb" / "doc-notes"
CACHE = HOME / ".cache" / "dash-docs"
BASE = re.compile(r"^\d{3}-\d{4}$")
DOCID = re.compile(r"^(\d{3}-\d{4})-([A-Z]{1,3})$")
REV = re.compile(r"^([A-Z]{1,3}|next)$")
MAX_PDF = 60 * 1024 * 1024
KINDS = ("comment", "ask")
MAX_RECTS = 300          # one per selected line; a whole dense page is well under this
PENDING_DAYS = 7         # a "next" the library never filed stops showing after this


def _jload(p, d):
    try: return json.loads(Path(p).read_text())
    except (OSError, ValueError): return d


def _safe_pdf(path):
    """A PDF under home, not through a symlink, of a sane size; else None."""
    if not path: return None
    p = Path(path)
    if not p.is_absolute(): p = HOME / p
    try:
        r = p.resolve(strict=True)
        r.relative_to(HOME)
    except (OSError, ValueError):
        return None
    if r != p.absolute() or r.suffix.lower() != ".pdf" or not r.is_file(): return None
    try:
        if r.stat().st_size > MAX_PDF: return None
    except OSError: return None
    return r


def _sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""): h.update(b)
    return h.hexdigest()


_SHAS = {}
def _filesha(p):
    """The file's sha as it is now (a PDF rebuilt in place gets a new one), remembered by path, mtime and size."""
    st = Path(p).stat(); k = (str(p), st.st_mtime_ns, st.st_size)
    if k not in _SHAS:
        if len(_SHAS) > 500: _SHAS.clear()
        _SHAS[k] = _sha(p)
    return _SHAS[k]


def _tmp(f):
    return f.with_name(f".{f.name}.{os.getpid()}-{threading.get_ident()}.tmp")


def _revkey(r):
    return (2, 0, "") if r == "next" else (1, len(r), r)


def _clean_title(t, doc):
    t = (t or "").strip()
    if not t or t == doc: return ""
    return re.sub(r"\s*\([a-z0-9_-]+\)$", "", t)   # "Jane Doe Resume (asic)": the slug in brackets is the outbox folder's, not a title


def _notes_file(base):
    if not BASE.match(base or ""): raise ValueError("bad document id")
    return NOTES / f"{base}.json"


def load_notes(base):
    d = _jload(_notes_file(base), {})
    if not isinstance(d, dict): d = {}
    d.setdefault("doc", base); d.setdefault("next", 1); d.setdefault("notes", [])
    return d


def _save_notes(base, d):
    NOTES.mkdir(parents=True, exist_ok=True)
    f = _notes_file(base); tmp = _tmp(f)
    tmp.write_text(json.dumps(d, indent=1, ensure_ascii=False) + "\n"); os.replace(tmp, f)


class _Lock:
    def __init__(self, base): self.f = None; self.base = base
    def __enter__(self):
        NOTES.mkdir(parents=True, exist_ok=True)
        self.f = open(NOTES / ".lock", "a"); fcntl.flock(self.f, fcntl.LOCK_EX); return load_notes(self.base)
    def __exit__(self, *a): fcntl.flock(self.f, fcntl.LOCK_UN); self.f.close()


def _revisions():
    """{base: {rev: {doc, path, sha, title, at, status}}} from the ledger: filed ones, and skipped ones that name the revision they match."""
    out = {}
    for sha, e in (_jload(LEDGER, {}) or {}).items():
        if not isinstance(e, dict): continue
        m = DOCID.match(e.get("doc") or "")
        if not m or e.get("status") not in ("filed", "skipped"): continue
        p = _safe_pdf(e.get("path"))
        if not p: continue
        base, rev = m.groups()
        have = out.setdefault(base, {}).get(rev)
        if have and (have["status"] == "filed" or e.get("status") != "filed"): continue
        out[base][rev] = {"rev": rev, "doc": e["doc"], "path": str(p.relative_to(HOME)), "sha": sha, "title": _clean_title(e.get("title"), e["doc"]),
                          "at": str(e.get("at") or ""), "status": e.get("status"), "mtime": int(p.stat().st_mtime)}
    return out


def _filed_shas():
    return {k for k, e in (_jload(LEDGER, {}) or {}).items() if isinstance(e, dict) and e.get("status") in ("filed", "skipped")}


def _pending(base, revs, notes):
    """The agent's new PDF while the library hasn't filed it yet (docs.py pending). Gone once the library has the file (as it was at
    `pending` or as it is now, under any document id), once a revision newer than the pending mark is filed, or after PENDING_DAYS."""
    pe = notes.get("pending")
    if not isinstance(pe, dict): return None
    p = _safe_pdf(pe.get("path"))
    if not p: return None
    try: at = time.mktime(time.strptime(pe.get("at") or "", "%Y-%m-%dT%H:%M:%S"))
    except (ValueError, OverflowError): return None
    if time.time() - at > PENDING_DAYS * 86400: return None
    now = _filesha(p); filed = _filed_shas()
    if now in filed or pe.get("sha") in filed: return None
    if any(r["status"] == "filed" and r["mtime"] > at + 1 for r in revs.values()): return None
    return {"rev": "next", "doc": f"{base} (filing)", "path": str(p.relative_to(HOME)), "sha": now, "title": "",
            "at": pe.get("at") or "", "status": "pending", "mtime": int(p.stat().st_mtime)}


def documents():
    """The list: one row per document at its newest revision, newest first; documents with open notes on top."""
    rows = []
    for base, revs in _revisions().items():
        notes = load_notes(base) if _notes_file(base).exists() else {"notes": []}
        pend = _pending(base, revs, notes)
        order = sorted(revs, key=_revkey)
        last = revs[order[-1]]
        title = next((revs[r]["title"] for r in reversed(order) if revs[r]["title"]), "") or Path(last["path"]).stem.replace("_", " ")
        ns = notes.get("notes", [])
        rows.append({"id": base, "title": title, "rev": "next" if pend else last["rev"], "revs": order + (["next"] if pend else []),
                     "at": max(r["mtime"] for r in revs.values()), "drafts": sum(n.get("status") == "draft" for n in ns),
                     "open": sum(n.get("status") == "sent" for n in ns), "pending": bool(pend)})
    rows.sort(key=lambda r: (-(r["drafts"] + r["open"] > 0), -r["at"]))
    return {"docs": rows}


def _words(pdf):
    """Pages as {w, h, words: [[x0, y0, x1, y1, text, line]]} in PDF points, from pdftotext -bbox-layout."""
    try:
        x = subprocess.run(["pdftotext", "-bbox-layout", str(pdf), "-"], capture_output=True, text=True, timeout=60).stdout
    except (OSError, subprocess.SubprocessError):
        return []
    import html
    pages, line = [], 0
    for m in re.finditer(r'<page width="([\d.]+)" height="([\d.]+)">|<line |<word xMin="([\d.]+)" yMin="([\d.]+)" xMax="([\d.]+)" yMax="([\d.]+)">(.*?)</word>', x):
        if m.group(1):
            pages.append({"w": float(m.group(1)), "h": float(m.group(2)), "words": []})
        elif m.group(0) == "<line ":
            line += 1
        elif pages:
            t = re.sub(r"[\x00-\x1f\x7f-\x9f]", "", html.unescape(m.group(7))).strip()
            if t: pages[-1]["words"].append([round(float(m.group(i)), 2) for i in (3, 4, 5, 6)] + [t, line])
    return pages


def _key(r):
    """The cache key: the file's sha as it is now, not the ledger's (the file may have been rebuilt in place since)."""
    return _filesha(HOME / r["path"])[:24]


def _cached_words(r):
    CACHE.mkdir(parents=True, exist_ok=True)
    f = CACHE / f"{_key(r)}.words.json"
    w = _jload(f, None)
    if w is None:
        w = _words(HOME / r["path"])
        if w: t = _tmp(f); t.write_text(json.dumps(w)); os.replace(t, f)
    return w


def _resolve_next(notes, revs, pend):
    """Notes made on, or addressed in, a pending build ("next") name the revision letter once the library files that build; a note
    from an older pending build that was never filed shows as "build" so it is found again by its words on what is shown now."""
    by = {}
    for r in revs.values(): by.setdefault(r["sha"], r["rev"]); by.setdefault(_filesha(HOME / r["path"]), r["rev"])
    cur = pend["sha"] if pend else None
    def t(x):
        try: return time.mktime(time.strptime(x or "", "%Y-%m-%dT%H:%M:%S"))
        except (ValueError, OverflowError): return None
    filed = sorted((t(r["at"]), r["rev"]) for r in revs.values() if r.get("status") == "filed" and t(r["at"]))
    def after(stamp):   # no build sha kept (no `pending` was set): the first revision the library filed at or after that moment
        s = t(stamp)
        return next((rv for ft, rv in filed if s is not None and ft >= s - 5), None)
    out = []
    for n in notes:
        n = dict(n)
        if n.get("rev") == "next":
            if not n.get("revSha"): n["rev"] = after(n.get("made")) or "next"
            elif n["revSha"] != cur: n["rev"] = by.get(n["revSha"], "build")
        if n.get("addressedIn") == "next":
            if not n.get("addressedSha"): n["addressedIn"] = after(n.get("addressedAt")) or "next"
            elif n["addressedSha"] != cur: n["addressedIn"] = by.get(n["addressedSha"], "a newer build")
        out.append(n)
    return out


def _rev_of(base, rev):
    revs = _revisions().get(base) or {}
    if rev == "next":
        return _pending(base, revs, load_notes(base)), revs
    return revs.get(rev), revs


def doc(base, rev=None):
    """One document: its revisions, the pages of one (the newest unless asked), and its notes."""
    if not BASE.match(base or ""): raise ValueError("bad document id")
    revs = _revisions().get(base)
    if not revs: raise KeyError(base)
    notes = load_notes(base)
    pend = _pending(base, revs, notes)
    allr = dict(revs, **({"next": pend} if pend else {}))
    order = sorted(allr, key=_revkey)
    rev = rev if rev in allr else order[-1]
    r = allr[rev]
    title = next((revs[x]["title"] for x in reversed(sorted(revs, key=_revkey)) if revs[x]["title"]), "") or Path(r["path"]).stem.replace("_", " ")
    return {"id": base, "title": title, "thread": f"doc-{base}", "rev": rev,
            "revs": [{k: allr[x][k] for k in ("rev", "doc", "path", "at", "status")} for x in order],
            "path": r["path"], "sha": _key(r), "pages": _cached_words(r), "notes": _resolve_next(notes["notes"], revs, pend), "next": notes["next"],
            "source": source_of(base, revs)}


_SRC = {}          # (base, sha of the newest revision) -> (time, the source path or "" when none was found)
_SRC_BUSY = set()  # lookups running in the background: a poll never waits on the walk of projects/


def _source_bg(k, base):
    try:
        out = [o for o in find_source(base)[1] if "copy filed" not in o]
        _SRC[k] = (time.time(), out[0].split("  ")[0] if out else "")
    except Exception:
        _SRC[k] = (time.time(), "")
    finally:
        _SRC_BUSY.discard(k)


def source_of(base, revs):
    """The likeliest file the newest revision was built from (docs.py source), or None while it is being looked up or when none is found."""
    k = (base, _key(revs[sorted(revs, key=_revkey)[-1]]))
    got = _SRC.get(k)
    if got and (got[1] or time.time() - got[0] < 300): return got[1] or None   # a miss is looked up again after 5 minutes
    if k not in _SRC_BUSY:
        _SRC_BUSY.add(k)
        t = threading.Thread(target=_source_bg, args=(k, base), daemon=True); t.start()
        t.join(1.0)   # a quick lookup shows on the first load; a slow one (a cold walk of projects/) on a later poll
    return (_SRC.get(k) or (0, ""))[1] or None


_PRUNED = [0.0]


def _prune_cache(days=14):
    """Drop cached page images and word files nobody has read for `days` (old revisions); at most once an hour."""
    now = time.time()
    if now - _PRUNED[0] < 3600: return
    _PRUNED[0] = now
    for f in CACHE.glob("*"):
        try:
            if not f.name.startswith(".") and now - max(f.stat().st_atime, f.stat().st_mtime) > days * 86400: f.unlink()
        except OSError:
            pass


def page_png(base, rev, p):
    """One page as PNG bytes (144 dpi), cached by the file's sha."""
    if not BASE.match(base or "") or not REV.match(rev or ""): raise ValueError("bad id")
    r, _ = _rev_of(base, rev)
    if not r: raise KeyError(base)
    p = int(p)
    if p < 1 or p > 500: raise ValueError("bad page")
    CACHE.mkdir(parents=True, exist_ok=True)
    f = CACHE / f"{_key(r)}-{p}.png"
    if not f.exists():
        _prune_cache()
        out = CACHE / f".{os.getpid()}-{threading.get_ident()}-{_key(r)}-{p}"   # each request thread its own name: two tabs, one page
        subprocess.run(["pdftoppm", "-r", "144", "-f", str(p), "-l", str(p), "-png", "-singlefile", str(HOME / r["path"]), str(out)],
                       capture_output=True, timeout=60, check=True)
        os.replace(str(out) + ".png", f)
    return f.read_bytes()


def _num(v):
    if not isinstance(v, (int, float)) or v != v or abs(v) > 1e5: raise ValueError("bad number")
    return round(float(v), 2)


def _clean_note(n):
    if not isinstance(n, dict): raise ValueError("bad note")
    kind = n.get("kind") if n.get("kind") in KINDS else "comment"
    text = str(n.get("text") or "").strip()[:4000]
    if not text: raise ValueError("empty note")
    rev = str(n.get("rev") or "")
    if not REV.match(rev): raise ValueError("bad revision")
    out = {"kind": kind, "rev": rev, "text": text}
    if n.get("p") is not None:
        p = int(n.get("p"))
        if p < 1 or p > 500: raise ValueError("bad page")
        out["p"] = p
        rects = n.get("rects") or []
        if not isinstance(rects, list): raise ValueError("bad rects")
        if len(rects) > MAX_RECTS: raise ValueError("selection too large: select less, or draw a box around it")
        out["rects"] = [[_num(v) for v in r] for r in rects if isinstance(r, list) and len(r) == 4]
        if n.get("box"): out["box"] = True
        q = str(n.get("quote") or "").strip()[:2000]
        if q: out["quote"] = q
    return out


def notes_do(d):
    """The page's changes to a document's notes: add, edit, delete, sent, done, reopen."""
    base = d.get("id") or ""
    if not BASE.match(base): raise ValueError("bad document id")
    do = d.get("do")
    with _Lock(base) as nd:
        ns = nd["notes"]
        by = {n.get("id"): n for n in ns}
        if do == "add":
            n = _clean_note(d.get("note") or {})
            if n["rev"] == "next":   # remember which pending build it was made on (the label "next" is reused by the next one)
                pend = _pending(base, _revisions().get(base) or {}, nd)
                if pend: n["revSha"] = pend["sha"]
            n.update(id=f"{base}-{nd['next']}", n=nd["next"], status="draft", made=time.strftime("%Y-%m-%dT%H:%M:%S"))
            nd["next"] += 1; ns.append(n); res = n
        elif do in ("edit", "delete", "done", "reopen"):
            n = by.get(d.get("note_id"))
            if not n: raise KeyError("no such note")
            if do == "edit":
                if n.get("status") != "draft": raise ValueError("a sent note can't be changed: add a new one")
                t = str(d.get("text") or "").strip()[:4000]
                if not t: raise ValueError("empty note")
                n["text"] = t
                if d.get("kind") in KINDS: n["kind"] = d["kind"]
            elif do == "delete": ns.remove(n)
            elif do == "done": n.update(status="addressed", addressedIn="by hand", addressedAt=time.strftime("%Y-%m-%dT%H:%M:%S"))
            else:
                n["status"] = "sent" if n.get("sentAt") else "draft"
                for k in ("addressedIn", "addressedAt"): n.pop(k, None)
            res = n
        elif do in ("sent", "stamp", "unsend"):
            # Send: "sent" marks the drafts before the post goes (so the agent the post starts sees them), "stamp" adds the post id
            # after, "unsend" puts them back to drafts if the post failed.
            ids = d.get("note_ids") or []
            if not isinstance(ids, list) or not ids: raise ValueError("no notes")
            post = str(d.get("post") or "")[:40]
            got = []
            for i in ids:
                n = by.get(i)
                if not n: continue
                if do == "sent" and n.get("status") == "draft":
                    n.update(status="sent", sentAt=time.strftime("%Y-%m-%dT%H:%M:%S"), post=post); got.append(i)
                elif do == "stamp" and n.get("status") in ("sent", "addressed") and not n.get("post"): n["post"] = post; got.append(i)
                elif do == "unsend" and n.get("status") == "sent" and not n.get("post"):
                    n["status"] = "draft"
                    for k in ("sentAt", "post"): n.pop(k, None)
                    got.append(i)
            res = {"ok": True, "ids": got}
        else:
            raise ValueError("unknown action")
        _save_notes(base, nd)
    return {"ok": True, "note": res, "notes": nd["notes"], "next": nd["next"]}


# The agent's side.

def cli_show(base):
    nd = load_notes(base)
    open_ = [n for n in nd["notes"] if n.get("status") == "sent"]
    drafts = sum(1 for n in nd["notes"] if n.get("status") == "draft")
    tail = f" ({drafts} more not sent yet: the person is still writing those, leave them)" if drafts else ""
    if not open_:
        print(f"{base}: no notes sent to you{tail}"); return
    if tail: print(f"{base}: notes sent to you{tail}")
    for n in open_:
        where = f"p.{n['p']}" if n.get("p") else "whole document"
        q = f' on "{n["quote"]}"' if n.get("quote") else ""
        if n.get("box"):
            box = (n.get("rects") or [[0, 0, 0, 0]])[0]
            where = "box, " + where + " at " + ",".join(str(int(v)) for v in box) + " pt"
            inside = _box_words(base, n, box)
            if inside: q = ' around "' + inside + chr(34)
        print(f"#{n['n']} [{n['kind']}, {n['status']}, rev {n['rev']}] {where}{q}: {n['text']}")


def _box_words(base, n, box):
    """The words inside a drawn box on the revision it was drawn on, so the agent needn't work them out from coordinates."""
    try:
        r, _ = _rev_of(base, n.get("rev") or "")
        pg = (_cached_words(r) or [])[n["p"] - 1] if r else None
    except (KeyError, IndexError, OSError, ValueError, TypeError):
        return ""
    if not pg: return ""
    ws = [w[4] for w in pg["words"] if (w[0] + w[2]) / 2 >= box[0] and (w[0] + w[2]) / 2 <= box[2] and (w[1] + w[3]) / 2 >= box[1] and (w[1] + w[3]) / 2 <= box[3]]
    t = " ".join(ws)
    return t if len(t) <= 600 else t[:599] + "…"


def cli_pending(base, pdf):
    p = _safe_pdf(pdf)
    if not p: sys.exit("not a PDF under home: " + pdf)
    with _Lock(base) as nd:
        nd["pending"] = {"path": str(p.relative_to(HOME)), "sha": _sha(p), "at": time.strftime("%Y-%m-%dT%H:%M:%S")}
        _save_notes(base, nd)
    print(f"{base}: the Documents page now shows {p.relative_to(HOME)} as the next revision")


def cli_addressed(base, nums, rev, sha=None):
    if not BASE.match(base or ""): sys.exit("bad document id: " + str(base))
    if rev is not None and not REV.match(rev): sys.exit("bad --rev (a revision letter like CM): " + rev)
    with _Lock(base) as nd:
        revs = _revisions().get(base) or {}
        pend = None if rev and rev != "next" else _pending(base, revs, nd)
        build = sha or (pend or {}).get("sha") if not rev or rev == "next" else None
        made = {x["n"]: x for x in _resolve_next(nd["notes"], revs, _pending(base, revs, nd))}
        own = []   # the real round: #47, #48 got `--rev DT` on notes made on DT; #28 kept the pending sha of the build it was made on
        for k in nums:
            n, m = next((x for x in nd["notes"] if x.get("n") == k), None), made.get(k)
            if not n or n.get("status") == "draft": continue
            on = {n.get("revSha"), (revs.get(m["rev"]) or {}).get("sha"), (revs.get(n.get("rev")) or {}).get("sha")} - {None}
            if (rev and rev != "next" and rev in (n.get("rev"), m["rev"])) or (build and build in on):
                own.append(k)
        if own:
            sys.exit(f"{base}: notes {own} were made on that revision, so it can't be the one that addressed them: pass the new build"
                     f" (--pdf path/to/new.pdf, rebuilt with the edit) or the later letter (--rev); nothing was marked")
        done, missing, unsent = [], [], []
        for k in nums:
            n = next((x for x in nd["notes"] if x.get("n") == k), None)
            if not n: missing.append(k); continue
            if n.get("status") == "draft": unsent.append(k); continue
            n.update(status="addressed", addressedIn=rev or "next", addressedAt=time.strftime("%Y-%m-%dT%H:%M:%S")); done.append(k)
            if (rev or "next") == "next" and (sha or pend): n["addressedSha"] = sha or pend["sha"]   # which build "next" was, for the letter once filed
            # (--pdf gives it even when the library already filed that PDF and the pending mark is gone)
        if not rev and not pend and not sha and done:
            print(f"{base}: no pending build is set, so these show as addressed in the first revision the library files from now"
                  f" (pass --pdf NEW.pdf, or --rev)", file=sys.stderr)
        _save_notes(base, nd)
        left = [x["n"] for x in nd["notes"] if x.get("status") == "sent"]
    print(f"{base}: addressed {done or 'none'}" + (f"; no such note {missing}" if missing else "")
          + (f"; not sent to you yet, left alone {unsent}" if unsent else "") + f"; still open {left or 'none'}")


def cli_check():
    """Every library document as the page opens it: the file is there, pdftotext gives pages, page 1 renders. Prints one line per
    problem and a total; exits 1 if any document fails. A document with no selectable text is named but passes (boxes still work)."""
    rows, bad, flat = documents()["docs"], [], []
    for r in rows:
        try:
            d = doc(r["id"])
            if not d["pages"]: bad.append(f"{r['id']} rev {d['rev']}: no pages from pdftotext ({d['path']})"); continue
            if not any(p["words"] for p in d["pages"]): flat.append(r["id"])
            if page_png(r["id"], d["rev"], 1)[:8] != b"\x89PNG\r\n\x1a\n": bad.append(f"{r['id']} rev {d['rev']}: page 1 is not a PNG")
        except Exception as e:
            bad.append(f"{r['id']}: {type(e).__name__}: {str(e)[:160]}")
    for b in bad: print("FAIL", b)
    if flat: print("no selectable text (boxes only):", " ".join(flat))
    print(f"{len(rows) - len(bad)} of {len(rows)} documents open")
    if bad: sys.exit(1)


SRC_EXT = (".tex", ".md", ".html", ".typ", ".rst", ".txt")
SRC_SKIP = {"node_modules", ".git", "__pycache__", ".venv", "venv", "dist", "build", "replay", "runs", "fixtures", "home"}   # home: fixture homes

_SRC_WALK = {}   # root -> (time, [(dir, [source and pdf files])]): one walk of projects/ serves every lookup for a minute


def _src_index(root, ttl=60):
    """(sorted names, {name: [(dir, file, ext)]}) of the source and PDF files under root, skipping SRC_SKIP folders."""
    got = _SRC_WALK.get(str(root))
    if got and time.time() - got[0] < ttl: return got[1]
    by = {}
    for d, dirs, files in os.walk(root):
        dirs[:] = [x for x in dirs if x not in SRC_SKIP]
        for f in files:
            fs, ext = os.path.splitext(f)
            if ext in SRC_EXT or ext == ".pdf": by.setdefault(fs, []).append((d, f, ext))
    idx = (sorted(by), by)
    _SRC_WALK[str(root)] = (time.time(), idx)
    return idx


def find_source(base, root=None):
    """Likely sources of a document's newest revision: files under projects/ whose name matches the PDF's (the exact stem, or the stem
    without a trailing revision letter, then variants of it like `_AI-human4`), exact names first, then newest first. A copy beside the
    PDF in the library outbox comes last: it is what was filed, not where to edit."""
    revs = _revisions().get(base) or {}
    if not revs: return None, []
    last = revs[sorted(revs, key=_revkey)[-1]]
    pdf = HOME / last["path"]
    stem = pdf.stem
    short = re.sub(r"[_-][A-Z]{1,3}$", "", stem)
    root = Path(root) if root else HOME / "projects"
    hits, beside = [], []
    names, by = _src_index(root)
    want = {stem, short}
    if len(short) >= 6:   # variants of the name: short + "-" or "_" + anything
        i = bisect.bisect_left(names, short)
        while i < len(names) and names[i].startswith(short):
            if names[i][len(short):len(short) + 1] in ("-", "_"): want.add(names[i])
            i += 1
    for fs in want:
        for d, f, ext in by.get(fs, ()):
            if ext == ".pdf":
                if fs in (stem, short): beside.append(Path(d))
                continue
            p = Path(d) / f
            try: hits.append((fs not in (stem, short), -p.stat().st_mtime, str(p.relative_to(HOME))))
            except (OSError, ValueError): pass
    words = _pdf_words(pdf)
    if words and len(hits) > 1:   # the file holding most of the PDF's words first: the resume has a dozen same-named variants
        hits = [(-round(_overlap(words, HOME / h[2]), 2),) + h for h in hits]
    hits.sort()
    out = [h[-1] for h in hits]
    if not out:   # no file by that name: a source beside a copy of the PDF in a project (projects/resume/asic has VLSI.pdf and one .tex)
        for d in beside:
            out += [str(f.relative_to(HOME)) + f"  (beside a copy of {pdf.name}; check it builds this PDF)"
                    for f in sorted(d.iterdir()) if f.suffix in SRC_EXT and f.is_file()]
    for f in sorted(pdf.parent.glob(stem + ".*")) if pdf.parent.is_dir() else []:
        if f.suffix in SRC_EXT: out.append(str(f.relative_to(HOME)) + "  (the copy filed with the PDF)")
    return last, out


def _pdf_words(pdf):
    try: t = subprocess.run(["pdftotext", "-q", str(pdf), "-"], capture_output=True, text=True, timeout=30).stdout
    except (OSError, subprocess.SubprocessError): return set()
    return {w for w in re.findall(r"[a-z]{4,}", t.lower())}


def _overlap(words, path):
    try: t = path.read_text(errors="replace")[:400000].lower()
    except OSError: return 0.0
    return len(words & set(re.findall(r"[a-z]{4,}", t))) / len(words)


def cli_source_all():
    rows = documents()["docs"]
    miss = [r["id"] for r in rows if not [o for o in find_source(r["id"])[1] if "copy filed" not in o]]
    print(f"{len(rows) - len(miss)} of {len(rows)} documents have a source found")
    if miss: print("none found:", " ".join(miss)); sys.exit(1)


def cli_source(base):
    if base == "--all": return cli_source_all()
    if not BASE.match(base or ""): sys.exit("bad document id: " + str(base))
    last, out = find_source(base)
    if not last: sys.exit(f"{base}: not in the library ledger")
    print(f"{base}-{last['rev']}: {last['path']}")
    for o in out[:8]: print("  " + o)
    if not out:
        print("  no source found by name; look for the PDF's name in the project that made it (grep -rl), or ask in the reply")
        sys.exit(1)


def cli_md_pdf(md, out):
    """Print a markdown source to PDF exactly as the library does for a filed .md (hive-library md_pdf: markdown-it, headless Chrome).
    File that PDF, not the .md: the library would print the .md again and those bytes differ from what `addressed --pdf` recorded."""
    import importlib.util
    if not md.endswith(".md") or not out.endswith(".pdf"): sys.exit("md-pdf SRC.md OUT.pdf")
    spec = importlib.util.spec_from_file_location("h2_library", Path(__file__).resolve().parent.parent / "library/library.py")
    lib = importlib.util.module_from_spec(spec); spec.loader.exec_module(lib)
    lib.md_pdf(os.path.abspath(md), os.path.abspath(out))
    print(out)


def _norm(t):
    return re.sub(r"\W+", " ", t).lower().strip()


def _note_words(r, n):
    """The words a note points at on revision r: inside its box (or its rects), or its quote."""
    if n.get("quote"): return n["quote"]
    try: pg = (_cached_words(r) or [])[int(n.get("p") or 0) - 1] if n.get("p") else None
    except (IndexError, OSError, ValueError): return ""
    if not pg or not n.get("rects"): return ""
    out = []
    for w in pg["words"]:
        cx, cy = (w[0] + w[2]) / 2, (w[1] + w[3]) / 2
        if any(b[0] <= cx <= b[2] and b[1] <= cy <= b[3] for b in n["rects"]): out.append(w[4])
    return " ".join(out)


_MARKS_JS = r"""
var doc=Document.openDocument(scriptArgs[0]);var out=[];
for(var p=0;p<doc.countPages();p++){var pg=doc.loadPage(p);var L=[],S=[],cur=null;
 var ls=pg.getLinks();for(var i=0;i<ls.length;i++){var b=ls[i].getBounds();L.push([b[0],b[1],b[2],b[3],ls[i].getURI()||""]);}
 pg.toStructuredText().walk({onChar:function(c,o,font,size,q){var n=font.getName();
   if(!cur||cur[4]!=n||Math.abs(cur[1]-Math.min(q[1],q[3]))>2){cur=[q[0],Math.min(q[1],q[3]),q[6],Math.max(q[5],q[7]),n,""];S.push(cur);}
   cur[5]+=c;cur[2]=Math.max(cur[2],q[2],q[6]);},endLine:function(){cur=null;}});
 out.push({links:L,spans:S});}
print(JSON.stringify(out));
"""


def _marks(r):
    """Per page {links: [[x0, y0, x1, y1, uri]], spans: [[x0, y0, x1, y1, font, text]]} from mutool, cached by the file's sha:
    what words alone miss (a link, an icon glyph, bold). [] when mutool is missing or fails, so verify falls back to words."""
    CACHE.mkdir(parents=True, exist_ok=True)
    f = CACHE / f"{_key(r)}.marks.json"
    m = _jload(f, None)
    if m is None:
        js = _tmp(CACHE / "marks.js"); js.write_text(_MARKS_JS)
        try:
            x = subprocess.run(["mutool", "run", str(js), str(HOME / r["path"])], capture_output=True, text=True, timeout=60)
            m = json.loads(x.stdout) if x.returncode == 0 else []
        except (OSError, subprocess.SubprocessError, ValueError):
            m = []
        finally:
            js.unlink(missing_ok=True)
        if m: t = _tmp(f); t.write_text(json.dumps(m)); os.replace(t, f)
    return m


def _mark_lines(pg):
    """A page's mutool spans grouped into lines: [[y0, y1, [span...]]] top to bottom."""
    lines = []
    for sp in sorted(pg["spans"], key=lambda s: (round(s[1]), s[0])):
        if lines and abs(lines[-1][0] - sp[1]) <= 2: lines[-1][2].append(sp)
        else: lines.append([sp[1], sp[3], [sp]])
    return lines


def _sig(pg, sps):
    """Links, icon glyphs and bold words on a run of spans: (uris, icons, bold words)."""
    y0, y1 = min(s[1] for s in sps) - 1, max(s[3] for s in sps) + 1
    uris = sorted({l[4] for l in pg["links"] if l[1] < y1 and l[3] > y0})
    icons = sum(1 for s in sps if "Awesome" in s[4] and s[5].strip() not in ("○", "•", ""))
    return (uris, icons, _norm(" ".join(s[5] for s in sps if "Bold" in s[4])))


def _text_marks(r, t, p=None, band=None):
    """Signatures of every run of up to 6 lines of revision r that starts where the words t (normalized) start; with p and band
    (y0, y1), only runs on page p whose lines sit in that band (a note's box)."""
    out = []
    for k, pg in enumerate(_marks(r), 1):
        if p and k != p: continue
        lines = _mark_lines(pg)
        if band: lines = [ln for ln in lines if band[0] <= (ln[0] + ln[1]) / 2 <= band[1]]
        norm = [_norm(" ".join(s[5] for s in ln[2])) for ln in lines]
        for i in range(len(lines)):
            acc = ""
            for j in range(i, min(i + 6, len(lines))):
                acc = (acc + " " + norm[j]).strip()
                if t in acc:
                    if j == i or t not in acc[len(norm[i]):].strip(): out.append(_sig(pg, [s for ln in lines[i:j + 1] for s in ln[2]]))
                    break
                if len(acc) > len(t) + 400: break
    return out


def _note_marks(r, n, t):
    """The signature of the note's words t inside its box on revision r, or None (no box, words not found there, no mutool)."""
    if not n.get("p") or not n.get("rects"): return None
    try: m = _text_marks(r, t, int(n["p"]), (min(b[1] for b in n["rects"]) - 2, max(b[3] for b in n["rects"]) + 2))
    except (TypeError, ValueError, IndexError): return None
    return m[0] if m else None


def verify(base):
    """Each addressed note against the revision it says addressed it: {same: [(n, rev)], unchanged: [(n, rev, from, words)],
    changed: [n], unknown: [(n, why)]}. "same" is a wrong letter (a note can't be addressed in the revision it was made on);
    "unchanged" means the words the note points at are still there word for word, with the same links, icons and bold on their
    lines, which spacing or a question can rightly leave, so those are for a look, not an error; a note marked `looked` (someone
    saw the change by eye: `docs.py looked`) goes to "looked" instead."""
    if not BASE.match(base or ""): raise ValueError("bad document id")
    revs = _revisions().get(base) or {}
    nd = load_notes(base)
    pend = _pending(base, revs, nd)
    allr = dict(revs, **({"next": pend} if pend else {}))
    res = {"same": [], "unchanged": [], "changed": [], "unknown": [], "looked": []}
    text = {}
    for n in _resolve_next(nd["notes"], revs, pend):
        if n.get("status") != "addressed": continue
        a, o = n.get("addressedIn"), n.get("rev")
        if a and a == o and a != "next":
            res["same"].append((n["n"], a)); continue
        if a not in allr or o not in allr:
            res["unknown"].append((n["n"], f"rev {o} -> {a}")); continue
        t = _norm(_note_words(allr[o], n))
        if len(t) < 3:
            res["unknown"].append((n["n"], "no words under the note")); continue
        if a not in text: text[a] = _norm(" ".join(w[4] for pg in (_cached_words(allr[a]) or []) for w in pg["words"]))
        if t not in text[a]: res["changed"].append(n["n"]); continue
        mo = _note_marks(allr[o], n, t)
        ma = _text_marks(allr[a], t) if mo is not None else []
        if ma and mo not in ma: res["changed"].append(n["n"]); continue   # same words, but a link, icon or bold changed
        if n.get("looked"): res["looked"].append(n["n"]); continue
        res["unchanged"].append((n["n"], a, o, t))
    return res


def cli_looked(base, nums, why=""):
    """Mark addressed notes as checked by eye (the change is there but words, links, icons and bold don't show it: spacing, an
    answered question), so verify stops listing them."""
    if not BASE.match(base or ""): sys.exit("bad document id: " + str(base))
    with _Lock(base) as nd:
        done, bad = [], []
        for k in nums:
            n = next((x for x in nd["notes"] if x.get("n") == k), None)
            if not n or n.get("status") != "addressed": bad.append(k); continue
            n["looked"] = {"at": time.strftime("%Y-%m-%dT%H:%M:%S"), **({"why": why} if why else {})}; done.append(k)
        _save_notes(base, nd)
    print(f"{base}: looked at {done or 'none'}" + (f"; not addressed or no such note, left alone {bad}" if bad else ""))


def cli_verify(base):
    if not BASE.match(base or ""): sys.exit("bad document id: " + str(base))
    r = verify(base)
    tot = sum(len(v) for v in r.values())
    print(f"{base}: {tot} addressed notes: {len(r['changed'])} changed, {len(r['unchanged'])} words unchanged, "
          f"{len(r['same'])} name the revision they were made on, {len(r['unknown'])} can't tell, {len(r['looked'])} looked at by eye")
    for k, a in r["same"]:
        print(f"  #{k}: addressed in rev {a}, the revision it was made on: the fix went into a later build; set it with"
              f" `docs.py addressed {base} {k} --rev <that letter>`")
    for k, a, o, t in r["unchanged"]:
        print(f'  #{k}: "{t[:70]}" reads the same in rev {a} as in rev {o}: fine for spacing or a question; look, then `docs.py looked {base} {k}` or reopen it')
    for k, why in r["unknown"]:
        print(f"  #{k}: can't tell ({why})")
    if r["same"]: sys.exit(1)


def main(a):
    if len(a) >= 2 and a[0] == "show": return cli_show(a[1])
    if len(a) == 3 and a[0] == "pending": return cli_pending(a[1], a[2])
    if len(a) >= 3 and a[0] == "addressed":
        rev = None
        if "--rev" in a:
            i = a.index("--rev")
            if i + 1 >= len(a): sys.exit("--rev needs a revision letter, like --rev CM")
            rev = a[i + 1]; a = a[:i] + a[i + 2:]
        pdf = None
        if "--pdf" in a:   # one step: record the new build and mark the notes in it (the person's round: agents skipped `pending`)
            i = a.index("--pdf")
            if i + 1 >= len(a): sys.exit("--pdf needs the new PDF, like --pdf path/to/new.pdf")
            pdf = a[i + 1]; a = a[:i] + a[i + 2:]
        try: nums = [int(x.lstrip("#")) for x in a[2:]]
        except ValueError: sys.exit("note numbers, like: addressed 006-0002 1 3")
        if pdf and not BASE.match(a[1] or ""): sys.exit("bad document id: " + str(a[1]))
        if pdf: cli_pending(a[1], pdf)
        return cli_addressed(a[1], nums, rev, _sha(_safe_pdf(pdf)) if pdf else None)
    if a == ["check"]: return cli_check()
    if len(a) == 2 and a[0] == "source": return cli_source(a[1])
    if len(a) == 3 and a[0] == "md-pdf": return cli_md_pdf(a[1], a[2])
    if len(a) == 2 and a[0] == "verify": return cli_verify(a[1])
    if len(a) >= 3 and a[0] == "looked":
        why = ""
        if "--why" in a:
            i = a.index("--why"); why = " ".join(a[i + 1:i + 2]); a = a[:i] + a[i + 2:]
        try: return cli_looked(a[1], [int(x.lstrip("#")) for x in a[2:]], why)
        except ValueError: sys.exit("note numbers, like: looked 006-0002 21 30 --why 'spacing'")
    if a and a[0] == "list":
        for r in documents()["docs"]: print(r["id"], r["rev"], r["title"], f"drafts={r['drafts']} open={r['open']}")
        return
    sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
