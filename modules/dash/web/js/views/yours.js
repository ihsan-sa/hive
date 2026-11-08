// Yours (#yours, and #yours/waits from the home's one line): one serial stream of every exchange the person had, newest day first, a thin
// rule with the conversation's name at each change of thread. An entry opens that post in its conversation, highlighted. The search box
// above it looks only over these exchanges. Built from the board (core/yours.js): the same in every build, with or without the Needs you page.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { yoursPosts, yoursSearch, yoursItems, yoursText } from "../core/yours.js";
import { waitingOn, needsPage } from "../core/needs.js";
import { clock, plural, short } from "../core/format.js";
import { threadHref, href } from "../core/router.js";
import { simpleOn, hiveName } from "../core/simple.js";
import { metrics } from "../templates.js";
import { button, empty, input, pageHeader } from "../ui/kit.js";
import { jump } from "../ui/message.js";

const Y = { q: "", limit: 40 };
const STEP = 60;
const who = p => (p.from === "person" ? "You" : simpleOn() ? hiveName() : p.from);

/** #yours/waits: only the exchanges that wait on the person (core/needs.js, the same list the badge counts), newest first. */
function waiting(rows) {
  const ids = new Set(), ths = new Set();
  for (const w of waitingOn()) { ids.add(w.post || w.src); ths.add(w.th); }
  const hit = rows.filter(p => ids.has(p.id));
  for (const th of ths) if (!hit.some(p => p.th === th)) { const p = rows.find(r => r.th === th); if (p) hit.push(p); }
  return hit.sort((a, b) => b.t - a.t);
}

function entry(p) {
  const open = e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); jump(p.id); };
  return h("a", { class: "yr-e", "data-key": "y:" + p.id, "data-who": p.from === "person" ? "me" : "agent", href: threadHref(p.th), onclick: open, title: "Open it in the conversation" },
    h("span", { class: "yr-h" }, h("b", null, who(p)), h("time", { class: "meta", datetime: new Date(p.t).toISOString() }, clock(p.t))),
    h("span", { class: "yr-x" }, short(yoursText(p), 420) || "(no text)"));
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
  const all = yoursPosts(), base = waits ? waiting(all) : all, rows = Y.q ? yoursSearch(Y.q, base) : base;
  const threads = new Set(rows.map(p => p.th)).size;
  const title = waits ? "Waits on you" : "Yours";
  const lead = waits
    ? "What waits on you, newest first: each one opens in its conversation, at that message."
    : "Every exchange you had, newest first: what you said, what was said to you, and each agent's answer. An entry opens in its conversation, at that message.";
  const meta = !base.length ? null : Y.q ? `${plural(rows.length, "match")} of ${base.length}` : `${plural(base.length, waits ? "thing" : "exchange")} in ${plural(threads, "conversation")}`;
  const actions = [
    waits ? button("Everything you exchanged", null, { href: href("yours"), kind: "quiet", size: "s" }) : null,
    !waits && waitingOn().length ? button(`${plural(waitingOn().length, "thing")} ${waitingOn().length === 1 ? "waits" : "wait"} on you`, null, { href: href("yours", "waits"), kind: "quiet", size: "s" }) : null,
    waits && needsPage() ? button("The full Needs you page", null, { href: needsPage(), kind: "quiet", size: "s" }) : null,
  ].filter(Boolean);
  const search = h("div", { class: "yr-search", "data-key": "search" },
    input({ type: "search", value: Y.q, placeholder: waits ? "Search what waits on you" : "Search your exchanges", "aria-label": "Search your exchanges",
      oninput: e => { Y.q = e.target.value; Y.limit = 40; ctx.update(); } }));
  const body = !S.live && !S.posts.length ? empty("Reading…", { kind: "loading" })
    : !rows.length ? empty(Y.q ? "Nothing of yours has those words." : waits ? "Nothing waits on you." : "No exchanges yet. What you say in Chat, and what is said to you, shows up here.")
    : stream(yoursItems(rows), ctx);
  return metrics({ header: pageHeader({ title, lead, meta: [meta], actions, after: search }), sections: [body] });
}

export default {
  id: "yours", label: "Yours", about: "every exchange you had, newest first",
  tab: true,
  enter(ctx) { Y.limit = 40; ctx.update(); },
  render(ctx) { return page(ctx); },
};
