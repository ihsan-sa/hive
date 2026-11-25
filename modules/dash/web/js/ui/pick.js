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
const ON = new Set();      // pages whose drawn messages keep their own cache (the conversation): told to redraw them
/** fn runs when a choice is held, undone, sent or failed, before the pages redraw. */
export const onPick = fn => ON.add(fn);
const redraw = () => { for (const fn of ON) fn(); emit({ pick: true }); };

/** Numbered options in a question: "1. …", "2) …", "Option 2: …". At most three, starting at 1. [{n, words}] */
export function choicesIn(text) {
  const out = new Map();
  for (const m of String(text || "").matchAll(/^[ \t>*-]*(?:\*\*)?(?:\(?([1-9])[.):]|option ([1-9])\b[:.]?)\s+(\S[^\n]*)/gim)) {
    const n = +(m[1] || m[2]);
    if (!out.has(n)) out.set(n, mdPlain(m[3]).replace(/\*\*/g, "").replace(/\s+/g, " ").trim());
  }
  const ns = [...out.keys()].sort((a, b) => a - b);
  if (ns[0] !== 1 || ns[1] !== 2) return [];
  return ns.filter((x, i) => x === i + 1).slice(0, 3).map(n => ({ n, words: out.get(n) }));
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
function start(p, n) {
  const was = HOLD.get(p.id); if (was && was.timer) clearTimeout(was.timer);
  const x = { n, th: p.th, at: Date.now(), timer: 0, it: null };
  x.timer = setTimeout(() => { x.timer = 0; x.it = answer(p.th, words(n), { reply: p }); redraw(); }, PICK_MS);
  HOLD.set(p.id, x); redraw();
  // focus moves to Undo, so a keyboard press can take it back (the button it was on is gone)
  requestAnimationFrame(() => { const u = document.querySelector(`[data-pick="${CSS.escape(p.id)}"] .pk-undo`); if (u) u.focus(); });
}
function undo(p) {
  const x = HOLD.get(p.id); if (!x || !x.timer) return;
  clearTimeout(x.timer); HOLD.delete(p.id); redraw();
  requestAnimationFrame(() => { const b = document.querySelector(`[data-pick="${CSS.escape(p.id)}"] .pk-b`); if (b) b.focus(); });
}

/** The choices under question post p, or the one line that stands for the choice once made. cls: the place's own class. null: no choices. */
export function pickBar(p, { cls = "" } = {}) {
  if (!p) return null;
  const x = HOLD.get(p.id);
  if (x) {
    const it = x.it, left = Math.max(0, PICK_MS - (Date.now() - x.at));
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
