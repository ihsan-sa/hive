// A SPICE netlist read into rows, as text only (no simulator): elements with their nodes and value, plus the dot-lines.
// Kept apart from the drawing so a node test can load it.
const TYPES = { R: "Resistor", C: "Capacitor", L: "Inductor", V: "Voltage source", I: "Current source", D: "Diode", Q: "BJT", M: "MOSFET", J: "JFET",
  E: "VCVS", F: "CCCS", G: "VCCS", H: "CCVS", K: "Coupling", S: "Switch", W: "Switch", T: "Transmission line", X: "Subcircuit", B: "Behavioural", O: "Line", U: "Line" };
// How many leading words after the name are nodes, by the first letter. Q/M/J/X/B vary, so they get a rule below.
const NODES = { R: 2, C: 2, L: 2, V: 2, I: 2, D: 2, E: 4, F: 2, G: 4, H: 2, S: 4, W: 2, T: 4, K: 0 };
const MAX_LINES = 20000;

export function parseSpice(src) {
  // A first line is the title in SPICE; a continuation line starts with +.
  const raw = src.split("\n"); if (raw.length > MAX_LINES) throw new Error("It's too long.");
  const lines = [];
  raw.forEach((t, i) => {
    t = t.replace(/\r$/, "");
    if (/^\s*\+/.test(t) && lines.length) { lines[lines.length - 1] += " " + t.replace(/^\s*\+/, "").trim(); return; }
    t = t.replace(/\s*(;|\$\s).*$/, ""); if (!t.trim() || t.trim()[0] === "*") return;
    lines.push(t.trim());
  });
  let title = ""; const startsCard = l => /^[A-Za-z.]/.test(l) && /^(\.|[RCLVIDQMJEFGHKSWTXBOU])/i.test(l);
  if (raw.length && raw[0].trim() && raw[0].trim()[0] !== "*" && !raw[0].trim().startsWith(".") && !/^\S+\s+\S+\s+\S+/.test(raw[0].trim())) { title = raw[0].trim(); lines.shift(); }
  const els = [], dots = [], nodes = new Set(); let sub = "";
  for (const l of lines) {
    if (l[0] === ".") {
      const w = l.split(/\s+/), d = w[0].toLowerCase();
      if (d === ".subckt") sub = w[1] || ""; else if (d === ".ends") sub = "";
      dots.push(l); continue;
    }
    if (!startsCard(l)) throw new Error(`"${l.slice(0, 30)}" isn't a SPICE line`);
    const w = l.split(/\s+/), name = w[0], k = name[0].toUpperCase();
    let n = NODES[k];
    if (k === "Q") n = w.length >= 6 && !w[5].includes("=") ? 4 : 3;
    else if (k === "M") n = 4; else if (k === "J") n = 3;
    else if (k === "X") { n = w.length - 2; }
    else if (k === "B") n = 2;
    n = Math.max(0, Math.min(n ?? 2, w.length - 1));
    const ns = w.slice(1, 1 + n), rest = w.slice(1 + n).join(" ");
    if (k !== "X") ns.forEach(x => { if (x !== "0" && x.toLowerCase() !== "gnd") nodes.add(x); });
    els.push({ name, type: TYPES[k] || "Element", nodes: ns.join(" "), value: k === "X" ? w[w.length - 1] : rest, sub });
  }
  return { title, els, dots, nodes: nodes.size };
}
