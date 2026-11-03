// A display page: tiles on one screen for a TV. Mesh and Timeline show their controls while the pointer moves. /single-screen, /dual-screen/1, /dual-screen/2; add /dark for dark.
// ?tiles=mesh,ledger,usage picks the tiles. ?page=dual-screen/1 is for the offline preview, which has no routes.
//
// Size, one rule for every screen and every tile:
// 1. The page is always BASE (1536) units wide: zoom = screen width / BASE x size. A 1366 laptop, a 1920 TV and a 4K TV show the same page.
// 2. Ctrl + and Ctrl - (or Ctrl+scroll) change size in 10% steps; Ctrl 0 resets. Kept per page in this browser; ?size=0.8 sets it for one load.
// 3. Inside a tile, content is whole items in priority order (the children of its [data-fit] element). After each render the items that
//    don't fit whole are hidden, and the tile's head says how many. Canvases (Mesh) and the Timeline fill their box and report the same way.
// So a bigger size shows fewer, larger rows and a smaller size shows more; nothing is ever cut in half.
import { S, start, subscribe } from "../core/store.js";
import { h, mount, delegate } from "../core/dom.js";
import { agentState, inUse, runway } from "../core/model.js";
import { ago, clock, dur, now } from "../core/format.js";
import { statusDot } from "../ui/kit.js";
import { fullscreenButton } from "../ui/shell.js";
import { LAYOUTS, evenLayout } from "./layouts.js";
import { TILES } from "./tiles.js";

const APP = "/";               // where a click on a name or a tile title leads
const RELOAD = 6 * 3600e3;     // a TV stays open for days: pick up new code now and then
const BASE = 1536;             // layout units across the screen

const q = new URLSearchParams(location.search), root = document.documentElement;
const path = (q.get("page") || location.pathname).replace(/^\/|\/$/g, ""), page = path.replace(/\/(light|dark)$/, "");
const picked = (q.get("tiles") || "").split(",").filter(id => TILES[id]);
const L = picked.length ? evenLayout(picked) : LAYOUTS[page] || LAYOUTS["single-screen"];
const ids = [...new Set(L.areas.join(" ").split(" "))];

root.dataset.theme = path.endsWith("/dark") ? "dark" : "light";

// ---- size ----
const SIZE = "displaySize:" + page;
let size = +q.get("size") || +localStorage.getItem(SIZE) || 1, sizeNote = 0, sizeTimer = 0;
const fit = () => { root.style.zoom = (innerWidth / BASE) * size; };
fit(); addEventListener("resize", () => { fit(); schedule(); });
function resize(k) {
  size = k ? Math.round(Math.min(2, Math.max(.5, size * k)) * 100) / 100 : 1;
  if (k) localStorage.setItem(SIZE, size); else localStorage.removeItem(SIZE);
  fit(); sizeNote = Math.round(size * 100); clearTimeout(sizeTimer); sizeTimer = setTimeout(() => { sizeNote = 0; schedule(); }, 2500); schedule();
}
addEventListener("keydown", e => {
  if (!(e.ctrlKey || e.metaKey)) return;
  const k = e.key === "+" || e.key === "=" ? 1.1 : e.key === "-" ? 1 / 1.1 : e.key === "0" ? 0 : null;
  if (k !== null) { e.preventDefault(); resize(k); }
});
addEventListener("wheel", e => { if (e.ctrlKey || e.metaKey) { e.preventDefault(); e.stopPropagation(); resize(e.deltaY < 0 ? 1.1 : 1 / 1.1); } }, { passive: false, capture: true });

// ---- the frame: built once; each render morphs the insides of a tile ----
const top = h("div", { class: "dt-top-in" });
const parts = ids.map(id => {
  const head = h("div", { class: "dt-head-r" }), body = h("div", { class: "dt-body" });
  const el = h("section", { class: "dt-tile", style: `grid-area:${id}`, "data-tile": id, "aria-label": TILES[id].label },
    h("header", { class: "dt-head" }, h("a", { class: "dt-name", href: APP + "#" + (TILES[id].view || id) }, TILES[id].label), head), body);
  return { t: TILES[id], el, head, body };
});
const grid = h("div", { class: "dt", style: `grid-template-columns:${L.cols};grid-template-rows:${L.rows};grid-template-areas:${L.areas.map(a => `"${a}"`).join(" ")}` }, parts.map(p => p.el));
document.body.append(grid);
grips();
document.body.prepend(h("header", { class: "dt-top" }, top));

// ---- moving the edges between tiles: drag one, or focus it and use the arrow keys; double-click resets. Kept per page in this browser. ----
// A grid line can only move as a whole, so an edge is the run of a line where the tiles on either side differ.
function grips() {
  const KEY = "displayTracks:" + page + (picked.length ? "?" + picked.join() : ""), cells = L.areas.map(a => a.split(" ")), nr = cells.length, nc = cells[0].length;
  const MIN = { col: 180, row: 90 }, prop = { col: "gridTemplateColumns", row: "gridTemplateRows" };
  const px = axis => getComputedStyle(grid)[prop[axis]].split(" ").map(parseFloat);
  const put = (axis, t) => { grid.style[prop[axis]] = t.map(v => `minmax(0,${v.toFixed(2)}fr)`).join(" "); };
  try { const saved = JSON.parse(localStorage.getItem(KEY) || "null"); if (saved && saved.col.length === nc && saved.row.length === nr) { put("col", saved.col); put("row", saved.row); } } catch (e) {}
  const save = () => localStorage.setItem(KEY, JSON.stringify({ col: px("col"), row: px("row") }));
  const move = (axis, i, d) => {   // d in layout px, down or right is positive: the track before the edge takes it from the tracks after, nearest first (and the other way round), so an edge can push past a small track
    const t = px(axis), lim = MIN[axis], from = d > 0 ? t.map((_, j) => i + 1 + j).filter(j => j < t.length) : t.map((_, j) => i - j).filter(j => j >= 0);
    let left = Math.abs(d); for (const j of from) { const take = Math.min(left, Math.max(0, t[j] - lim)); t[j] -= take; left -= take; }
    t[d > 0 ? i : i + 1] += Math.abs(d) - left; put(axis, t); schedule();
  };
  const grip = (axis, i, a, b) => {
    const col = axis === "col", el = h("div", { class: "dt-grip", "data-axis": axis, role: "separator", tabindex: "0", "aria-orientation": col ? "vertical" : "horizontal", "aria-label": "Move the edge between tiles", title: "Drag to resize, double-click to reset",
      style: col ? `grid-column:${i + 2};grid-row:${a + 1}/${b + 2}` : `grid-row:${i + 2};grid-column:${a + 1}/${b + 2}` });
    el.addEventListener("pointerdown", e => {
      e.preventDefault(); el.setPointerCapture(e.pointerId); el.dataset.on = "";
      const r = grid.getBoundingClientRect(), k = r.width / (grid.offsetWidth || 1); let last = col ? e.clientX : e.clientY;
      const mv = ev => { const at = col ? ev.clientX : ev.clientY; move(axis, i, (at - last) / k); last = at; };
      el.addEventListener("pointermove", mv);
      el.addEventListener("pointerup", () => { el.removeEventListener("pointermove", mv); delete el.dataset.on; save(); }, { once: true });
    });
    el.addEventListener("keydown", e => {
      const d = (col ? { ArrowLeft: -16, ArrowRight: 16 } : { ArrowUp: -16, ArrowDown: 16 })[e.key];
      if (d) { e.preventDefault(); move(axis, i, d); save(); }
    });
    el.addEventListener("dblclick", () => { localStorage.removeItem(KEY); grid.style.gridTemplateColumns = L.cols; grid.style.gridTemplateRows = L.rows; schedule(); });
    grid.append(el);
  };
  for (let i = 0; i < nc - 1; i++) for (let r = 0; r < nr; r++) { if (cells[r][i] === cells[r][i + 1]) continue; let e = r; while (e + 1 < nr && cells[e + 1][i] !== cells[e + 1][i + 1] && cells[e + 1][i] === cells[r][i] && cells[e + 1][i + 1] === cells[r][i + 1]) e++; grip("col", i, r, e); r = e; }
  for (let i = 0; i < nr - 1; i++) for (let c = 0; c < nc; c++) { if (cells[i][c] === cells[i + 1][c]) continue; let e = c; while (e + 1 < nc && cells[i][e + 1] !== cells[i + 1][e + 1] && cells[i][e + 1] === cells[i][c] && cells[i + 1][e + 1] === cells[i + 1][c]) e++; grip("row", i, c, e); c = e; }
}

/** Rule 3: hide the items that don't fit whole, in order. Returns how many were hidden. Runs before paint, so nothing flickers. */
function fitItems(body) {
  const box = body.querySelector("[data-fit]"); if (!box) return 0;
  const items = [...box.children]; for (const el of items) el.hidden = false;
  const r = body.getBoundingClientRect(), k = r.height / (body.offsetHeight || 1), lim = r.bottom - parseFloat(getComputedStyle(body).paddingBottom) * k + 1;
  let n = 0;
  for (const el of items) if (n || el.getBoundingClientRect().bottom > lim) { el.hidden = true; n++; }
  return n;
}

// ---- the top: the counts you look for from across the room, then trouble in words ----
function counts() {
  const A = S.live ? S.live.agents : [], st = A.map(a => agentState(a).key), n = k => st.filter(x => x === k).length;
  return h("div", { class: "dt-sum", role: "status" },
    h("span", { class: "dt-run" }, A.length + " running"),
    [["working", "working"], ["queue", "in job queue"], ["quiet", "quiet"], ["stuck", "stuck"]].map(([k, label]) => {
      const c = n(k);
      return h("span", { class: "dt-n", "data-zero": c ? null : "", "data-tone": k === "stuck" && c ? "bad" : null }, statusDot(k), h("b", null, c), h("span", null, label));
    }));
}
function room() {
  const U = S.usage; if (!U || !(U.logins || []).length) return null;
  const us = U.logins, live = us.filter(l => inUse(l, U));
  const soon = live.map(l => ({ l, r: runway(l, U) })).filter(x => x.r.before && x.r.toStop > 0).sort((a, b) => a.r.toStop - b.r.toStop)[0];
  return h("div", { class: "dt-room", "data-tone": !live.length ? "bad" : soon || live.length === 1 ? "warn" : null },
    statusDot(live.length ? "inuse" : "atstop"), h("b", null, `${live.length} of ${us.length}`),
    h("span", null, "logins have room" + (soon ? `; ${soon.l.name} reaches its ${soon.r.stop}% stop in about ${dur(soon.r.toStop)}` : "")));
}
function trouble() {
  const out = [], Lv = S.live, U = S.usage, t = now();
  if (S.failed) out.push(["Lost contact", "last data " + ago(S.fresh)]);
  if (Lv) {
    const stuck = Lv.agents.map(a => [a, agentState(a)]).filter(([, s]) => s.key === "stuck");
    if (stuck.length) out.push([`${stuck.length} stuck`, stuck.map(([a, s]) => `${a.name} on ${a.thread}, ${s.text}`).join(" · ")]);
    if (Lv.limit_until) out.push(["Starts held", "until " + clock(Lv.limit_until * 1e3)]);
  }
  if (U && (U.logins || []).length) {
    const live = U.logins.filter(l => inUse(l, U));
    if (!live.length) out.push(["No login has room", "nothing new starts"]);
    for (const l of live) { const w = l.limits && l.limits.five_hour; if (w && w.r * 1e3 > t && w.u >= .9) out.push([`${l.name} at ${Math.round(w.u * 100)}%`, "of its 5-hour limit"]); }
  }
  return out.length ? h("div", { class: "dt-trouble", role: "alert" }, out.map(([a, b]) => h("span", { "data-key": a }, statusDot("stuck"), h("b", null, a), b ? h("span", null, b) : null))) : null;
}

let queued = false;
const ctx = { update: () => schedule(), arg: null };
function schedule() { if (queued) return; queued = true; queueMicrotask(() => { queued = false; render(); }); }
function render() {
  mount(top, h("div", null,
    h("div", { class: "dt-top-row" }, h("b", { class: "dt-brand" }, "Hive"), counts(), room(),
      sizeNote ? h("span", { class: "dt-size" }, `Size ${sizeNote}% · Ctrl 0 resets`) : null,
      h("time", { class: "dt-clock" }, new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), fullscreenButton()),
    trouble()));
  for (const p of parts) {
    try {       // one tile's bug must not blank a screen nobody is watching closely
      mount(p.body, p.t.render(ctx, p.body));
      const n = fitItems(p.body), more = n ? (p.t.more ? p.t.more(n) : `+${n} more`) : null, head = p.t.head ? p.t.head() : null;
      mount(p.head, h("div", { class: "dt-head-in" }, more ? h("span", { class: "dt-more" }, more) : null, head ? h("span", null, head) : null));
    } catch (e) { console.error(p.t.id, e); mount(p.body, h("div", { class: "dt-broken" }, "This tile hit an error: " + e.message)); }
  }
}

// ---- things a TV needs ----
// Anything that navigates (a thread, a page link in the agent panel) goes to the app; the display has no routes of its own.
addEventListener("hashchange", () => { location.href = APP + location.hash; });
// Keep the screen on, where the browser allows it (https only).
const stayOn = () => { if (navigator.wakeLock && !document.hidden) navigator.wakeLock.request("screen").catch(() => {}); };
stayOn(); document.addEventListener("visibilitychange", stayOn);
// Hide the cursor when it isn't moving.
let idle = 0;
const moved = () => { delete root.dataset.idle; clearTimeout(idle); idle = setTimeout(() => { root.dataset.idle = ""; }, 4000); };
addEventListener("pointermove", moved); addEventListener("pointerdown", moved); addEventListener("keydown", moved); moved();
// The clock: once a minute is enough.
setInterval(schedule, 15e3);
// Reload for new code, but only if the page would come back.
setInterval(() => { fetch(location.href, { cache: "no-store" }).then(r => { if (r.ok && !r.redirected) location.reload(); }).catch(() => {}); }, RELOAD);

delegate(document);
subscribe(schedule);
for (const p of parts) if (p.t.enter) p.t.enter(ctx);
start().then(render);
render();
