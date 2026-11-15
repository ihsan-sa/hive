// Formatting. Plain words, short units, tabular numbers.
export const MIN = 6e4, HOUR = 36e5, DAY = 864e5;
export const now = () => Date.now();
// E18l: dates and times are written in the Language setting's language when one is chosen, else the browser's own ([]).
// Read here, not from lang.js, so this file still loads without a page (the node tests).
export const LOC = (() => { let v = null; try { v = localStorage.getItem("lang"); } catch {} return v === "es" ? "es" : []; })();

// E18m: numbers too ("7,6 GB", "12.345" in Spanish). fix(x, d) has d decimals; int(n) is a whole number with grouping.
const NF = {};
export const fix = (x, d = 0) => (NF[d] ||= new Intl.NumberFormat(LOC, { minimumFractionDigits: d, maximumFractionDigits: d })).format(x);
export const int = n => fix(Math.round(n), 0);

export function ago(t) {
  if (t == null) return "?";
  const d = Math.max(0, now() - t) / 1e3;
  if (d < 5) return "just now";
  if (d < 60) return Math.round(d) + "s ago";
  if (d < 3600) return Math.round(d / 60) + "m ago";
  if (d < 86400) { const h = d / 3600; return (h < 10 ? fix(h, 1) : Math.round(h)) + "h ago"; }
  return Math.round(d / 86400) + "d ago";
}
export function dur(ms) {
  const m = Math.round(ms / MIN);
  if (m < 1) return "under a minute";
  if (m < 60) return m + "m";
  const h = m / 60;
  if (h < 48) return (h < 10 ? fix(h, 1) : Math.round(h)) + "h";
  return Math.round(h / 24) + "d";
}
export const secs = s => (s < 60 ? Math.round(s) + "s" : dur(s * 1e3));
export const untilText = ms => (ms <= 0 ? "now" : dur(ms));
// One formatter each: toLocale*String builds a new one per call, about a millisecond on a loaded machine, and a long list calls these once per row. A bad date still reads "Invalid Date", where format() would throw.
const fmt = o => { const f = new Intl.DateTimeFormat(LOC, o); return t => { const d = new Date(t); return isNaN(d) ? "Invalid Date" : f.format(d); }; };
export const clock = fmt({ hour: "numeric", minute: "2-digit" });
export const dayName = fmt({ weekday: "short", month: "short", day: "numeric" });
export const stamp = t => dayName(t) + " " + clock(t);
export const hourLabel = fmt({ hour: "numeric" });
export function short(s, n) { s = String(s || "").replace(/\s+/g, " ").trim(); return s.length <= n ? s : s.slice(0, n - 1) + "…"; }
export const money = c => (c == null ? "–" : c >= 100 ? "$" + int(c) : "$" + fix(c, c < 10 ? 2 : 1));
export const money$ = v => (v == null ? "?" : v > 0 && v < 0.01 ? "<$0.01" : "$" + (v < 10 ? fix(v, 2) : fix(v, 0)));
export const toks = n => (n >= 1e9 ? fix((n / 1e9), 1) + "B" : n >= 1e6 ? fix((n / 1e6), 1) + "M" : n >= 1e3 ? Math.round(n / 1e3) + "k" : String(n));
export const pct = (a, b) => (b ? Math.round((100 * a) / b) + "%" : "–");
export const gb = mb => (mb >= 1024 ? fix(mb / 1024, mb >= 10240 ? 0 : 1) + " GB" : Math.round(mb) + " MB");
export const plural = (n, one, many = one + "s") => `${n} ${n === 1 ? one : many}`;
export function median(a) { if (!a.length) return null; a = [...a].sort((x, y) => x - y); return a[a.length >> 1]; }

/** A bare link in a one-line title reads as its site: "https://www.x.org/a/b?c" becomes "x.org". */
export const bareLinks = s => String(s || "").replace(/https?:\/\/(?:www\.)?([^\s/?#)]+)\S*/g, "$1");
// Markdown to plain text, for one-line previews.
export const mdPlain = s => String(s || "").replace(/\[([^\]\n]+)\]\(https?:[^\s)]+\)/g, "$1").replace(/\*\*|~~|`/g, "").replace(/^ {0,3}#{1,6}[ \t]+/gm, "").replace(/^\s*\|(.*)\|\s*$/gm, (m, c) => (/^[\s|:-]+$/.test(c) ? "" : c.split("|").map(x => x.trim()).join(", ")));
export function firstSentence(s) {
  s = mdPlain(s).replace(/^(@\S+\s+)+/, "").replace(/\s+/g, " ").trim();
  const m = s.match(/^.{20,240}?[.!?](\s|$)/);
  return m ? m[0].trim() : short(s, 240);
}

/** The asked sentence as a line for a table: a brief's own instruction or second person ("keeper round (periodic): read ~/core/keeper.md and follow it", "The person is calling you...") says what the run is instead. */
export function onWords(s) {
  if (/^the person is calling you\b/i.test(s)) return "On a phone call";
  const m = s.match(/^([^:]{3,60}): read ~\/\S+ and follow it\.?$/i);
  return m ? m[1][0].toUpperCase() + m[1].slice(1) : s;
}

/** A person's message that names no work: a short question ("what is this about") or only an attachment. A short instruction ("update the /dark/ page") names it. */
export function namesNoWork(s) {
  const t = s.trim();
  if (/^attached: \S+$/i.test(t)) return true;
  return t.length <= 80 && (t.endsWith("?") || /^(what|what's|whats|how|why|who|when|where|which|is|are|was|were|does|did|any)\b/i.test(t));
}

/** "Lost contact" line: no age when nothing has ever loaded (fresh is 0), so it never says "20760d ago". */
export const lostText = (fresh, what = "data") => fresh ? `Lost contact, last ${what} ${ago(fresh)}` : "Can't reach the server yet";
