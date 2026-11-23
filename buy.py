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
  hive buy done ID --recording PATH --receipt PATH [--charged 37.63]
  hive buy cancel ID [--why TEXT]
  hive buy list [--all] [--json] | show ID | setup | sweep

Ledger: ~/.hive/buys.jsonl, one event per line; a buy's state is its events merged in order. HIVE_LINK_CLI, HIVE_BUY_HOME and HIVE_BIN
override the link-cli binary, the state dir and the hive used to post (the tests use stubs)."""
import argparse, datetime as dt, json, os, re, subprocess, sys, time
from pathlib import Path

HOME = Path.home()
STATE = Path(os.environ.get("HIVE_BUY_HOME") or HOME / ".hive")
LEDGER, CARDS, CONF = STATE / "buys.jsonl", STATE / "buy-cards", STATE / "buy.json"
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
    post(b, lead, files=[a.recording, a.receipt])

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
    p = sp.add_parser("done", help="checkout finished: delete the card, post the recording and receipt")
    p.add_argument("id"); p.add_argument("--recording", required=True, help="the screen recording of the checkout, card number masked")
    p.add_argument("--receipt", required=True, help="the order confirmation or receipt"); p.add_argument("--charged", help="what the merchant charged, in dollars"); p.set_defaults(f=cmd_done)
    p = sp.add_parser("cancel", help="cancel a request or an unused card"); p.add_argument("id"); p.add_argument("--why", default=""); p.set_defaults(f=cmd_cancel)
    p = sp.add_parser("list", help="live buys (--all: every one)"); p.add_argument("--all", action="store_true"); p.add_argument("--json", action="store_true"); p.set_defaults(f=cmd_list)
    p = sp.add_parser("show", help="one buy as its card JSON"); p.add_argument("id"); p.set_defaults(f=cmd_show)
    sp.add_parser("sweep", help="delete card files whose buy ended or expired").set_defaults(f=cmd_sweep)
    sp.add_parser("setup", help="is Link signed in, and the person's step if not").set_defaults(f=cmd_setup)
    a = ap.parse_args(argv)
    if a.cmd == "ask" and not a.thread: die("--thread: which thread the purchase belongs to")
    os.umask(0o077)
    a.f(a)

if __name__ == "__main__": main()
