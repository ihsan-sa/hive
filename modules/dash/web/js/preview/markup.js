// Mark up any file shown as text in the chat's preview panel (a post's file, a project or kb file): select words, press Note (or
// Enter), write what you want, Enter again. Notes wait in a bar at the foot of the panel, kept per conversation and file so a reload
// or the file changing under them loses nothing, until Send posts them all to this conversation, naming its agent. The same way of
// working as a library document in the side panel (ui/docviewer.js); the person, 112122559e33 and 11212301f32d ("my old use cases
// too ... modernized and pulled in").
// A note shown as pages (paged.js) can also be boxed: Box (⌘⇧X / Ctrl+Shift+X, Esc cancels), drag over the page, write the note. The
// box stays drawn on the page, numbered, and the post says which page and quotes the words under it (the person, 1122104123fb).
// markup(it, thread, { root, body, key, where }) → { bar, stop, refresh }: bar goes under the body; stop() ends the listeners;
// refresh() redraws the boxes after the body was drawn again.
import { h, tr } from "../core/dom.js";
import { S, post } from "../core/store.js";
import { partner, isAgent } from "../core/model.js";
import { button, icon } from "../ui/kit.js";

const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const BOXKEY = isMac ? "⌘⇧X" : "Ctrl+Shift+X";
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
  function shut() { card.hidden = true; box.value = ""; if (pend) { pend.el.remove(); pend = null; } }
  function add() {
    const t = box.value.trim(); if (!t) return box.focus();
    if (pend) { notes.push({ q: clip(pend.q, 600), box: { p: pend.p, r: pend.r }, t, at: Date.now() }); pend.el.remove(); pend = null; }
    else notes.push({ q: clip(quote, 600), t, at: Date.now() });
    save(K, notes); shut(); getSelection().removeAllRanges(); paint(); boxes();
  }

  // ---- boxes on the pages ----
  function setBox(on) {
    boxing = !!on && sheets().length > 0; body.toggleAttribute("data-boxing", boxing); paint();
    if (boxing) { note.hidden = true; getSelection().removeAllRanges(); }
  }
  // The words under a box: every line of text whose middle falls inside it.
  function under(sh, R) {
    const out = [], c = sh.querySelector(".pg-c"); if (!c) return "";
    const w = document.createTreeWalker(c, NodeFilter.SHOW_TEXT), rg = document.createRange();
    for (let n; (n = w.nextNode());) {
      if (!n.data.trim()) continue;
      rg.selectNodeContents(n);
      for (const b of rg.getClientRects()) {
        const x = (b.left + b.right) / 2, y = (b.top + b.bottom) / 2;
        if (x >= R.left && x <= R.right && y >= R.top && y <= R.bottom) { out.push(n.data); break; }
      }
    }
    return out.join(" ").replace(/\s+/g, " ").trim();
  }
  body.addEventListener("pointerdown", e => {
    if (!boxing || e.button) return;
    const sh = e.target.closest && e.target.closest(".pg-sheet"); if (!sh || !sheets().includes(sh)) return;
    e.preventDefault();
    const S0 = sh.getBoundingClientRect(), x0 = e.clientX, y0 = e.clientY;
    const el = h("div", { class: "pg-box", "data-draft": "" }); sh.append(el);
    let r = [0, 0, 0, 0];
    const mv = ev => {
      const x1 = Math.max(S0.left, Math.min(S0.right, ev.clientX)), y1 = Math.max(S0.top, Math.min(S0.bottom, ev.clientY));
      r = [(Math.min(x0, x1) - S0.left) / S0.width, (Math.min(y0, y1) - S0.top) / S0.height, Math.abs(x1 - x0) / S0.width, Math.abs(y1 - y0) / S0.height];
      Object.assign(el.style, { left: pct(r[0]), top: pct(r[1]), width: pct(r[2]), height: pct(r[3]) });
    };
    const end = () => { removeEventListener("pointermove", mv); removeEventListener("pointerup", up); removeEventListener("pointercancel", cancel); };
    const cancel = () => { end(); el.remove(); };   // the touch turned into a scroll, or the pointer was lost
    const up = () => {
      end();
      if (r[2] * S0.width < 8 || r[3] * S0.height < 8) { el.remove(); return; }
      const R = { left: S0.left + r[0] * S0.width, top: S0.top + r[1] * S0.height }; R.right = R.left + r[2] * S0.width; R.bottom = R.top + r[3] * S0.height;
      setBox(false);
      if (pend) pend.el.remove();
      pend = { el, p: +sh.dataset.p || 1, r: r.map(v => Math.round(v * 10000) / 10000), q: under(sh, R) };
      q.textContent = tr("Box on page " + pend.p + (pend.q ? ": “" + clip(pend.q, 160) + "”" : ""));   // whole line through tr(): a pattern in lang-es-16 keeps the quoted text as written
      box.value = ""; card.hidden = false;
      const er = el.getBoundingClientRect(), RR = root.getBoundingClientRect();
      card.style.left = Math.max(8, Math.min(RR.width - card.offsetWidth - 8, er.left - RR.left)) + "px";
      card.style.top = Math.max(8, Math.min(RR.height - card.offsetHeight - 8, er.bottom - RR.top + 6)) + "px";
      box.focus();
    };
    addEventListener("pointermove", mv); addEventListener("pointerup", up); addEventListener("pointercancel", cancel);
    try { sh.setPointerCapture(e.pointerId); } catch (x) {}
  }, sig);
  // Saved boxes drawn on their pages, numbered as in the list.
  function boxes() {
    const ss = sheets();
    body.querySelectorAll(".pg-box:not([data-draft])").forEach(x => x.remove());
    notes.forEach((n, i) => {
      const sh = n.box && ss[n.box.p - 1]; if (!sh) return;
      const [x, y, w, hh] = n.box.r;
      sh.append(h("button", { type: "button", class: "pg-box", title: n.t, "aria-label": tr("Note " + (i + 1) + ": " + n.t), style: `left:${pct(x)};top:${pct(y)};width:${pct(w)};height:${pct(hh)}`,
        onclick: () => { if (boxing) return; open = true; paint(); } }, h("b", null, String(i + 1))));
    });
  }
  body.addEventListener("pg-laid", () => { boxes(); paint(); }, sig);
  document.addEventListener("keydown", e => {
    const typing = e.target.closest && e.target.closest("textarea, input, select, [contenteditable]");
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && e.key.toLowerCase() === "x" && !typing && sheets().length) { e.preventDefault(); setBox(!boxing); }
    else if (e.key === "Escape" && boxing) { e.preventDefault(); e.stopPropagation(); setBox(false); }
  }, { capture: true, signal: ac.signal });
  box.addEventListener("keydown", e => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); add(); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); shut(); }
  });

  async function send() {
    if (!notes.length || sending) return;
    const who = agentOf(thread), ns = notes.slice();
    const text = (who ? "@" + who + " " : "") + `Notes on ${it.name} (${where}), from the file preview in this conversation.\n\n` +
      ns.map((n, i) => `${i + 1}. ` + (n.box ? `On page ${n.box.p}, a box${n.q ? ` around "${n.q}"` : ` (from ${pct(n.box.r[0])} across, ${pct(n.box.r[1])} down)`}` : `On "${n.q}"`) + `: ${n.t}`).join("\n\n");
    sending = true; err = null; paint();
    try { await post("/api/post", { text, thread }); notes = notes.filter(n => !ns.includes(n)); save(K, notes); open = false; boxes(); }
    catch (e) { err = tr("Couldn't send") + ": " + e.message; }
    sending = false; paint();
  }

  const bar = h("div", { class: "pv-mk", role: "region", "aria-label": tr("Your notes") });
  function paint() {
    const pages = sheets().length > 0;
    bar.hidden = !notes.length && !err && !pages;
    if (bar.hidden) return bar.replaceChildren();
    const n = notes.length, who = agentOf(thread);
    bar.replaceChildren(...[
      open ? h("ol", { class: "pv-mk-list" }, notes.map((x, i) => h("li", null,
        h("div", { class: "pv-mk-q" }, x.box ? tr("Box on page " + x.box.p) + (x.q ? ": “" + clip(x.q, 120) + "”" : "") : "“" + clip(x.q, 140) + "”"), h("div", { class: "pv-mk-t" }, x.t),
        button("Remove note", () => { notes.splice(i, 1); save(K, notes); paint(); boxes(); }, { kind: "icon", icon: "close", size: "s", cls: "pv-mk-x" })))) : null,
      err ? h("div", { class: "pv-mk-err", role: "alert" }, err) : null,
      h("div", { class: "pv-mk-row" },
        pages ? h("button", { type: "button", class: "pv-mk-box btn", "data-size": "s", "data-kind": "quiet", "aria-pressed": String(boxing), title: tr("Draw a box on a page") + " (" + BOXKEY + ")", onclick: () => setBox(!boxing) },
          icon("marquee"), h("span", null, tr(boxing ? "Drag on a page · Esc" : "Box"))) : null,
        n ? h("button", { type: "button", class: "pv-mk-n", "aria-expanded": String(open), onclick: () => { open = !open; paint(); } },
          tr(n === 1 ? "1 note" : n + " notes"), h("span", { class: "meta" }, " · " + tr(open ? "hide" : "show")))
          : h("span", { class: "meta pv-mk-hint" }, tr("Select words to add a note, or draw a box")),
        h("span", { class: "pv-sp" }),
        n ? button(sending ? "Sending…" : who ? "Send to " + who : "Send to this chat", send, { size: "s", kind: "primary", cls: "pv-mk-send" }) : null)].filter(Boolean));
    const sb = bar.querySelector(".pv-mk-send"); if (sb) sb.disabled = sending || !n;
  }
  paint();
  return { bar, refresh: () => { if (boxing && !sheets().length) setBox(false); boxes(); paint(); },
    stop: () => { ac.abort(); note.remove(); card.remove(); bar.remove(); clearTimeout(tm); body.removeAttribute("data-boxing"); body.querySelectorAll(".pg-box").forEach(x => x.remove()); } };
}
