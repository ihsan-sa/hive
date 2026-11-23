// A buy fence (core/buy.py): the JSON of one spend request, read into what the card shows. Pure, so node tests it.
// Nothing here can approve anything: approval is only in the person's Link app (warden ruling 11231556881e).
const STATUS = {
  created: ["waiting", "Waiting for Link"], pending_approval: ["waiting", "Waiting in Link"], requires_action: ["waiting", "Waiting in Link"],
  approved: ["good", "Approved"], succeeded: ["good", "Approved"], done: ["good", "Bought"],
  denied: ["bad", "Declined"], expired: ["off", "Expired"], canceled: ["off", "Canceled"], failed: ["bad", "Failed"],
};
const SYM = { usd: "$", cad: "CA$", eur: "€", gbp: "£" };
const str = (v, n = 400) => typeof v === "string" ? v.slice(0, n) : "";
const int = v => Number.isInteger(v) && v >= 0 && v < 1e9 ? v : null;

/** Minor units to "$37.63". */
export function money(minor, cur = "usd") {
  if (minor == null) return "";
  const c = String(cur || "usd").toLowerCase(), s = SYM[c];
  const n = (minor / 100).toFixed(2).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return s ? s + n : `${n} ${c.toUpperCase()}`;
}
/** An http(s) URL or null: no javascript:, data: or relative links reach an href. */
export function safeUrl(u, httpsOnly = false) {
  try { const x = new URL(String(u)); return (x.protocol === "https:" || (!httpsOnly && x.protocol === "http:")) ? x.href : null; }
  catch { return null; }
}
/** Link's own https page or null: anyone can post a buy fence, so the Open in Link button goes to link.com and nowhere else. */
export function linkUrl(u) {
  const h = safeUrl(u, true); if (!h) return null;
  const host = new URL(h).hostname.toLowerCase();
  return host === "link.com" || host.endsWith(".link.com") ? h : null;
}
/** "in 23 min" / "2 h 10 min ago" between two ms times. */
export function rel(t, now) {
  const m = Math.round((t - now) / 60000), a = Math.abs(m);
  const s = a < 60 ? `${a} min` : a < 2880 ? `${Math.floor(a / 60)} h` + (a % 60 ? ` ${a % 60} min` : "") : `${Math.round(a / 1440)} days`;
  return m >= 0 ? `in ${s}` : `${s} ago`;
}
/** The card's model, or throws when the fence isn't a buy. */
export function parseBuy(code, now = Date.now()) {
  let j;
  try { j = JSON.parse(code); } catch { throw new Error("It isn't JSON."); }
  if (!j || typeof j !== "object" || Array.isArray(j)) throw new Error("It isn't a buy.");
  const amount = int(j.amount);
  if (amount == null || !str(j.merchant)) throw new Error("It has no merchant or amount.");
  const cur = str(j.currency, 8) || "usd", st = str(j.status, 40) || "created";
  const [tone, label] = STATUS[st] || ["off", "Unknown"];
  const t = s => { const v = Date.parse(str(s, 40)); return Number.isFinite(v) ? v : null; };
  const approveBy = t(j.approve_by), expires = t(j.expires_at);
  let when = "";
  if (tone === "waiting" && approveBy) when = approveBy > now ? `Lapses ${rel(approveBy, now)}` : "Should have lapsed";
  else if (st === "approved" && expires) when = expires > now ? `Card valid until use, ${rel(expires, now)} at most` : "Card expired";
  const lines = Array.isArray(j.lines) ? j.lines.slice(0, 30).map(l => ({
    name: str(l && l.name, 200), qty: int(l && (l.quantity ?? l.qty)) ?? 1, unit: int(l && (l.unit_amount ?? l.unit)),
  })).filter(l => l.name) : [];
  const shipping = int(j.shipping), tax = int(j.tax);
  const parts = shipping != null || tax != null ? [
    ...(shipping != null ? [["Shipping", money(shipping, cur)]] : []), ...(tax != null ? [["Tax", money(tax, cur)]] : []),
  ] : [];
  const last4 = typeof j.last4 === "string" && /^\d{4}$/.test(j.last4) ? j.last4 : "";
  return {
    id: str(j.id, 80), status: st, tone, label, when, test: j.test === true,
    agent: str(j.agent, 40) || "An agent", merchant: str(j.merchant, 120), url: safeUrl(j.url), urlText: str(j.url, 200).replace(/^https?:\/\/(www\.)?/, ""),
    item: str(j.item, 200), detail: str(j.detail, 600), lines, parts, cur,
    amount, total: money(amount, cur), charged: int(j.charged) != null ? money(j.charged, cur) : "",
    last4, approval: tone === "waiting" ? linkUrl(j.approval_url) : null,
    receipt: str(j.receipt, 300), recording: str(j.recording, 300), whyCancel: str(j.why_cancel, 300),
  };
}
