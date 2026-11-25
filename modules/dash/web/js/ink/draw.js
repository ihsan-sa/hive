// Ink geometry (thread 112515205dbf): the person's strokes as filled outlines whose width follows the pressure, the agents'
// marks in a hand-drawn pen (recipes from ~/designs/ink-chat/mock/hand.py, seeded so a mark looks the same on every load),
// hold-to-snap shapes, and the closed-loop test that offers Ask / Side chat. Pure functions of numbers: no DOM here.
import { W } from "./grammar.js";

// A small seeded random: the same mark draws the same wobble everywhere.
export function rnd(seed) {
  let a = typeof seed === "number" ? seed >>> 0 : [...String(seed)].reduce((h, c) => Math.imul(h ^ c.charCodeAt(0), 16777619), 2166136261) >>> 0;
  const f = () => { a = (a + 0x6D2B79F5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
  f.u = (lo, hi) => lo + (hi - lo) * f();
  return f;
}
const f1 = n => (Math.round(n * 10) / 10).toString();
const P = (x, y) => f1(x) + "," + f1(y);

/** Catmull-Rom through pts, as cubic Béziers (hand.py _smooth). */
export function smooth(pts, closed) {
  if (pts.length < 2) return pts.length ? `M${P(...pts[0])} l0,0` : "";
  const q = closed ? [pts.at(-1), ...pts, pts[0], pts[1]] : [pts[0], ...pts, pts.at(-1)];
  let d = `M${P(q[1][0], q[1][1])}`;
  for (let i = 1; i < q.length - 2; i++) {
    const [p0, p1, p2, p3] = [q[i - 1], q[i], q[i + 1], q[i + 2]];
    d += ` C${P(p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6)} ${P(p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6)} ${P(p2[0], p2[1])}`;
  }
  return d;
}
/** The rough length of a path's polyline, for the write-on dash (hand.py length). */
export function plen(d) {
  const n = (d.match(/-?\d+(?:\.\d+)?/g) || []).map(Number); let L = 0;
  for (let i = 2; i + 1 < n.length; i += 2) L += Math.hypot(n[i] - n[i - 2], n[i + 1] - n[i - 1]);
  return Math.round(L * 1.1) + 10;
}

// ---------- the person's strokes ----------
/** A stroke's outline: a closed path to fill, width from pressure (a pen) or from speed (a mouse, whose pressure is flat). */
export function outline(st) {
  const raw = st.p || [], hl = st.t === "hl", base = st.w || (hl ? 16 : 2.8);
  if (!raw.length) return "";
  // a light smoothing pass: each point half-way to the next one's pull, so a trembling hand reads as a line
  const pts = [raw[0]];
  for (let i = 1; i < raw.length; i++) { const a = pts[i - 1], b = raw[i]; pts.push([a[0] + (b[0] - a[0]) * .6, a[1] + (b[1] - a[1]) * .6, b[2] ?? .5]); }
  if (raw.length > 1) pts.push(raw.at(-1));
  const flat = pts.every(p => Math.abs((p[2] ?? .5) - .5) < .01);
  const ws = pts.map((p, i) => {
    if (hl) return base / 2;
    if (!flat) return base * (.3 + .95 * Math.min(1, p[2] ?? .5)) / 2;
    const q = pts[Math.max(0, i - 1)], v = Math.hypot(p[0] - q[0], p[1] - q[1]);
    return base * (1.05 - Math.min(.45, v / 30)) / 2;
  });
  // taper the first and last few points a little, as a pen lands and lifts
  if (!hl) for (let i = 0; i < Math.min(3, ws.length); i++) { const k = .55 + .15 * i; ws[i] *= k; ws[ws.length - 1 - i] *= Math.max(k, .7); }
  const bb = bbox(pts);
  if (pts.length < 3 || Math.max(bb.w, bb.h) < 1) {   // a dot
    const [x, y] = pts[0], r = Math.max(ws[0], base * .45);
    return `M${P(x - r, y)} a${f1(r)},${f1(r)} 0 1,0 ${f1(2 * r)},0 a${f1(r)},${f1(r)} 0 1,0 ${f1(-2 * r)},0Z`;
  }
  const L = [], R = [];
  for (let i = 0; i < pts.length; i++) {
    const a = pts[Math.max(0, i - 1)], b = pts[Math.min(pts.length - 1, i + 1)];
    let dx = b[0] - a[0], dy = b[1] - a[1]; const n = Math.hypot(dx, dy) || 1; dx /= n; dy /= n;
    const w = ws[i]; L.push([pts[i][0] - dy * w, pts[i][1] + dx * w]); R.push([pts[i][0] + dy * w, pts[i][1] - dx * w]);
  }
  const side = arr => { let d = ""; for (let i = 1; i < arr.length - 1; i++) d += ` Q${P(...arr[i])} ${P((arr[i][0] + arr[i + 1][0]) / 2, (arr[i][1] + arr[i + 1][1]) / 2)}`; return d + ` L${P(...arr.at(-1))}`; };
  const rEnd = ws.at(-1), rStart = ws[0];
  return `M${P(...L[0])}${side(L)} A${f1(rEnd)},${f1(rEnd)} 0 0,1 ${P(...R.at(-1))}${side(R.slice().reverse())} A${f1(rStart)},${f1(rStart)} 0 0,1 ${P(...L[0])}Z`;
}
export function bbox(pts) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const p of pts) { if (p[0] < x0) x0 = p[0]; if (p[0] > x1) x1 = p[0]; if (p[1] < y0) y0 = p[1]; if (p[1] > y1) y1 = p[1]; }
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}
const pathLen = pts => { let L = 0; for (let i = 1; i < pts.length; i++) L += Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]); return L; };

/** A stroke that ends where it began, round something: {x,y,w,h} of what it encloses, or null. Used for Ask / Side chat. */
export function isLoop(pts, min = 14) {
  if (pts.length < 8) return null;
  const b = bbox(pts), diag = Math.hypot(b.w, b.h), L = pathLen(pts);
  if (b.w < min || b.h < min * .6) return null;
  const gap = Math.hypot(pts[0][0] - pts.at(-1)[0], pts[0][1] - pts.at(-1)[1]);
  // closed: the ends meet (or cross) within a quarter of its size, and it went round, not back and forth
  if (gap > Math.max(min * 1.5, diag * .28) || L < diag * 1.7) return null;
  let turn = 0;
  for (let i = 2; i < pts.length; i++) {
    const a = Math.atan2(pts[i - 1][1] - pts[i - 2][1], pts[i - 1][0] - pts[i - 2][0]), c = Math.atan2(pts[i][1] - pts[i - 1][1], pts[i][0] - pts[i - 1][0]);
    let d = c - a; while (d > Math.PI) d -= 2 * Math.PI; while (d < -Math.PI) d += 2 * Math.PI; turn += d;
  }
  return Math.abs(turn) > Math.PI * 1.4 ? b : null;
}

/** Hold still at the end of a stroke: the clean shape it meant (an ellipse, a box or a line) as points, or null. */
export function snapShape(pts) {
  if (pts.length < 4) return null;
  const b = bbox(pts), diag = Math.hypot(b.w, b.h); if (diag < 12) return null;
  const pr = pts.reduce((s, p) => s + (p[2] ?? .5), 0) / pts.length, L = pathLen(pts);
  const a = pts[0], z = pts.at(-1), gap = Math.hypot(a[0] - z[0], a[1] - z[1]);
  if (gap / L > .85) return { k: "line", p: [[a[0], a[1], pr], [z[0], z[1], pr]] };
  if (gap > diag * .3) return null;
  const cx = b.x + b.w / 2, cy = b.y + b.h / 2, rx = b.w / 2 || 1, ry = b.h / 2 || 1;
  const eRes = pts.reduce((s, p) => s + Math.abs(Math.hypot((p[0] - cx) / rx, (p[1] - cy) / ry) - 1), 0) / pts.length;
  const bRes = pts.reduce((s, p) => s + Math.min(Math.abs(p[0] - b.x) / rx, Math.abs(p[0] - b.x - b.w) / rx, Math.abs(p[1] - b.y) / ry, Math.abs(p[1] - b.y - b.h) / ry), 0) / pts.length;
  const out = [];
  if (bRes < eRes * .8) {
    const c = [[b.x, b.y], [b.x + b.w, b.y], [b.x + b.w, b.y + b.h], [b.x, b.y + b.h], [b.x, b.y]];
    for (let i = 0; i < 4; i++) for (let j = 0; j < 8; j++) out.push([c[i][0] + (c[i + 1][0] - c[i][0]) * j / 8, c[i][1] + (c[i + 1][1] - c[i][1]) * j / 8, pr]);
    out.push([b.x, b.y, pr]);
    return { k: "box", p: out };
  }
  const t0 = Math.atan2(a[1] - cy, a[0] - cx);
  for (let i = 0; i <= 48; i++) { const t = t0 + 2 * Math.PI * i / 48; out.push([cx + rx * Math.cos(t), cy + ry * Math.sin(t), pr]); }
  return { k: "ellipse", p: out };
}
/** The id-th point list's nearest distance to (x,y), for the eraser. */
export function nearStroke(st, x, y, r) {
  const p = st.p || [], w = (st.w || 3) / 2 + r;
  for (let i = 0; i < p.length; i++) {
    const a = p[i], b = p[i + 1] || a, dx = b[0] - a[0], dy = b[1] - a[1], L2 = dx * dx + dy * dy;
    const t = L2 ? Math.max(0, Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / L2)) : 0;
    if (Math.hypot(a[0] + t * dx - x, a[1] + t * dy - y) <= w) return true;
  }
  return false;
}

// ---------- the agents' pen ----------
const circleP = (cx, cy, rx, ry, r, over = .18) => {
  const a0 = r.u(200, 235) * Math.PI / 180, n = 14, pts = [];
  for (let i = 0; i < n + 3; i++) { const t = a0 + 2 * Math.PI * (1 + over) * i / (n + 2), w = 1 + r.u(-.05, .06) + .05 * i / n; pts.push([cx + rx * w * Math.cos(t), cy + ry * w * Math.sin(t)]); }
  return [smooth(pts)];
};
const boxP = (x, y, w, h, r) => {
  const j = () => r.u(-2.2, 2.2), a = [x + j(), y + j()], b = [x + w + j(), y + j()], c = [x + w + j(), y + h + j()], d = [x + j(), y + h + j()];
  return [`M${P(a[0] - 3, a[1] + 1)} Q${P((a[0] + b[0]) / 2, a[1] - 1.5)} ${P(...b)} Q${P(b[0] + 1.5, (b[1] + c[1]) / 2)} ${P(...c)} Q${P((c[0] + d[0]) / 2, c[1] + 1.5)} ${P(...d)} Q${P(d[0] - 1.5, (d[1] + a[1]) / 2)} ${P(a[0] + 2, a[1] - 2)}`];
};
const headP = (x2, y2, ang, head = 12) => { const h = [-1, 1].map(s => { const a = ang + Math.PI - s * 28 * Math.PI / 180; return [x2 + head * Math.cos(a), y2 + head * Math.sin(a)]; }); return `M${P(...h[0])} L${P(x2, y2)} L${P(...h[1])}`; };
const arrowP = (pts, bow = .12) => {
  if (pts.length > 2) { const z = pts.at(-1), y = pts.at(-2); return [smooth(pts), headP(z[0], z[1], Math.atan2(z[1] - y[1], z[0] - y[0]))]; }
  const [[x1, y1], [x2, y2]] = pts, mx = (x1 + x2) / 2, my = (y1 + y2) / 2, dx = x2 - x1, dy = y2 - y1;
  const qx = mx - dy * bow, qy = my + dx * bow;
  return [`M${P(x1, y1)} Q${P(qx, qy)} ${P(x2, y2)}`, headP(x2, y2, Math.atan2(y2 - qy, x2 - qx))];
};
const checkP = (x, y, s = 1.25) => [`M${P(x - 9 * s, y - 2 * s)} Q${P(x - 5 * s, y + 2 * s)} ${P(x - 2 * s, y + 8 * s)} Q${P(x + 5 * s, y - 8 * s)} ${P(x + 16 * s, y - 16 * s)}`];
const crossP = (x, y, s = 1.15) => [`M${P(x - 8 * s, y - 8 * s)} Q${P(x, y - 1)} ${P(x + 8 * s, y + 9 * s)}`, `M${P(x + 8 * s, y - 9 * s)} Q${P(x + 1, y)} ${P(x - 8 * s, y + 8 * s)}`];
const underP = (x1, y, x2, r, wob = 1.6) => { const pts = []; for (let i = 0; i < 7; i++) pts.push([x1 + (x2 - x1) * i / 6, y + r.u(-wob, wob) + i / 6 * 1.5]); return [smooth(pts)]; };

/** A note in the agent's hand: wrapped at 60 characters, three lines at most (the rest belongs in the post's prose). */
export function wrapNote(s, n = 60) {
  const words = String(s || "").split(/\s+/).filter(Boolean), out = [];
  let cur = "";
  for (const w of words) { if (cur && (cur + " " + w).length > n) { out.push(cur); cur = w; } else cur = cur ? cur + " " + w : w; }
  if (cur) out.push(cur);
  if (out.length > 3) { out.length = 3; out[2] = out[2].replace(/\s*\S*$/, "") + "…"; }
  return out;
}
export const NOTE = 22;   // a note's size in sheet units
const noteW = s => s.length * NOTE * .5;

/** An agent mark as drawable parts: {k, paths:[d], note?:{x,y,lines}, box:{x,y,w,h}, hl?: band height}.
 *  own: the person's stroke boxes on this sheet (rough aim snaps to them within 24 units unless `exact`);
 *  find(words): the rect of printed words in sheet units, or null. h: the sheet's height (marks are pulled inside). */
export function markParts(m, seed, own = [], find = null, h = 4000) {
  const r = rnd(seed), k = m.k;
  let pts = (m.pts || []).map(p => [Math.max(0, Math.min(W, p[0])), Math.max(0, Math.min(h + 400, p[1]))]);
  let rect = null;
  if (m.anchor && find) rect = find(m.anchor);
  if (m.anchor && !rect) return null;
  // the region the mark means, before snapping
  if (!rect) {
    const [x, y] = pts[0] || [0, 0];
    if (k === "circle") { const rx = m.w ? m.w / 2 : m.r || 36, ry = m.h ? m.h / 2 : (m.r || 36) * .8; rect = { x: x - rx, y: y - ry, w: 2 * rx, h: 2 * ry }; }
    else if (k === "box" || k === "hl") rect = { x, y, w: m.w || 120, h: m.h || 36 };
    else if (k === "underline" || k === "strike") { const x2 = pts[1] ? pts[1][0] : x + (m.w || 120); rect = { x: Math.min(x, x2), y: k === "underline" ? y - 28 : y - 10, w: Math.abs(x2 - x), h: k === "underline" ? 28 : 20 }; }
  }
  let fit = !!m.anchor;   // fitted round something found (words or the person's ink): pad it; else draw as written
  if (rect && !m.exact && !m.anchor && ["circle", "box", "underline", "strike"].includes(k) && own.length) {
    const g = 24, hit = own.filter(b => b.x + b.w / 2 > rect.x - g && b.x + b.w / 2 < rect.x + rect.w + g && b.y + b.h / 2 > rect.y - g && b.y + b.h / 2 < rect.y + rect.h + g);
    if (hit.length) {
      const u = hit.reduce((a, b) => ({ x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), x1: Math.max(a.x1, b.x + b.w), y1: Math.max(a.y1, b.y + b.h) }), { x: Infinity, y: Infinity, x1: -Infinity, y1: -Infinity });
      const ur = { x: u.x, y: u.y, w: u.x1 - u.x, h: u.y1 - u.y };
      if (ur.w * ur.h < Math.max(rect.w * rect.h * 4, 2000)) { rect = ur; fit = true; }   // aimed at that ink: fit it; a mark round a lot more than it meant stays as written
    }
  }
  let paths = [], box = rect, hl = 0, note = null;
  switch (k) {
    case "circle": { const rx = rect.w / 2 + (fit ? 12 : 0), ry = rect.h / 2 + (fit ? (rect.h < 40 ? 10 : 8) : 0); const cx = rect.x + rect.w / 2, cy = rect.y + rect.h / 2; paths = circleP(cx, cy, rx, ry, r); box = { x: cx - rx, y: cy - ry, w: 2 * rx, h: 2 * ry }; break; }
    case "box": { const p = fit ? 6 : 0; box = { x: rect.x - p, y: rect.y - p, w: rect.w + 2 * p, h: rect.h + 2 * p }; paths = boxP(box.x, box.y, box.w, box.h, r); break; }
    case "hl": { hl = Math.max(10, rect.h); paths = [`M${P(rect.x, rect.y + rect.h / 2)} L${P(rect.x + rect.w, rect.y + rect.h / 2 + r.u(-1, 1))}`]; break; }
    case "underline": { const y = rect.y + rect.h + 4; paths = underP(rect.x, y, rect.x + rect.w, r); box = { x: rect.x, y: y - 4, w: rect.w, h: 8 }; break; }
    case "strike": { const y = rect.y + rect.h / 2; paths = underP(rect.x - 4, y, rect.x + rect.w + 4, r, .8); box = { x: rect.x, y: y - 4, w: rect.w, h: 8 }; break; }
    case "arrow": case "line": {
      if (m.anchor && rect) { const to = [rect.x - 6, rect.y + rect.h / 2]; pts = pts.length ? [pts[0], to] : [[rect.x - 90, rect.y + rect.h / 2 + 40], to]; }
      paths = k === "arrow" ? arrowP(pts) : [pts.length > 2 ? smooth(pts) : `M${P(...pts[0])} L${P(...pts[1])}`]; box = bbox(pts); break;
    }
    case "check": case "cross": { const [x, y] = rect ? [rect.x + rect.w + 16, rect.y + rect.h / 2] : pts[0]; paths = (k === "check" ? checkP : crossP)(x, y); box = { x: x - 12, y: y - 20, w: 34, h: 32 }; break; }
    case "path": { paths = [smooth(pts)]; box = bbox(pts); break; }
    case "text": { const [x, y] = pts[0]; const lines = wrapNote(m.label); note = { x, y, lines }; box = { x, y: y - NOTE, w: Math.max(...lines.map(noteW)), h: lines.length * NOTE * 1.15 }; break; }
    default: return null;
  }
  if (m.label && k !== "text") {
    const lines = wrapNote(m.label), lw = Math.max(...lines.map(noteW));
    let x = box.x + box.w + 10, y = box.y + Math.min(box.h, 40) / 2 + 7;
    if (k === "arrow" || k === "line") { const s = pts[0]; x = s[0] + (pts.at(-1)[0] >= s[0] ? -lw - 8 : 8); y = s[1] + 7; if (x < 4) { x = s[0] - 4; y = s[1] + 26; } }
    if (x + lw > W - 4) { x = Math.max(4, box.x + box.w - lw); y = box.y + box.h + NOTE + 2; }
    note = { x, y, lines };
  }
  return { k, paths, note, box, hl };
}

/** Write-on timing: the duration of one part in ms (900 per 600 units of path, capped at 1.6 s; a note by its letters). */
export const writeMs = d => Math.round(Math.min(1600, Math.max(220, 900 * plen(d) / 600)));
export const noteMs = lines => Math.min(1600, Math.max(300, lines.join("").length * 45));
