// The Agent page: one agent. What it is doing now and whether it is stuck, its numbers, its cost over its life,
// its recent steps, its threads, what it changed, who it talks to, its sessions and posts.
// agentPage(name) is the whole page; the side panel (ui/panel.js) draws the same thing in a narrower container.
import { h } from "../core/dom.js";
import { S, OUTSIDE, SYS, jget, loadAgentCosts, post, poll, can } from "../core/store.js";
import { agentCard, agentSpend, agentState, agentStatus, isAgent, isRunning, isWarden, modelName, WARDEN_HINT, sEnd, displayText } from "../core/model.js";
import { DAY, HOUR, MIN, ago, dur, mdPlain, money, now, pct, plural, short, stamp, toks } from "../core/format.js";
import { threadHref } from "../core/router.js";
import { metrics, cols } from "../templates.js";
import { button, defs, empty, legend, list, note, pageHeader, rank, section, share, statusLabel, stats, wardenBadge } from "../ui/kit.js";
import { table } from "../ui/table.js";
import { agentRef, copyButton, threadRef } from "../ui/refs.js";
import { message, pollDelivered, fileUrl } from "../ui/message.js";
import { contacts, lifeChart, priceBuckets, stepFor, tokenLine } from "../ui/agentviz.js";
import { feed } from "../ui/feed.js";
import { killButton } from "../actions.js";
import { agentActions } from "../ui/agentHover.js";

// ---- what the server reads for one agent at a time (the panel closes when the full page opens) ----
// F: /api/agent, its latest tool calls and words. W: /api/agent-work, files it changed, commits, PRs and tokens per 5 min.
const F = { name: null, items: null, sig: "" }, W = { name: null, data: null, sig: "", at: 0 };
async function loadWork(n, onChange) {
  W.at = now();
  try {
    const r = await jget("/api/agent-work?name=" + encodeURIComponent(n)) || {};
    // fill what's missing: the preview fixtures and older servers send nothing or only part of it
    const d = { files: [], commits: [], prs: [], b: [], nfiles: 0, ncommits: 0, ...r }, sig = JSON.stringify(d);
    for (const k of ["files", "commits", "prs", "b"]) if (!Array.isArray(d[k])) d[k] = [];
    d.nfiles = d.nfiles || d.files.length; d.ncommits = d.ncommits || d.commits.length;
    if (W.name === n && sig !== W.sig) { W.data = d; W.sig = sig; onChange(); }
  } catch (e) {}
}
// DF: the one file whose diff is open under "What it changed" (click a file again to close it)
const DF = { name: null, p: null, data: null }; let again = () => {};
async function openDiff(n, p) {
  if (DF.name === n && DF.p === p) { Object.assign(DF, { p: null, data: null }); again(); return; }
  Object.assign(DF, { name: n, p, data: null }); again();
  let d; try { d = await jget(`/api/agent-diff?name=${encodeURIComponent(n)}&path=${encodeURIComponent(p)}`) || {}; } catch (e) { d = { error: e.message }; }
  if (DF.name === n && DF.p === p) { DF.data = d; again(); }
}
// IN: /api/agent-input, what went into each model call by source; `open` is the turn (its time) whose raw pieces are shown
const IN = { name: null, data: null, sig: "", at: 0, open: null };
async function loadInput(n, onChange) {
  IN.at = now();
  try {
    const d = await jget("/api/agent-input?name=" + encodeURIComponent(n)) || {}, sig = JSON.stringify(d);
    if (IN.name === n && sig !== IN.sig) { IN.data = d; IN.sig = sig; onChange(); }
  } catch (e) {}
}
export async function loadFeed(n, onChange) {
  again = onChange;
  if (IN.name !== n) { Object.assign(IN, { name: n, data: null, sig: "", at: 0, open: null }); loadInput(n, onChange); }
  if (F.name !== n) { Object.assign(F, { name: n, items: null, sig: "" }); loadAgentCosts().then(onChange); }
  if (W.name !== n) { Object.assign(W, { name: n, data: null, sig: "", at: 0 }); loadWork(n, onChange); }
  try {
    const items = await jget("/api/agent?name=" + encodeURIComponent(n)), sig = JSON.stringify(items);
    if (F.name === n && sig !== F.sig) { F.items = items; F.sig = sig; onChange(); }
  } catch (e) {}
}

/** What the page and the panel poll: the feed while it's running (its work every 20 s), and the read marks on its posts. */
export async function pollAgent(n, onChange) {
  if (isRunning(n)) { loadFeed(n, onChange); if (now() - W.at > 20e3) loadWork(n, onChange); if (now() - IN.at > 10e3) loadInput(n, onChange); }
  if (await pollDelivered()) onChange();
}

// ---- what the page reads: scans the board and sessions, so once per data change, not once per poll ----
let memo = { key: null };
function gather(n) {
  const key = `${n}|${S.ver}|${S.uver}|${W.sig.length}|${W.name}`;
  if (memo.key === key) return memo;
  const ss = S.sessions.filter(x => x.name === n), mine = S.posts.filter(p => p.from === n), toMe = S.posts.filter(p => p.men.includes(n) && p.from !== n);
  const first = ss.find(x => !x.resumed) || ss[0], active = ss.reduce((a, x) => a + Math.max(0, sEnd(x) - x.start), 0);
  let cost = 0; const tk = [0, 0, 0, 0], tools = {}, byOut = {}, models = {}, hours = {};
  if (S.usage) for (const x of ss) {
    const f = x.out && S.usage.files[x.out]; if (!f) continue;
    cost += f.c; byOut[x.out] = f.c;
    for (let i = 0; i < 4; i++) tk[i] += f.t[i];
    for (const k in f.m) models[k] = (models[k] || 0) + f.m[k];
    for (const k in f.tools) tools[k] = (tools[k] || 0) + f.tools[k];
    for (const k in f.h || {}) hours[k] = (hours[k] || 0) + f.h[k];
  }
  // who it talks to: posts it wrote naming them, posts they wrote naming it
  const C = {}, at = m => (C[m] ||= { name: m, sent: 0, got: 0, tags: [], last: 0 });
  for (const p of mine) for (const m of new Set(p.men)) if (m !== n && (isAgent(m) || OUTSIDE.has(m))) { const c = at(m); c.sent++; c.last = Math.max(c.last, p.t); }
  for (const p of toMe) if (!SYS.has(p.from)) { const c = at(p.from); c.got++; c.last = Math.max(c.last, p.t); }
  const started = S.sessions.filter(x => !x.resumed && x.from === n), startedNames = [...new Set(started.map(x => x.name))];
  const chain = []; let cur = first;
  for (let i = 0; cur && cur.from && i < 8; i++) {
    chain.unshift(cur.from); if (!isAgent(cur.from)) break;
    cur = S.sessions.find(x => x.name === cur.from && !x.resumed) || S.sessions.find(x => x.name === cur.from);
  }
  const parent = first && first.from && first.from !== "anon" ? first.from : null;
  if (parent && !SYS.has(parent)) at(parent).tags.push("started it");
  for (const m of startedNames) at(m).tags.push("it started");
  const contactRows = Object.values(C).sort((a, b) => b.sent + b.got - (a.sent + a.got) || b.last - a.last || (a.name < b.name ? -1 : 1));
  // its threads: sessions there, posts there, when it was last there
  const T = {}, th = t => (T[t] ||= { name: t, posts: 0, runs: 0, last: 0 });
  for (const x of ss) { const r = th(x.thread); r.runs++; r.last = Math.max(r.last, sEnd(x)); }
  for (const p of mine) { const r = th(p.th); r.posts++; r.last = Math.max(r.last, p.t); }
  const threads = Object.values(T).filter(r => r.name).sort((a, b) => b.last - a.last);
  const toolList = Object.entries(tools).sort((a, b) => b[1] - a[1]).slice(0, 8);
  const spend = agentSpend(n, { c: cost, t: tk, m: models }), turns = ss.reduce((a, x) => a + ((S.usage && x.out && S.usage.files[x.out] && S.usage.files[x.out].turns) || 0), 0);
  const toolCalls = Object.values(tools).reduce((a, b) => a + b, 0);
  const attached = mine.flatMap(p => (p.files || []).map(f => ({ p, f })));
  return memo = { key, ss, mine, toMe, first, active, spend, turns, toolCalls, byOut, contactRows, started, startedNames, chain, threads, toolList, hours, attached, life: null };
}

const ended = x => (!x.end ? (isRunning(x.name) ? "running" : "unknown") : x.exit === 0 ? "finished" : x.exit === 143 ? "stopped by a signal" : x.exit == null ? "ended, no exit code" : x.limited ? "usage limit" : `exit ${x.exit}`);
const endTone = x => (!x.end ? (isRunning(x.name) ? "good" : "quiet") : x.exit === 0 || x.exit === 143 || x.exit == null ? "ended" : x.limited ? "warn" : "bad");

// ---- its life on one time axis: cost per step, sessions, posts ----
const SPAN_MAX = 14 * DAY;
function life(n, D) {
  const { ss, mine, toMe, spend } = D, L = isRunning(n);
  const times = [...ss.map(x => x.start), ...mine.map(p => p.t)];
  if (!times.length) return null;
  const end = Math.max(L ? now() : 0, ...ss.map(sEnd), ...mine.map(p => p.t));
  let t0 = Math.min(...times); const clipped = end - t0 > SPAN_MAX; if (clipped) t0 = end - SPAN_MAX;
  const work = W.name === n && W.data && W.data.b && W.data.b.length ? W.data.b : null;
  let step = stepFor(Math.max(end - t0, 30 * MIN), 48); if (!work) step = Math.max(step, HOUR);
  t0 = Math.floor(t0 / step) * step;
  const N = Math.max(1, Math.ceil((end - t0) / step)), t1 = t0 + N * step;
  let bars;
  if (work) {
    bars = priceBuckets(work, t0, step, N, spend.usd || 0).map(b => ({ v: b.v, tip: b.calls ? [`about ${money(b.v)}, ${plural(b.calls, "model call")}`, tokenLine(b.t)] : ["no model calls"] }));
  } else {
    bars = Array.from({ length: N }, () => ({ v: 0 }));
    for (const k in D.hours) { const i = Math.floor((k * 1e3 - t0) / step); if (i >= 0 && i < N) bars[i].v += D.hours[k]; }
    bars.forEach(b => (b.tip = [b.v ? money(b.v) : "no cost counted"]));
  }
  const lanes = ss.filter(x => sEnd(x) >= t0).map(x => ({ a: Math.max(x.start, t0), b: sEnd(x), tone: endTone(x), tip: `${x.resumed ? "resumed" : "started"} in ${x.thread} by ${x.from || "?"}, ${ended(x)}` }));
  const ticks = [...mine.filter(p => p.t >= t0).map(p => ({ t: p.t, kind: "out" })), ...toMe.filter(p => p.t >= t0 && !SYS.has(p.from)).map(p => ({ t: p.t, kind: "in" }))];
  const total = bars.reduce((a, b) => a + b.v, 0);
  return { chart: lifeChart({
    t0, t1, step, bars, lanes, ticks, top: m => money(m),
    label: `${n}'s cost per ${dur(step)} from ${stamp(t0)} to ${stamp(t1)}: ${money(total)} in all`,
    legend: legend([
      { label: "Cost", text: `per ${dur(step)}${work ? ", split by tokens" : ""}`, tone: "info" },
      { label: "Sessions", text: "grey ended, green running, red failed, amber at a limit", tone: "ended" },
      { label: "Ticks", text: "dark: it wrote, light: it was named", tone: "ink" },
    ]),
  }), note: (clipped ? "Last 14 days. " : "") + (work ? "Each bar is its share of the total by tokens used then, so it's an estimate." : "By hour, from the cost of each finished turn.") };
}

// ---- the parts ----
function nowBlock(n, L) {
  if (!L) return null;
  const st = agentState(L), task = S.byId[L.post], quiet = L.last ? now() - L.last * 1e3 : null;
  const rows = h("dl", { class: "ac-dl" },
    task ? [h("dt", null, "Asked"), h("dd", null, `${task.from === "person" && S.app.app ? "you" : task.from}: `, short(mdPlain(displayText(task)), 600))] : null,
    L.said ? [h("dt", null, "Said"), h("dd", null, L.said)] : null,
    [h("dt", null, "Last step"), h("dd", null, quiet == null ? "none yet" : quiet < MIN ? "just now" : ago(L.last * 1e3))]);
  return st.key === "stuck" ? note({ tone: "bad", title: `Looks stuck: ${st.text}`, actions: killButton(n), key: "stuck" }, rows) : rows;
}

// ---- what went into each model call: exact token counts from the log, the new content sized by an estimate and split by where it came from ----
const SRC = { self: "its own earlier output", tool: "tool result", hive: "sent to it" };
function inputs(n, panel) {
  const d = IN.name === n ? IN.data : null;
  if (!d) return empty("Reading…", { kind: "loading" });
  const turns = (d.turns || []).slice().reverse();
  if (!turns.length) return empty("No model calls in its latest session log.");
  const limited = turns.every(t => t.parts.every(p => !(p.items || []).some(Boolean)));
  const rows = turns.slice(0, panel ? 8 : 40), open = IN.open;
  const sum = t => t.parts.slice().sort((a, b) => b.n - a.n).map(p => `${p.label} ${toks(p.n)}`).join(", ") || "nothing new";
  return h("div", { class: "stack" },
    h("p", { class: "meta" }, "One row per model call, newest first. The totals are exact, from the session log. The pieces are what arrived since the call before, sized at about 4 characters a token, so they're an estimate; \"carried\" is everything already in the context. On a session's first call that is the system prompt, tool list, memory and the first message, which the log doesn't keep."),
    h("ul", { class: "wf" }, rows.map(t => {
      const on = open === t.t, bits = t.parts.slice().sort((a, b) => b.n - a.n);
      return [h("li", { "data-key": "in" + t.t, class: "wf-f wf-in", role: "button", tabindex: 0, "aria-expanded": String(on),
        onclick: () => { IN.open = on ? null : t.t; again(); }, onkeydown: e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); IN.open = on ? null : t.t; again(); } } },
        h("code", null, `${stamp(t.t * 1e3)} · ${toks(t.total)} in`), h("span", { class: "meta" }, `carried ${toks(t.carried)}${bits.length ? "; new: " + sum(t) : ""}`)),
        on ? h("li", { "data-key": "ind" + t.t, class: "wf-diff" },
          h("p", { class: "meta" }, `${toks(t.fresh)} uncached, ${toks(t.cw)} written to the cache, ${toks(t.cr)} read from it; ${toks(t.out)} output.`),
          bits.length ? bits.map(p => h("div", { class: "stack", "data-key": p.src + p.label }, h("p", null, h("b", null, p.label), h("span", { class: "meta" }, ` · ${SRC[p.src]} · about ${toks(p.n)} tokens${p.items.length > 1 ? `, ${p.items.length} pieces` : ""}`)),
            limited ? null : h("pre", { class: "diff" }, p.items.filter(Boolean).slice(0, 5).join("\n———\n")))) : h("p", { class: "meta" }, "Nothing new since the call before: it all came from the carried context.")) : null];
    })),
    limited ? h("p", { class: "meta" }, "Your sign-in sees the sizes, not the text.") : null);
}

function diffView(d) {
  if (!d) return h("p", { class: "meta" }, "Reading…");
  if (d.error) return h("p", { class: "meta", role: "alert" }, d.error);
  if (!d.diff.trim()) return h("p", { class: "meta" }, `No difference to show (${d.what}).`);
  const lines = d.diff.split("\n").map(l => h("span", { class: "dl " + (/^(\+\+\+|---|diff |index )/.test(l) ? "dh" : l[0] === "+" ? "da" : l[0] === "-" ? "dd" : l[0] === "@" ? "dk" : "") }, l + "\n"));
  return [h("p", { class: "meta" }, d.what + (d.cut ? "; cut at 60 KB" : "")), h("pre", { class: "diff", "data-diff": "1" }, lines)];
}

function changed(n, D, host) {
  const w = W.name === n ? W.data : null, panel = host === "panel";
  if (!w) return empty("Reading…", { kind: "loading" });
  const limited = w.nfiles && !w.files.length;
  const parts = [];
  if (w.prs.length) parts.push(section({ title: "Pull requests", note: "opened by this agent", level: null }, list(w.prs.map((x, i) => ({ key: "pr" + i, title: x.x, meta: ago(x.t * 1e3) })), { dense: true })));
  if (w.commits.length) parts.push(section({ title: "Commits", note: w.ncommits > w.commits.length ? `newest ${w.commits.length} of ${w.ncommits}` : null }, list(w.commits.map((x, i) => ({ key: "c" + i + x.t, title: x.x, meta: ago(x.t * 1e3) })), { dense: true, limit: panel ? 4 : 10 })));
  if (w.files.length) parts.push(section({ title: "Files", note: `${plural(w.nfiles, "file")}, newest first` },
    h("ul", { class: "wf" }, w.files.slice(0, panel ? 8 : 30).map(f => [h("li", { "data-key": f.p, class: "wf-f", role: "button", tabindex: 0, "aria-expanded": String(DF.name === n && DF.p === f.p),
      title: `${f.p}: ${plural(f.n, "change")}${f.k === "shell" ? " from the shell" : ""}, last ${stamp(f.t * 1e3)}. Click to see the diff.`,
      onclick: () => openDiff(n, f.p), onkeydown: e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openDiff(n, f.p); } } },
      h("code", null, "\u200e" + f.p + "\u200e"), h("span", { class: "meta" }, `${f.n > 1 ? f.n + "× · " : ""}${ago(f.t * 1e3)}`)),
      DF.name === n && DF.p === f.p ? h("li", { class: "wf-diff" }, diffView(DF.data)) : null])),
    w.files.length > (panel ? 8 : 30) ? h("p", { class: "meta" }, `and ${w.files.length - (panel ? 8 : 30)} more`) : null));
  if (D.attached.length) parts.push(section({ title: "Files it posted", note: "attached to its posts" }, h("ul", { class: "wf" }, D.attached.slice(-8).reverse().map(({ p, f }) => h("li", { "data-key": p.id + f.name },
    h("a", { href: fileUrl(p, f), target: "_blank", rel: "noopener" }, f.name), h("span", { class: "meta" }, ago(p.t)))))));
  if (limited) parts.push(h("p", { class: "meta" }, `${plural(w.nfiles, "file")} changed, ${plural(w.ncommits, "commit")}; your sign-in sees counts only.`));
  return parts.length ? h("div", { class: "stack" }, parts) : empty("Nothing it edited, committed or posted shows in its logs.");
}

// ---- the Model row as a menu: the pick holds from the agent's next run, like a post with --model (the person, 2026-11-04) ----
const PICKS = [["opus", "Opus"], ["sonnet", "Sonnet"], ["haiku", "Haiku"], ["fable", "Fable"], ["astra", "Astra", "codex:gpt-6-astra"]];
const MP = { busy: null, err: null };
function modelPicker(n, row, L) {
  const ss = S.sessions.filter(s => s.name === n), asked = (ss[ss.length - 1] || {}).model || "";
  const next = ((S.live && S.live.chosen) || {})[n] || "", word = (PICKS.find(([w, , id]) => (id || w) === next) || [])[0] || "";
  const moved = next && next !== asked, label = moved ? (PICKS.find(([w]) => w === word) || [, next])[1] : null;
  const pick = async e => {
    const m = e.target.value; if (!m) return;
    MP.busy = n; MP.err = null;
    try { await post("/api/agent-model", { name: n, model: m }); } catch (x) { MP.err = x.message; }
    MP.busy = null; await poll();
  };
  return h("span", { class: "mpick" },
    moved ? h("s", { class: "meta", title: "the model its current or last run used" }, row.v) : row.v,
    h("select", { class: "cp-model", "aria-label": `Model for ${n}'s next run`, disabled: MP.busy === n,
      title: L ? `${n} is running on ${row.v}. Pick another and it moves to it from its next run (it can't switch mid-run), and stays there.` : `${n} wakes on ${moved ? label : row.v}. Pick another and its next run starts on that one, and stays there. Nothing wakes it now.`,
      onchange: pick },
      [["", moved ? `→ ${label} next run` : "Change…"], ...PICKS.map(([w, l]) => [w, l])].map(([v, l]) => h("option", { value: v, selected: v === "" }, l))),
    MP.err ? h("span", { class: "meta", role: "alert" }, " " + MP.err) : null);
}

/** The whole page for one agent. Used by the view and by the panel. */
export function agentPage(n, host = "page") {
  if (!S.live) return empty("Reading…", { kind: "loading" });
  const D = gather(n), L = S.running.get(n), st = agentStatus(n), panel = host === "panel";
  const { ss, mine, toMe, spend, contactRows, started, threads, toolList } = D;
  if (!D.life) D.life = life(n, D) || {};
  const tk = spend.t, read = tk[0] + tk[2] + tk[3], byModel = Object.entries(spend.m || {}).filter(x => x[1] > 0).sort((a, b) => b[1] - a[1]);
  const line = [D.chain.length ? `Started by ${D.chain.join(" → ")}` : null, D.first ? `first seen ${stamp(D.first.start)}` : null];
  const w = W.name === n ? W.data : null;
  const last = mine[mine.length - 1];
  return h("div", { class: "agp", "data-key": n, "data-host": host }, metrics({
    header: pageHeader({
      title: h("span", { class: "ref-wrap" }, n, isAgent(n) ? copyButton("@" + n) : null, isWarden(n) ? wardenBadge(WARDEN_HINT) : null),
      lead: L ? L.doing || "Starting" : isWarden(n) ? WARDEN_HINT : last ? `Last said: ${short(mdPlain(displayText(last)), 220)}` : null,
      tone: L && agentState(L).key === "stuck" ? "bad" : null,
      actions: [L ? button("Open conversation", null, { href: threadHref(L.thread), size: "s", title: "Open " + L.thread }) : null, ...agentActions(n), button("All their posts", null, { href: threadHref("@" + n), size: "s" }), L ? killButton(n) : null],
      meta: [st.key === "outside" ? null : statusLabel(st.key, st.label || null, { live: isRunning(n) }), ...line],
      after: [nowBlock(n, L), defs(agentCard(n).map(r => r.k === "Model" && isAgent(n) && !isWarden(n) && can("post") ? { ...r, v: modelPicker(n, r, L) } : r))],
    }),
    sections: [
      section({ title: "In numbers", note: "this agent's turns, time and cost" }, stats([
        { label: "Cost", value: S.usage ? money(spend.usd) : "…", note: `${toks(tk[0] + tk[1] + tk[2] + tk[3])} tokens, ${pct(tk[2], read)} of reads from cache` + (spend.sub ? `; ${money(spend.sub)} in subagents` : ""), title: `At API list prices, from the ${spend.src}.` + (byModel.length > 1 ? " " + byModel.map(([k, v]) => `${modelName(k)} ${money(v)}`).join(", ") + "." : "") + (L ? " A turn under way may not be counted yet." : "") },
        { label: "Time running", value: ss.length ? dur(D.active) : "none", note: plural(ss.length, "session") + (spend.calls != null ? `, ${spend.calls.toLocaleString()} model calls` : "") },
        { label: "Posts", value: mine.length, note: `named in ${plural(toMe.length, "post")}; ${plural(threads.length, "thread")}` },
        { label: "Changed", value: w ? plural(w.nfiles, "file") : "…", note: w ? [plural(w.ncommits, "commit"), w.prs.length ? plural(w.prs.length, "PR") : null, started.length ? `started ${plural(D.startedNames.length, "agent")}` : null].filter(Boolean).join(", ") : null },
      ])),
      D.life.chart ? section({ title: "Over its life", note: D.life.note }, D.life.chart) : null,
      cols(
        section({ title: "Recent steps", note: "tool calls and what it said, newest first" },
          toolList.length ? share(toolList.map(([k, v]) => ({ key: k, label: k, value: v, tone: "data", text: String(v) })), { label: "Tool calls by tool" }) : null,
          feed(F.name === n ? F.items : null)),
        h("div", { class: "stack" },
          section({ title: "Threads", note: threads.length > 1 ? "latest first" : null }, table({
            rows: threads.slice(0, panel ? 6 : 20), key: r => r.name, empty: "No threads.", caption: `${n}'s threads`,
            columns: [
              { label: "Thread", render: r => [threadRef(r.name), L && L.thread === r.name ? h("span", { class: "meta" }, " · on it now") : null] },
              { label: "Posts", num: true, render: r => r.posts },
              { label: "Runs", num: true, render: r => r.runs },
              { label: "Last", num: true, render: r => ago(r.last) },
            ],
          })),
          section({ title: "Who it talks to", note: "posts naming each other" }, contacts(contactRows, { limit: panel ? 8 : 16 })))),
      section({ title: "What goes into each turn", note: "its input to each model call, by source" }, inputs(n, panel)),
      section({ title: "What it changed", note: w && w.nfiles ? "from its tool calls; edits made from the shell are found where they can be" : null }, changed(n, D, host)),
      section({ title: "Sessions", note: "Cost per session is from its session log, so these can add up to less than the total above." }, table({
        rows: [...ss].reverse().slice(0, panel ? 8 : 40), key: x => String(x.start), empty: "No sessions.", caption: `${n}'s sessions`,
        columns: [
          { label: "Started", render: x => stamp(x.start) },
          { label: "Thread", render: x => threadRef(x.thread) },
          { label: "Woken by", render: x => [x.from && isAgent(x.from) ? agentRef(x.from) : x.from === "person" && S.app.app ? "you" : x.from || "?", x.resumed ? " (resumed)" : ""] },
          { label: "Ran", num: true, render: x => dur(sEnd(x) - x.start) },
          { label: "Cost", num: true, render: x => (x.out && D.byOut[x.out] != null ? money(D.byOut[x.out]) : "–") },
          { label: "How it ended", render: x => h("span", { class: "sess-end", "data-tone": endTone(x) }, ended(x)) },
        ],
      })),
      section({ title: "Latest posts", note: "what it wrote on the board" }, mine.length ? h("div", { class: "msg-col" }, mine.slice(panel ? -5 : -12).reverse().map(p => message(p))) : empty("No posts yet.")),
    ],
  }));
}

export default {
  id: "agent", label: "Agent", tab: false,
  enter(ctx) { if (!ctx.arg) { location.hash = "#now"; return; } loadFeed(ctx.arg, ctx.update); },
  poll: { every: 2000, run(ctx) { if (ctx.arg) pollAgent(ctx.arg, ctx.update); } },
  render(ctx) { return ctx.arg ? agentPage(ctx.arg) : empty("Reading…", { kind: "loading" }); },
};
