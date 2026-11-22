// A Markdown file as printed pages: the rendered document set on US Letter sheets (8.5 × 11 in at 96 px an inch, 0.75 in margins),
// in a book face, numbered at the foot, scaled to fit the panel. The preview panel's Page tab (js/ui/preview.js), the Edit tab's live
// side (edit.js) and file markup's boxes (markup.js, which finds the .pg-sheet elements) use it. Save as → PDF prints the same pages.
// The person, 1122104123fb: "view MDs in PDF render where it renders them nicely as a PDF and then I can comment and box on those".
// paged(text) → node (.pv-pg). The split into pages happens once the node is in the document, since it needs measuring.
import { h } from "../core/dom.js";
import { markdown } from "../ui/markdown.js";

export const PAGE_W = 816, PAGE_H = 1056, MARGIN = 72;
const CH = PAGE_H - 2 * MARGIN;   // the text block's height
const HEAD = /^H[1-6]$/;

function sheet() {
  const c = h("div", { class: "pg-c" });
  return { sheet: h("div", { class: "pg-sheet" }, c, h("div", { class: "pg-n", "aria-hidden": "true" })), c };
}
const over = c => { const l = c.lastElementChild; return !!l && l.offsetTop + l.offsetHeight > CH + 0.5; };

// A list or table that runs off the foot is split between items: as many as fit stay, the rest go on as a copy of the block.
function split(b, c, alone) {
  let box = b, kids, rest;
  if (b.tagName === "UL" || b.tagName === "OL") {
    kids = [...b.children]; rest = b.cloneNode(false);
  } else if (b.classList && b.classList.contains("md-tw") && b.querySelector("tbody")) {
    box = b.querySelector("tbody"); kids = [...box.children];
    rest = b.cloneNode(true); rest.querySelector("tbody").replaceChildren();
  } else return null;
  if (kids.length < 2) return null;
  kids.forEach(k => k.remove());
  let n = 0;
  for (; n < kids.length; n++) { box.append(kids[n]); if (over(c)) { kids[n].remove(); break; } }
  if (n === kids.length) return null;   // it fits after all
  if (!n && !alone) { kids.forEach(k => box.append(k)); return null; }
  if (!n) { box.append(kids[0]); n = 1; }
  const into = rest.tagName === "UL" || rest.tagName === "OL" ? rest : rest.querySelector("tbody");
  kids.slice(n).forEach(k => into.append(k));
  if (rest.tagName === "OL") rest.start = (b.start || 1) + n;
  rest.dataset.cont = "";
  return rest;
}

/** Lay the rendered Markdown's blocks out on sheets inside host (which must be in the document). Returns the sheets. */
export function paginate(md, host) {
  const queue = [...md.children], sheets = [];
  let pg = null;
  const fresh = () => { pg = sheet(); host.append(pg.sheet); sheets.push(pg.sheet); };
  while (queue.length) {
    const b = queue.shift();
    if (!pg) fresh();
    pg.c.append(b);
    if (!over(pg.c)) continue;
    const alone = pg.c.children.length === 1;
    const rest = split(b, pg.c, alone);
    if (rest) { queue.unshift(rest); pg = null; continue; }
    if (alone) { pg.sheet.dataset.tall = ""; pg = null; continue; }   // taller than a page by itself: its sheet grows
    b.remove();
    if ([...pg.c.children].every(x => HEAD.test(x.tagName))) { pg.c.append(b); pg.sheet.dataset.tall = ""; pg = null; continue; }   // only its heading above it: they stay together on a sheet that grows
    const prev = pg.c.lastElementChild;   // a heading never ends a page: it goes over with what it heads
    if (prev && HEAD.test(prev.tagName) && pg.c.children.length > 1) { prev.remove(); queue.unshift(prev, b); }
    else queue.unshift(b);
    pg = null;
  }
  if (!sheets.length) fresh();
  sheets.forEach((s, i) => { s.dataset.p = String(i + 1); s.querySelector(".pg-n").textContent = sheets.length > 1 ? `${i + 1} of ${sheets.length}` : ""; });
  return sheets;
}

// A diagram or formula drawn after the split (mermaid, math) can come out taller than it measured: its sheet grows rather than cut it.
function grow(sheets) {
  const ro = new ResizeObserver(() => sheets.forEach(s => {
    const c = s.querySelector(".pg-c"); if (!c || s.hasAttribute("data-tall") || !c.lastElementChild) return;
    const C = c.getBoundingClientRect(), L = c.lastElementChild.getBoundingClientRect();   // in screen terms on both sides, so the panel's zoom cancels out
    // Small slack: scaled down in a narrow panel, lines round to whole pixels and a full page can come out a few px long (into the margin).
    if (C.height && (L.bottom - C.top) / C.height > 1.03) s.dataset.tall = ""; }));
  sheets.forEach(s => { for (const b of s.querySelector(".pg-c").children) ro.observe(b); });
}

/** The pages scale down to the width they have (never up), like a PDF viewer's fit-to-width. */
function fit(root, wrap) {
  const ro = new ResizeObserver(() => {
    const w = root.clientWidth;
    if (!w) return;
    wrap.style.zoom = String(Math.min(1, (w - (w < 500 ? 16 : 40)) / PAGE_W));
  });
  ro.observe(root);
  return ro;
}

export function paged(text, { cut } = {}) {
  const wrap = h("div", { class: "pg-sheets" });
  const root = h("div", { class: "pv-pg", "data-paging": "" }, wrap,
    cut ? h("p", { class: "pv-cut meta" }, "Cut here: the file is longer. Download it for the rest.") : null);
  const md = markdown(text);
  let ro = null, tries = 0;
  const lay = () => {
    if (!root.isConnected) { if (++tries < 600) requestAnimationFrame(lay); return; }   // measured only once it is on screen (given up after ~10 s off it)
    // Measured off to the side at full size, so the panel's zoom and width don't change where pages break.
    const host = h("div", { class: "pg-sheets pg-measure", "aria-hidden": "true" });
    document.body.append(host);
    try { const ss = paginate(md, host); wrap.replaceChildren(...host.children); grow(ss); } finally { host.remove(); }
    delete root.dataset.paging;
    if (!ro) ro = fit(root, wrap);
    root.dispatchEvent(new CustomEvent("pg-laid", { bubbles: true }));
  };
  (document.fonts && document.fonts.ready ? document.fonts.ready : Promise.resolve()).then(() => requestAnimationFrame(lay));
  return root;
}
