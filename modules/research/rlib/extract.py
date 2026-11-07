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

    def handle_starttag(self, tag, attrs):
        a = {k.lower(): (v or "") for k, v in attrs}
        if tag == "base" and a.get("href"): self.base = urllib.parse.urljoin(self.base, a["href"])
        if tag == "title" and not self.title: self.in_title = True
        if tag == "meta":
            k = (a.get("property") or a.get("name") or a.get("itemprop") or "").lower()
            if k and a.get("content"): self.meta.setdefault(k, a["content"])
        if tag == "time" and a.get("datetime"): self.meta.setdefault("time", a["datetime"])
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


def parse_date(s):
    """YYYY-MM-DD from an ISO-ish or written date, else ''."""
    if not s: return ""
    m = re.search(r"\b(20\d\d|19\d\d)-(\d\d)-(\d\d)", s)
    if m:
        try: return dt.date(*map(int, m.groups())).isoformat()
        except ValueError: pass
    m = _TEXT_DATE.search(s)
    if m:
        try: return dt.date(int(m.group(3)), MONTHS.index(m.group(1)[:3].lower()) + 1, int(m.group(2))).isoformat()
        except ValueError: pass
    return ""


def page_date(url, meta, ld_items, text):
    """When the page says it was published, and how it knows. A copyright line is not a date."""
    if "wikipedia.org" in url: return "", ""  # the first date there is from the article's subject (an infobox's founding year)
    for k in DATE_META:
        d = parse_date(meta.get(k, ""))
        if d: return d, "meta"
    for it in ld_items:
        for k in ("datePublished", "dateModified"):
            d = parse_date(str(it.get(k, "")))
            if d: return d, "meta"
    m = re.search(r"/(20\d\d)[/-](\d\d?)[/-](\d\d?)(?:/|-|$)", urllib.parse.urlsplit(url).path)
    if m:
        try: return dt.date(*map(int, m.groups())).isoformat(), "url"
        except ValueError: pass
    d = parse_date(text[:1500])
    return (d, "text") if d else ("", "")


def parse_html(html, url=""):
    """{title, text, links, date, date_from, people_ld}."""
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
    date, how = page_date(url, p.meta, items, text)
    return {"title": " ".join(htmllib.unescape(p.title).split()), "text": text, "links": list(dict(p.links).items()), "date": date, "date_from": how, "people_ld": people, "cut": cut}


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
    parts = host.split(".")
    if len(parts) >= 3 and len(parts[-1]) == 2 and parts[-2] in SECOND_LEVEL: return ".".join(parts[-3:])
    return ".".join(parts[-2:])


def canonical(url):
    """One spelling per page: for merging results."""
    u = urllib.parse.urlsplit(url)
    host = (u.hostname or "").lower().removeprefix("www.")
    q = [(k, v) for k, v in urllib.parse.parse_qsl(u.query) if not re.match(r"(utm_|fbclid|gclid|mc_|ref$|source$)", k)]
    return urllib.parse.urlunsplit(("https", host, u.path.rstrip("/") or "/", urllib.parse.urlencode(q), ""))
