// The plot fence (formulas, with sliders) and the chart fence (a small header plus csv). All own code: formulas go through
// formula.js, the drawing is built node by node, and colours come from CSS classes so it follows the theme by itself.
// Each panel has one y axis and at most three series: a different unit gets its own panel below, on the same x.
import { h, s } from "../core/dom.js";
import { figure } from "./figure.js";
import { sigOf } from "./math.js";
import { compile, constant, usesTrig } from "./formula.js";
import { parseCsv, numOf } from "./table.js";

const SAMPLES = 400, MAX_SRC = 500000, MAX_ROWS = 20000, MAX_SERIES = 12, MAX_PARAMS = 8, MAX_DEFS = 24, PER_PANEL = 3;
const VALS = new Map();
const valsOf = k => { let v = VALS.get(k); if (!v) { VALS.set(k, v = {}); if (VALS.size > 200) VALS.delete(VALS.keys().next().value); } return v; };

// ---- numbers and ticks ----
const SUF = [[1e12, "T"], [1e9, "G"], [1e6, "M"], [1e3, "k"], [1, ""], [1e-3, "m"], [1e-6, "u"], [1e-9, "n"], [1e-12, "p"]];
export function fmt(v) {
  if (!isFinite(v)) return "–";
  if (v === 0) return "0";
  const a = Math.abs(v), [k, u] = SUF.find(x => a >= x[0] * 0.9995) || [1, ""];
  if (a >= 1e15 || a < 1e-13) return v.toExponential(1);
  return String(+(v / k).toPrecision(3)) + u;
}
function linTicks(lo, hi, n = 5) {
  const raw = (hi - lo) / n || 1, mag = 10 ** Math.floor(Math.log10(raw)), st = [1, 2, 5, 10].map(x => x * mag).find(x => x >= raw * 0.999);
  const out = []; for (let v = Math.ceil(lo / st - 1e-9) * st; v <= hi + st * 1e-9 && out.length < 40; v += st) out.push(Math.abs(v) < st * 1e-9 ? 0 : v);
  return out;
}
function logTicks(lo, hi) {
  const a = Math.ceil(Math.log10(lo) - 1e-9), b = Math.floor(Math.log10(hi) + 1e-9), st = Math.max(1, Math.ceil((b - a) / 8));
  const out = []; for (let d = a; d <= b; d += st) out.push(10 ** d);
  return out.length > 1 ? out : linTicks(lo, hi);
}
/** A y range that ignores a few wild points, such as a pole shooting to 1e12, unless they are all there is. */
export function yRange(vals) {
  const f = vals.filter(isFinite).sort((a, b) => a - b); if (!f.length) return null;
  let lo = f[0], hi = f[f.length - 1];
  const p = q => f[Math.floor((f.length - 1) * q)], a = p(0.02), b = p(0.98);
  if (b > a && hi - lo > 12 * (b - a)) { lo = a; hi = b; }
  if (lo === hi) { const d = Math.abs(lo) * 0.1 || 1; lo -= d; hi += d; }
  const m = (hi - lo) * 0.06;
  return [lo - m, hi + m];
}

// ---- the header line grammar ----
const range = t => { const m = /^(\S+?)\.\.(\S+)$/.exec(t); if (!m) throw new Error(`"${t}" should look like 1k..10M`); const r = [constant(m[1]), constant(m[2])]; if (!(r[0] < r[1])) throw new Error(`The range ${t} runs backwards`); return r; };

export function parsePlot(src) {
  const P = { title: null, x: null, params: [], defs: [], ys: [], marks: [], deg: true };
  for (const raw of src.split("\n")) {
    const l = raw.trim(); if (!l || l[0] === "#") continue;
    const m = /^(title|x|param|def|y|mark|angle)\s*:\s*(.*)$/.exec(l);
    if (!m) throw new Error(`"${l.slice(0, 40)}" isn't a line a plot understands`);
    const [, key, v] = m;
    if (key === "title") P.title = v;
    else if (key === "angle") { if (v !== "rad" && v !== "deg") throw new Error("angle: is deg or rad"); P.deg = v === "deg"; }
    else if (key === "x") {
      const w = v.split(/\s+/), log = w[w.length - 1] === "log"; if (log) w.pop();
      if (w.length < 2 || w.length > 3) throw new Error("x: wants a name, a range, and maybe a unit and log");
      const [lo, hi] = range(w[1]); if (log && lo <= 0) throw new Error("A log axis has to start above 0");
      P.x = { name: w[0], lo, hi, unit: w[2] || "", log };
    } else if (key === "param") {
      const pm = /^([A-Za-z_]\w*)\s+(\S+)\s*=\s*(\S+)(?:\s+(.*))?$/.exec(v); if (!pm) throw new Error("param: looks like: fc 60k..300k = 120k Hz");
      const [lo, hi] = range(pm[2]), w = (pm[4] || "").split(/\s+/).filter(Boolean), log = w[w.length - 1] === "log"; if (log) w.pop();
      if (log && lo <= 0) throw new Error("A log slider has to start above 0");
      P.params.push({ name: pm[1], lo, hi, def: Math.min(hi, Math.max(lo, constant(pm[3]))), unit: w.join(" "), log });
    } else if (key === "def") {
      const dm = /^([A-Za-z_]\w*)\s*=\s*(.+)$/.exec(v); if (!dm) throw new Error("def: looks like: g = tan(pm/2 + 45)");
      P.defs.push({ name: dm[1], text: dm[2] });
    } else if (key === "y") {
      const i = v.indexOf("="); if (i < 1) throw new Error("y: looks like: phase deg = -180 + atan(f)");
      const w = v.slice(0, i).trim().split(/\s+/), unit = w.length > 1 ? w.pop() : "";
      P.ys.push({ name: w.join(" "), unit, text: v.slice(i + 1).trim() });
    } else if (key === "mark") {
      for (const part of v.split(",")) {
        const mm = /^\s*([xy])\s*=\s*(\S+)\s*$/.exec(part); if (!mm) throw new Error("mark: looks like: y=0, x=100k");
        P.marks.push({ axis: mm[1], at: constant(mm[2]) });
      }
    }
  }
  if (!P.x) throw new Error("It needs an x: line.");
  if (!P.ys.length) throw new Error("It needs a y: line.");
  if (P.ys.length > MAX_SERIES || P.params.length > MAX_PARAMS || P.defs.length > MAX_DEFS) throw new Error("It has too many lines to draw.");
  // Each def sees only what comes before it, so a formula can't refer to itself.
  const names = new Set([P.x.name, ...P.params.map(p => p.name)]);
  for (const d of P.defs) { d.f = compile(d.text, names, P.deg); names.add(d.name); }
  for (const y of P.ys) y.f = compile(y.text, names, P.deg);
  P.trig = [...P.defs, ...P.ys].some(d => usesTrig(d.text));
  return P;
}

/** Sample every y of a parsed plot at these parameter values. Only numbers come out. */
export function samplePlot(P, vals) {
  const { lo, hi, log, name } = P.x, xs = [], env = Object.create(null);
  for (const p of P.params) env[p.name] = vals[p.name] ?? p.def;
  const out = P.ys.map(() => []);
  for (let i = 0; i < SAMPLES; i++) {
    const t = i / (SAMPLES - 1), x = log ? lo * (hi / lo) ** t : lo + (hi - lo) * t;
    xs.push(x); env[name] = x;
    for (const d of P.defs) env[d.name] = d.f(env);
    P.ys.forEach((y, k) => out[k].push(y.f(env)));
  }
  return { xs, ys: out };
}

export function splitPanels(series) {
  const panels = [];
  for (const sr of series) {
    let p = panels.find(q => q.unit === sr.unit && q.series.length < PER_PANEL);
    if (!p) panels.push(p = { unit: sr.unit, series: [], range: null });
    p.series.push(sr);
  }
  return panels;
}

// ---- drawing: one svg, the panels stacked on one x ----
const axisLabel = a => a.name + (a.unit ? ` (${a.unit})` : "");

function drawChart(box, c) {
  const W = Math.max(280, box.clientWidth || 600), L = 56, R = 14, panelH = c.panels.length > 1 ? 150 : 210, gap = 10, top = 24, bottom = 40;
  const H = c.panels.length * (panelH + top + gap) - gap + bottom, pw = W - L - R;
  const root = s("svg", { width: W, height: H, viewBox: `0 0 ${W} ${H}`, class: "rc-plotsvg", role: "img", "aria-label": c.alt });
  const { x } = c, sx = x.log ? v => L + (Math.log(v / x.lo) / Math.log(x.hi / x.lo)) * pw : v => L + ((v - x.lo) / (x.hi - x.lo)) * pw;
  const xt = x.labels ? [] : x.log ? logTicks(x.lo, x.hi) : linTicks(x.lo, x.hi, Math.max(3, Math.floor(pw / 90)));
  c.geom = [];
  c.panels.forEach((P, pi) => {
    const y0 = pi * (panelH + top + gap) + top, r = P.range, sy = v => y0 + panelH - ((v - r[0]) / (r[1] - r[0])) * panelH;
    c.geom.push({ y0, panelH, sy, P });
    root.append(s("rect", { class: "g-frame", x: L, y: y0, width: pw, height: panelH }));
    const yt = linTicks(r[0], r[1], 4);
    for (const v of yt) {
      root.append(s("path", { class: "g-grid", d: `M${L} ${sy(v).toFixed(1)}H${L + pw}` }));
      root.append(s("text", { class: "g-tick", x: L - 7, y: sy(v) + 4, "text-anchor": "end" }, fmt(v)));
    }
    for (const v of xt) root.append(s("path", { class: "g-grid", d: `M${sx(v).toFixed(1)} ${y0}V${y0 + panelH}` }));
    // above the panel: a lone series is simply named; several get a legend, so nothing rests on colour alone
    let lx = L;
    P.series.forEach((sr, k) => {
      const label = sr.name + (sr.unit ? ` (${sr.unit})` : "");
      const many = P.series.length > 1, sw = many ? 23 : 0;
      if (many) root.append(c.type === "bar" ? s("rect", { class: `g-f${k}`, x: lx, y: y0 - 14, width: 10, height: 10, rx: 2 }) : s("path", { class: `g-s${k}`, d: `M${lx} ${y0 - 9}h18` }));
      root.append(s("text", { class: "g-lab", x: lx + sw, y: y0 - 5 }, label));
      lx += sw + label.length * 6.6 + 16;
    });
    const clip = s("svg", { x: L, y: y0, width: pw, height: panelH, viewBox: `${L} ${y0} ${pw} ${panelH}` });
    for (const m of c.marks) {
      if (m.axis === "y" && m.at >= r[0] && m.at <= r[1]) clip.append(s("path", { class: "g-mark", d: `M${L} ${sy(m.at).toFixed(1)}H${L + pw}` }));
      if (m.axis === "x" && m.at >= x.lo && m.at <= x.hi && (!x.log || m.at > 0)) clip.append(s("path", { class: "g-mark", d: `M${sx(m.at).toFixed(1)} ${y0}V${y0 + panelH}` }));
    }
    if (c.type === "bar") {
      const n = P.series.length, step = pw / c.xs.length, bw = Math.max(2, Math.min(36, (step - 4) / n - 2));
      const base = sy(Math.max(r[0], Math.min(r[1], 0)));
      P.series.forEach((sr, k) => sr.vals.forEach((v, i) => {
        if (!isFinite(v)) return;
        const bx = L + step * i + (step - n * (bw + 2) + 2) / 2 + k * (bw + 2), by = sy(v);
        clip.append(s("rect", { class: `g-f${k}`, x: bx.toFixed(1), y: Math.min(by, base).toFixed(1), width: bw.toFixed(1), height: Math.max(1, Math.abs(by - base)).toFixed(1), rx: Math.min(3, bw / 2) }));
      }));
    } else P.series.forEach((sr, k) => {
      if (c.type === "scatter") sr.vals.forEach((v, i) => { if (isFinite(v)) clip.append(s("circle", { class: `g-f${k}`, cx: sx(c.xs[i]).toFixed(1), cy: sy(v).toFixed(1), r: 4 })); });
      else {
        let d = "", pen = false;
        sr.vals.forEach((v, i) => { if (!isFinite(v)) { pen = false; return; } d += (pen ? "L" : "M") + sx(c.xs[i]).toFixed(1) + " " + sy(v).toFixed(1); pen = true; });
        clip.append(s("path", { class: `g-s${k}`, d }));
      }
    });
    root.append(clip);
    if (pi === c.panels.length - 1) {
      if (x.labels) {
        const every = Math.max(1, Math.ceil(x.labels.length * 64 / pw));
        x.labels.forEach((t, i) => { if (i % every === 0) root.append(s("text", { class: "g-tick", x: L + (pw / x.labels.length) * (i + 0.5), y: y0 + panelH + 16, "text-anchor": "middle" }, t.length > 10 ? t.slice(0, 9) + "…" : t)); });
      } else for (const v of xt) root.append(s("text", { class: "g-tick", x: sx(v), y: y0 + panelH + 16, "text-anchor": "middle" }, fmt(v)));
      if (x.name) root.append(s("text", { class: "g-lab", x: L + pw / 2, y: y0 + panelH + 34, "text-anchor": "middle" }, axisLabel(x)));
    }
  });
  const cross = s("g", { class: "g-hover" }); root.append(cross);
  c.sx = sx; c.L = L; c.pw = pw; c.cross = cross;
  return root;
}

function nearest(c, px) {
  if (c.type === "bar") return Math.max(0, Math.min(c.xs.length - 1, Math.floor((px - c.L) / (c.pw / c.xs.length))));
  let lo = 0, hi = c.xs.length - 1;
  if (c.sorted) { while (hi - lo > 1) { const m = (lo + hi) >> 1; if (c.sx(c.xs[m]) < px) lo = m; else hi = m; } return Math.abs(c.sx(c.xs[lo]) - px) < Math.abs(c.sx(c.xs[hi]) - px) ? lo : hi; }
  let best = 0, bd = Infinity; c.xs.forEach((v, i) => { const d = Math.abs(c.sx(v) - px); if (d < bd) { bd = d; best = i; } });
  return best;
}

function hoverable(host, c, read) {
  const clear = () => { c.cross?.replaceChildren(); read.textContent = c.hint; };
  host.onpointermove = e => {
    const svg = host.querySelector("svg"); if (!svg || !c.geom) return;
    const rect = svg.getBoundingClientRect(), px = e.clientX - rect.left;
    if (px < c.L || px > c.L + c.pw) return clear();
    const i = nearest(c, px), X = c.type === "bar" ? c.L + (c.pw / c.xs.length) * (i + 0.5) : c.sx(c.xs[i]), bits = [];
    bits.push(`${c.x.name || "x"} = ${c.x.labels ? c.x.labels[i] : fmt(c.xs[i])}${c.x.unit ? " " + c.x.unit : ""}`);
    const kids = [];
    for (const g of c.geom) {
      kids.push(s("path", { class: "g-cross", d: `M${X.toFixed(1)} ${g.y0}V${g.y0 + g.panelH}` }));
      g.P.series.forEach((sr, k) => {
        const v = sr.vals[i]; bits.push(`${sr.name} = ${fmt(v)}${sr.unit ? " " + sr.unit : ""}`);
        if (isFinite(v) && v >= g.P.range[0] && v <= g.P.range[1] && c.type !== "bar") kids.push(s("circle", { class: `g-f${k} g-ring`, cx: X.toFixed(1), cy: g.sy(v).toFixed(1), r: 4.5 }));
      });
    }
    c.cross.replaceChildren(...kids); read.textContent = bits.join("  ·  ");
  };
  host.onpointerleave = clear;
}

/** The frame around a drawing, once the data is ready: redraws on resize and keeps the readout under it. */
export function mount(c, extras = []) {
  const plot = h("div", { class: "rc-plotb" }), read = h("div", { class: "rc-plotread", "aria-live": "off" }, c.hint);
  const redraw = () => { plot.replaceChildren(drawChart(plot, c)); };
  const wrap = h("div", null, plot, read, ...extras);
  hoverable(plot, c, read);
  let w = 0;
  const ro = new ResizeObserver(() => {
    if (!plot.isConnected) return ro.disconnect();
    const n = plot.clientWidth; if (n && Math.abs(n - w) > 2) { w = n; redraw(); }
  });
  ro.observe(plot);
  return { wrap, plot, redraw };
}

// ---- the plot fence ----
function sliderOf(p, value, on) {
  const toT = v => (p.log ? Math.log(v / p.lo) / Math.log(p.hi / p.lo) : (v - p.lo) / (p.hi - p.lo)) * 1000;
  const fromT = t => (p.log ? p.lo * (p.hi / p.lo) ** (t / 1000) : p.lo + (p.hi - p.lo) * t / 1000);
  const text = v => fmt(v) + (p.unit ? " " + p.unit : "");
  const out = h("output", null, text(value));
  const input = h("input", { type: "range", min: 0, max: 1000, step: 1, value: Math.round(toT(value)), "aria-label": p.name, "aria-valuetext": text(value), oninput: (e, n) => {
    const v = fromT(+n.value); out.textContent = text(v); n.setAttribute("aria-valuetext", text(v)); on(v);
  } });
  return { row: h("div", { class: "rc-param" }, h("label", null, p.name), input, out), set: v => { input.value = Math.round(toT(v)); input.setAttribute("aria-valuetext", text(v)); out.textContent = text(v); } };
}

const titleOf = (src, given) => { const m = /^\s*title\s*:\s*(.+)$/m.exec(src.slice(0, 2000)); return given || (m ? m[1].trim() : null); };

export function plotFigure(code, { title }) {
  const key = sigOf("plot\0" + code), vals = valsOf(key); let P = null;
  const said = () => (P?.params.length ? "\n\nwith " + P.params.map(p => `${p.name} = ${fmt(vals[p.name] ?? p.def)}${p.unit ? " " + p.unit : ""}`).join(", ") : "");
  return figure({
    kind: "plot", label: "plot", title: titleOf(code, title), src: code, reserve: 300,
    ask: () => "```plot\n" + code + "\n```" + said(),
    draw: () => {
      if (code.length > MAX_SRC) throw new Error("It's too long.");
      P = parsePlot(code);
      const all = () => samplePlot(P, vals), first = all();
      if (!first.ys.some(y => y.some(isFinite))) throw new Error("Its formulas give no numbers over this range.");
      const series = P.ys.map((y, k) => ({ name: y.name, unit: y.unit, vals: first.ys[k] }));
      const c = { type: "line", x: P.x, xs: first.xs, sorted: true, marks: P.marks, panels: splitPanels(series), hint: "Move over the plot to read values.",
        alt: `Plot of ${P.ys.map(y => y.name).join(", ")} against ${P.x.name}` };
      const fit = () => { for (const q of c.panels) q.range = grow(q.range, yRange(q.series.flatMap(x => x.vals))) || [-1, 1]; };
      // Slider moves only widen the axes, so a curve visibly moves instead of the axes following it. Reset starts them again.
      const grow = (a, b) => (!a ? b : !b ? a : [Math.min(a[0], b[0]), Math.max(a[1], b[1])]);
      fit();
      let queued = false, m;
      const update = () => { if (queued) return; queued = true; requestAnimationFrame(() => {
        queued = false; const d = all(); series.forEach((x, k) => { x.vals = d.ys[k]; }); fit(); m.redraw();
      }); };
      const sliders = P.params.map(p => ({ p, ...sliderOf(p, vals[p.name] ?? p.def, v => { vals[p.name] = v; update(); }) }));
      const notes = [];
      if (P.params.length) notes.push(h("button", { type: "button", class: "rc-t", onclick: () => {
        for (const x of sliders) { delete vals[x.p.name]; x.set(x.p.def); }
        for (const q of c.panels) q.range = null; update();
      } }, "Reset"));
      if (P.trig) notes.push(h("span", null, P.deg ? "sin, cos, tan and atan work in degrees here." : "sin, cos, tan and atan work in radians here."));
      m = mount(c, sliders.length || notes.length ? [h("div", { class: "rc-params" }, ...sliders.map(x => x.row), notes.length ? h("div", { class: "rc-paramsn" }, ...notes) : null)] : []);
      return { own: m.wrap };
    },
  });
}

// ---- the chart fence: header lines, then csv with a heading row ----
export function parseChart(src) {
  const lines = src.split("\n"), H = { title: null, x: null, ys: [], type: null, marks: [] }; let i = 0;
  for (; i < lines.length; i++) {
    const l = lines[i].trim(); if (!l) continue;
    const m = /^(title|x|y|type|mark)\s*:\s*(.*)$/.exec(l); if (!m) break;
    const [, key, v] = m;
    if (key === "title") H.title = v;
    else if (key === "type") { if (!["line", "bar", "scatter"].includes(v)) throw new Error("type: is line, bar or scatter"); H.type = v; }
    else if (key === "mark") for (const part of v.split(",")) { const mm = /^\s*([xy])\s*=\s*(\S+)\s*$/.exec(part); if (!mm) throw new Error("mark: looks like: y=0, x=100k"); H.marks.push({ axis: mm[1], at: constant(mm[2]) }); }
    else { const w = v.split(/\s+/), log = w[w.length - 1] === "log"; if (log) w.pop(); const col = { name: w[0], unit: w.slice(1).join(" "), log }; if (key === "x") H.x = col; else H.ys.push(col); }
  }
  const body = lines.slice(i).join("\n"), rows = parseCsv(body, body.split("\n", 1)[0].includes("\t") ? "\t" : ",");
  if (rows.length < 2) throw new Error("It needs a heading row and some data.");
  if (rows.length > MAX_ROWS) throw new Error(`It's over ${MAX_ROWS} rows.`);
  const head = rows[0].map(t => t.trim()), data = rows.slice(1), at = n => { const k = head.indexOf(n); if (k < 0) throw new Error(`There's no column called ${n}`); return k; };
  const xi = H.x ? at(H.x.name) : 0;
  const ycols = H.ys.length ? H.ys : head.map((n, k) => ({ name: n, unit: "" })).filter((_, k) => k !== xi);
  if (!ycols.length) throw new Error("It needs a column to plot.");
  if (ycols.length > MAX_SERIES) throw new Error("It has too many columns to draw.");
  const rawx = data.map(r => (r[xi] ?? "").trim()), xn = rawx.map(numOf), numeric = xn.every(isFinite);
  const series = ycols.map(y => { const k = at(y.name); return { name: y.name, unit: y.unit || "", vals: data.map(r => numOf(r[k] ?? "")) }; });
  if (!series.some(q => q.vals.some(isFinite))) throw new Error("Its columns have no numbers in them.");
  return { H, xi, head, rawx, xn, numeric, series };
}

export function chartFigure(code, { title }) {
  return figure({
    kind: "chart", label: "chart", title: titleOf(code, title), src: code, reserve: 300,
    draw: () => {
      if (code.length > MAX_SRC) throw new Error("It's too long.");
      const d = parseChart(code), log = !!d.H.x?.log, name = d.H.x?.name || d.head[d.xi], unit = d.H.x?.unit || "";
      const type = d.H.type || (d.numeric ? "line" : "bar");
      if (!d.numeric && type !== "bar") throw new Error("Its x column has words in it, so it can only be a bar chart.");
      if (type === "bar" && d.rawx.length > 200) throw new Error("That's too many bars. Use a line.");
      let x, xs, sorted = true, series = d.series;
      if (type === "bar") { xs = d.rawx.map((_, i) => i); x = { name, unit, labels: d.rawx, lo: 0, hi: xs.length, log: false }; }
      else {
        const ord = d.xn.map((_, i) => i);
        if (type === "line") ord.sort((a, b) => d.xn[a] - d.xn[b]); else sorted = false;
        xs = ord.map(i => d.xn[i]); series = d.series.map(q => ({ ...q, vals: ord.map(i => q.vals[i]) }));
        let lo = Math.min(...xs), hi = Math.max(...xs);
        if (log && lo <= 0) throw new Error("A log axis needs every x above 0.");
        if (lo === hi) { lo -= Math.abs(lo) * 0.1 || 1; hi += Math.abs(hi) * 0.1 || 1; }
        x = { name, unit, lo, hi, log };
      }
      const panels = splitPanels(series);
      for (const q of panels) {
        let r = yRange(q.series.flatMap(sr => sr.vals)) || [-1, 1];
        if (type === "bar") r = [Math.min(r[0], 0), Math.max(r[1], 0)];
        q.range = r;
      }
      const c = { type, x, xs, sorted, marks: d.H.marks, panels, hint: "Move over the chart to read values.", alt: `${type} chart of ${series.map(q => q.name).join(", ")} against ${name}` };
      return { own: mount(c).wrap };
    },
  });
}
