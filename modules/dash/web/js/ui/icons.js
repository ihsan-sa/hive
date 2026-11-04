// Icons for the chrome (redesign 2026-11-04, thread ui-redesign). 24-unit grid, 1.7 stroke, styled by .ic in components.css.
// Each entry is one or more path strings. Built with createElementNS: no innerHTML.
const P = {
  chat: ["M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9a1.5 1.5 0 0 1-1.5 1.5H10l-4.5 4v-4h0A1.5 1.5 0 0 1 4 14.5z"],
  agents: ["M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z", "M2.5 20a6.5 6.5 0 0 1 13 0", "M16 4.3a3.5 3.5 0 0 1 0 6.4", "M18.5 14.5a6.5 6.5 0 0 1 3 5.5"],
  usage: ["M4 20V10", "M10 20V4", "M16 20v-7", "M21 20H3"],
  settings: ["M4 7h10", "M18 7h2", "M4 17h4", "M12 17h8", "M16 5v4", "M10 15v4"],
  more: ["M5 12h.01", "M12 12h.01", "M19 12h.01"],
  search: ["M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13z", "M20 20l-4.6-4.6"],
  new: ["M12 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-6", "M17.5 3.5a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4z"],
  sun: ["M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8z", "M12 2v2", "M12 20v2", "M4.9 4.9l1.4 1.4", "M17.7 17.7l1.4 1.4", "M2 12h2", "M20 12h2", "M4.9 19.1l1.4-1.4", "M17.7 6.3l1.4-1.4"],
  moon: ["M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"],
  auto: ["M12 20a8 8 0 1 0 0-16 8 8 0 0 0 0 16z", "M12 4v16"],
  expand: ["M15 4h5v5", "M9 20H4v-5", "M20 4l-6 6", "M4 20l6-6"],
  plus: ["M12 5v14", "M5 12h14"],
  "arrow-up": ["M12 19V5", "M6 11l6-6 6 6"],
  reply: ["M9 14L4 9l5-5", "M4 9h10a6 6 0 0 1 6 6v5"],
  copy: ["M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1z", "M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"],
  link: ["M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1", "M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"],
  chevron: ["M6 9l6 6 6-6"],
  stop: ["M7 7h10v10H7z"],
  "chevron-down": ["M6 9l6 6 6-6"],
  "chevron-up": ["M6 15l6-6 6 6"],
  "chevron-left": ["M15 6l-6 6 6 6"],
  "chevron-right": ["M9 6l6 6-6 6"],
  filter: ["M4 6h16", "M7 12h10", "M10 18h4"],
  clock: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 7v5l3 2"],
  shield: ["M12 3l7 3v6c0 4.4-3 7.6-7 9-4-1.4-7-4.6-7-9V6z"],
  dial: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 12l4-4", "M12 3v2", "M3 12h2", "M19 12h2"],
  board: ["M4 4h16v16H4z", "M4 9h16", "M9 9v11"],
  attach: ["M20 11.5l-8.1 8.1a5 5 0 0 1-7.1-7.1l8.5-8.5a3.3 3.3 0 0 1 4.7 4.7l-8.5 8.5a1.7 1.7 0 0 1-2.4-2.4l7.8-7.8"],
  close: ["M6 6l12 12", "M18 6L6 18"],
  pin: ["M9 4h6", "M10 4v5l-3 4h10l-3-4V4", "M12 13v7"],                       // threads: a3100
  archive: ["M4 5h16v4H4z", "M5 9v10h14V9", "M10 13h4"],
  bell: ["M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z", "M10 20.5a2 2 0 0 0 4 0"],
  edit: ["M15.5 4.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4z"],
};
const NS = "http://www.w3.org/2000/svg";
export function icon(name, { cls } = {}) {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", cls ? "ic " + cls : "ic");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  for (const d of P[name] || []) { const p = document.createElementNS(NS, "path"); p.setAttribute("d", d); svg.appendChild(p); }
  return svg;
}
export const hasIcon = name => name in P;
