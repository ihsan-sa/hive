// Mesh: who talks with whom. A thread is a rectangle with a line to each agent that worked on it; an agent only there sits beside it,
// an agent on several threads sits between them with a line to each. Links between discs run as bundles; weak links fade when busy.
import { h, selecting } from "../core/dom.js";
import { S, OUTSIDE, SYS, can, jget, subscribe, getPref, setPref, onTV } from "../core/store.js";
import { agentCard, agentThread, agentStatus, isAgent, isRunning, sEnd, tierOf, fileInfo } from "../core/model.js";
import { ago, clock, dur, firstSentence, money, now, short, stamp, toks, plural, DAY, HOUR } from "../core/format.js";
import { agentHref, threadHref, href } from "../core/router.js";
import { status, tier, STATUS } from "../core/status.js";
import { askTier } from "../actions.js";
import { canvas } from "../templates.js";
import { button, list, section, seg, statusDot, stats, share } from "../ui/kit.js";
import { agentRef, threadRef } from "../ui/refs.js";
import { createGraph, keep } from "../ui/graph.js";
import { openPanel } from "../ui/panel.js";
import { showTip, hideTip } from "../ui/tip.js";
import { toolTone } from "../ui/feed.js";

const WINS = [["1h", HOUR], ["6h", 6 * HOUR], ["24h", DAY], ["3d", 3 * DAY], ["All", 0]];
const MODES = [["agents", "Agents"], ["threads", "Agents + threads"]];
const M = {
  win: getPref("meshWin", 6 * HOUR), mode: getPref("meshMode", "threads"), active: !!getPref("meshActive", onTV() ? 1 : 0), cut: null, playing: false,
  raw: new Map(), edges: [], count: "", seenVer: -1, lastBuild: 0, sideW: +localStorage.getItem("sideW") || 0, update: () => {}, feeds: {},
};
const winStart = () => (M.win ? now() - M.win : S.posts[0] ? S.posts[0].t : now() - DAY);
const curT = () => M.cut ?? now();
const nm = i => (i.startsWith("#") ? i.slice(1) : i);

function lastSeen(id, t1) { let l = 0; for (const s of S.sessions) if (s.name === id && s.start <= t1) l = Math.max(l, Math.min(sEnd(s), t1)); return l; }
const stateText = n => (n.live ? "running" : n.last ? "ended " + ago(n.last) : "not started yet");
function opacity(n) {
  if (n.kind === "outside" || n.live) return 1;
  const age = curT() - n.last;
  return n.kind === "thread" ? Math.max(.32, 1 - age / (12 * HOUR)) : Math.max(.55, .92 * Math.exp(-age / (6 * HOUR)));
}
function look(n) {
  if (n.kind === "outside") return { tone: "ink", shape: "diamond" };
  if (n.kind === "thread") return { tone: tier(n.tier).tone, shape: "dot" };
  const st = status(agentStatus(n.id).key);
  return { tone: st.tone, shape: st.shape };
}

function build() {
  const t1 = curT(), t0 = winStart(), nodes = new Map(), E = new Map(), threadMode = M.mode === "threads";
  const node = (id, kind) => { let n = nodes.get(id); if (!n) { n = { id, kind, posts: 0, last: 0, tier: "other", deg: 0, live: false }; nodes.set(id, n); } return n; };
  const edge = (a, b, kind) => { const k = a < b ? a + "|" + b : b + "|" + a; let e = E.get(k); if (!e) { e = { a: a < b ? a : b, b: a < b ? b : a, n: 0, spawn: 0, last: 0, kind }; E.set(k, e); } return e; };
  const known = n => isAgent(n) || OUTSIDE.has(n), kindOf = n => (OUTSIDE.has(n) ? "outside" : "agent");
  // homes: every thread an agent worked on in this window, and how much (a post counts 1, a run 3, a run going now 6)
  const homes = new Map(), posted = new Map(), tposts = new Map();
  const home = (a, th, w) => { if (!th) return; let m = homes.get(a); if (!m) homes.set(a, (m = new Map())); m.set(th, (m.get(th) || 0) + w); };
  for (const p of S.posts) {
    if (p.t < t0) continue; if (p.t > t1) break; if (SYS.has(p.from) || !known(p.from)) continue;
    const f = node(p.from, kindOf(p.from)); f.posts++; f.last = Math.max(f.last, p.t); if (f.kind === "agent") { f.tier = tierOf(p.th); home(p.from, p.th, 1); posted.set(p.from + "\n" + p.th, (posted.get(p.from + "\n" + p.th) || 0) + 1); }
    tposts.set(p.th, (tposts.get(p.th) || 0) + 1);
    for (const m of p.men) { if (m === p.from || !known(m)) continue; node(m, kindOf(m)); const e = edge(p.from, m, "msg"); e.n++; e.last = Math.max(e.last, p.t); }
  }
  for (const s of S.sessions) {
    if (s.start > t1) continue; const en = sEnd(s); if (en < t0) continue;
    const n = node(s.name, "agent"); n.tier = tierOf(s.thread); n.thread = s.thread; n.last = Math.max(n.last, Math.min(en, t1));
    const live = s.start <= t1 && en >= t1 && (M.cut != null || isRunning(s.name));
    if (live) n.live = true;
    home(s.name, s.thread, live ? 6 : 3);
    if (!s.resumed && s.start >= t0 && s.from && known(s.from) && s.from !== s.name) { node(s.from, kindOf(s.from)); const e = edge(s.from, s.name, "msg"); e.spawn++; e.spawnFrom = s.from; e.last = Math.max(e.last, s.start); }
  }
  // a node with no session in the window still belongs to its thread; one only named here did nothing here, so look further back
  for (const n of nodes.values()) if (n.kind === "agent" && !n.thread) { n.thread = agentThread(n.id); n.tier = tierOf(n.thread); }
  for (const n of nodes.values()) if (n.kind === "agent" && !n.last) n.last = lastSeen(n.id, t1);
  // active only: running agents and the links among them
  if (M.active) {
    for (const n of [...nodes.values()]) if (!n.live) nodes.delete(n.id);
    for (const [k, e] of [...E]) if (!nodes.has(e.a) || !nodes.has(e.b)) E.delete(k);
  }
  const touched = new Set(); for (const e of E.values()) { touched.add(e.a).add(e.b); nodes.get(e.a).deg++; nodes.get(e.b).deg++; }
  // an ended agent with no link to anyone in this window is only dust
  for (const n of [...nodes.values()]) if (n.kind === "agent" && !n.live && !touched.has(n.id)) nodes.delete(n.id);

  // where each agent sits: one home, in that thread's group; several, between the groups. Every home gets a line where the thread is
  // drawn; with Agents alone the thread is only a grey name, so a line there would end at nothing and only several homes get one.
  // A home under a sixth of the agent's main one is a passing visit and isn't drawn; at most four are.
  const labels = {};
  for (const n of [...nodes.values()]) {
    if (n.kind !== "agent") continue;
    const m = homes.get(n.id) || new Map(); if (!m.size && n.thread) m.set(n.thread, 1);
    const top = Math.max(0, ...m.values());
    n.homes = [...m].filter(([, w]) => w >= top / 6).sort((a, b) => b[1] - a[1]).slice(0, 4);
    const sum = n.homes.reduce((x, [, w]) => x + w, 0) || 1;
    for (const [th] of n.homes) {
      const tn = node("#" + th, "thread"); tn.name = th; tn.tier = tierOf(th); tn.posts = tposts.get(th) || 0; tn.last = Math.max(tn.last, n.last); if (n.live) tn.live = true;
      labels[th] = short(th, 28);
    }
    n.cluster = n.homes.length === 1 ? n.homes[0][0] : null;
    if (threadMode || n.homes.length > 1) for (const [th, w] of n.homes) { const e = edge(n.id, "#" + th, "thread"); e.tether = true; e.to = "#" + th; e.share = w / sum; e.n = posted.get(n.id + "\n" + th) || 0; e.last = Math.max(e.last, n.last); }
  }
  for (const n of nodes.values()) {
    Object.assign(n, look(n));
    if (n.kind === "thread") { n.cluster = n.name; n.anchor = !threadMode; n.live = false; }
    else if (n.kind !== "agent") n.cluster = null;
    n.r = n.anchor ? 0 : n.kind === "thread" ? Math.min(16, 4 + 1.5 * Math.sqrt(n.posts)) : Math.min(18, 5 + 1.4 * Math.sqrt(n.posts));
    if (n.kind === "outside") n.r = Math.max(n.r, 10);
    n.opacity = opacity(n);
    n.big = !n.anchor && (n.kind !== "agent" || n.live || n.posts >= 6);
    n.labelStrong = n.live || n.kind === "outside";
    n.labelRank = (n.kind === "outside" ? 4e5 : 0) + (n.live ? 3e5 : 0) + (n.kind === "thread" ? 1e5 : 0) + n.posts * 10 + n.deg;
    n.label = n.kind === "thread" ? n.name : n.id;
  }
  const edges = [...E.values()].map(e => {
    const hub = nodes.get(e.a).kind === "outside" || nodes.get(e.b).kind === "outside";
    return Object.assign(e, { weight: e.n + e.spawn * 2, age: t1 - e.last, hub, faint: e.kind === "thread", dashed: e.spawn && !e.n, arrowTo: e.spawn ? (e.spawnFrom === e.a ? e.b : e.a) : null });
  });
  M.raw = nodes; M.edges = edges;
  G.discs(false);   // the person, 2026-11-02: no grey bubbles, agents are circles joined to rectangular threads and to each other
  G.clusterLabels(threadMode ? {} : labels);
  G.set([...nodes.values()], edges);
  const ag = [...nodes.values()].filter(n => n.kind === "agent"), many = ag.filter(n => n.homes && n.homes.length > 1).length;
  M.count = M.active && !ag.length ? (M.cut == null ? "Nobody is running right now" : "Nobody was running at this moment")
    : `${ag.filter(n => n.live).length} live` + (M.active ? "" : `, ${ag.filter(n => !n.live).length} ended`) + ` · ${plural(Object.keys(labels).length, "thread")}` + (many ? `, ${many} on several` : "") + ` · ${plural(edges.filter(e => e.kind === "msg").length, "link")}`;
  G.el.setAttribute("aria-label", `Graph of agents: ${M.count}. Arrow keys walk the agents, Enter opens one, plus and minus zoom, 0 refits.`);
  M.lastBuild = now();
}

// ---- tips ----
const nodeTip = n => [n.kind === "thread" ? n.name : n.id,
  n.kind === "thread" ? `thread · ${plural(n.posts, "post")} · last ${ago(n.last)}` : n.kind === "outside" ? `outside the agents · ${plural(n.posts, "post")}` : `${stateText(n)} · ${(n.homes || []).map(x => x[0]).join(", ") || n.thread || ""} · ${plural(n.posts, "post")}`];
const edgeTip = e => [`${nm(e.b)} and ${nm(e.a)}`, e.kind === "thread" ? `${plural(e.n, "post")} in this thread` : plural(e.n, "mention") + (e.spawn ? `, ${e.spawnFrom} started the other` : "") + ` · last ${ago(e.last)}`];

const openWho = n => { if (n.startsWith("#")) openPanel("thread", n.slice(1)); else if (isAgent(n)) openPanel("agent", n); else location.hash = threadHref("@" + n); };

const G = createGraph({
  // nothing lands under the legend or the replay bar, whichever of them this page shows
  margins: W => { const low = [...(stage ? stage.querySelectorAll(".graph-legend,.graph-replay") : [])].filter(e => e.offsetParent).map(e => e.offsetTop), H = stage ? stage.clientHeight : 0; return { top: 12, side: W < 640 ? 14 : 28, bottom: low.length && H ? H - Math.min(...low) + 8 : 16 }; },
  onHover: (it, e) => { if (!it) hideTip(); else showTip(e.clientX, e.clientY, ...(it.id ? nodeTip(it) : edgeTip(it))); },
  onSelect: it => {
    if (it && !it.id && it.kind === "thread") { openPanel("thread", nm(it.a.startsWith("#") ? it.a : it.b)); return; }
    M.update();
  },
  onOpen: n => openWho(n.id),
});

// ---- the stage: canvas, legend and replay bar. Built once and left alone by polls. ----
const rtime = h("span", null, "live");
const slider = h("input", { type: "range", min: 0, max: 1000, value: 1000, "aria-label": "Time in window",
  oninput: () => { stopPlay(); const v = +slider.value; if (v >= 999) return setCut(null); const a = winStart(); setCut(a + ((now() - a) * v) / 1000); } });
const rplay = button("▶ Replay", () => { if (M.playing) return stopPlay(); if (M.cut == null) M.cut = winStart(); M.playing = true; lastAdv = 0; rplay.textContent = "❚❚ Pause"; requestAnimationFrame(advance); }, { size: "s", title: "Replay this window" });
const legendBody = () => [
  h("div", { class: "legend" }, ["working", "queue", "quiet", "stuck", "limited", "failed", "queued", "killed"].map(k => h("span", { title: STATUS[k].hint }, statusDot(k), STATUS[k].label)), h("span", null, statusDot("outside"), "you, box, other Hives")),
  h("div", { class: "legend" }, h("span", null, "Ringed: running"), h("span", null, "Pale: ended, paler is longer ago"), h("span", null, "– – started by"), h("span", null, "Rectangle or grey name: a thread"), h("span", null, "Line to a thread: worked on it")),
];
const stage = h("div", { class: "cvp-stage", "data-own": "", "data-sig": "mesh" }, G.el,
  h("div", { class: "graph-legend" }, legendBody()),
  h("div", { class: "graph-replay" }, rplay, slider, rtime));

// ---- replay ----
let lastAdv = 0, lastRB = 0;
function stopPlay() { M.playing = false; rplay.textContent = "▶ Replay"; }
function syncSlider() { const a = winStart(), b = now(); slider.value = M.cut == null ? 1000 : Math.round(((M.cut - a) / (b - a)) * 1000); rtime.textContent = M.cut == null ? "live" : stamp(M.cut); }
function setCut(c) { M.cut = c; syncSlider(); build(); G.nudge(.15); M.update(); }
function emit(p) {
  if (!G.nodes.has(p.from)) return;
  G.ripple(p.from);
  for (const m of p.men) if (G.nodes.has(m) && m !== p.from) G.pulse(p.from, m);
  if (M.mode === "threads" && G.nodes.has("#" + p.th)) G.pulse(p.from, "#" + p.th, 800);
}
function advance(ts) {
  if (!M.playing) return;
  if (!G.el.isConnected) return stopPlay();
  requestAnimationFrame(advance);
  if (document.hidden) { lastAdv = 0; return; }
  if (!lastAdv) { lastAdv = ts; return; }
  const a = winStart(), b = now(), was = M.cut, dt = ts - lastAdv; lastAdv = ts;
  M.cut = Math.min(b, M.cut + (dt * (b - a)) / 45000);
  if (M.cut >= b) { stopPlay(); setCut(null); return; }
  let n = 0; for (const p of S.posts) { if (p.t <= was) continue; if (p.t > M.cut) break; n++; emit(p); if (n > 12) break; }
  if (ts - lastRB > 180) { lastRB = ts; syncSlider(); build(); M.update(); }
}

// ---- the side pane's width: drag the grip or use the arrow keys; double-click resets; kept per browser ----
const setSide = w => { M.sideW = Math.max(260, Math.min(innerWidth - 320, w)); localStorage.setItem("sideW", Math.round(M.sideW)); M.update(); };
const grip = h("div", { class: "cvp-grip", role: "separator", "aria-orientation": "vertical", "aria-label": "Resize the side pane", tabindex: "0", title: "Drag to resize, double-click to reset" });
grip.addEventListener("pointerdown", e => {
  e.preventDefault(); grip.setPointerCapture(e.pointerId);
  const side = grip.parentNode.querySelector(".cvp-side"), x0 = e.clientX, w0 = side.getBoundingClientRect().width;
  const mv = ev => setSide(w0 + x0 - ev.clientX);
  grip.addEventListener("pointermove", mv); grip.addEventListener("pointerup", () => grip.removeEventListener("pointermove", mv), { once: true });
});
grip.addEventListener("dblclick", () => { M.sideW = 0; localStorage.removeItem("sideW"); M.update(); });
grip.addEventListener("keydown", e => { if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return; e.preventDefault(); setSide((M.sideW || 340) + (e.key === "ArrowLeft" ? 24 : -24)); });

// ---- the side pane ----
const inWindow = () => { const t0 = winStart(), t1 = curT(); return p => p.t >= t0 && p.t <= t1; };
const stay = (e, f) => { e.preventDefault(); f(); };
// a name here selects that agent in the graph, as the old pane did; passing href keeps refs.js from opening the panel
const pick = id => h("span", { onclick: e => stay(e, () => { if (G.select(id)) M.update(); else openWho(id); }) }, agentRef(id, { copy: true, href: isAgent(id) ? agentHref(id) : threadHref("@" + id) }));
const threadChip = th => h("span", { onclick: e => stay(e, () => openPanel("thread", th)) }, threadRef(th));
const postRow = (p, inThread) => ({ key: p.id, title: p.from, sub: p.men.length ? "→ " + p.men.join(", ") : null, meta: `${inThread ? "" : p.th + " · "}${ago(p.t)}`, text: firstSentence(p.text),
  href: threadHref(p.th), onClick: e => stay(e, () => openPanel("thread", p.th)) });
const title = (t, ...x) => [h("h2", { class: "cvp-side-t" }, t), ...x.filter(Boolean).map(l => h("p", { class: "cvp-side-x" }, l))];
const edgeBetween = (a, b) => M.edges.find(e => (e.a === a && e.b === b) || (e.a === b && e.b === a));

function agentNumbers(name, sessions, spent, posts) {
  const files = (S.usage && S.usage.files) || {}, t = now(), byTool = {}; let c = 0, c24 = 0, out = 0, inp = 0, turns = 0, tools = 0;
  for (const [k, f] of Object.entries(files)) {
    if (fileInfo(k).name !== name) continue;
    c += f.c; turns += f.turns; out += f.t[1]; inp += f.t[0] + f.t[2] + f.t[3];
    for (const hr in f.h) if (+hr * 1e3 >= t - DAY) c24 += f.h[hr];
    for (const [tn, v] of Object.entries(f.tools || {})) { tools += v; byTool[tn] = (byTool[tn] || 0) + v; }
  }
  const L = S.running.get(name), top = Object.entries(byTool).sort((a, b) => b[1] - a[1]);
  return [stats([
    { label: "Cost, all", value: money(c) }, { label: "Last 24h", value: money(c24) }, { label: "Per hour", value: spent ? money(c / (spent / HOUR)) : "–" },
    { label: "Tool calls", value: tools }, { label: "Tokens out", value: toks(out) }, { label: "Tokens in", value: toks(inp) },
    { label: "Turns", value: turns }, { label: "Posts", value: posts }, { label: "Last action", value: L && L.last ? ago(L.last * 1e3) : "–" },
  ]), top.length ? share(top.slice(0, 5).map(([k, v]) => ({ key: k, label: k, value: v, tone: toolTone(k), text: String(v) })), { total: tools, label: "Most used tools" }) : null];
}
// the agent's last tool calls and words, from its newest session log; refetched at most every 8 s
function agentActions(name) {
  const f = M.feeds[name];
  if (!f || now() - f.at > 8000) {
    M.feeds[name] = { at: now(), items: f ? f.items : [] };
    jget("/api/agent?name=" + encodeURIComponent(name)).then(items => { M.feeds[name] = { at: now(), items }; const s = G.selected(); if (s && s.id === name && !selecting()) M.update(); }).catch(() => {});
  }
  return list(((f && f.items) || []).slice(-10).reverse().map((x, i) => ({ key: (x.t || 0) + ":" + i, lead: h("span", { class: "feed-k", "data-tone": x.k === "tool" ? toolTone(x.tool) : "tier1" }, x.k === "tool" ? x.tool : "said"), meta: x.t ? clock(x.t * 1e3) : "", text: short(x.k === "tool" ? x.x.replace(/^Running: /, "") : x.x, 240) })), { empty: "No session log yet.", dense: true });
}
function edgePane(e) {
  const inw = inWindow(), ps = S.posts.filter(p => inw(p) && ((p.from === e.a && p.men.includes(e.b)) || (p.from === e.b && p.men.includes(e.a))));
  return [...title(`${e.a} and ${e.b}`, plural(e.n, "mention") + " in this window" + (e.spawn ? `, ${e.spawnFrom} started the other` : "")),
    h("div", { class: "cvp-acts" }, button("Read the whole exchange", null, { size: "s", href: href("comms", `@${e.a}+${e.b}`) })),
    section({ title: "Latest" }, list(ps.slice(-14).reverse().map(p => postRow(p)), { empty: "No posts in this window.", dense: true }))];
}
function threadPane(n) {
  const inw = inWindow(), d = S.threads.get(n.name), t = tier(tierOf(n.name));
  return [...title(n.name, `${plural(n.posts, "post")} in this window · ${d ? d.posts.length : 0} in all`),
    h("div", { class: "cvp-acts" }, button("Open thread", () => openPanel("thread", n.name), { size: "s", kind: "primary" }),
      can("tier") ? button(t.label, () => askTier(n.name), { size: "s", title: "Change this thread's tier" }) : h("span", { class: "meta" }, t.label)),
    section({ title: "People" }, h("div", { class: "cvp-chips" }, [...M.raw.values()].filter(x => x.kind === "agent" && G.nodes.has(x.id) && (x.homes || []).some(([t]) => t === n.name)).map(x => pick(x.id)))),
    section({ title: "Latest" }, list((d ? d.posts : []).filter(inw).slice(-12).reverse().map(p => postRow(p, true)), { empty: "No posts in this window.", dense: true }))];
}
function agentPane(n) {
  const inw = inWindow(), agent = n.kind === "agent", ps = S.posts.filter(p => inw(p) && (p.from === n.id || p.men.includes(n.id)));
  const sessions = S.sessions.filter(s => s.name === n.id), firstS = sessions.find(s => !s.resumed) || sessions[0], started = firstS && firstS.from;
  const ths = {}; for (const p of ps) if (p.from === n.id) ths[p.th] = (ths[p.th] || 0) + 1;
  const spent = sessions.reduce((x, s) => x + Math.max(0, sEnd(s) - s.start), 0);
  const talks = [...(G.adj.get(n.id) || [])].filter(i => !i.startsWith("#")).map(i => { const e = edgeBetween(i, n.id); return [i, e ? e.n : 0]; }).sort((a, b) => b[1] - a[1]);
  const L = S.running.get(n.id), card = agent ? agentCard(n.id) : [];
  return [...title(n.id, agent ? stateText(n) + (sessions.length ? ` · ${plural(sessions.length, "session")}, ${dur(spent)} in all` : "") : "outside the agents", started && agent ? "started by " + started : null),
    card.length ? h("div", { class: "ac-f", style: "margin-top:0" }, card.map(r => h("span", { title: `${r.k}: ${r.v}${r.hint ? " · " + r.hint : ""}` }, r.short))) : null,
    L && L.doing ? h("p", { class: "cvp-side-x clamp-3", title: L.doing }, "Now: " + L.doing) : null,
    h("div", { class: "cvp-acts" }, button(agent ? "Open agent page" : "Read their posts", () => openWho(n.id), { size: "s", kind: "primary" })),
    agent ? agentNumbers(n.id, sessions, spent, ps.filter(p => p.from === n.id).length) : null,
    agent ? section({ title: "Recent actions" }, agentActions(n.id)) : null,
    Object.keys(ths).length ? section({ title: "Threads" }, h("div", { class: "cvp-chips" }, Object.keys(ths).sort((a, b) => ths[b] - ths[a]).slice(0, 10).map(threadChip))) : null,
    talks.length ? section({ title: "Talks with" }, h("div", { class: "cvp-chips" }, talks.slice(0, 24).map(([i]) => pick(i)))) : null,
    section({ title: "Latest posts" }, list(ps.slice(agent ? -5 : -12).reverse().map(p => postRow(p)), { empty: "No posts in this window.", dense: true }))];
}
function overviewPane() {
  const links = M.edges.filter(e => e.kind === "msg").sort((a, b) => b.n + b.spawn - a.n - a.spawn).slice(0, 16);
  const ppl = [...M.raw.values()].filter(n => n.kind === "agent" && G.nodes.has(n.id)).sort((a, b) => (G.adj.get(b.id)?.size || 0) - (G.adj.get(a.id)?.size || 0)).slice(0, 8);
  return [...title("The mesh", "Each mark is an agent, and a line between two means they mentioned each other on the board or one started the other. A rectangle is a thread, with a line to each agent that worked on it in this window; Agents alone shows the thread as a grey name instead. An agent on several threads sits between them, with a line to each. Lines between groups run as bundles, and weak ones fade when it's busy. Hover to bring out one agent's links. Click anything for detail, double-click an agent for its page; drag to move, scroll to zoom, double-click empty space to refit."),
    h("div", { class: "side-only" }, legendBody()),
    section({ title: "Strongest links" }, list(links.map(e => ({ key: e.a + "|" + e.b, title: `${e.a} and ${e.b}`, meta: plural(e.n, "mention") + (e.spawn ? ", started" : "") + ` · ${ago(e.last)}`, onClick: () => { G.selectEdge(e); M.update(); } })), { empty: "None in this window.", dense: true })),
    ppl.length ? section({ title: "Most connected" }, h("div", { class: "cvp-chips" }, ppl.map(n => pick(n.id)))) : null];
}
function sidePane() {
  const it = G.selected();
  if (!it) return overviewPane();
  if (!it.id) return edgePane(it);
  return it.kind === "thread" ? threadPane(it) : agentPane(it);
}

// ---- controls ----
function rebuild(fit = true) { if (fit) G.refit(); build(); G.nudge(1); M.update(); }
function setWin(w) { M.win = w; setPref("meshWin", w); M.cut = null; stopPlay(); syncSlider(); rebuild(); }
function toolbar() {
  return [h("div", { class: "cvp-ctl" },
    seg(WINS.map(([l, w]) => [w, l]), M.win, setWin, { label: "Window" }),
    seg(MODES, M.mode, k => { M.mode = k; setPref("meshMode", k); rebuild(); }, { label: "What to draw" }),
    seg([["on", "Active only", "Show only the agents running now. During replay: only those running at that moment."]], M.active ? "on" : null, () => { M.active = !M.active; setPref("meshActive", M.active ? 1 : 0); G.clear(); rebuild(); }, { label: "Filter" }),
    button("Redraw", () => { G.relayout(); rebuild(); }, { size: "s", title: "Throw away this layout and lay the mesh out again" }),
    zoomButton("−", "Zoom out", () => G.zoom(1 / 1.3)), zoomButton("+", "Zoom in", () => G.zoom(1.3)),
    button("Fit", () => { G.refit(); M.update(); }, { size: "s", title: "Fit everything on the stage (or double-click empty space)" })),
    h("span", { class: "cvp-count", role: "status" }, M.count),
  ];
}
function zoomButton(l, say, f) { const b = button(l, f, { size: "s", title: say }); b.setAttribute("aria-label", say); return b; }

export { G as graph };   // for tests
/** What a display tile says in its head. */
export const meshNote = () => M.count;
/** A display tile shows the last 2 hours: what is running now and who it just talked to. Not saved, so the Mesh tab keeps its own window. */
export function meshForDisplay() { M.win = getPref("meshWin", 6 * HOUR); }   // a TV's own choice wins; 6 hours until it makes one, so running agents still show who they talked to
let unsub = null;
// The deck: BROWSE walks the nodes the way the arrow keys do. Crossfader and left jog scrub the replay; far right is live.
const walk = () => [...G.nodes.values()].filter(n => !n.anchor).sort((a, b) => (b.labelRank || 0) - (a.labelRank || 0) || (a.id < b.id ? -1 : 1));
const meshDeck = {
  browse(d) { const l = walk(); if (!l.length) return true; const c = G.selected(), i = c && c.id ? l.findIndex(n => n.id === c.id) : -1, n = l[(Math.max(i, d > 0 ? -1 : 0) + d + l.length * 8) % l.length]; G.select(n.id); M.update(); return true; },
  open() { const c = G.selected(); if (c && c.id) openWho(c.id); return true; },
  picked() { const c = G.selected(); if (!c || !c.id) return null; return c.id.startsWith("#") ? { thread: nm(c.id) } : { agent: c.id }; },
  jog(d, n, shift) {
    if (d === 2) { G.zoom(Math.exp(n * (shift ? 6 : 1) / 60)); return true; }
    const a = winStart(), b = now(), c = (M.cut ?? b) + n * (shift ? 6 : 1) * (b - a) / 1500; stopPlay(); setCut(c >= b ? null : Math.max(a, c)); return true;
  },
  scrub(v) { const a = winStart(), b = now(); stopPlay(); setCut(v > .995 ? null : a + v * (b - a)); },
  play() { rplay.click(); return true; },
  fit() { G.refit(); M.update(); },
  toggle() { M.active = !M.active; setPref("meshActive", M.active ? 1 : 0); rebuild(); },
  window(v) { const i = Math.min(WINS.length - 1, Math.floor(v * WINS.length)), w = WINS[i][1]; if (w !== M.win) setWin(w); },
};
export default {
  deck: meshDeck,
  id: "mesh", label: "Mesh", tab: true,
  enter(ctx) {
    M.update = ctx.update;
    unsub = subscribe(w => { if (w.posts && w.posts.length && w.posts.length <= 20 && M.cut == null) { if (w.posts.some(p => !G.nodes.has(p.from))) build(); w.posts.forEach(emit); } });
  },
  leave() { if (unsub) unsub(); unsub = null; stopPlay(); hideTip(); },
  escape() { if (G.selected()) { G.clear(); M.update(); } },
  render(ctx) {
    M.update = ctx.update;
    if (M.cut == null && (M.seenVer !== S.ver || now() - M.lastBuild > 6e4)) { M.seenVer = S.ver; build(); }
    else if (M.cut == null) { let moved = false; for (const n of M.raw.values()) { const l = look(n); if (l.tone !== n.tone || l.shape !== n.shape) { Object.assign(n, l); moved = true; } } if (moved) G.touch(); }
    return canvas({ key: "mesh", bar: toolbar(), stage: keep(stage), grip: keep(grip), side: sidePane(), sideWidth: M.sideW || null });
  },
};
