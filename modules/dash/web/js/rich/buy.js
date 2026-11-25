// A buy fence (core/buy.py, the buy command): an agent's one-time spend request through Link, drawn like Link's own approval sheet.
// There is no Approve button here on purpose: the warden's ruling (11231556881e) makes approval in the person's Link app the only
// go to spend, so the card can only show the request and open it in Link. Everything goes in as text; links are http(s) only.
// Ask Link again (the person's ask, 24 h): on a request whose 30 min prompt lapsed it sends Link a fresh prompt for the same cart and
// total (POST /api/buy do=renew, the person's signed-in app only); that approves nothing, the Link app is still the only go.
// Open in Link opens Link's own page in a pop-up over the dashboard (openInLink below); Link's page can't be framed.
import { h, tr } from "../core/dom.js";
import { can, post } from "../core/store.js";
import { codeBlock } from "./code.js";
import { parseBuy, money as fmt } from "./buy_parse.js";

/** Open in Link: Link's own page for approving the buy in a small window over the dashboard (the person's 11241042be25, as Zeus shows it).
 *  Link's page refuses to be framed (frame-ancestors 'self'), so a pop-up is as close as it gets. When the request was made with
 *  a completion callback (buy.py), Link sends that window back to /?linkback=1 and index.html closes it. A blocked pop-up falls
 *  back to the plain link, a new tab. Nothing here approves: the person still presses Approve on Link's page or in the app. */
function openInLink(url) {
  const a = h("a", { class: "rc-buy-open", href: url, target: "_blank", rel: "noopener noreferrer" }, "Open in Link");
  a.addEventListener("click", (e) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;   // a new tab on purpose: leave it to the browser
    const W = 460, H = 760;
    const left = Math.max(0, Math.round((window.screenX || 0) + ((window.outerWidth || W) - W) / 2));
    const top = Math.max(0, Math.round((window.screenY || 0) + ((window.outerHeight || H) - H) / 2));
    let w = null;
    try { w = window.open(url, "h2-link", `popup,width=${W},height=${H},left=${left},top=${top}`); } catch { w = null; }
    if (!w) return;   // blocked: the link opens a tab as before
    try { w.opener = null; } catch { /* already cut */ }
    e.preventDefault();
    try { w.focus(); } catch { /* fine */ }
  });
  return a;
}

/** The Ask Link again button and the line that says how it went. */
function renewButton(d) {
  const note = h("span", { class: "rc-buy-note", role: "status" });
  const b = h("button", { type: "button", class: "rc-buy-open" }, "Ask Link again");
  b.addEventListener("click", async () => {
    b.disabled = true; note.textContent = tr("Asking Link…");
    try { await post("/api/buy", { do: "renew", id: d.id }); b.remove(); note.textContent = tr("Sent: approve or decline the new prompt in your Link app."); }
    catch (e) { b.disabled = false; note.textContent = e.message || tr("Couldn't ask Link again."); }
  });
  return h("div", { class: "rc-buy-act" }, b, note);
}

const row = (k, ...v) => h("div", { class: "rc-buy-row" }, h("div", { class: "rc-buy-k" }, k), h("div", { class: "rc-buy-v" }, ...v));

export function buyCard(code, { lang, title }) {
  let d;
  try { d = parseBuy(code); } catch { return codeBlock(lang, title, code); }
  const waiting = d.tone === "waiting";
  const head = waiting ? `${d.agent} is requesting to spend ${d.total}` : `${d.agent} asked to spend ${d.total}`;
  const items = d.lines.length > 1 || (d.lines[0] && d.lines[0].name !== d.item)
    ? h("ul", { class: "rc-buy-lines" }, ...d.lines.map(l => h("li", null, `${l.qty} × ${l.name}` + (l.unit != null ? `, ${fmt(l.unit, d.cur)} each` : ""))))
    : null;
  const money = d.parts.length ? h("div", { class: "rc-buy-sum" }, ...d.parts.map(([k, v]) => h("span", null, `${k} ${v}`)), h("b", null, `Total ${d.total}`)) : null;
  const after = [];
  if (d.charged) after.push(row("Charged", d.charged));
  if (d.receipt) after.push(row("Receipt", d.receipt));
  if (d.recording) after.push(row("Recording", d.recording));
  if (d.whyCancel) after.push(row("Why", d.whyCancel));
  return h("div", { class: `rc-buy rc-buy-${d.tone}`, role: "group", "aria-label": `Spend request: ${d.total} at ${d.merchant}, ${d.label}` },
    h("div", { class: "rc-buy-top" },
      h("span", { class: "rc-buy-chip" }, d.label), d.test ? h("span", { class: "rc-buy-test" }, "test mode") : null,
      d.when ? h("span", { class: "rc-buy-when" }, d.when) : null),
    h("div", { class: "rc-buy-head" }, head),
    row("Merchant", h("div", null, d.merchant),
      d.url ? h("a", { href: d.url, target: "_blank", rel: "noopener noreferrer", class: "rc-buy-url" }, d.urlText) : d.urlText ? h("div", { class: "rc-buy-url" }, d.urlText) : null),
    row("Item", h("div", null, d.item), d.detail ? h("div", { class: "rc-buy-detail" }, d.detail) : null, items, money),
    row("Card", d.last4 ? `One-time Link card •••• ${d.last4}` : "One-time card from Link, made on approval"),
    ...after,
    h("p", { class: "rc-buy-fine" }, waiting
      ? `Approve or decline in your Link app; nothing here can approve it. Approving lets ${d.agent} pay ${d.merchant} this amount once, with a one-time card from Link. Your real card details are never shared.`
      : `Paid, if at all, with a one-time card from Link for this amount at ${d.merchant} only. Your real card details are never shared.`),
    d.renewedBy ? h("p", { class: "rc-buy-fine" }, "Asked again: the newer card below is the live one.") : null,
    d.renewable && can("post") ? renewButton(d) : null,
    d.approval ? h("div", { class: "rc-buy-act" }, openInLink(d.approval)) : null);
}
