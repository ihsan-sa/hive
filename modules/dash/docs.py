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
"""
import fcntl, hashlib, json, os, re, subprocess, sys, threading, time
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
            "path": r["path"], "sha": _key(r), "pages": _cached_words(r), "notes": _resolve_next(notes["notes"], revs, pend), "next": notes["next"]}


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
        pend = None if rev and rev != "next" else _pending(base, _revisions().get(base) or {}, nd)
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
    if a and a[0] == "list":
        for r in documents()["docs"]: print(r["id"], r["rev"], r["title"], f"drafts={r['drafts']} open={r['open']}")
        return
    sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
