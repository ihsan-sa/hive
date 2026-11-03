// Graph: a force layout on canvas that keeps things apart by group. Takes plain nodes and edges; knows nothing about agents.
// Layout: a group (a cluster key) is drawn as a disc whose size comes only from how many nodes it holds, at one spacing for
// every disc, and each disc keeps a small, fixed gap around it. Gravity packs the discs in the stage's shape and Fit zooms to
// the pack, up to 1.8x, so a quiet graph sits in the middle at its natural size instead of being blown up.
// A node with several homes (homes on its edges: tether) is not put in any one disc: it sits outside them, pulled towards
// each of its discs by how much it belongs there, with a thin line to each. Links inside a disc are straight; links between
// discs bend through a gate on each disc so they run together as a bundle. Weak links fade once the graph is busy.
//
// createGraph({ onHover(node|edge|null, event), onSelect(node|edge|null), onOpen(node), margins })
//   .el                  the canvas; put it in a data-own stage
//   .set(nodes, edges)   nodes: {id, kind, cluster, anchor, r, opacity, tone, shape, live, label, labelRank, big, labelStrong, warden}
//                          anchor: an invisible node that only holds its cluster's place (a thread with nobody only in it)
//                        edges: {a, b, weight, age, faint, dashed, arrowTo, hub, tether, to, share}
//                          tether: b or a (the one named in to) is an anchor or member of the disc this node also belongs to
//   .select(id) .selectEdge(e) .selected() .refit() .relayout() .nudge(alpha) .pulse(a, b, ms) .ripple(a) .zoom(k) .clusterLabels(map)
import { color } from "../core/theme.js";
import { h } from "../core/dom.js";

const seeded = id => { let x = 2166136261; for (let i = 0; i < id.length; i++) x = Math.imul(x ^ id.charCodeAt(i), 16777619); return ((x >>> 0) % 1e6) / 1e6; };
// No prefers-reduced-motion check here: the person wants the pulses, and a browser or Windows setting had silently turned them all off (2026-11-03).
const rootZoom = () => parseFloat(getComputedStyle(document.documentElement).zoom) || 1;

/** A status shape as an SVG path, so canvas (Path2D) and SVG draw the same marks. dot is a circle; ring is drawn hollow by the caller. */
export function shapePath(shape, x, y, r) {
  const circle = `M${x - r} ${y}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0Z`;
  switch (shape) {
    case "square": { const q = r * .88; return `M${x - q} ${y - q}h${2 * q}v${2 * q}h${-2 * q}Z`; }
    case "tri": { const q = r * 1.2; return `M${x} ${y - q}L${x + q} ${y + q * .8}L${x - q} ${y + q * .8}Z`; }
    case "diamond": { const q = r * 1.2; return `M${x} ${y - q}L${x + q} ${y}L${x} ${y + q}L${x - q} ${y}Z`; }
    default: return circle;
  }
}
export const hollow = shape => shape === "ring";
/** A warden's mark: a hexagon around the node. It goes around the status shape, so shape and colour still say the status. */
const hexPath = (x, y, r) => { const q = r * .866, m = r / 2; return `M${x} ${y - r}L${x + q} ${y - m}L${x + q} ${y + m}L${x} ${y + r}L${x - q} ${y + m}L${x - q} ${y - m}Z`; };
/** A long-lived element for a data-own slot: itself the first time, then a bare copy so morph leaves the real one alone. */
export const keep = el => (el.isConnected ? el.cloneNode(false) : el);

const RECT_H = .72;   // a thread's rectangle: half its height, as a share of half its width
const TOKENS = ["--ink", "--ink-2", "--ink-3", "--surface", "--line", "--line-2", "--good", "--warn", "--bad", "--info", "--quiet", "--ended", "--tier1", "--tier2", "--held", "--other", "--warden"];
const TONES = { good: "--good", warn: "--warn", bad: "--bad", info: "--info", quiet: "--quiet", ended: "--ended", ink: "--ink", tier1: "--tier1", tier2: "--tier2", held: "--held", other: "--other" };

// inPage: the graph sits in a page that scrolls, so plain wheel and a one-finger swipe scroll the page; ctrl+wheel and pinch zoom, and a swipe that starts on a node drags it.
export function createGraph({ onHover, onSelect, onOpen, onViewChange, margins = { top: 16, bottom: 16, side: 24 }, inPage = false } = {}) {
  const cv = h("canvas", { class: "graph", tabindex: "0", role: "application", "data-inpage": inPage ? "" : null });
  const ctx = cv.getContext("2d");
  const G = { nodes: new Map(), edges: [], adj: new Map(), clusters: new Map(), pairs: [], view: { x: 0, y: 0, k: 1 }, fit: true, alpha: 1, hover: null, sel: null, selEdge: null, W: 0, H: 0, pulses: [], ripples: [], labels: {}, busy: false, needs: true };
  const pos = new Map();   // layout survives rebuilds
  let col = {}, font = "12px system-ui", fpx = 12, running = false, last = 0;

  const readColors = () => { col = {}; for (const t of TOKENS) col[t] = color(t); const cs = getComputedStyle(cv); fpx = parseFloat(cs.getPropertyValue("--fs-meta")) || 12; font = fpx + "px " + cs.fontFamily; G.needs = true; };
  const tone = t => col[TONES[t] || "--quiet"];

  // ---- layout ----
  function regroup() {
    const cl = new Map();
    for (const n of G.nodes.values()) { const k = n.cluster || "#" + n.id; let c = cl.get(k); if (!c) { c = { key: k, ns: [], x: 0, y: 0, r: 0, mass: 0 }; cl.set(k, c); } c.ns.push(n); n.c = c; }
    G.clusters = cl;
    for (const c of cl.values()) { c.m = c.ns.filter(n => !n.anchor).length; c.disc = c.ns.length > 1 || c.ns.some(n => n.anchor); }
    const pairs = new Map();
    for (const e of G.edges) {
      const A = G.nodes.get(e.a), B = G.nodes.get(e.b); e.A = A; e.B = B; e.inside = A.c === B.c;
      if (e.tether) { const to = G.nodes.get(e.to) || B; e.H = to.c; e.To = to; e.M = to === A ? B : A; continue; }   // pulled by its own force, below
      if (e.inside || e.hub) continue;   // a hub touches everyone: it must not pull the groups together
      const k = A.c.key < B.c.key ? A.c.key + "\n" + B.c.key : B.c.key + "\n" + A.c.key;
      const p = pairs.get(k) || pairs.set(k, { a: A.c, b: B.c, w: 0 }).get(k); p.w += e.weight;
    }
    G.pairs = [...pairs.values()];
    G.busy = G.edges.filter(e => !e.tether).length > 28;
    plan();
    measure();
  }
  // ---- how much room each group gets: its disc by its size, and the same small gap around every disc and lone node ----
  const GAP = 14, K_MAX = 1.8;
  function plan() {
    const W = G.W || 1100, H = G.H || 640, m = typeof margins === "function" ? margins(W, H) : margins, bw = Math.max(120, W - m.side * 2), bh = Math.max(120, H - m.top - m.bottom), cs = [...G.clusters.values()];
    for (const c of cs) {
      const n = c.m;
      // packed nodes about 30 apart: area per node ~780, so radius ~16 per square root of the count
      c.R = c.disc ? (n ? 10 + 16 * Math.sqrt(n) : 18) : Math.max(c.ns[0].r + 14, Math.min(60, (c.ns[0].label || c.ns[0].id).length * 3.2 + 4));   // a lone node's room is for its label
      c.q = .056 * c.R * c.R / Math.max(1, n);       // the push inside the disc
      c.L = Math.max(40, (c.R / Math.sqrt(Math.max(1, n))) * 1.1);   // the length of a link inside it
    }
    const was = G.terr || new Map(); let moved = was.size !== cs.length;
    G.terr = new Map();
    for (const c of cs) { c.T = c.R + GAP; G.terr.set(c.key, c.T); if (Math.abs((was.get(c.key) || 0) - c.T) > 1) moved = true; }
    const wide = bw / bh;
    if (moved || !G.wide || Math.abs(wide / G.wide - 1) > .1) { G.wide = wide; if (G.planned) G.alpha = Math.max(G.alpha, moved ? .15 : .5); }
    G.planned = true;
  }
  function measure() {
    for (const c of G.clusters.values()) {
      const ms = c.m ? c.ns.filter(n => !n.anchor) : c.ns;
      let x = 0, y = 0; for (const n of ms) { x += n.p.x; y += n.p.y; } x /= ms.length; y /= ms.length;
      let r = 0; for (const n of ms) r = Math.max(r, Math.hypot(n.p.x - x, n.p.y - y) + n.r);
      c.x = x; c.y = y; c.r = c.disc ? Math.max(c.R || 0, r + 12) : r + 6; c.t = Math.max(c.T || 0, c.r);
    }
  }
  function step() {
    const a = G.alpha; if (!a) return;
    measure();
    const cs = [...G.clusters.values()], wide = Math.min(4, Math.max(.35, G.wide || 1.4));
    // territories only push when they touch, so gravity packs them; Fit then scales the pack to the stage
    for (let i = 0; i < cs.length; i++) for (let j = i + 1; j < cs.length; j++) {
      const A = cs[i], B = cs[j]; let dx = A.x - B.x, dy = A.y - B.y, d = Math.hypot(dx, dy);
      if (d < 1) { dx = seeded(A.key) - .5; dy = seeded(B.key) - .5; d = Math.hypot(dx, dy) || 1; }
      const gap = A.t + B.t;
      if (d >= gap) continue;
      const f = Math.min(((gap - d) / d) * .5, 2), ux = dx * f, uy = dy * f, wa = B.ns.length / (A.ns.length + B.ns.length), wb = 1 - wa;
      for (const n of A.ns) { n.p.vx += ux * wa * .5; n.p.vy += uy * wa * .5; }
      for (const n of B.ns) { n.p.vx -= ux * wb * .5; n.p.vy -= uy * wb * .5; }
    }
    // groups that talk to each other sit nearby
    for (const p of G.pairs) {
      const dx = p.b.x - p.a.x, dy = p.b.y - p.a.y, d = Math.hypot(dx, dy) || 1, L = p.a.t + p.b.t;
      if (d <= L) continue;
      const f = ((d - L) / d) * Math.min(.03, .006 + .004 * Math.log2(1 + p.w)) * a;
      for (const n of p.a.ns) { n.p.vx += dx * f; n.p.vy += dy * f; }
      for (const n of p.b.ns) { n.p.vx -= dx * f; n.p.vy -= dy * f; }
    }
    // inside a cluster: pull to the centre, keep apart (an anchor only marks the centre)
    for (const c of cs) {
      const ns = c.ns;
      for (let i = 0; i < ns.length; i++) {
        const A = ns[i], p = A.p; if (A.anchor) continue;
        p.vx += (c.x - p.x) * .1 * a; p.vy += (c.y - p.y) * .1 * a;
        for (let j = i + 1; j < ns.length; j++) {
          if (ns[j].anchor) continue;
          const q = ns[j].p; let dx = p.x - q.x, dy = p.y - q.y, d2 = dx * dx + dy * dy;
          if (d2 < 1) { dx = seeded(A.id) - .5; dy = seeded(ns[j].id) - .5; d2 = 1; }
          const f = c.q * a / d2; p.vx += dx * f; p.vy += dy * f; q.vx -= dx * f; q.vy -= dy * f;
          const min = A.r + ns[j].r + 12;
          if (d2 < min * min) { const d = Math.sqrt(d2), o = ((min - d) / d) * .25; p.vx += dx * o; p.vy += dy * o; q.vx -= dx * o; q.vy -= dy * o; }
        }
      }
    }
    for (const e of G.edges) {
      if (e.tether) {
        // a node with several homes: drawn to each of its discs, by its share there; the disc is drawn a little towards it too
        const c = e.H, n = e.M, p = n.p; if (!c) continue;
        if (n.c === c) {   // its only home: a spring to the thread itself, so the thread sits among its agents
          const q = e.To.p, dx = q.x - p.x, dy = q.y - p.y, d = Math.hypot(dx, dy) || 1, L = Math.max(c.L, n.r + e.To.r + 18), f = ((d - L) / d) * .04 * a;   // long enough that the line shows, whatever the marks' sizes
          if (!e.To.anchor) { p.vx += dx * f; p.vy += dy * f; q.vx -= dx * f; q.vy -= dy * f; }
          continue;
        }
        const dx = c.x - p.x, dy = c.y - p.y, d = Math.hypot(dx, dy) || 1, L = c.r + n.r + 18;
        if (d <= L) continue;
        const f = ((d - L) / d) * .04 * (e.share ?? 1) * a, g = (f * .4) / c.ns.length;
        p.vx += dx * f; p.vy += dy * f;
        for (const m of c.ns) { m.p.vx -= dx * g; m.p.vy -= dy * g; }
        continue;
      }
      if (!e.inside || e.hub) continue;
      const A = e.A.p, B = e.B.p, dx = B.x - A.x, dy = B.y - A.y, d = Math.hypot(dx, dy) || 1, f = ((d - e.A.c.L) / d) * Math.min(.12, .03 + .014 * Math.log2(1 + e.weight)) * a;
      A.vx += dx * f; A.vy += dy * f; B.vx -= dx * f; B.vy -= dy * f;
    }
    for (const n of G.nodes.values()) {
      const p = n.p;
      p.vx -= (p.x * .012 / wide) * a; p.vy -= p.y * .012 * wide * a;   // gravity shaped to the stage
      if (p.fixed) continue;
      p.vx = Math.max(-60, Math.min(60, p.vx * .72)); p.vy = Math.max(-60, Math.min(60, p.vy * .72));
      p.x += p.vx; p.y += p.vy;
    }
    // nodes stay inside their disc; an anchor sits at its members' centre
    for (const c of cs) {
      if (!c.disc) continue;
      const ms = c.m ? c.ns.filter(n => !n.anchor) : null; if (!ms) continue;
      let x = 0, y = 0; for (const n of ms) { x += n.p.x; y += n.p.y; } x /= ms.length; y /= ms.length;
      for (const n of c.ns) {
        if (n.anchor) { n.p.x = x; n.p.y = y; n.p.vx = n.p.vy = 0; continue; }
        if (n.p.fixed) continue;
        const dx = n.p.x - x, dy = n.p.y - y, d = Math.hypot(dx, dy), lim = G.noDiscs ? c.R : c.R - n.r - 8;   // with no disc drawn there's no edge to stay inside, only the group's room
        if (d > lim && d > 0) { const o = Math.max(0, lim) / d; n.p.x = x + dx * o; n.p.y = y + dy * o; }
      }
    }
    const pin = G.pin && G.nodes.get(G.pin);   // soft pin: drawn back to the middle, and it can still be dragged
    if (pin && !pin.p.fixed) { pin.p.x *= .8; pin.p.y *= .8; pin.p.vx = pin.p.vy = 0; }
    G.alpha *= .985; if (G.alpha < .012) G.alpha = 0;
    G.needs = true;
  }
  const LABEL_INSIDE = 44;   // a disc at least this big on screen holds its own name
  const LABEL_UP = 18, LABEL_DOWN = 26;
  const labelHalf = n => (n.label || n.id).length * 3.3 + 4;
  function fitView(sm) {
    if (!G.nodes.size || !G.W) return;
    let x0 = 1e9, x1 = -1e9, y0 = 1e9, y1 = -1e9;
    // labels keep their size on screen whatever the zoom, so their room comes off the stage (LABEL_UP, LABEL_DOWN) rather than out of the layout
    for (const n of G.nodes.values()) { const r = n.r, q = n.big ? Math.max(r, labelHalf(n)) : r; x0 = Math.min(x0, n.p.x - q); x1 = Math.max(x1, n.p.x + q); y0 = Math.min(y0, n.p.y - r); y1 = Math.max(y1, n.p.y + r); }
    for (const c of G.clusters.values()) if (c.disc) { x0 = Math.min(x0, c.x - c.r); x1 = Math.max(x1, c.x + c.r); y0 = Math.min(y0, c.y - c.r); y1 = Math.max(y1, c.y + c.r); }
    const m = typeof margins === "function" ? margins(G.W, G.H) : margins, k = Math.min(K_MAX, (G.W - m.side * 2) / (x1 - x0 || 1), (G.H - m.top - m.bottom - LABEL_UP - LABEL_DOWN) / (y1 - y0 || 1));
    const tx = G.W / 2 - (k * (x0 + x1)) / 2, ty = m.top + LABEL_UP + (G.H - m.top - m.bottom - LABEL_UP - LABEL_DOWN) / 2 - (k * (y0 + y1)) / 2, v = G.view;
    const was = v.k + v.x + v.y;
    v.k += (k - v.k) * sm; v.x += (tx - v.x) * sm; v.y += (ty - v.y) * sm;
    if (Math.abs(was - v.k - v.x - v.y) > .01) G.needs = true;
  }

  // ---- edges ----
  /** Control points of an edge: straight inside a cluster, through the two discs' facing gates between clusters. */
  function curve(e) {
    const A = e.A.p, B = e.B.p;
    if (e.tether || e.inside || e.A.c.ns.length + e.B.c.ns.length <= 2) return null;
    const cA = e.A.c, cB = e.B.c, dx = cB.x - cA.x, dy = cB.y - cA.y, d = Math.hypot(dx, dy) || 1, ux = dx / d, uy = dy / d;
    const near = (c, sign, p) => (c.ns.length > 1 ? { x: c.x + sign * ux * c.r, y: c.y + sign * uy * c.r } : p);
    return { p: A, c1: near(cA, 1, A), c2: near(cB, -1, B), q: B };
  }
  function edgeAlpha(e, on, focus) {
    if (G.selEdge === e) return 1;
    if (e.tether) return focus ? (on || e.to === focus ? .9 : .04) : e.M.live ? .5 : .28;
    if (focus) return on ? .95 : .03;
    let al = Math.max(.14, .75 * Math.exp(-(e.age || 0) / (8 * 36e5)));
    if (e.A.live && e.B.live) al = Math.max(al, .6);
    if (e.faint) al *= .55;
    if (e.hub) al = Math.min(al, G.busy ? .07 : .22);
    else if (G.busy && e.weak) al = Math.min(al, .08);
    else if (G.busy && !e.inside) al = Math.min(al, .4);
    return Math.max(al, e.floor || 0);   // floor: a link that must stay readable however busy the graph is
  }

  // ---- draw ----
  function draw() {
    // world size: 1:1 up to the fit's 1.8x; zoomed out, marks shrink only by the square root, so they stay readable
    const c = ctx, v = G.view, dpr = devicePixelRatio || 1, t = performance.now(), zoomOut = v.k > K_MAX ? K_MAX / v.k : v.k < 1 ? 1 / Math.sqrt(v.k) : 1, grow = (zoomOut - 1) * 10;
    for (const n of G.nodes.values()) n.dr = n.r * zoomOut;
    const px = dpr * (G.z || 1); c.setTransform(px, 0, 0, px, 0, 0); c.clearRect(0, 0, G.W, G.H); c.translate(v.x, v.y); c.scale(v.k, v.k);
    const focus = G.hover || G.sel, near = focus ? G.adj.get(focus) || new Set() : null, dimN = id => focus && id !== focus && !near.has(id);
    const discR = cl => cl.r + Math.max(0, grow);
    // discs behind each group: the thread is the unit you read
    for (const cl of G.clusters.values()) {
      if (!cl.disc || G.noDiscs) continue;
      const dim = focus && !cl.ns.some(n => n.id === focus || near.has(n.id));
      c.globalAlpha = dim ? .25 : 1; c.fillStyle = col["--line"]; c.beginPath(); c.arc(cl.x, cl.y, discR(cl), 0, 6.3);
      c.globalAlpha = (dim ? .15 : .42) * (cl.ns.some(n => n.live) ? 1 : .6); c.fill();
    }
    // tethers: a thin line from a node to each of its homes. With discs it stops at the disc's edge and a node inside its only disc needs
    // none; without them it stops at the edge of the thread's rectangle, or at the group's centre if the thread isn't drawn
    for (const e of G.edges) {
      if (!e.tether || !e.H || (e.M.c === e.H && !G.noDiscs)) continue;
      const cl = e.H, n = e.M, on = focus && (n.id === focus || e.to === focus), al = edgeAlpha(e, on, focus);
      const rect = G.noDiscs && !e.To.anchor, ox = rect ? e.To.p.x : cl.x, oy = rect ? e.To.p.y : cl.y, dx = n.p.x - ox, dy = n.p.y - oy, d = Math.hypot(dx, dy) || 1;
      const R = rect ? Math.min(d, e.To.dr / Math.max(Math.abs(dx) / d, 1e-6), e.To.dr * RECT_H / Math.max(Math.abs(dy) / d, 1e-6)) : G.noDiscs ? 0 : discR(cl), ex = ox + (dx / d) * R, ey = oy + (dy / d) * R;
      c.globalAlpha = al; c.strokeStyle = on ? col["--ink"] : col["--ink-3"]; c.fillStyle = c.strokeStyle; c.lineWidth = 1.2 * zoomOut; c.setLineDash([]);
      c.beginPath(); c.moveTo(n.p.x, n.p.y); c.lineTo(ex, ey); c.stroke();
      c.beginPath(); c.arc(ex, ey, 2.2 * zoomOut, 0, 6.3); c.fill();
    }
    for (const e of G.edges) {
      if (e.tether) continue;
      const on = focus && (e.a === focus || e.b === focus), al = edgeAlpha(e, on, focus);
      c.globalAlpha = al; c.strokeStyle = on || G.selEdge === e ? col["--ink"] : col["--ink-3"];
      c.lineWidth = ((e.weak || e.hub) && !on && !e.floor ? .8 : Math.min(5, .8 + Math.log2(1 + e.weight) * .7)) * zoomOut;
      c.setLineDash(e.dashed ? [5, 4] : []);
      const k = curve(e); c.beginPath(); c.moveTo(e.A.p.x, e.A.p.y);
      if (k) c.bezierCurveTo(k.c1.x, k.c1.y, k.c2.x, k.c2.y, e.B.p.x, e.B.p.y); else c.lineTo(e.B.p.x, e.B.p.y);
      c.stroke();
      if (e.arrowTo && (al > .1 || on)) {
        const to = e.arrowTo === e.a ? e.A : e.B, from = to === e.A ? (k ? k.c1 : e.B.p) : (k ? k.c2 : e.A.p), dx = to.p.x - from.x, dy = to.p.y - from.y, d = Math.hypot(dx, dy) || 1, ux = dx / d, uy = dy / d, tx = to.p.x - ux * (to.dr + 3), ty = to.p.y - uy * (to.dr + 3);
        c.setLineDash([]); c.fillStyle = c.strokeStyle; c.beginPath(); c.moveTo(tx, ty); c.lineTo(tx - ux * 8 - uy * 4, ty - uy * 8 + ux * 4); c.lineTo(tx - ux * 8 + uy * 4, ty - uy * 8 - ux * 4); c.fill();
      }
    }
    c.setLineDash([]);
    G.pulses = G.pulses.filter(p => t - p.t0 < p.dur);
    for (const p of G.pulses) {
      const A = G.nodes.get(p.a), B = G.nodes.get(p.b); if (!A || !B) continue; const f = (t - p.t0) / p.dur;
      c.globalAlpha = 1; c.fillStyle = col["--good"]; c.beginPath(); c.arc(A.p.x + (B.p.x - A.p.x) * f, A.p.y + (B.p.y - A.p.y) * f, 4 * zoomOut, 0, 6.3); c.fill();
    }
    G.ripples = G.ripples.filter(p => t - p.t0 < 900);
    for (const p of G.ripples) {
      const A = G.nodes.get(p.a); if (!A) continue; const f = (t - p.t0) / 900;
      c.globalAlpha = (1 - f) * .7; c.strokeStyle = col["--good"]; c.lineWidth = 1.5; c.beginPath(); c.arc(A.p.x, A.p.y, A.dr + f * 22 * zoomOut, 0, 6.3); c.stroke();
    }
    for (const n of G.nodes.values()) {
      if (n.anchor) continue;
      const p = n.p, dim = dimN(n.id); let op = n.opacity ?? 1; if (dim) op = Math.min(op, .1); else if (n.id === focus) op = 1;
      c.globalAlpha = op;
      if (n.live && !dim) { const w = (t / 1400 + seeded(n.id)) % 1; c.globalAlpha = (1 - w) * .55; c.strokeStyle = col["--good"]; c.lineWidth = 1.5; c.beginPath(); c.arc(p.x, p.y, n.dr + (3 + w * 9) * zoomOut, 0, 6.3); c.stroke(); c.globalAlpha = op; }
      if (n.warden) {
        const hex = new Path2D(hexPath(p.x, p.y, n.dr + 5 * zoomOut)), a = dim ? op : Math.max(op, .85);
        c.fillStyle = col["--warden"]; c.globalAlpha = a * .16; c.fill(hex); c.globalAlpha = a; c.strokeStyle = col["--warden"]; c.lineWidth = 2 * zoomOut; c.stroke(hex); c.globalAlpha = op;
      }
      const fill = tone(n.tone);
      if (n.kind === "thread") {
        const r = n.dr; c.beginPath(); c.roundRect(p.x - r, p.y - r * RECT_H, r * 2, r * 2 * RECT_H, 3); c.fillStyle = fill; c.globalAlpha = op * .28; c.fill(); c.globalAlpha = op; c.strokeStyle = fill; c.lineWidth = 1.5; c.stroke();
      } else {
        const path = new Path2D(shapePath(n.shape, p.x, p.y, n.dr)), lw = 2 * zoomOut;
        if (hollow(n.shape)) { c.fillStyle = col["--surface"]; c.fill(path); c.strokeStyle = fill; c.lineWidth = lw; c.stroke(path); }
        else {
          c.fillStyle = fill; c.fill(path); c.strokeStyle = col["--surface"]; c.lineWidth = lw; c.stroke(path);
          if (n.shape === "half") { c.fillStyle = col["--surface"]; c.beginPath(); c.arc(p.x, p.y, n.dr * .7, -Math.PI / 2, Math.PI / 2); c.fill(); }
        }
      }
      if (G.sel === n.id || n.ring) { c.globalAlpha = 1; c.strokeStyle = col["--ink"]; c.lineWidth = 2 * zoomOut; c.beginPath(); c.arc(p.x, p.y, n.dr + (n.warden ? 9 : 4) * zoomOut, 0, 6.3); c.stroke(); }
    }
    // labels, screen space: groups first, then people most important first; any that would collide is dropped and shows on hover
    c.setTransform(px, 0, 0, px, 0, 0); c.font = font; c.textAlign = "center"; c.textBaseline = "top"; c.lineJoin = "round";
    const used = [], LH = fpx + 2, place = (box) => { if (used.some(u => box[0] < u[2] && box[2] > u[0] && box[1] < u[3] && box[3] > u[1])) return false; used.push(box); return true; };
    for (const n of G.nodes.values()) if (!n.anchor && n.dr * v.k > 3) { const sx = n.p.x * v.k + v.x, sy = n.p.y * v.k + v.y, q = n.dr * v.k; used.push([sx - q, sy - q, sx + q, sy + q]); }
    const text = (tx, sx, sy, strong) => { c.strokeStyle = col["--surface"]; c.lineWidth = 3; c.strokeText(tx, sx, sy); c.fillStyle = strong ? col["--ink"] : col["--ink-2"]; c.fillText(tx, sx, sy); };
    const onStage = (x, w) => (G.fit ? Math.max(w / 2 + 3, Math.min(G.W - w / 2 - 3, x)) : x);
    const rank = n => (n.id === focus ? 1e6 : 0) + (near && near.has(n.id) ? 5e5 : 0) + (n.labelRank || 0);
    if (focus) for (const n of [...G.nodes.values()].filter(n => !n.anchor && (n.id === focus || near.has(n.id))).sort((a, b) => rank(b) - rank(a))) label(n);
    c.font = font;
    for (const cl of [...G.clusters.values()].sort((a, b) => b.ns.length - a.ns.length)) {
      const tx = G.labels[cl.key]; if (!tx || !cl.disc) continue;
      if (focus && !cl.ns.some(n => n.id === focus || near.has(n.id))) continue;
      const R = G.noDiscs ? Math.max(0, ...cl.ns.map(n => Math.hypot(n.p.x - cl.x, n.p.y - cl.y) + (n.dr || 0))) + 6 : discR(cl), w = c.measureText(tx).width, sx = onStage(cl.x * v.k + v.x, w), top = (cl.y - R) * v.k + v.y, fits = sy => place([sx - w / 2 - 2, sy - 1, sx + w / 2 + 2, sy + LH]);
      const sy = [R * v.k >= LABEL_INSIDE ? top + 9 : null, top - LH - 2].find(y => y !== null && fits(y));   // inside the top of a big disc, else just above it
      if (sy === undefined) continue;
      c.globalAlpha = cl.ns.some(n => n.live) ? 1 : .7; text(tx, sx, sy, false);
    }
    if (!focus) for (const n of [...G.nodes.values()].filter(n => n.big && !n.anchor).sort((a, b) => rank(b) - rank(a))) label(n);
    c.globalAlpha = 1;
    function label(n) {
      const tx = n.label || n.id, w = c.measureText(tx).width, sx = onStage(n.p.x * v.k + v.x, w), sy = (n.p.y + n.dr) * v.k + v.y + 3 + (n.kind === "outside" ? 3 : 0);
      if (!place([sx - w / 2 - 2, sy - 1, sx + w / 2 + 2, sy + LH]) && n.id !== focus) return;
      c.globalAlpha = Math.max(.6, n.opacity ?? 1); text(tx, sx, sy, n.labelStrong || n.id === focus);
    }
  }

  function frame(ts) {
    if (!cv.isConnected) { running = false; return; }
    requestAnimationFrame(frame);
    if (!G.W && !document.hidden) { resize(); return; }   // laid out before it had a size: keep asking until it has one
    if (document.hidden || !G.W || ts - last < 33) return;
    last = ts;
    if (G.alpha > 0) step();
    if (G.fit) fitView(.12);
    const animated = G.pulses.length || G.ripples.length || [...G.nodes.values()].some(n => n.live);
    if (animated || G.needs) { draw(); G.needs = false; }
  }
  const wake = () => { if (!running && cv.isConnected) { running = true; requestAnimationFrame(frame); } };

  function resize() {
    const r = cv.getBoundingClientRect(), dpr = devicePixelRatio || 1;
    if (!r.width || !r.height) return;
    // a zoomed root (the TV pages): lay out and draw in layout pixels so labels and nodes scale with the page, on a canvas of real pixels so they stay crisp
    const first = !G.W; G.z = rootZoom();
    G.W = r.width / G.z; G.H = r.height / G.z; cv.width = r.width * dpr; cv.height = r.height * dpr;
    readColors();
    if (G.clusters.size) {
      plan(); measure();
      if (first && G.nodes.size > 8) { G.alpha = 1; for (let i = 0; i < 260; i++) step(); G.alpha = .15; }   // laid out before it had a size: settle again for the real stage
    }
    // setting the canvas size just cleared it, and observers run after this frame's rAF: draw now or the blank canvas gets painted
    if (G.fit) fitView(1); draw(); G.needs = false; wake();
  }
  new ResizeObserver(resize).observe(cv);
  addEventListener("themechange", readColors);
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", readColors);

  // ---- interaction ----
  const mpos = e => { const r = cv.getBoundingClientRect(), z = G.z || 1; return [(e.clientX - r.left) / z, (e.clientY - r.top) / z]; };
  const toWorld = (x, y) => [(x - G.view.x) / G.view.k, (y - G.view.y) / G.view.k];
  function hit(x, y, touch) {
    const [wx, wy] = toWorld(x, y); let best = null, bd = 1e9;
    for (const n of G.nodes.values()) { if (n.anchor) continue; const d = Math.hypot(n.p.x - wx, n.p.y - wy); if (d < Math.max((n.dr || n.r) + 4, (touch ? 22 : 9) / G.view.k) && d < bd) { best = n; bd = d; } }
    return best;
  }
  function hitEdge(x, y) {
    const [wx, wy] = toWorld(x, y), focus = G.hover || G.sel; let best = null, bd = 7 / G.view.k;
    for (const e of G.edges) {
      if (edgeAlpha(e, focus && (e.a === focus || e.b === focus), focus) < .06) continue;   // what you can't see you can't catch
      const k = curve(e), A = e.A.p, B = e.B.p, n = k ? 14 : 1; let px = A.x, py = A.y;
      for (let i = 1; i <= n; i++) {
        const t = i / n, u = 1 - t, qx = k ? u * u * u * A.x + 3 * u * u * t * k.c1.x + 3 * u * t * t * k.c2.x + t * t * t * B.x : B.x, qy = k ? u * u * u * A.y + 3 * u * u * t * k.c1.y + 3 * u * t * t * k.c2.y + t * t * t * B.y : B.y;
        const dx = qx - px, dy = qy - py, l = dx * dx + dy * dy || 1; let f = ((wx - px) * dx + (wy - py) * dy) / l; f = Math.max(0, Math.min(1, f));
        const d = Math.hypot(px + f * dx - wx, py + f * dy - wy); if (d < bd) { bd = d; best = e; }
        px = qx; py = qy;
      }
    }
    return best;
  }
  function zoomAt(x, y, f) { const v = G.view, nk = Math.max(.15, Math.min(5, v.k * f)); v.x = x - ((x - v.x) * nk) / v.k; v.y = y - ((y - v.y) * nk) / v.k; v.k = nk; G.fit = false; G.needs = true; onViewChange && onViewChange(); }
  const touches = new Map(); let drag = null, pinch = 0;
  cv.addEventListener("pointerdown", e => {
    const [x, y] = mpos(e), touch = e.pointerType === "touch"; cv.setPointerCapture(e.pointerId);
    touches.set(e.pointerId, [x, y]);
    if (touches.size === 2) { const [a, b] = [...touches.values()]; pinch = Math.hypot(a[0] - b[0], a[1] - b[1]); if (drag && drag.n) drag.n.p.fixed = false; drag = null; return; }
    const n = hit(x, y, touch);
    drag = n ? { n, x, y, moved: false, touch } : { pan: true, x, y, vx: G.view.x, vy: G.view.y, moved: false, touch };
    if (n) n.p.fixed = true;
  });
  cv.addEventListener("pointermove", e => {
    const [x, y] = mpos(e);
    if (touches.has(e.pointerId)) touches.set(e.pointerId, [x, y]);
    if (touches.size === 2 && pinch) { const [a, b] = [...touches.values()], d = Math.hypot(a[0] - b[0], a[1] - b[1]); if (d > 0) { zoomAt((a[0] + b[0]) / 2, (a[1] + b[1]) / 2, d / pinch); pinch = d; } return; }
    if (drag) {
      if (Math.hypot(x - drag.x, y - drag.y) > (drag.touch ? 8 : 3)) drag.moved = true;
      if (!drag.moved) return;
      if (drag.pan) { G.fit = false; G.view.x = drag.vx + x - drag.x; G.view.y = drag.vy + y - drag.y; onViewChange && onViewChange(); }
      else { G.fit = false; const [wx, wy] = toWorld(x, y); drag.n.p.x = wx; drag.n.p.y = wy; drag.n.p.vx = drag.n.p.vy = 0; G.alpha = Math.max(G.alpha, .25); onViewChange && onViewChange(); }
      G.needs = true; onHover && onHover(null, e); return;
    }
    if (e.pointerType === "touch") return;
    const n = hit(x, y), id = n ? n.id : null;
    if (id !== G.hover) { G.hover = id; G.needs = true; }
    const ed = n ? null : hitEdge(x, y);
    cv.style.cursor = n || ed ? "pointer" : "grab";
    onHover && onHover(n || ed, e);
  });
  cv.addEventListener("pointerleave", e => { if (G.hover) { G.hover = null; G.needs = true; } onHover && onHover(null, e); });
  const up = e => {
    touches.delete(e.pointerId); if (touches.size < 2) pinch = 0;
    if (!drag) return;
    if (drag.n) drag.n.p.fixed = false;
    const moved = drag.moved, touch = drag.touch; drag = null;
    if (moved || e.type === "pointercancel") return;
    const [x, y] = mpos(e), n = hit(x, y, touch);
    if (n) { G.sel = n.id; G.selEdge = null; onSelect && onSelect(n, e); }
    else { G.sel = null; G.selEdge = hitEdge(x, y); onSelect && onSelect(G.selEdge, e); }
    G.needs = true;
  };
  cv.addEventListener("pointerup", up); cv.addEventListener("pointercancel", up);
  if (inPage) cv.addEventListener("touchstart", e => { if (e.touches.length === 1) { const r = cv.getBoundingClientRect(), t = e.touches[0]; if (hit((t.clientX - r.left) / (G.z || 1), (t.clientY - r.top) / (G.z || 1), true)) e.preventDefault(); } }, { passive: false });
  cv.addEventListener("wheel", e => { if (inPage && !e.ctrlKey) return; e.preventDefault(); const [x, y] = mpos(e); zoomAt(x, y, Math.exp(-e.deltaY * .0012)); }, { passive: false });
  cv.addEventListener("dblclick", e => {
    const [x, y] = mpos(e), n = hit(x, y);
    if (n && n.kind !== "thread") { onHover && onHover(null, e); onOpen && onOpen(n); return; }
    api.refit();
  });
  // keyboard: arrows walk the nodes (most important first), Enter opens, Escape clears, + - zoom, 0 refits
  cv.addEventListener("keydown", e => {
    const list = [...G.nodes.values()].filter(n => !n.anchor).sort((a, b) => (b.labelRank || 0) - (a.labelRank || 0) || (a.id < b.id ? -1 : 1));
    const k = e.key, centre = () => zoomAt(G.W / 2, G.H / 2, k === "-" ? 1 / 1.3 : 1.3);
    if ((k === "ArrowRight" || k === "ArrowDown" || k === "ArrowLeft" || k === "ArrowUp") && list.length) {
      e.preventDefault(); const i = list.findIndex(n => n.id === G.sel), d = k === "ArrowRight" || k === "ArrowDown" ? 1 : -1, n = list[i < 0 ? (d > 0 ? 0 : list.length - 1) : (i + d + list.length) % list.length];
      G.sel = n.id; G.selEdge = null; G.needs = true; onSelect && onSelect(n, e);
    } else if (k === "Enter" && G.sel) { e.preventDefault(); onOpen && onOpen(G.nodes.get(G.sel)); }
    else if (k === "+" || k === "=" || k === "-") { e.preventDefault(); centre(); }
    else if (k === "0") { e.preventDefault(); api.refit(); }
    else if (k === "Escape" && (G.sel || G.selEdge)) { G.sel = null; G.selEdge = null; G.needs = true; onSelect && onSelect(null); }
  });

  const api = {
    el: cv,
    /** Off: groups stay together, but no shaded disc is drawn behind them. */
    discs(on) { if (G.noDiscs === !on) return; G.noDiscs = !on; G.needs = true; wake(); },
    get nodes() { return G.nodes; }, get edges() { return G.edges; }, get adj() { return G.adj; }, get view() { return G.view; }, get fit() { return G.fit; },
    /** Replace the graph. Positions survive by id. Returns how many nodes are new. */
    set(nodes, edges) {
      const had = G.nodes, map = new Map(nodes.map(n => [n.id, n])), adj = new Map();
      for (const e of edges) { (adj.get(e.a) || adj.set(e.a, new Set()).get(e.a)).add(e.b); (adj.get(e.b) || adj.set(e.b, new Set()).get(e.b)).add(e.a); }
      let added = 0;
      for (const n of nodes) {
        let p = pos.get(n.id);
        if (!p) {
          const buddy = nodes.find(m => m !== n && m.cluster && m.cluster === n.cluster && pos.get(m.id)), nb = [...(adj.get(n.id) || [])].map(i => pos.get(i)).find(Boolean), at = (buddy && pos.get(buddy.id)) || nb;
          const a = seeded(n.id) * 6.28, r = at ? 26 : 120 + seeded(n.id + "r") * 160;
          p = { x: (at ? at.x : 0) + Math.cos(a) * r, y: (at ? at.y : 0) + Math.sin(a) * r, vx: 0, vy: 0 }; pos.set(n.id, p); added++;
        }
        n.p = p;
      }
      const left = [...had.keys()].some(id => !map.has(id));
      // weak: below the strength most links reach; only matters when the graph is busy
      const sorted = edges.filter(e => !e.tether).map(e => e.weight).sort((a, b) => a - b), cut = sorted[Math.floor(sorted.length * .6)] || 0;
      for (const e of edges) e.weak = e.weight <= cut && e.weight < 4;
      G.nodes = map; G.edges = edges; G.adj = adj; regroup();
      if (G.sel && !map.has(G.sel)) { G.sel = null; onSelect && onSelect(null); }
      if (G.hover && !map.has(G.hover)) { G.hover = null; onHover && onHover(null); }   // a hovered node that left the graph must not keep everything dimmed
      if (G.selEdge) { const s = G.selEdge; G.selEdge = edges.find(e => e.a === s.a && e.b === s.b) || null; }
      if (left) { G.alpha = Math.max(G.alpha, .12); if (map.size <= 1) G.fit = true; }
      if (added) {
        G.alpha = Math.max(G.alpha, added > 8 ? 1 : .15);
        if (added > 8) { for (let i = 0; i < 260; i++) step(); G.alpha = .15; if (G.fit) fitView(1); }
      }
      G.needs = true; wake();
      return added;
    },
    /** Hold this node softly at the middle of the layout. */
    pin(id) { G.pin = id; },
    clusterLabels(map) { G.labels = map; G.needs = true; },
    select(id) { if (!G.nodes.has(id)) return false; G.sel = id; G.selEdge = null; G.needs = true; return true; },
    selectEdge(e) { G.sel = null; G.selEdge = e; G.needs = true; },
    selected() { return G.sel ? G.nodes.get(G.sel) : G.selEdge; },
    clear() { G.sel = null; G.selEdge = null; G.needs = true; },
    refit() { G.fit = true; G.alpha = Math.max(G.alpha, .05); fitView(1); G.needs = true; wake(); onViewChange && onViewChange(); },
    relayout() { pos.clear(); G.nodes = new Map(); G.selEdge = null; G.fit = true; G.terr = null; G.planned = false; },
    nudge(a = 1) { G.alpha = Math.max(G.alpha, a); wake(); },
    pulse(a, b, dur = 1100) { G.pulses.push({ a, b, t0: performance.now(), dur }); wake(); },
    ripple(a) { G.ripples.push({ a, t0: performance.now() }); wake(); },
    zoom(f) { zoomAt(G.W / 2, G.H / 2, f); },
    touch() { G.needs = true; },
    wake,
  };
  return api;
}
