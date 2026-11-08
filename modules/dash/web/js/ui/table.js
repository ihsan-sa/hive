// Data table. Columns are data: {label, key | render(row), num, mono, wrap, clip, width}. clip: takes the spare width and ellipsizes; give the cell a title.
// Grid rows, not <table> (HANDOFF "Shared DOM changes" 6, R7b S8): one --cols on the table, every row a subgrid of it, so the columns line up.
// Below 640px of container a row stacks: its lead cell (the first `lead` column, else the first non-mono `clip` one, else the first) as the title, each other cell a line with its column's name (css/components.css .tg).
import { h } from "../core/dom.js";
import { empty } from "./kit.js";

const track = c => (c.width ? c.width : c.wrap ? "minmax(14rem,1fr)" : c.clip ? "minmax(0,1fr)" : "auto");

export function table({ columns, rows, key, empty: none = "Nothing here.", caption }) {
  if (!rows.length) return empty(none);
  const at = f => columns.findIndex(f), li = Math.max(0, at(c => c.lead) >= 0 ? at(c => c.lead) : at(c => c.clip && !c.mono));   // a mono clip is a command, not a title
  const cls = (c, i) => [c.num && "num", c.mono && "mono", c.wrap && "wrap", c.clip && "clip", i === li && "lead", c.cls].filter(Boolean).join(" ") || null;
  return h("div", { class: "tblw" },
    h("div", { class: "tbl tg", role: "table", "aria-label": caption || null, style: "--cols:" + columns.map(track).join(" ") },
      h("div", { class: "tr th", role: "row" }, columns.map((c, i) => h("span", { class: cls(c, i), role: "columnheader" }, c.label))),
      h("div", { class: "tb", role: "rowgroup" }, rows.map((r, i) => h("div", { class: "tr", role: "row", "data-key": key ? key(r, i) : null },
        columns.map((c, j) => h("span", { class: cls(c, j), role: "cell", "data-label": j !== li && typeof c.label === "string" && c.label ? c.label : null, style: c.style ? c.style(r) : null }, c.render ? c.render(r) : r[c.key])))))));
}
