// "Its corner of the hive": a small mesh of who an agent talks with, whom it started, its threads, who else is on them,
// and the links among all of those. It draws on the Mesh's engine (ui/graph.js), so it behaves the same: live forces, drag a
// node and the rest react, pan, zoom, hover brings out one node's links, and pulses when a post lands. A poll moves nothing
// unless the data changed. The page scrolls around it: plain wheel scrolls, ctrl+wheel or pinch zooms.
import { h } from "../core/dom.js";
import { S, OUTSIDE, SYS, subscribe } from "../core/store.js";
import { agentStatus, isAgent, isRunning, sEnd, tierOf } from "../core/model.js";
import { agentHref, threadHref } from "../core/router.js";
import { ago, now, plural, short } from "../core/format.js";
import { status, tier } from "../core/status.js";
import { createGraph } from "./graph.js";
import { button } from "./kit.js";
import { hideTip, showTip } from "./tip.js";
import { openPanel } from "./panel.js";

const isTh = x => x[0] === "#", tid = x => x.slice(1);
const known = x => S.agents.has(x) || OUTSIDE.has(x);   // only names that have actually run

/** Which nodes and links, from the board and the sessions. Scans everything: memoised by the caller's data version. */
function shape(n, links, started, threads, parent) {
  const ths = threads.slice(0, 5), thSet = new Set(ths), ids = new Set([n]);
  const add = x => { if (known(x) && ids.size < 18) ids.add(x); };
  if (parent) add(parent);
  links.slice(0, 12).forEach(l => add(l[0]));
  started.slice(-6).forEach(add);
  // who else is on its threads: running first, then most recent
  const onTh = new Map(ths.map(t => [t, new Map()]));
  const seen = (t, id, when) => { const m = onTh.get(t); if (m && known(id) && id !== n) m.set(id, Math.max(m.get(id) || 0, when)); };
  for (const p of S.posts) if (thSet.has(p.th) && !SYS.has(p.from)) seen(p.th, p.from, p.t);
  for (const x of S.sessions) if (thSet.has(x.thread)) seen(x.thread, x.name, sEnd(x));
  for (const m of onTh.values()) [...m].sort((x, y) => isRunning(y[0]) - isRunning(x[0]) || y[1] - x[1]).slice(0, 3).forEach(e => add(e[0]));
  const E = new Map();
  const edge = (a, b, kind) => { const k = a < b ? a + "|" + b : b + "|" + a; let e = E.get(k); if (!e) E.set(k, e = { a, b, kind, n: 0, spawn: false, last: 0 }); return e; };
  for (const p of S.posts) {
    if (!ids.has(p.from)) continue;
    for (const m of p.men) if (m !== p.from && ids.has(m)) { const e = edge(p.from, m, "msg"); e.n++; e.last = Math.max(e.last, p.t); }
    if (thSet.has(p.th)) { const e = edge(p.from, "#" + p.th, "thread"); e.n++; e.last = Math.max(e.last, p.t); }
  }
  for (const x of S.sessions) {
    if (!ids.has(x.name)) continue;
    if (thSet.has(x.thread)) { const e = edge(x.name, "#" + x.thread, "thread"); e.last = Math.max(e.last, sEnd(x)); }
    if (!x.resumed && x.from && x.from !== x.name && ids.has(x.from)) { const e = edge(x.from, x.name, "msg"); e.spawn = true; e.spawnFrom = x.from; e.spawnTo = x.name; e.last = Math.max(e.last, x.start); }
  }
  return { edges: [...E.values()], nodeIds: [...ids, ...ths.map(t => "#" + t)], ths };
}

// ---- one graph per place it's drawn (the page, the panel), for the life of the tab: each is reused, never rebuilt ----
const hosts = {};
function hostFor(host) {
  if (hosts[host]) return hosts[host];
  const H = { name: null, sig: "", opened: 0 };
  const hrefOf = id => (isTh(id) ? threadHref(tid(id)) : isAgent(id) ? agentHref(id) : threadHref("@" + id));
  const open = id => { hideTip(); H.opened = now(); if (isTh(id)) openPanel("thread", tid(id)); else openPanel("agent", id); };
  const say = h("div", { class: "vh", role: "status" });
  const G = createGraph({
    inPage: true,
    margins: W => ({ top: 34, bottom: 26, side: W < 480 ? 34 : 52 }),
    onHover: (it, e) => { if (!it) hideTip(); else showTip(e.clientX, e.clientY, ...(it.id ? it.tip : edgeTip(it))); },
    onSelect: (it, e) => {
      if (e && e.type === "keydown") {   // arrows walk: say where they are, Enter opens
        const r = G.el.getBoundingClientRect(), v = G.view;
        if (it) { showTip(r.left + it.p.x * v.k + v.x, r.top + it.p.y * v.k + v.y, ...it.tip); say.textContent = it.tip.join(", "); } else hideTip();
        return;
      }
      G.clear();                         // a click opens; the agent in the middle stays the one marked
      if (!it) return;
      const to = it.id ? (it.id === H.name ? null : it.id) : it.kind === "thread" ? (isTh(it.a) ? it.a : it.b) : null;
      const href = to ? hrefOf(to) : !it.id && it.kind === "msg" ? threadHref(`@${it.a}+${it.b}`) : null;   // a link between two: read their exchange
      if (!href) return;
      if (e && (e.ctrlKey || e.metaKey)) { window.open(href, "_blank"); return; }
      if (to) open(to); else { hideTip(); location.hash = href; }
    },
    onOpen: n => { if (n.id !== H.name && now() - H.opened > 600) open(n.id); },   // a double click's second half opens nothing new
  });
  G.el.addEventListener("wheel", e => { if (!e.ctrlKey) hideTip(); }, { passive: true });
  const zoom = (l, title, f) => { const b = button(l, f, { size: "s", title }); b.setAttribute("aria-label", title); return b; };
  const bar = h("div", { class: "ego-bar" }, zoom("−", "Zoom out", () => G.zoom(1 / 1.3)), zoom("+", "Zoom in", () => G.zoom(1.3)), button("Fit", () => G.refit(), { size: "s", title: "Fit everything on the stage (or double-click empty space)" }));
  // a post landing between nodes: the same pulses as the Mesh
  subscribe(w => {
    if (!G.el.isConnected || !w.posts || !w.posts.length || w.posts.length > 20) return;
    for (const p of w.posts) {
      if (!G.nodes.has(p.from)) continue;
      G.ripple(p.from);
      for (const m of p.men) if (m !== p.from && G.nodes.has(m)) G.pulse(p.from, m);
      if (G.nodes.has("#" + p.th)) G.pulse(p.from, "#" + p.th, 800);
    }
  });
  return (hosts[host] = Object.assign(H, { G, parts: [G.el, bar, say] }));
}

const edgeTip = e => [`${e.a[0] === "#" ? tid(e.a) : e.a} and ${e.b[0] === "#" ? tid(e.b) : e.b}`,
  e.kind === "thread" ? `${plural(e.n, "post")} in this thread` : plural(e.n, "mention") + (e.spawn ? `, ${e.spawnFrom} started the other` : "") + (e.last ? ` · last ${ago(e.last)}` : "")];
const statusOf = x => (isTh(x) ? tierOf(tid(x)) : OUTSIDE.has(x) ? "out" : agentStatus(x).key + (isRunning(x) ? "+" : ""));

/** ego({name, links:[[who, n]], started:[names], threads:[names], parent, version, host}). Null when there's nothing to draw.
 *  Returns an empty data-own stage for morph to leave alone; the long-lived canvas is put in it just after the render. */
let memo = { key: null };
export function ego({ name: n, links, started, threads, parent, version, host = "page" }) {
  const key = n + "|" + version;
  if (memo.key !== key) memo = { key, ...shape(n, links, started, threads, parent) };
  const { edges, nodeIds, ths } = memo, N = nodeIds.length;
  if (N < 2 && !ths.length) return null;
  const H = hostFor(host), G = H.G;
  const sig = [n, nodeIds.join(), edges.map(e => `${e.a}|${e.b}|${e.n}|${e.spawn ? 1 : 0}`).join(), nodeIds.map(statusOf).join()].join("\n");
  if (sig !== H.sig) {
    const fresh = H.name !== n;
    if (fresh) G.relayout();
    H.sig = sig; H.name = n; G.pin(n);
    const direct = new Map(links), mx = Math.max(1, ...links.map(l => l[1])), t = now();
    const nodes = nodeIds.map(id => {
      const th = isTh(id), out = OUTSIDE.has(id), st = agentStatus(id), S_ = th || out ? null : status(st.key), c = direct.get(id) || 0;
      return { id, kind: th ? "thread" : out ? "outside" : "agent", tone: th ? tier(tierOf(tid(id))).tone : out ? "ink" : S_.tone, shape: out ? "diamond" : th ? null : S_.shape,
        live: !th && !out && isRunning(id), label: th ? short(tid(id), 16) : id, big: true, labelStrong: id === n, ring: id === n, labelRank: id === n ? 1000 : th ? 5 : 10 + c,
        r: id === n ? 15 : th ? 9 : out ? 10 : c ? 6 + 6 * Math.sqrt(c / mx) : 6,
        tip: [th ? tid(id) : id, th ? "thread" : out ? "outside the agents" : st.label + (c ? `, ${plural(c, "exchange")} with ${n}` : "")] };
    });
    const list = edges.map(e => { const mine = e.a === n || e.b === n; return { a: e.a, b: e.b, n: e.n, kind: e.kind, spawn: e.spawn, spawnFrom: e.spawnFrom, last: e.last,
      weight: Math.max(1, e.n), age: e.last ? t - e.last : 0, faint: !mine || e.kind === "thread", dashed: e.spawn && !e.n, arrowTo: e.spawn ? e.spawnTo : null, hub: OUTSIDE.has(e.a) || OUTSIDE.has(e.b), floor: mine ? (e.kind === "msg" ? 0.5 : 0.28) : 0 }; });
    const added = G.set(nodes, list);
    if (fresh && added <= 8) G.nudge(1);   // a small graph settles in view; a big one was settled by set()
    G.el.setAttribute("aria-label", `Who ${n} works with: ${plural(N, "node")}. Arrow keys walk them, Enter opens one, plus and minus zoom, 0 refits.`);
  }
  queueMicrotask(() => {
    const stage = document.querySelector(`.ego-stage[data-host="${host}"]`);
    if (stage && G.el.parentNode !== stage) { stage.replaceChildren(...H.parts); G.wake(); }
  });
  const phone = innerWidth < 640, height = phone ? (N <= 4 ? 300 : N <= 10 ? 400 : 470) : N <= 4 ? 280 : N <= 10 ? 380 : 480;
  return h("div", { class: "ego-stage", "data-own": "", "data-sig": "ego", "data-host": host, style: `height:${height}px` });
}

export const egoGraph = (host = "page") => hosts[host] && hosts[host].G;   // for tests
