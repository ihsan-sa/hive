// Timeline: a row per thread, a bar per session, a tick per post. Fit (the default) shows the current run of activity,
// so recent work doesn't sit in a sliver at the right. A bar's name shows only if it fits; hover or focus shows it always.
import { h, s, morph } from "../core/dom.js";
import { S, SYS, getPref, setPref } from "../core/store.js";
import { agentStatus, isRunning, sEnd, threadTitle, tierOf } from "../core/model.js";
import { clock, dayName, dur, mdPlain, now, short, stamp, DAY, HOUR, MIN } from "../core/format.js";
import { agentHref, threadHref } from "../core/router.js";
import { status, tier } from "../core/status.js";
import { canvas } from "../templates.js";
import { button, infoButton, seg, statusDot, waiting } from "../ui/kit.js";
import { keep, shapePath, hollow } from "../ui/graph.js";
import { openPanel } from "../ui/panel.js";
import { showTip, hideTip } from "../ui/tip.js";

const TMIN = 5 * MIN, TMAX = 30 * DAY, RIGHT = 18, AXIS = 24, GAP = 2 * HOUR;
const PRE = [["fit", "Fit", "Fit the current run of activity"], ["1h", "1h"], ["6h", "6h"], ["24h", "24h"], ["3d", "3d"], ["7d", "7d"]];
const WIN = { "1h": HOUR, "6h": 6 * HOUR, "24h": DAY, "3d": 3 * DAY, "7d": 7 * DAY };
// T.fit: the window comes from the data. Otherwise T.win is the span and T.end its right edge; a null end follows now.
const savedWin = getPref("tlWin", "fit");   // "fit" or a preset's key
const T = { fit: !WIN[savedWin], win: WIN[savedWin] || DAY, end: null, active: !!getPref("tlActive", 0), ver: -1, w: 0, last: 0, raf: 0, nLive: 0, nDone: 0, update: () => {} };

const sLive = x => !x.end && isRunning(x.name);
const gutter = W => (T.fitRows ? 128 : W < 640 ? 96 : 190);
const rowH = W => (W < 640 ? 22 : 18);
const TILE_MIN = 3 * HOUR;  // a display tile's window is the running agents' span, at least this, so ended work has somewhere to show
// type sizes come from the tokens, so a display page's larger type is measured as drawn
const fonts = () => { const cs = getComputedStyle(box), px = k => parseFloat(cs.getPropertyValue(k)) || 11; return { bar: px("--fs-micro"), name: px("--fs-meta") }; };

// ---- text width, for dropping what doesn't fit ----
const pen = document.createElement("canvas").getContext("2d");
const widths = new Map();
function textW(t, px) { const k = px + t; let w = widths.get(k); if (w == null) { pen.font = `${px}px ${getComputedStyle(document.body).fontFamily}`; w = pen.measureText(t).width; if (widths.size > 4000) widths.clear(); widths.set(k, w); } return w; }
function fitText(t, px, max) { if (textW(t, px) <= max) return t; let lo = 0, hi = t.length; while (lo < hi) { const m = (lo + hi + 1) >> 1; if (textW(t.slice(0, m) + "…", px) <= max) lo = m; else hi = m - 1; } return lo ? t.slice(0, lo) + "…" : ""; }

/** The current run of activity: walk back from the newest thing until a gap of 2 h, no further than a day. Ends at now if anything is recent. */
function fitRange(t) {
  const iv = [];
  const liveOnly = T.active || T.fitRows;   // a display tile's window follows who's running, even with ended work showing
  for (const x of S.sessions) { if (liveOnly && !sLive(x)) continue; iv.push([x.start, sLive(x) ? t : sEnd(x)]); }
  if (!liveOnly) for (const p of S.posts) if (!SYS.has(p.from)) iv.push([p.t, p.t]);
  // Nothing can be newer than now: one post stamped ahead (a machine's clock, a bad import) must not push the window past it and blank the chart.
  const seen = iv.filter(x => x[0] <= t);
  if (!seen.length) return [t - DAY, t, true];
  seen.sort((a, b) => b[1] - a[1]);
  const hi = Math.min(seen[0][1], t), cap = hi - DAY; let lo = hi;
  for (const [a, b] of seen) { if (b < lo - GAP) break; lo = Math.min(lo, a); }
  lo = Math.max(lo, cap);
  if (T.fitRows) lo = Math.min(lo, hi - TILE_MIN);
  const span = Math.max(TMIN, hi - lo), pad = Math.min(30 * MIN, span * .03);
  const follows = t - hi < Math.max(10 * MIN, span * .05);
  return [lo - pad, follows ? t : hi + pad, follows];
}
function range(t) {
  if (T.fit) { const [a, b, follows] = fitRange(t); T.win = b - a; return { t0: a, t1: b, follows }; }
  const t1 = T.end ?? t; return { t0: t1 - T.win, t1, follows: T.end == null };
}
function pin(t = now()) { if (T.fit) { const r = range(t); T.fit = false; T.end = r.follows ? null : r.t1; } }
function set(win, end) {
  T.win = Math.max(TMIN, Math.min(TMAX, win)); T.end = end == null || end >= now() - 1000 ? null : end; T.ver = -1;
  if (!T.raf) T.raf = requestAnimationFrame(() => { T.raf = 0; draw(); T.update(); });
}
const zoom = (k, frac) => { pin(); const c = (T.end ?? now()) - T.win + frac * T.win, w = Math.max(TMIN, Math.min(TMAX, T.win * k)); set(w, c + (1 - frac) * w); };
const pan = dt => { pin(); set(T.win, (T.end ?? now()) + dt); };
const centre = () => (T.end == null ? 1 : .5);   // following now, zoom keeps now on the right edge

// ---- drawing: both SVGs are rebuilt as data and morphed on, so hover and focus survive a redraw ----
const box = h("div", { class: "tl-box", tabindex: "-1" });
const axisEl = s("svg", { class: "tl-axis", "aria-hidden": "true" }), bodyEl = s("svg", { class: "tl-body" });
box.append(axisEl, bodyEl);
const stage = h("div", { class: "cvp-stage", "data-own": "", "data-sig": "timeline" }, box);

function lanes(t0, t1) {
  const all = new Map(), lane = th => { let l = all.get(th); if (!l) { l = { th, bars: [], ticks: [], last: 0 }; all.set(th, l); } return l; };
  let nLive = 0, nDone = 0;
  for (const x of S.sessions) { const e = sEnd(x); if (e < t0 || x.start > t1) continue; if (sLive(x)) nLive++; else { nDone++; if (T.active) continue; } const l = lane(x.thread || "(none)"); l.bars.push(x); l.last = Math.max(l.last, e); }
  for (const p of S.posts) { if (p.t < t0 || p.t > t1 || SYS.has(p.from)) continue; if (T.active && !all.has(p.th)) continue; const l = lane(p.th); l.ticks.push(p); l.last = Math.max(l.last, p.t); }
  const rank = l => Math.max(l.last, S.threads.get(l.th)?.last || 0), live = l => l.bars.some(sLive);   // rows keep their order as the view moves
  for (const l of all.values()) l.live = live(l);
  T.nLive = nLive; T.nDone = nDone;
  return [...all.values()].sort((a, b) => b.live - a.live || rank(b) - rank(a));
}
const barTone = x => (sLive(x) ? status(agentStatus(x.name).key) : x.exit ? { tone: "bad", shape: "diamond" } : { tone: "ended", shape: "dot" });
const barTip = (x, th) => () => [x.name, `${threadTitle(th)} · ${clock(x.start)}${x.end ? "–" + clock(x.end) : ", still running"} · ${dur(sEnd(x) - x.start)}`, `${x.resumed ? "woken" : "started"} by ${x.from || "?"}` + (x.exit ? ` · exit ${x.exit}` : "")];

// Rows by pixels, not by time: two short sessions never share a spot, however far out the zoom.
// On a display tile only running bars add height: ended ones go inline where they fit, newest first, and the rest are left out.
function placeBars(l, W, x) {
  const L = gutter(W), rows = [], placed = [];
  const put = (b, grow) => {
    const a = Math.max(L, x(b.start)), end = Math.max(a + 2.5, Math.min(W - RIGHT, x(sEnd(b))));
    let r = rows.findIndex(row => row.every(([p, q]) => end + 3 <= p || q + 3 <= a));
    if (r < 0) { if (!grow && rows.length) return; r = rows.length; rows.push([]); }
    rows[r].push([a, end]); placed.push({ s: b, a, b: end, r });
  };
  const byStart = (a, b) => a.start - b.start;
  if (T.fitRows) {
    l.bars.filter(sLive).sort(byStart).forEach(b => put(b, true));
    l.bars.filter(b => !sLive(b)).sort((a, b) => sEnd(b) - sEnd(a)).forEach(b => put(b, false));
    placed.sort((a, b) => byStart(a.s, b.s));
  } else [...l.bars].sort(byStart).forEach(b => put(b, true));
  return { placed, nRows: rows.length };
}

function drawLane(l, y, W, x, follows, nextLive) {
  const L = gutter(W), RH = T.rowPx || rowH(W), tight = RH < rowH(W), TICK = tight ? 2 : 6, PAD = tight ? 1 : 3;
  const { placed, nRows } = placeBars(l, W, x);
  const h_ = Math.max(1, nRows) * RH + TICK + PAD, label = fitText(threadTitle(l.th), T.F.name, L - 22);
  const kids = [
    s("rect", { class: "tl-bg", x: 0, y, width: W, height: h_ }),
    s("line", { class: "tl-rule", "data-live": l.live && !nextLive ? "" : null, x1: 0, x2: W, y1: y + h_, y2: y + h_ }),
    s("a", { href: threadHref(l.th), "aria-label": `${S.app.app ? "Conversation" : "Thread"} ${threadTitle(l.th)}, ${tier(tierOf(l.th)).label}`, "data-lane": "", ontip: () => [l.th, tier(tierOf(l.th)).label], onclick: e => { e.preventDefault(); } },
      s("rect", { class: "tl-name-hit", x: 0, y, width: L, height: h_ }),
      s("text", { class: "tl-name", "data-live": l.live ? "" : null, x: 16, y: y + h_ / 2 + 4 }, label)),
    l.live ? s("path", { class: "tl-cap", "data-tone": "good", d: shapePath("dot", 8, y + h_ / 2, 3.5) }) : null,
  ];
  for (const b of placed) {
    const st = barTone(b.s), live = sLive(b.s), by = y + TICK + b.r * RH + (tight ? 0 : 1), bh = RH - (tight ? 1 : 3), wid = b.b - b.a, cap = live && follows;
    const room = wid - 8 - (cap ? 6 : 0), name = textW(b.s.name, T.F.bar) <= room ? b.s.name : null;
    kids.push(s("a", { class: "tl-hit", "data-key": b.s.name + "@" + b.s.start, href: agentHref(b.s.name), tabindex: "-1", "data-tone": st.tone, "aria-label": `${b.s.name}, ${clock(b.s.start)}${b.s.end ? " to " + clock(b.s.end) : ", still running"}`,
      ontip: barTip(b.s, l.th), onclick: e => { e.preventDefault(); e.stopPropagation(); hideTip(); openPanel("agent", b.s.name); } },
      s("rect", { class: "tl-bar", "data-ended": live ? null : "", x: b.a + .5, y: by + .5, width: Math.max(1.5, wid - 1), height: bh - 1, rx: 2.5 }),
      name ? s("text", { class: "tl-barn", "data-ended": live ? null : "", x: b.a + 4, y: by + bh / 2 + 4 }, name) : null,
      cap ? s("path", { class: "tl-cap", "data-hollow": hollow(st.shape) ? "" : null, d: shapePath(st.shape, b.b, by + bh / 2, 4) }) : null));
  }
  for (const p of l.ticks) {
    const px = x(p.t);
    kids.push(s("rect", { class: "tl-tick", x: px - .75, y: y + 1, width: 1.5, height: TICK - 2 }),
      s("rect", { class: "tl-tickhit", "data-key": p.id, x: px - 3, y, width: 6, height: TICK + 1, ontip: () => [`${p.from} · ${clock(p.t)}`, short(mdPlain(p.text), 180)] }));
  }
  return { g: s("g", { class: "tl-lane", "data-key": l.th, onclick: () => { hideTip(); openPanel("thread", l.th); } }, kids), h: h_ };
}

function draw() {
  const W = box.clientWidth; if (!W) return;
  const t = now(), { t0, t1, follows } = range(t), L = gutter(W), span = Math.max(1, t1 - t0), x = v => L + ((v - t0) / span) * (W - L - RIGHT);
  T.ver = S.ver; T.w = W; T.hgt = box.clientHeight; T.last = t; T.F = fonts();
  const list = lanes(t0, t1); let rows = [], y = 0;
  const lay = some => { rows = []; y = 0; some.forEach((l, i) => { const r = drawLane(l, y, W, x, follows, some[i + 1] && some[i + 1].live); rows.push(r.g); y += r.h; }); };
  if (T.fitRows) {
    // A display tile: lanes are whole rows like any list, running threads first, then threads with only ended work.
    // A lane that doesn't fit is left out whole and counted in the head; Ctrl - shows more.
    const room = box.clientHeight - AXIS - 2, live = list.filter(l => l.live), dead = list.filter(l => !l.live);
    T.rowPx = Math.round(T.F.bar * 1.3); T.below = 0; T.deadAll = dead.length; T.deadShown = 0;
    for (let i = 0; i < live.length; i++) { const r = drawLane(live[i], y, W, x, follows, i < live.length - 1); if (y + r.h > room) { T.below = live.length - i; break; } rows.push(r.g); y += r.h; }
    if (!T.below) for (const l of dead) { const r = drawLane(l, y, W, x, follows, false); if (y + r.h > room) break; rows.push(r.g); y += r.h; T.deadShown++; }
  } else lay(list);
  const H = Math.max(y + 10, box.clientHeight - AXIS - 2);

  // axis: a label that would hit its neighbour or the edge is dropped, and "now" comes first
  const step = [1, 2, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880].map(m => m * MIN).find(v => (v / span) * (W - L - RIGHT) >= T.F.bar * 6.5) || 7 * DAY;
  const off = new Date().getTimezoneOffset() * MIN, grid = [], ticks = [], used = [];
  const fits = (cx, w) => { const a = cx - w / 2 - 3, b = cx + w / 2 + 3; if (a < L - 8 || b > W) return false; if (used.some(u => a < u[1] && b > u[0])) return false; used.push([a, b]); return true; };
  if (follows && fits(x(t), textW("now", T.F.bar))) ticks.push(s("text", { class: "tl-nowt", x: x(t), y: 16, "text-anchor": "middle" }, "now"));
  for (let m = Math.ceil((t0 - off) / step) * step + off; m <= t1; m += step) {
    grid.push(s("line", { class: "tl-grid", x1: x(m), x2: x(m), y1: 0, y2: H }));
    const d = new Date(m), tx = step >= DAY ? dayName(m) : d.toLocaleTimeString([], step < HOUR ? { hour: "numeric", minute: "2-digit" } : { hour: "numeric" }) + (d.getHours() === 0 && d.getMinutes() === 0 ? " " + dayName(m) : "");
    if (fits(x(m), textW(tx, T.F.bar))) ticks.push(s("text", { x: x(m), y: 16, "text-anchor": "middle" }, tx));
  }
  const body = s("svg", { class: "tl-body", width: W, height: H }, s("g", null, grid), s("g", { "data-rows": "" }, rows),
    follows ? s("line", { class: "tl-now", x1: x(t), x2: x(t), y1: 0, y2: H }) : null,
    list.length ? null : s("text", { x: W / 2, y: 60, "text-anchor": "middle" }, T.active ? "Nobody running in this range." : "Nothing in this window."));
  const axis = s("svg", { class: "tl-axis", "aria-hidden": "true", width: W, height: AXIS }, ticks);
  morph(axisEl, axis); morph(bodyEl, body);
}
new ResizeObserver(() => { if (box.isConnected && (box.clientWidth !== T.w || (T.fitRows && box.clientHeight !== T.hgt))) { T.ver = -1; draw(); T.update(); } }).observe(box);

// ---- interaction: drag and wheel move through time, Ctrl+wheel zooms, arrow keys walk the bars ----
{
  const area = () => Math.max(1, box.clientWidth - gutter(box.clientWidth) - RIGHT);
  let d = null, swallow = false;
  box.addEventListener("wheel", e => {
    if (e.ctrlKey || e.metaKey) { e.preventDefault(); const f = (e.clientX - box.getBoundingClientRect().left - gutter(box.clientWidth)) / area(); zoom(Math.exp(e.deltaY * (e.deltaMode ? .05 : Math.abs(e.deltaY) < 40 ? .015 : .004)), Math.max(0, Math.min(1, f))); return; }
    const dx = e.shiftKey ? e.deltaY : e.deltaX;
    if (Math.abs(dx) > Math.abs(e.shiftKey ? 0 : e.deltaY)) { e.preventDefault(); pan((dx / area()) * T.win); }
  }, { passive: false });
  box.addEventListener("pointerdown", e => { if (e.button === 0) d = { x: e.clientX, id: e.pointerId, on: false }; });
  box.addEventListener("pointermove", e => {
    if (d) {
      const dx = e.clientX - d.x;
      if (!d.on) { if (Math.abs(dx) < 5) return tipAt(e); pin(); d.end = T.end ?? now(); d.on = true; box.setPointerCapture(d.id); box.setAttribute("data-drag", ""); hideTip(); }
      set(T.win, d.end - (dx / area()) * T.win); return;
    }
    tipAt(e);
  });
  const stop = () => { if (d && d.on) { swallow = true; setTimeout(() => (swallow = false), 0); box.removeAttribute("data-drag"); } d = null; };
  box.addEventListener("pointerup", stop); box.addEventListener("pointercancel", stop);
  box.addEventListener("pointerleave", hideTip);
  box.addEventListener("click", e => { if (swallow) { e.stopPropagation(); e.preventDefault(); } }, true);
  // bars are one tab stop per thread: Tab reaches the thread's name, arrows move along its bars and between threads
  box.addEventListener("keydown", e => {
    const a = e.target.closest && e.target.closest("a"), k = e.key; if (!a) return;
    const lane = a.closest(".tl-lane"), kids = lane ? [...lane.querySelectorAll("a")] : [], i = kids.indexOf(a);
    if (k === "ArrowRight" || k === "ArrowLeft") { e.preventDefault(); (kids[i + (k === "ArrowRight" ? 1 : -1)] || a).focus(); }
    else if (k === "ArrowDown" || k === "ArrowUp") { e.preventDefault(); const next = k === "ArrowDown" ? lane.nextElementSibling : lane.previousElementSibling; if (next) next.querySelector("a").focus(); }
  });
  box.addEventListener("focusin", e => { const n = tipNode(e.target); if (!n) return; const r = n.getBoundingClientRect(); showTip(r.left, r.bottom, ...n._on.tip()); });
  box.addEventListener("focusout", hideTip);
}
function tipNode(t) { for (let n = t; n && n !== box; n = n.parentNode) if (n._on && n._on.tip) return n; return null; }
function tipAt(e) { const n = e.pointerType === "touch" ? null : tipNode(e.target); if (n) showTip(e.clientX, e.clientY, ...n._on.tip()); else hideTip(); }

// ---- toolbar ----
const presetValue = () => { if (T.fit) return "fit"; if (T.end != null) return null; return Object.keys(WIN).find(k => WIN[k] === T.win) || null; };
function small(l, say, f) { const b = button(l, f, { size: "s", title: say }); b.setAttribute("aria-label", say); return b; }
function toolbar() {
  const r = range(now());
  return [h("div", { class: "cvp-ctl" },
    seg(PRE, presetValue(), k => { setPref("tlWin", k); if (k === "fit") { T.fit = true; T.end = null; T.ver = -1; draw(); T.update(); } else { T.fit = false; set(WIN[k], null); } }, { label: "Window" }),
    h("span", { class: "row-a-in" },
      small("−", "Zoom out", () => zoom(1.6, centre())), small("+", "Zoom in", () => zoom(1 / 1.6, centre())),
      small("←", "Earlier", () => pan(-T.win / 4)), small("→", "Later", () => pan(T.win / 4))),
    !T.fit && T.end != null ? button("Now", () => set(T.win, null), { size: "s", title: "Back to the latest, following now" }) : null,
    seg([["on", "Active only", "Show only the agents running now, and the rows they're on"]], T.active ? "on" : null, () => { T.active = !T.active; setPref("tlActive", T.active ? 1 : 0); T.ver = -1; draw(); T.update(); }, { label: "Filter" })),
    infoButton("About this chart", say(HELP), { size: "s" }),
    h("span", { class: "cvp-count" }, `${stamp(r.t0)} – ${r.follows ? "now" : stamp(r.t1)} (${dur(r.t1 - r.t0)})`),
    h("span", { class: "cvp-count", role: "status" }, `${T.nLive} running, ${T.nDone} ended in this range`),
  ];
}
const HELP = "Each row is a thread, each bar an agent's session (filled with a status mark at its end while running; pale and dashed once ended; rows with someone running come first), each tick along the top of a row a post. Stacked bars mean agents worked in parallel. A bar's name shows when it fits: hover for any. Click a bar for the agent, a row to read the thread. To zoom: Ctrl+scroll or pinch, or the − and + buttons. Drag sideways to move through time.";
const NOTE = "A row is a thread, a bar is an agent's session, a tick is a post. Click a bar for the agent, a row for the thread.";
const say = t => (S.app.app ? t.replace(/\bthread\b/g, "conversation").replace(/\bpost\b/g, "message") : t);
const note = () => [say(NOTE), h("div", { class: "legend" },
  h("span", null, h("i", { class: "tl-sw" }), "running"), h("span", null, h("i", { class: "tl-sw", "data-ended": "" }), "ended"), h("span", { "data-tone": "bad" }, h("i", { class: "tl-sw", "data-tone": "bad" }), "failed"),
  ["working", "queue", "quiet", "stuck"].map(k => h("span", { class: "tl-lg" }, statusDot(k), status(k).label)))];

/** For a display page's tile: who's running first, in whole rows, then ended work if there's room. Its settings are saved apart from the Timeline tab's. */
export function fitRunning() { T.fitRows = true; T.ver = -1; }
/** What the tile says in its head. */
export const runningNote = () => `${T.nLive} running` + (T.below ? ` · ${T.below} more below` : "") + (T.deadShown ? ` · ${T.deadShown} of ${T.deadAll} that ended` : "");

export default {
  id: "timeline", label: "Timeline", about: "when each agent ran", tab: true,
  deck: {
    jog(d, n, shift) { const k = shift ? 6 : 1; if (d === 1) pan(n * k * T.win / 400); else zoom(Math.exp(-n * k / 60), centre()); return true; },
    play() { T.fit = false; set(T.win, null); T.update(); return true; },
    fit() { setPref("tlWin", "fit"); T.fit = true; T.end = null; T.ver = -1; draw(); T.update(); },
    toggle() { T.active = !T.active; setPref("tlActive", T.active ? 1 : 0); T.ver = -1; draw(); T.update(); },
    window(v) { pin(); set(TMIN * Math.pow(TMAX / TMIN, v), T.end); },
  },
  enter(ctx) { T.update = ctx.update; T.ver = -1; },
  leave() { hideTip(); },
  render(ctx) {
    T.update = ctx.update;
    if (!S.live) return h("div", { class: "page" }, waiting(S.failed, "Reading…", "the timeline"));   // no board yet: an empty chart would say nothing ran
    const t = now(), r = range(t);
    if (box.isConnected && (T.ver !== S.ver || box.clientWidth !== T.w || t - T.last >= Math.min(3e4, (r.t1 - r.t0) / 60))) draw();
    else if (!box.isConnected) queueMicrotask(() => { if (box.isConnected) { draw(); T.update(); } });
    return canvas({ key: "timeline", bar: toolbar(), note: note(), stage: keep(stage) });
  },
};
