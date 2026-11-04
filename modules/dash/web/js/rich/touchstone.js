// A Touchstone block (.s1p to .s4p text) as magnitude and phase against frequency, drawn by plot.js. Parsing is in touchstone_parse.js.
import { figure } from "./figure.js";
import { splitPanels, yRange, mount } from "./plot.js";
import { h } from "../core/dom.js";

import { parseTouchstone } from "./touchstone_parse.js";

const MAX_SRC = 2000000;

export function touchstoneFigure(code, { lang, title }) {
  const pm = /^s(\d)p$/.exec(lang);
  return figure({
    kind: "touchstone", label: "touchstone", title, src: code, reserve: 300,
    draw: () => {
      if (code.length > MAX_SRC) throw new Error("It's too long.");
      const d = parseTouchstone(code, pm ? +pm[1] : 0), N = d.ports;
      if (d.f.length < 2 || d.f.some((v, i) => i && v <= d.f[i - 1])) throw new Error("The frequencies must rise.");
      const names = d.S.map((_, k) => `S${Math.floor(k / N) + 1}${(k % N) + 1}`);
      // Past two ports only the first column is drawn, or the page is all lines.
      const pick = d.S.map((_, k) => k).filter(k => N <= 2 || k % N === 0);
      const mags = pick.map(k => ({ name: names[k], unit: "dB", vals: d.S[k].mag })), phs = pick.map(k => ({ name: names[k], unit: "deg", vals: d.S[k].ph }));
      const lo = d.f[0], hi = d.f[d.f.length - 1], log = lo > 0 && hi / lo > 30;
      const c = { type: "line", x: { name: "f", lo, hi, unit: "Hz", log }, xs: d.f, sorted: true, marks: [], panels: null, hint: "Move over the plot to read values.",
        alt: `S-parameters ${pick.map(k => names[k]).join(", ")} against frequency` };
      let phase = false;
      const build = () => { c.panels = splitPanels(phase ? mags.concat(phs) : mags); for (const q of c.panels) q.range = yRange(q.series.flatMap(x => x.vals)) || [-1, 1]; };
      build();
      const m = mount(c);
      const btn = h("button", { type: "button", class: "rc-t", onclick: (e, n) => { phase = !phase; n.textContent = phase ? "Hide phase" : "Show phase"; build(); m.redraw(); } }, "Show phase");
      return { own: h("div", null, m.wrap, h("div", { class: "rc-paramsn" }, btn, h("span", null, `${N}-port, ${d.r} Ω, ${d.f.length} points`))) };
    },
  });
}
