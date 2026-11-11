// Map: where the hive's agents are, as a graph with no time axis (Timeline is the view for "when"). Threads are cards in tier
// bands; each agent is drawn once, fanned out right of its main thread with a spoke whose width says how much it posted, and a
// faint "ghost" beside any other thread it posted in. The layout is fixed by the data (mesh-layout.js), never random. Hand-off
// lines appear only for the selection. The window picks who appears; replay shows the graph as it was at the cut.
import { h } from "../core/dom.js";
import { S, OUTSIDE, SYS, subscribe, getPref, setPref } from "../core/store.js";
import { agentLabel, agentStatus, isAgent, isRunning, isWarden, modelName, sEnd, threadTitle, tierOf, fileInfo, spendSince } from "../core/model.js";
import { ago, clock, dur, firstSentence, money, now, short, toks, plural, DAY, HOUR, MIN } from "../core/format.js";
import { agentHref, threadHref } from "../core/router.js";
import { status, tier } from "../core/status.js";
import { button, waiting, icon, pageNav, seg, statusDot, toolsButton, toolsMore } from "../ui/kit.js";
const navBox = () => { const n = pageNav(); return n && h("div", { class: "cvp-nav" }, n); };   // the page row (R7 step 6)
import { killButton } from "../actions.js";
import { openPanel } from "../ui/panel.js";
import { showTip, hideTip } from "../ui/tip.js";
import { TIERS, layout, spokePath, linkPath } from "./mesh-layout.js";
import { LAYOUTS, graphLayout } from "./mesh-layouts.js";

const WINS = [["1h", HOUR], ["6h", 6 * HOUR], ["24h", DAY], ["3d", 3 * DAY], ["All", 0]];
const WIN_NAME = { [HOUR]: "hour", [6 * HOUR]: "6 hours", [DAY]: "24 hours", [3 * DAY]: "3 days" };
const TIER_ORDER = { tier1: 0, tier2: 1, held: 2, other: 3 };
const PLAY_MS = 12000;
const M = {
  win: getPref("meshWin", 6 * HOUR), lay: LAYOUTS.some(l => l[0] === getPref("meshLayout")) ? getPref("meshLayout") : "lineage", active: !!getPref("meshActive", 1), cut: null, playing: false, q: "",
  sel: null, lanes: [], more: [], D: null, count: "", seenVer: -1, lastBuild: 0, update: () => {},
  showMore: false, allHand: false, timer: 0, root: null,
};
const firstT = () => (S.posts[0] ? S.posts[0].t : now() - DAY);
const winStart = () => (M.win ? now() - M.win : firstT());
const curT = () => M.cut ?? now();
const span = () => { const t1 = curT(); return [M.win ? t1 - M.win : Math.min(firstT(), t1 - MIN), t1]; };
const winName = () => (M.win ? "Last " + WIN_NAME[M.win] : "All time");

// ---- data ----
/** Session logs have no figure for a run still going: a dash, not a made-up $0.00. */
const spent = v => v > 0 ? money(v) : "–";
function fileModel(s) { const f = s.out && S.usage && S.usage.files[s.out], id = (f && f.init && f.init.model) || s.model; if (!id) return ""; const m = modelName(id.includes(":") ? id.split(":").pop() : id); return String(m).split(/[\s-]/)[0].replace(/^./, c => c.toUpperCase()); }
function costs(t0, t1) {
  // a session's cost in [t0, t1], from the usage files' hourly buckets; it counts toward its own thread only
  const byTh = new Map(), byAg = new Map(), files = (S.usage && S.usage.files) || {}; let total = 0;
  for (const [k, f] of Object.entries(files)) {
    let c = 0; for (const hr in f.h) { const t = +hr * 1e3; if (t + HOUR > t0 && t <= t1) c += f.h[hr]; }
    if (!c) continue;
    const fi = fileInfo(k); total += c;
    if (fi.thread) byTh.set(fi.thread, (byTh.get(fi.thread) || 0) + c);
    byAg.set(fi.name, (byAg.get(fi.name) || 0) + c);
  }
  return { byTh, byAg, total };
}

function build() {
  const [t0, t1] = span(), replay = M.cut != null;
  const homes = new Map(), runs = new Map(), aposts = new Map(), outPosts = new Map(), tposts = new Map(), spawns = [], pairs = new Map(), posts = [];
  const home = (a, th, w) => { if (!th) return; let m = homes.get(a); if (!m) homes.set(a, (m = new Map())); m.set(th, (m.get(th) || 0) + w); };
  const push = (m, k, v) => { let l = m.get(k); if (!l) m.set(k, (l = [])); l.push(v); };
  for (const p of S.posts) {
    if (p.t < t0) continue; if (p.t > t1) break; if (SYS.has(p.from)) continue;
    posts.push(p); push(tposts, p.th, p);
    if (OUTSIDE.has(p.from)) push(outPosts, p.th + "\n" + p.from, p);
    else if (isAgent(p.from)) {
      push(aposts, p.from + "\n" + p.th, p); home(p.from, p.th, 1);
      for (const m of p.men) if (m !== p.from && isAgent(m)) { const k = p.from + "\n" + m; pairs.set(k, (pairs.get(k) || 0) + 1); }
    }
  }
  for (const s of S.sessions) {
    if (s.start > t1 || !s.thread) continue; const en = sEnd(s); if (en < t0) continue;
    const live = en >= t1 && (replay || isRunning(s.name));
    push(runs, s.name + "\n" + s.thread, { s, start: Math.max(s.start, t0), end: live ? t1 : Math.min(en, t1), live, from: s.resumed ? null : s.from && s.from !== s.name ? s.from : null, model: fileModel(s) });
    home(s.name, s.thread, live ? 6 : 3);
    if (!s.resumed && s.start >= t0 && s.from && s.from !== s.name && !SYS.has(s.from)) spawns.push({ from: s.from, to: s.name, th: s.thread, t: s.start });
  }
  // an agent's main thread is its heaviest home; the full bar is drawn there only, other threads get an "also" track
  // (only a thread it has a run in can hold its drawn run: an agent that posted more elsewhere than it ran, like a fan-out parent, would otherwise be drawn nowhere)
  const main = new Map(); for (const [a, m] of homes) { const own = [...m].filter(([th]) => runs.has(a + "\n" + th)), cand = own.length ? own : [...m]; main.set(a, cand.sort((x, y) => y[1] - x[1] || (x[0] < y[0] ? -1 : 1))[0][0]); }
  const C = costs(t0, t1), L = new Map();
  const lane = th => { let l = L.get(th); if (!l) L.set(th, (l = { th, tier: tierOf(th), tracks: [], last: 0, live: 0, posts: (tposts.get(th) || []).length, cost: C.byTh.get(th) || 0 })); return l; };
  for (const [a, m] of homes) for (const [th] of m) {
    const l = lane(th), rs = (runs.get(a + "\n" + th) || []).sort((x, y) => x.start - y.start), ps = aposts.get(a + "\n" + th) || [];
    const last = Math.max(0, ...rs.map(r => r.end), ...ps.map(p => p.t)); l.last = Math.max(l.last, last);
    if (main.get(a) === th && rs.length) {
      const live = rs.some(r => r.live), st = replay ? (live ? "working" : "ended") : live ? agentStatus(a).key : rs[rs.length - 1].s === S.lastSess[a] ? agentStatus(a).key : "ended";
      if (live) l.live++;
      l.tracks.push({ kind: "run", a, runs: rs, posts: ps, live, st, end: Math.max(...rs.map(r => r.end)), start: rs.find(r => r.live)?.start ?? rs[0].start,
        from: rs.find(r => r.from)?.from || null, model: rs[rs.length - 1].model });
    } else if (ps.length || rs.length) l.tracks.push({ kind: "also", a, posts: ps, runs: rs, n: ps.length });
  }
  for (const [k, ps] of outPosts) { const [th, who] = k.split("\n"); const l = L.get(th); if (l) l.tracks.push({ kind: "person", a: who, posts: ps, n: ps.length }); }
  const RK = { run: 0, also: 1, person: 2 };
  for (const l of L.values()) l.tracks.sort((x, y) => RK[x.kind] - RK[y.kind] || (x.kind === "run" ? (y.live - x.live) || (x.live ? x.start - y.start : y.end - x.end) : y.n - x.n) || (x.a < y.a ? -1 : 1));
  let lanes = [...L.values()].sort((x, y) => TIER_ORDER[x.tier] - TIER_ORDER[y.tier] || (M.active ? y.live - x.live : 0) || y.last - x.last || (x.th < y.th ? -1 : 1));
  const withWork = lanes.length;
  if (M.active) lanes = lanes.filter(l => l.live).map(l => ({ ...l, tracks: l.tracks.filter(t => t.kind === "person" || (t.kind === "run" && t.live)).map(t => t.kind === "run" ? { ...t, runs: t.runs.filter(r => r.live) } : t) }));
  // threads with posts only (the person, the box, other Hives) fold into a "more" row
  const more = [...tposts.keys()].filter(th => !L.has(th)).map(th => { const ps = tposts.get(th); return { th, tier: tierOf(th), posts: ps.length, last: ps[ps.length - 1].t, cost: C.byTh.get(th) || 0, tracks: [...outPosts].filter(([k]) => k.startsWith(th + "\n")).map(([k, p]) => ({ kind: "person", a: k.split("\n")[1], posts: p, n: p.length })) }; })
    .sort((x, y) => y.last - x.last);
  // related to an agent: itself, whoever started it, those it started, and those it mentioned or was mentioned by
  const rel = new Map(); const relate = (a, b) => { if (!rel.has(a)) rel.set(a, new Set([a])); rel.get(a).add(b); };
  for (const s of spawns) { relate(s.from, s.to); relate(s.to, s.from); }
  for (const k of pairs.keys()) { const [a, b] = k.split("\n"); relate(a, b); relate(b, a); }
  const allRuns = lanes.flatMap(l => l.tracks.filter(t => t.kind === "run"));
  M.D = { t0, t1, replay, homes, main, runs, aposts, spawns: spawns.sort((x, y) => y.t - x.t), pairs, rel, posts, C, withWork, allRuns, byId: new Map(posts.map(p => [p.id, p])) };
  M.lanes = lanes; M.more = more;
  const live = replay ? allRuns.filter(t => t.live).length : S.running.size;   // the top bar's count, not the drawn runs
  M.count = M.active && !live ? (replay ? "Nobody was running then" : "Nobody is running right now") : `${live} running · ${plural(lanes.length, S.app.app ? "conversation" : "thread")}${C.total > 0 ? " · " + money(C.total) : ""}`;
  M.lastBuild = now();
}


// ---- selection ----
const selected = () => M.sel;
function select(kind, id) {
  if (!kind) M.sel = null; else M.sel = M.sel && M.sel.kind === kind && M.sel.id === id ? null : { kind, id };
  hideTip(); M.update();
  if (M.sel && matchMedia("(max-width: 760px)").matches) requestAnimationFrame(() => M.root && M.root.querySelector(".map-side")?.scrollIntoView({ block: "start", behavior: "smooth" }));
}
const relSet = a => (M.D && M.D.rel.get(a)) || new Set([a]);
const stop = (e, f) => { e.preventDefault(); e.stopPropagation(); f(); };
const pickAgent = a => (e => stop(e, () => (e.detail >= 2 && isAgent(a) ? openPanel("agent", a) : select("agent", a))));
const pickThread = th => (e => stop(e, () => select("thread", th)));
const idName = a => (a === "person" ? "You" : agentLabel(a));   // §5.3: a hand-off from the person says "You"
const idLink = a => h("a", { class: "map-id", href: isAgent(a) ? agentHref(a) : threadHref("@" + a), onclick: pickAgent(a), title: a }, idName(a));
const thLink = th => h("a", { class: "map-th", href: threadHref(th), title: threadTitle(th) !== th ? th : null, onclick: pickThread(th) }, threadTitle(th));

// ---- the graph: Claude Design's hub-and-fan layout (~/designs/mesh-graph/GRAPH-HANDOFF.md) ----
// Threads are cards in tier bands. Each agent is drawn once, fanned out right of its main thread with a spoke to the card; a thread
// it also posted in gets a faint "ghost" of it. Nothing moves at random: the same data gives the same picture. Lines between agents
// (hand-offs, agent to its ghosts) are drawn only for what is selected. SVG for the lines, HTML buttons for everything you click.
const keep = el => (el.isConnected ? el.cloneNode(false) : el);
const SVGNS = "http://www.w3.org/2000/svg";
const OUT_NAME = { person: "you", box: "box", hives: "hives" };
const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;
const since = t => (t ? dur(Math.max(0, now() - t)).replace("under a minute", "now") : "");
const GS = { L: null, nodes: new Map(), sig: "", w: 0, q: "", hits: [], posted: new Map(), busy: new Set(), queued: new Set(), capT: 0, hoverT: 0, h: 0, pin: new Map(), view: { k: 1, x: 0, y: 0, user: false } };

function graphInput(D) {
  const per = new Map(), total = new Map();
  for (const [k, ps] of D.aposts) { const [a, th] = k.split("\n"); let m = per.get(a); if (!m) per.set(a, (m = {})); m[th] = ps.length; total.set(a, (total.get(a) || 0) + ps.length); }
  const threads = [], agents = [];
  for (const l of M.lanes) {
    const outside = {}, tr = TIERS.includes(l.tier) ? l.tier : "other";
    for (const t of l.tracks) if (t.kind === "person") { const k = OUT_NAME[t.a] || t.a; outside[k] = (outside[k] || 0) + t.n; }
    threads.push({ id: l.th, tier: tr, posts: l.posts, cost: l.cost, last: l.last, outside, stuck: l.tracks.filter(t => t.kind === "run" && t.st === "stuck").length });
    for (const t of l.tracks) if (t.kind === "run") {
      const L = S.running.get(t.a), th = { ...(per.get(t.a) || {}) }; th[l.th] = th[l.th] || 0;
      agents.push({ id: t.a, status: status(t.st) === status("none") && t.st !== "none" ? "ended" : t.st, running: t.live ? 1 : 0, main: l.th, threads: th, posts: total.get(t.a) || 0,
        cost: D.C.byAg.get(t.a) || 0, model: t.model, now: !D.replay && t.live && L && L.doing ? L.doing : "", last: L && L.last ? L.last * 1e3 : 0, from: t.from, warden: isWarden(t.a), track: t });
    }
  }
  return { threads, agents, now: D.t1 };
}

// what is lit: the selection and what it touches, or the search hits
function litOf(L, D) {
  const s = M.sel, lit = new Set(), acc = new Set(), links = [];
  const node = id => GS.nodes.get(id);
  if (s && s.kind === "agent") {
    const mine = L.nodes.filter(n => n.agent && n.agent.id === s.id), real = L.byAgent.get(s.id);
    for (const n of mine) { lit.add("n:" + n.id); acc.add(n.id); lit.add("c:" + n.thread); if (real && n.ghost) links.push({ p: real, q: n, ghost: true }); }
    if (real && L.mode) for (const sp of L.spokes) if (sp.node === real.id) lit.add("c:" + sp.thread);   // graph layouts: the other threads it posted in
    const seen = new Set();
    for (const sp of D.spawns) {
      const o = sp.from === s.id ? sp.to : sp.to === s.id ? sp.from : null, on = o && L.byAgent.get(o);
      if (!on || !real || seen.has(sp.from + ">" + sp.to)) continue; seen.add(sp.from + ">" + sp.to);
      lit.add("n:" + on.id); lit.add("c:" + on.thread);
      links.push(sp.from === s.id ? { p: real, q: on } : { p: on, q: real });
    }
  } else if (s && s.kind === "thread") {
    const cl = L.byThread.get(s.id); lit.add("c:" + s.id);
    if (cl) for (const id of cl.linked || []) lit.add("n:" + id);
    if (cl) for (const id of cl.nodeIds) { const n = node(id); lit.add("n:" + id); acc.add(id); const r = n && n.ghost && L.byAgent.get(n.agent.id); if (r) { lit.add("n:" + r.id); lit.add("c:" + r.thread); links.push({ p: r, q: n, ghost: true }); } }
  } else if (s && s.kind === "outside") {
    const k = OUT_NAME[s.id] || s.id; for (const cl of L.clusters) if (cl.thread.outside[k]) lit.add("c:" + cl.thread.id);
  }
  const hits = [];
  if (!s && M.q) {
    const q = M.q;
    for (const cl of L.clusters) if (cl.thread.id.toLowerCase().includes(q)) { lit.add("c:" + cl.thread.id); hits.push({ kind: "thread", id: cl.thread.id, y: cl.card.y }); }
    for (const n of L.nodes) if (n.agent && !n.ghost && (n.agent.id.includes(q) || n.agent.now.toLowerCase().includes(q))) { lit.add("n:" + n.id); hits.push({ kind: "agent", id: n.agent.id, y: n.y }); }
    hits.sort((a, b) => a.y - b.y);
  }
  return { on: !!s || !!M.q, lit, acc, links, hits };
}

const capEl = h("div", { class: "map-cap", role: "status" });
const cv = h("div", { class: "map-cv" });
const svg = document.createElementNS(SVGNS, "svg"), fx = document.createElementNS(SVGNS, "svg");
svg.setAttribute("class", "map-svg"); svg.setAttribute("aria-hidden", "true"); fx.setAttribute("class", "map-fx"); fx.setAttribute("aria-hidden", "true");
const hcEl = h("div", { class: "map-hc", role: "tooltip", hidden: true });
cv.append(svg, fx, hcEl);
const scroller = h("div", { class: "map-scroll" }, cv);
const legendEl = h("div", { class: "map-legend" });
const zoomBy = f => { const V = GS.view; zoomAt(V.k * f, scroller.clientWidth / 2, scroller.clientHeight / 2, true); };
const zoomEl = h("div", { class: "map-zoom", role: "group", "aria-label": "Zoom" },
  h("button", { type: "button", class: "btn", "data-size": "s", title: "Zoom in (or Ctrl + wheel, or pinch)", "aria-label": "Zoom in", onclick: () => zoomBy(1.25) }, "+"),
  h("button", { type: "button", class: "btn", "data-size": "s", title: "Zoom out", "aria-label": "Zoom out", onclick: () => zoomBy(0.8) }, "−"),
  h("button", { type: "button", class: "btn", "data-size": "s", title: "Fit the graph to the panel and let dragged nodes go", onclick: () => refit() }, "Fit"));
const stage = h("div", { class: "map-stage", "data-key": "stage", "data-own": "", "data-sig": "mesh" }, capEl, scroller, zoomEl, legendEl);
const EL = new Map(), SPK = new Map();
const spG = document.createElementNS(SVGNS, "g"), topG = document.createElementNS(SVGNS, "g"), ringG = document.createElementNS(SVGNS, "g");
svg.append(ringG, spG, topG);
new ResizeObserver(() => { const w = scroller.clientWidth, hh = scroller.clientHeight; if (w && (Math.abs(w - GS.w) > 2 || (M.lay !== "bands" && Math.abs(hh - GS.h) > 2)) && M.D) { syncGraph(M.D); } }).observe(scroller);

// ---- the free view (Web, Orbit, Lineage): fitted to the panel, wheel zooms, drag the background to pan, drag a node to move it ----
const free = () => scroller.hasAttribute("data-free");
// Level of detail. Past TILE_K a mesh isn't drawn smaller: it's laid out again as tiles (opt.tile in mesh-layouts.js), small grey thread boxes and bare agent dots packed
// close together and scaled up to fill the panel. Zooming out past TILE_K goes there, zooming far enough in comes back.
const TILE_K = 0.4, FIT_TILE_K = 0.55;                       // a card under about half size can't be read, so a fit goes to tiles sooner than a hand zoom does
function applyView() { const V = GS.view; if (free()) cv.setAttribute("data-lod", GS.tile ? "min" : V.k < 0.62 ? "small" : "full"); cv.style.transform = `translate(${V.x.toFixed(1)}px,${V.y.toFixed(1)}px) scale(${V.k.toFixed(4)})`; }
function viewBox(L) {
  let x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
  const add = (a, b, c, d) => { x0 = Math.min(x0, a); y0 = Math.min(y0, b); x1 = Math.max(x1, c); y1 = Math.max(y1, d); };
  for (const cl of L.clusters) add(cl.card.x, cl.card.y, cl.card.x + cl.card.w, cl.card.y + cl.card.h);
  for (const n of L.nodes) L.tile ? add(n.x - n.r - 2, n.y - n.r - 2, n.x + n.r + 2, n.y + n.r + 2) : add(n.x - 12, n.y - 12, n.labelX + Math.max(40, n.labelW) + 8, n.y + 14);
  for (const r of L.rings || []) if (r.ox) add(r.cx - r.ox, r.cy - r.oy - 20, r.cx + r.ox, r.cy + r.oy);
  for (const c of (L.olderCount && L.older.chips) || []) add(c.x, c.y - 30, c.x + c.w, c.y + c.h);
  return { x0, y0, x1, y1 };
}
// a small graph grows to fill the panel; a big one shrinks to fit whole
// On a phone a graph is not shrunk below PHONE_MIN_K to fit the panel (R-1110d-a: whole, the 26-agent Lineage drew at 0.26, 3 px labels): it opens at the top left at a readable size and is panned, or zoomed out by hand.
const PHONE_MIN_K = 0.85;
const fitK = (L, b) => { const k = Math.max(0.1, Math.min(L.tile ? 1.4 : 1.25, (scroller.clientWidth - 64) / (b.x1 - b.x0), (scroller.clientHeight - 64) / (b.y1 - b.y0))); return scroller.clientWidth < 760 && !L.tile ? Math.max(k, PHONE_MIN_K) : k; };
function fitView(L) {
  const B = viewBox(L), { x0, y0, x1, y1 } = B;
  if (x0 > x1) { GS.view = { k: 1, x: 0, y: 0, user: false }; return; }
  const W = scroller.clientWidth, H = scroller.clientHeight, m = 32, k = fitK(L, B), bw = (x1 - x0) * k, bh = (y1 - y0) * k;
  if (L.tile) GS.tileFit = k;
  GS.view = { k, x: bw <= W - 2 * m ? (W - bw) / 2 - x0 * k : m - x0 * k, y: bh <= H - 2 * m ? (H - bh) / 2 - y0 * k : m - y0 * k, user: false };
}
// Between cards and tiles by hand. To tiles: the whole mesh, fitted. Back to cards: the thing under the pointer stays under it.
function setTile(on, px, py) {
  const V = GS.view, L = GS.L; let near = null, best = 1e18;
  if (!on && L) {
    const gx = (px - V.x) / V.k, gy = (py - V.y) / V.k, see = (id, x, y) => { const d = (x - gx) ** 2 + (y - gy) ** 2; if (d < best) { best = d; near = id; } };
    for (const n of L.nodes) see(n.id, n.x, n.y);
    for (const cl of L.clusters) see("c:" + cl.thread.id, cl.port.x, cl.port.y);
  }
  GS.tile = on; GS.pos = null; GS.pin = new Map(); GS.sig = ""; GS.view = { ...V, user: true };   // by hand, so a new post doesn't flip it back
  syncGraph(M.D);
  if (on) { fitView(GS.L); GS.view.user = true; applyView(); return; }
  const k = 0.7, n = GS.nodes.get(near), c = GS.L.byThread.get(near && near.slice(2)), x = n ? n.x : c ? c.port.x : 0, y = n ? n.y : c ? c.port.y : 0;
  GS.view = { k, x: px - x * k, y: py - y * k, user: true }; applyView();
}
function zoomAt(k, px, py, smooth) {
  const V = GS.view, k2 = Math.max(GS.tile ? (GS.tileFit || 0.2) * 0.9 : 0.2, Math.min(3, k));
  if (free() && M.D && k2 !== V.k) {
    if (!GS.tile && k2 < TILE_K && k2 < V.k) return setTile(true);
    if (GS.tile && k2 > V.k && k2 > Math.min(2.8, (GS.tileFit || 1) * 1.6)) return setTile(false, px, py);
  }
  GS.view = { k: k2, x: px - (px - V.x) * k2 / V.k, y: py - (py - V.y) * k2 / V.k, user: true };
  if (!smooth) scroller.setAttribute("data-drag", ""); applyView();
  if (!smooth) { clearTimeout(GS.wheelT); GS.wheelT = setTimeout(() => scroller.removeAttribute("data-drag"), 120); }
}
function refit() { GS.pin = new Map(); GS.pos = null; GS.view.user = false; GS.sig = ""; GS.tileKey = ""; if (M.D) syncGraph(M.D); }
scroller.addEventListener("wheel", e => {
  if (!free()) return; e.preventDefault();
  const r = scroller.getBoundingClientRect(), u = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? scroller.clientHeight : 1;
  if (e.ctrlKey || e.metaKey) return zoomAt(GS.view.k * Math.exp(-e.deltaY * u * 0.01), e.clientX - r.left, e.clientY - r.top, false);   // pinch, or ctrl + wheel
  const dx = (e.shiftKey && !e.deltaX ? e.deltaY : e.deltaX) * u, dy = (e.shiftKey && !e.deltaX ? 0 : e.deltaY) * u;    // the wheel scrolls, as it did
  GS.view.x -= dx; GS.view.y -= dy; GS.view.user = true; scroller.setAttribute("data-drag", ""); applyView();
  clearTimeout(GS.wheelT); GS.wheelT = setTimeout(() => scroller.removeAttribute("data-drag"), 120);
}, { passive: false });
scroller.addEventListener("pointerdown", e => {
  if (!free() || e.button !== 0 || e.target.closest(".map-hc")) return;
  const b = e.target.closest("[data-body]"), id = b && b.dataset.body, p0 = id && GS.pos && GS.pos.get(id);
  if (b && !p0) return;
  const st = { x: e.clientX, y: e.clientY, v0: { ...GS.view }, on: false };
  const mv = ev => {
    const dx = ev.clientX - st.x, dy = ev.clientY - st.y;
    if (!st.on) {
      if (Math.hypot(dx, dy) < 4) return;
      st.on = true; scroller.setPointerCapture(ev.pointerId); scroller.setAttribute("data-drag", ""); hideCard(); hideTip(); GS.view.user = true;
    }
    if (id) {
      GS.pin.set(id, { x: p0.x + dx / GS.view.k, y: p0.y + dy / GS.view.k });
      if (!GS.raf) GS.raf = requestAnimationFrame(() => { GS.raf = 0; GS.sig = ""; syncGraph(M.D); });
    } else { GS.view.x = st.v0.x + dx; GS.view.y = st.v0.y + dy; applyView(); }
  };
  const up = () => {
    scroller.removeEventListener("pointermove", mv); scroller.removeEventListener("pointerup", up); scroller.removeEventListener("pointercancel", up);
    if (st.on) { GS.dragged = true; setTimeout(() => { GS.dragged = false; }, 0); scroller.removeAttribute("data-drag"); }
  };
  scroller.addEventListener("pointermove", mv); scroller.addEventListener("pointerup", up); scroller.addEventListener("pointercancel", up);
});
scroller.addEventListener("click", e => { if (GS.dragged) { e.stopPropagation(); e.preventDefault(); GS.dragged = false; } }, true);

function put(seen, key, tag, cls, x, y, attrs, kids) {
  let el = EL.get(key); const fresh = !el;
  if (fresh) { el = document.createElement(tag); if (tag === "button") el.type = "button"; EL.set(key, el); }
  el.className = cls + (fresh ? " map-in" : "");
  for (const [k, v] of Object.entries(attrs)) { if (v == null || v === false) el.removeAttribute(k); else el.setAttribute(k, v === true ? "" : v); }
  el.style.transform = `translate(${x}px,${y}px)`;
  el.replaceChildren(...kids.flat().filter(k => k != null && k !== false).map(k => (typeof k === "string" ? document.createTextNode(k) : k)));
  if (fresh) cv.append(el);
  seen.add(key); return el;
}
const glyph = (n, sel) => h("i", { class: "map-g", "data-st": n.ghost ? "ghost" : n.agent.status, "data-w": n.agent.warden && !n.ghost ? "" : null, "data-sel": sel ? "" : null, style: `--r:${n.r}px` });
function nowLine(a) {
  const q = a.last ? since(a.last) : "", txt = a.now ? short(a.now, 160) : "";
  if (a.status === "stuck") return h("span", { class: "map-nl", "data-tone": "bad" }, `stuck${q ? " " + q : ""}${txt ? " · " + txt : ""}`);
  if (a.status === "quiet") return h("span", { class: "map-nl", "data-tone": "quiet" }, `quiet${q ? " " + q : ""}${txt ? " · " + txt : ""}`);
  if (!a.running) return h("span", { class: "map-nl", "data-tone": "quiet" }, `${status(a.status).label} ${M.D.replay ? "" : ago(a.track.end)}`.trim());
  return h("span", { class: "map-nl" }, txt || status(a.status).label);
}
function agentMeta(a) { return [[a.model, a.cost >= 0.005 ? money(a.cost) : null].filter(Boolean).join(" · "), a.from ? "from " + agentLabel(a.from) : null].filter(Boolean).join(" "); }   // no "·" before "from": this line wraps there, and a dot would dangle

function syncGraph(D) {
  const w = scroller.clientWidth || GS.w || 960, phone = w < 760, isFree = M.lay !== "bands", hh = isFree ? scroller.clientHeight : 0;
  const sig = [M.lastBuild, M.q, M.sel && M.sel.kind + M.sel.id, w, hh, M.active, M.lay].join("|");
  if (sig === GS.sig) return; GS.sig = sig;
  if (GS.lay !== M.lay || Math.abs(w - GS.w) > 40 || Math.abs(hh - GS.h) > 40) { GS.pos = null; GS.pin = new Map(); GS.view.user = false; }  // a new layout or panel size starts the graph afresh; else it settles from where it was
  GS.w = w; GS.h = hh; GS.lay = M.lay;
  const override = phone ? { pad: 12, run: 28, minColW: 300 } : null;
  const input = graphInput(D);
  if (!isFree) GS.tile = false;
  else if (w && hh && !GS.view.user) {                        // does this mesh need tiles? Asked of the card layout, so the answer doesn't depend on the answer
    const key = [M.lay, w, hh, input.threads.length, input.agents.length].join("|");
    if (key !== GS.tileKey) {
      GS.tileKey = key;
      const probe = graphLayout(input, { width: w, height: hh, override, free: true }, M.lay), b = viewBox(probe), on = !phone && b.x0 <= b.x1 && fitK(probe, b) < FIT_TILE_K;   // a phone keeps its labels: rows of bare squares say nothing
      if (on !== !!GS.tile) { GS.tile = on; GS.pos = null; GS.pin = new Map(); }
    }
  }
  const L = !isFree ? layout(input, { width: w, override }) : graphLayout(input, { width: w, height: hh, override, prev: GS.pos, pin: GS.pin, free: true, tile: !!GS.tile }, M.lay);
  GS.pos = L.pos || null;
  GS.L = L; GS.nodes = new Map(L.nodes.map(n => [n.id, n]));
  const S2 = litOf(L, D), seen = new Set(), lines = L.D.lines, compact = L.density === "compact";
  GS.hits = S2.hits;
  cv.style.height = L.height + "px"; svg.setAttribute("viewBox", `0 0 ${w} ${L.height}`); svg.setAttribute("width", w); svg.setAttribute("height", L.height);
  fx.setAttribute("viewBox", `0 0 ${w} ${L.height}`); fx.setAttribute("width", w); fx.setAttribute("height", L.height);
  cv.toggleAttribute("data-dim", S2.on); cv.toggleAttribute("data-compact", compact);
  const lit = k => (S2.on && S2.lit.has(k) ? true : null);
  // the bands
  for (const b of L.bands) put(seen, "b:" + b.tier, "div", "map-band", b.x, b.y, { "data-tier": b.tier, style: `width:${w - 2 * L.D.pad}px` },
    [h("i", { class: "map-tsw", "data-tier": b.tier }), `${b.name} · ${plural(b.count, "thread")}`]);
  // the cards
  for (const cl of L.clusters) {
    const t = cl.thread, sel = M.sel && M.sel.kind === "thread" && M.sel.id === t.id;
    const outs = Object.entries(t.outside).sort((x, y) => y[1] - x[1] || (x[0] < y[0] ? -1 : 1));
    const chips = L.cols === 1 && outs.length > 1 ? [h("span", { class: "map-chip" }, "+ " + outs.length)] : outs.slice(0, 2).map(([k, n]) => h("span", { class: "map-chip" }, k + " ", h("b", null, String(n))));
    put(seen, "c:" + t.id, "button", "map-card", cl.card.x, cl.card.y,
      { "data-kind": "thread", "data-id": t.id, "data-body": "c:" + t.id, "data-tier": t.tier, "data-sel": sel || null, "data-lit": lit("c:" + t.id), style: `width:${cl.card.w}px;height:${cl.card.h}px`, title: t.id, "aria-label": `Thread ${t.id}, ${tier(t.tier).label}, ${plural(t.posts, "post")}${t.cost > 0 ? ", " + money(t.cost) : ""}`, "aria-pressed": sel ? "true" : "false" },
      [h("span", { class: "map-cr1" }, h("i", { class: "map-tsw", "data-tier": t.tier }), (nm => h("b", { class: "map-cn", title: nm !== t.id ? t.id + ": " + nm : t.id, "data-named": nm !== t.id ? "" : null }, nm))(threadTitle(t.id)), t.cost > 0 ? h("span", { class: "map-cc" }, money(t.cost)) : null),
       compact ? null : h("span", { class: "map-cr2" }, t.stuck ? h("em", null, `▲ ${t.stuck} stuck`) : null, (m => h("span", { class: "map-cm", title: m }, m))(`${plural(t.posts, "post")} · ${since(t.last) || "–"}`), ...chips),
       h("i", { class: "map-cb" }, h("i", { style: `width:${Math.max(2, cl.costShare * 100).toFixed(1)}%` }))]);
  }
  // the agents, their ghosts, and "+N more"
  for (const n of L.nodes) {
    const x = n.x - 10, y = n.y - 9, wd = n.labelX - x + Math.max(40, n.labelW);
    if (n.more) {
      put(seen, "n:" + n.id, "button", "map-more", x, y, { "data-kind": "thread", "data-id": n.thread, "data-body": n.id, "data-lit": lit("c:" + n.thread), style: `--lx:${n.labelX - x}px` },
        [h("i", { class: "map-g", "data-st": "more", style: `--r:${n.r}px` }), h("span", { class: "map-pill" }, `+ ${n.more} more`)]);
      continue;
    }
    const a = n.agent, sel = M.sel && M.sel.kind === "agent" && M.sel.id === a.id;
    const kids = n.ghost
      ? [glyph(n), h("span", { class: "map-l1" }, h("span", { class: "map-gl" }, compact ? a.id : `${a.id} also here · ${plural(n.posts, "post")}`))]
      : [glyph(n, sel), h("span", { class: "map-l1" }, h("b", { class: "map-aid", title: a.id }, agentLabel(a.id)), h("span", { class: "map-am" }, compact ? (a.cost >= 0.005 ? " " + money(a.cost) : "") : agentMeta(a) ? " " + agentMeta(a) : "")),
         lines >= 2 ? h("span", { class: "map-l2" }, nowLine(a)) : null];
    put(seen, "n:" + n.id, "button", "map-ag", x, y,
      { "data-kind": "agent", "data-id": a.id, "data-node": n.id, "data-body": n.ghost ? null : n.id, "data-ghost": n.ghost || null, "data-ended": !a.running || null, "data-sel": sel || null, "data-lit": lit("n:" + n.id),
        style: `width:${wd}px;--lx:${n.labelX - x}px`, "aria-label": n.ghost ? `${a.id} also posted in ${n.thread}` : `${a.id}, ${status(a.status).label}, main thread ${a.main}`, "aria-pressed": sel ? "true" : "false" }, kids);
  }
  // older threads, compact view only
  if (L.olderCount) {
    put(seen, "b:older", "div", "map-band", L.D.pad, L.older.y, { style: `width:${w - 2 * L.D.pad}px` }, [`Older · ${plural(L.olderCount, S.app.app ? "conversation" : "thread")}`]);
    for (const c of L.older.chips) put(seen, "o:" + c.thread.id, "button", "map-old", c.x, c.y, { "data-kind": "thread", "data-id": c.thread.id, "data-lit": lit("c:" + c.thread.id) }, [c.thread.id + " ", h("b", null, String(c.thread.posts))]);
  }
  for (const [k, el] of EL) if (!seen.has(k)) { el.remove(); EL.delete(k); }
  requestAnimationFrame(() => { for (const el of EL.values()) el.classList.remove("map-in"); });
  // the lines: spokes, then the selection's links on top
  const t = now(), rg = [], path = [], spSeen = new Set();
  for (const r of L.rings || []) if (r.ox) rg.push(`<ellipse class="map-ring" cx="${r.cx}" cy="${r.cy}" rx="${r.ox.toFixed(1)}" ry="${r.oy.toFixed(1)}"/><text class="map-ring-t" x="${r.cx}" y="${(r.cy - r.oy + 14).toFixed(1)}">${r.name}</text>`);
  for (const hd of L.heads || []) rg.push(`<text class="map-gen" x="${hd.x - 6}" y="${hd.y}">${hd.text}</text>`);
  // spokes are kept by key, so a moved node's line eases to its new place with it (CSS `d`)
  for (const sp of L.spokes) {
    const acc = S2.acc.has(sp.node) || (GS.posted.get(sp.node) || 0) > t, key = sp.node + ">" + sp.thread, d = sp.d || spokePath(sp);
    let el = SPK.get(key); if (!el) { el = document.createElementNS(SVGNS, "path"); SPK.set(key, el); spG.append(el); }
    spSeen.add(key);
    el.setAttribute("class", "map-sp"); el.setAttribute("data-k", sp.node); el.setAttribute("d", d); el.style.d = `path("${d}")`; el.setAttribute("stroke-width", sp.width);
    for (const [k, on] of [["data-dash", sp.dashed], ["data-ended", sp.ended], ["data-acc", acc], ["data-lit", S2.on && S2.lit.has("n:" + sp.node)]]) el.toggleAttribute(k, !!on);
  }
  for (const [k, el] of SPK) if (!spSeen.has(k)) { el.remove(); SPK.delete(k); }
  path.push(`<defs><marker id="map-ah" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8z" class="map-ahd"/></marker><marker id="map-ah2" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L8 4L0 8z" class="map-ahg"/></marker></defs>`);
  for (const k of L.handoffs || []) path.push(`<path class="map-ho" marker-end="url(#map-ah2)" data-k="${k.to}" d="${k.d}"/>`);
  for (const k of S2.links) path.push(`<path class="map-ln"${k.ghost ? " data-ghost" : ` marker-end="url(#map-ah)"`} d="${linkPath(k.p, k.q)}"/>`);
  ringG.innerHTML = rg.join(""); topG.innerHTML = path.join("");
  scroller.toggleAttribute("data-free", isFree); zoomEl.hidden = !isFree;
  if (isFree) { if (!GS.view.user) fitView(L); applyView(); } else { cv.style.transform = ""; cv.removeAttribute("data-lod"); }
  syncCap(D);
  stage.setAttribute("aria-label", `Graph of the hive: ${M.count}. Tab walks ${S.app.app ? "conversations" : "threads"} and agents; Enter selects, Escape clears.`);
  if (M.q !== GS.q) { GS.q = M.q; if (S2.hits[0] && !isFree) scroller.scrollTo({ top: Math.max(0, S2.hits[0].y - 60), behavior: reduced() ? "auto" : "smooth" }); }
  legend(L);
}
function syncCap(D) {
  if (GS.capT > now()) return;
  const n = M.lanes.length, drawn = GS.L ? GS.L.clusters.length : 0;
  capEl.replaceChildren(D.replay ? "As it was at " + clock(D.t1) : "Right now", h("small", null, ` · ${winName().toLowerCase()}${n > drawn ? ` · ${plural(n - drawn, S.app.app ? "conversation" : "thread")} with no agent drawn` : ""}`));
  capEl.toggleAttribute("data-cut", D.replay); capEl.removeAttribute("data-post");
}
function legend(L) {
  const outs = {}; for (const cl of L.clusters) for (const [k, n] of Object.entries(cl.thread.outside)) outs[k] = (outs[k] || 0) + n;
  const g = st => h("i", { class: "map-g", "data-st": st, style: "--r:6px" });
  const sig = JSON.stringify(outs) + L.nodes.some(n => n.agent && n.agent.warden) + L.mode;
  if (legendEl.dataset.sig === sig) return; legendEl.dataset.sig = sig;
  legendEl.replaceChildren(...[...[["working", "working"], ["queue", "in queue"], ["quiet", "quiet"], ["stuck", "stuck"], ["failed", "failed"], ["ended", "ended"]].map(([s, l]) => h("span", null, g(s), l)),
    L.nodes.some(n => n.agent && n.agent.warden) ? h("span", null, h("i", { class: "map-g", "data-st": "working", "data-w": "", style: "--r:6px" }), "warden") : null,
    h("span", null, g("ghost"), "also posts here"),
    ...Object.entries(outs).sort((x, y) => y[1] - x[1]).map(([k, n]) => h("span", null, h("span", { class: "map-chip" }, k + " ", h("b", null, String(n)))), ) , Object.keys(outs).length ? h("span", null, "outside posts") : null].filter(Boolean));
  legendEl.title = L.mode === "lineage" ? "Arrow: who started whom. Bar: share of the cost." : "Thicker: more posts. Bar: share of the cost.";
}

// ---- pointer and keys on the graph ----
cv.addEventListener("click", e => {
  const b = e.target.closest("[data-kind]");
  if (!b) { if (M.sel) select(null); return; }
  const { kind, id } = b.dataset; hideCard();
  if (e.detail >= 2) { if (!M.sel || M.sel.id !== id) { M.sel = { kind, id }; M.update(); } openPanel(kind === "agent" ? "agent" : "thread", id); return; }
  select(kind, id);
});
scroller.addEventListener("click", e => { if (e.target === scroller && M.sel) select(null); });   // a fitted graph's box doesn't cover the panel
cv.addEventListener("pointerover", e => {
  const b = e.target.closest("[data-kind]"); if (!b || e.pointerType === "touch") return;
  if (b.dataset.node) { for (const p of svg.querySelectorAll(`[data-k="${CSS.escape(b.dataset.node)}"]`)) p.setAttribute("data-hot", ""); clearTimeout(GS.hoverT); GS.hoverT = setTimeout(() => showCard(b.dataset.node), 300); }
  else if (b.classList.contains("map-card")) { const t = GS.L && GS.L.threadById.get(b.dataset.id), r = b.getBoundingClientRect(); if (t) showTip(r.left + r.width / 2, r.bottom, threadTitle(t.id) !== t.id ? `${t.id} · ${threadTitle(t.id)}` : t.id, t.last ? "last activity " + (M.D.replay ? clock(t.last) : ago(t.last)) : "no posts yet"); }
});
cv.addEventListener("pointerout", e => {
  const b = e.target.closest("[data-kind]"); if (!b || (e.relatedTarget && b.contains(e.relatedTarget))) return;
  for (const p of svg.querySelectorAll("[data-hot]")) p.removeAttribute("data-hot");
  hideCard(); hideTip();
});
function hideCard() { clearTimeout(GS.hoverT); hcEl.hidden = true; }
function showCard(id) {
  const n = GS.nodes.get(id); if (!n || !n.agent) return; const a = n.agent, st = status(a.status), D = M.D;
  hcEl.replaceChildren(
    h("div", { class: "map-hc-h" }, glyph({ ...n, ghost: false }), h("b", null, a.id), h("span", null, [a.running ? st.label + (a.last ? " · " + ago(a.last) : "") : "ended " + clock(a.track.end), a.model, a.warden ? "warden" : null].filter(Boolean).join(" · "))),
    a.now ? h("div", { class: "map-hc-now" }, short(a.now, 220)) : null,
    h("div", { class: "map-hc-f" }, `${S.app.app ? "Main conversation" : "Main thread"} ${a.main} · ${plural(a.posts, "post")}${a.cost >= 0.005 ? " · " + money(a.cost) : ""}`),
    a.from ? h("div", { class: "map-hc-f" }, "Started by " + a.from) : null,
    h("small", null, "Click to select, double-click to open"));
  hcEl.hidden = false;
  const W = GS.w, H = GS.L.height, cw = Math.min(280, W - 16), x = Math.max(8, Math.min(n.labelX + 4, W - cw - 8));
  hcEl.style.width = cw + "px";
  const ch = hcEl.offsetHeight, y = n.y + 18 + ch > H - 8 ? n.y - 14 - ch : n.y + 18;
  hcEl.style.transform = `translate(${x}px,${Math.max(4, y)}px)`;
  if (D.replay) hcEl.querySelector(".map-hc-now")?.remove();
}

// ---- a live post: a dot runs from the agent to its thread's card ----
function emit(p) {
  const L = GS.L; if (!L || !isAgent(p.from)) return;
  const real = L.byAgent.get(p.from); if (!real) return;
  const id = real.thread === p.th ? real.id : p.from + "@" + p.th, n = GS.nodes.get(id); if (!n) return;
  GS.posted.set(id, now() + 1200);
  const sp = () => svg.querySelector(`[data-k="${CSS.escape(id)}"]`);
  const el = sp(); if (el) { el.setAttribute("data-acc", ""); el.setAttribute("data-post", ""); }
  setTimeout(() => { const e2 = sp(); if (!e2) return; e2.removeAttribute("data-post"); if (!litOfSel(id)) e2.removeAttribute("data-acc"); }, 1200);
  if (!M.D.replay) { GS.capT = now() + 4000; capEl.replaceChildren(h("b", null, p.from), " just posted in ", h("b", null, p.th)); capEl.setAttribute("data-post", ""); setTimeout(() => { if (GS.capT <= now() + 50) { GS.capT = 0; syncCap(M.D); } }, 4000); }
  if (!reduced()) runDot(id);
}
const litOfSel = id => { const s = M.sel, n = GS.nodes.get(id); if (!s || !n) return false; return s.kind === "agent" ? n.agent && n.agent.id === s.id : s.kind === "thread" && n.thread === s.id; };
function runDot(id) {
  if (GS.busy.has(id)) { GS.queued.add(id); return; }
  const p = svg.querySelector(`[data-k="${CSS.escape(id)}"]`); if (!p) return;
  GS.busy.add(id);
  const len = p.getTotalLength(), dot = document.createElementNS(SVGNS, "circle"); dot.setAttribute("r", "3.5"); dot.setAttribute("class", "map-dot"); fx.append(dot);
  const t0 = performance.now();
  const step = ts => {
    const k = Math.min(1, (ts - t0) / 600), e = 1 - Math.pow(1 - k, 3), pt = p.getPointAtLength(len * (1 - e));
    dot.setAttribute("cx", pt.x); dot.setAttribute("cy", pt.y);
    if (k < 1 && p.isConnected) return requestAnimationFrame(step);
    dot.remove(); GS.busy.delete(id); if (GS.queued.delete(id)) runDot(id);
  };
  requestAnimationFrame(step);
}

// ---- empty states ----
function emptyEl(D) {
  const nothing = !M.lanes.length && !M.more.length && !M.active;
  if (nothing || !M.active) return h("div", { class: "map-empty" }, h("span", { class: "map-empty-ic" }, icon("clock")),
    h("h2", null, M.win ? `Nothing happened in the ${winName().toLowerCase()}.` : "Nothing has happened yet."),
    M.win !== DAY && !M.tile ? h("div", { class: "map-acts" }, button("Last 24 hours", () => setWin(DAY), { size: "s" })) : null);
  const last = lastEnded(D, 1)[0], ran = new Set(S.sessions.filter(s => s.start <= D.t1 && sEnd(s) >= D.t0).map(s => s.name)).size;
  return h("div", { class: "map-empty" }, h("span", { class: "map-empty-ic" }, icon("agents")), h("h2", null, "Nobody is running"),
    h("p", null, (last ? `The last agent, ${last.name}, ended ${ago(sEnd(last))} on ${last.thread}. ` : "") + `${plural(ran, "agent")} ran in the ${winName().toLowerCase().replace(/^last /, "last ")}.`),
    M.tile ? null : h("div", { class: "map-acts" }, button("Show agents that ended", () => setActive(false), { size: "s", kind: "primary" }), M.win !== DAY ? button("Last 24 hours", () => { setActive(false); setWin(DAY); }, { size: "s" }) : null));
}
function lastEnded(D, n) {
  const seen = new Set(), out = [];
  for (let i = S.sessions.length - 1; i >= 0 && out.length < n; i--) { const s = S.sessions[i]; if (seen.has(s.name) || isRunning(s.name) || s.end == null || s.end > D.t1) continue; seen.add(s.name); out.push(s); }
  return out.sort((a, b) => b.end - a.end);
}

// ---- the replay bar: built once, its fill and handle are moved in place ----
const rpPlay = button("Replay", () => (M.playing ? stopPlay() : startPlay()), { size: "s", icon: "clock", title: "Replay this window" });
const rpFill = h("i", { class: "map-rp-f" });
const rpHandle = h("span", { class: "map-rp-h", role: "slider", tabindex: "0", "aria-label": "Replay time", "aria-valuemin": "0", "aria-valuemax": "100" });
const rpTrack = h("div", { class: "map-rp-t" }, rpFill, rpHandle);
const rpChip = h("span", { class: "map-rp-c" });
const rpBack = button("Back to live", () => { stopPlay(); setCut(null); }, { size: "s" });
const replayEl = h("div", { class: "map-rp", "data-key": "rp", "data-own": "", "data-sig": "rp" }, rpPlay, rpTrack, rpChip, rpBack);
const cutAt = x => { const r = rpTrack.getBoundingClientRect(), v = Math.max(0, Math.min(1, (x - r.left) / r.width)), a = winStart(), b = now(); return v > .995 ? null : a + v * (b - a); };
rpTrack.addEventListener("pointerdown", e => {
  e.preventDefault(); rpTrack.setPointerCapture(e.pointerId); stopPlay(); setCut(cutAt(e.clientX)); rpHandle.focus({ preventScroll: true });
  const mv = ev => setCut(cutAt(ev.clientX));
  rpTrack.addEventListener("pointermove", mv); rpTrack.addEventListener("pointerup", () => rpTrack.removeEventListener("pointermove", mv), { once: true });
});
rpHandle.addEventListener("keydown", e => {
  if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return; e.preventDefault(); e.stopPropagation(); stopPlay();
  const a = winStart(), b = now(), c = (M.cut ?? b) + (e.key === "ArrowLeft" ? -1 : 1) * (b - a) / 60; setCut(c >= b ? null : Math.max(a, c));
});
function syncReplay() {
  const a = winStart(), b = now(), v = M.cut == null ? 1 : (M.cut - a) / (b - a || 1), p = (Math.max(0, Math.min(1, v)) * 100).toFixed(2) + "%";
  rpFill.style.width = p; rpHandle.style.left = p; rpHandle.setAttribute("aria-valuenow", Math.round(v * 100)); rpHandle.setAttribute("aria-valuetext", M.cut == null ? "live" : clock(M.cut));
  rpHandle.toggleAttribute("data-cut", M.cut != null);
  rpChip.textContent = M.cut == null ? "Live" : "At " + clock(M.cut); rpChip.toggleAttribute("data-cut", M.cut != null);
  rpBack.hidden = M.cut == null;
  rpPlay.lastChild.textContent = M.playing ? "Pause" : "Replay";
}
let lastAdv = 0, lastDraw = 0;
function startPlay() { if (M.cut == null) M.cut = winStart(); M.playing = true; lastAdv = 0; syncReplay(); requestAnimationFrame(advance); }
function stopPlay() { M.playing = false; syncReplay(); }
function setCut(c) {
  const was = M.cut; M.cut = c; build(); syncReplay(); M.update();
  // replay: the posts the cut just passed light up their lines, as live posts do
  if (M.playing && was != null && c != null && c > was) for (const p of M.D.posts) if (p.t > was && p.t <= c) emit(p);
}
function advance(ts) {
  if (!M.playing) return;
  if (!replayEl.isConnected) return stopPlay();
  requestAnimationFrame(advance);
  if (document.hidden) { lastAdv = 0; return; }
  if (!lastAdv) { lastAdv = ts; return; }
  const a = winStart(), b = now(), dt = ts - lastAdv; lastAdv = ts;
  M.cut = Math.min(b, M.cut + (dt * (b - a)) / PLAY_MS);
  if (M.cut >= b) { M.playing = false; setCut(null); return; }
  if (ts - lastDraw > 120) { lastDraw = ts; setCut(M.cut); }
}

// ---- side pane ----
const fig = (label, value, small) => h("div", { class: "fig" }, h("span", null, label), h("b", null, value), small ? h("small", null, small) : null);
const sec = (title, body, more) => h("section", { class: "map-sec", "data-more": more ? "" : null }, h("h3", null, title), body);
const row = (key, ...kids) => h("div", { class: "map-row", "data-key": key }, ...kids);
const asOf = D => (D.replay ? "to " + clock(D.t1) : null);
const sideHead = (title, sub, close) => h("div", { class: "map-sh" }, h("div", null, h("h2", null, title), sub ? h("small", null, sub) : null), close ? button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" }) : null);

function needsLook(D) {
  if (D.replay) return [];
  const rank = { stuck: 0, failed: 0, limited: 1, quiet: 2 }, out = [];
  for (const t of D.allRuns) {
    const st = t.st, L = S.running.get(t.a), q = L && L.last ? now() - L.last * 1e3 : 0;
    if (st === "stuck" || st === "failed" || st === "limited" || (st === "quiet" && q > 30 * MIN)) out.push({ t, st, q, th: M.D.main.get(t.a) });
  }
  return out.sort((a, b) => rank[a.st] - rank[b.st] || b.q - a.q);
}
function lookRows(list) {
  return list.map(x => row(x.t.a, statusDot(x.st), h("div", { class: "map-rt" }, h("div", null, idLink(x.t.a), ` ${status(x.st).label} on `, thLink(x.th)),
    h("small", null, x.q ? "No output for " + dur(x.q) : status(x.st).hint)), h("span", { class: "r" }, button("Open", () => select("agent", x.t.a), { size: "s", kind: "quiet" }))));
}
function overviewPane(D) {
  const run = D.allRuns.filter(t => t.live), by = k => run.filter(t => t.st === k).length;
  const split = ["working", "quiet", "stuck", "queue"].map(k => [by(k), k]).filter(([n]) => n).map(([n, k]) => `${n} ${status(k).label}`).join(" · ");
  const hour = D.posts.filter(p => p.t > D.t1 - HOUR).length, look = needsLook(D);
  const money5 = M.lanes.concat(M.more).filter(l => l.cost > 0.005).sort((a, b) => b.cost - a.cost), top = money5.slice(0, 5), others = money5.slice(5), maxC = top[0] ? top[0].cost : 1;
  const hand = M.allHand ? D.spawns : D.spawns.slice(0, 6);
  const pairs = new Map(); for (const [k, n] of D.pairs) { const [a, b] = k.split("\n"), key = a < b ? a + "\n" + b : b + "\n" + a; const p = pairs.get(key) || { a: a < b ? a : b, b: a < b ? b : a, n: 0, ab: 0, ba: 0 }; p.n += n; if (a === p.a) p.ab += n; else p.ba += n; pairs.set(key, p); }
  const talk = [...pairs.values()].sort((x, y) => y.n - x.n).slice(0, 3);
  const empty = M.active && !run.length;
  return [
    sideHead(winName(), D.replay ? "to " + clock(D.t1) : clock(now())),
    h("div", { class: "figs" },
      fig("Running", String(run.length), split || (D.replay ? "at " + clock(D.t1) : null)),
      fig("Threads with work", String(D.withWork), M.more.length ? `+${M.more.length} with posts only` : null),
      fig("Spent", spent(D.C.total), D.replay ? asOf(D) : D.C.total > 0 ? money(spendSince(now() - HOUR)) + " an hour now" : null),
      fig("Posts", String(D.posts.length), `${hour} in the last hour`)),
    empty && lastEnded(D, 1).length ? sec("Last to end", lastEnded(D, 3).map(s => row(s.name, statusDot(agentStatus(s.name).key), h("div", { class: "map-rt" }, h("div", null, idLink(s.name), " on ", thLink(s.thread)), h("small", null, agentStatus(s.name).label)), h("span", { class: "r" }, clock(s.end)))))
      : look.length ? sec("Needs a look", lookRows(look)) : null,
    h("button", { type: "button", class: "btn map-more-btn", "data-size": "s", "aria-expanded": String(M.showMore), onclick: () => { M.showMore = !M.showMore; M.update(); } }, M.showMore ? "Hide spend, hand-offs and links" : "Spend, hand-offs and links"),
    top.length ? sec("Where the money went", [...top.map(l => h("div", { class: "map-bar", "data-key": l.th }, thLink(l.th), h("em", null, h("i", { style: `width:${(l.cost / maxC * 100).toFixed(1)}%` })), h("span", null, money(l.cost)))),
      others.length ? h("div", { class: "map-bar", "data-key": "others" }, h("span", { class: "map-dim" }, plural(others.length, "other")), h("em"), h("span", null, money(others.reduce((s, l) => s + l.cost, 0)))) : null], true) : null,
    hand.length ? sec("Hand-offs", [...hand.map(s => row(s.to + s.t, h("div", { class: "map-rt" }, h("div", null, idLink(s.from), " → ", idLink(s.to)), h("small", null, S.app.app ? threadTitle(s.th) : s.th)), h("span", { class: "r" }, clock(s.t)))),
      D.spawns.length > 6 && !M.allHand ? button(`Show all ${D.spawns.length}`, () => { M.allHand = true; M.update(); }, { size: "s", kind: "quiet" }) : null], true) : null,
    talk.length ? sec("Talk most", talk.map(p => row(p.a + p.b, h("div", { class: "map-rt" }, h("div", null, ...(p.ab && p.ba ? [idLink(p.a), " and ", idLink(p.b)] : p.ab ? [idLink(p.a), " to ", idLink(p.b)] : [idLink(p.b), " to ", idLink(p.a)]))), h("span", { class: "r" }, plural(p.n, "mention")))), true) : null,
  ];
}
function agentNums(a, D) {
  const files = (S.usage && S.usage.files) || {}; let tools = 0, tk = 0, hr = 0;
  for (const [k, f] of Object.entries(files)) {
    if (fileInfo(k).name !== a || !Object.keys(f.h).some(x => +x * 1e3 + HOUR > D.t0 && +x * 1e3 <= D.t1)) continue;
    for (const v of Object.values(f.tools || {})) tools += v;
    tk += f.t[0] + f.t[1] + f.t[2] + f.t[3];
    for (const x in f.h) if (+x * 1e3 > D.t1 - HOUR && +x * 1e3 <= D.t1) hr += f.h[x];
  }
  return { tools, tk, hr };
}
function agentPane(a, D) {
  const t = D.allRuns.find(x => x.a === a), st = t ? status(t.st) : status(agentStatus(a).key), live = !!(t && t.live);
  const ss = S.sessions.filter(s => s.name === a && s.start <= D.t1), first = ss.find(s => !s.resumed && s.start >= D.t0) || ss[ss.length - 1];
  const model = t ? t.model : ss.length ? fileModel(ss[ss.length - 1]) : "", L = S.running.get(a);
  const sub = [live ? st.label.replace(/^./, c => c.toUpperCase()) : (ss.length ? "Ended " + clock(Math.min(sEnd(ss[ss.length - 1]), D.t1)) : st.label), model, isWarden(a) ? "warden" : null,
    first && first.from && first.from !== a ? `started by ${first.from} at ${clock(first.start)}` : null].filter(Boolean).join(" · ");
  const cost = D.C.byAg.get(a) || 0, n = agentNums(a, D), up = t && live ? D.t1 - t.start : 0;
  const myPosts = D.posts.filter(p => p.from === a), ths = D.homes.get(a) || new Map(), tw = [...ths.values()].reduce((x, y) => x + y, 0) || 1;
  const talks = new Map(), bump = (b, f) => { if (b === a) return; const x = talks.get(b) || { rel: new Set(), n: 0 }; f(x); talks.set(b, x); };
  for (const s of D.spawns) { if (s.to === a) bump(s.from, x => x.rel.add("started it")); if (s.from === a) bump(s.to, x => x.rel.add("it started")); }
  for (const [k, c] of D.pairs) { const [x, y] = k.split("\n"); if (y === a) bump(x, z => { z.rel.add("asked it"); z.n += c; }); if (x === a) bump(y, z => { z.rel.add("it asked"); z.n += c; }); }
  const tl = [...talks].sort((x, y) => y[1].n - x[1].n || (x[0] < y[0] ? -1 : 1)).slice(0, 8);
  return [
    h("div", { class: "map-idh" }, h("span", { class: "map-av", "aria-hidden": "true" }, a.slice(0, 3), statusDot(t ? t.st : agentStatus(a).key, { live })),
      h("div", { class: "map-idh-t" }, h("b", null, a), h("small", null, sub)), button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" })),
    L && L.doing && !D.replay ? h("div", { class: "map-now" }, h("div", null, "Now: " + short(L.doing, 220)), L.last ? h("small", null, "Last action " + ago(L.last * 1e3)) : null) : null,
    h("div", { class: "figs" },
      fig("Spent", spent(cost), n.hr ? money(n.hr) + " in the last hour" : asOf(D)),
      fig("Up", up ? dur(up) : "–", live ? "since " + clock(t.start) : null),
      fig("Posts", String(myPosts.length), plural(new Set(myPosts.map(p => p.th)).size, "thread")),
      fig("Work", plural(n.tools, "tool call"), n.tk ? toks(n.tk) + " tokens" : null)),
    ths.size ? sec("Threads it worked on", [...ths].sort((x, y) => y[1] - x[1]).slice(0, 6).map(([th, w]) => h("div", { class: "map-bar", "data-key": th }, thLink(th), h("em", null, h("i", { style: `width:${(w / tw * 100).toFixed(1)}%` })), h("span", null, Math.round(w / tw * 100) + "%"))), true) : null,
    tl.length ? sec("Talks with", tl.map(([b, x]) => row(b, statusDot(agentStatus(b).key), h("div", { class: "map-rt" }, h("div", null, idLink(b)), h("small", null, [...x.rel].join(", "))), x.n ? h("span", { class: "r" }, plural(x.n, "mention")) : null)), true) : null,
    h("div", { class: "map-acts" }, isAgent(a) ? button("Open agent page", () => openPanel("agent", a), { size: "s", kind: "primary" }) : null, live && !D.replay ? killButton(a) : null),
  ];
}
function threadPane(th, D) {
  const l = M.lanes.find(x => x.th === th) || M.more.find(x => x.th === th), t = tier(tierOf(th)), d = S.threads.get(th);
  const ps = D.posts.filter(p => p.th === th), runs = l ? l.tracks.filter(x => x.kind === "run") : [], live = runs.filter(x => x.live).length;
  return [
    h("div", { class: "map-idh" }, h("span", { class: "lane-sw", "data-tone": t.tone, "aria-hidden": "true" }),
      h("div", { class: "map-idh-t" }, h("b", null, th), h("small", null, [threadTitle(th) !== th ? threadTitle(th) : null, t.label].filter(Boolean).join(" · "))), button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" })),
    h("div", { class: "figs" },
      fig("Posts", String(ps.length), `${d ? d.posts.length : ps.length} in all`),
      fig("Spent", spent(l ? l.cost : 0), asOf(D)),
      fig("Agents", String(runs.length), live ? `${live} running` : null),
      fig("Last post", ps.length ? clock(ps[ps.length - 1].t) : "–", ps.length ? ago(ps[ps.length - 1].t) : null)),
    runs.length ? sec("Agents", runs.map(x => { const L = S.running.get(x.a); return row(x.a, statusDot(x.st, { live: x.live }), h("div", { class: "map-rt" }, h("div", null, idLink(x.a), x.model ? " · " + x.model : "", x.from ? " · from " + x.from : ""),
      h("small", null, x.live && L && L.doing && !D.replay ? "Now: " + short(L.doing, 120) : x.live ? status(x.st).label : "Ended " + clock(x.end)))); })) : null,
    ps.length ? sec("Latest posts", ps.slice(-3).reverse().map(p => h("div", { class: "map-post", "data-key": p.id }, h("div", null, h("b", null, p.from), " · " + clock(p.t)), h("p", null, firstSentence(p.text)))), true) : null,
    h("div", { class: "map-acts" }, button("Open thread", () => openPanel("thread", th), { size: "s", kind: "primary" })),
  ];
}
function outsidePane(who, D) {
  const ps = D.posts.filter(p => p.from === who), by = new Map(); for (const p of ps) by.set(p.th, (by.get(p.th) || 0) + 1);
  const ths = [...by].sort((x, y) => y[1] - x[1]), max = ths[0] ? ths[0][1] : 1;
  return [
    h("div", { class: "map-idh" }, h("span", { class: "map-av", "aria-hidden": "true" }, who.slice(0, 3)),
      h("div", { class: "map-idh-t" }, h("b", null, who), h("small", null, "outside the agents")), button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" })),
    h("div", { class: "figs" }, fig("Posts", String(ps.length), asOf(D)), fig("Threads", String(ths.length), null)),
    ths.length ? sec("Where they posted", ths.slice(0, 8).map(([th, n]) => h("div", { class: "map-bar", "data-key": th }, thLink(th), h("em", null, h("i", { style: `width:${(n / max * 100).toFixed(1)}%` })), h("span", null, String(n))))) : null,
    ps.length ? sec("Latest", ps.slice(-3).reverse().map(p => h("div", { class: "map-post", "data-key": p.id }, h("div", null, h("b", null, p.th), " · " + clock(p.t)), h("p", null, firstSentence(p.text))))) : null,
  ];
}
function sidePane(D) {
  const s = M.sel;
  const body = !s ? overviewPane(D) : s.kind === "agent" ? agentPane(s.id, D) : s.kind === "outside" ? outsidePane(s.id, D) : threadPane(s.id, D);
  return h("aside", { class: "map-side", "data-open": M.showMore ? "" : null, "data-view": s ? s.kind : "overview" }, body);
}

// ---- toolbar ----
function setWin(w) { M.win = w; setPref("meshWin", w); M.cut = null; M.playing = false; build(); syncReplay(); M.update(); }
function setActive(on) { M.active = on; setPref("meshActive", on ? 1 : 0); build(); M.update(); }
function setLay(k) {
  M.lay = k; setPref("meshLayout", k);
  cv.setAttribute("data-move", ""); clearTimeout(GS.moveT); GS.moveT = setTimeout(() => cv.removeAttribute("data-move"), 700);
  M.update();
}
function toolbar() {
  return h("div", { class: "map-tb" },
    seg(WINS.map(([l, w]) => [w, l]), M.win, setWin, { label: "Window" }),
    toolsButton(M.tools, () => { M.tools = !M.tools; M.update(); }, (M.active ? 0 : 1) + (M.q ? 1 : 0)),
    toolsMore(seg(LAYOUTS.map(([k, l, t]) => [k, l, t, null, k]), M.lay, setLay, { label: "Layout" })),
    h("label", { class: "map-sw tools-more" }, h("input", { type: "checkbox", role: "switch", class: "switch", checked: M.active, "aria-label": "Running only", onchange: (e, n) => setActive(n.checked) }), "Running only"),
    h("label", { class: "map-search tools-more" }, icon("search"), h("input", { class: "input", type: "search", placeholder: "Find an agent or thread", "aria-label": "Find an agent or thread", value: M.q,
      oninput: (e, n) => { M.q = n.value.trim().toLowerCase(); M.update(); },
      onkeydown: (e, n) => { if (e.key === "Enter" && GS.hits[0]) { e.preventDefault(); const f = GS.hits[0]; M.q = ""; n.value = ""; select(f.kind, f.id); } } })),
    h("span", { class: "map-sum", role: "status" }, M.count));
}
// ---- hover tips ----
function runTip(a) {
  const D = M.D, t = D.allRuns.find(x => x.a === a), L = S.running.get(a), st = t ? status(t.st) : status(agentStatus(a).key);
  const n = D.posts.filter(p => p.from === a).length, hr = agentNums(a, D).hr;
  const up = t ? (t.live ? D.t1 - t.start : t.end - t.start) : 0;
  return [`${a} · ${t && !t.live ? "ended " + clock(t.end) : st.label}${t && t.model ? " · " + t.model : ""}${isWarden(a) ? " · warden" : ""}`,
    L && L.doing && !D.replay ? "Now: " + short(L.doing, 140) : null,
    [up ? "Up " + dur(up) : null, hr ? money(hr) + " an hour" : null, plural(n, "post"), L && L.last && !D.replay ? "last action " + ago(L.last * 1e3) : null, t && t.from ? "started by " + t.from : null].filter(Boolean).join(" · ")].filter(Boolean);
}
// ---- for tests and the display tiles ----
const G = {
  selected: () => (M.sel ? { ...M.sel, id: M.sel.kind === "thread" ? "#" + M.sel.id : M.sel.id } : null),
  select(id) { if (id.startsWith("#")) select("thread", id.slice(1)); else select("agent", id); return true; },
  clear() { M.sel = null; M.update(); },
  emit(p) { emit(p); },
  get lanes() { return M.lanes; }, get data() { return M.D; }, get el() { return M.root; }, get state() { return M; }, get layout() { return GS.L; },
  get nodes() { return new Map(GS.L ? GS.L.nodes.filter(n => n.agent && !n.ghost).map(n => [n.agent.id, { id: n.agent.id, warden: n.agent.warden, thread: n.thread, status: n.agent.status }]) : []); },
};
export { G as graph };
/** What a display tile says in its head. */
export const meshNote = () => M.count;
/** A display tile keeps the Map tab's window; 6 hours until the TV picks one. */
export function meshForDisplay() { M.tile = true; M.win = getPref("meshWin", 6 * HOUR); if (M.lay === "bands") M.lay = "lineage"; }   // a TV can't scroll: Bands runs past the tile, so it gets the newest layout
let unsub = null;
// The deck: BROWSE walks the agents in map order; crossfader and left jog scrub the replay; far right is live.
const walk = () => M.lanes.flatMap(l => l.tracks.filter(t => t.kind === "run").map(t => t.a));
const meshDeck = {
  browse(d) { const l = walk(); if (!l.length) return true; const c = M.sel && M.sel.kind === "agent" ? l.indexOf(M.sel.id) : -1; M.sel = { kind: "agent", id: l[(Math.max(c, d > 0 ? -1 : 0) + d + l.length * 8) % l.length] }; M.update(); return true; },
  open() { if (M.sel) openPanel(M.sel.kind, M.sel.id); return true; },
  picked() { return M.sel ? { [M.sel.kind]: M.sel.id } : null; },
  jog(d, n, shift) { const a = winStart(), b = now(), c = (M.cut ?? b) + n * (shift ? 6 : 1) * (b - a) / 1500; M.playing = false; setCut(c >= b ? null : Math.max(a, c)); return true; },
  scrub(v) { const a = winStart(), b = now(); M.playing = false; setCut(v > .995 ? null : a + v * (b - a)); },
  play() { if (M.playing) stopPlay(); else startPlay(); return true; },
  fit() { if (free()) refit(); else scroller.scrollTop = 0; },
  toggle() { setActive(!M.active); },
  window(v) { const i = Math.min(WINS.length - 1, Math.floor(v * WINS.length)), w = WINS[i][1]; if (w !== M.win) setWin(w); },
};
export default {
  deck: meshDeck,
  id: "mesh", label: () => (S.app.app ? "Map" : "Mesh"), about: "where the agents are: a graph of threads and the agents working in them", tab: true,
  enter(ctx) {
    M.update = ctx.update;
    // live: a new post pulses its agent's dot; the map rebuilds at most every 2 s
    unsub = subscribe(w => { if (M.cut != null || !w.posts || !w.posts.length) return; for (const p of w.posts) emit(p); clearTimeout(M.timer); M.timer = setTimeout(() => M.update(), 950); });
  },
  leave() { if (unsub) unsub(); unsub = null; M.tile = false; M.playing = false; clearTimeout(M.timer); hideTip(); },
  escape() { if (M.sel) { M.sel = null; M.update(); return true; } },
  render(ctx) {
    M.update = ctx.update;
    // before the live feed has answered, "nobody is running" would be a guess: say it's loading, and build nothing yet
    if (!S.live) return h("div", { class: "map", "data-key": "mesh", "aria-label": "Map: loading" }, navBox(), waiting(S.failed, "Reading…", "the map"));
    if (!M.D || (M.cut == null && M.seenVer !== S.ver && now() - M.lastBuild >= 2000) || (M.cut == null && now() - M.lastBuild > 30000)) { M.seenVer = S.ver; build(); }
    else if (M.cut == null && M.seenVer !== S.ver) { clearTimeout(M.timer); M.timer = setTimeout(() => M.update(), 2000 - (now() - M.lastBuild)); }
    const D = M.D;
    if (M.sel && !(M.sel.kind === "agent" ? D.homes.has(M.sel.id) || D.allRuns.some(t => t.a === M.sel.id) : M.sel.kind === "outside" ? D.posts.some(p => p.from === M.sel.id) : M.lanes.some(l => l.th === M.sel.id) || M.more.some(l => l.th === M.sel.id))) M.sel = null;
    syncReplay();
    const showEmpty = !M.lanes.length && (M.active || !M.more.length);
    if (!showEmpty) syncGraph(D);
    const chart = h("main", { class: "map-chart" }, showEmpty ? emptyEl(D) : keep(stage), M.tile ? null : replayEl);
    const root = h("div", { class: "map", "data-key": "mesh", "data-tile": M.tile ? "" : null, "aria-label": "Map: " + M.count },
      M.tile ? null : navBox(), M.tile ? null : toolbar(), h("div", { class: "map-bd" }, chart, M.tile ? null : sidePane(D)));
    // the live element is found after the page puts it in place
    requestAnimationFrame(() => {
      M.root = document.querySelector(".map[data-key=mesh]");
      // the morph can put the empty stand-in from keep() in place of the stage (a view switch mid-render): swap the real one back
      const live = M.root && M.root.querySelector(".map-stage");
      if (live && live !== stage) { live.replaceWith(stage); GS.sig = ""; syncGraph(M.D); }
    });
    return root;
  },
};
