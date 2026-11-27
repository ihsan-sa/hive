#!/usr/bin/env python3
"""Share this result: one conversation as one self-contained HTML page its owner can post anywhere.

  share.py CONVERSATION [-o FILE] [--remove ID,ID] [--no-files] [--title TEXT]

The page holds the ask, a short replay of the agents working, the result and its files, and a small footer.
Nothing is uploaded: this only reads the board and the log and writes a file. Everything that goes into the page is
scrubbed first (paths, hostnames, emails, tokens, addresses, tool output); the app shows a preview with a remove button
per message, and `remove` here is the same list. Used by dash.py (/api/share-preview, /api/share-build) and by `hive share`."""
import argparse, base64, csv, datetime as dt, html, io, json, mimetypes, os, re, sys
from pathlib import Path

HOME = Path(os.environ.get("HIVE_HOME") or os.environ.get("HIVE_HOME") or Path.home())
SYS = {"starter", "anon", "all", "mention", "name", "box", "autobox", "warden", "rootwarden", "hives", "h0", "h1"}
FOOT = "Made by a Hive: hivelab.ca"
MAX_FILE, MAX_FILES_TOTAL, MAX_TEXT = 600_000, 4_000_000, 4000
EMBED = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml"}
TEXTY = {".md", ".txt", ".csv", ".json", ".log", ".py", ".js", ".html", ".css", ".sh", ".yaml", ".yml", ".toml"}

# What never goes out. The first five mirror dist/scan_private.py's kinds; the rest are what a conversation holds that a release doesn't.
RULES = [
    (re.compile(r"sk-ant-[\w-]{12,}|sk-or-[\w-]{12,}|\bgh[pousr]_[A-Za-z0-9]{20,}|github_pat_\w{20,}|\bxox[abeprs]-[\w-]{10,}|\bxapp-[\w-]{10,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z ]*PRIVATE KEY[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)|\beyJ[\w-]{15,}\.[\w-]{10,}\.[\w-]{10,}"), "[secret removed]"),
    (re.compile(r"(?i)\b(password|passwd|secret|token|api[_-]?key)\b(\s*[:=]\s*)\S+"), r"\1\2[removed]"),
    (re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[a-zA-Z]{2,}"), "[email removed]"),
    (re.compile(r"\b(?:100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])|192\.168\.\d{1,3}|10\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3})\.\d{1,3}\b|\b\d{1,3}(?:\.\d{1,3}){3}\b"), "[address removed]"),
    (re.compile(r"\b[UCDGWTB](?=[A-Z0-9]*\d)(?=[A-Z0-9]*[A-Z])[A-Z0-9]{8,12}\b"), "[id removed]"),
    # paths: a home folder, ~/anything, or any absolute path of three parts
    (re.compile(r"(?:/home|/Users|/root|/mnt/[a-z]|/var|/etc|/opt|/srv|/tmp|[A-Z]:\\Users)[^\s\"'`)>\]]*|~/[^\s\"'`)>\]]*|(?<![\w:/.])/(?:[\w.-]+/){2,}[\w.-]*"), "[path]"),
    # hostnames and links other than the Hive's own site; a link's text is kept, its address is not
    (re.compile(r"(?i)\bhttps?://(?!(?:www\.)?hivelab\.ca\b)[^\s)>\]\"']+"), "[link removed]"),
    (re.compile(r"(?i)\b(?!hivelab\.ca\b)(?:[a-z0-9-]+\.)+(?:com|ca|net|org|io|dev|app|ai|co|cc|me|sh|local|lan|internal|ts\.net|xyz|cloud)\b"), "[host removed]"),
    (re.compile(r"(?i)\b(?:ssh|scp|rsync)\s+[^\n`]*"), "[command removed]"),
    # the machine's own names
    (re.compile(r"\b(?:the )?" + "tow" + "er\\b|\\b" + "ii" + "ks\\d\\b", re.I), "the machine"),
    (re.compile(r"\bH[0-" + "2]\\b"), "a Hive"),
    (re.compile(r"\(from \[email removed\][^)]*\)\s*"), ""),
    (re.compile(r"\b" + "h" + "2 (?=[a-z])"), "hive "),
]
FENCE = re.compile(r"```[^\n]*\n([\s\S]*?)```")
# What the chat draws (a page, a chart, a drawing, a table) is the result, not tool output: it stays whole and is drawn on the page.
VISUAL = {"html", "svg", "chart", "csv", "plot", "mermaid"}
VFENCE = re.compile(r"(?m)^(`{3,})[ \t]*(html|svg|chart|csv|plot|mermaid)(?=[ \t]|$)([^\n]*)\n([\s\S]*?)\n?^\1[ \t]*$")
MAX_VIS, MAX_VIS_MSG = 150_000, 400_000


def _rules(t, skip=()):
    for rx, sub in RULES:
        if rx not in skip: t = rx.sub(sub, t)
    return t


HOST = next(rx for rx, _ in RULES if "host removed" in _)
NAMES = {rx for rx, sub in RULES if sub in ("the machine", "a Hive", "hive ")}   # words, not secrets: harmless to skip inside markup and code
MARKUP = re.compile(r"(?is)(<(script|style)\b[^>]*>)(.*?)(</\2\s*>)|<[^>]*>")


def _page_body(body):
    """Scrub a page or drawing without breaking it: words are scrubbed in full; tags keep h1/hive and code keeps a.b names.
    None when something private would survive (spelled as entities, say): then the page shows as scrubbed source."""
    out, pos = [], 0
    for m in MARKUP.finditer(body):
        out.append(_rules(body[pos:m.start()]))
        if m.group(2):
            # code: a.b is a name, not a host, so host names are taken out only inside quoted strings
            code = re.sub(r"(\"|'|`)((?:\\.|(?!\1).)*)\1", lambda q: q.group(1) + HOST.sub("[host removed]", q.group(2)) + q.group(1), m.group(3), flags=re.S)
            out.append(_rules(m.group(1), NAMES) + _rules(code, NAMES | {HOST}) + m.group(4))
        else: out.append(_rules(m.group(0), NAMES | {HOST}) if not HOST.search(re.sub(r"(?i)\b(?:xmlns(?::\w+)?|href)=[\"']https?://www\.w3\.org[^\"']*[\"']", "", m.group(0))) else _rules(m.group(0), NAMES))
        pos = m.end()
    out.append(_rules(body[pos:]))
    t = "".join(out)
    t = re.sub(r"(?is)<meta[^>]+http-equiv\s*=\s*[\"']?refresh[^>]*>", "", t)
    plain = html.unescape(t)   # text spelled as &#64; or &#46; is read the way the browser reads it
    if _rules(plain, NAMES | {HOST}) != plain: return None
    return t


def scrub(text):
    """The text with everything private taken out. Long code blocks are tool output: they go whole, short ones stay.
    A block the chat draws (html, svg, chart, csv, plot, mermaid) stays whole, scrubbed inside like the words around it."""
    keep, used = [], [0]
    def hold(m):
        body, lang, fence = m.group(4), m.group(2), m.group(1)
        if len(body) > MAX_VIS or used[0] + len(body) > MAX_VIS_MSG: keep.append(f"[a {lang} block too big for the page]")
        else:
            used[0] += len(body)
            safe = _page_body(body) if lang in ("html", "svg") else _rules(body)
            keep.append(f"{fence}{lang}{_rules(m.group(3))}\n{safe}\n{fence}" if safe is not None else f"{fence}\n{_rules(body)}\n{fence}")
        return f"\x00V{len(keep) - 1}\x00"
    t = VFENCE.sub(hold, str(text or "").replace("\x00", "").replace("\r\n", "\n"))
    t = FENCE.sub(lambda m: m.group(0) if m.group(1).count("\n") <= 8 and len(m.group(1)) < 700 else "[tool output removed]", t)
    t = _rules(t)
    return re.sub(r"\x00V(\d+)\x00", lambda m: keep[int(m.group(1))] if int(m.group(1)) < len(keep) else "", t)


def _cut(text, n=MAX_TEXT):
    """At most n characters of words; a drawn block never counts toward n and is never cut in half."""
    out, left, pos, text = [], n, 0, text.replace("\r\n", "\n")
    for m in list(VFENCE.finditer(text)) + [None]:
        prose = text[pos:m.start() if m else len(text)]
        if len(prose) > left:
            out.append(prose[:left].rsplit(" ", 1)[0] + " …"); return "".join(out)
        out.append(prose); left -= len(prose)
        if m is None: break
        out.append(m.group(0)); pos = m.end()
    return "".join(out)


def _t(s):
    try: return dt.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
    except ValueError: return None


def _read(path, thread=None, key="thread"):
    out = []
    try:
        for ln in open(path, errors="replace"):
            if thread and f'"{thread}"' not in ln: continue
            try: o = json.loads(ln)
            except ValueError: continue
            if not thread or o.get(key) == thread: out.append(o)
    except OSError: pass
    return out


def _short(t, n=70):
    """A title from the ask: whole words, with an ellipsis if cut."""
    t = t.strip(" .:")
    return t if len(t) <= n else t[:n].rsplit(" ", 1)[0].rstrip(" ,;:-") + "…"


def preview(thread, home=HOME, title=None):
    """What would go on the page: the conversation as plain scrubbed items the owner can take messages out of."""
    posts, runs = _read(home / "board.jsonl", thread), [e for e in _read(home / "log.jsonl", thread) if e.get("event") == "start"]
    if not posts: raise KeyError(thread)
    items, files = [], []
    for p in posts:
        who = p.get("from") or "?"
        if who in SYS: continue
        text = scrub(re.sub(r"^(?:@\S+\s+)+", "", str(p.get("text") or ""))).strip()
        text = _cut(text)
        fs = []
        for f in p.get("files") or []:
            name = os.path.basename(str(f.get("name") or f.get("path") or "file"))
            fs.append({"name": scrub(name), "size": f.get("size", 0), "post": p["id"], "path": f.get("path")})
        if not text and not fs: continue
        items.append({"id": p["id"], "at": p["at"], "who": "asker" if who == "person" else who, "text": text, "files": [dict(f, path=None) for f in fs]})
        files += fs
    first = next((i for i in items if i["who"] == "asker"), items[0])
    agents = []
    for e in runs:
        n = e.get("name")
        if n and n not in SYS and not any(a["name"] == n for a in agents):
            agents.append({"name": n, "by": "asker" if e.get("from") == "person" else e.get("from"), "at": e.get("at")})
    for i in items:
        if i["who"] != "asker" and not any(a["name"] == i["who"] for a in agents): agents.append({"name": i["who"], "by": None, "at": i["at"]})
    known = {a["name"] for a in agents}
    for a in agents:
        if a["by"] not in known and a["by"] != "asker": a["by"] = None
    ts = [x for x in (_t(i["at"]) for i in items) if x]
    secs = int((max(ts) - min(ts)).total_seconds()) if ts else 0
    t = title or _short(scrub(re.sub(r"\s+", " ", first["text"]))) or "A Hive at work"
    return {"thread": thread, "title": scrub(t), "ask": first["text"], "ask_id": first["id"], "items": items, "agents": agents,
            "files": [{k: v for k, v in f.items() if k != "path"} for f in files], "seconds": secs,
            "notice": "Everything on this page becomes public if you post it. Paths, links, emails, addresses and long tool output are already taken out; read it and remove anything else."}



# ---- the page ----
def esc(s): return html.escape(str(s), quote=True)


def md(text):
    """Small markdown: paragraphs, lists, bold, code, links to the Hive's own site, and what the chat draws. Everything is escaped first."""
    out, pos, text = [], 0, str(text).replace("\r\n", "\n")
    for m in VFENCE.finditer(text):
        out.append(_md(text[pos:m.start()])); out.append(visual(m.group(2), m.group(4))); pos = m.end()
    out.append(_md(text[pos:]))
    return "".join(out)


def _md(text):
    out, para, lst, code = [], [], [], None
    def flush():
        nonlocal para, lst
        if para: out.append("<p>" + inline(" ".join(para)) + "</p>"); para = []
        if lst: out.append("<ul>" + "".join("<li>" + inline(x) + "</li>" for x in lst) + "</ul>"); lst = []
    for ln in str(text).split("\n"):
        if ln.startswith("```"):
            if code is None: flush(); code = []
            else: out.append("<pre><code>" + esc("\n".join(code)) + "</code></pre>"); code = None
            continue
        if code is not None: code.append(ln); continue
        m = re.match(r"\s*[-*]\s+(.*)", ln)
        if m: (para and flush()); lst.append(m.group(1)); continue
        if not ln.strip(): flush(); continue
        if lst: flush()
        h = re.match(r"#{1,6}\s+(.*)", ln)
        if h: flush(); out.append("<p><b>" + inline(h.group(1)) + "</b></p>"); continue
        para.append(ln.strip())
    if code is not None: out.append("<pre><code>" + esc("\n".join(code)) + "</code></pre>")
    flush()
    return "".join(out)


# ---- what the chat draws, drawn on the page with nothing fetched ----
# A page runs in a sealed frame: no network, no access to this page, its height reported back by one line of script.
SEAL = ('<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'unsafe-inline\'; '
        'style-src \'unsafe-inline\'; img-src data: blob:; font-src data:; media-src data: blob:; base-uri \'none\'; form-action \'none\'">')
TELL = ('<script>(function(){function s(){parent.postMessage({hiveFrame:1,h:Math.ceil(document.documentElement.scrollHeight)},"*")}'
        'addEventListener("load",s);try{new ResizeObserver(s).observe(document.documentElement)}catch(e){}setTimeout(s,600)})()</script>')
COLORS = ["#2f6fdb", "#e0782f", "#2a9d6f", "#b24fbf", "#c9a227", "#6b7280"]


def _src(body, what):
    return f'<details class="src"><summary>{what}</summary><pre><code>{esc(body)}</code></pre></details>'


def visual(lang, body):
    try:
        if lang == "html":
            doc = re.sub(r"(?is)^((?:\s|<!--.*?-->)*<!doctype[^>]*>)?", lambda m: (m.group(1) or "") + SEAL, body, count=1) + TELL
            return (f'<figure class="vis page"><iframe sandbox="allow-scripts" srcdoc="{esc(doc)}" title="A page from the chat" loading="lazy"></iframe>'
                    f'<figcaption>A page from the chat, running here sealed off: nothing in it loads from the web.</figcaption></figure>')
        if lang == "svg":
            if re.search(r"<script|\bon\w+\s*=|javascript:|<foreignObject", body, re.I): return _src(body, "A drawing (its source)")
            svg = body if "xmlns" in body else body.replace("<svg", '<svg xmlns="http://www.w3.org/2000/svg"', 1)
            return f'<figure class="vis"><img alt="A drawing from the chat" src="data:image/svg+xml;base64,{base64.b64encode(svg.encode()).decode()}"></figure>'
        if lang == "chart": return chart(body)
        if lang == "csv": return table(body)
    except Exception: pass
    what = {"plot": "An interactive plot with sliders: it moves in the Hive; here is its formula",
            "mermaid": "A diagram: it draws in the Hive; here is its source"}.get(lang, "Its source")
    return _src(body, what)


def _num(v):
    v = str(v).strip().replace(",", "").replace("$", "").replace("%", "").replace("£", "").replace("€", "")
    try: return float(v)
    except ValueError: return None


def _nice(lo, hi, n=5):
    if hi <= lo: hi = lo + 1
    raw = (hi - lo) / n; mag = 10 ** __import__("math").floor(__import__("math").log10(raw))
    step = next(k * mag for k in (1, 2, 2.5, 5, 10) if k * mag >= raw)
    a = __import__("math").floor(lo / step) * step
    out = []
    while a <= hi + step * 1e-9: out.append(round(a, 10)); a += step
    if out[-1] < hi: out.append(out[-1] + step)
    return out


def _fmt(v):
    a = abs(v)
    if a >= 1e9: return f"{v / 1e9:g}B"
    if a >= 1e6: return f"{v / 1e6:g}M"
    if a >= 1e4: return f"{v / 1e3:g}k"
    return f"{v:g}"


def table(body):
    rows = [r for r in csv.reader(io.StringIO(body.strip()), delimiter="\t" if "\t" in body.split("\n", 1)[0] else ",")][:201]
    if len(rows) < 2: raise ValueError
    head = "".join(f"<th>{esc(c)}</th>" for c in rows[0])
    trs = "".join("<tr>" + "".join(f"<td>{esc(c)}</td>" for c in r) + "</tr>" for r in rows[1:])
    return f'<figure class="vis tbl"><table><thead><tr>{head}</tr></thead><tbody>{trs}</tbody></table></figure>'


def chart_svg(body, W=640):
    """The chart fence (ui rich/plot.js parseChart): title/type/x/y lines, then csv. Drawn as a still SVG."""
    lines, H, i = body.split("\n"), {"title": None, "type": None, "x": None, "y": None}, 0
    for i, l in enumerate(lines):
        m = re.match(r"^\s*(title|x|y|type|mark)\s*:\s*(.*)$", l)
        if not l.strip(): continue
        if not m: break
        if m.group(1) in ("title", "type"): H[m.group(1)] = m.group(2).strip()
        if m.group(1) == "x": H["x"] = m.group(2).split()[0] if m.group(2).split() else None
        if m.group(1) == "y" and m.group(2).split(): H["y"] = (H["y"] or []) + [m.group(2).split()[0]]
    rows = [r for r in csv.reader(io.StringIO("\n".join(lines[i:]).strip()), delimiter="\t" if "\t" in lines[i] else ",")]
    if len(rows) < 2: raise ValueError("no rows")
    head, data = [c.strip() for c in rows[0]], rows[1:501]
    xi = head.index(H["x"]) if H["x"] in head else 0
    xs_raw = [(r[xi] if xi < len(r) else "").strip() for r in data]
    xn = [_num(v) for v in xs_raw]
    numeric = all(v is not None for v in xn)
    cols = [k for k in range(len(head)) if k != xi and (not H["y"] or head[k] in H["y"])]
    if len(cols) > 6: raise ValueError("too many series")
    ser = [(head[k], [_num(r[k]) if k < len(r) else None for r in data]) for k in cols]
    ser = [(n, v) for n, v in ser if any(x is not None for x in v)]
    if not ser: raise ValueError("no numbers")
    typ = (H["type"] or ("line" if numeric else "bar")).lower()
    if typ not in ("line", "bar", "stack", "scatter") or (typ in ("line", "scatter") and not numeric) or (not numeric and len(data) > 200): raise ValueError("chart the chat would not draw")
    bars = typ in ("bar", "stack") or not numeric
    Ht, L, R, T, B = (300, 56, 16, 14, 46) if W > 400 else (260, 40, 8, 10, 42)
    pw, ph = W - L - R, Ht - T - B
    vals = [v for _, vs in ser for v in vs if v is not None]
    if typ == "stack": vals = vals + [sum(vs[j] or 0 for _, vs in ser) for j in range(len(data))]
    ticks = _nice(min(0, min(vals)), max(vals))
    y0, y1 = ticks[0], ticks[-1]
    Y = lambda v: T + ph * (1 - (v - y0) / (y1 - y0))
    g = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {Ht}" role="img" aria-label="{esc(H["title"] or "A chart")}" class="chart">']
    for t in ticks:
        g.append(f'<line x1="{L}" x2="{W - R}" y1="{Y(t):.1f}" y2="{Y(t):.1f}" class="grid"/><text x="{L - 6}" y="{Y(t) + 4:.1f}" text-anchor="end">{esc(_fmt(t))}</text>')
    n = len(data)
    if bars:
        bw = pw / max(1, n)
        for j in range(n):
            base = 0
            for k, (_, vs) in enumerate(ser):
                v = vs[j]
                if v is None: continue
                if typ == "stack":
                    g.append(f'<rect x="{L + j * bw + bw * .15:.1f}" width="{bw * .7:.1f}" y="{Y(base + v):.1f}" height="{abs(Y(base) - Y(base + v)):.1f}" fill="{COLORS[k]}"/>'); base += v
                else:
                    w = bw * .7 / len(ser)
                    g.append(f'<rect x="{L + j * bw + bw * .15 + k * w:.1f}" width="{w:.1f}" y="{min(Y(v), Y(0)):.1f}" height="{abs(Y(0) - Y(v)):.1f}" fill="{COLORS[k]}"/>')
            if n <= (24 if W > 400 else 8) or j % (n // (12 if W > 400 else 5) + 1) == 0:
                g.append(f'<text x="{L + (j + .5) * bw:.1f}" y="{Ht - B + 16}" text-anchor="middle">{esc(xs_raw[j][:14])}</text>')
    else:
        x0, x1 = min(xn), max(xn)
        if x1 == x0: x1 = x0 + 1
        X = lambda v: L + pw * (v - x0) / (x1 - x0)
        for t in _nice(x0, x1, 6 if W > 400 else 3):
            if x0 - 1e-9 <= t <= x1 + 1e-9: g.append(f'<text x="{X(t):.1f}" y="{Ht - B + 16}" text-anchor="middle">{esc(_fmt(t))}</text>')
        for k, (_, vs) in enumerate(ser):
            pts = sorted((xn[j], vs[j]) for j in range(n) if vs[j] is not None)
            if typ == "scatter":
                g += [f'<circle cx="{X(a):.1f}" cy="{Y(b):.1f}" r="3.5" fill="{COLORS[k]}"/>' for a, b in pts]
            else:
                g.append(f'<polyline fill="none" stroke="{COLORS[k]}" stroke-width="2.2" points="' + " ".join(f"{X(a):.1f},{Y(b):.1f}" for a, b in pts) + '"/>')
    g.append(f'<text x="{L + pw / 2:.1f}" y="{Ht - 8}" text-anchor="middle" class="ax">{esc(head[xi])}</text></svg>')
    key = "".join(f'<span><i style="background:{COLORS[k]}"></i>{esc(nm)}</span>' for k, (nm, _) in enumerate(ser)) if len(ser) > 1 else ""
    cap = f'<figcaption><b>{esc(H["title"])}</b></figcaption>' if H["title"] else ""
    return "".join(g), cap, key


def chart(body):
    """Drawn twice, wide and narrow, so a phone reads the same text size as a computer."""
    wide, cap, key = chart_svg(body, 640)
    narrow = chart_svg(body, 340)[0].replace('class="chart"', 'class="chart narrow"', 1)
    return f'<figure class="vis">{cap}{wide}{narrow}{f"<p class=key>{key}</p>" if key else ""}</figure>'


def inline(s):
    s = esc(s)
    s = re.sub(r"`([^`]+)`", r"<code>\1</code>", s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<b>\1</b>", s)
    s = re.sub(r"(https://(?:www\.)?hivelab\.ca[^\s<]*)", r'<a href="\1" rel="noopener">\1</a>', s)
    return s


def dur(sec):
    if sec < 90: return f"{sec} seconds"
    if sec < 5400: return f"{round(sec / 60)} minutes"
    if sec < 172800: return f"{sec / 3600:.1f} hours".replace(".0 ", " ")
    return f"{round(sec / 86400)} days"


def _embed(f, home, budget):
    """A file's content for the page, or None: images and plain text small enough, read only from the files folder."""
    p = Path(str(f.get("path") or ""))
    try: p = p.resolve(); root = (home / "files").resolve()
    except OSError: return None
    if root not in p.parents or not p.is_file() or p.stat().st_size > MAX_FILE or p.stat().st_size > budget[0]: return None
    ext = p.suffix.lower()
    if ext in EMBED:
        budget[0] -= p.stat().st_size
        data = p.read_bytes()
        if ext == ".svg" and re.search(rb"<script|onload|onerror|javascript:", data, re.I): return None
        return "img", f"data:{EMBED[ext]};base64," + base64.b64encode(data).decode()
    if ext in TEXTY:
        budget[0] -= p.stat().st_size
        return "text", scrub(p.read_text(errors="replace"))[:20000]
    return None


CSS = """
:root{color-scheme:light dark;--bg:light-dark(#fbfbfc,#1a1b20);--surface:light-dark(#fff,#23242a);--line:light-dark(#e6e7ea,#34353c);--ink:light-dark(#1d1e22,#f1f1f3);--ink2:light-dark(#4a4c55,#c4c5cc);--ink3:light-dark(#6a6c76,#9d9fa8);--accent:light-dark(#b87d00,#f5b025);--accent-bg:light-dark(#f5b02522,#f5b02526);--font:Inter,system-ui,-apple-system,"Segoe UI",Roboto,Arial,sans-serif;--mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.55 var(--font)}
main{max-width:46rem;margin:0 auto;padding:1.5rem 1rem 3rem}
h1{font-size:1.6rem;line-height:1.2;margin:.2rem 0 .6rem}h3{font-size:.78rem;letter-spacing:.06em;text-transform:uppercase;color:var(--ink3);margin:2.2rem 0 .6rem}
.hex{width:1.6rem;height:1.6rem;display:inline-block;vertical-align:-.35rem;margin-right:.4rem}
.stats{display:flex;flex-wrap:wrap;gap:.4rem 1.2rem;color:var(--ink2);font-size:.9rem;margin:0}.stats b{color:var(--ink)}
.card{background:var(--surface);border:1px solid var(--line);border-radius:12px;padding:.9rem 1rem}.card p{margin:.4rem 0}
.ask{border-left:4px solid var(--accent)}.result{border-color:var(--accent);background:linear-gradient(var(--accent-bg),var(--accent-bg)),var(--surface)}
.agents{list-style:none;margin:0;padding:0;font-size:.9rem}.agents li{padding:.15rem 0;color:var(--ink2)}.agents li b{color:var(--ink);font-family:var(--mono);font-weight:600}
.agents ul{list-style:none;margin:0;padding-left:1.1rem;border-left:1px solid var(--line)}
.bar{display:flex;gap:.6rem;align-items:center;margin:.8rem 0}.bar button{font:inherit;border:1px solid var(--line);background:var(--surface);color:var(--ink);padding:.45rem .9rem;border-radius:8px;cursor:pointer;min-height:44px}
.bar button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.bar span{color:var(--ink3);font-size:.85rem}
.msg{margin:.6rem 0;padding:.5rem .8rem;border-radius:10px;border:1px solid var(--line);background:var(--surface)}
.msg header{display:flex;justify-content:space-between;gap:.6rem;font-size:.8rem;color:var(--ink3)}.msg header b{font-family:var(--mono);color:var(--ink2)}.msg.asker header b{font-family:var(--font)}
.msg p{margin:.35rem 0}.msg ul{margin:.35rem 0;padding-left:1.2rem}
.msg.hide{display:none}.js .msg.wait{opacity:0;transform:translateY(6px)}.msg{transition:opacity .35s,transform .35s}
code{font-family:var(--mono);font-size:.88em;background:var(--accent-bg);padding:.05em .3em;border-radius:4px}pre{overflow:auto;background:var(--bg);border:1px solid var(--line);border-radius:8px;padding:.6rem;max-width:100%}pre code{background:none;padding:0}
figure{margin:.8rem 0}figure img{max-width:100%;height:auto;border-radius:8px;border:1px solid var(--line)}figcaption,.file{font-size:.85rem;color:var(--ink3)}
details{margin:.5rem 0}summary{cursor:pointer;color:var(--ink2)}
footer{margin-top:2.5rem;padding-top:1rem;border-top:1px solid var(--line);font-size:.9rem;color:var(--ink3)}footer a{color:var(--accent)}
a{color:var(--accent)}
.vis{margin:.7rem 0}.vis iframe{width:100%;height:360px;border:1px solid var(--line);border-radius:8px;background:#fff;display:block}
.vis img{max-width:100%;height:auto}.vis svg.chart{width:100%;height:auto;background:var(--surface);border:1px solid var(--line);border-radius:8px}
.vis svg.narrow{display:none}@media(max-width:560px){.vis svg.chart{display:none}.vis svg.chart.narrow{display:block}}
.vis svg text{font:12px var(--font);fill:var(--ink3)}.vis svg .ax{fill:var(--ink2)}.vis svg .grid{stroke:var(--line);stroke-width:1}
.key{display:flex;flex-wrap:wrap;gap:.3rem 1rem;font-size:.85rem;color:var(--ink2);margin:.3rem 0}.key i{display:inline-block;width:.8rem;height:.8rem;border-radius:2px;margin-right:.35rem;vertical-align:-.1rem}
.tbl{overflow:auto;max-width:100%}.tbl table{border-collapse:collapse;font-size:.9rem}.tbl th,.tbl td{border:1px solid var(--line);padding:.3rem .55rem;text-align:left}.tbl th{background:var(--accent-bg)}
@media(prefers-reduced-motion:reduce){.msg{transition:none}}
"""
HEX = '<svg class="hex" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2l8.5 5v10L12 22l-8.5-5V7z" fill="none" stroke="#f5b025" stroke-width="2"/><path d="M12 8l3.5 2v4L12 16l-3.5-2v-4z" fill="#f5b025"/></svg>'
JS = """
addEventListener("message",function(e){var d=e.data;if(!d||d.hiveFrame!==1)return;[].forEach.call(document.querySelectorAll(".vis iframe"),function(f){if(f.contentWindow===e.source)f.style.height=Math.max(120,Math.min(1400,(+d.h||0)+4))+"px"})});
(function(){var d=document,m=[].slice.call(d.querySelectorAll('.replay .msg')),b=d.getElementById('play'),s=d.getElementById('state'),t=null;
if(!b||!m.length)return;d.documentElement.className='js';var i=m.length;
function reset(){clearTimeout(t);i=0;m.forEach(function(x){x.classList.add('wait')})}
function step(){if(i>=m.length){b.textContent='Play again';s.textContent='Done';return}
var x=m[i++];x.classList.remove('wait');x.scrollIntoView({block:'nearest',behavior:'smooth'});s.textContent=i+' of '+m.length;t=setTimeout(step,Math.min(2200,600+x.textContent.length*8))}
b.onclick=function(){reset();b.textContent='Playing…';step()};m.forEach(function(x){x.classList.remove('wait')});s.textContent='All shown. Press play to watch it in order.'})();
"""


def render(pv, remove=(), files=True, home=HOME):
    remove = set(remove)
    if pv["ask_id"] in remove: pv = {**pv, "title": "A Hive at work"}  # the title was made from the ask
    items = [i for i in pv["items"] if i["id"] not in remove]
    if not items: raise ValueError("nothing left to share")
    ask = next((i for i in items if i["id"] == pv["ask_id"]), None) or next((i for i in items if i["who"] == "asker"), items[0])
    last = next((i for i in reversed(items) if i["who"] != "asker" and i["id"] != ask["id"]), None)
    rest = [i for i in items if i is not ask]
    names = {i["who"] for i in items if i["who"] != "asker"}
    ags = [a for a in pv["agents"] if a["name"] in names]
    kids = {}
    for a in ags: kids.setdefault(a["by"] if a["by"] in {x["name"] for x in ags} or a["by"] == "asker" else None, []).append(a)
    def tree(by):
        return "".join("<li><b>%s</b>%s</li>" % (esc(a["name"]), ("<ul>" + tree(a["name"]) + "</ul>") if a["name"] in kids else "") for a in kids.get(by, []))
    roots = tree("asker") + tree(None)
    budget = [MAX_FILES_TOTAL]
    def fileblock(it):
        out = []
        for f in it["files"]:
            full = next((x for x in pv.get("_paths", []) if x["post"] == it["id"] and scrub(os.path.basename(x["name"])) == f["name"]), None) if files else None
            e = _embed(full, home, budget) if full else None
            if e and e[0] == "img": out.append(f'<figure><img src="{e[1]}" alt="{esc(f["name"])}" loading="lazy"><figcaption>{esc(f["name"])}</figcaption></figure>')
            elif e: out.append(f'<details><summary>{esc(f["name"])}</summary><pre><code>{esc(e[1])}</code></pre></details>')
            else: out.append(f'<p class="file">File: {esc(f["name"])} (not included)</p>')
        return "".join(out)
    def when(i):
        t0, t = _t(rest[0]["at"] if rest else ask["at"]), _t(i["at"])
        return dur(int((t - t0).total_seconds())) if t0 and t else ""
    def msg(i):
        who = "The asker" if i["who"] == "asker" else i["who"]
        return f'<article class="msg {"asker" if i["who"] == "asker" else "agent"}" data-id="{esc(i["id"])}"><header><b>{esc(who)}</b><span>{esc(_clock(i["at"]))}</span></header>{md(i["text"])}{fileblock(i)}</article>'
    res = ""
    if last:
        res = f'<h3>The result</h3><section class="card result"><p class="file">{esc(last["who"])}</p>{md(last["text"])}{fileblock(last)}</section>'
    n_ag = len(ags)
    desc = esc(re.sub(r"\s+", " ", ask["text"])[:150])
    stats = f'<p class="stats"><span><b>{n_ag}</b> agent{"s" if n_ag != 1 else ""}</span><span><b>{len(items)}</b> message{"s" if len(items) != 1 else ""}</span><span><b>{esc(dur(pv["seconds"]))}</b> start to finish</span></p>'
    return f"""<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{esc(pv["title"])} · a Hive at work</title>
<meta name="description" content="{desc}"><style>{CSS}</style></head><body><main>
<p class="file">{HEX}A Hive at work</p><h1>{esc(pv["title"])}</h1>{stats}
<h3>The ask</h3><section class="card ask">{md(ask["text"])}</section>
{('<h3>Who worked on it</h3><ul class="agents">' + roots + '</ul>') if roots else ''}{res}
<h3>The replay</h3><div class="bar"><button id="play" type="button">Play</button><span id="state"></span></div>
<div class="replay">{"".join(msg(i) for i in rest)}</div>
<footer>{FOOT.replace("hivelab.ca", '<a href="https://hivelab.ca" rel="noopener">hivelab.ca</a>')}. Overnight agents on your own machine.</footer>
</main><script>{JS}</script></body></html>"""


def build(thread, remove=(), files=True, home=HOME, title=None):
    pv = preview(thread, home, title)
    paths = []
    for p in _read(home / "board.jsonl", thread):
        for f in p.get("files") or []: paths.append({"post": p["id"], "name": f.get("name") or f.get("path") or "", "path": f.get("path")})
    pv["_paths"] = paths
    return render(pv, remove, files, home), pv


def _clock(s):
    t = _t(s)
    return t.astimezone(dt.timezone.utc).strftime("%H:%M UTC") if t else ""


def main(argv=None):
    a = argparse.ArgumentParser(prog="hive share", description="Write one conversation as a page you can post. Nothing is uploaded.")
    a.add_argument("conversation"); a.add_argument("-o", "--out"); a.add_argument("--remove", default="", help="comma-separated message ids to leave out")
    a.add_argument("--no-files", action="store_true"); a.add_argument("--title"); a.add_argument("--show", action="store_true", help="print the scrubbed text and stop, to read it first")
    a = a.parse_args(argv)
    try: page, pv = build(a.conversation, [x for x in a.remove.split(",") if x], not a.no_files, title=a.title)
    except KeyError: sys.exit(f"hive share: no conversation {a.conversation!r}")
    except ValueError as e: sys.exit(f"hive share: {e}")
    if a.show:
        print(pv["notice"], "\n")
        for i in pv["items"]: print(f"[{i['id']}] {i['who']}: {i['text']}\n")
        return
    out = Path(a.out or f"hive-{re.sub(r'[^a-z0-9]+', '-', pv['title'].lower()).strip('-')[:40] or 'result'}.html")
    out.write_text(page)
    print(f"{out} ({len(page) // 1024} KB). {pv['notice']}\nRead it, then put it wherever you like; use --show to read the text first and --remove ID to leave messages out.")


if __name__ == "__main__": main()
