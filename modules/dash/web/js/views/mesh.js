// Map: where the hive's agents are, as a graph with no time axis (Timeline is the view for "when"). Threads are tier-toned
// rectangles; each agent is a status-shaped circle beside its main thread, with faint dashed lines to other threads it posted in;
// the person, the box and other Hives are diamonds. Live agents ring and pulse, ended ones fade. The window picks who appears;
// replay shows the graph as it was at the cut. No lines between agents: mentions and hand-offs live in the side pane.
import { h } from "../core/dom.js";
import { S, OUTSIDE, SYS, subscribe, getPref, setPref, onTV } from "../core/store.js";
import { agentStatus, isAgent, isRunning, isWarden, modelName, sEnd, threadTitle, tierOf, fileInfo, spendSince } from "../core/model.js";
import { ago, clock, dur, firstSentence, money, now, short, toks, plural, DAY, HOUR, MIN } from "../core/format.js";
import { agentHref, threadHref } from "../core/router.js";
import { status, tier } from "../core/status.js";
import { button, icon, seg, statusDot } from "../ui/kit.js";
import { killButton } from "../actions.js";
import { openPanel } from "../ui/panel.js";
import { showTip, hideTip } from "../ui/tip.js";
import { createGraph, keep } from "../ui/graph.js";

const WINS = [["1h", HOUR], ["6h", 6 * HOUR], ["24h", DAY], ["3d", 3 * DAY], ["All", 0]];
const WIN_NAME = { [HOUR]: "hour", [6 * HOUR]: "6 hours", [DAY]: "24 hours", [3 * DAY]: "3 days" };
const TIER_ORDER = { tier1: 0, tier2: 1, held: 2, other: 3 };
const PLAY_MS = 12000;
const M = {
  win: getPref("meshWin", 6 * HOUR), active: !!getPref("meshActive", onTV() ? 1 : 0), cut: null, playing: false, q: "",
  sel: null, lanes: [], more: [], D: null, count: "", seenVer: -1, lastBuild: 0, update: () => {},
  showMore: false, allHand: false, timer: 0, root: null,
};
const firstT = () => (S.posts[0] ? S.posts[0].t : now() - DAY);
const winStart = () => (M.win ? now() - M.win : firstT());
const curT = () => M.cut ?? now();
const span = () => { const t1 = curT(); return [M.win ? t1 - M.win : Math.min(firstT(), t1 - MIN), t1]; };
const winName = () => (M.win ? "Last " + WIN_NAME[M.win] : "All time");

// ---- data ----
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
  const main = new Map(); for (const [a, m] of homes) main.set(a, [...m].sort((x, y) => y[1] - x[1] || (x[0] < y[0] ? -1 : 1))[0][0]);
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
  const live = allRuns.filter(t => t.live).length;
  M.count = M.active && !live ? (replay ? "Nobody was running then" : "Nobody is running right now") : `${live} running · ${plural(lanes.length, "thread")} · ${money(C.total)}`;
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
const idLink = a => h("a", { class: "map-id", href: isAgent(a) ? agentHref(a) : threadHref("@" + a), onclick: pickAgent(a) }, a);
const thLink = th => h("a", { class: "map-id", href: threadHref(th), onclick: pickThread(th) }, th);

// ---- the graph: threads are tier-toned rectangles, agents are status-shaped circles joined to them ----
// An agent sits beside its main thread (a solid line); other threads it posted in get a faint dashed line. The person, the box and
// other Hives are diamonds with faint lines to the threads they posted in. No lines between agents: hand-offs and mentions are in
// the side pane. Live agents ring and pulse; ended ones fade with age.
const MAX_THREADS = 40;
const fade = (last, t1, thread) => Math.max(thread ? .45 : .4, Math.exp(-(t1 - last) / (thread ? 12 : 6) / HOUR));
function graphData(D) {
  const q = M.q, nodes = new Map(), edges = [], t1 = D.t1;
  let lanes = M.lanes; if (q) lanes = lanes.filter(l => l.th.includes(q) || l.tracks.some(t => t.a.includes(q)));
  lanes = lanes.slice(0, MAX_THREADS);
  const hit = id => !q || id.includes(q);
  for (const l of lanes) {
    const t = tier(l.tier), id = "#" + l.th;
    nodes.set(id, { id, kind: "thread", name: l.th, tier: l.tier, tone: t.tone, shape: "dot", cluster: l.th, posts: l.posts, last: l.last, cost: l.cost, liveN: l.live,
      r: Math.min(26, 11 + 2 * Math.sqrt(l.posts)), label: short(l.th, 24), big: true, labelStrong: l.live > 0, opacity: q && !hit(l.th) ? .35 : l.live ? 1 : fade(l.last, t1, true),
      labelRank: 1e5 + (l.live ? 2e5 : 0) + l.posts, ring: !!(q && l.th.includes(q)) });
  }
  const tether = (a, th, kind, share, n) => edges.push({ a, b: "#" + th, tether: true, to: "#" + th, share, n, kind, weight: n || 1, faint: kind !== "main", dashed: kind === "also" });
  for (const l of lanes) for (const t of l.tracks) {
    if (t.kind === "run") {
      const st = status(t.st), n = D.aposts.get(t.a + "\n" + l.th)?.length || 0, all = D.posts.filter(p => p.from === t.a).length;
      nodes.set(t.a, { id: t.a, kind: "agent", tone: t.live ? st.tone : "quiet", shape: t.live ? st.shape : "dot", cluster: l.th, live: t.live, st: t.st, last: t.end, posts: all, model: t.model,
        r: Math.min(15, 6 + 1.2 * Math.sqrt(all)), warden: isWarden(t.a), label: t.a, big: t.live || all >= 6 || isWarden(t.a), labelStrong: t.live,
        opacity: q && !hit(t.a) ? .25 : t.live ? 1 : fade(t.end, t1), labelRank: (t.live ? 3e5 : 0) + all * 10, ring: !!(q && t.a.includes(q)) });
      tether(t.a, l.th, "main", 1, n);
    }
  }
  // "also" lines only to an agent that is drawn; the person and the others outside are one diamond each
  for (const l of lanes) for (const t of l.tracks) {
    if (t.kind === "also" && nodes.has(t.a)) tether(t.a, l.th, "also", .3, t.n);
    if (t.kind === "person") {
      if (!nodes.has("@" + t.a)) nodes.set("@" + t.a, { id: "@" + t.a, who: t.a, kind: "outside", tone: "ink", shape: "diamond", cluster: null, posts: 0, last: 0, r: 10, label: t.a, big: true, labelStrong: true, opacity: 1, labelRank: 4e5 });
      const o = nodes.get("@" + t.a); o.posts += t.n; o.last = Math.max(o.last, t.posts[t.posts.length - 1].t);
      tether("@" + t.a, l.th, "person", .5, t.n);
    }
  }
  return { nodes: [...nodes.values()], edges };
}
const gid = sel => (sel ? (sel.kind === "thread" ? "#" + sel.id : sel.kind === "outside" ? "@" + sel.id : sel.id) : null);
const nodeTip = n => n.kind === "thread"
  ? [threadTitle(n.name) !== n.name ? `${n.name} · ${threadTitle(n.name)}` : n.name, [tier(n.tier).label, n.liveN ? `${n.liveN} running` : null, plural(n.posts, "post"), n.cost ? money(n.cost) : null, n.last ? "last " + (M.D.replay ? clock(n.last) : ago(n.last)) : null].filter(Boolean).join(" · ")]
  : n.kind === "outside" ? [n.who, `${plural(n.posts, "post")} in this window`] : runTip(n.id);
const edgeTip = e => { const th = (e.to || "").slice(1), who = e.a.startsWith("@") ? e.a.slice(1) : e.a;
  return [`${who} → ${th}`, e.kind === "main" ? `its main thread · ${plural(e.n || 0, "post")}` : `${plural(e.n || 0, "post")} here too`]; };
let stage = null;
const GR = createGraph({
  // nothing lands under the caption at the top
  margins: W => ({ top: 34, side: W < 640 ? 14 : 28, bottom: 16 }),
  onHover: (it, e) => { if (!it || matchMedia("(hover: none)").matches) hideTip(); else showTip(e.clientX, e.clientY, ...(it.id ? nodeTip(it) : edgeTip(it))); },
  onSelect: (it, e) => {
    if (!it) { if (M.sel) select(null); return; }
    if (!it.id) { if (it.to) select("thread", it.to.slice(1)); return; }   // a line picks its thread
    const kind = it.kind === "thread" ? "thread" : it.kind === "outside" ? "outside" : "agent", id = it.kind === "thread" ? it.name : it.kind === "outside" ? it.who : it.id;
    if (e && e.type === "keydown") { M.sel = { kind, id }; M.update(); return; }   // walking with the arrows never toggles off
    select(kind, id);
  },
  onOpen: n => (n.kind === "outside" ? (location.hash = threadHref("@" + n.who)) : isAgent(n.id) ? openPanel("agent", n.id) : null),
});
const capEl = h("div", { class: "map-cap" });
stage = h("div", { class: "map-stage", "data-own": "", "data-sig": "mesh" }, GR.el, capEl);
let gsig = "";
function syncGraph(D) {
  const sig = M.lastBuild + "\n" + M.q;
  if (sig !== gsig) {
    gsig = sig; const g = graphData(D);
    GR.discs(false); GR.clusterLabels({}); GR.set(g.nodes, g.edges);
    GR.el.setAttribute("aria-label", `Graph of the hive: ${M.count}. Arrow keys walk it, Enter opens an agent, plus and minus zoom, 0 refits.`);
  }
  const want = gid(M.sel), have = GR.selected();
  if (want && GR.nodes.has(want)) { if (!have || have.id !== want) GR.select(want); } else if (have) GR.clear();
  const n = M.lanes.length, shown = Math.min(n, MAX_THREADS);
  capEl.replaceChildren(D.replay ? "As it was at " + clock(D.t1) : "Right now", h("small", null, ` · ${winName().toLowerCase()}${n > shown ? ` · busiest ${shown} of ${n} threads` : ""}`));
  capEl.toggleAttribute("data-cut", D.replay);
}

// ---- empty states ----
function emptyEl(D) {
  const nothing = !M.lanes.length && !M.more.length && !M.active;
  if (nothing || !M.active) return h("div", { class: "map-empty" }, h("span", { class: "map-empty-ic" }, icon("clock")),
    h("h2", null, M.win ? `Nothing happened in the ${winName().toLowerCase()}.` : "Nothing has happened yet."),
    M.win !== DAY ? h("div", { class: "map-acts" }, button("Last 24 hours", () => setWin(DAY), { size: "s" })) : null);
  const last = lastEnded(D, 1)[0], ran = new Set(S.sessions.filter(s => s.start <= D.t1 && sEnd(s) >= D.t0).map(s => s.name)).size;
  return h("div", { class: "map-empty" }, h("span", { class: "map-empty-ic" }, icon("agents")), h("h2", null, "Nobody is running"),
    h("p", null, (last ? `The last agent, ${last.name}, ended ${ago(sEnd(last))} on ${last.thread}. ` : "") + `${plural(ran, "agent")} ran in the ${winName().toLowerCase().replace(/^last /, "last ")}.`),
    h("div", { class: "map-acts" }, button("Show agents that ended", () => setActive(false), { size: "s", kind: "primary" }), M.win !== DAY ? button("Last 24 hours", () => { setActive(false); setWin(DAY); }, { size: "s" }) : null));
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
const replayEl = h("div", { class: "map-rp", "data-own": "", "data-sig": "rp" }, rpPlay, rpTrack, rpChip, rpBack);
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
function emit(p) {
  const id = OUTSIDE.has(p.from) ? "@" + p.from : p.from; if (!GR.nodes.has(id)) return;
  GR.ripple(id); if (GR.nodes.has("#" + p.th)) GR.pulse(id, "#" + p.th, 800);
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
      fig("Spent", money(D.C.total), D.replay ? asOf(D) : money(spendSince(now() - HOUR)) + " an hour now"),
      fig("Posts", String(D.posts.length), `${hour} in the last hour`)),
    empty ? sec("Last to end", lastEnded(D, 3).map(s => row(s.name, statusDot(agentStatus(s.name).key), h("div", { class: "map-rt" }, h("div", null, idLink(s.name), " on ", thLink(s.thread)), h("small", null, agentStatus(s.name).label)), h("span", { class: "r" }, clock(s.end)))))
      : look.length ? sec("Needs a look", lookRows(look)) : null,
    h("button", { type: "button", class: "btn map-more-btn", "data-size": "s", "aria-expanded": String(M.showMore), onclick: () => { M.showMore = !M.showMore; M.update(); } }, M.showMore ? "Hide spend, hand-offs and links" : "Spend, hand-offs and links"),
    top.length ? sec("Where the money went", [...top.map(l => h("div", { class: "map-bar", "data-key": l.th }, thLink(l.th), h("em", null, h("i", { style: `width:${(l.cost / maxC * 100).toFixed(1)}%` })), h("span", null, money(l.cost)))),
      others.length ? h("div", { class: "map-bar", "data-key": "others" }, h("span", { class: "map-dim" }, plural(others.length, "other")), h("em"), h("span", null, money(others.reduce((s, l) => s + l.cost, 0)))) : null], true) : null,
    hand.length ? sec("Hand-offs", [...hand.map(s => row(s.to + s.t, h("div", { class: "map-rt" }, h("div", null, idLink(s.from), " → ", idLink(s.to)), h("small", null, s.th)), h("span", { class: "r" }, clock(s.t)))),
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
      fig("Spent", money(cost), n.hr ? money(n.hr) + " in the last hour" : asOf(D)),
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
      fig("Spent", money(l ? l.cost : 0), asOf(D)),
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
function toolbar() {
  return h("div", { class: "map-tb" },
    seg(WINS.map(([l, w]) => [w, l]), M.win, setWin, { label: "Window" }),
    h("label", { class: "map-sw" }, h("input", { type: "checkbox", role: "switch", class: "switch", checked: M.active, "aria-label": "Running only", onchange: (e, n) => setActive(n.checked) }), "Running only"),
    h("label", { class: "map-search" }, icon("search"), h("input", { class: "input", type: "search", placeholder: "Find an agent or thread", "aria-label": "Find an agent or thread", value: M.q,
      oninput: (e, n) => { M.q = n.value.trim().toLowerCase(); M.update(); } })),
    h("span", { class: "map-sum", role: "status" }, M.count));
}
const key = () => h("div", { class: "map-key" },
  h("span", null, statusDot("working", { live: true }), "working"), h("span", null, statusDot("quiet"), "quiet"), h("span", null, statusDot("stuck"), "stuck"),
  h("span", null, h("i", { class: "map-k-ag", "data-ended": "" }), "ended (paler is longer ago)"), h("span", null, h("i", { class: "map-k-th" }), "thread, coloured by tier"),
  h("span", null, h("i", { class: "map-k-ln" }), "its main thread"), h("span", null, h("i", { class: "map-k-ln", "data-also": "" }), "posted there too"),
  h("span", null, h("i", { class: "tok-dm" }), "you, box, Hives"));

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
  get lanes() { return M.lanes; }, get data() { return M.D; }, get el() { return M.root; }, get state() { return M; }, get graph() { return GR; },
};
export { G as graph };
/** What a display tile says in its head. */
export const meshNote = () => M.count;
/** A display tile keeps the Map tab's window; 6 hours until the TV picks one. */
export function meshForDisplay() { M.tile = true; M.win = getPref("meshWin", 6 * HOUR); }
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
  fit() { GR.refit(); },
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
    if (!M.D || (M.cut == null && M.seenVer !== S.ver && now() - M.lastBuild >= 2000) || (M.cut == null && now() - M.lastBuild > 30000)) { M.seenVer = S.ver; build(); }
    else if (M.cut == null && M.seenVer !== S.ver) { clearTimeout(M.timer); M.timer = setTimeout(() => M.update(), 2000 - (now() - M.lastBuild)); }
    const D = M.D;
    if (M.sel && !(M.sel.kind === "agent" ? D.homes.has(M.sel.id) || D.allRuns.some(t => t.a === M.sel.id) : M.sel.kind === "outside" ? D.posts.some(p => p.from === M.sel.id) : M.lanes.some(l => l.th === M.sel.id) || M.more.some(l => l.th === M.sel.id))) M.sel = null;
    syncReplay();
    const showEmpty = !M.lanes.length && (M.active || !M.more.length);
    if (!showEmpty) syncGraph(D);
    const chart = h("main", { class: "map-chart" }, showEmpty ? emptyEl(D) : keep(stage), M.tile ? null : replayEl, M.tile || showEmpty ? null : key());
    const root = h("div", { class: "map", "data-key": "mesh", "data-tile": M.tile ? "" : null, "aria-label": "Map: " + M.count },
      M.tile ? null : toolbar(), h("div", { class: "map-bd" }, chart, M.tile ? null : sidePane(D)));
    // the live element is found after the page puts it in place
    requestAnimationFrame(() => { M.root = document.querySelector(".map[data-key=mesh]"); });
    return root;
  },
};
