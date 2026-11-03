// Agents: every agent that has ever run, one row each, in the order they were spawned. Search, filter and sort happen here in the
// browser, from what the store already holds; the name opens the agent's panel with all its numbers. Cost and tokens: agentSpend.
import { h } from "../core/dom.js";
import { S, OUTSIDE, getPref, loadAgentCosts, setPref } from "../core/store.js";
import { agentSpend, agentStatus, isRunning, loginName, modelName, sEnd, threadTitle } from "../core/model.js";
import { ago, dur, money, plural, short, stamp, toks } from "../core/format.js";
import { metrics } from "../templates.js";
import { button, empty, input, pageHeader, stats, toolbar } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";

const STATUS = [["all", "Any status"], ["running", "Running"], ["queued", "Waiting to start"], ["ended", "Ended"], ["killed", "Killed"], ["limited", "Stopped at a limit"], ["failed", "Failed"]];
const BY = [["all", "Started by anyone"], ["person", "Started by a person"], ["agent", "Started by an agent"], ["system", "Started by the hive"]];
const SINCE = [["all", "Any time"], ["1", "Last 24 hours"], ["7", "Last 7 days"], ["30", "Last 30 days"]];
const PAGE = 200, DAY = 864e5;
const L = { q: "", status: "all", by: "all", since: "all", model: "all", account: "all", limit: PAGE, sig: "", dataKey: "", all: [], shown: [] };
const sort = () => getPref("agents.sort", { k: "spawned", d: -1 });

const statusOf = n => { const k = agentStatus(n).key; return isRunning(n) ? "running" : k; };
const whoKind = f => (!f || f === "anon" ? "system" : OUTSIDE.has(f) ? "person" : S.agents.has(f) ? "agent" : "system");

/** One record per agent, from one pass over sessions and one over posts. Rebuilt when the data changes. */
function gather() {
  const key = `${S.ver}|${S.uver}`;
  if (L.dataKey === key) return L.all;
  L.dataKey = key;
  const A = new Map(), files = (S.usage && S.usage.files) || {};
  for (const s of S.sessions) {
    let a = A.get(s.name);
    if (!a) A.set(s.name, a = { name: s.name, first: s, last: s, runs: 0, ran: 0, c: 0, t: [0, 0, 0, 0], m: {}, turns: 0, tools: 0, posts: 0, spawned: 0 });
    a.last = s; a.runs++; a.ran += Math.max(0, sEnd(s) - s.start);
    if (!s.resumed && s.from && s.from !== s.name) { const by = A.get(s.from); if (by) by.spawned++; }
    const f = s.out && files[s.out]; if (!f) continue;
    a.c += f.c; a.turns += f.turns || 0;
    for (let i = 0; i < 4; i++) a.t[i] += f.t[i];
    for (const k in f.m) a.m[k] = (a.m[k] || 0) + f.m[k];
    for (const k in f.tools) a.tools += f.tools[k];
  }
  for (const p of S.posts) { const a = A.get(p.from); if (a) { a.posts++; a.lastPost = p.t; } }
  L.all = [...A.values()].map(a => {
    const x = agentSpend(a.name, { c: a.c, t: a.t, m: a.m }), s = a.last, init = (s.out && files[s.out] && files[s.out].init) || {};
    const prov = s.provider || (s.model && s.model.includes(":") ? s.model.split(":")[0] : null);
    const model = init.model ? modelName(init.model) : s.model ? (prov ? s.model.slice(prov.length + 1) : modelName(s.model)) : "default";
    const lastSeen = Math.max(sEnd(s), a.lastPost || 0), account = prov || loginName(s.login) || "–";
    const status = statusOf(a.name), from = a.first.from, thread = a.first.thread;
    return { ...a, spend: x, usd: x.usd, tok: x.t[0] + x.t[1] + x.t[2] + x.t[3], calls: x.calls, model, account, status, from, thread, lastSeen,
      when: a.first.start, lc: [a.name, thread, threadTitle(thread), from, model, account, status, s.effort, s.harness].join(" ").toLowerCase() };
  });
  return L.all;
}

const COLS = [
  { k: "name", label: "Agent", get: a => a.name, render: a => agentRef(a.name) },
  { k: "status", label: "Status", get: a => a.status, render: a => h("span", { title: agentStatus(a.name).label }, (STATUS.find(x => x[0] === a.status) || [0, a.status])[1]) },
  { k: "spawned", label: "Spawned", get: a => a.when, render: a => h("span", { title: new Date(a.when).toISOString() }, stamp(a.when)) },
  { k: "thread", label: "Thread", cls: "clip ag-tm", get: a => threadTitle(a.thread) || "", render: a => threadRef(a.thread) },
  { k: "from", label: "Started by", get: a => a.from || "", render: a => (a.from && S.agents.has(a.from) ? agentRef(a.from) : a.from || "–") },
  { k: "model", label: "Model", cls: "ag-m", get: a => a.model, render: a => h("span", { title: a.model }, a.model) },
  { k: "account", label: "Account", get: a => a.account, render: a => a.account },
  { k: "runs", label: "Runs", num: 1, title: "How many times it has been started or woken", get: a => a.runs, render: a => a.runs },
  { k: "ran", label: "Ran for", num: 1, title: "Time running, over all its runs", get: a => a.ran, render: a => (a.ran < 6e4 ? "<1m" : dur(a.ran)) },
  { k: "lastSeen", label: "Last active", num: 1, get: a => a.lastSeen, render: a => (isRunning(a.name) ? "now" : ago(a.lastSeen)) },
  { k: "usd", label: "Cost", num: 1, title: "At API list prices; nobody is billed this", get: a => a.usd, render: a => h("span", { title: `From the ${a.spend.src}` + (a.spend.sub ? `; ${money(a.spend.sub)} in subagents` : "") }, money(a.usd)) },
  { k: "tok", label: "Tokens", num: 1, title: "Input, output and cache together", get: a => a.tok, render: a => h("span", { title: `${toks(a.spend.t[0])} input, ${toks(a.spend.t[1])} output, ${toks(a.spend.t[2])} cache read, ${toks(a.spend.t[3])} cache write` }, toks(a.tok)) },
  { k: "calls", label: "Calls", num: 1, title: "Model calls, subagents included", get: a => a.calls ?? -1, render: a => (a.calls == null ? "–" : a.calls.toLocaleString()) },
  { k: "tools", label: "Tools", num: 1, title: "Tool calls in its session logs", get: a => a.tools, render: a => a.tools.toLocaleString() },
  { k: "posts", label: "Posts", num: 1, get: a => a.posts, render: a => a.posts },
  { k: "children", label: "Started", num: 1, title: "Agents it started", get: a => a.spawned, render: a => a.spawned },
];

function select() {
  const words = L.q.toLowerCase().split(/\s+/).filter(Boolean), from = L.since === "all" ? 0 : Date.now() - +L.since * DAY;
  const found = gather().filter(a => (L.status === "all" || a.status === L.status) && (L.by === "all" || whoKind(a.from) === L.by)
    && (L.model === "all" || a.model === L.model) && (L.account === "all" || a.account === L.account) && a.when >= from
    && words.every(w => a.lc.includes(w)));
  const { k, d } = sort(), get = (COLS.find(c => c.k === k) || COLS[2]).get;
  return found.sort((a, b) => { const x = get(a), y = get(b); return (typeof x === "string" ? x.localeCompare(y) : x - y) * d || a.when - b.when; });
}

const options = (rows, key) => { const n = {}; for (const a of rows) n[a[key]] = (n[a[key]] || 0) + 1; return Object.entries(n).sort((a, b) => b[1] - a[1]).map(([v, c]) => [v, `${v} (${c})`]); };
const picker = (label, value, opts, on) => h("select", { class: "input", value, "aria-label": label, title: label, onchange: e => on(e.target.value) },
  opts.map(([k, l]) => h("option", { value: k, selected: k === value ? "" : null }, l)));

export default {
  id: "agents", label: "Agents", about: "every agent, in spawn order: search, filter, sort", tab: true,
  leave() { L.sig = ""; },   // the table is built afresh on the way back, so its rows must be too
  poll: { every: 30000, run() { loadAgentCosts(); } },
  render(ctx) {
    if (!S.live) return empty("Reading…", { kind: "loading" });
    const all = gather(), s = sort(), set = (k, v) => { L[k] = v; L.limit = PAGE; ctx.update(); };
    const sig = [L.dataKey.split("|")[0], S.agentCosts && S.agentCosts.at, L.q, L.status, L.by, L.since, L.model, L.account, L.limit, s.k, s.d].join("|"), changed = sig !== L.sig; L.sig = sig;
    if (changed || !L.found) L.found = select();
    const found = L.found, narrowed = found.length < all.length, sum = f => found.reduce((a, x) => a + f(x), 0);
    const by = k => () => { const c = sort(), d = c.k === k ? -c.d : COLS.find(x => x.k === k).num || k === "spawned" ? -1 : 1; setPref("agents.sort", { k, d }); L.limit = PAGE; ctx.update(); };
    const costs = S.agentCosts;
    return metrics({
      header: pageHeader({ title: "Agents", lead: `Every agent that has run, ${s.k === "spawned" && s.d < 0 ? "newest spawned first" : s.k === "spawned" ? "oldest spawned first" : "sorted by the column you chose"}. Click a name for its numbers.`, after: toolbar(
        input({ type: "search", placeholder: "Search name, thread, model, who started it", "aria-label": "Search agents", value: L.q, oninput: e => set("q", e.target.value) }),
        picker("Status", L.status, STATUS, v => set("status", v)),
        picker("Model", L.model, [["all", "Any model"], ...options(all, "model")], v => set("model", v)),
        picker("Account", L.account, [["all", "Any account"], ...options(all, "account")], v => set("account", v)),
        picker("Started by", L.by, BY, v => set("by", v)),
        picker("Spawned", L.since, SINCE, v => set("since", v)),
        narrowed ? button("Clear", () => { Object.assign(L, { q: "", status: "all", by: "all", since: "all", model: "all", account: "all", limit: PAGE }); ctx.update(); }, { kind: "quiet", size: "s" }) : null,
        h("span", { class: "meta" }, narrowed ? `${found.length} of ${plural(all.length, "agent")}` : plural(all.length, "agent"))),
        meta: [costs && costs.at ? `Cost and tokens from the transcripts, read ${ago(costs.at * 1e3)}${costs.refreshing ? ", reading again" : ""}. API list prices; nobody is billed this. Tokens add up every model call's input, output and cache, so context read again on each call counts each time.` : costs && costs.refreshing ? "Reading the transcripts for exact costs; until then these come from the session logs." : "Cost and tokens from the session logs."] }),
      sections: [
        stats([
          { label: narrowed ? "Agents shown" : "Agents", value: found.length.toLocaleString(), note: `${found.filter(a => a.status === "running").length} running` },
          { label: "Total cost", value: money(sum(a => a.usd)), note: found.length ? `${money(sum(a => a.usd) / found.length)} per agent` : null },
          { label: "Tokens", value: toks(sum(a => a.tok)), note: `${toks(sum(a => a.spend.t[1]))} written` },
          { label: "Time running", value: dur(sum(a => a.ran)), note: plural(sum(a => a.runs), "run") },
        ]),
        found.length ? h("div", { class: "tbl-w ag-w" }, h("table", { class: "tbl ag" },
          h("caption", { class: "vh" }, "Agents"),
          h("thead", null, h("tr", null, COLS.map(c => h("th", { scope: "col", class: c.num ? "num" : null, title: c.title || null, "aria-sort": s.k === c.k ? (s.d > 0 ? "ascending" : "descending") : null, "data-key": c.k },
            h("button", { type: "button", class: "ag-s", onclick: by(c.k) }, c.label, s.k === c.k ? h("span", { "aria-hidden": "true" }, s.d > 0 ? "▲" : "▼") : null))))),
          h("tbody", { "data-own": "morph", "data-sig": sig }, changed ? found.slice(0, L.limit).map(a => h("tr", { "data-key": a.name },
            COLS.map(c => h("td", { class: [c.num ? "num" : null, c.cls || null].filter(Boolean).join(" ") || null }, c.render(a))))) : null)))
          : empty("No agents match."),
        found.length > L.limit ? h("div", { class: "lr-more" },
          button(`Show ${Math.min(PAGE * 5, found.length - L.limit)} more (${found.length - L.limit} left)`, () => { L.limit += PAGE * 5; ctx.update(); }, { kind: "quiet", size: "s" })) : null,
      ],
    });
  },
};
