// Formatting. Plain words, short units, tabular numbers.
export const MIN = 6e4, HOUR = 36e5, DAY = 864e5;
export const now = () => Date.now();

export function ago(t) {
  if (t == null) return "?";
  const d = Math.max(0, now() - t) / 1e3;
  if (d < 5) return "just now";
  if (d < 60) return Math.round(d) + "s ago";
  if (d < 3600) return Math.round(d / 60) + "m ago";
  if (d < 86400) { const h = d / 3600; return (h < 10 ? h.toFixed(1) : Math.round(h)) + "h ago"; }
  return Math.round(d / 86400) + "d ago";
}
export function dur(ms) {
  const m = Math.round(ms / MIN);
  if (m < 1) return "under a minute";
  if (m < 60) return m + "m";
  const h = m / 60;
  if (h < 48) return (h < 10 ? h.toFixed(1) : Math.round(h)) + "h";
  return Math.round(h / 24) + "d";
}
export const secs = s => (s < 60 ? Math.round(s) + "s" : dur(s * 1e3));
export const untilText = ms => (ms <= 0 ? "now" : dur(ms));
export const clock = t => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
export const dayName = t => new Date(t).toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
export const stamp = t => dayName(t) + " " + clock(t);
export const hourLabel = t => new Date(t).toLocaleTimeString([], { hour: "numeric" });
export function short(s, n) { s = String(s || "").replace(/\s+/g, " ").trim(); return s.length <= n ? s : s.slice(0, n - 1) + "…"; }
export const money = c => (c == null ? "–" : c >= 100 ? "$" + Math.round(c).toLocaleString() : "$" + c.toFixed(c < 10 ? 2 : 1));
export const money$ = v => (v == null ? "?" : v > 0 && v < 0.01 ? "<$0.01" : "$" + (v < 10 ? v.toFixed(2) : v.toFixed(0)));
export const toks = n => (n >= 1e9 ? (n / 1e9).toFixed(1) + "B" : n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? Math.round(n / 1e3) + "k" : String(n));
export const pct = (a, b) => (b ? Math.round((100 * a) / b) + "%" : "–");
export const gb = mb => (mb >= 1024 ? (mb / 1024).toFixed(mb >= 10240 ? 0 : 1) + " GB" : Math.round(mb) + " MB");
export const plural = (n, one, many = one + "s") => `${n} ${n === 1 ? one : many}`;
export function median(a) { if (!a.length) return null; a = [...a].sort((x, y) => x - y); return a[a.length >> 1]; }

// Markdown to plain text, for one-line previews.
export const mdPlain = s => String(s || "").replace(/\[([^\]\n]+)\]\(https?:[^\s)]+\)/g, "$1").replace(/\*\*|~~|`/g, "").replace(/^ {0,3}#{1,6}[ \t]+/gm, "").replace(/^\s*\|(.*)\|\s*$/gm, (m, c) => (/^[\s|:-]+$/.test(c) ? "" : c.split("|").map(x => x.trim()).join(", ")));
export function firstSentence(s) {
  s = mdPlain(s).replace(/^(@\S+\s+)+/, "").replace(/\s+/g, " ").trim();
  const m = s.match(/^.{20,240}?[.!?](\s|$)/);
  return m ? m[0].trim() : short(s, 240);
}

/** "Lost contact" line: no age when nothing has ever loaded (fresh is 0), so it never says "20760d ago". */
export const lostText = (fresh, what = "data") => fresh ? `Lost contact, last ${what} ${ago(fresh)}` : "Can't reach the server yet";
