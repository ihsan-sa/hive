// Post text as DOM nodes, never an HTML string: any HTML in a post stays visible text, only http(s) links are made,
// and images stay links because this page loads nothing from elsewhere. Single line breaks are kept, as the board means them.
// Emphasis is stricter than GFM on purpose: `*` has to open after a space or bracket, so globs and products stay as written.
import { h } from "../core/dom.js";

const INLINE = new RegExp([
  "\\\\([\\\\`*_~\\[\\]()#>|!-])",                                         // 1 escaped character
  "(`+)(?!`)(.+?)(?<!`)\\2(?!`)",                                          // 2,3 code
  "!?\\[([^\\]\\n]+)\\]\\((https?:\\/\\/[^\\s)]+)(?:\\s+\"[^\"]*\")?\\)",  // 4,5 link or image
  "\\*\\*(?=\\S)(.+?)(?<=\\S)\\*\\*",                                      // 6 bold
  "(?<![A-Za-z0-9_])__(?=\\S)(.+?)(?<=\\S)__(?![A-Za-z0-9_])",             // 7 bold
  "(?<![^\\s(\\[\"'>])\\*(?=[^\\s*])(.+?)(?<=[^\\s*/])\\*(?![A-Za-z0-9*])", // 8 italic
  "(?<![A-Za-z0-9_])_(?=[^\\s_])(.+?)(?<=[^\\s_])_(?![A-Za-z0-9_])",       // 9 italic
  "~~(?=\\S)(.+?)(?<=\\S)~~",                                              // 10 struck out
  "https?:\\/\\/[^\\s<>]+",                                                // bare link
].join("|"), "g");

const link = (href, ...kids) => h("a", { href, target: "_blank", rel: "noopener noreferrer" }, ...kids);

function inline(s) {
  const out = []; let at = 0;
  for (const m of s.matchAll(INLINE)) {
    if (m.index < at) continue;
    let end = m.index + m[0].length, node;
    if (m[1] != null) node = m[1];
    else if (m[3] != null) { const c = m[3]; node = h("code", null, /^ .*[^ ].* $|^ [^ ] $/.test(c) ? c.slice(1, -1) : c); }
    else if (m[5] != null) node = link(m[5], inline(m[4]));
    else if (m[6] != null || m[7] != null) node = h("strong", null, inline(m[6] ?? m[7]));
    else if (m[8] != null || m[9] != null) node = h("em", null, inline(m[8] ?? m[9]));
    else if (m[10] != null) node = h("del", null, inline(m[10]));
    else {
      // A bare link: leave sentence punctuation and an unmatched ) outside it.
      let u = m[0].replace(/[.,;:!?'"*_`\]]+$/, "");
      while (u.endsWith(")") && u.match(/\)/g).length > (u.match(/\(/g) || []).length) u = u.slice(0, -1);
      end = m.index + u.length; node = link(u, u);
    }
    if (m.index > at) out.push(s.slice(at, m.index));
    out.push(node); at = end;
  }
  if (at < s.length) out.push(s.slice(at));
  return out;
}

const FENCE = /^ *(`{3,}|~{3,})/, HEAD = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/, HR = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/, QUOTE = /^ {0,3}> ?/,
  ITEM = /^( *)([-*+]|\d{1,9}[.)])([ \t]+)(.*)$/, TSEP = /^ *\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;
const indent = l => l.match(/^ */)[0].length;

// A | inside code doesn't split a cell.
function cells(l) {
  l = l.trim().replace(/^\|/, ""); const out = []; let c = "", tick = false;
  for (let i = 0; i < l.length; i++) {
    const k = l[i];
    if (k === "\\" && l[i + 1] === "|") { c += "\\|"; i++; }
    else if (k === "`") { tick = !tick; c += k; }
    else if (k === "|" && !tick) { out.push(c.trim()); c = ""; }
    else c += k;
  }
  if (c.trim()) out.push(c.trim());
  return out;
}
const isTable = (l, n) => l.includes("|") && n != null && n.includes("-") && n.includes("|") && TSEP.test(n);
const startsBlock = (l, n) => FENCE.test(l) || HEAD.test(l) || HR.test(l) || QUOTE.test(l) || ITEM.test(l) || isTable(l, n);

function blocks(ls) {
  const out = []; let i = 0;
  while (i < ls.length) {
    const l = ls[i]; let m;
    if (!l.trim()) { i++; continue; }
    if ((m = l.match(FENCE))) { // an unclosed fence runs to the end
      const pad = indent(l), end = new RegExp("^ *" + m[1][0] + "{" + m[1].length + ",}[ \\t]*$"), code = [];
      for (i++; i < ls.length && !end.test(ls[i]); i++) code.push(ls[i].slice(Math.min(pad, indent(ls[i]))));
      i++; out.push(h("pre", null, h("code", null, code.join("\n")))); continue;
    }
    if (isTable(l, ls[i + 1])) {
      const head = cells(l), rows = [];
      for (i += 2; i < ls.length && ls[i].trim() && ls[i].includes("|"); i++) rows.push(cells(ls[i]));
      out.push(h("div", { class: "md-tw" }, h("table", null,
        h("thead", null, h("tr", null, head.map(c => h("th", null, inline(c))))),
        h("tbody", null, rows.map(r => h("tr", null, head.map((_, k) => h("td", null, inline(r[k] || "")))))))));
      continue;
    }
    if ((m = l.match(HEAD))) { out.push(h("h" + m[1].length, null, inline(m[2]))); i++; continue; }
    if (HR.test(l)) { out.push(h("hr")); i++; continue; }
    if (QUOTE.test(l)) { const q = []; for (; i < ls.length && QUOTE.test(ls[i]); i++) q.push(ls[i].replace(QUOTE, "")); out.push(h("blockquote", null, blocks(q))); continue; }
    if ((m = l.match(ITEM))) {
      const ord = /\d/.test(m[2]), pad = m[1].length, items = [];
      while (i < ls.length) {
        const k = ls[i].match(ITEM);
        if (!k || /\d/.test(k[2]) !== ord || k[1].length > pad + 1 || HR.test(ls[i])) break;
        const width = k[1].length + k[2].length + k[3].length, body = [k[4]];
        for (i++; i < ls.length; i++) {
          const x = ls[i];
          if (x.trim()) { if (indent(x) <= k[1].length) break; body.push(x.slice(Math.min(width, indent(x)))); continue; }
          let j = i; while (j < ls.length && !ls[j].trim()) j++; // blank lines stay in the item only if indented text follows
          if (j >= ls.length || indent(ls[j]) <= k[1].length) break;
          body.push("");
        }
        items.push(h("li", null, blocks(body)));
        let j = i; while (j < ls.length && !ls[j].trim()) j++; i = j;
      }
      const start = ord ? parseInt(m[2], 10) : 1;
      out.push(h(ord ? "ol" : "ul", start !== 1 ? { start } : null, items));
      continue;
    }
    const para = [l];
    for (i++; i < ls.length && ls[i].trim() && !startsBlock(ls[i], ls[i + 1]); i++) para.push(ls[i]);
    out.push(h("p", null, inline(para.join("\n"))));
  }
  return out;
}

/** markdown(text): a div.md of DOM nodes. */
export const markdown = text => h("div", { class: "md" }, blocks(String(text ?? "").replace(/\r\n?/g, "\n").split("\n")));

/** The collapsed view of a long post: cut on a line end if one is near, and close any bold or code the cut left open. */
export function mdCut(s, n) {
  let c = s.slice(0, n); const nl = c.lastIndexOf("\n");
  if (nl > n / 2) c = c.slice(0, nl); else { const sp = c.lastIndexOf(" "); if (sp > n / 2) c = c.slice(0, sp); }
  c = c.trimEnd().replace(/[.,;:]+$/, "");
  const last = c.slice(c.lastIndexOf("\n") + 1);
  if (!FENCE.test(last)) { const odd = x => (last.split(x).length - 1) % 2 === 1; if (odd("`")) c += "`"; if (odd("**")) c += "**"; }
  return c + "…";
}
