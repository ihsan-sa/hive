// Chart frame: title, plot, y range, time axis, hover readout. SVG scales to the frame; text stays HTML
// so it never stretches. Redraws only when its data changes (data-sig).
import { h, s } from "../core/dom.js";
import { hourLabel, stamp, HOUR, LOC } from "../core/format.js";
import { showTip, hideTip } from "./tip.js";

/**
 * chart({title, note, series:[vals, vals2?], kind:"bar"|"step", start, step, fmt, max, top, mark, height, compact, area, key:[labels]})
 * series[0] is drawn as kind; further series as dashed lines. null values leave a gap.
 * colors:[css color per series] draws every series as its own solid line in that color, with no area: one line per entity.
 */
export function chart(o) {
  const series = o.series, N = series[0].length, max = o.max || Math.max(1, ...series.flat().map(v => v || 0));
  const W = 1000, H = 100, x = i => (i / N) * W, y = v => H * (1 - Math.min(v, max) / max);
  const sig = JSON.stringify([series, o.mark, max, o.start, o.step, o.kind, o.colors]);
  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: "none", "aria-hidden": "true" });
  if (o.kind === "bar") {
    const w = W / N;
    series[0].forEach((v, i) => { if (v) svg.append(s("rect", { class: "p-bar", x: x(i) + w * 0.14, y: y(v), width: Math.max(1, w * 0.72), height: H - y(v) })); });
  }
  series.forEach((vals, k) => {
    if (k === 0 && o.kind === "bar") return;
    let d = "", pen = false;
    vals.forEach((v, i) => { if (v == null) { pen = false; return; } d += `${pen ? "L" : "M"}${x(i)} ${y(v)}L${x(i + 1)} ${y(v)}`; pen = true; });
    if (!d) return;
    const i0 = vals.findIndex(v => v != null);
    if (o.colors) { svg.append(s("path", { class: "p-line", style: `--c:${o.colors[k]}`, d })); return; }
    if (k === 0 && o.area !== false) svg.append(s("path", { class: "p-area", d: d + `L${x(vals.length - 1 - [...vals].reverse().findIndex(v => v != null)) + W / N} ${H}L${x(i0)} ${H}Z` }));
    svg.append(s("path", { class: "p-line", "data-k": k, d }));
  });
  if (o.mark != null) svg.append(s("line", { class: "p-mark", x1: 0, x2: W, y1: y(o.mark), y2: y(o.mark) }));

  const cross = h("div", { class: "plot-cross" });
  const plot = h("div", { class: "plot", style: `--h:${o.height || 96}px` }, svg, cross,
    o.compact || (o.mark != null && max && Math.abs(o.mark - max) / max <= 0.1) ? null : h("span", { class: "plot-y", style: "top:0" }, o.top || String(Math.round(max * 100) / 100)),
    o.compact ? null : h("span", { class: "plot-y", style: "top:100%" }, "0"),
    o.mark != null && !o.compact ? h("span", { class: "plot-y", style: `top:${y(o.mark)}%` }, o.markLabel || "") : null);
  plot.addEventListener("pointermove", e => {
    const r = plot.getBoundingClientRect(), i = Math.max(0, Math.min(N - 1, Math.floor(((e.clientX - r.left) / r.width) * N)));
    cross.style.display = "block"; cross.style.left = ((i + 0.5) / N) * 100 + "%";
    showTip(e.clientX, e.clientY, stamp(o.start + i * o.step), ...series.map((v, k) => (v[i] == null ? "no data" : (o.names ? o.names[k] + " " : "") + o.fmt(v[i]))));
  });
  plot.addEventListener("pointerleave", () => { cross.style.display = "none"; hideTip(); });

  const span = N * o.step, days = span > 3 * 24 * HOUR, tick = days ? 24 * HOUR : span > 24 * HOUR ? 12 * HOUR : span > 8 * HOUR ? 6 * HOUR : span > 2 * HOUR ? HOUR : HOUR / 4, ticks = [];
  // Over a few days: one tick a day, at local midnight, named by its weekday.
  const first = days ? new Date(o.start).setHours(24, 0, 0, 0) : Math.ceil(o.start / tick) * tick;
  for (let t = first; t < o.start + span; t = days ? new Date(t).setHours(24, 0, 0, 0) : t + tick) ticks.push(h("span", { style: `left:${((t - o.start) / span) * 100}%` }, days ? new Date(t).toLocaleDateString(LOC, { weekday: "short" }) : hourLabel(t)));
  return h("figure", { class: "chart", style: "margin:0", "data-compact": o.compact ? "" : null, "data-own": "", "data-sig": sig, "data-tone": o.tone || null },
    o.title ? h("figcaption", { class: "chart-h" }, h("b", null, o.title), o.note ? h("span", null, o.note) : null) : null,
    plot,
    o.compact ? null : h("div", { class: "plot-x", "aria-hidden": "true" }, ticks));
}

/** Bucket timestamps or session spans into fixed steps, carried forward. Helpers for views. */
export function carry(a) { for (let i = 1; i < a.length; i++) if (a[i] == null && a[i - 1] != null) a[i] = a[i - 1]; return a; }
