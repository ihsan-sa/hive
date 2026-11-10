// How tall a block was the last time it settled, kept per phone or desk, so while its data is still on the way the page can leave it that room
// and what is below doesn't jump when it lands (R67, the same idea as Flow's fl.held). Only a height: never content.
const KEY = "page.room";
const R = (() => { try { return JSON.parse(localStorage.getItem(KEY)) || {}; } catch (e) { return {}; } })();
const due = new Set();
const keyOf = id => id + (matchMedia("(max-width: 640px)").matches ? ":p" : ":d");
const heightOf = at => { if (typeof at === "function") return at(); const n = document.querySelector(at); return n ? n.offsetHeight : 0; };

/** The room to leave for block `id` while it isn't settled (px, 0: none known). Once settled it remembers the height after the draw; `at` is a selector or a function giving the px.
 *  usual: false for a look that isn't the page's usual one (a search, an open fold), which is neither remembered nor held for. */
export function heldRoom(id, at, { settled, usual = true }) {
  if (!usual) return 0;
  const k = keyOf(id);
  if (!settled) return R[k] || 0;
  if (!due.has(k)) { due.add(k); requestAnimationFrame(() => {
    due.delete(k);
    const px = Math.round(heightOf(at));
    if (px > 0 && px !== R[k]) { R[k] = px; try { localStorage.setItem(KEY, JSON.stringify(R)); } catch (e) { /* no storage: this page only */ } }
  }); }
  return 0;
}
