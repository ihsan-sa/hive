// The quick-answer panel (the person, 11081242a38e): a waiting item on the home or a focus page opens here, on the right
// (a bottom sheet on a phone), with the question, the last few messages above it, and a box that sends through the
// chat's own path (composer.answer: same signing, outbox, sending/sent/failed and Retry). The page under it stays put.
import { h } from "../core/dom.js";
import { S, post, emit } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { displayText, isAgent, threadTitle } from "../core/model.js";
import { focusById, focusName, loadFoci, pills } from "../core/foci.js";
import { mdPlain } from "../core/format.js";
import { agentRef } from "./refs.js";
import { laterKey } from "../core/needs.js";
import { answer, actions } from "./composer.js";
import { markdown } from "./markdown.js";
import { button } from "./kit.js";
import { when } from "./when.js";

const BEFORE = 4;   // messages shown above the question: enough to answer, not the whole thread
const Q = { w: null, opener: null, sent: [], update: null, full: false };
const LONG = 280;   // characters past which the question clamps at four lines, with "Show the whole question"
const refresh = () => Q.update && Q.update();
const phone = () => typeof matchMedia === "function" && matchMedia("(max-width: 699px)").matches;

/** Open the panel for a waiting item w ({th, text, t, src, post?, focus?, key?}). opener gets focus back on close. */
export function openQuick(w, opener) {
  if (!w) return;
  const same = Q.w && keyOf(Q.w) === keyOf(w);
  Q.w = w; Q.opener = opener || (typeof document !== "undefined" ? document.activeElement : null);
  if (!same) { Q.sent = []; Q.full = false; }
  refresh();
  if (!phone()) requestAnimationFrame(() => { const el = document.querySelector(".qa-in"); if (el) el.focus(); });
  else requestAnimationFrame(() => { const el = document.querySelector(".qa-x"); if (el) el.focus(); });
}
export const quickOpen = () => !!Q.w;
/** Whether w is the item open in the panel (its opener shows aria-expanded). */
export const quickIs = w => !!(Q.w && w && keyOf(Q.w) === keyOf(w));
export function closeQuick() {
  if (!Q.w) return false;
  const o = Q.opener; Q.w = null; Q.opener = null; Q.sent = []; refresh();
  if (o && o.isConnected) o.focus();
  return true;
}
/** Put w off (the person, 1108133400fb, 110813526b10): "later" moves it off the home and its count into Later on its focus page and on
 *  Waits on you; "moot" (Not needed) drops it; "back" brings it back. Shown at once here, kept by focus.py (the "later" action), so agents see it too. */
export function setLater(w, what) {
  const key = laterKey(w), F = S.foci || (S.foci = { foci: [], tags: {} });
  const rest = (F.later || []).filter(e => e.key !== key);
  const fid = w.focus || (F.tags || {})[w.th] || null;
  F.later = what === "back" ? rest : [{ key, what, th: w.th, focus: fid, text: w.text || "", t: w.t || null, from: "person", at: Date.now() / 1e3 }, ...rest];
  if (Q.w && keyOf(Q.w) === keyOf(w) && what !== "back") closeQuick();
  emit({ foci: true });
  return post("/api/focus", { later: { what, key, th: w.th, focus: fid, text: w.text || "", t: w.t || null } }).then(() => loadFoci(true), () => loadFoci(true));
}
/** The two quiet buttons beside Answer. Each has an icon and its name as aria-label: the home's waiting rows show the icon alone (css §1), the panel shows both. */
const laterBtn = (label, ic, cls, title, fn) => { const b = button(label, e => { if (e) e.stopPropagation(); fn(); }, { size: "s", kind: "quiet", cls, title, icon: ic }); b.setAttribute("aria-label", label); return b; };
export const laterButtons = w => [
  laterBtn("Later", "clock", "nd-later", "Later: not now, move it out of the count", () => setLater(w, "later")),
  laterBtn("Not needed", "close", "nd-moot", "Not needed: it's obsolete or done, drop it", () => setLater(w, "moot")),
];
const keyOf = w => w.key || (w.post ? "p:" + w.post : w.src + ":" + w.th);

/** Where it is answered and how: {th, reply, to, quote, focus}. */
function target(w) {
  const f = w.focus ? focusById(w.focus) : null;
  if (w.src === "summary") return { th: w.th, reply: null, to: f ? f.id : null, quote: { who: f ? focusName(f) : "Focus", where: "Needs you", text: w.text }, focus: f };
  const p = w.post && S.byId[w.post];
  return { th: w.th, reply: p && isAgent(p.from) ? p : null, to: null, quote: null, focus: f, post: p || null };
}

function send(el) {
  const text = el.value.trim(); if (!text || !Q.w) return;
  const g = target(Q.w);
  const it = answer(g.th, text, { reply: g.reply, to: g.to, quote: g.quote });
  Q.sent.push(it); el.value = ""; refresh();
}

const who = p => (p.from === "person" ? "You" : p.from);
function msg(p, cls, reply) {
  return h("li", { class: ["qa-p", cls], "data-key": p.id, "data-me": p.from === "person" ? "" : null },
    h("div", { class: "qa-pm" }, h("b", null, who(p)), cls === "qa-ask" ? h("span", { class: "qa-mark" }, reply ? "The message" : "The question") : null, when(p.t)),
    h("div", { class: "qa-pb" }, markdown(displayText(p))));
}
const stateWord = it => (it.state === "failed" ? "Couldn't send" : it.state === "sent" ? "Sent" : "Sending…");

/** The panel, or null when none is open. Rendered by the home and the focus page (views/comms.js frame, views/focus.js). */
export function quickPanel(update) {
  Q.update = update;
  const w = Q.w; if (!w) return null;
  const g = target(w), d = S.threads.get(g.th), posts = d ? d.posts : [];
  const reading = !S.fresh && !posts.length;
  const at = g.post ? posts.indexOf(g.post) : -1;
  // Above the question: the BEFORE messages before it; below it, everything since, so an answer sent here lands under it.
  // A summary line or a bar line has no post: it splits at its time (11081336a101: a sent answer jumped above the
  // question and looked lost). With no time either, the last BEFORE messages go above.
  const cut = at >= 0 ? at : w.t ? (i => (i < 0 ? posts.length : i))(posts.findIndex(p => p.t > w.t)) : posts.length;
  const before = posts.slice(Math.max(0, cut - BEFORE), cut);
  const after = posts.slice(at >= 0 ? at + 1 : cut);
  const reply = w.src === "reply";   // Reply on a focus page's For you here (views/focus.js): a message, not something waiting
  const where = g.focus && !reply ? focusName(g.focus) : threadTitle(g.th);
  const head = w.src === "summary" ? "From the focus summary" : reply ? "Reply" : "Waiting on you";
  const pending = Q.sent.filter(it => !(it.id && S.byId[it.id]) || it.state !== "sent");
  const done = Q.sent.some(it => it.state === "sent");
  const qtext = g.post ? mdPlain(displayText(g.post)).replace(/^(@\S+\s+)+/, "").replace(/\s+/g, " ").trim() : String(w.text || "");
  const long = qtext.length > LONG;
  const by = g.post ? [reply ? "From " : "Asked by ", g.post.from === "person" ? "you" : agentRef(g.post.from, { dot: false, pill: true }), " · ", when(g.post.t)]
    : w.t ? ["Since ", when(w.t)] : [];
  return h("div", { class: "qa-wrap", "data-key": "qa" },
    h("div", { class: "scrim qa-scrim", "data-dim": "", "aria-hidden": "true", onclick: () => closeQuick() }),
    h("aside", { class: "qa", role: "dialog", "aria-modal": "false", "aria-labelledby": "qa-q", "data-key": "qa:" + keyOf(w), "data-src": w.src,
      onkeydown: e => { if (e.key === "Escape") { e.stopPropagation(); e.preventDefault(); closeQuick(); } } },
      h("div", { class: "qa-grab", "aria-hidden": "true" }),
      // The question heads the panel and names the dialog (review 110814055a7a: it sat below the context under a generic heading).
      h("header", { class: "qa-hd" },
        h("div", { class: "qa-ht" },
          h("p", { class: "qa-k" }, h("span", { class: "qa-kw" }, head), " · ", h("span", { class: "qa-src", title: where }, where)),
          h("h2", { id: "qa-q", class: "qa-q", "data-clamp": long && !Q.full ? "" : null }, pills(qtext)),
          long ? button(Q.full ? "Show less" : reply ? "Show the whole message" : "Show the whole question", () => { Q.full = !Q.full; refresh(); }, { size: "s", kind: "quiet", cls: "qa-more" }) : null,
          by.length ? h("p", { class: "meta qa-by" }, ...by) : null),
        button("Close", () => closeQuick(), { kind: "icon", icon: "close", cls: "qa-x", title: "Close (Esc)" })),
      h("div", { class: "qa-body" },
        reading ? h("p", { class: "meta qa-note", role: "status" }, "Reading the conversation…") : null,
        !reading && before.length ? h("ol", { class: "qa-ctx", "aria-label": "Before the question" }, before.map(p => msg(p))) : null,
        !reading && !posts.length ? h("p", { class: "meta qa-note" }, w.src === "summary" ? "This focus has no conversation yet. Your answer starts it, and reaches whoever looks after the focus." : "No messages here yet. Your answer starts the conversation.") : null,
        g.post ? h("ol", { class: "qa-ctx", "aria-label": reply ? "The message" : "The question" }, msg(g.post, "qa-ask", reply)) : null,
        after.length ? h("ol", { class: "qa-ctx", "aria-label": reply ? "Since the message" : "Since the question" }, after.map(p => msg(p))) : null,
        pending.length ? h("ul", { class: "qa-out", "aria-live": "polite" }, pending.map(it => h("li", { class: "qa-o", "data-key": "o" + it.lid, "data-state": it.state },
          h("p", { class: "qa-ot" }, it.text),
          h("p", { class: "meta qa-os" }, stateWord(it), it.state === "failed" && it.err ? ": " + it.err : "",
            it.state === "failed" ? button("Retry", () => { actions.retry(it); refresh(); }, { size: "s", cls: "qa-retry" }) : null)))) : null),
      h("div", { class: "qa-f" },
        h("textarea", { class: "input qa-in", "data-key": "in:" + keyOf(w), rows: 3, placeholder: done ? "Add something…" : reply ? "Your reply…" : "Your answer…", "aria-label": reply ? "Your reply" : "Your answer",
          onkeydown: (e, n) => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !phone()) { e.preventDefault(); send(n || e.target); } } }),
        h("div", { class: "qa-fa" },
          h("a", { class: "btn qa-open", href: threadHref(g.th), onclick: () => { Q.w = null; Q.opener = null; Q.sent = []; refresh(); } }, "Open the conversation"),
          ...(reply ? [] : laterButtons(w)),
          button("Send", e => send(e.target.closest(".qa").querySelector(".qa-in")), { kind: "primary", cls: "qa-send" })))));
}
