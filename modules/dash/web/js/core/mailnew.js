// How much mail came since the person last opened Mail: Mail has no signal of its own, so Chat's rail carries one.
// Reads the same /api/mail the Mail page does (owner-only), every minute; the last-seen time is kept in this browser.
import { S, can, jget } from "./store.js";

const K = "hive.mail.seen";
export const MN = { n: 0, items: null };
const subs = new Set();
export const onMail = fn => (subs.add(fn), () => subs.delete(fn));
const at = m => Date.parse(m.received_at || "") || 0;
const lastSeen = () => { try { return +localStorage.getItem(K) || 0; } catch (e) { return 0; } };

function count(items) {
  const t = lastSeen();
  if (!t && items.length) { try { localStorage.setItem(K, String(Math.max(...items.map(at)))); } catch (e) {} return 0; }   // first look: what's there is not new
  return items.filter(m => !m.held && at(m) > t).length;
}
/** The Mail page has been opened: everything in it is seen. */
export function markMailSeen(items) {
  const t = Math.max(lastSeen(), ...(items || []).map(at));
  try { localStorage.setItem(K, String(t)); } catch (e) {}
  MN.items = items || MN.items; MN.n = 0; for (const f of subs) f();
}
export async function pollMail() {
  if (!can("files") || document.hidden) return;
  try { const v = await jget("/api/mail"); MN.items = v.items || []; const n = count(MN.items); if (n !== MN.n) { MN.n = n; for (const f of subs) f(); } } catch (e) {}
}
setInterval(pollMail, 60000);
setTimeout(pollMail, 1500);
