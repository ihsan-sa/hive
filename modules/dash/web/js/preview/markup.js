// Mark up any file shown as text in the chat's preview panel (a post's file, a project or kb file): select words, press Note (or
// Enter), write what you want, Enter again. Notes wait in a bar at the foot of the panel, kept per conversation and file so a reload
// or the file changing under them loses nothing, until Send posts them all to this conversation, naming its agent. The same way of
// working as a library document in the side panel (ui/docviewer.js); the person, 112122559e33 and 11212301f32d ("my old use cases
// too ... modernized and pulled in").
// A note shown as pages (paged.js) can also be boxed: Box (⌘⇧X / Ctrl+Shift+X, Esc cancels), drag over the page, write the note. The
// box stays drawn on the page, numbered, and the post says which page and quotes the words under it (the person, 1122104123fb).
// A web page in its sandboxed frame can be boxed too (the person, 112512335abe: "draw a box and ask a question"): a clear layer over
// the frame takes the drag, and the frame is sent { hive: 1, type: "region", id, x, y, w, h } (its own CSS pixels). A page that knows
// the message answers { hive: 1, type: "region", id, text, path }; only that frame's window is believed, only strings are read, the
// text is cut to 4000 characters and an @ in it can't call anyone. A page that doesn't answer gets a note with the box's place.
// Comment mode (the person, 1125142595e6: "the user effectively enters comment mode where they can click somewhere to add a comment
// at a point, or draw a box"): Comment stays on until Esc or the button; a click is a spot (a numbered pin, the frame asked about a
// 1×1 region), a drag is a box, and nothing tints the page while it is on. The first Esc in the card closes the card, the next ends
// the mode. A wheel over the layer lets the frame scroll for a moment.
// markup(it, thread, { root, body, key, where }) → { bar, stop, refresh }: bar goes under the body; stop() ends the listeners;
// refresh() redraws the boxes after the body was drawn again.
import { h, tr } from "../core/dom.js";
import { S, post } from "../core/store.js";
import { partner, isAgent } from "../core/model.js";
import { button, icon } from "../ui/kit.js";

const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const BOXKEY = isMac ? "⌘⇧X" : "Ctrl+Shift+X";
const defuse = s => String(s).replace(/@(?=[\w-])/g, "@\u200b");   // words from a page never call an agent
const pct = v => Math.round(v * 1000) / 10 + "%";

const clip = (s, n) => (s = String(s || "").replace(/\s+/g, " ").trim()).length > n ? s.slice(0, n - 1) + "…" : s;
const load = k => { try { return JSON.parse(localStorage.getItem(k) || "[]"); } catch (e) { return []; } };
const save = (k, ns) => { try { ns.length ? localStorage.setItem(k, JSON.stringify(ns)) : localStorage.removeItem(k); } catch (e) {} };

/** Who a post from here should name: the conversation's agent, else the last agent who posted in it. */
export function agentOf(thread) {
  const ps = (S.threads && S.threads.get(thread) || { posts: [] }).posts;
  return partner(thread) || ps.slice().reverse().map(x => x.from).find(isAgent) || null;
}

export function markup(it, thread, { root, body, key, where }) {
  const ac = new AbortController(), sig = { signal: ac.signal };
  const K = "pv.notes:" + thread + ":" + key;
  let notes = load(K), open = false, sending = false, err = null, quote = "", boxing = false, pend = null;
  const sheets = () => [...body.querySelectorAll(".pg-sheet")].filter(s => !s.closest(".pv-ed"));   // the pages, not the editor's preview
  const frame = () => body.querySelector(".pv-page>.pv-frame:not([data-pdf]), .pgv-b>.pgv-frame");   // a web page shown in its sandboxed frame, in the panel or full page (views/page.js)
  const canBox = () => sheets().length > 0 || !!frame();
  let ov = null, ro = null, wt = 0;   // the layer over the frame that takes a click or a drag, and what keeps it on the frame
  const fresh = new Set();   // notes on the frame made while this layer was up: drawn on it, numbered (the frame may scroll later, so only these)
  const dropOv = () => { if (ov) { ov.remove(); ov = null; } if (ro) { ro.disconnect(); ro = null; } clearTimeout(wt); fresh.clear(); };
  const fit = () => { const fr = frame(); if (ov && fr) Object.assign(ov.style, { left: fr.offsetLeft + "px", top: fr.offsetTop + "px", width: fr.offsetWidth + "px", height: fr.offsetHeight + "px" }); };
  function mkOv(fr) {
    dropOv();
    ov = h("div", { class: "pv-ov" }); fr.parentElement.append(ov); fit();
    ro = new ResizeObserver(fit); ro.observe(fr.parentElement); ro.observe(fr);
    // The layer would stop the page scrolling: a wheel over it lets the frame have the pointer for a moment (the marks drawn on the
    // layer would no longer sit on what they marked, so they go).
    ov.addEventListener("wheel", () => { if (!ov) return; ov.style.pointerEvents = "none"; fresh.clear(); boxes(); clearTimeout(wt); wt = setTimeout(() => { if (ov) ov.style.pointerEvents = ""; }, 900); }, { passive: true });
  }

  // The floating Note button by a selection, and the card it opens.
  const note = h("button", { type: "button", class: "pv-mk-go btn", "data-size": "s", "data-kind": "primary", hidden: true,
    onpointerdown: e => e.preventDefault(), onclick: () => compose() }, tr("Note"));
  const box = h("textarea", { class: "pv-mk-in", rows: 2, placeholder: tr("What should change here?"), "aria-label": tr("Your note") });
  const q = h("div", { class: "pv-mk-q" });
  const card = h("div", { class: "pv-mk-card", role: "dialog", "aria-label": tr("Add a note"), hidden: true }, q, box,
    h("div", { class: "pv-mk-row" }, h("span", { class: "meta" }, tr("Enter adds · Esc cancels")),
      button("Cancel", () => shut(), { size: "s", kind: "quiet" }), button("Add", () => add(), { size: "s", kind: "primary" })));
  root.append(note, card);
  card._shut = () => shut();   // the panel's Escape (js/ui/preview.js) closes the card and drops a box drawn for it

  // A selection in the page or text, not in the Edit tab's editor (its text is yours to change, not to note on).
  const within = r => { if (!r || !body.contains(r.commonAncestorContainer)) return false; const n = r.commonAncestorContainer; return !(n.nodeType === 1 ? n : n.parentElement).closest(".pv-ed"); };
  function place(el, r) {
    const R = root.getBoundingClientRect(), b = r.getBoundingClientRect(), w = el.offsetWidth || 80;
    el.style.left = Math.max(8, Math.min(R.width - w - 8, b.right - R.left - w / 2)) + "px";
    el.style.top = Math.max(8, Math.min(R.height - (el.offsetHeight || 32) - 8, b.bottom - R.top + 6)) + "px";
  }
  function sel() {
    if (!card.hidden) return;
    const s = getSelection(), r = s && s.rangeCount ? s.getRangeAt(0) : null, t = s ? String(s).trim() : "";
    if (!t || !within(r)) { note.hidden = true; return; }
    quote = t; note.hidden = false; place(note, r);
  }
  let tm = 0;
  document.addEventListener("selectionchange", () => { clearTimeout(tm); tm = setTimeout(sel, 120); }, sig);
  body.addEventListener("scroll", () => { note.hidden = true; }, sig);
  body.addEventListener("keydown", e => { if (e.key === "Enter" && !note.hidden && !e.isComposing && !e.target.closest(".pv-ed")) { e.preventDefault(); compose(); } }, sig);

  function compose() {
    const s = getSelection(), r = s && s.rangeCount ? s.getRangeAt(0) : null;
    note.hidden = true; q.textContent = "“" + clip(quote, 160) + "”"; box.value = ""; card.hidden = false;
    if (r && within(r)) place(card, r);
    box.focus();
  }
  function shut() { card.hidden = true; box.value = ""; box.placeholder = tr("What should change here?"); if (pend) { pend.el.remove(); pend = null; } if (!boxing) dropOv(); }
  function add() {
    const t = box.value.trim(); if (!t) return box.focus();
    const pt = pend && pend.pt ? { pt: 1 } : {};
    const n = pend && pend.f ? { q: pend.q, box: { f: 1, ...pt, path: pend.path, r: pend.r }, t, at: Date.now() }
      : pend ? { q: clip(pend.q, 600), box: { p: pend.p, ...pt, r: pend.r }, t, at: Date.now() }
      : { q: clip(quote, 600), t, at: Date.now() };
    if (pend) { pend.el.remove(); pend = null; }
    notes.push(n); if (n.box && n.box.f && ov && boxing) fresh.add(n);
    save(K, notes); shut(); getSelection().removeAllRanges(); paint(); boxes();
  }

  // ---- comment mode: spots and boxes on the pages or the web page ----
  function setBox(on) {
    boxing = !!on && canBox(); body.toggleAttribute("data-boxing", boxing);
    const fr = boxing && !sheets().length && frame();
    if (fr) { if (!ov || !ov.isConnected) mkOv(fr); else ov.removeAttribute("data-idle"); }
    else if (ov && !(pend && pend.f)) dropOv();
    else if (ov) ov.setAttribute("data-idle", "");   // the mode ended with a card still open: the layer keeps its draft, takes no clicks
    paint();
    if (boxing) { note.hidden = true; getSelection().removeAllRanges(); }
  }
  // Ask the frame what is under a box; null if it doesn't say within 1.5 s.
  function ask(fr, r) {
    return new Promise(res => {
      const w = fr.contentWindow, id = Math.random().toString(36).slice(2); let t = 0;
      const fin = v => { removeEventListener("message", on); clearTimeout(t); res(v); };
      const on = e => {
        const d = e.data;
        if (e.source !== w || e.origin !== "null" || !d || typeof d !== "object" || d.hive !== 1 || d.type !== "region" || d.id !== id) return;
        const s = v => typeof v === "string" ? v : "";
        fin({ text: clip(s(d.text).slice(0, 8000).split("\n").map(x => x.trim()).filter(Boolean).join(" · "), 4000), path: clip(s(d.path).slice(0, 1000), 300) });
      };
      addEventListener("message", on, sig); t = setTimeout(() => fin(null), 1500);
      try { w.postMessage({ hive: 1, type: "region", id, x: r.x, y: r.y, w: r.w, h: r.h }, "*"); } catch (x) { fin(null); }
    });
  }
  // One press on a page or the layer: under 6 px of travel it is a click, a spot (a pin); more is a drag, a box. done(el, r, O, spot)
  // gets the drawn mark, its place in fractions of `on` ([x, y, w, h]; w = h = 0 for a spot) and `on`'s rectangle.
  function gesture(e, on, done) {
    e.preventDefault();
    const O = on.getBoundingClientRect(), x0 = e.clientX, y0 = e.clientY, r4 = v => Math.round(v * 10000) / 10000;
    let el = null, far = false, r = [(x0 - O.left) / O.width, (y0 - O.top) / O.height, 0, 0];
    const mv = ev => {
      const x1 = Math.max(O.left, Math.min(O.right, ev.clientX)), y1 = Math.max(O.top, Math.min(O.bottom, ev.clientY));
      if (!far && Math.hypot(x1 - x0, y1 - y0) < 6) return;
      far = true;
      if (!el) { el = h("div", { class: "pg-box", "data-draft": "" }); on.append(el); }
      r = [(Math.min(x0, x1) - O.left) / O.width, (Math.min(y0, y1) - O.top) / O.height, Math.abs(x1 - x0) / O.width, Math.abs(y1 - y0) / O.height];
      Object.assign(el.style, { left: pct(r[0]), top: pct(r[1]), width: pct(r[2]), height: pct(r[3]) });
    };
    const end = () => { removeEventListener("pointermove", mv); removeEventListener("pointerup", up); removeEventListener("pointercancel", cancel); };
    const cancel = () => { end(); if (el) el.remove(); };   // the touch turned into a scroll, or the pointer was lost
    const up = () => {
      end();
      if (far && (r[2] * O.width < 8 || r[3] * O.height < 8)) { el.remove(); return; }   // a line, not a box
      if (!far) { el = h("div", { class: "pg-pin", "data-draft": "", style: `left:${pct(r[0])};top:${pct(r[1])}` }, h("b", null, String(notes.length + 1))); on.append(el); }
      done(el, r.map(r4), O, !far);
    };
    addEventListener("pointermove", mv); addEventListener("pointerup", up); addEventListener("pointercancel", cancel);
    try { on.setPointerCapture(e.pointerId); } catch (x) {}
  }
  function cardBy(el, label, hint) {
    q.textContent = label; box.value = ""; if (hint) box.placeholder = tr(hint); card.hidden = false;
    const er = el.getBoundingClientRect(), RR = root.getBoundingClientRect();
    card.style.left = Math.max(8, Math.min(RR.width - card.offsetWidth - 8, er.left - RR.left)) + "px";
    card.style.top = Math.max(8, Math.min(RR.height - card.offsetHeight - 8, er.bottom - RR.top + 6)) + "px";
    box.focus();
  }
  async function onFrame(el, r, O, spot) {
    const fr = frame(); if (!fr) { el.remove(); return; }
    if (pend) pend.el.remove();
    const me = pend = { el, f: 1, pt: spot, r, q: "", path: "" }, px = v => Math.round(v);
    const got = await ask(fr, spot ? { x: px(r[0] * O.width), y: px(r[1] * O.height), w: 1, h: 1 } : { x: px(r[0] * O.width), y: px(r[1] * O.height), w: px(r[2] * O.width), h: px(r[3] * O.height) });
    if (pend !== me) return;   // cancelled or redrawn while the page was asked
    if (got) { me.q = got.text; me.path = got.path; }
    cardBy(el, tr(spot ? "Spot on the page" : "Box on the page") + (me.path ? " · " + me.path : "") + (me.q ? ": “" + clip(me.q, 160) + "”" : ""), "Ask about this, or say what should change");
  }
  // The words under a box: every line of text whose middle falls inside it. For a spot (R with left = right, top = bottom): the
  // text whose line holds it.
  function under(sh, R) {
    const out = [], c = sh.querySelector(".pg-c"); if (!c) return "";
    const w = document.createTreeWalker(c, NodeFilter.SHOW_TEXT), rg = document.createRange(), spot = R.left === R.right && R.top === R.bottom;
    for (let n; (n = w.nextNode());) {
      if (!n.data.trim()) continue;
      rg.selectNodeContents(n);
      for (const b of rg.getClientRects()) {
        const x = spot ? R.left : (b.left + b.right) / 2, y = spot ? R.top : (b.top + b.bottom) / 2, I = spot ? b : R;
        if (x >= I.left && x <= I.right && y >= I.top && y <= I.bottom) { out.push(n.data); break; }
      }
    }
    return out.join(" ").replace(/\s+/g, " ").trim();
  }
  const onSheet = sh => (el, r, O, spot) => {
    const R = { left: O.left + r[0] * O.width, top: O.top + r[1] * O.height }; R.right = R.left + r[2] * O.width; R.bottom = R.top + r[3] * O.height;
    if (pend) pend.el.remove();
    pend = { el, p: +sh.dataset.p || 1, pt: spot, r, q: under(sh, R) };
    cardBy(el, tr((spot ? "Spot on page " : "Box on page ") + pend.p + (pend.q ? ": “" + clip(pend.q, 160) + "”" : "")));   // whole line through tr(): a pattern in lang-es-16 keeps the quoted text as written
  };
  body.addEventListener("pointerdown", e => {
    if (!boxing || e.button) return;
    if (ov && e.target === ov) return gesture(e, ov, onFrame);
    const sh = e.target.closest && e.target.closest(".pg-sheet"); if (!sh || !sheets().includes(sh)) return;
    gesture(e, sh, onSheet(sh));
  }, sig);
  // Saved spots and boxes drawn on their pages, numbered as in the list; on the web page, those made while the layer is up.
  function boxes() {
    const ss = sheets();
    body.querySelectorAll(".pg-box:not([data-draft]), .pg-pin:not([data-draft])").forEach(x => x.remove());
    notes.forEach((n, i) => {
      const on = n.box && (n.box.f ? fresh.has(n) && ov : ss[n.box.p - 1]); if (!on) return;
      const [x, y, w, hh] = n.box.r;
      on.append(h("button", { type: "button", class: n.box.pt ? "pg-pin" : "pg-box", title: n.t, "aria-label": tr("Note " + (i + 1) + ": " + n.t),
        style: n.box.pt ? `left:${pct(x)};top:${pct(y)}` : `left:${pct(x)};top:${pct(y)};width:${pct(w)};height:${pct(hh)}`,
        onclick: () => { if (boxing) return; open = true; paint(); } }, h("b", null, String(i + 1))));
    });
  }
  body.addEventListener("pg-laid", () => { boxes(); paint(); }, sig);
  document.addEventListener("keydown", e => {
    const typing = e.target.closest && e.target.closest("textarea, input, select, [contenteditable]");
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && e.key.toLowerCase() === "x" && !typing && canBox()) { e.preventDefault(); setBox(!boxing); }
    else if (e.key === "Escape" && boxing && !(e.target.closest && e.target.closest(".pv-mk-card"))) { e.preventDefault(); e.stopPropagation(); setBox(false); }   // in the card, Esc closes the card first
  }, { capture: true, signal: ac.signal });
  box.addEventListener("keydown", e => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); add(); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); shut(); }
  });

  // Where a spot or box is, in the post: the page, then the words under it, else its place.
  function where1(n) {
    const b = n.box, [x, y, w, hh] = b.r, f = !!b.f, q = f ? defuse(n.q || "") : n.q;
    const at = f ? `In the page${b.path ? `, at ${defuse(b.path)}` : ""}` : `On page ${b.p}`;
    if (b.pt) return at + ", a point" + (q ? ` on "${q}"` : ` (${pct(x)} across, ${pct(y)} down)`);
    return at + ", a box" + (q ? ` around "${q}"` : f ? ` (from ${pct(x)} across, ${pct(y)} down, ${pct(w)} wide, ${pct(hh)} high)` : ` (from ${pct(x)} across, ${pct(y)} down)`);
  }
  async function send() {
    if (!notes.length || sending) return;
    const who = agentOf(thread), ns = notes.slice();
    const text = (who ? "@" + who + " " : "") + `Notes on ${it.name} (${where}), from the file preview in this conversation.\n\n` +
      ns.map((n, i) => `${i + 1}. ` + (n.box ? where1(n) : `On "${n.q}"`) + `: ${n.t}`).join("\n\n");
    sending = true; err = null; paint();
    try { await post("/api/post", { text, thread }); notes = notes.filter(n => !ns.includes(n)); save(K, notes); open = false; boxes(); }
    catch (e) { err = tr("Couldn't send") + ": " + e.message; }
    sending = false; paint();
  }

  const bar = h("div", { class: "pv-mk", role: "region", "aria-label": tr("Your notes") });
  function paint() {
    const pages = canBox(), web = !sheets().length && pages;
    bar.hidden = !notes.length && !err && !pages;
    if (bar.hidden) return bar.replaceChildren();
    const n = notes.length, who = agentOf(thread);
    bar.replaceChildren(...[
      open ? h("ol", { class: "pv-mk-list" }, notes.map((x, i) => h("li", null,
        h("div", { class: "pv-mk-q" }, x.box && x.box.f ? tr(x.box.pt ? "Spot on the page" : "Box on the page") + (x.box.path ? " · " + x.box.path : "") + (x.q ? ": “" + clip(x.q, 120) + "”" : "") : x.box ? tr((x.box.pt ? "Spot on page " : "Box on page ") + x.box.p) + (x.q ? ": “" + clip(x.q, 120) + "”" : "") : "“" + clip(x.q, 140) + "”"), h("div", { class: "pv-mk-t" }, x.t),
        button("Remove note", () => { notes.splice(i, 1); save(K, notes); paint(); boxes(); }, { kind: "icon", icon: "close", size: "s", cls: "pv-mk-x" })))) : null,
      err ? h("div", { class: "pv-mk-err", role: "alert" }, err) : null,
      h("div", { class: "pv-mk-row" },
        pages ? h("button", { type: "button", class: "pv-mk-box btn", "data-size": "s", "data-kind": "quiet", "aria-pressed": String(boxing), title: tr("Comment mode: click a spot or drag a box") + " (" + BOXKEY + ")", onclick: () => setBox(!boxing) },
          icon("chat"), h("span", null, tr(boxing ? "Commenting · Esc" : "Comment"))) : null,
        n ? h("button", { type: "button", class: "pv-mk-n", "aria-expanded": String(open), onclick: () => { open = !open; paint(); } },
          tr(n === 1 ? "1 note" : n + " notes"), h("span", { class: "meta" }, " · " + tr(open ? "hide" : "show")))
          : h("span", { class: "meta pv-mk-hint" }, tr(boxing ? "Click a spot or drag a box" : web ? "Press Comment, then click a spot or drag a box" : "Select words to add a note, or press Comment")),
        h("span", { class: "pv-sp" }),
        n ? button(sending ? "Sending…" : who ? "Send to " + who : "Send to this chat", send, { size: "s", kind: "primary", cls: "pv-mk-send" }) : null)].filter(Boolean));
    const sb = bar.querySelector(".pv-mk-send"); if (sb) sb.disabled = sending || !n;
  }
  paint();
  return { bar, refresh: () => { if (boxing && !canBox()) setBox(false); else if (ov && !ov.isConnected) { dropOv(); if (boxing) setBox(true); } boxes(); paint(); },
    stop: () => { ac.abort(); note.remove(); card.remove(); bar.remove(); dropOv(); clearTimeout(tm); body.removeAttribute("data-boxing"); body.querySelectorAll(".pg-box").forEach(x => x.remove()); } };
}
