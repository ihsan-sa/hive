// One-key shortcuts (WCAG 2.1.4): a shortcut that is a single letter, number or symbol, with no Ctrl, ⌘ or Alt, can fire by
// accident for someone who speaks to the page or uses a sticky keyboard. Each one checks oneKey() first; Settings → This device
// turns them all off. Kept in this browser, like the theme. A new one-key shortcut must check oneKey() too (test/drive_app_onekey.mjs).
const KEY = "hive.onekey";
let mem = null;   // no storage: this page only

/** True when one-key shortcuts may act (the default). */
export function oneKeyOn() {
  let v = mem;
  try { const got = localStorage.getItem(KEY); if (got !== null) v = got; } catch (e) { /* no storage */ }
  return v !== "0";
}
export function setOneKey(on) {
  mem = on ? "1" : "0";
  try { localStorage.setItem(KEY, mem); } catch (e) { /* no storage: this page only */ }
}
/** For a keydown: is this a press of one character key alone (no Ctrl, ⌘ or Alt)? */
export const bareChar = e => e.key && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey;
/** For a keydown handler of a one-key shortcut: may it act? Off when switched off, or when the press is typing into a field. */
export function oneKey(e) {
  if (!oneKeyOn()) return false;
  const t = e.target;
  return !(t && (t.isContentEditable || (t.closest && t.closest("input,textarea,select,[contenteditable]"))));
}
