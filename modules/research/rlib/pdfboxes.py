"""Org charts read by position. A line-by-line PDF reading runs side-by-side boxes into one line, so a box's post lands on its neighbour's name
(Montgomery College's chart gave Deidre Price the post from the box under hers, fnwords43). This groups words into boxes from where they sit
(`pdftotext -bbox`) and writes each box as "Name" then its post, the way a staff list reads. Only pages that look like an org chart are rewritten:
on a table or plain prose the layout reading is better, and the caller keeps it."""
import html as _html, re, unicodedata
from . import people

_WORD = re.compile(r'<word xMin="([-\d.]+)" yMin="([-\d.]+)" xMax="([-\d.]+)" yMax="([-\d.]+)">(.*?)</word>', re.S)
_NAME_LINE = re.compile(rf"{people.HONOR}{people.LINE_NAME}{people.SUFFIX}")
_HONOUR = re.compile(r"(?:Dr|Prof|Mr|Ms|Mrs|Mx)\.?\s")
_ACTING = re.compile(r"^(?:Interim|Acting)\s+", re.I)
_ROLE_END = re.compile(r"\b" + people.ROLE + r"$", re.I)


def pages(xml):
    """[[(x0, y0, x1, y1, word), ...] per page] from `pdftotext -bbox` output."""
    return [[(float(a), float(b), float(c), float(d), unicodedata.normalize("NFKC", _html.unescape(t))) for a, b, c, d, t in _WORD.findall(pg)]
            for pg in re.findall(r"<page[^>]*>(.*?)</page>", xml, re.S)]


def _frag(ws):
    return dict(x0=min(w[0] for w in ws), x1=max(w[2] for w in ws), y0=min(w[1] for w in ws), y1=max(w[3] for w in ws), t=" ".join(w[4] for w in ws))


def fragments(words):
    """Words on one baseline, cut where the gap is wider than a word space: two boxes side by side are two fragments."""
    rows = []
    for w in sorted(words, key=lambda w: ((w[1] + w[3]) / 2, w[0])):
        cy, h = (w[1] + w[3]) / 2, w[3] - w[1]
        for r in rows:
            if abs(r[0] - cy) < h * 0.3: r[1].append(w); break
        else: rows.append((cy, [w]))
    out = []
    for _, ws in rows:
        ws.sort(key=lambda w: w[0])
        cur = [ws[0]]
        for w in ws[1:]:
            if w[0] - cur[-1][2] > 0.4 * max(w[3] - w[1], cur[-1][3] - cur[-1][1]): out.append(_frag(cur)); cur = [w]
            else: cur.append(w)
        out.append(_frag(cur))
    return out


def columns(frags):
    """Fragments stacked under each other (close below, overlapping across) chain into one column; boxes stacked tight in a column share one."""
    chains = []
    for f in sorted(frags, key=lambda f: (f["y0"], f["x0"])):
        h, best = f["y1"] - f["y0"], None
        for c in chains:
            g = c[-1]
            pitch, gap = f["y0"] - g["y0"], f["y0"] - g["y1"]
            over = min(f["x1"], g["x1"]) - max(f["x0"], g["x0"])
            # A name sits further below its post than one line of a post below the next: "Chief of Staff" .. "Dr. Stephen Cain" is 1.3 lines down.
            room = 1.6 if f["n"] and not g["n"] else 0.8
            if 0 < pitch < 3 * h and gap < room * h and over > 0.5 * min(f["x1"] - f["x0"], g["x1"] - g["x0"]):
                if best is None or pitch < best[0]: best = (pitch, c)
        if best: best[1].append(f)
        else: chains.append([f])
    return chains


def name_line(t, honour=False):
    """The person a box line names, or "". With `honour`, only a line with Dr./Mr./Ms. counts: on a chart that titles its people,
    "Environmental Safety" and "Auxillary Enterprises" are lines of a post."""
    t = _ACTING.sub("", re.sub(r"\s*\([^)]*\)", "", t).strip())  # Ms. Elizabeth (Liz) Greaney; Interim Dr. Margo Vargas-Ayala
    if t.lower() == "vacant": return "Vacant"
    if not _NAME_LINE.fullmatch(t) or (honour and not _HONOUR.match(t)): return ""
    n = people.clean_name(t)
    return n if people.plausible_name(n) and not people._ROLE_RE.search(n) else ""


def mark_names(frags):
    """Set f["n"] on each fragment. A page where most name lines carry an honorific needs one on all."""
    found = [f["t"] for f in frags if name_line(f["t"]) not in ("", "Vacant")]
    honour = len(found) >= 3 and sum(bool(_HONOUR.match(t)) for t in found) >= 0.6 * len(found)
    for f in frags: f["n"] = name_line(f["t"], honour)
    return frags


def boxes(chains):
    """Cut each column at its names: one name to a box. Whether the name ends a box or opens it is read off the page (most columns start or end on one)."""
    for c in chains:  # "Assistant Director of" / "Educational Technologies": a line that finishes the post above isn't a name
        for g, f in zip(c, c[1:]):
            if f["n"] and re.search(r"(?:\b(?:of|for|and|the|in|to)|[&,/-])$", g["t"].strip(), re.I): f["n"] = ""
    ends = sum(bool(c[-1]["n"]) for c in chains if len(c) > 1)
    starts = sum(bool(c[0]["n"]) for c in chains if len(c) > 1)
    out = []
    for c in chains:
        cur = []
        for f in c:
            if f["n"] and starts > ends and cur: out.append(cur); cur = []
            cur.append(f)
            if f["n"] and starts <= ends: out.append(cur); cur = []
        if cur: out.append(cur)
    return out


def _post(lines):
    """A box's lines are one post wrapped to its width; only a first line that is a whole role ("Dean", "Senior Vice President") stands apart from the unit under it."""
    lines = [t.strip() for t in lines if t.strip()]
    if not lines: return ""
    head, rest = lines[0], " ".join(lines[1:])
    if not rest: return head
    return head + (", " if _ROLE_END.search(head) and not rest[:1].islower() and not head.endswith(("&", ",", "/")) else " ") + rest


def _box_text(b):
    names = [f["n"] for f in b]
    if sum(bool(n) for n in names) != 1: return "\n".join(f["t"] for f in b)
    name, line = next((n, f["t"]) for f, n in zip(b, names) if n)
    post = _post(f["t"] for f, n in zip(b, names) if not n)
    acting = _ACTING.match(line.strip())
    if acting and post: post = acting.group(0).strip().title() + " " + post
    if name == "Vacant": return post + "\nVacant" if post else "Vacant"
    return name + ("\n" + post if post else "")


def is_org_chart(bs):
    """Several boxes holding one name and a post, at least two of them side by side."""
    good = [b for b in bs if len(b) > 1 and sum(bool(f["n"]) for f in b) == 1 and any(not f["n"] and people._ROLE_RE.search(f["t"]) for f in b)]
    if len(good) < 3: return False
    multi = [b for b in bs if len(b) > 1]
    if len(good) * 2 < len(multi): return False
    span = [(min(f["y0"] for f in b), max(f["y1"] for f in b)) for b in good]
    return any(a[0] < b[1] and b[0] < a[1] for i, a in enumerate(span) for b in span[i + 1:])


def page_text(words):
    """The page as boxes, or None when it doesn't read as an org chart."""
    if not words: return None
    bs = boxes(columns(mark_names(fragments(words))))
    if not is_org_chart(bs): return None
    bs.sort(key=lambda b: (round(min(f["y0"] for f in b) / 8), min(f["x0"] for f in b)))
    # A name alone in its box (staff under a head, no post of its own) is left out: written down, it takes the post of the box after it.
    return "\n\n".join(_box_text(b) for b in bs if not (len(b) == 1 and b[0]["n"] and b[0]["n"] != "Vacant"))


def org_chart_pages(xml):
    """{page index: box text} for the pages of a `pdftotext -bbox` reading that are org charts."""
    out = {}
    for i, ws in enumerate(pages(xml)):
        t = page_text(ws)
        if t: out[i] = t
    return out
