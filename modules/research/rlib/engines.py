"""Search sources. Each returns Hits. A source that fails says so and the others carry on: a thin result must never look like an empty world."""
import base64, concurrent.futures as cf, html as htmllib, json, os, re, urllib.parse, xml.etree.ElementTree as ET
from dataclasses import dataclass, asdict
from pathlib import Path
from . import net
from .extract import canonical, domain_of, fold, parse_date, parse_html, tidy

@dataclass
class Hit:
    url: str
    title: str = ""
    snippet: str = ""
    date: str = ""
    source: str = ""
    def dict(self): return asdict(self)

q_ = urllib.parse.quote_plus
BASES = {}   # the tests point a source at a fake server


def base(name, default): return BASES.get(name, default)


class SourceError(Exception): pass


def _get(url, **k):
    r = net.get(url, **k)
    if r.error: raise SourceError(r.error)
    if r.status != 200: raise SourceError(f"HTTP {r.status}")
    return r


def _strip(s): return tidy(parse_html(s or "")["text"]).replace("\n", " ")


def _rss(body, src, n):
    try: root = ET.fromstring(body)
    except ET.ParseError as e: raise SourceError(f"bad feed: {e}")
    out = []
    for it in root.iter("item"):
        src_el = it.find("source")
        title = (it.findtext("title") or "").strip()
        out.append(Hit(url=(it.findtext("link") or "").strip(), title=title, snippet=_strip(it.findtext("description")) + (f" [{src_el.text}]" if src_el is not None and src_el.text else ""), date=parse_date(it.findtext("pubDate") or "") or _rfc822(it.findtext("pubDate")), source=src))
    return out[:n]


def _rfc822(s):
    import email.utils
    try: return email.utils.parsedate_to_datetime(s).date().isoformat() if s else ""
    except (TypeError, ValueError): return ""


def _feed_or_fail(hits):
    """An empty feed can mean nothing matched or that the engine is limiting us, and it doesn't say which. Calling it a failure keeps it out of the cache and in the report."""
    if not hits: raise SourceError("empty feed: nothing matched, or the engine is limiting us")
    return hits


def bing(q, n): return _feed_or_fail(_rss(_get(base("bing", "https://www.bing.com") + f"/search?q={q_(q)}&format=rss&count={min(n, 50)}").body, "bing", n))
def bing_news(q, n): return _feed_or_fail(_rss(_get(base("bing", "https://www.bing.com") + f"/news/search?q={q_(q)}&format=rss&count={min(n, 50)}").body, "bing_news", n))
def google_news(q, n): return _rss(_get(base("google_news", "https://news.google.com") + f"/rss/search?q={q_(q)}&hl=en-US&gl=US&ceid=US:en").body, "google_news", n)


def _parse_ddg(html, n):
    links = re.findall(r"""<a[^>]*href=["']([^"']+)["'][^>]*class=['"]result-link['"][^>]*>(.*?)</a>|<a[^>]*class=['"]result-link['"][^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>""", html, re.S)
    snips = re.findall(r"""class=['"]result-snippet['"][^>]*>(.*?)</td>""", html, re.S)
    out = []
    for i, m in enumerate(links):
        u, t = (m[0], m[1]) if m[0] else (m[2], m[3])
        u = htmllib.unescape(u)
        if "uddg=" in u: u = urllib.parse.parse_qs(urllib.parse.urlsplit(u).query).get("uddg", [u])[0]
        if u.startswith("//"): u = "https:" + u
        if "duckduckgo.com/y.js" in u: continue   # an ad
        out.append(Hit(url=u, title=_strip(t), snippet=_strip(snips[i]) if i < len(snips) else "", source="ddg"))
    return out[:n]


def _browser_page(url):
    html, err = net.chrome_dump(url, budget_ms=4000)
    if err: raise SourceError(err)
    return html


def ddg(q, n):
    """The plain request is quick, but this engine often answers it with a 202 and no results. The browser usually still gets them."""
    url = base("ddg", "https://lite.duckduckgo.com") + f"/lite/?q={q_(q)}"
    r = net.get(url)
    hits = _parse_ddg(r.text, n) if r.status == 200 and not r.error else []
    if hits: return hits
    page = _browser_page(url)
    hits = _parse_ddg(page, n)
    # A real empty answer says so. Anything else is a block page or a challenge, and that isn't "no results".
    if not hits and not re.search(r"no\s+(?:more\s+)?results", page, re.I): raise SourceError(f"no results page (plain request gave HTTP {r.status}): probably blocked")
    return hits


def _bing_target(u):
    """Bing wraps results in a click link holding the real address in u=a1<base64>."""
    u = htmllib.unescape(u)
    if "bing.com/ck/a" not in u: return u
    enc = urllib.parse.parse_qs(urllib.parse.urlsplit(u).query).get("u", [""])[0]
    if not enc.startswith("a1"): return ""
    try: return base64.urlsafe_b64decode(enc[2:] + "=" * (-len(enc[2:]) % 4)).decode("utf-8", "replace")
    except ValueError: return ""


def _parse_bing(html, n):
    out = []
    for block in re.split(r"""<li[^>]*class=["'][^"']*\bb_algo\b""", html)[1:]:
        m = re.search(r"""<h[1-3][^>]*>.*?<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>""", block, re.S)
        if not m: continue
        u = _bing_target(m.group(1))
        sn = re.search(r"""<p[^>]*>(.*?)</p>""", block, re.S)
        if u.startswith("http"): out.append(Hit(url=u, title=_strip(m.group(2)), snippet=_strip(sn.group(1)) if sn else "", source="bing_web"))
    return out[:n]


def bing_web(q, n):
    """Bing's real result page, read in a browser. It reads real pages where the feed is thin, but it can ignore quotes and site:, so merge() holds its results to the query."""
    return _parse_bing(_browser_page(base("bing", "https://www.bing.com") + f"/search?q={q_(q)}&count={min(n, 30)}"), n)


def wikipedia(q, n):
    d = _get(base("wikipedia", "https://en.wikipedia.org") + f"/w/api.php?action=query&list=search&format=json&srlimit={min(n, 50)}&srsearch={q_(q)}").body
    return [Hit(url="https://en.wikipedia.org/wiki/" + urllib.parse.quote(x["title"].replace(" ", "_")), title=x["title"], snippet=_strip(x.get("snippet")), date=parse_date(x.get("timestamp", "")), source="wikipedia") for x in json.loads(d)["query"]["search"]]


def hn(q, n):
    d = _get(base("hn", "https://hn.algolia.com") + f"/api/v1/search?query={q_(q)}&hitsPerPage={min(n, 50)}").body
    return [Hit(url=x.get("url") or f"https://news.ycombinator.com/item?id={x['objectID']}", title=x.get("title") or x.get("story_title") or "", snippet=_strip(x.get("story_text") or x.get("comment_text"))[:300], date=parse_date(x.get("created_at", "")), source="hn") for x in json.loads(d)["hits"]]


def arxiv(q, n):
    root = ET.fromstring(_get(base("arxiv", "https://export.arxiv.org") + f"/api/query?search_query=all:{q_(q)}&max_results={min(n, 50)}").body)
    A = "{http://www.w3.org/2005/Atom}"
    return [Hit(url=e.findtext(A + "id", ""), title=" ".join(e.findtext(A + "title", "").split()), snippet=" ".join(e.findtext(A + "summary", "").split())[:400], date=parse_date(e.findtext(A + "published", "")), source="arxiv") for e in root.iter(A + "entry")]


def openalex(q, n):
    d = json.loads(_get(base("openalex", "https://api.openalex.org") + f"/works?search={q_(q)}&per-page={min(n, 50)}").body)
    out = []
    for x in d.get("results", []):
        who = ", ".join(a.get("author", {}).get("display_name", "") for a in x.get("authorships", [])[:4])
        out.append(Hit(url=(x.get("primary_location") or {}).get("landing_page_url") or x.get("doi") or x.get("id", ""), title=x.get("display_name") or "", snippet=f"{who}; cited {x.get('cited_by_count', 0)}", date=x.get("publication_date") or "", source="openalex"))
    return out


def crossref(q, n):
    d = json.loads(_get(base("crossref", "https://api.crossref.org") + f"/works?query={q_(q)}&rows={min(n, 50)}").body)
    return [Hit(url=x.get("URL", ""), title=" ".join(x.get("title") or [""]), snippet=x.get("container-title", [""])[0] if x.get("container-title") else "", date="-".join(f"{p:02d}" for p in (x.get("issued", {}).get("date-parts") or [[]])[0] if p is not None), source="crossref") for x in d["message"]["items"]]


def github(q, n):
    d = json.loads(_get(base("github", "https://api.github.com") + f"/search/repositories?q={q_(q)}&per_page={min(n, 50)}", headers={"Accept": "application/vnd.github+json"}).body)
    deny = net.denied_orgs()
    return [Hit(url=x["html_url"], title=x["full_name"], snippet=f"{x.get('description') or ''} ({x.get('stargazers_count', 0)} stars)", date=parse_date(x.get("pushed_at", "")), source="github") for x in d.get("items", []) if deny is not None and x["owner"]["login"].lower() not in deny]


def on_site(url, site):
    """Is this address on that site? By host, so x.com doesn't take in netflix.com; site: may carry a path, as in linkedin.com/in."""
    host, _, path = site.lower().partition("/")
    try: parts = urllib.parse.urlsplit(url)
    except ValueError: return False
    h = (parts.hostname or "").lower()
    return (h == host or h.endswith("." + host)) and (not path or parts.path.lower().lstrip("/").startswith(path))


def _site(name, sites):
    """A site's public pages through a general engine. Snippets only say where to look: they prove nothing, and these pages mostly won't open."""
    def run(q, n):
        out, errors = [], []
        for s in sites:
            for engine in (ddg, bing_web):
                try: hits = [h for h in engine(f"{q} site:{s}", n) if on_site(h.url, s)]
                except SourceError as e: errors.append(str(e)); continue
                for h in hits: h.source = name
                out += hits
                if hits: break
        if errors and not out: raise SourceError(errors[0])
        return out[:n]
    return run


x_posts = _site("x", ["x.com"])
linkedin = _site("linkedin", ["linkedin.com/in"])
reddit = _site("reddit", ["reddit.com"])


# Sources that need a key. Each is off until its key is there, and each key's plan is the owner's to choose.
def _key(name):
    v = os.environ.get(f"{name.upper()}_API_KEY", "")
    if v: return v.strip()
    try: return (Path.home() / ".config" / "research" / f"{name}-key").read_text().strip()
    except OSError: return ""


def brave(q, n):
    d = json.loads(_get(base("brave", "https://api.search.brave.com") + f"/res/v1/web/search?q={q_(q)}&count={min(n, 20)}", headers={"X-Subscription-Token": _key("brave"), "Accept": "application/json"}).body)
    return [Hit(url=x["url"], title=x.get("title", ""), snippet=_strip(x.get("description")), date=parse_date(x.get("page_age", "")), source="brave") for x in d.get("web", {}).get("results", [])]


def tavily(q, n):
    d = json.loads(_get(base("tavily", "https://api.tavily.com") + "/search", headers={"Content-Type": "application/json"}, data=json.dumps({"api_key": _key("tavily"), "query": q, "max_results": min(n, 20)}).encode()).body)
    return [Hit(url=x["url"], title=x.get("title", ""), snippet=x.get("content", "")[:400], date=parse_date(x.get("published_date") or ""), source="tavily") for x in d.get("results", [])]


def exa(q, n):
    d = json.loads(_get(base("exa", "https://api.exa.ai") + "/search", headers={"x-api-key": _key("exa"), "Content-Type": "application/json"}, data=json.dumps({"query": q, "numResults": min(n, 20)}).encode()).body)
    return [Hit(url=x["url"], title=x.get("title") or "", snippet=(x.get("text") or "")[:400], date=parse_date(x.get("publishedDate") or ""), source="exa") for x in d.get("results", [])]


SOURCES = {"ddg": ddg, "bing_web": bing_web, "bing": bing, "bing_news": bing_news, "google_news": google_news, "wikipedia": wikipedia, "hn": hn, "arxiv": arxiv, "openalex": openalex, "crossref": crossref, "github": github, "x": x_posts, "linkedin": linkedin, "reddit": reddit, "brave": brave, "tavily": tavily, "exa": exa}
KEYED = {"brave", "tavily", "exa"}
GROUPS = {"web": ["ddg", "bing_web", "bing"], "news": ["bing_news", "google_news"], "papers": ["openalex", "arxiv", "crossref"], "code": ["github", "hn"], "social": ["x", "reddit", "hn"], "people": ["linkedin", "ddg", "bing_web"], "all": ["ddg", "bing_web", "bing", "bing_news", "google_news", "wikipedia", "hn", "openalex", "arxiv", "github"]}


def expand(names):
    """Names and group names to sources. Keyed sources join web when their key is present."""
    out = []
    for n in names or ["web"]:
        for s in GROUPS.get(n, [n]):
            if s not in SOURCES: raise SystemExit(f"research: no source {s}. Sources: {', '.join(SOURCES)}; groups: {', '.join(GROUPS)}")
            if s in KEYED and not _key(s): raise SystemExit(f"research: {s} needs a key in {s.upper()}_API_KEY or ~/.config/research/{s}-key. The plan is the owner's to choose.")
            if s not in out: out.append(s)
    if {"web", "all"} & set(names or ["web"]): out += [s for s in sorted(KEYED) if _key(s) and s not in out]
    return out


STOP = set("a an and are as at be by for from has have in is it of on or that the this to was what when where which who why will with how".split())


def parse_query(q):
    """(phrases, words, sites) in a query, folded for matching."""
    sites = [fold(x) for x in re.findall(r"\bsite:(\S+)", q)]
    rest = re.sub(r"\bsite:\S+", " ", q)
    phrases = [fold(x) for x in re.findall(r'"([^"]+)"', rest)]
    words = [w for w in re.findall(r"[\w'+#.-]+", fold(re.sub(r'"[^"]+"', " ", rest))) if w not in STOP and len(w) > 1]
    return phrases, words, sites


def relevance(q, title, snippet, url):
    """0 to 1: how much of the query this result actually speaks to. One engine here ignores quotes, so the results get held to them."""
    phrases, words, _ = parse_query(q)
    hay = fold(f"{title} {snippet} {urllib.parse.unquote(url).replace('-', ' ').replace('_', ' ')}")
    terms = len(phrases) * 2 + len(words)
    if not terms: return 1.0
    got = sum(2 for p in phrases if p in hay) + sum(1 for w in words if w in hay)
    return got / terms * (1.0 if all(p in hay for p in phrases) else 0.4)


def merge(lists, n=30, rrf=60, q=""):
    """Reciprocal-rank fusion across sources, weighted by how well each result speaks to the query. A page several engines agree on rises; each keeps its sources. A site: filter is enforced here."""
    best = {}
    _, _, sites = parse_query(q)
    for hits in lists:
        for rank, h in enumerate(hits):
            if not h.url.startswith("http"): continue
            if sites and not any(on_site(h.url, st) for st in sites): continue
            if net.denied_url(h.url): continue
            try: k, dom = canonical(h.url), domain_of(h.url)
            except ValueError: continue  # an address a page or engine made up
            e = best.setdefault(k, {"url": h.url, "title": h.title, "snippet": h.snippet, "date": h.date, "sources": [], "score": 0.0, "domain": dom})
            e["score"] += 1 / (rrf + rank + 1)
            if h.source not in e["sources"]: e["sources"].append(h.source)
            if len(h.snippet) > len(e["snippet"]): e["snippet"] = h.snippet
            e["date"] = e["date"] or h.date
    for e in best.values():
        e["relevance"] = round(relevance(q, e["title"], e["snippet"], e["url"]), 2)
        e["score"] = round(e["score"] * (0.15 + e["relevance"]), 5)
    return sorted(best.values(), key=lambda e: -e["score"])[:n]


def search(q, sources, n=10, case=None):
    """Run the sources side by side. (merged, {source: error}). A source that errors stays out of the merge and stays in the report, and isn't cached, so a rerun asks it again."""
    errors, lists = {}, []
    def one(s):
        cached = case.cached_search(q, s) if case else None
        if cached is not None: return s, [Hit(**h) for h in cached], ""
        try:
            hits = SOURCES[s](q, n)
            if case: case.save_search(q, s, [h.dict() for h in hits], "")
            return s, hits, ""
        except net.Refused as e:
            return s, [], f"refused: {e}"
        except Exception as e:  # one engine's bad answer, of any kind, mustn't cost the others' results
            if case: case.save_search(q, s, [], f"{type(e).__name__}: {e}"[:200])
            return s, [], f"{type(e).__name__}: {e}"[:200]
    with cf.ThreadPoolExecutor(max_workers=max(1, len(sources))) as ex:
        for s, hits, err in ex.map(one, sources):
            lists.append(hits)
            if err: errors[s] = err
    return merge(lists, n=max(n * len(sources), n), q=q), errors
