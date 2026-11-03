// The Agent page: one agent's numbers, what it runs on, its corner of the hive, activity, sessions and posts.
// agentPage(name) is the whole page; the side panel (ui/panel.js) draws the same thing in a narrower container.
import { h } from "../core/dom.js";
import { S, OUTSIDE, SYS, jget } from "../core/store.js";
import { agentCard, agentState, agentStatus, isAgent, isRunning, sEnd, displayText } from "../core/model.js";
import { dur, mdPlain, money, pct, plural, short, stamp, toks } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { metrics, cols } from "../templates.js";
import { button, defs, empty, pageHeader, rank, section, share, statusLabel, stats } from "../ui/kit.js";
import { table } from "../ui/table.js";
import { agentRef, copyButton, threadRef } from "../ui/refs.js";
import { message, pollDelivered } from "../ui/message.js";
import { ego } from "../ui/ego.js";
import { feed, toolTone } from "../ui/feed.js";
import { killButton } from "../actions.js";

// ---- the feed: /api/agent, for one agent at a time (the panel closes when the full page opens) ----
const F = { name: null, items: null, sig: "" };
export async function loadFeed(n, onChange) {
  if (F.name !== n) Object.assign(F, { name: n, items: null, sig: "" });
  try {
    const items = await jget("/api/agent?name=" + encodeURIComponent(n)), sig = JSON.stringify(items);
    if (F.name === n && sig !== F.sig) { F.items = items; F.sig = sig; onChange(); }
  } catch (e) {}
}

/** What the page and the panel poll: the feed while it's running, and the read marks on its posts. */
export async function pollAgent(n, onChange) {
  if (isRunning(n)) loadFeed(n, onChange);
  if (await pollDelivered()) onChange();
}

// ---- what the page reads: scans the board and sessions, so once per data change, not once per poll ----
let memo = { key: null };
function gather(n) {
  const key = `${n}|${S.ver}|${S.uver}`;
  if (memo.key === key) return memo;
  const ss = S.sessions.filter(x => x.name === n), mine = S.posts.filter(p => p.from === n), toMe = S.posts.filter(p => p.men.includes(n) && p.from !== n);
  const first = ss.find(x => !x.resumed) || ss[0], active = ss.reduce((a, x) => a + Math.max(0, sEnd(x) - x.start), 0);
  let cost = 0; const tk = [0, 0, 0, 0], tools = {}, byOut = {};
  if (S.usage) for (const x of ss) {
    const f = x.out && S.usage.files[x.out]; if (!f) continue;
    cost += f.c; byOut[x.out] = f.c;
    for (let i = 0; i < 4; i++) tk[i] += f.t[i];
    for (const k in f.tools) tools[k] = (tools[k] || 0) + f.tools[k];
  }
  const counts = {};
  for (const p of mine) for (const m of p.men) if (m !== n && (isAgent(m) || OUTSIDE.has(m))) counts[m] = (counts[m] || 0) + 1;
  for (const p of toMe) if (!SYS.has(p.from)) counts[p.from] = (counts[p.from] || 0) + 1;
  const links = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  const started = S.sessions.filter(x => !x.resumed && x.from === n), startedNames = [...new Set(started.map(x => x.name))];
  const chain = []; let cur = first;
  for (let i = 0; cur && cur.from && i < 8; i++) {
    chain.unshift(cur.from); if (!isAgent(cur.from)) break;
    cur = S.sessions.find(x => x.name === cur.from && !x.resumed) || S.sessions.find(x => x.name === cur.from);
  }
  const ths = {};
  for (const x of ss) ths[x.thread] = ths[x.thread] || 0;
  for (const p of mine) ths[p.th] = (ths[p.th] || 0) + 1;
  const threads = Object.keys(ths).sort((a, b) => ths[b] - ths[a]);
  const toolList = Object.entries(tools).sort((a, b) => b[1] - a[1]).slice(0, 8);
  return memo = { key, ss, mine, toMe, first, active, cost, tk, byOut, links, started, startedNames, chain, threads, toolList };
}

const ended = x => (!x.end ? (isRunning(x.name) ? "running" : "unknown") : x.exit === 0 ? "finished" : x.exit === 143 ? "stopped by a signal" : x.exit == null ? "ended, no exit code" : S.limitedSess.has(x.session) ? "usage limit" : `exit ${x.exit}`);

/** The whole page for one agent. Used by the view and by the panel. */
export function agentPage(n, host = "page") {
  if (!S.live) return empty("Reading…", { kind: "loading" });
  const D = gather(n), L = S.running.get(n), st = agentStatus(n), task = L && S.byId[L.post];
  const state = L ? agentState(L).text : null;
  const { ss, mine, toMe, first, tk, links, started, startedNames, threads, toolList } = D;
  const read = tk[0] + tk[2] + tk[3], corner = ego({ name: n, links, started: startedNames, threads, parent: first && first.from, version: D.key, host });
  const line = [D.chain.length ? `Started by ${D.chain.join(" → ")} → ${n}` : null, first ? `first seen ${stamp(first.start)}` : null];
  return h("div", { "data-key": n }, metrics({
    header: pageHeader({
      title: h("span", { class: "ref-wrap" }, n, isAgent(n) ? copyButton("@" + n) : null),
      lead: L ? L.doing || "Starting" : null,
      actions: [button("All their posts", null, { href: threadHref("@" + n), size: "s" }), L ? threadRef(L.thread) : null, L ? killButton(n) : null],
      meta: [st.key === "outside" ? null : statusLabel(st.key, state || st.label || null, { live: isRunning(n) }), ...line],
      after: L && (task || L.said) ? h("dl", { class: "ac-dl" },
        task ? [h("dt", null, "Asked"), h("dd", null, `${task.from === "person" && S.app.app ? "you" : task.from}: `, short(mdPlain(displayText(task)), 600))] : null,
        L.said ? [h("dt", null, "Said"), h("dd", null, L.said)] : null) : null,
    }),
    sections: [
      defs(agentCard(n)),
      section({ title: "In numbers" }, stats([
        { label: "Cost", value: S.usage ? money(D.cost) : "…", note: L ? "as of its last finished turn, at API list prices" : "at API list prices" },
        { label: "Tokens written", value: toks(tk[1]), note: `${toks(read)} read, ${pct(tk[2], read)} from cache` },
        { label: "Time running", value: ss.length ? dur(D.active) : "none", note: plural(ss.length, "session") },
        { label: "Posts written", value: mine.length, note: `${plural(toMe.length, "mention")} of it by others` },
        { label: "Agents it started", value: started.length, note: started.length ? plural(new Set(started.map(x => x.thread)).size, "thread") : "none" },
        { label: "Talks with", value: links.length, note: links.length ? `most with ${links[0][0]}` : "nobody yet" },
      ])),
      cols(
        h("div", { class: "stack" },
          section({ title: "Its corner of the hive", note: corner ? "Thicker line: more messages. Arrow: started. Box: thread. Drag a node; ctrl+scroll or pinch zooms." : null }, corner || empty("No exchanges yet.")),
          startedNames.length ? section({ title: "Started" }, h("div", { class: "ref-row" }, startedNames.slice(0, 30).map(x => h("span", { "data-key": x }, agentRef(x, { copy: true }))))) : null,
          threads.length ? section({ title: "Threads" }, h("div", { class: "ref-row" }, threads.slice(0, 16).map(x => h("span", { "data-key": x }, threadRef(x))))) : null,
          toolList.length ? section({ title: "What it reached for" },
            share(toolList.map(([k, v]) => ({ key: k, label: k, value: v, tone: toolTone(k), text: String(v) })), { label: "Tool calls by tool" }),
            rank(toolList.map(([k, v]) => ({ key: k, label: k, value: v, text: String(v), tone: toolTone(k) })))) : null),
        section({ title: "Latest activity", note: "tool calls and what it said, newest first" }, feed(F.name === n ? F.items : null))),
      section({ title: "Sessions" }, table({
        rows: [...ss].reverse().slice(0, 40), key: x => String(x.start), empty: "No sessions.", caption: `${n}'s sessions`,
        columns: [
          { label: "Started", render: x => stamp(x.start) },
          { label: "Thread", render: x => threadRef(x.thread) },
          { label: "Woken by", render: x => [x.from && isAgent(x.from) ? agentRef(x.from) : x.from || "?", x.resumed ? " (resumed)" : ""] },
          { label: "Ran", num: true, render: x => dur(sEnd(x) - x.start) },
          { label: "Cost", num: true, render: x => (x.out && D.byOut[x.out] != null ? money(D.byOut[x.out]) : "–") },
          { label: "How it ended", render: ended },
        ],
      })),
      section({ title: "Latest posts" }, mine.length ? h("div", { class: "msg-col" }, mine.slice(-12).reverse().map(p => message(p))) : empty("No posts yet.")),
    ],
  }));
}

export default {
  id: "agent", label: "Agent", tab: false,
  enter(ctx) { if (!ctx.arg) { location.hash = "#now"; return; } loadFeed(ctx.arg, ctx.update); },
  poll: { every: 2000, run(ctx) { if (ctx.arg) pollAgent(ctx.arg, ctx.update); } },
  render(ctx) { return ctx.arg ? agentPage(ctx.arg) : empty("Reading…", { kind: "loading" }); },
};
