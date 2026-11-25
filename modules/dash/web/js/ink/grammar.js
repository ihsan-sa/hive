// The ```ink fence (thread 112515205dbf; spec: ~/designs/ink-chat/INK-FENCE.md, the design's reference parser, kept close).
// An agent offers a writing space (no on=: the body is the printed problem) or draws on one (on=<id>: one mark per line).
// Forgiving: case, commas or spaces, (x, y) brackets, → or => for ->, r=40 or r40, 90x40 or 90×40, curly quotes, synonyms.
// A line it can't read is kept in `skipped`, never thrown: the post still shows and the card says what was left out.
// Every sheet is 800 units wide, y grows down, a ruled line is 32 units.

export const W = 800, LINE = 32;
const VERB = {
  circle: "circle", ring: "circle", ellipse: "circle", loop: "circle", oval: "circle",
  box: "box", rect: "box", rectangle: "box", square: "box",
  underline: "underline", under: "underline",
  strike: "strike", strikethrough: "strike", cross_out: "strike",
  arrow: "arrow", line: "line",
  check: "check", tick: "check", ok: "check", "✓": "check", "✔": "check",
  cross: "cross", x: "cross", wrong: "cross", "✗": "cross", "✘": "cross",
  text: "text", note: "text", write: "text", label: "text",
  highlight: "hl", hl: "hl",
  path: "path", draw: "path",
  clear: "clear",
};
const PAPER = new Set(["ruled", "grid", "dot", "blank"]);
const NUM = String.raw`-?\d+(?:\.\d+)?`;
const PT = new RegExp(String.raw`^\(?\s*(${NUM})\s*(?:,\s*|\s+)(${NUM})\s*\)?`);
/** A sheet id as the page keeps it: letters, digits, - and _ only, at most 40. */
export const cleanId = s => String(s || "").replace(/[^\w-]/g, "").slice(0, 40);
/** What an on= names: a post id (12 hex) is the ink over that message, "wb" or "board" the whiteboard, else a sheet. */
export function target(on) {
  const s = String(on || "").trim().replace(/^m[:-]/, "");
  if (/^[0-9a-f]{12}$/i.test(s)) return "m-" + s.toLowerCase();
  if (/^(wb|board|whiteboard)$/i.test(s)) return "board";
  return cleanId(s);
}

/** The info string: `ink on=p3` (mark up) or `ink id=p3 title="Problem 3" paper=ruled lines=8` (offer). */
export function inkInfo(s) {
  const a = {}, re = /(\w+)=(?:"([^"]*)"|“([^”]*)”|(\S+))/g;
  let m; while ((m = re.exec(s || ""))) a[m[1].toLowerCase()] = m[2] ?? m[3] ?? m[4];
  const paper = (a.paper || "").toLowerCase();
  const out = { on: a.on ? target(a.on) : null, id: a.id ? cleanId(a.id) : null, title: a.title || null, paper: PAPER.has(paper) ? paper : "ruled" };
  const n = parseInt(a.lines, 10); out.lines = n > 0 ? Math.min(n, 40) : null;
  const sz = /^(\d+)\s*[x×]\s*(\d+)$/i.exec(a.size || a.height && ("800x" + a.height) || "");
  out.h = sz ? Math.max(LINE, Math.min(+sz[2], 4000)) : (out.lines || 6) * LINE;
  return out;
}

/** One body line: a mark, {print}, {bad}, or null (blank or a comment). */
export function markLine(raw) {
  let s = raw.trim();
  if (!s || s.startsWith("#") || s.startsWith("//")) return null;
  s = s.replace(/→|⟶|=>/g, "->").replace(/[“”]/g, '"');
  const q = /"([^"]*)"/.exec(s), label = q ? q[1] : null;
  const rest = q ? (s.slice(0, q.index) + " " + s.slice(q.index + q[0].length)).trim() : s;
  const w = /^([^\s(]+)\s*(.*)$/.exec(rest); if (!w) return { print: raw };
  let word = w[1].toLowerCase(), color = null;
  const verb = VERB[word];
  if (!verb) return { print: raw };
  if (verb === "clear") return { k: "clear" };
  const pts = [], opt = {};
  let args = w[2], left = "";
  const cm = /\b(red|blue|green|black|orange|purple|violet|teal)\b/i.exec(args); if (cm) { color = cm[1].toLowerCase(); args = args.replace(cm[0], " "); }
  if (/\bexact\b/i.test(args)) { opt.exact = true; args = args.replace(/\bexact\b/ig, " "); }
  const at = /^\s*@/.test(args) || /@\s*$/.test(args);
  for (const part of args.replace(/@/g, " ").split("->")) {
    let t = part.trim(), m;
    while (t && (m = PT.exec(t))) {
      pts.push([+m[1], +m[2]]); t = t.slice(m[0].length).trim();
      const r = /^r\s*=?\s*(\d+(?:\.\d+)?)/i.exec(t); if (r) { opt.r = +r[1]; t = t.slice(r[0].length).trim(); }
      const wh = /^(\d+(?:\.\d+)?)\s*[x×]\s*(\d+(?:\.\d+)?)/i.exec(t); if (wh) { opt.w = +wh[1]; opt.h = +wh[2]; t = t.slice(wh[0].length).trim(); }
      const ww = /^w\s*=?\s*(\d+(?:\.\d+)?)/i.exec(t); if (ww) { opt.w = +ww[1]; t = t.slice(ww[0].length).trim(); }
    }
    left += " " + t;
  }
  const mk = { k: verb, pts, label: at ? null : label, anchor: at ? label : null, color, ...opt };
  if (/[a-z]{2,}/i.test(left)) mk.loose = true;   // words after the numbers: on an offer sheet that's prose ("Box 3, 4 apples into crates")
  const need = { circle: 1, box: 1, underline: 1, strike: 1, arrow: 2, line: 2, check: 1, cross: 1, text: 1, hl: 1, path: 2 }[verb];
  if (!mk.anchor && pts.length < need) return { bad: raw };
  if (verb === "text" && !mk.label) return { bad: raw };
  return mk;
}

/** The whole fence: {info, marks, printed, skipped}. On an offer sheet a line that isn't a mark is the problem's text. */
export function parseInk(infoStr, code) {
  const info = inkInfo(infoStr), marks = [], printed = [], skipped = [];
  String(code || "").split("\n").forEach((raw, i) => {
    const r = markLine(raw);
    if (!r) { if (!info.on) printed.push(raw); return; }
    if (r.print != null || r.bad || (r.loose && !info.on)) { if (info.on) skipped.push({ n: i + 1, raw }); else printed.push(raw); }
    else marks.push(r);
  });
  return { info, marks, printed: printed.join("\n").trim(), skipped };
}

// The ```ink fences in a post's text, in order: {info, code}. The same fence rule as markdown.js: ``` or ~~~, closed by the same or longer.
const OPEN = /^ {0,3}(`{3,}|~{3,})\s*ink(?=\s|$)(.*)$/i;
export function fences(text) {
  const out = [], ls = String(text || "").replace(/\r\n?/g, "\n").split("\n");
  for (let i = 0; i < ls.length; i++) {
    const m = OPEN.exec(ls[i]); if (!m) continue;
    const f = m[1], body = [];
    for (i++; i < ls.length; i++) { const c = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(ls[i]); if (c && c[1][0] === f[0] && c[1].length >= f.length) break; body.push(ls[i]); }
    out.push({ info: m[2], code: body.join("\n") });
  }
  return out;
}
