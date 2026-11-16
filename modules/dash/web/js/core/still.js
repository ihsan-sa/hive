// Still screen (WCAG 2.2.2 Pause, Stop, Hide): one switch in Settings > This device that stops everything that moves by itself —
// the live status dots, the Map's breathing rings and post dots, loading shimmers and spinners. Off by default: the person wants the
// motion, even when a browser or OS reduced-motion setting is on (2026-11-03, css/tokens.css). This browser only, like the theme.
const KEY = "hive.still";
let mem = null;   // no storage: this page only
export function stillOn() {
  let v = mem;
  try { const got = localStorage.getItem(KEY); if (got !== null) v = got; } catch (e) { /* no storage */ }
  return v === "1";
}
export function setStill(on) {
  mem = on ? "1" : "0";
  try { localStorage.setItem(KEY, mem); } catch (e) { /* no storage: this page only */ }
  document.documentElement.toggleAttribute("data-still", on);
}
document.documentElement.toggleAttribute("data-still", stillOn());
