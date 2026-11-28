// A writing space in the chat (thread 112515205dbf; design ~/designs/ink-chat/HANDOFF.md §1-3).
// ```ink with no on= offers a sheet: printed problem on ruled (grid, dot, blank) paper the person writes on with a Pencil, a pen,
// a mouse drag, or a finger on the Write page. ```ink on=<id> draws on one: in the agent's post it is a card that jumps to the
// sheet, and on the sheet it is a layer in that agent's pen that writes itself on once.
// The sheet is a live element (the pybox.js pattern): one per chat and sheet id, kept across the chat's redraws.
import { h, s } from "../core/dom.js";
import { S } from "../core/store.js";
import { agentLabel, capFirst } from "../core/model.js";
import { chatCtx } from "../rich/artifact.js";
import { printedNodes } from "./printed.js";
import { button } from "../ui/kit.js";
import { icon } from "../ui/icons.js";
import { parseInk, W, LINE } from "./grammar.js";
import * as K from "./store.js";
import { outline, outlineOf, markParts, bbox, snapShape, nearStroke, isLoop, writeMs, noteMs, plen, FAR } from "./draw.js";

const LIVE = new Map();   // th \0 sid -> {el, ghost, sheet}
const COARSE = matchMedia("(any-pointer: coarse)");
const COLORS = { red: "var(--ag-1)", violet: "var(--ag-2)", purple: "var(--ag-2)", teal: "var(--ag-3)", orange: "var(--ag-4)", blue: "var(--pen-blue)", green: "var(--pen-green)", black: "var(--ink)" };
export const sheetTitle = (sh, sid) => sid === "board" ? "Whiteboard" : /^m-/.test(sid) ? msgName(S.byId[sid.slice(2)]) : (sh && sh.title) || "Writing space";
const msgName = post => !post ? "a message" : K.isPerson(post.from) ? "your message" : capFirst(agentLabel(post.from) || post.from) + "'s message";
const plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;

const BLOCK = "p,li,div,h1,h2,h3,h4,h5,h6,td,th,pre,blockquote,dd,dt";
/** Where printed words are in root (an @"words" anchor): their client rect, or null. The text is read as shown: runs of space
 *  and the breaks between blocks count as one space, so words that wrap still match; KaTeX's hidden MathML and anything matching
 *  skip are left out. */
export function wordsRect(root, words, skip = ".katex-mathml") {
  const want = String(words).replace(/\s+/g, " ").trim().toLowerCase(); if (!want) return null;
  const acceptNode = n => n.parentElement && n.parentElement.closest(skip) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT;
  const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, { acceptNode }), map = [];
  let flat = "", line = null;
  const space = (n, i) => { if (flat && !flat.endsWith(" ")) { flat += " "; map.push([n, i]); } };
  for (let n; (n = walk.nextNode());) {
    const d = n.parentElement.closest(BLOCK); if (d !== line) { space(n, 0); line = d; }
    const v = n.nodeValue;
    for (let i = 0; i < v.length; i++) {
      if (/\s/.test(v[i])) { space(n, i); continue; }
      for (const c of v[i].toLowerCase()) { flat += c; map.push([n, i]); }
    }
  }
  const i = flat.indexOf(want); if (i < 0) return null;
  const [a, ao] = map[i], [b, bo] = map[i + want.length - 1], r = document.createRange();
  try { r.setStart(a, ao); r.setEnd(b, Math.min(bo + 1, b.nodeValue.length)); } catch (e) { return null; }
  const R = r.getBoundingClientRect(); return R.width || R.height ? R : null;
}

// ---------- an agent's layer as SVG ----------
/** One mark as a <g class=ink-ag>: paths in its pen and its note, with write-on timing from `at` (ms). Returns {g, at} or null. */
function markG(m, seed, own, find, H, at, pen, z = 1) {
  let p; try { p = markParts(m, seed, own, find, H, z); } catch (e) { console.warn("ink mark", m, e); return null; } if (!p) return null;
  const g = s("g", { class: "ink-ag", "data-k": p.k, style: m.color && COLORS[m.color] ? `--ag:${COLORS[m.color]}` : pen ? `--ag:${pen}` : null });
  for (const d of p.paths) {
    const len = plen(d), dur = writeMs(d);
    const a = s("path", { d, style: `--len:${len};--at:${at}ms;--dur-w:${dur}ms` + (p.hl ? `;stroke-width:calc(${p.hl}px * var(--z, 1))` : "") });
    g.append(a); at += Math.round(dur * .85);
    if (p.k !== "hl") { const second = s("path", { d, transform: "translate(.7 .5)", style: `--len:${len};--at:${at - Math.round(dur * .85)}ms;--dur-w:${dur}ms` }); g.append(second); }
  }
  if (p.note) {
    const t = s("text", { class: "ink-note", x: p.note.x, y: p.note.y, style: `--at:${at}ms;--dur-w:${noteMs(p.note.lines)}ms;font-size:${p.note.n}px` });
    p.note.lines.forEach((l, i) => t.append(s("tspan", { x: p.note.x, dy: i ? p.note.n * 1.15 : 0 }, l)));
    g.append(t); at += noteMs(p.note.lines);
  }
  return { g, at: at + 120, box: p.box };
}
const strokeP = (st, pend) => s("path", { class: "ink-s", d: outlineOf(st), "data-tool": st.t === "hl" ? "hl" : null, "data-pending": pend ? "" : null, "data-id": st.id, style: st.t === "hl" ? null : `color:${K.PENS[st.c] || K.PENS.ink}` });

/** The ink on a sheet with no paper of its own (a message, the whiteboard): the agents' layers and the person's strokes, in an
 *  <svg class=ink-svg> 800 units wide and H high. find(words) places @"words" anchors; play starts fresh layers' write-on. */
export function inkLayerSvg(th, sid, H, { find = null, play = false, z = 1 } = {}) {
  const gL = s("g", { class: "ink-layers" }), gO = s("g", { class: "ink-own" }), hid = K.hidden(th, sid);
  const sh = K.fold(th).sheets.get(sid), own = K.ownStrokes(th, sid), sent = [];
  const gone = new Set(); if (sh) for (const l of sh.layers) if (l.own) for (const e of l.erase) gone.add(e);
  if (sh) for (const l of sh.layers) {
    if (l.own) { for (const st of l.strokes) if (!gone.has(st.id)) sent.push(bbox(st.p)); continue; }
    if (l.cleared || hid.has(l.who)) continue;
    const g = s("g", { class: "ink-layer", "data-who": l.who, "data-post": l.post }), pen = K.agentPen(th, l.who), boxes = sent.slice(); let at = 0;
    l.marks.forEach((m, i) => { const r = markG(m, l.post + ":" + i, boxes, find, H, at, pen, z); if (r) { g.append(r.g); at = r.at; } });
    if (play && K.shouldPlay(l.post)) { K.markPlayed(l.post); for (const c of g.children) c.setAttribute("data-new", ""); }
    gL.append(g);
  }
  gO.append(...own.map(st => strokeP(st, st.pending)));
  gO.toggleAttribute("data-hidden", hid.has("person"));
  return s("svg", { class: "ink-svg" + (z > 1 ? " ink-z" : ""), viewBox: `0 0 ${W} ${Math.max(1, H)}`, "aria-hidden": "true", style: z > 1 ? `--z:${z}` : null }, gL, gO);
}

// ---------- thumbnails (cards, chips) ----------
/** A small picture of part of a sheet: own strokes and the given marks, cropped to them. */
/** Marks tied to words (`underline @"cos(x²)"`) have no words to find in a thumbnail, so each gets a stand-in word box,
 *  one under the other: the card shows the mark's shape rather than an empty picture (R-1125-s). */
export function thumb(th, sid, { marks = null, who = null, w = 72, hgt = 48 } = {}) {
  let row = 0; const stand = a => ({ x: 40, y: 40 + 70 * row++, w: Math.min(W - 80, 16 * String(a).length + 24), h: 26 });
  const sv = s("svg", { class: "ink-thumb", "aria-hidden": "true" }), sh = th ? K.fold(th).sheets.get(sid) : null;
  const own = th ? K.ownStrokes(th, sid) : [], boxes = [];
  for (const st of own) { sv.append(strokeP(st)); boxes.push(bbox(st.p)); }
  if (marks) { const ob = own.map(st => bbox(st.p)); let i = 0; for (const m of marks) { const r = markG(m, (sid || "") + i++, ob, stand, (sh && sh.h) || 400, 0, who && th ? K.agentPen(th, who) : null); if (r) { sv.append(r.g); boxes.push(r.box); } } }
  let bb = boxes.length ? boxes.reduce((a, b) => ({ x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), x1: Math.max(a.x1, b.x + b.w), y1: Math.max(a.y1, b.y + b.h) }), { x: 1e9, y: 1e9, x1: -1e9, y1: -1e9 }) : { x: 0, y: 0, x1: W, y1: 160 };
  let bw = Math.max(80, bb.x1 - bb.x + 40), bh = Math.max(50, bb.y1 - bb.y + 40); const asp = w / hgt;
  if (bw / bh > asp) bh = bw / asp; else bw = bh * asp;
  const cx = (bb.x + bb.x1) / 2, cy = (bb.y + bb.y1) / 2;
  sv.setAttribute("viewBox", `${Math.round(cx - bw / 2)} ${Math.round(cy - bh / 2)} ${Math.round(bw)} ${Math.round(bh)}`);
  return sv;
}

// ---------- how far the paper reaches ----------
const G = 96;   // the paper grows in steps of 96 units, a whole number of rules (32) and dots (24), so they stay lined up
/** The part of sheet units a sheet shows, [x0, y0, x1, y1]: its 800 × H, and out to any ink of the person's past the left,
 *  right or top (the Write page has no sides; a bottom edge is already in H). */
export function inkBox(own, H) {
  let a = 0, b = 0, c = W;
  for (const st of own) for (const p of st.p) { if (p[0] < a) a = p[0]; if (p[1] < b) b = p[1]; if (p[0] > c) c = p[0]; }
  // a stroke that strays a few units over an edge doesn't zoom the sheet out
  if (a > -16) a = 0; if (b > -16) b = 0; if (c < W + 16) c = W;
  return [a < 0 ? Math.floor((a - 16) / G) * G : 0, b < 0 ? Math.floor((b - 16) / G) * G : 0, c > W ? Math.ceil((c + 16) / G) * G : W, H];
}
const union = (a, b) => b ? [Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.max(a[2], b[2]), Math.max(a[3], b[3])] : a;

// ---------- the live sheet ----------
export class Sheet {
  /** mode: "msg" (in a message), "focus" (the Write page: a finger draws), "static" (no chat: printed only),
   *  "snap" (offscreen, for the picture the agent gets: no input, nothing plays). */
  constructor(th, sid, { mode = "msg", offer = null } = {}) {
    this.th = th; this.sid = sid; this.mode = mode; this.offer = offer; this.ac = new AbortController();
    this.rows = 0; this.extra = 0; this.lg = new Map(); this.cur = null; this.seen = false;
    const sig = { signal: this.ac.signal };
    this.title = h("span", { class: "ink-sheet-t" });
    this.who = h("span", { class: "ink-who" });
    const tools = [];
    if (mode === "msg" && th) tools.push(button("Write", () => openFocus(th, sid), { kind: "quiet", size: "s", icon: "pen", cls: "ink-write", title: "Write on this sheet with a finger" }));
    this.head = h("div", { class: "ink-sheet-h" }, this.title, this.who, ...tools);
    this.p = h("div", { class: "ink-sheet-p" });
    this.gPrint = s("g", { class: "ink-printed" }); this.gOwn = s("g", { class: "ink-own" }); this.gLayers = s("g", { class: "ink-layers" }); this.gLive = s("g", { class: "ink-live" });
    this.svg = s("svg", { class: "ink-svg", viewBox: `0 0 ${W} ${6 * LINE}`, "aria-hidden": "true" }, this.gPrint, this.gLayers, this.gOwn, this.gLive);
    this.invite = h("div", { class: "ink-invite" }, icon("pen"), mode !== "msg" ? "Write here" : COARSE.matches ? "Tap to open, or write with your Pencil" : "Write here", mode === "msg" && !COARSE.matches ? h("small", {}, "drag to draw, click to open") : null);
    this.paper = h("div", { class: "ink-paper" });
    this.c = h("div", { class: "ink-sheet-c", "data-paper": "ruled" }, this.paper, this.p, this.svg, this.invite, h("div", { class: "ink-grow" }));
    this.f = h("div", { class: "ink-sheet-f" });
    this.el = h("div", { class: "ink-sheet", "data-mode": mode, "data-own": "", "data-sig": sid, "data-key": "ink-" + sid, "data-sheet": sid, "data-empty": "" }, this.head, this.c, this.f);
    this.el._sheet = this;
    if (mode === "msg" || mode === "focus") {
      const c = this.c;
      c.addEventListener("pointerdown", e => this.down(e), sig);
      c.addEventListener("pointermove", e => this.move(e), sig);
      c.addEventListener("pointerup", e => this.up(e), sig);
      c.addEventListener("pointercancel", e => this.up(e, true), sig);
      // A tap or a plain click on the sheet (or its title line) opens its page (the person, 112720497dfd); a scroll sends no click, a drag still draws.
      if (mode === "msg") this.el.addEventListener("click", e => {
        const tap = this.tap; this.tap = false;
        if (!th || e.target.closest("a, button, input, .rc-im, .ink-sheet-f")) return;
        if (tap || (this.head.contains(e.target) && !getSelection().toString())) openFocus(th, sid);
      }, sig);
      // iPad Safari: a Pencil on a pan-y surface would scroll; claiming its touch keeps the stroke (a finger still scrolls)
      const stylus = e => { if ([...e.touches].some(t => t.touchType === "stylus")) e.preventDefault(); };
      c.addEventListener("touchstart", stylus, { passive: false, signal: this.ac.signal });
      c.addEventListener("touchmove", stylus, { passive: false, signal: this.ac.signal });
      // under the Write page the chat's copy waits until it closes (closeFocus says so again)
      addEventListener("ink:change", e => { if ((!e.detail || e.detail.th === th) && !(mode === "msg" && K.writePage())) this.paint(); }, sig);
      addEventListener("ink:tool", () => this.toolAttr(), sig);
      this.toolAttr();
    }
    this.ro = new ResizeObserver(() => this.measure()); this.ro.observe(this.c); this.ro.observe(this.p);   // maths and pictures arrive late
    this.io = new IntersectionObserver(es => { for (const e of es) { this.seen = e.isIntersecting && mode !== "snap"; if (this.seen) this.play(); } }, { threshold: .25 }); this.io.observe(this.el);
    this.ac.signal.addEventListener("abort", () => { this.ro.disconnect(); this.io.disconnect(); });
    this.paint();
  }
  toolAttr() { this.c.dataset.tool = K.TOOL.t; }
  /** What the sheet is now: its offer, its layers, the person's ink. */
  model() {
    const f = this.th ? K.fold(this.th) : null, sh = f && f.sheets.get(this.sid);
    const o = this.offer || (sh && sh.offers.length ? sh.offers.at(-1).parsed : null);
    const info = o ? o.info : { paper: this.sid === "board" ? "dot" : "blank", h: 6 * LINE };
    return { sh, info, printed: o ? o.printed : "", figure: o ? o.marks : [], layers: sh ? sh.layers : [], by: sh && sh.by, title: sheetTitle(sh || (o && { title: o.info.title }), this.sid) };
  }
  measure() {
    const w = this.c.clientWidth; if (!w) return;
    this.k = W / w;
    // under 520 px the printed text grows to 3.6cqw (css .ink-sheet-p): the agents' notes and strokes grow with it, so they stay
    // as readable as the problem they mark
    const z = w <= 520 ? 1.44 : 1;
    if (z !== this.z) { this.z = z; this.svg.classList.toggle("ink-z", z > 1); this.svg.style.setProperty("--z", z); this.measured = false; }
    // rows: the sheet lines (LINE units) the printed problem covers, whatever its line height at this width
    const rows = this.p.textContent.trim() ? Math.ceil(this.p.scrollHeight * this.k / LINE - .05) : 0;
    if (rows !== this.rows || !this.measured || Math.round(w) !== this.w) { this.rows = rows; this.w = Math.round(w); this.measured = true; this.figDone = false; this.paint(); }
  }
  /** Find printed words on this sheet: their box in sheet units, or null (an @"words" anchor). */
  find(words) {
    if (!this.c.isConnected || !this.k) return null;
    const R = wordsRect(this.p, words), C = this.c.getBoundingClientRect(); if (!R) return null;
    const k = W / C.width;   // not this.k: a sheet zoomed out to hold wide ink is drawn smaller than it is laid out
    return { x: (R.left - C.left) * k, y: (R.top - C.top) * k, w: R.width * k, h: R.height * k };
  }
  /** Lay the drawing and the paper over what shows. In a message, ink past the sheet's edges zooms the whole sheet out to fit;
   *  on the Write page (this.view, grown by roam) the page scrolls over it, and what was in view stays put as it grows. */
  lay(more) {
    const H = this.H, box = inkBox(this.th ? K.ownStrokes(this.th, this.sid) : [], H);
    const focus = this.mode === "focus" && this.sc, was = this.view;
    this.box = box;
    const v = focus ? (this.view = union(union(box, was), more)) : box, [x0, y0, x1, y1] = v, w = x1 - x0, hh = y1 - y0;
    const plain = !x0 && !y0 && x1 === W && y1 === H, pc = n => n * 100 + "%";
    this.svg.setAttribute("viewBox", `${x0} ${y0} ${w} ${hh}`);
    for (const el of [this.svg, this.paper]) Object.assign(el.style, plain ? { left: "", top: "", width: "", height: "" } : { left: pc(x0 / W), top: pc(y0 / H), width: pc(w / W), height: pc(hh / H) });
    if (this.mode === "msg") {
      const f = W / w;
      Object.assign(this.c.style, plain ? { transform: "", transformOrigin: "", marginBottom: "" } : { transform: `scale(${f}) translate(${pc(-x0 / W)}, ${pc(-y0 / H)})`, transformOrigin: "0 0", marginBottom: pc((hh * f - H) / W) });
    }
    if (!focus) return;
    const s = this.base / W, sc = this.sc, at = [sc.scrollLeft, sc.scrollTop];
    Object.assign(sc.firstChild.style, { width: w * s + "px", height: hh * s + "px" });
    Object.assign(this.el.style, { width: this.base + "px", left: -x0 * s + "px", top: -y0 * s + "px" });
    if (was && x0 < was[0]) sc.scrollLeft = at[0] + (was[0] - x0) * s;
    if (was && y0 < was[1]) sc.scrollTop = at[1] + (was[1] - y0) * s;
  }
  /** The Write page opens in scroller sc: the sheet at its width, a screen of paper on every side of it. */
  open(sc) {
    this.sc = sc; this.fit(); this.measure(); this.paint();
    const s = this.base / W, sw = Math.ceil(sc.clientWidth / s / G) * G, sh = Math.ceil(sc.clientHeight / s / G) * G;
    this.view = union(this.view, [-sw, -sh, W + sw, this.H + sh]); this.lay();
    sc.scrollLeft = -this.view[0] * s - (sc.clientWidth - this.base) / 2; sc.scrollTop = -this.view[1] * s;
    sc.addEventListener("scroll", () => this.roam(), { passive: true, signal: this.ac.signal });
    addEventListener("resize", () => this.turn(), { signal: this.ac.signal });
  }
  fit() { this.base = Math.max(280, Math.min(1000, this.sc.clientWidth - 24)); this.el.style.width = this.base + "px"; this.size = [this.sc.clientWidth, this.sc.clientHeight]; }
  /** A turned iPad or a resized window: what was in the middle of the view stays there. */
  turn() {
    const sc = this.sc, [w, h] = this.size, s = this.base / W, v = this.view;
    const x = v[0] + (sc.scrollLeft + w / 2) / s, y = v[1] + (sc.scrollTop + h / 2) / s;
    this.fit(); this.lay();
    const s2 = this.base / W; sc.scrollLeft = (x - v[0]) * s2 - this.size[0] / 2; sc.scrollTop = (y - v[1]) * s2 - this.size[1] / 2;
    this.roam();
  }
  /** Endless paper: whenever the view comes within half a screen of an edge, a screen more is laid there. */
  roam() {
    const sc = this.sc, v = this.view; if (!sc || !v || !sc.clientWidth) return;
    const s = this.base / W, cw = sc.clientWidth, ch = sc.clientHeight, sw = Math.ceil(cw / s / G) * G, sh = Math.ceil(ch / s / G) * G;
    const n = v.slice();
    if (sc.scrollLeft < cw / 2 && v[0] > -FAR) n[0] -= sw;
    if (sc.scrollTop < ch / 2 && v[1] > -FAR) n[1] -= sh;
    if (sc.scrollWidth - sc.scrollLeft - cw < cw / 2 && v[2] < FAR) n[2] += sw;
    if (sc.scrollHeight - sc.scrollTop - ch < ch / 2 && v[3] < FAR) n[3] += sh;
    if (n.every((x, i) => x === v[i])) return;
    this.lay(n);
  }
  paint() {
    const M = this.model(), th = this.th, sid = this.sid;
    this.title.replaceChildren(M.title);
    this.c.dataset.paper = M.info.paper || "ruled";
    // printed text and figure: rebuilt only when the offer changes
    const psig = M.printed + "\0" + JSON.stringify(M.figure);
    if (this.psig !== psig) {
      this.psig = psig; this.figDone = false;
      const at = M.sh && M.sh.offers.findLast(o => o.parsed.printed === M.printed), post = at && S.byId[at.post];
      this.p.replaceChildren(...(M.printed ? printedNodes(M.printed, post) : []));
    }
    const top = this.rows * LINE, own = th ? K.ownStrokes(th, sid) : [], pend = th && Object.hasOwn(K.pend(th).sheets, sid) ? K.pend(th).sheets[sid] : null;
    // height: the rows the problem takes, the writing lines asked for, and room for everything drawn
    let H = top + (M.info.h || 6 * LINE), low = 0;
    for (const st of own) for (const p of st.p) if (p[1] > low) low = p[1];
    for (const l of M.layers) if (l.h && l.h > H) H = l.h;
    if (pend && pend.h > H) H = pend.h;
    H = Math.max(H, Math.ceil((low + 40) / LINE) * LINE) + this.extra;
    if (this.mode === "focus") {   // the Write page: at least the screen's height of paper (from the scroller, so it can't feed on itself)
      const sc = this.c.closest(".ink-focus-s"), room = sc ? sc.clientHeight - this.head.offsetHeight - this.f.offsetHeight : 0;
      if (room > 0) H = Math.max(H, Math.floor((room * (this.k || 1)) / LINE) * LINE);
    }
    this.H = H;
    this.c.style.setProperty("--h", H); this.c.style.setProperty("--rows", this.rows);
    this.lay();
    if (!this.figDone && (!M.figure.some(m => m.anchor) || this.k)) {
      this.figDone = true; this.gPrint.replaceChildren();
      M.figure.forEach((m, i) => { const r = markG(m, sid + "p" + i, [], w => this.find(w), H, 0, "var(--ink-2)"); if (r) this.gPrint.append(r.g); });
    }
    // the person's ink
    const hid = th ? K.hidden(th, sid) : new Set();
    this.paintOwn(own);
    this.gOwn.toggleAttribute("data-hidden", hid.has("person"));
    // the agents' layers: each built once (its own snap boxes are the person's ink sent before it), then kept
    const keep = new Set(), sentBefore = [];
    const ownSent = new Map(); for (const l of M.layers) if (l.own) ownSent.set(l.post, l);
    const gone = new Set(); for (const l of M.layers) if (l.own) for (const e of l.erase) gone.add(e);
    let at = 0; const nIn = new Map();
    for (const l of M.layers) {
      if (l.own) { for (const st of l.strokes) if (!gone.has(st.id)) sentBefore.push(bbox(st.p)); continue; }
      if (l.cleared) continue;
      const nth = (nIn.get(l.post) || 0) + 1; nIn.set(l.post, nth);
      const key = l.post + ":" + nth, sig = sentBefore.length + ":" + (this.k ? 1 : 0) + ":" + this.rows + ":" + (this.z || 1) + ":" + (this.w || 0) + ":" + l.code.length;
      keep.add(key);
      let c = this.lg.get(key);
      if (!c || c.sig !== sig) {
        const g = s("g", { class: "ink-layer", "data-who": l.who, "data-post": l.post }); at = 0;
        const pen = K.agentPen(th, l.who), boxes = sentBefore.slice();
        l.marks.forEach((m, i) => { const r = markG(m, l.post + ":" + i, boxes, w => this.find(w), H, at, pen, this.z || 1); if (r) { g.append(r.g); at = r.at; } });
        if (c) c.g.replaceWith(g); else this.gLayers.append(g);
        const fresh = !c && this.mode !== "snap" && K.shouldPlay(l.post);
        c = { g, sig, post: l.post, pend: fresh || (c && c.pend) };
        this.lg.set(key, c);
      }
      c.g.toggleAttribute("data-hidden", hid.has(l.who));
    }
    for (const [k, c] of this.lg) if (!keep.has(k)) { c.g.remove(); this.lg.delete(k); }
    if (this.seen) this.play();
    // header: who has written here (only when more than one hand has)
    const authors = [];
    if (own.length) authors.push(["person", "var(--pen-ink)", "You"]);
    for (const l of M.layers) if (!l.own && !l.cleared && !authors.some(a => a[0] === l.who)) authors.push([l.who, K.agentPen(th, l.who), capFirst(agentLabel(l.who) || l.who)]);
    this.who.replaceChildren(...(authors.length > 1 ? authors.map(([w, c, n]) => h("button", { type: "button", style: `--c:${c}`, "aria-pressed": String(!hid.has(w)), title: hid.has(w) ? `Show ${n}'s ink` : `Hide ${n}'s ink`, onclick: () => K.toggleHidden(th, sid, w) }, n)) : []));
    // state and footer
    const n = pend ? pend.strokes.length : 0, er = pend ? pend.erase.length : 0;
    this.el.toggleAttribute("data-empty", !own.length && !M.layers.some(l => !l.own && !l.cleared && l.marks.length) && !er);
    this.el.toggleAttribute("data-pending", !!(n || er));
    if (n || er) {
      const what = [n ? plural(n, "stroke") : null, er ? plural(er, "erased stroke") : null].filter(Boolean).join(", ");
      this.f.replaceChildren(h("span", {}, `${what}, not sent yet`),
        button("Undo", () => K.undo(th), { kind: "quiet", size: "s", icon: "undo" }),
        button("Send work", () => dispatchEvent(new CustomEvent("ink:send", { detail: { th, sids: [sid] } })), { kind: "primary", size: "s" }));
    } else this.f.replaceChildren();
    if (this.send) this.send.disabled = !(n || er);
  }
  /** The person's strokes, one path each, kept from paint to paint: only what changed touches the page. */
  paintOwn(own) {
    const had = this.own || new Map(), now = this.own = new Map(), g = this.gOwn;
    let at = g.firstChild;
    for (const st of own) {
      let el = had.get(st.p);
      if (!el || now.has(st.p)) el = strokeP(st, st.pending); else el.toggleAttribute("data-pending", !!st.pending);
      now.set(st.p, el);
      if (el === at) at = at.nextSibling; else g.insertBefore(el, at);
    }
    while (at) { const next = at.nextSibling; at.remove(); at = next; }
  }
  /** Start the write-on of layers that are fresh, once the sheet is on screen. */
  play() {
    for (const c of this.lg.values()) if (c.pend) {
      c.pend = false; K.markPlayed(c.post);
      for (const g of c.g.children) g.setAttribute("data-new", "");
    }
  }
  /** Play a layer again (its card was clicked). */
  replay(post) {
    for (const c of this.lg.values()) if (c.post === post) for (const g of c.g.children) { g.removeAttribute("data-new"); void g.getBoundingClientRect(); g.setAttribute("data-new", ""); }
  }

  // ---------- input ----------
  pt(e, r = this.c.getBoundingClientRect()) {
    const k = W / r.width; let x = (e.clientX - r.left) * k, y = (e.clientY - r.top) * k;
    // in a message a stroke stops at the sheet's edges (more paper is on the Write page)
    if (this.mode !== "focus" && this.box) { x = Math.max(this.box[0], Math.min(this.box[2], x)); y = Math.max(this.box[1], y); }
    return [x, y, e.pointerType === "pen" ? (e.pressure || .5) : .5];
  }
  down(e) {
    if (!this.th) return;
    K.handMoved();
    // The Pencil always writes: a stroke whose lift never arrived is finished here, and a palm that started one is dropped. Waiting
    // for that lift left the sheet deaf to the pen.
    if (this.cur && e.pointerType === "pen") { if (this.curType === "pen") this.up({ pointerId: this.pid, pointerType: "pen", clientX: -1e4, clientY: -1e4 }); else this.cancel(); }
    if (e.isPrimary) this.tap = false;
    // where a press began, to tell a tap from a stroke; a palm resting while (or just after) the Pencil writes is not a tap
    if (this.mode === "msg") this.press = e.isPrimary && !this.cur && !(e.pointerType === "touch" && Date.now() - (this.penAt || 0) < 1500) ? { id: e.pointerId, x: e.clientX, y: e.clientY, far: 0 } : null;
    if (this.cur && (e.pointerType !== "touch" || this.curType !== "touch")) return;   // a palm or a second pointer while writing
    if (e.pointerType === "touch") {
      if (this.mode !== "focus") return;   // a finger scrolls, except on the Write page
      (this.touches ||= new Map()).set(e.pointerId, [e.clientX, e.clientY]);
      if (K.TOOL.pencil || this.touches.size > 1) { this.cancel(); this.pan = true; return; }   // two fingers (or one, once a Pencil writes) move the page
    } else if (e.pointerType === "mouse" && e.button !== 0) return;
    if (e.target.closest && e.target.closest("a, button, .rc-im, input")) return;
    if (e.pointerType === "pen" && !K.TOOL.pencil) K.setTool("pencil", true);
    e.preventDefault();
    const at = this.pt(e), t = K.TOOL.t;
    this.c.setPointerCapture(e.pointerId);
    this.pid = e.pointerId; this.curType = e.pointerType;
    this.el.setAttribute("data-writing", "");
    const bx = this.el.closest(".cm-msgs"); if (bx) bx.dispatchEvent(new Event("rc-hand"));   // writing up the chat lets go of its end (goSheet letGo)
    if (t === "eraser") { this.cur = { erase: true }; this.erase(at); return; }
    this.cur = { id: K.sid(), t: t === "hl" ? "hl" : "pen", c: K.TOOL.c, w: t === "hl" ? 16 : 2.8, p: [at], lasso: t === "lasso" };
    this.livePath = s("path", { class: "ink-s", "data-tool": this.cur.t === "hl" ? "hl" : null, style: this.cur.lasso ? "color:var(--accent);opacity:.7" : `color:${K.PENS[this.cur.c] || K.PENS.ink}` });
    this.gLive.replaceChildren(this.livePath);
    this.hold(at);
  }
  move(e) {
    if (this.pan && this.touches && this.touches.has(e.pointerId)) {
      if (this.curType === "pen" && (this.cur || Date.now() - (this.penAt || 0) < 600)) { this.touches.set(e.pointerId, [e.clientX, e.clientY]); return; }   // a palm moving as the Pencil writes must not slide the paper
      const was = this.touches.get(e.pointerId), sc = this.c.closest(".ink-focus-s");
      if (sc) { sc.scrollTop -= (e.clientY - was[1]) / this.touches.size; sc.scrollLeft -= (e.clientX - was[0]) / this.touches.size; }
      this.touches.set(e.pointerId, [e.clientX, e.clientY]); return;
    }
    this.moved(e);
    if (!this.cur || e.pointerId !== this.pid) return;
    K.handMoved();
    const evs = e.getCoalescedEvents ? e.getCoalescedEvents() : [e], r = this.c.getBoundingClientRect();
    for (const ev of evs.length ? evs : [e]) {
      const p = this.pt(ev, r);
      if (this.cur.erase) { this.erase(p); continue; }
      const q = this.cur.p.at(-1); if (Math.hypot(p[0] - q[0], p[1] - q[1]) < .8) continue;
      this.cur.p.push(p);
      if (Math.hypot(p[0] - this.holdAt[0], p[1] - this.holdAt[1]) > 3) this.hold(p);
    }
    if (this.cur.erase) return;
    // the line under the pen is drawn once a frame, however many points arrive in it
    this.liveRaf ||= requestAnimationFrame(() => { this.liveRaf = 0; if (this.cur && !this.cur.erase) this.livePath.setAttribute("d", outline(this.cur)); });
    const y = this.cur.p.at(-1)[1];
    if (this.mode !== "focus" && y > this.H - LINE * 1.2) { this.extra += 2 * LINE; this.paint(); }   // writing nears the bottom: the paper grows two lines
  }
  moved(e) { const q = this.press; if (q && e.pointerId === q.id) q.far = Math.max(q.far, Math.hypot(e.clientX - q.x, e.clientY - q.y)); }
  hold(p) {
    this.holdAt = p; clearTimeout(this.holdT);
    this.holdT = setTimeout(() => {   // held still: the stroke becomes the shape it meant
      if (!this.cur || this.cur.erase || this.cur.t === "hl" || this.cur.lasso) return;
      const sn = snapShape(this.cur.p); if (!sn) return;
      this.cur.p = sn.p; this.cur.snap = sn.k; this.livePath.setAttribute("d", outline(this.cur));
    }, 500);
  }
  erase(p) {
    for (const st of K.ownStrokes(this.th, this.sid)) if (nearStroke(st, p[0], p[1], 8)) K.eraseStroke(this.th, this.sid, st.id);
  }
  cancel() { clearTimeout(this.holdT); this.cur = null; this.gLive.replaceChildren(); this.el.removeAttribute("data-writing"); }
  up(e, cancelled) {
    this.moved(e);
    K.handMoved();
    if (e.pointerType === "pen") this.penAt = Date.now();
    // A tap: a finger or a click that stayed put, or the Pencil on a sheet you haven't written on (with ink there, its tap is a dot). Not with the pen or eraser on.
    const q = this.press, mine = q && q.id === e.pointerId;
    if (mine) this.press = null;
    const tap = !!mine && !cancelled && q.far < (e.pointerType === "mouse" ? 4 : 10) && (e.pointerType === "touch" || (!K.TOOL.pen && K.TOOL.t !== "eraser" && (e.pointerType !== "pen" || !K.ownStrokes(this.th, this.sid).length)));
    if (tap) this.tap = true;
    if (this.touches) { this.touches.delete(e.pointerId); if (!this.touches.size) this.pan = false; }
    if (!this.cur || e.pointerId !== this.pid) return;
    clearTimeout(this.holdT);
    const cur = this.cur; this.cur = null; this.el.removeAttribute("data-writing");
    if (cur.erase) return;
    if (tap) { this.gLive.replaceChildren(); return; }   // it opens the page, so it leaves no dot
    if (cancelled && cur.p.length < 3) { this.gLive.replaceChildren(); return; }
    if (cur.lasso) {
      const b = isLoop(cur.p, 10) || bbox(cur.p), r = this.c.getBoundingClientRect(), k = r.width / W;
      const R = new DOMRect(r.left + b.x * k, r.top + b.y * k, b.w * k, b.h * k);
      const st = { id: cur.id, t: "pen", c: "ink", w: 2, p: cur.p };
      this.gLive.replaceChildren();
      import("./over.js").then(m => m.bubble({ th: this.th, sid: this.sid, R, sheet: this, stroke: st }));
      return;
    }
    const st = { id: cur.id, t: cur.t, c: cur.c, w: cur.w, p: cur.p.map(p => [Math.round(p[0] * 10) / 10, Math.round(p[1] * 10) / 10, Math.round(p[2] * 100) / 100]) };
    if (cur.snap) st.snap = cur.snap;
    this.gLive.replaceChildren();
    K.addStroke(this.th, this.sid, st, { title: this.model().title, h: this.H });
  }
  /** Flash the sheet (its card was clicked). */
  flash() { this.el.removeAttribute("data-flash"); void this.el.offsetWidth; this.el.setAttribute("data-flash", ""); setTimeout(() => this.el.removeAttribute("data-flash"), 1600); }
}

/** The live sheet of chat th and id sid, if one is on the page. */
export const liveSheet = (th, sid) => { const v = LIVE.get(th + "\0" + sid); return v && v.el.isConnected ? v.sheet : null; };
/** Repaint chat th's sheets (a post that touches them arrived). */
export function repaintSheets(th) { for (const [k, v] of LIVE) if (k.startsWith(th + "\0") && v.el.isConnected) v.sheet.paint(); }

function liveEl(th, sid, offer) {
  const k = th + "\0" + sid, had = LIVE.get(k);
  if (had) {
    if (!had.el.isConnected && had.ghost && had.ghost.isConnected) had.ghost.replaceWith(had.el);
    if (!had.el.isConnected) { had.sheet.offer = offer; had.sheet.paint(); return had.el; }
    const c = had.el.cloneNode(false); c._real = had.el; had.ghost = c; return c;
  }
  for (const [kk, v] of LIVE) if (LIVE.size > 24 && !v.el.isConnected) { v.sheet.ac.abort(); LIVE.delete(kk); }
  const sheet = new Sheet(th, sid, { offer });
  LIVE.set(k, { el: sheet.el, sheet });
  return sheet.el;
}

// ---------- going to a sheet ----------
const SEEN = new Set();
/** A jump up the chat is the reader's own move: once it lands, tell the chat (comms.js "rc-hand") so it stops holding the end,
 *  or the composer's ink chip appearing after the next stroke would pin the chat back to the bottom. */
function letGo() {
  const box = document.querySelector("#main .cm-msgs"); if (!box) return;
  let done = false; const go = () => { if (done) return; done = true; box.dispatchEvent(new Event("rc-hand")); };
  box.addEventListener("scrollend", go, { once: true }); setTimeout(go, 1200);
}
/** Scroll the chat so the ink on message art is in view: post's agent layer, else the person's own strokes, else any ink.
 *  A long message with ink low down (under its pictures) would otherwise be centred on its middle, far from the drawing. */
function toInk(art, post) {
  const box = document.querySelector("#main .cm-msgs"), svg = art.querySelector("svg.ink-mark[data-sid]"); if (!box || !svg) return false;
  const seen = el => { const r = el && el.getBoundingClientRect(); return r && r.width + r.height > 2 ? r : null; };
  const r = seen(post && svg.querySelector(`g.ink-layer[data-post="${CSS.escape(post)}"]`)) || seen(svg.querySelector("g.ink-own")) || seen(svg.querySelector("g.ink-layers")); if (!r) return false;
  const b = box.getBoundingClientRect(), mid = r.height > b.height * .8 ? r.top - b.top - 48 : r.top + r.height / 2 - (b.top + b.height / 2);
  box.scrollBy({ top: mid, behavior: "smooth" }); return true;
}
/** Scroll to sheet sid in chat th, flash it, and replay post's layer on it. A message ref scrolls to that message. */
export function goSheet(th, sid, post) {
  if (/^m-/.test(sid)) {
    const art = document.querySelector(`#main article.msg[data-key="${CSS.escape(sid.slice(2))}"]`);
    if (art) { K.setInkOff(false); toInk(art, post) || art.scrollIntoView({ block: "center", behavior: "smooth" }); letGo(); art.classList.remove("msg-flash"); void art.offsetWidth; art.classList.add("msg-flash"); }
    return;
  }
  const sh = liveSheet(th, sid); if (!sh) return;
  sh.el.scrollIntoView({ block: "center", behavior: "smooth" }); letGo(); sh.flash();
  if (post) setTimeout(() => sh.replay(post), 350);
}

// ---------- the fence ----------
/** ```ink: an offer draws the sheet; on= draws a card in the agent's post that jumps to the sheet. */
export function inkFigure(code, { title, info } = {}) {
  const r = parseInk(info || "", code), ctx = chatCtx();
  if (!ctx || !ctx.th) return staticSheet(r);
  const th = ctx.th, post = S.byId[ctx.id];
  if (!post || K.isPerson(post.from)) return staticSheet(r);
  if (r.info.on) return markCard(th, post, r);
  const sid = K.offerId(post, info || "", code), sh = K.fold(th).sheets.get(sid);
  if (sh && sh.home && sh.home !== post.id) return homeCard(th, sid, sh);
  return liveEl(th, sid, r);
}
function staticSheet(r) {
  const sheet = new Sheet(null, r.info.id || "x", { mode: "static", offer: r });
  return sheet.el;
}
function markCard(th, post, r) {
  const sid = r.info.on, sh = K.fold(th).sheets.get(sid);
  if (!SEEN.has(post.id)) { SEEN.add(post.id); queueMicrotask(() => { const v = liveSheet(th, sid); if (v) v.paint(); }); }
  const n = r.marks.filter(m => m.k !== "clear").length, k = r.skipped.length;
  const kinds = {}; for (const m of r.marks) if (m.k !== "clear") kinds[m.k === "text" ? "note" : m.k] = (kinds[m.k === "text" ? "note" : m.k] || 0) + 1;
  const what = Object.entries(kinds).slice(0, 4).map(([w, c]) => c > 1 ? `${c} ${w}s` : w).join(", ");
  const missing = !/^m-|^board$/.test(sid) && !(sh && sh.offers.length);
  const name = sheetTitle(sh, sid);
  const meta = missing ? `No sheet "${sid}" in this chat` : [plural(n, "mark"), what, k ? `${plural(k, "line")} skipped` : null].filter(Boolean).join(" · ");
  return h("a", { class: "ink-card", href: "#", "data-err": missing || k ? "" : null, style: `--ag:${K.agentPen(th, post.from)}`,
    title: k ? "Skipped: " + r.skipped.map(x => x.raw.trim()).join(" | ") : `Go to ${name}`,
    onclick: e => { e.preventDefault(); goSheet(th, sid, post.id); } },
    thumb(th, sid, { marks: r.marks, who: post.from }),
    h("span", {}, h("b", {}, r.marks.some(m => m.k === "clear") && !n ? `Cleared its marks on ${name}` : `Marked up ${name}`), h("span", { class: "meta" }, meta)));
}
function homeCard(th, sid, sh) {
  return h("a", { class: "ink-card", href: "#", "data-home": "", onclick: e => { e.preventDefault(); goSheet(th, sid); } },
    thumb(th, sid), h("span", {}, h("b", {}, sheetTitle(sh, sid)), h("span", { class: "meta" }, "Offered again further down: write there")));
}

// ---------- the Write page (a phone, or anyone who wants a finger to draw) ----------
let FOCUS = null;
export function openFocus(th, sid) {
  closeFocus();
  const sheet = new Sheet(th, sid, { mode: "focus" });
  const done = () => closeFocus();
  const bar = h("div", { class: "ink-focus-tb", role: "toolbar", "aria-label": "Pen" });
  const sc = h("div", { class: "ink-focus-s" }, h("div", { class: "ink-pad" }, sheet.el));
  const back = document.activeElement;
  const page = h("div", { class: "ink-focus", role: "dialog", "aria-modal": "true", tabindex: "-1", "data-th": th, "aria-label": "Write on " + sheet.model().title },
    h("div", { class: "ink-focus-h" }, h("b", {}, sheet.model().title), button("Done", done, { kind: "primary", size: "s" })),
    sc, bar);
  const send = sheet.send = button("Send", () => { closeFocus(); dispatchEvent(new CustomEvent("ink:send", { detail: { th, sids: [sid] } })); }, { kind: "primary", size: "s", cls: "ink-send", title: "Send what you wrote" });
  send.disabled = !(K.inkPending(th)?.sheets || []).includes(sid);
  const place = movable(bar, page, sheet.ac.signal);
  import("./toolbar.js").then(m => { bar.replaceChildren(place.grip, ...m.toolRow(th, { dock: "focus" }), h("hr"), send); place(); }).catch(() => {});
  const key = e => { if (e.key === "Escape" && !e.defaultPrevented && !document.querySelector(".ink-bubble")) { e.preventDefault(); done(); } };
  addEventListener("keydown", key, { signal: sheet.ac.signal });
  document.body.append(page);
  FOCUS = { page, sheet, back };
  document.documentElement.setAttribute("data-ink-focus", "");
  sheet.open(sc);
  page.focus({ preventScroll: true });
}
/** The Write page's toolbar floats and goes where it is dragged (by its grip or any bare part of it; arrows on the grip), and
 *  stays there next time. The place is kept as a share of the free room, so it survives a turned iPad. */
const TB = "dash.ink.tb";
function movable(bar, page, signal) {
  const grip = h("button", { type: "button", class: "ink-grip", "aria-label": "Move the toolbar", title: "Drag to move the toolbar" });
  let at = null, from = null;
  try { const v = JSON.parse(localStorage.getItem(TB)); if (Array.isArray(v) && v.every(Number.isFinite)) at = v; } catch (e) {}
  const head = () => page.firstElementChild.offsetHeight;   // never over the title and Done
  const room = () => [Math.max(1, page.clientWidth - bar.offsetWidth), Math.max(1, page.clientHeight - head() - bar.offsetHeight)];
  const place = () => {
    if (!at) return;
    const [w, hh] = room(); Object.assign(bar.style, { left: at[0] * w + "px", top: head() + at[1] * hh + "px", bottom: "auto", translate: "none" });
  };
  const go = (x, y) => { const [w, hh] = room(), c = n => Math.max(0, Math.min(1, n)); at = [c(x / w), c((y - head()) / hh)]; place(); };
  const keep = () => { try { localStorage.setItem(TB, JSON.stringify(at)); } catch (e) {} };
  bar.addEventListener("pointerdown", e => {
    if (e.target !== bar && e.target !== grip) return;
    e.preventDefault(); const r = bar.getBoundingClientRect(); from = [e.pointerId, e.clientX - r.left, e.clientY - r.top];
    bar.setPointerCapture(e.pointerId);
  }, { signal });
  bar.addEventListener("pointermove", e => { if (from && e.pointerId === from[0]) { const p = page.getBoundingClientRect(); go(e.clientX - p.left - from[1], e.clientY - p.top - from[2]); } }, { signal });
  for (const t of ["pointerup", "pointercancel"]) bar.addEventListener(t, e => { if (from && e.pointerId === from[0]) { from = null; if (at) keep(); } }, { signal });
  grip.addEventListener("keydown", e => {
    const d = { ArrowLeft: [-24, 0], ArrowRight: [24, 0], ArrowUp: [0, -24], ArrowDown: [0, 24] }[e.key]; if (!d) return;
    e.preventDefault(); go(bar.offsetLeft - (at ? 0 : bar.offsetWidth / 2) + d[0], bar.offsetTop + d[1]); keep();
  }, { signal });
  addEventListener("resize", place, { signal });
  place.grip = grip; return place;
}
export function closeFocus() {
  if (!FOCUS) return;
  const back = FOCUS.back, FOCUS_TH = FOCUS.sheet.th; FOCUS.sheet.ac.abort(); FOCUS.page.remove(); FOCUS = null;
  if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
  document.documentElement.removeAttribute("data-ink-focus");
  dispatchEvent(new CustomEvent("ink:change", { detail: { th: FOCUS_TH, pendOnly: true } }));   // what waited under the page draws now
}
