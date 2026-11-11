// Agents › Map: the graph layouts beside the band layout (mesh-layout.js). Same output shape, so mesh.js draws all of them the same way.
//   web   — a force graph with stiff links: each agent is held close to its main thread, a weaker link pulls it toward the other threads it posted in,
//           so threads that share agents end up next to each other. Tiers sit in horizontal slices, Tier 1 at the top.
//   orbit — the same forces, but tiers are rings round a centre: Tier 1 in the middle, Tier 2 round it, then held, then other.
// No randomness: the start comes from the sort order (or from `opt.prev`, the last positions, so a new post nudges rather than reshuffles).
//
// input  same as layout(): data = { threads, agents, now }, opt = { width, override, prev: Map(id -> {x, y}),
//        pin: Map(id -> {x, y}) bodies dragged by hand, held where they were put; height: the panel's, for the slices;
//        free: the page fits the picture to its panel itself, so don't shift it to the top;
//        tile: a mesh too big to read: threads are small rounded tiles, agents bare dots, and the spacing closes up round them so the picture can be scaled up to fill the panel }
// output layout()'s shape, plus spokes carry their own path `d`, clusters carry `linked` (agents from other threads), and orbit adds `rings`.

import { TIERS, TIER_NAME, DENSITY, STATUS_RANK, spokeWidth } from './mesh-layout.js';

export const LAYOUTS = [['bands', 'Bands', 'Threads in rows by tier, agents fanned out to the right'],
                        ['web', 'Web', 'A force graph: agents held close to their threads, threads that share agents pulled together'],
                        ['orbit', 'Orbit', 'Tier 1 in the middle, the other tiers in rings round it'],
                        ['lineage', 'Lineage', 'Threads down the left; agents in columns by hand-off generation, so who started whom reads left to right']];

const DAY = 864e5;
const byStr = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
const GAP = 10;                                    // the least space between two boxes
const TILE = 15;                                   // a thread's side in the `tile` layouts: a little more than an agent's dot

export function graphLayout(data, opt, mode) {
  if (mode === 'lineage') return lineageLayout(data, opt);
  const tile = !!opt.tile;
  if (tile && opt.height) {                        // room for what's there, in the panel's shape: the page scales it up to fill
    const n = data.threads.length + data.agents.length, aspect = opt.width / opt.height, w = Math.round(Math.sqrt(n * 3000 * aspect) * (mode === 'orbit' ? 2 : 1));
    opt = { ...opt, width: Math.max(240, w), height: Math.max(240, w) / aspect };
  }
  const width = opt.width, prev = opt.prev || null, pin = opt.pin && opt.pin.size ? opt.pin : null;
  const compact = data.agents.length > 24;
  const density = compact ? 'compact' : 'normal';
  const D = { ...(compact ? DENSITY.compact : DENSITY.normal), ...(opt.override || {}), lines: 1 };
  const cardW = tile ? TILE : Math.min(compact ? 150 : 176, Math.round((width - 2 * D.pad) * 0.45)), cardH = tile ? TILE : D.cardH;
  const labelW = tile ? 0 : compact ? 112 : 176, maxMain = tile ? 40 : compact ? 6 : 8;
  const spokeLen = tile ? 22 : 54, otherLen = tile ? 44 : 90;

  // members and the older row, as in layout()
  const members = new Map(data.threads.map(t => [t.id, { main: [], ghost: [] }]));
  for (const a of data.agents) {
    if (members.has(a.main)) members.get(a.main).main.push(a);
    for (const [tid, n] of Object.entries(a.threads || {})) if (tid !== a.main && n > 0 && members.has(tid)) members.get(tid).ghost.push({ a, posts: n });
  }
  const agentOrder = (x, y) => (y.running - x.running) || (STATUS_RANK[x.status] - STATUS_RANK[y.status]) ||
    ((y.threads[y.main] || 0) - (x.threads[x.main] || 0)) || byStr(x.id, y.id);
  for (const m of members.values()) m.main.sort(agentOrder);
  const older = [], shown = [];
  for (const t of data.threads) {
    const m = members.get(t.id);
    if (compact && !m.main.some(a => a.running) && data.now - t.last > 3 * DAY) older.push(t);
    else if (m.main.length || m.ghost.length) shown.push(t);
  }
  older.sort((a, b) => (b.posts - a.posts) || byStr(a.id, b.id));
  const bucket = n => Math.floor(n / 10);
  shown.sort((a, b) => (TIERS.indexOf(a.tier) - TIERS.indexOf(b.tier)) || (bucket(b.posts) - bucket(a.posts)) || byStr(a.id, b.id));

  // bodies: a box per thread card, per agent (glyph + label), per "+N more"
  const B = [], tB = new Map(), aB = new Map(), links = [];
  const glyphOff = r => (r + 8 + labelW - 10) / 2;          // box centre minus glyph centre, x
  for (const t of shown) { const b = { kind: 't', id: 'c:' + t.id, t, w: cardW, h: cardH, m: 4 }; B.push(b); tB.set(t.id, b); }
  for (const t of shown) {
    const m = members.get(t.id), vis = m.main.slice(0, m.main.length > maxMain ? maxMain - 1 : maxMain), more = m.main.length - vis.length;
    for (const a of vis) {
      const r = a.running ? D.r : D.r - 2;
      const b = { kind: 'a', id: a.id, a, t, r, w: r + 18 + labelW, h: 18, m: 1 };
      B.push(b); aB.set(a.id, b);
      links.push({ p: b, q: tB.get(t.id), k: 0.5, len: cardW / 2 + spokeLen, main: true, posts: a.threads[t.id] || 0 });
    }
    if (more) {
      const b = { kind: 'm', id: 'more@' + t.id, t, more, r: D.r, w: D.r + 18 + (tile ? 0 : 64), h: 18, m: 1 };
      B.push(b); links.push({ p: b, q: tB.get(t.id), k: 0.5, len: cardW / 2 + spokeLen, main: true, more: true });
    }
  }
  for (const [id, b] of aB) for (const [tid, n] of Object.entries(b.a.threads || {})) {
    if (tid === b.t.id || !(n > 0) || !tB.has(tid)) continue;
    links.push({ p: b, q: tB.get(tid), k: 0.03 + 0.03 * Math.min(3, Math.log2(1 + n)), len: cardW / 2 + otherLen, posts: n });
  }

  // the room: tiers get space by how much they hold
  const area = new Map(TIERS.map(k => [k, 0]));
  for (const b of B) area.set(b.t.tier, area.get(b.t.tier) + (b.w + 2 * GAP) * (b.h + 3 * GAP) * 1.6);
  const total = [...area.values()].reduce((s, v) => s + v, 0) || 1;
  const inner = Math.max(200, width - 2 * D.pad);
  const used = TIERS.filter(k => area.get(k) > 0);
  let target, rings = [], H;
  if (mode === 'orbit') {
    // rings: annulus area by load; squash into an ellipse when wider than the page
    let cum = 0; const R = [];
    for (const k of used) { const r0 = Math.sqrt(cum / Math.PI); cum += area.get(k) * 1.25; R.push({ k, r0, r1: Math.sqrt(cum / Math.PI) }); }
    const rmax = R.length ? R[R.length - 1].r1 : 100, sx = Math.min(tile ? 1.4 : 1, (inner / 2 - 40) / rmax);
    const ry = sx > 1 ? rmax / sx : rmax * Math.max(1, (sx < 1 ? 1 / sx : 1) * 0.55);      // keep the area when squashed in x
    const sy = ry / rmax, cx = width / 2, cy = D.pad + ry + 20;
    H = 2 * ry + 40 + 2 * D.pad;
    rings = R.map(({ k, r0, r1 }, i) => ({ tier: k, name: TIER_NAME[k], cx, cy, rx: (i ? (r0 + r1) / 2 : 0) * sx, ry: (i ? (r0 + r1) / 2 : 0) * sy, ox: r1 * sx, oy: r1 * sy }));
    const ringOf = new Map(rings.map(r => [r.tier, r]));
    target = (b, i, n) => { const g = ringOf.get(b.t.tier); return { g, cx, cy }; };
    for (const [i, b] of B.filter(b => b.kind === 't').entries()) {                // start: evenly round its ring
      const g = ringOf.get(b.t.tier), same = B.filter(c => c.kind === 't' && c.t.tier === b.t.tier), j = same.indexOf(b);
      const ang = -Math.PI / 2 + (j + (TIERS.indexOf(b.t.tier) * 0.37)) * 2 * Math.PI / same.length;
      b.x = cx + Math.cos(ang) * Math.max(g.rx, 1); b.y = cy + Math.sin(ang) * Math.max(g.ry, 1) * (same.length === 1 && !g.rx ? 0 : 1);
    }
  } else {
    // slices: Tier 1 on top
    H = Math.max(360, opt.height || 0, total / inner + 2 * D.pad);          // a free view gets the panel's height to spread into
    let y = D.pad; const slice = new Map();
    for (const k of used) { const h = (area.get(k) / total) * (H - 2 * D.pad); slice.set(k, { y0: y, y1: y + h }); y += h; }
    rings = [];
    target = b => ({ s: slice.get(b.t.tier) });
    for (const k of used) {
      const list = B.filter(b => b.kind === 't' && b.t.tier === k), s = slice.get(k);
      const cols = Math.max(1, Math.min(list.length, Math.floor(inner / (cardW + (tile ? 60 : 170))))), rows = Math.ceil(list.length / cols);
      list.forEach((b, j) => { b.x = D.pad + (j % cols + 0.5) * inner / cols; b.y = s.y0 + (Math.floor(j / cols) + 0.5) * (s.y1 - s.y0) / rows; });
    }
  }
  // agents start round their main thread (golden angle), or where they were
  const kids = new Map();
  for (const l of links) if (l.main) { const n = kids.get(l.q) || 0; kids.set(l.q, n + 1); const ang = n * 2.39996 + 0.3; l.p.x = l.q.x + Math.cos(ang) * l.len; l.p.y = l.q.y + Math.sin(ang) * l.len * 0.6; }
  let warm = 0;
  if (prev) for (const b of B) { const p = prev.get(b.id); if (p) { b.x = p.x; b.y = p.y; warm++; } }
  const warmStart = warm > B.length * 0.6;
  if (pin) for (const b of B) { const p = pin.get(b.id); if (p) { b.pin = p; b.x = p.x; b.y = p.y; } }   // dragged by hand: stays put, the rest settle round it

  // the simulation
  const N = B.length, iters = warmStart ? 90 : 360;
  for (const b of B) { b.vx = 0; b.vy = 0; }
  const clampB = b => {
    if (b.pin) { b.x = b.pin.x; b.y = b.pin.y; return; }
    b.x = Math.max(D.pad + b.w / 2, Math.min(width - D.pad - b.w / 2, b.x));
    b.y = Math.max(D.pad + b.h / 2, b.y);
  };
  for (let it = 0; it < iters; it++) {
    const temp = (warmStart ? 8 : 40) * (1 - it / iters) + 1;
    for (const b of B) { b.fx = 0; b.fy = 0; }
    // repulsion
    for (let i = 0; i < N; i++) for (let j = i + 1; j < N; j++) {
      const p = B[i], q = B[j];
      let dx = q.x - p.x, dy = q.y - p.y; if (!dx && !dy) { dx = (i - j) * 0.1; dy = 0.1; }
      const d2 = dx * dx + dy * dy; if (d2 > 160000) continue;
      const c = (p.kind === 't' && q.kind === 't' ? 9000 : p.kind === 't' || q.kind === 't' ? 2600 : 900) * (tile ? 0.3 : 1) / Math.max(d2, 100);
      const d = Math.sqrt(d2), fx = dx / d * c, fy = dy / d * c;
      p.fx -= fx / p.m; p.fy -= fy / p.m; q.fx += fx / q.m; q.fy += fy / q.m;
    }
    // springs: stiff to the main thread, soft to the others
    for (const l of links) {
      const dx = l.q.x - l.p.x, dy = l.q.y - l.p.y, d = Math.hypot(dx, dy) || 1, f = l.k * (d - l.len);
      l.p.fx += dx / d * f / l.p.m; l.p.fy += dy / d * f / l.p.m;
      l.q.fx -= dx / d * f / l.q.m * (l.main ? 0.2 : 1); l.q.fy -= dy / d * f / l.q.m * (l.main ? 0.2 : 1);
    }
    // where each tier lives
    for (const b of B) if (b.kind === 't') {
      const g = target(b);
      if (g.s) { const mid = (g.s.y0 + g.s.y1) / 2, half = (g.s.y1 - g.s.y0) / 2; const off = b.y - mid; if (Math.abs(off) > half * 0.5) b.fy -= 0.06 * (off - Math.sign(off) * half * 0.5); b.fx += 0.002 * (width / 2 - b.x); }
      else {
        const dx = b.x - g.cx, dy = b.y - g.cy, e = Math.hypot(dx / Math.max(g.g.ox, 1), dy / Math.max(g.g.oy, 1)) || 1e-6;
        const want = g.g.rx ? Math.hypot(g.g.rx, g.g.ry) / Math.hypot(g.g.ox, g.g.oy) : 0;
        const k = 0.05 * (e - want); b.fx -= dx / e * k / Math.max(g.g.ox, 1) * 60; b.fy -= dy / e * k / Math.max(g.g.oy, 1) * 60;
      }
    }
    for (const b of B) {
      b.vx = (b.vx + b.fx) * 0.55; b.vy = (b.vy + b.fy) * 0.55;
      const v = Math.hypot(b.vx, b.vy); if (v > temp) { b.vx *= temp / v; b.vy *= temp / v; }
      b.x += b.vx; b.y += b.vy; clampB(b);
    }
    if (it > iters * 0.4) collide(B, 1);
  }
  for (let k = 0; k < 6; k++) { collide(B, 12); for (const b of B) clampB(b); }
  collide(B, 400, true);                             // whatever the width clamp squeezed back, settle downwards (there is always room below)
  if (pin) for (const b of B) if (b.pin) { b.x = b.pin.x; b.y = b.pin.y; }

  // fit: top at pad
  const top = opt.free ? D.pad : Math.min(...B.map(b => b.y - b.h / 2), ...rings.map(r => r.cy - r.oy - 20)), dy0 = B.length ? D.pad - top : 0;
  for (const b of B) b.y += dy0;
  if (rings.length) for (const r of rings) r.cy += dy0;

  // output
  const maxCost = Math.max(0.01, ...shown.map(t => t.cost));
  const clusters = [], nodes = [], spokes = [], byAgent = new Map(), byThread = new Map(), pos = new Map();
  for (const b of B) pos.set(b.id, { x: b.x, y: b.y });
  for (const b of B) if (b.kind === 't') {
    const card = { x: Math.round(b.x - b.w / 2), y: Math.round(b.y - b.h / 2), w: b.w, h: b.h };
    const cl = { thread: b.t, card, port: { x: card.x + card.w, y: Math.round(b.y) }, labelW, costShare: b.t.cost / maxCost, more: 0, nodeIds: [], linked: [] };
    clusters.push(cl); byThread.set(b.t.id, cl);
  }
  const nodeOf = new Map();
  for (const b of B) if (b.kind !== 't') {
    const x = Math.round(b.x - glyphOff(b.r)), y = Math.round(b.y), labelX = x + b.r + 8, cl = byThread.get(b.t.id);
    const node = b.kind === 'a'
      ? { id: b.a.id, agent: b.a, thread: b.t.id, ghost: false, x, y, r: b.r, labelX, labelW, posts: b.a.threads[b.t.id] || 0 }
      : { id: b.id, more: b.more, thread: b.t.id, x, y, r: b.r, labelX, labelW: 64 };
    nodes.push(node); nodeOf.set(b, node); cl.nodeIds.push(node.id); if (b.more) cl.more = b.more;
    if (b.kind === 'a') byAgent.set(b.a.id, node);
  }
  for (const l of links) {
    const n = nodeOf.get(l.p), cl = byThread.get(l.q.t.id), c = cl.card;
    const from = edgePoint(c, n.x, n.y), dx = n.x - from.x, dy = n.y - from.y, d = Math.hypot(dx, dy) || 1;
    const to = { x: n.x - dx / d * (n.r + 1), y: n.y - dy / d * (n.r + 1) };
    if (!l.main && n.agent) cl.linked.push(n.id);
    spokes.push({ from, to, node: n.id, thread: l.q.t.id, d: curve(c, n),
                  width: l.more ? 1 : l.main ? spokeWidth(l.posts) : 1, dashed: !l.main || !!l.more, ended: n.agent ? !n.agent.running : false });
  }

  // older threads under everything
  let y = Math.max(...B.map(b => b.y + b.h / 2), D.pad) + D.pad;
  if (mode === 'orbit' && rings.length) y = Math.max(y, ...rings.map(r => r.cy + r.oy + D.pad));
  const olderRow = { x: D.pad, y, chips: [] };
  if (older.length) {
    y += D.bandH; let cx = D.pad;
    for (const t of older) {
      const w = tile ? 12 : Math.round(t.id.length * 7.2 + String(t.posts).length * 7 + 34);
      if (cx + w > width - D.pad) { cx = D.pad; y += tile ? 18 : 32; }
      olderRow.chips.push({ thread: t, x: cx, y, w, h: tile ? 12 : 24 }); cx += w + 8;
    }
    y += (tile ? 12 : 24) + D.pad;
  }
  return { mode, tile, density, D, cols: 2, colW: inner, cardW, width, height: Math.max(y, 360), bands: [], rings, clusters, nodes, spokes,
           older: olderRow, olderCount: older.length, byAgent, byThread, threadById: new Map(data.threads.map(t => [t.id, t])), pos };
}

// push overlapping boxes apart along the shallower axis; agents give way to cards
function collide(B, passes) {
  for (let p = 0; p < passes; p++) {
    let moved = 0;
    for (let i = 0; i < B.length; i++) for (let j = i + 1; j < B.length; j++) {
      const a = B[i], b = B[j];
      const ox = (a.w + b.w) / 2 + GAP - Math.abs(a.x - b.x); if (ox <= 0) continue;
      const oy = (a.h + b.h) / 2 + GAP - Math.abs(a.y - b.y); if (oy <= 0) continue;
      const wa = b.m / (a.m + b.m), wb = a.m / (a.m + b.m);
      if (ox < oy * 1.6) { const s = a.x < b.x || (a.x === b.x && i < j) ? -1 : 1; a.x += s * ox * wa; b.x -= s * ox * wb; }
      else { const s = a.y < b.y || (a.y === b.y && i < j) ? -1 : 1; a.y += s * oy * wa; b.y -= s * oy * wb; }
      moved++;
    }
    if (!moved) break;
  }
}

// top to bottom, put each box just below any box already placed that it overlaps: no overlaps left, in one sweep
function settle(B) {
  const placed = [];
  for (const b of [...B].sort((p, q) => p.y - q.y)) {
    for (let hit = true; hit;) {
      hit = false;
      for (const p of placed) {
        if ((p.w + b.w) / 2 + GAP <= Math.abs(p.x - b.x) || (p.h + b.h) / 2 + GAP <= Math.abs(p.y - b.y)) continue;
        b.y = p.y + (p.h + b.h) / 2 + GAP; hit = true;
      }
    }
    placed.push(b);
  }
}

// where the line from the card's centre to (x, y) leaves the card
// a spoke in the free layouts: the same S-curve as Lineage and Bands. It leaves the card from the side facing the agent and arrives level with
// the dot; an agent mostly above or below the card gets the same curve turned upright (feedback 11100245a336: all connections curved alike)
function curve(c, n) {
  const cx = c.x + c.w / 2, cy = c.y + c.h / 2, f = v => v.toFixed(1);
  if (Math.abs(n.x - cx) > c.w / 2 + n.r) {
    const s = n.x > cx ? 1 : -1, x1 = s > 0 ? c.x + c.w : c.x, x2 = n.x - s * (n.r + 1), m = s * Math.max(16, Math.abs(x2 - x1) * 0.5);
    return `M${f(x1)} ${f(cy)}C${f(x1 + m)} ${f(cy)} ${f(x2 - m)} ${f(n.y)} ${f(x2)} ${f(n.y)}`;
  }
  const s = n.y > cy ? 1 : -1, y1 = s > 0 ? c.y + c.h : c.y, y2 = n.y - s * (n.r + 1), m = s * Math.max(12, Math.abs(y2 - y1) * 0.5);
  return `M${f(n.x > cx ? Math.min(n.x, c.x + c.w) : Math.max(n.x, c.x))} ${f(y1)}C${f(n.x)} ${f(y1 + m)} ${f(n.x)} ${f(y2 - m)} ${f(n.x)} ${f(y2)}`;
}
function edgePoint(c, x, y) {
  const cx = c.x + c.w / 2, cy = c.y + c.h / 2, dx = x - cx, dy = y - cy;
  if (!dx && !dy) return { x: cx, y: cy };
  const s = Math.min(Math.abs(dx) > 0 ? (c.w / 2) / Math.abs(dx) : Infinity, Math.abs(dy) > 0 ? (c.h / 2) / Math.abs(dy) : Infinity);
  return { x: cx + dx * s, y: cy + dy * s };
}


// Lineage: threads are a fixed column of cards (dearest first); each agent sits in the column of its hand-off generation (0 = started by a
// person, 1 = by a gen-0 agent, ...), level with its main thread. No randomness: the position follows from the data, so nothing shuffles.
// Extra output: handoffs [{from, to, d, chain}] - the arrows between a running parent and its running child; `chain` = same main thread, so
// the child's own spoke is left out (the arrow already says it).
function lineageLayout(data, opt) {
  const tile = !!opt.tile, width = opt.width, pin = opt.pin && opt.pin.size ? opt.pin : null;
  const compact = data.agents.length > 28;
  const D = { ...(compact ? DENSITY.compact : DENSITY.normal), ...(opt.override || {}), lines: 1 };
  const pad = tile ? 10 : D.pad, byId = new Map(data.agents.map(a => [a.id, a]));
  // an agent started by an agent that has ended (so it isn't in the data) is still a generation below it, never "Started by you" (feedback 11100257bb2d)
  const gen = a => { let g = 0, x = a; while (x && x.from && (byId.has(x.from) || /^a[0-9a-f]{4,}$/.test(x.from)) && g < 8) { x = byId.get(x.from); g++; } return g; };
  const G = new Map(data.agents.map(a => [a.id, gen(a)]));
  const maxG = Math.max(0, ...G.values());
  const mine = new Map(data.threads.map(t => [t.id, []]));
  for (const a of data.agents) if (mine.has(a.main)) mine.get(a.main).push(a);
  const used = new Set(); for (const a of data.agents) for (const k of Object.keys(a.threads || {})) used.add(k);
  const ck = t => Math.floor(Math.log2(t.cost + 1) * 2);
  const shown = data.threads.filter(t => used.has(t.id) || mine.get(t.id).length).sort((a, b) => (ck(b) - ck(a)) || byStr(a.id, b.id));
  const cardW = tile ? TILE : Math.min(compact ? (width < 760 ? 172 : 150) : 176, Math.round((width - 2 * pad) * (width < 760 ? 0.5 : 0.4))), cardH = tile ? TILE : D.cardH, labelW = tile ? 0 : compact ? 112 : 204;
  const run = tile ? 36 : 110, tail = tile ? 22 : 70, genW = tile ? 40 : 150, step = tile ? 22 : 56, rowMin = tile ? 26 : 64;
  // lineages side by side: the threads are split into B blocks left to right (each block is the card column plus its generation columns), B chosen
  // so the whole picture fits the panel's shape; at B = 1 it is the old single column
  const rowOf = t => { const cnt = new Map(); for (const a of mine.get(t.id)) cnt.set(G.get(a.id), (cnt.get(G.get(a.id)) || 0) + 1); return Math.max(rowMin, Math.max(1, ...cnt.values()) * step); };
  const rows = shown.map(rowOf), total = rows.reduce((p, q) => p + q, 0), H = opt.height || 0;
  const natW = cardW + run + maxG * genW + labelW + tail;
  let B = 1, fitK = 1;
  if (H && shown.length > 1) {
    let best = 0;
    for (let b = 1; b <= Math.min(tile ? 16 : 6, shown.length); b++) {
      const k = Math.min(tile ? 1.4 : 1.25, (width - 2 * pad) / (b * natW), (H - 64) / (total / b + 2 * pad));
      // a block too narrow for its generation columns would spill into the next block's cards. Measured at the scale it is drawn at: a tall mesh is
      // shrunk to fit the height anyway, so its blocks have that much more room, down to 0.7 (below that the cards are unreadable and tiles do better).
      // Round 18: 14 threads drew one column at 0.6 on a 1707 screen, the right half of the panel blank
      if (!tile && b > 1 && ((width - 2 * pad) / (b * Math.min(1, Math.max(0.7, k))) - cardW - run - labelW - tail) / Math.max(1, maxG) < 90) break;
      if (k > best * 1.05) { best = k; B = b; fitK = k; }
    }
  }
  // tiles take no more room than they need (what's left of the panel's width at the scale that fits goes to the generation columns, within reason); cards take the panel's width
  const blockW = tile ? Math.max(natW, (width - 2 * pad) / (fitK * B)) : (width - 2 * pad) / (B * Math.min(1, fitK));
  const cw = tile ? Math.min(90, (blockW - cardW - run - tail) / Math.max(1, maxG)) : Math.max(90, Math.min(220, (blockW - cardW - 110 - labelW - 70) / Math.max(1, maxG)));
  const blockOf = new Map(), target = total / B; { let acc = 0, bi = 0; shown.forEach((t, i) => { if (bi < B - 1 && acc >= target * (bi + 1) - rows[i] / 2) bi++; blockOf.set(t.id, bi); acc += rows[i]; }); }
  const colX = (g, bi) => pad + bi * blockW + cardW + run + g * cw;
  const clusters = [], nodes = [], spokes = [], byAgent = new Map(), byThread = new Map(), pos = new Map(), yOf = new Map();
  let y = pad, yEnd = pad, curB = 0;
  shown.forEach((t, i) => {
    const bi = blockOf.get(t.id); if (bi !== curB) { curB = bi; y = pad; }
    const rowH = rows[i], cy = y + rowH / 2; y += rowH; yEnd = Math.max(yEnd, y); yOf.set(t.id, cy);
  });
  const maxCost = Math.max(0.01, ...shown.map(t => t.cost));
  for (const t of shown) {
    const cy = yOf.get(t.id), card = { x: pad + blockOf.get(t.id) * blockW, y: Math.round(cy - cardH / 2), w: cardW, h: cardH };
    const cl = { thread: t, card, port: { x: card.x + card.w, y: Math.round(cy) }, labelW, costShare: t.cost / maxCost, more: 0, nodeIds: [], linked: [] };
    clusters.push(cl); byThread.set(t.id, cl); pos.set('c:' + t.id, { x: card.x + cardW / 2, y: cy });
  }
  // agents: per thread and generation, stacked about the thread's level; an agent that also posted elsewhere is pulled a little toward those threads
  const slots = new Map();
  for (const t of shown) {
    const lists = new Map(); for (const a of [...mine.get(t.id)].sort((p, q) => (q.running - p.running) || ((q.threads[t.id] || 0) - (p.threads[t.id] || 0)) || byStr(p.id, q.id))) { const g = G.get(a.id); (lists.get(g) || lists.set(g, []).get(g)).push(a); }
    for (const [g, l] of lists) l.forEach((a, i) => {
      let wy = yOf.get(t.id), ws = 1;
      for (const [k, n] of Object.entries(a.threads || {})) if (k !== t.id && yOf.has(k)) { const w = 0.15 * Math.min(1, n / 8); wy += yOf.get(k) * w; ws += w; }
      slots.set(a.id, { x: colX(g, blockOf.get(t.id)), y: wy / ws + (i - (l.length - 1) / 2) * step });
    });
  }
  // agents of different threads can be pulled to the same spot in a column (the pull toward their other threads): space each column out, top down,
  // so no two dots or labels sit on each other (feedback 1110024612c1)
  { const gap = tile ? 18 : 42, cols = new Map();
    for (const [id, p] of slots) (cols.get(p.x) || cols.set(p.x, []).get(p.x)).push(p);
    for (const l of cols.values()) { l.sort((p, q) => p.y - q.y); for (let i = 1; i < l.length; i++) if (l[i].y - l[i - 1].y < gap) l[i].y = l[i - 1].y + gap; } }
  const rOf = a => (a.running ? D.r : D.r - 2);
  for (const t of shown) for (const a of mine.get(t.id)) {
    const s = slots.get(a.id), p = pin && pin.get(a.id), r = rOf(a), px = p ? p.x : s.x, py = p ? p.y : s.y;
    const node = { id: a.id, agent: a, thread: t.id, ghost: false, x: Math.round(px), y: Math.round(py), r, labelX: Math.round(px) + r + 8, labelW, posts: a.threads[t.id] || 0 };
    nodes.push(node); byAgent.set(a.id, node); byThread.get(t.id).nodeIds.push(a.id); pos.set(a.id, { x: px, y: py });
  }
  // the same horizontal S-curve in either direction: leaves x1 heading toward x2 and arrives level (a right-to-left run bends the mirror way)
  const bez = (x1, y1, x2, y2) => { const s = x2 < x1 ? -1 : 1, m = s * Math.max(20, Math.abs(x2 - x1) * 0.5); return `M${x1.toFixed(1)} ${y1.toFixed(1)}C${(x1 + m).toFixed(1)} ${y1.toFixed(1)} ${(x2 - m).toFixed(1)} ${y2.toFixed(1)} ${x2.toFixed(1)} ${y2.toFixed(1)}`; };
  const handoffs = [], chained = new Set();
  for (const n of nodes) {
    const a = n.agent, par = a.from && a.from !== 'person' ? byAgent.get(a.from) : null;
    if (!par || !a.running || !par.agent.running) continue;
    const chain = par.agent.main === a.main; if (chain) chained.add(a.id);
    // leave from the end of the parent's label, so the arrows don't run through its name (from the dot only when the columns are too close)
    const x2 = n.x - n.r - 7, past = par.labelX + par.labelW + 4, x1 = !tile && x2 - past >= 24 ? past : par.x + par.r + 3;
    handoffs.push({ from: par.id, to: a.id, chain, d: bez(x1, par.y, x2, n.y) });
  }
  for (const n of nodes) for (const [tid, c] of Object.entries(n.agent.threads || {})) {
    const cl = byThread.get(tid); if (!cl) continue;
    const main = tid === n.thread; if (main && chained.has(n.id)) continue;
    if (!main && !(c > 0)) continue;
    if (!main) cl.linked.push(n.id);
    // a card to the agent's right (a later block) is joined from its left edge to the end of the agent's label, so the curve doesn't cut back across either
    const back = cl.card.x > n.x, lx = n.labelX + n.labelW + 4;
    const d = !back ? bez(cl.card.x + cl.card.w, cl.port.y, n.x - n.r - 3, n.y) : bez(cl.card.x, cl.port.y, !tile && cl.card.x - lx >= 24 ? lx : n.x + n.r + 3, n.y);
    spokes.push({ node: n.id, thread: tid, d, width: main ? spokeWidth(c) : 1, dashed: !main, ended: !n.agent.running });
  }
  // column heads: what each generation column means (the layout's title says it only on hover)
  const GEN = ['Started by you', 'Started by an agent', 'Started by those'];
  const heads = [];
  const colUsed = new Set([...slots.values()].map(p => p.x));   // a column nobody is in gets no heading
  if (!tile) for (let bi = 0; bi < B; bi++) for (let g = 0; g <= maxG; g++) if (colUsed.has(colX(g, bi))) heads.push({ x: Math.round(colX(g, bi)), y: Math.max(12, pad - 8), text: GEN[g] || 'Generation ' + (g + 1) });
  const height = Math.max(yEnd + pad, ...nodes.map(n => n.y + step / 2 + pad), 360);
  return { mode: 'lineage', tile, heads, density: compact ? 'compact' : 'normal', D, cols: 2, colW: width - 2 * pad, cardW, width, height, bands: [], rings: [], clusters, nodes, spokes, handoffs,
           older: { x: pad, y: height, chips: [] }, olderCount: 0, byAgent, byThread, threadById: new Map(data.threads.map(t => [t.id, t])), pos };
}
