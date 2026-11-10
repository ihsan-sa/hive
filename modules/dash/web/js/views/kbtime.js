// Knowledge over time: how ~/kb grew, from what was recorded and nothing else. Each note keeps one place (its topic's box,
// oldest first), so moving the time slider shows notes appear, grow, link and go without anything jumping. Times come in two
// kinds and are never mixed: recorded time (a backup commit saw it, minutes apart; `kb file` logged it, exact) and stated time
// (a date the note writes about itself, to the day). A link is documented when the note writes the other note's path, and
// inferred when it only names it and we matched the name. Stretches with no backup commit are hatched on the slider: what
// happened in them is not known, and nothing is drawn in their place.
import { h, s } from "../core/dom.js";
import { can, jget } from "../core/store.js";
import { plural, stamp } from "../core/format.js";
import { href } from "../core/router.js";
import { button, empty, icon, seg, needFull } from "../ui/kit.js";

const PLAY_MS = 12000, PITCH = 30, WIDTH = 1000;
const K = { D: null, L: null, failed: "", at: 0, cut: null, playing: false, sel: null, how: false, show: "all", update: () => {} };
const LINKS = [["all", "All links"], ["doc", "Documented", "The note writes the other note's path"], ["inf", "Inferred", "The note only names the other note; the name was matched"], ["none", "No links"]];

async function load(ctx) {
  if (K.D && Date.now() - K.at < 60000) return;
  K.at = Date.now();
  try {
    const v = await jget("/api/kb/history"), ms = t => (t == null ? null : t * 1e3);
    for (const n of v.notes) {
      n.spans = n.spans.map(([a, b]) => [ms(a), ms(b)]); n.v = n.v.map(([t, l, c]) => [ms(t), l, c]);
      for (const x of n.moves) x.t = ms(x.t);
      for (const x of n.filed) x.t = ms(x.t);
    }
    v.links.forEach((l, i) => { l.i = i; l.spans = l.spans.map(([a, b]) => [ms(a), ms(b)]); });
    v.start = ms(v.start); v.now = ms(v.now); v.gaps = v.gaps.map(([a, b]) => [ms(a), ms(b)]);
    v.by = new Map(v.notes.map(n => [n.path, n]));
    K.D = v; K.L = layout(v); K.failed = "";
  } catch (e) { K.failed = e.message || "failed"; }
  ctx.update();
}

// ---- the layout: fixed by the data, never by time or chance ----
addEventListener("resize", () => { if (K.update && document.querySelector(".kbt-sc")) K.update(); });
function layout(D, W = WIDTH) {
  const topics = new Map();
  for (const n of D.notes) (topics.get(n.topic) || topics.set(n.topic, []).get(n.topic)).push(n);
  const order = [...topics].sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]));
  const pos = new Map(), boxes = [];
  let x = 0, y = 0, rowH = 0;
  for (const [topic, ns] of order) {
    ns.sort((a, b) => (a.spans[0]?.[0] ?? 0) - (b.spans[0]?.[0] ?? 0) || a.path.localeCompare(b.path));
    const cols = Math.min(ns.length, Math.max(2, Math.ceil(Math.sqrt(ns.length * 1.8)))), rows = Math.ceil(ns.length / cols);
    const w = cols * PITCH + 16, bh = rows * PITCH + 30;
    if (x && x + w > W) { x = 0; y += rowH + 14; rowH = 0; }
    boxes.push({ topic, x, y, w, h: bh, ns });
    ns.forEach((n, i) => pos.set(n.path, [x + 8 + PITCH / 2 + (i % cols) * PITCH, y + 26 + PITCH / 2 + Math.floor(i / cols) * PITCH]));
    x += w + 14; rowH = Math.max(rowH, bh);
  }
  const used = Math.max(...boxes.map(b => b.x + b.w));
  return { pos, boxes, h: y + rowH + 2, w: Math.max(used, W / 1.5), box: W };   // w: the drawing's width, grown at most 1.5x to fill a box it doesn't need
}

// ---- what was true at the cut ----
const cutT = () => K.cut ?? K.D.now;
const inSpan = (spans, t) => spans.some(([a, b]) => a <= t && (b == null || b > t));
const born = (n, t) => n.spans.some(([a]) => a <= t);
const lines = (n, t) => { let l = null; for (const v of n.v) { if (v[0] > t) break; if (v[1] != null) l = v[1]; } return l; };
function linksAt(t) {
  const D = K.D;
  return D.links.filter(l => inSpan(l.spans, t) && inSpan(D.by.get(l.a).spans, t) && inSpan(D.by.get(l.b).spans, t));
}
const gone = (n, t) => born(n, t) && !inSpan(n.spans, t);

function select(sel) { K.sel = sel; K.update(); }
const selNote = () => (K.sel && K.sel.kind === "note" ? K.D.by.get(K.sel.id) : null);
const selLink = () => (K.sel && K.sel.kind === "link" ? K.D.links[K.sel.i] : null);

// ---- the graph ----
function curve(a, b) {
  const [x1, y1] = a, [x2, y2] = b, mx = (x1 + x2) / 2, my = (y1 + y2) / 2, dx = x2 - x1, dy = y2 - y1, k = 0.18;
  return `M${x1},${y1} Q${mx - dy * k},${my + dx * k} ${x2},${y2}`;
}
function graph(t) {
  const D = K.D, sn = selNote(), sc = document.querySelector(".kbt-sc"), box = sc ? Math.max(560, Math.round(sc.clientWidth - 32)) : WIDTH;   // lay out to the box's real width, so the drawing fills it
  if (Math.abs(box - K.L.box) > 24) K.L = layout(D, box);
  if (!sc && K.update) queueMicrotask(K.update);   // first draw: the box isn't there to measure yet
  const L = K.L, sl = selLink(), win = (D.now - D.start) / 50;
  const near = sn ? new Set([sn.path]) : sl ? new Set([sl.a, sl.b]) : null;
  const shown = linksAt(t).filter(l => K.show === "all" || K.show === l.kind || (sn && (l.a === sn.path || l.b === sn.path)));
  if (sn) for (const l of shown) if (l.a === sn.path || l.b === sn.path) near.add(l.a).add(l.b);
  const g = [];
  for (const b of L.boxes) {
    if (!b.ns.some(n => born(n, t))) continue;
    g.push(s("g", { class: "kbt-box", "data-key": "b:" + b.topic },
      s("rect", { x: b.x, y: b.y, width: b.w, height: b.h, rx: 8 }),
      s("text", { x: b.x + 8, y: b.y + 16, class: "kbt-bl" }, b.topic + " · " + b.ns.filter(n => inSpan(n.spans, t)).length)));
  }
  for (const l of K.show === "none" && !sn ? [] : shown) {
    const on = sl ? sl === l : sn ? l.a === sn.path || l.b === sn.path : false, d = curve(L.pos.get(l.a), L.pos.get(l.b));
    g.push(s("g", { class: ["kbt-l", "kbt-" + l.kind, l.sup && "kbt-sup", on && "on", (sn || sl) && !on && "dim"], "data-key": "l:" + l.i, onclick: () => select({ kind: "link", i: l.i }) },
      s("path", { d, class: "kbt-lh" }), s("path", { d, class: "kbt-lv", "marker-end": l.sup ? "url(#kbt-ar)" : null }),
      s("title", null, `${l.a} → ${l.b} · ${l.kind === "doc" ? "documented" : "inferred"}${l.sup ? " · supersedes" : ""}`)));
  }
  for (const n of D.notes) {
    if (!born(n, t)) continue;
    const [x, y] = L.pos.get(n.path), ln = lines(n, t), r = Math.min(12, 3.5 + Math.sqrt(ln || 1) / 2.4), off = gone(n, t);
    const hot = !off && n.v.some(v => v[0] <= t && v[0] > t - win);
    g.push(s("g", { class: ["kbt-n", off && "gone", hot && "hot", sn === n && "sel", near && !near.has(n.path) && "dim"], "data-key": "n:" + n.path, "data-path": n.path, onclick: () => select({ kind: "note", id: n.path }) },
      s("circle", { cx: x, cy: y, r: off ? 4 : r }),
      s("title", null, `${n.title}\n${n.path}${off ? " · gone by then" : ln ? " · " + plural(ln, "line") : ""}`)));
  }
  return s("svg", { class: "kbt-svg", viewBox: `-4 -4 ${L.w + 8} ${L.h + 8}`, role: "img", "aria-label": "Notes by topic at the chosen time" },
    s("defs", null, s("marker", { id: "kbt-ar", viewBox: "0 0 8 8", refX: 7, refY: 4, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, s("path", { d: "M0,0 L8,4 L0,8 z" }))), g);
}

// ---- the slider: recorded span from the first backup commit to now, with the unrecorded stretches hatched ----
function setCut(c) { K.cut = c; K.update(); }
function cutAt(track, x) { const r = track.getBoundingClientRect(), v = Math.max(0, Math.min(1, (x - r.left) / r.width)); return v > 0.995 ? null : K.D.start + v * (K.D.now - K.D.start); }
let lastAdv = 0, lastDraw = 0;
function advance(ts) {
  if (!K.playing) return;
  if (!document.querySelector(".kbt")) { K.playing = false; return; }
  requestAnimationFrame(advance);
  if (!lastAdv || document.hidden) { lastAdv = ts; return; }
  const D = K.D, dt = ts - lastAdv; lastAdv = ts;
  K.cut = Math.min(D.now, (K.cut ?? D.start) + (dt * (D.now - D.start)) / PLAY_MS);
  if (K.cut >= D.now) { K.playing = false; K.cut = null; K.update(); return; }
  if (ts - lastDraw > 100) { lastDraw = ts; K.update(); }
}
function play() {
  if (K.playing) { K.playing = false; K.update(); return; }
  if (K.cut == null) K.cut = K.D.start;
  K.playing = true; lastAdv = 0; requestAnimationFrame(advance); K.update();
}
function slider() {
  const D = K.D, span = D.now - D.start || 1, at = t => ((100 * (t - D.start)) / span).toFixed(2) + "%", v = K.cut == null ? 1 : (K.cut - D.start) / span, p = (v * 100).toFixed(2) + "%";
  const filed = D.notes.flatMap(n => n.filed.map(f => f.t)).filter(t => t >= D.start);
  return h("div", { class: "map-rp kbt-rp", "data-key": "rp" },
    button(K.playing ? "Pause" : "Replay", play, { size: "s", icon: "clock", title: "Play the knowledge base from its first backup to now" }),
    h("div", { class: "map-rp-t", onpointerdown: (e, n) => {
      e.preventDefault(); n.setPointerCapture(e.pointerId); K.playing = false; setCut(cutAt(n, e.clientX));
      const mv = ev => setCut(cutAt(n, ev.clientX));
      n.addEventListener("pointermove", mv); n.addEventListener("pointerup", () => n.removeEventListener("pointermove", mv), { once: true });
    } },
      D.gaps.map(([a, b]) => h("i", { class: "kbt-gap", "data-key": "g" + a, style: `left:${at(a)};width:${at(D.start + (b - a))}`, title: `No backup commit from ${stamp(a)} to ${stamp(b)}: not recorded` })),
      filed.map(t => h("i", { class: "kbt-tick", "data-key": "f" + t, style: `left:${at(t)}` })),
      h("i", { class: "map-rp-f", style: `width:${p}` }),
      h("span", { class: "map-rp-h", role: "slider", tabindex: "0", "aria-label": "Time", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(Math.round(v * 100)),
        "aria-valuetext": K.cut == null ? "now" : stamp(K.cut), "data-cut": K.cut != null ? "" : null, style: `left:${p}`,
        onkeydown: e => {
          if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return; e.preventDefault(); e.stopPropagation(); K.playing = false;
          const c = cutT() + (e.key === "ArrowLeft" ? -1 : 1) * span / 100; setCut(c >= D.now ? null : Math.max(D.start, c));
        } })),
    h("span", { class: "map-rp-c", "data-cut": K.cut != null ? "" : null }, K.cut == null ? "Now" : stamp(K.cut)),
    K.cut != null ? button("Back to now", () => { K.playing = false; setCut(null); }, { size: "s" }) : null);
}

// ---- the side pane: the evidence ----
const fig = (label, value, small) => h("div", { class: "fig" }, h("span", null, label), h("b", null, value), small ? h("small", null, small) : null);
const sec = (title, ...body) => h("section", { class: "map-sec" }, h("h3", null, title), ...body);
const row = (key, main, right, onclick) => h(onclick ? "button" : "div", { class: "map-row kbt-row", "data-key": key, type: onclick ? "button" : null, onclick }, h("span", { class: "map-rt" }, main), right ? h("span", { class: "r" }, right) : null);
const ev = (key, when, what, kind) => h("div", { class: "map-row kbt-ev", "data-key": key }, h("span", { class: "kbt-k", "data-kind": kind }, kind), h("span", { class: "map-rt" }, what, h("small", null, when)));
const noteName = p => (K.D.by.get(p)?.title || p);

function overview(t) {
  const D = K.D, ls = linksAt(t), alive = D.notes.filter(n => inSpan(n.spans, t)), gaps = D.gaps.filter(([a]) => a < t);
  const recent = D.notes.map(n => [n, n.v.filter(v => v[0] <= t).pop()]).filter(([n, v]) => v && inSpan(n.spans, t)).sort((a, b) => b[1][0] - a[1][0]).slice(0, 8);
  return [
    h("div", { class: "map-sh" }, h("div", null, h("h2", null, "Knowledge over time"), h("small", null, K.cut == null ? "now" : "as recorded at " + stamp(t)))),
    h("div", { class: "figs" }, fig("Notes", String(alive.length)), fig("Documented links", String(ls.filter(l => l.kind === "doc").length)),
      fig("Inferred links", String(ls.filter(l => l.kind === "inf").length)), fig("Not recorded", plural(gaps.length, "stretch", "stretches"), "before this time")),
    sec("Changed last", recent.map(([n, v]) => row("r:" + n.path, n.title, stamp(v[0]), () => select({ kind: "note", id: n.path })))),
    h("section", { class: "map-sec" }, button("How to read this", () => { K.how = !K.how; K.update(); }, { kind: "quiet", size: "s", icon: K.how ? "chevron-down" : "chevron-right", cls: "kbt-how-t" })),
    !K.how ? null : h("section", { class: "map-sec" }, h("ul", { class: "kbt-how" },
      h("li", null, "Each dot is a note, in its topic's box, oldest first. Its size is its length. Dots that changed lately are lit; a hollow dot was deleted or dropped."),
      h("li", null, "A solid line is documented: the note writes the other note's path. A dashed line is inferred: it only names the other note, and the name was matched. An arrow means the line says it supersedes or replaces."),
      h("li", null, `Times on the slider are recorded times: backup commits, a few minutes apart, from ${stamp(D.start)}. Notes that were already there then show as existing before records began.`),
      h("li", null, "Dates a note gives itself (Filed … by …) are stated times, kept apart and to the day."),
      h("li", null, `Hatched stretches had no backup commit for over ${Math.round(D.gap / 60)} minutes. What happened in them is not known, and nothing is drawn for them.`))),
  ];
}

function noteSide(n, t) {
  const D = K.D, ls = linksAt(t), out = ls.filter(l => l.a === n.path), inn = ls.filter(l => l.b === n.path), ln = lines(n, t);
  const evs = [];
  n.spans.forEach(([a, b], i) => {
    evs.push([a, ev("s" + i, n.pre && i === 0 ? "already there at the first backup, " + stamp(a) : stamp(a), i ? "Seen again in the backup" : n.pre ? "Existed before records began" : "First seen in the backup", "recorded")]);
    if (b != null) evs.push([b, ev("e" + i, stamp(b), n.moves.some(m => m.t === b && m.dropped) ? "Moved to kb/_dropped" : n.moves.some(m => m.t === b && m.to) ? "Moved to " + n.moves.find(m => m.t === b).to : "Deleted", "recorded")]);
  });
  for (const m of n.moves) if (m.from) evs.push([m.t, ev("m" + m.t, stamp(m.t) + " · commit " + m.c, "Renamed from " + m.from, "recorded")]);
  for (const f of n.filed) evs.push([f.t, ev("f" + f.t, stamp(f.t) + " · logged by kb file", (f.added ? "Added to by " : "Filed by ") + f.who, "exact")]);
  for (const x of n.stated) evs.push([Date.parse(x.day + "T12:00"), ev("d" + x.day + x.what, x.day + " · written in the note", (x.what === "filed" ? "Says it was filed by " : "Section “" + x.what + "” by ") + x.who, "stated")]);
  evs.sort((a, b) => a[0] - b[0]);
  const vs = n.v.filter(v => v[0] <= t);
  return [
    h("div", { class: "map-sh" }, h("div", null, h("h2", null, n.title), h("small", null, h("a", { href: href("knowledge", n.path) }, n.path))), button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" })),
    h("div", { class: "figs" }, fig("Length", ln == null ? "–" : plural(ln, "line")), fig("Versions", String(vs.length), "backup commits that changed it"), fig("Links", `${out.length} out · ${inn.length} in`)),
    sec("Its record", evs.map(e => e[1])),
    out.length ? sec("Links to", out.map(l => row("o:" + l.i, noteName(l.b), l.kind === "doc" ? "documented" : "inferred", () => select({ kind: "link", i: l.i })))) : null,
    inn.length ? sec("Linked from", inn.map(l => row("i:" + l.i, noteName(l.a), l.kind === "doc" ? "documented" : "inferred", () => select({ kind: "link", i: l.i })))) : null,
    vs.length > 1 ? sec("Length over its versions", h("div", { class: "kbt-spark", "aria-hidden": "true" }, spark(vs, D))) : null,
  ];
}
function spark(vs, D) {
  const max = Math.max(1, ...vs.map(v => v[1] || 0)), span = D.now - D.start || 1, W = 300, H = 40;
  const pts = vs.filter(v => v[1] != null).map(v => `${((W * (v[0] - D.start)) / span).toFixed(1)},${(H - (H * v[1]) / max).toFixed(1)}`);
  return s("svg", { viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: "none" }, s("polyline", { points: pts.join(" ") }));
}

function linkSide(l, t) {
  const kind = l.kind === "doc" ? "Documented: the note writes this note's path." : "Inferred: the note only names it (a [[name]] or a bare file name), and the name was matched to this note.";
  return [
    h("div", { class: "map-sh" }, h("div", null, h("h2", null, "Link"), h("small", null, l.sup ? "says it supersedes or replaces" : l.kind === "doc" ? "documented" : "inferred")), button("Close", () => select(null), { kind: "icon", icon: "close", size: "s" })),
    sec("From", row("a", noteName(l.a), null, () => select({ kind: "note", id: l.a }))),
    sec("To", row("b", noteName(l.b), null, () => select({ kind: "note", id: l.b }))),
    sec("Evidence", h("p", { class: "kbt-p" }, kind),
      l.sup ? h("p", { class: "kbt-p" }, "The line that holds the link also says supersedes, replaces or replaced by.") : null,
      l.spans.map(([a, b], i) => ev("sp" + i, `${stamp(a)}${i ? "" : " · commit " + l.first} → ${b == null ? "still there" : stamp(b)}`, i ? "In the note again" : "First in the note", "recorded")),
      h("p", { class: "kbt-p" }, h("a", { href: href("knowledge", l.a) }, "Read " + l.a))),
    inSpan(l.spans, t) ? null : h("p", { class: "kbt-p map-dim" }, "Not there at the chosen time."),
  ];
}

export default {
  id: "kbtime", label: "Knowledge over time", about: "how the notes grew, linked and changed, from what was recorded", tab: false,
  enter(ctx) { K.update = ctx.update; K.cut = null; K.sel = ctx.arg ? { kind: "note", id: ctx.arg } : null; load(ctx); },   // a fresh visit opens at now
  leave() { K.playing = false; },
  escape() { if (K.sel) { select(null); return true; } },
  poll: { every: 60000, run(ctx) { load(ctx); } },   // the server memoizes 60 s, so this is cheap
  render(ctx) {
    K.update = ctx.update;
    if (!can("files")) return needFull("Seeing the knowledge base over time");
    if (!K.D) return h("div", { class: "page" }, K.failed ? empty("Couldn't load the history: " + K.failed, { kind: "error" }) : empty("Reading the backup history…", { kind: "loading" }));
    const D = K.D;
    if (!D.notes.length) return empty("No backup history of ~/kb yet.", { kind: "page" });
    if (K.sel && !(K.sel.kind === "note" ? D.by.has(K.sel.id) : D.links[K.sel.i])) K.sel = null;
    const t = cutT(), sn = selNote(), sl = selLink();
    return h("div", { class: "map kbt", "data-key": "kbtime" },
      h("div", { class: "map-tb" },
        h("a", { href: href("knowledge"), class: "kbt-back" }, icon("chevron-left"), "Knowledge"),
        seg(LINKS, K.show, v => { K.show = v; K.update(); }, { label: "Links" }),
        h("span", { class: "map-sum", role: "status" }, `${plural(D.notes.length, "note")} ever · ${plural(D.commits, "backup commit")} touched ~/kb`)),
      h("div", { class: "map-bd" },
        h("main", { class: "map-chart" },
          h("div", { class: "map-stage kbt-stage" }, h("div", { class: "map-cap", "data-cut": K.cut != null ? "" : null }, K.cut == null ? "Now" : "As recorded at " + stamp(t)), h("div", { class: "kbt-sc" }, graph(t))),
          slider()),
        h("aside", { class: "map-side" }, sn ? noteSide(sn, t) : sl ? linkSide(sl, t) : overview(t))));
  },
};
export { K as kbtime };
