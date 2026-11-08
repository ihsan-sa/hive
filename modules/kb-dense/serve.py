#!/usr/bin/env python3
"""kb-dense: nearest passages by meaning for `kb find`, over a unix socket.

One JSON line in, one JSON line out, then the connection closes:
  {"db": PATH, "q": TEXT, "n": 30}  ->  {"ok": true, "hits": [[path, line, end], ...], "ms": 41}
  {"db": PATH, "warm": 60}          ->  waits (up to that many seconds) until the row map of that index is ready
  {"stats": 1}                      ->  sizes and memory
Errors are {"ok": false, "err": "..."}; the caller (kb.py) then answers with the word search alone.

Vectors live in one store keyed by a hash of the exact text that was embedded, so one store serves any index
(the live one, a snapshot). A passage whose text has no vector yet is simply not in the answer.
CPU only. The vectors are fp16 on disk and memory-mapped; the product runs in chunks, never as one fp32 matrix."""
import os, signal
os.environ["CUDA_VISIBLE_DEVICES"] = ""
os.environ.setdefault("OMP_NUM_THREADS", "2"); os.environ.setdefault("MKL_NUM_THREADS", "2")
os.environ.setdefault("TOKENIZERS_PARALLELISM", "false"); os.environ.setdefault("HF_HUB_OFFLINE", "1")
import hashlib, json, socketserver, sqlite3, struct, sys, threading, time
import numpy as np

HOME = os.path.expanduser("~")
DIR = os.environ.get("KB_DENSE_DIR", HOME + "/.cache/kb-dense")
SOCK = os.environ.get("KB_DENSE_SOCK", DIR + "/sock")
LIVE = os.path.realpath(os.path.expanduser(os.environ.get("KB_DENSE_LIVE", "~/.cache/hive-kb/index.db")))  # the only index whose missing vectors are embedded
MODEL = os.environ.get("KB_DENSE_MODEL", "BAAI/bge-small-en-v1.5"); MAXLEN = 256; DIM = 384
STORE = f"{DIR}/store/{MODEL.split('/')[-1]}-{MAXLEN}"
SEED = os.path.expanduser(os.environ.get("KB_DENSE_SEED", "~/projects/kb-search/cache/dense/2026-11-06r2-bge-small-en-v1.5-256"))
SEED_DB = os.path.expanduser(os.environ.get("KB_DENSE_SEED_DB", "~/projects/kb-search/snap/2026-11-06r2/index.db"))
REFRESH = float(os.environ.get("KB_DENSE_REFRESH", 180))   # seconds between looks at the live index
BATCH = int(os.environ.get("KB_DENSE_BATCH", 8)); DUTY = float(os.environ.get("KB_DENSE_DUTY", 1.0))  # sleep DUTY x the batch time after each batch
MAXMAPS = 4; PERDOC = 2; DEEP = 200
PFX = "Represent this sentence for searching relevant passages: "

def log(*a): print(time.strftime("%H:%M:%S"), *a, file=sys.stderr, flush=True)

def text_of(t, h, b): return f"{t or ''} > {h or ''}\n{b or ''}"[:MAXLEN * 6]   # exactly what engines/dense/build.py embeds
def key_of(s): return struct.unpack("<q", hashlib.sha1(s.encode("utf-8", "replace")).digest()[:8])[0]

class Store:
    """keys.bin: int64 per vector; vec.f16: DIM float16 per vector. Append only, memory-mapped for reading."""
    def __init__(self, path):
        os.makedirs(path, exist_ok=True); self.k = path + "/keys.bin"; self.v = path + "/vec.f16"; self.lock = threading.Lock()
        for f in (self.k, self.v): open(f, "ab").close()
        n = min(os.path.getsize(self.k) // 8, os.path.getsize(self.v) // (2 * DIM))   # a torn append leaves the shorter file's count
        with open(self.k, "rb") as f: keys = np.frombuffer(f.read(n * 8), dtype=np.int64)
        self.row = {int(k): i for i, k in enumerate(keys)}; self.n = n; self._map()
    def _map(self):
        self.mm = np.memmap(self.v, dtype=np.float16, mode="r", shape=(self.n, DIM)) if self.n else np.zeros((0, DIM), np.float16)
    def add(self, keys, E):
        """E: float16 (len(keys), DIM) -> the rows they got"""
        with self.lock:
            with open(self.v, "r+b") as f:
                f.truncate(self.n * 2 * DIM); f.seek(0, 2); f.write(np.ascontiguousarray(E, dtype=np.float16).tobytes()); f.flush()
            with open(self.k, "r+b") as f:
                f.truncate(self.n * 8); f.seek(0, 2); f.write(np.asarray(keys, dtype=np.int64).tobytes()); f.flush()
            rows = list(range(self.n, self.n + len(keys)))
            for k, r in zip(keys, rows): self.row[int(k)] = r
            self.n += len(keys); self._map()
        return rows
    def scores(self, q):
        """the product of every stored vector with q, in chunks (no fp32 copy of the matrix)"""
        with self.lock: mm, n = self.mm, self.n
        out = np.empty(n, np.float32); q = q.astype(np.float32)
        for i in range(0, n, 8192): out[i:i + 8192] = mm[i:i + 8192].astype(np.float32) @ q
        return out

def seed(st):
    """First start: take the vectors of the snapshot the project built (ids are fts rowids of SEED_DB), keyed by their text."""
    if st.n or not (os.path.exists(SEED + "/E.npy") and os.path.exists(SEED_DB)): return
    t0 = time.time(); E = np.load(SEED + "/E.npy", mmap_mode="r"); ids = np.load(SEED + "/ids.npy"); at = {int(i): j for j, i in enumerate(ids)}
    c = sqlite3.connect(f"file:{SEED_DB}?mode=ro", uri=True); keys = np.zeros(len(ids), np.int64); got = np.zeros(len(ids), bool)
    for rid, t, h, b in c.execute("SELECT rowid, title, head, body FROM fts"):
        j = at.get(rid)
        if j is not None: keys[j] = key_of(text_of(t, h, b)); got[j] = True
    c.close(); keep = np.nonzero(got)[0]
    for i in range(0, len(keep), 20000):
        ix = keep[i:i + 20000]; st.add(keys[ix], E[ix])
    log(f"seeded {len(keep)} vectors in {time.time() - t0:.1f}s")

class Index:
    """The row map of one index file: for every passage its place and the store row of its vector (-1: none yet)."""
    def __init__(self, path): self.path = path; self.sig = (); self.ids = np.zeros(0, np.int64); self.vec = np.zeros(0, np.int32); self.rowsig = {}; self.missing = []; self.ready = threading.Event(); self.lock = threading.Lock(); self.paths = []
    def stat(self):
        s = []
        for suf in ("", "-wal"):
            try: x = os.stat(self.path + suf); s.append((x.st_mtime_ns, x.st_size))
            except OSError: s.append(None)
        return tuple(s)
    def build(self, st):
        sig = self.stat(); c = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True, timeout=20)
        try:
            rows = c.execute("SELECT chunk.id, doc.path, chunk.line, chunk.end, chunk.hash, doc.mtime FROM chunk JOIN doc ON doc.id=chunk.doc ORDER BY chunk.id").fetchall()
            old = self.rowsig; new = {}; need = []
            for cid, p, l, e, h, m in rows:
                s = (p, l, e, h, m); o = old.get(cid)
                if o and o[0] == s: new[cid] = o
                else: new[cid] = (s, None); need.append(cid)
            if len(need) > 5000: pull = c.execute("SELECT rowid, title, head, body FROM fts")
            else: pull = (c.execute("SELECT rowid, title, head, body FROM fts WHERE rowid=?", (i,)).fetchone() for i in need)
            want = set(need)
            for r in pull:
                if r and r[0] in want: new[r[0]] = (new[r[0]][0], key_of(text_of(*r[1:])))
        finally: c.close()
        n = len(rows); ids = np.empty(n, np.int64); vec = np.full(n, -1, np.int32); miss = []; pidx = np.empty(n, np.int32); line = np.empty(n, np.int32); end = np.empty(n, np.int32)
        pathno = {}; paths = []
        for j, (cid, p, l, e, h, m) in enumerate(rows):
            s, k = new[cid]; ids[j] = cid; line[j] = l; end[j] = e
            if p not in pathno: pathno[p] = len(paths); paths.append(p)
            pidx[j] = pathno[p]
            r = st.row.get(k) if k is not None else None
            if r is None: miss.append(j)
            else: vec[j] = r
        with self.lock:
            self.ids, self.vec, self.line, self.end, self.pidx, self.paths, self.rowsig, self.missing, self.sig = ids, vec, line, end, pidx, paths, new, miss, sig
        self.ready.set()
        return len(need)
    def top(self, sc, n):
        v = self.vec; s = np.where((v >= 0) & (v < len(sc)), sc[np.clip(v, 0, max(len(sc) - 1, 0))], -2.0)   # a vector stored after sc was taken has no score yet: skip it, don't fail
        k = min(DEEP, len(s))
        if not k: return []
        ix = np.argpartition(-s, k - 1)[:k]; ix = ix[np.argsort(-s[ix], kind="stable")]
        out, seen = [], {}
        for j in ix:
            if s[j] <= -2.0: break
            p = self.paths[self.pidx[j]]
            if seen.get(p, 0) >= PERDOC: continue
            seen[p] = seen.get(p, 0) + 1; out.append([p, int(self.line[j]), int(self.end[j])])
            if len(out) >= n: break
        return out

class Model:
    def __init__(self):
        import torch
        from transformers import AutoModel, AutoTokenizer
        torch.set_num_threads(2); self.torch = torch
        self.tok = AutoTokenizer.from_pretrained(MODEL); self.m = AutoModel.from_pretrained(MODEL).eval()
    def emb(self, texts):
        torch = self.torch
        with torch.inference_mode():
            x = self.tok(texts, padding=True, truncation=True, max_length=MAXLEN, return_tensors="pt")
            y = self.m(**x).last_hidden_state[:, 0]
            return torch.nn.functional.normalize(y, dim=-1).numpy()

class Service:
    def __init__(self):
        self.st = Store(STORE); seed(self.st); self.model = None; self.mlock = threading.Lock(); self.maps = {}; self.mapslock = threading.Lock(); self.born = time.time()
        self.embedded = 0
    def load_model(self):
        with self.mlock:
            if self.model is None: self.model = Model()
        return self.model
    def index(self, db, start=True):
        with self.mapslock:
            ix = self.maps.get(db)
            if ix is None:
                if len(self.maps) >= MAXMAPS:
                    old = [p for p in self.maps if p != LIVE]
                    if old: del self.maps[old[0]]
                ix = self.maps[db] = Index(db)
                if start: threading.Thread(target=self._build, args=(ix,), daemon=True).start()
            else:   # most recently used last
                self.maps[db] = self.maps.pop(db)
        return ix
    def _build(self, ix):
        try: t0 = time.time(); n = ix.build(self.st); log(f"map {ix.path}: {len(ix.ids)} passages, {n} read, {len(ix.missing)} without vector, {time.time() - t0:.1f}s")
        except Exception as e: log("map failed", ix.path, repr(e)); ix.ready.set()
    def search(self, req):
        t0 = time.time(); db = os.path.realpath(os.path.expanduser(req["db"]))   # one map per file, however it was reached (a snapshot linked into a worktree)
        if not os.path.exists(db): return {"ok": False, "err": "no such index"}
        ix = self.index(db)
        if "warm" in req:
            ok = ix.ready.wait(float(req["warm"])); return {"ok": ok and len(ix.ids) > 0, "n": len(ix.ids), "missing": len(ix.missing)}
        if not ix.ready.is_set() or not len(ix.ids): return {"ok": False, "err": "loading"}
        q = self.load_model().emb([PFX + str(req["q"])[:2000]])[0]
        hits = ix.top(self.st.scores(q), int(req.get("n", 30)))
        return {"ok": True, "hits": hits, "ms": int((time.time() - t0) * 1000)}
    def stats(self):
        rss = 0
        for l in open("/proc/self/status"):
            if l.startswith("VmRSS"): rss = int(l.split()[1]) // 1024
        return {"ok": True, "rss_mb": rss, "vectors": self.st.n, "maps": {p: {"passages": len(i.ids), "missing": len(i.missing)} for p, i in self.maps.items()}, "embedded": self.embedded, "up_s": int(time.time() - self.born)}
    def refresh_loop(self):
        while True:
            try: self.refresh()
            except Exception as e: log("refresh failed", repr(e))
            time.sleep(REFRESH)
    def refresh(self):
        if not os.path.exists(LIVE): return
        ix = self.index(LIVE, start=False)
        if not ix.ready.is_set() or ix.stat() != ix.sig:
            self._build(ix)
        while ix.missing:
            todo = ix.missing[:BATCH]; c = sqlite3.connect(f"file:{LIVE}?mode=ro", uri=True, timeout=20)
            try: rows = [c.execute("SELECT title, head, body FROM fts WHERE rowid=?", (int(ix.ids[j]),)).fetchone() for j in todo]
            finally: c.close()
            t0 = time.time()
            texts = [text_of(*r) if r else "" for r in rows]
            E = self.load_model().emb(texts).astype(np.float16)
            keys = [key_of(t) for t in texts]
            fresh = [(j, k, e) for j, k, e, r in zip(todo, keys, E, rows) if r and k not in self.st.row]
            if fresh:
                rs = self.st.add([k for _, k, _ in fresh], np.stack([e for _, _, e in fresh]))
                for (j, _, _), r in zip(fresh, rs): ix.vec[j] = r
            for j, k in zip(todo, keys):   # another passage with the same text, or one that vanished
                if ix.vec[j] < 0 and k in self.st.row: ix.vec[j] = self.st.row[k]
            with ix.lock: ix.missing = [j for j in ix.missing if ix.vec[j] < 0 and j not in set(todo)]
            self.embedded += len(todo)
            time.sleep(max(0.05, (time.time() - t0) * DUTY))
            if ix.stat() != ix.sig: break   # the index moved on: look again at the next round

class Handler(socketserver.StreamRequestHandler):
    def handle(self):
        self.request.settimeout(10)
        try:
            req = json.loads(self.rfile.readline(1 << 20))
            r = S.stats() if "stats" in req else S.search(req)
        except Exception as e: r = {"ok": False, "err": f"{type(e).__name__}: {e}"[:200]}
        try: self.wfile.write((json.dumps(r) + "\n").encode()); self.wfile.flush()
        except OSError: pass

class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True; request_queue_size = 32

def alive(path):
    import socket
    s = socket.socket(socket.AF_UNIX); s.settimeout(1)
    try: s.connect(path); return True
    except OSError: return False
    finally: s.close()

def main():
    global S
    os.makedirs(DIR, mode=0o700, exist_ok=True); os.chmod(DIR, 0o700)
    if os.path.exists(SOCK):
        if alive(SOCK): sys.exit("already running")
        os.unlink(SOCK)
    S = Service()
    old = os.umask(0o177); srv = Server(SOCK, Handler); os.umask(old)
    threading.Thread(target=S.refresh_loop, daemon=True).start()
    log("listening on", SOCK, "store", STORE, S.st.n, "vectors")
    signal.signal(signal.SIGTERM, lambda *a: sys.exit(0))   # systemctl stop: leave through the finally, so no dead socket stays behind
    try: srv.serve_forever()
    finally:
        try: os.unlink(SOCK)
        except OSError: pass

if __name__ == "__main__": main()
