// Plain-text parsing of Touchstone (.sNp): the option line (# GHZ S DB R 50), then one frequency and 2·N² numbers per point, wrapped any way.
// Kept apart from the drawing so a node test can load it.
const MAX_PTS = 20000, UNIT = { hz: 1, khz: 1e3, mhz: 1e6, ghz: 1e9 };

export function parseTouchstone(src, ports) {
  const O = { unit: "ghz", fmt: "ma", r: 50 }; let seenOpt = false; const tok = [];
  for (const raw of src.split("\n")) {
    const l = raw.replace(/!.*/, "").trim(); if (!l) continue;
    if (l[0] === "[") { const m = /^\[number of ports\]\s*(\d+)/i.exec(l); if (m && !ports) ports = +m[1]; continue; }
    if (l[0] === "#") {
      if (seenOpt) continue; seenOpt = true;
      const w = l.slice(1).toLowerCase().split(/\s+/).filter(Boolean);
      for (let i = 0; i < w.length; i++) {
        if (w[i] in UNIT) O.unit = w[i]; else if (["ma", "db", "ri"].includes(w[i])) O.fmt = w[i];
        else if (w[i] === "r") O.r = +w[++i] || 50; else if (!["s", "y", "z", "g", "h"].includes(w[i])) throw new Error(`"${w[i]}" isn't a Touchstone option`);
        else if (w[i] !== "s") throw new Error("Only S-parameters are drawn here.");
      }
      continue;
    }
    for (const t of l.split(/\s+/)) { const v = +t; if (!isFinite(v)) throw new Error(`"${t.slice(0, 20)}" isn't a number`); tok.push(v); }
  }
  if (!tok.length) throw new Error("It has no data points.");
  if (!ports) ports = [2, 1, 3, 4].find(n => tok.length % (1 + 2 * n * n) === 0);
  if (!ports) throw new Error("The number count fits no port count from 1 to 4. Use an s2p style tag.");
  const per = 1 + 2 * ports * ports; if (tok.length % per) throw new Error(`${tok.length} numbers don't fill whole points of ${per}.`);
  const n = tok.length / per; if (n > MAX_PTS) throw new Error("It has too many points.");
  const f = [], S = Array.from({ length: ports * ports }, () => ({ mag: [], ph: [] }));
  // Two-port files list S11 S21 S12 S22; every other count goes row by row.
  const idx = k => (ports === 2 ? [0, 2, 1, 3][k] : k);
  for (let p = 0; p < n; p++) {
    f.push(tok[p * per] * UNIT[O.unit]);
    for (let k = 0; k < ports * ports; k++) {
      const a = tok[p * per + 1 + 2 * k], b = tok[p * per + 2 + 2 * k]; let mag, ph;
      if (O.fmt === "ri") { mag = Math.hypot(a, b); ph = Math.atan2(b, a) * 180 / Math.PI; }
      else { mag = O.fmt === "db" ? 10 ** (a / 20) : a; ph = b; }
      const s = S[idx(k)]; s.mag.push(20 * Math.log10(Math.max(mag, 1e-12))); s.ph.push(ph);
    }
  }
  return { ports, f, S, r: O.r, fmt: O.fmt };
}

