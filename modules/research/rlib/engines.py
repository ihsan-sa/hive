"""Search sources. Each returns Hits. A source that fails says so and the others carry on: a thin result must never look like an empty world."""
import base64, concurrent.futures as cf, html as htmllib, json, os, re, time, urllib.parse, xml.etree.ElementTree as ET
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
        out.append(Hit(url=_feed_target((it.findtext("link") or "").strip()), title=title, snippet=_strip(it.findtext("description")) + (f" [{src_el.text}]" if src_el is not None and src_el.text else ""), date=parse_date(it.findtext("pubDate") or "") or _rfc822(it.findtext("pubDate")), source=src))
    return out[:n]


def _feed_target(u):
    """Bing's news feed links to its own click counter, bing.com/news/apiclick.aspx?url=<the page>. Kept wrapped, every hit sits on bing.com: a site: filter drops it and two engines' copies of one page never meet."""
    if "bing.com/news/apiclick" not in u: return u
    t = urllib.parse.parse_qs(urllib.parse.urlsplit(u).query).get("url", [""])[0]
    return t if t.startswith("http") else u


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

def _parse_ddg_html(html, n):
    """html.duckduckgo.com: one div.result block per answer, the link in a.result__a (wrapped in a //duckduckgo.com/l/?uddg= redirect), the snippet in a.result__snippet."""
    out = []
    for block in re.split(r"""<div[^>]*class=["'][^"']*?\bresult\b""", html)[1:]:
        if re.match(r"""[^"']*result--ad""", block): continue   # an ad block
        m = re.search(r"""<a[^>]*class=["']result__a["'][^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>|<a[^>]*href=["']([^"']+)["'][^>]*class=["']result__a["'][^>]*>(.*?)</a>""", block, re.S)
        if not m: continue
        u, t = (m.group(1), m.group(2)) if m.group(1) else (m.group(3), m.group(4))
        u = htmllib.unescape(u)
        if "uddg=" in u: u = urllib.parse.parse_qs(urllib.parse.urlsplit(u).query).get("uddg", [u])[0]
        if u.startswith("//"): u = "https:" + u
        if "duckduckgo.com/y.js" in u: continue   # an ad
        sn = re.search(r"""class=["']result__snippet["'][^>]*>(.*?)</a>""", block, re.S)
        out.append(Hit(url=u, title=_strip(t), snippet=_strip(sn.group(1)) if sn else "", source="ddg"))
    return out[:n]


def ddg_html(q, n):
    """The same engine's html front end, on its own host, so the per-host gap in net queues its asks apart from lite's."""
    url = base("ddg_html", "https://html.duckduckgo.com") + f"/html/?q={q_(q)}"
    r = net.get(url)
    hits = _parse_ddg_html(r.text, n) if r.status == 200 and not r.error else []
    if hits: return hits
    if r.status == 200 and not r.error and re.search(r"no\s+(?:more\s+)?results", r.text, re.I): return []
    raise SourceError(f"no results page (HTTP {r.status}{': ' + r.error if r.error else ''}): probably blocked")


def ddg_twice(q, n):
    """ddg asked twice (lite and html front ends), the answers interleaved without repeats, as one ddg list (so merge doesn't count it as two engines agreeing). ddg answers the same query with different pages from call to call: 57% of the URLs repeated across two runs on `site:` queries, against 97% for bing, and fnwords56 lost 5 verified leads to pages only an earlier ddg answer had held. A hunt's site searches use it; one answer stands if the other call fails."""
    def one(ask):
        try: return ask(q, n), None
        except SourceError as e: return None, e
    # Side by side, and on two hosts: one after the other, fnwords57's site searches waited 338 worker-seconds against 148; both on lite, the per-host gap queued every ddg ask behind the doubled load (fnwords58: 3.6 s engine wait a search).
    with cf.ThreadPoolExecutor(max_workers=2) as ex: got = list(ex.map(one, (ddg, ddg_html)))
    answers = [a for a, _ in got if a is not None]
    if not answers: raise next(e for _, e in got if e)
    out, seen = [], set()
    for i in range(max(len(a) for a in answers)):
        for a in answers:
            if i >= len(a): continue
            try: k = canonical(a[i].url)
            except ValueError: continue
            if k not in seen: seen.add(k); out.append(a[i])
    return out[:2 * n]


def ddg_deep(q, n):
    """ddg twice (see ddg_twice) and then lite's next result pages, up to n hits, as one ddg list. Discovery asks it: the same two discovery queries named 21 to 31 organisations a run over fnwords150 to fnwords153, from a union of 41, because each engine gave its first 10 to 19 hits and those changed from run to run; bing's feed and lite's plain GET give no second page, but lite's next-page form does, with the page's vqd token (15 more hits a page, 2026-11-26). A later page that fails leaves the first page's answer standing."""
    out = list(ddg_twice(q, 10))
    seen = set()
    for h in out:
        try: seen.add(canonical(h.url))
        except ValueError: pass
    root = base("ddg", "https://lite.duckduckgo.com") + "/lite/"
    try: r = net.get(root + f"?q={q_(q)}")
    except Exception: return out[:n]
    html, s = (r.text if r.status == 200 and not r.error else ""), 0
    for _ in range(3):
        vqd = re.search(r"""name=["']vqd["']\s+value=["']([^"']+)["']""", html)
        nxt = [int(x) for x in re.findall(r"""name=["']s["']\s+value=["'](\d+)["']""", html) if int(x) > s]
        if len(out) >= n or not vqd or not nxt: break
        s = min(nxt)
        try: r = net.get(root + "?" + urllib.parse.urlencode({"q": q, "s": s, "dc": s + 1, "v": "l", "o": "json", "api": "d.js", "vqd": vqd.group(1), "kl": "wt-wt"}))
        except Exception: break
        html = r.text if r.status == 200 and not r.error else ""
        got = _parse_ddg(html, 50)
        if not got: break
        for h in got:
            try: k = canonical(h.url)
            except ValueError: continue
            if k not in seen: seen.add(k); out.append(h)
    return out[:n]

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


SOURCES = {"ddg": ddg, "ddg_twice": ddg_twice, "ddg_deep": ddg_deep, "bing_web": bing_web, "bing": bing, "bing_news": bing_news, "google_news": google_news, "wikipedia": wikipedia, "hn": hn, "arxiv": arxiv, "openalex": openalex, "crossref": crossref, "github": github, "x": x_posts, "linkedin": linkedin, "reddit": reddit, "brave": brave, "tavily": tavily, "exa": exa}
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
    phrases = parse_query(q)[0]
    for e in best.values():
        rel = relevance(q, e["title"], e["snippet"], e["url"])
        e["relevance"] = round(rel, 2)
        # Word overlap only sinks results that barely speak to the query. Above FULL_FIT the engines' own order stands:
        # a canonical page (a repo, a paper's home) has a short title and says fewer of the query's words than a page written to echo them.
        held = phrases and not all(p in fold(f"{e['title']} {e['snippet']} {urllib.parse.unquote(e['url']).replace('-', ' ').replace('_', ' ')}") for p in phrases)
        e["score"] = round(e["score"] * (0.15 + (rel if held else 0.85 * min(1.0, rel / FULL_FIT))), 5)
    return sorted(best.values(), key=lambda e: -e["score"])[:n]


FULL_FIT = 0.25  # the share of the query's words a result needs before its engines' rank alone decides


SECS, _SECS_LOCK = {}, __import__("threading").Lock()   # seconds each source spent answering, cache hits aside: a hunt logs it, to say which engine its searches wait on
LIVE = {}   # source -> searches sent to it, cache hits aside: a hunt's report says when its pass ran from the case's cache (fnwords146 review)


def search(q, sources, n=10, case=None, grace=None, late=None):
    """Run the sources side by side. (merged, {source: error}). A source that errors stays out of the merge and stays in the report, and isn't cached, so a rerun asks it again.
    With `grace` (seconds), once one source has answered with hits the search waits at most that long for the rest, then returns what it has: the late ones finish on their own and save their answer to the case, so a rerun has it, but this search doesn't wait (fnwords67: 33 of 292 searches waited over 6 s, up to 48 s, and took 371 of the 923 engine-seconds). Their names go in `late` (a list) if one is given; they are neither answers nor errors."""
    errors, lists = {}, []
    def one(s):
        cached = case.cached_search(q, s) if case else None
        if cached is not None: return s, [Hit(**h) for h in cached], ""
        t0 = time.monotonic()
        with _SECS_LOCK: LIVE[s] = LIVE.get(s, 0) + 1
        try:
            hits = SOURCES[s](q, n)
            if case: case.save_search(q, s, [h.dict() for h in hits], "")
            return s, hits, ""
        except net.Refused as e:
            return s, [], f"refused: {e}"
        except Exception as e:  # one engine's bad answer, of any kind, mustn't cost the others' results
            if case: case.save_search(q, s, [], f"{type(e).__name__}: {e}"[:200])
            return s, [], f"{type(e).__name__}: {e}"[:200]
        finally:
            with _SECS_LOCK: SECS[s] = SECS.get(s, 0.0) + time.monotonic() - t0
    ex = cf.ThreadPoolExecutor(max_workers=max(1, len(sources)))
    try:
        pending = {ex.submit(one, s): s for s in sources}
        until, got = None, {}
        while pending:
            left = None if until is None else until - time.monotonic()
            if left is not None and left <= 0: break
            done, _ = cf.wait(pending, timeout=left, return_when=cf.FIRST_COMPLETED)
            for f in done:
                del pending[f]
                s, hits, err = f.result()
                got[s] = hits
                if err: errors[s] = err
                if hits and grace is not None and until is None: until = time.monotonic() + grace
        lists = [got[s] for s in sources if s in got]   # in the sources' order, as before, so merge breaks ties the same way
        if late is not None: late.extend(pending.values())
    finally:
        ex.shutdown(wait=False)
    return merge(lists, n=max(n * len(sources), n), q=q), errors
