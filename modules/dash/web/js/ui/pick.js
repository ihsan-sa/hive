// One-tap answer (friction pass, thread friction-redesign, the person's 11242341532d): a question's numbered choices are buttons that send
// "Go with N." at once, through the composer's own path (composer.answer: same signing, outbox, sending/sent/failed and Retry), after
// 10 seconds in which Undo takes it back. Then the choices fold to one line: "You: Go with 1." Used under the ask in the conversation
// (ui/message.js) and on the home's waiting rows (views/comms.js homeNeeds). The hold is kept here by question, so both places and any
// re-render show the same countdown; the bar's CSS animation starts at a negative delay, so a redraw doesn't restart it.
import { h, tr } from "../core/dom.js";
import { S, emit } from "../core/store.js";
import { mdPlain } from "../core/format.js";
import { answer, actions } from "./composer.js";
import { button } from "./kit.js";

export const PICK_MS = 10000;
const HOLD = new Map();   // question post id -> { n, th, at, timer, it }
// A hold also lives in localStorage until it is sent or undone, so a reload (or a closed tab) in the 10 seconds neither loses the
// answer nor sends it twice: the next load sends one whose time is up and takes over the countdown of one whose time isn't (review a937f).
const KEPT = "hive.pick.hold";
const kept = () => { try { return JSON.parse(localStorage.getItem(KEPT) || "{}") || {}; } catch { return {}; } };
const keep = (id, v) => { try { const all = kept(); if (v) all[id] = v; else delete all[id]; localStorage.setItem(KEPT, JSON.stringify(all)); } catch {} };
const asReply = p => ({ id: p.id, from: p.from, text: p.text, th: p.th });
const ON = new Set();      // pages whose drawn messages keep their own cache (the conversation): told to redraw them
/** fn runs when a choice is held, undone, sent or failed, before the pages redraw. */
export const onPick = fn => ON.add(fn);
const redraw = () => { for (const fn of ON) fn(); emit({ pick: true }); };

/** Numbered options in a question: "1. …", "2) …", "Option 2: …". [{n, words}], or [] when there is no choice to make.
 *  Only a list the post asks you to pick from: the text has a question mark or says pick / choose / which / option. Lines in a quote (>) or a
 *  code fence don't count. With several lists, the one nearest the last question (a report's list above "Which next?" and its two options
 *  under it: the options). The list runs 1, 2 (, 3) in order; four or more is a list to read, not to tap, so no buttons (review a937f). */
const ITEM = /^[ \t]*(?:[*-][ \t]+)?(?:\*\*)?(?:\(?([1-9])[.):]|option ([1-9])\b[:.]?)(?:\*\*)?[ \t]+(\S.*)$/i;
export function choicesIn(text) {
  const lines = [], all = String(text || "").split("\n");
  for (let i = 0, fence = false; i < all.length; i++) {
    if (/^[ \t]*(```|~~~)/.test(all[i])) { fence = !fence; lines.push(""); continue; }
    lines.push(fence || /^[ \t]*>/.test(all[i]) ? "\u0000" : all[i]);   // a quoted or fenced line ends a list and is never an item
  }
  const flat = lines.join("\n");
  // "Do you want:", "Would you like:", "Shall I:", "Should I:" over a list ask too, with no "?" (a0ad9's re-check 112504460309, ad748 11250449f4d2)
  const WANT = /\b(?:(?:do|would) you (?:want|like|prefer)|(?:shall|should) I)\b[^.!?\n]{0,40}:[ \t]*$/i;
  if (!/\?/.test(flat.replace(/https?:\/\/\S+/g, "")) && !/\b(pick|choose|which|options?)\b/i.test(flat) && !lines.some(l => WANT.test(l))) return [];
  const lists = []; let cur = null;
  lines.forEach((l, i) => {
    const m = l.match(ITEM);
    if (m) { if (!cur) lists.push(cur = { a: i, b: i, items: [] }); cur.b = i; cur.items.push({ n: +(m[1] || m[2]), words: mdPlain(m[3]).replace(/\*\*/g, "").replace(/\s+/g, " ").trim() }); return; }
    if (cur && (l.trim() === "" || /^[ \t]+\S/.test(l)) && l !== "\u0000") return;   // a blank or indented line goes on with the list
    cur = null;
  });
  // A list counts only when it is the question's own (review a0ad9 #1): the line just before it asks (its last sentence has a "?", or says
  // pick / choose / option), or the first sentence just after it asks which / pick / choose / prefer / option, or "1 or 2?". A list under "Done", "Fixed", "Steps" or
  // "For you" is a report or steps to do, never a choice: "For you: two steps?" with one tap would answer in place of doing the step.
  const NOT = /^[ \t*#_]*(done|fixed|finished|steps?|for you|what (?:i|we) (?:did|changed|checked|found)|which means)\b/i;
  const noUrl = l => l.replace(/https?:\/\/\S+/g, "");
  const asks = L => {
    let i = L.a - 1; while (i >= 0 && lines[i].trim() === "") i--;
    const before = i >= 0 && lines[i] !== "\u0000" ? noUrl(lines[i]) : "";
    const last = before.trim().split(/(?<=[.!?])\s+/).pop() || "";
    if (before && !NOT.test(before) && !NOT.test(last) && (/\?/.test(last) || /\b(pick|choose)\b(?!\s+(?:it\s+|them\s+)?up)/i.test(last) || /^\W*options\b/i.test(last) || WANT.test(last))) return true;   // "Pick one:", "Options for the person:", not "pick it up" or "(option A)" 
    let j = L.b + 1; while (j < lines.length && (lines[j].trim() === "" || /^[ \t]+\S/.test(lines[j]))) j++;
    const after = j < lines.length && lines[j] !== "\u0000" ? noUrl(lines[j]) : "";
    const first = after.trim().split(/(?<=[.!?])\s+/)[0] || "";   // "Which do you prefer?" or "1 or 2?", not "Can you do step 1, or …?"
    return !!first && !NOT.test(first) && /\?/.test(first) && (/\b(which|pick|choose|prefer|option)\b/i.test(first) || /^\W*[1-3]\b|\b[1-3]\s*(or|,)\s*[1-3]\b/i.test(first));
  };
  for (let k = lists.length - 1; k >= 0; k--) if (!asks(lists[k])) lists.splice(k, 1);
  if (!lists.length) return [];
  const qs = lines.map((l, i) => (l !== "\u0000" && /\?/.test(l.replace(/https?:\/\/\S+/g, "")) ? i : -1)).filter(i => i >= 0), q = qs[qs.length - 1];
  const far = L => (q == null ? -L.a : Math.min(Math.abs(q - L.a), Math.abs(q - L.b)));
  const L = lists.reduce((best, x) => (far(x) <= far(best) ? x : best));
  const ns = L.items.map(x => x.n);
  if (ns.length < 2 || ns.length > 3 || ns.some((n, i) => n !== i + 1)) return [];
  return L.items;
}
const words = n => `Go with ${n}.`;
const short = s => (s.length > 34 ? s.slice(0, 33).replace(/\s+\S*$/, "") + "…" : s);

/** Has a choice been made on this question here (held, sending, or sent and not on the board yet)? */
export function picked(p) {
  const x = p && HOLD.get(p.id);
  if (!x) return false;
  if (x.it && x.it.state === "sent" && x.it.id && S.byId[x.it.id]) return "posted";
  return true;
}
// Send the held answer once: only the tab that still finds it kept sends it (another tab, or an Undo there, may have taken it).
// Two tabs whose timers fire together could both read it kept before either's delete reaches the other (review a0ad9 #4), so the read and
// delete run under a cross-tab Web Lock: a tab that finds the lock taken stands down, and the sender holds it a few seconds so its delete
// has reached every tab before the lock frees. Without Web Locks (an old browser) it falls back to the storage check alone.
const LOCK_HOLD_MS = 3000;
function send(id, x, r) {
  x.timer = 0;
  const drop = () => { if (HOLD.get(id) === x) HOLD.delete(id); redraw(); };
  const go = () => {
    if (!kept()[id]) return drop();
    keep(id, null); x.it = answer(x.th, words(x.n), { reply: r }); redraw();
  };
  if (!(navigator.locks && navigator.locks.request)) return go();
  let ran = false;
  navigator.locks.request("hive.pick." + id, { ifAvailable: true }, async lock => {
    ran = true; if (!lock) return drop();
    go(); await new Promise(res => setTimeout(res, LOCK_HOLD_MS));
  }).catch(() => { if (!ran) go(); });   // the lock itself refused (not a secure page): the storage check alone
}
// The time left, never more than the 10 seconds: a clock set back since the hold was made (or the preview's own clock) can't stretch it.
const leftOf = x => { if (x.at > Date.now()) x.at = Date.now(); return Math.max(0, PICK_MS - (Date.now() - x.at)); };
function hold(id, x, r) { x.timer = setTimeout(() => send(id, x, r), leftOf(x)); HOLD.set(id, x); }
function start(p, n) {
  const was = HOLD.get(p.id); if (was && was.timer) clearTimeout(was.timer);
  const x = { n, th: p.th, at: Date.now(), timer: 0, it: null }, r = asReply(p);
  keep(p.id, { n, th: p.th, at: x.at, r }); hold(p.id, x, r); redraw();
  // focus moves to Undo, so a keyboard press can take it back (the button it was on is gone)
  requestAnimationFrame(() => { const u = document.querySelector(`[data-pick="${CSS.escape(p.id)}"] .pk-undo`); if (u) u.focus(); });
}
function undo(p) {
  const x = HOLD.get(p.id); if (!x || !x.timer) return;
  clearTimeout(x.timer); HOLD.delete(p.id); keep(p.id, null); redraw();
  requestAnimationFrame(() => { const b = document.querySelector(`[data-pick="${CSS.escape(p.id)}"] .pk-b`); if (b) b.focus(); });
}

// On load: every kept hold comes back, counting down from when it was made; one already past its 10 seconds goes now.
for (const [id, v] of Object.entries(kept())) {
  if (!v || !v.r || !v.n || typeof v.at !== "number" || Date.now() - v.at > 864e5) { keep(id, null); continue; }   // malformed or a day old: drop it
  hold(id, { n: v.n, th: v.th, at: v.at, timer: 0, it: null }, v.r);
}
// Undone or sent in another tab: stop this tab's countdown for it.
addEventListener("storage", e => {
  if (e.key !== KEPT) return;
  const all = kept();
  for (const [id, x] of HOLD) if (x.timer && !all[id]) { clearTimeout(x.timer); HOLD.delete(id); redraw(); }
});

/** The choices under question post p, or the one line that stands for the choice once made. cls: the place's own class. null: no choices. */
export function pickBar(p, { cls = "" } = {}) {
  if (!p) return null;
  const x = HOLD.get(p.id);
  if (x) {
    const it = x.it, left = leftOf(x);
    if (!it) return h("div", { class: ["pk", "pk-hold", cls], "data-pick": p.id, "data-key": "pk:" + p.id, onclick: e => e.stopPropagation() },
      h("span", { class: "pk-bar", "aria-hidden": "true", style: `animation-duration:${PICK_MS}ms;animation-delay:-${PICK_MS - left}ms` }),
      h("span", { class: "pk-t", role: "status" }, tr("Sending"), ` “${words(x.n)}”`),
      button("Undo", () => undo(p), { size: "s", kind: "quiet", cls: "pk-undo" }));
    const failed = it.state === "failed";
    return h("div", { class: ["pk", "pk-done", cls], "data-pick": p.id, "data-key": "pk:" + p.id, "data-state": it.state, onclick: e => e.stopPropagation() },
      h("span", { class: "pk-t", role: "status" }, h("b", null, tr("You")), ": ", words(x.n),
        h("span", { class: "meta" }, failed ? ` · ${tr("Couldn't send")}${it.err ? ": " + it.err : ""}` : it.state === "sent" ? "" : ` · ${tr("Sending…")}`)),
      failed ? button("Retry", () => { actions.retry(it); redraw(); }, { size: "s", cls: "pk-retry" }) : null);
  }
  const cs = choicesIn(p.disp || p.text);
  if (!cs.length) return null;
  return h("div", { class: ["pk", cls], "data-pick": p.id, "data-key": "pk:" + p.id, role: "group", "aria-label": tr("Answer with one tap"), onclick: e => e.stopPropagation() },
    cs.map(c => {
      const b = button(c.words ? `${c.n} · ${short(c.words)}` : words(c.n), () => start(p, c.n), { size: "s", cls: "pk-b", title: `${tr("Sends")} “${words(c.n)}” ${tr("now")}${c.words ? ": " + c.words : ""}. ${tr("10 seconds to undo.")}` });
      b.setAttribute("data-n", String(c.n));
      return b;
    }));
}
