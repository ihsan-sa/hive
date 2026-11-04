// Agents › Map layout, from Claude Design's handoff (~/designs/mesh-graph/GRAPH-HANDOFF.md). Labels are cut by CSS ellipsis, not here.
// Agents › Map: deterministic graph layout. Plain JS, no dependencies, no randomness, no physics.
// Same input -> same x,y for every node. One new post never moves anything unless it changes an order key.
//
// input  data = { threads:[{id, tier, posts, cost, last, outside:{you,box,hives}}],
//                 agents:[{id, status, running, main, threads:{<threadId>:posts}, posts, cost, model, now, from, warden}],
//                 now }                                           // `last` and `now` are epoch ms
// input  opt  = { width, window }                                 // width of the graph host in px; window '1h'|'6h'|'24h'|'3d'|'all'
// output { density, cols, colW, width, height, bands:[], clusters:[], nodes:[], spokes:[], older:[], byAgent, byThread }

export const TIERS = ['tier1', 'tier2', 'held', 'other'];
export const TIER_NAME = { tier1: 'Tier 1', tier2: 'Tier 2', held: 'Held', other: 'Other' };

export const DENSITY = {
  //            card size      spoke run  node step  node r    label lines  gaps
  normal:  { cardW: 208, cardH: 60, run: 56, step: 40, r: 6, lines: 2, rowGap: 24, bandH: 28, bandGap: 8, pad: 24, minColW: 480, maxNodes: 7 },
  compact: { cardW: 160, cardH: 46, run: 32, step: 22, r: 5, lines: 1, rowGap: 20, bandH: 26, bandGap: 12, pad: 24, minColW: 320, maxNodes: 6 },
};

// lower = drawn first in a stack. Problems sit at the top of their thread's fan.
export const STATUS_RANK = { stuck: 0, failed: 1, limited: 2, working: 3, queue: 4, queued: 5, quiet: 6, killed: 7, ended: 8 };

const DAY = 864e5;
const byStr = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// line width from posts by that agent in that thread: 4 buckets, never continuous (stable when one post arrives)
export function spokeWidth(posts) {
  return posts >= 60 ? 3.5 : posts >= 20 ? 2.5 : posts >= 5 ? 1.5 : 1;
}

export function layout(data, opt) {
  const width = opt.width;
  const threadById = new Map(data.threads.map(t => [t.id, t]));

  // 1. density: compact when more than 24 agents would be drawn, or the host is narrow and busy
  const base = data.agents.length > 24 ? DENSITY.compact : DENSITY.normal;
  const density = base === DENSITY.compact ? 'compact' : 'normal';
  const D = { ...base, ...(opt.override || {}) };          // the phone passes a smaller pad and run

  // 2. columns: as many as fit at minColW, 1..3
  const inner = width - 2 * D.pad;
  const cols = Math.max(1, Math.min(3, Math.floor(inner / D.minColW)));
  const colW = Math.floor(inner / cols);
  const cardW = cols === 1 ? Math.min(D.cardW, Math.round(colW * 0.42)) : D.cardW;

  // 3. members of each thread: its main agents, then "ghosts" (agents whose main thread is elsewhere)
  const members = new Map(data.threads.map(t => [t.id, { main: [], ghost: [] }]));
  for (const a of data.agents) {
    if (members.has(a.main)) members.get(a.main).main.push(a);
    for (const [tid, n] of Object.entries(a.threads || {})) {
      if (tid !== a.main && n > 0 && members.has(tid)) members.get(tid).ghost.push({ a, posts: n });
    }
  }
  const agentOrder = (x, y) =>
    (y.running - x.running) || (STATUS_RANK[x.status] - STATUS_RANK[y.status]) ||
    ((y.threads[y.main] || 0) - (x.threads[x.main] || 0)) || byStr(x.id, y.id);
  for (const m of members.values()) {
    m.main.sort(agentOrder);
    m.ghost.sort((x, y) => (y.posts - x.posts) || byStr(x.a.id, y.a.id));
  }

  // 4. collapse: in a busy view, a thread with nobody running and no post for 3 days goes to the "older" row
  const older = [], shown = [];
  for (const t of data.threads) {
    const m = members.get(t.id);
    const anyRunning = m.main.some(a => a.running);
    const stale = data.now - t.last > 3 * DAY;
    if (density === 'compact' && !anyRunning && stale) older.push(t);
    else if (m.main.length || m.ghost.length) shown.push(t);
  }
  older.sort((a, b) => (b.posts - a.posts) || byStr(a.id, b.id));

  // 5. order: tier band, then posts in window (bucketed to tens so one post does not reorder), then name
  const bucket = n => Math.floor(n / 10);
  shown.sort((a, b) =>
    (TIERS.indexOf(a.tier) - TIERS.indexOf(b.tier)) || (bucket(b.posts) - bucket(a.posts)) || byStr(a.id, b.id));

  // 6. place: band label, then rows of `cols` clusters; cards in a row share one centre y
  const maxCost = Math.max(0.01, ...shown.map(t => t.cost));
  const bands = [], clusters = [], nodes = [], spokes = [];
  const byAgent = new Map(), byThread = new Map();
  let y = D.pad;
  for (const tier of TIERS) {
    const list = shown.filter(t => t.tier === tier);
    if (!list.length) continue;
    bands.push({ tier, name: TIER_NAME[tier], count: list.length, x: D.pad, y });
    y += D.bandH;
    for (let i = 0; i < list.length; i += cols) {
      const row = list.slice(i, i + cols).map(t => {
        const m = members.get(t.id);
        let vis = [...m.main.map(a => ({ a, ghost: false, posts: a.threads[t.id] || 0 })),
                   ...m.ghost.map(g => ({ a: g.a, ghost: true, posts: g.posts }))];
        let more = 0;
        if (vis.length > D.maxNodes) { more = vis.length - (D.maxNodes - 1); vis = vis.slice(0, D.maxNodes - 1); }
        const n = vis.length + (more ? 1 : 0);
        return { t, vis, more, n, h: Math.max(D.cardH, n * D.step) };
      });
      const rowH = Math.max(...row.map(c => c.h));
      const cy = y + rowH / 2;
      row.forEach((c, k) => {
        const x = D.pad + k * colW;
        const card = { x, y: Math.round(cy - D.cardH / 2), w: cardW, h: D.cardH };
        const port = { x: x + cardW, y: Math.round(cy) };
        const nx = x + cardW + D.run + D.r;
        const labelX = nx + D.r + 8;
        const labelW = x + colW - 16 - labelX;
        const cl = { thread: c.t, card, port, labelW, costShare: c.t.cost / maxCost, more: c.more, nodeIds: [] };
        clusters.push(cl); byThread.set(c.t.id, cl);
        const place = (j) => Math.round(cy + (j - (c.n - 1) / 2) * D.step);
        c.vis.forEach((v, j) => {
          const node = { id: v.a.id + (v.ghost ? '@' + c.t.id : ''), agent: v.a, thread: c.t.id, ghost: v.ghost,
                         x: nx, y: place(j), r: v.a.running ? D.r : D.r - 2, labelX, labelW, posts: v.posts };
          nodes.push(node); cl.nodeIds.push(node.id);
          if (!v.ghost) byAgent.set(v.a.id, node);
          spokes.push({ from: port, to: { x: nx - node.r, y: node.y }, node: node.id, thread: c.t.id,
                        width: v.ghost ? 1 : spokeWidth(v.posts), dashed: v.ghost, ended: !v.a.running });
        });
        if (c.more) {
          const node = { id: 'more@' + c.t.id, more: c.more, thread: c.t.id, x: nx, y: place(c.vis.length), r: D.r, labelX, labelW };
          nodes.push(node); cl.nodeIds.push(node.id);
          spokes.push({ from: port, to: { x: nx - D.r, y: node.y }, node: node.id, thread: c.t.id, width: 1, dashed: true });
        }
      });
      y += rowH + D.rowGap;
    }
    y += D.bandGap;
  }

  // 7. older threads: one wrapping row of chips at the bottom
  const olderRow = { x: D.pad, y, chips: [] };
  if (older.length) {
    y += D.bandH;
    let cx = D.pad;
    for (const t of older) {
      const w = Math.round(t.id.length * 7.2 + String(t.posts).length * 7 + 34);
      if (cx + w > width - D.pad) { cx = D.pad; y += 32; }
      olderRow.chips.push({ thread: t, x: cx, y, w, h: 24 });
      cx += w + 8;
    }
    y += 24 + D.pad;
  }

  return { density, D, cols, colW, cardW, width, height: Math.max(y, 360), bands, clusters, nodes, spokes,
           older: olderRow, olderCount: older.length, byAgent, byThread, threadById };
}

// A cubic from the card's right-middle port to the node's left edge. Control points at half the run: a tidy fan.
export function spokePath(s) {
  const mx = (s.from.x + s.to.x) / 2;
  return `M${s.from.x} ${s.from.y} C${mx} ${s.from.y} ${mx} ${s.to.y} ${s.to.x} ${s.to.y}`;
}

// Selection-only links (hand-offs, agent to its ghost). Different columns: S-curve. Same column: bow out left.
export function linkPath(p, q) {
  if (Math.abs(p.x - q.x) < 4) {
    const bow = 48 + Math.min(80, Math.abs(q.y - p.y) / 6);
    return `M${p.x - p.r} ${p.y} C${p.x - bow} ${p.y} ${q.x - bow} ${q.y} ${q.x - q.r - 3} ${q.y}`;
  }
  const mx = (p.x + q.x) / 2;
  const dir = q.x > p.x ? 1 : -1;
  return `M${p.x + dir * p.r} ${p.y} C${mx} ${p.y} ${mx} ${q.y} ${q.x - dir * (q.r + 3)} ${q.y}`;
}
