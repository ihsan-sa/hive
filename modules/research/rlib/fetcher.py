"""Read a page by whichever honest route works: plain request, then a real browser, then the Internet Archive's copy.
It goes no further than that: no proxies or challenge solving. A page it can't read comes back as blocked, never as content."""
import datetime as dt, html.parser, json, re, shutil, subprocess, tempfile, threading, unicodedata, urllib.parse
from . import net, pdfboxes
from .extract import domain_of, parse_date, parse_html
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
        try: text = subprocess.run([exe, "-layout", f.name, "-"], capture_output=True, timeout=60).stdout.decode("utf-8", "replace")
        except (OSError, subprocess.TimeoutExpired): return None
        try: boxes = pdfboxes.org_chart_pages(subprocess.run([exe, "-bbox", f.name, "-"], capture_output=True, timeout=60).stdout.decode("utf-8", "replace"))
        except (OSError, subprocess.TimeoutExpired): boxes = {}
    return pdf_pages(text, boxes)


def pdf_pages(text, boxes):
    """The layout reading, with each org-chart page swapped for its boxes (`pdfboxes`), and ligatures undone: "Ofﬁcer" isn't a role to a regex."""
    pages = text.split("\f")
    for i, t in boxes.items():
        if i < len(pages): pages[i] = t + "\n"
    return unicodedata.normalize("NFKC", "\f".join(pages))


def _ok(url, final, via, http, parsed, **more):
    if parsed.get("cut"): more.setdefault("note", "the page took too long to parse: only part of it was read")
    return dict(url=url, final_url=final, via=via, status="ok", http=http, title=parsed["title"], text=parsed["text"], date=parsed["date"], date_from=parsed["date_from"], published=parsed.get("published") or "", links=parsed["links"], people_ld=parsed["people_ld"], **more)


def _wp_date(parsed, url):
    """A WordPress page dated only by its last edit gets the published date from its own REST record, the <link rel="alternate" type="application/json"> it prints: uConnect's episode page says og:updated_time 2026-05-05, its record "date" 2026-02-03 (Castonguay, fnwords120 review)."""
    u = parsed.get("wp_json") or ""
    if parsed.get("date_from") != "modified" or not u or domain_of(u) != domain_of(url): return parsed
    r = net.get(u)
    if r.error or r.status != 200: return parsed
    try: rec = json.loads(r.body)
    except ValueError: return parsed
    d = parse_date(str(rec.get("date") or "")) if isinstance(rec, dict) else ""
    return dict(parsed, date=d, date_from="rest") if d else parsed   # its own source: the episode HTML has no such date, so "its page's metadata" was wrong (fnwords147 review)


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
    return _ok(url, r.url, "direct", 200, _wp_date(parsed, r.url)), ""


def is_redirector(url): return (urllib.parse.urlsplit(url or "").hostname or "").lower() in REDIRECTORS


def _browser(url):
    html, err = net.chrome_dump(url, budget_ms=12000, timeout=80) if is_redirector(url) else net.chrome_dump(url)   # Google's hop to the article and the article itself: 7 s came back empty for Moore's link, 12 s read it
    if err: return None, err
    parsed = parse_html(html, url)
    if _looks_blocked(parsed): return None, "challenge page in a browser"
    if len(parsed["text"]) < THIN: return None, "almost no text in a browser"
    # The browser route can't see a status or a redirect, so a page's own word for where it lives is the best there is. http stays 0: nobody saw one. Only within the site asked for: a page can't talk its way onto someone's official domain.
    m = re.search(r"""<link[^>]+rel=["']canonical["'][^>]+href=["'](https?://[^"']+)|<meta[^>]+property=["']og:url["'][^>]+content=["'](https?://[^"']+)""", html[:200000], re.I)
    said = (m.group(1) or m.group(2)) if m else url
    if is_redirector(url) and not is_redirector(said) and domain_of(said) != domain_of(url):
        # A news.google.com link only sends the browser on to the publisher's article, and the article names its own address. That address is read again directly, so the page saved is the publisher's server's word for it and not the page's alone: Moore's quote was kept under an unresolved Google link while the page read was Vegas Inc's (fnwords134 review).
        try:
            net.check_url(said, resolve=False)
            page, why = _direct(said)
        except net.Refused: page = None
        if page: return dict(page, url=url), ""
    return _ok(url, said if domain_of(said) == domain_of(url) else url, "browser", 0, _wp_date(parsed, url)), ""


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


def archive_before(url, day):
    """(text, YYYY-MM-DD) of the Wayback copy of url saved nearest before `day` (an ISO date), or (None, why): the copy a page's last edit is compared with (Kaylor's Boise State guide, edited 2026-06-01, against its copy of 2026-01-12: fnwords157 review)."""
    try: before = (dt.date.fromisoformat(str(day)[:10]) - dt.timedelta(days=1)).strftime("%Y%m%d")
    except ValueError: return None, "no day"
    r = net.get(ARCHIVE + "/wayback/available?url=" + urllib.parse.quote(url, safe="") + "&timestamp=" + before)
    if r.error or r.status != 200: return None, r.error or f"HTTP {r.status}"
    try: snap = json.loads(r.body)["archived_snapshots"]["closest"]
    except (ValueError, KeyError, TypeError): return None, "no copy"
    if not snap.get("available") or str(snap.get("status")) != "200": return None, "no copy"
    ts = str(snap["timestamp"])
    when = f"{ts[:4]}-{ts[4:6]}-{ts[6:8]}"
    if when >= str(day)[:10]: return None, f"no copy before {str(day)[:10]} (nearest {when})"   # the API gives the nearest copy either side
    raw = re.sub(r"(/web/\d+)/", r"\1id_/", re.sub(r"^http:", "https:", snap["url"]), count=1)
    g = net.get(raw)
    if g.error or g.status != 200: return None, g.error or f"HTTP {g.status}"
    parsed = parse_html(g.text, url)
    if len(parsed["text"]) < THIN or _looks_blocked(parsed): return None, "the copy has no text"
    return parsed["text"], when


NEEDS_BROWSER = ("news.google.com",)
REDIRECTORS = ("news.google.com",)   # hosts whose links lead to another site's article: the page is saved under the publisher's address too


def resolve(url, case):
    """The publisher's address a redirector link (news.google.com) leads to, read now and saved under both addresses, or "" when it doesn't resolve. Unlike fetch(refresh=True), a read that fails leaves the saved copy as it was. For a stored row whose link was saved before the browser route followed it."""
    if not is_redirector(url): return ""
    try:
        net.check_url(url, resolve=False)
        page, _ = _browser(url)
    except net.Refused: return ""
    except Exception: return ""   # one bad page never ends a long run
    if not page or is_redirector(page.get("final_url")): return ""
    page = case.save_page(dict(page, note=""))
    case.save_page(dict(page, url=page["final_url"]))
    return page["final_url"]



ROSTER_PATH = re.compile(r"""["'`](/(?:[\w.-]+/)*[\w.-]*(?:staff|people|directory|personnel)[\w.-]*(?:/[\w.-]+)*)["'`?]""", re.I)
ROSTER_NOT = re.compile(r"[\[\]{}:]|inactive|former|alumni|emerit|retire|admin|summary|section|categor|count|/new\b", re.I)
ROSTER_SCRIPTS = 20   # same-site scripts read for a roster path: Purdue CCO's page loads 15 and names /api/staff-management in its layout chunk, the eleventh
NAME_KEYS, TITLE_KEYS = ("name", "full_name", "fullname", "display_name", "displayname"), ("title", "job_title", "jobtitle", "position", "position_title")


def roster_pairs(data):
    """The (name, title) pairs of a JSON staff list: each object with a name and a title string, its active flag not false. A name may be split into first and last."""
    out = []
    def walk(x, depth=0):
        if depth > 6: return
        if isinstance(x, list):
            for y in x: walk(y, depth + 1)
            return
        if not isinstance(x, dict): return
        low = {str(k).lower(): v for k, v in x.items()}
        name = next((low[k] for k in NAME_KEYS if isinstance(low.get(k), str) and low[k].strip()), "")
        if not name and isinstance(low.get("first_name"), str) and isinstance(low.get("last_name"), str): name = f"{low['first_name']} {low['last_name']}"
        title = next((low[k] for k in TITLE_KEYS if isinstance(low.get(k), str) and low[k].strip()), "")
        if name and title:
            if not any(low.get(k) is False for k in ("is_active", "active", "isactive", "published")): out.append((" ".join(name.split()), " ".join(title.split())))
            return
        for v in x.values(): walk(v, depth + 1)
    walk(data)
    return out


def script_roster(url):
    """(JSON address, [(name, title)]) of the staff list a page draws by script, or ("", []). The page's own HTML and its same-site scripts are read for a same-site path with staff, people, directory or personnel in it; the paths are asked shortest first, up to three, and the first JSON with two pairs or more is the roster, with its next pages (up to five) when it says it has more. Purdue CCO's meet-the-staff page prints 1,644 characters and no one: its script reads /api/staff-management, whose JSON lists "Mary Andrade", "Senior Director, Career Success" (fnwords155 review)."""
    r = net.get(url)
    if r.error or r.status != 200 or not r.text: return "", []
    base, host = r.url or url, (urllib.parse.urlsplit(r.url or url).hostname or "").lower()
    def paths(text):
        return {m.group(1).rstrip("/") for m in ROSTER_PATH.finditer(text) if not ROSTER_NOT.search(m.group(1)) and ("/api/" in m.group(1).lower() or "json" in m.group(1).lower())}
    found = paths(r.text)
    srcs = [urllib.parse.urljoin(base, m.group(1)) for m in re.finditer(r"""<script[^>]+src=["']([^"']+)["']""", r.text, re.I)]
    for src in [u for u in srcs if (urllib.parse.urlsplit(u).hostname or "").lower() == host][:ROSTER_SCRIPTS]:
        if found: break
        sr = net.get(src)
        if not sr.error and sr.status == 200 and sr.text: found |= paths(sr.text)
    for path in sorted(found, key=lambda x: (len(x), x))[:3]:
        api = urllib.parse.urljoin(base, path)
        if (urllib.parse.urlsplit(api).hostname or "").lower() != host: continue
        jr = net.get(api)
        if jr.error or jr.status != 200: continue
        try: data = json.loads(jr.body)
        except ValueError: continue
        pairs = roster_pairs(data)
        if len(pairs) < 2: continue
        pg = data.get("pagination") if isinstance(data, dict) else None
        total = next((pg.get(k) for k in ("totalPages", "total_pages", "pages", "last_page") if isinstance(pg, dict) and isinstance(pg.get(k), int)), 1)
        for n in range(2, min(total, 5) + 1):
            nr = net.get(api + ("&" if "?" in api else "?") + f"page={n}")
            if nr.error or nr.status != 200: break
            try: pairs += roster_pairs(json.loads(nr.body))
            except ValueError: break
        return api, pairs
    return "", []

class _Forms(html.parser.HTMLParser):
    """The <form>s of a page: [(action, method, [(tag, type, name, value)])]."""
    def __init__(self):
        super().__init__(convert_charrefs=True); self.forms, self.cur = [], None
    def handle_starttag(self, tag, attrs):
        a = {k: (v or "") for k, v in attrs}
        if tag == "form": self.cur = (a.get("action", ""), (a.get("method") or "get").lower(), []); self.forms.append(self.cur)
        elif self.cur and tag in ("input", "select", "textarea"): self.cur[2].append((tag, (a.get("type") or ("text" if tag == "input" else tag)).lower(), a.get("name", ""), a.get("value", "")))
    def handle_endtag(self, tag):
        if tag == "form": self.cur = None


def directory_form(url):
    """(action URL, the text field's name, [(hidden name, value)]) of the search form a staff directory keeps for itself, or None. The form asks by GET for the page's own address (not the site's /search), and has one text field; its hidden fields go with every query, its drop-downs are left at all. Madison College's directory prints no one until it is asked: `/about/directory?name_title=career` lists its Director, Career & Employment Services, and Houston CC's profile listing wants `?keywords=workforce&showall=1` (fnwords157, 6z12x)."""
    r = net.get(url)
    if r.error or r.status != 200 or not r.text: return None
    base = r.url or url
    here = urllib.parse.urlsplit(base)
    p = _Forms()
    try: p.feed(r.text)
    except Exception: return None
    for action, method, fields in p.forms:
        act = urllib.parse.urljoin(base, action or base)
        a = urllib.parse.urlsplit(act)
        if method != "get" or (a.hostname or "").lower() != (here.hostname or "").lower() or a.path.rstrip("/") != here.path.rstrip("/"): continue
        text = [n for t, ty, n, v in fields if t in ("input", "textarea") and ty in ("text", "search", "textarea") and n]
        if len(text) != 1: continue
        hidden = [(n, v) for t, ty, n, v in fields if t == "input" and ty == "hidden" and n]
        return urllib.parse.urlunsplit((a.scheme, a.netloc, a.path, "", "")), text[0], hidden
    return None


def form_query(form, word):
    """The address a directory form asks for one word."""
    action, field, hidden = form
    return action + "?" + urllib.parse.urlencode([(field, word)] + list(hidden))


LAST = threading.local()   # LAST.route: how this thread's last fetch ended: saved (from the case), direct, browser, archive, blocked or refused. A hunt splits its page-read seconds by it


def fetch(url, case=None, refresh=False, allow_archive=True, allow_browser=True):
    """The page dict, saved in the case. status is ok, blocked or refused; note says what each route answered."""
    notes, page = [], None
    LAST.route = "blocked"
    try:
        net.check_url(url, resolve=False)   # a saved page too: the list may have changed since
        if case and not refresh:
            known = case.page(url)
            if known and known["status"] == "ok":
                LAST.route = "saved"
                return known
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
            if page:
                LAST.route = name
                break
            notes.append(f"{name}: {why}")
            gone = gone or why in GONE
    except net.Refused as e:
        LAST.route = "refused"
        return dict(url=url, status="refused", note=str(e), text="", title="", links=[], people_ld=[], via="", date="", fetched_at=now())
    if not page:
        page = dict(url=url, status="blocked", via="", text="", title="", links=[], people_ld=[], date="", fetched_at=now())
    page["note"] = "; ".join(notes)
    if case and page["status"] == "ok" and is_redirector(url) and not is_redirector(page.get("final_url")): case.save_page(dict(page, url=page["final_url"]))   # a row keeps the publisher's address, and case.page finds the page under it
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
