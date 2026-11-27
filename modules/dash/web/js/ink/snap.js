// The picture the agent gets of the person's ink (thread 112515205dbf; design ~/designs/ink-chat/HANDOFF.md §5).
// For each sheet the send touches: ink-<sid>.json (the strokes, for the page to redraw), ink-<sid>.png (what the agent looks at:
// the paper or the message under the ink, rulers in sheet units, and a footer that teaches the ```ink reply), and one line of
// plain text for the post. The page paints the picture itself (no library): boxes and words from the DOM, SVG as Path2D.
import { S } from "../core/store.js";
import { agentLabel, capFirst } from "../core/model.js";
import { wordsIn } from "../ui/sidechat.js";
import { W, LINE } from "./grammar.js";
import * as K from "./store.js";
import { Sheet, inkLayerSvg, liveSheet, sheetTitle, wordsRect } from "./sheet.js";
import { bbox, isLoop } from "./draw.js";

const RL = 36, TT = 28, RT = 20, FT = 40, RP = 12, TOP = TT + RT;   // the frame round the content, in units
const plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;

/** Everything pending in chat th (or only sids): {files: [File], lines: [string], packed: {sid: {strokes: [id], erase: [id]}}}.
 *  Each sheet is copied before its snapshot is awaited, so ink drawn meanwhile stays pending for the next message. */
export async function inkPack(th, sids) {
  const v = K.pend(th), files = [], lines = [], packed = {};
  for (const sid of sids || Object.keys(v.sheets)) {
    const q = Object.hasOwn(v.sheets, sid) && v.sheets[sid]; if (!q || (!q.strokes.length && !q.erase.length)) continue;
    const p = { ...q, strokes: q.strokes.slice(), erase: q.erase.slice() };
    packed[sid] = { strokes: p.strokes.map(x => x.id), erase: p.erase.slice() };
    let shot = null;
    try { shot = /^m-/.test(sid) ? await msgShot(th, sid, p) : sid === "board" ? await boardShot(th, sid, p) : await sheetShot(th, sid, p); }
    catch (e) { console.warn("ink snapshot", sid, e); }
    const json = { v: 1, sheet: sid, title: shot ? shot.title : p.title || null, h: shot ? shot.h : p.h || null, strokes: p.strokes.map(({ at, ...x }) => x), erase: p.erase };   // at: over.js, this page only
    files.push(new File([JSON.stringify(json)], `ink-${sid}.json`, { type: "application/json" }));
    if (shot && shot.png) files.push(new File([shot.png], `ink-${sid}.png`, { type: "image/png" }));
    lines.push(shot ? shot.line : `✎ Drew on ${sid} (${plural(p.strokes.length, "stroke")}).`);
  }
  return { files, lines, packed };
}

// ---------- the three kinds ----------
async function sheetShot(th, sid, p) {
  const live = liveSheet(th, sid), lw = live && live.c.clientWidth;
  const host = document.createElement("div");
  host.setAttribute("data-theme", "light");
  host.style.cssText = `color-scheme:light;position:fixed;left:-10000px;top:0;width:${lw && lw <= 520 ? lw : W}px;pointer-events:none`;
  document.body.append(host);
  const sh = new Sheet(th, sid, { mode: "snap", offer: live ? live.offer : null });
  try {
    host.append(sh.el); sh.measure(); sh.paint();
    for (const e of sh.el.querySelectorAll("[data-pending]")) e.removeAttribute("data-pending");
    await fontsIn();
    const M = sh.model(), H = sh.H, r = sh.c.getBoundingClientRect(), k = W / r.width;
    const png = await render({ y0: 0, y1: H, title: `${M.title} · sheet ${sid}`, on: sid, paint: (ctx, base) => {
      paper(ctx, base, M.info.paper || "ruled", H, host);
      const T = base.multiply(new DOMMatrix([k, 0, 0, k, -r.left * k, -r.top * k]));
      paintHtml(ctx, sh.p, T, base);
      paintSvg(ctx, sh.svg, T);
    } });
    const n = p.strokes.length, er = p.erase.length;
    const what = [n ? plural(n, "stroke") : null, er ? plural(er, "erased stroke") : null].filter(Boolean).join(", ");
    return { png, h: H, title: M.title, line: `✎ Wrote on sheet ${sid} "${M.title}" (${what}).` };
  } finally { sh.ac.abort(); host.remove(); }
}

async function msgShot(th, sid, p) {
  const id = sid.slice(2), art = document.querySelector(`#main article.msg[data-key="${CSS.escape(id)}"]`), post = S.byId[id], fr = art && frameOf(art);
  const who = post && !K.isPerson(post.from) ? capFirst(agentLabel(post.from) || post.from) + "'s" : "your";
  const verb = kind(p.strokes);
  if (!art) return { png: null, h: null, title: "Ink on a message", line: `✎ ${verb} ${who} message ${id} (${plural(p.strokes.length, "stroke")}).` };
  const ar = fr.getBoundingClientRect(), k = W / ar.width, H = Math.round(ar.height * k);
  const b = p.strokes.length ? bbox(p.strokes.flatMap(st => st.p)) : { x: 0, y: 0, w: W, h: Math.min(H, 400) };
  const y0 = Math.max(0, Math.floor(b.y - 80)), y1 = Math.min(Math.max(H, b.y + b.h + 10), Math.ceil(b.y + b.h + 80));
  // ink past the body's sides (a note in the margin, a long arrow) widens the picture rather than being cut off
  const x0 = Math.min(0, Math.floor(b.x - 20)), x1 = Math.max(W, Math.ceil(b.x + b.w + 20));
  const out = bgOf(art.parentElement || art);
  // the quote: the words the ink encloses (an underline: the line of words just above it)
  const up = verb === "Underlined" ? 34 : 4;
  const R = { left: ar.left + (b.x - 4) / k, right: ar.left + (b.x + b.w + 4) / k, top: ar.top + (b.y - up) / k, bottom: ar.top + (b.y + b.h + 4) / k };
  const quote = (wordsIn(art, R).text || "").replace(/\s+/g, " ").trim().slice(0, 300);
  const fill = bgOf(fr);
  await fontsIn();
  const wrap = document.createElement("div");
  wrap.style.cssText = `position:fixed;left:0;top:0;width:${W}px;height:${H}px;opacity:0;pointer-events:none`;
  const svg = inkLayerSvg(th, sid, H, { find: w => findIn(art, w, k), z: zoomOf(fr) });
  wrap.append(svg); document.body.append(wrap);
  try {
    const png = await render({ x0, x1, y0, y1, title: `${who === "your" ? "Your" : who} message ${id} · ink ${sid}`, on: id, paint: (ctx, base) => {
      ctx.save(); ctx.setTransform(base);
      if (x0 < 0 || x1 > W) { ctx.fillStyle = out; ctx.fillRect(x0, y0, x1 - x0, y1 - y0); }
      ctx.fillStyle = fill; ctx.fillRect(0, y0, W, y1 - y0); ctx.restore();
      paintHtml(ctx, art, base.multiply(new DOMMatrix([k, 0, 0, k, -ar.left * k, -ar.top * k])), base, { skip: ".ink-over,.ink-mark,.sc-layer,.ink-bubble,.katex-mathml,script,style" });
      const wr = svg.getBoundingClientRect();
      paintSvg(ctx, svg, base.multiply(new DOMMatrix([W / wr.width, 0, 0, W / wr.width, -wr.left * W / wr.width, -wr.top * W / wr.width])));
    } });
    return { png, h: H, title: "Ink on a message", line: `✎ ${verb} ${who} message ${id} (ink ${sid})` + (quote ? `: "${quote}"` : ` (${plural(p.strokes.length, "stroke")}).`) };
  } finally { wrap.remove(); }
}

async function boardShot(th, sid, p) {
  const own = K.ownStrokes(th, sid), all = own.flatMap(st => st.p);
  const b = all.length ? bbox(all) : { x: 0, y: 0, w: W, h: 400 };
  const y0 = Math.max(0, Math.floor(b.y - 40)), y1 = Math.ceil(b.y + b.h + 40), H = y1 + 10;
  const wrap = document.createElement("div");
  wrap.setAttribute("data-theme", "light");
  wrap.style.cssText = `color-scheme:light;position:fixed;left:0;top:0;width:${W}px;height:${H}px;opacity:0;pointer-events:none`;
  const svg = inkLayerSvg(th, sid, H); wrap.append(svg); document.body.append(wrap);
  try {
    const png = await render({ y0, y1, title: `Whiteboard · ink board`, on: "wb", paint: (ctx, base) => {
      paper(ctx, base, "dot", y1, wrap, y0);
      const wr = svg.getBoundingClientRect(), k = W / wr.width;
      paintSvg(ctx, svg, base.multiply(new DOMMatrix([k, 0, 0, k, -wr.left * k, -wr.top * k])));
    } });
    return { png, h: H, title: "Whiteboard", line: `✎ Drew on the whiteboard (${plural(p.strokes.length, "stroke")}).` };
  } finally { wrap.remove(); }
}

/** What the strokes are: all loops "Circled", all flat lines "Underlined", else "Drew in". */
function kind(strokes) {
  if (!strokes.length) return "Erased ink in";
  if (strokes.every(st => isLoop(st.p, 10))) return "Circled in";
  if (strokes.every(st => { const b = bbox(st.p); return b.w > 24 && b.h < Math.max(14, b.w * .15); })) return "Underlined in";
  return "Drew in";
}
/** The box message ink is measured in: the message's body (a person's bubble, an agent's text under its name), 800 units wide.
 *  Not the whole article: a bubble sits right-aligned at a width of its own, so the article's frame would drift between a phone
 *  and a desktop. Ink beyond the body still draws (the layer doesn't clip it); the picture sent to agents crops to the body. */
export const frameOf = art => art.querySelector(":scope > .msg-c > .msg-b") || art;
/** How much bigger to draw pen widths and notes on a frame that narrow (a bubble 290 px wide makes a unit 0.36 px): about the
 *  size they have on a sheet. inkLayerSvg places notes at that size and sets --z for CSS .ink-z; the person's own strokes take it into their width. */
export const zoomOf = el => { const w = el.getBoundingClientRect().width; return w ? Math.round(Math.max(1, Math.min(3, .85 * W / w)) * 100) / 100 : 1; };
/** @"words" over a message: their box in that message's units. */
function findIn(art, words, k) {
  const R = wordsRect(art, words, ".ink-over,.sc-layer,svg,.katex-mathml,button"); if (!R) return null;
  const A = frameOf(art).getBoundingClientRect();
  return { x: (R.left - A.left) * k, y: (R.top - A.top) * k, w: R.width * k, h: R.height * k };
}
export { findIn };
const fontsIn = () => (document.fonts && document.fonts.ready ? Promise.race([document.fonts.ready, new Promise(r => setTimeout(r, 400))]) : Promise.resolve());
function bgOf(el) {
  for (let e = el; e; e = e.parentElement) { const c = getComputedStyle(e).backgroundColor; if (c && !/^(transparent|rgba\(0, 0, 0, 0\))$/.test(c)) return c; }
  return "#fff";
}

// ---------- the frame: title, rulers, footer ----------
/** Draw content x0..x1, y0..y1 (units) into a framed PNG. paint(ctx, base): base maps content units to canvas px.
 *  x0..x1 is 0..800 unless ink runs past the sides of what it was drawn on. */
async function render({ x0 = 0, x1 = W, y0, y1, title, on, paint }) {
  const cw = x1 - x0, CW = RL + cw + RP, ch = Math.max(LINE, y1 - y0), total = TOP + ch + FT, S = Math.min(1.5, 2400 / total, 2400 / CW);
  const cv = document.createElement("canvas"); cv.width = Math.ceil(CW * S); cv.height = Math.ceil(total * S);
  const draw = safe => {
    const ctx = cv.getContext("2d"); ctx.setTransform(1, 0, 0, 1, 0, 0); ctx.clearRect(0, 0, cv.width, cv.height);
    ctx.fillStyle = "#fff"; ctx.fillRect(0, 0, cv.width, cv.height);
    const base = new DOMMatrix([S, 0, 0, S, S * RL - S * x0, S * TOP - S * y0]);
    ctx.save(); ctx.beginPath(); ctx.rect(S * RL, S * TOP, S * cw, S * ch); ctx.clip();
    try { paint(ctx, base, safe); } catch (e) { console.warn("ink paint", e); }
    ctx.restore();
    frame(ctx, S, x0, x1, y0, ch, title, on);
    return ctx;
  };
  draw(false);
  try { return await blob(cv); }
  catch (e) { SAFE = true; try { draw(true); return await blob(cv); } finally { SAFE = false; } }
}
let SAFE = false;   // a picture from elsewhere tainted the canvas: draw images as labelled boxes instead
const blob = cv => new Promise((res, rej) => { try { cv.toBlob(b => b ? res(b) : rej(new Error("toBlob")), "image/png"); } catch (e) { rej(e); } });

function frame(ctx, S, x0, x1, y0, ch, title, on) {
  ctx.save(); ctx.setTransform(S, 0, 0, S, 0, 0);
  const grey = "#8a8f98", faint = "#c9cdd3", cw = x1 - x0, CW = RL + cw + RP, wide = x0 < 0 || x1 > W;
  if (wide) {   // where the message itself is: dashed lines at 0 and 800
    ctx.save(); ctx.strokeStyle = "#9aa3b0"; ctx.lineWidth = 1 / S; ctx.setLineDash([4, 4]); ctx.beginPath();
    for (const x of [0, W]) { ctx.moveTo(RL + x - x0, TOP); ctx.lineTo(RL + x - x0, TOP + ch); }
    ctx.stroke(); ctx.restore();
  }
  ctx.fillStyle = "#f6f7f9"; ctx.fillRect(0, 0, CW, TOP); ctx.fillRect(0, TOP, RL, ch); ctx.fillRect(0, TOP + ch, CW, FT);
  ctx.fillStyle = "#1d2025"; ctx.font = "600 14px system-ui, sans-serif"; ctx.textBaseline = "middle";
  ctx.fillText(title.length > 90 ? title.slice(0, 89) + "…" : title, RL, TT / 2 + 1);
  ctx.strokeStyle = faint; ctx.lineWidth = 1 / S;
  ctx.beginPath(); ctx.moveTo(RL, TOP); ctx.lineTo(RL + cw, TOP); ctx.moveTo(RL, TOP); ctx.lineTo(RL, TOP + ch); ctx.stroke();
  ctx.fillStyle = grey; ctx.strokeStyle = grey; ctx.font = "10px ui-monospace, monospace";
  // top: x every 50, a number every 100
  ctx.textAlign = "center"; ctx.textBaseline = "alphabetic"; ctx.beginPath();
  for (let x = Math.ceil(x0 / 50) * 50; x <= x1; x += 50) { const big = x % 100 === 0, xx = RL + x - x0; ctx.moveTo(xx, TOP); ctx.lineTo(xx, TOP - (big ? 7 : 4)); if (big && x < x1 - 20) ctx.fillText(String(x), xx + (xx - RL < 12 ? 6 : 0), TOP - 8); }
  // left: y every 50 at its real offset, a number every 100
  ctx.textAlign = "right"; ctx.textBaseline = "middle";
  for (let y = Math.ceil(y0 / 50) * 50; y <= y0 + ch; y += 50) { const yy = TOP + y - y0, big = y % 100 === 0; ctx.moveTo(RL, yy); ctx.lineTo(RL - (big ? 7 : 4), yy); if (big) ctx.fillText(String(y), RL - 9, yy + (yy - TOP < 6 ? 5 : 0)); }
  ctx.stroke();
  // footer: the reply, taught in two lines
  ctx.textAlign = "left"; ctx.textBaseline = "alphabetic"; ctx.fillStyle = "#4a4f57"; ctx.font = "11px ui-monospace, monospace";
  ctx.fillText(`${wide ? "message is 0–800 (dashed), ink beyond it shown" : "800 units wide"} · to mark this up, reply with \`\`\`ink on=${on} then lines like`, RL, TOP + ch + 16);
  ctx.fillText(`circle x,y r40 "note" · arrow x,y -> x,y · text x,y "words" · check x,y`, RL, TOP + ch + 31);
  ctx.restore();
}

/** The sheet's paper, drawn from its kind (the CSS draws it with gradients the canvas can't read). */
function paper(ctx, base, kind, H, el, from = 0) {
  // the tokens are light-dark() pairs: a probe's computed colour resolves them for the host's scheme
  const probe = document.createElement("i"); el.append(probe);
  const v = n => { probe.style.color = `var(${n})`; const c = getComputedStyle(probe).color; return c && !CLEAR(c) ? c : ""; };
  const cols = { rule: v("--paper-rule"), margin: v("--paper-margin"), grid: v("--paper-grid"), dot: v("--paper-dot") }; probe.remove();
  ctx.save(); ctx.setTransform(base);
  ctx.fillStyle = "#fff"; ctx.fillRect(0, from, W, H - from);
  if (kind === "ruled") {
    ctx.fillStyle = cols.rule || "#dfe5ee";
    for (let y = LINE; y <= H; y += LINE) ctx.fillRect(0, y - 1, W, 1);
    ctx.fillStyle = cols.margin || "rgba(200,80,60,.35)"; ctx.fillRect(44, 0, 1, H);
  } else if (kind === "grid") {
    ctx.fillStyle = cols.grid || "#e8ecf2";
    for (let y = 0; y <= H; y += LINE) ctx.fillRect(0, y, W, 1);
    for (let x = 0; x <= W; x += LINE) ctx.fillRect(x, 0, 1, H);
  } else if (kind === "dot") {
    ctx.fillStyle = cols.dot || "#c4c8cf";
    for (let y = Math.ceil(from / 24) * 24 + 12; y <= H; y += 24) for (let x = 12; x <= W; x += 24) { ctx.beginPath(); ctx.arc(x, y, 1.1, 0, 7); ctx.fill(); }
  }
  ctx.restore();
}

// ---------- HTML: boxes, words, pictures ----------
const CLEAR = c => !c || c === "transparent" || /rgba\([^)]*,\s*0\)$/.test(c);
/** Paint root's visible boxes and words. T maps screen px to canvas px. */
export function paintHtml(ctx, root, T, _base, { skip = ".ink-over,.ink-mark,.sc-layer,.katex-mathml,script,style" } = {}) {
  ctx.save(); ctx.setTransform(T);
  const inv = T.inverse(), clip = { l: inv.e, t: inv.f, r: inv.e + ctx.canvas.width * inv.a, b: inv.f + ctx.canvas.height * inv.d };
  const near = r => r.right >= clip.l && r.left <= clip.r && r.bottom >= clip.t && r.top <= clip.b;
  const rg = document.createRange();
  const walk = (el, alpha) => {
    if (el.matches(skip)) return;
    const cs = getComputedStyle(el);
    if (cs.display === "none") return;
    const a = alpha * (parseFloat(cs.opacity) || 0); if (a <= .01) return;
    const r = el.getBoundingClientRect();
    if (cs.display !== "contents" && !near(r) && cs.overflow !== "visible") return;
    const vis = cs.visibility !== "hidden";
    ctx.globalAlpha = a;
    if (vis && r.width && r.height) {
      if (!CLEAR(cs.backgroundColor)) { ctx.fillStyle = cs.backgroundColor; box(ctx, r, cs); ctx.fill(); }
      for (const [side, x, y, w, hh] of [["Top", r.left, r.top, r.width, 0], ["Bottom", r.left, r.bottom, r.width, 0], ["Left", r.left, r.top, 0, r.height], ["Right", r.right, r.top, 0, r.height]]) {
        const bw = parseFloat(cs[`border${side}Width`]) || 0, bc = cs[`border${side}Color`];
        if (!bw || CLEAR(bc) || cs[`border${side}Style`] === "none") continue;
        ctx.fillStyle = bc;
        ctx.fillRect(side === "Right" ? x - bw : x, side === "Bottom" ? y - bw : y, w || bw, hh || bw);
      }
    }
    const tag = el.tagName;
    if (tag === "svg" || el instanceof SVGSVGElement) { if (vis) { ctx.save(); paintSvg(ctx, el, T); ctx.restore(); ctx.setTransform(T); } return; }
    if (vis && (tag === "IMG" || tag === "CANVAS" || tag === "VIDEO" || tag === "IFRAME")) { picture(ctx, el, r, cs); return; }
    for (const n of el.childNodes) {
      if (n.nodeType === 1) walk(n, a);
      else if (n.nodeType === 3 && vis && n.nodeValue.trim()) { ctx.globalAlpha = a; words(ctx, n, cs, rg, near); }
    }
  };
  let a0 = 1; for (let e = root.parentElement; e; e = e.parentElement) a0 *= parseFloat(getComputedStyle(e).opacity) || 1;
  walk(root, a0);
  ctx.restore();
}
function box(ctx, r, cs) {
  const rad = Math.min(parseFloat(cs.borderTopLeftRadius) || 0, r.width / 2, r.height / 2);
  ctx.beginPath(); if (rad && ctx.roundRect) ctx.roundRect(r.left, r.top, r.width, r.height, rad); else ctx.rect(r.left, r.top, r.width, r.height);
}
function words(ctx, n, cs, rg, near) {
  rg.selectNodeContents(n); if (!near(rg.getBoundingClientRect())) return;
  ctx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
  ctx.fillStyle = cs.webkitTextFillColor && !CLEAR(cs.webkitTextFillColor) ? cs.webkitTextFillColor : cs.color;
  ctx.textBaseline = "alphabetic"; ctx.textAlign = "left";
  try { ctx.letterSpacing = cs.letterSpacing === "normal" ? "0px" : cs.letterSpacing; } catch (e) {}
  const tt = cs.textTransform, text = n.nodeValue;
  for (const m of text.matchAll(/\S+/g)) {
    rg.setStart(n, m.index); rg.setEnd(n, m.index + m[0].length);
    const rs = rg.getClientRects(); if (!rs.length) continue;
    let w = tt === "uppercase" ? m[0].toUpperCase() : tt === "lowercase" ? m[0].toLowerCase() : tt === "capitalize" ? m[0][0].toUpperCase() + m[0].slice(1) : m[0];
    if (rs.length > 1) {   // a word broken over lines: each piece where it sits, by its characters
      let i = 0;
      for (const r of rs) { let j = i; const ww = r.width; let acc = ""; while (j < w.length && ctx.measureText(acc + w[j]).width <= ww + 1) acc += w[j++]; line(ctx, acc || w.slice(i), r); w = w.slice(acc.length || w.length); i = 0; if (!w) break; }
      continue;
    }
    line(ctx, w, rs[0]);
  }
}
function line(ctx, w, r) {
  const m = ctx.measureText(w), asc = m.fontBoundingBoxAscent ?? m.actualBoundingBoxAscent, desc = m.fontBoundingBoxDescent ?? m.actualBoundingBoxDescent;
  const y = r.top + (r.height - (asc + desc)) / 2 + asc;
  ctx.fillText(w, r.left, y, r.width + 2);
}
function picture(ctx, el, r, cs) {
  const tag = el.tagName;
  try {
    if (!SAFE && tag === "CANVAS") { ctx.drawImage(el, r.left, r.top, r.width, r.height); return; }
    if (!SAFE && tag === "IMG" && el.complete && el.naturalWidth) {
      const u = new URL(el.currentSrc || el.src, location.href);
      if (u.origin === location.origin || u.protocol === "data:" || u.protocol === "blob:") { ctx.drawImage(el, r.left, r.top, r.width, r.height); return; }
    }
  } catch (e) {}
  ctx.fillStyle = "#eef0f3"; ctx.fillRect(r.left, r.top, r.width, r.height);
  ctx.strokeStyle = "#c9cdd3"; ctx.lineWidth = 1; ctx.strokeRect(r.left + .5, r.top + .5, r.width - 1, r.height - 1);
  ctx.fillStyle = "#6b7079"; ctx.font = "12px system-ui, sans-serif"; ctx.textBaseline = "middle";
  const label = tag === "IMG" ? "image" + (el.alt ? ": " + el.alt : "") : tag === "VIDEO" ? "video" : tag === "IFRAME" ? "page" : "picture";
  ctx.fillText(label.slice(0, 60), r.left + 8, r.top + Math.min(r.height / 2, 14), Math.max(10, r.width - 16));
}

// ---------- SVG: shapes as Path2D, text by its glyph positions ----------
const SHAPES = new Set(["path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text"]);
/** Paint an <svg>'s visible shapes. T maps screen px to canvas px. */
export function paintSvg(ctx, svg, T) {
  const vis = el => { for (let e = el; e && e !== svg.parentElement; e = e.parentElement) { const c = getComputedStyle(e); if (c.display === "none" || c.visibility === "hidden") return false; } return true; };
  const alpha = el => { let a = 1; for (let e = el; e; e = e.parentElement) { a *= parseFloat(getComputedStyle(e).opacity); if (e === svg) break; } let o = svg.parentElement; while (o) { a *= parseFloat(getComputedStyle(o).opacity) || 1; o = o.parentElement; if (o && o.style && o.style.opacity === "0") break; } return a; };
  for (const el of svg.querySelectorAll("*")) {
    const tag = el.tagName.toLowerCase(); if (!SHAPES.has(tag) || el.closest("defs,clipPath,mask,marker,pattern,symbol")) continue;
    if (tag !== "text" && el.closest("text")) continue;
    if (!vis(el)) continue;
    const M = el.getScreenCTM(); if (!M) continue;
    const cs = getComputedStyle(el), a = alpha(el);
    ctx.save();
    ctx.setTransform(T.multiply(DOMMatrix.fromMatrix(M)));
    ctx.globalAlpha = Math.max(0, Math.min(1, a));
    const blend = cs.mixBlendMode; if (blend && blend !== "normal") ctx.globalCompositeOperation = blend;
    if (tag === "text") { svgText(ctx, el, cs); ctx.restore(); continue; }
    const p = shape(el, tag); if (!p) { ctx.restore(); continue; }
    if (!CLEAR(cs.fill) && cs.fill !== "none" && !/^url/.test(cs.fill) && tag !== "line") {
      ctx.fillStyle = cs.fill; const fa = parseFloat(cs.fillOpacity); ctx.globalAlpha *= isNaN(fa) ? 1 : fa;
      ctx.fill(p, cs.fillRule === "evenodd" ? "evenodd" : "nonzero"); ctx.globalAlpha = Math.max(0, Math.min(1, a));
    }
    if (cs.stroke && cs.stroke !== "none" && !CLEAR(cs.stroke) && !/^url/.test(cs.stroke)) {
      ctx.strokeStyle = cs.stroke; ctx.lineWidth = parseFloat(cs.strokeWidth) || 1;
      ctx.lineCap = cs.strokeLinecap || "butt"; ctx.lineJoin = cs.strokeLinejoin || "miter";
      const da = cs.strokeDasharray; if (da && da !== "none") { ctx.setLineDash(da.split(/[\s,]+/).map(parseFloat).filter(x => !isNaN(x))); ctx.lineDashOffset = parseFloat(cs.strokeDashoffset) || 0; }
      const sa = parseFloat(cs.strokeOpacity); ctx.globalAlpha *= isNaN(sa) ? 1 : sa;
      ctx.stroke(p);
    }
    ctx.restore();
  }
}
const num = (el, k) => { const v = el[k]; return v && v.baseVal ? v.baseVal.value : parseFloat(el.getAttribute(k)) || 0; };
function shape(el, tag) {
  try {
    if (tag === "path") { const d = el.getAttribute("d"); return d ? new Path2D(d) : null; }
    const p = new Path2D();
    if (tag === "rect") { const x = num(el, "x"), y = num(el, "y"), w = num(el, "width"), hh = num(el, "height"), rx = num(el, "rx"); if (rx && p.roundRect) p.roundRect(x, y, w, hh, rx); else p.rect(x, y, w, hh); }
    else if (tag === "circle") p.arc(num(el, "cx"), num(el, "cy"), num(el, "r"), 0, Math.PI * 2);
    else if (tag === "ellipse") p.ellipse(num(el, "cx"), num(el, "cy"), num(el, "rx"), num(el, "ry"), 0, 0, Math.PI * 2);
    else if (tag === "line") { p.moveTo(num(el, "x1"), num(el, "y1")); p.lineTo(num(el, "x2"), num(el, "y2")); }
    else { const pts = el.points; for (let i = 0; i < pts.numberOfItems; i++) { const q = pts.getItem(i); i ? p.lineTo(q.x, q.y) : p.moveTo(q.x, q.y); } if (tag === "polygon") p.closePath(); }
    return p;
  } catch (e) { return null; }
}
function svgText(ctx, el, cs) {
  const n = el.getNumberOfChars ? el.getNumberOfChars() : 0; if (!n) return;
  const txt = el.textContent;
  ctx.textBaseline = "alphabetic"; ctx.textAlign = "left";
  let cur = null;
  for (let i = 0; i < n && i < txt.length; i++) {
    const ch = txt[i]; if (!ch.trim()) continue;
    let pos; try { pos = el.getStartPositionOfChar(i); } catch (e) { continue; }
    // the char's own element (a tspan) for its font and colour
    const own = charEl(el, i) || el, c = own === cur ? null : getComputedStyle(own);
    if (c) { cur = own; ctx.font = `${c.fontStyle} ${c.fontWeight} ${c.fontSize} ${c.fontFamily}`; ctx.fillStyle = c.fill && c.fill !== "none" && !/^url/.test(c.fill) ? c.fill : c.color; }
    ctx.fillText(ch, pos.x, pos.y);
  }
}
function charEl(el, i) {
  let at = 0;
  const walk = e => { for (const n of e.childNodes) { if (n.nodeType === 3) { if (i < at + n.nodeValue.length) return n.parentElement; at += n.nodeValue.length; } else if (n.nodeType === 1) { const r = walk(n); if (r) return r; } } return null; };
  return walk(el);
}
