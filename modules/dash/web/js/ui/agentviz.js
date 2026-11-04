// Charts for the agent page. Deterministic: the same data always draws the same picture, nothing moves.
// lifeChart: cost per step over an agent's life, with its sessions as a lane and its posts as ticks under it.
// contacts: who it talks to, a ranked list with sent and received bars back to back.
import { h, s } from "../core/dom.js";
import { HOUR, MIN, clock, dayName, money, plural, stamp, toks } from "../core/format.js";
import { showTip, hideTip } from "./tip.js";
import { agentRef } from "./refs.js";
import { empty } from "./kit.js";

const STEPS = [5, 10, 15, 30, 60, 120, 180, 360, 720, 1440].map(m => m * MIN);
/** The step that puts at most `most` bars across a span. */
export const stepFor = (span, most = 48) => STEPS.find(x => span / x <= most) || Math.ceil(span / most / HOUR) * HOUR;

/** The x axis: about five labels at round times. A span over a day shows the day too. */
function axis(t0, t1) {
  const span = t1 - t0, every = STEPS.find(x => span / x <= 5) || Math.ceil(span / 5 / HOUR) * HOUR, out = [];
  const off = new Date(t0).getTimezoneOffset() * MIN;   // round in local time
  for (let t = Math.ceil((t0 - off) / every) * every + off; t <= t1; t += every)
    out.push(h("span", { style: `left:${(100 * (t - t0)) / span}%`, "data-edge": (t - t0) / span > 0.9 ? "end" : (t - t0) / span < 0.06 ? "start" : null }, span > 20 * HOUR && every >= HOUR ? (new Date(t).getHours() === 0 || out.length === 0 ? dayName(t) + " " : "") + clock(t) : clock(t)));
  return out;
}

/**
 * lifeChart({t0, t1, step, bars:[{v, tip:[lines]}], lanes:[{a, b, tone, tip}], ticks:[{t, kind:"out"|"in", tip}], top, note})
 * bars[i] covers t0 + i*step. Every time is ms. A lane is a session; ticks are posts it wrote (out) or got (in).
 */
export function lifeChart(o) {
  const span = Math.max(o.t1 - o.t0, MIN), N = o.bars.length, max = Math.max(1e-9, ...o.bars.map(b => b.v || 0));
  const X = t => (100 * (t - o.t0)) / span;
  const sig = JSON.stringify([o.t0, o.t1, o.step, o.bars.map(b => b.v), o.lanes.map(l => [l.a, l.b, l.tone]), o.ticks.map(k => [k.t, k.kind])]);
  const svg = s("svg", { viewBox: `0 0 1000 100`, preserveAspectRatio: "none", "aria-hidden": "true" });
  const w = (1000 * o.step) / span;
  o.bars.forEach((b, i) => { if (b.v > 0) { const hgt = Math.max(1.5, (100 * b.v) / max); svg.append(s("rect", { class: "lc-bar", x: i * w + w * 0.12, width: Math.max(1, w * 0.76), y: 100 - hgt, height: hgt })); } });
  const cross = h("i", { class: "lc-cross" });
  const plot = h("div", { class: "lc-plot" }, svg, cross, h("span", { class: "lc-y" }, o.top ? o.top(max) : String(max)));
  const lane = h("div", { class: "lc-lane", "aria-hidden": "true" }, o.lanes.map(l => h("i", { "data-tone": l.tone, style: `left:${X(l.a)}%;width:max(3px,${X(l.b) - X(l.a)}%)` })));
  const ticks = h("div", { class: "lc-ticks", "aria-hidden": "true" }, o.ticks.map(k => h("i", { "data-kind": k.kind, style: `left:${X(k.t)}%` })));
  const fig = h("figure", { class: "lc", "data-own": "", "data-sig": sig, role: "img", "aria-label": o.label || null },
    plot, lane, ticks, h("div", { class: "lc-x", "aria-hidden": "true" }, axis(o.t0, o.t1)),
    o.legend ? h("figcaption", { class: "lc-key" }, o.legend) : null);
  fig.addEventListener("pointermove", e => {
    const r = plot.getBoundingClientRect(), f = (e.clientX - r.left) / r.width;
    if (f < 0 || f > 1) return;
    const t = o.t0 + f * span, i = Math.min(N - 1, Math.max(0, Math.floor((t - o.t0) / o.step)));
    cross.style.display = "block"; cross.style.left = (100 * (i + 0.5) * o.step) / span + "%";
    const a = o.t0 + i * o.step, b = a + o.step;
    const ses = o.lanes.filter(l => l.a < b && l.b >= a).map(l => l.tip);
    const ps = o.ticks.filter(k => k.t >= a && k.t < b);
    const out = ps.filter(k => k.kind === "out").length, inn = ps.length - out;
    showTip(e.clientX, e.clientY, `${stamp(a)} – ${clock(b)}`, ...(o.bars[i] ? o.bars[i].tip : []), ...ses,
      ...(ps.length ? [[out ? plural(out, "post") + " written" : null, inn ? plural(inn, "mention") + " of it" : null].filter(Boolean).join(", ")] : []));
  });
  fig.addEventListener("pointerleave", () => { cross.style.display = "none"; hideTip(); });
  return fig;
}

/** Bucket the server's 5-minute token rows into `step`, and price them: the agent's cost split by weighted tokens.
 *  rows: [[t0 s, in, out, cache read, cache write, calls]]. -> [{v, t:[4], calls}] of length N. */
export function priceBuckets(rows, t0, step, N, usd) {
  const out = Array.from({ length: N }, () => ({ v: 0, w: 0, t: [0, 0, 0, 0], calls: 0 }));
  let W = 0;
  for (const r of rows) {
    const i = Math.floor((r[0] * 1e3 - t0) / step); if (i < 0 || i >= N) continue;
    const w = r[1] + 5 * r[2] + 0.1 * r[3] + 1.25 * r[4], o = out[i];
    o.w += w; W += w; o.calls += r[5]; for (let k = 0; k < 4; k++) o.t[k] += r[k + 1];
  }
  for (const o of out) o.v = W ? (usd * o.w) / W : 0;
  return out;
}

/** contacts([{name, sent, got, tags:[...]}], {limit}) — sorted by the caller. Bars share one scale. */
export function contacts(rows, { limit, more } = {}) {
  if (!rows.length) return empty("Nobody yet: it hasn't written to or been named by another agent.");
  const max = Math.max(1, ...rows.map(r => Math.max(r.sent, r.got))), shown = limit ? rows.slice(0, limit) : rows;
  return h("div", { class: "ct" },
    h("div", { class: "ct-head", "aria-hidden": "true" }, h("span", null, ""), h("span", null, "it wrote to them"), h("span", null, "they wrote to it")),
    h("ul", { class: "ct-list" }, shown.map(r => h("li", { "data-key": r.name, title: `${r.name}: it named them in ${plural(r.sent, "post")}, they named it in ${plural(r.got, "post")}` },
      h("span", { class: "ct-who" }, agentRef(r.name), r.tags.map(t => h("span", { class: "ct-tag" }, t))),
      h("span", { class: "ct-bar", "data-side": "out" }, h("b", null, r.sent || ""), h("i", { style: `width:${(100 * r.sent) / max}%` })),
      h("span", { class: "ct-bar", "data-side": "in" }, h("i", { style: `width:${(100 * r.got) / max}%` }), h("b", null, r.got || ""))))),
    rows.length > shown.length ? (more || h("p", { class: "meta" }, `and ${rows.length - shown.length} more`)) : null);
}

export const tokenLine = t => `${toks(t[0] + t[2] + t[3])} read (${toks(t[2])} from cache), ${toks(t[1])} written`;
export { money };
