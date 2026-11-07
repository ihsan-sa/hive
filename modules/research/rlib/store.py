"""A case: one folder holding everything a piece of research saw, so each claim can be traced to the words on a page and the moment they were read."""
import datetime as dt, hashlib, json, os, re, sqlite3, threading
from pathlib import Path

SCHEMA = """
create table if not exists pages(url text primary key, final_url text, via text, status text, http int, title text, text text, sha text, fetched_at text, date text, date_from text, snapshot text, links text, people_ld text, note text);
create table if not exists searches(q text, source text, at text, hits text, error text, primary key(q, source));
create table if not exists meta(k text primary key, v text);
"""


def safe_cell(v):
    """A CSV cell that a spreadsheet won't run as a formula. Names and titles here come from web pages, and a page can write what it likes."""
    if not isinstance(v, str) or v[:1] not in ("=", "+", "-", "@", "\t", "\r"): return v
    try: float(v); return v
    except ValueError: return "'" + v


def safe_row(row): return {k: safe_cell(v) for k, v in row.items()}


def now(): return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def default_root():
    return Path(os.environ.get("RESEARCH_CASE") or Path.home() / ".cache" / "research" / "default")


class Case:
    def __init__(self, root=None):
        self.root = Path(root or default_root())
        self.root.mkdir(parents=True, exist_ok=True)
        self.db = sqlite3.connect(self.root / "case.db", check_same_thread=False, timeout=30)
        self.db.row_factory = sqlite3.Row
        self.lock = threading.Lock()
        with self.lock:
            self.db.executescript(SCHEMA)
            self.db.commit()

    def cached_search(self, q, source, hours=24):
        with self.lock: r = self.db.execute("select at, hits, error from searches where q=? and source=?", (q, source)).fetchone()
        if not r or r["error"]: return None
        age = dt.datetime.now(dt.timezone.utc) - dt.datetime.strptime(r["at"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
        # An empty answer isn't kept: asking again costs little, and it may have been a block that looked like no results.
        return (json.loads(r["hits"]) or None) if age.total_seconds() < hours * 3600 else None

    def save_search(self, q, source, hits, error):
        with self.lock:
            self.db.execute("insert or replace into searches values(?,?,?,?,?)", (q, source, now(), json.dumps(hits), error))
            self.db.commit()

    def save_page(self, p):
        p = dict(p, sha=hashlib.sha256((p.get("text") or "").encode()).hexdigest()[:16], fetched_at=p.get("fetched_at") or now())
        with self.lock:
            self.db.execute("insert or replace into pages(url,final_url,via,status,http,title,text,sha,fetched_at,date,date_from,snapshot,links,people_ld,note) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
                            (p["url"], p.get("final_url"), p.get("via"), p.get("status"), p.get("http"), p.get("title"), p.get("text"), p["sha"], p["fetched_at"], p.get("date"), p.get("date_from"), p.get("snapshot"), json.dumps(p.get("links") or []), json.dumps(p.get("people_ld") or []), p.get("note")))
            self.db.commit()
        return p

    @staticmethod
    def _row(r):
        d = dict(r)
        d["links"], d["people_ld"] = json.loads(d["links"] or "[]"), json.loads(d["people_ld"] or "[]")
        return d

    def page(self, url):
        with self.lock: r = self.db.execute("select * from pages where url=?", (url,)).fetchone()
        return self._row(r) if r else None

    def pages(self, ok_only=True):
        with self.lock: rows = self.db.execute("select * from pages" + (" where status='ok'" if ok_only else "")).fetchall()
        return [self._row(r) for r in rows]

    def counts(self):
        with self.lock:
            s = self.db.execute("select count(*) c, sum(error!='') e from searches").fetchone()
            p = self.db.execute("select status, count(*) c from pages group by status").fetchall()
        return {"searches": s["c"], "search_errors": s["e"] or 0, "pages": {r["status"]: r["c"] for r in p}}
