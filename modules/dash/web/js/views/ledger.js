// Ledger: every event, one row each, newest first. S.ledger is built by the store as data.
// The rows are rebuilt only when the data or the filter changes, and morphed by key, so new rows slide in and nothing else moves.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { clock, dayName, mdPlain, plural, short } from "../core/format.js";
import { metrics } from "../templates.js";
import { button, empty, input, pageHeader, seg, toolbar } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";

const KINDS = [["all", "Everything"], ["post", "Messages"], ["agent", "Agents"], ["thread", "Threads"], ["trouble", "Trouble"], ["hive", "Hive"]];
const L = { kind: "all", q: "", limit: 300, seen: 0, sig: "", count: 0 };

const part = x => (typeof x === "string" ? x : x.a !== undefined ? agentRef(x.a) : threadRef(x.th));

export function row(r, i, fresh) {
  const p = r.post;
  return h("li", { class: "lg-r", "data-key": "e" + i, "data-kind": r.kind, "data-fresh": fresh ? "" : null },
    h("time", { class: "lg-t", title: new Date(r.t).toISOString() }, clock(r.t)),
    h("span", { class: "lg-b" }, r.parts.map(part),
      p ? h("span", { class: "lg-q" }, ": " + short(mdPlain(p.text).replace(/^(@\S+\s+)+/, ""), 170), (p.files || []).length ? ` (${plural(p.files.length, "file")})` : "") : null));
}

function rows() {
  const words = L.q.toLowerCase().split(/\s+/).filter(Boolean), found = [];
  S.ledger.forEach((r, i) => { if ((L.kind === "all" || r.kind === L.kind) && words.every(w => r.lc.includes(w))) found.push([r, i]); });
  found.sort((a, b) => b[0].t - a[0].t);
  L.count = found.length;
  const seen = L.seen; if (found.length) L.seen = Math.max(L.seen, found[0][0].t);
  const out = []; let day = null;
  for (const [r, i] of found.slice(0, L.limit)) {
    const d = dayName(r.t);
    if (d !== day) { out.push(h("li", { class: "lg-day", "data-key": "d" + d }, d)); day = d; }
    out.push(row(r, i, seen && r.t > seen));
  }
  return out;
}

export default {
  id: "ledger", label: "Ledger", tab: true,
  render(ctx) {
    const sig = [S.ver, L.kind, L.q, L.limit].join("|"), changed = sig !== L.sig; L.sig = sig;
    const built = changed ? rows() : null;     // on an unchanged poll the list below is an empty node with the same sig: morph leaves the page's list alone
    const set = (k, v) => { L[k] = v; ctx.update(); };
    return metrics({
      header: pageHeader({ title: "Ledger", after: toolbar(
        seg(KINDS, L.kind, k => set("kind", k), { label: "Kind of event" }),
        input({ type: "search", placeholder: "Filter by agent, thread or words", "aria-label": "Filter", value: L.q, oninput: e => set("q", e.target.value) }),
        h("span", { class: "meta" }, plural(L.count, "event"))) }),
      sections: [
        L.count ? null : empty("No events match."),
        h("ol", { class: "lg", "data-own": "morph", "data-sig": sig }, built),
        L.count > L.limit ? h("div", { class: "lr-more" }, button(`Show older (${L.count - L.limit} more)`, () => set("limit", L.limit + 500), { kind: "quiet", size: "s" })) : null,
      ],
    });
  },
};
