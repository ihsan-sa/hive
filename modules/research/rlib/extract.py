"""HTML to readable text, links, dates, people. Pages are data, so nothing here runs or trusts them."""
import collections, datetime as dt, html as htmllib, json, re, time, unicodedata, urllib.parse
from html.parser import HTMLParser

PARSE_SECONDS = 30  # a page built to be slow to parse gets read this far and no further
SKIP = {"script", "style", "noscript", "template", "svg", "iframe", "canvas", "head", "select", "textarea"}
BLOCK = {"p", "div", "li", "ul", "ol", "tr", "table", "section", "article", "header", "footer", "aside", "main", "nav", "br", "hr", "blockquote", "dd", "dt", "dl", "figure", "figcaption", "address", "td", "th"}
HEADING = re.compile(r"h[1-6]$")
VOID = {"br", "hr", "img", "meta", "link", "input", "area", "base", "col", "embed", "source", "track", "wbr", "param"}


class _Page(HTMLParser):
    def __init__(self, base):
        super().__init__(convert_charrefs=True)
        self.base, self.chunks, self.links, self.title, self.meta, self.jsonld = base, [], [], "", {}, []
        self.open = collections.Counter()  # how many of each tag are open: a long page of unclosed tags mustn't mean searching the stack each time
        self.stack, self.skip, self.in_title, self.in_ld, self.href, self.anchor = [], 0, False, False, None, []
        self.ld = []
        self.times = set()   # the days of every <time datetime>: more than one is a list (a podcast page's "Latest episodes" sidebar), and its first is not the page's date

    def handle_starttag(self, tag, attrs):
        a = {k.lower(): (v or "") for k, v in attrs}
        if tag == "base" and a.get("href"): self.base = urllib.parse.urljoin(self.base, a["href"])
        if tag == "title" and not self.title: self.in_title = True
        if tag == "meta":
            k = (a.get("property") or a.get("name") or a.get("itemprop") or "").lower()
            if k and a.get("content"): self.meta.setdefault(k, a["content"])
        if tag == "time" and a.get("datetime"):
            self.meta.setdefault("time", a["datetime"])
            self.times.add(a["datetime"][:10])
        if tag == "script" and "ld+json" in a.get("type", ""): self.in_ld, self.ld = True, []
        if tag in VOID: 
            if tag in BLOCK: self.chunks.append("\n")
            return
        if tag in SKIP: self.skip += 1
        self.stack.append(tag)
        self.open[tag] += 1
        if tag in BLOCK or HEADING.match(tag): self.chunks.append("\n")
        if tag == "a" and a.get("href"): self.href, self.anchor = a["href"], []

    def handle_endtag(self, tag):
        if tag in VOID or not self.open[tag]: return
        while self.stack:
            t = self.stack.pop()
            self.open[t] -= 1
            if t in SKIP: self.skip = max(0, self.skip - 1)
            if t == "script" and self.in_ld:
                self.in_ld = False
                self.jsonld.append("".join(self.ld))
            if t == "title": self.in_title = False
            if t == tag: break
        if tag in BLOCK or HEADING.match(tag): self.chunks.append("\n")
        if tag == "a" and self.href:
            try: u = urllib.parse.urldefrag(urllib.parse.urljoin(self.base, self.href))[0]
            except ValueError: u = ""
            if u.startswith(("http://", "https://")): self.links.append((u, " ".join("".join(self.anchor).split())[:120]))
            self.href = None

    def handle_data(self, data):
        if self.in_ld: self.ld.append(data)
        if self.in_title: self.title += data
        if self.skip: return
        self.chunks.append(data)
        if self.href is not None: self.anchor.append(data)


def tidy(s):
    lines = (" ".join(l.split()) for l in s.split("\n"))
    out, blank = [], False
    for l in lines:
        if l: out.append(l); blank = False
        elif not blank and out: out.append(""); blank = True
    return "\n".join(out).strip()


def _ld_items(raw):
    try: d = json.loads(raw.strip())
    except ValueError: return
    todo = [d]
    while todo:
        x = todo.pop()
        if isinstance(x, list): todo += x
        elif isinstance(x, dict):
            yield x
            todo += [v for v in x.values() if isinstance(v, (list, dict))]


DATE_META = ("article:published_time", "article:modified_time", "datepublished", "datemodified", "date", "dc.date", "dc.date.issued", "pubdate", "publish-date", "sailthru.date", "time")
MONTHS = "jan feb mar apr may jun jul aug sep oct nov dec".split()
_TEXT_DATE = re.compile(r"\b(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|June?|July?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+(\d{1,2}),?\s+(20\d\d)\b")


_UTC_STAMP = re.compile(r"\b((?:20|19)\d\d-\d\d-\d\d)[T ](\d\d):(\d\d)(?::(\d\d)(?:\.\d+)?)?\s*(?:Z|[+-]00:?00)(?![\d:])")


def local_day(stamp):
    """The run's own calendar day for a UTC timestamp ("2026-11-25T00:45:34+00:00" is the 24th in New York), else the date the stamp starts with. A page modified or read in the evening in America is stamped the next day in UTC: fnwords134's reload read Garib's page_date and Roark's read_on as 2026-11-25 on a run of the 24th."""
    s = str(stamp or "")
    m = _UTC_STAMP.match(s.strip())
    if m:
        try: return dt.datetime.fromisoformat(f"{m.group(1)}T{m.group(2)}:{m.group(3)}:{m.group(4) or '00'}+00:00").astimezone().date().isoformat()
        except ValueError: pass
    return s[:10]


def parse_date(s):
    """YYYY-MM-DD from an ISO-ish or written date, else ''. A UTC timestamp is the run's local day (local_day); one with its own offset keeps its own date."""
    if not s: return ""
    if (u := _UTC_STAMP.search(s)) and (d := local_day(u.group(0))) and len(d) == 10: return d
    m = re.search(r"\b(20\d\d|19\d\d)-(\d\d)-(\d\d)", s)
    if m:
        try: return dt.date(*map(int, m.groups())).isoformat()
        except ValueError: pass
    m = re.search(r"\b(20\d\d|19\d\d)(\d\d)(\d\d)T\d", s)   # ISO 8601 basic form, <time datetime="20260928T090000Z"> under Cedarville's bylines (Arminio, fnwords127-128 reviews); eight digits alone are no date
    if m:
        try: return dt.date(*map(int, m.groups())).isoformat()
        except ValueError: pass
    m = _TEXT_DATE.search(s)
    if m:
        try: return dt.date(int(m.group(3)), MONTHS.index(m.group(1)[:3].lower()) + 1, int(m.group(2))).isoformat()
        except ValueError: pass
    return ""


def url_date(url):
    """A date the address itself gives: /2024/03/15/ in a news path, or the month folder of an upload (/wp-content/uploads/2011/05/Jul08_OrganizationalChart.pdf is a file put up in May 2011: fnwords51 'verified' a dean from it). A PDF has no other date, so this is what marks an old one."""
    path = urllib.parse.urlsplit(url).path
    m = re.search(r"/(20\d\d)[/-](\d\d?)[/-](\d\d?)(?:/|-|$)", path)
    if m:
        try: return dt.date(*map(int, m.groups())).isoformat()
        except ValueError: pass
    m = re.search(r"/uploads/(?:sites/\d+/)?((?:19|20)\d\d)/(\d\d)/", path)
    if m and 1 <= int(m.group(2)) <= 12: return f"{m.group(1)}-{m.group(2)}-01"
    m = re.search(r"/archives?/((?:19|20)\d\d)-(?:(?:19|20)?\d\d)/", path)  # an archived catalog's year, /archives/2013-14/directory/: its staff list ended with that year (Cincinnati State's workforce VPs, fnwords104)
    if m: return f"{int(m.group(1)) + 1}-06-30"
    return ""


PEOPLE_PAGE = re.compile(r"/(?:profiles?|directory|staff(?:-directory)?|people|our-team|meet-the-team|meet-our-staff|leadership)(?:/|\.html?|$|[?#])", re.I)  # a page that lists people by name


META_DATES_SINCE = "2026-11-25T01:05:00Z"   # pages saved before this read their metadata dates under older rules (a template's node first, ISO basic form unread, a last edit before the first publication): Hunt.stored_dates reads a stored row's page again once. Move it forward when page_date's metadata rules change.
ARTICLE_LD = re.compile(r"Article|BlogPosting|Posting|PressRelease|Report\b|NewsItem")


def page_date(url, meta, ld_items, text, title=""):
    """When the page says it was published, and how it knows. A copyright line is not a date."""
    if "wikipedia.org" in url: return "", ""  # the first date there is from the article's subject (an infobox's founding year)
    # A staff list is kept up to date, not written once: its last edit says it is current. TCNJ's career staff directory was published 2011-05-26 and modified 2025-12-03 (fnwords107). A story keeps its first date: an edit doesn't make old news new.
    # Not a story with "Staff" in its headline: Purdue Today's "Staff Excellence: Center for Career Opportunities" took its edit of 2025-10-22 over its publication on 10-23 (fnwords123 review). A story URL is a story, and an edit dated before the publication is a scheduled post's draft.
    story = re.search(r"/(?:news|newsroom|stories|story|press|blog|articles?)/", url, re.I)
    pub = parse_date(meta.get("article:published_time", ""))
    if (PEOPLE_PAGE.search(url) or (not story and re.search(r"\b(?:staff|directory|our team|leadership)\b", title, re.I))) and not (story and pub):
        for d in [parse_date(meta.get(k, "")) for k in ("article:modified_time", "og:updated_time", "datemodified")] + [parse_date(str(it.get("dateModified", ""))) for it in ld_items]:
            if d: return d, "meta"
    for k in DATE_META:
        if k == "time" and meta.get("time_many"): continue   # the first of several <time> tags is the first item of a list: uConnect's episode page took 2026-11-03 from its "Latest episodes" sidebar for an episode of 2026-02-03 (fnwords119 review)
        d = parse_date(meta.get(k, ""))
        if d: return d, "meta"
    for it in sorted(ld_items, key=lambda it: not ARTICLE_LD.search(str(it.get("@type", "")))):   # the story's own node before the page's or site's: Cedarville's template WebPage (dateModified 2026-02-05) came before Arminio's NewsArticle of 2026-09-28 (fnwords127-128 reviews)
        for k in ("datePublished", "dateModified"):
            d = parse_date(str(it.get(k, "")))
            if d: return d, "meta"
    d = url_date(url)
    if d: return d, "url"
    d = body_date(text, title)
    if d: return d, "text"
    d = parse_date(meta.get("og:updated_time", ""))   # the last edit, when nothing says when it was written: later than the truth, never a sidebar's date
    return (d, "modified") if d else ("", "")   # "modified": the fetcher asks the site's WordPress record for the published date


def published_date(meta, ld_items):
    """When the page says it was first put up, apart from when it was last edited: article:published_time or a datePublished, the story's own JSON-LD node first. '' if it doesn't say. A signal taken from a page is as old as its words, and an edit doesn't make them new: UF's AI in Careers page (Glover's unit signal) gives article:modified_time 2026-01-28 in its meta tags and datePublished 2022-11-17 only in its JSON-LD, and page_date took the edit (fnwords133-134 reviews). page_date keeps the edit, because a staff or contact page is current by it."""
    for k in ("article:published_time", "datepublished"):
        if (d := parse_date(meta.get(k, ""))): return d
    for it in sorted(ld_items, key=lambda it: not ARTICLE_LD.search(str(it.get("@type", "")))):
        if (d := parse_date(str(it.get("datePublished", "")))): return d
    return ""


_DATELINE = re.compile(r"(?m)^[ \t]*" + _TEXT_DATE.pattern + r"\s*[,\u2013\u2014-]\s*[A-Z][A-Z.'\-]+(?:\s+[A-Z][A-Z.'\-]+)*\b(?![a-z])")


_NUM_DATE = re.compile(r"(?<![\d/])(\d{1,2})/(\d{1,2})/(20\d\d)(?![\d/])")


def _past(m, today):
    try: d = dt.date(int(m.group(3)), MONTHS.index(m.group(1)[:3].lower()) + 1, int(m.group(2)))
    except ValueError: return ""
    return d.isoformat() if d <= today else ""


def _heads(title):
    """The headline inside a page title, longest guess first: "The Rise of the Lifelong Learner || St. Petersburg College", or a site name first and the headline cut short ("Mason Korea | News: Strengthening career connections:...", fnwords123 review)."""
    out = []
    for part in re.split(r"\s+\|+\s+", title or ""):
        part = re.sub(r"(?:\.\.\.|\u2026)\s*$", "", part.strip()).strip()
        for h in (part, re.sub(r"^[A-Z][\w&' ]{0,20}:\s+", "", part)):   # "News: ..." is a section label
            if len(h) >= 20 and h not in out: out.append(h)
    return sorted(out, key=len, reverse=True)   # a site name is shorter than a story's headline


def body_date(text, title="", today=None):
    """The date a page's text gives: the first date in its opening, else text_date. Never one after the day it was read: GTCC's pages opened with a closure notice ("campuses will close at noon on Wednesday, November 25, 2026") and were dated by it on 2026-11-24 (fnwords134 reload)."""
    today = today or dt.date.today()
    d = parse_date((text or "")[:1500])
    return d if d and d <= today.isoformat() else text_date(text, title, today)


def text_date(text, title="", today=None):
    """A date the body gives as the story's own, past the first 1500 characters a menu can fill: a press dateline ("Jan. 10, 2022, GRAND RAPIDS, Mich. –", a line opening with a date then a place in capitals), or a date on a short line of its own in the 300 characters after the headline (the title before its " | Site" tail). Not any date in the text: an event, a deadline or a © year is not when the page was written, and a future date never is (Parks' GRCC news page, fnwords74, read as undated)."""
    if not text: return ""
    today = today or dt.date.today()
    for m in _DATELINE.finditer(text):
        if (d := _past(m, today)): return d
    for head in _heads(title):
        i = text.find(head)
        if i >= 0:   # only the first head found in the text: the site name's place in a menu is no headline
            for line in text[i + len(head): i + len(head) + 300].split("\n"):   # a date line of its own ("Published on: January 27, 2015"), not a date inside a sentence ("join us on Mar. 3")
                m = _TEXT_DATE.search(line)
                if m and len((line[:m.start()] + line[m.end():]).split()) <= 4 and (d := _past(m, today)): return d
                n = _NUM_DATE.search(line)   # a byline's US numeric date: "Lori Trimmier | 8/31/2026" under SPC's story (fnwords119 review)
                if n and len((line[:n.start()] + line[n.end():]).split()) <= 4:
                    try: x = dt.date(int(n.group(3)), int(n.group(1)), int(n.group(2)))
                    except ValueError: continue
                    if x <= today: return x.isoformat()
            break
    return ""


EVENT_PATH = re.compile(r"/(?:events?|calendar)/[^/?#]+", re.I)


def event_date(url, ld_items, text, title="", today=None):
    """The day an event page's event is on, if it has passed: a JSON-LD Event's startDate, or on an /event/ address the date on a short line of its own just under the headline ("AI in the Job Search\n\nSeptember 25, 2024\n\n@\n\n1:00 pm"). An event page's words are about the event, so they are as old as it, not as old as the day it was posted: Beth Mitchell's Forsyth Works page was dated by its datePublished 2024-07-30 for her talk of 2024-09-25 (fnwords135 review). A coming event says nothing yet about when it was held, so the posting date stays. '' if none."""
    today = (today or dt.date.today()).isoformat()
    for it in ld_items or []:
        if re.search(r"Event\b", str(it.get("@type", ""))) and (d := parse_date(str(it.get("startDate", "")))):
            return d if d <= today else ""
    if not EVENT_PATH.search(urllib.parse.urlsplit(url or "").path) or not text: return ""
    heads = _heads(title) + [h for part in re.split(r"\s+[|\u2013\u2014-]\s+", title or "") if len(h := part.strip()) >= 8 for _ in [0]]
    for head in heads:
        i = text.find(head)
        if i < 0: continue
        for line in [x.strip() for x in text[i + len(head): i + len(head) + 200].split("\n") if x.strip()][:3]:
            m = _TEXT_DATE.search(line)
            if m and len((line[:m.start()] + line[m.end():]).split()) <= 4:
                d = _past(m, dt.date.fromisoformat(today))
                return d
        break
    return ""


def parse_html(html, url=""):
    """{title, text, links, date, date_from, published, people_ld}."""
    p = _Page(url)
    deadline = time.monotonic() + PARSE_SECONDS
    cut = False
    try:
        for i in range(0, len(html), 1 << 17):
            p.feed(html[i:i + (1 << 17)])
            if time.monotonic() > deadline: cut = True; break
        if not cut: p.close()
    except (AssertionError, ValueError):
        pass
    text = tidy("".join(p.chunks))
    items = [it for raw in p.jsonld for it in _ld_items(raw)]
    people = []
    for it in items:
        t = it.get("@type")
        if (t == "Person" or (isinstance(t, list) and "Person" in t)) and isinstance(it.get("name"), str) and isinstance(it.get("jobTitle"), str):
            people.append({"name": it["name"], "title": it["jobTitle"]})
    title = " ".join(htmllib.unescape(p.title).split())
    if len(p.times) > 1: p.meta["time_many"] = "1"
    date, how = page_date(url, p.meta, items, text, title)
    wp = re.search(r"""<link[^>]+rel=["']alternate["'][^>]+type=["']application/json["'][^>]+href=["'](https?://[^"']+/wp-json/wp/v2/[^"']+)|<link[^>]+href=["'](https?://[^"']+/wp-json/wp/v2/[^"']+)["'][^>]+type=["']application/json["']""", html[:300000], re.I) if how == "modified" else None
    published = "" if "wikipedia.org" in url else published_date(p.meta, items)
    if (ev := event_date(url, items, text, title)): date, how, published = ev, "event", ev
    return {"title": title, "text": text, "links": list(dict(p.links).items()), "date": date, "date_from": how, "published": published, "people_ld": people, "cut": cut, "wp_json": htmllib.unescape(wp.group(1) or wp.group(2)) if wp else ""}


def fold(s):
    """Lower case, no accents, single spaces: for matching only."""
    s = unicodedata.normalize("NFKD", s or "")
    return " ".join("".join(c for c in s if not unicodedata.combining(c)).casefold().replace("’", "'").split())


def domain_of(url):
    h = (urllib.parse.urlsplit(url).hostname or "").lower()
    return h[4:] if h.startswith("www.") else h


SECOND_LEVEL = {"ac", "co", "com", "edu", "gov", "net", "org"}


def registrable(host):
    """foo.bar.edu -> bar.edu; foo.ac.uk -> foo.ac.uk."""
    if re.fullmatch(r"[\d.]+|\[?[0-9a-f:]+\]?", host or ""): return host  # an IP address is its own site, not "0.1"
    parts = host.split(".")
    if len(parts) >= 3 and len(parts[-1]) == 2 and parts[-2] in SECOND_LEVEL: return ".".join(parts[-3:])
    return ".".join(parts[-2:])


def canonical(url):
    """One spelling per page: for merging results."""
    u = urllib.parse.urlsplit(url)
    host = (u.hostname or "").lower().removeprefix("www.")
    q = [(k, v) for k, v in urllib.parse.parse_qsl(u.query) if not re.match(r"(utm_|fbclid|gclid|mc_|ref$|source$)", k)]
    return urllib.parse.urlunsplit(("https", host, u.path.rstrip("/") or "/", urllib.parse.urlencode(q), ""))
