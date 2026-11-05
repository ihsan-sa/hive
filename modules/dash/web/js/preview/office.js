// Office files read in the page: a small zip reader (the browser's own DecompressionStream does the inflating) and DOMParser
// on the XML inside. xlsx shows each sheet as a table, docx its headings, paragraphs and tables, pptx the text of each slide.
// It's a reading view, not a layout engine: no fonts or positions. Text goes in as text nodes only; a docx's embedded
// pictures (PNG, JPEG, GIF, WebP, BMP) show as <img> from a blob of their own bytes, never as markup.
import { h } from "../core/dom.js";
import { drawTable } from "../rich/table.js";

const ENTRY_MAX = 64 * 1048576, ROWS_MAX = 20000, COLS_MAX = 200;

/** name -> (bytes) => Promise<string> (or a Uint8Array when bytes is true), from a zip's central directory. */
export function unzip(buf) {
  const v = new DataView(buf), u8 = new Uint8Array(buf);
  let e = -1;
  for (let i = buf.byteLength - 22; i >= Math.max(0, buf.byteLength - 65557); i--) if (v.getUint32(i, true) === 0x06054b50) { e = i; break; }
  if (e < 0) throw new Error("it isn't a zip file, so it can't be an Office file");
  const count = v.getUint16(e + 10, true); let p = v.getUint32(e + 16, true);
  const out = new Map(), dec = new TextDecoder();
  for (let n = 0; n < count; n++) {
    if (v.getUint32(p, true) !== 0x02014b50) break;
    const method = v.getUint16(p + 10, true), csize = v.getUint32(p + 20, true), usize = v.getUint32(p + 24, true);
    const nl = v.getUint16(p + 28, true), xl = v.getUint16(p + 30, true), cl = v.getUint16(p + 32, true), off = v.getUint32(p + 42, true);
    const name = dec.decode(u8.subarray(p + 46, p + 46 + nl));
    p += 46 + nl + xl + cl;
    out.set(name, async bytes => {
      if (usize > ENTRY_MAX) throw new Error(name + " is too big to read here");
      const start = off + 30 + v.getUint16(off + 26, true) + v.getUint16(off + 28, true), data = u8.subarray(start, start + csize);
      if (method === 0) return bytes ? data.slice() : dec.decode(data);
      if (method !== 8) throw new Error("a part of it is packed in a way this reader doesn't know");
      const s = new Blob([data]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
      const r = new Response(s);
      return bytes ? new Uint8Array(await r.arrayBuffer()) : r.text();
    });
  }
  return out;
}
const xml = t => new DOMParser().parseFromString(t, "application/xml");
const kids = (el, name) => [...el.children].filter(c => c.localName === name);
const all = (el, name) => el.getElementsByTagNameNS("*", name);
async function read(z, name) { const f = z.get(name); return f ? xml(await f()) : null; }
async function rels(z, base) {   // "xl/workbook.xml" -> { rId: "xl/worksheets/sheet1.xml" }
  const dir = base.replace(/[^/]*$/, ""), doc = await read(z, dir + "_rels/" + base.slice(dir.length) + ".rels"), m = {};
  if (doc) for (const r of all(doc, "Relationship")) {
    const t = r.getAttribute("Target") || "";
    m[r.getAttribute("Id")] = t.startsWith("/") ? t.slice(1) : norm(dir + t);
  }
  return m;
}
function norm(p) { const o = []; for (const s of p.split("/")) { if (s === "..") o.pop(); else if (s !== ".") o.push(s); } return o.join("/"); }
const rid = el => el.getAttributeNS("http://schemas.openxmlformats.org/officeDocument/2006/relationships", "id") || el.getAttribute("r:id");

// ---- xlsx ----
const colNum = ref => { let n = 0; for (const ch of ref.replace(/\d+$/, "")) n = n * 26 + ch.charCodeAt(0) - 64; return n - 1; };
const colName = n => { let s = ""; for (n++; n > 0; n = Math.floor((n - 1) / 26)) s = String.fromCharCode(65 + ((n - 1) % 26)) + s; return s; };
export async function xlsx(buf) {
  const z = unzip(buf), wb = await read(z, "xl/workbook.xml");
  if (!wb) throw new Error("there's no workbook in it");
  const map = await rels(z, "xl/workbook.xml"), ssd = await read(z, "xl/sharedStrings.xml");
  const shared = ssd ? [...all(ssd, "si")].map(si => [...all(si, "t")].map(t => t.textContent).join("")) : [];
  const sheets = [...all(wb, "sheet")].map(s => ({ name: s.getAttribute("name"), path: map[rid(s)] })).filter(s => s.path && z.has(s.path));
  if (!sheets.length) throw new Error("it has no sheets");
  async function rowsOf(path) {
    const d = await read(z, path), rows = []; let width = 0;
    for (const r of all(d, "row")) {
      const at = (+r.getAttribute("r") || rows.length + 1) - 1; if (at >= ROWS_MAX) break;
      const row = rows[at] = [];
      for (const c of kids(r, "c")) {
        const ci = c.getAttribute("r") ? colNum(c.getAttribute("r")) : row.length; if (ci >= COLS_MAX) continue;
        const t = c.getAttribute("t"), vEl = kids(c, "v")[0], raw = vEl ? vEl.textContent : "";
        row[ci] = t === "s" ? shared[+raw] ?? "" : t === "inlineStr" ? [...all(c, "t")].map(x => x.textContent).join("") : t === "b" ? (raw === "1" ? "TRUE" : "FALSE") : raw;
        width = Math.max(width, ci + 1);
      }
    }
    const filled = Array.from(rows, r => Array.from({ length: width }, (_, i) => (r && r[i] != null ? String(r[i]) : "")));
    while (filled.length && filled[filled.length - 1].every(x => x === "")) filled.pop();
    return filled;
  }
  const box = h("div", { class: "pv-sheet" }), tabs = h("div", { class: "pv-tabs", role: "tablist", "aria-label": "Sheets" });
  async function pick(i) {
    for (const [j, b] of [...tabs.children].entries()) b.setAttribute("aria-selected", String(j === i));
    const rows = await rowsOf(sheets[i].path);
    if (!rows.length) return box.replaceChildren(h("p", { class: "meta" }, "This sheet is empty."));
    const head = rows[0].every(x => x !== "") ? rows : [rows[0].map((_, c) => colName(c)), ...rows];
    box.replaceChildren(h("div", { class: "rc-block" }, drawTable(head, sheets[i].name, {}, { col: -1, dir: 1, q: "", shown: 500 })));
  }
  sheets.forEach((s, i) => tabs.append(h("button", { type: "button", role: "tab", class: "pv-tab", onclick: () => pick(i) }, s.name)));
  await pick(0);
  return h("div", null, sheets.length > 1 ? tabs : null, box);
}

// ---- docx ----
const REL = "http://schemas.openxmlformats.org/officeDocument/2006/relationships";
/** A picture's placeholder: an <img> with the relationship id of its bytes, filled in by docx(). */
function picture(c) {
  const blip = all(c, "blip")[0] || all(c, "imagedata")[0], id = blip && (blip.getAttributeNS(REL, "embed") || blip.getAttributeNS(REL, "id") || blip.getAttribute("r:embed") || blip.getAttribute("r:id"));
  if (!id) return " [picture] ";
  const ext = all(c, "extent")[0], pr = all(c, "docPr")[0], w = ext ? Math.round(+ext.getAttribute("cx") / 9525) : 0;
  return h("img", { class: "pv-docimg", "data-rid": id, alt: (pr && (pr.getAttribute("descr") || pr.getAttribute("name"))) || "picture", width: w > 0 ? String(w) : null });
}
function runs(p) {
  const out = [];
  for (const r of all(p, "r")) {
    const pr = kids(r, "rPr")[0], b = pr && kids(pr, "b").length, i = pr && kids(pr, "i").length;
    let t = "";
    const text = () => { if (!t) return; let n = t; if (i) n = h("em", null, n); if (b) n = h("strong", null, n); out.push(n); t = ""; };
    for (const c of r.children) {
      if (c.localName === "drawing" || c.localName === "pict") { const pic = picture(c); if (typeof pic === "string") t += pic; else { text(); out.push(pic); } }
      else t += c.localName === "t" ? c.textContent : c.localName === "tab" ? "\t" : c.localName === "br" ? "\n" : "";
    }
    text();
  }
  return out;
}
const PIC = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", bmp: "image/bmp" };
/** Fills each picture placeholder with its bytes from word/media, as a typed blob; one it can't show becomes "[picture]". */
async function pictures(z, root) {
  const imgs = [...root.querySelectorAll("img[data-rid]")];
  if (!imgs.length) return;
  const map = await rels(z, "word/document.xml");
  for (const img of imgs.slice(0, 200)) {
    const path = map[img.dataset.rid], type = path && PIC[path.split(".").pop().toLowerCase()];
    if (!type || !z.has(path)) { img.replaceWith(" [picture] "); continue; }
    try {
      const url = URL.createObjectURL(new Blob([await z.get(path)(true)], { type }));
      img.addEventListener("load", () => URL.revokeObjectURL(url), { once: true });
      img.removeAttribute("data-rid"); img.src = url;
    } catch { img.replaceWith(" [picture] "); }
  }
  for (const img of root.querySelectorAll("img[data-rid]")) img.replaceWith(" [picture] ");
}
function para(p, num) {
  const pr = kids(p, "pPr")[0], st = pr && kids(pr, "pStyle")[0], style = st ? st.getAttribute("w:val") || st.getAttributeNS("*", "val") || "" : "";
  const lvl = /^(?:heading|berschrift|titre)\s*(\d)$/i.exec(style) || (/^title$/i.test(style) ? [0, "1"] : null);
  // A list item: numbering on the paragraph (looked up in numbering.xml), or a list style such as "ListBullet"/"ListNumber".
  const np = pr && kids(pr, "numPr")[0], ilvl = np && kids(np, "ilvl")[0], nid = np && kids(np, "numId")[0];
  const val = el => el ? el.getAttribute("w:val") || el.getAttributeNS("*", "val") || "" : "";
  const list = np && val(nid) !== "0" ? ((num && num(val(nid), val(ilvl) || "0")) || "bullet") : /^list\s*(?:bullet|number|paragraph)/i.test(style) ? (/number/i.test(style) ? "decimal" : "bullet") : null;
  const body = runs(p);
  if (lvl) return h("h" + Math.min(6, +lvl[1] + 1), null, body);
  if (list) { const li = h("li", null, body); li.dataset.ol = list === "bullet" || list === "none" ? "" : "1"; return li; }
  return body.length ? h("p", null, body) : null;
}
/** numId, level -> its number format ("bullet", "decimal", ...) from word/numbering.xml. */
async function numbering(z) {
  const d = await read(z, "word/numbering.xml"); if (!d) return null;
  const v = el => el ? el.getAttribute("w:val") || el.getAttributeNS("*", "val") || "" : "", abs = {}, map = {};
  for (const a of all(d, "abstractNum")) abs[a.getAttribute("w:abstractNumId") || a.getAttributeNS("*", "abstractNumId")] = a;
  for (const n of all(d, "num")) map[n.getAttribute("w:numId") || n.getAttributeNS("*", "numId")] = abs[v(kids(n, "abstractNumId")[0])];
  return (id, lv) => { const a = map[id]; if (!a) return null; const l = kids(a, "lvl").find(x => (x.getAttribute("w:ilvl") || x.getAttributeNS("*", "ilvl")) === lv); return l ? v(kids(l, "numFmt")[0]) || null : null; };
}
export async function docx(buf) {
  const z = unzip(buf), d = await read(z, "word/document.xml");
  if (!d) throw new Error("there's no document in it");
  const body = all(d, "body")[0], out = [], num = await numbering(z), P = el => para(el, num);
  let list = null;
  for (const el of body ? body.children : []) {
    if (el.localName === "p") {
      const n = P(el); if (!n) { list = null; continue; }
      if (n.tagName === "LI") {
        const tag = n.dataset.ol ? "OL" : "UL"; delete n.dataset.ol;
        if (!list || list.tagName !== tag) out.push(list = h(tag.toLowerCase()));
        list.append(n);
      } else { list = null; out.push(n); }
    } else if (el.localName === "tbl") {
      list = null;
      out.push(h("div", { class: "rc-tw" }, h("table", { class: "rc-tbl" }, h("tbody", null, kids(el, "tr").map(tr => h("tr", null, kids(tr, "tc").map(tc => h("td", null, kids(tc, "p").map(P)))))))));
    }
  }
  const root = h("div", { class: "md pv-docx" }, out.length ? out : h("p", { class: "meta" }, "No text in it."));
  await pictures(z, root);
  return root;
}

// ---- pptx ----
export async function pptx(buf) {
  const z = unzip(buf), pres = await read(z, "ppt/presentation.xml");
  if (!pres) throw new Error("there's no presentation in it");
  const map = await rels(z, "ppt/presentation.xml");
  const paths = [...all(pres, "sldId")].map(s => map[rid(s)]).filter(p => p && z.has(p));
  const out = [];
  for (const [i, p] of paths.entries()) {
    const d = await read(z, p), paras = [];
    for (const sp of all(d, "sp")) {
      const ph = all(sp, "ph")[0], title = ph && /title/i.test(ph.getAttribute("type") || "");
      for (const ap of all(sp, "p")) {
        const t = [...all(ap, "t")].map(x => x.textContent).join("");
        if (t.trim()) paras.push(title ? h("h3", null, t) : h("p", null, t));
      }
    }
    const pics = all(d, "pic").length;
    out.push(h("section", { class: "pv-slide" }, h("span", { class: "pv-slide-n meta" }, `Slide ${i + 1}` + (pics ? ` · ${pics} picture${pics > 1 ? "s" : ""}` : "")), paras.length ? paras : h("p", { class: "meta" }, "No text on this slide.")));
  }
  return h("div", { class: "md pv-deck" }, out.length ? out : h("p", { class: "meta" }, "No slides in it."));
}
