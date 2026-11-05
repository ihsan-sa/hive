// Post text as DOM nodes, never an HTML string: any HTML in a post stays visible text, only http(s) links are made,
// and images stay links because this page loads nothing from elsewhere. Single line breaks are kept, as the board means them.
// Emphasis is stricter than GFM on purpose: `*` has to open after a space or bracket, so globs and products stay as written.
// A plain post costs what it always did: maths, footnotes and fences build only light placeholders; js/rich/ draws them near the screen.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { fenceBlock } from "../rich/blocks.js";
import { equation, inlineMath } from "../rich/math.js";
import { mathScan, opensDisplay, displayMath } from "../rich/mathscan.js";

const INLINE = new RegExp([
  "\\\\([\\\\`*_~\\[\\]()#>|!$-])",                                        // 1 escaped character
  "(`+)(?!`)(.+?)(?<!`)\\2(?!`)",                                          // 2,3 code
  "!?\\[([^\\]\\n]+)\\]\\((https?:\\/\\/[^\\s)]+)(?:\\s+\"[^\"]*\")?\\)",  // 4,5 link or image
  "\\*\\*(?=\\S)(.+?)(?<=\\S)\\*\\*",                                      // 6 bold
  "(?<![A-Za-z0-9_])__(?=\\S)(.+?)(?<=\\S)__(?![A-Za-z0-9_])",             // 7 bold
  "(?<![^\\s(\\[\"'>])\\*(?=[^\\s*])(.+?)(?<=[^\\s*/])\\*(?![A-Za-z0-9*])", // 8 italic
  "(?<![A-Za-z0-9_])_(?=[^\\s_])(.+?)(?<=[^\\s_])_(?![A-Za-z0-9_])",       // 9 italic
  "~~(?=\\S)(.+?)(?<=\\S)~~",                                              // 10 struck out
  "https?:\\/\\/[^\\s<>]+",                                                // bare link
  "\\[\\^([^\\]\\s]+)\\]",                                                  // 11 footnote reference
  "(?<![\\w&#/])#([A-Za-z0-9][\\w.-]*[A-Za-z0-9_]|[A-Za-z0-9])",                // 12 #thread
  "(?<![\\w/.-])(?:~|\\/home\\/[\\w.-]+)?\\/files\\/([0-9a-f]{12})\\/([A-Za-z0-9_][A-Za-z0-9_.-]*[A-Za-z0-9_])",  // 13,14 a post's file
].join("|"), "g");
// A post's file, written as ~/files/<post id>/<name>: a link that opens it in the preview panel (preview.js handles the click).
const FILE = /^(?:~|\/home\/[\w.-]+)?\/files\/([0-9a-f]{12})\/([A-Za-z0-9_][A-Za-z0-9_.-]*)$/;
const fileRef = (id, name, ...kids) => h("a", { class: "ref", "data-kind": "file", "data-pv-key": id + "/" + name, href: `/files/${id}/${encodeURIComponent(name)}`, title: "Preview " + name }, ...kids);

// A file in a project or the kb, written as projects/x/plan.md: opens in the preview panel, which keeps showing its newest revision.
const KBREF = /^(?:~\/)?((?:projects|kb|modules|core|tools|investigations)\/[\w.-]+(?:\/[\w.-]+)*\.(?:md|markdown|txt|csv|json|ya?ml|py|sh|html))$/;
const kbRef = (path, ...kids) => h("a", { class: "ref", "data-kind": "kbfile", "data-pv-kb": path, "data-pv-key": "kb:" + path, href: "#knowledge/" + encodeURIComponent(path), title: "Open " + path + " (live)" }, ...kids);

const link = (href, ...kids) => h("a", { href, target: "_blank", rel: "noopener noreferrer" }, ...kids);

// While inline text runs, maths sits behind a placeholder (a private-use character and its index), so the emphasis and link
// rules can't reach into a formula. `\uE000` in a post itself turns the scan off, in mathScan.
const SLOT = /\uE000(\d+)\uE001/g;
const raw = (s, ms) => (ms ? s.replace(SLOT, (_, n) => ms[+n].src) : s);
function text(s, ms) {
  if (!ms || !s.includes("\uE000")) return s;
  const out = []; let at = 0;
  for (const m of s.matchAll(SLOT)) { out.push(s.slice(at, m.index), inlineMath(ms[+m[1]].tex, ms[+m[1]].src)); at = m.index + m[0].length; }
  out.push(s.slice(at));
  return out;
}

function run(s, ms) {
  const out = []; let at = 0;
  for (const m of s.matchAll(INLINE)) {
    if (m.index < at) continue;
    let end = m.index + m[0].length, node;
    if (m[1] != null) node = m[1];
    else if (m[3] != null) { let c = raw(m[3], ms); c = /^ .*[^ ].* $|^ [^ ] $/.test(c) ? c.slice(1, -1) : c; const f = FILE.exec(c), k = !f && KBREF.exec(c); node = f ? fileRef(f[1], f[2], h("code", null, c)) : k ? kbRef(k[1], h("code", null, c)) : h("code", null, c); }
    else if (m[5] != null) node = link(raw(m[5], ms), run(m[4], ms));
    else if (m[6] != null || m[7] != null) node = h("strong", null, run(m[6] ?? m[7], ms));
    else if (m[8] != null || m[9] != null) node = h("em", null, run(m[8] ?? m[9], ms));
    else if (m[10] != null) node = h("del", null, run(m[10], ms));
    else if (m[11] != null) node = footRef(m[11]) || m[0];
    else if (m[13] != null) node = fileRef(m[13], m[14], m[0]);
    else if (m[15] != null) node = kbRef(m[15], m[0]);
    else if (m[12] != null) node = S.threads && S.threads.has(m[12]) ? h("a", { class: "ref", "data-kind": "thread", href: threadHref(m[12]), title: "Thread " + m[12] }, "#" + m[12]) : m[0];
    else {
      // A bare link: leave sentence punctuation and an unmatched ) outside it.
      let u = m[0].replace(/[.,;:!?'"*_`\]]+$/, "");
      while (u.endsWith(")") && u.match(/\)/g).length > (u.match(/\(/g) || []).length) u = u.slice(0, -1);
      end = m.index + u.length; u = raw(u, ms); node = link(u, u);
    }
    if (m.index > at) out.push(text(s.slice(at, m.index), ms));
    out.push(node); at = end;
  }
  if (at < s.length) out.push(text(s.slice(at), ms));
  return out;
}

function inline(s) {
  if (!s.includes("$") && !s.includes("\\(")) return run(s, null);  // what almost every line takes
  const spans = mathScan(s);
  if (!spans.length) return run(s, null);
  const ms = []; let t = "", at = 0;
  for (const m of spans) { t += s.slice(at, m.from) + "\uE000" + ms.length + "\uE001"; ms.push({ tex: m.tex, src: s.slice(m.from, m.to) }); at = m.to; }
  return run(t + s.slice(at), ms);
}

// ---- footnotes: only a post that holds `[^` looks for them ----
let notes = null;
function footRef(id) {
  const n = notes && notes.get(id);
  return n ? h("sup", null, h("button", { type: "button", class: "rc-fn", "aria-label": "Footnote " + n.at, onclick: (e, b) => {
    b.closest(".md")?.querySelector(`[data-fn="${CSS.escape(id)}"]`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  } }, String(n.at))) : null;
}
const FOOT = /^\[\^([^\]\s]+)\]:[ \t]+(.*)$/;
/** Take the definitions out of the lines, outside fences. Returns the lines left. */
function takeNotes(ls) {
  const out = []; let open = null;
  for (let i = 0; i < ls.length; i++) {
    const f = ls[i].match(FENCE);
    if (open) { if (f && f[1][0] === open[0] && f[1].length >= open.length && !f[2].trim()) open = null; }
    else if (f) open = f[1];
    const m = !open && !f ? ls[i].match(FOOT) : null;
    if (!m) { out.push(ls[i]); continue; }
    const body = [m[2]];
    while (ls[i + 1] != null && /^ {2,}\S/.test(ls[i + 1])) body.push(ls[++i].trim());
    notes.set(m[1], { at: notes.size + 1, body });
  }
  return out;
}

const FENCE = /^ *(`{3,}(?=[^`]*$)|~{3,})(.*)$/, TASK = /^\[( |x|X)\][ \t]+/, HEAD = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/, HR = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/, QUOTE = /^ {0,3}> ?/,
  ITEM = /^( *)([-*+]|\d{1,9}[.)])([ \t]+)(.*)$/, TSEP = /^ *\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;
const indent = l => l.match(/^ */)[0].length;

// A | inside code doesn't split a cell.
function cells(l) {
  l = l.trim().replace(/^\|/, ""); const out = []; let c = "", tick = false;
  const ms = l.includes("$") || l.includes("\\(") ? mathScan(l) : null, inMath = i => ms && ms.some(m => i > m.from && i < m.to);  // nor one inside a formula
  for (let i = 0; i < l.length; i++) {
    const k = l[i];
    if (k === "\\" && l[i + 1] === "|") { c += "\\|"; i++; }
    else if (k === "`") { tick = !tick; c += k; }
    else if (k === "|" && !tick && !inMath(i)) { out.push(c.trim()); c = ""; }
    else c += k;
  }
  if (c.trim()) out.push(c.trim());
  return out;
}
const isTable = (l, n) => l.includes("|") && n != null && n.includes("-") && n.includes("|") && TSEP.test(n);
const startsBlock = (ls, i) => { const l = ls[i]; return FENCE.test(l) || HEAD.test(l) || HR.test(l) || QUOTE.test(l) || ITEM.test(l) || isTable(l, ls[i + 1]) || (opensDisplay(l) && displayMath(ls, i)); };

function blocks(ls) {
  const out = []; let i = 0;
  while (i < ls.length) {
    const l = ls[i]; let m;
    if (!l.trim()) { i++; continue; }
    if ((m = l.match(FENCE))) { // an unclosed fence runs to the end
      const pad = indent(l), end = new RegExp("^ *" + m[1][0] + "{" + m[1].length + ",}[ \\t]*$"), code = [];
      for (i++; i < ls.length && !end.test(ls[i]); i++) code.push(ls[i].slice(Math.min(pad, indent(ls[i]))));
      i++; out.push(fenceBlock(m[2], code.join("\n"))); continue;
    }
    if (opensDisplay(l) && (m = displayMath(ls, i))) { out.push(equation(m.tex)); i = m.end; continue; }
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
        const width = k[1].length + k[2].length + k[3].length, task = k[4].match(TASK), body = [task ? k[4].slice(task[0].length) : k[4]];
        for (i++; i < ls.length; i++) {
          const x = ls[i];
          if (x.trim()) { if (indent(x) <= k[1].length) break; body.push(x.slice(Math.min(width, indent(x)))); continue; }
          let j = i; while (j < ls.length && !ls[j].trim()) j++; // blank lines stay in the item only if indented text follows
          if (j >= ls.length || indent(ls[j]) <= k[1].length) break;
          body.push("");
        }
        const done = task && task[1] !== " ";
        items.push(task ? h("li", { class: "rc-task", "data-done": done ? "" : null }, h("span", { class: "rc-tk", role: "img", "aria-label": done ? "done" : "to do" }, done ? "✓" : "○"), h("div", null, blocks(body))) : h("li", null, blocks(body)));
        let j = i; while (j < ls.length && !ls[j].trim()) j++; i = j;
      }
      const start = ord ? parseInt(m[2], 10) : 1;
      out.push(h(ord ? "ol" : "ul", start !== 1 ? { start } : null, items));
      continue;
    }
    const para = [l];
    for (i++; i < ls.length && ls[i].trim() && !startsBlock(ls, i); i++) para.push(ls[i]);
    out.push(h("p", null, inline(para.join("\n"))));
  }
  return out;
}

/** markdown(text): a div.md of DOM nodes. */
export function markdown(text) {
  const t = String(text ?? "").replace(/\r\n?/g, "\n");
  let ls = t.split("\n");
  notes = t.includes("[^") ? new Map() : null;
  if (notes) ls = takeNotes(ls);
  const body = blocks(ls), got = notes && notes.size ? [...notes] : null;
  const end = got ? h("div", { class: "rc-fns" }, h("ol", null, got.map(([id, n]) => h("li", { "data-fn": id }, blocks(n.body))))) : null;
  notes = null;
  return h("div", { class: "md", "data-rich": "" }, body, end);
}

// Which fence a text ends inside, if any.
function openFence(s) {
  let at = 0, open = null, openAt = -1;
  for (const l of s.split("\n")) {
    const m = l.match(FENCE);
    if (open) { if (m && m[1][0] === open[0] && m[1].length >= open.length && !m[2].trim()) open = null; }
    else if (m) { open = m[1]; openAt = at; }
    at += l.length + 1;
  }
  return open ? { open, openAt } : null;
}

/** The collapsed view of a long post: cut on a line end if one is near, and close any bold or code the cut left open. */
export function mdCut(s, n) {
  let c = s.slice(0, n); const nl = c.lastIndexOf("\n");
  if (nl > n / 2) c = c.slice(0, nl); else { const sp = c.lastIndexOf(" "); if (sp > n / 2) c = c.slice(0, sp); }
  const f = openFence(c);
  if (f) {  // never cut a fence in half: stop before it, or if it comes early, take all of it (a long one folds itself)
    if (f.openAt > n / 4) c = s.slice(0, f.openAt);
    else {
      const close = new RegExp("^ *" + f.open[0] + "{" + f.open.length + ",}[ \\t]*$", "m"), m = close.exec(s.slice(c.length + 1));
      if (!m) return s;
      const to = c.length + 1 + m.index + m[0].length;
      return to >= s.length - 1 ? s : s.slice(0, to) + "\n…";
    }
  }
  c = c.trimEnd().replace(/[.,;:]+$/, "");
  const last = c.slice(c.lastIndexOf("\n") + 1);
  if (!FENCE.test(last)) { const odd = x => (last.split(x).length - 1) % 2 === 1; if (odd("`")) c += "`"; if (odd("**")) c += "**"; }
  return c + "…";
}
