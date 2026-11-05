// Data table. Columns are data: {label, key | render(row), num, mono, wrap, clip, width}. clip: takes the spare width and ellipsizes; give the cell a title..
import { h } from "../core/dom.js";
import { empty } from "./kit.js";

export function table({ columns, rows, key, empty: none = "Nothing here.", caption }) {
  if (!rows.length) return empty(none);
  const cls = c => [c.num && "num", c.mono && "mono", c.wrap && "wrap", c.clip && "clip", c.cls].filter(Boolean).join(" ") || null;
  return h("div", { class: "tbl-w" },
    h("table", { class: "tbl" },
      caption ? h("caption", { class: "vh" }, caption) : null,
      h("thead", null, h("tr", null, columns.map(c => h("th", { class: cls(c), scope: "col", style: c.width ? `width:${c.width}` : null }, c.label)))),
      h("tbody", null, rows.map((r, i) => h("tr", { "data-key": key ? key(r, i) : null },
        columns.map(c => h("td", { class: cls(c), style: c.style ? c.style(r) : null }, c.render ? c.render(r) : r[c.key])))))));
}
