// Yours (#yours, and #yours/waits from the home's one line): one serial stream of every exchange the person had, newest day first, a thin
// rule with the conversation's name at each change of thread. An entry opens that post in its conversation, highlighted. The search box
// above it looks only over these exchanges. Built from the board (core/yours.js): the same in every build, with or without the Needs you page.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { yoursPosts, yoursSearch, yoursItems, yoursText } from "../core/yours.js";
import { waitingOn, waitingLater, waitingMoot, needsPage, olderWhy, needsN, needsSplit } from "../core/needs.js";
import { quickPanel, closeQuick, laterButtons, setLater, quickIs } from "../ui/quick.js";
import { answerer, flat } from "./comms.js";
import { agentRef } from "../ui/refs.js";
import { focusOf, focusName, railSay } from "../core/foci.js";
import { clock, plural, short, ago } from "../core/format.js";
import { threadTitle } from "../core/model.js";
import { threadHref, href, go } from "../core/router.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { metrics } from "../templates.js";
import { button, empty, input, pageHeader, scrim } from "../ui/kit.js";
import { jump } from "../ui/message.js";

const Y = { q: "", limit: 40, by: "new", menu: false };   // by: new (newest first) | focus (grouped by focus, the person, 11081355ecce)
const STEP = 60;
// An agent's code plain, not a pill (the Nov 8 handoff §4.6): agentRef with no pill, flattened to a span because the row is a link.
const who = p => (p.from === "person" ? "You" : simpleOn() ? hiveName() : flat(agentRef(p.from, { dot: false }))[0]);

/** #yours/waits: the one list itself (core/needs.js, what the badge counts), each entry with its reason in words, newest first;
 *  the R6 review found the stream of exchanges here didn't say why a thing waited. A search cuts it by the words. */
const waitRows = q => { const k = q.trim().toLowerCase(); return waitingOn().filter(w => !k || `${threadTitle(w.th)} ${w.text || ""}`.toLowerCase().includes(k)); };
/** What waits now, then Stuff to address: the older ones (core/needs.js olderWhy: a day on work that moved on, two on quiet work),
 *  then Later: what the person put off (the person, 1108133400fb, 110813526b10). */
function waitParts(rows, ctx) {
  const now = rows.filter(w => !olderWhy(w)), old = rows.filter(w => olderWhy(w));
  const later = putOff(waitingLater, Y.q), moot = putOff(waitingMoot, Y.q);
  const part = (k, title, lead, list, kind) => list.length ? [
    h("h2", { class: "sec-t yr-older", "data-key": k + "-h" }, `${title} \u00b7 ${list.length}`),
    lead ? h("p", { class: "meta", "data-key": k + "-l" }, lead) : null, grouped(list, kind || k, ctx)] : [];
  return h("div", { "data-key": "wparts" },
    now.length ? grouped(now, "waits", ctx) : h("p", { class: "meta" }, rows.length ? "Nothing new waits on you." : "Nothing waits on you."),
    ...part("older", "Stuff to address", "Not in the count: asks that sat a day while their work moved on (likely obsolete), or two days on quiet work. Go through them when you have time.", old),
    ...part("later", "Later", "You put these off. They stay out of the count until you bring one back.", later),
    ...part("moot", "Not needed", "You said these aren't needed. They're kept here in case you want one back.", moot));
}
const match = q => { const k = q.trim().toLowerCase(); return w => !k || `${threadTitle(w.th)} ${w.text || ""}`.toLowerCase().includes(k); };
const putOff = (src, q) => src().filter(match(q));
const focusFor = w => w.focus || focusOf(w.th) || "";
/** By focus: one small heading per focus, in the order its newest item came, then that focus's items newest first. */
function grouped(rows, key, ctx) {
  if (Y.by !== "focus") return waitList(rows, key, ctx);
  const g = new Map();
  for (const w of [...rows].sort((a, b) => (b.t || 0) - (a.t || 0))) { const f = focusFor(w); if (!g.has(f)) g.set(f, []); g.get(f).push(w); }
  return h("div", { class: "yr-groups", "data-key": key + "-g" }, [...g].map(([f, ws]) => h("div", { class: "yr-group", "data-key": key + ":f:" + f },
    h("h3", { class: "yr-gt" }, f ? focusName(f) : "No focus", h("span", { class: "meta" }, " \u00b7 " + ws.length)), waitList(ws, key, ctx))));
}
const WHY = { moved: "its work moved on", quiet: "quiet work" };
function waitList(rows, key = "waits", ctx) {
  const open = w => e => { if (!w.post || e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(w.post); };
  const post = can("post");
  return h("ul", { class: "list yr-waits", "data-dense": "", "data-key": key }, rows.map(w => {
    const ans = post && key !== "later" && key !== "moot" ? answerer(w) : null, why = key === "older" ? WHY[olderWhy(w)] : null;
    return h("li", { class: "lr yr-w", "data-key": "w:" + (w.key || w.th) },
      h("a", { class: "lr-in", href: threadHref(w.th), onclick: open(w), title: w.post ? "Open it in the conversation, at that message" : "Open the conversation" },
        h("div", { class: "lr-t" }, h("b", null, threadTitle(w.th)), h("span", { class: "meta" }, [why, ago(w.t)].filter(Boolean).join(" \u00b7 "))),
        h("div", { class: "lr-x clamp-2" }, w.text ? railSay(w.text, 400) : "Waits on you")),
      post ? h("div", { class: "yr-wb" },
        key === "later" || key === "moot" ? button("Bring back", () => { setLater(w, "back"); ctx && ctx.update(); }, { size: "s", kind: "quiet" })
          : [laterButtons(w), ans ? h("button", { type: "button", class: "btn cm-need-a", "data-size": "s", "aria-haspopup": "dialog", "aria-expanded": String(quickIs(w)), onclick: ans }, "Answer") : null]) : null);
  }));
}

function entry(p) {
  const open = e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(p.id); };
  return h("a", { class: "yr-e", "data-key": "y:" + p.id, "data-who": p.from === "person" ? "me" : "agent", href: threadHref(p.th), onclick: open, title: "Open it in the conversation" },
    h("span", { class: "yr-h" }, h("b", null, who(p)), h("time", { class: "meta", datetime: new Date(p.t).toISOString() }, clock(p.t))),
    h("span", { class: "yr-x" }, yoursText(p) ? railSay(yoursText(p), 420) : "(no text)"));
}

function stream(items, ctx) {
  const shown = items.slice(0, Y.limit);
  while (shown.length && !shown[shown.length - 1].post) shown.pop();   // never end on a day or a rule with nothing under it
  const more = items.length - shown.length, left = items.slice(shown.length).filter(it => it.post).length;
  return h("div", { class: "yr-stream", "data-key": "stream" },
    ...shown.map(it => it.day ? h("div", { class: "yr-day", "data-key": it.key, role: "heading", "aria-level": "2" }, h("span", null, it.day))
      : it.thread ? h("div", { class: "yr-rule", "data-key": it.key }, h("a", { class: "yr-rule-n", href: threadHref(it.thread), title: "The whole conversation" }, it.name))
      : entry(it.post)),
    more ? h("div", { class: "lr-more" }, button(`Show ${Math.min(left, STEP)} more`, () => { Y.limit += STEP; ctx.update(); }, { kind: "quiet", size: "s" })) : null);
}

function page(ctx) {
  const waits = ctx.arg === "waits";
  const all = yoursPosts(), base = waits ? waitRows("") : all, rows = waits ? waitRows(Y.q) : Y.q ? yoursSearch(Y.q, all) : all;
  const threads = new Set(rows.map(p => p.th)).size;
  const title = waits ? "Waits on you" : "Yours";
  const lead = waits
    ? "What waits on you, newest first: each one opens in its conversation, at that message."
    : "Every exchange you had, newest first: what you said, what was said to you, and each agent's answer. An entry opens in its conversation, at that message.";
  const split = needsSplit();   // the switch below counts the recent ones only: say both halves, so 5 there and 47 here don't look like a mistake
  const meta = !base.length ? null : Y.q ? `${plural(rows.length, "match")} of ${base.length}` : waits ? `${split.recent} recent, ${split.older} older, in ${plural(threads, "conversation")}` : `${plural(base.length, "exchange")} in ${plural(threads, "conversation")}`;
  // One switch between the two lists (the Nov 8 handoff §4.2); the full Needs you page stays reachable from the home's "All".
  const n = needsN();
  const actions = h("span", { class: "seg yr-seg", role: "group", "aria-label": "Show" },
    h("button", { type: "button", class: "seg-b", "aria-pressed": String(!waits), onclick: () => go("yours") }, "Everything"),
    h("button", { type: "button", class: "seg-b", "aria-pressed": String(waits), onclick: () => go("yours", "waits") }, "Waits on you", n ? h("i", { class: "n" }, String(n)) : null));
  const ORDER = [["new", "Newest first"], ["focus", "By focus"]];
  const order = waits ? h("span", { class: "yr-order", style: "position:relative" },
    button("Order: newest first, or by focus", () => { Y.menu = !Y.menu; ctx.update(); }, { kind: "icon", size: "s", icon: "filter", cls: "yr-sort" }),
    Y.menu ? [scrim(() => { Y.menu = false; ctx.update(); }), h("div", { class: "menu", role: "menu", "data-side": "bottom-end", "data-key": "yr-menu" }, ORDER.map(([k, l]) =>
      h("button", { type: "button", class: "menu-i", role: "menuitemradio", "aria-checked": String(Y.by === k), onclick: () => { Y.by = k; Y.menu = false; ctx.update(); } }, l)))] : null) : null;
  const search = h("div", { class: "yr-tools", "data-key": "tools" }, h("div", { class: "yr-search", "data-key": "search" },
    input({ type: "search", value: Y.q, placeholder: waits ? "Search what waits on you" : "Search your exchanges", "aria-label": "Search your exchanges",
      oninput: e => { Y.q = e.target.value; Y.limit = 40; ctx.update(); } }),
    waits ? h("div", { class: "seg yr-by", role: "group", "aria-label": "Order" }, ORDER.map(([k, l]) =>
      h("button", { type: "button", class: "seg-b", "aria-pressed": String(Y.by === k), onclick: () => { Y.by = k; ctx.update(); } }, l))) : null), order);
  const body = !S.live && !S.posts.length ? empty("Reading…", { kind: "loading" })
    : !rows.length && !(waits && (waitingLater().length || waitingMoot().length)) ? empty(Y.q ? "Nothing of yours has those words." : waits ? "Nothing waits on you." : "No exchanges yet. What you say in Chat, and what is said to you, shows up here.")
    : waits ? waitParts(rows, ctx) : stream(yoursItems(rows), ctx);
  const pg = metrics({ header: pageHeader({ title, hint: lead, actions, after: search }), sections: [meta ? h("p", { class: "meta yr-meta", "data-key": "meta" }, meta) : null, body, waits ? quickPanel(ctx.update) : null].filter(Boolean) });
  pg.setAttribute("data-view", "yours");   // centred at the reading width (the Nov 8 handoff §4.1)
  return pg;
}

export default {
  id: "yours", label: "Yours", about: "every exchange you had, newest first",
  tab: true,
  enter(ctx) { Y.limit = 40; Y.menu = false; ctx.update(); },
  render(ctx) { return page(ctx); },
  escape: () => closeQuick(),
  leave() { closeQuick(); },
};
