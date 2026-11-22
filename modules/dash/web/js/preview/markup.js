// Mark up any file shown as text in the chat's preview panel (a post's file, a project or kb file): select words, press Note (or
// Enter), write what you want, Enter again. Notes wait in a bar at the foot of the panel, kept per conversation and file so a reload
// or the file changing under them loses nothing, until Send posts them all to this conversation, naming its agent. The same way of
// working as a library document in the side panel (ui/docviewer.js); the person, 112122559e33 and 11212301f32d ("my old use cases
// too ... modernized and pulled in").
// markup(it, thread, { root, body, key, where }) → { bar, stop }: bar goes under the body; stop() ends the listeners.
import { h, tr } from "../core/dom.js";
import { S, post } from "../core/store.js";
import { partner, isAgent } from "../core/model.js";
import { button } from "../ui/kit.js";

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
  let notes = load(K), open = false, sending = false, err = null, quote = "";

  // The floating Note button by a selection, and the card it opens.
  const note = h("button", { type: "button", class: "pv-mk-go btn", "data-size": "s", "data-kind": "primary", hidden: true,
    onpointerdown: e => e.preventDefault(), onclick: () => compose() }, tr("Note"));
  const box = h("textarea", { class: "pv-mk-in", rows: 2, placeholder: tr("What should change here?"), "aria-label": tr("Your note") });
  const q = h("div", { class: "pv-mk-q" });
  const card = h("div", { class: "pv-mk-card", role: "dialog", "aria-label": tr("Add a note"), hidden: true }, q, box,
    h("div", { class: "pv-mk-row" }, h("span", { class: "meta" }, tr("Enter adds · Esc cancels")),
      button("Cancel", () => shut(), { size: "s", kind: "quiet" }), button("Add", () => add(), { size: "s", kind: "primary" })));
  root.append(note, card);

  const within = r => r && body.contains(r.commonAncestorContainer);
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
  body.addEventListener("keydown", e => { if (e.key === "Enter" && !note.hidden && !e.isComposing) { e.preventDefault(); compose(); } }, sig);

  function compose() {
    const s = getSelection(), r = s && s.rangeCount ? s.getRangeAt(0) : null;
    note.hidden = true; q.textContent = "“" + clip(quote, 160) + "”"; box.value = ""; card.hidden = false;
    if (r && within(r)) place(card, r);
    box.focus();
  }
  function shut() { card.hidden = true; box.value = ""; }
  function add() {
    const t = box.value.trim(); if (!t) return box.focus();
    notes.push({ q: clip(quote, 600), t, at: Date.now() }); save(K, notes); shut(); getSelection().removeAllRanges(); paint();
  }
  box.addEventListener("keydown", e => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); add(); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); shut(); }
  });

  async function send() {
    if (!notes.length || sending) return;
    const who = agentOf(thread), ns = notes.slice();
    const text = (who ? "@" + who + " " : "") + `Notes on ${it.name} (${where}), from the file preview in this conversation.\n\n` +
      ns.map((n, i) => `${i + 1}. On "${n.q}": ${n.t}`).join("\n");
    sending = true; err = null; paint();
    try { await post("/api/post", { text, thread }); notes = notes.filter(n => !ns.includes(n)); save(K, notes); open = false; }
    catch (e) { err = tr("Couldn't send") + ": " + e.message; }
    sending = false; paint();
  }

  const bar = h("div", { class: "pv-mk", role: "region", "aria-label": tr("Your notes") });
  function paint() {
    bar.hidden = !notes.length && !err;
    if (bar.hidden) return bar.replaceChildren();
    const n = notes.length, who = agentOf(thread);
    bar.replaceChildren(...[
      open ? h("ol", { class: "pv-mk-list" }, notes.map((x, i) => h("li", null,
        h("div", { class: "pv-mk-q" }, "“" + clip(x.q, 140) + "”"), h("div", { class: "pv-mk-t" }, x.t),
        button("Remove note", () => { notes.splice(i, 1); save(K, notes); paint(); }, { kind: "icon", icon: "close", size: "s", cls: "pv-mk-x" })))) : null,
      err ? h("div", { class: "pv-mk-err", role: "alert" }, err) : null,
      h("div", { class: "pv-mk-row" },
        h("button", { type: "button", class: "pv-mk-n", "aria-expanded": String(open), onclick: () => { open = !open; paint(); } },
          tr(n === 1 ? "1 note" : n + " notes"), h("span", { class: "meta" }, " · " + tr(open ? "hide" : "show"))),
        h("span", { class: "pv-sp" }),
        button(sending ? "Sending…" : who ? "Send to " + who : "Send to this chat", send, { size: "s", kind: "primary", cls: "pv-mk-send" }))].filter(Boolean));
    const sb = bar.querySelector(".pv-mk-send"); if (sb) sb.disabled = sending || !n;
  }
  paint();
  return { bar, stop: () => { ac.abort(); note.remove(); card.remove(); bar.remove(); clearTimeout(tm); } };
}
