// Ink over the chat (thread 112515205dbf; design ~/designs/ink-chat/HANDOFF.md §2-4).
// A Pencil draws anywhere over the messages, always; a mouse or trackpad draws only with the pen on (Pen D in the header);
// a finger never draws here, it scrolls. Each stroke belongs to the message under it (sheet m-<postid>, in that message's
// units: 800 across its width, from its top-left), so it scrolls with the message and goes in the next send as that
// message's ink. A closed loop shows the bubble: Ask about this, Side chat, Clear. Reading, selecting, scrolling, pins and
// links are untouched while the pen is off: the layer takes no pointer at all.
import { h, s } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { button } from "../ui/kit.js";
import { here, boxSideChat } from "../ui/sidechat.js";
import { W } from "./grammar.js";
import * as K from "./store.js";
import { inkLayerSvg } from "./sheet.js";
import { outline, isLoop, bbox, snapShape, nearStroke } from "./draw.js";
import { findIn, frameOf, zoomOf } from "./snap.js";
import "./toolbar.js";   // the keys and the iPad row

const NO_START = "a,button,input,textarea,select,summary,label,iframe,video,audio,[contenteditable],.ink-sheet,.ink-bubble,.cp,.sc-pin,[data-ink-skip]";
const box = () => document.querySelector("#main .cm-msgs");
const layerEl = () => document.querySelector("#main .ink-over");
/** The chat the messages box shows, if the person can write in it. */
function chatTh() {
  const b = box(); if (!b || !can("post")) return null;
  const k = b.dataset.key || ""; return k.startsWith("m:") ? k.slice(2) : null;
}
const artOf = id => { const b = box(); return b && b.querySelector(`article.msg[data-key="${CSS.escape(id)}"]`); };
/** The message a point belongs to: the one under it, else the nearest one level with it (its margins), within 48 px. */
function artAt(x, y) {
  const b = box(); if (!b) return null;
  let best = null, d = 49;
  for (const a of b.querySelectorAll("article.msg")) {
    const r = a.getBoundingClientRect(); if (r.bottom < y - 60 || r.top > y + 60) continue;
    const dy = y < r.top ? r.top - y : y > r.bottom ? y - r.bottom : 0;
    if (dy < d && S.byId[a.dataset.key]) { d = dy; best = a; if (!dy && x >= r.left && x <= r.right) break; }
  }
  return best;
}

// ---------- drawing ----------
let cur = null;   // {th, sid, art, id, t, c, w, p, lasso, erase, pid, x0, y0, t0, moved, live, holdT, holdAt, snap, stylus}
let stylusAt = 0;
const unitPt = (art, e) => { const r = frameOf(art).getBoundingClientRect(), k = W / r.width; return [(e.clientX - r.left) * k, (e.clientY - r.top) * k, e.pointerType === "pen" ? (e.pressure || .5) : .5]; };
const wants = e => e.pointerType === "pen" || (e.pointerType === "mouse" && K.TOOL.pen && e.button === 0);

document.addEventListener("pointerdown", e => {
  if (cur || !wants(e) || !e.target.closest) return;
  if (!e.target.closest("#main .cm-msgs") || e.target.closest(NO_START)) return;
  const th = chatTh(), art = th && artAt(e.clientX, e.clientY); if (!art) return;
  if (e.pointerType === "pen" && !K.TOOL.pencil) K.setTool("pencil", true);
  closeBubble();
  e.preventDefault();   // no text selection, no focus change; a mouse click still follows a tap, a Pencil's is made on pointerup
  const sid = "m-" + art.dataset.key, t = K.TOOL.t;
  steady(art); follow(art);
  cur = { th, sid, art, pid: e.pointerId, x0: e.clientX, y0: e.clientY, t0: performance.now(), moved: false, target: e.target, stylus: performance.now() - stylusAt < 600 };
  if (t === "eraser") { cur.erase = true; if (!K.inkOff()) erase(unitPt(art, e)); }   // never erases what is hidden on a bare tap
  else Object.assign(cur, { id: K.sid(), t: t === "hl" ? "hl" : "pen", c: K.TOOL.c, w: Math.round((t === "hl" ? 16 : 2.8) * zoomOf(frameOf(art)) * 10) / 10, p: [unitPt(art, e)], lasso: t === "lasso" });
  try { box().setPointerCapture(e.pointerId); box().dispatchEvent(new Event("rc-hand")); } catch (er) {}
  addEventListener("pointermove", move, true);
  addEventListener("pointerup", up, true);
  addEventListener("pointercancel", cancel, true);
}, true);

function move(e) {
  if (!cur || e.pointerId !== cur.pid) return;
  e.preventDefault();
  if (!cur.moved && Math.hypot(e.clientX - cur.x0, e.clientY - cur.y0) < 6) return;
  if (!cur.moved) { cur.moved = true; document.body.setAttribute("data-ink-drawing", ""); K.setInkOff(false); }   // a stroke brings hidden drawings back, a tap does not
  const evs = e.getCoalescedEvents ? e.getCoalescedEvents() : [];
  for (const ev of evs.length ? evs : [e]) {
    const p = unitPt(cur.art, ev);
    if (cur.erase) { erase(p); continue; }
    const q = cur.p.at(-1); if (Math.hypot(p[0] - q[0], p[1] - q[1]) < .8) continue;
    cur.p.push(p);
    if (!cur.holdAt || Math.hypot(p[0] - cur.holdAt[0], p[1] - cur.holdAt[1]) > 3) hold(p);
  }
  if (!cur.erase) liveDraw();
}
function hold(p) {
  cur.holdAt = p; clearTimeout(cur.holdT);
  const c = cur;
  c.holdT = setTimeout(() => {   // held still: the stroke becomes the shape it meant
    if (cur !== c || c.t === "hl" || c.lasso) return;
    const sn = snapShape(c.p); if (!sn) return;
    c.p = sn.p; c.snap = sn.k; liveDraw();
  }, 500);
}
function erase(p) { for (const st of K.ownStrokes(cur.th, cur.sid)) if (nearStroke(st, p[0], p[1], 8)) K.eraseStroke(cur.th, cur.sid, st.id); }
function stop() {
  removeEventListener("pointermove", move, true); removeEventListener("pointerup", up, true); removeEventListener("pointercancel", cancel, true);
  document.body.removeAttribute("data-ink-drawing");
  if (cur) clearTimeout(cur.holdT);
  const c = cur; cur = null; if (LIVE) LIVE.remove(); LIVE = null;
  return c;
}
function cancel(e) { if (cur && e.pointerId === cur.pid) stop(); }
function up(e) {
  if (!cur || e.pointerId !== cur.pid) return;
  const c = stop(), quick = performance.now() - c.t0 < 250;
  if (!c.moved) {
    // a tap passes through: a mouse's click follows by itself; a Pencil's touch was claimed, so its click is made here
    if (c.stylus && quick) { const el = document.elementFromPoint(e.clientX, e.clientY); if (el && el.click) el.click(); }
    return;
  }
  eatClick();
  if (c.erase) return;
  if (c.lasso) { const st = { id: c.id, t: "pen", c: "ink", w: 2, p: c.p }; bubble({ th: c.th, sid: c.sid, stroke: st, lasso: true }); return; }
  const st = { id: c.id, t: c.t, c: c.c, w: c.w, p: c.p.map(p => [Math.round(p[0] * 10) / 10, Math.round(p[1] * 10) / 10, Math.round(p[2] * 100) / 100]) };
  if (c.snap) st.snap = c.snap;
  const art = c.art.isConnected ? c.art : artOf(c.sid.slice(2)), at = art && pinOf(art, st.p); if (at) st.at = at;
  K.addStroke(c.th, c.sid, st, { title: "Ink on a message" });
  if (st.t !== "hl" && isLoop(st.p, 14)) bubble({ th: c.th, sid: c.sid, stroke: st });
}
/** The click that ends a drag lands on whatever is under it: a stroke is not a click. */
function eatClick() {
  const eat = e => { e.stopPropagation(); e.preventDefault(); };
  addEventListener("click", eat, { capture: true, once: true });
  setTimeout(() => removeEventListener("click", eat, true), 60);
}
// iPad Safari: a Pencil on the messages would scroll them; claiming its touch keeps the stroke. While one is down, a palm
// resting on the glass doesn't scroll either. A finger alone still scrolls.
const claim = e => {
  if (!e.target.closest || !e.target.closest("#main .cm-msgs")) return;
  if ([...e.touches].some(t => t.touchType === "stylus")) {
    if (e.type === "touchstart" && e.target.closest(NO_START)) return;
    stylusAt = performance.now(); e.preventDefault();
  } else if (cur && cur.moved) e.preventDefault();
};
addEventListener("touchstart", claim, { passive: false, capture: true });
addEventListener("touchmove", claim, { passive: false, capture: true });

// ---------- the layer ----------
let LIVE = null;   // the stroke being drawn: an <svg> over its message
function liveDraw() {
  const L = layerEl(); if (!L || !cur) return;
  const r = place(cur.art, L); if (!r) return;
  if (!LIVE || !LIVE.isConnected) {
    const path = s("path", { class: "ink-s", "data-tool": cur.t === "hl" ? "hl" : null, style: cur.lasso ? "color:var(--accent);opacity:.7" : `color:${K.PENS[cur.c] || K.PENS.ink}` });
    LIVE = s("svg", { class: "ink-mark ink-live", "aria-hidden": "true" }, path);
    L.append(LIVE);
  }
  LIVE.setAttribute("viewBox", `0 0 ${W} ${r.H}`);
  Object.assign(LIVE.style, r.css);
  LIVE.firstChild.setAttribute("d", outline(cur));
}
/** Where message art's frame (snap.js frameOf) sits in the layer: {css, H (its height in units), k (px per unit), ar (its rect)}. */
function place(art, L) {
  const ar = frameOf(art).getBoundingClientRect(), lr = L.getBoundingClientRect(); if (!ar.width) return null;
  const k = ar.width / W, z = Math.round(Math.max(1, Math.min(3, .85 / k)) * 100) / 100;
  return { k, z, H: Math.max(1, Math.round(ar.height / k)), ar, css: { left: ar.left - lr.left + "px", top: ar.top - lr.top + "px", width: ar.width + "px", height: ar.height + "px" } };
}

const MARKS = new Map();   // sid -> {svg, sig}
let raf = 0;
export function paint() { if (!raf) raf = requestAnimationFrame(draw); }
function draw() {
  raf = 0;
  const L = layerEl(), b = box(); if (!L || !b) return;
  const th = chatTh();
  if (!th) { if (L.firstChild) L.replaceChildren(); MARKS.clear(); return; }
  const mw = L.parentElement.getBoundingClientRect(), br = b.getBoundingClientRect();
  Object.assign(L.style, { left: br.left - mw.left + "px", top: br.top - mw.top + "px", width: b.clientWidth + "px", height: br.height + "px" });
  const f = K.fold(th), sids = [...K.sheetIds(th)].filter(k => k.startsWith("m-"));
  const keep = new Set(), pj = JSON.stringify(K.pend(th).sheets), hid = sid => [...K.hidden(th, sid)].join();
  for (const sid of sids) {
    const art = artOf(sid.slice(2)); if (!art) continue;
    const r = place(art, art), was = MARKS.get(sid);
    if (!r) continue;
    // near the view by the whole message, not its text: ink beside its pictures lies below the text, and it vanished
    // whenever the text was scrolled off (person, thread 11261907a87c)
    if (!(was && was.svg.parentNode === art)) { const a = art.getBoundingClientRect(); if (a.bottom < br.top - 300 || a.top > br.bottom + 300) continue; }
    steady(art); seen(art); if (!(cur && cur.art === art)) follow(art);
    const sig = [th, f.n, f.last, pj.length, pj.includes(sid) ? pj : "", Math.round(r.ar.width), r.H, hid(sid), K.ownStrokes(th, sid).length].join("|");
    let m = MARKS.get(sid);
    if (!m || m.sig !== sig || m.svg.parentNode !== art) {
      const svg = inkLayerSvg(th, sid, r.H, { find: w => findIn(art, w, W / r.ar.width), play: true, z: r.z });
      svg.classList.add("ink-mark"); svg.dataset.sid = sid;
      // inside its message, placed against the message: it scrolls with the page itself, so it can't trail behind a fast scroll
      // (it was a layer over the list, moved on each scroll event, and on an iPad it lagged; person, thread 112515205dbf)
      if (m && m.svg.parentNode === art) m.svg.replaceWith(svg); else { if (m) m.svg.remove(); art.append(svg); }
      m = { svg, sig }; MARKS.set(sid, m);
    }
    Object.assign(m.svg.style, r.css);
    keep.add(sid);
  }
  for (const [sid, m] of MARKS) if (!keep.has(sid)) { m.svg.remove(); MARKS.delete(sid); }
  // under the pen, the pictures near the view load now, not when they scroll in: a stroke drawn beside one stays beside it
  if (K.TOOL.pen || K.TOOL.pencil) for (const img of b.querySelectorAll(".msg-thumb img[loading=lazy]")) {
    const r = img.getBoundingClientRect(); if (r.bottom > br.top - 1500 && r.top < br.bottom + 1500) img.loading = "eager";
  }
  seen(b); watch(b);
  if (BUB) BUB.place();
}

// ---------- the ground under the ink ----------
// A stroke sits in its message's units, from the top of the message's text; the pictures under the text grow when they load.
// One that loaded after a stroke was drawn beside a later picture left the stroke behind (person, thread 11261907a87c). So in
// a message with ink (or under the pen), its pictures load at once, and a picture seen before keeps the size it had from the
// first frame (remembered here, per picture and message width) instead of growing from a placeholder when it arrives.
const PK = "dash.ink.pics";
let PICS = null;
const pics = () => { if (!PICS) { try { PICS = JSON.parse(localStorage.getItem(PK)) || {}; } catch (e) { PICS = {}; } } return PICS; };
const cwOf = img => { const a = img.closest("article.msg"); return a ? Math.round(a.clientWidth) : 0; };
/** Make the pictures in art load now, and hold one still loading at the size it had last time. */
function steady(art) {
  for (const img of art.querySelectorAll(".msg-thumb img")) {
    if (img.loading === "lazy") img.loading = "eager";
    if (img.complete || img.dataset.inkHeld != null || img.closest("[data-broken]")) continue;   // complete: loaded, or broken
    const P = pics(), k = img.getAttribute("src"), m = P[k], cw = cwOf(img);
    if (m && Math.abs(m[0] - cw) <= 2) { img.style.width = m[1] + "px"; img.style.height = m[2] + "px"; img.dataset.inkHeld = ""; delete P[k]; P[k] = m; }   // used: newest
  }
}
// A picture with no size remembered still grows when it arrives, and a narrower chat moves the pictures against the text. A
// stroke drawn by a picture (below the text, or on a message that is all pictures) is pinned to it at pen-up: st.at = [which
// picture, where its corner was, in units, top or bottom]. While the stroke is not sent, it moves with that picture by however
// far the picture now sits from the pin, and the pin moves with it, so the same layout always gives the same place: a reload,
// a resize or a late picture can't move it twice (the review of thread 11261907a87c). The pin stays in this page; it isn't sent.
const thumbs = art => [...art.querySelectorAll(".msg-thumb")].filter(t => t.querySelector("img"));
const keyOf = (t, i) => i + ":" + t.querySelector("img").getAttribute("src");
const r1 = v => Math.round(v * 10) / 10;
function pinOf(art, pts) {
  const fr = frameOf(art), f = fr.getBoundingClientRect(); if (!f.width || !pts.length) return null;
  const k = W / f.width, b = bbox(pts), cx = b.x + b.w / 2, cy = b.y + b.h / 2;
  if (fr !== art && cy < f.height * k) return null;   // on the text: it moves with the text already
  let best = null, bd = Infinity;
  thumbs(art).forEach((t, i) => {
    const r = t.getBoundingClientRect(); if (!r.height) return;
    const L = (r.left - f.left) * k, T = (r.top - f.top) * k, R = (r.right - f.left) * k, B = (r.bottom - f.top) * k;
    const dx = cx < L ? L - cx : cx > R ? cx - R : 0, dy = cy < T ? T - cy : cy > B ? cy - B : 0, d = Math.hypot(dx, dy * 2);
    // the corner nearest it: the strokes grow with the text's width, the pictures don't, so one to the right of a picture is
    // kept by its right edge, one below it by its bottom (the review of thread 11261907a87c)
    if (d < bd) { bd = d; const e = (cy > B ? 1 : 0) | (cx > R ? 2 : 0); best = [keyOf(t, i), r1(e & 2 ? R : L), r1(e & 1 ? B : T), e]; }
  });
  return best;
}
/** Where pin at's picture sits now in art ([x, y] in units), or null if it isn't there. */
function spot(art, f, at) {
  const T = thumbs(art), src = at[0].slice(at[0].indexOf(":") + 1);
  const t = T.find((x, i) => keyOf(x, i) === at[0]) || (T.filter(x => x.querySelector("img").getAttribute("src") === src).length === 1 ? T.find(x => x.querySelector("img").getAttribute("src") === src) : null);
  const r = t && t.getBoundingClientRect(); if (!r || !r.height) return null;
  const k = W / f.width; return [((at[3] & 2 ? r.right : r.left) - f.left) * k, ((at[3] & 1 ? r.bottom : r.top) - f.top) * k];
}
function follow(art) {
  const th = chatTh(); if (!th || !art.dataset.key || !art.querySelector(".msg-thumb")) return;
  const sid = "m-" + art.dataset.key, pinned = K.ownStrokes(th, sid).filter(st => st.pending && st.at && st.p.length); if (!pinned.length) return;
  const f = frameOf(art).getBoundingClientRect(); if (!f.width) return;
  const moves = {};
  for (const st of pinned) {
    const z = spot(art, f, st.at); if (!z) continue;
    const d = [r1(z[0] - st.at[1]), r1(z[1] - st.at[2])]; if (d[0] || d[1]) moves[st.id] = d;
  }
  if (Object.keys(moves).length) K.shiftStrokes(th, sid, moves);
}
const unhold = img => { if (img.dataset.inkHeld != null) { img.style.width = img.style.height = ""; delete img.dataset.inkHeld; } };
if (typeof document !== "undefined") {
  document.addEventListener("load", e => {
    const img = e.target; if (!img || img.tagName !== "IMG" || !img.closest || !img.closest("#main .cm-msgs .msg-thumb")) return;
    unhold(img);
    paint();
    // remembered only for a message with ink, or while the pen is out (the next stroke may go by it)
    const a = img.closest("article.msg"), th = chatTh();
    if (!a || !th || !(K.TOOL.pen || K.TOOL.pencil || K.sheetIds(th).has("m-" + a.dataset.key))) return;
    PICS = null;   // another tab may have written since
    const r = img.getBoundingClientRect(), cw = cwOf(img), P = pics(), k = img.getAttribute("src");
    if (!r.width || !cw) return;
    const v = [cw, Math.round(r.width * 10) / 10, Math.round(r.height * 10) / 10];
    const ks0 = Object.keys(P); if (P[k] && P[k].join() === v.join() && ks0[ks0.length - 1] === k) return;
    delete P[k]; P[k] = v;   // newest last: the oldest go first
    const ks = Object.keys(P); for (const x of ks.slice(0, Math.max(0, ks.length - 400))) delete P[x];
    try { localStorage.setItem(PK, JSON.stringify(P)); } catch (er) {}
  }, true);
  document.addEventListener("error", e => {
    const img = e.target; if (!img || img.tagName !== "IMG" || !img.closest || !img.closest("#main .cm-msgs .msg-thumb")) return;
    unhold(img); paint();   // a broken one shrinks to its tile
  }, true);
}
// A message that changes size with no scroll or resize to say so (a picture loading, the preview panel opening beside the
// chat, a re-render) moves its ink at once, not on the next scroll.
const RO = typeof ResizeObserver !== "undefined" ? new ResizeObserver(() => paint()) : null;
const SEEN = new Set();
function seen(el) {
  if (!RO || SEEN.has(el)) return;
  for (const x of SEEN) if (!x.isConnected) { RO.unobserve(x); SEEN.delete(x); }   // gone from the page: let it go
  SEEN.add(el); RO.observe(el);
}
// A re-render of a message drops its ink (the morph keeps only what the render makes): whatever the render, it comes straight back.
let MOB = null;
const MO = typeof MutationObserver !== "undefined" ? new MutationObserver(() => { for (const m of MARKS.values()) if (!m.svg.isConnected) { paint(); break; } }) : null;
function watch(b) { if (MO && MOB !== b) { MO.disconnect(); MOB = b; MO.observe(b, { childList: true, subtree: true }); } }
addEventListener("scroll", e => { if (e.target === document || (e.target.closest && e.target.closest("#main"))) paint(); }, { capture: true, passive: true });
addEventListener("resize", () => paint());
addEventListener("ink:change", () => { if (!K.writePage()) paint(); });
addEventListener("hashchange", () => { closeBubble(); paint(); });
/** After the chat view renders (comms.js frame). */
export function sync() { paint(); }

// ---------- the bubble: Ask about this · Side chat · Clear ----------
let BUB = null;
export function closeBubble() { if (BUB) { BUB.ac.abort(); BUB.el.remove(); if (BUB.loop) BUB.loop.remove(); BUB = null; } }
/** After a closed loop (or a lasso): three choices beside the loop's top right, never over what it circles.
 *  sheet: the loop was a lasso on a writing space (its stroke is not kept unless the person asks about it). */
export function bubble({ th, sid, sheet = null, stroke, lasso = !!sheet }) {
  closeBubble();
  const ref = () => { if (sheet) return sheet.c; const a = artOf(sid.slice(2)); return a && frameOf(a); };
  const b = isLoop(stroke.p, 10) || bbox(stroke.p);
  const rect = () => { const el = ref(); if (!el || !el.isConnected) return null; const r = el.getBoundingClientRect(), k = r.width / W; return new DOMRect(r.left + b.x * k, r.top + b.y * k, b.w * k, b.h * k); };
  const keep = () => { if (lasso) K.addStroke(th, sid, { ...stroke, p: stroke.p.map(p => [Math.round(p[0] * 10) / 10, Math.round(p[1] * 10) / 10, .5]) }, sheet ? { title: sheet.model().title, h: sheet.H } : { title: "Ink on a message" }); };
  const focusMode = sheet && sheet.mode === "focus", main = here();
  const ask = () => {
    keep(); closeBubble();
    const go = () => { const t = document.querySelector("#main .cp-t"); if (t) { t.focus(); t.setSelectionRange(t.value.length, t.value.length); } };
    if (focusMode) import("./sheet.js").then(m => { m.closeFocus(); setTimeout(go, 60); }); else go();
  };
  const side = () => { const R = rect(); closeBubble(); if (!lasso) K.dropStroke(th, sid, stroke.id); if (R) boxSideChat(main, { left: R.left, top: R.top, right: R.right, bottom: R.bottom }); };
  const clear = () => { closeBubble(); if (!lasso) K.dropStroke(th, sid, stroke.id); };
  const el = h("div", { class: "ink-bubble", role: "toolbar", "aria-label": "What to do with what you circled" },
    button("Ask about this", ask, { kind: "primary", size: "s", cls: "ink-ask", title: "Ask about it in your next message (Enter)" }),
    main && !focusMode ? button("Side chat", side, { kind: "quiet", size: "s", icon: "chat", cls: "ink-side", title: "Ask about it in a side chat beside this one" }) : null,
    h("hr"),
    button("Clear", clear, { kind: "quiet", size: "s", cls: "ink-clear", title: lasso ? "Never mind" : "Remove the loop" }));
  const ak = el.querySelector(".ink-ask"); if (ak) ak.append(h("kbd", {}, "↵"));
  const ac = new AbortController();
  document.body.append(el);
  // a select loop is not kept as ink, so while the bubble asks what to do with it, a dashed copy shows what was circled
  const lp = lasso ? s("path", { d: "" }) : null, loop = lp ? s("svg", { class: "ink-loop", "aria-hidden": "true" }, lp) : null;
  if (loop) document.body.append(loop);
  const placeIt = () => {
    const R = rect(); if (!R) { closeBubble(); return; }
    if (loop) {
      const k = R.width / Math.max(1, b.w), ox = R.left - b.x * k, oy = R.top - b.y * k;
      lp.setAttribute("d", "M" + stroke.p.map(p => (ox + p[0] * k).toFixed(1) + " " + (oy + p[1] * k).toFixed(1)).join("L") + "Z");
    }
    const bw = el.offsetWidth, bh = el.offsetHeight, top = (document.querySelector("#main .cm-msgs") || document.body).getBoundingClientRect().top;
    let x = Math.min(innerWidth - bw - 8, Math.max(8, R.right - bw)), y = R.top - bh - 8;
    if (y < Math.max(8, top)) y = R.bottom + 8;   // no room above: under it
    if (y + bh > innerHeight - 8) { y = Math.max(8, R.top + 8); x = Math.min(innerWidth - bw - 8, R.right + 8); }   // nor under: beside it
    el.style.left = x + "px"; el.style.top = y + "px";
    if (R.bottom < top - 40 || R.top > innerHeight + 40) closeBubble();
  };
  BUB = { el, ac, loop, place: placeIt };
  placeIt();
  addEventListener("keydown", e => {
    if (e.key === "Escape") { e.stopPropagation(); closeBubble(); }
    else if (e.key === "Enter" && !e.shiftKey && !e.target.closest("input,textarea,[contenteditable]")) { e.preventDefault(); ask(); }
  }, { capture: true, signal: ac.signal });
  // drawing on, or a tap elsewhere, dismisses it (the loop stays as pending ink)
  addEventListener("pointerdown", e => { if (!el.contains(e.target)) closeBubble(); }, { capture: true, signal: ac.signal });
  addEventListener("scroll", () => placeIt(), { capture: true, passive: true, signal: ac.signal });
}
