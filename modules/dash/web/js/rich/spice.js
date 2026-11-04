// A spice fence: the netlist as a table of its elements (name, type, nodes, value) with the dot-commands listed below. Text only,
// nothing is simulated. The source stays one press away like every figure.
import { h } from "../core/dom.js";
import { figure } from "./figure.js";
import { drawTable } from "./table.js";
import { parseSpice } from "./spice_parse.js";

const ST = { col: -1, dir: 1, q: "", shown: 500 };
export function spiceFigure(code, { title }) {
  return figure({
    kind: "spice", label: "spice", title, src: code, reserve: 160,
    draw: () => {
      if (code.length > 1000000) throw new Error("It's over 1 MB.");
      const d = parseSpice(code);
      if (!d.els.length) throw new Error("It has no circuit elements.");
      const rows = [["Element", "Type", "Nodes", "Value"], ...d.els.map(e => [e.sub ? `${e.sub}/${e.name}` : e.name, e.type, e.nodes, e.value])];
      const st = { ...ST }, line = `${d.els.length} elements, ${d.nodes} nodes` + (d.title ? `: ${d.title}` : "");
      return { own: h("div", null, h("div", { class: "rc-paramsn" }, h("span", null, line)), drawTable(rows, title || "netlist", {}, st),
        d.dots.length ? h("pre", { class: "rc-fig-src", style: "display:block" }, d.dots.join("\n")) : null) };
    },
  });
}
