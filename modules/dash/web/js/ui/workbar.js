// Where the work went (the person, 1110182802a0): the home's one look at the day and the week by focus. Two segmented bars over the
// same parts, the main focuses and Other, from the shares the focus page already shows (core/foci.js). Work is runs and agent messages.
// Six colours at most, always in this order, because the dataviz validator passes them only as neighbours (light and dark): an eighth
// or a shuffled order puts two that colour-blind eyes can't part side by side. Parts sort by id, not size, so both bars read alike
// and a focus keeps its colour while the main ones stay the main ones. Simple view shows no bars (as on the focus page).
import { h } from "../core/dom.js";
import { href } from "../core/router.js";
import { simpleOn } from "../core/simple.js";
import { focusName, pct, usd } from "../core/foci.js";

const SLOTS = [["#2872d0", "#3987e5", "#fff", "#0b0b0b"], ["#eb6834", "#d95926", "#0b0b0b", "#0b0b0b"], ["#1baf7a", "#199e70", "#0b0b0b", "#0b0b0b"],   // slot 1 light: white on it measures 4.76:1 (was #2a78d6, 4.42:1; drive_app_contrast)
  ["#eda100", "#c98500", "#0b0b0b", "#0b0b0b"], ["#e87ba4", "#d55181", "#0b0b0b", "#0b0b0b"], ["#6250d6", "#9085e9", "#fff", "#0b0b0b"]];
const WINDOWS = [["day", "Last 24 hours"], ["week", "Last 7 days"]];
const MAIN = 6, LABEL_FROM = 0.07;   // under 7% a share's digits don't fit its block on a phone: it stays in the tooltip
const work = b => (b ? (b.runs || 0) + (b.posts || 0) : 0);

/** The bars' parts from /api/foci's activity: the MAIN focuses with the largest shares over both windows, and Other for the rest
 *  (smaller focuses and conversations in no focus). null until there is work to show. */
export function workParts(a) {
  if (!a || !a.total || !a.foci) return null;
  const total = w => work(a.total[w]);
  if (!total("day") && !total("week")) return null;
  const share = (f, w) => (total(w) ? work(f[w]) / total(w) : 0);
  const main = Object.entries(a.foci).filter(([, f]) => work(f.day) + work(f.week) > 0)
    .sort(([i, x], [j, y]) => (share(y, "day") + share(y, "week")) - (share(x, "day") + share(x, "week")) || i.localeCompare(j))
    .slice(0, MAIN).map(([id]) => id).sort();
  const rest = Object.keys(a.foci).filter(id => !main.includes(id));
  const parts = main.map((id, i) => ({ id, slot: i, by: w => a.foci[id][w] }));
  const sum = (w, k) => [a.misc, ...rest.map(id => a.foci[id])].reduce((n, f) => n + ((f && f[w] && f[w][k]) || 0), 0);
  const other = { id: null, by: w => ({ runs: sum(w, "runs"), posts: sum(w, "posts"), cost: sum(w, "cost") }), rest: rest.filter(id => work(a.foci[id].day) + work(a.foci[id].week) > 0) };
  return { parts: [...parts, other], total };
}

const nameOf = p => (p.id ? focusName(p.id) : "Other");
const fill = p => (p.id ? `background:light-dark(${SLOTS[p.slot][0]},${SLOTS[p.slot][1]});color:light-dark(${SLOTS[p.slot][2]},${SLOTS[p.slot][3]})` : "background:var(--other);color:var(--ink)");
const hasCost = b => usd((b && b.cost) || 0) !== usd(0);
const commas = t => t.replace(/\B(?=(\d{3})+$)/g, ",");   // only whole figures reach four digits: usd drops the cents from $100
const amount = b => { const n = b.posts || 0; return `${commas(String(n))} ${n === 1 ? "message" : "messages"}${hasCost(b) ? ` and ${commas(usd(b.cost))}` : ""}`; };

function segment(p, w, label, total) {
  const b = p.by(w), d = work(b) / total; if (!d) return null;
  const rest = p.rest && p.rest.length ? `Also in it: ${p.rest.map(focusName).join(", ")}, and conversations in no focus` : p.id ? null : "Conversations in no focus";
  const says = `${nameOf(p)}: ${d < 0.005 ? "under 1%" : pct(d)} of the work in the ${label.toLowerCase()}, ${amount(b)}`;
  const props = { class: "wkb-seg", "data-key": p.id || "~other", style: `flex:${d.toFixed(4)} 1 0;${fill(p)}`, title: rest ? `${says}\n${rest}` : says, "aria-label": rest ? `${says}. ${rest}` : says };
  const text = d >= LABEL_FROM ? pct(d) : null;
  return p.id ? h("a", { ...props, href: href("focus", p.id) }, text) : h("span", { ...props, tabindex: "0" }, text);
}

/** The block on the home, or null where it has nothing to say. */
export function workBar(activity) {
  if (simpleOn()) return null;
  const r = workParts(activity); if (!r) return null;
  const shown = r.parts.filter(p => WINDOWS.some(([w]) => work(p.by(w))));
  return h("section", { class: "wkb", "data-key": "workbar", "aria-label": "Where the work went" },
    h("div", { class: "sec-h" }, h("h2", { class: "sec-t" }, "Where the work went")),
    WINDOWS.map(([w, label]) => { const t = r.total(w), all = activity.total[w];
      return h("div", { class: "wkb-r", "data-key": w },
        h("div", { class: "wkb-h" }, h("span", null, label), h("span", { class: "meta" }, t ? amount(all) : "Nothing yet")),
        t ? h("div", { class: "wkb-bar", role: "group", "aria-label": label }, shown.map(p => segment(p, w, label, t))) : h("div", { class: "wkb-bar", "data-empty": "" })); }),
    h("ul", { class: "wkb-key", "aria-label": "What the colours are" }, shown.map(p => h("li", { "data-key": p.id || "~other" },
      h("i", { style: fill(p), "aria-hidden": "true" }),
      p.id ? h("a", { href: href("focus", p.id), title: nameOf(p) }, nameOf(p)) : h("span", null, "Other")))));
}

/** The room the block will take while the focuses are read, so nothing around it moves when it lands: px is its last height (core/room.js), or the CSS's one-line key. */
export const workBarHeld = px => (simpleOn() ? null : h("div", { class: "wkb wkb-held", "data-key": "workbar", "aria-hidden": "true", style: px ? `min-height:${px}px` : null }));
