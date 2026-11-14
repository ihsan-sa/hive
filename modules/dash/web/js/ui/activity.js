// What the setup pages are doing, in words: every click gets a row at once, and the row says how it went. Rows are written by the code that
// acts, from fixed text and the server's answer. Keys, sign-in codes and a sign-in's raw terminal output never go in one.
import { h } from "../core/dom.js";
import { LOC } from "../core/format.js";
import { copyButton } from "./refs.js";

const A = { rows: [], n: 0, timer: 0, update: null };
const open = id => A.rows.find(r => r.id === id && r.state === "busy");
export const isBusy = id => !!open(id);

// While something runs, redraw once a second so its time moves: a row that counts is how you tell slow from stuck.
function ticking(update) {
  A.update = update;
  if (A.timer) return;
  A.timer = setInterval(() => {
    if (A.rows.some(r => r.state === "busy")) return A.update && A.update();
    clearInterval(A.timer); A.timer = 0;
  }, 1000);
}
function add(id, state, text) {
  A.rows.push({ n: ++A.n, id, state, text, at: Date.now(), end: state === "busy" ? 0 : Date.now() });
  if (A.rows.length > 40) A.rows.shift();
}
/** A row that runs until finish(). A new one under the same id ends the old one. */
export function begin(id, text, update) {
  const was = open(id);
  if (was) { was.state = "info"; was.end = Date.now(); }
  add(id, "busy", text); ticking(update); update();
}
/** End the running row with this id, or add a finished one if there's none. state: ok, bad or info. */
export function finish(id, state, text, update) {
  const r = open(id);
  if (r) Object.assign(r, { state, text, end: Date.now() }); else add(id, state, text);
  update();
}
export function note(state, text, update) { add(null, state, text); update(); }
/** Run fn under a row. A second click while it runs is dropped (false). A failure goes in the row and is thrown on to the caller. */
export async function act(id, text, fn, update, done) {
  if (isBusy(id)) return false;
  begin(id, text, update);
  try { const r = await fn(); finish(id, "ok", done || text, update); return r === undefined ? true : r; }
  catch (e) { finish(id, "bad", `${text}: ${e.message}`, update); throw e; }
}

const long = ms => { const s = Math.max(0, Math.round(ms / 1000)); return s < 60 ? s + "s" : Math.floor(s / 60) + "m " + (s % 60) + "s"; };
const clock = t => new Date(t).toLocaleTimeString(LOC, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
const when = r => r.state === "busy" ? long(Date.now() - r.at) : clock(r.end) + (r.end - r.at >= 1000 ? " · " + long(r.end - r.at) : "");
const plain = () => A.rows.map(r => `${clock(r.at)} ${r.state} ${r.text}`).join("\n");

/** The panel, newest first. hideEmpty: nothing at all until there's something to say. */
export function activityLog({ hideEmpty } = {}) {
  if (!A.rows.length && hideEmpty) return null;
  return h("aside", { class: "alog", "data-key": "activity", "aria-label": "What's happening" },
    h("div", { class: "alog-h" }, h("b", null, "What's happening"), A.rows.length ? copyButton(plain()) : null),
    A.rows.length ? h("ol", { class: "alog-l", role: "log", "aria-live": "polite" }, [...A.rows].reverse().map(r => h("li", { class: "alog-r", "data-key": "a" + r.n, "data-state": r.state },
      h("span", { class: "alog-i", "aria-hidden": "true" }), h("span", { class: "alog-t" }, r.text), h("span", { class: "alog-w", "aria-hidden": "true" }, when(r)))))
      : h("p", { class: "alog-e" }, "Nothing yet. Each thing you click shows up here, with how it went."));
}
