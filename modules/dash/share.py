#!/usr/bin/env python3
"""Share this result: one conversation as one self-contained HTML page its owner can post anywhere.

  share.py CONVERSATION [-o FILE] [--remove ID,ID] [--no-files] [--title TEXT]

The page holds the ask, a short replay of the agents working, the result and its files, and a small footer.
Nothing is uploaded: this only reads the board and the log and writes a file. Everything that goes into the page is
scrubbed first (paths, hostnames, emails, tokens, addresses, tool output); the app shows a preview with a remove button
per message, and `remove` here is the same list. Used by dash.py (/api/share-preview, /api/share-build) and by `hive share`."""
import argparse, base64, datetime as dt, html, json, mimetypes, os, re, sys
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


def scrub(text):
    """The text with everything private taken out. Long code blocks are tool output: they go whole, short ones stay."""
    t = str(text or "")
    t = FENCE.sub(lambda m: m.group(0) if m.group(1).count("\n") <= 8 and len(m.group(1)) < 700 else "[tool output removed]", t)
    for rx, sub in RULES: t = rx.sub(sub, t)
    return t


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
        if len(text) > MAX_TEXT: text = text[:MAX_TEXT].rsplit(" ", 1)[0] + " …"
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
    """Small markdown: paragraphs, lists, bold, code, links to the Hive's own site. Everything is escaped first."""
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
@media(prefers-reduced-motion:reduce){.msg{transition:none}}
"""
HEX = '<svg class="hex" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2l8.5 5v10L12 22l-8.5-5V7z" fill="none" stroke="#f5b025" stroke-width="2"/><path d="M12 8l3.5 2v4L12 16l-3.5-2v-4z" fill="#f5b025"/></svg>'
JS = """
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
