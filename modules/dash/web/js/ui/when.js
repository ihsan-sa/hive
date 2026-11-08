// A quiet time for a line: "12m ago", with the exact date and time on hover (title) and for machines (datetime).
import { h } from "../core/dom.js";
import { ago } from "../core/format.js";

export const exact = t => new Date(t).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
/** <time> for ms t, or null when there is no time. cls adds a class (e.g. "t"); word goes before it ("updated 12m ago") and on the hover. */
export function when(t, cls, word) {
  if (!t || !isFinite(t)) return null;
  return h("time", { class: ["meta", "when", cls], datetime: new Date(t).toISOString(), title: (word ? word[0].toUpperCase() + word.slice(1) + " " : "") + exact(t) }, word ? word + " " + ago(t) : ago(t));
}
