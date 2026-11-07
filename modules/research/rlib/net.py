"""Getting bytes: refuses local addresses and blocked GitHub orgs, is polite per host, and never hides a block."""
import contextlib, http.client, ipaddress, os, re, select, shutil, socket, socketserver, subprocess, tempfile, threading, time, unicodedata, posixpath, urllib.error, urllib.parse, urllib.request, zlib
from pathlib import Path

UA = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
MAX_BYTES = 12_000_000
MAX_HOPS = 6
MAX_SECONDS = 90   # for one whole reply, however slowly it drips
HOST_GAP = 1.0   # seconds between two requests to one host
ALLOW_PRIVATE = False   # the tests' local server turns this on; nothing else does


class Refused(Exception):
    """The URL may not be fetched at all (a local address, a blocked org, not http)."""


def denied_orgs():
    """The GitHub orgs this Hive refuses: one per line, # comments. No list denies nothing; a list that should be there and can't be read returns None, and all of GitHub is refused."""
    named = os.environ.get("CC_GITHUB_DENY")
    path = Path(named or Path.home() / ".cc" / "github-deny")
    try: lines = path.read_text().splitlines()
    except FileNotFoundError:
        return None if named else set()
    except (OSError, UnicodeError): return None
    orgs = (re.sub(r"\s", "", l.split("#")[0]).lower() for l in lines)
    return {o for o in orgs if re.fullmatch(r"[a-z0-9-]+", o)}


# Anywhere in the address, so a copy, a cache or a wrapper around a GitHub address counts as what it wraps.
_GH_PATH = re.compile(r"(?<![a-z0-9-])(?:[a-z0-9-]+\.)*(?:github\.com|githubusercontent\.com|ghcr\.io)\.?(?::\d+)?(?:/+([^?#\s]*)|(?=[?#\s]|$))")
_GH_PAGES = re.compile(r"(?<![a-z0-9-])([a-z0-9-]+)\.github\.io(?![a-z0-9-])")
_GH_ASK = re.compile(r"(?:^|[^a-z0-9-])(?:org|user|owner|repo)[:=]\s*([a-z0-9-]+)")
_GH_PREFIX = {"repos", "orgs", "users", "media", "enterprises"}


def _unwrapped(url):
    s = url.lower()
    for _ in range(3): s = urllib.parse.unquote_plus(s)
    s = unicodedata.normalize("NFKC", s).translate(str.maketrans({"。": ".", "．": ".", "｡": "."}))
    return s


# Hosts that serve a GitHub repo's files under another name: CDNs, editors, other forges' mirrors.
_MIRRORS = ("jsdelivr.net", "githack.com", "statically.io", "gitee.com", "deepwiki.com", "github.dev", "vscode.dev", "githubbox.com", "gitpod.io", "sourcegraph.com")
_MIRROR = re.compile(r"(?<![a-z0-9-])(?:[a-z0-9-]+\.)*(?:" + "|".join(re.escape(h) for h in _MIRRORS) + r")\.?(?![a-z0-9.-])")
# Routes that name a repo or owner by number, or search across owners: the URL alone can't say whose content comes back.
_GH_BLIND = re.compile(r"^(?:repositories|user|organizations|search|github-production-[a-z0-9-]*|[0-9]+)$")


def is_github(url):
    s = _unwrapped(url)
    return bool(_GH_PATH.search(s) or _GH_PAGES.search(s) or _MIRROR.search(s))


def github_owners(url):
    """Every owner a URL names on GitHub, however it's written: github.com., org.github.io, raw and media hosts, /orgs/x, ?q=org:x, an archive copy or other wrapper."""
    s, owners = _unwrapped(url), set()
    for m in _GH_PATH.finditer(s):
        parts = [p for p in posixpath.normpath("/" + (m.group(1) or "")).split("/") if p]
        if parts: owners.add(parts[1] if parts[0] in _GH_PREFIX and len(parts) > 1 else parts[0])
    owners.update(_GH_PAGES.findall(s))
    if is_github(url): owners.update(_GH_ASK.findall(s))
    return owners


def denied_url(url):
    """Does this address reach a GitHub org on the list?"""
    if not is_github(url): return False
    deny = denied_orgs()
    if deny is None: return True
    if not deny: return False
    s = _unwrapped(url)
    # A mirror or a search may carry the owner anywhere in the address, so any word of it that is a denied owner counts.
    if set(re.findall(r"[a-z0-9-]+", s)) & deny: return True
    if _MIRROR.search(s) and not _GH_PATH.search(s): return False
    owners = github_owners(url)
    for m in _GH_PATH.finditer(s):
        parts = [p for p in posixpath.normpath("/" + (m.group(1) or "")).split("/") if p]
        if parts and (_GH_BLIND.match(parts[0]) or (parts[0] in _GH_PREFIX and len(parts) > 1 and _GH_BLIND.match(parts[1]))): return True
    return bool(owners & deny)


def _public(addr):
    """Is this address on the public internet? Looks inside the forms that carry an IPv4 address."""
    ip = ipaddress.ip_address(addr.split("%")[0])
    if ip.version == 6:
        if ip in ipaddress.ip_network("64:ff9b::/96"): ip = ipaddress.ip_address(int(ip) & 0xffffffff)
        else: ip = ip.ipv4_mapped or ip.sixtofour or (ip.teredo[1] if ip.teredo else None) or ip
    return ip.is_global and not ip.is_multicast and not ip.is_reserved


_BAD_URL = re.compile(r"[\\\x00-\x1f\x7f]")


def check_url(url, resolve=True):
    """Raises Refused unless this is a public http(s) URL we may read. resolve=False is for a page already saved: same rules, no lookup.
    A backslash, a login or an escape in the host is refused outright: Python and a browser read those differently, so a check on one reading proves nothing about the other."""
    if _BAD_URL.search(url): raise Refused(f"a backslash or control character in the address: {url[:120]!r}")
    try:
        u = urllib.parse.urlsplit(url)
        host, port = u.hostname, u.port
    except ValueError as e: raise Refused(f"not a web address: {url[:120]!r} ({e})")
    if u.scheme not in ("http", "https") or not host: raise Refused(f"not a web address: {url[:120]}")
    if "@" in u.netloc or "%" in u.netloc or " " in u.netloc: raise Refused(f"a login or escape in the host: {url[:120]}")
    if denied_url(url): raise Refused("this Hive's list blocks that GitHub address")
    if ALLOW_PRIVATE: return
    host = host.lower().rstrip(".")
    if host == "localhost" or host.endswith((".localhost", ".local", ".internal")): raise Refused(f"local address: {host}")
    try:
        if not _public(host): raise Refused(f"local address: {host}")
        return
    except ValueError: pass   # a name, not an address
    if not resolve: return
    try: infos = socket.getaddrinfo(host, port or (443 if u.scheme == "https" else 80), type=socket.SOCK_STREAM)
    except (OSError, UnicodeError) as e: raise Refused(f"can't resolve {host}: {e}")
    for info in infos:
        if not _public(info[4][0]): raise Refused(f"local address: {host} is {info[4][0]}")


def _guarded_connection(address, timeout=None, source_address=None, **_):
    """Connect only to the public addresses the name resolves to at this moment, so a name that changes its answer after check_url gets nowhere."""
    if ALLOW_PRIVATE: return socket.create_connection(address, timeout, source_address)
    host, port = address
    try: infos = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except UnicodeError as e: raise OSError(f"can't resolve {host}: {e}")
    last = OSError(f"can't resolve {host}")
    for family, kind, proto, _, addr in infos:
        if not _public(addr[0]): raise Refused(f"local address: {host} is {addr[0]}")
    for family, kind, proto, _, addr in infos:
        s = socket.socket(family, kind, proto)
        try:
            if isinstance(timeout, (int, float)): s.settimeout(timeout)
            s.connect(addr)
            return s
        except OSError as e:
            last = e
            s.close()
    raise last


_request_clock = threading.local()


class _TimedResponse(http.client.HTTPResponse):
    """Shut down a dripped header or body at the request's absolute deadline."""
    def __init__(self, sock, *a, **k):
        super().__init__(sock, *a, **k)
        def expire():
            with contextlib.suppress(OSError): sock.shutdown(socket.SHUT_RDWR)
        remaining = getattr(_request_clock, "deadline", time.monotonic() + MAX_SECONDS) - time.monotonic()
        self._timer = threading.Timer(max(0, remaining), expire)
        self._timer.daemon = True
        self._timer.start()

    def close(self):
        self._timer.cancel()
        super().close()


class _GuardedHTTP(http.client.HTTPConnection):
    response_class = _TimedResponse
    def __init__(self, *a, **k):
        super().__init__(*a, **k)
        self._create_connection = socket.create_connection if urllib.request.getproxies() else _guarded_connection


class _GuardedHTTPS(http.client.HTTPSConnection):
    response_class = _TimedResponse
    def __init__(self, *a, **k):
        super().__init__(*a, **k)
        self._create_connection = socket.create_connection if urllib.request.getproxies() else _guarded_connection


class _HTTPHandler(urllib.request.HTTPHandler):
    def http_open(self, req): return self.do_open(_GuardedHTTP, req)


class _HTTPSHandler(urllib.request.HTTPSHandler):
    def https_open(self, req): return self.do_open(_GuardedHTTPS, req, context=self._context)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None


# Behind a proxy the machine's own proxy does the connecting, so there's nothing here to guard.
_opener = urllib.request.build_opener(_NoRedirect, _HTTPHandler, _HTTPSHandler)
_gate_lock, _gate = threading.Lock(), {}


def _wait_turn(host):
    """Hold this thread until the host's last request is HOST_GAP old."""
    with _gate_lock:
        now = time.monotonic()
        at = max(now, _gate.get(host, 0))
        _gate[host] = at + HOST_GAP
    if at > now: time.sleep(at - now)


class Resp:
    def __init__(self, url, status=0, body=b"", headers=None, error=""):
        self.url, self.status, self.body, self.headers, self.error = url, status, body, headers or {}, error

    @property
    def ctype(self): return self.headers.get("content-type", "").split(";")[0].strip().lower()

    @property
    def text(self):
        m = re.search(r"charset=([\w-]+)", self.headers.get("content-type", ""), re.I) or re.search(rb"<meta[^>]+charset=[\"']?([\w-]+)", self.body[:4000], re.I)
        enc = m.group(1) if m else "utf-8"
        enc = enc.decode("ascii", "replace") if isinstance(enc, bytes) else enc
        try: return self.body.decode(enc, "replace")
        except (LookupError, UnicodeError): return self.body.decode("utf-8", "replace")


def _read_body(r, headers, deadline):
    """At most MAX_BYTES, within the deadline. A reply shorter than it promised is an error, not a page."""
    chunks, size = [], 0
    read = getattr(r, "read1", r.read)
    while size < MAX_BYTES:
        if time.monotonic() > deadline: raise TimeoutError(f"the reply took over {MAX_SECONDS} s")
        c = read(min(65536, MAX_BYTES - size))
        if not c: break
        chunks.append(c); size += len(c)
    want = headers.get("content-length", "")
    if want.isdigit() and size < min(int(want), MAX_BYTES): raise EOFError(f"the reply stopped at {size} of {want} bytes")
    return b"".join(chunks)


def _unpack(body, enc):
    """Unpacks no further than MAX_BYTES: a small reply can claim to be gigabytes."""
    if enc not in ("gzip", "x-gzip", "deflate"): return body
    d = zlib.decompressobj(31 if "gzip" in enc else 15)
    try: out = d.decompress(body, MAX_BYTES + 1)
    except zlib.error:
        if "gzip" in enc: raise
        d = zlib.decompressobj(-15)   # "deflate" in the wild is often the raw stream, without the header the standard asks for
        out = d.decompress(body, MAX_BYTES + 1)
    if len(out) > MAX_BYTES: raise ValueError(f"the reply unpacks to over {MAX_BYTES} bytes")
    if not d.eof: raise EOFError("the compressed reply stops early")
    return out


def _origin(url):
    u = urllib.parse.urlsplit(url)
    return u.scheme, (u.hostname or "").lower(), u.port or (443 if u.scheme == "https" else 80)


def get(url, headers=None, timeout=25, data=None):
    """One GET (or POST with data), following redirects itself so that every hop gets checked. Refused propagates; network trouble comes back as Resp.error.
    Custom headers (API keys) and the body go only to the host asked for, never along a redirect to another one."""
    plain = {"User-Agent": UA, "Accept": "text/html,application/xhtml+xml,application/json,application/xml;q=0.9,*/*;q=0.8", "Accept-Language": "en-US,en;q=0.9", "Accept-Encoding": "gzip, deflate"}
    deadline, first = time.monotonic() + MAX_SECONDS, None
    for _ in range(MAX_HOPS):
        check_url(url)
        first = first or _origin(url)
        same = _origin(url) == first
        hdrs = {**plain, **(headers or {})} if same else plain
        _wait_turn(urllib.parse.urlsplit(url).hostname)
        if time.monotonic() >= deadline: return Resp(url, error="TimeoutError: the request deadline expired")
        _request_clock.deadline = deadline
        try:
            r = _opener.open(urllib.request.Request(url, headers=hdrs, data=data if same else None), timeout=min(timeout, max(0.001, deadline - time.monotonic())))
        except urllib.error.HTTPError as e:
            r = e
        except (OSError, ValueError, UnicodeError, http.client.HTTPException) as e:
            return Resp(url, error=f"{type(e).__name__}: {e}"[:300])
        try:
            status, h = r.getcode() or r.status, {k.lower(): v for k, v in r.headers.items()}
            if status in (301, 302, 303, 307, 308) and h.get("location"):
                try: url = urllib.parse.urljoin(url, h["location"])
                except ValueError as e: return Resp(url, status=status, headers=h, error=f"a bad redirect: {e}"[:300])
                if status == 303: data = None
                continue
            try:
                body = _unpack(_read_body(r, h, deadline), h.get("content-encoding", "").lower())
            except (OSError, EOFError, zlib.error, ValueError, http.client.HTTPException) as e:
                return Resp(url, status=status, headers=h, error=f"{type(e).__name__}: {e}"[:300])
            return Resp(url, status, body, h)
        finally:
            r.close()
    return Resp(url, error="too many redirects")


_chrome_slots = threading.Semaphore(2)   # a headless Chrome is a few hundred MB


def chrome_path():
    for name in ("google-chrome", "chromium", "chromium-browser", "google-chrome-stable", "chrome"):
        p = shutil.which(name)
        if p: return p
    mac = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
    return mac if os.path.exists(mac) else None


def _github_host(host):
    host = host.lower().rstrip(".")
    return any(host == h or host.endswith("." + h) for h in ("github.com", "githubusercontent.com", "github.io", "ghcr.io") + _MIRRORS)


class _ProxyHandler(socketserver.StreamRequestHandler):
    """Every request the browser makes comes through here, so what a page asks for is judged by where it really goes, not by how its address is spelt."""
    def handle(self):
        head = b""
        self.connection.settimeout(30)
        try:
            while b"\r\n\r\n" not in head and len(head) < 65536:
                c = self.connection.recv(65536)
                if not c: return
                head += c
            line, _, rest = head.partition(b"\r\n")
            method, target, version = line.decode("latin-1").split(" ", 2)
            if method == "CONNECT":
                host, _, port = target.rpartition(":")
                host, url = host.strip("[]"), None
            else:
                url = target
                u = urllib.parse.urlsplit(url)
                host, port = u.hostname, u.port or 80
            self.server.judge(host, url)
            up = _guarded_connection((host, int(port)), timeout=20)
        except Refused:
            return self._say(b"403 Forbidden")
        except (OSError, ValueError, UnicodeError):
            return self._say(b"502 Bad Gateway")
        try:
            if method == "CONNECT":
                self.connection.sendall(b"HTTP/1.1 200 Connection established\r\n\r\n")
            else:
                path = urllib.parse.urlunsplit(("", "", u.path or "/", u.query, ""))
                keep = [l for l in rest.split(b"\r\n") if not l.lower().startswith((b"proxy-connection:", b"connection:"))]
                up.sendall(f"{method} {path} {version}\r\n".encode("latin-1") + b"Connection: close\r\n" + b"\r\n".join(keep))
            self._pump(up)
        except OSError:
            pass
        finally:
            up.close()

    def _say(self, status):
        with contextlib.suppress(OSError): self.connection.sendall(b"HTTP/1.1 " + status + b"\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")

    def _pump(self, up):
        pair, ends = {self.connection: up, up: self.connection}, time.monotonic() + 120
        while time.monotonic() < ends:
            ready, _, _ = select.select(list(pair), [], [], 5)
            for s in ready:
                c = s.recv(65536)
                if not c: return
                pair[s].sendall(c)


class _Proxy(socketserver.ThreadingTCPServer):
    daemon_threads, allow_reuse_address = True, True

    def __init__(self, start_url):
        self.start_host = (urllib.parse.urlsplit(start_url).hostname or "").lower().rstrip(".")
        super().__init__(("127.0.0.1", 0), _ProxyHandler)

    def judge(self, host, url):
        """Raises Refused for what this page may not reach. Encrypted requests show only their host, so a page that isn't on GitHub can't reach GitHub through the browser at all when there's a list."""
        if url: check_url(url, resolve=False)
        else: check_url(f"https://{'[' + host + ']' if ':' in host else host}/", resolve=False)
        if _github_host(host) and denied_orgs() != set():
            # CONNECT hides the path. Only a Pages owner's isolated host can be
            # allowed; github.com itself can serve every denied owner.
            isolated = host.lower().rstrip(".") == self.start_host and self.start_host.endswith(".github.io")
            if not isolated: raise Refused("an encrypted GitHub path cannot be checked against the deny list")


@contextlib.contextmanager
def _browser_proxy(start_url):
    proxy = _Proxy(start_url)
    t = threading.Thread(target=proxy.serve_forever, kwargs={"poll_interval": 0.2}, daemon=True)
    t.start()
    try:
        yield proxy.server_address[1]
    finally:
        proxy.shutdown()
        proxy.server_close()


@contextlib.contextmanager
def _throwaway_profile():
    """A temporary profile folder. Chrome's helpers can still be writing as it exits, so removal retries and a leftover never ends a run."""
    prof = tempfile.mkdtemp(prefix="research-chrome-")
    try:
        yield prof
    finally:
        for _ in range(4):
            shutil.rmtree(prof, ignore_errors=True)
            if not os.path.exists(prof): break
            time.sleep(0.5)


def _run_chrome(cmd, timeout):
    """(html, error). A hostile page can reach a browser, so it keeps its sandbox unless the owner of this machine said otherwise."""
    extras = [[], ["--no-sandbox"]] if os.environ.get("RESEARCH_CHROME_NO_SANDBOX") else [[]]
    for extra in extras:
        try:
            p = subprocess.run(cmd[:1] + extra + cmd[1:], capture_output=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            return "", "Chrome timed out"
        except OSError as e:
            return "", f"Chrome: {e}"
        html = p.stdout.decode("utf-8", "replace")
        if html.strip(): return html, ""
        if b"sandbox" not in p.stderr.lower(): return "", "Chrome returned no page"
    return "", "Chrome's sandbox doesn't work on this machine; RESEARCH_CHROME_NO_SANDBOX=1 runs pages without it, if you accept that"


def chrome_dump(url, budget_ms=7000, timeout=50):
    """(html, error) after the page's JavaScript ran, in a throwaway profile. Holds no login and doesn't get round a challenge page: it comes back as what it is."""
    check_url(url)
    exe = chrome_path()
    if not exe: return "", "no Chrome on this machine"
    with _chrome_slots, _throwaway_profile() as prof:
        base = [exe, "--headless=new", "--disable-gpu", "--no-first-run", "--disable-extensions", "--disable-dev-shm-usage", "--disable-background-networking", f"--user-data-dir={prof}", f"--user-agent={UA}", f"--virtual-time-budget={budget_ms}", "--dump-dom"]
        if ALLOW_PRIVATE: return _run_chrome(base + [url], timeout)
        with _browser_proxy(url) as port:
            # The browser's own lookups find nothing, so every request has to go through the proxy, which checks where it really leads.
            guard = [f"--proxy-server=http://127.0.0.1:{port}", "--proxy-bypass-list=<-loopback>", "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--disable-quic", "--force-webrtc-ip-handling-policy=disable_non_proxied_udp", "--webrtc-ip-handling-policy=disable_non_proxied_udp", "--enforce-webrtc-ip-permission-check"]
            return _run_chrome(base + guard + [url], timeout)
