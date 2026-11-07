"""Read a page by whichever honest route works: plain request, then a real browser, then the Internet Archive's copy.
It goes no further than that: no proxies or challenge solving. A page it can't read comes back as blocked, never as content."""
import json, re, shutil, subprocess, tempfile, urllib.parse
from . import net
from .extract import domain_of, parse_html
from .store import now

CHALLENGE = re.compile(r"just a moment\.\.\.|attention required|verify you are (a )?human|checking your browser|enable javascript and cookies|access denied|request unsuccessful|are you a robot|captcha|pardon our interruption|you have been blocked|unusual traffic|prove your humanity|complete the challenge below", re.I)
GONE = ("HTTP 404", "HTTP 410")   # the site answered: a browser would only render its error page as if it were the page
# Wordings not in CHALLENGE: a short page whose title is a verification step, or that says several of these things, is a gate and not a page.
GATE_TITLE = re.compile(r"verif|security check|human|robot|bot\b|one more step|just a (moment|second)|please wait|checking|are you|access (denied|check)|blocked|challenge|confirm you|not a (robot|bot)", re.I)
GATE_SOFT = [re.compile(p, re.I) for p in (r"\bbots?\b|\brobots?\b|automated (traffic|access|requests?)", r"\bhuman\b|real person", r"\bbrowser\b", r"\bverif(y|ication|ying)\b|\bconfirm\b", r"security (check|service|measure)|ray id|cloudflare|incident id|reference (id|number)", r"\bjavascript\b|\bcookies\b", r"suspicious|unusual|\bblocked\b|denied|not allowed", r"\bcomplete the\b|\bsolve\b|\bclick (the )?(box|button)\b|\bwait while\b|\bredirect")]
THIN = 200
ARCHIVE = "https://archive.org"   # the tests replace this


def _looks_blocked(parsed):
    """A challenge page is short and says so. A long page mentioning captcha is just a page."""
    text, title = parsed["text"], parsed["title"]
    if len(text) >= 1500: return False
    if CHALLENGE.search(title + " " + text): return True
    if len(text) < 600 and GATE_TITLE.search(title): return True
    return len(text) < 800 and sum(1 for g in GATE_SOFT if g.search(text)) >= 3


def _pdf_text(body):
    exe = shutil.which("pdftotext")
    if not exe: return None
    with tempfile.NamedTemporaryFile(suffix=".pdf") as f:
        f.write(body); f.flush()
        try: return subprocess.run([exe, "-layout", f.name, "-"], capture_output=True, timeout=60).stdout.decode("utf-8", "replace")
        except (OSError, subprocess.TimeoutExpired): return None


def _ok(url, final, via, http, parsed, **more):
    if parsed.get("cut"): more.setdefault("note", "the page took too long to parse: only part of it was read")
    return dict(url=url, final_url=final, via=via, status="ok", http=http, title=parsed["title"], text=parsed["text"], date=parsed["date"], date_from=parsed["date_from"], links=parsed["links"], people_ld=parsed["people_ld"], **more)


def _direct(url):
    """(page or None, why not)."""
    r = net.get(url)
    if r.error: return None, r.error
    if r.status != 200: return None, f"HTTP {r.status}"
    if r.ctype == "application/pdf" or r.body[:5] == b"%PDF-":
        text = _pdf_text(r.body)
        if text is None: return None, "a PDF, and pdftotext isn't installed"
        if not text.strip(): return None, "a PDF with no text in it (scanned?)"
        return _ok(url, r.url, "direct", 200, {"title": url.rsplit("/", 1)[-1], "text": text.strip(), "links": [], "date": "", "date_from": "", "people_ld": []}), ""
    if r.ctype and not (r.ctype.startswith("text/") or "html" in r.ctype or "xml" in r.ctype or "json" in r.ctype): return None, f"not a page ({r.ctype})"
    parsed = parse_html(r.text, r.url)
    if _looks_blocked(parsed): return None, "challenge page"
    if len(parsed["text"]) < THIN: return None, "almost no text (a JavaScript page?)"
    return _ok(url, r.url, "direct", 200, parsed), ""


def _browser(url):
    html, err = net.chrome_dump(url)
    if err: return None, err
    parsed = parse_html(html, url)
    if _looks_blocked(parsed): return None, "challenge page in a browser"
    if len(parsed["text"]) < THIN: return None, "almost no text in a browser"
    # The browser route can't see a status or a redirect, so a page's own word for where it lives is the best there is. http stays 0: nobody saw one. Only within the site asked for: a page can't talk its way onto someone's official domain.
    m = re.search(r"""<link[^>]+rel=["']canonical["'][^>]+href=["'](https?://[^"']+)|<meta[^>]+property=["']og:url["'][^>]+content=["'](https?://[^"']+)""", html[:200000], re.I)
    said = (m.group(1) or m.group(2)) if m else url
    return _ok(url, said if domain_of(said) == domain_of(url) else url, "browser", 0, parsed), ""


def archive_lookup(url):
    """(snapshot URL, YYYY-MM-DD) of the newest copy that loaded, or (None, why)."""
    r = net.get(ARCHIVE + "/wayback/available?url=" + urllib.parse.quote(url, safe=""))
    if r.error or r.status != 200: return None, r.error or f"HTTP {r.status}"
    try: snap = json.loads(r.body)["archived_snapshots"]["closest"]
    except (ValueError, KeyError, TypeError): return None, "no copy"
    if not snap.get("available") or str(snap.get("status")) != "200": return None, "no copy"
    ts = snap["timestamp"]
    return re.sub(r"^http:", "https:", snap["url"]), f"{ts[:4]}-{ts[4:6]}-{ts[6:8]}"


def _archive(url):
    snap, when = archive_lookup(url)
    if not snap: return None, when
    raw = re.sub(r"(/web/\d+)/", r"\1id_/", snap, count=1)   # the page as saved, without the archive's toolbar
    r = net.get(raw)
    if r.error or r.status != 200: return None, r.error or f"HTTP {r.status}"
    parsed = parse_html(r.text, url)
    if len(parsed["text"]) < THIN or _looks_blocked(parsed): return None, "the copy has no text"
    return _ok(url, snap, "archive", 200, parsed, snapshot=when), ""


NEEDS_BROWSER = ("news.google.com",)


def fetch(url, case=None, refresh=False, allow_archive=True, allow_browser=True):
    """The page dict, saved in the case. status is ok, blocked or refused; note says what each route answered."""
    notes, page = [], None
    try:
        net.check_url(url, resolve=False)   # a saved page too: the list may have changed since
        if case and not refresh:
            known = case.page(url)
            if known and known["status"] == "ok": return known
        host = (urllib.parse.urlsplit(url).hostname or "").lower()
        routes = [("direct", _direct), ("browser", _browser), ("archive", _archive)]
        if host in NEEDS_BROWSER: routes = routes[1:2]
        gone = False
        for name, route in routes:
            if name == "browser" and (gone or not allow_browser): continue
            if name == "archive" and not allow_archive: continue
            try: page, why = route(url)
            except net.Refused: raise
            except Exception as e: page, why = None, f"{type(e).__name__}: {e}"  # one bad page never ends a long run
            if page: break
            notes.append(f"{name}: {why}")
            gone = gone or why in GONE
    except net.Refused as e:
        return dict(url=url, status="refused", note=str(e), text="", title="", links=[], people_ld=[], via="", date="", fetched_at=now())
    if not page:
        page = dict(url=url, status="blocked", via="", text="", title="", links=[], people_ld=[], date="", fetched_at=now())
    page["note"] = "; ".join(notes)
    return case.save_page(page) if case else page


ROBOTS_CACHE = {}


def robots_allows(url):
    """A crawl keeps to robots.txt. One page asked for by name doesn't need it: it's what a person opening a link does."""
    import urllib.robotparser
    u = urllib.parse.urlsplit(url)
    root = f"{u.scheme}://{u.netloc}"
    rp = ROBOTS_CACHE.get(root)
    if rp is None:
        rp = urllib.robotparser.RobotFileParser()
        try: r = net.get(root + "/robots.txt", timeout=15)
        except net.Refused: r = None
        if r is None or r.status in (401, 403): rp.disallow_all = True
        elif r.status != 200 or r.error: rp.allow_all = True
        else: rp.parse(r.text.splitlines())
        ROBOTS_CACHE[root] = rp
    return rp.can_fetch("*", url)
