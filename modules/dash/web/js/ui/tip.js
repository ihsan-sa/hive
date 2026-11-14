// Tooltip: one floating element, text only.
import { h, tr } from "../core/dom.js";
let el;
export function showTip(x, y, ...lines) {
  if (!el) { el = h("div", { class: "tip", role: "tooltip" }); document.body.append(el); }
  el.replaceChildren(...lines.flatMap((l, i) => (i ? [h("br"), tr(String(l))] : [tr(String(l))])));
  el.hidden = false;
  const r = el.getBoundingClientRect();
  el.style.left = Math.max(6, Math.min(x + 14, innerWidth - r.width - 6)) + "px";
  el.style.top = Math.max(6, Math.min(y + 14, innerHeight - r.height - 6)) + "px";
}
export function hideTip() { if (el) el.hidden = true; }
