// A csv or tsv fence as a table: sort, filter, copy, download, ask about a row. Cells only ever go in as text.
import { h } from "../core/dom.js";
import { figure } from "./figure.js";
import { askQuote } from "./ask.js";
import { sigOf } from "./math.js";

const MAX = 2000000, PAGE = 500, ST = new Map();
const stateOf = k => { let s = ST.get(k); if (!s) { ST.set(k, s = { col: -1, dir: 1, q: "", shown: PAGE }); if (ST.size > 200) ST.delete(ST.keys().next().value); } return s; };

/** Rows of cells. Quotes follow RFC 4180; the separator is a tab for tsv, or a comma. */
export function parseCsv(text, sep = ",") {
  const rows = []; let row = [], cell = "", q = false;
  const end = () => { row.push(cell); cell = ""; }, line = () => { end(); if (row.length > 1 || row[0] !== "") rows.push(row); row = []; };
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) { if (c === '"') { if (text[i + 1] === '"') { cell += '"'; i++; } else q = false; } else cell += c; }
    else if (c === '"' && cell === "") q = true;
    else if (c === sep) end();
    else if (c === "\n") line();
    else if (c !== "\r") cell += c;
  }
  if (cell !== "" || row.length) line();
  return rows;
}

const SI = { p: 1e-12, n: 1e-9, u: 1e-6, m: 1e-3, k: 1e3, M: 1e6, G: 1e9, T: 1e12, meg: 1e6 };
/** A number as a person writes it: 1.5, 2e6, 120k. NaN for anything else. */
export function numOf(t) {
  const m = /^\s*([-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?)(meg|[pnumkMGT])?\s*$/.exec(t);
  return m ? +m[1] * (m[2] ? SI[m[2]] : 1) : NaN;
}

function drawTable(rows, title, api, st) {
  const head = rows[0], body = rows.slice(1).map((r, i) => ({ r, i, low: r.join("\t").toLowerCase() }));
  const numeric = head.map((_, c) => body.some(x => x.r[c]) && body.every(x => !x.r[c] || !isNaN(numOf(x.r[c]))));
  const tb = h("tbody"), more = h("button", { type: "button", class: "rc-foldb", hidden: true });
  const heads = head.map((t, c) => h("th", { scope: "col", "data-t": numeric[c] ? null : "" },
    h("button", { type: "button", onclick: () => { if (st.col === c) st.dir = -st.dir; else { st.col = c; st.dir = 1; } paint(); } }, t)));
  function paint() {
    const q = st.q.trim().toLowerCase();
    let list = q ? body.filter(x => x.low.includes(q)) : body;
    if (st.col >= 0) {
      const c = st.col, val = x => (numeric[c] ? numOf(x.r[c] || "") : x.r[c] || "");
      list = [...list].sort((a, b) => {
        const u = val(a), v = val(b);
        if (numeric[c]) return (isNaN(u) ? 1 : isNaN(v) ? -1 : (u - v) * st.dir);
        return u.localeCompare(v, undefined, { numeric: true }) * st.dir;
      });
    }
    heads.forEach((th, c) => { th.setAttribute("aria-sort", st.col === c ? (st.dir > 0 ? "ascending" : "descending") : "none"); th.firstChild.textContent = head[c] + (st.col === c ? (st.dir > 0 ? " ↑" : " ↓") : ""); });
    tb.replaceChildren(...list.slice(0, st.shown).map(x => h("tr", null,
      ...head.map((_, c) => h("td", { "data-t": numeric[c] ? null : "" }, x.r[c] ?? "")),
      h("td", null, h("button", { type: "button", class: "rc-t rc-ask", "aria-label": `Ask about row ${x.i + 1}`, onclick: (e, b) => askQuote(b, head.map((t, c) => `${t}: ${x.r[c] ?? ""}`).join(", "), `${title || "table"}, row ${x.i + 1}`) }, "Ask")))));
    more.hidden = list.length <= st.shown;
    more.textContent = `Show ${Math.min(PAGE, list.length - st.shown)} more of ${list.length - st.shown} rows`;
  }
  more.onclick = () => { st.shown += PAGE; paint(); };
  api.filter = q => { st.q = q; st.shown = PAGE; paint(); };
  paint();
  return h("div", null, h("div", { class: "rc-tw", tabindex: "0" }, h("table", { class: "rc-tbl" }, h("thead", null, h("tr", null, ...heads, h("th", { "aria-label": "Ask" }))), tb)), more);
}

export function tableFigure(code, { lang, title }) {
  const sep = lang === "tsv" ? "\t" : ",", st = stateOf(sigOf(lang + "\0" + code)), api = {};
  const name = /^[\w.-]+$/.test(title || "") ? title : "table." + lang;
  return figure({
    kind: lang, label: lang, title, src: code, reserve: 160,
    tools: [
      h("input", { class: "rc-filter", type: "search", placeholder: "Filter", "aria-label": "Filter rows", value: st.q, oninput: (e, n) => api.filter?.(n.value) }),
      h("button", { type: "button", class: "rc-t", "aria-label": "Download this table", onclick: () => {
        const u = URL.createObjectURL(new Blob([code], { type: lang === "tsv" ? "text/tab-separated-values" : "text/csv" }));
        const a = h("a", { href: u, download: /\.\w+$/.test(name) ? name : name + "." + lang }); document.body.append(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(u), 5000);
      } }, "Download"),
    ],
    draw: () => {
      if (code.length > MAX) throw new Error("It's over 2 MB.");
      const rows = parseCsv(code, sep);
      if (!rows.length) throw new Error("It has no rows.");
      return { own: drawTable(rows, title, api, st) };
    },
  });
}
