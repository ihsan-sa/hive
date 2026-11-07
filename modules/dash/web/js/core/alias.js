// Easter egg: show agents as ihsan-1, ihsan-2, … instead of their hashes (5 clicks: ihsan-N, 5 more: short names, 5 more: normal). Display only: the board, the log and what you send keep the real names.
// Toggle: click the logo 5 times quickly, or run hiveNames() in the console. Kept in this browser.
import { S } from "./store.js";
import { setTextMap } from "./dom.js";
import { beeAround } from "./bee.js";
import { logoClicks } from "./easter.js";

const KEY = "hive.names";
// 0 = real names, 1 = ihsan-N, 2 = short human names
const mode = () => { try { return +localStorage.getItem(KEY) || 0; } catch (e) { return 0; } };
const NAMES = "Ada Al Amy Ann Ben Bo Cal Cy Dan Di Ed Eli Eva Fay Gus Hal Ian Ida Jo Jay Kai Kim Lee Leo Liz Lou Max Mia Ned Nia Odi Pat Rae Ray Rex Sam Sky Tim Tom Uma Val Vic Wes Xan Yan Zed Zoe".split(" ");
const hash = s => { let h = 2166136261; for (const c of s) h = Math.imul(h ^ c.charCodeAt(0), 16777619); return h >>> 0; };
let sig = -1, re = null, map = null;

function build() {
  if (S.agents.size === sig) return;
  sig = S.agents.size; map = new Map(); let i = 0;
  const used = new Set();
  for (const n of S.agents) {
    i++;
    if (mode() === 1) { map.set(n, "ihsan-" + i); continue; }
    let k = hash(n) % NAMES.length, base = NAMES[k], name = base, j = 1;
    while (used.has(name)) name = base + ++j;   // same first name twice: Max2
    used.add(name); map.set(n, name);
  }
  re = map.size ? new RegExp("(?<![\\w.-])(" + [...map.keys()].sort((a, b) => b.length - a.length).map(x => x.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|") + ")(?![\\w-])", "g") : null;
}
function alias(t) { build(); return re ? t.replace(re, m => map.get(m)) : t; }

export function toggleNames() {
  try { localStorage.setItem(KEY, String((mode() + 1) % 3)); } catch (e) { /* none */ }
  location.reload();
}
export function initAlias() {
  if (mode()) setTextMap(alias);
  window.hiveNames = toggleNames;
}
/** Logo click handler: five quick clicks flip it. */
let clicks = [];
export function logoClick(e) {
  // Bees only on the third quick click, not on every click (the person, 2026-11-07).
  if (e.currentTarget && logoClicks()) beeAround(e.currentTarget);
  const t = Date.now(); clicks = clicks.filter(x => t - x < 1500); clicks.push(t);
  if (clicks.length >= 5) { clicks = []; e.preventDefault(); toggleNames(); }
}
