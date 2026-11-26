// Orders (GET /api/orders, `hive order` in modules/buy; `hive buy` records come as cards too): one card per order, whoever pays for it. Read only. The button opens the shop's
// own page in a new window, where the person checks it and pays; nothing here pays or sends anything.
import { h } from "../core/dom.js";
import { S, can, jget } from "../core/store.js";
import { ago } from "../core/format.js";
import { block, button, empty, pageHeader, section, statusLabel } from "../ui/kit.js";

const O = { v: null, err: "", t: 0, loading: false };
const TONE = { drafted: "off", quoted: "off", waiting: "asks", approved: "working", ordered: "working", shipped: "working", arrived: "asks", stocked: "ended", returned: "ended", cancelled: "ended" };
const OVER = new Set(["stocked", "returned", "cancelled"]);
const BUY = new Set(["drafted", "quoted", "waiting"]);   // before the order is placed the button hands over; after, it only opens the page
const https = u => { try { return new URL(u).protocol === "https:"; } catch { return false; } };

async function load(ctx) {
  if (O.loading) return;
  O.loading = true;
  try { O.v = await jget("/api/orders"); O.err = ""; } catch (e) { O.err = e.message; }
  O.loading = false; O.t = Date.now(); ctx.update();
}

function card(o) {
  const when = o.updated ? ago(Date.parse(o.updated)) : null;
  const meta = [o.tracking ? "tracking " + o.tracking : null, o.receipt ? "receipt " + o.receipt : null, o.agent, when].filter(Boolean).join(" · ");
  const go = o.url && https(o.url) && !OVER.has(o.state);
  return block({ title: o.shop || "Order", sub: o.total || null, state: statusLabel(TONE[o.state] || "off", o.state_words), key: "order-" + o.id },
    h("ul", { class: "order-items" }, (o.items || []).map(i => h("li", null, i))),
    o.why ? h("p", { class: "field-h order-note" }, o.why) : null,
    o.note ? h("p", { class: "field-h order-note" }, o.note) : null,
    go ? h("div", { class: "frow" }, h("a", { class: "btn", "data-kind": BUY.has(o.state) ? "primary" : null, "data-size": "s", href: o.url, target: "_blank", rel: "noreferrer noopener" }, o.source === "buy" && o.state === "waiting" && /(^|\.)link\.com$/.test(o.host) ? "Open in Link" : BUY.has(o.state) ? "Open the order" : "Open the shop"),
      h("span", { class: "muted order-host" }, o.host)) : null,
    h("p", { class: "muted order-meta" }, meta, o.thread ? [meta ? " · " : "", h("a", { href: "#comms/t:" + encodeURIComponent(o.thread) }, "conversation")] : null));
}

// Its name, label and tab rule are in views/index.js, which loads this file when the page opens.
export default {
  enter(ctx) { load(ctx); },
  render(ctx) {
    if (!can("logins")) return h("div", { class: "page", "data-width": "narrow" }, empty("Orders need full mode. Sign in again if your hour is up.", { kind: "page", title: "Orders" }));
    const v = O.v;
    if (!O.loading && Date.now() - O.t > 15000) setTimeout(() => load(ctx), 0);   // re-read while the page is open
    const all = (v && v.orders) || [], open = all.filter(o => !OVER.has(o.state)), done = all.filter(o => OVER.has(o.state));
    return h("div", { class: "page", "data-width": "narrow", "data-key": "orders" },
      pageHeader({ title: "Orders", lead: "You pay at the shop, or approve in Link. Nothing on this page pays or sends anything." }),
      O.err ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, "Couldn't read the orders: " + O.err) : null,
      !v ? (O.err ? null : empty("Reading…", { kind: "loading" }))
        : !v.installed ? empty("Orders come with the buy module (hive module add buy).")
        : v.error ? h("p", { class: "fmsg", "data-tone": "bad", role: "status" }, "The buy module didn't answer: " + v.error)
        : !all.length ? empty("No orders yet. When an agent drafts one with hive order, it shows here.")
        : h("div", { class: "stack" },
          open.length ? h("div", { class: "stack", "data-key": "orders-open" }, open.map(card)) : empty("Nothing open."),
          done.length ? section({ title: "Finished", summary: String(done.length), fold: true, key: "orders-done" }, h("div", { class: "stack" }, done.map(card))) : null,
          v.more ? h("p", { class: "muted" }, `${v.more} older orders aren't shown (hive order list --all).`) : null));
  },
};
