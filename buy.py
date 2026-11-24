#!/usr/bin/env python3
"""hive buy: an agent asks to buy parts, the person approves (or declines) the exact purchase in their Link app, and the agent gets a
one-time card for that one checkout. Stripe Link's Agent Wallet (link-cli) does the money side; this keeps the Hive's rules around it.

The rules are warden's ruling of 2026-11-23 (post 11231556881e, thread 11231554379b), enforced here where code can:
  1. The person's approval in the Link app is the only go to spend. Nothing here approves: link() runs only spend-request create,
     retrieve and cancel and auth status, and refuses any approve, policy or recurring flag however it is spelt.
  2. One card, one purchase: the request names the merchant, the items and the FULL checkout total (shipping and tax stated, even
     when 0). A cart that comes out higher or different means cancel and ask again; a card is never kept or reused.
  3. One-time only: no recurring request can be made from here.
  4. The card is a credential: link-cli writes it to a 0600 file under ~/.hive/buy-cards (a 0700 dir); stdout, the ledger and the
     board only ever see the last four digits. The file is deleted, and the card canceled at Link, at done, cancel, decline or expiry
     (every hive buy call sweeps, and the hive-buy-sweep timer does every 10 min). All agents share one user: 0600 keeps out other
     users, not other agents.
  5. done requires the checkout recording and the receipt, and attaches both in the thread.
  6. Signing in to Link is the person's: hive buy setup shows the state and their step; no agent runs auth login.

  hive buy ask --thread T --merchant NAME --url URL --item TEXT [--detail TEXT] --amount 37.63 --shipping 0 --tax 2.13 --why TEXT
             [--line "name;qty;unit price"]... [--currency usd] [--test]
  hive buy wait ID [--timeout 1800]   wait for the person's answer in Link; on approval the card goes to its 0600 file
  hive buy card ID                    where the card file is and what it may be used for (never the number itself)
  hive buy record ID --steps STEPS.json [--contact FILE]  the checkout in Chrome on screen record, fields masked, frames OCR'd
                                    (core/buy_record.mjs); $contact.* tokens come from ~/.hive/buy-contact.json (0600, the person's)
  hive buy record --dry --url URL --steps STEPS.json [--contact FILE] [--max-total N]  a practice run: no card, placeholders for who
                                    and where; only an explicit --contact gives its city, state and zip for the shipping and tax quote
  hive buy done ID --recording PATH --receipt PATH [--charged 37.63]
  hive buy cancel ID [--why TEXT]
  hive buy list [--all] [--json] | show ID | setup | sweep
  hive buy audit [--scan] [--json]    every finished buy against the warden's six conditions, and the measure; exit 1 on a breach

Ledger: ~/.hive/buys.jsonl, one event per line; a buy's state is its events merged in order. HIVE_LINK_CLI, HIVE_BUY_HOME and HIVE_BIN
override the link-cli binary, the state dir and the hive used to post (the tests use stubs)."""
import argparse, datetime as dt, json, os, re, shutil, subprocess, sys, time, urllib.parse
from pathlib import Path

HOME = Path.home()
STATE = Path(os.environ.get("HIVE_BUY_HOME") or HOME / ".hive")
LEDGER, CARDS, CONF, RECS = STATE / "buys.jsonl", STATE / "buy-cards", STATE / "buy.json", STATE / "buy-recordings"
CONTACT = STATE / "buy-contact.json"  # the person's email, phone, name and delivery address (0600, filled by the person)
LINK = os.environ.get("HIVE_LINK_CLI") or str(HOME / ".local" / "bin" / "link-cli")
Hive = os.environ.get("HIVE_BIN") or str(HOME / "core" / "hive")
CAP_USD = 200.0          # hive's own ceiling per purchase (Link's is 500); the person may change it in ~/.hive/buy.json {"max": N}
LINK_MAX_MINOR = 50000
VALID_HOURS = 3          # how long an approved card stays usable; Link's least is 3 h (its default is 7 days)
CARD_MAX_AGE = 6 * 3600  # a card file older than this goes, whatever the ledger says
CURRENCIES = {"usd", "cad", "eur", "gbp", "aud"}  # two-decimal ones only, so dollars x 100 is right
FORBIDDEN = ("--approve", "--use-approval-policy", "--approval-detail", "--recurring-interval", "--recurring-interval-count",
             "--no-request-approval", "--credential-type", "--payment-method-id", "--force", "--yes")
ALLOWED = {("spend-request", "create"), ("spend-request", "retrieve"), ("spend-request", "cancel"), ("auth", "status")}
def flag(a): return re.sub(r"[-_]", "", a.split("=")[0]).lower()   # --useApprovalPolicy, --use_approval_policy, --use-approval-policy alike
FORBIDDEN_N = {flag(f) for f in FORBIDDEN}
def forbidden(a):
    n = flag(a)
    return a.startswith("-") and (n in FORBIDDEN_N or n == "y" or (n != "requestapproval" and ("approv" in n or "polic" in n or "recurr" in n)))
OPEN = {"created", "pending_approval", "requires_action"}
LIVE = OPEN | {"approved"}
ENDED = {"denied", "expired", "failed", "canceled", "succeeded", "done"}

def now(): return dt.datetime.now(dt.timezone.utc)
def iso(t=None): return (t or now()).isoformat(timespec="seconds")
def die(msg, code=1): print(f"hive buy: {msg}", file=sys.stderr); sys.exit(code)
def cap_usd():
    try: return float(json.loads(CONF.read_text()).get("max", CAP_USD))
    except (OSError, ValueError, TypeError, AttributeError): return CAP_USD

def money(s, what):
    """Dollars as typed ("37.63", "$37.63", "0") to minor units; refuses anything else or more than two decimals."""
    m = re.fullmatch(r"\$?\s*(\d{1,6})(?:\.(\d{1,2}))?", str(s).strip())
    if not m: die(f"{what} must be an amount like 37.63, got {s!r}")
    return int(m.group(1)) * 100 + int((m.group(2) or "0").ljust(2, "0"))
def fmt(minor, cur="usd"):
    sym = {"usd": "$", "cad": "CA$", "aud": "A$", "eur": "€", "gbp": "£"}.get(cur, cur.upper() + " ")
    return f"{sym}{minor // 100:,}.{minor % 100:02d}"
def kv(s):
    """A value for link-cli's key:value,key:value flags: it splits on commas with no escape, so a comma becomes a semicolon."""
    return re.sub(r"\s*,\s*", "; ", str(s)).replace("\n", " ").strip()

# ---- the ledger ----
def events():
    try: lines = LEDGER.read_text(errors="replace").splitlines()
    except OSError: return []
    out = []
    for l in lines:
        try: out.append(json.loads(l))
        except ValueError: pass
    return out
def record(ev):
    STATE.mkdir(parents=True, exist_ok=True)
    ev = {"at": iso(), **ev}
    assert not re.search(r"\b\d{13,19}\b", json.dumps(ev)), "a card number must never reach the ledger"
    with LEDGER.open("a") as f: f.write(json.dumps(ev) + "\n")
def buys():
    st = {}
    for e in events():
        if e.get("id"): st.setdefault(e["id"], {}).update({k: v for k, v in e.items() if k not in ("at", "ev")}, updated=e.get("at"))
    return st
def get(bid, exact=False):
    st = buys(); b = st.get(bid)
    if not b and not exact:
        hits = [k for k in st if k.endswith(bid) or k.startswith(bid)]
        if len(hits) == 1: b = st[hits[0]]
    return b or die(f"no buy {bid!r} (hive buy list --all" + (": done, cancel and card want the whole id)" if exact else ")"))

# ---- link-cli ----
def link(*args, timeout=120):
    if tuple(args[:2]) not in ALLOWED: raise SystemExit(f"hive buy: refusing link-cli {' '.join(args[:2])}: only {sorted(ALLOWED)} (warden 11231556881e)")
    bad = [a for a in args if forbidden(str(a))]
    if bad: raise SystemExit(f"hive buy: refusing to pass {bad} to link-cli (warden 11231556881e)")
    argv = [LINK, *args, "--format", "json"]
    try: p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    except FileNotFoundError: die(f"link-cli not found at {LINK}: npm i -g --prefix ~/.local @stripe/link-cli")
    except subprocess.TimeoutExpired: return None, "link-cli timed out"
    out = (p.stdout or "").strip()
    try: j = json.loads(out) if out else None
    except ValueError: j = None
    if isinstance(j, list): j = next((x for x in reversed(j) if isinstance(x, dict)), None)  # a streamed command answers with a list
    if p.returncode != 0 or (isinstance(j, dict) and j.get("code") and not j.get("id")):
        msg = (j or {}).get("message") if isinstance(j, dict) else None
        code = (j or {}).get("code") if isinstance(j, dict) else None
        return None, (f"[{code}] " if code else "") + scrub(msg or (p.stderr or out or f"exit {p.returncode}").strip())[:600]
    return j, None
def scrub(s): return re.sub(r"\b(\d[ -]?){13,19}\b", "[card]", s or "")

def last4(j):
    c = (j or {}).get("card") or (j if isinstance(j, dict) else {}) or {}
    if not isinstance(c, dict): return None
    for k in ("last4", "last_four"):
        if re.fullmatch(r"\d{4}", str(c.get(k) or "")): return str(c[k])
    n = re.sub(r"\D", "", str(c.get("number") or ""))
    return n[-4:] if len(n) >= 12 else None
def last4_file(p):
    """The last four digits from the 0600 card file; the number is read into memory only and never leaves this function."""
    try: return last4(json.loads(Path(p).read_text()))
    except (OSError, ValueError, AttributeError): return None
def short_url(u): return re.sub(r"^https?://(www\.)?", "", u or "").rstrip("/")

# ---- the card file ----
def card_path(bid): return CARDS / f"{re.sub(r'[^A-Za-z0-9_-]', '_', bid)}.json"
def card_dir():
    CARDS.mkdir(parents=True, exist_ok=True); os.chmod(CARDS, 0o700); return CARDS
def shred(bid):
    p = card_path(bid)
    if not os.path.lexists(p): return False
    try:
        if not p.is_symlink():
            fd = os.open(p, os.O_RDWR | getattr(os, "O_NOFOLLOW", 0))
            try: n = os.fstat(fd).st_size; os.write(fd, b"\0" * n); os.fsync(fd)
            finally: os.close(fd)
    except OSError: pass
    p.unlink(missing_ok=True); return True
def revoke(bid):
    """Cancel the spend request at Link, which revokes its card: done, cancel and sweep all call this, so a card can't be fetched again."""
    j, err = link("spend-request", "cancel", bid)
    return err
def sweep():
    """Card files go once their buy has ended, once Link's expiry has passed, or once they are older than CARD_MAX_AGE."""
    if not CARDS.exists(): return []
    gone, st, t = [], buys(), time.time()
    for p in CARDS.glob("*.json"):
        b = next((v for k, v in st.items() if card_path(k) == p), None)
        exp = parse_t((b or {}).get("expires_at"))
        if (b is None or b.get("status") in ENDED or (exp and exp < now()) or t - p.lstat().st_mtime > CARD_MAX_AGE):
            bid = (b or {}).get("id") or p.stem
            shred(bid) if b else p.unlink(missing_ok=True); gone.append(bid)
            if b and not b.get("revoked"):
                err = revoke(bid)
                record({"ev": "sweep", "id": bid, "card_file": None, **({"revoked": True} if not err else {"revoke_error": err}),
                        **({"status": "expired"} if b.get("status") not in ENDED else {})})
    return gone
def parse_t(s):
    try: t = dt.datetime.fromisoformat(str(s).replace("Z", "+00:00")) if s else None
    except ValueError: return None
    return t.replace(tzinfo=dt.timezone.utc) if t and t.tzinfo is None else t

# ---- the post: a sentence and the ```buy card the dash draws ----
def card_json(b):
    keys = ("id", "status", "merchant", "url", "item", "detail", "amount", "currency", "shipping", "tax", "lines", "last4", "expires_at",
            "approve_by", "approval_url", "agent", "thread", "test", "receipt", "recording", "charged", "why_cancel")
    return json.dumps({k: b[k] for k in keys if b.get(k) not in (None, "", [])}, ensure_ascii=False, indent=1)
def post(b, lead, files=()):
    if os.environ.get("HIVE_BUY_NOPOST"): print(lead); return None
    body = f"{lead}\n\n```buy\n{card_json(b)}\n```\n"
    cmd = [Hive, "post", "--thread", b["thread"]]
    for f in files: cmd += ["--file", str(Path(f).expanduser())]
    p = subprocess.run(cmd, input=body, text=True, capture_output=True)
    if p.returncode: print(f"hive buy: posting failed: {p.stderr.strip()[:300]}", file=sys.stderr)
    else: print(p.stdout.strip())
    return p.returncode == 0

# ---- commands ----
def cmd_ask(a):
    sweep()
    cur = a.currency.lower()
    if cur not in CURRENCIES: die(f"currency {cur!r}: one of {', '.join(sorted(CURRENCIES))}")
    amount, ship, tax = money(a.amount, "--amount"), money(a.shipping, "--shipping"), money(a.tax, "--tax")
    if amount <= 0: die("--amount must be more than 0")
    if amount > min(LINK_MAX_MINOR, round(cap_usd() * 100)):
        die(f"{fmt(amount, cur)} is over hive's cap of {fmt(round(cap_usd() * 100), cur)} a purchase (Link's is $500): split it or ask the person to raise max in {CONF}")
    if ship + tax >= amount: die("--shipping plus --tax leave nothing for the goods: --amount is the whole checkout total")
    if not re.fullmatch(r"https://[^\s,/]+\.[^\s,]+", a.url): die("--url must be the merchant's https:// page for the item (no commas or spaces)")
    if len(a.why.strip()) < 20: die("--why: a sentence the person reads in Link on why this part and this merchant")
    lines = []
    for raw in a.line or []:
        parts = [x.strip() for x in raw.split(";")]
        if len(parts) != 3 or not parts[0]: die(f"--line {raw!r}: want \"name;qty;unit price\"")
        try: q = int(parts[1])
        except ValueError: die(f"--line {raw!r}: qty must be a whole number")
        if q < 1: die(f"--line {raw!r}: qty must be at least 1")
        lines.append({"name": parts[0][:200], "quantity": q, "unit_amount": money(parts[2], f"--line {raw!r} price")})
    goods = amount - ship - tax
    if lines and sum(l["quantity"] * l["unit_amount"] for l in lines) != goods:
        have = sum(l["quantity"] * l["unit_amount"] for l in lines)
        die(f"the lines add up to {fmt(have, cur)} but --amount minus shipping and tax is {fmt(goods, cur)}: the request must be the exact checkout total")
    if not lines: lines = [{"name": a.item[:200], "quantity": 1, "unit_amount": goods}]
    agent = os.environ.get("HIVE_NAME") or "agent"
    ctx = (f"Checkout total {fmt(amount, cur)} incl. shipping {fmt(ship, cur)} and tax {fmt(tax, cur)}, one time, at {a.merchant[:120]}."
           f" Asked by Hive agent {agent[:40]} (thread {a.thread[:40]}); the card is used once for this cart only and deleted after."
           f" Item: {a.item.strip()[:300]}" + (f" ({a.detail.strip()[:400]})" if a.detail else "") + f". Why: {a.why.strip()[:500]}")
    exp = now() + dt.timedelta(hours=VALID_HOURS, minutes=10)
    argv = ["spend-request", "create", "--merchant-name", kv(a.merchant)[:120], "--merchant-url", a.url, "--context", ctx[:1500],
            "--amount", str(amount), "--currency", cur, "--request-approval", "--expires-at", exp.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "--metadata", f"h2_agent:{kv(agent)[:80]}", "--metadata", f"h2_thread:{kv(a.thread)[:80]}"]
    for l in lines:
        argv += ["--line-item", f"name:{kv(l['name'])},quantity:{l['quantity']},unit_amount:{l['unit_amount']}"
                 + (f",description:{kv(a.detail)[:300]}" if a.detail and len(lines) == 1 else "") + f",product_url:{a.url}"]
    argv += ["--total", f"type:subtotal,display_text:Items,amount:{goods}", "--total", f"type:shipping,display_text:Shipping,amount:{ship}",
             "--total", f"type:tax,display_text:Tax,amount:{tax}", "--total", f"type:total,display_text:Total,amount:{amount}"]
    if a.test: argv.append("--test")
    j, err = link(*argv)
    if err: die(f"Link refused the request: {err}\n(hive buy setup shows whether Link is signed in)")
    bid = j.get("id") or die(f"link-cli gave no id: {str(j)[:300]}")
    b = {"id": bid, "status": j.get("status", "created"), "merchant": a.merchant, "url": a.url, "item": a.item, "detail": a.detail,
         "amount": amount, "currency": cur, "shipping": ship, "tax": tax, "lines": lines if a.line else None,
         "expires_at": j.get("expires_at") or iso(exp), "approve_by": iso(now() + dt.timedelta(minutes=30)),
         "approval_url": j.get("approval_url") or (((j.get("status_details") or {}).get("requires_action") or {}).get("next_action") or {}).get("action_url"),
         "agent": agent, "thread": a.thread, "test": bool(a.test) or None, "why": a.why}
    record({"ev": "ask", **b})
    lead = (f"Asking to buy {a.item} at {a.merchant} for {fmt(amount, cur)} (shipping and tax included). Approve or decline it in your Link app;"
            f" the request lapses in 30 min. Nothing is spent unless you approve there." + (" (Link test mode: no real money.)" if a.test else ""))
    if b["status"] == "requires_action": lead = f"Link wants a step first (open the link in the card), then the approval. {lead}"
    post(b, lead)
    print(f"{bid}  {b['status']}  next: hive buy wait {bid}")

def cmd_wait(a):
    sweep()
    b = get(a.id)
    if b["status"] not in OPEN: print(f"{b['id']} is {b['status']}"); return show(b)
    card_dir(); out = card_path(b["id"])
    if os.path.lexists(out): shred(b["id"])  # a leftover from an earlier wait: link-cli won't overwrite it, and it must not be a symlink
    deadline = time.time() + a.timeout
    while True:
        left = max(10, int(deadline - time.time()))
        j, err = link("spend-request", "retrieve", b["id"], "--interval", "3", "--timeout", str(min(left, 600)), "--include", "card",
                      "--output-file", str(out), timeout=min(left, 600) + 60)
        if j and j.get("status") not in OPEN: break
        if j and j.get("status") == "requires_action":
            url = (((j.get("status_details") or {}).get("requires_action") or {}).get("next_action") or {}).get("action_url") or b.get("approval_url")
            print(f"{b['id']} requires_action: Link wants a step from the person first" + (f": {url}" if url else "") + f". Then: hive buy wait {b['id']}"); return
        if err and "POLLING_TIMEOUT" not in err and "timed out" not in err: die(f"retrieve failed: {err}")
        if time.time() >= deadline:
            print(f"{b['id']} still {(j or {}).get('status', b['status'])} after {a.timeout}s" + (f" ({err})" if err else "")); return
        time.sleep(3)
    st = j["status"]
    ev = {"ev": "status", "id": b["id"], "status": st}
    if st == "approved":
        if out.is_file() and not out.is_symlink(): os.chmod(out, 0o600); ev.update(card_file=str(out), last4=last4_file(out) or last4(j))
        else: ev.update(card_file=None, note="approved but link-cli wrote no card file")
    elif os.path.lexists(out): shred(b["id"])
    record(ev); b = get(b["id"])
    if st == "approved":
        post(b, f"Approved in Link: a one-time card ending {b.get('last4') or '????'} for {b['merchant']}, up to {fmt(b['amount'], b['currency'])}."
                f" {b['agent']} checks out now, on screen record, and posts the receipt here.")
        print(f"card file: {out} (0600; use it for this one checkout, never print it)\nthen: hive buy done {b['id']} --recording PATH --receipt PATH")
    else:
        post(b, {"denied": "Declined in Link: nothing bought.", "expired": "The request expired in Link without an answer: nothing bought.",
                 "canceled": "Canceled: nothing bought."}.get(st, f"Link says {st}: nothing bought."))

def cmd_card(a):
    sweep()
    b = get(a.id, exact=True); p = card_path(b["id"])
    if b["status"] != "approved" or not p.exists(): die(f"{b['id']} has no live card (status {b['status']})")
    print(f"{p}\nonly at {b['merchant']} ({short_url(b['url'])}), only this cart, at most {fmt(b['amount'], b['currency'])} with shipping and tax;"
          f" a higher total or another cart: hive buy cancel {b['id']} and ask again. Never print, paste or screenshot the number.")

def contact_file(a, dry=False):
    """--contact FILE; a real run falls back to ~/.hive/buy-contact.json when the person has filled it in. A dry run reads only an
    explicit --contact (and from it only the city, state, zip and country): it never picks up the person's file by itself."""
    f = Path(a.contact).expanduser() if a.contact else None if dry else CONTACT if CONTACT.is_file() else None
    if a.contact and not f.is_file(): die(f"--contact: no file {a.contact}")
    return f
def contact_values(path):
    """The contact file's email, phone, name and street, for the audit; None if it is missing, a symlink or not JSON."""
    try:
        fd = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, encoding="utf-8") as f: j = json.load(f)
    except (OSError, ValueError, TypeError): return None
    if not isinstance(j, dict): return None
    ad = j.get("address") if isinstance(j.get("address"), dict) else {}
    t = lambda x: "" if x is None or isinstance(x, (dict, list)) else str(x).strip()
    name = t(j.get("name")); w = name.split()
    return {"email": t(j.get("email")), "phone": t(j.get("phone")), "name": name, "first": t(j.get("first_name")) or (w[0] if w else ""),
            "last": t(j.get("last_name")) or (w[-1] if len(w) > 1 else ""), "line1": t(ad.get("line1"))}
_nw = lambda s: " " + re.sub(r"[^a-z0-9@\u00c0-\uffff]+", " ", str(s or "").lower()).strip() + " "
_cmp = lambda s: re.sub(r"[^a-z0-9@\u00c0-\uffff]", "", str(s or "").lower())
def contact_hits(text, c):
    """Which of the person's contact values a text holds, as buy_record.mjs's contactLeaks reads them: the email, the phone's last
    7 digits, the first and last name on one line (or run together), the street's house number and first word."""
    if not c or not text: return []
    hits, email, ph = set(), _cmp(c["email"]), re.sub(r"\D", "", c["phone"])[-7:]
    f, l, whole = _nw(c["first"]).strip(), _nw(c["last"]).strip(), _cmp(c["name"])
    k = _nw(c["line1"]).split()
    street = re.compile(rf"(^|[^a-z0-9]){re.escape(k[0])}\s+{re.escape(k[1])}([^a-z0-9]|$)") if len(k) >= 2 and re.fullmatch(r"\d+[a-z]?", k[0]) else None
    for line in str(text).split("\n"):
        lw, lc = _nw(line), _cmp(line)
        if len(email) >= 6 and email in lc: hits.add("email")
        if len(ph) == 7 and ph in re.sub(r"\D", "", line): hits.add("phone")
        if (len(f) >= 2 and len(l) >= 2 and f" {f} " in lw and f" {l} " in lw) or (len(whole) >= 6 and whole in lc): hits.add("name")
        if street and street.search(line.lower()): hits.add("street")
    return sorted(hits)

def cmd_record(a):
    """The checkout on screen record (buy_record.mjs): only this merchant's host, the place-order button only at the approved total or
    less, every text field masked in every frame, and every frame OCR'd for a card number before anything is kept."""
    if a.dry: return record_dry(a)
    if not a.id: die("record needs the buy id (or --dry --url URL for a practice run with no card)")
    sweep()
    b = get(a.id, exact=True); p = card_path(b["id"])
    if b["status"] != "approved" or not p.is_file(): die(f"{b['id']} has no live card (status {b['status']})")
    host = (urllib.parse.urlsplit(b.get("url") or "").hostname or "").lower()
    if not host: die(f"{b['id']} has no merchant url")
    host = host.removeprefix("www.")  # the approved merchant and its subdomains (www., checkout., ...)
    out = RECS / re.sub(r"[^A-Za-z0-9_-]", "_", b["id"]); out.mkdir(parents=True, exist_ok=True, mode=0o700); os.chmod(out, 0o700)
    for d in list(out.glob(".stage-*")) + list(out.glob(".video-*")): shutil.rmtree(d, ignore_errors=True)  # a killed run's unscanned files
    total = f"{b['amount'] / 100:.2f}"; cf = contact_file(a)
    rc = subprocess.run(["node", str(Path(__file__).resolve().parent / "buy_record.mjs"), "run", "--steps", a.steps, "--card", str(p),
                         *(["--contact", str(cf)] if cf else []), "--out", str(out), "--allow-host", host, "--max-total", total,
                         "--currency", (b.get("currency") or "usd").lower()]).returncode
    kept = rc in (0, 4, 5) and (out / "checkout.webm").is_file()
    record({"ev": "record", "id": b["id"], "recording_exit": rc, **({"recording": str(out / "checkout.webm")} if kept else {}),
            **({"contact": str(cf)} if cf else {})})
    if rc == 3: die("a card number or the person's contact details showed in the recording; it is deleted. Post that in the thread (never the image), and finish by hand.", 3)
    if rc == 5: die(f"the page showed a total above the approved {fmt(b['amount'], b.get('currency', 'usd'))} after the order button: check the order,"
                    f" post the recording ({out / 'checkout.webm'}) and say so in the thread", 5)
    if rc == 0: print(f"then: hive buy done {b['id']} --recording {out / 'checkout.webm'} --receipt {out}/<the receipt shot>.png --charged AMOUNT")
    sys.exit(rc)

def record_dry(a):
    """A practice walk through a real merchant's checkout with no card and no buy: placeholders for the address, the card fields only
    checked, nothing pressed at the order button. Nothing goes in the ledger; the recording is scanned like a real one."""
    if a.id: die("--dry takes no buy id: it holds no card and buys nothing")
    if not a.url: die("--dry needs --url, the merchant's page; only https on its host and subdomains is allowed")
    host = (urllib.parse.urlsplit(a.url).hostname or "").lower().removeprefix("www.")
    if not host or not a.url.startswith("https://"): die(f"--url {a.url} is not an https url")
    out = RECS / f"dry-{re.sub(r'[^A-Za-z0-9_-]', '_', host)}-{time.strftime('%Y%m%d-%H%M%S')}"; out.mkdir(parents=True, mode=0o700)
    cmd = ["node", str(Path(__file__).resolve().parent / "buy_record.mjs"), "run", "--dry", "--steps", a.steps, *(["--contact", str(cf)] if (cf := contact_file(a, dry=True)) else []), "--out", str(out), "--allow-host", host]
    if a.max_total: cmd += ["--max-total", f"{money(a.max_total, '--max-total') / 100:.2f}"]
    rc = subprocess.run(cmd).returncode
    print(f"dry run in {out}")
    sys.exit(rc)

def cmd_done(a):
    b = get(a.id, exact=True)
    if b["status"] not in ("approved", "expired"): die(f"{b['id']} is {b['status']}: done is for an approved buy")
    if not a.recording or not a.receipt: die("--recording PATH (the screen recording of the checkout) and --receipt PATH are both required (warden 11231556881e, rule 5)")
    for f in (a.recording, a.receipt):
        if not Path(f).expanduser().is_file(): die(f"no file {f}")
    charged = money(a.charged, "--charged") if a.charged else None
    shred(b["id"]); err = None if b.get("revoked") else revoke(b["id"])
    record({"ev": "done", "id": b["id"], "status": "done", "card_file": None, "receipt": a.receipt, "recording": a.recording,
            "charged": charged, **({"revoked": True} if not err else {"revoke_error": err})})
    b = get(b["id"])
    over = charged is not None and charged > b["amount"]
    lead = (f"Bought: {b['item']} at {b['merchant']}" + (f", charged {fmt(charged, b['currency'])}" if charged is not None else "") +
            ". The card file is deleted" + (" and the card is canceled at Link." if not err else f"; canceling the card at Link FAILED ({err}): hive buy sweep retries.") +
            " Recording and receipt attached." + (" CHARGED MORE THAN APPROVED: this is a breach of the rule, say why." if over else ""))
    ok = post(b, lead, files=[a.recording, a.receipt])
    record({"ev": "posted", "id": b["id"], "done_post": ok is not False})

def cmd_cancel(a):
    b = get(a.id, exact=True)
    shred(b["id"])
    if b["status"] in ENDED:
        if b.get("revoked"): print(f"{b['id']} already {b['status']}, card canceled at Link"); return
        err = revoke(b["id"]); record({"ev": "revoke", "id": b["id"], "card_file": None, **({"revoked": True} if not err else {"revoke_error": err})})
        print(f"{b['id']} already {b['status']}; " + ("card canceled at Link" if not err else f"Link refused the cancel: {err}")); return
    err = revoke(b["id"])
    if err:
        record({"ev": "cancel_failed", "id": b["id"], "card_file": None, "why_cancel": a.why, "revoke_error": err})
        post(get(b["id"]), f"Tried to cancel the request for {b['item']} at {b['merchant']}, but Link refused ({err}). The card file here is deleted,"
             f" but the request is still live at Link: decline or cancel it in the Link app. Retry: hive buy cancel {b['id']}")
        die(f"Link refused the cancel: {err}")
    record({"ev": "cancel", "id": b["id"], "status": "canceled", "card_file": None, "why_cancel": a.why, "revoked": True})
    post(get(b["id"]), f"Canceled the request for {b['item']} at {b['merchant']}" + (f": {a.why}" if a.why else "") + ". Nothing bought; the card is canceled at Link.")

def show(b):
    print(card_json(b))
def cmd_show(a): sweep(); show(get(a.id))
def cmd_list(a):
    sweep()
    rows = sorted(buys().values(), key=lambda b: b.get("updated") or "", reverse=True)
    if not a.all: rows = [b for b in rows if b.get("status") in LIVE]
    if a.json: print(json.dumps(rows, indent=1)); return
    if not rows: print("no live buys" + ("" if a.all else " (--all for the finished ones)")); return
    for b in rows:
        print(f"{b['id'][-12:]:12}  {b.get('status', '?'):16} {fmt(b.get('amount', 0), b.get('currency', 'usd')):>10}  {b.get('merchant', '')[:20]:20}"
              f"  {b.get('item', '')[:40]:40}  {b.get('agent', '')}  {b.get('thread', '')}")
# ---- audit: every finished buy scored against the warden's conditions (11231556881e); it only reads, and --scan OCRs ----
CONDS = ("link_approved", "total", "card_gone", "recorded", "scanned", "no_leak")
def luhn(d):
    s = 0
    for i, c in enumerate(reversed(d)):
        n = int(c) * (2 if i % 2 else 1); s += n - 9 if n > 9 else n
    return s % 10 == 0
def card_numbers(text):
    """Digit runs that could be a card: 13-19 digits (spaces or dashes allowed), a card network's first digit, Luhn-valid."""
    out = []
    for m in re.finditer(r"(?<![\d.])\d(?:[ -]?\d){12,18}(?![\d.])", text or ""):
        d = re.sub(r"\D", "", m.group())
        if d[0] in "23456" and luhn(d): out.append(d[-4:])
    return out
def board_posts(thread, since):
    path = Path(os.environ.get("HIVE_BUY_BOARD") or HOME / "board.jsonl")
    try: f = path.open(errors="replace")
    except OSError: return None
    out = []
    with f:
        for l in f:
            if f'"{thread}"' not in l: continue
            try: j = json.loads(l)
            except ValueError: continue
            if j.get("thread") == thread and str(j.get("at") or "") >= (since or ""): out.append(j)
    return out
def scan_files(files, card=None, contact=None):
    """buy_record.mjs scan: 0 clean, 3 a card number or the contact file's details in some frame, else it could not scan."""
    cmd = ["node", str(Path(__file__).resolve().parent / "buy_record.mjs"), "scan", *([ "--card", str(card)] if card else []),
           *(["--contact", str(contact)] if contact and Path(contact).is_file() else []), *map(str, files)]
    try: return subprocess.run(cmd, capture_output=True, text=True, timeout=1800).returncode
    except (OSError, subprocess.TimeoutExpired): return None
def audit_one(bid, b, evs, scan=False):
    """{cond: ("ok"|"unmet"|"breach", why)} for one buy, from its ledger events, the card dir and the board."""
    mine = [e for e in evs if e.get("id") == bid]; done = next((e for e in reversed(mine) if e.get("ev") == "done"), {})
    r, cf = {}, card_path(bid)
    if b.get("status") != "done":
        r["card_gone"] = ("breach", f"card file still at {cf}") if os.path.lexists(cf) else ("ok", "")
        return r
    r["link_approved"] = ("ok", "") if any(e.get("ev") == "status" and e.get("status") == "approved" for e in mine) else \
        ("breach", "no approval from Link in the ledger")
    ch, amt = done.get("charged"), b.get("amount")
    r["total"] = ("unmet", "charged not recorded") if ch is None else ("breach", f"charged {fmt(ch, b.get('currency', 'usd'))} > approved {fmt(amt, b.get('currency', 'usd'))}") \
        if amt is None or ch > amt else ("ok", "")
    r["card_gone"] = ("breach", f"card file still at {cf}") if os.path.lexists(cf) else ("ok", "") if b.get("revoked") else \
        ("unmet", "card not canceled at Link: " + str(b.get("revoke_error") or "no cancel recorded"))
    rec, rcpt = done.get("recording"), done.get("receipt")
    files = [Path(x).expanduser() for x in (rec, rcpt) if x]
    missing = [str(f) for f in files if not f.is_file()]
    posted = next((e.get("done_post") for e in reversed(mine) if e.get("ev") == "posted"), None)
    r["recorded"] = ("unmet", "missing " + ", ".join(missing)) if missing or len(files) < 2 else \
        ("unmet", "the done post failed") if posted is False else ("ok", "")
    clean = any(e.get("ev") == "record" and e.get("recording_exit") == 0 and e.get("recording") and rec and
                Path(e["recording"]).expanduser() == Path(rec).expanduser() for e in mine) or \
        any(e.get("ev") == "scan" and e.get("scan_exit") == 0 for e in mine)
    if any(e.get("ev") == "scan" and e.get("scan_exit") == 3 for e in mine):
        r["scanned"] = ("breach", "OCR found a card number or contact details in the kept recording or receipt"); clean = True
    cpath = next((e.get("contact") for e in reversed(mine) if e.get("ev") == "record" and e.get("contact")), None) or CONTACT
    if not clean and scan and not missing:
        rc = scan_files(files, contact=cpath); record({"ev": "scan", "id": bid, "scan_exit": rc, "files": [str(f) for f in files]})
        clean = rc == 0
        if rc == 3: r["scanned"] = ("breach", "OCR found a card number or contact details in the recording or receipt")
    if "scanned" not in r:
        r["scanned"] = ("ok", "") if clean else ("unmet", "recording not OCR-checked (hive buy audit --scan)" if not missing else "nothing to scan")
    cv = contact_values(cpath)
    leaks, said = card_numbers(json.dumps(mine)), contact_hits("\n".join(json.dumps(e) for e in mine), cv)
    posts = board_posts(b.get("thread") or "", mine[0].get("at") if mine else "")
    if posts is None: r["no_leak"] = ("unmet", "board not readable")
    else:
        for j in posts: leaks += card_numbers(j.get("text") or ""); said += contact_hits(j.get("text") or "", cv)
        r["no_leak"] = ("breach", f"a card number ending {leaks[0]} in the ledger or thread {b.get('thread')}") if leaks else \
            ("breach", f"the person's {', '.join(sorted(set(said)))} in the ledger or thread {b.get('thread')}") if said else ("ok", "")
    return r
def cmd_audit(a):
    st, evs = buys(), events()
    rows, tally = [], {k: {"done": 0, "met": 0, "breaches": 0, "unmet": 0, "ended": 0} for k in ("real", "test")}
    for bid, b in sorted(st.items(), key=lambda kv: kv[1].get("updated") or ""):
        if b.get("status") not in ENDED: continue
        r = audit_one(bid, b, evs, a.scan); t = tally["test" if b.get("test") else "real"]
        br = [k for k, (v, _) in r.items() if v == "breach"]; un = [k for k, (v, _) in r.items() if v == "unmet"]
        t["ended"] += 1; t["breaches"] += len(br); t["unmet"] += len(un)
        if b.get("status") == "done": t["done"] += 1; t["met"] += not br and not un
        rows.append({"id": bid, "status": b.get("status"), "test": bool(b.get("test")), "merchant": b.get("merchant"),
                     "amount": b.get("amount"), "conditions": {k: {"v": v, "why": w} for k, (v, w) in r.items()}})
    breaches = tally["real"]["breaches"] + tally["test"]["breaches"]
    measure = (f"Measure: real buys done {tally['real']['done']}, all six conditions met {tally['real']['met']}; "
               f"test buys done {tally['test']['done']} (met {tally['test']['met']}); breaches {breaches}")
    if a.json: print(json.dumps({"buys": rows, "tally": tally, "breaches": breaches, "measure": measure}, indent=1))
    else:
        for x in rows:
            c = x["conditions"]; bad = [f"{k} {v['v']}: {v['why']}" for k, v in c.items() if v["v"] != "ok"]
            print(f"{x['id'][-12:]:12}  {x['status']:9} {'test' if x['test'] else 'real'}  {(x['merchant'] or '')[:18]:18}  "
                  + ("all met" if not bad and x["status"] == "done" else "card gone" if not bad else "; ".join(bad)))
        print(measure)
    sys.exit(1 if breaches else 0)

def cmd_sweep(a):
    g = sweep(); print(f"deleted {len(g)} card file(s)" + (f": {', '.join(g)}" if g else ""))
def cmd_setup(a):
    j, err = link("auth", "status", timeout=30)
    signed = bool(j) and not err and (j.get("authenticated") or j.get("logged_in") or j.get("status") in ("authenticated", "logged_in"))
    print(f"link-cli: {LINK}  " + ("signed in" if signed else f"not signed in ({err or 'no session'})"))
    print(f"hive cap a purchase: {fmt(round(cap_usd() * 100))} (Link's own: $500); cards: {CARDS} (0700, files 0600, swept)")
    if not signed:
        print("Signing in is the person's step (warden 11231556881e): they need a Link account (US or Canada) with the Link app on their phone,"
              " then THEY run, in their own terminal: link-cli auth login --client-name Hive --no-financial-insights, and approve the phrase it shows in Link."
              " An agent never runs auth login.")

def main(argv=None):
    ap = argparse.ArgumentParser(prog="hive buy", description=__doc__.split("\n\n")[0])
    sp = ap.add_subparsers(dest="cmd", required=True)
    p = sp.add_parser("ask", help="ask the person, in their Link app, to approve one purchase")
    p.add_argument("--thread", default=os.environ.get("HIVE_THREAD"), help="where the card and its updates are posted (default: the thread that woke you)")
    p.add_argument("--merchant", required=True); p.add_argument("--url", required=True, help="the merchant's page for the item")
    p.add_argument("--item", required=True, help="what it is, as the person reads it: 'Leg tube for the Heavylift drone'")
    p.add_argument("--detail", default="", help="the spec line: '1 x aluminum tube, 1 in OD x 0.065 in wall, 6061-T6, 4 ft'")
    p.add_argument("--amount", required=True, help="the WHOLE checkout total, shipping and tax included, in dollars")
    p.add_argument("--shipping", required=True, help="shipping in that total (0 if free)"); p.add_argument("--tax", required=True, help="tax in that total (0 if none)")
    p.add_argument("--line", action="append", help="a cart line 'name;qty;unit price' (repeat); they must add up to amount - shipping - tax")
    p.add_argument("--currency", default="usd"); p.add_argument("--why", required=True, help="why this part from this merchant; the person reads it in Link")
    p.add_argument("--test", action="store_true", help="Link test mode: a test card, no real money"); p.set_defaults(f=cmd_ask)
    p = sp.add_parser("wait", help="wait for the answer in Link; on approval the card is written to its 0600 file")
    p.add_argument("id"); p.add_argument("--timeout", type=int, default=1800); p.set_defaults(f=cmd_wait)
    p = sp.add_parser("card", help="the card file's path and what it may pay for"); p.add_argument("id"); p.set_defaults(f=cmd_card)
    p = sp.add_parser("record", help="check out on screen record from a steps file, card fields masked, every frame OCR'd (core/buy_record.mjs)")
    p.add_argument("id", nargs="?"); p.add_argument("--steps", required=True, help="the checkout as JSON steps; card values only as $card.number etc.")
    p.add_argument("--dry", action="store_true", help="practice run with no card and no buy: card fields only checked, nothing pressed at the order button")
    p.add_argument("--url", help="with --dry: the merchant's https page (its host is the one allowed)")
    p.add_argument("--max-total", help="with --dry: the total the place step checks against")
    p.add_argument("--contact", help=f"the person's email, phone, name and address for $contact.* tokens (0600 JSON; a real run's default is {CONTACT} if it exists;"
                                       " a dry run uses only an explicit --contact, and from it only the city, state and zip)")
    p.set_defaults(f=cmd_record)
    p = sp.add_parser("done", help="checkout finished: delete the card, post the recording and receipt")
    p.add_argument("id"); p.add_argument("--recording", required=True, help="the screen recording of the checkout, card number masked")
    p.add_argument("--receipt", required=True, help="the order confirmation or receipt"); p.add_argument("--charged", help="what the merchant charged, in dollars"); p.set_defaults(f=cmd_done)
    p = sp.add_parser("cancel", help="cancel a request or an unused card"); p.add_argument("id"); p.add_argument("--why", default=""); p.set_defaults(f=cmd_cancel)
    p = sp.add_parser("list", help="live buys (--all: every one)"); p.add_argument("--all", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("show", help="one buy as its card JSON"); p.add_argument("id"); p.set_defaults(f=cmd_show)
    p = sp.add_parser("audit", help="score every finished buy against the warden's conditions and print the measure; exit 1 on a breach")
    p.add_argument("--scan", action="store_true", help="OCR a recording and receipt the recorder didn't already check"); p.add_argument("--json", action="store_true")
    p.set_defaults(f=cmd_audit)
    sp.add_parser("sweep", help="delete card files whose buy ended or expired").set_defaults(f=cmd_sweep)
    sp.add_parser("setup", help="is Link signed in, and the person's step if not").set_defaults(f=cmd_setup)
    a = ap.parse_args(argv)
    if a.cmd == "ask" and not a.thread: die("--thread: which thread the purchase belongs to")
    os.umask(0o077)
    a.f(a)

if __name__ == "__main__": main()
