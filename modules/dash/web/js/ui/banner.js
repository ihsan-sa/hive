// One banner component for the whole app (R7 step 5): a line under the bar that says what is wrong in words, with the
// one button that helps. Tones: bad (lost contact, the hive stopped), info (a newer version), warn. Nothing else on the
// page moves when one shows: it takes its own row in the shell, above the page.
// Every banner can be put away (friction pass 11-25, design §9): a × on the row, or the note's own Later / No thanks. A
// dismissed banner stays gone until its words change, kept per key in localStorage (hive.banner.<key>). A note (info
// tone) also leaves on its own BANNER_AGE after it first showed. "Lost contact" has no × and doesn't age: it leaves when
// contact is back. A state band (the hive stopped, sign-in) has a × but doesn't age, since what it says is still true.
import { h, tr } from "../core/dom.js";

export const BANNER_AGE = 6 * 3600e3;   // = --banner-age in tokens.css
const KEY = k => "hive.banner." + k;
function read(k) { try { return JSON.parse(localStorage.getItem(KEY(k)) || "null"); } catch { return null; } }
function write(k, v) { try { localStorage.setItem(KEY(k), JSON.stringify(v)); } catch { /* private mode: it just shows again */ } }

/** banner({tone, text, actions, cls, key, lead, dismiss, age}) → .banner[data-tone], or null when dismissed or aged out.
 *  `text` is a string or nodes; the first <b> reads as the lead. `lead` goes before the text (a status dot).
 *  dismiss: false for lost contact and for a note with its own Later button. age: true (the default for info) lets it leave on its own after BANNER_AGE. */
export function banner({ tone = "info", text, actions = [], cls, key = "band", lead = null, dismiss = true, age = tone === "info", onDismiss } = {}) {
  const t = h("span", { class: "banner-t" }, text), words = t.textContent.trim();
  if (dismiss || age) {
    let s = read(key);
    if (!s || s.t !== words) { s = { t: words, seen: Date.now() }; write(key, s); }   // new words: shown again, and its clock starts over
    if (s.gone || (age && Date.now() - s.seen > BANNER_AGE)) return null;
  }
  const row = h("div", { class: "banner" + (cls ? " " + cls : ""), role: "status", "data-tone": tone, "data-key": key }, lead, t, ...[].concat(actions).filter(Boolean));
  if (dismiss) row.append(h("button", { type: "button", class: "banner-x", "aria-label": tr("Dismiss"), title: tr("Dismiss"), onclick: (e, x) => {
    write(key, { ...(read(key) || { t: words, seen: Date.now() }), gone: Date.now() });
    ((x && x.closest(".banner")) || row).remove(); if (onDismiss) onDismiss();   // the row on screen: the shell morphs new nodes into the old ones
  } }, h("span", { "aria-hidden": "true" }, "×")));
  return row;
}
