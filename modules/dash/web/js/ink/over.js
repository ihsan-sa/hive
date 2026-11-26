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
  cur = { th, sid, art, pid: e.pointerId, x0: e.clientX, y0: e.clientY, t0: performance.now(), moved: false, target: e.target, stylus: performance.now() - stylusAt < 600 };
  if (t === "eraser") { cur.erase = true; erase(unitPt(art, e)); }
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
  if (!cur.moved) { cur.moved = true; document.body.setAttribute("data-ink-drawing", ""); }
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
    if (!r || (!(was && was.svg.parentNode === art) && (r.ar.bottom < br.top - 300 || r.ar.top > br.bottom + 300))) continue;
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
  if (BUB) BUB.place();
}
addEventListener("scroll", e => { if (e.target === document || (e.target.closest && e.target.closest("#main"))) paint(); }, { capture: true, passive: true });
addEventListener("resize", () => paint());
addEventListener("ink:change", () => paint());
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
