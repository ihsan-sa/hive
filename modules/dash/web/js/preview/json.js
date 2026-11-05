// A JSON value as a tree you fold: objects and arrays are <details>, the first two levels open. Keys and values are text only.
import { h } from "../core/dom.js";

const KIDS = 500, OPEN_DEPTH = 2;
const kind = v => (v === null ? "null" : Array.isArray(v) ? "array" : typeof v);
function leaf(v) {
  const t = kind(v);
  return h("span", { class: "jt-v", "data-t": t }, t === "string" ? JSON.stringify(v) : String(v));
}
function node(key, v, depth) {
  const t = kind(v), k = key == null ? null : h("span", { class: "jt-k" }, key);
  if (t !== "object" && t !== "array") return h("div", { class: "jt-row" }, k, k ? ": " : null, leaf(v));
  const entries = t === "array" ? v.map((x, i) => [String(i), x]) : Object.entries(v);
  const sum = h("span", { class: "jt-sum meta" }, t === "array" ? `[${entries.length}]` : `{${entries.length}}`);
  if (!entries.length) return h("div", { class: "jt-row" }, k, k ? ": " : null, sum);
  const d = h("details", { class: "jt", open: depth < OPEN_DEPTH || null }, h("summary", null, k, k ? ": " : null, sum));
  let drawn = false;
  const fill = () => {
    if (drawn) return; drawn = true;
    const body = h("div", { class: "jt-in" }, entries.slice(0, KIDS).map(([a, b]) => node(t === "array" ? a : a, b, depth + 1)));
    if (entries.length > KIDS) body.append(h("div", { class: "jt-row meta" }, `… ${entries.length - KIDS} more`));
    d.append(body);
  };
  if (d.open) fill(); else d.addEventListener("toggle", fill);
  return d;
}
export function jsonTree(v) {
  const tree = h("div", { class: "jt-root", "data-own": "" }, node(null, v, 0));
  if (v === null || typeof v !== "object") return tree;
  // Expand all opens every fold, but not past a few thousand, so a huge file doesn't hang the page.
  const setAll = open => { let n = 0; for (const d of tree.querySelectorAll("details.jt")) { if (open && ++n > 3000) break; d.open = open; } if (open) for (let i = 0; i < 12 && tree.querySelector("details.jt:not([open])"); i++) for (const d of tree.querySelectorAll("details.jt:not([open])")) { if (++n > 3000) break; d.open = true; } };
  const bar = h("div", { class: "jt-bar" }, h("button", { type: "button", class: "pv-zb", onclick: () => setAll(true) }, "Expand all"), h("button", { type: "button", class: "pv-zb", onclick: () => setAll(false) }, "Collapse all"));
  return h("div", null, bar, tree);
}
