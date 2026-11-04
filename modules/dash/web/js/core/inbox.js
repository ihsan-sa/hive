// What agents addressed to the person, and which of it they've seen. Shares the door's read marks (~/.hive/delivered/person).
// Polled every 3 s in the app, since the Chat tab carries the count and Chat's home lists what needs you.
import { S, can, jget, post, subscribe } from "./store.js";
import { notifyNew, notifyOn } from "./notify.js";

export const IB = { items: null, unread: 0, sig: "", err: "", byId: new Map() };
const subs = new Set();
export const onInbox = fn => (subs.add(fn), () => subs.delete(fn));

export async function pollInbox() {
  if (!S.app.app || !can("read") || (document.hidden && !notifyOn())) return;   // a hidden tab keeps polling only to notify
  try {
    const v = await jget("/api/inbox"), sig = JSON.stringify(v);
    if (sig === IB.sig) return;
    notifyNew(v.items);
    IB.sig = sig; IB.items = v.items; IB.unread = v.unread; IB.byId = new Map(v.items.map(x => [x.id, x]));
    for (const f of subs) f();
  } catch (e) {}
}
setInterval(pollInbox, 3000);
subscribe(w => { if (w.app) pollInbox(); });

/** Has the person seen this post (opened its thread, or marked it read)? Unknown counts as seen. */
export const seen = p => { const x = IB.byId.get(p.id); return !x || x.read; };
export const unreadIn = th => (IB.items || []).filter(x => !x.read && x.thread === th);

export async function markRead(body) {
  IB.err = "";
  if (!can("post")) return;
  try { await post("/api/inbox-read", body); }
  catch (e) { IB.err = "Couldn't mark it read: " + e.message + "."; }
  IB.sig = ""; await pollInbox();
}
