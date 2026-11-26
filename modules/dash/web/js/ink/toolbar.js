// The pen's tools (thread 112515205dbf; design ~/designs/ink-chat/HANDOFF.md "Toolbar").
// Desktop: one Pen D button in the chat header; on, the tool row opens beside it and a strip under the header says what the
// pointer does now. iPad: once a Pencil has touched the chat, a floating row sits at the top right, folding to one nib after 3 s
// without a stroke. The Write page (sheet.js openFocus) docks the same row in its header.
// Keys: D pen on/off (a one-key shortcut, so it checks oneKey); while it is on, P pen, H highlighter, E eraser, S select,
// 1-3 colour, ⌘Z / ⇧⌘Z, Esc off.
import { h } from "../core/dom.js";
import { can } from "../core/store.js";
import { oneKey } from "../core/onekey.js";
import { icon } from "../ui/icons.js";
import * as K from "./store.js";

const TOOLS = [["pen", "Pen", "pen", "P"], ["hl", "Highlighter", "highlighter", "H"], ["eraser", "Eraser", "eraser", "E"], ["lasso", "Select: circle something to ask about it", "lasso", "S"]];
const COLS = [["ink", "Black"], ["blue", "Blue"], ["green", "Green"]];
const thNow = () => { const b = document.querySelector("#main .cm-msgs"), k = b && b.dataset.key || ""; return k.startsWith("m:") && k.length > 2 && can("post") ? k.slice(2) : null; };
const focusTh = () => { const f = document.querySelector(".ink-focus"); return f && f.dataset.th; };
const inkTh = () => focusTh() || thNow();

/** The tool row: Pen, Highlighter, Eraser, Select | three colours | Undo, Redo. dock: "head", "float" or "focus". */
export function toolRow(th, { dock = "head" } = {}) {
  const t = ([k, label, ic, key]) => h("button", { type: "button", class: "ink-t", "data-t": k, "aria-label": label, title: `${label} (${key})`, "aria-pressed": String(K.TOOL.t === k),
    style: `--c:${K.PENS[K.TOOL.c]}`, onclick: () => pick(k) }, icon(ic));
  const c = ([k, label], i) => h("button", { type: "button", class: "ink-sw", "data-c": k, "aria-label": label, title: `${label} (${i + 1})`, "aria-pressed": String(K.TOOL.c === k), style: `--c:${K.PENS[k]}`, onclick: () => colour(k) });
  const u = (k, label, ic, key) => h("button", { type: "button", class: "ink-t", "data-u": k, "aria-label": label, title: `${label} (${key})`, disabled: (k === "undo" ? K.canUndo(th) : K.canRedo(th)) ? null : "", onclick: () => { k === "undo" ? K.undo(inkTh() || th) : K.redo(inkTh() || th); } }, icon(ic));
  return [...TOOLS.map(t), h("hr"), ...COLS.map(c), h("hr"), u("undo", "Undo", "undo", "⌘Z"), u("redo", "Redo", "redo", "⇧⌘Z")];
}
function pick(k) {
  // the eraser and select are one-shot from the colour's point of view; picking a tool turns the pen on (a mouse needs it to draw)
  K.setTool("t", k);
  if (!K.TOOL.pen && !K.TOOL.pencil && !focusTh()) pen(true);
  wake();
}
function colour(k) { K.setTool("c", k); if (K.TOOL.t !== "pen" && K.TOOL.t !== "hl") K.setTool("t", "pen"); wake(); }

/** Pen on or off: a mouse draws on the chat only while it is on. */
export function pen(on = !K.TOOL.pen) {
  if (on === K.TOOL.pen) return;
  K.setTool("pen", on);
  if (on) document.body.setAttribute("data-ink-pen", ""); else document.body.removeAttribute("data-ink-pen");
}
/** The header's Pen button (comms.js head). */
export function penButton() {
  return h("button", { type: "button", class: "btn ink-pen", "data-size": "s", "aria-pressed": String(K.TOOL.pen), title: "Pen: draw on the messages, circle something to ask about it (D)", onclick: () => pen() },
    icon("pen"), h("span", { class: "ink-pen-l" }, "Pen"), h("kbd", {}, "D"));
}
/** The strip under the header while the pen is on: the tool row, then what the pointer does now. (The design put the row in the
 *  header itself; a 390 px header has no room for it, so it rides in this strip at every width.) */
export function penStrip(th) {
  return h("div", { class: "ink-on", "data-key": "ink-on" },
    h("div", { class: "ink-tb", "data-dock": "head", role: "toolbar", "aria-label": "Pen" }, ...toolRow(th, { dock: "head" })),
    h("span", { class: "ink-on-say", role: "status" }, "Draw anywhere in the chat · circle something to ask about it"),
    h("span", { class: "ink-on-k" }, h("kbd", {}, "D"), " or ", h("kbd", {}, "Esc"), " to read again"));
}

// Every toolbar on the page shows the same state: re-marked on each change rather than re-rendered.
function mark() {
  const th = inkTh();
  for (const b of document.querySelectorAll(".ink-t[data-t]")) { b.setAttribute("aria-pressed", String(K.TOOL.t === b.dataset.t)); b.style.setProperty("--c", K.TOOL.t === "eraser" || K.TOOL.t === "lasso" ? "var(--ink)" : K.PENS[K.TOOL.c]); }
  for (const b of document.querySelectorAll(".ink-sw[data-c]")) b.setAttribute("aria-pressed", String(K.TOOL.c === b.dataset.c));
  for (const b of document.querySelectorAll(".ink-t[data-u]")) b.disabled = !th || !(b.dataset.u === "undo" ? K.canUndo(th) : K.canRedo(th));
  for (const b of document.querySelectorAll(".ink-pen")) b.setAttribute("aria-pressed", String(K.TOOL.pen));
  if (K.TOOL.pen) document.body.setAttribute("data-ink-pen", ""); else document.body.removeAttribute("data-ink-pen");
  document.body.dataset.inkTool = K.TOOL.t;
  float();
}
addEventListener("ink:tool", mark);
addEventListener("ink:change", () => { mark(); wake(); });
addEventListener("hashchange", () => setTimeout(() => { if (!thNow()) pen(false); float(); }, 0));   // after the page has moved

// ---------- iPad: the floating row ----------
let FL = null, foldT = 0;
function float() {
  const want = K.TOOL.pencil && thNow() && !focusTh();
  if (!want) { if (FL) { FL.remove(); FL = null; } return; }
  if (FL && FL.isConnected) return;
  FL = h("div", { class: "ink-tb", "data-dock": "float", role: "toolbar", "aria-label": "Pen" }, ...toolRow(thNow(), { dock: "float" }));
  FL.addEventListener("click", e => { if (FL && FL.hasAttribute("data-collapsed")) { e.stopPropagation(); e.preventDefault(); wake(); } }, true);
  document.body.append(FL);
  wake();
}
/** Open the floating row and fold it again 3 s later. */
function wake() {
  if (!FL) return;
  FL.removeAttribute("data-collapsed"); clearTimeout(foldT);
  foldT = setTimeout(() => { if (FL && !FL.matches(":hover,:focus-within")) FL.setAttribute("data-collapsed", ""); }, 3000);
}

// ---------- keys ----------
addEventListener("keydown", e => {
  if (e.defaultPrevented || e.altKey || document.querySelector("dialog[open]")) return;
  const focus = !!focusTh(), th = inkTh();
  if (!th) return;
  const mod = e.metaKey || e.ctrlKey, typing = !!(e.target && e.target.closest && e.target.closest("input,textarea,select,[contenteditable]"));
  if (mod && !typing && (K.TOOL.pen || focus) && e.key.toLowerCase() === "z") {
    e.preventDefault(); e.shiftKey ? K.redo(th) : K.undo(th); return;
  }
  if (mod || e.key.length !== 1 && e.key !== "Escape") return;
  if (e.key === "Escape") { if (K.TOOL.pen && !focus && !typing && !document.querySelector(".ink-bubble")) { e.preventDefault(); e.stopPropagation(); pen(false); } return; }
  if (!oneKey(e)) return;
  const k = e.key.toLowerCase();
  if (k === "d" && !focus) { e.preventDefault(); pen(); return; }
  if (!K.TOOL.pen && !focus) return;
  const tool = { p: "pen", h: "hl", e: "eraser", s: "lasso" }[k];
  if (tool) { e.preventDefault(); pick(tool); return; }
  if (/^[1-3]$/.test(k)) { e.preventDefault(); colour(COLS[+k - 1][0]); }
}, true);
