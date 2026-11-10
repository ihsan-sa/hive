// Agents: every agent that has ever run, one row each, in the order they were spawned. Search, filter and sort happen here in the
// browser, from what the store already holds; the name opens the agent's panel with all its numbers. Cost and tokens: agentSpend.
import { h } from "../core/dom.js";
import { S, OUTSIDE, getPref, loadAgentCosts, setPref } from "../core/store.js";
import { HOUSE_THREADS, agentOn, agentSpend, agentStatus, isRunning, loginName, modelName, sEnd, threadTitle } from "../core/model.js";
import { ago, dur, money, plural, short, stamp, toks } from "../core/format.js";
import { metrics } from "../templates.js";
import { button, empty, waiting, input, pageHeader, seg, stats, toolbar, pageNav } from "../ui/kit.js";
import { heldRoom } from "../core/room.js";
import { agentAvatar, agentRef, threadRef } from "../ui/refs.js";

const STATUS = [["all", "Any status"], ["running", "Running"], ["queued", "Waiting to start"], ["ended", "Ended"], ["killed", "Killed"], ["limited", "Stopped at a limit"], ["failed", "Failed"]];
const BY = [["all", "Started by anyone"], ["person", "Started by a person"], ["agent", "Started by an agent"], ["system", "Started by the Hive"]];
const SINCE = [["all", "Any time"], ["1", "Last 24 hours"], ["7", "Last 7 days"], ["30", "Last 30 days"]];
const PAGE = matchMedia("(max-width: 640px)").matches ? 36 : 200, DAY = 864e5;   // R48: a phone opens on a short list; a phone row is about 110 px
const L = { q: "", house: false, status: "all", by: "all", since: "all", model: "all", account: "all", limit: PAGE, sig: "", dataKey: "", all: [], shown: [] };
const pctOf = (a, b) => b ? Math.round(100 * a / b) + "%" : "0%";
const app = () => !!S.app.app;
const APP_COLS = ["name", "status", "spawned", "thread", "on", "model", "lastSeen", "usd"];   // the app look starts with these; the developer look has every column on
const colsOn = () => { const c = getPref("agents.cols", null); return new Set(Array.isArray(c) && c.length ? c : app() ? APP_COLS : COLS.map(x => x.k)); };
const sort = () => getPref("agents.sort", { k: "spawned", d: -1 });

// Housekeeping runs: the hive looking after itself (status-bar refreshes, keeper and improve rounds, watchdog trips, phone-call starts). Folded by default so the runs on someone's work show; one click brings them back.
const HOUSE_FROM = new Set(["status-timer", "phone", "watchdog"]);
const isHouse = a => { const p = S.byId[a.first.post]; return HOUSE_THREADS.has(a.first.thread) || !!p && (HOUSE_FROM.has(p.from) || /^\W*(@\w+\s+)?keeper round/i.test(String(p.text || ""))); };
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
    const on = agentOn(a.name);
    return { ...a, house: isHouse(a), spend: x, usd: x.usd, tok: x.t[0] + x.t[1] + x.t[2] + x.t[3], calls: x.calls, model, account, status, from, thread, lastSeen, on,
      when: a.first.start, lc: [a.name, thread, threadTitle(thread), from, model, account, status, s.effort, s.harness, on ? on.text : ""].join(" ").toLowerCase() };
  });
  return L.all;
}

let COST_DATA = true;
// On a phone a row stacks (R10 1, like ui/table.js): avatar, name and status on the first line, what it is on under them, then one labelled line per other column.
// R63: between the phone and the width the default columns need (1012 px, so a 1075 px window), rows stack too; a table there needed its own scrollbar beside the page's.
const STACK_BELOW = "(max-width: 1074px)";
const phoneRank = c => c.k === "name" ? 0 : c.k === "status" ? 1 : c.k === "on" ? 2 : 3;
const PHONE_BARE = new Set(["name", "status", "on"]);   // these stand on their own lines on a phone; the rest carry their column's name
const phoneName = a => { const av = agentAvatar(a.name, { size: "s" }); av.tabIndex = -1; av.setAttribute("aria-hidden", "true"); return [av, agentRef(a.name, { dot: false })]; };   // the avatar carries the status shape
const COLS = [
  { k: "name", label: "Agent", cls: "ag-n", get: a => a.name, render: a => agentRef(a.name) },
  { k: "status", label: "Status", cls: "ag-st", get: a => a.status, render: a => h("span", { title: agentStatus(a.name).label }, (STATUS.find(x => x[0] === a.status) || [0, a.status])[1]) },
  { k: "spawned", get label() { return S.app.app ? "Started" : "Spawned"; }, get: a => a.when, render: a => h("span", { title: new Date(a.when).toISOString() }, stamp(a.when)) },
  { k: "thread", get label() { return S.app.app ? "Conversation" : "Thread"; }, cls: "clip ag-tm", get: a => threadTitle(a.thread) || "", render: a => threadRef(a.thread) },
  // its own words (hive me) with their age; muted when it has said nothing and this is what its last run was asked; "was on" when the words predate the run
  { k: "on", label: "On", cls: "clip ag-on", title: "What it says it is on (hive me), and when it said so; muted: what it was asked, when it has said nothing", get: a => (a.on && a.on.text) || "",
    render: a => { const m = a.on; return m ? h("span", { class: m.said ? null : "meta", title: m.said ? `${m.before ? "Said in an earlier run, " : "Said "}${ago(m.at)}: ${m.text}` : `Has not said what it is on; ${m.bar ? `this is its thread's status, and its last run was woken by: ` : `its last run was asked: `}${m.asked}` }, m.before ? h("span", { class: "meta" }, "was on: ") : null, m.text, h("span", { class: "meta ag-onage" }, " · " + ago(m.at).replace(/ ago$/, ""))) : h("span", { class: "meta" }, "–"); } },
  { k: "from", label: "Started by", get: a => a.from || "", render: a => (a.from && S.agents.has(a.from) ? agentRef(a.from) : a.from === "person" && app() ? "you" : a.from || "–") },
  { k: "model", label: "Model", cls: "ag-m", get: a => a.model, render: a => h("span", { title: a.model }, a.model) },
  { k: "account", label: "Account", get: a => a.account, render: a => a.account },
  { k: "runs", label: "Runs", num: 1, title: "How many times it has been started or woken", get: a => a.runs, render: a => a.runs },
  { k: "ran", label: "Ran for", num: 1, title: "Time running, over all its runs", get: a => a.ran, render: a => (a.ran < 6e4 ? "<1m" : dur(a.ran)) },
  { k: "lastSeen", label: "Last active", num: 1, get: a => a.lastSeen, render: a => (isRunning(a.name) ? "now" : ago(a.lastSeen)) },
  { k: "usd", label: "Cost", num: 1, title: "At API list prices; nobody is billed this", get: a => a.usd, render: a => !COST_DATA ? "–" : h("span", { title: `From the ${a.spend.src}` + (a.spend.sub ? `; ${money(a.spend.sub)} in subagents` : "") }, money(a.usd)) },
  { k: "tok", label: "Tokens", num: 1, title: "Input, output and cache together", get: a => a.tok, render: a => !COST_DATA ? "–" : h("span", { title: `${toks(a.spend.t[0])} input, ${toks(a.spend.t[1])} output, ${toks(a.spend.t[2])} cache read, ${toks(a.spend.t[3])} cache write` }, toks(a.tok)) },
  { k: "calls", label: "Calls", num: 1, title: "Model calls, subagents included", get: a => a.calls ?? -1, render: a => (a.calls == null ? "–" : a.calls.toLocaleString()) },
  { k: "tools", label: "Tools", num: 1, title: "Tool calls in its session logs", get: a => a.tools, render: a => a.tools.toLocaleString() },
  { k: "posts", label: "Posts", num: 1, get: a => a.posts, render: a => a.posts },
  { k: "children", label: "Started", num: 1, title: "Agents it started", get: a => a.spawned, render: a => a.spawned },
];

// A column's track in the grid rows' --cols: the words get the spare room, the rest their usual width
const W = { name: "minmax(120px,1fr)", status: "90px", spawned: "130px", thread: "minmax(140px,1.2fr)", on: "minmax(150px,2fr)", from: "110px", model: "110px", account: "110px", lastSeen: "100px" };
// Do the chosen columns fit #main? Then the page scrolls and the table needs no box (and no second scrollbar) of its own. Each column's least width, 12px gaps, 16px row padding; 48px is the page's own side padding.
const tableFits = cols => { const m = document.getElementById("main"); return !!m && m.clientWidth - 48 >= cols.reduce((n, c) => n + parseInt(colW(c).match(/\d+/)[0], 10) + 12, 4); };
const colW = (c, phone) => (phone && c.k === "on" ? "min(22rem,calc(100vw - 13rem))" : W[c.k] || (c.num ? "72px" : "110px"));

function select(withHouse) {
  const words = L.q.toLowerCase().split(/\s+/).filter(Boolean), from = L.since === "all" ? 0 : Date.now() - +L.since * DAY;
  const found = gather().filter(a => (L.status === "all" || a.status === L.status) && (L.by === "all" || whoKind(a.from) === L.by)
    && (L.model === "all" || a.model === L.model) && (L.account === "all" || a.account === L.account) && a.when >= from && (withHouse || !a.house || a.status === "running")
    && words.every(w => a.lc.includes(w)));
  const { k, d } = sort(), get = (COLS.find(c => c.k === k) || COLS[2]).get;
  return found.sort((a, b) => { const x = get(a), y = get(b); return (typeof x === "string" ? x.localeCompare(y) : x - y) * d || a.when - b.when; });
}

const options = (rows, key) => { const n = {}; for (const a of rows) n[a[key]] = (n[a[key]] || 0) + 1; return Object.entries(n).sort((a, b) => b[1] - a[1]).map(([v, c]) => [v, `${v} (${c})`]); };
const picker = (label, value, opts, on) => h("select", { class: "input", value, "aria-label": label, title: label, onchange: e => on(e.target.value) },
  opts.map(([k, l]) => h("option", { value: k, selected: k === value ? "" : null }, l)));

// The phone order (name, status, On first) is chosen at render, so crossing 640 px re-renders: nothing else may come in time.
for (const q of ["(max-width: 640px)", STACK_BELOW]) matchMedia(q).addEventListener("change", () => { if (L.update && document.querySelector(".ag")) L.update(); });

// The room under the list that the Show more row takes, once the older log has made the list longer than a page (R67).
const moreRoom = () => { const w = document.querySelector(".ag-w"), m = w && w.nextElementSibling; return m && m.matches(".lr-more") ? m.getBoundingClientRect().bottom - w.getBoundingClientRect().bottom : 0; };
export default {
  id: "agents", label: "Agents", about: "every agent, in spawn order: search, filter, sort", tab: true,
  leave() { L.sig = ""; },   // the table is built afresh on the way back, so its rows must be too
  poll: { every: 30000, run() { loadAgentCosts(); } },
  render(ctx) {
    const whole = settled => heldRoom("agents-page", "#main > *", { settled });   // the whole page, for the wait before anything is known
    if (!S.live) return h("div", { class: "page", style: whole(false) ? `min-height:${whole(false)}px` : null }, pageNav(), waiting(S.failed, "Reading…", "the agents"));
    whole(!!S.history);
    L.update = ctx.update;
    const all = gather(), s = sort(), set = (k, v) => { L[k] = v; L.limit = PAGE; ctx.update(); };
    const sig = [matchMedia("(max-width: 640px)").matches, matchMedia(STACK_BELOW).matches, [...colsOn()].join(), L.dataKey.split("|")[0], S.agentCosts && S.agentCosts.at, L.q, L.status, L.by, L.since, L.model, L.account, L.limit, L.house, s.k, s.d].join("|"), changed = sig !== L.sig; L.sig = sig;
    if (changed || !L.found) { L.found = select(L.house); L.nHouse = L.house ? 0 : select(true).length - L.found.length; }   // nHouse: the housekeeping runs the fold hides now
    const nHouse = L.nHouse;
    const held = { settled: !!S.history, usual: !L.q && L.limit === PAGE && !L.house }, room = heldRoom("agents", ".ag-w", held), moreR = heldRoom("agents-more", moreRoom, held);   // the list grows as the older log arrives
    const found = L.found, narrowed = found.length < all.length, sum = f => found.reduce((a, x) => a + f(x), 0);
    const by = k => () => { const c = sort(), d = c.k === k ? -c.d : COLS.find(x => x.k === k).num || k === "spawned" ? -1 : 1; setPref("agents.sort", { k, d }); L.limit = PAGE; ctx.update(); };
    const on = colsOn(), phone = matchMedia("(max-width: 640px)").matches, shown = COLS.filter(c => on.has(c.k)), stack = phone || (matchMedia(STACK_BELOW).matches && !tableFits(shown)), cols = stack ? shown.sort((x, y) => phoneRank(x) - phoneRank(y)) : shown, fits = !stack && tableFits(cols), nf = ["status", "model", "account", "by", "since"].filter(k => L[k] !== "all").length;
    const costs = S.agentCosts; COST_DATA = all.some(a => a.usd > 0 || a.tok > 0);   // no cost data is "–", not a real zero (handoff 33)
    const costNote = costs && costs.at ? `Cost and tokens from the transcripts, read ${ago(costs.at * 1e3)}${costs.refreshing ? ", reading again" : ""}. API list prices; nobody is billed this. Tokens add up every model call's input, output and cache, so context read again on each call counts each time.` : costs && costs.refreshing ? "Reading the transcripts for exact costs; until then these come from the session logs." : "Cost and tokens from the session logs.";
    const totals = stats([
        { label: narrowed ? "Agents shown" : "Agents", value: found.length.toLocaleString(), note: `${found.filter(a => a.status === "running").length} running` },
        { label: "Total cost", value: COST_DATA ? money(sum(a => a.usd)) : "–", note: found.length && COST_DATA ? `${money(sum(a => a.usd) / found.length)} per agent` : null },
        { label: "Tokens", value: COST_DATA ? toks(sum(a => a.tok)) : "–", note: !COST_DATA ? null : `${pctOf(sum(a => a.spend.t[2]), sum(a => a.tok))} re-read from cache; ${toks(sum(a => a.spend.t[1]))} written` },
        { label: "Agent time", value: found.length ? dur(sum(a => a.ran)) : "–", note: found.length ? `added up over ${plural(sum(a => a.runs), "run")}, in ${dur(Date.now() - Math.min(...found.map(a => a.when || Date.now())))}` : null },
    ]);
    // Filters and Columns open into a full-width row under the controls. The row is the fold's sibling, not its body: a body would widen the fold and throw its button to a new line.
    const fold = (k, label) => h("details", { class: "now-more ag-f", open: L[k] || null, ontoggle: (e, n) => { if (e.target === n && !!L[k] !== n.open) { L[k] = n.open; ctx.update(); } } }, h("summary", null, label));
    return metrics({
      header: pageHeader({ subnav: pageNav(), title: "Agents", hint: `Every agent that has run, ${s.k === "spawned" && s.d < 0 ? "newest first" : s.k === "spawned" ? "oldest first" : "sorted by the column you chose"}.`, after: toolbar(
        input({ type: "search", placeholder: `Search name, ${S.app.app ? "conversation" : "thread"}, model, who started it`, "aria-label": "Search agents", value: L.q, oninput: e => set("q", e.target.value) }),
        seg(STATUS.filter(x => ["all", "running", "ended", "failed"].includes(x[0]) || x[0] === L.status).map(([k, l]) => [k, k === "all" ? "All" : l]), L.status, v => set("status", v), { label: "Status" }),
        fold("fOpen", "Filters" + (nf ? ` (${nf})` : "")),
        !L.fOpen ? null : h("div", { class: "toolbar ag-f ag-fp", "data-key": "filters" },
            picker("Status", L.status, STATUS, v => set("status", v)),   // the row above shows the common four; the rest (waiting, killed, at a limit) are here
            picker("Model", L.model, [["all", "Any model"], ...options(all, "model")], v => set("model", v)),
            picker("Account", L.account, [["all", "Any account"], ...options(all, "account")], v => set("account", v)),
            picker("Started by", L.by, BY, v => set("by", v)),
            picker(S.app.app ? "Started" : "Spawned", L.since, SINCE, v => set("since", v))),
        fold("cOpen", "Columns"),
        !L.cOpen ? null : h("div", { class: "toolbar ag-f ag-fp", "data-key": "columns" }, COLS.map(c => h("label", { class: "meta" }, h("input", { type: "checkbox", checked: on.has(c.k) ? "" : null, disabled: c.k === "name" ? "" : null,
            onchange: e => { const n = new Set(on); e.target.checked ? n.add(c.k) : n.delete(c.k); setPref("agents.cols", [...n]); L.sig = ""; ctx.update(); } }), " " + c.label))),
        narrowed && found.length < all.length - nHouse ? button("Clear", () => { Object.assign(L, { q: "", status: "all", by: "all", since: "all", model: "all", account: "all", limit: PAGE }); ctx.update(); }, { kind: "quiet", size: "s" }) : null,
        stack ? picker("Sort", s.k, cols.map(c => [c.k, "Sort: " + c.label]), k => { const d = COLS.find(x => x.k === k).num || k === "spawned" ? -1 : 1; setPref("agents.sort", { k, d }); L.limit = PAGE; ctx.update(); }) : null,   // the column headers sort on a desktop; a phone has no header row
        // The count and its button are one row on a phone: one digit more once the whole log is in wrapped them apart and pushed the list 48 px (R67).
        h("div", { class: "ag-cnt", "data-key": "count" }, h("span", { class: "meta" }, narrowed ? `${found.length} of ${phone && nHouse ? all.length : plural(all.length, "agent")}` : plural(all.length, "agent")),
        nHouse > 0 || L.house ? button(L.house ? "Hide housekeeping" : `+${nHouse} housekeeping`, () => { L.house = !L.house; ctx.update(); }, { kind: "quiet", size: "s", title: "Housekeeping runs: status-bar refreshes, keeper and improve rounds, watchdog trips and phone-call starts" }) : null),
        // On a phone the note and the tiles fold into the controls, so the page opens on the agents; the fold still says the two totals people look for.
        phone ? h("details", { class: "now-more ag-f ag-sum", open: L.sOpen || null, ontoggle: (e, n) => { if (e.target === n) L.sOpen = n.open; } },
          h("summary", null, `Totals: ${found.filter(a => a.status === "running").length} running${COST_DATA ? ", " + money(sum(a => a.usd)) : ""}`), totals, h("p", { class: "ph-meta" }, costNote)) : null),
        meta: phone ? null : [costNote] }),
      sections: [
        phone ? null : totals,
        // grid rows, not <table> (HANDOFF "Shared DOM changes" 6, R7 step 6): one --cols for every row, so the columns line up
        found.length ? h("div", { class: "tblw ag-w" + (fits ? " ag-page" : stack ? " ag-stack" : ""), style: room ? `min-height:${room + (found.length > L.limit ? 0 : moreR)}px` : null }, h("div", { class: "tbl ag", role: "table", "aria-label": "Agents", style: "--cols:" + cols.map(c => colW(c, stack)).join(" ") },
          h("div", { class: "tr th", role: "row" }, cols.map(c => h("span", { role: "columnheader", class: c.num ? "r" : null, title: c.title || null, "aria-sort": s.k === c.k ? (s.d > 0 ? "ascending" : "descending") : null, "data-key": c.k },
            h("button", { type: "button", class: "ag-s", onclick: by(c.k) }, c.label, s.k === c.k ? h("span", { "aria-hidden": "true" }, s.d > 0 ? "▲" : "▼") : null)))),
          h("div", { class: "tb", role: "rowgroup", "data-own": "morph", "data-sig": sig }, changed ? found.slice(0, L.limit).map(a => h("div", { class: "tr", role: "row", "data-key": a.name },
            cols.map(c => h("span", { role: "cell", class: [c.num ? "r num" : null, c.cls || null].filter(Boolean).join(" ") || null, "data-label": stack && !PHONE_BARE.has(c.k) ? c.label : null }, stack && c.k === "name" ? phoneName(a) : c.render(a))))) : null)))
          : empty(all.length ? "No agents match." : "No agents have run yet. When one starts, it shows here."),
        found.length > L.limit ? h("div", { class: "lr-more" },
          button(found.length - L.limit > PAGE * 5 ? `Show ${PAGE * 5} more (${found.length - L.limit} left)` : `Show ${found.length - L.limit} more`, () => { L.limit += PAGE * 5; ctx.update(); }, { kind: "quiet", size: "s" })) : null,
      ],
    });
  },
};
