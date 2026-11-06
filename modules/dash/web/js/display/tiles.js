// The tiles a display page can show. A tile is { id, label, head?(), more?(n), render(ctx, body), enter?(ctx) }, the same shape as a view,
// so Mesh and Timeline are simply their views (their controls float over the tile while the pointer moves). Adding a tile is one entry here.
// Size: put the tile's items, most important first, in one element marked data-fit; app.js hides whatever doesn't fit whole
// and shows more(n) (default "+n more") in the head. No tile measures anything itself.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { agentState, inUse, spendSince } from "../core/model.js";
import { DAY, HOUR, MIN, money, now, plural } from "../core/format.js";
import { empty, list, meter, stats, statusDot } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import mesh, { meshForDisplay, meshNote } from "../views/mesh.js";
import timeline, { fitRunning, runningNote } from "../views/timeline.js";
import { row as ledgerRow } from "../views/ledger.js";
import { activityCharts, forYouRows, hiveFigures } from "../views/now.js";
import { loginRow, providerRow, usageAnswer } from "../views/usage.js";

const reading = () => empty("Reading…", { kind: "loading" });
const fitting = el => { if (el && el.setAttribute) el.setAttribute("data-fit", ""); return el; };

// ---- agents: one line each, trouble first, then who's working; order otherwise holds still ----
const ORDER = { stuck: 0, working: 1, queue: 2, quiet: 3 };
const agents = {
  id: "agents", label: "Agents",
  more: n => `+${n} more, quietest last`,
  render() {
    if (!S.live) return reading();
    const rows = S.live.agents.map(a => ({ a, st: agentState(a) }))
      .sort((x, y) => (ORDER[x.st.key] ?? 4) - (ORDER[y.st.key] ?? 4) || (x.a.since || 0) - (y.a.since || 0) || String(x.a.name).localeCompare(String(y.a.name)));
    if (!rows.length) return empty("Nobody is running right now.");
    const active = a => a.last && now() - a.last * 1e3 < 3 * MIN;
    return h("ul", { class: "dt-agents", "data-fit": "" }, rows.map(({ a, st }) => h("li", { "data-key": a.name, "data-tone": st.key === "stuck" ? "bad" : null },
      agentRef(a.name), threadRef(a.thread),
      (txt => h("span", { class: "dt-now", title: txt }, txt))(st.key === "stuck" ? "Stuck: " + st.text : active(a) ? a.doing || "Starting" : st.text))));
  },
};

// ---- ledger: the newest events; only rebuilt when there's something new ----
let seen = 0;
const ledger = {
  id: "ledger", label: "Ledger",
  more: () => null,     // older events leaving the bottom of a feed is expected; the head gives the hour's count
  head() { const t = now(); return plural(S.ledger.filter(r => r.t > t - HOUR).length, "event") + " in the last hour"; },
  render() {
    const sig = String(S.ver);
    if (ledger.sig === sig) return h("ol", { class: "lg dt-lg", "data-fit": "", "data-own": "morph", "data-sig": sig });
    ledger.sig = sig;
    const newest = S.ledger.map((r, i) => [r, i]).sort((a, b) => b[0].t - a[0].t).slice(0, 80), was = seen;
    if (newest.length) seen = Math.max(seen, newest[0][0].t);
    return h("ol", { class: "lg dt-lg", "data-fit": "", "data-own": "morph", "data-sig": sig }, newest.map(([r, i]) => ledgerRow(r, i, was && r.t > was)));
  },
};

// ---- usage: the answer, then each login's limits as bars, logins with room first ----
const limitBar = g => h("div", { class: "dt-g" },
  h("div", { class: "dt-g-t" }, h("span", null, g.label), h("b", null, g.value, g.unit ? " " + g.unit : "")),
  g.pct != null ? meter({ value: g.pct, stop: g.stop, label: g.label }) : null,
  g.lines && g.lines[0] && typeof g.lines[0] === "string" ? h("div", { class: "dt-g-n", title: g.lines[0] }, g.lines[0]) : null);
const usage = {
  id: "usage", label: "Usage",
  more: n => `+${n} not shown`,
  head: () => (S.usage ? money(spendSince(now() - DAY)) + " of work in 24h" : null),
  render() {
    const U = S.usage;
    if (!U) return reading();
    const t = now(), { lead, tone } = usageAnswer(U, t), us = [...(U.logins || [])].sort((a, b) => inUse(b, U) - inUse(a, U));
    const P = U.providers || {}, rows = [...us.map(l => loginRow(U, l, t)), ...Object.keys(P).filter(k => !k.startsWith("_")).map(k => providerRow(k, P[k], P._checked))].filter(r => r.gauges);
    return h("div", { class: "dt-usage" },
      h("p", { class: "dt-lead", "data-tone": tone }, lead),
      h("div", { "data-fit": "" }, rows.map(r => h("div", { class: "dt-acct", "data-key": r.key, "data-dim": r.dim ? "" : null },
        h("div", { class: "dt-acct-n", title: r.state ? r.state.text : null }, r.state ? statusDot(r.state.key) : null, h("span", null, r.name)),
        r.gauges.slice(0, 2).map(limitBar)))));
  },
};

// ---- the smaller ones ----
const activity = { id: "activity", label: "Activity", more: n => `+${n} chart${n > 1 ? "s" : ""} not shown`, render: () => (S.live ? h("div", { class: "dt-charts", "data-fit": "" }, activityCharts()) : reading()) };
const hive = { id: "hive", label: "How it's working", more: n => `+${n} figures not shown`, render: () => (S.live ? fitting(stats(hiveFigures())) : reading()) };
const foryou = {
  id: "foryou", label: "For you",
  head: () => { const n = forYouRows().length; return n ? n + " open" : null; },
  render: () => { const el = list(forYouRows(), { empty: "Nothing open.", dense: true }); fitting(el.querySelector && el.querySelector(".list")); return el; },
};

export const TILES = Object.fromEntries([
  { ...mesh, label: "Mesh", head: meshNote, enter(ctx) { meshForDisplay(); mesh.enter(ctx); } },
  { ...timeline, label: "Timeline", head: runningNote, enter(ctx) { fitRunning(); timeline.enter(ctx); } },
  agents, ledger, usage, activity, hive, foryou,
].map(t => [t.id, t]));
