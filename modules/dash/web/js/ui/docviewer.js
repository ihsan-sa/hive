// The Documents viewer (views/docs.js): a library PDF as page pictures with an invisible text layer, and the person's notes on it, in the
// library's comment style (site-edit's __se/comments.js). Select text or draw a box (⌘⇧X / Ctrl+Shift+X, Esc cancels): a card opens,
// Enter adds the note as a draft. Drafts wait until Send (one) or Send all; the agent in the document's thread (doc-<id>) edits the
// file, files the new revision in the library, and marks the notes it addressed (docs.py addressed). The rest stay open and are drawn
// on the newer revision, found again by their quoted words. The person, 11211810943e and 112118149ee3.
// One live element per document, kept across the app's redraws: views/docs.js returns a bare stand-in (data-own) carrying _real.
import { h, mount, selecting } from "../core/dom.js";
import { S, jget, post } from "../core/store.js";
import { ago } from "../core/format.js";
import { threadHref } from "../core/router.js";

const C = { comment: "#C2552C", ask: "#4F6D8A", draw: "#4A4740" };
const HL = { comment: "rgba(217,120,80,0.30)", ask: "rgba(79,109,138,0.22)" };
const FILL = { comment: "rgba(194,85,44,0.08)", ask: "rgba(79,109,138,0.08)", draw: "rgba(21,20,15,0.05)" };
const isMac = /Mac|iP(hone|ad)/.test(navigator.platform || navigator.userAgent);
const BOXKEY = isMac ? "⌘⇧X" : "Ctrl+Shift+X";
const KINDWORD = { comment: "Comment", ask: "Ask" };

let V = null;   // the open document: { base, el, d, ... }

/** The live viewer for document `base`, or its stand-in when it is already on the page. */
export function viewer(base, ctx) {
  if (!V || V.base !== base) { if (V) V.ac.abort(); V = make(base); }
  V.ctx = ctx;
  if (!V.d && !V.loading) load();
  V.syncPanel();
  if (!V.el.isConnected) return V.el;
  const c = V.el.cloneNode(false); c._real = V.el; return c;
}
/** The poll: a newer revision or the agent's marks show up without a reload, but never under a card being written. */
export function poll() { if (V && V.el.isConnected && !V.compose && !V.open && !V.sending && !inCard() && !selecting()) load(); }
const inCard = () => { const a = document.activeElement; return !!(a && a.closest && a.closest(".dv-card")); };
export function leave() { if (V) { V.ac.abort(); V = null; } }

async function load() {
  const v = V; v.loading = true;
  try {
    const d = await jget(`/api/docs/doc?id=${encodeURIComponent(v.base)}` + (v.rev ? `&rev=${encodeURIComponent(v.rev)}` : ""));
    if (v !== V) return;
    // The banner is for a revision that arrived while the page was open, not for the person switching back to the latest.
    const was = v.d, last = d.revs?.at(-1)?.rev, newer = was && v.latest && last !== v.latest && !v.rev;
    v.latest = last;
    if (newer) v.banner = arrived(d, was);
    // Repaint only on a change: a repaint rebuilds the open card, and a poll mustn't take away what is being typed in it.
    const sg = JSON.stringify([d.sha, d.rev, d.revs, d.notes]), same = was && sg === v.dataSig && !v.err;
    v.d = d; v.err = null; v.dataSig = sg;
    if (!was || d.sha !== was.sha || d.rev !== was.rev) v.drawPages();
    if (!same || newer) v.paint();
  } catch (e) { if (v === V) { v.err = e.message; v.paint(); } }
  finally { v.loading = false; }
}
function arrived(d, was) {
  const now = d.notes.filter(n => n.addressedIn === d.rev).length, open = d.notes.filter(n => n.status === "sent").length;
  return `Revision ${d.rev} is here` + (now ? `: ${now} note${now > 1 ? "s" : ""} addressed` : "") + (open ? `, ${open} still open (drawn below)` : "") + ".";
}

// ---- the text layer: transparent words over the page picture, so the browser's own selection works ----
let cv;
function natural(t) { cv ||= document.createElement("canvas").getContext("2d"); cv.font = "100px sans-serif"; return cv.measureText(t).width / 100; }
function wordsLayer(pg) {
  const L = h("div", { class: "dv-text" });
  for (const [i, w] of pg.words.entries()) {
    const [x0, y0, x1, y1, t] = w, fs = (y1 - y0) / pg.w * 100, nat = natural(t) * fs, want = (x1 - x0) / pg.w * 100;
    const s = document.createElement("span");
    s.textContent = t; s.dataset.i = i;
    s.style.cssText = `left:${x0 / pg.w * 100}%;top:${y0 / pg.h * 100}%;font-size:${fs}cqw;transform:scaleX(${nat > 0 ? Math.min(4, Math.max(0.25, want / nat)) : 1})`;
    L.append(s);
  }
  return L;
}

// ---- where a note is: its own revision's rects, or on a newer one the same words found again ----
const norm = s => s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");
function refind(d, n) {
  if (n.rev === d.rev || !n.quote || n.box) return null;
  const q = n.quote.split(/\s+/).map(norm).filter(Boolean); if (!q.length) return null;
  // The same words can be there more than once ("Python" in two places): take the one nearest where the note was, same page first.
  const order = [n.p, ...d.pages.map((_, i) => i + 1).filter(p => p !== n.p)], r0 = (n.rects || [])[0];
  for (const p of order) {
    const ws = (d.pages[p - 1] || {}).words || [], t = ws.map(w => norm(w[4]));
    let best = null, bd = Infinity;
    for (let i = 0; i + q.length <= t.length; i++) {
      let ok = true; for (let k = 0; k < q.length && ok; k++) ok = t[i + k] === q[k];
      if (!ok) continue;
      const dist = p === n.p && r0 ? Math.hypot(ws[i][0] - r0[0], ws[i][1] - r0[1]) : 0;
      if (dist < bd) { bd = dist; best = i; }
      if (!dist) break;
    }
    if (best != null) return { p, rects: lineRects(ws.slice(best, best + q.length)) };
  }
  return { gone: true };
}
function lineRects(ws) {
  const by = new Map();
  for (const w of ws) { const r = by.get(w[5]); if (!r) by.set(w[5], [w[0], w[1], w[2], w[3]]); else { r[0] = Math.min(r[0], w[0]); r[1] = Math.min(r[1], w[1]); r[2] = Math.max(r[2], w[2]); r[3] = Math.max(r[3], w[3]); } }
  return [...by.values()].map(r => r.map(v => Math.round(v * 100) / 100));
}
const isOpen = n => n.status === "draft" || n.status === "sent";
/** The notes drawn on the revision shown: every open one, and the ones addressed on this very revision (faint). */
function placed(d) {
  const out = [];
  for (const n of d.notes) {
    if (n.p == null) continue;
    if (!isOpen(n) && n.rev !== d.rev) continue;
    const f = refind(d, n);
    if (f && f.p) out.push({ n, p: f.p, rects: f.rects, moved: false });
    else out.push({ n, p: n.p, rects: n.rects || [], moved: !!(f && f.gone) });
  }
  return out;
}
const where = n => n.p == null ? "whole document" : (n.box ? "box, " : "") + "p. " + n.p;

function make(base) {
  const ac = new AbortController(), sig = ac.signal;
  const v = { base, ac, d: null, rev: null, compose: null, open: null, boxMode: false, banner: null, err: null, panelSig: "" };
  const pagesEl = h("div", { class: "dv-pages", "aria-label": "Document pages" });
  const panelEl = h("div", { class: "dv-panel" });
  const el = h("div", { class: "dv", "data-key": "dv:" + base, "data-own": "", "data-sig": base }, pagesEl, h("aside", { class: "dv-side", "aria-label": "Notes" }, panelEl));
  Object.assign(v, { el, pagesEl, panelEl });

  v.drawPages = () => {
    v.compose = null; v.open = null;
    const d = v.d;
    pagesEl.replaceChildren(...d.pages.map((pg, i) => {
      const p = i + 1;
      const img = h("img", { class: "dv-img", alt: `Page ${p} of ${d.pages.length}`, draggable: "false", loading: i < 2 ? "eager" : "lazy",
        src: `/api/docs/page?id=${encodeURIComponent(d.id)}&rev=${encodeURIComponent(d.rev)}&p=${p}&s=${d.sha}` });
      const page = h("div", { class: "dv-page", "data-p": p, style: `aspect-ratio:${pg.w}/${pg.h}` }, img, wordsLayer(pg), h("div", { class: "dv-marks" }));
      page._pg = pg;
      return page;
    }));
  };

  // ---- marks ----
  v.drawMarks = () => {
    const d = v.d; if (!d) return;
    const all = placed(d), pages = [...pagesEl.children];
    for (const page of pages) {
      const p = +page.dataset.p, pg = page._pg, M = page.querySelector(".dv-marks"), out = [];
      const pct = r => `left:${r[0] / pg.w * 100}%;top:${r[1] / pg.h * 100}%;width:${(r[2] - r[0]) / pg.w * 100}%;height:${(r[3] - r[1]) / pg.h * 100}%`;
      for (const m of all.filter(m => m.p === p)) {
        const n = m.n, k = n.kind, done = !isOpen(n), last = m.rects[m.rects.length - 1]; if (!last) continue;
        const attrs = { "data-kind": k, "data-done": done ? "" : null, "data-moved": m.moved ? "" : null };
        if (n.box) out.push(h("div", { class: "dv-box", ...attrs, style: pct(last) + `;border-color:${C[k]};background:${FILL[k]}` }));
        else for (const r of m.rects) out.push(h("div", { class: "dv-hl", ...attrs, style: pct(r) + `;background:${HL[k]}` }));
        const pinAt = n.box ? `left:${last[0] / pg.w * 100}%;top:${last[1] / pg.h * 100}%` : `left:${last[2] / pg.w * 100}%;top:${last[1] / pg.h * 100}%`;
        out.push(h("button", { type: "button", class: n.box ? "dv-pin dv-pin-box" : "dv-pin", ...attrs, style: pinAt + `;background:${C[k]}`,
          title: (m.moved ? "This passage changed in this revision. " : "") + KINDWORD[k] + ": " + n.text, "aria-label": `Note ${n.n}, ${KINDWORD[k]}: ${n.text}`,
          onclick: e => { e.stopPropagation(); openNote(n.id); } }, String(n.n)));
      }
      const c = v.compose;
      if (c && c.p === p) {
        if (c.box) out.push(h("div", { class: "dv-box dv-drawing", style: pct(c.rects[0]) }));
        else for (const r of c.rects) out.push(h("div", { class: "dv-hl", style: pct(r) + `;background:${HL[c.kind]}` }));
      }
      M.replaceChildren(...out);
    }
    placeCard();
  };

  // ---- the card: composing a new note, or one note opened from its pin ----
  let cardEl = null;
  function placeCard() {
    if (cardEl) { cardEl.remove(); cardEl = null; }
    const c = v.compose, o = v.open && v.d && v.d.notes.find(n => n.id === v.open);
    if (!c && !o) return;
    let p, rect;
    if (c) { p = c.p; rect = c.rects[c.rects.length - 1]; }
    else { const m = placed(v.d).find(m => m.n.id === o.id); if (!m) { v.open = null; return; } p = m.p; rect = m.rects[m.rects.length - 1]; }
    const page = pagesEl.querySelector(`.dv-page[data-p="${p}"]`); if (!page || !rect) return;
    cardEl = c ? composeCard(c) : noteCard(o);
    const pg = page._pg;
    cardEl.style.top = `calc(${rect[3] / pg.h * 100}% + 10px)`;
    cardEl.style.left = `clamp(4px, calc(${(rect[0] + rect[2]) / 2 / pg.w * 100}% - 160px), calc(100% - 324px))`;
    page.append(cardEl);
    const t = cardEl.querySelector("textarea");
    if (t && c) requestAnimationFrame(() => { t.focus({ preventScroll: true }); t.setSelectionRange(t.value.length, t.value.length); });
    else if (v.focusCard) { v.focusCard = false; requestAnimationFrame(() => { const f = cardEl && cardEl.querySelector("textarea, .dv-act, .dv-x"); if (f) f.focus({ preventScroll: true }); }); }
    requestAnimationFrame(() => { if (cardEl) cardEl.scrollIntoView({ block: "nearest", behavior: "smooth" }); });
  }
  const dot = (n, kind) => h("span", { class: "dv-dot", style: `background:${C[kind]}` }, String(n));
  function kindSeg(cur, set) {
    return h("span", { class: "dv-kinds", role: "radiogroup", "aria-label": "Kind of note" },
      ...["ask", "comment"].map(k => h("button", { type: "button", role: "radio", "aria-checked": String(cur === k), class: "dv-kind", "data-on": cur === k ? "" : null, style: cur === k ? `color:${C[k]}` : null,
        onclick: e => { e.stopPropagation(); set(k); } }, KINDWORD[k])));
  }
  function field(value, kind, onEnter, onEsc) {
    const t = h("textarea", { class: "dv-field", rows: "2", placeholder: kind === "ask" ? "Ask about this" : "What should change", "aria-label": "Note" });
    t.value = value;
    t.addEventListener("input", () => { t.style.height = "auto"; t.style.height = Math.min(240, t.scrollHeight) + "px"; if (v.compose) v.compose.text = t.value; });
    t.addEventListener("keydown", e => {
      e.stopPropagation();
      if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); onEnter(t.value.trim()); }
      else if (v.boxMode && !typing && !e.metaKey && !e.ctrlKey && !e.altKey && kbKey(e)) { e.preventDefault(); e.stopPropagation(); }
    else if (e.key === "Escape") { e.preventDefault(); onEsc(); }
    });
    return t;
  }
  function shell(kind, ...kids) {
    const c = h("div", { class: "dv-card", "data-kind": kind, role: "dialog", "aria-label": KINDWORD[kind] }, ...kids);
    c.style.borderColor = C[kind];
    for (const t of ["pointerdown", "pointerup", "mousedown", "mouseup"]) c.addEventListener(t, e => e.stopPropagation());
    return c;
  }
  function composeCard(c) {
    const t = field(c.text || "", c.kind, text => { if (text) add(c, text); }, cancel);
    return shell(c.kind,
      h("div", { class: "dv-card-head" }, kindSeg(c.kind, k => { c.kind = k; c.text = t.value; v.drawMarks(); }),
        h("span", { class: "dv-where" }, where(c)), h("button", { type: "button", class: "dv-x", title: "Cancel", "aria-label": "Cancel", onclick: cancel }, "×")),
      c.quote ? h("div", { class: "dv-quote" }, "“" + clip(c.quote, 140) + "”") : null,
      t, h("div", { class: "dv-acts" }, h("span", { class: "dv-hint" }, "↵ add · ⇧↵ new line · esc"),
        h("button", { type: "button", class: "dv-act", onclick: () => { const x = t.value.trim(); if (x) return add(c, x); t.focus(); } }, "Add")));
  }
  function noteCard(n) {
    const k = n.kind, draft = n.status === "draft";
    const close = () => { v.open = null; placeCard(); };
    const t = draft ? field(n.text, k, text => text && act({ do: "edit", note_id: n.id, text, kind: k }).then(close), close) : null;
    const state = n.status === "draft" ? "Draft, not sent yet" : n.status === "sent" ? "Sent " + ago(Date.parse(n.sentAt)) + ", waiting for the agent"
      : n.addressedIn === "by hand" ? "Marked done" : "Addressed in revision " + n.addressedIn;
    return shell(k,
      h("div", { class: "dv-card-head" }, dot(n.n, k), h("b", null, KINDWORD[k]), h("span", { class: "dv-where" }, where(n) + (n.rev !== v.d.rev ? " · from rev " + n.rev : "")),
        h("button", { type: "button", class: "dv-x", title: "Close", "aria-label": "Close", onclick: close }, "×")),
      n.quote ? h("div", { class: "dv-quote" }, "“" + clip(n.quote, 140) + "”") : null,
      t || h("div", { class: "dv-text-ro" }, n.text),
      h("div", { class: "dv-hint" }, state),
      h("div", { class: "dv-acts" },
        draft ? h("button", { type: "button", class: "dv-act", onclick: () => send([n.id]).then(close) }, "Send") : null,
        draft ? h("button", { type: "button", class: "dv-act", onclick: () => act({ do: "edit", note_id: n.id, text: t.value.trim() || n.text, kind: k }).then(close) }, "Save") : null,
        n.status === "sent" ? h("button", { type: "button", class: "dv-act", onclick: () => act({ do: "done", note_id: n.id }).then(close) }, "Mark done") : null,
        n.status === "addressed" ? h("button", { type: "button", class: "dv-act", onclick: () => act({ do: "reopen", note_id: n.id }).then(close) }, "Reopen") : null,
        n.status !== "sent" ? h("button", { type: "button", class: "dv-act dv-del", onclick: () => act({ do: "delete", note_id: n.id }).then(close) }, "Delete") : null));
  }
  function openNote(id) { v.compose = null; v.open = id; v.focusCard = true; getSelection().removeAllRanges(); v.drawMarks(); }
  function cancel() { v.compose = null; v.drawMarks(); }
  v.focusNote = id => {
    const n = v.d.notes.find(x => x.id === id); if (!n) return;
    if (n.p == null) return;
    openNote(id);
  };

  // ---- making notes: a text selection, or a drawn box ----
  function fromSelection() {
    if (v.boxMode) return;
    const s = getSelection(); if (!s || s.isCollapsed || !s.rangeCount) return;
    const r = s.getRangeAt(0), anc = r.commonAncestorContainer, node = anc.nodeType === 1 ? anc : anc.parentNode;
    if (!pagesEl.contains(node)) return;
    let page = node.closest(".dv-page");
    const spans = [...(page ? page.querySelectorAll(".dv-text span") : pagesEl.querySelectorAll(".dv-text span"))].filter(sp => s.containsNode(sp, true));
    if (!spans.length) return;
    page = spans[0].closest(".dv-page");
    const pg = page._pg, ws = spans.filter(sp => sp.closest(".dv-page") === page).map(sp => pg.words[+sp.dataset.i]);
    v.open = null;
    v.compose = { kind: v.compose ? v.compose.kind : "comment", p: +page.dataset.p, rects: lineRects(ws), quote: ws.map(w => w[4]).join(" "), text: v.compose ? v.compose.text : "" };
    s.removeAllRanges();
    v.drawMarks();
  }
  let selT = 0;
  pagesEl.addEventListener("pointerup", e => { if (e.target.closest(".dv-card,.dv-pin")) return; clearTimeout(selT); selT = setTimeout(fromSelection, 10); }, { signal: sig });
  document.addEventListener("selectionchange", () => {   // a keyboard selection (shift+arrows) has no pointerup
    if (!v.el.isConnected) return; clearTimeout(selT); selT = setTimeout(() => { if (!document.querySelector(".dv-pages:active")) fromSelection(); }, 700);
  }, { signal: sig });

  let drag = null;
  pagesEl.addEventListener("pointerdown", e => {
    if (!v.boxMode || e.button !== 0 || e.target.closest(".dv-card,.dv-pin")) return;
    const page = e.target.closest(".dv-page"); if (!page) return;
    e.preventDefault(); page.setPointerCapture(e.pointerId);
    const b = page.getBoundingClientRect();
    drag = { page, b, x: e.clientX, y: e.clientY, ghost: h("div", { class: "dv-box dv-drawing" }) };
    page.querySelector(".dv-marks").append(drag.ghost);
  }, { signal: sig });
  pagesEl.addEventListener("pointermove", e => {
    if (!drag) return;
    const { b, x, y, ghost } = drag, l = Math.max(b.left, Math.min(x, e.clientX)), t = Math.max(b.top, Math.min(y, e.clientY)), r = Math.min(b.right, Math.max(x, e.clientX)), bt = Math.min(b.bottom, Math.max(y, e.clientY));
    Object.assign(ghost.style, { left: (l - b.left) / b.width * 100 + "%", top: (t - b.top) / b.height * 100 + "%", width: (r - l) / b.width * 100 + "%", height: (bt - t) / b.height * 100 + "%" });
    drag.box = [l, t, r, bt];
  }, { signal: sig });
  const endDrag = () => {
    if (!drag) return;
    const { page, b, box, ghost } = drag; drag = null; ghost.remove();
    if (!box || box[2] - box[0] < 12 || box[3] - box[1] < 12) return;
    const pg = page._pg, X = x => Math.round((x - b.left) / b.width * pg.w * 100) / 100, Y = y => Math.round((y - b.top) / b.height * pg.h * 100) / 100;
    commitBox(page, [X(box[0]), Y(box[1]), X(box[2]), Y(box[3])]);
  };
  function commitBox(page, r) {
    setBox(false);
    v.open = null;
    v.compose = { kind: "comment", p: +page.dataset.p, box: true, rects: [r], text: "" };
    v.drawMarks();
  }
  // A box by keyboard: arrows move it, Shift+arrows resize it, Enter opens the card. It starts 1/5 of the page in, on the page in view.
  let kb = null;
  function kbShow() {
    kb.ghost.style.cssText = `left:${kb.r[0] / kb.pg.w * 100}%;top:${kb.r[1] / kb.pg.h * 100}%;width:${(kb.r[2] - kb.r[0]) / kb.pg.w * 100}%;height:${(kb.r[3] - kb.r[1]) / kb.pg.h * 100}%`;
  }
  function kbEnd() { if (kb) { kb.ghost.remove(); kb = null; } }
  function kbKey(e) {
    const dir = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
    if (!dir && e.key !== "Enter") return false;
    if (!kb) {
      const pages = [...pagesEl.querySelectorAll(".dv-page")], vis = pages.find(p => p.getBoundingClientRect().bottom > innerHeight * .3) || pages[0];
      if (!vis || !vis._pg) return false;
      const pg = vis._pg; kb = { page: vis, pg, r: [pg.w * .2, pg.h * .2, pg.w * .5, pg.h * .3], ghost: h("div", { class: "dv-box dv-drawing" }) };
      vis.querySelector(".dv-marks").append(kb.ghost);
      if (e.key === "Enter") { kbShow(); return true; }
    }
    if (e.key === "Enter") { const { page, r } = kb; kbEnd(); commitBox(page, r.map(x => Math.round(x * 100) / 100)); return true; }
    const st = 12, [dx, dy] = dir, r = kb.r, W = kb.pg.w, H = kb.pg.h;
    if (e.shiftKey) { r[2] = Math.max(r[0] + 12, Math.min(W, r[2] + dx * st)); r[3] = Math.max(r[1] + 12, Math.min(H, r[3] + dy * st)); }
    else { const mx = Math.max(-r[0], Math.min(W - r[2], dx * st)), my = Math.max(-r[1], Math.min(H - r[3], dy * st)); r[0] += mx; r[2] += mx; r[1] += my; r[3] += my; }
    kbShow(); kb.ghost.scrollIntoView({ block: "nearest" }); return true;
  }
  pagesEl.addEventListener("pointerup", endDrag, { signal: sig });
  pagesEl.addEventListener("pointercancel", () => { if (drag) { drag.ghost.remove(); drag = null; } }, { signal: sig });

  function setBox(on) { if (!on) kbEnd(); v.boxMode = on; el.toggleAttribute("data-boxing", on); v.syncPanel(true); }
  document.addEventListener("keydown", e => {
    if (!v.el.isConnected) return;
    const typing = e.target.closest && e.target.closest("textarea, input, select, [contenteditable]");
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && e.key.toLowerCase() === "x" && !typing) { e.preventDefault(); setBox(!v.boxMode); }
    else if (v.boxMode && !typing && !e.metaKey && !e.ctrlKey && !e.altKey && kbKey(e)) { e.preventDefault(); e.stopPropagation(); }
    else if (e.key === "Escape" && (v.boxMode || v.compose || v.open)) { e.preventDefault(); e.stopPropagation(); if (v.boxMode) setBox(false); v.compose = null; v.open = null; v.drawMarks(); }
  }, { signal: sig, capture: true });

  // ---- saving ----
  async function act(body) {
    try { const r = await post("/api/docs/notes", { id: v.base, ...body }); v.d.notes = r.notes; v.err = null; v.paint(); return r; }
    catch (e) { v.err = "Couldn't save: " + e.message; v.paint(); throw e; }
  }
  async function add(c, text) {
    const note = { kind: c.kind, rev: v.d.rev, text };
    if (c.p != null) Object.assign(note, { p: c.p, rects: c.rects, box: !!c.box, quote: c.quote || "" });
    await act({ do: "add", note });
    if (v.compose === c) v.compose = null;
    v.drawMarks();
  }
  async function send(ids) {
    const d = v.d, ns = ids.map(id => d.notes.find(n => n.id === id)).filter(n => n && n.status === "draft");
    if (!ns.length) return;
    const line = n => `#${n.n} ${n.p == null ? "whole document" : n.box ? `p.${n.p}, box at ${n.rects[0].map(Math.round).join(",")} pt` : `p.${n.p}, on "${clip(n.quote || "", 300)}"`}` + (n.rev !== d.rev ? ` (made on revision ${n.rev})` : "") + ` · ${KINDWORD[n.kind]}: ${n.text}`;
    const text = `Notes on "${d.title}" (${d.id}, revision ${d.rev}), from the Documents page.\n\n` + ns.map(line).join("\n") +
      `\n\nHow to work these: projects/doc-review-view/AGENT.md. Every note, with where it sits: \`python3 ~/modules/dash/docs.py show ${d.id}\`.`;
    v.sending = true; v.paint();
    // Marked sent before the post goes, so an agent that answers at once never finds them still drafts; put back if the post fails.
    const ids2 = ns.map(n => n.id);
    try { await act({ do: "sent", note_ids: ids2 }); } catch (e) { v.sending = false; v.paint(); return; }
    try {
      const r = await post("/api/post", { text, thread: d.thread });
      if (r && r.id) await act({ do: "stamp", note_ids: ids2, post: r.id }).catch(() => {});
    } catch (e) {
      await act({ do: "unsend", note_ids: ids2 }).catch(() => {});
      v.err = "Couldn't send: " + e.message;
    }
    finally { v.sending = false; v.paint(); }
  }
  async function reply(t, box) {
    try { await post("/api/post", { text: t, thread: v.d.thread }); if (box && box.value.trim() === t) box.value = ""; v.err = null; } catch (e) { v.err = "Couldn't send: " + e.message; }
    v.paint();
  }

  // ---- the side panel ----
  v.paint = () => { v.drawMarks(); v.syncPanel(true); };
  v.syncPanel = force => {
    const d = v.d, th = d && S.threads && S.threads.get(d.thread), posts = th ? th.posts.slice(-4) : [];
    const sg = posts.map(p => p.id).join() + "|" + (d ? d.rev + d.notes.map(n => n.id + n.status + n.text.length).join() : "") + v.boxMode + v.err + v.banner + v.sending;
    if (!force && sg === v.panelSig) return;
    v.panelSig = sg;
    mount(panelEl, panel(posts));
  };
  function panel(posts) {
    const d = v.d;
    if (!d) return h("div", { class: "dv-pn" }, h("p", { class: "meta" }, v.err ? "Couldn't open this document: " + v.err : "Opening…"));
    const drafts = d.notes.filter(n => n.status === "draft"), sent = d.notes.filter(n => n.status === "sent"), done = d.notes.filter(n => n.status === "addressed");
    const item = n => h("li", { class: "dv-item", "data-key": n.id, "data-kind": n.kind },
      h("button", { type: "button", class: "dv-item-go", onclick: () => v.focusNote(n.id), disabled: n.p == null || null, title: n.p == null ? null : "Show it on the page" },
        dot(n.n, n.kind), h("span", { class: "dv-item-t" }, h("span", { class: "dv-item-w" }, KINDWORD[n.kind] + " · " + where(n) + (n.status === "addressed" ? " · " + (n.addressedIn === "by hand" ? "done by hand" : "rev " + n.addressedIn) : "")),
          n.quote ? h("span", { class: "dv-item-q" }, "“" + clip(n.quote, 70) + "”") : null, h("span", { class: "dv-item-x" }, n.text))),
      n.status === "draft" ? h("button", { type: "button", class: "dv-mini", onclick: () => send([n.id]), disabled: v.sending || null }, "Send") : null,
      n.status === "sent" ? h("button", { type: "button", class: "dv-mini", onclick: () => act({ do: "done", note_id: n.id }), title: "Mark it done yourself" }, "Done") : null,
      n.status === "addressed" ? h("button", { type: "button", class: "dv-mini", onclick: () => act({ do: "reopen", note_id: n.id }) }, "Reopen") : null);
    const latest = d.revs[d.revs.length - 1];
    return h("div", { class: "dv-pn" },
      h("div", { class: "dv-head" },
        h("h1", { class: "dv-title" }, d.title),
        h("div", { class: "dv-meta" }, h("span", null, d.id), h("label", null, "Revision ",
          h("select", { class: "dv-rev", "data-key": "rev", "aria-label": "Revision", onchange: e => { v.rev = e.target.value === latest.rev ? null : e.target.value; v.banner = null; load(); } },
            ...d.revs.map(r => h("option", { value: r.rev, selected: r.rev === d.rev || null }, r.rev === "next" ? "new, being filed" : r.rev + (r === latest ? " (latest)" : "")))))),
        v.rev && v.rev !== latest.rev ? h("p", { class: "dv-note", "data-tone": "warn" }, "An older revision. ", h("button", { type: "button", class: "dv-link", onclick: () => { v.rev = null; load(); } }, "Show the latest")) : null,
        v.banner ? h("p", { class: "dv-note", "data-tone": "good", role: "status" }, v.banner, " ", h("button", { type: "button", class: "dv-link", onclick: () => { v.banner = null; v.syncPanel(true); } }, "OK")) : null,
        v.err ? h("p", { class: "dv-note", "data-tone": "bad", role: "alert" }, v.err) : null),
      h("div", { class: "dv-tools" },
        h("button", { type: "button", class: "dv-tool", "aria-pressed": String(v.boxMode), onclick: () => setBox(!v.boxMode), title: "Draw a box on the page (" + BOXKEY + ")" }, v.boxMode ? "Drawing: drag, or arrows + Enter · esc" : "Draw a box"),
        h("span", { class: "dv-tip" }, "or select text on the page")),
      h("div", { class: "dv-general" },
        h("textarea", { class: "dv-gfield", "data-key": "gen", rows: "2", placeholder: "A note on the whole document", "aria-label": "A note on the whole document",
          onkeydown: e => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); const box = e.target, t = box.value.trim(); if (t) add({ kind: "comment", p: null }, t).then(() => { if (box.value.trim() === t) box.value = ""; }, () => {}); } } }),
        h("div", { class: "dv-hint" }, "↵ add to the list")),
      section("Not sent yet", drafts, item, drafts.length ? h("button", { type: "button", class: "dv-send", onclick: () => send(drafts.map(n => n.id)), disabled: v.sending || null }, v.sending ? "Sending…" : `Send all (${drafts.length})`) : null,
        "Select text or draw a box, then press Enter: notes wait here until you send them."),
      sent.length ? section("With the agent", sent, item, null) : null,
      done.length ? h("details", { class: "dv-sec", "data-key": "done" }, h("summary", null, `Done (${done.length})`), h("ul", { class: "dv-list" }, ...done.map(item))) : null,
      h("div", { class: "dv-sec dv-chat" },
        h("div", { class: "dv-sec-h" }, h("h2", null, "Conversation"), h("a", { href: threadHref(d.thread), class: "dv-link" }, posts.length ? "Open" : "")),
        posts.length ? h("ul", { class: "dv-posts" }, ...posts.map(p => h("li", { "data-key": p.id }, h("b", null, p.from === "person" ? "You" : p.from), h("span", { class: "meta" }, " · " + ago(p.t * 1000 > 1e12 ? p.t * 1000 : Date.parse(p.t))), h("div", null, clip(p.text, 220)))))
          : h("p", { class: "meta" }, "When you send notes, an agent picks them up here, edits the file and files the new revision in the library. It shows up on this page by itself."),
        posts.length ? h("textarea", { class: "dv-gfield", "data-key": "reply", rows: "2", placeholder: "Reply to the agent", "aria-label": "Reply to the agent",
          onkeydown: e => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); const t = e.target.value.trim(); if (t) reply(t, e.target); } } }) : null));
  }
  function section(title, ns, item, action, none) {
    return h("div", { class: "dv-sec" }, h("div", { class: "dv-sec-h" }, h("h2", null, title + (ns.length ? ` (${ns.length})` : "")), action),
      ns.length ? h("ul", { class: "dv-list" }, ...ns.map(item)) : h("p", { class: "meta" }, none));
  }
  return v;
}
const clip = (s, n) => (s = String(s || "").replace(/\s+/g, " ").trim()).length > n ? s.slice(0, n - 1) + "…" : s;
