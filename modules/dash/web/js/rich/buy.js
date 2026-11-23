// A buy fence (core/buy.py, the buy command): an agent's one-time spend request through Link, drawn like Link's own approval sheet.
// There is no Approve button here on purpose: the warden's ruling (11231556881e) makes approval in the person's Link app the only
// go to spend, so the card can only show the request and open it in Link. Everything goes in as text; links are http(s) only.
import { h } from "../core/dom.js";
import { codeBlock } from "./code.js";
import { parseBuy, money as fmt } from "./buy_parse.js";

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
    d.approval ? h("div", { class: "rc-buy-act" }, h("a", { class: "rc-buy-open", href: d.approval, target: "_blank", rel: "noopener noreferrer" }, "Open in Link")) : null);
}
